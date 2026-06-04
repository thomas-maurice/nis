package services

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
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

// fakeAccountSyncer satisfies the clusterAccountSyncer interface and records
// what was called. The push side optionally returns an error to drive
// transient/permanent classification paths.
type fakeAccountSyncer struct {
	pushCount   atomic.Int32
	deleteCount atomic.Int32
	pushErr     error
	deleteErr   error
	lastPushed  atomic.Pointer[entities.Account]
}

func (f *fakeAccountSyncer) PushAccountToCluster(_ context.Context, _ uuid.UUID, account *entities.Account) error {
	f.pushCount.Add(1)
	if account != nil {
		copy := *account
		f.lastPushed.Store(&copy)
	}
	return f.pushErr
}

func (f *fakeAccountSyncer) DeleteAccountFromCluster(_ context.Context, _, _ uuid.UUID, _ string) error {
	f.deleteCount.Add(1)
	return f.deleteErr
}

func buildAccountPushHandlerFixture(t *testing.T) (persistence.RepositoryFactory, *fakeAccountSyncer, *clusterAccountPushHandler, *clusterAccountDeleteHandler) {
	t.Helper()

	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlpkg.Close(db) })

	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))

	_, err = encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)

	factory := persistence.NewSQLRepositoryFactoryFromDB(db)
	syncer := &fakeAccountSyncer{}
	push := &clusterAccountPushHandler{
		factory:  factory,
		syncer:   syncer,
		registry: &handlerRegistry{maxAttempts: accountPushMaxAttempts},
	}
	del := &clusterAccountDeleteHandler{factory: factory, syncer: syncer}
	return factory, syncer, push, del
}

func seedHandlerFixture(t *testing.T, factory persistence.RepositoryFactory, withCreds bool, jwt string) (operatorID, accountID, clusterID uuid.UUID, accountPubkey string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	op := &entities.Operator{
		ID: uuid.New(), Name: "h-op",
		PublicKey:      "OAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
		CreatedAt:      now, UpdatedAt: now,
	}
	require.NoError(t, factory.OperatorRepository().Create(ctx, op))

	acc := &entities.Account{
		ID: uuid.New(), Name: "h-acct",
		PublicKey:  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		OperatorID: op.ID,
		JWT:        jwt,
		CreatedAt:  now, UpdatedAt: now,
	}
	require.NoError(t, factory.AccountRepository().Create(ctx, acc))

	c := &entities.Cluster{
		ID: uuid.New(), Name: "h-cluster",
		ServerURLs: []string{"nats://localhost:4222"},
		OperatorID: op.ID,
		CreatedAt:  now, UpdatedAt: now,
	}
	if withCreds {
		c.EncryptedCreds = "encrypted:test-key:fake"
	}
	require.NoError(t, factory.ClusterRepository().Create(ctx, c))

	return op.ID, acc.ID, c.ID, acc.PublicKey
}

func marshalPushPayload(t *testing.T, opID, accID, clusterID uuid.UUID, jwt string) []byte {
	t.Helper()
	body, err := json.Marshal(accountPushPayload{
		OperatorID: opID, AccountID: accID, ClusterID: clusterID, JWTHash: hashJWT(jwt),
	})
	require.NoError(t, err)
	return body
}

// TestClusterAccountPushHandler_HappyPath: a valid payload + populated DB
// state ⇒ the handler calls PushAccountToCluster once, returns nil, and
// emits the cluster.account.synced event with trigger:"auto". This pins the
// success contract every auto-sync path lights up.
func TestClusterAccountPushHandler_HappyPath(t *testing.T) {
	factory, syncer, push, _ := buildAccountPushHandlerFixture(t)
	opID, accID, clusterID, _ := seedHandlerFixture(t, factory, true, "fresh-jwt-v1")
	ctx := context.Background()

	err := push.Run(ctx, marshalPushPayload(t, opID, accID, clusterID, "fresh-jwt-v1"))
	require.NoError(t, err)
	assert.EqualValues(t, 1, syncer.pushCount.Load())

	res, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 10})
	require.NoError(t, err)
	var found bool
	for _, e := range res.Events {
		if e.Type == entities.EventTypeClusterAccountSynced {
			found = true
			var payload map[string]any
			require.NoError(t, json.Unmarshal(e.Payload, &payload))
			assert.Equal(t, "auto", payload["trigger"], "auto-sync events must carry trigger:auto")
			assert.Equal(t, clusterID.String(), payload["cluster_id"])
			assert.Equal(t, accID.String(), payload["account_id"])
		}
	}
	assert.True(t, found, "expected cluster.account.synced event")
}

