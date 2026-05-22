package services

import (
	"context"
	"strings"
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

// SSKRotationTestSuite covers the P3 rotation flow at the service layer.
// Lives in its own suite so the harness can stand up users (the SSK suite
// doesn't wire a UserService).
type SSKRotationTestSuite struct {
	suite.Suite
	ctx        context.Context
	db         *gorm.DB
	factory    persistence.RepositoryFactory
	encryptor  encryption.Encryptor
	jwtService *JWTService
	opSvc      *OperatorService
	accSvc     *AccountService
	userSvc    *UserService
	sskSvc     *ScopedSigningKeyService
}

func (s *SSKRotationTestSuite) SetupSuite() {
	s.ctx = context.Background()

	db, err := sqlpkg.NewDB("sqlite", ":memory:")
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

	s.factory = persistence.NewSQLRepositoryFactoryFromDB(db)
	s.jwtService = NewJWTService(s.encryptor)
	s.accSvc = NewAccountService(s.factory, s.jwtService, s.encryptor)
	s.opSvc = NewOperatorService(s.factory, s.accSvc, s.jwtService, s.encryptor)
	s.userSvc = NewUserService(
		s.factory.UserRepository(),
		s.factory.AccountRepository(),
		s.factory.ScopedSigningKeyRepository(),
		s.factory.OperatorRepository(),
		s.jwtService,
		s.encryptor,
	).WithFactory(s.factory)
	s.sskSvc = NewScopedSigningKeyService(s.factory, s.jwtService, s.encryptor)
}

func (s *SSKRotationTestSuite) TearDownSuite() {
	_ = sqlpkg.Close(s.db)
}

func (s *SSKRotationTestSuite) SetupTest() {
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

func TestSSKRotationSuite(t *testing.T) {
	suite.Run(t, new(SSKRotationTestSuite))
}

// scaffoldTree creates one operator + one account + the default SSK for
// that account + `n` users all signed by the default SSK. Returns the SSK
// + the slice of users in creation order.
func (s *SSKRotationTestSuite) scaffoldTree(prefix string, n int) (*entities.ScopedSigningKey, []*entities.User) {
	op, err := s.opSvc.CreateOperator(s.ctx, CreateOperatorRequest{Name: prefix + "-op"})
	require.NoError(s.T(), err)

	acc, err := s.accSvc.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: op.ID,
		Name:       prefix + "-acc",
	})
	require.NoError(s.T(), err)

	// CreateAccount auto-creates a default SSK; grab it.
	keys, err := s.factory.ScopedSigningKeyRepository().ListByAccount(s.ctx, acc.ID, repositories.ListOptions{Limit: 10})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), keys)
	ssk := keys[0]

	users := make([]*entities.User, 0, n)
	for i := 0; i < n; i++ {
		u, err := s.userSvc.CreateUser(s.ctx, CreateUserRequest{
			AccountID:          acc.ID,
			Name:               prefix + "-user-" + string(rune('a'+i)),
			ScopedSigningKeyID: &ssk.ID,
		})
		require.NoError(s.T(), err)
		users = append(users, u)
	}
	return ssk, users
}

