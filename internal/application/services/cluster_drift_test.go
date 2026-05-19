package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
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

// ---------------------------------------------------------------------------
// compareJWTs — pure-function classification table tests. Constructs two
// account JWTs differing only in IssuedAt to exercise each branch without
// standing up the full service stack.
// ---------------------------------------------------------------------------

func TestCompareJWTs(t *testing.T) {
	// We test the strings entrypoint only for cases where we can construct
	// inputs without fighting jwt v2's IssuedAt-stamping behaviour: byte
	// equality (fast path) and undecodable resolver replies. The
	// interesting classification branches (DB_AHEAD, OUT_OF_BAND,
	// matching-jti) live on classifyDecoded — see TestClassifyDecoded.

	opKP, err := nkeys.CreateOperator()
	require.NoError(t, err)
	accKP, err := nkeys.CreateAccount()
	require.NoError(t, err)
	accPub, err := accKP.PublicKey()
	require.NoError(t, err)

	claims := jwt.NewAccountClaims(accPub)
	claims.Name = "drift-test"
	validJWT, err := claims.Encode(opKP)
	require.NoError(t, err)

	t.Run("byte-equal strings are IN_SYNC (fast path)", func(t *testing.T) {
		status, _, _ := compareJWTs(validJWT, validJWT)
		assert.Equal(t, DriftStatusInSync, status)
	})

	t.Run("undecodable resolver reply is OUT_OF_BAND when NIS JWT is valid", func(t *testing.T) {
		status, _, _ := compareJWTs(validJWT, "not-a-jwt-at-all")
		assert.Equal(t, DriftStatusOutOfBand, status)
	})

	t.Run("driftStatusLabel covers every status", func(t *testing.T) {
		for _, s := range []DriftStatus{
			DriftStatusInSync, DriftStatusDBAhead, DriftStatusOutOfBand,
			DriftStatusMissingOnResolver, DriftStatusUnreachable,
		} {
			label := driftStatusLabel(s)
			assert.NotEqual(t, "unspecified", label, "status %d missing label", s)
			assert.NotContains(t, label, " ", "label must be metric-safe (no spaces)")
		}
	})
}

// TestClassifyDecoded covers the post-decode branch of compareJWTs by handing
// in two *jwt.AccountClaims with deterministic iat+jti. Splitting this out
// from the strings path is necessary because jwt v2's Encode stamps IssuedAt
// to time.Now() on every call, making it hard to construct ordered-iat JWT
// pairs from the encoder.
func TestClassifyDecoded(t *testing.T) {
	mk := func(iat int64, id string) *jwt.AccountClaims {
		c := &jwt.AccountClaims{}
		c.IssuedAt = iat
		c.ID = id
		return c
	}

	t.Run("matching jti is IN_SYNC even at different iat", func(t *testing.T) {
		// Defensive: should never happen in practice (jti is content-
		// hashed and includes iat in the hash), but if it ever does the
		// claims are equivalent and there's nothing to reconcile.
		status, resIat, resJTI := classifyDecoded(mk(1000, "X"), mk(2000, "X"))
		assert.Equal(t, DriftStatusInSync, status)
		assert.Equal(t, int64(2000), resIat)
		assert.Equal(t, "X", resJTI)
	})

	t.Run("NIS iat newer is DB_AHEAD", func(t *testing.T) {
		status, resIat, resJTI := classifyDecoded(mk(2000, "NIS"), mk(1000, "RES"))
		assert.Equal(t, DriftStatusDBAhead, status)
		assert.Equal(t, int64(1000), resIat)
		assert.Equal(t, "RES", resJTI)
	})

	t.Run("resolver iat newer is OUT_OF_BAND", func(t *testing.T) {
		status, resIat, resJTI := classifyDecoded(mk(1000, "NIS"), mk(2000, "RES"))
		assert.Equal(t, DriftStatusOutOfBand, status)
		assert.Equal(t, int64(2000), resIat)
		assert.Equal(t, "RES", resJTI)
	})

	t.Run("equal iat but different jti is OUT_OF_BAND", func(t *testing.T) {
		// Same-second regen from two sources, or a re-encode that didn't
		// preserve content. Either way: something outside NIS pushed.
		status, _, _ := classifyDecoded(mk(1500, "NIS"), mk(1500, "RES"))
		assert.Equal(t, DriftStatusOutOfBand, status)
	})
}

