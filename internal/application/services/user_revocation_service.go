// Package services — UserRevocationService handles user-JWT revocation,
// revocation-list pruning, and user-creds regeneration (P2).
//
// Why a separate service from UserService?
//   - UserService stays focused on CRUD: callers reading user_service.go can
//     reason about it without wading through revocation semantics.
//   - Revocation composes user + account JWT regen + cluster JWT push, and
//     emits two-to-three distinct events per operation; keeping that out of
//     UserService keeps each file at one job.
//   - The sweeper, which calls RegenerateUserJWT for auto-renew and uses
//     ResolveActiveRevocations during prune, only needs this surface.
//
// All mutating methods run inside factory.WithTx. NATS pushes happen AFTER the
// tx commits (rule A1).
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ErrUserAlreadyRevoked is returned when RevokeUser is called on a user whose
// RevokedAt is already set. The caller can choose to surface or ignore.
var ErrUserAlreadyRevoked = errors.New("user already revoked")

// AccountJWTPusher is the subset of *ClusterService that the revocation
// service needs. Decoupled so tests can swap a no-op pusher in. The real
// implementation is ClusterService.PushAccountToAllClusters.
type AccountJWTPusher interface {
	PushAccountToAllClusters(ctx context.Context, operatorID uuid.UUID, account *entities.Account) []SyncError
}

// UserRevocationService is the P2 entry point for revocation and regeneration.
type UserRevocationService struct {
	factory     persistence.RepositoryFactory
	jwtService  *JWTService
	clusterPush AccountJWTPusher
	encryptor   encryption.Encryptor
}

// NewUserRevocationService constructs the service. clusterPush is allowed to
// be nil for tests that don't care about the NATS push path; revoke/renew
// still update the DB + emit events.
func NewUserRevocationService(
	factory persistence.RepositoryFactory,
	jwtService *JWTService,
	clusterPush AccountJWTPusher,
	encryptor encryption.Encryptor,
) *UserRevocationService {
	return &UserRevocationService{
		factory:     factory,
		jwtService:  jwtService,
		clusterPush: clusterPush,
		encryptor:   encryptor,
	}
}

// RevokeUser adds the user's NATS public key to the parent account JWT's
// Revocations map, re-signs the account JWT, and emits user.revoked. After
// the tx commits, the updated account JWT is pushed to every cluster the
// operator owns (the push failure does NOT roll back — the revocation row is
// authoritative, and the next sweep / manual sync will reconcile).
//
// Idempotency: a second RevokeUser on an already-revoked user returns
// ErrUserAlreadyRevoked with the current entity, no state change.
func (s *UserRevocationService) RevokeUser(ctx context.Context, userID uuid.UUID, reason string) (*entities.User, error) {
	var (
		updatedUser    *entities.User
		updatedAccount *entities.Account
	)
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		userRepo := tx.UserRepository()
		accountRepo := tx.AccountRepository()
		operatorRepo := tx.OperatorRepository()
		scopedKeyRepo := tx.ScopedSigningKeyRepository()
		revRepo := tx.UserJWTRevocationRepository()

		user, err := userRepo.GetByID(ctx, userID)
		if err != nil {
			return fmt.Errorf("failed to get user: %w", err)
		}
		if user.RevokedAt != nil {
			updatedUser = user
			return ErrUserAlreadyRevoked
		}

		account, err := accountRepo.GetByID(ctx, user.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get account: %w", err)
		}
		operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
		if err != nil {
			return fmt.Errorf("failed to get operator: %w", err)
		}

		// Compute the JWT exp the revocation entry can be pruned after. If the
		// current user JWT has an explicit exp, use it. Otherwise (no-expiry
		// JWT — the back-compat default), fall back to the operator's user
		// TTL: if THAT is also zero, the revocation must live forever, so
		// stamp exp = far future (year 9999). Bookkeeping never expires for
		// no-expiry credentials; the JWT itself never expires either.
		var jwtExp time.Time
		switch {
		case user.JWTExpiresAt != nil:
			jwtExp = *user.JWTExpiresAt
		case operator.UserJWTTTL > 0:
			jwtExp = time.Now().Add(operator.UserJWTTTL)
		default:
			jwtExp = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		}

		now := time.Now()
		rev := &entities.UserJWTRevocation{
			ID:            uuid.New(),
			AccountID:     account.ID,
			UserID:        &user.ID,
			UserPublicKey: user.PublicKey,
			RevokedAt:     now,
			JWTExp:        jwtExp,
			Reason:        reason,
			CreatedAt:     now,
		}
		if err := revRepo.Create(ctx, rev); err != nil {
			return fmt.Errorf("failed to persist revocation: %w", err)
		}

		// Mark the user soft-revoked on the user row.
		user.RevokedAt = &now
		user.RevocationReason = reason
		user.UpdatedAt = now
		if err := userRepo.Update(ctx, user); err != nil {
			return fmt.Errorf("failed to mark user revoked: %w", err)
		}

		// Regenerate the parent account JWT with the updated revocation list.
		scopedKeys, err := scopedKeyRepo.ListByAccount(ctx, account.ID, repositories.ListOptions{Limit: 1000})
		if err != nil {
			return fmt.Errorf("failed to list scoped signing keys: %w", err)
		}
		revs, err := revRepo.ListActiveByAccount(ctx, account.ID)
		if err != nil {
			return fmt.Errorf("failed to list active revocations: %w", err)
		}
		newJWT, err := s.jwtService.GenerateAccountJWT(ctx, account, operator, scopedKeys, revs, operator.AccountJWTTTL)
		if err != nil {
			return fmt.Errorf("failed to regenerate account JWT: %w", err)
		}
		account.JWT = newJWT
		account.UpdatedAt = now
		if err := accountRepo.Update(ctx, account); err != nil {
			return fmt.Errorf("failed to persist regenerated account JWT: %w", err)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeUserRevoked,
			OperatorID:   &account.OperatorID,
			AccountID:    &account.ID,
			ResourceType: "user",
			ResourceID:   user.ID.String(),
			Payload: map[string]any{
				"name":       user.Name,
				"public_key": user.PublicKey,
				"reason":     reason,
				"jwt_exp":    jwtExp.Unix(),
			},
		}); err != nil {
			return fmt.Errorf("emit user.revoked: %w", err)
		}

		updatedUser = user
		updatedAccount = account
		return nil
	})
	if err != nil && !errors.Is(err, ErrUserAlreadyRevoked) {
		metrics.Default().RecordUserJWTRevocation(ctx, "err")
		return nil, err
	}

	// Push outside the tx. Failure is recorded but does NOT poison the revoke.
	if updatedAccount != nil && s.clusterPush != nil {
		if pushErrs := s.clusterPush.PushAccountToAllClusters(ctx, updatedAccount.OperatorID, updatedAccount); len(pushErrs) > 0 {
			log := logging.LogFromContext(ctx)
			for _, e := range pushErrs {
				log.Warn("revoke: account JWT push failed", "account", e.AccountName, "error", e.Error)
			}
		}
	}
	if errors.Is(err, ErrUserAlreadyRevoked) {
		return updatedUser, err
	}
	metrics.Default().RecordUserJWTRevocation(ctx, "ok")
	return updatedUser, nil
}

