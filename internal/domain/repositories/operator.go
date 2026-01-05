package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// OperatorRepository defines the interface for operator persistence
type OperatorRepository interface {
	// Create creates a new operator
	Create(ctx context.Context, operator *entities.Operator) error

	// GetByID retrieves an operator by ID
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Operator, error)

	// GetByName retrieves an operator by name
	GetByName(ctx context.Context, name string) (*entities.Operator, error)

	// GetByPublicKey retrieves an operator by its NATS public key
	GetByPublicKey(ctx context.Context, publicKey string) (*entities.Operator, error)

	// List retrieves operators with pagination
	List(ctx context.Context, opts ListOptions) ([]*entities.Operator, error)

	// Update updates an existing operator
	Update(ctx context.Context, operator *entities.Operator) error

	// Delete deletes an operator by ID
	Delete(ctx context.Context, id uuid.UUID) error
}
