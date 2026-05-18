package services

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
)

type JWTServiceTestSuite struct {
	suite.Suite
	ctx       context.Context
	encryptor encryption.Encryptor
	service   *JWTService
}

func (s *JWTServiceTestSuite) SetupTest() {
	s.ctx = context.Background()

	// Create encryptor with a test key (32 bytes, base64-encoded)
	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(s.T(), err)
	s.encryptor = enc

	s.service = NewJWTService(s.encryptor)
}

func TestJWTServiceSuite(t *testing.T) {
	suite.Run(t, new(JWTServiceTestSuite))
}

// TestGenerateNKey tests NKey generation for different types
func (s *JWTServiceTestSuite) TestGenerateNKey() {
	tests := []struct {
		name   string
		prefix nkeys.PrefixByte
		want   byte
	}{
		{"Operator", nkeys.PrefixByteOperator, 'O'},
		{"Account", nkeys.PrefixByteAccount, 'A'},
		{"User", nkeys.PrefixByteUser, 'U'},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			seed, publicKey, err := GenerateNKey(tt.prefix)
			require.NoError(s.T(), err)
			assert.NotEmpty(s.T(), seed)
			assert.NotEmpty(s.T(), publicKey)
			assert.Equal(s.T(), tt.want, publicKey[0], "Public key should start with correct prefix")

			// Verify seed is valid
			kp, err := nkeys.FromSeed(seed)
			require.NoError(s.T(), err)

			// Verify we can extract the same public key
			extractedPubKey, err := kp.PublicKey()
			require.NoError(s.T(), err)
			assert.Equal(s.T(), publicKey, extractedPubKey)
		})
	}
}

// TestValidateNKeySeed tests seed validation
func (s *JWTServiceTestSuite) TestValidateNKeySeed() {
	// Generate a valid seed
	seed, expectedPubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
	require.NoError(s.T(), err)

	// Validate it
	publicKey, err := ValidateNKeySeed(seed)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), expectedPubKey, publicKey)
}

// TestValidateNKeySeed_Invalid tests validation with invalid seed
func (s *JWTServiceTestSuite) TestValidateNKeySeed_Invalid() {
	_, err := ValidateNKeySeed([]byte("not-a-valid-seed"))
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "invalid seed")
}

// TestGenerateOperatorJWT tests operator JWT generation
func (s *JWTServiceTestSuite) TestGenerateOperatorJWT() {
	// Generate operator keys
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
	require.NoError(s.T(), err)

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(s.ctx, seed)
	require.NoError(s.T(), err)

	// Create operator entity
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "Test Operator",
		Description:   "Test Description",
		EncryptedSeed: encryptedSeed,
		PublicKey:     pubKey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Generate JWT
	token, err := s.service.GenerateOperatorJWT(s.ctx, operator)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), token)

	// Decode and validate the JWT
	claims, err := jwt.DecodeOperatorClaims(token)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), operator.Name, claims.Name)
	assert.Equal(s.T(), operator.PublicKey, claims.Subject)

	// Verify signature
	kp, err := nkeys.FromSeed(seed)
	require.NoError(s.T(), err)
	vr := jwt.CreateValidationResults()
	claims.Validate(vr)
	assert.Empty(s.T(), vr.Errors())
	assert.Empty(s.T(), vr.Warnings())

	// Verify the JWT can be validated with the public key
	operatorPubKey, err := kp.PublicKey()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), operator.PublicKey, operatorPubKey)
}

// TestGenerateOperatorJWT_WithSystemAccount tests operator JWT with system account
func (s *JWTServiceTestSuite) TestGenerateOperatorJWT_WithSystemAccount() {
	// Generate operator keys
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
	require.NoError(s.T(), err)

	// Generate system account keys
	_, sysAccountPubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	require.NoError(s.T(), err)

	// Encrypt the seed
	encryptedSeed, err := s.encryptor.Encrypt(s.ctx, seed)
	require.NoError(s.T(), err)

	// Create operator entity with system account
	operator := &entities.Operator{
		ID:                  uuid.New(),
		Name:                "Test Operator",
		EncryptedSeed:       encryptedSeed,
		PublicKey:           pubKey,
		SystemAccountPubKey: sysAccountPubKey,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	// Generate JWT
	token, err := s.service.GenerateOperatorJWT(s.ctx, operator)
	require.NoError(s.T(), err)

	// Decode and validate
	claims, err := jwt.DecodeOperatorClaims(token)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), sysAccountPubKey, claims.SystemAccount)
}

