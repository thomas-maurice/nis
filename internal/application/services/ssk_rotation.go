package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ErrSSKPlainSignerRotation is returned by RotateScopedSigningKey when the
// target SSK has IsPlainSigner=true. Plain-signer SSKs come from NSC
// imports: NIS does not own the embedded permissions of users signed by
// them, so re-minting under a new key would produce JWTs with NIS-default
// permissions and silently over-permission those users. v1 refuses these
// outright; a follow-up may add decode-and-preserve-perms support.
var ErrSSKPlainSignerRotation = errors.New("plain-signer scoped signing keys cannot be rotated (NSC-imported permissions are not NIS-managed)")

// RotateScopedSigningKeyResult is the return value of RotateScopedSigningKey.
// AffectedUsers counts the active dependents (revoked_at IS NULL) whose JWTs
// were re-minted under the new key. RevokedUserPublicKeys is the same set
// expressed as NATS U-prefix public keys — useful for audit + UI confirmation.
// PushOutcomes contains one entry per attached cluster from the post-commit
// account-JWT push.
type RotateScopedSigningKeyResult struct {
	ScopedSigningKey      *entities.ScopedSigningKey
	OldPublicKey          string
	AffectedUsers         int
	RevokedUserPublicKeys []string
	PushOutcomes          []ClusterPushOutcome
}

