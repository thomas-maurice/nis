package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// APIUserRepository defines the interface for API user persistence
type APIUserRepository interface {
	// Create creates a new API user
	Create(ctx context.Context, user *entities.APIUser) error

	// GetByID retrieves an API user by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.APIUser, error)

	// GetByUsername retrieves an API user by username
	GetByUsername(ctx context.Context, username string) (*entities.APIUser, error)

	// List retrieves all API users with pagination
	List(ctx context.Context, opts ListOptions) ([]*entities.APIUser, error)

	// Update updates an existing API user
	Update(ctx context.Context, user *entities.APIUser) error

	// Delete deletes an API user by ID
	Delete(ctx context.Context, id uuid.UUID) error
}
