package persistence

import (
	"context"
	"fmt"

	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// RepositoryFactory provides an abstraction for creating repository instances
// and managing database lifecycle (connections, migrations, etc.)
type RepositoryFactory interface {
	// Repository accessors
	OperatorRepository() repositories.OperatorRepository
	AccountRepository() repositories.AccountRepository
	UserRepository() repositories.UserRepository
	ScopedSigningKeyRepository() repositories.ScopedSigningKeyRepository
	ClusterRepository() repositories.ClusterRepository
	APIUserRepository() repositories.APIUserRepository

	// Database lifecycle methods
	Connect(ctx context.Context) error
	Close() error
	Migrate(ctx context.Context) error
	Rollback(ctx context.Context) error
	MigrationStatus(ctx context.Context) ([]MigrationInfo, error)
}

// MigrationInfo represents information about a database migration
type MigrationInfo struct {
	Version   int64
	Name      string
	AppliedAt *string // nil if not applied
}

// Config holds configuration for creating a repository factory
type Config struct {
	Driver string // "sqlite", "postgres", "mongodb", etc.
	DSN    string // Database connection string

	// Optional: Migration directory (defaults to "migrations")
	MigrationDir string
}

// NewRepositoryFactory creates a new repository factory based on the driver type
func NewRepositoryFactory(cfg Config) (RepositoryFactory, error) {
	if cfg.MigrationDir == "" {
		cfg.MigrationDir = "migrations"
	}

	switch cfg.Driver {
	case "sqlite", "postgres", "postgresql":
		return newSQLRepositoryFactory(cfg)
	// case "mongodb":
	//     return newMongoRepositoryFactory(cfg)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}
}
