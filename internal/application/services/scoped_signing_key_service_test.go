package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/config"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

type ScopedSigningKeyServiceTestSuite struct {
	suite.Suite
	ctx                  context.Context
	db                   *gorm.DB
	encryptor            encryption.Encryptor
	jwtService           *JWTService
	operatorRepo         repositories.OperatorRepository
	accountRepo          repositories.AccountRepository
	userRepo             repositories.UserRepository
	scopedSigningKeyRepo repositories.ScopedSigningKeyRepository
	accountService       *AccountService
	operatorService      *OperatorService
	scopedKeyService     *ScopedSigningKeyService
}

func (s *ScopedSigningKeyServiceTestSuite) SetupSuite() {
	s.ctx = context.Background()

	// Create in-memory database
	db, err := sql.NewDB(config.DatabaseConfig{
		Driver: "sqlite",
		Path:   ":memory:",
	})
	require.NoError(s.T(), err)
	s.db = db

	// Run migrations
	sqlDB, err := db.DB()
	require.NoError(s.T(), err)

	goose.SetBaseFS(migrations.Migrations)
	err = goose.SetDialect("sqlite3")
	require.NoError(s.T(), err)

	err = goose.Up(sqlDB, ".")
	require.NoError(s.T(), err)

	// Create encryptor
	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(s.T(), err)
	s.encryptor = enc

	// Create services
	s.jwtService = NewJWTService(s.encryptor)
	s.operatorRepo = sql.NewOperatorRepo(s.db)
	s.accountRepo = sql.NewAccountRepo(s.db)
	s.userRepo = sql.NewUserRepo(s.db)
	s.scopedSigningKeyRepo = sql.NewScopedSigningKeyRepo(s.db)

	s.accountService = NewAccountService(s.accountRepo, s.operatorRepo, s.scopedSigningKeyRepo, s.jwtService, s.encryptor)
	s.operatorService = NewOperatorService(s.operatorRepo, s.accountRepo, s.userRepo, s.accountService, s.jwtService, s.encryptor)
	s.scopedKeyService = NewScopedSigningKeyService(s.scopedSigningKeyRepo, s.accountRepo, s.encryptor)
}

func (s *ScopedSigningKeyServiceTestSuite) TearDownSuite() {
	_ = sql.Close(s.db)
}

func (s *ScopedSigningKeyServiceTestSuite) TearDownTest() {
	// Clean up database after each test
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	s.db.Exec("DELETE FROM api_users")
}

func TestScopedSigningKeyServiceSuite(t *testing.T) {
	suite.Run(t, new(ScopedSigningKeyServiceTestSuite))
}

// createTestAccountForScopedKey is a helper that creates an operator and account for scoped key tests
func (s *ScopedSigningKeyServiceTestSuite) createTestAccountForScopedKey() uuid.UUID {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Test Operator",
	})
	require.NoError(s.T(), err)

	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)

	return account.ID
}

// TestCreateScopedSigningKey tests creating a scoped signing key with permission template
func (s *ScopedSigningKeyServiceTestSuite) TestCreateScopedSigningKey() {
	accountID := s.createTestAccountForScopedKey()

	req := CreateScopedSigningKeyRequest{
		AccountID:       accountID,
		Name:            "Developer Key",
		Description:     "Key for developer access",
		PubAllow:        []string{"dev.>"},
		PubDeny:         []string{"prod.>"},
		SubAllow:        []string{"dev.>", "metrics.>"},
		SubDeny:         []string{"admin.>"},
		ResponseMaxMsgs: 10,
		ResponseTTL:     5 * time.Second,
	}

	scopedKey, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, req)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), scopedKey)
	assert.NotEqual(s.T(), uuid.Nil, scopedKey.ID)
	assert.Equal(s.T(), req.Name, scopedKey.Name)
	assert.Equal(s.T(), req.Description, scopedKey.Description)
	assert.Equal(s.T(), accountID, scopedKey.AccountID)
	assert.NotEmpty(s.T(), scopedKey.PublicKey)
	assert.Equal(s.T(), byte('A'), scopedKey.PublicKey[0]) // Account key prefix for signing keys
	assert.NotEmpty(s.T(), scopedKey.EncryptedSeed)

	// Verify permissions
	assert.Equal(s.T(), req.PubAllow, scopedKey.PubAllow)
	assert.Equal(s.T(), req.PubDeny, scopedKey.PubDeny)
	assert.Equal(s.T(), req.SubAllow, scopedKey.SubAllow)
	assert.Equal(s.T(), req.SubDeny, scopedKey.SubDeny)
	assert.Equal(s.T(), req.ResponseMaxMsgs, scopedKey.ResponseMaxMsgs)
	assert.Equal(s.T(), req.ResponseTTL, scopedKey.ResponseTTL)

	// Verify it was saved to repository
	retrieved, err := s.scopedSigningKeyRepo.GetByID(s.ctx, scopedKey.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), scopedKey.ID, retrieved.ID)
	assert.Equal(s.T(), scopedKey.Name, retrieved.Name)
}