// RotateScopedSigningKey replaces the NKey material on an existing SSK and
// invalidates every dependent user's old JWT.
//
// Inside a single factory.WithTx:
//  1. Generate a fresh NKey pair + encrypted seed.
//  2. UPDATE the SSK row's public_key + encrypted_seed (everything else —
//     perms, template binding, drifted flag, TrackLatest, IsPlainSigner —
//     preserved verbatim).
//  3. List active dependent users (scoped_signing_key_id = id AND
//     revoked_at IS NULL).
//  4. Pick the revocation timestamp T = clock.Now() - 1s. jwt v2's
//     RevocationList.IsRevoked uses `iat >= revoked_at` (>=, not >), so
//     same-Unix-second mints inside one tx would otherwise be born-revoked.
//     Backdating by 1s guarantees every freshly-minted user JWT (iat at
//     or after now) is strictly after the revocation moment.
//  5. For each active dependent: insert user_jwt_revocations row keyed by
//     user.PublicKey at T, mint a new user JWT signed by the new key,
//     update user row, emit user.revoked with payload.triggered_by =
//     "ssk_rotation".
//  6. Call regenerateAccountJWTTx(accountID) — it re-reads scoped_keys +
//     active revocations from the same tx and produces an account JWT
//     that lists the new SSK pubkey AND carries the new revocation entries.
//  7. Emit one summary scoped_key.rotated event.
//
// On any error: tx rolls back, nothing changes. After commit:
// PushAccountToAllClustersDetailed is called best-effort; per-cluster
// outcomes are returned in the result so the operator sees lag in the RPC
// response instead of having to cross-reference the drift dashboard.
//
// Plain-signer SSKs (IsPlainSigner=true) are refused with
// ErrSSKPlainSignerRotation. See that sentinel's docstring.
func (s *ScopedSigningKeyService) RotateScopedSigningKey(ctx context.Context, id uuid.UUID, reason string) (*RotateScopedSigningKeyResult, error) {
	var (
		result      RotateScopedSigningKeyResult
		updatedAcct *entities.Account
	)

	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		sskRepo := tx.ScopedSigningKeyRepository()
		userRepo := tx.UserRepository()
		accountRepo := tx.AccountRepository()
		operatorRepo := tx.OperatorRepository()
		revRepo := tx.UserJWTRevocationRepository()

		ssk, err := sskRepo.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to get scoped signing key: %w", err)
		}
		if ssk.IsPlainSigner {
			return fmt.Errorf("%w: ssk %s", ErrSSKPlainSignerRotation, ssk.Name)
		}

		account, err := accountRepo.GetByID(ctx, ssk.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get account: %w", err)
		}
		operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
		if err != nil {
			return fmt.Errorf("failed to get operator: %w", err)
		}

		// 1. Generate new NKey pair (account prefix — same as the SSK had).
		newSeed, newPubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
		if err != nil {
			return fmt.Errorf("failed to generate new scoped signing key material: %w", err)
		}
		newEncryptedSeed, err := s.encryptor.Encrypt(ctx, newSeed)
		if err != nil {
			return fmt.Errorf("failed to encrypt new scoped signing key seed: %w", err)
		}

		oldPubKey := ssk.PublicKey
		now := clock.Now()

		// 2. Update SSK row in place. Perms, template binding, drifted
		// flag, TrackLatest, IsPlainSigner are preserved. Rotation isn't
		// a permission edit so the drifted flag stays as-is.
		ssk.PublicKey = newPubKey
		ssk.EncryptedSeed = newEncryptedSeed
		ssk.UpdatedAt = now
		if err := sskRepo.Update(ctx, ssk); err != nil {
			return fmt.Errorf("failed to update scoped signing key: %w", err)
		}

		// 3. List active dependent users. Limit 10000 — way past any
		// realistic single-SSK fan-out; treated as a soft cap rather than
		// pagination because we want the rotation to be atomic across all
		// dependents (a partial rotation leaves some users invalidated and
		// some still working, which is the worst state).
		users, err := userRepo.ListByScopedSigningKey(ctx, ssk.ID, repositories.ListOptions{Limit: 10000})
		if err != nil {
			return fmt.Errorf("failed to list dependent users: %w", err)
		}

		// 4. Revocation timestamp — see the docstring for the -1s
		// rationale. Capture once so every revocation row uses the same T.
		revokedAt := now.Add(-1 * time.Second)

		var revokedPubKeys []string
		for _, user := range users {
			// Skip already-revoked users. Their old JWT is already
			// invalid via the existing user_jwt_revocations entry; adding
			// another would be noise. Skip re-mint too — leave their
			// revoked state alone.
			if user.RevokedAt != nil {
				continue
			}
			// Skip users with no current JWT. Edge case — usually means
			// the row is in a half-state mid-create. Nothing to invalidate.
			if user.JWT == "" {
				continue
			}

			// JWTExp on the revocation row — see UserRevocationService.RevokeUser
			// for the same logic. Used by the prune sweeper to know when
			// the entry can be removed (NATS rejects on exp anyway after
			// that).
			var jwtExp time.Time
			switch {
			case user.JWTExpiresAt != nil:
				jwtExp = *user.JWTExpiresAt
			case operator.UserJWTTTL > 0:
				jwtExp = now.Add(operator.UserJWTTTL)
			default:
				jwtExp = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
			}

			// Structured reason marker so the P13 panel can distinguish
			// rotation-induced revocations from operator-driven ones.
			revReason := fmt.Sprintf("ssk_rotation:%s:%s", ssk.ID.String(), reason)
			rev := &entities.UserJWTRevocation{
				ID:            uuid.New(),
				AccountID:     account.ID,
				UserID:        &user.ID,
				UserPublicKey: user.PublicKey,
				RevokedAt:     revokedAt,
				JWTExp:        jwtExp,
				Reason:        revReason,
				CreatedAt:     now,
			}
			if err := revRepo.Create(ctx, rev); err != nil {
				return fmt.Errorf("failed to persist revocation for user %s: %w", user.Name, err)
			}

			// 5. Re-mint user JWT signed by the new key. ssk row in this
			// tx already carries the new pubkey + new encrypted seed, so
			// GenerateUserJWT picks them up via the scopedKey argument.
			mint, err := s.jwtService.GenerateUserJWT(ctx, user, account, ssk, user.EffectiveJWTTTL(operator))
			if err != nil {
				return fmt.Errorf("failed to mint new JWT for user %s: %w", user.Name, err)
			}

			user.JWT = mint.Token
			user.JWTIssuedAt = &mint.IssuedAt
			user.JWTExpiresAt = mint.ExpiresAt
			user.LastExpiringWarnIAT = nil
			user.LastExpiredAlertIAT = nil
			user.UpdatedAt = now
			if err := userRepo.Update(ctx, user); err != nil {
				return fmt.Errorf("failed to update user %s: %w", user.Name, err)
			}

			if err := events.EmitTx(ctx, tx, events.Event{
				Type:         entities.EventTypeUserRevoked,
				OperatorID:   &account.OperatorID,
				AccountID:    &account.ID,
				ResourceType: "user",
				ResourceID:   user.ID.String(),
				Payload: map[string]any{
					"name":          user.Name,
					"public_key":    user.PublicKey,
					"reason":        revReason,
					"jwt_exp":       jwtExp.Unix(),
					"triggered_by":  "ssk_rotation",
					"scoped_key_id": ssk.ID.String(),
				},
			}); err != nil {
				return fmt.Errorf("emit user.revoked for %s: %w", user.Name, err)
			}

			revokedPubKeys = append(revokedPubKeys, user.PublicKey)
		}

		// 6. Re-sign the account JWT. regenerateAccountJWTTx re-reads
		// scoped_keys (so it picks up the new SSK pubkey) and active
		// revocations (so the new entries are flattened into Revocations).
		if err := s.regenerateAccountJWTTx(ctx, tx, account.ID); err != nil {
			return err
		}
		// Re-fetch the account after JWT regen so the post-commit push
		// sees the fresh JWT.
		account, err = accountRepo.GetByID(ctx, account.ID)
		if err != nil {
			return fmt.Errorf("failed to re-read account after JWT regen: %w", err)
		}

		// 7. Summary event. Per-user user.revoked already emitted above;
		// this top-level event is the audit anchor for the operator
		// action.
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyRotated,
			OperatorID:   &account.OperatorID,
			AccountID:    &account.ID,
			ResourceType: "scoped_key",
			ResourceID:   ssk.ID.String(),
			Payload: map[string]any{
				"name":                     ssk.Name,
				"old_public_key":           oldPubKey,
				"new_public_key":           ssk.PublicKey,
				"affected_users":           len(revokedPubKeys),
				"revoked_user_public_keys": revokedPubKeys,
				"reason":                   reason,
			},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.rotated: %w", err)
		}

		// Capture state for the post-commit push.
		result.ScopedSigningKey = ssk
		result.OldPublicKey = oldPubKey
		result.AffectedUsers = len(revokedPubKeys)
		result.RevokedUserPublicKeys = revokedPubKeys
		updatedAcct = account
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Post-commit push. Failures are reported in PushOutcomes — never
	// propagated. DB is source of truth; the operator can re-sync via
	// `nisctl cluster sync` and the P9 drift dashboard surfaces lag.
	if s.clusterService != nil && updatedAcct != nil {
		result.PushOutcomes = s.clusterService.PushAccountToAllClustersDetailed(ctx, updatedAcct.OperatorID, updatedAcct)
	}
	return &result, nil
}
