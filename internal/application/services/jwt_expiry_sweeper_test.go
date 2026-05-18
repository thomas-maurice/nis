package services

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlpkg "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

// sweeperNoop satisfies AccountJWTPusher without hitting NATS.
type sweeperNoop struct{}

func (s *sweeperNoop) PushAccountToAllClusters(_ context.Context, _ uuid.UUID, _ *entities.Account) []SyncError {
	return nil
}

// JWTExpirySweeperTestSuite exercises JWTExpirySweeper.Tick directly.
// No Run() is ever called — all tests call Tick so behaviour is deterministic.
type JWTExpirySweeperTestSuite struct {
	suite.Suite
	ctx         context.Context
	db          *gorm.DB
	factory     persistence.RepositoryFactory
	encryptor   encryption.Encryptor
	jwtService  *JWTService
	revSvc      *UserRevocationService
	sweeper     *JWTExpirySweeper
	operatorSvc *OperatorService
	accountSvc  *AccountService
	userSvc     *UserService
}

func (s *JWTExpirySweeperTestSuite) SetupSuite() {
	s.ctx = context.Background()

	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(s.T(), err)
	s.db = db

	sqlDB, err := db.DB()
	require.NoError(s.T(), err)

	goose.SetBaseFS(migrations.Migrations)
	require.NoError(s.T(), goose.SetDialect("sqlite3"))
	require.NoError(s.T(), goose.Up(sqlDB, "."))

	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(s.T(), err)
	s.encryptor = enc

	s.factory = persistence.NewSQLRepositoryFactoryFromDB(db)
	s.jwtService = NewJWTService(s.encryptor)
	s.accountSvc = NewAccountService(s.factory, s.jwtService, s.encryptor)
	s.operatorSvc = NewOperatorService(s.factory, s.accountSvc, s.jwtService, s.encryptor)
	s.userSvc = NewUserService(
		s.factory.UserRepository(),
		s.factory.AccountRepository(),
		s.factory.ScopedSigningKeyRepository(),
		s.factory.OperatorRepository(),
		s.jwtService,
		s.encryptor,
	).WithFactory(s.factory)

	noop := &sweeperNoop{}
	s.revSvc = NewUserRevocationService(s.factory, s.jwtService, noop, s.encryptor)
	// interval=0 → defaults to 1h, but we only ever call Tick directly so it doesn't matter.
	s.sweeper = NewJWTExpirySweeper(s.factory, s.jwtService, s.revSvc, noop, 0, 500)
}

func (s *JWTExpirySweeperTestSuite) TearDownSuite() {
	_ = sqlpkg.Close(s.db)
}

func (s *JWTExpirySweeperTestSuite) SetupTest() {
	s.db.Exec("DELETE FROM user_jwt_revocations")
	s.db.Exec("DELETE FROM events")
	s.db.Exec("DELETE FROM webhook_deliveries")
	s.db.Exec("DELETE FROM webhook_subscriptions")
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
}

func TestJWTExpirySweeperSuite(t *testing.T) {
	suite.Run(t, new(JWTExpirySweeperTestSuite))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// scaffoldSweeperTree creates operator → account → user via services.
func (s *JWTExpirySweeperTestSuite) scaffoldSweeperTree(prefix string) (opID, accID, userID uuid.UUID) {
	op, err := s.operatorSvc.CreateOperator(s.ctx, CreateOperatorRequest{Name: prefix + "-op"})
	require.NoError(s.T(), err)
	opID = op.ID

	acc, err := s.accountSvc.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: opID,
		Name:       prefix + "-acc",
	})
	require.NoError(s.T(), err)
	accID = acc.ID

	scopedKeys, err := s.factory.ScopedSigningKeyRepository().ListByAccount(
		s.ctx, accID, repositories.ListOptions{Limit: 10},
	)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), scopedKeys)

	user, err := s.userSvc.CreateUser(s.ctx, CreateUserRequest{
		AccountID:          accID,
		Name:               prefix + "-user",
		ScopedSigningKeyID: &scopedKeys[0].ID,
	})
	require.NoError(s.T(), err)
	userID = user.ID
	return
}