// ---------------------------------------------------------------------------
// ScanClusterDrift — uses a real SQLite DB but no NATS. Verifies branches that
// don't require a live resolver. The "actually-talks-to-NATS" coverage lives
// in tests/e2e/cluster_drift_test.go.
// ---------------------------------------------------------------------------

type ClusterDriftTestSuite struct {
	suite.Suite
	ctx       context.Context
	db        *gorm.DB
	encryptor encryption.Encryptor
	factory   persistence.RepositoryFactory
	accSvc    *AccountService
	opSvc     *OperatorService
	clSvc     *ClusterService
	operator  *entities.Operator
	account   *entities.Account
}

func (s *ClusterDriftTestSuite) SetupSuite() {
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
	s.accSvc = NewAccountService(s.factory, jwtSvc, s.encryptor)
	s.opSvc = NewOperatorService(s.factory, s.accSvc, jwtSvc, s.encryptor)

	s.clSvc = NewClusterService(
		s.factory.ClusterRepository(),
		s.factory.OperatorRepository(),
		s.factory.AccountRepository(),
		s.factory.UserRepository(),
		s.factory.ScopedSigningKeyRepository(),
		s.encryptor,
		jwtSvc,
	).WithFactory(s.factory)
}

func (s *ClusterDriftTestSuite) TearDownSuite() {
	_ = sql.Close(s.db)
}

func (s *ClusterDriftTestSuite) SetupTest() {
	op, err := s.opSvc.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "drift-op-" + uuid.NewString()[:8],
		Description: "cluster drift tests",
	})
	require.NoError(s.T(), err)
	s.operator = op

	acc, err := s.accSvc.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID:  op.ID,
		Name:        "drift-acc-" + uuid.NewString()[:8],
		Description: "cluster drift tests",
	})
	require.NoError(s.T(), err)
	s.account = acc
}

func (s *ClusterDriftTestSuite) TearDownTest() {
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM operators")
}

func TestClusterDriftSuite(t *testing.T) {
	suite.Run(t, new(ClusterDriftTestSuite))
}

// insertCluster mirrors the JetStream-usage suite helper: bypass ClusterService
// so we can set Healthy/EncryptedCreds/LastHealthCheck precisely.
func (s *ClusterDriftTestSuite) insertCluster(name string, healthy, healthChecked, withCreds bool, urls []string, operatorID uuid.UUID) *entities.Cluster {
	c := &entities.Cluster{
		ID:                  uuid.New(),
		Name:                name,
		OperatorID:          operatorID,
		ServerURLs:          urls,
		SystemAccountPubKey: "A" + uuid.NewString()[:32],
		Healthy:             healthy,
	}
	if healthChecked {
		t := clock.Now()
		c.LastHealthCheck = &t
	}
	if withCreds {
		enc, err := s.encryptor.Encrypt(s.ctx, []byte("-----BEGIN NATS USER JWT-----\nfake\n------END NATS USER JWT------\n-----BEGIN USER NKEY SEED-----\nSUAFAKE\n------END USER NKEY SEED------\n"))
		require.NoError(s.T(), err)
		c.EncryptedCreds = enc
	}
	require.NoError(s.T(), s.factory.ClusterRepository().Create(s.ctx, c))
	return c
}

