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
	token, err := s.service.GenerateAccountJWT(s.ctx, account, operator)
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
	token, err := s.service.GenerateAccountJWT(s.ctx, account, operator)
	require.NoError(s.T(), err)

	// Decode and validate
	claims, err := jwt.DecodeAccountClaims(token)
	require.NoError(s.T(), err)

	// Verify JetStream limits
	assert.Equal(s.T(), account.JetStreamMaxMemory, claims.Limits.JetStreamLimits.MemoryStorage)
	assert.Equal(s.T(), account.JetStreamMaxStorage, claims.Limits.JetStreamLimits.DiskStorage)
	assert.Equal(s.T(), account.JetStreamMaxStreams, claims.Limits.JetStreamLimits.Streams)
	assert.Equal(s.T(), account.JetStreamMaxConsumers, claims.Limits.JetStreamLimits.Consumer)
	assert.Equal(s.T(), int64(-1), claims.Limits.JetStreamLimits.MemoryMaxStreamBytes)
	assert.Equal(s.T(), int64(-1), claims.Limits.JetStreamLimits.DiskMaxStreamBytes)
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
	token, err := s.service.GenerateUserJWT(s.ctx, user, account, nil)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), token)

	// Decode and validate
	claims, err := jwt.DecodeUserClaims(token)
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
	token, err := s.service.GenerateUserJWT(s.ctx, user, account, scopedKey)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), token)

	// Decode and validate
	claims, err := jwt.DecodeUserClaims(token)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), user.Name, claims.Name)
	assert.Equal(s.T(), user.PublicKey, claims.Subject)
	assert.Equal(s.T(), scopedPubKey, claims.Issuer)
	assert.Equal(s.T(), account.PublicKey, claims.IssuerAccount)

	// Verify permissions from scoped key (claims use jwt.StringList type)
	assert.ElementsMatch(s.T(), scopedKey.PubAllow, claims.Pub.Allow)
	assert.ElementsMatch(s.T(), scopedKey.PubDeny, claims.Pub.Deny)
	assert.ElementsMatch(s.T(), scopedKey.SubAllow, claims.Sub.Allow)
	assert.ElementsMatch(s.T(), scopedKey.SubDeny, claims.Sub.Deny)

	// Verify response permissions
	require.NotNil(s.T(), claims.Resp)
	assert.Equal(s.T(), scopedKey.ResponseMaxMsgs, claims.Resp.MaxMsgs)
	assert.Equal(s.T(), scopedKey.ResponseTTL, claims.Resp.Expires)
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

	_, err := s.service.GenerateAccountJWT(s.ctx, account, operator)
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

	_, err := s.service.GenerateUserJWT(s.ctx, user, account, nil)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to decrypt account seed")
}
