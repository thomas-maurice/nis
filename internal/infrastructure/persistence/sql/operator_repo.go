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

// OperatorRepo implements repositories.OperatorRepository using GORM
type OperatorRepo struct {
	db *gorm.DB
}

// NewOperatorRepo creates a new operator repository
func NewOperatorRepo(db *gorm.DB) *OperatorRepo {
	return &OperatorRepo{db: db}
}

// Create creates a new operator
func (r *OperatorRepo) Create(ctx context.Context, operator *entities.Operator) error {
	model := OperatorModelFromEntity(operator)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create operator: %w", err)
	}

	return nil
}

// GetByID retrieves an operator by ID
func (r *OperatorRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Operator, error) {
	var model OperatorModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get operator: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves an operator by name
func (r *OperatorRepo) GetByName(ctx context.Context, name string) (*entities.Operator, error) {
	var model OperatorModel

	err := r.db.WithContext(ctx).First(&model, "name = ?", name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get operator by name: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByPublicKey retrieves an operator by its NATS public key
func (r *OperatorRepo) GetByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error) {
	var model OperatorModel

	err := r.db.WithContext(ctx).First(&model, "public_key = ?", publicKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get operator by public key: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves operators with pagination
func (r *OperatorRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Operator, error) {
	var models []OperatorModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list operators: %w", err)
	}

	operators := make([]*entities.Operator, len(models))
	for i, model := range models {
		operators[i] = model.ToEntity()
	}

	return operators, nil
}

// Update updates an existing operator
func (r *OperatorRepo) Update(ctx context.Context, operator *entities.Operator) error {
	model := OperatorModelFromEntity(operator)

	result := r.db.WithContext(ctx).Model(&OperatorModel{}).
		Where("id = ?", model.ID).
		Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update operator: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes an operator by ID
func (r *OperatorRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&OperatorModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete operator: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