// TestGenerateAccountJWT tests account JWT generation
func (s *JWTServiceTestSuite) TestGenerateAccountJWT() {
	// Generate operator keys
	opSeed, opPubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
	require.NoError(s.T(), err)

	// Generate account keys
	accSeed, accPubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	require.NoError(s.T(), err)

	// Encrypt seeds
	encryptedOpSeed, err := s.encryptor.Encrypt(s.ctx, opSeed)
	require.NoError(s.T(), err)
	encryptedAccSeed, err := s.encryptor.Encrypt(s.ctx, accSeed)
	require.NoError(s.T(), err)

	// Create entities
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "Test Operator",
		EncryptedSeed: encryptedOpSeed,
		PublicKey:     opPubKey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	account := &entities.Account{
		ID:               uuid.New(),
		OperatorID:       operator.ID,
		Name:             "Test Account",
		EncryptedSeed:    encryptedAccSeed,
		PublicKey:        accPubKey,
		JetStreamEnabled: false,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// Generate JWT
	token, err := s.service.GenerateAccountJWT(s.ctx, account, operator, nil, nil, 0)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), token)

	// Decode and validate
	claims, err := jwt.DecodeAccountClaims(token)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), account.Name, claims.Name)
	assert.Equal(s.T(), account.PublicKey, claims.Subject)
	assert.Equal(s.T(), operator.PublicKey, claims.Issuer)

	// Verify signature with operator key
	opKP, err := nkeys.FromSeed(opSeed)
	require.NoError(s.T(), err)
	vr := jwt.CreateValidationResults()
	claims.Validate(vr)
	assert.Empty(s.T(), vr.Errors())

	// Verify issuer matches operator
	issuerPubKey, err := opKP.PublicKey()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), issuerPubKey, claims.Issuer)
}