// TestRotate_HappyPath_3Users walks the canonical flow:
//
//	scaffold 3 users → snapshot old pubkey + old user JWTs → rotate
//	→ assert new SSK pubkey, 3 revocations added, 3 users have new JWTs,
//	new JWTs decode + verify against new SSK pubkey, old JWTs are
//	revoked-by-pubkey in the new account JWT.
func (s *SSKRotationTestSuite) TestRotate_HappyPath_3Users() {
	ssk, users := s.scaffoldTree("happy", 3)

	oldPubKey := ssk.PublicKey
	oldUserJWTs := make(map[uuid.UUID]string, len(users))
	oldUserPubKeys := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		oldUserJWTs[u.ID] = u.JWT
		oldUserPubKeys[u.ID] = u.PublicKey
	}

	result, err := s.sskSvc.RotateScopedSigningKey(s.ctx, ssk.ID, "incident-test")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), result)

	// New SSK pubkey, old pubkey reported.
	assert.NotEqual(s.T(), oldPubKey, result.ScopedSigningKey.PublicKey, "rotation must produce a new public key")
	assert.True(s.T(), strings.HasPrefix(result.ScopedSigningKey.PublicKey, "A"), "rotated SSK pubkey must still be account-prefix")
	assert.Equal(s.T(), oldPubKey, result.OldPublicKey)

	// Affected users count + payload.
	assert.Equal(s.T(), 3, result.AffectedUsers)
	require.Len(s.T(), result.RevokedUserPublicKeys, 3)
	revokedSet := map[string]bool{}
	for _, pk := range result.RevokedUserPublicKeys {
		revokedSet[pk] = true
	}
	for _, u := range users {
		assert.True(s.T(), revokedSet[u.PublicKey], "user %s pubkey must be in RevokedUserPublicKeys", u.Name)
	}

	// 3 revocation rows.
	accID := ssk.AccountID
	revs, err := s.factory.UserJWTRevocationRepository().ListActiveByAccount(s.ctx, accID)
	require.NoError(s.T(), err)
	require.Len(s.T(), revs, 3, "rotation must add one revocation per active user")
	for _, rev := range revs {
		assert.True(s.T(), strings.HasPrefix(rev.Reason, "ssk_rotation:"+ssk.ID.String()+":"),
			"revocation reason must carry the structured ssk_rotation marker (got %q)", rev.Reason)
	}

	// Each user has a fresh JWT.
	for _, u := range users {
		reloaded, err := s.factory.UserRepository().GetByID(s.ctx, u.ID)
		require.NoError(s.T(), err)
		assert.NotEqual(s.T(), oldUserJWTs[u.ID], reloaded.JWT, "user %s JWT must be re-minted", u.Name)
		assert.Nil(s.T(), reloaded.RevokedAt, "rotation does NOT mark the user row as revoked (only the old JWT is revoked)")
		assert.Equal(s.T(), oldUserPubKeys[u.ID], reloaded.PublicKey, "user's own NKey pubkey is unchanged")

		// New JWT decodes and reports the new SSK pubkey as its signer.
		claims, err := jwt.DecodeUserClaims(reloaded.JWT)
		require.NoError(s.T(), err)
		assert.Equal(s.T(), result.ScopedSigningKey.PublicKey, claims.Issuer,
			"new user JWT must be signed by the new SSK pubkey")
	}

	// Account JWT regenerated: lists the new SSK pubkey in signing_keys
	// AND carries every user pubkey in its Revocations map.
	acc, err := s.factory.AccountRepository().GetByID(s.ctx, accID)
	require.NoError(s.T(), err)
	accClaims, err := jwt.DecodeAccountClaims(acc.JWT)
	require.NoError(s.T(), err)

	assert.True(s.T(), accClaims.SigningKeys.Contains(result.ScopedSigningKey.PublicKey),
		"account JWT signing_keys must include the new SSK pubkey")
	assert.False(s.T(), accClaims.SigningKeys.Contains(oldPubKey),
		"account JWT signing_keys must NOT include the old SSK pubkey")

	// Iat strictly before revoked_at must be revoked; iat strictly after
	// must NOT be — pin the -1s race fix from the review.
	for _, rev := range revs {
		iatBefore := rev.RevokedAt.Add(-time.Second)
		iatAfter := rev.RevokedAt.Add(time.Second)
		assert.True(s.T(), accClaims.Revocations.IsRevoked(rev.UserPublicKey, iatBefore),
			"account JWT must mark old iat as revoked for %s", rev.UserPublicKey)
		assert.False(s.T(), accClaims.Revocations.IsRevoked(rev.UserPublicKey, iatAfter),
			"account JWT must NOT revoke a future iat for %s (the new JWT must be accepted)", rev.UserPublicKey)
	}
}

