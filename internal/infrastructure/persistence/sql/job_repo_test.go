package sql

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

// JobRepoTestSuite exercises JobRepo in isolation against an in-memory SQLite
// DB with the real migrations applied. Each test gets a clean jobs table.
type JobRepoTestSuite struct {
	suite.Suite
	db   *gorm.DB
	repo *JobRepo
}

func (s *JobRepoTestSuite) SetupSuite() {
	db, err := NewDB("sqlite", ":memory:")
	require.NoError(s.T(), err)
	s.db = db

	sqlDB, err := db.DB()
	require.NoError(s.T(), err)

	goose.SetBaseFS(migrations.Migrations)
	require.NoError(s.T(), goose.SetDialect("sqlite3"))
	require.NoError(s.T(), goose.Up(sqlDB, "sqlite"))

	s.repo = NewJobRepo(db)
}

func (s *JobRepoTestSuite) TearDownSuite() {
	sqlDB, _ := s.db.DB()
	_ = sqlDB.Close()
}

func (s *JobRepoTestSuite) SetupTest() {
	s.db.Exec("DELETE FROM jobs")
}

func TestJobRepoSuite(t *testing.T) {
	suite.Run(t, new(JobRepoTestSuite))
}

// newJob builds a Job with sensible defaults for tests. Caller can override
// fields by mutating the returned pointer before passing it to Enqueue.
func newJob(jobType string, dedup string) *entities.Job {
	now := time.Now().UTC()
	j := &entities.Job{
		ID:           uuid.New(),
		Type:         jobType,
		Payload:      []byte(`{}`),
		Status:       entities.JobStatusPending,
		ScheduledFor: now,
		MaxAttempts:  3,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if dedup != "" {
		j.DedupKey = dedup
	}
	return j
}

// TestEnqueueAndGet_RoundTrip verifies every field survives the
// FromEntity → DB → ToEntity round-trip, including the nullable DedupKey
// and the empty-payload normalization to '{}'.
func (s *JobRepoTestSuite) TestEnqueueAndGet_RoundTrip() {
	ctx := context.Background()
	j := newJob("test.alpha", "k1")
	j.Payload = []byte(`{"x":1}`)

	require.NoError(s.T(), s.repo.Enqueue(ctx, j))

	got, err := s.repo.Get(ctx, j.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), j.Type, got.Type)
	assert.Equal(s.T(), j.Status, got.Status)
	assert.Equal(s.T(), j.DedupKey, got.DedupKey)
	assert.Equal(s.T(), j.MaxAttempts, got.MaxAttempts)
	assert.JSONEq(s.T(), `{"x":1}`, string(got.Payload))
}

func (s *JobRepoTestSuite) TestGet_NotFound() {
	_, err := s.repo.Get(context.Background(), uuid.New())
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestEnqueue_PartialUniqueIndex_RejectsDuplicateWhilePending is the central
// dedup contract: two rows with the same (type, dedup_key) cannot both be
// pending. The second enqueue gets ErrJobAlreadyEnqueued.
func (s *JobRepoTestSuite) TestEnqueue_PartialUniqueIndex_RejectsDuplicateWhilePending() {
	ctx := context.Background()
	first := newJob("dedup.test", "singleton")
	require.NoError(s.T(), s.repo.Enqueue(ctx, first))

	second := newJob("dedup.test", "singleton")
	err := s.repo.Enqueue(ctx, second)
	assert.ErrorIs(s.T(), err, repositories.ErrJobAlreadyEnqueued)
}

// TestEnqueue_PartialUniqueIndex_AllowsAfterTerminal proves the WHERE clause
// on the unique index — once the first row is in a terminal state, the same
// (type, dedup_key) can be enqueued again. This is what makes
// "self-re-enqueue at the end of a successful handler" work for the
// recurring-schedule pattern.
func (s *JobRepoTestSuite) TestEnqueue_PartialUniqueIndex_AllowsAfterTerminal() {
	ctx := context.Background()
	first := newJob("recur.test", "singleton")
	require.NoError(s.T(), s.repo.Enqueue(ctx, first))

	// Claim and succeed the first one so it's no longer pending/running.
	claimed, err := s.repo.ClaimDue(ctx, "w1", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimed, 1)
	require.NoError(s.T(), s.repo.MarkSucceeded(ctx, claimed[0].ID))

	// Now a fresh enqueue with the same dedup key must succeed.
	second := newJob("recur.test", "singleton")
	assert.NoError(s.T(), s.repo.Enqueue(ctx, second))
}

// TestEnqueue_NullDedupKey_AllowsMultiple verifies "no dedup_key = no dedup".
// SQL UNIQUE treats NULLs as distinct, so two NULL-dedup rows of the same
// type can coexist — that's the one-shot job path.
func (s *JobRepoTestSuite) TestEnqueue_NullDedupKey_AllowsMultiple() {
	ctx := context.Background()
	require.NoError(s.T(), s.repo.Enqueue(ctx, newJob("oneshot", "")))
	require.NoError(s.T(), s.repo.Enqueue(ctx, newJob("oneshot", "")))
	require.NoError(s.T(), s.repo.Enqueue(ctx, newJob("oneshot", "")))

	count, err := s.repo.CountByStatus(ctx, entities.JobStatusPending)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), int64(3), count)
}

