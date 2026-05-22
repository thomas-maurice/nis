package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// UserService provides business logic for user management
type UserService struct {
	repo          repositories.UserRepository
	accountRepo   repositories.AccountRepository
	scopedKeyRepo repositories.ScopedSigningKeyRepository
	operatorRepo  repositories.OperatorRepository
	jwtService    *JWTService
	encryptor     encryption.Encryptor
	factory       persistence.RepositoryFactory // optional; set via WithFactory for event emission
}

// NewUserService creates a new user service
func NewUserService(
	repo repositories.UserRepository,
	accountRepo repositories.AccountRepository,
	scopedKeyRepo repositories.ScopedSigningKeyRepository,
	operatorRepo repositories.OperatorRepository,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *UserService {
	return &UserService{
		repo:          repo,
		accountRepo:   accountRepo,
		scopedKeyRepo: scopedKeyRepo,
		operatorRepo:  operatorRepo,
		jwtService:    jwtService,
		encryptor:     encryptor,
	}
}

// WithFactory attaches a repository factory to the service, enabling event emission.
// Call this from serve.go after constructing the service. Tests that don't call this
// will skip event emission (factory is nil).
func (s *UserService) WithFactory(f persistence.RepositoryFactory) *UserService {
	s.factory = f
	return s
}

// CreateUserRequest contains the data needed to create a user
type CreateUserRequest struct {
	AccountID          uuid.UUID
	Name               string
	Description        string
	ScopedSigningKeyID *uuid.UUID     // Optional - if provided, user JWT will be signed by scoped key
	JWTTTLOverride     *time.Duration // Optional - per-user TTL override; nil = inherit operator default
}

// CreateUser creates a new user with generated keys and JWT
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) (*entities.User, error) {
	if s.factory == nil {
		// Test/no-factory path: use the service's own repos. The operatorRepo
		// dep is needed to resolve TTL; tests that don't wire WithFactory must
		// supply operatorRepo through the constructor anyway.
		return s.createUserWith(ctx, s.repo, s.accountRepo, s.scopedKeyRepo, s.operatorRepo, req)
	}
	var result *entities.User
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		user, err := s.createUserWith(ctx, tx.UserRepository(), tx.AccountRepository(), tx.ScopedSigningKeyRepository(), tx.OperatorRepository(), req)
		if err != nil {
			return err
		}
		account, err := tx.AccountRepository().GetByID(ctx, user.AccountID)
		if err != nil {
			return fmt.Errorf("emit user.created: lookup account: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeUserCreated,
			OperatorID:   &account.OperatorID,
			AccountID:    &user.AccountID,
			ResourceType: "user",
			ResourceID:   user.ID.String(),
			Payload:      map[string]any{"name": user.Name, "public_key": user.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit user.created: %w", err)
		}
		result = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CreateUserTx is the tx-aware variant of CreateUser. Callers already running
// inside another service's factory.WithTx block should use this so the
// account/scoped-key lookups see uncommitted writes from the surrounding tx,
// and the user.Create participates in the same rollback boundary.
func (s *UserService) CreateUserTx(ctx context.Context, tx persistence.RepositoryFactory, req CreateUserRequest) (*entities.User, error) {
	user, err := s.createUserWith(ctx, tx.UserRepository(), tx.AccountRepository(), tx.ScopedSigningKeyRepository(), tx.OperatorRepository(), req)
	if err != nil {
		return nil, err
	}
	account, err := tx.AccountRepository().GetByID(ctx, user.AccountID)
	if err != nil {
		return nil, fmt.Errorf("emit user.created: lookup account: %w", err)
	}
	if err := events.EmitTx(ctx, tx, events.Event{
		Type:         entities.EventTypeUserCreated,
		OperatorID:   &account.OperatorID,
		AccountID:    &user.AccountID,
		ResourceType: "user",
		ResourceID:   user.ID.String(),
		Payload:      map[string]any{"name": user.Name, "public_key": user.PublicKey},
	}); err != nil {
		return nil, fmt.Errorf("emit user.created: %w", err)
	}
	return user, nil
}

// createUserWith is the shared body. It takes the three repos as parameters so
// the public method and the *Tx variant can hand it either the global repos or
// tx-scoped ones without duplicating logic.
func (s *UserService) createUserWith(ctx context.Context, userRepo repositories.UserRepository, accountRepo repositories.AccountRepository, scopedKeyRepo repositories.ScopedSigningKeyRepository, operatorRepo repositories.OperatorRepository, req CreateUserRequest) (*entities.User, error) {
	// Validate request
	if req.Name == "" {
		return nil, fmt.Errorf("user name is required")
	}

	// Get account
	account, err := accountRepo.GetByID(ctx, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	// Load parent operator for TTL policy.
	operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	// Check if user with this name already exists for this account
	existing, err := userRepo.GetByName(ctx, req.AccountID, req.Name)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, fmt.Errorf("failed to check existing user: %w", err)
	}
	if existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// If scoped signing key is provided, get it and validate it belongs to this account
	var scopedKey *entities.ScopedSigningKey
	if req.ScopedSigningKeyID != nil {
		scopedKey, err = scopedKeyRepo.GetByID(ctx, *req.ScopedSigningKeyID)
		if err != nil {
			return nil, fmt.Errorf("failed to get scoped signing key: %w", err)
		}
		if scopedKey.AccountID != req.AccountID {
			return nil, fmt.Errorf("scoped signing key does not belong to the specified account")
		}
	}

	// Generate user NKey pair
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	if err != nil {
		return nil, fmt.Errorf("failed to generate user keys: %w", err)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt user seed: %w", err)
	}

	// Create user entity (TTL override applied via req)
	user := &entities.User{
		ID:                 uuid.New(),
		AccountID:          req.AccountID,
		Name:               req.Name,
		Description:        req.Description,
		EncryptedSeed:      encryptedSeed,
		PublicKey:          pubKey,
		ScopedSigningKeyID: req.ScopedSigningKeyID,
		JWTTTL:             req.JWTTTLOverride,
		CreatedAt:          clock.Now(),
		UpdatedAt:          clock.Now(),
	}

	// Generate JWT (signed by account or scoped signing key). TTL resolves to
	// the per-user override if set, else the operator default, else 0 (no exp).
	mint, err := s.jwtService.GenerateUserJWT(ctx, user, account, scopedKey, user.EffectiveJWTTTL(operator))
	if err != nil {
		return nil, fmt.Errorf("failed to generate user JWT: %w", err)
	}
	user.JWT = mint.Token
	user.JWTIssuedAt = &mint.IssuedAt
	user.JWTExpiresAt = mint.ExpiresAt

	// Save to repository
	if err := userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return user, nil
}

// GetUser retrieves a user by ID
func (s *UserService) GetUser(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	return s.repo.GetByID(ctx, id)
}

// GetUserByName retrieves a user by account ID and name
func (s *UserService) GetUserByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.User, error) {
	return s.repo.GetByName(ctx, accountID, name)
}

// GetUserByPublicKey retrieves a user by public key
func (s *UserService) GetUserByPublicKey(ctx context.Context, publicKey string) (*entities.User, error) {
	return s.repo.GetByPublicKey(ctx, publicKey)
}

// ListUsersByAccount retrieves all users for an account with pagination
func (s *UserService) ListUsersByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	return s.repo.ListByAccount(ctx, accountID, opts)
}

// ListAllUsers lists all users across all accounts (legacy — kept for internal callers).
func (s *UserService) ListAllUsers(ctx context.Context, opts repositories.ListOptions) ([]*entities.User, error) {
	return s.repo.List(ctx, opts)
}

// ListUsersPage returns one keyset-paginated page of users visible under scope.
func (s *UserService) ListUsersPage(ctx context.Context, scope authz.Scope, filter repositories.UserListFilter) ([]*entities.User, string, error) {
	return s.repo.ListPage(ctx, scope, filter)
}

// ListUsersByScopedKey retrieves all users signed by a scoped signing key
func (s *UserService) ListUsersByScopedKey(ctx context.Context, scopedKeyID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	return s.repo.ListByScopedSigningKey(ctx, scopedKeyID, opts)
}

// UpdateUserRequest contains the fields that can be updated
type UpdateUserRequest struct {
	Name        *string
	Description *string
	// JWTTTL: when SetJWTTTL is true, JWTTTL is applied as the per-user override.
	// JWTTTL == nil with SetJWTTTL=true means "clear the override" (inherit
	// operator default). Without SetJWTTTL the field is untouched. Decoupling
	// the two avoids the proto3-optional ambiguity for "field absent" vs "field
	// set to zero".
	JWTTTL    *time.Duration
	SetJWTTTL bool
}

// UpdateUser updates a user's metadata and regenerates JWT
func (s *UserService) UpdateUser(ctx context.Context, id uuid.UUID, req UpdateUserRequest) (*entities.User, error) {
	updateFn := func(userRepo repositories.UserRepository, accountRepo repositories.AccountRepository, scopedKeyRepo repositories.ScopedSigningKeyRepository, operatorRepo repositories.OperatorRepository) (*entities.User, error) {
		// Get existing user
		user, err := userRepo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}

		// Update fields if provided
		updated := false
		if req.Name != nil && *req.Name != user.Name {
			// Check if new name is already taken for this account
			existing, err := userRepo.GetByName(ctx, user.AccountID, *req.Name)
			if err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return nil, fmt.Errorf("failed to check existing user: %w", err)
			}
			if existing != nil && existing.ID != id {
				return nil, repositories.ErrAlreadyExists
			}
			user.Name = *req.Name
			updated = true
		}

		if req.Description != nil && *req.Description != user.Description {
			user.Description = *req.Description
			updated = true
		}

		if req.SetJWTTTL {
			user.JWTTTL = req.JWTTTL
			updated = true
		}

		if !updated {
			return user, nil
		}

		user.UpdatedAt = clock.Now()

		// Get account, operator, and optional scoped key to regenerate JWT
		account, err := accountRepo.GetByID(ctx, user.AccountID)
		if err != nil {
			return nil, fmt.Errorf("failed to get account: %w", err)
		}
		operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
		if err != nil {
			return nil, fmt.Errorf("failed to get operator: %w", err)
		}

		var scopedKey *entities.ScopedSigningKey
		if user.ScopedSigningKeyID != nil {
			scopedKey, err = scopedKeyRepo.GetByID(ctx, *user.ScopedSigningKeyID)
			if err != nil {
				return nil, fmt.Errorf("failed to get scoped signing key: %w", err)
			}
		}

		// Regenerate JWT with updated metadata and the effective TTL.
		mint, err := s.jwtService.GenerateUserJWT(ctx, user, account, scopedKey, user.EffectiveJWTTTL(operator))
		if err != nil {
			return nil, fmt.Errorf("failed to regenerate user JWT: %w", err)
		}
		user.JWT = mint.Token
		user.JWTIssuedAt = &mint.IssuedAt
		user.JWTExpiresAt = mint.ExpiresAt
		// New iat means previous expiring/expired alerts no longer apply.
		user.LastExpiringWarnIAT = nil
		user.LastExpiredAlertIAT = nil

		// Save changes
		if err := userRepo.Update(ctx, user); err != nil {
			return nil, fmt.Errorf("failed to update user: %w", err)
		}

		return user, nil
	}

	if s.factory == nil {
		return updateFn(s.repo, s.accountRepo, s.scopedKeyRepo, s.operatorRepo)
	}

	var result *entities.User
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		user, err := updateFn(tx.UserRepository(), tx.AccountRepository(), tx.ScopedSigningKeyRepository(), tx.OperatorRepository())
		if err != nil {
			return err
		}
		if user == nil {
			return nil
		}
		account, err := tx.AccountRepository().GetByID(ctx, user.AccountID)
		if err != nil {
			return fmt.Errorf("emit user.updated: lookup account: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeUserUpdated,
			OperatorID:   &account.OperatorID,
			AccountID:    &user.AccountID,
			ResourceType: "user",
			ResourceID:   user.ID.String(),
			Payload:      map[string]any{"name": user.Name},
		}); err != nil {
			return fmt.Errorf("emit user.updated: %w", err)
		}
		result = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetUserCredentials returns the complete .creds file content for a user
