package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ErrOperatorHasClusters is returned by DeleteOperator when clusters are still
// attached to the operator. Callers (handlers, CLI) should detect this with
// errors.Is and surface it as a precondition failure — the underlying FK is
// ON DELETE RESTRICT, so without this guard the user gets a raw SQLSTATE
// 23503 leak. The wrapped error's message lists the attached cluster names.
var ErrOperatorHasClusters = errors.New("operator has attached clusters")

// OperatorService provides business logic for operator management.
//
// CreateOperator and DeleteOperator span multiple writes (and in Create's case,
// a nested AccountService call for the $SYS account). Both wrap their writes in
// a single factory.WithTx so a partial failure rolls back the entire tree,
// instead of leaving the API with a half-created operator the caller can't
// clean up.
type OperatorService struct {
	factory        persistence.RepositoryFactory
	accountService *AccountService
	jwtService     *JWTService
	encryptor      encryption.Encryptor
}

// NewOperatorService creates a new operator service
func NewOperatorService(
	factory persistence.RepositoryFactory,
	accountService *AccountService,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *OperatorService {
	return &OperatorService{
		factory:        factory,
		accountService: accountService,
		jwtService:     jwtService,
		encryptor:      encryptor,
	}
}

// CreateOperatorRequest contains the data needed to create an operator
type CreateOperatorRequest struct {
	Name           string
	Description    string
	OrganizationID *uuid.UUID // nil → default organization
}

// CreateOperator creates a new operator with generated keys and JWT.
// Automatically creates a $SYS account for cluster management.
//
// All four writes (operator row → $SYS account+default scoped key → system user
// → operator row update with system account pubkey) share the same tx, so a
// failure midway rolls the whole creation back atomically.
func (s *OperatorService) CreateOperator(ctx context.Context, req CreateOperatorRequest) (*entities.Operator, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("operator name is required")
	}

	var result *entities.Operator
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		operatorRepo := tx.OperatorRepository()
		userRepo := tx.UserRepository()

		// Resolve organization ID (default org when not specified).
		orgID := uuid.MustParse(entities.DefaultOrganizationID)
		if req.OrganizationID != nil {
			orgID = *req.OrganizationID
			if _, err := tx.OrganizationRepository().GetByID(ctx, orgID); err != nil {
				return fmt.Errorf("organization not found: %w", err)
			}
		}

		// Check if operator with this name already exists within the org.
		// Names are unique per (organization_id, name), not globally.
		existing, err := operatorRepo.GetByName(ctx, orgID, req.Name)
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return fmt.Errorf("failed to check existing operator: %w", err)
		}
		if existing != nil {
			return repositories.ErrAlreadyExists
		}

		// Generate operator NKey pair
		seed, pubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
		if err != nil {
			return fmt.Errorf("failed to generate operator keys: %w", err)
		}

		// Encrypt the seed
		encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
		if err != nil {
			return fmt.Errorf("failed to encrypt operator seed: %w", err)
		}

		// Create operator entity (without system account initially).
		operator := &entities.Operator{
			ID:                  uuid.New(),
			Name:                req.Name,
			Description:         req.Description,
			EncryptedSeed:       encryptedSeed,
			PublicKey:           pubKey,
			SystemAccountPubKey: "",
			OrganizationID:      orgID,
			CreatedAt:           clock.Now(),
			UpdatedAt:           clock.Now(),
		}

		// Generate JWT (without system account)
		opJWT, err := s.jwtService.GenerateOperatorJWT(ctx, operator)
		if err != nil {
			return fmt.Errorf("failed to generate operator JWT: %w", err)
		}
		operator.JWT = opJWT

		// Save to repository
		if err := operatorRepo.Create(ctx, operator); err != nil {
			return fmt.Errorf("failed to create operator: %w", err)
		}

		// Create $SYS account using the SAME tx-scoped factory so the account +
		// its default scoped signing key are inside the same atomic boundary.
		sysAccount, err := s.accountService.createAccountTx(ctx, tx, CreateAccountRequest{
			OperatorID:  operator.ID,
			Name:        "$SYS",
			Description: "System account for operator management and syncing",
		})
		if err != nil {
			return fmt.Errorf("failed to create $SYS account: %w", err)
		}

		// Create system user in $SYS account
		sysUserSeed, sysUserPubKey, err := GenerateNKey(nkeys.PrefixByteUser)
		if err != nil {
			return fmt.Errorf("failed to generate system user keys: %w", err)
		}

		encryptedSysUserSeed, err := s.encryptor.Encrypt(ctx, sysUserSeed)
		if err != nil {
			return fmt.Errorf("failed to encrypt system user seed: %w", err)
		}

		sysUser := &entities.User{
			ID:                 uuid.New(),
			AccountID:          sysAccount.ID,
			Name:               "system",
			Description:        "System user for operator management",
			EncryptedSeed:      encryptedSysUserSeed,
			PublicKey:          sysUserPubKey,
			ScopedSigningKeyID: nil,
			CreatedAt:          clock.Now(),
			UpdatedAt:          clock.Now(),
		}

		// Generate system user JWT. TTL=0 — the system user is operator-internal
		// and is not subject to the per-operator user-JWT TTL policy.
		sysUserMint, err := s.jwtService.GenerateUserJWT(ctx, sysUser, sysAccount, nil, 0)
		if err != nil {
			return fmt.Errorf("failed to generate system user JWT: %w", err)
		}
		sysUser.JWT = sysUserMint.Token
		sysUser.JWTIssuedAt = &sysUserMint.IssuedAt
		sysUser.JWTExpiresAt = sysUserMint.ExpiresAt

		// Save system user
		if err := userRepo.Create(ctx, sysUser); err != nil {
			return fmt.Errorf("failed to create system user: %w", err)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeUserCreated,
			OperatorID:   &operator.ID,
			AccountID:    &sysAccount.ID,
			ResourceType: "user",
			ResourceID:   sysUser.ID.String(),
			Payload:      map[string]any{"name": sysUser.Name, "public_key": sysUser.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit user.created (system): %w", err)
		}

		// Update operator with system account public key and regenerate JWT
		operator.SystemAccountPubKey = sysAccount.PublicKey
		operator.UpdatedAt = clock.Now()

		opJWT, err = s.jwtService.GenerateOperatorJWT(ctx, operator)
		if err != nil {
			return fmt.Errorf("failed to regenerate operator JWT with system account: %w", err)
		}
		operator.JWT = opJWT

		// Update operator with system account reference
		if err := operatorRepo.Update(ctx, operator); err != nil {
			return fmt.Errorf("failed to update operator with system account: %w", err)
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorCreated,
			OperatorID:   &operator.ID,
			ResourceType: "operator",
			ResourceID:   operator.ID.String(),
			Payload:      map[string]any{"name": operator.Name, "public_key": operator.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit operator.created: %w", err)
		}

		result = operator
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetOperator retrieves an operator by ID
func (s *OperatorService) GetOperator(ctx context.Context, id uuid.UUID) (*entities.Operator, error) {
	return s.factory.OperatorRepository().GetByID(ctx, id)
}

// GetOperatorByName retrieves an operator by name within an organization.
func (s *OperatorService) GetOperatorByName(ctx context.Context, orgID uuid.UUID, name string) (*entities.Operator, error) {
	return s.factory.OperatorRepository().GetByName(ctx, orgID, name)
}

// GetOperatorByPublicKey retrieves an operator by public key
func (s *OperatorService) GetOperatorByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error) {
	return s.factory.OperatorRepository().GetByPublicKey(ctx, publicKey)
}

// ListOperators retrieves all operators with pagination (legacy — kept for internal callers).
func (s *OperatorService) ListOperators(ctx context.Context, opts repositories.ListOptions) ([]*entities.Operator, error) {
	return s.factory.OperatorRepository().List(ctx, opts)
}

// ListOperatorsPage returns one keyset-paginated page of operators visible under scope.
func (s *OperatorService) ListOperatorsPage(ctx context.Context, scope authz.Scope, filter repositories.OperatorListFilter) ([]*entities.Operator, string, error) {
	return s.factory.OperatorRepository().ListPage(ctx, scope, filter)
}

// UpdateOperatorRequest contains the fields that can be updated
type UpdateOperatorRequest struct {
	Name        *string
	Description *string
}

// UpdateOperator updates an operator's metadata (does not regenerate keys)
func (s *OperatorService) UpdateOperator(ctx context.Context, id uuid.UUID, req UpdateOperatorRequest) (*entities.Operator, error) {
	var result *entities.Operator
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		repo := tx.OperatorRepository()

		// Get existing operator
		operator, err := repo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// P1 diff snapshot: capture pre-mutation values before any assignment.
		beforeName := operator.Name
		beforeDescription := operator.Description

		// Update fields if provided
		updated := false
		if req.Name != nil && *req.Name != operator.Name {
			// Check if new name is already taken within the same org.
			existing, err := repo.GetByName(ctx, operator.OrganizationID, *req.Name)
			if err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return fmt.Errorf("failed to check existing operator: %w", err)
			}
			if existing != nil && existing.ID != id {
				return repositories.ErrAlreadyExists
			}
			operator.Name = *req.Name
			updated = true
		}

		if req.Description != nil && *req.Description != operator.Description {
			operator.Description = *req.Description
			updated = true
		}

		if !updated {
			result = operator
			return nil
		}

		operator.UpdatedAt = clock.Now()

		// Regenerate JWT with updated name
		opJWT, err := s.jwtService.GenerateOperatorJWT(ctx, operator)
		if err != nil {
			return fmt.Errorf("failed to regenerate operator JWT: %w", err)
		}
		operator.JWT = opJWT

		// Save changes
		if err := repo.Update(ctx, operator); err != nil {
			return fmt.Errorf("failed to update operator: %w", err)
		}

		var diff events.DiffBuilder
		diff.Set("name", beforeName, operator.Name)
		diff.Set("description", beforeDescription, operator.Description)
		diff.SetRedacted("jwt", true) // JWT always regenerates on UpdateOperator

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorUpdated,
			OperatorID:   &operator.ID,
			ResourceType: "operator",
			ResourceID:   operator.ID.String(),
			Payload:      map[string]any{"name": operator.Name},
			Diff:         diff.Finalize(),
		}); err != nil {
			return fmt.Errorf("emit operator.updated: %w", err)
		}

		result = operator
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// JWTPolicyUpdate is the SetJWTPolicy request shape — each pointer is nil
// when the caller doesn't want to change that field. Set the pointer to a
// *zero* duration (or to false) to explicitly disable that knob.
type JWTPolicyUpdate struct {
	UserJWTTTL    *time.Duration
	AccountJWTTTL *time.Duration
	JWTWarnWindow *time.Duration
	JWTAutoRenew  *bool
}

