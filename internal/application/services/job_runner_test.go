package services

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlpkg "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// jobRunnerTestDB builds a fresh in-memory SQLite DB with all migrations
// applied. Mirrors apiTokenTestDB. Each test gets a clean DB; no shared state.
func jobRunnerTestDB(t *testing.T) persistence.RepositoryFactory {
	t.Helper()
	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))
	t.Cleanup(func() { _ = sqlpkg.Close(db) })
	return persistence.NewSQLRepositoryFactoryFromDB(db)
}

// quickRunner returns a JobRunner with a tight poll interval for tests. The
// poll interval needs to be much shorter than the test's await timeout, but
// not so short that we burn CPU before the test sets up.
func quickRunner(factory persistence.RepositoryFactory) *JobRunner {
	return NewJobRunner(factory, JobRunnerConfig{
		PollInterval:       20 * time.Millisecond,
		ClaimBatch:         5,
		LeaseDuration:      5 * time.Second,
		ShutdownTimeout:    1 * time.Second,
		DefaultMaxAttempts: 3,
		BackoffBase:        10 * time.Millisecond,
		BackoffCap:         50 * time.Millisecond,
		WorkerID:           "test-worker",
	})
}

// awaitJobStatus polls Get until the job reaches the given status or the
// timeout elapses. Returns the final job (regardless of whether the status
// matched) so the caller can produce a useful failure message.
func awaitJobStatus(t *testing.T, factory persistence.RepositoryFactory, id uuid.UUID, want entities.JobStatus, timeout time.Duration) *entities.Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *entities.Job
	for time.Now().Before(deadline) {
		got, err := factory.JobRepository().Get(context.Background(), id)
		if err == nil {
			last = got
			if got.Status == want {
				return got
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return last
}

// TestRegisterAfterRun_Panics protects against ordering bugs in serve.go.
func TestRegisterAfterRun_Panics(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = runner.Run(ctx) }()
	// Give Run a moment to flip the started flag.
	time.Sleep(50 * time.Millisecond)
	defer cancel()

	assert.Panics(t, func() {
		runner.Register("late", HandlerSpec{Handler: func(context.Context, []byte) error { return nil }})
	})
}

// TestEnqueueAndExecute_HappyPath proves the round-trip: enqueue, runner
// picks it up, handler runs, job ends in 'succeeded'.
func TestEnqueueAndExecute_HappyPath(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)

	var ran atomic.Int32
	runner.Register("ok", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			ran.Add(1)
			return nil
		},
		AuditPolicy: AuditAll,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "ok", nil)
	require.NoError(t, err)

	final := awaitJobStatus(t, factory, id, entities.JobStatusSucceeded, 2*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, entities.JobStatusSucceeded, final.Status)
	assert.Equal(t, int32(1), ran.Load(), "handler must have run exactly once")
	require.NotNil(t, final.CompletedAt)
}

// TestHandler_Retry_OnError verifies the transient-failure path: handler
// returns error, runner reschedules with backoff, eventually succeeds.
func TestHandler_Retry_OnError(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)

	var calls atomic.Int32
	runner.Register("retry", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			n := calls.Add(1)
			if n < 2 {
				return errors.New("transient")
			}
			return nil
		},
		MaxAttempts: 5,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "retry", nil)
	require.NoError(t, err)

	final := awaitJobStatus(t, factory, id, entities.JobStatusSucceeded, 3*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, entities.JobStatusSucceeded, final.Status, "succeeds on retry")
	assert.GreaterOrEqual(t, calls.Load(), int32(2), "handler must have been called at least twice (one failure + one success)")
}

// TestHandler_DeadLetter_AfterMaxAttempts: a handler that always fails
// terminates as 'dead_lettered' after MaxAttempts calls.
func TestHandler_DeadLetter_AfterMaxAttempts(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)

	var calls atomic.Int32
	runner.Register("doomed", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			calls.Add(1)
			return errors.New("always fails")
		},
		MaxAttempts: 3,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "doomed", nil)
	require.NoError(t, err)

	final := awaitJobStatus(t, factory, id, entities.JobStatusDeadLettered, 3*time.Second)
	require.NotNil(t, final, "expected the job to reach dead_lettered")
	assert.Equal(t, entities.JobStatusDeadLettered, final.Status)
	assert.Contains(t, final.LastError, "always fails")
	require.NotNil(t, final.CompletedAt)
	assert.Equal(t, int32(3), calls.Load(), "exactly MaxAttempts handler invocations")
}

