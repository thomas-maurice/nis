package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	// Database drivers
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	sqlRepo "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// sqlRepositoryFactory implements RepositoryFactory for SQL databases (SQLite, PostgreSQL)
type sqlRepositoryFactory struct {
	config Config

	// GORM DB for repositories
	gormDB *gorm.DB

	// Standard library sql.DB for migrations
	sqlDB *sql.DB

	// Repository instances (lazy-loaded)
	operatorRepo                repositories.OperatorRepository
	accountRepo                 repositories.AccountRepository
	userRepo                    repositories.UserRepository
	scopedSigningKeyRepo         repositories.ScopedSigningKeyRepository
	clusterRepo                 repositories.ClusterRepository
	apiUserRepo                 repositories.APIUserRepository
	apiTokenRepo                repositories.APITokenRepository
	eventRepo                   repositories.EventRepository
	webhookSubscriptionRepo     repositories.WebhookSubscriptionRepository
	webhookDeliveryRepo         repositories.WebhookDeliveryRepository
	userJWTRevocationRepo       repositories.UserJWTRevocationRepository
}

func newSQLRepositoryFactory(cfg Config) (RepositoryFactory, error) {
	return &sqlRepositoryFactory{
		config: cfg,
	}, nil
}

// NewSQLRepositoryFactoryFromDB wraps an already-open *gorm.DB in a RepositoryFactory.
// Used by tests that bring their own connection (e.g. in-memory SQLite); production
// code calls NewRepositoryFactory(Config) and then Connect() instead.
func NewSQLRepositoryFactoryFromDB(db *gorm.DB) RepositoryFactory {
	sqlDB, _ := db.DB()
	return &sqlRepositoryFactory{
		gormDB: db,
		sqlDB:  sqlDB,
	}
}

func (f *sqlRepositoryFactory) Connect(ctx context.Context) error {
	// Open GORM connection
	var gormDB *gorm.DB
	var err error

	switch f.config.Driver {
	case "sqlite":
		// Enable foreign keys for SQLite + force UTC for time.Time bindings.
		// See sql/db.go for why _loc=UTC is mandatory; the short version is
		// that without it, lexical timestamp comparisons drift by the host
		// machine's offset and the JWT-lifecycle sweeper (P2) silently misses
		// rows.
		dsn := f.config.DSN
		if dsn != ":memory:" && dsn != "" {
			if len(dsn) > 0 && dsn[len(dsn)-1] != '?' {
				dsn += "?_foreign_keys=on&_loc=UTC"
			}
		} else if dsn == ":memory:" {
			dsn = ":memory:?_foreign_keys=on&_loc=UTC"
		}
		gormDB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{NowFunc: clock.Now})
		if err == nil {
			// Also set PRAGMA for extra safety
			gormDB.Exec("PRAGMA foreign_keys = ON")
		}
	case "postgres", "postgresql":
		gormDB, err = gorm.Open(postgres.Open(f.config.DSN), &gorm.Config{NowFunc: clock.Now})
	default:
		return fmt.Errorf("unsupported SQL driver: %s", f.config.Driver)
	}

	if err != nil {
		return fmt.Errorf("failed to open GORM connection: %w", err)
	}

	f.gormDB = gormDB

	// Get underlying sql.DB for migrations
	sqlDB, err := gormDB.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB from GORM: %w", err)
	}

	// Test the connection
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// SQLite serialises writes at the engine level. Allowing multiple Go-side
	// connections lets two writes race for the engine lock and surfaces as
	// `database is locked` under any contention. The unit-test path in
	// internal/infrastructure/persistence/sql/db.go has always pinned to 1
	// connection; this is the production-path equivalent. Once we wrap multi-
	// step writes in a single tx (A1), the lock-contention window grew enough
	// that not pinning here would be flaky.
	if f.config.Driver == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}

	f.sqlDB = sqlDB

	return nil
}

func (f *sqlRepositoryFactory) Close() error {
	if f.sqlDB != nil {
		return f.sqlDB.Close()
	}
	return nil
}

// Ping verifies the database connection is alive. Cheap enough to be called per
// /readyz probe (Postgres + SQLite both implement Ping as a connection check).
func (f *sqlRepositoryFactory) Ping(ctx context.Context) error {
	if f.sqlDB == nil {
		return fmt.Errorf("database not connected")
	}
	return f.sqlDB.PingContext(ctx)
}

