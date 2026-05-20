package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// JobHandler executes one job. The payload is opaque (handler-specific JSON
// in practice). Return nil for success; return non-nil to trigger retry.
//
// Wrap ErrPermanentJobFailure in the returned error to dead-letter the row
// immediately, regardless of remaining attempts. Use this for failures the
// handler has already classified as unrecoverable (e.g. it has already
// written terminal state to a typed companion row, like webhook_deliveries
// → dead_letter). Without the sentinel, the substrate would retry until
// MaxAttempts before dead-lettering, painting a misleading "still pending"
// state on the companion row in between.
//
// Panics are recovered by the runner and treated as a non-nil error so a
// buggy handler can't take down the runner goroutine.
type JobHandler func(ctx context.Context, payload []byte) error

// ErrPermanentJobFailure marks a handler error as unretryable. Wrap with
// fmt.Errorf("...: %w", services.ErrPermanentJobFailure) so errors.Is can
// detect it.
var ErrPermanentJobFailure = errors.New("permanent job failure")

// AuditPolicy controls which lifecycle events the runner emits to the
// events table for a given handler. High-frequency recurring sweeps should
// use AuditFailuresOnly (or AuditNone) so they don't flood the audit log.
// One-shot user-initiated jobs use AuditAll so an admin can trace them.
type AuditPolicy int

const (
	AuditAll AuditPolicy = iota
	AuditFailuresOnly
	AuditNone
)

// HandlerSpec wires a handler into the runner with its per-type policy.
//
// RecurEvery, if non-zero, turns this handler into a recurring schedule:
// on every poll tick the runner calls EnsureScheduled to insert a row
// scheduled for `now + RecurEvery` if no pending/running row already
// exists for this type. The dedup key is "recur-<type>". This is the
// watchdog mechanism — it survives both handler crashes AND restarts
// without per-handler bootstrap code.
type HandlerSpec struct {
	Handler     JobHandler
	MaxAttempts int           // 0 = use JobRunnerConfig.DefaultMaxAttempts
	BackoffBase time.Duration // 0 = use JobRunnerConfig.BackoffBase
	BackoffCap  time.Duration // 0 = use JobRunnerConfig.BackoffCap
	AuditPolicy AuditPolicy
	RecurEvery  time.Duration // 0 = one-shot only; non-zero = recurring schedule
}

// JobRunnerConfig tunes the runner's polling + retry behavior.
type JobRunnerConfig struct {
	PollInterval        time.Duration // how often to call ClaimDue (default 2s)
	ClaimBatch          int           // rows claimed per tick (default 10)
	LeaseDuration       time.Duration // how long a claim holds a row (default 5m)
	ShutdownTimeout     time.Duration // graceful drain on ctx cancel (default 30s)
	DefaultMaxAttempts  int           // per-handler override possible (default 5)
	BackoffBase         time.Duration // exponential base (default 10s)
	BackoffCap          time.Duration // exponential cap (default 10m)
	WorkerID            string        // identifies this process in locked_by; auto-generated if empty
}

func (c *JobRunnerConfig) applyDefaults() {
	if c.PollInterval <= 0 {
		c.PollInterval = 2 * time.Second
	}
	if c.ClaimBatch <= 0 {
		c.ClaimBatch = 10
	}
	if c.LeaseDuration <= 0 {
		c.LeaseDuration = 5 * time.Minute
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = 30 * time.Second
	}
	if c.DefaultMaxAttempts <= 0 {
		c.DefaultMaxAttempts = 5
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 10 * time.Second
	}
	if c.BackoffCap <= 0 {
		c.BackoffCap = 10 * time.Minute
	}
	if c.WorkerID == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "unknown"
		}
		// Short UUID suffix for log/event correlation. The full ID isn't
		// label-safe (cardinality) so metrics deliberately omit worker_id.
		c.WorkerID = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8])
	}
}

// EnqueueOption mutates a Job before insertion.
type EnqueueOption func(*entities.Job)

// WithScheduledFor delays a job until the given time. Default is "now".
func WithScheduledFor(t time.Time) EnqueueOption {
	return func(j *entities.Job) { j.ScheduledFor = t }
}

