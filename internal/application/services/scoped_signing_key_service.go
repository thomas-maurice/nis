package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
)

// ScopedSigningKeyService provides business logic for scoped signing key management.
//
// Every mutating method (Create / Update / Delete) re-signs the parent account's JWT
// so the NATS resolver sees the up-to-date set of scoped signers. Without that, NATS
// rejects users signed by newly-created or just-modified scoped keys as
// "Authorization Violation" — the bug previously labelled E1 in PROPOSALS.md.
type ScopedSigningKeyService struct {
	repo         repositories.ScopedSigningKeyRepository
	accountRepo  repositories.AccountRepository
	operatorRepo repositories.OperatorRepository
	jwtService   *JWTService
	encryptor    encryption.Encryptor
}

// NewScopedSigningKeyService creates a new scoped signing key service
func NewScopedSigningKeyService(
	repo repositories.ScopedSigningKeyRepository,
	accountRepo repositories.AccountRepository,
	operatorRepo repositories.OperatorRepository,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *ScopedSigningKeyService {
	return &ScopedSigningKeyService{
		repo:         repo,
		accountRepo:  accountRepo,
		operatorRepo: operatorRepo,
		jwtService:   jwtService,
		encryptor:    encryptor,
	}
}

// regenerateAccountJWT re-signs the account's JWT to reflect the current set of
// scoped signing keys. Call this after every Create/Update/Delete on a scoped key
// so the resolver eventually trusts (or stops trusting) the key as a signer.
// SyncCluster pushes the regenerated JWT to NATS the next time it runs.
func (s *ScopedSigningKeyService) regenerateAccountJWT(ctx context.Context, accountID uuid.UUID) error {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	operator, err := s.operatorRepo.GetByID(ctx, account.OperatorID)
	if err != nil {
		return fmt.Errorf("failed to get operator: %w", err)
	}
	scopedKeys, err := s.repo.ListByAccount(ctx, accountID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return fmt.Errorf("failed to list scoped signing keys: %w", err)
	}
	newJWT, err := s.jwtService.GenerateAccountJWT(ctx, account, operator, scopedKeys)
	if err != nil {
		return fmt.Errorf("failed to regenerate account JWT: %w", err)
	}
	account.JWT = newJWT
	account.UpdatedAt = time.Now()
	if err := s.accountRepo.Update(ctx, account); err != nil {
		return fmt.Errorf("failed to persist regenerated account JWT: %w", err)
	}
	return nil
}

// CreateScopedSigningKeyRequest contains the data needed to create a scoped signing key
type CreateScopedSigningKeyRequest struct {
	AccountID       uuid.UUID
	Name            string
	Description     string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs int
	ResponseTTL     time.Duration
}

// CreateScopedSigningKey creates a new scoped signing key with generated keys
func (s *ScopedSigningKeyService) CreateScopedSigningKey(ctx context.Context, req CreateScopedSigningKeyRequest) (*entities.ScopedSigningKey, error) {
	// Validate request
	if req.Name == "" {
		return nil, fmt.Errorf("scoped signing key name is required")
	}

	// Get account to verify it exists
	_, err := s.accountRepo.GetByID(ctx, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	// Check if scoped key with this name already exists for this account
	existing, err := s.repo.GetByName(ctx, req.AccountID, req.Name)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, fmt.Errorf("failed to check existing scoped signing key: %w", err)
	}
	if existing != nil {
		return nil, repositories.ErrAlreadyExists
	}

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

	// Create scoped signing key entity
	scopedKey := &entities.ScopedSigningKey{
		ID:              uuid.New(),
		AccountID:       req.AccountID,
		Name:            req.Name,
		Description:     req.Description,
		EncryptedSeed:   encryptedSeed,
		PublicKey:       pubKey,
		PubAllow:        req.PubAllow,
		PubDeny:         req.PubDeny,
		SubAllow:        req.SubAllow,
		SubDeny:         req.SubDeny,
		ResponseMaxMsgs: req.ResponseMaxMsgs,
		ResponseTTL:     req.ResponseTTL,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Save to repository
	if err := s.repo.Create(ctx, scopedKey); err != nil {
		return nil, fmt.Errorf("failed to create scoped signing key: %w", err)
	}

	// Re-sign the account JWT so NATS recognises the new scoped signer.
	if err := s.regenerateAccountJWT(ctx, req.AccountID); err != nil {
		// Best-effort rollback: delete the just-created key so the DB matches the JWT.
		if delErr := s.repo.Delete(ctx, scopedKey.ID); delErr != nil {
			logging.LogFromContext(ctx).Error("failed to roll back scoped signing key after JWT regen failure",
				"scoped_key_id", scopedKey.ID, "error", delErr)
		}
		return nil, err
	}

	return scopedKey, nil
}

// GetScopedSigningKey retrieves a scoped signing key by ID
func (s *ScopedSigningKeyService) GetScopedSigningKey(ctx context.Context, id uuid.UUID) (*entities.ScopedSigningKey, error) {
	return s.repo.GetByID(ctx, id)
}

// GetScopedSigningKeyByName retrieves a scoped signing key by account ID and name
func (s *ScopedSigningKeyService) GetScopedSigningKeyByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.ScopedSigningKey, error) {
	return s.repo.GetByName(ctx, accountID, name)
}

// GetScopedSigningKeyByPublicKey retrieves a scoped signing key by public key
func (s *ScopedSigningKeyService) GetScopedSigningKeyByPublicKey(ctx context.Context, publicKey string) (*entities.ScopedSigningKey, error) {
	return s.repo.GetByPublicKey(ctx, publicKey)
}

// ListScopedSigningKeysByAccount retrieves all scoped signing keys for an account with pagination
func (s *ScopedSigningKeyService) ListScopedSigningKeysByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	return s.repo.ListByAccount(ctx, accountID, opts)
}

