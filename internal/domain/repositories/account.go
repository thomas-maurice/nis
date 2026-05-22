package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// AccountListFilter holds the filtering and pagination parameters for AccountRepository.ListPage.
type AccountListFilter struct {
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
	// OperatorID, if set, further filters to accounts belonging to this operator.
	// Must be compatible with the scope (e.g. an operator-admin scope with a different
	// operator ID will produce empty results).
	OperatorID *uuid.UUID
}

// AccountRepository defines the interface for account persistence
type AccountRepository interface {
	// Create creates a new account
	Create(ctx context.Context, account *entities.Account) error

	// GetByID retrieves an account by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Account, error)

	// GetByName retrieves an account by name within an operator
	GetByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Account, error)

	// GetByPublicKey retrieves an account by its NATS public key
	GetByPublicKey(ctx context.Context, publicKey string) (*entities.Account, error)

	// List retrieves all accounts with pagination (legacy — kept for internal sweepers).
	List(ctx context.Context, opts ListOptions) ([]*entities.Account, error)

	// ListByOperator retrieves accounts for a specific operator (legacy — kept for internal sweepers).
	ListByOperator(ctx context.Context, operatorID uuid.UUID, opts ListOptions) ([]*entities.Account, error)

	// ListPage returns one keyset-paginated page of accounts visible under scope.
	// Order: (created_at DESC, id DESC). Empty next_cursor means no more pages.
	ListPage(ctx context.Context, scope authz.Scope, filter AccountListFilter) ([]*entities.Account, string, error)

	// Update updates an existing account
	Update(ctx context.Context, account *entities.Account) error

	// Delete deletes an account by ID
	Delete(ctx context.Context, id uuid.UUID) error

	// Search returns accounts whose name, description, or public_key contain
	// the (case-insensitive) substring `q`. Up to `limit` rows. Used by P11
	// global search.
	Search(ctx context.Context, q string, limit int) ([]*entities.Account, error)
}
