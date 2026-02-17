package services

import (
	"context"
	"fmt"
	"testing"

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

type AccountServiceTestSuite struct {
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
	userService          *UserService
}

func (s *AccountServiceTestSuite) SetupSuite() {
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
	s.userService = NewUserService(s.userRepo, s.accountRepo, s.scopedSigningKeyRepo, s.jwtService, s.encryptor)
}

func (s *AccountServiceTestSuite) TearDownSuite() {
	sql.Close(s.db)
}

func (s *AccountServiceTestSuite) TearDownTest() {
	// Clean up database after each test
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	s.db.Exec("DELETE FROM api_users")
}

func TestAccountServiceSuite(t *testing.T) {
	suite.Run(t, new(AccountServiceTestSuite))
}

// createTestOperator is a helper that creates an operator for account tests
func (s *AccountServiceTestSuite) createTestOperator(name string) *CreateOperatorRequest {
	return &CreateOperatorRequest{
		Name:        name,
		Description: "Test operator for account tests",
	}
}

// TestCreateAccount tests account creation under an operator
func (s *AccountServiceTestSuite) TestCreateAccount() {
	// Create operator first
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	req := CreateAccountRequest{
		OperatorID:  operator.ID,
		Name:        "Test Account",
		Description: "A test account",
	}

	account, err := s.accountService.CreateAccount(s.ctx, req)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), account)
	assert.NotEqual(s.T(), uuid.Nil, account.ID)
	assert.Equal(s.T(), req.Name, account.Name)
	assert.Equal(s.T(), req.Description, account.Description)
	assert.Equal(s.T(), operator.ID, account.OperatorID)
	assert.NotEmpty(s.T(), account.PublicKey)
	assert.Equal(s.T(), byte('A'), account.PublicKey[0]) // Account key prefix
	assert.NotEmpty(s.T(), account.EncryptedSeed)
	assert.NotEmpty(s.T(), account.JWT)

	// Verify it was saved to repository
	retrieved, err := s.accountRepo.GetByID(s.ctx, account.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), account.ID, retrieved.ID)
	assert.Equal(s.T(), account.Name, retrieved.Name)
}

// TestCreateAccount_WithJetStreamLimits tests account creation with JetStream configuration
func (s *AccountServiceTestSuite) TestCreateAccount_WithJetStreamLimits() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	req := CreateAccountRequest{
		OperatorID:            operator.ID,
		Name:                  "JetStream Account",
		Description:           "An account with JetStream",
		JetStreamEnabled:      true,
		JetStreamMaxMemory:    1024 * 1024 * 1024,      // 1GB
		JetStreamMaxStorage:   10 * 1024 * 1024 * 1024,  // 10GB
		JetStreamMaxStreams:   100,
		JetStreamMaxConsumers: 1000,
	}

	account, err := s.accountService.CreateAccount(s.ctx, req)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), account)
	assert.True(s.T(), account.JetStreamEnabled)
	assert.Equal(s.T(), req.JetStreamMaxMemory, account.JetStreamMaxMemory)
	assert.Equal(s.T(), req.JetStreamMaxStorage, account.JetStreamMaxStorage)
	assert.Equal(s.T(), req.JetStreamMaxStreams, account.JetStreamMaxStreams)
	assert.Equal(s.T(), req.JetStreamMaxConsumers, account.JetStreamMaxConsumers)
	assert.NotEmpty(s.T(), account.JWT)
}

// TestCreateAccount_DefaultScopedSigningKey tests that a default scoped signing key is created
func (s *AccountServiceTestSuite) TestCreateAccount_DefaultScopedSigningKey() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)

	// Verify default scoped signing key was created
	scopedKeys, err := s.scopedSigningKeyRepo.ListByAccount(s.ctx, account.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), scopedKeys, 1)
	assert.Equal(s.T(), "default", scopedKeys[0].Name)
	assert.NotEmpty(s.T(), scopedKeys[0].PublicKey)
	assert.Equal(s.T(), byte('A'), scopedKeys[0].PublicKey[0])
}

