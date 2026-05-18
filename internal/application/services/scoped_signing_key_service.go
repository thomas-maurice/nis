package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nkeys"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ScopedSigningKeyService provides business logic for scoped signing key management.
//
// Every mutating method (Create / Update / Delete) re-signs the parent account's JWT
// so the NATS resolver sees the up-to-date set of scoped signers. Without that, NATS
// rejects users signed by newly-created or just-modified scoped keys as
// "Authorization Violation" — the bug previously labelled E1 in PROPOSALS.md.
//
// The key mutation and the account-JWT regen share a single tx via
// factory.WithTx, so a JWT-regen failure rolls back the key mutation too. That
// prevents the account-JWT-references-missing-key (or missing-JWT-references-existing-key)
// split-brain state that previously required best-effort manual rollback.
type ScopedSigningKeyService struct {
	factory    persistence.RepositoryFactory
	jwtService *JWTService
	encryptor  encryption.Encryptor
}

// NewScopedSigningKeyService creates a new scoped signing key service
func NewScopedSigningKeyService(
	factory persistence.RepositoryFactory,
	jwtService *JWTService,
	encryptor encryption.Encryptor,
) *ScopedSigningKeyService {
	return &ScopedSigningKeyService{
		factory:    factory,
		jwtService: jwtService,
		encryptor:  encryptor,
	}
}

