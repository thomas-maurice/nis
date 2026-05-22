package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/s3backup"
)

// ErrBackupsDisabled is returned by BackupService methods when the NIS-wide
// `backups.enabled` config flag is off. Operator-level configuration is
// preserved across the global flip; this only blocks mutation + execution
// paths so an admin can re-enable later without surprises.
var ErrBackupsDisabled = errors.New("backups are disabled at the NIS-wide level")

// ErrBackupIntervalTooSmall mirrors the DB CHECK constraint. Surfaced
// before write so callers get a meaningful error instead of an opaque
// constraint violation.
var ErrBackupIntervalTooSmall = errors.New("backup interval must be at least 1h")

// BackupService owns per-operator scheduled backups (P12). One service per
// process. Holds the S3 client (nil when disabled — methods return
// ErrBackupsDisabled). The job handlers in job_handlers_backup.go invoke
// RunBackup; manual one-shots go through the same path with trigger="manual".
type BackupService struct {
	factory       persistence.RepositoryFactory
	exportService *ExportService
	s3            *s3backup.Client
}

// NewBackupService constructs the service. Pass a nil s3 client when
// backups.enabled=false; every public method will short-circuit with
// ErrBackupsDisabled.
func NewBackupService(factory persistence.RepositoryFactory, exportService *ExportService, s3 *s3backup.Client) *BackupService {
	return &BackupService{
		factory:       factory,
		exportService: exportService,
		s3:            s3,
	}
}

// Enabled reports whether the global S3 client is wired. False when the
// backups.enabled config flag is off (or when init failed and the runner
// is operating in degraded mode).
func (s *BackupService) Enabled() bool { return s != nil && s.s3 != nil }

// BackupSettings is the per-operator opt-in payload. nil pointers mean
// "no change" (matches the JWTPolicyUpdate convention).
type BackupSettings struct {
	Enabled        *bool
	Interval       *time.Duration
	RetentionCount *int // nil = no change; explicit *int(0) means "no retention limit"
}

