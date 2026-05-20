package entities

import (
	"time"

	"github.com/google/uuid"
)

// JobStatus enumerates the lifecycle states of a row in the jobs table.
//
// pending: enqueued, not yet claimed. Eligible for ClaimDue when
// scheduled_for <= now and locked_until is null or expired.
//
// running: claimed by a worker. locked_by and locked_until are set; the
// row is reclaimable by another worker once locked_until elapses
// (handles dead-worker recovery).
//
// succeeded: handler returned nil.
//
// failed: handler returned a transient error; will be re-attempted with
// backoff until attempts >= max_attempts.
//
// dead_lettered: terminal failure (attempts >= max_attempts). Never
// auto-deleted by the retention sweep.
//
// cancelled: admin-initiated stop on a pending row.
type JobStatus string

const (
	JobStatusPending      JobStatus = "pending"
	JobStatusRunning      JobStatus = "running"
	JobStatusSucceeded    JobStatus = "succeeded"
	JobStatusFailed       JobStatus = "failed"
	JobStatusDeadLettered JobStatus = "dead_lettered"
	JobStatusCancelled    JobStatus = "cancelled"
)

// Job is one row in the jobs table — the unit of work for the A2 jobs
// substrate.
//
// Payload is opaque bytes (handler-specific JSON in practice). Type is the
// registry key used by JobRunner to dispatch.
//
// DedupKey, when non-empty, participates in the partial unique index
// (type, dedup_key) WHERE status IN ('pending','running') so a debounced
// or singleton job can't be enqueued twice while in-flight. NULL/empty
// dedup_key means "no dedup" — multiple rows can coexist.
type Job struct {
	ID           uuid.UUID
	Type         string
	Payload      []byte
	Status       JobStatus
	ScheduledFor time.Time
	LockedBy     string
	LockedUntil  *time.Time
	Attempts     int
	MaxAttempts  int
	LastError    string
	DedupKey     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
}