// TestEnqueueIfAbsent_SkipsDuplicate verifies the watchdog path: a per-tick
// EnsureScheduled call must NOT raise an error when the row already exists.
func (s *JobRepoTestSuite) TestEnqueueIfAbsent_SkipsDuplicate() {
	ctx := context.Background()
	first := newJob("watchdog.test", "singleton")
	inserted, err := s.repo.EnqueueIfAbsent(ctx, first)
	require.NoError(s.T(), err)
	assert.True(s.T(), inserted)

	second := newJob("watchdog.test", "singleton")
	inserted, err = s.repo.EnqueueIfAbsent(ctx, second)
	require.NoError(s.T(), err)
	assert.False(s.T(), inserted)
}

// TestClaimDue_HappyPath claims an eligible row and verifies the side effects:
// status flips to 'running', attempts increments, lease + worker ID + started_at
// are set.
func (s *JobRepoTestSuite) TestClaimDue_HappyPath() {
	ctx := context.Background()
	j := newJob("claim.alpha", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))

	claimed, err := s.repo.ClaimDue(ctx, "worker-1", 30*time.Second, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimed, 1)
	got := claimed[0]
	assert.Equal(s.T(), entities.JobStatusRunning, got.Status)
	assert.Equal(s.T(), 1, got.Attempts)
	assert.Equal(s.T(), "worker-1", got.LockedBy)
	require.NotNil(s.T(), got.LockedUntil)
	require.NotNil(s.T(), got.StartedAt)
}

// TestClaimDue_RespectsScheduledFor: a row scheduled in the future must not
// be claimed.
func (s *JobRepoTestSuite) TestClaimDue_RespectsScheduledFor() {
	ctx := context.Background()
	j := newJob("delayed", "")
	j.ScheduledFor = time.Now().UTC().Add(1 * time.Hour)
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))

	claimed, err := s.repo.ClaimDue(ctx, "w1", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), claimed)
}

// TestClaimDue_DoesNotDoubleClaim: a row already claimed by worker A must
// not appear in worker B's claim while the lease is alive.
func (s *JobRepoTestSuite) TestClaimDue_DoesNotDoubleClaim() {
	ctx := context.Background()
	j := newJob("contended", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))

	claimedA, err := s.repo.ClaimDue(ctx, "w-a", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimedA, 1)

	claimedB, err := s.repo.ClaimDue(ctx, "w-b", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), claimedB)
}

// TestClaimDue_ReclaimsLeaseExpired: dead-worker recovery — when a row's
// locked_until is in the past, the next claim reclaims it and increments
// attempts again. This is how a worker crash mid-handler gets retried.
func (s *JobRepoTestSuite) TestClaimDue_ReclaimsLeaseExpired() {
	ctx := context.Background()
	j := newJob("reclaim", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))

	// First claim — pretend the worker died.
	now := time.Now().UTC()
	first, err := s.repo.ClaimDue(ctx, "dead-worker", time.Minute, now, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), first, 1)
	assert.Equal(s.T(), 1, first[0].Attempts)

	// Now simulate "time has passed beyond the lease" by claiming with a
	// 'now' value that's after the original lease_until.
	futureNow := now.Add(2 * time.Minute)
	second, err := s.repo.ClaimDue(ctx, "live-worker", time.Minute, futureNow, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), second, 1)
	assert.Equal(s.T(), 2, second[0].Attempts, "attempts must increment on lease reclaim")
	assert.Equal(s.T(), "live-worker", second[0].LockedBy)
}

