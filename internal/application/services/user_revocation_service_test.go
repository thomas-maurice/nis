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

// revNoop satisfies AccountJWTPusher without touching NATS.
type revNoop struct{}

func (r *revNoop) PushAccountToAllClusters(_ context.Context, _ uuid.UUID, _ *entities.Account) []SyncError {
	return nil
}

// UserRevocationServiceTestSuite is the test suite for UserRevocationService.
type UserRevocationServiceTestSuite struct {
	suite.Suite
	ctx         context.Context
	db          *gorm.DB
	factory     persistence.RepositoryFactory
	encryptor   encryption.Encryptor
	jwtService  *JWTService
	revSvc      *UserRevocationService
	operatorSvc *OperatorService
	accountSvc  *AccountService
	userSvc     *UserService
}

func (s *UserRevocationServiceTestSuite) SetupSuite() {
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

	s.revSvc = NewUserRevocationService(s.factory, s.jwtService, &revNoop{}, s.encryptor)
}

func (s *UserRevocationServiceTestSuite) TearDownSuite() {
	_ = sqlpkg.Close(s.db)
}

func (s *UserRevocationServiceTestSuite) SetupTest() {
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

func TestUserRevocationServiceSuite(t *testing.T) {
	suite.Run(t, new(UserRevocationServiceTestSuite))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// scaffoldTree creates a full operator→account→user chain via the service
// layer (which generates real NKeys + encrypts seeds). Returns the IDs.
func (s *UserRevocationServiceTestSuite) scaffoldTree(prefix string) (opID, accID, userID uuid.UUID) {
	op, err := s.operatorSvc.CreateOperator(s.ctx, CreateOperatorRequest{Name: prefix + "-op"})
	require.NoError(s.T(), err)
	opID = op.ID

	acc, err := s.accountSvc.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: opID,
		Name:       prefix + "-acc",
	})
	require.NoError(s.T(), err)
	accID = acc.ID

	// Use the default scoped key created automatically by CreateAccount.
	scopedKeys, err := s.factory.ScopedSigningKeyRepository().ListByAccount(
		s.ctx, accID, repositories.ListOptions{Limit: 10},
	)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), scopedKeys, "CreateAccount must create a default scoped key")

	user, err := s.userSvc.CreateUser(s.ctx, CreateUserRequest{
		AccountID:          accID,
		Name:               prefix + "-user",
		ScopedSigningKeyID: &scopedKeys[0].ID,
	})
	require.NoError(s.T(), err)
	userID = user.ID
	return
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRevokeUser_PersistsRowAndRegensJWT verifies:
//   - user.RevokedAt is set after RevokeUser
//   - a UserJWTRevocation row exists with the correct public key
//   - the account JWT, when decoded, has the user's public key in its Revocations map
func (s *UserRevocationServiceTestSuite) TestRevokeUser_PersistsRowAndRegensJWT() {
	_, accID, userID := s.scaffoldTree("rv-persist")

	user, err := s.revSvc.RevokeUser(s.ctx, userID, "security incident")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), user.RevokedAt, "user.RevokedAt must be set after revocation")

	// Revocation row must exist and have the right public key.
	revs, err := s.factory.UserJWTRevocationRepository().ListActiveByAccount(s.ctx, accID)
	require.NoError(s.T(), err)
	require.Len(s.T(), revs, 1, "exactly one active revocation row expected")
	assert.Equal(s.T(), user.PublicKey, revs[0].UserPublicKey)

	// Account JWT must include the revoked key.
	acc, err := s.factory.AccountRepository().GetByID(s.ctx, accID)
	require.NoError(s.T(), err)

	claims, err := jwt.DecodeAccountClaims(acc.JWT)
	require.NoError(s.T(), err)

	// Any iat strictly before revokedAt should be marked revoked.
	iatBefore := user.RevokedAt.Add(-time.Second)
	assert.True(s.T(), claims.Revocations.IsRevoked(user.PublicKey, iatBefore),
		"account JWT must have pubkey in Revocations after RevokeUser")
}

// TestRevokeUser_Idempotent verifies that calling RevokeUser twice returns
// ErrUserAlreadyRevoked on the second call without mutating state.
func (s *UserRevocationServiceTestSuite) TestRevokeUser_Idempotent() {
	_, _, userID := s.scaffoldTree("rv-idem")

	u1, err := s.revSvc.RevokeUser(s.ctx, userID, "first")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), u1.RevokedAt)
	firstRevokedAt := u1.RevokedAt.UTC()

	_, err = s.revSvc.RevokeUser(s.ctx, userID, "duplicate")
	require.ErrorIs(s.T(), err, ErrUserAlreadyRevoked,
		"second RevokeUser must return ErrUserAlreadyRevoked")

	// Reload and confirm state unchanged. Compare in UTC because SQLite strips
	// the timezone on roundtrip — the wallclock instant is what we care about.
	reloaded, loadErr := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), loadErr)
	require.NotNil(s.T(), reloaded.RevokedAt)
	assert.True(s.T(), firstRevokedAt.Equal(reloaded.RevokedAt.UTC()),
		"RevokedAt must not change on a no-op second revoke (first=%v, after=%v)",
		firstRevokedAt, reloaded.RevokedAt.UTC())
}