// Inventory returns counts for the entity tables surfaced as Prometheus gauges.
// Called from the metrics refresh goroutine — not from every Prom scrape — so a
// handful of COUNT(*) queries is acceptable. clusters_healthy uses the cached
// Healthy flag maintained by the 60s health-check loop.
func (f *sqlRepositoryFactory) Inventory(ctx context.Context) (Inventory, error) {
	if f.gormDB == nil {
		return Inventory{}, fmt.Errorf("database not connected")
	}
	var inv Inventory
	db := f.gormDB.WithContext(ctx)
	if err := db.Table("operators").Count(&inv.Operators).Error; err != nil {
		return Inventory{}, fmt.Errorf("count operators: %w", err)
	}
	if err := db.Table("accounts").Count(&inv.Accounts).Error; err != nil {
		return Inventory{}, fmt.Errorf("count accounts: %w", err)
	}
	if err := db.Table("users").Count(&inv.Users).Error; err != nil {
		return Inventory{}, fmt.Errorf("count users: %w", err)
	}
	if err := db.Table("scoped_signing_keys").Count(&inv.ScopedKeys).Error; err != nil {
		return Inventory{}, fmt.Errorf("count scoped keys: %w", err)
	}
	if err := db.Table("clusters").Count(&inv.Clusters).Error; err != nil {
		return Inventory{}, fmt.Errorf("count clusters: %w", err)
	}
	if err := db.Table("clusters").Where("healthy = ?", true).Count(&inv.ClustersHealthy).Error; err != nil {
		return Inventory{}, fmt.Errorf("count healthy clusters: %w", err)
	}
	if err := db.Table("webhook_deliveries").Where("status = ?", "pending").Count(&inv.PendingDeliveries).Error; err != nil {
		return Inventory{}, fmt.Errorf("count pending deliveries: %w", err)
	}
	return inv, nil
}

// useEmbeddedMigrations wires goose to the dialect-specific embedded migration
// tree and returns the in-FS subdirectory to pass to goose.Up/Down. Atlas
// emits per-dialect SQL, so we maintain separate trees and pick at runtime.
func (f *sqlRepositoryFactory) useEmbeddedMigrations() (string, error) {
	fs, subdir, ok := migrations.FSForDriver(f.config.Driver)
	if !ok {
		return "", fmt.Errorf("no migrations for driver %q", f.config.Driver)
	}
	goose.SetBaseFS(fs)
	if err := goose.SetDialect(f.getGooseDriver()); err != nil {
		return "", fmt.Errorf("failed to set goose dialect: %w", err)
	}
	return subdir, nil
}

