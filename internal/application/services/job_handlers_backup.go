package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
)

// Job types for the scheduled backup feature (P12).
const (
	// JobTypeBackupSweep ticks on a recurring schedule, enumerates operators
	// that are due for a backup based on (BackupEnabled, BackupInterval,
	// LastBackupAt), and enqueues one backup.execute per due operator. The
	// per-operator dedup key prevents pile-up if a previous execute is
	// still running.
	JobTypeBackupSweep = "backup.sweep"

	// JobTypeBackupExecute runs one backup end-to-end (export → S3 upload
	// → DB row → retention). One-shot per operator per due window.
	// Manual "Run now" RPCs also enqueue rows of this type so the same
	// failure/retry/audit path is used regardless of trigger.
	JobTypeBackupExecute = "backup.execute"
)

// BackupExecutePayload is the JSON shape stored in the job's payload column
// for backup.execute rows.
type BackupExecutePayload struct {
	OperatorID string                       `json:"operator_id"`
	Trigger    entities.BackupTriggerKind   `json:"trigger"`
}

// BackupHandlerConfig is the operator-facing knobs the sweep handler reads
// at registration time.
type BackupHandlerConfig struct {
	// SweepInterval is how often the watchdog reschedules backup.sweep.
	// Defaults to 1h — tight enough to feel responsive after an operator
	// changes their interval, loose enough that the per-tick operator-list
	// scan stays cheap.
	SweepInterval time.Duration

	// ExecuteLeaseDuration is the per-row lease for backup.execute. The
	// substrate default is 5min; a large operator (many accounts, slow S3)
	// may legitimately exceed that. 15min matches the reviewer-requested
	// ceiling for v1; over the lease a stuck handler is reclaimed by a
	// peer worker, which would produce a duplicate S3 object that
	// retention eventually trims.
	ExecuteLeaseDuration time.Duration
}

func (c *BackupHandlerConfig) applyDefaults() {
	if c.SweepInterval <= 0 {
		c.SweepInterval = time.Hour
	}
	if c.ExecuteLeaseDuration <= 0 {
		c.ExecuteLeaseDuration = 15 * time.Minute
	}
}

// RegisterBackupHandlers attaches the two backup handlers to the runner.
// MUST be called before runner.Run; the substrate panics on post-start
// registration.
//
// Audit policy is AuditFailuresOnly on BOTH handlers — the substrate-level
// job.* lifecycle events would otherwise triple-emit per scheduled tick.
// BackupService emits its own semantic operator.backup.{succeeded,failed}
// events with the relevant context, which is the audit surface operators
// actually want.
func RegisterBackupHandlers(runner *JobRunner, svc *BackupService, cfg BackupHandlerConfig) {
	cfg.applyDefaults()

	sweep := &backupSweepHandler{runner: runner, svc: svc}
	exec := &backupExecuteHandler{svc: svc}

	runner.Register(JobTypeBackupSweep, HandlerSpec{
		Handler:     sweep.Run,
		MaxAttempts: 3,
		AuditPolicy: AuditFailuresOnly,
		RecurEvery:  cfg.SweepInterval,
	})
	runner.Register(JobTypeBackupExecute, HandlerSpec{
		Handler:       exec.Run,
		MaxAttempts:   3,
		AuditPolicy:   AuditFailuresOnly,
		LeaseDuration: cfg.ExecuteLeaseDuration,
	})
}

type backupSweepHandler struct {
	runner *JobRunner
	svc    *BackupService
}

func (h *backupSweepHandler) Run(ctx context.Context, _ []byte) error {
	if !h.svc.Enabled() {
		// Backups disabled at the NIS-wide level. The sweep handler being
		// registered at all suggests serve.go thought backups were on; this
		// is best treated as a degraded mode (still a no-op) rather than a
		// dead-letter.
		return nil
	}
	now := clock.Now()
	due, err := h.svc.FindDueOperators(ctx, now)
	if err != nil {
		return fmt.Errorf("find due operators: %w", err)
	}
	log := logging.LogFromContext(ctx)
	for _, opID := range due {
		payload := BackupExecutePayload{
			OperatorID: opID.String(),
			Trigger:    entities.BackupTriggerScheduled,
		}
		enqueued, err := h.runner.EnsureScheduled(ctx, JobTypeBackupExecute, payload, now, "backup-exec-"+opID.String())
		if err != nil {
			log.Warn("backup sweep: EnsureScheduled failed", "operator_id", opID, "error", err)
			continue
		}
		if !enqueued {
			log.Debug("backup sweep: previous backup.execute still pending/running, skipping",
				"operator_id", opID)
		}
	}
	return nil
}

type backupExecuteHandler struct {
	svc *BackupService
}

func (h *backupExecuteHandler) Run(ctx context.Context, payload []byte) error {
	if !h.svc.Enabled() {
		// Treat as permanent — the operator turned backups off globally
		// after this row was enqueued. No point retrying.
		return fmt.Errorf("%w: backups disabled", ErrPermanentJobFailure)
	}
	var p BackupExecutePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("%w: unmarshal payload: %v", ErrPermanentJobFailure, err)
	}
	operatorID, err := uuid.Parse(p.OperatorID)
	if err != nil {
		return fmt.Errorf("%w: parse operator_id: %v", ErrPermanentJobFailure, err)
	}
	trigger := p.Trigger
	if trigger == "" {
		trigger = entities.BackupTriggerScheduled
	}
	_, err = h.svc.RunBackup(ctx, operatorID, trigger)
	if err != nil {
		if errors.Is(err, ErrBackupsDisabled) {
			return fmt.Errorf("%w: %v", ErrPermanentJobFailure, err)
		}
		return err
	}
	return nil
}
