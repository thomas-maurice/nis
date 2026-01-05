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

// UserRepo implements repositories.UserRepository using GORM
type UserRepo struct {
	db *gorm.DB
}

// NewUserRepo creates a new user repository
func NewUserRepo(db *gorm.DB) *UserRepo {
	return &UserRepo{db: db}
}

// Create creates a new user
func (r *UserRepo) Create(ctx context.Context, user *entities.User) error {
	model := UserModelFromEntity(user)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	return nil
}

// GetByID retrieves a user by ID
func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	var model UserModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves a user by name within an account
func (r *UserRepo) GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.User, error) {
	var model UserModel

	err := r.db.WithContext(ctx).First(&model, "account_id = ? AND name = ?", accountID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves a user by its NATS public key
func (r *UserRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.User, error) {
	var model UserModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all users with pagination
func (r *UserRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.User, error) {
	var models []UserModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	users := make([]*entities.User, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// ListByAccount retrieves users for a specific account
func (r *UserRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	var models []UserModel

	query := r.db.WithContext(ctx).Where("account_id = ?", accountID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list users by account: %w", err)
	}

	users := make([]*entities.User, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// ListByScopedSigningKey retrieves users signed by a specific scoped signing key
func (r *UserRepo) ListByScopedSigningKey(ctx context.Context, scopedKeyID uuid.UUID, opts repositories.ListOptions) ([]*entities.User, error) {
	var models []UserModel

	query := r.db.WithContext(ctx).Where("scoped_signing_key_id = ?", scopedKeyID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list users by scoped signing key: %w", err)
	}

	users := make([]*entities.User, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// Update updates an existing user
func (r *UserRepo) Update(ctx context.Context, user *entities.User) error {
	model := UserModelFromEntity(user)

	result := r.db.WithContext(ctx).Model(&UserModel{}).
		Where("id = ?", model.ID).
		Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes a user by ID
func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&UserModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