// TestGenerateAccountJWT_WithJetStream tests account JWT with JetStream limits
func (s *JWTServiceTestSuite) TestGenerateAccountJWT_WithJetStream() {
	// Generate operator keys
	opSeed, opPubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
	require.NoError(s.T(), err)

	// Generate account keys
	accSeed, accPubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	require.NoError(s.T(), err)

	// Encrypt seeds
	encryptedOpSeed, err := s.encryptor.Encrypt(s.ctx, opSeed)
	require.NoError(s.T(), err)
	encryptedAccSeed, err := s.encryptor.Encrypt(s.ctx, accSeed)
	require.NoError(s.T(), err)

	// Create entities
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "Test Operator",
		EncryptedSeed: encryptedOpSeed,
		PublicKey:     opPubKey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	account := &entities.Account{
		ID:                    uuid.New(),
		OperatorID:            operator.ID,
		Name:                  "Test Account",
		EncryptedSeed:         encryptedAccSeed,
		PublicKey:             accPubKey,
		JetStreamEnabled:      true,
		JetStreamMaxMemory:    1024 * 1024 * 1024, // 1GB
		JetStreamMaxStorage:   10 * 1024 * 1024 * 1024, // 10GB
		JetStreamMaxStreams:   100,
		JetStreamMaxConsumers: 1000,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	// Generate JWT
	token, err := s.service.GenerateAccountJWT(s.ctx, account, operator, nil, nil, 0)
	require.NoError(s.T(), err)

	// Decode and validate
	claims, err := jwt.DecodeAccountClaims(token)
	require.NoError(s.T(), err)

	// Verify JetStream limits
	assert.Equal(s.T(), account.JetStreamMaxMemory, claims.Limits.MemoryStorage)
	assert.Equal(s.T(), account.JetStreamMaxStorage, claims.Limits.DiskStorage)
	assert.Equal(s.T(), account.JetStreamMaxStreams, claims.Limits.Streams)
	assert.Equal(s.T(), account.JetStreamMaxConsumers, claims.Limits.Consumer)
	assert.Equal(s.T(), int64(-1), claims.Limits.MemoryMaxStreamBytes)
	assert.Equal(s.T(), int64(-1), claims.Limits.DiskMaxStreamBytes)
}

// TestGenerateUserJWT_SignedByAccount tests user JWT signed by account
func (s *JWTServiceTestSuite) TestGenerateUserJWT_SignedByAccount() {
	// Generate account keys
	accSeed, accPubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	require.NoError(s.T(), err)

	// Generate user keys
	userSeed, userPubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	require.NoError(s.T(), err)

	// Encrypt seeds
	encryptedAccSeed, err := s.encryptor.Encrypt(s.ctx, accSeed)
	require.NoError(s.T(), err)
	encryptedUserSeed, err := s.encryptor.Encrypt(s.ctx, userSeed)
	require.NoError(s.T(), err)

	// Create entities
	account := &entities.Account{
		ID:            uuid.New(),
		OperatorID:    uuid.New(),
		Name:          "Test Account",
		EncryptedSeed: encryptedAccSeed,
		PublicKey:     accPubKey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	user := &entities.User{
		ID:            uuid.New(),
		AccountID:     account.ID,
		Name:          "Test User",
		EncryptedSeed: encryptedUserSeed,
		PublicKey:     userPubKey,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Generate JWT (no scoped key)
	mint, err := s.service.GenerateUserJWT(s.ctx, user, account, nil, 0)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), mint.Token)

	// Decode and validate
	claims, err := jwt.DecodeUserClaims(mint.Token)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), user.Name, claims.Name)
	assert.Equal(s.T(), user.PublicKey, claims.Subject)
	assert.Equal(s.T(), account.PublicKey, claims.Issuer)

	// Verify signature with account key
	accKP, err := nkeys.FromSeed(accSeed)
	require.NoError(s.T(), err)
	vr := jwt.CreateValidationResults()
	claims.Validate(vr)
	assert.Empty(s.T(), vr.Errors())

	// Verify issuer matches account
	issuerPubKey, err := accKP.PublicKey()
	require.NoError(s.T(), err)
	assert.Equal(s.T(), issuerPubKey, claims.Issuer)
}

// TestGenerateUserJWT_SignedByScopedKey tests user JWT signed by scoped signing key
func (s *JWTServiceTestSuite) TestGenerateUserJWT_SignedByScopedKey() {
	// Generate account keys
	_, accPubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	require.NoError(s.T(), err)

	// Generate scoped signing keys
	scopedSeed, scopedPubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	require.NoError(s.T(), err)

	// Generate user keys
	userSeed, userPubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	require.NoError(s.T(), err)

	// Encrypt seeds
	encryptedScopedSeed, err := s.encryptor.Encrypt(s.ctx, scopedSeed)
	require.NoError(s.T(), err)
	encryptedUserSeed, err := s.encryptor.Encrypt(s.ctx, userSeed)
	require.NoError(s.T(), err)

	// Create entities
	account := &entities.Account{
		ID:        uuid.New(),
		Name:      "Test Account",
		PublicKey: accPubKey,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	scopedKey := &entities.ScopedSigningKey{
		ID:            uuid.New(),
		AccountID:     account.ID,
		Name:          "Test Scoped Key",
		Description:   "Developer scoped key",
		EncryptedSeed: encryptedScopedSeed,
		PublicKey:     scopedPubKey,
		PubAllow:      []string{"dev.>"},
		PubDeny:       []string{"prod.>"},
		SubAllow:      []string{"dev.>", "metrics.>"},
		SubDeny:       []string{"admin.>"},
		ResponseMaxMsgs: 10,
		ResponseTTL:     5 * time.Second,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	user := &entities.User{
		ID:                 uuid.New(),
		AccountID:          account.ID,
		Name:               "Test User",
		EncryptedSeed:      encryptedUserSeed,
		PublicKey:          userPubKey,
		ScopedSigningKeyID: &scopedKey.ID,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	// Generate JWT with scoped key
	mint, err := s.service.GenerateUserJWT(s.ctx, user, account, scopedKey, 0)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), mint.Token)

	// Decode and validate
	claims, err := jwt.DecodeUserClaims(mint.Token)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), user.Name, claims.Name)
	assert.Equal(s.T(), user.PublicKey, claims.Subject)
	assert.Equal(s.T(), scopedPubKey, claims.Issuer)
	assert.Equal(s.T(), account.PublicKey, claims.IssuerAccount)

	// For a scoped-key-signed user, the user JWT MUST have empty permissions and
	// limits — NATS rejects scoped users whose UserPermissionLimits are non-zero
	// (`UserScope.ValidateScopedSigner` → `HasEmptyPermissions`). The effective
	// permissions come from the scope template embedded in the account JWT, not
	// from the user JWT.
	assert.True(s.T(), claims.HasEmptyPermissions(),
		"scoped user JWT must have empty UserPermissionLimits; got %+v", claims.UserPermissionLimits)
}