// TestRotate_AlreadyRevokedUserSkipped — a user that was revoked before
// rotation should not show up in RevokedUserPublicKeys, and no new
// revocation row should be added for them. Their JWT is left as-is
// (already invalidated by the prior revocation).
func (s *SSKRotationTestSuite) TestRotate_AlreadyRevokedUserSkipped() {
	ssk, users := s.scaffoldTree("skip-revoked", 2)
	pre := users[0]
	keepActive := users[1]

	// Soft-revoke the first user directly via the repo so we don't pull
	// UserRevocationService into this suite. The rotation code path
	// only checks user.RevokedAt — anything that sets it works.
	revokedAt := time.Now().UTC()
	pre.RevokedAt = &revokedAt
	pre.RevocationReason = "pre-rotation"
	require.NoError(s.T(), s.factory.UserRepository().Update(s.ctx, pre))

	oldKeepJWT := keepActive.JWT

	result, err := s.sskSvc.RotateScopedSigningKey(s.ctx, ssk.ID, "test")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 1, result.AffectedUsers, "only the active user should be affected")
	require.Len(s.T(), result.RevokedUserPublicKeys, 1)
	assert.Equal(s.T(), keepActive.PublicKey, result.RevokedUserPublicKeys[0])

	// Pre-revoked user's JWT must NOT have been re-minted.
	preReloaded, err := s.factory.UserRepository().GetByID(s.ctx, pre.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), pre.JWT, preReloaded.JWT, "already-revoked user's JWT must not be re-minted")
	require.NotNil(s.T(), preReloaded.RevokedAt)

	// Active user got a new JWT.
	keepReloaded, err := s.factory.UserRepository().GetByID(s.ctx, keepActive.ID)
	require.NoError(s.T(), err)
	assert.NotEqual(s.T(), oldKeepJWT, keepReloaded.JWT, "active user must get a fresh JWT")
}

// TestRotate_PlainSignerSSKRefused — IsPlainSigner=true SSKs (NSC
// imports) cannot be rotated by NIS in v1. The service returns
// ErrSSKPlainSignerRotation; the row is unchanged.
func (s *SSKRotationTestSuite) TestRotate_PlainSignerSSKRefused() {
	ssk, _ := s.scaffoldTree("plain", 1)

	// Flip the flag on the SSK directly via the repo so the rotation
	// code sees a plain-signer SSK without us having to drive an NSC
	// import in this test.
	ssk.IsPlainSigner = true
	require.NoError(s.T(), s.factory.ScopedSigningKeyRepository().Update(s.ctx, ssk))

	_, err := s.sskSvc.RotateScopedSigningKey(s.ctx, ssk.ID, "test")
	require.Error(s.T(), err)
	assert.ErrorIs(s.T(), err, ErrSSKPlainSignerRotation)

	// Pubkey unchanged.
	reloaded, err := s.factory.ScopedSigningKeyRepository().GetByID(s.ctx, ssk.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), ssk.PublicKey, reloaded.PublicKey, "plain-signer rotation must be a no-op on the SSK row")

	// No revocations added.
	revs, err := s.factory.UserJWTRevocationRepository().ListActiveByAccount(s.ctx, ssk.AccountID)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), revs, "plain-signer rotation must not add revocations")
}

// TestRotate_PreservesPermsAndTemplateBinding — rotation only touches
// public_key + encrypted_seed. Perms, response limits, name, description,
// template_drifted are all preserved.
func (s *SSKRotationTestSuite) TestRotate_PreservesPermsAndTemplateBinding() {
	ssk, _ := s.scaffoldTree("preserve", 1)

	// Set distinguishing fields via UpdateScopedSigningKey so the row
	// doesn't sit at default zeros.
	desc := "rotation-preserve-marker"
	newName := "preserve-key"
	updated, err := s.sskSvc.UpdateScopedSigningKey(s.ctx, ssk.ID, UpdateScopedSigningKeyRequest{
		Name:        &newName,
		Description: &desc,
		PubAllow:    []string{"demo.>"},
		SubAllow:    []string{"events.>"},
	})
	require.NoError(s.T(), err)

	result, err := s.sskSvc.RotateScopedSigningKey(s.ctx, ssk.ID, "preserve-test")
	require.NoError(s.T(), err)

	got := result.ScopedSigningKey
	assert.Equal(s.T(), newName, got.Name, "name preserved")
	assert.Equal(s.T(), desc, got.Description, "description preserved")
	assert.Equal(s.T(), []string{"demo.>"}, got.PubAllow, "PubAllow preserved")
	assert.Equal(s.T(), []string{"events.>"}, got.SubAllow, "SubAllow preserved")
	assert.NotEqual(s.T(), updated.PublicKey, got.PublicKey, "pubkey rotated")
	assert.NotEqual(s.T(), updated.EncryptedSeed, got.EncryptedSeed, "encrypted_seed rotated")
}

