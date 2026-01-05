package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// UserRepository defines the interface for user persistence
type UserRepository interface {
	// Create creates a new user
	Create(ctx context.Context, user *entities.User) error

	// GetByID retrieves a user by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)

	// GetByName retrieves a user by name within an account
	GetByName(ctx context.Context, accountID uuid.UUID, name string) (*entities.User, error)

	// GetByPublicKey retrieves a user by its NATS public key
	GetByPublicKey(ctx context.Context, publicKey string) (*entities.User, error)

	// List retrieves all users with pagination
	List(ctx context.Context, opts ListOptions) ([]*entities.User, error)

	// ListByAccount retrieves users for a specific account
	ListByAccount(ctx context.Context, accountID uuid.UUID, opts ListOptions) ([]*entities.User, error)

	// ListByScopedSigningKey retrieves users signed by a specific scoped signing key
	ListByScopedSigningKey(ctx context.Context, scopedKeyID uuid.UUID, opts ListOptions) ([]*entities.User, error)

	// Update updates an existing user
	Update(ctx context.Context, user *entities.User) error

	// Delete deletes a user by ID
	Delete(ctx context.Context, id uuid.UUID) error
}