// TestCreateAccount_EmptyName tests validation for empty name
func (s *AccountServiceTestSuite) TestCreateAccount_EmptyName() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	_, err = s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "",
	})
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "account name is required")
}

// TestCreateAccount_DuplicateName tests that duplicate names under the same operator are rejected
func (s *AccountServiceTestSuite) TestCreateAccount_DuplicateName() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	req := CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Duplicate Account",
	}

	// Create first account
	_, err = s.accountService.CreateAccount(s.ctx, req)
	require.NoError(s.T(), err)

	// Try to create second with same name
	_, err = s.accountService.CreateAccount(s.ctx, req)
	assert.ErrorIs(s.T(), err, repositories.ErrAlreadyExists)
}

// TestCreateAccount_InvalidOperator tests account creation with non-existent operator
func (s *AccountServiceTestSuite) TestCreateAccount_InvalidOperator() {
	_, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: uuid.New(),
		Name:       "Test Account",
	})
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to get operator")
}

// TestGetAccount tests retrieving an account by ID
func (s *AccountServiceTestSuite) TestGetAccount() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	created, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)

	// Get by ID
	account, err := s.accountService.GetAccount(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.ID, account.ID)
	assert.Equal(s.T(), created.Name, account.Name)
}

// TestGetAccount_NotFound tests retrieving a non-existent account
func (s *AccountServiceTestSuite) TestGetAccount_NotFound() {
	_, err := s.accountService.GetAccount(s.ctx, uuid.New())
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestGetAccountByName tests retrieving an account by operator ID and name
func (s *AccountServiceTestSuite) TestGetAccountByName() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	created, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)

	// Get by name
	account, err := s.accountService.GetAccountByName(s.ctx, operator.ID, "Test Account")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.ID, account.ID)
}

// TestListAccountsByOperator tests listing accounts for an operator with pagination
func (s *AccountServiceTestSuite) TestListAccountsByOperator() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	// Create multiple accounts (note: operator already has $SYS account)
	for i := 0; i < 3; i++ {
		_, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
			OperatorID: operator.ID,
			Name:       fmt.Sprintf("Account %d", i),
		})
		require.NoError(s.T(), err)
	}

	// List all (should include $SYS + 3 created = 4 total)
	accounts, err := s.accountService.ListAccountsByOperator(s.ctx, operator.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), accounts, 4)

	// List with limit
	accounts, err = s.accountService.ListAccountsByOperator(s.ctx, operator.ID, repositories.ListOptions{Limit: 2})
	require.NoError(s.T(), err)
	assert.Len(s.T(), accounts, 2)
}

// TestUpdateAccount tests updating account metadata
func (s *AccountServiceTestSuite) TestUpdateAccount() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	created, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID:  operator.ID,
		Name:        "Original Name",
		Description: "Original Description",
	})
	require.NoError(s.T(), err)
	originalJWT := created.JWT

	// Update name and description
	newName := "Updated Name"
	newDesc := "Updated Description"
	updated, err := s.accountService.UpdateAccount(s.ctx, created.ID, UpdateAccountRequest{
		Name:        &newName,
		Description: &newDesc,
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), newName, updated.Name)
	assert.Equal(s.T(), newDesc, updated.Description)
	assert.NotEqual(s.T(), originalJWT, updated.JWT) // JWT should be regenerated
}

// TestUpdateAccount_NoChanges tests updating without changes
func (s *AccountServiceTestSuite) TestUpdateAccount_NoChanges() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	created, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)
	originalJWT := created.JWT

	// Update with no changes
	updated, err := s.accountService.UpdateAccount(s.ctx, created.ID, UpdateAccountRequest{})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), originalJWT, updated.JWT) // JWT should not be regenerated
}