// TestClaimDue_ReclaimAnnotatesLastError: when a row is reclaimed from a
// dead worker, last_error is set to the canonical reclaim string so an
// admin can distinguish "handler raised an error" from "worker died". The
// SKILL §2 / reviewer asked for this annotation specifically.
func (s *JobRepoTestSuite) TestClaimDue_ReclaimAnnotatesLastError() {
	ctx := context.Background()
	j := newJob("reclaim-annotate", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))

	now := time.Now().UTC()
	first, err := s.repo.ClaimDue(ctx, "dead", time.Minute, now, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), first, 1)
	assert.Equal(s.T(), "", first[0].LastError, "fresh claim has no last_error")

	futureNow := now.Add(2 * time.Minute)
	second, err := s.repo.ClaimDue(ctx, "live", time.Minute, futureNow, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), second, 1)
	assert.Equal(s.T(), "lease reclaimed after timeout", second[0].LastError)
}

// TestClaimDue_OrdersByScheduledFor: when more rows are due than `limit`,
// the earliest-scheduled rows are claimed first. Matters for fairness.
func (s *JobRepoTestSuite) TestClaimDue_OrdersByScheduledFor() {
	ctx := context.Background()
	now := time.Now().UTC()
	earlier := newJob("ordered", "a")
	earlier.ScheduledFor = now.Add(-2 * time.Second)
	later := newJob("ordered", "b")
	later.ScheduledFor = now.Add(-1 * time.Second)
	require.NoError(s.T(), s.repo.Enqueue(ctx, later))
	require.NoError(s.T(), s.repo.Enqueue(ctx, earlier))

	claimed, err := s.repo.ClaimDue(ctx, "w", time.Minute, now, 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimed, 1)
	assert.Equal(s.T(), "a", claimed[0].DedupKey, "earlier-scheduled row must be claimed first")
}

func (s *JobRepoTestSuite) TestMarkSucceeded_HappyPath() {
	ctx := context.Background()
	j := newJob("succeed", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	claimed, err := s.repo.ClaimDue(ctx, "w", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimed, 1)

	require.NoError(s.T(), s.repo.MarkSucceeded(ctx, claimed[0].ID))

	got, err := s.repo.Get(ctx, claimed[0].ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), entities.JobStatusSucceeded, got.Status)
	require.NotNil(s.T(), got.CompletedAt)
	assert.Equal(s.T(), "", got.LockedBy)
	assert.Nil(s.T(), got.LockedUntil)
}

// TestMarkSucceeded_RejectsNonRunning protects against double-completion or
// completing a row that was already cancelled/timed-out.
func (s *JobRepoTestSuite) TestMarkSucceeded_RejectsNonRunning() {
	ctx := context.Background()
	j := newJob("not-running", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	// j is pending, not running — MarkSucceeded must refuse.
	err := s.repo.MarkSucceeded(ctx, j.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrJobInvalidStateTransition)
}

func (s *JobRepoTestSuite) TestMarkFailed_Retry() {
	ctx := context.Background()
	j := newJob("retry-me", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	claimed, err := s.repo.ClaimDue(ctx, "w", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimed, 1)

	retryAt := time.Now().UTC().Add(30 * time.Second)
	require.NoError(s.T(), s.repo.MarkFailed(ctx, claimed[0].ID, "transient error", &retryAt))

	got, err := s.repo.Get(ctx, claimed[0].ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), entities.JobStatusPending, got.Status, "transient failure goes back to pending")
	assert.Equal(s.T(), "transient error", got.LastError)
	assert.Equal(s.T(), "", got.LockedBy)
	// scheduled_for moved forward
	assert.WithinDuration(s.T(), retryAt, got.ScheduledFor, 100*time.Millisecond)
}

func (s *JobRepoTestSuite) TestMarkFailed_DeadLetter() {
	ctx := context.Background()
	j := newJob("dead", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	claimed, err := s.repo.ClaimDue(ctx, "w", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimed, 1)

	require.NoError(s.T(), s.repo.MarkFailed(ctx, claimed[0].ID, "fatal", nil))

	got, err := s.repo.Get(ctx, claimed[0].ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), entities.JobStatusDeadLettered, got.Status)
	require.NotNil(s.T(), got.CompletedAt, "dead-lettered rows record completed_at")
	assert.Equal(s.T(), "fatal", got.LastError)
}

func (s *JobRepoTestSuite) TestCancel_OnlyPending() {
	ctx := context.Background()
	j := newJob("cancel-me", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))

	require.NoError(s.T(), s.repo.Cancel(ctx, j.ID))

	got, err := s.repo.Get(ctx, j.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), entities.JobStatusCancelled, got.Status)
	require.NotNil(s.T(), got.CompletedAt)

	// Second cancel must fail — already cancelled.
	err = s.repo.Cancel(ctx, j.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrJobInvalidStateTransition)
}