// SetJWTPolicy updates the operator's JWT lifecycle policy (P2). The
// operator JWT itself is NOT regenerated — policy doesn't go into the
// operator claims; it's a NIS-side setting that flows into newly-minted user
// and account JWTs at sign time.
//
// Note: changing the policy does NOT retroactively re-sign existing user
// JWTs. They keep whatever exp they were minted with until their next
// renewal/regenerate. This is intentional — bulk re-signing is a separate
// operation (and one the sweeper handles when auto-renew is enabled).
func (s *OperatorService) SetJWTPolicy(ctx context.Context, id uuid.UUID, p JWTPolicyUpdate) (*entities.Operator, error) {
	var result *entities.Operator
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		repo := tx.OperatorRepository()
		operator, err := repo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// P1 diff snapshot: capture pre-mutation policy values.
		beforeUserJWTTTL := operator.UserJWTTTL
		beforeAccountJWTTTL := operator.AccountJWTTTL
		beforeJWTWarnWindow := operator.JWTWarnWindow
		beforeJWTAutoRenew := operator.JWTAutoRenew

		changed := false
		if p.UserJWTTTL != nil && *p.UserJWTTTL != operator.UserJWTTTL {
			operator.UserJWTTTL = *p.UserJWTTTL
			changed = true
		}
		if p.AccountJWTTTL != nil && *p.AccountJWTTTL != operator.AccountJWTTTL {
			operator.AccountJWTTTL = *p.AccountJWTTTL
			changed = true
		}
		if p.JWTWarnWindow != nil && *p.JWTWarnWindow != operator.JWTWarnWindow {
			operator.JWTWarnWindow = *p.JWTWarnWindow
			changed = true
		}
		if p.JWTAutoRenew != nil && *p.JWTAutoRenew != operator.JWTAutoRenew {
			operator.JWTAutoRenew = *p.JWTAutoRenew
			changed = true
		}
		if !changed {
			result = operator
			return nil
		}
		operator.UpdatedAt = clock.Now()
		if err := repo.Update(ctx, operator); err != nil {
			return fmt.Errorf("failed to update operator JWT policy: %w", err)
		}
		var diff events.DiffBuilder
		diff.Set("user_jwt_ttl_seconds", int64(beforeUserJWTTTL.Seconds()), int64(operator.UserJWTTTL.Seconds()))
		diff.Set("account_jwt_ttl_seconds", int64(beforeAccountJWTTTL.Seconds()), int64(operator.AccountJWTTTL.Seconds()))
		diff.Set("jwt_warn_window_seconds", int64(beforeJWTWarnWindow.Seconds()), int64(operator.JWTWarnWindow.Seconds()))
		diff.Set("jwt_auto_renew", beforeJWTAutoRenew, operator.JWTAutoRenew)

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorUpdated,
			OperatorID:   &operator.ID,
			ResourceType: "operator",
			ResourceID:   operator.ID.String(),
			Payload: map[string]any{
				"changed":                 []string{"jwt_policy"},
				"user_jwt_ttl_seconds":    int64(operator.UserJWTTTL.Seconds()),
				"account_jwt_ttl_seconds": int64(operator.AccountJWTTTL.Seconds()),
				"jwt_warn_window_seconds": int64(operator.JWTWarnWindow.Seconds()),
				"jwt_auto_renew":          operator.JWTAutoRenew,
			},
			Diff: diff.Finalize(),
		}); err != nil {
			return fmt.Errorf("emit operator.updated (jwt_policy): %w", err)
		}
		result = operator
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// SetSystemAccount sets or updates the system account for an operator
func (s *OperatorService) SetSystemAccount(ctx context.Context, operatorID uuid.UUID, systemAccountPubKey string) (*entities.Operator, error) {
	return s.SetSystemAccountTx(ctx, s.factory, operatorID, systemAccountPubKey)
}

