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

// AccountService provides business logic for account management
type AccountService struct {
	repo             repositories.AccountRepository
	operatorRepo     repositories.OperatorRepository
	scopedKeyRepo    repositories.ScopedSigningKeyRepository
	jwtService       *JWTService
	encryptor        encryption.Encryptor
}

// NewAccountService creates a new account service
func NewAccountService(
	repo repositories.AccountRepository,
	operatorRepo repositories.OperatorRepository,
	scopedKeyRepo repositories.ScopedSigningKeyRepository,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *AccountService {
	return &AccountService{
		repo:          repo,
		operatorRepo:  operatorRepo,
		scopedKeyRepo: scopedKeyRepo,
		jwtService:    jwtService,
		encryptor:     encryptor,
	}
}

// CreateAccountRequest contains the data needed to create an account
type CreateAccountRequest struct {
	OperatorID            uuid.UUID
	Name                  string
	Description           string
	JetStreamEnabled      bool
	JetStreamMaxMemory    int64
	JetStreamMaxStorage   int64
	JetStreamMaxStreams   int64
	JetStreamMaxConsumers int64
}

// CreateAccount creates a new account with generated keys and JWT
func (s *AccountService) CreateAccount(ctx context.Context, req CreateAccountRequest) (*entities.Account, error) {
	// Validate request
	if req.Name == "" {
		return nil, fmt.Errorf("account name is required")
	}

	// Get operator to sign the account JWT
	operator, err := s.operatorRepo.GetByID(ctx, req.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	// Check if account with this name already exists for this operator
	existing, err := s.repo.GetByName(ctx, req.OperatorID, req.Name)
	if err != nil && err != repositories.ErrNotFound {
		return nil, fmt.Errorf("failed to check existing account: %w", err)
	}
	if existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

	// Generate account NKey pair
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	if err != nil {
		return nil, fmt.Errorf("failed to generate account keys: %w", err)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt account seed: %w", err)
	}

	// Create account entity
	account := &entities.Account{
		ID:                    uuid.New(),
		OperatorID:            req.OperatorID,
		Name:                  req.Name,
		Description:           req.Description,
		EncryptedSeed:         encryptedSeed,
		PublicKey:             pubKey,
		JetStreamEnabled:      req.JetStreamEnabled,
		JetStreamMaxMemory:    req.JetStreamMaxMemory,
		JetStreamMaxStorage:   req.JetStreamMaxStorage,
		JetStreamMaxStreams:   req.JetStreamMaxStreams,
		JetStreamMaxConsumers: req.JetStreamMaxConsumers,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	// Generate JWT signed by operator
	jwt, err := s.jwtService.GenerateAccountJWT(ctx, account, operator)
	if err != nil {
		return nil, fmt.Errorf("failed to generate account JWT: %w", err)
	}
	account.JWT = jwt

	// Save to repository
	if err := s.repo.Create(ctx, account); err != nil {
		return nil, fmt.Errorf("failed to create account: %w", err)
	}

	// Create a default scoped signing key with unlimited permissions (mandatory)
	defaultKey, err := s.createDefaultScopedSigningKey(ctx, account.ID)
	if err != nil {
		// Rollback account creation if signing key creation fails
		if deleteErr := s.repo.Delete(ctx, account.ID); deleteErr != nil {
			fmt.Printf("Error: failed to rollback account creation after signing key failure: %v\n", deleteErr)
		}
		return nil, fmt.Errorf("failed to create default scoped signing key for account: %w", err)
	}

	fmt.Printf("Created account '%s' with default scoped signing key '%s'\n", account.Name, defaultKey.Name)

	return account, nil
}

// createDefaultScopedSigningKey creates a default scoped signing key with unlimited account permissions
func (s *AccountService) createDefaultScopedSigningKey(ctx context.Context, accountID uuid.UUID) (*entities.ScopedSigningKey, error) {
	// Generate account NKey pair (scoped signing keys use account key prefix)
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	if err != nil {
		return nil, fmt.Errorf("failed to generate scoped signing key: %w", err)
	}

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt scoped signing key seed: %w", err)
	}

	// Create scoped signing key entity with unlimited permissions
	scopedKey := &entities.ScopedSigningKey{
		ID:              uuid.New(),
		AccountID:       accountID,
		Name:            "default",
		Description:     "Default scoped signing key with unlimited account permissions",
		EncryptedSeed:   encryptedSeed,
		PublicKey:       pubKey,
		PubAllow:        []string{},          // Empty = allow all
		PubDeny:         []string{},
		SubAllow:        []string{},          // Empty = allow all
		SubDeny:         []string{},
		ResponseMaxMsgs: 0,                   // 0 = unlimited
		ResponseTTL:     0,                   // 0 = unlimited
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Save to repository
	if err := s.scopedKeyRepo.Create(ctx, scopedKey); err != nil {
		return nil, fmt.Errorf("failed to create default scoped signing key: %w", err)
	}

	return scopedKey, nil
}

// GetAccount retrieves an account by ID
func (s *AccountService) GetAccount(ctx context.Context, id uuid.UUID) (*entities.Account, error) {
	return s.repo.GetByID(ctx, id)
}

// GetAccountByName retrieves an account by operator ID and name
func (s *AccountService) GetAccountByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Account, error) {
	return s.repo.GetByName(ctx, operatorID, name)
}

// GetAccountByPublicKey retrieves an account by public key
func (s *AccountService) GetAccountByPublicKey(ctx context.Context, publicKey string) (*entities.Account, error) {
	return s.repo.GetByPublicKey(ctx, publicKey)
}

// ListAccountsByOperator retrieves all accounts for an operator with pagination
func (s *AccountService) ListAccountsByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Account, error) {
	return s.repo.ListByOperator(ctx, operatorID, opts)
}

// ListAllAccounts lists all accounts across all operators
func (s *AccountService) ListAllAccounts(ctx context.Context, opts repositories.ListOptions) ([]*entities.Account, error) {
	return s.repo.List(ctx, opts)
}

// UpdateAccountRequest contains the fields that can be updated
type UpdateAccountRequest struct {
	Name        *string
	Description *string
}

// UpdateAccount updates an account's metadata and regenerates JWT
func (s *AccountService) UpdateAccount(ctx context.Context, id uuid.UUID, req UpdateAccountRequest) (*entities.Account, error) {
	// Get existing account
	account, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update fields if provided
	updated := false
	if req.Name != nil && *req.Name != account.Name {
		// Check if new name is already taken for this operator
		existing, err := s.repo.GetByName(ctx, account.OperatorID, *req.Name)
		if err != nil && err != repositories.ErrNotFound {
			return nil, fmt.Errorf("failed to check existing account: %w", err)
		}
		if existing != nil && existing.ID != id {
			return nil, repositories.ErrAlreadyExists
		}
		account.Name = *req.Name
		updated = true
	}

	if req.Description != nil && *req.Description != account.Description {
		account.Description = *req.Description
		updated = true
	}

	if !updated {
		return account, nil
	}

	account.UpdatedAt = time.Now()

	// Get operator to sign the updated JWT
	operator, err := s.operatorRepo.GetByID(ctx, account.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	// Regenerate JWT with updated metadata
	jwt, err := s.jwtService.GenerateAccountJWT(ctx, account, operator)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate account JWT: %w", err)
	}
	account.JWT = jwt

	// Save changes
	if err := s.repo.Update(ctx, account); err != nil {
		return nil, fmt.Errorf("failed to update account: %w", err)
	}

	return account, nil
}

// UpdateJetStreamLimitsRequest contains JetStream configuration
type UpdateJetStreamLimitsRequest struct {
	Enabled      bool
	MaxMemory    int64
	MaxStorage   int64
	MaxStreams   int64
	MaxConsumers int64
}

// UpdateJetStreamLimits updates JetStream limits and regenerates JWT
func (s *AccountService) UpdateJetStreamLimits(ctx context.Context, id uuid.UUID, req UpdateJetStreamLimitsRequest) (*entities.Account, error) {
	// Get existing account
	account, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update JetStream configuration
	account.JetStreamEnabled = req.Enabled
	account.JetStreamMaxMemory = req.MaxMemory
	account.JetStreamMaxStorage = req.MaxStorage
	account.JetStreamMaxStreams = req.MaxStreams
	account.JetStreamMaxConsumers = req.MaxConsumers
	account.UpdatedAt = time.Now()

	// Get operator to sign the updated JWT
	operator, err := s.operatorRepo.GetByID(ctx, account.OperatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	// Regenerate JWT with new JetStream limits
	jwt, err := s.jwtService.GenerateAccountJWT(ctx, account, operator)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate account JWT: %w", err)
	}
	account.JWT = jwt

	// Save changes
	if err := s.repo.Update(ctx, account); err != nil {
		return nil, fmt.Errorf("failed to update account: %w", err)
	}

	return account, nil
}

// DeleteAccount deletes an account and all associated data (cascades to users)
func (s *AccountService) DeleteAccount(ctx context.Context, id uuid.UUID) error {
	// Check if account exists
	account, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Check if this account is a system account for any operator
	operator, err := s.operatorRepo.GetByID(ctx, account.OperatorID)
	if err != nil {
		return fmt.Errorf("failed to get operator: %w", err)
	}

	if operator.SystemAccountPubKey == account.PublicKey {
		return fmt.Errorf("cannot delete system account: this account is designated as the system account for operator '%s'", operator.Name)
	}

	// Delete account (cascades to users and scoped signing keys)
	return s.repo.Delete(ctx, id)
}