// TestCreateScopedSigningKey_EmptyName tests validation for empty name
func (s *ScopedSigningKeyServiceTestSuite) TestCreateScopedSigningKey_EmptyName() {
	accountID := s.createTestAccountForScopedKey()

	_, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "",
	})
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "scoped signing key name is required")
}

// TestCreateScopedSigningKey_DuplicateName tests that duplicate names under the same account are rejected
func (s *ScopedSigningKeyServiceTestSuite) TestCreateScopedSigningKey_DuplicateName() {
	accountID := s.createTestAccountForScopedKey()

	req := CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "Duplicate Key",
	}

	// Create first key
	_, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, req)
	require.NoError(s.T(), err)

	// Try to create second with same name
	_, err = s.scopedKeyService.CreateScopedSigningKey(s.ctx, req)
	assert.ErrorIs(s.T(), err, repositories.ErrAlreadyExists)
}

// TestCreateScopedSigningKey_InvalidAccount tests creation with non-existent account
func (s *ScopedSigningKeyServiceTestSuite) TestCreateScopedSigningKey_InvalidAccount() {
	_, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: uuid.New(),
		Name:      "Test Key",
	})
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to get account")
}

// TestGetScopedSigningKey tests retrieving a scoped signing key by ID
func (s *ScopedSigningKeyServiceTestSuite) TestGetScopedSigningKey() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID:   accountID,
		Name:        "Test Key",
		Description: "A test key",
	})
	require.NoError(s.T(), err)

	// Get by ID
	key, err := s.scopedKeyService.GetScopedSigningKey(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.ID, key.ID)
	assert.Equal(s.T(), created.Name, key.Name)
}

// TestGetScopedSigningKey_NotFound tests retrieving a non-existent key
func (s *ScopedSigningKeyServiceTestSuite) TestGetScopedSigningKey_NotFound() {
	_, err := s.scopedKeyService.GetScopedSigningKey(s.ctx, uuid.New())
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestGetScopedSigningKeyByName tests retrieving by account ID and name
func (s *ScopedSigningKeyServiceTestSuite) TestGetScopedSigningKeyByName() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "Named Key",
	})
	require.NoError(s.T(), err)

	key, err := s.scopedKeyService.GetScopedSigningKeyByName(s.ctx, accountID, "Named Key")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.ID, key.ID)
}

// TestGetScopedSigningKeyByPublicKey tests retrieving by public key
func (s *ScopedSigningKeyServiceTestSuite) TestGetScopedSigningKeyByPublicKey() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "PubKey Key",
	})
	require.NoError(s.T(), err)

	key, err := s.scopedKeyService.GetScopedSigningKeyByPublicKey(s.ctx, created.PublicKey)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.ID, key.ID)
}

// TestListScopedSigningKeysByAccount tests listing keys for an account with pagination
func (s *ScopedSigningKeyServiceTestSuite) TestListScopedSigningKeysByAccount() {
	accountID := s.createTestAccountForScopedKey()

	// Create multiple scoped signing keys (note: account already has a "default" key from creation)
	for i := 0; i < 3; i++ {
		_, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
			AccountID: accountID,
			Name:      fmt.Sprintf("Key %d", i),
		})
		require.NoError(s.T(), err)
	}

	// List all (should include default + 3 created = 4 total)
	keys, err := s.scopedKeyService.ListScopedSigningKeysByAccount(s.ctx, accountID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), keys, 4)

	// List with limit
	keys, err = s.scopedKeyService.ListScopedSigningKeysByAccount(s.ctx, accountID, repositories.ListOptions{Limit: 2})
	require.NoError(s.T(), err)
	assert.Len(s.T(), keys, 2)
}