func (f *sqlRepositoryFactory) Migrate(ctx context.Context) error {
	if f.sqlDB == nil {
		return fmt.Errorf("database not connected")
	}

	subdir, err := f.useEmbeddedMigrations()
	if err != nil {
		return err
	}

	if err := goose.Up(f.sqlDB, subdir); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

func (f *sqlRepositoryFactory) Rollback(ctx context.Context) error {
	if f.sqlDB == nil {
		return fmt.Errorf("database not connected")
	}

	subdir, err := f.useEmbeddedMigrations()
	if err != nil {
		return err
	}

	if err := goose.Down(f.sqlDB, subdir); err != nil {
		return fmt.Errorf("failed to rollback migration: %w", err)
	}

	return nil
}

func (f *sqlRepositoryFactory) MigrationStatus(ctx context.Context) ([]MigrationInfo, error) {
	if f.sqlDB == nil {
		return nil, fmt.Errorf("database not connected")
	}

	subdir, err := f.useEmbeddedMigrations()
	if err != nil {
		return nil, err
	}

	// Get migrations
	migrations, err := goose.CollectMigrations(subdir, 0, goose.MaxVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to collect migrations: %w", err)
	}

	// Get current version
	currentVersion, err := goose.GetDBVersion(f.sqlDB)
	if err != nil {
		return nil, fmt.Errorf("failed to get DB version: %w", err)
	}

	var result []MigrationInfo
	for _, m := range migrations {
		info := MigrationInfo{
			Version: m.Version,
			Name:    m.Source,
		}

		// Check if this migration has been applied
		if m.Version <= currentVersion {
			// Migration is applied, but we don't have the timestamp easily available
			// from Goose's API, so we just mark it as applied
			applied := "applied"
			info.AppliedAt = &applied
		}

		result = append(result, info)
	}

	return result, nil
}

func (f *sqlRepositoryFactory) getGooseDriver() string {
	switch f.config.Driver {
	case "sqlite":
		return "sqlite3"
	case "postgres", "postgresql":
		return "postgres"
	default:
		return f.config.Driver
	}
}

// Repository accessors - lazy initialization

func (f *sqlRepositoryFactory) OperatorRepository() repositories.OperatorRepository {
	if f.operatorRepo == nil {
		f.operatorRepo = sqlRepo.NewOperatorRepo(f.gormDB)
	}
	return f.operatorRepo
}

func (f *sqlRepositoryFactory) AccountRepository() repositories.AccountRepository {
	if f.accountRepo == nil {
		f.accountRepo = sqlRepo.NewAccountRepo(f.gormDB)
	}
	return f.accountRepo
}

func (f *sqlRepositoryFactory) UserRepository() repositories.UserRepository {
	if f.userRepo == nil {
		f.userRepo = sqlRepo.NewUserRepo(f.gormDB)
	}
	return f.userRepo
}

func (f *sqlRepositoryFactory) ScopedSigningKeyRepository() repositories.ScopedSigningKeyRepository {
	if f.scopedSigningKeyRepo == nil {
		f.scopedSigningKeyRepo = sqlRepo.NewScopedSigningKeyRepo(f.gormDB)
	}
	return f.scopedSigningKeyRepo
}

func (f *sqlRepositoryFactory) ClusterRepository() repositories.ClusterRepository {
	if f.clusterRepo == nil {
		f.clusterRepo = sqlRepo.NewClusterRepo(f.gormDB)
	}
	return f.clusterRepo
}

func (f *sqlRepositoryFactory) APIUserRepository() repositories.APIUserRepository {
	if f.apiUserRepo == nil {
		f.apiUserRepo = sqlRepo.NewAPIUserRepo(f.gormDB)
	}
	return f.apiUserRepo
}

func (f *sqlRepositoryFactory) APITokenRepository() repositories.APITokenRepository {
	if f.apiTokenRepo == nil {
		f.apiTokenRepo = sqlRepo.NewAPITokenRepo(f.gormDB)
	}
	return f.apiTokenRepo
}

func (f *sqlRepositoryFactory) EventRepository() repositories.EventRepository {
	if f.eventRepo == nil {
		f.eventRepo = sqlRepo.NewEventRepo(f.gormDB)
	}
	return f.eventRepo
}

func (f *sqlRepositoryFactory) WebhookSubscriptionRepository() repositories.WebhookSubscriptionRepository {
	if f.webhookSubscriptionRepo == nil {
		f.webhookSubscriptionRepo = sqlRepo.NewWebhookSubscriptionRepo(f.gormDB)
	}
	return f.webhookSubscriptionRepo
}

func (f *sqlRepositoryFactory) WebhookDeliveryRepository() repositories.WebhookDeliveryRepository {
	if f.webhookDeliveryRepo == nil {
		f.webhookDeliveryRepo = sqlRepo.NewWebhookDeliveryRepo(f.gormDB)
	}
	return f.webhookDeliveryRepo
}

func (f *sqlRepositoryFactory) UserJWTRevocationRepository() repositories.UserJWTRevocationRepository {
	if f.userJWTRevocationRepo == nil {
		f.userJWTRevocationRepo = sqlRepo.NewUserJWTRevocationRepo(f.gormDB)
	}
	return f.userJWTRevocationRepo
}

// WithTx runs fn inside a GORM transaction. The factory passed to fn hands out
// fresh repos bound to the tx's *gorm.DB, so any read or write goes through the
// transaction. GORM commits when fn returns nil, rolls back on error or panic.
//
// Anything that escapes the database (NATS publishes, file writes, network
// calls) must NOT happen inside fn — they can't be rolled back, and a failure
// after them would leave external state ahead of the DB.
func (f *sqlRepositoryFactory) WithTx(ctx context.Context, fn func(tx RepositoryFactory) error) error {
	if f.gormDB == nil {
		return fmt.Errorf("database not connected")
	}
	return f.gormDB.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		txFactory := &sqlRepositoryFactory{
			config: f.config,
			gormDB: txDB,
			sqlDB:  f.sqlDB, // shared; tx scope doesn't replace the connection pool
		}
		return fn(txFactory)
	})
}
