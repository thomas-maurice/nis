package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// ErrJobAlreadyEnqueued is returned by Enqueue when the partial unique index
// `(type, dedup_key) WHERE status IN ('pending','running')` rejects a row —
// a job with the same (type, dedup_key) is already pending or running.
// Treated as a successful no-op by callers using dedup_key for debouncing.
var ErrJobAlreadyEnqueued = errors.New("job already enqueued for this (type, dedup_key)")

// ErrJobInvalidStateTransition is returned when a Mark/Cancel/Retry call
// targets a row whose current status doesn't permit the requested transition.
// Carries no "current state" info — repository methods don't fetch first, they
// rely on `UPDATE ... WHERE id=? AND status IN (...)` returning RowsAffected=0
// to signal the violation. Callers that need to know why should Get() first.
var ErrJobInvalidStateTransition = errors.New("job not in a state that permits this transition")

// JobFilter narrows ListJobs results. Empty fields are treated as "any".
//
// Types/Statuses are inclusive — multi-value filters. Since/Until apply to
// created_at (matches the EventService.ListEvents shape so UI patterns
// translate; jobs the operator cares about most are recent ones).
//
// Cursor is opaque to callers — base64-encoded server state. Empty cursor
// means "first page".
type JobFilter struct {
	Types    []string
	Statuses []entities.JobStatus
	Since    *time.Time
	Until    *time.Time
	Cursor   string
	Limit    int
}

// JobRepository persists rows in the jobs table.
//
// Driver-aware locking lives in ClaimDue: Postgres uses FOR UPDATE SKIP
// LOCKED for multi-replica safety; SQLite relies on SetMaxOpenConns(1) +
// GORM's Transaction (a runtime assertion at startup enforces this).
type JobRepository interface {
	// Enqueue inserts one row. Returns ErrJobAlreadyEnqueued if the partial
	// unique index rejects it.
	Enqueue(ctx context.Context, job *entities.Job) error

	// EnqueueIfAbsent inserts the row only if no row with the same
	// (type, dedup_key) is currently pending or running. Equivalent to
	// Enqueue + swallow ErrJobAlreadyEnqueued. Use from watchdog paths
	// (per-tick EnsureScheduled) where "already there" is the happy path.
	// Returns (true, nil) when inserted, (false, nil) when skipped.
	EnqueueIfAbsent(ctx context.Context, job *entities.Job) (bool, error)

	// ClaimDue atomically claims up to `limit` pending rows whose
	// scheduled_for <= now and whose locked_until has elapsed (or is NULL).
	// Returned jobs have status='running', locked_by=workerID,
	// locked_until=now+lease, attempts incremented, started_at set.
	//
	// On the Postgres path the claim is one SQL statement
	// (UPDATE ... FROM (SELECT ... FOR UPDATE SKIP LOCKED) RETURNING *), so
	// two concurrent workers can never see the same row.
	//
	// On SQLite the claim runs inside a GORM transaction; SetMaxOpenConns(1)
	// serialises everything at the connection level, so there's no
	// in-process race. Multi-process SQLite is unsupported (SKILL §14).
	ClaimDue(ctx context.Context, workerID string, lease time.Duration, now time.Time, limit int) ([]*entities.Job, error)

	// MarkSucceeded transitions a 'running' row to 'succeeded'. Clears the
	// lease. Returns ErrJobInvalidStateTransition if the row isn't running.
	MarkSucceeded(ctx context.Context, id uuid.UUID) error

	// MarkFailed transitions a 'running' row. If retryAfter is non-nil,
	// status becomes 'pending' and scheduled_for=retryAfter (backoff path).
	// If retryAfter is nil, status becomes 'dead_lettered' (terminal).
	// In both cases the lease is cleared and last_error is set.
	MarkFailed(ctx context.Context, id uuid.UUID, lastError string, retryAfter *time.Time) error

	// ExtendLease pushes the row's locked_until to `until`, leaving status,
	// attempts, and worker untouched. Used by the runner when a HandlerSpec
	// opts into a longer lease than the global default (e.g. backup uploads
	// that may exceed 5min). Returns ErrJobInvalidStateTransition if the row
	// isn't currently 'running'.
	ExtendLease(ctx context.Context, id uuid.UUID, until time.Time) error

	// Cancel transitions a 'pending' row to 'cancelled'. Returns
	// ErrJobInvalidStateTransition if the row is already running, finished,
	// or cancelled.
	Cancel(ctx context.Context, id uuid.UUID) error

	// Retry resets a 'failed', 'dead_lettered', or 'cancelled' row back to
	// 'pending' with attempts=0, scheduled_for=now, last_error=''. The
	// admin-triggered "give this another go" path. Returns
	// ErrJobInvalidStateTransition if the row is pending or running.
	Retry(ctx context.Context, id uuid.UUID, now time.Time) error

	// Get returns one row by ID. ErrNotFound if absent.
	Get(ctx context.Context, id uuid.UUID) (*entities.Job, error)

	// List returns a page of rows matching the filter, plus an opaque next
	// cursor (empty string when no more pages).
	List(ctx context.Context, filter JobFilter) ([]*entities.Job, string, error)

	// DeleteCompletedBefore removes 'succeeded' and 'cancelled' rows older
	// than cutoff (compared against completed_at). Dead-lettered + failed
	// rows are never auto-deleted. Returns rows affected.
	DeleteCompletedBefore(ctx context.Context, cutoff time.Time) (int64, error)

	// CountByStatus returns the number of rows in the given status. Used
	// by the metrics gauge refresh.
	CountByStatus(ctx context.Context, status entities.JobStatus) (int64, error)
}
