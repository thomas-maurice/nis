package sql

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// ScopedSigningKeyRepo implements repositories.ScopedSigningKeyRepository using GORM
type ScopedSigningKeyRepo struct {
	db *gorm.DB
}

// NewScopedSigningKeyRepo creates a new scoped signing key repository
func NewScopedSigningKeyRepo(db *gorm.DB) *ScopedSigningKeyRepo {
	return &ScopedSigningKeyRepo{db: db}
}

// Create creates a new scoped signing key
func (r *ScopedSigningKeyRepo) Create(ctx context.Context, key *entities.ScopedSigningKey) error {
	model := ScopedSigningKeyModelFromEntity(key)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create scoped signing key: %w", err)
	}

	return nil
}

// GetByID retrieves a scoped signing key by ID
func (r *ScopedSigningKeyRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.ScopedSigningKey, error) {
	var model ScopedSigningKeyModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get scoped signing key: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves a scoped signing key by name within an account
func (r *ScopedSigningKeyRepo) GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.ScopedSigningKey, error) {
	var model ScopedSigningKeyModel

	err := r.db.WithContext(ctx).First(&model, "account_id = ? AND name = ?", accountID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get scoped signing key by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves a scoped signing key by its NATS public key
func (r *ScopedSigningKeyRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.ScopedSigningKey, error) {
	var model ScopedSigningKeyModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get scoped signing key by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all scoped signing keys with pagination
func (r *ScopedSigningKeyRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	var models []ScopedSigningKeyModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list scoped signing keys: %w", err)
	}

	keys := make([]*entities.ScopedSigningKey, len(models))
	for i, model := range models {
		keys[i] = model.ToEntity()
	}

	return keys, nil
}

// ListByAccount retrieves scoped signing keys for a specific account
func (r *ScopedSigningKeyRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.ScopedSigningKey, error) {
	var models []ScopedSigningKeyModel

	query := r.db.WithContext(ctx).Where("account_id = ?", accountID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list scoped signing keys by account: %w", err)
	}

	keys := make([]*entities.ScopedSigningKey, len(models))
	for i, model := range models {
		keys[i] = model.ToEntity()
	}

	return keys, nil
}

// Update updates an existing scoped signing key
func (r *ScopedSigningKeyRepo) Update(ctx context.Context, key *entities.ScopedSigningKey) error {
	model := ScopedSigningKeyModelFromEntity(key)

	result := r.db.WithContext(ctx).Model(&ScopedSigningKeyModel{}).
		Where("id = ?", model.ID).
		Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update scoped signing key: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes a scoped signing key by ID
func (r *ScopedSigningKeyRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&ScopedSigningKeyModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete scoped signing key: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