// countEvents counts events of the given type for the given resource.
func (s *JWTExpirySweeperTestSuite) countEvents(evType string, resourceID string) int {
	result, err := s.factory.EventRepository().List(s.ctx, repositories.EventFilter{
		Types:      []string{evType},
		ResourceID: resourceID,
		Limit:      1000,
	})
	require.NoError(s.T(), err)
	return len(result.Events)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestSweeperTick_PrunesExpiredRevocations verifies the prune phase:
// a revocation whose JWTExp is in the past gets PrunedAt set, and the
// account JWT is regenerated without that pubkey in its Revocations map.
func (s *JWTExpirySweeperTestSuite) TestSweeperTick_PrunesExpiredRevocations() {
	_, accID, userID := s.scaffoldSweeperTree("prune")

	user, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)

	// Insert a revocation whose JWTExp is already in the past so the sweeper
	// will immediately want to prune it.
	pastExp := time.Now().Add(-time.Hour)
	rev := &entities.UserJWTRevocation{
		ID:            uuid.New(),
		AccountID:     accID,
		UserID:        &userID,
		UserPublicKey: user.PublicKey,
		RevokedAt:     pastExp.Add(-time.Hour),
		JWTExp:        pastExp,
		CreatedAt:     time.Now(),
	}
	require.NoError(s.T(), s.factory.UserJWTRevocationRepository().Create(s.ctx, rev))

	// Manually embed the pubkey into the account JWT so we can confirm it's gone after prune.
	acc, err := s.factory.AccountRepository().GetByID(s.ctx, accID)
	require.NoError(s.T(), err)
	op, err := s.factory.OperatorRepository().GetByID(s.ctx, acc.OperatorID)
	require.NoError(s.T(), err)
	// Regenerate with the revocation active so the JWT has it.
	revsBefore, err := s.factory.UserJWTRevocationRepository().ListActiveByAccount(s.ctx, accID)
	require.NoError(s.T(), err)
	require.Len(s.T(), revsBefore, 1)
	newJWT, err := s.jwtService.GenerateAccountJWT(s.ctx, acc, op, nil, revsBefore, op.AccountJWTTTL)
	require.NoError(s.T(), err)
	acc.JWT = newJWT
	require.NoError(s.T(), s.factory.AccountRepository().Update(s.ctx, acc))

	result, err := s.sweeper.Tick(s.ctx)
	require.NoError(s.T(), err)
	assert.GreaterOrEqual(s.T(), result.RevocationsPruned, 1, "at least one revocation should be pruned")

	// Row should be marked pruned now.
	pruned, err := s.factory.UserJWTRevocationRepository().GetByID(s.ctx, rev.ID)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), pruned.PrunedAt, "revocation must have PrunedAt set after prune phase")

	// Account JWT must no longer have the revoked pubkey.
	accAfter, err := s.factory.AccountRepository().GetByID(s.ctx, accID)
	require.NoError(s.T(), err)
	claims, err := jwt.DecodeAccountClaims(accAfter.JWT)
	require.NoError(s.T(), err)
	oldIAT := rev.RevokedAt.Add(-time.Second)
	assert.False(s.T(), claims.Revocations.IsRevoked(rev.UserPublicKey, oldIAT),
		"pruned pubkey must not appear in account JWT Revocations after prune")
}

// TestSweeperTick_EmitsExpiringSoonOncePerIAT verifies that the expiring-soon
// alert fires exactly once per JWT iat (dedup via LastExpiringWarnIAT).
// Calling Tick twice for the same user must produce only one event.
func (s *JWTExpirySweeperTestSuite) TestSweeperTick_EmitsExpiringSoonOncePerIAT() {
	opID, _, userID := s.scaffoldSweeperTree("warn")

	// Set operator TTL so the sweeper considers this operator for the expiring-soon phase.
	ttl := 30 * 24 * time.Hour
	op, err := s.factory.OperatorRepository().GetByID(s.ctx, opID)
	require.NoError(s.T(), err)
	op.UserJWTTTL = ttl
	// Large warn window (90d) so expiring-in-1-minute qualifies.
	op.JWTWarnWindow = 90 * 24 * time.Hour
	require.NoError(s.T(), s.factory.OperatorRepository().Update(s.ctx, op))

	// Stamp the user with a JWT that expires in 1 minute (inside the warn window).
	now := time.Now()
	expiresAt := now.Add(time.Minute)
	user, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)
	user.JWTExpiresAt = &expiresAt
	user.JWTIssuedAt = &now
	user.LastExpiringWarnIAT = nil // ensure no prior warn
	require.NoError(s.T(), s.factory.UserRepository().Update(s.ctx, user))

	// First tick — should emit one expiring_soon event.
	_, err = s.sweeper.Tick(s.ctx)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, s.countEvents(entities.EventTypeUserCredExpiringSoon, userID.String()),
		"first Tick must emit exactly one expiring_soon event")

	// Second tick — dedup should suppress the event (same iat, warn pin set).
	_, err = s.sweeper.Tick(s.ctx)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, s.countEvents(entities.EventTypeUserCredExpiringSoon, userID.String()),
		"second Tick must NOT emit a duplicate expiring_soon event for the same iat")
}