// TestClusterAccountPushHandler_ClusterGone: cluster deleted between enqueue
// and claim ⇒ handler returns nil, no push attempted. Normal lifecycle, not
// failure; substrate must not retry.
func TestClusterAccountPushHandler_ClusterGone(t *testing.T) {
	factory, syncer, push, _ := buildAccountPushHandlerFixture(t)
	opID, accID, _, _ := seedHandlerFixture(t, factory, true, "jwt")
	ctx := context.Background()

	missingClusterID := uuid.New()
	err := push.Run(ctx, marshalPushPayload(t, opID, accID, missingClusterID, "jwt"))
	require.NoError(t, err)
	assert.EqualValues(t, 0, syncer.pushCount.Load())
}

// TestClusterAccountPushHandler_AccountGone: account deleted between enqueue
// and claim ⇒ nil (the delete-job path handles resolver cleanup; nothing to
// push here).
func TestClusterAccountPushHandler_AccountGone(t *testing.T) {
	factory, syncer, push, _ := buildAccountPushHandlerFixture(t)
	opID, _, clusterID, _ := seedHandlerFixture(t, factory, true, "jwt")
	ctx := context.Background()

	missingAccountID := uuid.New()
	err := push.Run(ctx, marshalPushPayload(t, opID, missingAccountID, clusterID, "jwt"))
	require.NoError(t, err)
	assert.EqualValues(t, 0, syncer.pushCount.Load())
}

// TestClusterAccountPushHandler_NoCreds: cluster has no EncryptedCreds ⇒
// short-circuit nil. Same as today's PushAccountToAllClusters skip semantic.
func TestClusterAccountPushHandler_NoCreds(t *testing.T) {
	factory, syncer, push, _ := buildAccountPushHandlerFixture(t)
	opID, accID, clusterID, _ := seedHandlerFixture(t, factory, false, "jwt") // withCreds=false
	ctx := context.Background()

	err := push.Run(ctx, marshalPushPayload(t, opID, accID, clusterID, "jwt"))
	require.NoError(t, err)
	assert.EqualValues(t, 0, syncer.pushCount.Load())
}

// TestClusterAccountPushHandler_EmptyJWTIsPermanent: account.JWT == "" is a
// code bug, not lifecycle. Return ErrPermanentJobFailure so JobsView surfaces
// it for human investigation instead of burning MaxAttempts retries.
func TestClusterAccountPushHandler_EmptyJWTIsPermanent(t *testing.T) {
	factory, _, push, _ := buildAccountPushHandlerFixture(t)
	opID, accID, clusterID, _ := seedHandlerFixture(t, factory, true, "") // empty JWT
	ctx := context.Background()

	err := push.Run(ctx, marshalPushPayload(t, opID, accID, clusterID, ""))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentJobFailure), "empty-JWT must dead-letter immediately")
}

// TestClusterAccountPushHandler_PushFailsTransient: PushAccountToCluster
// returns an error → handler returns a non-nil non-permanent error so the
// substrate retries with backoff.
func TestClusterAccountPushHandler_PushFailsTransient(t *testing.T) {
	factory, syncer, push, _ := buildAccountPushHandlerFixture(t)
	opID, accID, clusterID, _ := seedHandlerFixture(t, factory, true, "jwt")
	syncer.pushErr = errors.New("nats dial timeout")
	ctx := context.Background()

	err := push.Run(ctx, marshalPushPayload(t, opID, accID, clusterID, "jwt"))
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrPermanentJobFailure), "transient errors must NOT be permanent")
}