// UpdateSettings is the single entry point for changing per-operator backup
// configuration. Toggles enable/disable + interval + retention atomically.
// Validates the interval against the DB CHECK floor (1h) so the caller gets
// ErrBackupIntervalTooSmall instead of a constraint violation.
func (s *BackupService) UpdateSettings(ctx context.Context, operatorID uuid.UUID, settings BackupSettings) (*entities.Operator, error) {
	if !s.Enabled() {
		return nil, ErrBackupsDisabled
	}
	if settings.Interval != nil && *settings.Interval > 0 && *settings.Interval < time.Hour {
		return nil, ErrBackupIntervalTooSmall
	}

	var result *entities.Operator
	err := s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		repo := tx.OperatorRepository()
		operator, err := repo.GetByID(ctx, operatorID)
		if err != nil {
			return err
		}
		eventType := entities.EventTypeOperatorBackupEnabled
		changed := false
		if settings.Enabled != nil && *settings.Enabled != operator.BackupEnabled {
			operator.BackupEnabled = *settings.Enabled
			if !operator.BackupEnabled {
				eventType = entities.EventTypeOperatorBackupDisabled
			}
			changed = true
		}
		if settings.Interval != nil {
			if *settings.Interval == 0 {
				operator.BackupInterval = nil
			} else {
				v := *settings.Interval
				operator.BackupInterval = &v
			}
			changed = true
		}
		if settings.RetentionCount != nil {
			v := *settings.RetentionCount
			if v <= 0 {
				operator.BackupRetention = nil
			} else {
				operator.BackupRetention = &v
			}
			changed = true
		}
		if !changed {
			result = operator
			return nil
		}
		operator.UpdatedAt = clock.Now()
		if err := repo.Update(ctx, operator); err != nil {
			return fmt.Errorf("backup: update operator: %w", err)
		}
		payload := map[string]any{
			"backup_enabled": operator.BackupEnabled,
		}
		if operator.BackupInterval != nil {
			payload["backup_interval_seconds"] = int64(operator.BackupInterval.Seconds())
		}
		if operator.BackupRetention != nil {
			payload["backup_retention_count"] = *operator.BackupRetention
		}
		if err := events.EmitTx(ctx, tx, events.Event{
			Type:         eventType,
			OperatorID:   &operator.ID,
			ResourceType: "operator",
			ResourceID:   operator.ID.String(),
			Payload:      payload,
		}); err != nil {
			return fmt.Errorf("emit %s: %w", eventType, err)
		}
		result = operator
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RunBackup exports the operator, uploads the YAML bytes to S3, inserts a
// metadata row, updates last_backup_at, enforces retention. Trigger is
// "scheduled" (sweep) or "manual" (admin button / CLI).
//
// Encryption mode is SecretsEncrypted: NKey seeds remain encrypted with
// the data encryption key. Restoring requires an NIS instance with the
// same key; the README documents this trade-off explicitly. Plaintext-
// mode with envelope-encryption is deferred to v1.1.
func (s *BackupService) RunBackup(ctx context.Context, operatorID uuid.UUID, trigger entities.BackupTriggerKind) (*entities.OperatorBackup, error) {
	if !s.Enabled() {
		return nil, ErrBackupsDisabled
	}

	start := time.Now() // duration-only; offset cancels per skill §"Time discipline"
	startedAt := clock.Now()
	log := logging.LogFromContext(ctx)

	// recordManualJobOutcome inserts a synthetic job row for manual runs
	// so they appear in the admin JobsView alongside scheduled-sweep job
	// rows. Scheduled runs already create their own rows via the substrate's
	// claim+run loop; manual runs are a sync RPC and would otherwise be
	// invisible there. The synthetic row carries the same payload shape
	// (BackupExecutePayload with trigger="manual") and goes straight to a
	// terminal status — bypassing the substrate's lifecycle is intentional
	// since the work already happened inline.
	recordManualJobOutcome := func(status entities.JobStatus, errMsg string) {
		if trigger != entities.BackupTriggerManual {
			return
		}
		s.recordManualJob(ctx, operatorID, startedAt, status, errMsg)
	}

	bytesPayload, err := s.exportService.ExportOperatorBytes(ctx, operatorID, SecretsEncrypted, FormatYAML)
	if err != nil {
		metrics.Default().RecordBackupFailed(string(trigger))
		s.emitBackupFailed(ctx, operatorID, trigger, "export", err)
		recordManualJobOutcome(entities.JobStatusDeadLettered, "export: "+err.Error())
		return nil, fmt.Errorf("backup: export: %w", err)
	}

	sum := sha256.Sum256(bytesPayload)
	sha := hex.EncodeToString(sum[:])

	takenAt := clock.Now()
	objectKey := s.s3.ObjectKey(operatorID.String(), takenAt.Format(time.RFC3339))

	uploadCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	size, err := s.s3.Put(uploadCtx, objectKey, bytes.NewReader(bytesPayload), int64(len(bytesPayload)))
	if err != nil {
		metrics.Default().RecordBackupFailed(string(trigger))
		s.emitBackupFailed(ctx, operatorID, trigger, "s3_upload", err)
		recordManualJobOutcome(entities.JobStatusDeadLettered, "s3_upload: "+err.Error())
		return nil, fmt.Errorf("backup: s3 upload: %w", err)
	}

	backup := &entities.OperatorBackup{
		ID:          uuid.New(),
		OperatorID:  operatorID,
		ObjectKey:   objectKey,
		SizeBytes:   size,
		Sha256:      sha,
		TriggerKind: trigger,
		CreatedAt:   takenAt,
	}

	var prunable []*entities.OperatorBackup
	err = s.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := tx.OperatorBackupRepository().Create(ctx, backup); err != nil {
			return fmt.Errorf("create backup row: %w", err)
		}
		opRepo := tx.OperatorRepository()
		operator, err := opRepo.GetByID(ctx, operatorID)
		if err != nil {
			return fmt.Errorf("reload operator: %w", err)
		}
		operator.LastBackupAt = &takenAt
		operator.UpdatedAt = clock.Now()
		if err := opRepo.Update(ctx, operator); err != nil {
			return fmt.Errorf("update last_backup_at: %w", err)
		}
		if operator.BackupRetention != nil && *operator.BackupRetention > 0 {
			prunable, err = tx.OperatorBackupRepository().ListPrunable(ctx, operatorID, *operator.BackupRetention)
			if err != nil {
				return fmt.Errorf("list prunable: %w", err)
			}
		}
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeOperatorBackupSucceeded,
			OperatorID:   &operatorID,
			ResourceType: "operator_backup",
			ResourceID:   backup.ID.String(),
			Payload: map[string]any{
				"object_key": backup.ObjectKey,
				"size_bytes": backup.SizeBytes,
				"sha256":     backup.Sha256,
				"trigger":    string(trigger),
			},
		})
	})
	if err != nil {
		// DB write failed AFTER the S3 PutObject succeeded. Best-effort
		// remove the orphan object so the next run doesn't see a phantom.
		// Logged at warn — the failure surfaces to the caller regardless.
		if removeErr := s.s3.Remove(context.Background(), objectKey); removeErr != nil {
			log.Warn("backup: failed to remove orphan S3 object after DB error",
				"object_key", objectKey, "error", removeErr, "db_error", err)
		}
		metrics.Default().RecordBackupFailed(string(trigger))
		s.emitBackupFailed(ctx, operatorID, trigger, "db_write", err)
		recordManualJobOutcome(entities.JobStatusDeadLettered, "db_write: "+err.Error())
		return nil, err
	}

	for _, p := range prunable {
		// S3 first — DeleteObject is idempotent on missing keys, so if the
		// DB delete fails the next sweep retries cleanly. Reverse order
		// would leave orphan S3 objects when DB delete succeeded.
		if err := s.s3.Remove(ctx, p.ObjectKey); err != nil {
			log.Warn("backup retention: failed to remove S3 object (DB row preserved for next run)",
				"object_key", p.ObjectKey, "error", err)
			continue
		}
		if err := s.factory.OperatorBackupRepository().Delete(ctx, p.ID); err != nil {
			log.Warn("backup retention: failed to delete DB row (orphan possible until next sweep)",
				"backup_id", p.ID, "object_key", p.ObjectKey, "error", err)
		}
	}

	metrics.Default().RecordBackupSucceeded(string(trigger), time.Since(start).Seconds())
	recordManualJobOutcome(entities.JobStatusSucceeded, "")
	return backup, nil
}

