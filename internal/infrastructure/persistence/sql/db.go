package sql

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewDB creates a new database connection. Used by the service-layer test
// suites — production code uses persistence.NewRepositoryFactory instead.
// driver is "sqlite" or "postgres"; for sqlite, dsn is a filesystem path
// (or ":memory:"); for postgres, dsn is the connection string.
func NewDB(driver, dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector

	switch driver {
	case "sqlite":
		if dsn == "" {
			return nil, fmt.Errorf("SQLite path is required")
		}
		dialector = sqlite.Open(dsn + "?_foreign_keys=on")

	case "postgres":
		dialector = postgres.Open(dsn)

	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Enable foreign key constraints for SQLite on all connections
	if driver == "sqlite" {
		// Set on the current connection
		if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
			return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
		}

		// Get the underlying SQL DB to configure the connection pool
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("failed to get underlying SQL DB: %w", err)
		}

		// Set max open connections to 1 for SQLite to avoid concurrency issues
		// and ensure foreign keys are always enabled
		sqlDB.SetMaxOpenConns(1)
	}

	return db, nil
}

// Close closes the database connection
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying SQL DB: %w", err)
	}
	return sqlDB.Close()
}
