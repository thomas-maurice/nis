package sql

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// JobRepo implements repositories.JobRepository using GORM.
//
// Driver-aware locking in ClaimDue: Postgres uses FOR UPDATE SKIP LOCKED;
// SQLite relies on the SetMaxOpenConns(1) constraint enforced by the
// factory (panic at startup if violated). See ClaimDue for details.
type JobRepo struct {
	db *gorm.DB
}

func NewJobRepo(db *gorm.DB) *JobRepo { return &JobRepo{db: db} }

// normalizeJobTimes coerces all time fields to UTC before write. Defensive
// belt-and-suspenders for the rule documented in internal/clock: every value
// destined for storage must be UTC. clock.Now() guarantees this at the source,
// but a time.Time constructed from external input (parsed string, decoded
// JSON, etc.) won't have gone through clock.Now(); this helper traps that.
func normalizeJobTimes(m *JobModel) {
	m.ScheduledFor = m.ScheduledFor.UTC()
	m.CreatedAt = m.CreatedAt.UTC()
	m.UpdatedAt = m.UpdatedAt.UTC()
	if m.LockedUntil != nil {
		u := m.LockedUntil.UTC()
		m.LockedUntil = &u
	}
	if m.StartedAt != nil {
		u := m.StartedAt.UTC()
		m.StartedAt = &u
	}
	if m.CompletedAt != nil {
		u := m.CompletedAt.UTC()
		m.CompletedAt = &u
	}
}

// isUniqueViolation matches the partial-unique-index rejection across both
// drivers. GORM's `gorm.ErrDuplicatedKey` requires `TranslateError: true`
// in the gorm.Config, which the factory doesn't set; we sniff driver
// error strings directly. The two phrases are stable across recent
// versions of mattn/go-sqlite3 and lib/pq.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || // sqlite
		strings.Contains(s, "duplicate key value violates") // postgres
}

func (r *JobRepo) Enqueue(ctx context.Context, job *entities.Job) error {
	model := JobModelFromEntity(job)
	normalizeJobTimes(model)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if isUniqueViolation(err) {
			return repositories.ErrJobAlreadyEnqueued
		}
		return fmt.Errorf("failed to enqueue job: %w", err)
	}
	return nil
}