func (s *UserService) GetUserCredentials(ctx context.Context, id uuid.UUID) (string, error) {
	// Get user
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return "", err
	}

	// Generate credentials using JWT service
	return s.jwtService.GetUserCredentials(ctx, user)
}

// DeleteUser deletes a user
func (s *UserService) DeleteUser(ctx context.Context, id uuid.UUID) error {
	deleteFn := func(userRepo repositories.UserRepository, accountRepo repositories.AccountRepository) (*entities.User, *entities.Account, error) {
		// Check if user exists
		user, err := userRepo.GetByID(ctx, id)
		if err != nil {
			return nil, nil, err
		}

		// Get the account to check if this is a system user
		account, err := accountRepo.GetByID(ctx, user.AccountID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get account: %w", err)
		}

		// Check if this is the system user in the $SYS account
		if account.Name == "$SYS" && user.Name == "system" {
			return nil, nil, fmt.Errorf("cannot delete system user: this user is the system user in the $SYS account")
		}

		// Delete user
		if err := userRepo.Delete(ctx, id); err != nil {
			return nil, nil, err
		}

		return user, account, nil
	}

	if s.factory == nil {
		_, _, err := deleteFn(s.repo, s.accountRepo)
		return err
	}

	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		user, account, err := deleteFn(tx.UserRepository(), tx.AccountRepository())
		if err != nil {
			return err
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeUserDeleted,
			OperatorID:   &account.OperatorID,
			AccountID:    &user.AccountID,
			ResourceType: "user",
			ResourceID:   user.ID.String(),
			Payload:      map[string]any{"name": user.Name, "public_key": user.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit user.deleted: %w", err)
		}
		return nil
	})
}
