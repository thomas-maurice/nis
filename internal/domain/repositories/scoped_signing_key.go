package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

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

	// List retrieves all scoped signing keys with pagination
	List(ctx context.Context, opts ListOptions) ([]*entities.ScopedSigningKey, error)

	// ListByAccount retrieves scoped signing keys for a specific account
	ListByAccount(ctx context.Context, accountID uuid.UUID, opts ListOptions) ([]*entities.ScopedSigningKey, error)

	// Update updates an existing scoped signing key
	Update(ctx context.Context, key *entities.ScopedSigningKey) error

	// Delete deletes a scoped signing key by ID
	Delete(ctx context.Context, id uuid.UUID) error

	// Search returns scoped signing keys whose name, description, public_key,
	// or any of pub_allow / pub_deny / sub_allow / sub_deny contain the
	// (case-insensitive) substring `q`. Up to `limit` rows. The pub/sub lists
	// are stored as JSON arrays of subject strings, so a query like
	// "metrics.>" finds keys that publish-allow that subject. Used by P11
	// global search.
	Search(ctx context.Context, q string, limit int) ([]*entities.ScopedSigningKey, error)
}
