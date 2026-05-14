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
	Name        string
	Description string
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

		// Check if operator with this name already exists
		existing, err := operatorRepo.GetByName(ctx, req.Name)
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

		// Create operator entity (without system account initially)
		operator := &entities.Operator{
			ID:                  uuid.New(),
			Name:                req.Name,
			Description:         req.Description,
			EncryptedSeed:       encryptedSeed,
			PublicKey:           pubKey,
			SystemAccountPubKey: "",
			CreatedAt:           time.Now(),
			UpdatedAt:           time.Now(),
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
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}

		// Generate system user JWT
		sysUserJWT, err := s.jwtService.GenerateUserJWT(ctx, sysUser, sysAccount, nil)
		if err != nil {
			return fmt.Errorf("failed to generate system user JWT: %w", err)
		}
		sysUser.JWT = sysUserJWT

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
		operator.UpdatedAt = time.Now()

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

// GetOperatorByName retrieves an operator by name
func (s *OperatorService) GetOperatorByName(ctx context.Context, name string) (*entities.Operator, error) {
	return s.factory.OperatorRepository().GetByName(ctx, name)
}

// GetOperatorByPublicKey retrieves an operator by public key
func (s *OperatorService) GetOperatorByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error) {
	return s.factory.OperatorRepository().GetByPublicKey(ctx, publicKey)
}

// ListOperators retrieves all operators with pagination
func (s *OperatorService) ListOperators(ctx context.Context, opts repositories.ListOptions) ([]*entities.Operator, error) {
	return s.factory.OperatorRepository().List(ctx, opts)
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

		// Update fields if provided
		updated := false
		if req.Name != nil && *req.Name != operator.Name {
			// Check if new name is already taken
			existing, err := repo.GetByName(ctx, *req.Name)
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

		operator.UpdatedAt = time.Now()

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

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorUpdated,
			OperatorID:   &operator.ID,
			ResourceType: "operator",
			ResourceID:   operator.ID.String(),
			Payload:      map[string]any{"name": operator.Name},
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

	// Update system account
	operator.SystemAccountPubKey = systemAccountPubKey
	operator.UpdatedAt = time.Now()

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