// SetSystemAccountTx is the tx-aware variant of SetSystemAccount. Pass a
// tx-scoped factory (from a parent service's factory.WithTx block) to make
// the read+write participate in the surrounding transaction. Used by
// ExportService.ImportFromNSC so the operator update is part of the same
// rollback boundary as the rest of the import.
func (s *OperatorService) SetSystemAccountTx(ctx context.Context, tx persistence.RepositoryFactory, operatorID uuid.UUID, systemAccountPubKey string) (*entities.Operator, error) {
	repo := tx.OperatorRepository()

	// Get existing operator
	operator, err := repo.GetByID(ctx, operatorID)
	if err != nil {
		return nil, err
	}

	// Validate the system account public key format (should start with 'A')
	if systemAccountPubKey != "" && systemAccountPubKey[0] != 'A' {
		return nil, fmt.Errorf("invalid system account public key: must start with 'A'")
	}

	beforeSystemAccountPubKey := operator.SystemAccountPubKey

	// Update system account
	operator.SystemAccountPubKey = systemAccountPubKey
	operator.UpdatedAt = clock.Now()

	// Check if the operator JWT already has the correct system account
	// This happens when importing from NSC where the JWT is preserved
	existingClaims, err := jwt.DecodeOperatorClaims(operator.JWT)
	// Regenerate JWT only if decoding failed or system account doesn't match
	regenerateJWT := err != nil || existingClaims.SystemAccount != systemAccountPubKey

	// Regenerate JWT with new system account only if needed
	if regenerateJWT {
		newJWT, err := s.jwtService.GenerateOperatorJWT(ctx, operator)
		if err != nil {
			return nil, fmt.Errorf("failed to regenerate operator JWT: %w", err)
		}
		operator.JWT = newJWT
	}

	// Save changes
	if err := repo.Update(ctx, operator); err != nil {
		return nil, fmt.Errorf("failed to update operator: %w", err)
	}

	var diff events.DiffBuilder
	diff.Set("system_account_pub_key", beforeSystemAccountPubKey, operator.SystemAccountPubKey)

	if err := events.EmitTx(ctx, tx, events.Event{
		Type:         entities.EventTypeOperatorUpdated,
		OperatorID:   &operator.ID,
		ResourceType: "operator",
		ResourceID:   operator.ID.String(),
		Payload:      map[string]any{"name": operator.Name, "changed": []string{"system_account"}},
		Diff:         diff.Finalize(),
	}); err != nil {
		return nil, fmt.Errorf("emit operator.updated (set_system_account): %w", err)
	}

	return operator, nil
}

