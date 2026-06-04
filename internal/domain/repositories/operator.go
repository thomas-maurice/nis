package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// OperatorListFilter holds the filtering and pagination parameters for ListPage.
type OperatorListFilter struct {
	// Limit is the maximum number of rows to return (0 → default 50; clamped to max 200).
	Limit int
	// Cursor is the opaque pagination cursor ("" → first page).
	Cursor string
	// NameLike is a case-insensitive substring match on name ("" disables).
	NameLike string
	// CreatedSince is an inclusive lower bound on created_at (nil disables).
	CreatedSince *time.Time
	// CreatedUntil is an exclusive upper bound on created_at (nil disables).
	CreatedUntil *time.Time
}

// OperatorRepository defines the interface for operator persistence
type OperatorRepository interface {
	// Create creates a new operator
	Create(ctx context.Context, operator *entities.Operator) error

	// GetByID retrieves an operator by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Operator, error)

	// GetByName retrieves an operator by name within an organization.
	// Operator names are unique per (organization_id, name), not globally.
	GetByName(ctx context.Context, orgID uuid.UUID, name string) (*entities.Operator, error)

	// GetByPublicKey retrieves an operator by its NATS public key
	GetByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error)

	// List retrieves operators with pagination (legacy — kept for internal sweepers).
	List(ctx context.Context, opts ListOptions) ([]*entities.Operator, error)

	// ListPage returns one keyset-paginated page of operators visible under scope.
	// Order: (created_at DESC, id DESC). Empty next_cursor means no more pages.
	ListPage(ctx context.Context, scope authz.Scope, filter OperatorListFilter) ([]*entities.Operator, string, error)

	// Update updates an existing operator
	Update(ctx context.Context, operator *entities.Operator) error

	// Delete deletes an operator by ID
	Delete(ctx context.Context, id uuid.UUID) error

	// Search returns operators visible under scope whose name, description, or
	// public_key contain the (case-insensitive) substring `q`. Up to `limit`
	// rows. Used by P11 global search. Scope narrowing matches ListPage so
	// search and list cannot drift apart on the same query.
	Search(ctx context.Context, scope authz.Scope, q string, limit int) ([]*entities.Operator, error)
}