// TestSweeperTick_AutoRenewWhenEnabled verifies that when operator.JWTAutoRenew=true
// and a user is inside the warn window, Tick re-signs the user JWT and emits
// user.cred.renewed.
func (s *JWTExpirySweeperTestSuite) TestSweeperTick_AutoRenewWhenEnabled() {
	opID, _, userID := s.scaffoldSweeperTree("autorenew")

	ttl := 30 * 24 * time.Hour
	op, err := s.factory.OperatorRepository().GetByID(s.ctx, opID)
	require.NoError(s.T(), err)
	op.UserJWTTTL = ttl
	op.JWTWarnWindow = 90 * 24 * time.Hour
	op.JWTAutoRenew = true
	require.NoError(s.T(), s.factory.OperatorRepository().Update(s.ctx, op))

	now := time.Now()
	expiresAt := now.Add(time.Minute) // inside the warn window
	user, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)
	oldJWT := user.JWT
	user.JWTExpiresAt = &expiresAt
	user.JWTIssuedAt = &now
	user.LastExpiringWarnIAT = nil
	require.NoError(s.T(), s.factory.UserRepository().Update(s.ctx, user))

	result, err := s.sweeper.Tick(s.ctx)
	require.NoError(s.T(), err)
	assert.GreaterOrEqual(s.T(), result.AutoRenewed, 1, "at least one auto-renewal expected")

	// User JWT must have changed.
	renewed, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)
	assert.NotEqual(s.T(), oldJWT, renewed.JWT, "user JWT must be re-signed after auto-renew")

	// user.cred.renewed event must exist.
	assert.Equal(s.T(), 1, s.countEvents(entities.EventTypeUserCredRenewed, userID.String()),
		"auto-renew must emit user.cred.renewed event")
}

// TestSweeperTick_SkipsAutoRenewForAlreadyExpired verifies that a JWT whose exp
// is in the past does NOT get auto-renewed (phase 4 only emits an alert).
// A user.cred.expired event must be emitted; no user.cred.renewed event.
func (s *JWTExpirySweeperTestSuite) TestSweeperTick_SkipsAutoRenewForAlreadyExpired() {
	opID, _, userID := s.scaffoldSweeperTree("expired-no-renew")

	ttl := 30 * 24 * time.Hour
	op, err := s.factory.OperatorRepository().GetByID(s.ctx, opID)
	require.NoError(s.T(), err)
	op.UserJWTTTL = ttl
	op.JWTWarnWindow = 90 * 24 * time.Hour
	op.JWTAutoRenew = true // even with auto-renew on, expired JWTs must not be renewed
	require.NoError(s.T(), s.factory.OperatorRepository().Update(s.ctx, op))

	// JWT is already past exp.
	past := time.Now().Add(-time.Hour)
	iat := past.Add(-24 * time.Hour)
	user, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)
	oldJWT := user.JWT
	user.JWTExpiresAt = &past
	user.JWTIssuedAt = &iat
	user.LastExpiredAlertIAT = nil
	user.LastExpiringWarnIAT = nil
	require.NoError(s.T(), s.factory.UserRepository().Update(s.ctx, user))

	_, err = s.sweeper.Tick(s.ctx)
	require.NoError(s.T(), err)

	// JWT must NOT have changed.
	after, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), oldJWT, after.JWT, "already-expired JWT must NOT be auto-renewed")

	// No renewed event.
	assert.Equal(s.T(), 0, s.countEvents(entities.EventTypeUserCredRenewed, userID.String()),
		"no user.cred.renewed event must be emitted for an already-expired JWT")

	// One expired event.
	assert.Equal(s.T(), 1, s.countEvents(entities.EventTypeUserCredExpired, userID.String()),
		"exactly one user.cred.expired event must be emitted")
}

// TestSweeperTick_ExpiredDedup verifies that calling Tick twice for the same
// expired user produces only one user.cred.expired event (dedup via LastExpiredAlertIAT).
func (s *JWTExpirySweeperTestSuite) TestSweeperTick_ExpiredDedup() {
	opID, _, userID := s.scaffoldSweeperTree("expired-dedup")

	ttl := 30 * 24 * time.Hour
	op, err := s.factory.OperatorRepository().GetByID(s.ctx, opID)
	require.NoError(s.T(), err)
	op.UserJWTTTL = ttl
	require.NoError(s.T(), s.factory.OperatorRepository().Update(s.ctx, op))

	past := time.Now().Add(-time.Hour)
	iat := past.Add(-24 * time.Hour)
	user, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)
	user.JWTExpiresAt = &past
	user.JWTIssuedAt = &iat
	user.LastExpiredAlertIAT = nil
	require.NoError(s.T(), s.factory.UserRepository().Update(s.ctx, user))

	_, err = s.sweeper.Tick(s.ctx)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, s.countEvents(entities.EventTypeUserCredExpired, userID.String()),
		"first Tick must emit one expired event")

	_, err = s.sweeper.Tick(s.ctx)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, s.countEvents(entities.EventTypeUserCredExpired, userID.String()),
		"second Tick must NOT emit a duplicate expired event (dedup by iat)")
}
