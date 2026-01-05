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

	"github.com/thomas-maurice/nis/internal/domain/repositories"
	sqlRepo "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
)

// sqlRepositoryFactory implements RepositoryFactory for SQL databases (SQLite, PostgreSQL)
type sqlRepositoryFactory struct {
	config Config

	// GORM DB for repositories
	gormDB *gorm.DB

	// Standard library sql.DB for migrations
	sqlDB *sql.DB

	// Repository instances (lazy-loaded)
	operatorRepo         repositories.OperatorRepository
	accountRepo          repositories.AccountRepository
	userRepo             repositories.UserRepository
	scopedSigningKeyRepo repositories.ScopedSigningKeyRepository
	clusterRepo          repositories.ClusterRepository
	apiUserRepo          repositories.APIUserRepository
}

func newSQLRepositoryFactory(cfg Config) (RepositoryFactory, error) {
	return &sqlRepositoryFactory{
		config: cfg,
	}, nil
}

func (f *sqlRepositoryFactory) Connect(ctx context.Context) error {
	// Open GORM connection
	var gormDB *gorm.DB
	var err error

	switch f.config.Driver {
	case "sqlite":
		gormDB, err = gorm.Open(sqlite.Open(f.config.DSN), &gorm.Config{})
	case "postgres", "postgresql":
		gormDB, err = gorm.Open(postgres.Open(f.config.DSN), &gorm.Config{})
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

	f.sqlDB = sqlDB

	return nil
}

func (f *sqlRepositoryFactory) Close() error {
	if f.sqlDB != nil {
		return f.sqlDB.Close()
	}
	return nil
}

func (f *sqlRepositoryFactory) Migrate(ctx context.Context) error {
	if f.sqlDB == nil {
		return fmt.Errorf("database not connected")
	}

	// Set Goose dialect
	gooseDriver := f.getGooseDriver()
	if err := goose.SetDialect(gooseDriver); err != nil {
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	// Run Goose migrations
	if err := goose.Up(f.sqlDB, f.config.MigrationDir); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

func (f *sqlRepositoryFactory) Rollback(ctx context.Context) error {
	if f.sqlDB == nil {
		return fmt.Errorf("database not connected")
	}

	// Set Goose dialect
	gooseDriver := f.getGooseDriver()
	if err := goose.SetDialect(gooseDriver); err != nil {
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	// Rollback last migration
	if err := goose.Down(f.sqlDB, f.config.MigrationDir); err != nil {
		return fmt.Errorf("failed to rollback migration: %w", err)
	}

	return nil
}

func (f *sqlRepositoryFactory) MigrationStatus(ctx context.Context) ([]MigrationInfo, error) {
	if f.sqlDB == nil {
		return nil, fmt.Errorf("database not connected")
	}

	// Set Goose dialect
	gooseDriver := f.getGooseDriver()
	if err := goose.SetDialect(gooseDriver); err != nil {
		return nil, fmt.Errorf("failed to set goose dialect: %w", err)
	}

	// Get migrations
	migrations, err := goose.CollectMigrations(f.config.MigrationDir, 0, goose.MaxVersion)
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