// TestUpdateAccount_DuplicateName tests that renaming to an existing name is rejected
func (s *AccountServiceTestSuite) TestUpdateAccount_DuplicateName() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	_, err = s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Account A",
	})
	require.NoError(s.T(), err)

	accountB, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Account B",
	})
	require.NoError(s.T(), err)

	// Try to rename Account B to Account A
	dupName := "Account A"
	_, err = s.accountService.UpdateAccount(s.ctx, accountB.ID, UpdateAccountRequest{
		Name: &dupName,
	})
	assert.ErrorIs(s.T(), err, repositories.ErrAlreadyExists)
}

// TestUpdateJetStreamLimits tests updating JetStream limits
func (s *AccountServiceTestSuite) TestUpdateJetStreamLimits() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	created, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)
	assert.False(s.T(), created.JetStreamEnabled)

	// Enable JetStream with limits
	updated, err := s.accountService.UpdateJetStreamLimits(s.ctx, created.ID, UpdateJetStreamLimitsRequest{
		Enabled:      true,
		MaxMemory:    512 * 1024 * 1024,
		MaxStorage:   5 * 1024 * 1024 * 1024,
		MaxStreams:   50,
		MaxConsumers: 500,
	})
	require.NoError(s.T(), err)
	assert.True(s.T(), updated.JetStreamEnabled)
	assert.Equal(s.T(), int64(512*1024*1024), updated.JetStreamMaxMemory)
	assert.Equal(s.T(), int64(5*1024*1024*1024), updated.JetStreamMaxStorage)
	assert.Equal(s.T(), int64(50), updated.JetStreamMaxStreams)
	assert.Equal(s.T(), int64(500), updated.JetStreamMaxConsumers)
	assert.NotEqual(s.T(), created.JWT, updated.JWT) // JWT should be regenerated
}

// TestDeleteAccount tests account deletion
func (s *AccountServiceTestSuite) TestDeleteAccount() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	created, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)

	// Delete account
	err = s.accountService.DeleteAccount(s.ctx, created.ID)
	require.NoError(s.T(), err)

	// Verify it's gone
	_, err = s.accountService.GetAccount(s.ctx, created.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestDeleteAccount_CascadeToUsers tests that deleting an account also deletes its users
func (s *AccountServiceTestSuite) TestDeleteAccount_CascadeToUsers() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "Test Account",
	})
	require.NoError(s.T(), err)

	// Get the default scoped signing key for user creation
	scopedKeys, err := s.scopedSigningKeyRepo.ListByAccount(s.ctx, account.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), scopedKeys)

	// Create users under this account
	user, err := s.userService.CreateUser(s.ctx, CreateUserRequest{
		AccountID:          account.ID,
		Name:               "Test User",
		ScopedSigningKeyID: &scopedKeys[0].ID,
	})
	require.NoError(s.T(), err)

	// Delete the account
	err = s.accountService.DeleteAccount(s.ctx, account.ID)
	require.NoError(s.T(), err)

	// Verify user is also gone
	_, err = s.userRepo.GetByID(s.ctx, user.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestDeleteAccount_NotFound tests deleting a non-existent account
func (s *AccountServiceTestSuite) TestDeleteAccount_NotFound() {
	err := s.accountService.DeleteAccount(s.ctx, uuid.New())
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestDeleteAccount_SystemAccount tests that deleting the system account is rejected
func (s *AccountServiceTestSuite) TestDeleteAccount_SystemAccount() {
	operator, err := s.operatorService.CreateOperator(s.ctx, *s.createTestOperator("Test Operator"))
	require.NoError(s.T(), err)

	// Find the $SYS account
	sysAccount, err := s.accountService.GetAccountByName(s.ctx, operator.ID, "$SYS")
	require.NoError(s.T(), err)

	// Try to delete system account
	err = s.accountService.DeleteAccount(s.ctx, sysAccount.ID)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "cannot delete system account")
}