func (s *JobRepoTestSuite) TestCancel_RejectsRunning() {
	ctx := context.Background()
	j := newJob("running-cancel", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	_, err := s.repo.ClaimDue(ctx, "w", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	// Now running — cancel must refuse so we don't lose work in flight.
	err = s.repo.Cancel(ctx, j.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrJobInvalidStateTransition)
}

// TestRetry_ResetsAttempts confirms the admin "give this job another go"
// semantics: attempts goes back to 0, scheduled_for is now, lease cleared.
func (s *JobRepoTestSuite) TestRetry_ResetsAttempts() {
	ctx := context.Background()
	j := newJob("retry-admin", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	claimed, err := s.repo.ClaimDue(ctx, "w", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), claimed, 1)
	require.NoError(s.T(), s.repo.MarkFailed(ctx, claimed[0].ID, "fatal", nil))

	require.NoError(s.T(), s.repo.Retry(ctx, claimed[0].ID, time.Now().UTC()))

	got, err := s.repo.Get(ctx, claimed[0].ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), entities.JobStatusPending, got.Status)
	assert.Equal(s.T(), 0, got.Attempts)
	assert.Equal(s.T(), "", got.LastError)
	assert.Nil(s.T(), got.StartedAt)
	assert.Nil(s.T(), got.CompletedAt)
}

// TestRetry_RejectsPending: retrying a row that's already pending or
// running is a no-op error — caller probably has a bug.
func (s *JobRepoTestSuite) TestRetry_RejectsPending() {
	ctx := context.Background()
	j := newJob("retry-bad", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	err := s.repo.Retry(ctx, j.ID, time.Now().UTC())
	assert.ErrorIs(s.T(), err, repositories.ErrJobInvalidStateTransition)
}

// TestList_FiltersByTypeAndStatus exercises the filter + pagination shape.
func (s *JobRepoTestSuite) TestList_FiltersByTypeAndStatus() {
	ctx := context.Background()
	// Insert a mix.
	for i := 0; i < 3; i++ {
		require.NoError(s.T(), s.repo.Enqueue(ctx, newJob("type.a", "")))
	}
	for i := 0; i < 2; i++ {
		require.NoError(s.T(), s.repo.Enqueue(ctx, newJob("type.b", "")))
	}

	// All type.a
	got, _, err := s.repo.List(ctx, repositories.JobFilter{Types: []string{"type.a"}})
	require.NoError(s.T(), err)
	assert.Len(s.T(), got, 3)

	// All pending
	got, _, err = s.repo.List(ctx, repositories.JobFilter{
		Statuses: []entities.JobStatus{entities.JobStatusPending},
	})
	require.NoError(s.T(), err)
	assert.Len(s.T(), got, 5)
}

// TestList_CursorPagination: insert enough rows to exceed one page, then
// walk forward using the returned cursor.
func (s *JobRepoTestSuite) TestList_CursorPagination() {
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		j := newJob("paged", "")
		// Spread out created_at so the (created_at DESC, id DESC) ordering
		// is deterministic — sleeping isn't reliable enough at sub-second.
		j.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Second)
		require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	}

	// Page 1: limit 3
	first, cur1, err := s.repo.List(ctx, repositories.JobFilter{
		Types: []string{"paged"},
		Limit: 3,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), first, 3)
	require.NotEmpty(s.T(), cur1)

	// Page 2: limit 3
	second, cur2, err := s.repo.List(ctx, repositories.JobFilter{
		Types:  []string{"paged"},
		Limit:  3,
		Cursor: cur1,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), second, 3)
	require.NotEmpty(s.T(), cur2)

	// Pages must not overlap.
	seen := map[uuid.UUID]bool{}
	for _, j := range first {
		seen[j.ID] = true
	}
	for _, j := range second {
		assert.False(s.T(), seen[j.ID], "page 2 returned a row from page 1")
	}

	// Page 3: one row left, no further cursor.
	third, cur3, err := s.repo.List(ctx, repositories.JobFilter{
		Types:  []string{"paged"},
		Limit:  3,
		Cursor: cur2,
	})
	require.NoError(s.T(), err)
	assert.Len(s.T(), third, 1)
	assert.Equal(s.T(), "", cur3, "last page returns no cursor")
}