// TestClusterAccountPushHandler_MalformedPayloadPermanent: garbage payload ⇒
// permanent failure. Otherwise the substrate would retry MaxAttempts times on
// a row that's structurally unusable.
func TestClusterAccountPushHandler_MalformedPayloadPermanent(t *testing.T) {
	factory, _, push, _ := buildAccountPushHandlerFixture(t)
	_, _, _, _ = seedHandlerFixture(t, factory, true, "jwt")
	ctx := context.Background()

	err := push.Run(ctx, []byte("not json"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentJobFailure))
}

// TestClusterAccountPushHandler_PostPushStalenessEnqueuesFollowup proves
// the staleness-race fix end-to-end. We use a fake syncer with a "mutation
// hook" that flips the account JWT between the handler's pre-push read and
// its post-push re-read, simulating mutation B committing while J1 is
// 'running'. The handler must detect the drift via JWT-bytes comparison and
// enqueue a follow-up keyed on the new JWT hash (which the partial unique
// index permits even while the parent row is 'running' because the dedup
// key differs by hash suffix).
func TestClusterAccountPushHandler_PostPushStalenessEnqueuesFollowup(t *testing.T) {
	factory, syncer, push, _ := buildAccountPushHandlerFixture(t)
	opID, accID, clusterID, _ := seedHandlerFixture(t, factory, true, "jwt-v1")
	ctx := context.Background()

	// Inject a syncer wrapper that mutates the DB between read and re-read
	// — runs synchronously inside PushAccountToCluster's call frame so the
	// timing is deterministic.
	mutating := &mutatingSyncer{
		base: syncer,
		hook: func() {
			acc, _ := factory.AccountRepository().GetByID(ctx, accID)
			acc.JWT = "jwt-v2"
			acc.UpdatedAt = time.Now().UTC()
			_ = factory.AccountRepository().Update(ctx, acc)
		},
	}
	push.syncer = mutating

	err := push.Run(ctx, marshalPushPayload(t, opID, accID, clusterID, "jwt-v1"))
	require.NoError(t, err)
	assert.EqualValues(t, 1, syncer.pushCount.Load(), "primary push happens once")

	// The handler's post-push re-read sees jwt-v2 differs from the v1 it
	// pushed → follow-up enqueue with the v2 hash in the dedup key.
	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Limit: 100})
	require.NoError(t, err)
	var foundFollowup bool
	expectedDedup := accountPushFollowupDedupKey(accID, clusterID, hashJWT("jwt-v2"))
	for _, j := range jobs {
		if j.DedupKey == expectedDedup {
			foundFollowup = true
			break
		}
	}
	assert.True(t, foundFollowup, "expected follow-up enqueue with dedup key %q", expectedDedup)
}

// TestClusterAccountPushHandler_NoStalenessNoFollowup pins the negative
// case: when the JWT does NOT change between pre-push and post-push, the
// handler must NOT enqueue a spurious follow-up.
func TestClusterAccountPushHandler_NoStalenessNoFollowup(t *testing.T) {
	factory, syncer, push, _ := buildAccountPushHandlerFixture(t)
	opID, accID, clusterID, _ := seedHandlerFixture(t, factory, true, "stable-jwt")
	ctx := context.Background()

	err := push.Run(ctx, marshalPushPayload(t, opID, accID, clusterID, "stable-jwt"))
	require.NoError(t, err)
	assert.EqualValues(t, 1, syncer.pushCount.Load())

	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Limit: 100})
	require.NoError(t, err)
	for _, j := range jobs {
		assert.NotContains(t, j.DedupKey, "account-push-followup:",
			"no follow-up should be enqueued when pre-push and post-push JWT match")
	}
}

// mutatingSyncer wraps a fakeAccountSyncer and runs hook() inside
// PushAccountToCluster, before the underlying push records. Lets the
// staleness test mutate DB state at a deterministic point in the handler's
// execution.
type mutatingSyncer struct {
	base *fakeAccountSyncer
	hook func()
}