// recordManualJob inserts a synthetic `backup.execute` job row for a
// manual RPC-driven backup. The row is created in a terminal state
// (succeeded / dead_lettered) and never goes through the substrate's
// claim/run loop. This is the audit hook that makes manual runs
// visible in the admin JobsView alongside scheduled runs — without it
// operators would have to dig through the events table to see them.
//
// Errors here are logged and swallowed: the backup itself already
// succeeded (or already failed and was reported), so a follow-up
// record-keeping failure shouldn't change what the caller sees.
func (s *BackupService) recordManualJob(ctx context.Context, operatorID uuid.UUID, startedAt time.Time, status entities.JobStatus, lastError string) {
	payload, err := json.Marshal(BackupExecutePayload{
		OperatorID: operatorID.String(),
		Trigger:    entities.BackupTriggerManual,
	})
	if err != nil {
		logging.LogFromContext(ctx).Warn("backup: marshal manual job payload", "error", err)
		return
	}
	now := clock.Now()
	startedCopy := startedAt
	job := &entities.Job{
		ID:           uuid.New(),
		Type:         JobTypeBackupExecute,
		Payload:      payload,
		Status:       status,
		ScheduledFor: startedAt,
		Attempts:     1,
		MaxAttempts:  1,
		LastError:    lastError,
		StartedAt:    &startedCopy,
		CompletedAt:  &now,
	}
	if err := s.factory.JobRepository().Enqueue(ctx, job); err != nil {
		logging.LogFromContext(ctx).Warn("backup: record manual job row failed",
			"operator_id", operatorID, "status", status, "error", err)
	}
}

// ListBackups returns metadata rows ordered newest-first. S3 is NOT queried —
// the DB row is the source of truth.
func (s *BackupService) ListBackups(ctx context.Context, operatorID uuid.UUID) ([]*entities.OperatorBackup, error) {
	if !s.Enabled() {
		return nil, ErrBackupsDisabled
	}
	return s.factory.OperatorBackupRepository().ListByOperator(ctx, operatorID)
}

// ListBackupsPage returns one keyset-paginated page of backups visible under
// scope. Tenant narrowing happens in the repo via authz.Scope.
func (s *BackupService) ListBackupsPage(ctx context.Context, scope authz.Scope, filter repositories.OperatorBackupListFilter) ([]*entities.OperatorBackup, string, error) {
	if !s.Enabled() {
		return nil, "", ErrBackupsDisabled
	}
	return s.factory.OperatorBackupRepository().ListPage(ctx, scope, filter)
}