// TestNoAccounts: an operator with no accounts → empty rows, no error.
func (s *ClusterDriftTestSuite) TestNoAccounts() {
	// Use a fresh operator with no accounts.
	op, err := s.opSvc.CreateOperator(s.ctx, CreateOperatorRequest{Name: "empty-op"})
	require.NoError(s.T(), err)
	// CreateOperator auto-creates the $SYS account, so even a "fresh"
	// operator has at least one account. Verify the scan tolerates this —
	// the $SYS account will appear as MISSING_ON_RESOLVER (no resolver
	// configured here) or UNREACHABLE depending on the cluster state.
	// For the genuinely-empty assertion, we'd need to delete $SYS, which
	// the service refuses by design. Skip the truly-empty case; the
	// no-clusters path is exercised by TestUnhealthyShortCircuits.

	// Build a healthy-but-no-creds cluster so every row collapses to
	// UNREACHABLE without dialing.
	c := s.insertCluster("c-no-creds", true, true, false, []string{"nats://127.0.0.1:1"}, op.ID)
	rows, err := s.clSvc.ScanClusterDrift(s.ctx, c.ID, true)
	require.NoError(s.T(), err)
	// $SYS is the only account; one row.
	assert.Len(s.T(), rows, 1)
	for _, r := range rows {
		assert.Equal(s.T(), DriftStatusUnreachable, r.Status)
		assert.Contains(s.T(), r.ErrorMessage, "no system account credentials")
	}
}

// TestUnhealthyShortCircuits: cluster.Healthy=false AND LastHealthCheck != nil
// → every row UNREACHABLE without paying the dial timeout. Mirrors P10's
// guarantee that a confirmed-dead cluster doesn't slow down a refresh.
func (s *ClusterDriftTestSuite) TestUnhealthyShortCircuits() {
	c := s.insertCluster("c-dead", false, true, true, []string{"nats://255.255.255.255:14222"}, s.operator.ID)

	start := time.Now()
	rows, err := s.clSvc.ScanClusterDrift(s.ctx, c.ID, true)
	elapsed := time.Since(start)
	require.NoError(s.T(), err)

	// CreateOperator auto-created $SYS, so there's one account. Every row
	// should be UNREACHABLE with the "unhealthy" reason.
	require.NotEmpty(s.T(), rows)
	for _, r := range rows {
		assert.Equal(s.T(), DriftStatusUnreachable, r.Status)
		assert.Contains(s.T(), r.ErrorMessage, "unhealthy")
	}
	// Short-circuit must complete well under the 3s+ dial timeout.
	assert.Less(s.T(), elapsed, 1*time.Second, "short-circuit should not dial")
}

// TestNeverHealthCheckedClusterStillDialed: a freshly-created cluster has
// Healthy=false because the health-check loop hasn't fired yet. The drift
// scan must NOT short-circuit on those — it dials, then fails on the bogus
// URL and reports UNREACHABLE via the dial path. The "unhealthy" error
// substring distinguishes the two paths: it only appears when short-
// circuiting. Regression pin: identical guard to P10's analogue.
func (s *ClusterDriftTestSuite) TestNeverHealthCheckedClusterStillDialed() {
	c := s.insertCluster("c-new", false, false, true, []string{"nats://255.255.255.255:14222"}, s.operator.ID)

	rows, err := s.clSvc.ScanClusterDrift(s.ctx, c.ID, true)
	require.NoError(s.T(), err)

	require.NotEmpty(s.T(), rows)
	for _, r := range rows {
		assert.Equal(s.T(), DriftStatusUnreachable, r.Status)
		// Crucially NOT "unhealthy" — dial-path error message.
		assert.NotContains(s.T(), r.ErrorMessage, "unhealthy")
	}
}