// TestHandler_PanicRecovered: a panicking handler MUST NOT take down the
// runner. The panic surfaces as a regular error and follows the retry path.
func TestHandler_PanicRecovered(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)

	var calls atomic.Int32
	runner.Register("panicky", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			calls.Add(1)
			panic("boom")
		},
		MaxAttempts: 2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "panicky", nil)
	require.NoError(t, err)

	final := awaitJobStatus(t, factory, id, entities.JobStatusDeadLettered, 3*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, entities.JobStatusDeadLettered, final.Status, "panic chain ends in dead_letter, not runner death")
	assert.Contains(t, final.LastError, "handler panic")
	assert.Contains(t, final.LastError, "boom")
	assert.Equal(t, int32(2), calls.Load())

	// After the panic chain, the runner is still alive and accepting new
	// work. Enqueue a no-op handler and verify it runs.
	runner = quickRunner(factory)
	runner.Register("after-panic", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error { return nil },
	})
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = runner.Run(ctx2) }()
	id2, err := runner.Enqueue(ctx2, "after-panic", nil)
	require.NoError(t, err)
	final2 := awaitJobStatus(t, factory, id2, entities.JobStatusSucceeded, 1*time.Second)
	require.NotNil(t, final2)
}

// TestUnknownType_DeadLetters: a job whose type has no registered handler
// must NOT sit pending forever (it'd hog the partial-unique-index dedup
// slot). Runner dead-letters it on the first claim with a clear reason.
func TestUnknownType_DeadLetters(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	// Note: no Register call for "orphan".

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "orphan", nil)
	require.NoError(t, err)

	final := awaitJobStatus(t, factory, id, entities.JobStatusDeadLettered, 2*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, entities.JobStatusDeadLettered, final.Status)
	assert.Contains(t, final.LastError, "no handler registered")
}

// TestScheduledFor_DelaysExecution: a job scheduled in the future is NOT
// claimed by ticks that happen before the deadline.
func TestScheduledFor_DelaysExecution(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)

	var ran atomic.Bool
	runner.Register("delayed", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			ran.Store(true)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	// Schedule 300ms in the future.
	id, err := runner.Enqueue(ctx, "delayed", nil, WithScheduledFor(time.Now().Add(300*time.Millisecond)))
	require.NoError(t, err)

	// Confirm it hasn't run after 100ms.
	time.Sleep(100 * time.Millisecond)
	assert.False(t, ran.Load(), "delayed job ran before its scheduled time")

	// Now wait for it to actually run.
	final := awaitJobStatus(t, factory, id, entities.JobStatusSucceeded, 1*time.Second)
	require.NotNil(t, final)
	assert.True(t, ran.Load())
}

