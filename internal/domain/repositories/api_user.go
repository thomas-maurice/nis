package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// APIUserListFilter filters the keyset-paginated APIUserRepository.ListPage.
// API users are an admin-only surface, so there is no per-row tenant scope:
// non-admin callers see no rows regardless of these filters.
type APIUserListFilter struct {
	Limit        int
	Cursor       string
	Role         string // exact-match on role; "" disables
	UsernameLike string // case-insensitive substring on username
	AuthSource   string // exact-match on auth_source ("local" | "oidc"); "" disables
	CreatedSince *time.Time
	CreatedUntil *time.Time
}

// APIUserRepository defines the interface for API user persistence
type APIUserRepository interface {
	// Create creates a new API user
	Create(ctx context.Context, user *entities.APIUser) error

	// GetByID retrieves an API user by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.APIUser, error)

	// GetByUsername retrieves an API user by username
	GetByUsername(ctx context.Context, username string) (*entities.APIUser, error)

	// List retrieves all API users with pagination (legacy)
	List(ctx context.Context, opts ListOptions) ([]*entities.APIUser, error)

	// ListPage returns one keyset-paginated page of API users. Admin-only:
	// any non-admin scope yields an empty result.
	ListPage(ctx context.Context, scope authz.Scope, filter APIUserListFilter) ([]*entities.APIUser, string, error)

	// Update updates an existing API user
	Update(ctx context.Context, user *entities.APIUser) error

	// GetByExternalSubject retrieves an OIDC-sourced api_user by
	// (organization_id, external_subject). Returns ErrNotFound if no row exists.
	GetByExternalSubject(ctx context.Context, orgID uuid.UUID, subject string) (*entities.APIUser, error)

	// Delete deletes an API user by ID
	Delete(ctx context.Context, id uuid.UUID) error
}