// TestRegenerateUserJWT_ClearsRevokedAndWarnPins verifies that RegenerateUserJWT:
//   - clears RevokedAt and RevocationReason
//   - clears LastExpiringWarnIAT and LastExpiredAlertIAT
//   - stamps a new JWT with a fresh iat, different from the revoked one
func (s *UserRevocationServiceTestSuite) TestRegenerateUserJWT_ClearsRevokedAndWarnPins() {
	_, _, userID := s.scaffoldTree("rv-regen")

	// Revoke first.
	_, err := s.revSvc.RevokeUser(s.ctx, userID, "test revoke")
	require.NoError(s.T(), err)

	// Manually stamp the warn pins via repo to simulate sweeper state.
	user, err := s.factory.UserRepository().GetByID(s.ctx, userID)
	require.NoError(s.T(), err)
	oldJWT := user.JWT
	warnTime := time.Now().Add(-time.Hour)
	user.LastExpiringWarnIAT = &warnTime
	user.LastExpiredAlertIAT = &warnTime
	require.NoError(s.T(), s.factory.UserRepository().Update(s.ctx, user))

	// Regenerate.
	updated, err := s.revSvc.RegenerateUserJWT(s.ctx, userID)
	require.NoError(s.T(), err)

	assert.Nil(s.T(), updated.RevokedAt, "RevokedAt must be cleared after regeneration")
	assert.Empty(s.T(), updated.RevocationReason, "RevocationReason must be cleared")
	assert.Nil(s.T(), updated.LastExpiringWarnIAT, "LastExpiringWarnIAT must be cleared")
	assert.Nil(s.T(), updated.LastExpiredAlertIAT, "LastExpiredAlertIAT must be cleared")
	assert.NotEmpty(s.T(), updated.JWT, "JWT must be non-empty")
	assert.NotEqual(s.T(), oldJWT, updated.JWT, "new JWT must differ from the revoked JWT")
	require.NotNil(s.T(), updated.JWTIssuedAt, "JWTIssuedAt must be stamped")
}

// TestRegenerateUserJWT_PreservesNoExpWhenTTLZero verifies that regeneration
// with operator UserJWTTTL=0 (the default) leaves JWTExpiresAt nil.
func (s *UserRevocationServiceTestSuite) TestRegenerateUserJWT_PreservesNoExpWhenTTLZero() {
	_, _, userID := s.scaffoldTree("rv-noexp")

	// Default operator TTL is 0 — no changes needed.
	updated, err := s.revSvc.RegenerateUserJWT(s.ctx, userID)
	require.NoError(s.T(), err)
	assert.Nil(s.T(), updated.JWTExpiresAt, "JWTExpiresAt must be nil when operator TTL is 0")
}

// TestRegenerateUserJWT_SetsExpWhenOperatorTTLNonZero verifies that regeneration
// respects a non-zero operator UserJWTTTL and stamps JWTExpiresAt accordingly.
func (s *UserRevocationServiceTestSuite) TestRegenerateUserJWT_SetsExpWhenOperatorTTLNonZero() {
	opID, _, userID := s.scaffoldTree("rv-exp")

	ttl := 30 * 24 * time.Hour
	op, err := s.factory.OperatorRepository().GetByID(s.ctx, opID)
	require.NoError(s.T(), err)
	op.UserJWTTTL = ttl
	require.NoError(s.T(), s.factory.OperatorRepository().Update(s.ctx, op))

	before := time.Now()
	updated, err := s.revSvc.RegenerateUserJWT(s.ctx, userID)
	after := time.Now()
	require.NoError(s.T(), err)

	require.NotNil(s.T(), updated.JWTExpiresAt, "JWTExpiresAt must be set when operator TTL is non-zero")
	lo := before.Add(ttl).Add(-5 * time.Second)
	hi := after.Add(ttl).Add(5 * time.Second)
	assert.True(s.T(),
		updated.JWTExpiresAt.After(lo) && updated.JWTExpiresAt.Before(hi),
		"JWTExpiresAt %v should be within ±5s of now+%v", updated.JWTExpiresAt, ttl)
}