// WithDedupKey participates in the partial unique index on (type, dedup_key)
// while a row is pending or running. Empty = no dedup (multiple rows allowed).
func WithDedupKey(k string) EnqueueOption {
	return func(j *entities.Job) { j.DedupKey = k }
}

// WithMaxAttempts overrides the handler's default MaxAttempts for this job.
func WithMaxAttempts(n int) EnqueueOption {
	return func(j *entities.Job) { j.MaxAttempts = n }
}

// JobRunner polls the jobs table for due rows, dispatches them by type, and
// handles retry / dead-letter. One instance per process. Multi-replica is
// safe on Postgres via FOR UPDATE SKIP LOCKED; single-replica only on
// SQLite (enforced via SetMaxOpenConns=1, see sql_factory.go).
type JobRunner struct {
	factory persistence.RepositoryFactory
	cfg     JobRunnerConfig

	mu       sync.RWMutex
	handlers map[string]HandlerSpec

	inflight sync.WaitGroup
	started  atomic.Bool
	stopped  atomic.Bool
}

// NewJobRunner returns a runner ready for Register + Run.
func NewJobRunner(factory persistence.RepositoryFactory, cfg JobRunnerConfig) *JobRunner {
	cfg.applyDefaults()
	return &JobRunner{
		factory:  factory,
		cfg:      cfg,
		handlers: make(map[string]HandlerSpec),
	}
}

// WorkerID returns the runner's identifier. Useful for log correlation.
func (r *JobRunner) WorkerID() string { return r.cfg.WorkerID }

