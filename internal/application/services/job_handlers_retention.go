package services

import (
	"context"
	"fmt"
	"time"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// Job type constants. Adding new types here is the natural place — keeps
// the substring registry visible in one file and lets a future
// "ListJobTypes" RPC enumerate without reflection.
const (
	JobTypeEventsRetentionSweep = "events.retention_sweep"
	JobTypeJobsRetentionSweep   = "jobs.retention_sweep"
)

// RetentionConfig configures the two retention handlers. The shape mirrors
// the previous (now-removed) EventsRetentionWorker config so the operator-
// facing knobs don't change.
type RetentionConfig struct {
	// EventRetentionDays controls the events.retention_sweep handler.
	// 0 disables the handler (the row is still enqueued; it's just a no-op).
	EventRetentionDays int

	// SucceededDeliveryRetentionDays — webhook deliveries that completed
	// successfully are pruned this many days after `completed_at`. 0
	// disables that branch.
	SucceededDeliveryRetentionDays int

	// JobRetentionDays controls the jobs.retention_sweep handler. Applies
	// to 'succeeded' and 'cancelled' rows only — dead-lettered + failed
	// rows survive forever (operator audit).
	JobRetentionDays int

	// SweepInterval is how often the watchdog reschedules each sweep
	// handler. Default 24h. Tests can shrink this.
	SweepInterval time.Duration
}

func (c *RetentionConfig) applyDefaults() {
	if c.EventRetentionDays == 0 {
		c.EventRetentionDays = 30
	}
	if c.SucceededDeliveryRetentionDays == 0 {
		c.SucceededDeliveryRetentionDays = 7
	}
	if c.JobRetentionDays == 0 {
		c.JobRetentionDays = 30
	}
	if c.SweepInterval <= 0 {
		c.SweepInterval = 24 * time.Hour
	}
}

// RegisterRetentionHandlers attaches the events.retention_sweep and
// jobs.retention_sweep handlers to the runner. MUST be called before
// runner.Run starts (Register panics otherwise — see job_runner.go).
//
// Both handlers are AuditFailuresOnly: they fire on a 24h cadence and
// shouldn't flood the audit log on every successful run.
func RegisterRetentionHandlers(runner *JobRunner, factory persistence.RepositoryFactory, cfg RetentionConfig) {
	cfg.applyDefaults()

	eventsHandler := &eventsRetentionHandler{
		factory:                        factory,
		eventRetentionDays:             cfg.EventRetentionDays,
		succeededDeliveryRetentionDays: cfg.SucceededDeliveryRetentionDays,
	}
	jobsHandler := &jobsRetentionHandler{
		factory:          factory,
		jobRetentionDays: cfg.JobRetentionDays,
	}

	runner.Register(JobTypeEventsRetentionSweep, HandlerSpec{
		Handler:     eventsHandler.Run,
		MaxAttempts: 3,
		AuditPolicy: AuditFailuresOnly,
		RecurEvery:  cfg.SweepInterval,
	})
	runner.Register(JobTypeJobsRetentionSweep, HandlerSpec{
		Handler:     jobsHandler.Run,
		MaxAttempts: 3,
		AuditPolicy: AuditFailuresOnly,
		RecurEvery:  cfg.SweepInterval,
	})
}

// eventsRetentionHandler prunes the events + succeeded webhook_deliveries
// tables. Lifted verbatim from the (now-removed) EventsRetentionWorker.
// Behavioural parity matters: ops have wired alerts off the existing log
// lines.
type eventsRetentionHandler struct {
	factory                        persistence.RepositoryFactory
	eventRetentionDays             int
	succeededDeliveryRetentionDays int
}

func (h *eventsRetentionHandler) Run(ctx context.Context, _ []byte) error {
	log := logging.LogFromContext(ctx)
	now := clock.Now()

	if h.eventRetentionDays > 0 {
		cutoff := now.AddDate(0, 0, -h.eventRetentionDays)
		n, err := h.factory.EventRepository().DeleteOlderThan(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("delete old events: %w", err)
		}
		if n > 0 {
			log.Info("events retention: deleted old events", "count", n, "cutoff", cutoff)
		}
	}

	if h.succeededDeliveryRetentionDays > 0 {
		cutoff := now.AddDate(0, 0, -h.succeededDeliveryRetentionDays)
		n, err := h.factory.WebhookDeliveryRepository().DeleteSucceededOlderThan(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("delete old succeeded deliveries: %w", err)
		}
		if n > 0 {
			log.Info("events retention: deleted old succeeded deliveries", "count", n, "cutoff", cutoff)
		}
	}

	return nil
}

// jobsRetentionHandler prunes succeeded + cancelled job rows older than
// the configured cutoff. Dead-lettered + failed survive forever — that's
// the operator audit surface and the only thing the JobsView's "Show
// succeeded=off" default makes invisible by default.
type jobsRetentionHandler struct {
	factory          persistence.RepositoryFactory
	jobRetentionDays int
}

func (h *jobsRetentionHandler) Run(ctx context.Context, _ []byte) error {
	if h.jobRetentionDays <= 0 {
		return nil
	}
	cutoff := clock.Now().AddDate(0, 0, -h.jobRetentionDays)
	n, err := h.factory.JobRepository().DeleteCompletedBefore(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("delete completed jobs: %w", err)
	}
	if n > 0 {
		logging.LogFromContext(ctx).Info("jobs retention: deleted completed jobs",
			"count", n, "cutoff", cutoff)
	}
	return nil
}