func (r *JobRepo) EnqueueIfAbsent(ctx context.Context, job *entities.Job) (bool, error) {
	if err := r.Enqueue(ctx, job); err != nil {
		if errors.Is(err, repositories.ErrJobAlreadyEnqueued) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ClaimDue atomically claims up to `limit` due rows for execution.
//
// Postgres path: one statement, FOR UPDATE SKIP LOCKED inside the subquery
// so concurrent claims on multiple replicas never see the same row. The
// outer UPDATE ... RETURNING gives us the fully-updated rows in one
// round-trip.
//
// SQLite path: a GORM transaction wraps the SELECT + UPDATE. The
// SetMaxOpenConns(1) constraint enforced at factory startup means
// in-process concurrent claims are serialised at the connection-pool
// level; multi-process SQLite is unsupported (SKILL §14 WAL gotcha).
func (r *JobRepo) ClaimDue(ctx context.Context, workerID string, lease time.Duration, now time.Time, limit int) ([]*entities.Job, error) {
	now = now.UTC()
	leaseUntil := now.Add(lease).UTC()
	dialect := r.db.Dialector.Name()

	switch dialect {
	case "postgres":
		return r.claimDuePostgres(ctx, workerID, leaseUntil, now, limit)
	case "sqlite", "sqlite3":
		return r.claimDueSQLite(ctx, workerID, leaseUntil, now, limit)
	default:
		return nil, fmt.Errorf("ClaimDue: unsupported dialect %q", dialect)
	}
}

func (r *JobRepo) claimDuePostgres(ctx context.Context, workerID string, leaseUntil, now time.Time, limit int) ([]*entities.Job, error) {
	// One-shot atomic claim. The subquery acquires row-level locks via
	// FOR UPDATE SKIP LOCKED so two concurrent workers can never claim
	// the same row; rows already locked by another claim are silently
	// skipped (returned to the next round).
	//
	// Candidate set: pending rows whose schedule has arrived, AND running
	// rows whose lease has expired (dead-worker recovery). The CASE on
	// last_error annotates reclaimed rows so an admin can see why attempts
	// jumped without a matching handler error.
	const q = `
UPDATE jobs SET
  status = 'running',
  locked_by = ?,
  locked_until = ?,
  started_at = ?,
  attempts = attempts + 1,
  last_error = CASE WHEN status = 'running' THEN 'lease reclaimed after timeout' ELSE last_error END,
  updated_at = ?
FROM (
  SELECT id FROM jobs
  WHERE scheduled_for <= ?
    AND (
      status = 'pending'
      OR (status = 'running' AND locked_until IS NOT NULL AND locked_until <= ?)
    )
  ORDER BY scheduled_for ASC
  LIMIT ?
  FOR UPDATE SKIP LOCKED
) s
WHERE jobs.id = s.id
RETURNING jobs.*`
	var models []JobModel
	if err := r.db.WithContext(ctx).Raw(q,
		workerID, leaseUntil, now, now,
		now, now, limit,
	).Scan(&models).Error; err != nil {
		return nil, fmt.Errorf("postgres ClaimDue: %w", err)
	}
	jobs := make([]*entities.Job, len(models))
	for i, m := range models {
		jobs[i] = m.ToEntity()
	}
	return jobs, nil
}

func (r *JobRepo) claimDueSQLite(ctx context.Context, workerID string, leaseUntil, now time.Time, limit int) ([]*entities.Job, error) {
	var claimed []*entities.Job
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Select candidates. We rely on the connection-pool-level
		// serialisation (SetMaxOpenConns=1) to keep this safe; SQLite
		// itself would also serve a consistent snapshot inside the tx.
		// Candidate set: pending rows whose schedule has arrived, AND
		// running rows whose lease has expired (dead-worker recovery).
		var models []JobModel
		if err := tx.
			Where(`scheduled_for <= ? AND (
				status = ?
				OR (status = ? AND locked_until IS NOT NULL AND locked_until <= ?)
			)`,
				now,
				string(entities.JobStatusPending),
				string(entities.JobStatusRunning), now).
			Order("scheduled_for ASC").
			Limit(limit).
			Find(&models).Error; err != nil {
			return fmt.Errorf("sqlite ClaimDue select: %w", err)
		}
		if len(models) == 0 {
			return nil
		}
		ids := make([]string, len(models))
		for i, m := range models {
			ids[i] = m.ID
		}
		// Update the same set in one statement. CASE on last_error
		// annotates reclaimed rows; otherwise the prior error stays.
		if err := tx.Model(&JobModel{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":       string(entities.JobStatusRunning),
				"locked_by":    workerID,
				"locked_until": leaseUntil,
				"started_at":   now,
				"attempts":     gorm.Expr("attempts + 1"),
				"last_error":   gorm.Expr("CASE WHEN status = ? THEN ? ELSE last_error END", string(entities.JobStatusRunning), "lease reclaimed after timeout"),
				"updated_at":   now,
			}).Error; err != nil {
			return fmt.Errorf("sqlite ClaimDue update: %w", err)
		}
		// Re-read with the new state so attempts/started_at/etc. are accurate
		// for the caller. Cheap — same rows, fresh values.
		var updated []JobModel
		if err := tx.Where("id IN ?", ids).Find(&updated).Error; err != nil {
			return fmt.Errorf("sqlite ClaimDue refetch: %w", err)
		}
		claimed = make([]*entities.Job, len(updated))
		for i, m := range updated {
			claimed[i] = m.ToEntity()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *JobRepo) MarkSucceeded(ctx context.Context, id uuid.UUID) error {
	now := clock.Now()
	result := r.db.WithContext(ctx).Model(&JobModel{}).
		Where("id = ? AND status = ?", id.String(), string(entities.JobStatusRunning)).
		Updates(map[string]any{
			"status":       string(entities.JobStatusSucceeded),
			"completed_at": now,
			"locked_by":    "",
			"locked_until": gorm.Expr("NULL"),
			"updated_at":   now,
		})
	if result.Error != nil {
		return fmt.Errorf("MarkSucceeded: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrJobInvalidStateTransition
	}
	return nil
}

func (r *JobRepo) MarkFailed(ctx context.Context, id uuid.UUID, lastError string, retryAfter *time.Time) error {
	now := clock.Now()
	updates := map[string]any{
		"last_error":   lastError,
		"locked_by":    "",
		"locked_until": gorm.Expr("NULL"),
		"updated_at":   now,
	}
	if retryAfter != nil {
		// Transient failure path: back to pending, scheduled_for set.
		updates["status"] = string(entities.JobStatusPending)
		updates["scheduled_for"] = retryAfter.UTC()
	} else {
		// Terminal failure: dead-letter.
		updates["status"] = string(entities.JobStatusDeadLettered)
		updates["completed_at"] = now
	}
	result := r.db.WithContext(ctx).Model(&JobModel{}).
		Where("id = ? AND status = ?", id.String(), string(entities.JobStatusRunning)).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("MarkFailed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrJobInvalidStateTransition
	}
	return nil
}

func (r *JobRepo) Cancel(ctx context.Context, id uuid.UUID) error {
	now := clock.Now()
	result := r.db.WithContext(ctx).Model(&JobModel{}).
		Where("id = ? AND status = ?", id.String(), string(entities.JobStatusPending)).
		Updates(map[string]any{
			"status":       string(entities.JobStatusCancelled),
			"completed_at": now,
			"updated_at":   now,
		})
	if result.Error != nil {
		return fmt.Errorf("Cancel: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrJobInvalidStateTransition
	}
	return nil
}

func (r *JobRepo) Retry(ctx context.Context, id uuid.UUID, now time.Time) error {
	now = now.UTC()
	result := r.db.WithContext(ctx).Model(&JobModel{}).
		Where("id = ? AND status IN ?", id.String(), []string{
			string(entities.JobStatusFailed),
			string(entities.JobStatusDeadLettered),
			string(entities.JobStatusCancelled),
		}).
		Updates(map[string]any{
			"status":        string(entities.JobStatusPending),
			"scheduled_for": now,
			"attempts":      0,
			"last_error":    "",
			"locked_by":     "",
			"locked_until":  gorm.Expr("NULL"),
			"started_at":    gorm.Expr("NULL"),
			"completed_at":  gorm.Expr("NULL"),
			"updated_at":    now,
		})
	if result.Error != nil {
		return fmt.Errorf("Retry: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrJobInvalidStateTransition
	}
	return nil
}

func (r *JobRepo) Get(ctx context.Context, id uuid.UUID) (*entities.Job, error) {
	var model JobModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("get job: %w", err)
	}
	return model.ToEntity(), nil
}

// jobCursor encodes the (created_at, id) pair of the last row returned so the
// next page can continue strictly after it. Tie-break on id is necessary
// because created_at has only 1-second resolution on some SQL stores.
type jobCursor struct {
	CreatedAt time.Time `json:"c"`
	ID        string    `json:"i"`
}

func encodeJobCursor(c jobCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeJobCursor(s string) (*jobCursor, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	var c jobCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	return &c, nil
}

func (r *JobRepo) List(ctx context.Context, filter repositories.JobFilter) ([]*entities.Job, string, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := r.db.WithContext(ctx).Model(&JobModel{})

	if len(filter.Types) > 0 {
		query = query.Where("type IN ?", filter.Types)
	}
	if len(filter.Statuses) > 0 {
		statuses := make([]string, len(filter.Statuses))
		for i, s := range filter.Statuses {
			statuses[i] = string(s)
		}
		query = query.Where("status IN ?", statuses)
	}
	if filter.Since != nil {
		query = query.Where("created_at >= ?", filter.Since.UTC())
	}
	if filter.Until != nil {
		query = query.Where("created_at <= ?", filter.Until.UTC())
	}

	cursor, err := decodeJobCursor(filter.Cursor)
	if err != nil {
		return nil, "", err
	}
	if cursor != nil {
		// Strictly-after on (created_at DESC, id DESC).
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt.UTC(), cursor.CreatedAt.UTC(), cursor.ID)
	}

	// Fetch one extra row to determine whether a next page exists without
	// a second COUNT query.
	var models []JobModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("list jobs: %w", err)
	}

	nextCursor := ""
	if len(models) > limit {
		last := models[limit-1]
		nextCursor = encodeJobCursor(jobCursor{CreatedAt: last.CreatedAt.UTC(), ID: last.ID})
		models = models[:limit]
	}

	jobs := make([]*entities.Job, len(models))
	for i, m := range models {
		jobs[i] = m.ToEntity()
	}
	return jobs, nextCursor, nil
}

func (r *JobRepo) DeleteCompletedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoff = cutoff.UTC()
	result := r.db.WithContext(ctx).
		Where("status IN ? AND completed_at IS NOT NULL AND completed_at < ?",
			[]string{string(entities.JobStatusSucceeded), string(entities.JobStatusCancelled)},
			cutoff).
		Delete(&JobModel{})
	if result.Error != nil {
		return 0, fmt.Errorf("DeleteCompletedBefore: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (r *JobRepo) CountByStatus(ctx context.Context, status entities.JobStatus) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&JobModel{}).
		Where("status = ?", string(status)).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("CountByStatus: %w", err)
	}
	return count, nil
}