// TestGetUserCredentials tests credentials file generation
func (s *JWTServiceTestSuite) TestGetUserCredentials() {
	// Generate user keys
	userSeed, userPubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	require.NoError(s.T(), err)

	// Encrypt seed
	encryptedUserSeed, err := s.encryptor.Encrypt(s.ctx, userSeed)
	require.NoError(s.T(), err)

	// Create user entity with a JWT
	user := &entities.User{
		ID:            uuid.New(),
		AccountID:     uuid.New(),
		Name:          "Test User",
		EncryptedSeed: encryptedUserSeed,
		PublicKey:     userPubKey,
		JWT:           "eyJ0eXAiOiJKV1QiLCJhbGciOiJlZDI1NTE5LW5rZXkifQ.test.jwt",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Get credentials
	creds, err := s.service.GetUserCredentials(s.ctx, user)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), creds)

	// Verify credentials format
	assert.Contains(s.T(), creds, "-----BEGIN NATS USER JWT-----")
	assert.Contains(s.T(), creds, "------END NATS USER JWT------")
	assert.Contains(s.T(), creds, "-----BEGIN USER NKEY SEED-----")
	assert.Contains(s.T(), creds, "------END USER NKEY SEED------")
	assert.Contains(s.T(), creds, user.JWT)
	assert.Contains(s.T(), creds, string(userSeed))
}

// TestGenerateOperatorJWT_DecryptionError tests error handling when seed decryption fails
func (s *JWTServiceTestSuite) TestGenerateOperatorJWT_DecryptionError() {
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "Test Operator",
		EncryptedSeed: "invalid-encrypted-seed",
		PublicKey:     "OABC123",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	_, err := s.service.GenerateOperatorJWT(s.ctx, operator)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to decrypt operator seed")
}

