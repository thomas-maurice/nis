package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// newTestFactory builds an in-memory SQLite-backed factory with all migrations
// applied. Tests scope each invocation to a fresh DB so they don't observe each
// other's writes.
func newTestFactory(t *testing.T) persistence.RepositoryFactory {
	t.Helper()

	db, err := sql.NewDB("sqlite", ":memory:")
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "."))

	return persistence.NewSQLRepositoryFactoryFromDB(db)
}

func makeOperator(name string) *entities.Operator {
	return &entities.Operator{
		ID:            uuid.New(),
		Name:          name,
		Description:   "test",
		EncryptedSeed: "not-real-but-non-empty",
		PublicKey:     "OAA" + uuid.New().String(), // unique
		JWT:           "fake-jwt",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

// TestWithTx_CommitOnSuccess writes inside WithTx, returns nil, then verifies
// the row is visible on a fresh read outside the tx.
func TestWithTx_CommitOnSuccess(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator("commit-me")

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return tx.OperatorRepository().Create(ctx, op)
	})
	require.NoError(t, err)

	got, err := factory.OperatorRepository().GetByID(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, op.Name, got.Name)
}

// TestWithTx_RollbackOnError is the regression test for A1: an inner failure
// after partial writes must leave NO partial state visible to a fresh read.
func TestWithTx_RollbackOnError(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator("rollback-me")
	sentinel := errors.New("force rollback")

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.OperatorRepository().Create(ctx, op); err != nil {
			return err
		}
		// Simulate a downstream failure AFTER the operator was already written
		// inside the tx. Without rollback, the operator would survive.
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	_, err = factory.OperatorRepository().GetByID(ctx, op.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound, "rolled-back row must not be visible")
}

// TestWithTx_RollbackOnPanic asserts a panic inside fn rolls back too. GORM
// recovers, returns the panic as an error, and the caller can recover it.
func TestWithTx_RollbackOnPanic(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator("panic-me")

	func() {
		defer func() { _ = recover() }()
		_ = factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
			_ = tx.OperatorRepository().Create(ctx, op)
			panic("simulated panic mid-tx")
		})
	}()

	_, err := factory.OperatorRepository().GetByID(ctx, op.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound, "row written before panic must not be visible")
}

// TestWithTx_NestedReadsSeeUncommittedWrites confirms that reads inside the
// same tx observe the writes — that's the whole point of a tx. Without this,
// regenerateAccountJWT inside CreateScopedSigningKey would still see the
// pre-write state.
func TestWithTx_NestedReadsSeeUncommittedWrites(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator("read-after-write")

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.OperatorRepository().Create(ctx, op); err != nil {
			return err
		}
		got, err := tx.OperatorRepository().GetByID(ctx, op.ID)
		if err != nil {
			return err
		}
		assert.Equal(t, op.Name, got.Name, "tx-scoped read must see tx-scoped write")
		return nil
	})
	require.NoError(t, err)
}