// Register associates a JobHandler with a job type. MUST be called before
// Run — registering after the runner is started is a programming error and
// panics so the bug is loud.
func (r *JobRunner) Register(jobType string, spec HandlerSpec) {
	if r.started.Load() {
		panic(fmt.Sprintf("JobRunner.Register(%q) called after Run() started", jobType))
	}
	if spec.Handler == nil {
		panic(fmt.Sprintf("JobRunner.Register(%q): Handler is nil", jobType))
	}
	if spec.MaxAttempts == 0 {
		spec.MaxAttempts = r.cfg.DefaultMaxAttempts
	}
	if spec.BackoffBase == 0 {
		spec.BackoffBase = r.cfg.BackoffBase
	}
	if spec.BackoffCap == 0 {
		spec.BackoffCap = r.cfg.BackoffCap
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[jobType] = spec
}

// Enqueue inserts a new job. Returns the assigned ID. If the job's type
// has no registered handler, the row is still inserted (it will be claimed
// when a handler appears) but a warning is logged.
//
// Returns repositories.ErrJobAlreadyEnqueued (wrapped) when the partial
// unique index rejects the row.
func (r *JobRunner) Enqueue(ctx context.Context, jobType string, payload any, opts ...EnqueueOption) (uuid.UUID, error) {
	body, err := marshalPayload(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal payload: %w", err)
	}
	r.mu.RLock()
	spec, hasSpec := r.handlers[jobType]
	r.mu.RUnlock()
	maxAttempts := r.cfg.DefaultMaxAttempts
	if hasSpec {
		maxAttempts = spec.MaxAttempts
	} else {
		logging.LogFromContext(ctx).Warn("JobRunner.Enqueue: unknown job type",
			"type", jobType,
			"hint", "the row will sit pending until a handler is registered")
	}
	now := clock.Now()
	j := &entities.Job{
		ID:           uuid.New(),
		Type:         jobType,
		Payload:      body,
		Status:       entities.JobStatusPending,
		ScheduledFor: now,
		MaxAttempts:  maxAttempts,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	for _, opt := range opts {
		opt(j)
	}
	if err := r.factory.JobRepository().Enqueue(ctx, j); err != nil {
		return uuid.Nil, err
	}
	if r.shouldAudit(spec, hasSpec, true) {
		r.emitJobEvent(ctx, j, entities.EventTypeJobEnqueued, nil)
	}
	metrics.Default().RecordJobEnqueued(jobType)
	return j.ID, nil
}

// EnsureScheduled inserts the row only if no row with the same
// (type, dedup_key) is currently pending or running. Used by the per-tick
// watchdog so a recurring schedule survives a handler crash that skipped
// the self-re-enqueue.
//
// dedupKey MUST be non-empty — passing empty would create duplicate rows
// on every tick (NULL dedup_key doesn't participate in the unique index).
func (r *JobRunner) EnsureScheduled(ctx context.Context, jobType string, payload any, scheduledFor time.Time, dedupKey string) (bool, error) {
	if dedupKey == "" {
		return false, fmt.Errorf("EnsureScheduled: dedupKey must be non-empty (otherwise the watchdog inserts a fresh row every tick)")
	}
	body, err := marshalPayload(payload)
	if err != nil {
		return false, fmt.Errorf("marshal payload: %w", err)
	}
	r.mu.RLock()
	spec, hasSpec := r.handlers[jobType]
	r.mu.RUnlock()
	maxAttempts := r.cfg.DefaultMaxAttempts
	if hasSpec {
		maxAttempts = spec.MaxAttempts
	}
	now := clock.Now()
	j := &entities.Job{
		ID:           uuid.New(),
		Type:         jobType,
		Payload:      body,
		Status:       entities.JobStatusPending,
		ScheduledFor: scheduledFor.UTC(),
		MaxAttempts:  maxAttempts,
		DedupKey:     dedupKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	inserted, err := r.factory.JobRepository().EnqueueIfAbsent(ctx, j)
	if err != nil {
		return false, err
	}
	if inserted {
		if r.shouldAudit(spec, hasSpec, true) {
			r.emitJobEvent(ctx, j, entities.EventTypeJobEnqueued, nil)
		}
		metrics.Default().RecordJobEnqueued(jobType)
	}
	return inserted, nil
}

// Run blocks until ctx is cancelled. On cancel, polling stops and the
// runner waits up to ShutdownTimeout for in-flight handlers to finish.
func (r *JobRunner) Run(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return errors.New("JobRunner.Run: already started")
	}
	defer r.stopped.Store(true)

	log := logging.GetLogger()
	log.Info("job runner started",
		"worker_id", r.cfg.WorkerID,
		"poll_interval", r.cfg.PollInterval,
		"claim_batch", r.cfg.ClaimBatch,
		"lease_duration", r.cfg.LeaseDuration,
	)

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	// errBackoff stops log flooding when ClaimDue itself keeps failing
	// (DB outage, partial-index violation in a buggy migration, etc.).
	errBackoff := time.Second

	// Prime the watchdog once before entering the loop so the first
	// recurring schedules are visible immediately, without waiting for
	// the first tick.
	r.runWatchdog(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Info("job runner stopping, draining in-flight handlers", "worker_id", r.cfg.WorkerID)
			done := make(chan struct{})
			go func() {
				r.inflight.Wait()
				close(done)
			}()
			select {
			case <-done:
				log.Info("job runner drained", "worker_id", r.cfg.WorkerID)
			case <-time.After(r.cfg.ShutdownTimeout):
				log.Warn("job runner shutdown timed out; running jobs will be reclaimed via lease",
					"worker_id", r.cfg.WorkerID,
					"timeout", r.cfg.ShutdownTimeout)
			}
			return nil

		case <-ticker.C:
			// Watchdog first so a handler crash mid-tick still leaves the
			// schedule healthy for the next tick.
			r.runWatchdog(ctx)

			due, err := r.factory.JobRepository().ClaimDue(
				ctx, r.cfg.WorkerID, r.cfg.LeaseDuration, clock.Now(), r.cfg.ClaimBatch,
			)
			if err != nil {
				log.Error("job runner: ClaimDue failed",
					"error", err, "worker_id", r.cfg.WorkerID, "backoff", errBackoff)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(errBackoff):
				}
				if errBackoff < 30*time.Second {
					errBackoff *= 2
					if errBackoff > 30*time.Second {
						errBackoff = 30 * time.Second
					}
				}
				continue
			}
			errBackoff = time.Second

			for _, j := range due {
				j := j
				r.inflight.Add(1)
				go func() {
					defer r.inflight.Done()
					r.processOne(ctx, j)
				}()
			}
		}
	}
}