// regenerateAccountJWTTx re-signs the account's JWT to reflect the current set
// of scoped signing keys, using the tx-scoped factory. Call this after every
// Create/Update/Delete on a scoped key inside the same tx so a JWT-regen
// failure rolls back the key mutation too.
func (s *ScopedSigningKeyService) regenerateAccountJWTTx(ctx context.Context, tx persistence.RepositoryFactory, accountID uuid.UUID) error {
	accountRepo := tx.AccountRepository()
	operatorRepo := tx.OperatorRepository()
	scopedKeyRepo := tx.ScopedSigningKeyRepository()

	account, err := accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	operator, err := operatorRepo.GetByID(ctx, account.OperatorID)
	if err != nil {
		return fmt.Errorf("failed to get operator: %w", err)
	}
	scopedKeys, err := scopedKeyRepo.ListByAccount(ctx, accountID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return fmt.Errorf("failed to list scoped signing keys: %w", err)
	}
	revs, err := tx.UserJWTRevocationRepository().ListActiveByAccount(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to list active revocations: %w", err)
	}
	newJWT, err := s.jwtService.GenerateAccountJWT(ctx, account, operator, scopedKeys, revs, operator.AccountJWTTTL)
	if err != nil {
		return fmt.Errorf("failed to regenerate account JWT: %w", err)
	}
	account.JWT = newJWT
	account.UpdatedAt = time.Now()
	if err := accountRepo.Update(ctx, account); err != nil {
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
	if req.Name == "" {
		return nil, fmt.Errorf("scoped signing key name is required")
	}

	var result *entities.ScopedSigningKey
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		scopedKeyRepo := tx.ScopedSigningKeyRepository()
		accountRepo := tx.AccountRepository()

		// Get account to verify it exists and capture OperatorID for event emission
		account, err := accountRepo.GetByID(ctx, req.AccountID)
		if err != nil {
			return fmt.Errorf("failed to get account: %w", err)
		}

		// Check if scoped key with this name already exists for this account
		existing, err := scopedKeyRepo.GetByName(ctx, req.AccountID, req.Name)
		if err != nil && !errors.Is(err, repositories.ErrNotFound) {
			return fmt.Errorf("failed to check existing scoped signing key: %w", err)
		}
		if existing != nil {
			return repositories.ErrAlreadyExists
		}

		// Generate account NKey pair (scoped signing keys use account key prefix)
		seed, pubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
		if err != nil {
			return fmt.Errorf("failed to generate scoped signing key: %w", err)
		}

		// Encrypt the seed
		encryptedSeed, err := s.encryptor.Encrypt(ctx, seed)
		if err != nil {
			return fmt.Errorf("failed to encrypt scoped signing key seed: %w", err)
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
		if err := scopedKeyRepo.Create(ctx, scopedKey); err != nil {
			return fmt.Errorf("failed to create scoped signing key: %w", err)
		}

		// Re-sign the account JWT so NATS recognises the new scoped signer.
		// On failure, WithTx rolls back both the just-persisted key and any
		// account update we made — no manual cleanup needed.
		if err := s.regenerateAccountJWTTx(ctx, tx, req.AccountID); err != nil {
			return err
		}

		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyCreated,
			OperatorID:   &account.OperatorID,
			AccountID:    &scopedKey.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   scopedKey.ID.String(),
			Payload:      map[string]any{"name": scopedKey.Name, "public_key": scopedKey.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.created: %w", err)
		}

		result = scopedKey
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetScopedSigningKey retrieves a scoped signing key by ID
func (s *ScopedSigningKeyService) GetScopedSigningKey(ctx context.Context, id uuid.UUID) (*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().GetByID(ctx, id)
}

// GetScopedSigningKeyByName retrieves a scoped signing key by account ID and name
func (s *ScopedSigningKeyService) GetScopedSigningKeyByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().GetByName(ctx, accountID, name)
}

// GetScopedSigningKeyByPublicKey retrieves a scoped signing key by public key
func (s *ScopedSigningKeyService) GetScopedSigningKeyByPublicKey(ctx context.Context, publicKey string) (*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().GetByPublicKey(ctx, publicKey)
}

// ListScopedSigningKeysByAccount retrieves all scoped signing keys for an account with pagination
func (s *ScopedSigningKeyService) ListScopedSigningKeysByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().ListByAccount(ctx, accountID, opts)
}

// ListAllScopedSigningKeys retrieves all scoped signing keys across all accounts with pagination
func (s *ScopedSigningKeyService) ListAllScopedSigningKeys(ctx context.Context, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	return s.factory.ScopedSigningKeyRepository().List(ctx, opts)
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

// UpdateScopedSigningKey updates a scoped signing key's configuration.
// Note: Updating permissions will require regenerating user JWTs that use this key.
func (s *ScopedSigningKeyService) UpdateScopedSigningKey(ctx context.Context, id uuid.UUID, req UpdateScopedSigningKeyRequest) (*entities.ScopedSigningKey, error) {
	var result *entities.ScopedSigningKey
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		scopedKeyRepo := tx.ScopedSigningKeyRepository()

		// Get existing scoped signing key
		scopedKey, err := scopedKeyRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		// Update fields if provided
		updated := false
		if req.Name != nil && *req.Name != scopedKey.Name {
			// Check if new name is already taken for this account
			existing, err := scopedKeyRepo.GetByName(ctx, scopedKey.AccountID, *req.Name)
			if err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return fmt.Errorf("failed to check existing scoped signing key: %w", err)
			}
			if existing != nil && existing.ID != id {
				return repositories.ErrAlreadyExists
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
			result = scopedKey
			return nil
		}

		scopedKey.UpdatedAt = time.Now()

		// Save changes
		if err := scopedKeyRepo.Update(ctx, scopedKey); err != nil {
			return fmt.Errorf("failed to update scoped signing key: %w", err)
		}

		// Re-sign the account JWT so the updated template permissions take
		// effect on NATS. WithTx rolls back the scoped-key update on failure.
		if err := s.regenerateAccountJWTTx(ctx, tx, scopedKey.AccountID); err != nil {
			return err
		}

		account, err := tx.AccountRepository().GetByID(ctx, scopedKey.AccountID)
		if err != nil {
			return fmt.Errorf("emit scoped_key.updated: lookup account: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyUpdated,
			OperatorID:   &account.OperatorID,
			AccountID:    &scopedKey.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   scopedKey.ID.String(),
			Payload:      map[string]any{"name": scopedKey.Name},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.updated: %w", err)
		}

		result = scopedKey
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteScopedSigningKey deletes a scoped signing key.
// Note: This will cascade to users signed by this key (foreign key constraint).
func (s *ScopedSigningKeyService) DeleteScopedSigningKey(ctx context.Context, id uuid.UUID) error {
	return s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		scopedKeyRepo := tx.ScopedSigningKeyRepository()

		// Need the accountID for the post-delete JWT regen.
		existing, err := scopedKeyRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}

		if err := scopedKeyRepo.Delete(ctx, id); err != nil {
			return err
		}

		// Re-sign the account JWT so NATS stops trusting the deleted key as a signer.
		// On failure, WithTx rolls back the delete itself — the key reappears.
		if err := s.regenerateAccountJWTTx(ctx, tx, existing.AccountID); err != nil {
			return err
		}

		account, err := tx.AccountRepository().GetByID(ctx, existing.AccountID)
		if err != nil {
			return fmt.Errorf("emit scoped_key.deleted: lookup account: %w", err)
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeScopedKeyDeleted,
			OperatorID:   &account.OperatorID,
			AccountID:    &existing.AccountID,
			ResourceType: "scoped_key",
			ResourceID:   existing.ID.String(),
			Payload:      map[string]any{"name": existing.Name, "public_key": existing.PublicKey},
		}); err != nil {
			return fmt.Errorf("emit scoped_key.deleted: %w", err)
		}

		return nil
	})
}
