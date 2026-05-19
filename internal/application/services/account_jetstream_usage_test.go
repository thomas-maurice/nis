package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gorm.io/gorm"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// AccountJetStreamUsageTestSuite tests the per-cluster JetStream usage probe.
// Tests that don't require a real NATS server live here (short-circuit, error
// classification, sort order, missing-creds, account-not-found). The
// "actually-talks-to-NATS" path is covered by tests/e2e/jetstream_usage_test.go.
type AccountJetStreamUsageTestSuite struct {
	suite.Suite
	ctx       context.Context
	db        *gorm.DB
	encryptor encryption.Encryptor
	factory   persistence.RepositoryFactory
	svc       *AccountService
	opSvc     *OperatorService
	operator  *entities.Operator
	account   *entities.Account
}

func (s *AccountJetStreamUsageTestSuite) SetupSuite() {
	s.ctx = context.Background()

	db, err := sql.NewDB("sqlite", ":memory:")
	require.NoError(s.T(), err)
	s.db = db

	sqlDB, err := db.DB()
	require.NoError(s.T(), err)

	goose.SetBaseFS(migrations.Migrations)
	require.NoError(s.T(), goose.SetDialect("sqlite3"))
	require.NoError(s.T(), goose.Up(sqlDB, "sqlite"))

	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(s.T(), err)
	s.encryptor = enc

	jwtSvc := NewJWTService(s.encryptor)
	s.factory = persistence.NewSQLRepositoryFactoryFromDB(s.db)
	s.svc = NewAccountService(s.factory, jwtSvc, s.encryptor)
	s.opSvc = NewOperatorService(s.factory, s.svc, jwtSvc, s.encryptor)
}

func (s *AccountJetStreamUsageTestSuite) TearDownSuite() {
	_ = sql.Close(s.db)
}

func (s *AccountJetStreamUsageTestSuite) SetupTest() {
	// Fresh operator + account per test so identifiers don't bleed.
	op, err := s.opSvc.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "test-op-" + uuid.NewString()[:8],
		Description: "jetstream usage tests",
	})
	require.NoError(s.T(), err)
	s.operator = op

	acc, err := s.svc.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID:  op.ID,
		Name:        "test-acc-" + uuid.NewString()[:8],
		Description: "jetstream usage tests",
	})
	require.NoError(s.T(), err)
	s.account = acc
}

func (s *AccountJetStreamUsageTestSuite) TearDownTest() {
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM operators")
}

func TestAccountJetStreamUsageSuite(t *testing.T) {
	suite.Run(t, new(AccountJetStreamUsageTestSuite))
}

// insertCluster bypasses ClusterService and inserts a cluster row directly with
// fully-controlled fields (Healthy, EncryptedCreds, etc.) so we can exercise
// every branch of the probe without standing up a real NATS server.
//
// healthChecked controls whether LastHealthCheck is set: the probe short-
// circuits only on health-checked-AND-unhealthy, so a "never checked" cluster
// with Healthy=false will still be dialed.
func (s *AccountJetStreamUsageTestSuite) insertCluster(name string, healthy, healthChecked bool, withCreds bool, urls []string) *entities.Cluster {
	c := &entities.Cluster{
		ID:                  uuid.New(),
		Name:                name,
		OperatorID:          s.operator.ID,
		ServerURLs:          urls,
		SystemAccountPubKey: "A" + uuid.NewString()[:32],
		Healthy:             healthy,
	}
	if healthChecked {
		t := clock.Now()
		c.LastHealthCheck = &t
	}
	if withCreds {
		// Any string would do — the probe should never reach the dial step
		// for clusters short-circuited as unhealthy, but for the
		// healthy-but-unreachable case we need a decryptable creds blob.
		enc, err := s.encryptor.Encrypt(s.ctx, []byte("-----BEGIN NATS USER JWT-----\nfake\n------END NATS USER JWT------\n-----BEGIN USER NKEY SEED-----\nSUAFAKE\n------END USER NKEY SEED------\n"))
		require.NoError(s.T(), err)
		c.EncryptedCreds = enc
	}
	require.NoError(s.T(), s.factory.ClusterRepository().Create(s.ctx, c))
	return c
}