// ListAllScopedSigningKeys retrieves all scoped signing keys across all accounts with pagination
func (s *ScopedSigningKeyService) ListAllScopedSigningKeys(ctx context.Context, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	return s.repo.List(ctx, opts)
}

// UpdateScopedSigningKeyRequest contains the fields that can be updated
type UpdateScopedSigningKeyRequest struct {
	Name            *string
	Description     *string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs *int
	ResponseTTL     *time.Duration
}

// UpdateScopedSigningKey updates a scoped signing key's configuration
// Note: Updating permissions will require regenerating user JWTs that use this key
func (s *ScopedSigningKeyService) UpdateScopedSigningKey(ctx context.Context, id uuid.UUID, req UpdateScopedSigningKeyRequest) (*entities.ScopedSigningKey, error) {
	// Get existing scoped signing key
	scopedKey, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Update fields if provided
	updated := false
	if req.Name != nil && *req.Name != scopedKey.Name {
		// Check if new name is already taken for this account
		existing, err := s.repo.GetByName(ctx, scopedKey.AccountID, *req.Name)
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return nil, fmt.Errorf("failed to check existing scoped signing key: %w", err)
		}
		if existing != nil && existing.ID != id {
			return nil, repositories.ErrAlreadyExists
		}
		scopedKey.Name = *req.Name
		updated = true
	}

	if req.Description != nil && *req.Description != scopedKey.Description {
		scopedKey.Description = *req.Description
		updated = true
	}

	// Update permission arrays if provided (even if empty)
	if req.PubAllow != nil {
		scopedKey.PubAllow = req.PubAllow
		updated = true
	}
	if req.PubDeny != nil {
		scopedKey.PubDeny = req.PubDeny
		updated = true
	}
	if req.SubAllow != nil {
		scopedKey.SubAllow = req.SubAllow
		updated = true
	}
	if req.SubDeny != nil {
		scopedKey.SubDeny = req.SubDeny
		updated = true
	}

	if req.ResponseMaxMsgs != nil && *req.ResponseMaxMsgs != scopedKey.ResponseMaxMsgs {
		scopedKey.ResponseMaxMsgs = *req.ResponseMaxMsgs
		updated = true
	}

	if req.ResponseTTL != nil && *req.ResponseTTL != scopedKey.ResponseTTL {
		scopedKey.ResponseTTL = *req.ResponseTTL
		updated = true
	}

	if !updated {
		return scopedKey, nil
	}

	scopedKey.UpdatedAt = time.Now()

	// Save changes
	if err := s.repo.Update(ctx, scopedKey); err != nil {
		return nil, fmt.Errorf("failed to update scoped signing key: %w", err)
	}

	// Re-sign the account JWT so the updated template permissions take effect on NATS.
	if err := s.regenerateAccountJWT(ctx, scopedKey.AccountID); err != nil {
		return nil, err
	}

	return scopedKey, nil
}

// DeleteScopedSigningKey deletes a scoped signing key
// Note: This will cascade to users signed by this key (foreign key constraint)
func (s *ScopedSigningKeyService) DeleteScopedSigningKey(ctx context.Context, id uuid.UUID) error {
	// Need the accountID for the post-delete JWT regen.
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	// Re-sign the account JWT so NATS stops trusting the deleted key as a signer.
	return s.regenerateAccountJWT(ctx, existing.AccountID)
}
