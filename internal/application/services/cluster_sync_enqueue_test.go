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

// buildClusterSyncEnqueueFixture spins up a real in-memory SQLite + migrations
// and returns a repo factory. No service plumbing — the enqueue helpers are
// pure functions of (ctx, tx, ...) so a bare factory is enough.
func buildClusterSyncEnqueueFixture(t *testing.T) persistence.RepositoryFactory {
	t.Helper()

	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlpkg.Close(db) })

	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))

	// An encryptor is required by NewSQLRepositoryFactoryFromDB but the
	// enqueue tests never touch encrypted columns.
	_, err = encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)

	return persistence.NewSQLRepositoryFactoryFromDB(db)
}

// seedEnqueueFixture creates one operator, one account with a stub JWT, and
// `withCreds + withoutCreds` clusters. Returns the operator/account IDs and
// the per-cluster slices for assertions.
func seedEnqueueFixture(t *testing.T, factory persistence.RepositoryFactory, withCreds, withoutCreds int) (operatorID, accountID uuid.UUID, withCredsIDs, withoutCredsIDs []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	op := &entities.Operator{
		ID:             uuid.New(),
		Name:           "fixture-op",
		PublicKey:      "OAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, factory.OperatorRepository().Create(ctx, op))

	acc := &entities.Account{
		ID:         uuid.New(),
		Name:       "fixture-account",
		PublicKey:  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		OperatorID: op.ID,
		JWT:        "stub-jwt-bytes",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	require.NoError(t, factory.AccountRepository().Create(ctx, acc))

	for i := 0; i < withCreds; i++ {
		c := &entities.Cluster{
			ID:             uuid.New(),
			Name:           "with-creds-" + uuid.New().String()[:8],
			ServerURLs:     []string{"nats://localhost:4222"},
			OperatorID:     op.ID,
			EncryptedCreds: "encrypted:test-key:fake-payload",
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		require.NoError(t, factory.ClusterRepository().Create(ctx, c))
		withCredsIDs = append(withCredsIDs, c.ID)
	}
	for i := 0; i < withoutCreds; i++ {
		c := &entities.Cluster{
			ID:         uuid.New(),
			Name:       "no-creds-" + uuid.New().String()[:8],
			ServerURLs: []string{"nats://localhost:4222"},
			OperatorID: op.ID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		require.NoError(t, factory.ClusterRepository().Create(ctx, c))
		withoutCredsIDs = append(withoutCredsIDs, c.ID)
	}
	return op.ID, acc.ID, withCredsIDs, withoutCredsIDs
}

// TestEnqueueAccountPush_OneJobPerCluster: with 3 with-creds + 2 no-creds
// clusters, EnqueueAccountPush must emit exactly 3 pending cluster.account.push
// rows. Asserts the core fan-out + the skip-no-creds invariant; if either
// breaks, every auto-sync path either over-pushes (one to nowhere) or
// under-pushes (silent drift).
func TestEnqueueAccountPush_OneJobPerCluster(t *testing.T) {
	factory := buildClusterSyncEnqueueFixture(t)
	opID, accID, withCreds, _ := seedEnqueueFixture(t, factory, 3, 2)
	ctx := context.Background()

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		acc, err := tx.AccountRepository().GetByID(ctx, accID)
		require.NoError(t, err)
		return EnqueueAccountPush(ctx, tx, opID, acc)
	})
	require.NoError(t, err)

	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Limit: 100})
	require.NoError(t, err)

	pushJobs := filterByType(jobs, JobTypeClusterAccountPush)
	require.Len(t, pushJobs, 3, "expected one push job per with-creds cluster, no jobs for no-creds clusters")

	gotClusterIDs := map[uuid.UUID]bool{}
	for _, j := range pushJobs {
		var p accountPushPayload
		require.NoError(t, json.Unmarshal(j.Payload, &p))
		gotClusterIDs[p.ClusterID] = true
		assert.Equal(t, opID, p.OperatorID)
		assert.Equal(t, accID, p.AccountID)
		assert.NotEmpty(t, p.JWTHash, "payload must capture JWT hash for staleness check")
		assert.Equal(t, accountPushDedupKey(accID, p.ClusterID), j.DedupKey)
		assert.Equal(t, accountPushMaxAttempts, j.MaxAttempts)
		assert.Equal(t, entities.JobStatusPending, j.Status)
	}
	for _, id := range withCreds {
		assert.True(t, gotClusterIDs[id], "expected job for with-creds cluster %s", id)
	}
}