// TestUpdateScopedSigningKey tests updating key metadata and permissions
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID:   accountID,
		Name:        "Original Key",
		Description: "Original Description",
		PubAllow:    []string{"old.>"},
	})
	require.NoError(s.T(), err)

	// Update name, description, and permissions
	newName := "Updated Key"
	newDesc := "Updated Description"
	updated, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, created.ID, UpdateScopedSigningKeyRequest{
		Name:        &newName,
		Description: &newDesc,
		PubAllow:    []string{"new.>", "also.>"},
		SubAllow:    []string{"events.>"},
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), newName, updated.Name)
	assert.Equal(s.T(), newDesc, updated.Description)
	assert.Equal(s.T(), []string{"new.>", "also.>"}, updated.PubAllow)
	assert.Equal(s.T(), []string{"events.>"}, updated.SubAllow)
}

// TestUpdateScopedSigningKey_NoChanges tests updating without changes
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey_NoChanges() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "No Change Key",
	})
	require.NoError(s.T(), err)

	// Update with no changes
	updated, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, created.ID, UpdateScopedSigningKeyRequest{})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.Name, updated.Name)
}

// TestUpdateScopedSigningKey_DuplicateName tests that renaming to an existing name is rejected
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey_DuplicateName() {
	accountID := s.createTestAccountForScopedKey()

	_, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "Key A",
	})
	require.NoError(s.T(), err)

	keyB, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "Key B",
	})
	require.NoError(s.T(), err)

	// Try to rename Key B to Key A
	dupName := "Key A"
	_, err = s.scopedKeyService.UpdateScopedSigningKey(s.ctx, keyB.ID, UpdateScopedSigningKeyRequest{
		Name: &dupName,
	})
	assert.ErrorIs(s.T(), err, repositories.ErrAlreadyExists)
}

// TestUpdateScopedSigningKey_ResponseLimits tests updating response limits
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey_ResponseLimits() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "Response Key",
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 0, created.ResponseMaxMsgs)

	// Update response limits
	maxMsgs := 25
	ttl := 10 * time.Second
	updated, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, created.ID, UpdateScopedSigningKeyRequest{
		ResponseMaxMsgs: &maxMsgs,
		ResponseTTL:     &ttl,
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 25, updated.ResponseMaxMsgs)
	assert.Equal(s.T(), 10*time.Second, updated.ResponseTTL)
}

// TestDeleteScopedSigningKey tests key deletion
func (s *ScopedSigningKeyServiceTestSuite) TestDeleteScopedSigningKey() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "Delete Key",
	})
	require.NoError(s.T(), err)

	// Delete key
	err = s.scopedKeyService.DeleteScopedSigningKey(s.ctx, created.ID)
	require.NoError(s.T(), err)

	// Verify it's gone
	_, err = s.scopedKeyService.GetScopedSigningKey(s.ctx, created.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestDeleteScopedSigningKey_NotFound tests deleting a non-existent key
func (s *ScopedSigningKeyServiceTestSuite) TestDeleteScopedSigningKey_NotFound() {
	err := s.scopedKeyService.DeleteScopedSigningKey(s.ctx, uuid.New())
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestScopedSigningKey_AccountAssociation tests that keys are correctly associated with accounts
func (s *ScopedSigningKeyServiceTestSuite) TestScopedSigningKey_AccountAssociation() {
	// Create two accounts under the same operator
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Multi Account Operator",
	})
	require.NoError(s.T(), err)

	account1, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Account 1",
	})
	require.NoError(s.T(), err)

	account2, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Account 2",
	})
	require.NoError(s.T(), err)

	// Create keys in each account
	key1, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: account1.ID,
		Name:      "Key For Account 1",
	})
	require.NoError(s.T(), err)

	key2, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: account2.ID,
		Name:      "Key For Account 2",
	})
	require.NoError(s.T(), err)

	// Verify each key is associated with the correct account
	assert.Equal(s.T(), account1.ID, key1.AccountID)
	assert.Equal(s.T(), account2.ID, key2.AccountID)

	// List keys for account 1 (should have default + 1 created = 2)
	keys1, err := s.scopedKeyService.ListScopedSigningKeysByAccount(s.ctx, account1.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), keys1, 2)

	// List keys for account 2 (should have default + 1 created = 2)
	keys2, err := s.scopedKeyService.ListScopedSigningKeysByAccount(s.ctx, account2.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), keys2, 2)

	// Same name can be used in different accounts
	_, err = s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: account2.ID,
		Name:      "Key For Account 1", // Same name as in account1
	})
	assert.NoError(s.T(), err) // Should succeed because it's a different account
}