// TestRotate_NotFound returns the repository's not-found error wrapped.
func (s *SSKRotationTestSuite) TestRotate_NotFound() {
	_, err := s.sskSvc.RotateScopedSigningKey(s.ctx, uuid.New(), "nope")
	require.Error(s.T(), err)
}

// TestPushAccountToAllClustersDetailed_UnhealthyClusterShortCircuited —
// confirmed-unhealthy clusters (LastHealthCheck != nil && !Healthy) get a
// "cluster is unhealthy: ..." outcome instead of an actual dial attempt.
// Mirrors the P9/P10 pattern; matters most when the operator's dev NATS
// is in open mode (no JWT resolver) and the rotation push would otherwise
// produce the confusing `no responders available` raw error.
func (s *SSKRotationTestSuite) TestPushAccountToAllClustersDetailed_UnhealthyClusterShortCircuited() {
	op, err := s.opSvc.CreateOperator(s.ctx, CreateOperatorRequest{Name: "unhealthy-skip-op"})
	require.NoError(s.T(), err)
	acc, err := s.accSvc.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: op.ID,
		Name:       "unhealthy-skip-acc",
	})
	require.NoError(s.T(), err)

	// Insert a cluster row in confirmed-unhealthy state. EncryptedCreds
	// must be non-empty so we don't short-circuit on the empty-creds
	// branch instead.
	checked := time.Now().Add(-1 * time.Minute)
	cluster := &entities.Cluster{
		ID:               uuid.New(),
		OperatorID:       op.ID,
		Name:             "unhealthy-cluster",
		ServerURLs:       []string{"nats://localhost:9999"}, // unreachable
		EncryptedCreds:   "encrypted:test-key:fake",
		Healthy:          false,
		LastHealthCheck:  &checked,
		HealthCheckError: "JWT resolver did not respond on $SYS.REQ.CLAIMS.LIST: nats: no responders available for request",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	require.NoError(s.T(), s.factory.ClusterRepository().Create(s.ctx, cluster))

	// We need the clusterService to expose PushAccountToAllClustersDetailed —
	// construct one with the same deps as the real service.
	clusterRepo := s.factory.ClusterRepository()
	clusterSvc := NewClusterService(
		clusterRepo,
		s.factory.OperatorRepository(),
		s.factory.AccountRepository(),
		s.factory.UserRepository(),
		s.factory.ScopedSigningKeyRepository(),
		s.encryptor,
		s.jwtService,
	)
	outcomes := clusterSvc.PushAccountToAllClustersDetailed(s.ctx, op.ID, acc)
	require.Len(s.T(), outcomes, 1)
	assert.False(s.T(), outcomes[0].OK)
	assert.Contains(s.T(), outcomes[0].ErrorMessage, "cluster is unhealthy",
		"unhealthy short-circuit must surface the cluster's last health-check error context")
	assert.Contains(s.T(), outcomes[0].ErrorMessage, "no responders",
		"the underlying NATS error must be threaded through so the operator sees the actionable cause")
}

// TestRotate_EmitsScopedKeyRotatedEvent — the summary event is emitted
// inside the rotation tx with the right shape.
func (s *SSKRotationTestSuite) TestRotate_EmitsScopedKeyRotatedEvent() {
	ssk, _ := s.scaffoldTree("event", 2)

	_, err := s.sskSvc.RotateScopedSigningKey(s.ctx, ssk.ID, "audit-marker")
	require.NoError(s.T(), err)

	// We don't have direct access to ListEvents at the service layer
	// here (no EventService wiring). Read straight from the events
	// table to verify shape.
	var rows []struct {
		Type       string
		ResourceID string
	}
	require.NoError(s.T(), s.db.Raw("SELECT type, resource_id FROM events WHERE type = ?",
		entities.EventTypeScopedKeyRotated).Scan(&rows).Error)
	require.Len(s.T(), rows, 1, "exactly one scoped_key.rotated event per rotation")
	assert.Equal(s.T(), ssk.ID.String(), rows[0].ResourceID)

	// And one user.revoked per affected user.
	var userRevokeCount int64
	require.NoError(s.T(), s.db.Raw(
		"SELECT COUNT(*) FROM events WHERE type = ?",
		entities.EventTypeUserRevoked,
	).Scan(&userRevokeCount).Error)
	assert.Equal(s.T(), int64(2), userRevokeCount, "one user.revoked per affected user")
}
