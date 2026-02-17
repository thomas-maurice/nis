package sql

import (
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/config"
	"github.com/thomas-maurice/nis/migrations"
)

func TestNewDB_SQLite(t *testing.T) {
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		Path:   ":memory:",
	}

	db, err := NewDB(cfg)
	require.NoError(t, err)
	require.NotNil(t, db)

	// Verify connection works
	sqlDB, err := db.DB()
	require.NoError(t, err)
	err = sqlDB.Ping()
	require.NoError(t, err)

	// Clean up
	err = Close(db)
	assert.NoError(t, err)
}

func TestMigrations(t *testing.T) {
	// Create in-memory SQLite database
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		Path:   ":memory:",
	}

	db, err := NewDB(cfg)
	require.NoError(t, err)
	defer func() { _ = Close(db) }()

	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Set up goose
	goose.SetBaseFS(migrations.Migrations)
	err = goose.SetDialect("sqlite3")
	require.NoError(t, err)

	// Run migrations up
	err = goose.Up(sqlDB, ".")
	require.NoError(t, err)

	// Verify tables exist
	tables := []string{
		"operators",
		"accounts",
		"users",
		"scoped_signing_keys",
		"clusters",
		"api_users",
	}

	for _, table := range tables {
		var count int
		err = sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "table %s should exist", table)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_accounts_operator_id",
		"idx_users_account_id",
		"idx_users_scoped_signing_key_id",
		"idx_scoped_signing_keys_account_id",
		"idx_clusters_operator_id",
	}

	for _, index := range indexes {
		var count int
		err = sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "index %s should exist", index)
	}

	// Test migration down
	err = goose.Down(sqlDB, ".")
	require.NoError(t, err)

	// Verify tables are dropped
	for _, table := range tables {
		var count int
		err = sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count, "table %s should be dropped", table)
	}
}

func TestNewDB_InvalidDriver(t *testing.T) {
	cfg := config.DatabaseConfig{
		Driver: "invalid",
	}

	db, err := NewDB(cfg)
	assert.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "unsupported database driver")
}

func TestNewDB_SQLite_MissingPath(t *testing.T) {
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		Path:   "",
	}

	db, err := NewDB(cfg)
	assert.Error(t, err)
	assert.Nil(t, db)
	assert.Contains(t, err.Error(), "SQLite path is required")
}