// GetBackup fetches one row.
func (s *BackupService) GetBackup(ctx context.Context, id uuid.UUID) (*entities.OperatorBackup, error) {
	if !s.Enabled() {
		return nil, ErrBackupsDisabled
	}
	return s.factory.OperatorBackupRepository().Get(ctx, id)
}

// DownloadBackup streams the YAML bytes from S3. Caller closes the reader.
// The returned metadata row is the same one Get would return; provided
// so callers don't need a second round-trip.
func (s *BackupService) DownloadBackup(ctx context.Context, id uuid.UUID) (io.ReadCloser, *entities.OperatorBackup, error) {
	if !s.Enabled() {
		return nil, nil, ErrBackupsDisabled
	}
	row, err := s.factory.OperatorBackupRepository().Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	rc, err := s.s3.Get(ctx, row.ObjectKey)
	if err != nil {
		return nil, nil, err
	}
	return rc, row, nil
}

// DeleteBackup removes one backup. S3 first (idempotent on miss), then DB.
// Reverse order would leave orphan objects if DB delete fails after S3.
func (s *BackupService) DeleteBackup(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	if !s.Enabled() {
		return ErrBackupsDisabled
	}
	row, err := s.factory.OperatorBackupRepository().Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.s3.Remove(ctx, row.ObjectKey); err != nil {
		return fmt.Errorf("backup: s3 remove: %w", err)
	}
	if err := s.factory.OperatorBackupRepository().Delete(ctx, id); err != nil {
		return fmt.Errorf("backup: db delete: %w", err)
	}
	// Audit is emitted with its own short tx — DeleteBackup isn't multi-write
	// and pulling EmitTx in would force a transaction we don't otherwise need.
	if err := events.EmitSystem(ctx, s.factory, events.Event{
		Type:         entities.EventTypeOperatorBackupDeleted,
		OperatorID:   &row.OperatorID,
		ResourceType: "operator_backup",
		ResourceID:   row.ID.String(),
		Payload: map[string]any{
			"object_key": row.ObjectKey,
		},
	}); err != nil {
		// Audit failure is logged but does not roll back the deletion
		// — the resource is already gone from S3 + DB.
		logging.LogFromContext(ctx).Warn("backup: emit deleted event failed", "error", err, "backup_id", id)
	}
	_ = actor // kept in signature for future per-actor audit tagging
	return nil
}

// FindDueOperators returns the operator IDs for which BackupEnabled=true and
// (last_backup_at IS NULL OR last_backup_at + interval <= now). Used by the
// sweep handler.
func (s *BackupService) FindDueOperators(ctx context.Context, now time.Time) ([]uuid.UUID, error) {
	ops, err := s.factory.OperatorRepository().List(ctx, repositories.ListOptions{Limit: 10000})
	if err != nil {
		return nil, fmt.Errorf("list operators: %w", err)
	}
	due := make([]uuid.UUID, 0, len(ops))
	for _, op := range ops {
		if !op.BackupEnabled || op.BackupInterval == nil || *op.BackupInterval <= 0 {
			continue
		}
		if op.LastBackupAt == nil || op.LastBackupAt.Add(*op.BackupInterval).Before(now) || op.LastBackupAt.Add(*op.BackupInterval).Equal(now) {
			due = append(due, op.ID)
		}
	}
	return due, nil
}

func (s *BackupService) emitBackupFailed(ctx context.Context, operatorID uuid.UUID, trigger entities.BackupTriggerKind, phase string, err error) {
	if emitErr := events.EmitSystem(ctx, s.factory, events.Event{
		Type:         entities.EventTypeOperatorBackupFailed,
		OperatorID:   &operatorID,
		ResourceType: "operator",
		ResourceID:   operatorID.String(),
		Payload: map[string]any{
			"phase":   phase,
			"trigger": string(trigger),
			"error":   err.Error(),
		},
	}); emitErr != nil {
		logging.LogFromContext(ctx).Warn("backup: emit failed event failed",
			"emit_error", emitErr, "original_error", err, "phase", phase)
	}
}
