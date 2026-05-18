package services

import (
	"context"
	"time"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// EventsRetentionWorker sweeps old events and succeeded webhook deliveries on
// a configurable cadence. It runs once immediately on start, then every
// sweepInterval.
//
// Deleting events cascades to webhook_deliveries via FK ON DELETE CASCADE (see
// migration 00002). The separate succeeded-delivery sweep covers deliveries
// whose events are still within retention but whose delivery rows are old.
type EventsRetentionWorker struct {
	factory                        persistence.RepositoryFactory
	retentionDays                  int
	succeededDeliveryRetentionDays int
	sweepInterval                  time.Duration
}

// NewEventsRetentionWorker creates a retention worker. retentionDays=0 disables
// event cleanup; succeededDays=0 disables succeeded-delivery cleanup.
func NewEventsRetentionWorker(factory persistence.RepositoryFactory, retentionDays, succeededDays int, sweepInterval time.Duration) *EventsRetentionWorker {
	if sweepInterval <= 0 {
		sweepInterval = 24 * time.Hour
	}
	return &EventsRetentionWorker{
		factory:                        factory,
		retentionDays:                  retentionDays,
		succeededDeliveryRetentionDays: succeededDays,
		sweepInterval:                  sweepInterval,
	}
}

// Run blocks until ctx is cancelled, running a sweep immediately then on the
// configured interval.
func (w *EventsRetentionWorker) Run(ctx context.Context) {
	log := logging.GetLogger()
	log.Info("events retention worker started",
		"event_retention_days", w.retentionDays,
		"delivery_retention_days", w.succeededDeliveryRetentionDays,
		"sweep_interval", w.sweepInterval,
	)

	w.sweep(ctx)

	ticker := time.NewTicker(w.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *EventsRetentionWorker) sweep(ctx context.Context) {
	log := logging.GetLogger()
	now := clock.Now()

	// Events retention.
	if w.retentionDays > 0 {
		cutoff := now.AddDate(0, 0, -w.retentionDays)
		n, err := w.factory.EventRepository().DeleteOlderThan(ctx, cutoff)
		if err != nil {
			log.Error("events retention: delete old events", "error", err)
		} else if n > 0 {
			log.Info("events retention: deleted old events", "count", n, "cutoff", cutoff)
		}
	}

	// Succeeded webhook deliveries retention.
	if w.succeededDeliveryRetentionDays > 0 {
		cutoff := now.AddDate(0, 0, -w.succeededDeliveryRetentionDays)
		n, err := w.factory.WebhookDeliveryRepository().DeleteSucceededOlderThan(ctx, cutoff)
		if err != nil {
			log.Error("events retention: delete old succeeded deliveries", "error", err)
		} else if n > 0 {
			log.Info("events retention: deleted old succeeded deliveries", "count", n, "cutoff", cutoff)
		}
	}
}