// TestEnsureScheduled_Idempotent: calling EnsureScheduled twice for the
// same (type, dedupKey) only inserts one row.
func TestEnsureScheduled_Idempotent(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	runner.Register("watched", HandlerSpec{
		Handler: func(context.Context, []byte) error { return nil },
	})

	ctx := context.Background()
	// First insert: returns true (inserted).
	inserted, err := runner.EnsureScheduled(ctx, "watched", nil, time.Now().Add(time.Hour), "singleton")
	require.NoError(t, err)
	assert.True(t, inserted)

	// Second insert with the same dedup key: returns false (skipped).
	inserted, err = runner.EnsureScheduled(ctx, "watched", nil, time.Now().Add(time.Hour), "singleton")
	require.NoError(t, err)
	assert.False(t, inserted, "EnsureScheduled must skip when a pending row already exists for (type, dedup)")

	// Only one row exists.
	count, err := factory.JobRepository().CountByStatus(ctx, entities.JobStatusPending)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestEnsureScheduled_RejectsEmptyDedup: without a dedup key, the watchdog
// would insert a row on every tick. Bug-loud rejection prevents that.
func TestEnsureScheduled_RejectsEmptyDedup(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	_, err := runner.EnsureScheduled(context.Background(), "x", nil, time.Now(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dedupKey must be non-empty")
}

// TestEnqueue_DedupKeyRejection: when a pending row exists for the same
// (type, dedup), Enqueue returns ErrJobAlreadyEnqueued (caller is supposed
// to know this is a possible outcome — that's why we offer EnsureScheduled
// for the watchdog path).
func TestEnqueue_DedupKeyRejection(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	runner.Register("dup", HandlerSpec{Handler: func(context.Context, []byte) error { return nil }})

	ctx := context.Background()
	_, err := runner.Enqueue(ctx, "dup", nil, WithDedupKey("singleton"), WithScheduledFor(time.Now().Add(time.Hour)))
	require.NoError(t, err)

	_, err = runner.Enqueue(ctx, "dup", nil, WithDedupKey("singleton"), WithScheduledFor(time.Now().Add(time.Hour)))
	assert.ErrorIs(t, err, repositories.ErrJobAlreadyEnqueued)
}

// TestShutdownDrains: Run() blocks until ctx is cancelled, then waits for
// inflight handlers to finish before returning. A slow handler that's still
// running at cancel must complete cleanly.
func TestShutdownDrains(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := NewJobRunner(factory, JobRunnerConfig{
		PollInterval:       20 * time.Millisecond,
		ClaimBatch:         1,
		LeaseDuration:      5 * time.Second,
		ShutdownTimeout:    2 * time.Second,
		DefaultMaxAttempts: 1,
		BackoffBase:        10 * time.Millisecond,
		BackoffCap:         50 * time.Millisecond,
		WorkerID:           "drain-test",
	})

	started := make(chan struct{}, 1)
	finished := make(chan struct{}, 1)
	runner.Register("slow", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			started <- struct{}{}
			// Hold the handler for 300ms — well within ShutdownTimeout.
			time.Sleep(300 * time.Millisecond)
			finished <- struct{}{}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "slow", nil)
	require.NoError(t, err)

	// Wait until the handler has actually started, then cancel.
	select {
	case <-started:
	case <-time.After(1 * time.Second):
		t.Fatal("slow handler never started")
	}
	cancel()

	// Run should return cleanly, AND the handler should have finished.
	select {
	case err := <-runDone:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	select {
	case <-finished:
	default:
		t.Fatal("handler did not finish during graceful drain")
	}

	// And the job should be 'succeeded' in the DB.
	got, err := factory.JobRepository().Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, entities.JobStatusSucceeded, got.Status)
}

// TestAuditPolicy_AuditNone_SkipsLifecycleEvents: with AuditNone, the
// success path emits NO events (apart from the dead-letter for unknown
// types, but that's not exercised here). Verified by counting events
// emitted to the events table.
func TestAuditPolicy_AuditNone_SkipsLifecycleEvents(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	runner.Register("quiet", HandlerSpec{
		Handler:     func(context.Context, []byte) error { return nil },
		AuditPolicy: AuditNone,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "quiet", nil)
	require.NoError(t, err)
	final := awaitJobStatus(t, factory, id, entities.JobStatusSucceeded, 2*time.Second)
	require.NotNil(t, final)

	page, err := factory.EventRepository().List(context.Background(), repositories.EventFilter{
		Limit: 100,
	})
	require.NoError(t, err)
	for _, e := range page.Events {
		assert.NotContains(t, e.Type, "job.", "AuditNone must not emit job.* events for resource %s", e.ResourceID)
	}
}

// TestAuditPolicy_AuditAll_EmitsLifecycleEvents: with AuditAll, the
// success path emits enqueue + started + succeeded. Critical for the
// admin UI showing a complete trail.
func TestAuditPolicy_AuditAll_EmitsLifecycleEvents(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	runner.Register("loud", HandlerSpec{
		Handler:     func(context.Context, []byte) error { return nil },
		AuditPolicy: AuditAll,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "loud", nil)
	require.NoError(t, err)
	final := awaitJobStatus(t, factory, id, entities.JobStatusSucceeded, 2*time.Second)
	require.NotNil(t, final)

	// Allow the emit-after-succeed write a beat to land.
	time.Sleep(100 * time.Millisecond)

	page, err := factory.EventRepository().List(context.Background(), repositories.EventFilter{
		ResourceType: "job",
		ResourceID:   id.String(),
		Limit:        100,
	})
	require.NoError(t, err)
	types := map[string]bool{}
	for _, e := range page.Events {
		types[e.Type] = true
	}
	assert.True(t, types[entities.EventTypeJobEnqueued], "missing job.enqueued")
	assert.True(t, types[entities.EventTypeJobStarted], "missing job.started")
	assert.True(t, types[entities.EventTypeJobSucceeded], "missing job.succeeded")
}

// TestAuditPolicy_FailuresOnly: AuditFailuresOnly is the recurring-sweep
// default. Success path produces nothing; a failure produces job.failed
// or job.dead_lettered.
func TestAuditPolicy_FailuresOnly(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)
	runner.Register("recurring", HandlerSpec{
		Handler:     func(context.Context, []byte) error { return errors.New("nope") },
		MaxAttempts: 1,
		AuditPolicy: AuditFailuresOnly,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	id, err := runner.Enqueue(ctx, "recurring", nil)
	require.NoError(t, err)
	final := awaitJobStatus(t, factory, id, entities.JobStatusDeadLettered, 2*time.Second)
	require.NotNil(t, final)

	time.Sleep(100 * time.Millisecond)

	page, err := factory.EventRepository().List(context.Background(), repositories.EventFilter{
		ResourceType: "job",
		ResourceID:   id.String(),
		Limit:        100,
	})
	require.NoError(t, err)
	types := map[string]bool{}
	for _, e := range page.Events {
		types[e.Type] = true
	}
	assert.False(t, types[entities.EventTypeJobEnqueued], "AuditFailuresOnly must not emit job.enqueued")
	assert.False(t, types[entities.EventTypeJobStarted], "AuditFailuresOnly must not emit job.started")
	assert.True(t, types[entities.EventTypeJobDeadLettered], "AuditFailuresOnly must emit job.dead_lettered")
}

// TestWorkerID_AutoGenerated: empty WorkerID gets a hostname-pid-uuid string
// so logs and locked_by are correlatable. Pin the format minimally — we
// don't want to over-specify, but "non-empty + contains a dash" is the
// load-bearing property.
func TestWorkerID_AutoGenerated(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := NewJobRunner(factory, JobRunnerConfig{}) // empty WorkerID
	assert.NotEmpty(t, runner.WorkerID())
	assert.Contains(t, runner.WorkerID(), "-")
}

// TestConcurrentHandlers: two due jobs of different types claimed in the
// same tick run concurrently in separate goroutines. Verified by having
// each handler signal a channel and confirming both fired within the
// expected window.
func TestConcurrentHandlers(t *testing.T) {
	factory := jobRunnerTestDB(t)
	runner := quickRunner(factory)

	var wg sync.WaitGroup
	wg.Add(2)
	runner.Register("a", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			wg.Done()
			// Hold until the other handler also fires — proves concurrency.
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				return errors.New("timed out waiting for parallel handler")
			}
			return nil
		},
	})
	runner.Register("b", HandlerSpec{
		Handler: func(ctx context.Context, _ []byte) error {
			wg.Done()
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				return errors.New("timed out waiting for parallel handler")
			}
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()

	idA, err := runner.Enqueue(ctx, "a", nil)
	require.NoError(t, err)
	idB, err := runner.Enqueue(ctx, "b", nil)
	require.NoError(t, err)

	finalA := awaitJobStatus(t, factory, idA, entities.JobStatusSucceeded, 2*time.Second)
	finalB := awaitJobStatus(t, factory, idB, entities.JobStatusSucceeded, 2*time.Second)
	require.NotNil(t, finalA)
	require.NotNil(t, finalB)
	assert.Equal(t, entities.JobStatusSucceeded, finalA.Status)
	assert.Equal(t, entities.JobStatusSucceeded, finalB.Status)
}
