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
	APITokenRepository() repositories.APITokenRepository
	EventRepository() repositories.EventRepository
	WebhookSubscriptionRepository() repositories.WebhookSubscriptionRepository
	WebhookDeliveryRepository() repositories.WebhookDeliveryRepository
	UserJWTRevocationRepository() repositories.UserJWTRevocationRepository
	TemplateRepository() repositories.TemplateRepository
	TemplateVersionRepository() repositories.TemplateVersionRepository
	JobRepository() repositories.JobRepository

	// Database lifecycle methods
	Connect(ctx context.Context) error
	Close() error
	Migrate(ctx context.Context) error
	Rollback(ctx context.Context) error
	MigrationStatus(ctx context.Context) ([]MigrationInfo, error)

	// Ping verifies the database connection is alive. Used by /readyz.
	Ping(ctx context.Context) error

	// Inventory returns aggregate counts for the entity tables. Used by the metrics
	// refresh loop to populate domain gauges without paying COUNT(*) on every Prom scrape.
	Inventory(ctx context.Context) (Inventory, error)

	// WithTx runs fn inside a database transaction. The factory passed to fn hands
	// out repositories backed by that transaction — every read/write through tx-
	// scoped repos sees the same uncommitted state. The tx commits when fn
	// returns nil, rolls back on a non-nil error or a panic.
	//
	// Side-effects that can't be rolled back (e.g. NATS pushes) MUST happen AFTER
	// WithTx returns, never inside fn. See PROPOSALS.md A1.
	WithTx(ctx context.Context, fn func(tx RepositoryFactory) error) error
}

// Inventory is a snapshot of entity counts surfaced as Prometheus gauges.
type Inventory struct {
	Operators        int64
	Accounts         int64
	Users            int64
	ScopedKeys       int64
	Clusters         int64
	ClustersHealthy  int64
	PendingDeliveries int64
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