// TestMissingCredsReportedAsUnreachable: a cluster row with no EncryptedCreds
// cannot be probed. Surface as UNREACHABLE so operators see something is off,
// rather than silently returning IN_SYNC or empty rows.
func (s *ClusterDriftTestSuite) TestMissingCredsReportedAsUnreachable() {
	c := s.insertCluster("c-no-creds", true, true, false, []string{"nats://localhost:14222"}, s.operator.ID)

	rows, err := s.clSvc.ScanClusterDrift(s.ctx, c.ID, true)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), rows)
	for _, r := range rows {
		assert.Equal(s.T(), DriftStatusUnreachable, r.Status)
		assert.Contains(s.T(), r.ErrorMessage, "no system account credentials")
	}
}

// TestFilterInSync: includeInSync=false strips IN_SYNC rows. The unhealthy
// short-circuit produces only UNREACHABLE rows, so flipping the flag does not
// change row count here — but we can still verify the filter helper doesn't
// accidentally drop non-IN_SYNC rows.
func (s *ClusterDriftTestSuite) TestFilterInSync() {
	rows := []*AccountDriftRow{
		{AccountName: "in-sync", Status: DriftStatusInSync},
		{AccountName: "drifted", Status: DriftStatusDBAhead},
		{AccountName: "missing", Status: DriftStatusMissingOnResolver},
	}
	filtered := filterInSync(rows)
	assert.Len(s.T(), filtered, 2)
	for _, r := range filtered {
		assert.NotEqual(s.T(), DriftStatusInSync, r.Status)
	}
}

// TestSortDriftRows: rows are returned sorted by account name to give the UI
// stable ordering across refreshes.
func (s *ClusterDriftTestSuite) TestSortDriftRows() {
	rows := []*AccountDriftRow{
		{AccountName: "zeta"},
		{AccountName: "alpha"},
		{AccountName: "mu"},
	}
	sortDriftRows(rows)
	assert.Equal(s.T(), "alpha", rows[0].AccountName)
	assert.Equal(s.T(), "mu", rows[1].AccountName)
	assert.Equal(s.T(), "zeta", rows[2].AccountName)
}

// TestReconcileCrossOperatorRejected: an account from operator A pushed to a
// cluster from operator B must be rejected before any NATS connection is
// attempted. SyncCluster is safe because it iterates accounts-by-operator;
// per-account reconcile has to enforce the equivalent boundary explicitly.
// Without this guard, an admin (who can read every cluster) could corrupt the
// foreign resolver with a JWT it does not trust.
func (s *ClusterDriftTestSuite) TestReconcileCrossOperatorRejected() {
	// Second operator + account.
	opB, err := s.opSvc.CreateOperator(s.ctx, CreateOperatorRequest{Name: "drift-op-b-" + uuid.NewString()[:8]})
	require.NoError(s.T(), err)

	// Cluster belongs to opB; account belongs to s.operator (opA).
	cluster := s.insertCluster("c-opB", true, true, true, []string{"nats://localhost:14222"}, opB.ID)

	err = s.clSvc.ReconcileAccountOnCluster(s.ctx, cluster.ID, s.account.ID)
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "does not belong")
}

// TestReconcileMissingJWT: an account row with empty JWT cannot be pushed.
// Should fail early with a clear error rather than producing a confusing
// resolver-side rejection.
func (s *ClusterDriftTestSuite) TestReconcileMissingJWT() {
	// Insert a synthetic account row with no JWT, scoped to the suite operator.
	acc := &entities.Account{
		ID:         uuid.New(),
		OperatorID: s.operator.ID,
		Name:       "no-jwt-acc",
		PublicKey:  "A" + strings.Repeat("X", 55),
		JWT:        "",
		CreatedAt:  clock.Now(),
		UpdatedAt:  clock.Now(),
	}
	require.NoError(s.T(), s.factory.AccountRepository().Create(s.ctx, acc))

	cluster := s.insertCluster("c-recon-nojwt", true, true, true, []string{"nats://localhost:14222"}, s.operator.ID)

	err := s.clSvc.ReconcileAccountOnCluster(s.ctx, cluster.ID, acc.ID)
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "no JWT to push")
}