// DeleteOperator deletes an operator and all associated data atomically. If any
// step fails (e.g. one of the user deletes), the whole cascade rolls back —
// without that, you can end up with orphan accounts referencing a deleted
// operator that no list endpoint can surface.
//
// Refuses to delete when clusters are still attached: clusters.operator_id is
// ON DELETE RESTRICT because clusters model live NATS infra (an existing
// resolver, baked-in operator JWT, etc.) — we don't want a silent cascade
// removing that record just because someone wanted the operator gone.
// Returns ErrOperatorHasClusters (wrapped, with names) on that path.
func (s *OperatorService) DeleteOperator(ctx context.Context, id uuid.UUID) error {
	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		operatorRepo := tx.OperatorRepository()
		accountRepo := tx.AccountRepository()
		userRepo := tx.UserRepository()
		clusterRepo := tx.ClusterRepository()

		// Check if operator exists (and fail fast if not)
		op, err := operatorRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// Pre-flight: clusters block deletion. List up to 1000 — anyone with
		// more clusters than that on a single operator has bigger problems.
		clusters, err := clusterRepo.ListByOperator(ctx, id, repositories.ListOptions{Limit: 1000})
		if err != nil {
			return fmt.Errorf("failed to list clusters for deletion check: %w", err)
		}
		if len(clusters) > 0 {
			names := make([]string, 0, len(clusters))
			for _, c := range clusters {
				names = append(names, c.Name)
			}
			return fmt.Errorf("%w: %d attached (%s); delete them first (note: scoped API users under this operator will also be removed when delete proceeds)", ErrOperatorHasClusters, len(clusters), strings.Join(names, ", "))
		}

		// Get all accounts for this operator
		accounts, err := accountRepo.ListByOperator(ctx, id, repositories.ListOptions{Limit: 10000})
		if err != nil {
			return fmt.Errorf("failed to list accounts for deletion: %w", err)
		}

		// Delete all accounts (and their associated users/signing keys)
		for _, account := range accounts {
			// Get all users for this account
			users, err := userRepo.ListByAccount(ctx, account.ID, repositories.ListOptions{Limit: 10000})
			if err != nil {
				return fmt.Errorf("failed to list users for account %s: %w", account.ID, err)
			}

			// Delete all users
			for _, user := range users {
				if err := userRepo.Delete(ctx, user.ID); err != nil {
					return fmt.Errorf("failed to delete user %s: %w", user.ID, err)
				}
			}

			// Delete account (cascade to scoped keys happens at the FK layer)
			if err := accountRepo.Delete(ctx, account.ID); err != nil {
				return fmt.Errorf("failed to delete account %s: %w", account.ID, err)
			}
		}

		// Finally delete the operator
		if err := operatorRepo.Delete(ctx, id); err != nil {
			return err
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorDeleted,
			OperatorID:   &id,
			ResourceType: "operator",
			ResourceID:   id.String(),
			Payload:      map[string]any{"name": op.Name, "public_key": op.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit operator.deleted: %w", err)
		}

		return nil
	})
}

