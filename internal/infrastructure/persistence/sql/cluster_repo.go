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

// normalizeClusterTimes coerces all time fields to UTC before write. Defensive
// belt-and-suspenders for the rule documented in internal/clock: every value
// destined for storage must be UTC. clock.Now() guarantees this at the source,
// but a time.Time constructed from external input (parsed string, NATS frame)
// won't have gone through clock.Now(); this helper traps that case.
func normalizeClusterTimes(m *ClusterModel) {
	m.CreatedAt = m.CreatedAt.UTC()
	m.UpdatedAt = m.UpdatedAt.UTC()
	if m.LastHealthCheck != nil {
		u := m.LastHealthCheck.UTC()
		m.LastHealthCheck = &u
	}
}

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
	normalizeClusterTimes(model)

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
	normalizeClusterTimes(model)

	result := r.db.WithContext(ctx).Model(&ClusterModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)

	if result.Error != nil {
		return fmt.Errorf("failed to update cluster: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}

	return nil
}

// Search returns clusters whose name, description, or server_urls JSON list
// contain `q` (case-insensitive). Bounded by `limit`. server_urls is stored
// as a JSON array of URL strings (`serializer:json` TEXT) — raw LIKE matches
// hostname / port substrings as written. See OperatorRepo.Search for the
// dialect-uniform LOWER(LIKE) rationale.
func (r *ClusterRepo) Search(ctx context.Context, q string, limit int) ([]*entities.Cluster, error) {
	if limit <= 0 {
		return []*entities.Cluster{}, nil
	}
	pat := "%" + escapeLikeParam(q) + "%"
	var models []ClusterModel
	err := r.db.WithContext(ctx).
		Where(`LOWER(name) LIKE LOWER(?) OR LOWER(description) LIKE LOWER(?) OR LOWER(server_urls) LIKE LOWER(?)`, pat, pat, pat).
		Order("name ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to search clusters: %w", err)
	}
	out := make([]*entities.Cluster, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
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