// TestEnqueueAccountPush_DedupCollapsesDuplicates: calling EnqueueAccountPush
// twice with the same (account, cluster, JWT) must NOT produce duplicate rows.
// The partial unique index on (type, dedup_key) WHERE status IN ('pending',
// 'running') is what makes burst mutations idempotent — if this breaks, a
// rapid-fire UI workflow floods the jobs table.
func TestEnqueueAccountPush_DedupCollapsesDuplicates(t *testing.T) {
	factory := buildClusterSyncEnqueueFixture(t)
	opID, accID, _, _ := seedEnqueueFixture(t, factory, 1, 0)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
			acc, err := tx.AccountRepository().GetByID(ctx, accID)
			require.NoError(t, err)
			return EnqueueAccountPush(ctx, tx, opID, acc)
		})
		require.NoError(t, err)
	}

	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Limit: 100})
	require.NoError(t, err)
	pushJobs := filterByType(jobs, JobTypeClusterAccountPush)
	assert.Len(t, pushJobs, 1, "three calls with the same dedup key must collapse to one row")
}

// TestEnqueueAccountPush_NilAccountIsNoOp: nil account / empty-JWT account are
// no-ops, NOT errors. Lets caller paths use the helper unconditionally without
// pre-checking JWT state.
func TestEnqueueAccountPush_NilAccountIsNoOp(t *testing.T) {
	factory := buildClusterSyncEnqueueFixture(t)
	ctx := context.Background()

	require.NoError(t, factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return EnqueueAccountPush(ctx, tx, uuid.New(), nil)
	}))
	require.NoError(t, factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return EnqueueAccountPush(ctx, tx, uuid.New(), &entities.Account{ID: uuid.New()})
	}))

	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Limit: 100})
	require.NoError(t, err)
	assert.Empty(t, filterByType(jobs, JobTypeClusterAccountPush))
}

// TestEnqueueAccountDelete_OneJobPerCluster: same fan-out invariants as push,
// for the delete-claim path. Payload carries pubkey (not ID) because the
// account row is gone from the DB by the time the handler runs.
func TestEnqueueAccountDelete_OneJobPerCluster(t *testing.T) {
	factory := buildClusterSyncEnqueueFixture(t)
	opID, _, withCreds, _ := seedEnqueueFixture(t, factory, 2, 1)
	ctx := context.Background()

	const accountPubkey = "ATESTPUBKEYAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return EnqueueAccountDelete(ctx, tx, opID, accountPubkey)
	})
	require.NoError(t, err)

	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Limit: 100})
	require.NoError(t, err)
	delJobs := filterByType(jobs, JobTypeClusterAccountDelete)
	require.Len(t, delJobs, 2)

	gotClusterIDs := map[uuid.UUID]bool{}
	for _, j := range delJobs {
		var p accountDeletePayload
		require.NoError(t, json.Unmarshal(j.Payload, &p))
		gotClusterIDs[p.ClusterID] = true
		assert.Equal(t, opID, p.OperatorID)
		assert.Equal(t, accountPubkey, p.AccountPubkey)
		assert.Equal(t, accountDeleteDedupKey(accountPubkey, p.ClusterID), j.DedupKey)
	}
	for _, id := range withCreds {
		assert.True(t, gotClusterIDs[id], "expected delete-job for with-creds cluster %s", id)
	}
}

// TestEnqueueAccountDelete_EmptyPubkeyIsNoOp: a buggy caller passing an empty
// pubkey must not flood the queue with junk rows. Defensive guard.
func TestEnqueueAccountDelete_EmptyPubkeyIsNoOp(t *testing.T) {
	factory := buildClusterSyncEnqueueFixture(t)
	opID, _, _, _ := seedEnqueueFixture(t, factory, 1, 0)
	ctx := context.Background()

	require.NoError(t, factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return EnqueueAccountDelete(ctx, tx, opID, "")
	}))

	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Limit: 100})
	require.NoError(t, err)
	assert.Empty(t, filterByType(jobs, JobTypeClusterAccountDelete))
}

// TestAccountPushFollowupDedupKey_PerRevision: two distinct JWT hashes must
// yield distinct follow-up dedup keys; an identical hash collapses. The
// post-push staleness-race fix relies on this: a follow-up keyed on a fresh
// hash can be inserted even while the primary row is still 'running'.
func TestAccountPushFollowupDedupKey_PerRevision(t *testing.T) {
	accID := uuid.New()
	clusterID := uuid.New()
	k1 := accountPushFollowupDedupKey(accID, clusterID, hashJWT("jwt-v1"))
	k1b := accountPushFollowupDedupKey(accID, clusterID, hashJWT("jwt-v1"))
	k2 := accountPushFollowupDedupKey(accID, clusterID, hashJWT("jwt-v2"))
	assert.Equal(t, k1, k1b, "same JWT must produce identical dedup keys")
	assert.NotEqual(t, k1, k2, "distinct JWTs must produce distinct dedup keys")
}

func filterByType(jobs []*entities.Job, t string) []*entities.Job {
	out := make([]*entities.Job, 0, len(jobs))
	for _, j := range jobs {
		if j.Type == t {
			out = append(out, j)
		}
	}
	return out
}