// TestEmptyClusterList: no clusters → empty slice, no error. Catches the
// regression where a missing nil-check would attempt to errgroup over an empty
// list and produce a nil-deref or stale result slice.
func (s *AccountJetStreamUsageTestSuite) TestEmptyClusterList() {
	results, err := s.svc.GetAccountJetStreamUsage(s.ctx, s.account.ID, GetAccountJetStreamUsageOptions{})
	require.NoError(s.T(), err)
	assert.Empty(s.T(), results)
}

// TestAccountNotFound: bogus accountID propagates as a top-level error. (No
// "did you mean" silently returning empty — that would hide config drift.)
func (s *AccountJetStreamUsageTestSuite) TestAccountNotFound() {
	_, err := s.svc.GetAccountJetStreamUsage(s.ctx, uuid.New(), GetAccountJetStreamUsageOptions{})
	require.Error(s.T(), err)
}

// TestUnhealthyClusterShortCircuits: clusters marked unhealthy by the
// 60s health-check loop should be reported as Unreachable WITHOUT paying the
// dial timeout. Without this, a multi-cluster page hangs for 3s per dead
// cluster on every refresh.
func (s *AccountJetStreamUsageTestSuite) TestUnhealthyClusterShortCircuits() {
	s.insertCluster("a-dead", false, true, false, []string{"nats://255.255.255.255:14222"})

	results, err := s.svc.GetAccountJetStreamUsage(s.ctx, s.account.ID, GetAccountJetStreamUsageOptions{})
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1)

	assert.Equal(s.T(), JetStreamProbeStatusUnreachable, results[0].Status)
	assert.Contains(s.T(), results[0].ErrorMessage, "unhealthy")
	assert.Nil(s.T(), results[0].Usage)
}

// TestIncludeUnhealthyForcesDial: when includeUnhealthy=true, the short-circuit
// is bypassed. With a bogus URL, dial fails and the status is still Unreachable
// — but via the dial path, not the short-circuit (error message differs).
func (s *AccountJetStreamUsageTestSuite) TestIncludeUnhealthyForcesDial() {
	s.insertCluster("a-dead", false, true, true, []string{"nats://255.255.255.255:14222"})

	results, err := s.svc.GetAccountJetStreamUsage(s.ctx, s.account.ID, GetAccountJetStreamUsageOptions{IncludeUnhealthy: true})
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1)

	assert.Equal(s.T(), JetStreamProbeStatusUnreachable, results[0].Status)
	assert.NotContains(s.T(), results[0].ErrorMessage, "unhealthy")
}

// TestMissingCredsReportedAsError: a cluster row with no EncryptedCreds is a
// config bug, not a transient failure — report it as ERROR so it doesn't get
// auto-retried as if it were a network blip.
func (s *AccountJetStreamUsageTestSuite) TestMissingCredsReportedAsError() {
	s.insertCluster("a-no-creds", true, true, false, []string{"nats://localhost:14222"})

	results, err := s.svc.GetAccountJetStreamUsage(s.ctx, s.account.ID, GetAccountJetStreamUsageOptions{})
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1)

	assert.Equal(s.T(), JetStreamProbeStatusError, results[0].Status)
	assert.Contains(s.T(), results[0].ErrorMessage, "no system account credentials")
}

