package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// ClusterListFilter holds filter + pagination parameters for ClusterRepository.ListPage.
type ClusterListFilter struct {
	Limit        int
	Cursor       string
	NameLike     string
	OperatorID   *uuid.UUID
	Healthy      *bool
	CreatedSince *time.Time
	CreatedUntil *time.Time
}

// ClusterRepository defines the interface for cluster persistence
type ClusterRepository interface {
	// Create creates a new cluster
	Create(ctx context.Context, cluster *entities.Cluster) error

	// GetByID retrieves a cluster by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Cluster, error)

	// GetByName retrieves a cluster by name
	GetByName(ctx context.Context, name string) (*entities.Cluster, error)

	// List retrieves all clusters with pagination (legacy — kept for internal
	// sweepers: cluster.health.sweep enumerates with Limit:1000, etc.).
	List(ctx context.Context, opts ListOptions) ([]*entities.Cluster, error)

	// ListByOperator retrieves clusters for a specific operator (legacy).
	ListByOperator(ctx context.Context, operatorID uuid.UUID, opts ListOptions) ([]*entities.Cluster, error)

	// ListPage returns one keyset-paginated page of clusters visible under
	// scope. Order: (created_at DESC, id DESC).
	ListPage(ctx context.Context, scope authz.Scope, filter ClusterListFilter) ([]*entities.Cluster, string, error)

	// Update updates an existing cluster
	Update(ctx context.Context, cluster *entities.Cluster) error

	// Delete deletes a cluster by ID
	Delete(ctx context.Context, id uuid.UUID) error

	// Search returns clusters whose name, description, or server_urls JSON
	// list contain the (case-insensitive) substring `q`. Up to `limit` rows.
	// Used by P11 global search.
	Search(ctx context.Context, q string, limit int) ([]*entities.Cluster, error)
}
