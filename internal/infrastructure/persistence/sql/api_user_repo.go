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

// APIUserRepo implements repositories.APIUserRepository using GORM
type APIUserRepo struct {
	db *gorm.DB
}

// NewAPIUserRepo creates a new API user repository
func NewAPIUserRepo(db *gorm.DB) *APIUserRepo {
	return &APIUserRepo{db: db}
}

// Create creates a new API user
func (r *APIUserRepo) Create(ctx context.Context, user *entities.APIUser) error {
	model := APIUserModelFromEntity(user)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create API user: %w", err)
	}

	return nil
}

// GetByID retrieves an API user by ID
func (r *APIUserRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.APIUser, error) {
	var model APIUserModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API user: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByUsername retrieves an API user by username
func (r *APIUserRepo) GetByUsername(ctx context.Context, username string) (*entities.APIUser, error) {
	var model APIUserModel

	err := r.db.WithContext(ctx).First(&model, "username = ?", username).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API user by username: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all API users with pagination
func (r *APIUserRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.APIUser, error) {
	var models []APIUserModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list API users: %w", err)
	}

	users := make([]*entities.APIUser, len(models))
	for i, model := range models {
		users[i] = model.ToEntity()
	}

	return users, nil
}

// Update updates an existing API user
func (r *APIUserRepo) Update(ctx context.Context, user *entities.APIUser) error {
	model := APIUserModelFromEntity(user)

	result := r.db.WithContext(ctx).Model(&APIUserModel{}).
		Where("id = ?", model.ID).
		Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update API user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes an API user by ID
func (r *APIUserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&APIUserModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete API user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
