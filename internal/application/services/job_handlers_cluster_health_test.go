package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlpkg "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// buildClusterHealthFixture spins up a real in-memory SQLite + migrations +
// a ClusterService backed by it. We use a real ClusterService (not a stub)
// because the sweep handler reaches through to repo.List — the contract
// under test is "the right rows get enqueued", not "the handler calls a
// mocked method".
func buildClusterHealthFixture(t *testing.T) (*ClusterService, persistence.RepositoryFactory) {
	t.Helper()

	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlpkg.Close(db) })

	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))

	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)

	factory := persistence.NewSQLRepositoryFactoryFromDB(db)
	jwtSvc := NewJWTService(enc)
	clusterSvc := NewClusterService(
		factory.ClusterRepository(),
		factory.OperatorRepository(),
		factory.AccountRepository(),
		factory.UserRepository(),
		factory.ScopedSigningKeyRepository(),
		enc,
		jwtSvc,
	).WithFactory(factory)
	return clusterSvc, factory
}

// seedOperatorAndClusters creates one operator and N bare cluster rows
// directly via the repos. We bypass ClusterService.CreateCluster on purpose:
// it would try to set encrypted creds + emit events + (if WithJobRunner is
// wired) eagerly enqueue, which is more surface than the sweep test needs.
func seedOperatorAndClusters(t *testing.T, factory persistence.RepositoryFactory, n int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	op := &entities.Operator{
		ID:        uuid.New(),
		Name:      "fixture-op",
		PublicKey: "OAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, factory.OperatorRepository().Create(ctx, op))

	ids := make([]uuid.UUID, n)
	for i := 0; i < n; i++ {
		c := &entities.Cluster{
			ID:         uuid.New(),
			Name:       "cluster-" + uuid.New().String()[:8],
			ServerURLs: []string{"nats://localhost:4222"},
			OperatorID: op.ID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		require.NoError(t, factory.ClusterRepository().Create(ctx, c))
		ids[i] = c.ID
	}
	return ids
}

// TestClusterHealthSweep_EnqueuesPerCluster is the load-bearing test: a
// sweep against N clusters must produce N pending cluster.health_check
// rows in the jobs table, each with the canonical dedup key. This is the
// fanout primitive the whole A15 design rests on — if it ever regresses to
// "one job for everything" or "one job per replica", multi-cluster
// deployments silently lose checks.
func TestClusterHealthSweep_EnqueuesPerCluster(t *testing.T) {
	clusterSvc, factory := buildClusterHealthFixture(t)
	ids := seedOperatorAndClusters(t, factory, 3)

	runner := NewJobRunner(factory, JobRunnerConfig{
		PollInterval:    time.Hour, // never actually ticks during the test
		ShutdownTimeout: time.Second,
	})
	RegisterClusterHealthHandlers(runner, clusterSvc, ClusterHealthHandlerConfig{
		Interval:      60 * time.Second,
		LeaseDuration: 60 * time.Second,
	})

	sweep := &clusterHealthSweepHandler{runner: runner, clusterService: clusterSvc}
	require.NoError(t, sweep.Run(context.Background(), nil))

	for _, id := range ids {
		jobs, _, err := factory.JobRepository().List(context.Background(), repositories.JobFilter{
			Types: []string{JobTypeClusterHealthCheck},
			Limit: 100,
		})
		require.NoError(t, err)
		found := false
		for _, j := range jobs {
			if j.DedupKey == clusterHealthCheckDedupKey(id) {
				found = true
				assert.Equal(t, entities.JobStatusPending, j.Status, "fresh enqueue must be pending")
				var p ClusterHealthCheckPayload
				require.NoError(t, json.Unmarshal(j.Payload, &p))
				assert.Equal(t, id.String(), p.ClusterID, "payload cluster_id must match the seeded cluster")
				break
			}
		}
		assert.True(t, found, "expected one cluster.health_check enqueued for cluster %s", id)
	}
}

// TestClusterHealthSweep_IdempotentAcrossTicks verifies the multi-replica
// safety story: two consecutive sweep runs (simulating two replicas, or
// the same replica ticking twice) must not double-enqueue. The partial
// unique index + EnsureScheduled skip-on-duplicate is what makes A3
// unnecessary; without this guarantee, two replicas would race-create
// duplicate per-cluster rows and waste claim cycles.
func TestClusterHealthSweep_IdempotentAcrossTicks(t *testing.T) {
	clusterSvc, factory := buildClusterHealthFixture(t)
	ids := seedOperatorAndClusters(t, factory, 2)

	runner := NewJobRunner(factory, JobRunnerConfig{
		PollInterval:    time.Hour,
		ShutdownTimeout: time.Second,
	})
	RegisterClusterHealthHandlers(runner, clusterSvc, ClusterHealthHandlerConfig{
		Interval:      60 * time.Second,
		LeaseDuration: 60 * time.Second,
	})
	sweep := &clusterHealthSweepHandler{runner: runner, clusterService: clusterSvc}

	// First sweep — fresh enqueues.
	require.NoError(t, sweep.Run(context.Background(), nil))
	jobs1, _, err := factory.JobRepository().List(context.Background(), repositories.JobFilter{
		Types: []string{JobTypeClusterHealthCheck},
		Limit: 100,
	})
	require.NoError(t, err)
	assert.Len(t, jobs1, len(ids), "first sweep should enqueue exactly one job per cluster")

	// Second sweep — pre-existing pending rows must be reused.
	require.NoError(t, sweep.Run(context.Background(), nil))
	jobs2, _, err := factory.JobRepository().List(context.Background(), repositories.JobFilter{
		Types: []string{JobTypeClusterHealthCheck},
		Limit: 100,
	})
	require.NoError(t, err)
	assert.Len(t, jobs2, len(ids), "second sweep must NOT create duplicate rows (partial unique index in action)")
}

// TestClusterHealthCheck_HandlesMissingCluster asserts the per-cluster
// handler tolerates a cluster deleted between sweep enqueue and claim.
// This is normal lifecycle (sweep + cluster.delete racing); without the
// nil-on-ErrNotFound branch a deleted cluster would dead-letter its
// final pending job and clutter the JobsView.
func TestClusterHealthCheck_HandlesMissingCluster(t *testing.T) {
	clusterSvc, _ := buildClusterHealthFixture(t)
	check := &clusterHealthCheckHandler{clusterService: clusterSvc}
	payload, err := json.Marshal(ClusterHealthCheckPayload{ClusterID: uuid.New().String()})
	require.NoError(t, err)
	// Cluster doesn't exist — repo returns ErrNotFound, handler must swallow.
	assert.NoError(t, check.Run(context.Background(), payload))
}

// TestClusterHealthCheck_RejectsBadPayload pins the permanent-failure
// classification on malformed payloads. Without ErrPermanentJobFailure,
// the substrate would retry MaxAttempts times (= 1, so not catastrophic
// here, but the contract still matters for forward-compat).
func TestClusterHealthCheck_RejectsBadPayload(t *testing.T) {
	clusterSvc, _ := buildClusterHealthFixture(t)
	check := &clusterHealthCheckHandler{clusterService: clusterSvc}

	err := check.Run(context.Background(), []byte("not json"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermanentJobFailure)

	bad, _ := json.Marshal(ClusterHealthCheckPayload{ClusterID: "not-a-uuid"})
	err = check.Run(context.Background(), bad)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPermanentJobFailure)
}

// TestClusterHealthHandlers_RegisterDefaults locks in the documented
// defaults so a future zero-value config doesn't silently disable the
// recurring schedule (RecurEvery=0 means "not recurring") or stretch
// the per-cluster lease beyond the sweep interval.
func TestClusterHealthHandlers_RegisterDefaults(t *testing.T) {
	clusterSvc, factory := buildClusterHealthFixture(t)
	runner := NewJobRunner(factory, JobRunnerConfig{})

	RegisterClusterHealthHandlers(runner, clusterSvc, ClusterHealthHandlerConfig{})

	runner.mu.RLock()
	sweepSpec, sweepOK := runner.handlers[JobTypeClusterHealthSweep]
	checkSpec, checkOK := runner.handlers[JobTypeClusterHealthCheck]
	runner.mu.RUnlock()

	require.True(t, sweepOK, "sweep handler must be registered")
	require.True(t, checkOK, "check handler must be registered")
	assert.Equal(t, 60*time.Second, sweepSpec.RecurEvery, "default sweep interval must be 60s")
	assert.Equal(t, 3, sweepSpec.MaxAttempts, "sweep MaxAttempts must be 3 (transient DB blips retry)")
	assert.Equal(t, AuditFailuresOnly, sweepSpec.AuditPolicy)
	assert.Equal(t, 1, checkSpec.MaxAttempts, "per-cluster MaxAttempts must be 1 (state lives on cluster row)")
	assert.Equal(t, 60*time.Second, checkSpec.LeaseDuration, "default per-cluster lease must be 60s")
	assert.Equal(t, AuditFailuresOnly, checkSpec.AuditPolicy)
}