// runWatchdog iterates registered handlers and, for each one with
// RecurEvery > 0, ensures a future row is scheduled. Cheap — one
// INSERT-or-skip per recurring handler per tick, all conflicting with
// the partial unique index. Reviewer-flagged correctness control: the
// alternative ("handler self-re-enqueues at end of work") loses the
// schedule entirely if the handler crashes between work-done and
// re-enqueue.
func (r *JobRunner) runWatchdog(ctx context.Context) {
	r.mu.RLock()
	type pending struct {
		jobType  string
		interval time.Duration
	}
	var todo []pending
	for jobType, spec := range r.handlers {
		if spec.RecurEvery > 0 {
			todo = append(todo, pending{jobType: jobType, interval: spec.RecurEvery})
		}
	}
	r.mu.RUnlock()

	for _, p := range todo {
		if ctx.Err() != nil {
			return
		}
		dedup := "recur-" + p.jobType
		// Schedule the next occurrence p.interval out so a clean restart
		// doesn't fire every handler immediately — the schedule restarts
		// from the next interval boundary. Failures here are logged only;
		// the next tick retries automatically.
		if _, err := r.EnsureScheduled(ctx, p.jobType, nil, clock.Now().Add(p.interval), dedup); err != nil {
			logging.GetLogger().Warn("job runner: watchdog EnsureScheduled failed",
				"type", p.jobType, "error", err)
		}
	}
}

// processOne dispatches a single claimed job and persists the outcome.
//
// ctx is the runner's shutdown context — passed to the handler so it can
// cooperate with shutdown — but the post-handler state writes (MarkSucceeded
// / MarkFailed / EmitSystem) use a fresh background context with a bounded
// timeout. Without that detachment, a cancel mid-handler races the cleanup
// writes and leaves the row stuck in 'running' until the lease expires.
// Handlers that genuinely overrun ShutdownTimeout get reclaimed via lease
// expiry anyway — that's the existing recovery path.
func (r *JobRunner) processOne(ctx context.Context, j *entities.Job) {
	log := logging.GetLogger()
	r.mu.RLock()
	spec, hasSpec := r.handlers[j.Type]
	r.mu.RUnlock()

	// writeCtx is detached from ctx so the bookkeeping survives shutdown.
	// 10s is well above the typical SQL latency and below any reasonable
	// graceful-drain window.
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWrite()

	// Unknown type: dead-letter immediately. Sitting forever is worse —
	// the row is taking up an active dedup slot and admins should notice.
	if !hasSpec {
		log.Error("job runner: no handler registered for type",
			"job_id", j.ID, "type", j.Type)
		_ = r.factory.JobRepository().MarkFailed(writeCtx, j.ID, "no handler registered for type "+j.Type, nil)
		metrics.Default().RecordJobCompleted(j.Type, "dead_lettered")
		// Audit even when AuditNone — an orphan job IS a config bug.
		r.emitJobEvent(writeCtx, j, entities.EventTypeJobDeadLettered, map[string]any{"reason": "no_handler"})
		return
	}

	if spec.AuditPolicy == AuditAll {
		r.emitJobEvent(writeCtx, j, entities.EventTypeJobStarted, nil)
	}

	start := time.Now() // duration measurement only
	handlerErr := runHandlerSafely(ctx, spec.Handler, j.Payload)
	elapsed := time.Since(start).Seconds()
	metrics.Default().RecordJobDuration(j.Type, elapsed)

	if handlerErr == nil {
		if err := r.factory.JobRepository().MarkSucceeded(writeCtx, j.ID); err != nil {
			log.Error("job runner: MarkSucceeded failed",
				"job_id", j.ID, "type", j.Type, "error", err)
			return
		}
		metrics.Default().RecordJobCompleted(j.Type, "succeeded")
		if spec.AuditPolicy == AuditAll {
			r.emitJobEvent(writeCtx, j, entities.EventTypeJobSucceeded, nil)
		}
		return
	}

	// Handler error path. Decide retry vs dead-letter.
	errMsg := handlerErr.Error()
	permanent := errors.Is(handlerErr, ErrPermanentJobFailure)
	if permanent || j.Attempts >= spec.MaxAttempts {
		if err := r.factory.JobRepository().MarkFailed(writeCtx, j.ID, errMsg, nil); err != nil {
			log.Error("job runner: MarkFailed (dead-letter) failed",
				"job_id", j.ID, "type", j.Type, "error", err)
			return
		}
		metrics.Default().RecordJobCompleted(j.Type, "dead_lettered")
		if spec.AuditPolicy != AuditNone {
			r.emitJobEvent(writeCtx, j, entities.EventTypeJobDeadLettered, map[string]any{"error": errMsg})
		}
		return
	}

	retryAt := clock.Now().Add(computeJobBackoff(j.Attempts, spec.BackoffBase, spec.BackoffCap))
	if err := r.factory.JobRepository().MarkFailed(writeCtx, j.ID, errMsg, &retryAt); err != nil {
		log.Error("job runner: MarkFailed (retry) failed",
			"job_id", j.ID, "type", j.Type, "error", err)
		return
	}
	metrics.Default().RecordJobCompleted(j.Type, "failed")
	if spec.AuditPolicy != AuditNone {
		r.emitJobEvent(writeCtx, j, entities.EventTypeJobFailed, map[string]any{
			"error":   errMsg,
			"attempt": j.Attempts,
		})
	}
}