func (s *JobRepoTestSuite) TestList_InvalidCursorRejected() {
	_, _, err := s.repo.List(context.Background(), repositories.JobFilter{Cursor: "not-base64-json"})
	assert.Error(s.T(), err)
}

// TestDeleteCompletedBefore_OnlySucceededOrCancelled is the retention-sweep
// safety: failed and dead-lettered rows must survive the sweep so an
// operator can audit them after the fact.
func (s *JobRepoTestSuite) TestDeleteCompletedBefore_OnlySucceededOrCancelled() {
	ctx := context.Background()

	// 1: succeed
	jSucc := newJob("retain", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, jSucc))
	c, _ := s.repo.ClaimDue(ctx, "w", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), s.repo.MarkSucceeded(ctx, c[0].ID))

	// 2: cancel
	jCanc := newJob("retain", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, jCanc))
	require.NoError(s.T(), s.repo.Cancel(ctx, jCanc.ID))

	// 3: dead-letter (must survive)
	jDead := newJob("retain", "")
	require.NoError(s.T(), s.repo.Enqueue(ctx, jDead))
	c2, _ := s.repo.ClaimDue(ctx, "w", time.Minute, time.Now().UTC(), 10)
	require.NoError(s.T(), s.repo.MarkFailed(ctx, c2[0].ID, "fatal", nil))

	// Bump completed_at into the past so the cutoff catches them.
	past := time.Now().UTC().Add(-1 * time.Hour)
	s.db.Model(&JobModel{}).Where("completed_at IS NOT NULL").Update("completed_at", past)

	deleted, err := s.repo.DeleteCompletedBefore(ctx, time.Now().UTC())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), int64(2), deleted, "succeeded + cancelled deleted; dead_lettered survives")

	// Confirm dead-letter row is still queryable.
	got, err := s.repo.Get(ctx, jDead.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), entities.JobStatusDeadLettered, got.Status)
}

func (s *JobRepoTestSuite) TestCountByStatus() {
	ctx := context.Background()
	require.NoError(s.T(), s.repo.Enqueue(ctx, newJob("count", "")))
	require.NoError(s.T(), s.repo.Enqueue(ctx, newJob("count", "")))

	n, err := s.repo.CountByStatus(ctx, entities.JobStatusPending)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), int64(2), n)

	n, err = s.repo.CountByStatus(ctx, entities.JobStatusSucceeded)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), int64(0), n)
}

// TestNormalizeJobTimes_ForcesUTC: a payload constructed in a non-UTC
// timezone must be stored as UTC. This is the SKILL §2 defensive layer.
func (s *JobRepoTestSuite) TestNormalizeJobTimes_ForcesUTC() {
	ctx := context.Background()
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(s.T(), err)
	j := newJob("tz", "")
	j.ScheduledFor = time.Date(2026, 5, 20, 12, 0, 0, 0, loc) // not UTC

	require.NoError(s.T(), s.repo.Enqueue(ctx, j))
	got, err := s.repo.Get(ctx, j.ID)
	require.NoError(s.T(), err)
	// The retrieved time must equal the original *instant* and be expressible in UTC
	// without any non-UTC offset surviving the round-trip.
	assert.True(s.T(), got.ScheduledFor.Equal(j.ScheduledFor.UTC()))
	_, offset := got.ScheduledFor.Zone()
	assert.Equal(s.T(), 0, offset, "stored time must be UTC")
}

// Sanity check: Cancel on non-existent ID returns invalid-transition (not
// not-found). The repo treats "no row matched the WHERE clause" uniformly.
func (s *JobRepoTestSuite) TestCancel_NonExistentReturnsInvalidTransition() {
	err := s.repo.Cancel(context.Background(), uuid.New())
	assert.True(s.T(), errors.Is(err, repositories.ErrJobInvalidStateTransition))
}