// GenerateInclude generates a NATS server configuration with operator JWT and preloaded system account
func (s *OperatorService) GenerateInclude(ctx context.Context, id uuid.UUID) (string, error) {
	operatorRepo := s.factory.OperatorRepository()
	accountRepo := s.factory.AccountRepository()

	// Get operator
	operator, err := operatorRepo.GetByID(ctx, id)
	if err != nil {
		return "", err
	}

	// Check if operator has system account configured
	if operator.SystemAccountPubKey == "" {
		return "", fmt.Errorf("operator does not have a system account configured")
	}

	// Get all accounts for this operator and find the system account
	accounts, err := accountRepo.ListByOperator(ctx, operator.ID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return "", fmt.Errorf("failed to list accounts: %w", err)
	}

	// Find the system account by public key
	var sysAccount *entities.Account
	for _, account := range accounts {
		if account.PublicKey == operator.SystemAccountPubKey {
			sysAccount = account
			break
		}
	}

	if sysAccount == nil {
		return "", fmt.Errorf("system account not found with public key: %s", operator.SystemAccountPubKey)
	}

	// Generate NATS config
	config := fmt.Sprintf(`# NATS Server Configuration with JWT Authentication
# Generated by NIS for operator: %s

# Operator JWT
operator: %s

# File resolver - supports dynamic updates via $SYS.REQ.CLAIMS.UPDATE
resolver: {
    type: full
    dir: '/resolver'
    allow_delete: true
    interval: "2m"
}

# Preload system account (%s)
resolver_preload: {
    %s: %s
}

# JetStream configuration
jetstream: {
    store_dir: /data/jetstream
}
`, operator.Name, operator.JWT, sysAccount.Name, operator.SystemAccountPubKey, sysAccount.JWT)

	return config, nil
}
