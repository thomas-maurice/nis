package services

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
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlpkg "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// buildJWTExpirySweeperFixture spins up an in-memory SQLite + a real
// JWTExpirySweeper. Keeps the handler tests honest: they exercise the
// same Tick code path the goroutine path uses today.
func buildJWTExpirySweeperFixture(t *testing.T) (*JWTExpirySweeper, persistence.RepositoryFactory, *OperatorService, *AccountService, *UserService) {
	t.Helper()

	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlpkg.Close(db) })

	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))

	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)

	factory := persistence.NewSQLRepositoryFactoryFromDB(db)
	jwtSvc := NewJWTService(enc)
	accSvc := NewAccountService(factory, jwtSvc, enc)
	opSvc := NewOperatorService(factory, accSvc, jwtSvc, enc)
	userSvc := NewUserService(
		factory.UserRepository(),
		factory.AccountRepository(),
		factory.ScopedSigningKeyRepository(),
		factory.OperatorRepository(),
		jwtSvc,
		enc,
	).WithFactory(factory)

	revSvc := NewUserRevocationService(factory, jwtSvc, enc)
	sweeper := NewJWTExpirySweeper(factory, jwtSvc, revSvc, 500)
	return sweeper, factory, opSvc, accSvc, userSvc
}

// TestJWTExpiryHandler_RunDelegatesToTick wires a real handler over a
// real sweeper + DB and asserts the prune phase ran (a past-exp
// revocation flipped to PrunedAt). Catches any future regression where
// the handler accidentally short-circuits or pre-empts Tick.
func TestJWTExpiryHandler_RunDelegatesToTick(t *testing.T) {
	sweeper, factory, opSvc, accSvc, userSvc := buildJWTExpirySweeperFixture(t)
	ctx := context.Background()

	op, err := opSvc.CreateOperator(ctx, CreateOperatorRequest{Name: "handler-op"})
	require.NoError(t, err)
	acc, err := accSvc.CreateAccount(ctx, CreateAccountRequest{OperatorID: op.ID, Name: "handler-acc"})
	require.NoError(t, err)
	scopedKeys, err := factory.ScopedSigningKeyRepository().ListByAccount(ctx, acc.ID, repositories.ListOptions{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, scopedKeys)
	user, err := userSvc.CreateUser(ctx, CreateUserRequest{
		AccountID:          acc.ID,
		Name:               "handler-user",
		ScopedSigningKeyID: &scopedKeys[0].ID,
	})
	require.NoError(t, err)

	pastExp := time.Now().Add(-time.Hour)
	rev := &entities.UserJWTRevocation{
		ID:            uuid.New(),
		AccountID:     acc.ID,
		UserID:        &user.ID,
		UserPublicKey: user.PublicKey,
		RevokedAt:     pastExp.Add(-time.Hour),
		JWTExp:        pastExp,
		CreatedAt:     time.Now(),
	}
	require.NoError(t, factory.UserJWTRevocationRepository().Create(ctx, rev))

	h := &jwtExpirySweepHandler{sweeper: sweeper}
	require.NoError(t, h.Run(ctx, nil))

	pruned, err := factory.UserJWTRevocationRepository().GetByID(ctx, rev.ID)
	require.NoError(t, err)
	assert.NotNil(t, pruned.PrunedAt, "handler must drive the prune phase end-to-end")
}

// TestJWTExpiryHandler_RegisterPanicsAfterRunStart pins the
// register-before-run invariant. Mirrors the equivalent JobRunner
// safety test — without this, a misconfigured Register() call from a
// future serve.go path would silently get dropped instead of crashing
// loud at startup.
func TestJWTExpiryHandler_RegisterPanicsAfterRunStart(t *testing.T) {
	_, factory, _, _, _ := buildJWTExpirySweeperFixture(t)
	runner := NewJobRunner(factory, JobRunnerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		_ = runner.Run(ctx)
		close(runDone)
	}()
	// Give Run a chance to flip the started atomic.
	time.Sleep(100 * time.Millisecond)

	defer func() {
		r := recover()
		assert.NotNil(t, r, "Register after Run must panic")
		cancel()
		<-runDone
	}()
	RegisterJWTExpiryHandler(runner, nil, JWTExpiryHandlerConfig{SweepInterval: time.Hour})
	t.Fatal("expected panic, got none")
}

// TestJWTExpiryHandler_RegisterDefaults asserts the zero-value config
// picks the documented defaults. Forgetting these would make the
// handler register with a zero RecurEvery (no schedule, watchdog
// silently inert) and a zero LeaseDuration (substrate falls back to
// global 5min — too short for the auto-renew phase under load).
func TestJWTExpiryHandler_RegisterDefaults(t *testing.T) {
	_, factory, _, _, _ := buildJWTExpirySweeperFixture(t)
	runner := NewJobRunner(factory, JobRunnerConfig{})
	revSvc := NewUserRevocationService(factory, NewJWTService(nil), nil)
	sweeper := NewJWTExpirySweeper(factory, NewJWTService(nil), revSvc, 500)

	RegisterJWTExpiryHandler(runner, sweeper, JWTExpiryHandlerConfig{})

	runner.mu.RLock()
	spec, ok := runner.handlers[JobTypeJWTExpirySweep]
	runner.mu.RUnlock()
	require.True(t, ok, "handler must be registered")
	assert.Equal(t, time.Hour, spec.RecurEvery, "zero SweepInterval must default to 1h")
	assert.Equal(t, 15*time.Minute, spec.LeaseDuration, "zero LeaseDuration must default to 15m")
	assert.Equal(t, 3, spec.MaxAttempts, "MaxAttempts must be 3")
	assert.Equal(t, AuditFailuresOnly, spec.AuditPolicy, "AuditPolicy must be AuditFailuresOnly")
}

// TestJWTExpiryHandler_TickErrorPropagates ensures handler errors flow
// back to the substrate so the retry path engages. The contract is "the
// handler returns whatever Tick returns" — fault-inject by cancelling
// the ctx before Run and assert non-nil.
func TestJWTExpiryHandler_TickErrorPropagates(t *testing.T) {
	sweeper, _, _, _, _ := buildJWTExpirySweeperFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := &jwtExpirySweepHandler{sweeper: sweeper}
	err := h.Run(ctx, nil)
	// Tick reads operators first; under a cancelled context the repo call
	// returns context.Canceled which Tick surfaces verbatim.
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "ctx cancel must propagate up the handler return")
}
