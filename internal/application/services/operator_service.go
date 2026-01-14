package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
)

// OperatorService provides business logic for operator management
type OperatorService struct {
	repo           repositories.OperatorRepository
	accountRepo    repositories.AccountRepository
	userRepo       repositories.UserRepository
	accountService *AccountService
	jwtService     *JWTService
	encryptor      encryption.Encryptor
}

// NewOperatorService creates a new operator service
func NewOperatorService(
	repo repositories.OperatorRepository,
	accountRepo repositories.AccountRepository,
	userRepo repositories.UserRepository,
	accountService *AccountService,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *OperatorService {
	return &OperatorService{
		repo:           repo,
		accountRepo:    accountRepo,
		userRepo:       userRepo,
		accountService: accountService,
		jwtService:     jwtService,
		encryptor:      encryptor,
	}
}

// CreateOperatorRequest contains the data needed to create an operator
type CreateOperatorRequest struct {
	Name                string
	Description         string
	SystemAccountPubKey string // Optional
}

// CreateOperator creates a new operator with generated keys and JWT
// Automatically creates a $SYS account for cluster management
func (s *OperatorService) CreateOperator(ctx context.Context, req CreateOperatorRequest) (*entities.Operator, error) {
	// Validate request
	if req.Name == "" {
		return nil, fmt.Errorf("operator name is required")
	}

	// Check if operator with this name already exists
	existing, err := s.repo.GetByName(ctx, req.Name)
	if err != nil && err != repositories.ErrNotFound {
		return nil, fmt.Errorf("failed to check existing operator: %w", err)
	}
	if existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// Generate operator NKey pair
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
	if err != nil {
		return nil, fmt.Errorf("failed to generate operator keys: %w", err)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt operator seed: %w", err)
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
	jwt, err := s.jwtService.GenerateOperatorJWT(ctx, operator)
	if err != nil {
		return nil, fmt.Errorf("failed to generate operator JWT: %w", err)
	}
	operator.JWT = jwt

	// Save to repository
	if err := s.repo.Create(ctx, operator); err != nil {
		return nil, fmt.Errorf("failed to create operator: %w", err)
	}

	// Create $SYS account using AccountService (ensures signing key is created)
	sysAccount, err := s.accountService.CreateAccount(ctx, CreateAccountRequest{
		OperatorID:  operator.ID,
		Name:        "$SYS",
		Description: "System account for operator management and syncing",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create $SYS account: %w", err)
	}

	// Create system user in $SYS account
	sysUserSeed, sysUserPubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	if err != nil {
		return nil, fmt.Errorf("failed to generate system user keys: %w", err)
	}

	encryptedSysUserSeed, err := s.encryptor.Encrypt(ctx, sysUserSeed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt system user seed: %w", err)
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
		return nil, fmt.Errorf("failed to generate system user JWT: %w", err)
	}
	sysUser.JWT = sysUserJWT

	// Save system user
	if err := s.userRepo.Create(ctx, sysUser); err != nil {
		return nil, fmt.Errorf("failed to create system user: %w", err)
	}

	// Update operator with system account public key and regenerate JWT
	operator.SystemAccountPubKey = sysAccount.PublicKey
	operator.UpdatedAt = time.Now()

	jwt, err = s.jwtService.GenerateOperatorJWT(ctx, operator)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate operator JWT with system account: %w", err)
	}
	operator.JWT = jwt

	// Update operator with system account reference
	if err := s.repo.Update(ctx, operator); err != nil {
		return nil, fmt.Errorf("failed to update operator with system account: %w", err)
	}

	return operator, nil
}

// GetOperator retrieves an operator by ID
func (s *OperatorService) GetOperator(ctx context.Context, id uuid.UUID) (*entities.Operator, error) {
	return s.repo.GetByID(ctx, id)
}

// GetOperatorByName retrieves an operator by name
func (s *OperatorService) GetOperatorByName(ctx context.Context, name string) (*entities.Operator, error) {
	return s.repo.GetByName(ctx, name)
}

// GetOperatorByPublicKey retrieves an operator by public key
func (s *OperatorService) GetOperatorByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error) {
	return s.repo.GetByPublicKey(ctx, publicKey)
}

// ListOperators retrieves all operators with pagination
func (s *OperatorService) ListOperators(ctx context.Context, opts repositories.ListOptions) ([]*entities.Operator, error) {
	return s.repo.List(ctx, opts)
}

// UpdateOperatorRequest contains the fields that can be updated
type UpdateOperatorRequest struct {
	Name        *string
	Description *string
}

// UpdateOperator updates an operator's metadata (does not regenerate keys)
func (s *OperatorService) UpdateOperator(ctx context.Context, id uuid.UUID, req UpdateOperatorRequest) (*entities.Operator, error) {
	// Get existing operator
	operator, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update fields if provided
	updated := false
	if req.Name != nil && *req.Name != operator.Name {
		// Check if new name is already taken
		existing, err := s.repo.GetByName(ctx, *req.Name)
		if err != nil && err != repositories.ErrNotFound {
			return nil, fmt.Errorf("failed to check existing operator: %w", err)
		}
		if existing != nil && existing.ID != id {
			return nil, repositories.ErrAlreadyExists
		}
		operator.Name = *req.Name
		updated = true
	}

	if req.Description != nil && *req.Description != operator.Description {
		operator.Description = *req.Description
		updated = true
	}

	if !updated {
		return operator, nil
	}

	operator.UpdatedAt = time.Now()

	// Regenerate JWT with updated name
	jwt, err := s.jwtService.GenerateOperatorJWT(ctx, operator)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate operator JWT: %w", err)
	}
	operator.JWT = jwt

	// Save changes
	if err := s.repo.Update(ctx, operator); err != nil {
		return nil, fmt.Errorf("failed to update operator: %w", err)
	}

	return operator, nil
}