func (m *mutatingSyncer) PushAccountToCluster(ctx context.Context, clusterID uuid.UUID, account *entities.Account) error {
	if m.hook != nil {
		m.hook()
	}
	return m.base.PushAccountToCluster(ctx, clusterID, account)
}

func (m *mutatingSyncer) DeleteAccountFromCluster(ctx context.Context, clusterID, operatorID uuid.UUID, accountPubkey string) error {
	return m.base.DeleteAccountFromCluster(ctx, clusterID, operatorID, accountPubkey)
}

// TestClusterAccountDeleteHandler_HappyPath: valid payload ⇒ syncer's
// DeleteAccountFromCluster called once, success event emitted.
func TestClusterAccountDeleteHandler_HappyPath(t *testing.T) {
	factory, syncer, _, del := buildAccountPushHandlerFixture(t)
	opID, _, clusterID, accPubkey := seedHandlerFixture(t, factory, true, "jwt")
	ctx := context.Background()

	body, err := json.Marshal(accountDeletePayload{
		OperatorID: opID, ClusterID: clusterID, AccountPubkey: accPubkey,
	})
	require.NoError(t, err)
	require.NoError(t, del.Run(ctx, body))
	assert.EqualValues(t, 1, syncer.deleteCount.Load())

	res, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 10})
	require.NoError(t, err)
	var found bool
	for _, e := range res.Events {
		if e.Type == entities.EventTypeClusterAccountDeletedFromResolver {
			found = true
		}
	}
	assert.True(t, found, "expected cluster.account.deleted_from_resolver event")
}

// TestClusterAccountDeleteHandler_ClusterGone: same as push — deleted cluster
// is normal lifecycle, return nil.
func TestClusterAccountDeleteHandler_ClusterGone(t *testing.T) {
	factory, syncer, _, del := buildAccountPushHandlerFixture(t)
	opID, _, _, accPubkey := seedHandlerFixture(t, factory, true, "jwt")
	ctx := context.Background()

	body, err := json.Marshal(accountDeletePayload{
		OperatorID: opID, ClusterID: uuid.New(), AccountPubkey: accPubkey,
	})
	require.NoError(t, err)
	require.NoError(t, del.Run(ctx, body))
	assert.EqualValues(t, 0, syncer.deleteCount.Load())
}

// TestClusterAccountDeleteHandler_MalformedPermanent: garbage payload ⇒
// permanent failure.
func TestClusterAccountDeleteHandler_MalformedPermanent(t *testing.T) {
	factory, _, _, del := buildAccountPushHandlerFixture(t)
	_, _, _, _ = seedHandlerFixture(t, factory, true, "jwt")
	ctx := context.Background()

	err := del.Run(ctx, []byte("not json"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentJobFailure))
}

// TestRegisterClusterAccountPushHandlers_AppliesDefaults: zero-value config
// hydrates to documented defaults via applyDefaults. Without this, a
// constructor that leaves config zero gets a runtime "MaxAttempts=0 means
// silently inert" gotcha.
func TestRegisterClusterAccountPushHandlers_AppliesDefaults(t *testing.T) {
	factory, _, _, _ := buildAccountPushHandlerFixture(t)
	clusterSvc := &ClusterService{} // not actually invoked; we don't run the handler.
	runner := NewJobRunner(factory, JobRunnerConfig{})
	RegisterClusterAccountPushHandlers(runner, factory, clusterSvc, ClusterAccountSyncConfig{})

	runner.mu.RLock()
	push, okPush := runner.handlers[JobTypeClusterAccountPush]
	del, okDel := runner.handlers[JobTypeClusterAccountDelete]
	runner.mu.RUnlock()

	require.True(t, okPush, "push handler must be registered")
	require.True(t, okDel, "delete handler must be registered")
	assert.Equal(t, accountPushMaxAttempts, push.MaxAttempts)
	assert.Equal(t, AuditFailuresOnly, push.AuditPolicy)
	assert.Equal(t, 60*time.Second, push.LeaseDuration)
	assert.Equal(t, accountPushMaxAttempts, del.MaxAttempts)
	assert.Equal(t, AuditFailuresOnly, del.AuditPolicy)
}