// RegenerateUserJWT mints a fresh user JWT (and clears RevokedAt if set —
// "reinstate"). Used by the manual nisctl/UI flow ("regenerate creds") AND
// by the sweeper's auto-renew path.
func (s *UserRevocationService) RegenerateUserJWT(ctx context.Context, userID uuid.UUID) (*entities.User, error) {
	var updated *entities.User
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		userRepo := tx.UserRepository()
		accountRepo := tx.AccountRepository()
		operatorRepo := tx.OperatorRepository()
		scopedKeyRepo := tx.ScopedSigningKeyRepository()

		user, err := userRepo.GetByID(ctx, userID)
		if err != nil {
			return fmt.Errorf("failed to get user: %w", err)
		}
		account, err := accountRepo.GetByID(ctx, user.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get account: %w", err)
		}
		operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
		if err != nil {
			return fmt.Errorf("failed to get operator: %w", err)
		}

		var scopedKey *entities.ScopedSigningKey
		if user.ScopedSigningKeyID != nil {
			scopedKey, err = scopedKeyRepo.GetByID(ctx, *user.ScopedSigningKeyID)
			if err != nil {
				return fmt.Errorf("failed to get scoped signing key: %w", err)
			}
		}

		mint, err := s.jwtService.GenerateUserJWT(ctx, user, account, scopedKey, user.EffectiveJWTTTL(operator))
		if err != nil {
			return fmt.Errorf("failed to regenerate user JWT: %w", err)
		}

		now := time.Now()
		user.JWT = mint.Token
		user.JWTIssuedAt = &mint.IssuedAt
		user.JWTExpiresAt = mint.ExpiresAt
		// Fresh iat — reset both dedup pins so future warns/alerts can fire.
		user.LastExpiringWarnIAT = nil
		user.LastExpiredAlertIAT = nil
		// Reinstate semantics: clear the revoked marker. The revocation entry
		// in user_jwt_revocations stays put — NATS will keep rejecting any
		// pre-revoke JWT until its (old) exp, but the new JWT carries a fresh
		// iat which is AFTER revoked_at so it's accepted.
		user.RevokedAt = nil
		user.RevocationReason = ""
		user.UpdatedAt = now
		if err := userRepo.Update(ctx, user); err != nil {
			return fmt.Errorf("failed to persist regenerated user: %w", err)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeUserCredRenewed,
			OperatorID:   &account.OperatorID,
			AccountID:    &account.ID,
			ResourceType: "user",
			ResourceID:   user.ID.String(),
			Payload: map[string]any{
				"name":       user.Name,
				"public_key": user.PublicKey,
				"jwt_iat":    mint.IssuedAt.Unix(),
				"jwt_exp":    optionalUnix(mint.ExpiresAt),
			},
		}); err != nil {
			return fmt.Errorf("emit user.cred.renewed: %w", err)
		}

		updated = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// ResolveActiveRevocations exposes the active revocation list for an account.
// Mostly used by tests and admin tooling — the sweeper and AccountJWT regen
// paths read the repo directly inside their tx.
func (s *UserRevocationService) ResolveActiveRevocations(ctx context.Context, accountID uuid.UUID) ([]*entities.UserJWTRevocation, error) {
	return s.factory.UserJWTRevocationRepository().ListActiveByAccount(ctx, accountID)
}

func optionalUnix(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}