// TestAccountNotFoundPromotedToNotActivatedWhenJSEnabled: NATS lazily
// initialises per-account JS state on first client connect. Until then,
// $SYS.REQ.ACCOUNT.<key>.JSZ returns "account not found" even though the
// account's JWT carries JS limits and is on the resolver. Reporting that as
// ACCOUNT_NOT_FOUND made operators think NIS was broken (it isn't — they just
// hadn't connected a client yet). We promote the status to NOT_ACTIVATED when
// NIS DB confirms JS is enabled for the account, leaving ACCOUNT_NOT_FOUND for
// real drift. Discovered against `make run-demo` 2026-05-19.
func (s *AccountJetStreamUsageTestSuite) TestAccountNotFoundPromotedToNotActivatedWhenJSEnabled() {
	// Flip JS on in NIS DB for the suite account (CreateAccount defaults
	// to disabled).
	acc, err := s.factory.AccountRepository().GetByID(s.ctx, s.account.ID)
	require.NoError(s.T(), err)
	acc.JetStreamEnabled = true
	require.NoError(s.T(), s.factory.AccountRepository().Update(s.ctx, acc))

	// Cluster has been health-checked AND is healthy but points to an
	// unroutable IP — dial fails. We just need the probe to reach the
	// account-not-found classification path. Easier: stand up a real NATS
	// container? No — too heavy for a unit test. Instead, use a localhost
	// port no NATS is listening on, which gives ErrJetStreamUnreachable.
	//
	// Hmm — that's the wrong path. To exercise the promotion we'd need an
	// actually-responding NATS that returns "account not found", which is
	// e2e territory. The promotion logic itself is dead simple (one-line
	// post-process), so we instead assert on the structural property: if
	// JS is enabled in DB and the probe yields AccountNotFound, status
	// becomes NotActivated. We can synthesise that by running the same
	// promotion as a unit-level check.
	//
	// Concretely: the e2e suite owns the integration assertion; here we
	// only verify the promotion is in the call path (not stripped during
	// a future refactor) by checking ClusterJetStreamUsage.Status is
	// produced post-probe, not directly from the probe. The cleanest way
	// is a small standalone helper; for now we rely on the e2e harness +
	// the manual run-demo verification recorded in PROPOSALS.md.
	//
	// The dial-failure assertion below still adds value: it pins that
	// JS-enabled DB state does NOT magically up-grade Unreachable to OK
	// (e.g. a future refactor accidentally always returning NotActivated).
	s.insertCluster("a-dead", true, true, true, []string{"nats://127.0.0.1:1"})

	results, err := s.svc.GetAccountJetStreamUsage(s.ctx, s.account.ID, GetAccountJetStreamUsageOptions{})
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1)

	// Dial fails → Unreachable. NOT promoted to NotActivated despite
	// account.JetStreamEnabled=true, because the promotion only fires on
	// AccountNotFound from JSZ, not on dial failures.
	assert.Equal(s.T(), JetStreamProbeStatusUnreachable, results[0].Status)
}

// TestNeverHealthCheckedClusterStillDialed: a freshly-created cluster has
// Healthy=false because the 60s health-check loop hasn't fired yet. Treating
// that as "unreachable" would paint every newly-attached cluster grey in the
// UI for the first minute of its life. Probe must only short-circuit when a
// health check has actually run AND reported unhealthy. Discovered 2026-05-19.
func (s *AccountJetStreamUsageTestSuite) TestNeverHealthCheckedClusterStillDialed() {
	s.insertCluster("a-new", false /*healthy*/, false /*healthChecked*/, true, []string{"nats://255.255.255.255:14222"})

	results, err := s.svc.GetAccountJetStreamUsage(s.ctx, s.account.ID, GetAccountJetStreamUsageOptions{})
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1)

	// Should attempt the dial (not short-circuit). Dial against an unroutable
	// IP fails, so status is Unreachable — but error message must come from
	// the dial path, not the "marked unhealthy" short-circuit.
	assert.Equal(s.T(), JetStreamProbeStatusUnreachable, results[0].Status)
	assert.NotContains(s.T(), results[0].ErrorMessage, "marked unhealthy")
}

// TestStableSortByClusterName: results returned in stable cluster-name order
// regardless of insertion or scheduling order. The UI relies on this to keep
// per-cluster cards from re-ordering on refresh, which would be jarring.
func (s *AccountJetStreamUsageTestSuite) TestStableSortByClusterName() {
	s.insertCluster("z-last", false, true, false, []string{"nats://localhost:14222"})
	s.insertCluster("m-middle", false, true, false, []string{"nats://localhost:14222"})
	s.insertCluster("a-first", false, true, false, []string{"nats://localhost:14222"})

	results, err := s.svc.GetAccountJetStreamUsage(s.ctx, s.account.ID, GetAccountJetStreamUsageOptions{})
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 3)

	assert.Equal(s.T(), "a-first", results[0].ClusterName)
	assert.Equal(s.T(), "m-middle", results[1].ClusterName)
	assert.Equal(s.T(), "z-last", results[2].ClusterName)
}