// SetSystemAccount sets or updates the system account for an operator
func (s *OperatorService) SetSystemAccount(ctx context.Context, operatorID uuid.UUID, systemAccountPubKey string) (*entities.Operator, error) {
	// Get existing operator
	operator, err := s.repo.GetByID(ctx, operatorID)
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
	regenerateJWT := true
	if err == nil && existingClaims.SystemAccount == systemAccountPubKey {
		// JWT already has correct system account, don't regenerate
		regenerateJWT = false
	}

	// Regenerate JWT with new system account only if needed
	if regenerateJWT {
		newJWT, err := s.jwtService.GenerateOperatorJWT(ctx, operator)
		if err != nil {
			return nil, fmt.Errorf("failed to regenerate operator JWT: %w", err)
		}
		operator.JWT = newJWT
	}

	// Save changes
	if err := s.repo.Update(ctx, operator); err != nil {
		return nil, fmt.Errorf("failed to update operator: %w", err)
	}

	return operator, nil
}

// DeleteOperator deletes an operator and all associated data (manual cascade)
func (s *OperatorService) DeleteOperator(ctx context.Context, id uuid.UUID) error {
	// Check if operator exists
	_, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Get all accounts for this operator
	accounts, err := s.accountRepo.ListByOperator(ctx, id, repositories.ListOptions{Limit: 10000})
	if err != nil {
		return fmt.Errorf("failed to list accounts for deletion: %w", err)
	}

	// Delete all accounts (and their associated users/signing keys)
	for _, account := range accounts {
		// Get all users for this account
		users, err := s.userRepo.ListByAccount(ctx, account.ID, repositories.ListOptions{Limit: 10000})
		if err != nil {
			return fmt.Errorf("failed to list users for account %s: %w", account.ID, err)
		}

		// Delete all users
		for _, user := range users {
			if err := s.userRepo.Delete(ctx, user.ID); err != nil {
				return fmt.Errorf("failed to delete user %s: %w", user.ID, err)
			}
		}

		// Delete account (this should also cascade to signing keys if FK works, but we're being explicit)
		if err := s.accountRepo.Delete(ctx, account.ID); err != nil {
			return fmt.Errorf("failed to delete account %s: %w", account.ID, err)
		}
	}

	// Finally delete the operator
	return s.repo.Delete(ctx, id)
}

// GenerateInclude generates a NATS server configuration with operator JWT and preloaded system account
func (s *OperatorService) GenerateInclude(ctx context.Context, id uuid.UUID) (string, error) {
	// Get operator
	operator, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return "", err
	}

	// Check if operator has system account configured
	if operator.SystemAccountPubKey == "" {
		return "", fmt.Errorf("operator does not have a system account configured")
	}

	// Get all accounts for this operator and find the system account
	accounts, err := s.accountRepo.ListByOperator(ctx, operator.ID, repositories.ListOptions{Limit: 1000})
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
