package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// ClusterRepository defines the interface for cluster persistence
type ClusterRepository interface {
	// Create creates a new cluster
	Create(ctx context.Context, cluster *entities.Cluster) error

	// GetByID retrieves a cluster by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Cluster, error)

	// GetByName retrieves a cluster by name
	GetByName(ctx context.Context, name string) (*entities.Cluster, error)

	// List retrieves all clusters with pagination
	List(ctx context.Context, opts ListOptions) ([]*entities.Cluster, error)

	// ListByOperator retrieves clusters for a specific operator
	ListByOperator(ctx context.Context, operatorID uuid.UUID, opts ListOptions) ([]*entities.Cluster, error)

	// Update updates an existing cluster
	Update(ctx context.Context, cluster *entities.Cluster) error

	// Delete deletes a cluster by ID
	Delete(ctx context.Context, id uuid.UUID) error
}
