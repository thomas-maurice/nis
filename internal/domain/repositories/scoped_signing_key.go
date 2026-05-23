package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// ScopedSigningKeyListFilter holds the filter + pagination parameters for
// ScopedSigningKeyRepository.ListPage.
type ScopedSigningKeyListFilter struct {
	Limit           int
	Cursor          string
	NameLike        string
	AccountID       *uuid.UUID
	TemplateID      *uuid.UUID
	IsPlainSigner   *bool
	TemplateDrifted *bool
	CreatedSince    *time.Time
	CreatedUntil    *time.Time
}

// ScopedSigningKeyRepository defines the interface for scoped signing key persistence
type ScopedSigningKeyRepository interface {
	// Create creates a new scoped signing key
	Create(ctx context.Context, key *entities.ScopedSigningKey) error

	// GetByID retrieves a scoped signing key by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.ScopedSigningKey, error)

	// GetByName retrieves a scoped signing key by name within an account
	GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.ScopedSigningKey, error)

	// GetByPublicKey retrieves a scoped signing key by its NATS public key
	GetByPublicKey(ctx context.Context, publicKey string) (*entities.ScopedSigningKey, error)

	// List retrieves all scoped signing keys with pagination (legacy — kept
	// for internal callers like SSK rotation, which loads up to 10000
	// dependent users in one tx atomically. Not API-exposed.).
	List(ctx context.Context, opts ListOptions) ([]*entities.ScopedSigningKey, error)

	// ListByAccount retrieves scoped signing keys for a specific account (legacy).
	ListByAccount(ctx context.Context, accountID uuid.UUID, opts ListOptions) ([]*entities.ScopedSigningKey, error)

	// ListPage returns one keyset-paginated page of SSKs visible under scope.
	// Order: (created_at DESC, id DESC).
	ListPage(ctx context.Context, scope authz.Scope, filter ScopedSigningKeyListFilter) ([]*entities.ScopedSigningKey, string, error)

	// Update updates an existing scoped signing key
	Update(ctx context.Context, key *entities.ScopedSigningKey) error

	// Delete deletes a scoped signing key by ID
	Delete(ctx context.Context, id uuid.UUID) error

	// Search returns scoped signing keys visible under scope whose name,
	// description, public_key, or any of pub_allow / pub_deny / sub_allow /
	// sub_deny contain the (case-insensitive) substring `q`. Up to `limit`
	// rows. The pub/sub lists are stored as JSON arrays of subject strings,
	// so a query like "metrics.>" finds keys that publish-allow that subject.
	// Used by P11 global search. Scope narrowing matches ListPage.
	Search(ctx context.Context, scope authz.Scope, q string, limit int) ([]*entities.ScopedSigningKey, error)
}
