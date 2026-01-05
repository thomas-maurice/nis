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

// AccountRepo implements repositories.AccountRepository using GORM
type AccountRepo struct {
	db *gorm.DB
}

// NewAccountRepo creates a new account repository
func NewAccountRepo(db *gorm.DB) *AccountRepo {
	return &AccountRepo{db: db}
}

// Create creates a new account
func (r *AccountRepo) Create(ctx context.Context, account *entities.Account) error {
	model := AccountModelFromEntity(account)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create account: %w", err)
	}

	return nil
}

// GetByID retrieves an account by ID
func (r *AccountRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Account, error) {
	var model AccountModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves an account by name within an operator
func (r *AccountRepo) GetByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Account, error) {
	var model AccountModel

	err := r.db.WithContext(ctx).First(&model, "operator_id = ? AND name = ?", operatorID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get account by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves an account by its NATS public key
func (r *AccountRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.Account, error) {
	var model AccountModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get account by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all accounts with pagination
func (r *AccountRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Account, error) {
	var models []AccountModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	accounts := make([]*entities.Account, len(models))
	for i, model := range models {
		accounts[i] = model.ToEntity()
	}

	return accounts, nil
}

// ListByOperator retrieves accounts for a specific operator
func (r *AccountRepo) ListByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Account, error) {
	var models []AccountModel

	query := r.db.WithContext(ctx).Where("operator_id = ?", operatorID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list accounts by operator: %w", err)
	}

	accounts := make([]*entities.Account, len(models))
	for i, model := range models {
		accounts[i] = model.ToEntity()
	}

	return accounts, nil
}

// Update updates an existing account
func (r *AccountRepo) Update(ctx context.Context, account *entities.Account) error {
	model := AccountModelFromEntity(account)

	result := r.db.WithContext(ctx).Model(&AccountModel{}).
		Where("id = ?", model.ID).
		Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update account: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes an account by ID
func (r *AccountRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&AccountModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete account: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
