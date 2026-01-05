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

// ClusterRepo implements repositories.ClusterRepository using GORM
type ClusterRepo struct {
	db *gorm.DB
}

// NewClusterRepo creates a new cluster repository
func NewClusterRepo(db *gorm.DB) *ClusterRepo {
	return &ClusterRepo{db: db}
}

// Create creates a new cluster
func (r *ClusterRepo) Create(ctx context.Context, cluster *entities.Cluster) error {
	model := ClusterModelFromEntity(cluster)

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	return nil
}

// GetByID retrieves a cluster by ID
func (r *ClusterRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Cluster, error) {
	var model ClusterModel

	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	return model.ToEntity(), nil
}

// GetByName retrieves a cluster by name
func (r *ClusterRepo) GetByName(ctx context.Context, name string) (*entities.Cluster, error) {
	var model ClusterModel

	err := r.db.WithContext(ctx).First(&model, "name = ?", name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get cluster by name: %w", err)
	}

	return model.ToEntity(), nil
}

// List retrieves all clusters with pagination
func (r *ClusterRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Cluster, error) {
	var models []ClusterModel

	query := r.db.WithContext(ctx)

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	clusters := make([]*entities.Cluster, len(models))
	for i, model := range models {
		clusters[i] = model.ToEntity()
	}

	return clusters, nil
}

// ListByOperator retrieves clusters for a specific operator
func (r *ClusterRepo) ListByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Cluster, error) {
	var models []ClusterModel

	query := r.db.WithContext(ctx).Where("operator_id = ?", operatorID.String())

	if opts.Limit > 0 {
		query = query.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		query = query.Offset(opts.Offset)
	}

	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list clusters by operator: %w", err)
	}

	clusters := make([]*entities.Cluster, len(models))
	for i, model := range models {
		clusters[i] = model.ToEntity()
	}

	return clusters, nil
}

// Update updates an existing cluster
func (r *ClusterRepo) Update(ctx context.Context, cluster *entities.Cluster) error {
	model := ClusterModelFromEntity(cluster)

	result := r.db.WithContext(ctx).Model(&ClusterModel{}).
		Where("id = ?", model.ID).
		Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update cluster: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Delete deletes a cluster by ID
func (r *ClusterRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&ClusterModel{}, "id = ?", id.String())

	if result.Error != nil {
		return fmt.Errorf("failed to delete cluster: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}