// TestGenerateAccountJWT_DecryptionError tests error handling when operator seed decryption fails
func (s *JWTServiceTestSuite) TestGenerateAccountJWT_DecryptionError() {
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "Test Operator",
		EncryptedSeed: "invalid-encrypted-seed",
		PublicKey:     "OABC123",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	account := &entities.Account{
		ID:            uuid.New(),
		OperatorID:    operator.ID,
		Name:          "Test Account",
		EncryptedSeed: "some-encrypted-seed",
		PublicKey:     "AABC123",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	_, err := s.service.GenerateAccountJWT(s.ctx, account, operator, nil, nil, 0)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to decrypt operator seed")
}

// TestGenerateUserJWT_DecryptionError tests error handling when seed decryption fails
func (s *JWTServiceTestSuite) TestGenerateUserJWT_DecryptionError() {
	account := &entities.Account{
		ID:            uuid.New(),
		Name:          "Test Account",
		EncryptedSeed: "invalid-encrypted-seed",
		PublicKey:     "AABC123",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	user := &entities.User{
		ID:            uuid.New(),
		AccountID:     account.ID,
		Name:          "Test User",
		EncryptedSeed: "some-encrypted-seed",
		PublicKey:     "UABC123",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	_, err := s.service.GenerateUserJWT(s.ctx, user, account, nil, 0)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to decrypt account seed")
}

// ---------------------------------------------------------------------------
// P2 — JWT lifecycle tests
// ---------------------------------------------------------------------------

// helpers shared by the P2 JWT-lifecycle tests below.
func (s *JWTServiceTestSuite) makeOperator() (*entities.Operator, []byte) {
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteOperator)
	require.NoError(s.T(), err)
	encSeed, err := s.encryptor.Encrypt(s.ctx, seed)
	require.NoError(s.T(), err)
	return &entities.Operator{
		ID:            uuid.New(),
		Name:          "op",
		EncryptedSeed: encSeed,
		PublicKey:     pubKey,
	}, seed
}

func (s *JWTServiceTestSuite) makeAccount(opID uuid.UUID) (*entities.Account, []byte) {
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteAccount)
	require.NoError(s.T(), err)
	encSeed, err := s.encryptor.Encrypt(s.ctx, seed)
	require.NoError(s.T(), err)
	return &entities.Account{
		ID:            uuid.New(),
		OperatorID:    opID,
		Name:          "acc",
		EncryptedSeed: encSeed,
		PublicKey:     pubKey,
	}, seed
}

func (s *JWTServiceTestSuite) makeUser(accID uuid.UUID) *entities.User {
	seed, pubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	require.NoError(s.T(), err)
	encSeed, err := s.encryptor.Encrypt(s.ctx, seed)
	require.NoError(s.T(), err)
	return &entities.User{
		ID:            uuid.New(),
		AccountID:     accID,
		Name:          "user",
		EncryptedSeed: encSeed,
		PublicKey:     pubKey,
	}
}

// TestGenerateUserJWT_NoExpiryWhenTTLZero asserts that ttl=0 produces a JWT
// without an exp claim and a mint whose ExpiresAt is nil, while iat is stamped.
func (s *JWTServiceTestSuite) TestGenerateUserJWT_NoExpiryWhenTTLZero() {
	op, _ := s.makeOperator()
	acc, _ := s.makeAccount(op.ID)
	user := s.makeUser(acc.ID)

	before := time.Now().Add(-time.Second)
	mint, err := s.service.GenerateUserJWT(s.ctx, user, acc, nil, 0)
	after := time.Now().Add(time.Second)

	require.NoError(s.T(), err)
	assert.Nil(s.T(), mint.ExpiresAt, "no TTL → ExpiresAt must be nil")
	assert.True(s.T(), mint.IssuedAt.After(before) && mint.IssuedAt.Before(after),
		"IssuedAt must be stamped even with TTL=0; got %v", mint.IssuedAt)

	claims, err := jwt.DecodeUserClaims(mint.Token)
	require.NoError(s.T(), err)
	assert.EqualValues(s.T(), 0, claims.Expires,
		"decoded exp claim must be zero when no TTL")
	assert.NotZero(s.T(), claims.IssuedAt, "iat must be set in the decoded claims")
}

// TestGenerateUserJWT_SetsExpWhenTTLPositive asserts that a positive TTL stamps
// both ExpiresAt on the mint and exp in the decoded claims within ±5s of now+TTL.
func (s *JWTServiceTestSuite) TestGenerateUserJWT_SetsExpWhenTTLPositive() {
	op, _ := s.makeOperator()
	acc, _ := s.makeAccount(op.ID)
	user := s.makeUser(acc.ID)

	ttl := 90 * 24 * time.Hour
	mintTime := time.Now()
	mint, err := s.service.GenerateUserJWT(s.ctx, user, acc, nil, ttl)
	require.NoError(s.T(), err)

	require.NotNil(s.T(), mint.ExpiresAt, "ExpiresAt must be non-nil for positive TTL")
	expectedExp := mintTime.Add(ttl)
	delta := mint.ExpiresAt.Sub(expectedExp)
	if delta < 0 {
		delta = -delta
	}
	assert.Less(s.T(), delta, 5*time.Second,
		"ExpiresAt %v should be within 5s of %v", mint.ExpiresAt, expectedExp)

	claims, err := jwt.DecodeUserClaims(mint.Token)
	require.NoError(s.T(), err)
	assert.NotZero(s.T(), claims.Expires, "decoded exp claim must be set")
	claimDelta := time.Duration(claims.Expires-mintTime.Add(ttl).Unix()) * time.Second
	if claimDelta < 0 {
		claimDelta = -claimDelta
	}
	assert.Less(s.T(), claimDelta, 5*time.Second,
		"decoded claims.Expires should match ExpiresAt within 5s")
}

// TestGenerateAccountJWT_EncodesActiveRevocations asserts that a non-pruned
// revocation entry appears in the decoded account JWT's Revocations map.
func (s *JWTServiceTestSuite) TestGenerateAccountJWT_EncodesActiveRevocations() {
	op, _ := s.makeOperator()
	acc, _ := s.makeAccount(op.ID)

	revokedAt := time.Now().UTC().Truncate(time.Second)
	_, userPubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	require.NoError(s.T(), err)

	rev := &entities.UserJWTRevocation{
		ID:            uuid.New(),
		AccountID:     acc.ID,
		UserPublicKey: userPubKey,
		RevokedAt:     revokedAt,
		JWTExp:        time.Now().Add(24 * time.Hour),
		PrunedAt:      nil, // active
	}

	token, err := s.service.GenerateAccountJWT(s.ctx, acc, op, nil, []*entities.UserJWTRevocation{rev}, 0)
	require.NoError(s.T(), err)

	claims, err := jwt.DecodeAccountClaims(token)
	require.NoError(s.T(), err)

	// The NATS JWT library stores Revocations as map[string]int64 internally.
	// claims.Revocations is a jwt.RevocationList. We check IsRevoked to confirm
	// the entry is present — any iat before revokedAt should be rejected.
	iatBeforeRevoke := revokedAt.Add(-time.Second)
	assert.True(s.T(), claims.Revocations.IsRevoked(userPubKey, iatBeforeRevoke),
		"a JWT issued before revokedAt must be revoked; pubkey=%s revokedAt=%v", userPubKey, revokedAt)
}

// TestGenerateAccountJWT_OmitsPrunedRevocations asserts that a revocation with
// PrunedAt set does NOT appear in the decoded account JWT's Revocations map.
func (s *JWTServiceTestSuite) TestGenerateAccountJWT_OmitsPrunedRevocations() {
	op, _ := s.makeOperator()
	acc, _ := s.makeAccount(op.ID)

	_, userPubKey, err := GenerateNKey(nkeys.PrefixByteUser)
	require.NoError(s.T(), err)

	pruned := time.Now()
	rev := &entities.UserJWTRevocation{
		ID:            uuid.New(),
		AccountID:     acc.ID,
		UserPublicKey: userPubKey,
		RevokedAt:     time.Now().Add(-24 * time.Hour),
		JWTExp:        time.Now().Add(-time.Hour), // already expired
		PrunedAt:      &pruned,
	}

	token, err := s.service.GenerateAccountJWT(s.ctx, acc, op, nil, []*entities.UserJWTRevocation{rev}, 0)
	require.NoError(s.T(), err)

	claims, err := jwt.DecodeAccountClaims(token)
	require.NoError(s.T(), err)

	// A JWT issued a long time ago — if the revocation were in the map it would
	// appear revoked; since we pruned it, it should NOT be revoked.
	oldIAT := time.Now().Add(-48 * time.Hour)
	assert.False(s.T(), claims.Revocations.IsRevoked(userPubKey, oldIAT),
		"pruned revocation must not appear in account JWT; pubkey=%s", userPubKey)
}
