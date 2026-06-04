package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// OrganizationRepository manages Organization persistence.
type OrganizationRepository interface {
	// Create inserts a new organization.
	Create(ctx context.Context, org *entities.Organization) error

	// GetByID retrieves an organization by ID.
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Organization, error)

	// GetBySlug retrieves an organization by slug.
	GetBySlug(ctx context.Context, slug string) (*entities.Organization, error)

	// List retrieves all organizations with basic pagination.
	List(ctx context.Context, opts ListOptions) ([]*entities.Organization, error)

	// Update updates an existing organization.
	Update(ctx context.Context, org *entities.Organization) error

	// Delete deletes an organization by ID.
	Delete(ctx context.Context, id uuid.UUID) error
}

// OrganizationSSOConfigRepository manages SSO configuration per organization.
type OrganizationSSOConfigRepository interface {
	// Upsert creates or replaces the SSO config for the given organization.
	Upsert(ctx context.Context, cfg *entities.OrganizationSSOConfig) error

	// GetByOrganizationID retrieves the SSO config for an organization.
	// Returns ErrNotFound when no config exists yet.
	GetByOrganizationID(ctx context.Context, orgID uuid.UUID) (*entities.OrganizationSSOConfig, error)

	// Delete removes the SSO config for the given organization.
	Delete(ctx context.Context, orgID uuid.UUID) error
}

// SSORoleMappingListFilter holds filtering parameters for ListByOrganization.
type SSORoleMappingListFilter struct {
	Limit  int
	Offset int
}

// SSORoleMappingRepository manages OIDC group → role mappings.
type SSORoleMappingRepository interface {
	// Create inserts a new role mapping.
	Create(ctx context.Context, m *entities.SSORoleMapping) error

	// GetByID retrieves a role mapping by ID.
	GetByID(ctx context.Context, id uuid.UUID) (*entities.SSORoleMapping, error)

	// ListByOrganization returns all mappings for the given organization,
	// ordered by priority DESC (highest first).
	ListByOrganization(ctx context.Context, orgID uuid.UUID, opts SSORoleMappingListFilter) ([]*entities.SSORoleMapping, error)

	// Update updates an existing role mapping.
	Update(ctx context.Context, m *entities.SSORoleMapping) error

	// Delete removes a role mapping by ID.
	Delete(ctx context.Context, id uuid.UUID) error
}

// OIDCLoginStateRepository manages short-lived OIDC login state tokens.
type OIDCLoginStateRepository interface {
	// Create persists a new login state.
	Create(ctx context.Context, s *entities.OIDCLoginState) error

	// GetAndDelete atomically retrieves and removes the login state for the
	// given state token. Returns ErrNotFound when the token does not exist or
	// has already been consumed.
	GetAndDelete(ctx context.Context, state string) (*entities.OIDCLoginState, error)

	// DeleteExpired removes all states whose expires_at is before cutoff.
	// Used by the retention worker to prevent table bloat.
	DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error)
}