// runHandlerSafely invokes the handler in a deferred-recover wrapper so a
// panic surfaces as a regular error and follows the normal retry path.
// Without this, a buggy handler would kill the runner goroutine, freezing
// the entire job substrate until the process restarts.
func runHandlerSafely(ctx context.Context, h JobHandler, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("handler panic: %v\n%s", r, stack)
		}
	}()
	return h(ctx, payload)
}

// shouldAudit decides whether to emit a lifecycle event for the given
// outcome stage. enqueue=true skips the "AuditFailuresOnly" filter (enqueue
// failures bubble up via the Enqueue return value, not an audit event).
func (r *JobRunner) shouldAudit(spec HandlerSpec, hasSpec bool, enqueue bool) bool {
	if !hasSpec {
		return true // unknown type → always audit
	}
	if spec.AuditPolicy == AuditNone {
		return false
	}
	if enqueue {
		return spec.AuditPolicy == AuditAll
	}
	return true
}

func (r *JobRunner) emitJobEvent(ctx context.Context, j *entities.Job, eventType string, extra map[string]any) {
	payload := map[string]any{
		"type":    j.Type,
		"dedup":   j.DedupKey,
		"worker":  r.cfg.WorkerID,
		"attempt": j.Attempts,
	}
	for k, v := range extra {
		payload[k] = v
	}
	err := events.EmitSystem(ctx, r.factory, events.Event{
		Type:         eventType,
		ResourceType: "job",
		ResourceID:   j.ID.String(),
		Payload:      payload,
	})
	if err != nil {
		logging.GetLogger().Error("job runner: emit event",
			"event_type", eventType, "job_id", j.ID, "error", err)
	}
}

// computeJobBackoff returns a jittered exponential backoff duration for
// the given attempt count (1-based). Jitter is in [0.8, 1.2] to spread
// retries across a fleet of workers (matters once A3 leader-election lands).
func computeJobBackoff(attempt int, base, cap time.Duration) time.Duration {
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 30 { // avoid overflow at obscene attempt counts
		shift = 30
	}
	d := base * (1 << uint(shift))
	if d <= 0 || d > cap {
		d = cap
	}
	jitter := 0.8 + rand.Float64()*0.4
	d = time.Duration(float64(d) * jitter)
	if d > cap {
		d = cap
	}
	return d
}

func marshalPayload(p any) ([]byte, error) {
	if p == nil {
		return []byte("{}"), nil
	}
	if raw, ok := p.([]byte); ok {
		if len(raw) == 0 {
			return []byte("{}"), nil
		}
		return raw, nil
	}
	if s, ok := p.(string); ok {
		if s == "" {
			return []byte("{}"), nil
		}
		return []byte(s), nil
	}
	return json.Marshal(p)
}
