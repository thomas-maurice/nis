package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

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

	// List retrieves all accounts with pagination
	List(ctx context.Context, opts ListOptions) ([]*entities.Account, error)

	// ListByOperator retrieves accounts for a specific operator
	ListByOperator(ctx context.Context, operatorID uuid.UUID, opts ListOptions) ([]*entities.Account, error)

	// Update updates an existing account
	Update(ctx context.Context, account *entities.Account) error

	// Delete deletes an account by ID
	Delete(ctx context.Context, id uuid.UUID) error
}
