package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
)

// UserService provides business logic for user management
type UserService struct {
	repo              repositories.UserRepository
	accountRepo       repositories.AccountRepository
	scopedKeyRepo     repositories.ScopedSigningKeyRepository
	jwtService        *JWTService
	encryptor         encryption.Encryptor
}

// NewUserService creates a new user service
func NewUserService(
	repo repositories.UserRepository,
	accountRepo repositories.AccountRepository,
	scopedKeyRepo repositories.ScopedSigningKeyRepository,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *UserService {
	return &UserService{
		repo:          repo,
		accountRepo:   accountRepo,
		scopedKeyRepo: scopedKeyRepo,
		jwtService:    jwtService,
		encryptor:     encryptor,
	}
}

// CreateUserRequest contains the data needed to create a user
type CreateUserRequest struct {
	AccountID          uuid.UUID
	Name               string
	Description        string
	ScopedSigningKeyID *uuid.UUID // Optional - if provided, user JWT will be signed by scoped key
}

// CreateUser creates a new user with generated keys and JWT
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest) (*entities.User, error) {
	// Validate request
	if req.Name == "" {
		return nil, fmt.Errorf("user name is required")
	}

	// Get account
	account, err := s.accountRepo.GetByID(ctx, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	// Check if user with this name already exists for this account
	existing, err := s.repo.GetByName(ctx, req.AccountID, req.Name)
	if err != nil && err != repositories.ErrNotFound {
		return nil, fmt.Errorf("failed to check existing user: %w", err)
	}
	if existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// If scoped signing key is provided, get it and validate it belongs to this account
	var scopedKey *entities.ScopedSigningKey
	if req.ScopedSigningKeyID != nil {
		scopedKey, err = s.scopedKeyRepo.GetByID(ctx, *req.ScopedSigningKeyID)
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

	// Create user entity
	user := &entities.User{
		ID:                 uuid.New(),
		AccountID:          req.AccountID,
		Name:               req.Name,
		Description:        req.Description,
		EncryptedSeed:      encryptedSeed,
		PublicKey:          pubKey,
		ScopedSigningKeyID: req.ScopedSigningKeyID,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	// Generate JWT (signed by account or scoped signing key)
	jwt, err := s.jwtService.GenerateUserJWT(ctx, user, account, scopedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate user JWT: %w", err)
	}
	user.JWT = jwt

	// Save to repository
	if err := s.repo.Create(ctx, user); err != nil {
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

// ListAllUsers lists all users across all accounts
func (s *UserService) ListAllUsers(ctx context.Context, opts repositories.ListOptions) ([]*entities.User, error) {
	return s.repo.List(ctx, opts)
}

// ListUsersByScopedKey retrieves all users signed by a scoped signing key
func (s *UserService) ListUsersByScopedKey(ctx context.Context, scopedKeyID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	return s.repo.ListByScopedSigningKey(ctx, scopedKeyID, opts)
}

// UpdateUserRequest contains the fields that can be updated
type UpdateUserRequest struct {
	Name        *string
	Description *string
}

// UpdateUser updates a user's metadata and regenerates JWT
func (s *UserService) UpdateUser(ctx context.Context, id uuid.UUID, req UpdateUserRequest) (*entities.User, error) {
	// Get existing user
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update fields if provided
	updated := false
	if req.Name != nil && *req.Name != user.Name {
		// Check if new name is already taken for this account
		existing, err := s.repo.GetByName(ctx, user.AccountID, *req.Name)
		if err != nil && err != repositories.ErrNotFound {
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

	if !updated {
		return user, nil
	}

	user.UpdatedAt = time.Now()

	// Get account and optional scoped key to regenerate JWT
	account, err := s.accountRepo.GetByID(ctx, user.AccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	var scopedKey *entities.ScopedSigningKey
	if user.ScopedSigningKeyID != nil {
		scopedKey, err = s.scopedKeyRepo.GetByID(ctx, *user.ScopedSigningKeyID)
		if err != nil {
			return nil, fmt.Errorf("failed to get scoped signing key: %w", err)
		}
	}

	// Regenerate JWT with updated metadata
	jwt, err := s.jwtService.GenerateUserJWT(ctx, user, account, scopedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate user JWT: %w", err)
	}
	user.JWT = jwt

	// Save changes
	if err := s.repo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return user, nil
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
	// Check if user exists
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Get the account to check if this is a system user
	account, err := s.accountRepo.GetByID(ctx, user.AccountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}

	// Check if this is the system user in the $SYS account
	if account.Name == "$SYS" && user.Name == "system" {
		return fmt.Errorf("cannot delete system user: this user is the system user in the $SYS account")
	}

	// Delete user
	return s.repo.Delete(ctx, id)
}
