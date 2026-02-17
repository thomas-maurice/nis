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

type OperatorServiceTestSuite struct {
	suite.Suite
	ctx                   context.Context
	db                    *gorm.DB
	encryptor             encryption.Encryptor
	jwtService            *JWTService
	operatorRepo          repositories.OperatorRepository
	accountRepo           repositories.AccountRepository
	userRepo              repositories.UserRepository
	scopedSigningKeyRepo  repositories.ScopedSigningKeyRepository
	accountService        *AccountService
	operatorService       *OperatorService
}

func (s *OperatorServiceTestSuite) SetupSuite() {
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

	// Create accountService first (required by operatorService)
	s.accountService = NewAccountService(s.accountRepo, s.operatorRepo, s.scopedSigningKeyRepo, s.jwtService, s.encryptor)
	s.operatorService = NewOperatorService(s.operatorRepo, s.accountRepo, s.userRepo, s.accountService, s.jwtService, s.encryptor)
}

func (s *OperatorServiceTestSuite) TearDownSuite() {
	_ = sql.Close(s.db)
}

func (s *OperatorServiceTestSuite) TearDownTest() {
	// Clean up database after each test
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	s.db.Exec("DELETE FROM api_users")
}

func TestOperatorServiceSuite(t *testing.T) {
	suite.Run(t, new(OperatorServiceTestSuite))
}

// TestCreateOperator tests operator creation
func (s *OperatorServiceTestSuite) TestCreateOperator() {
	req := CreateOperatorRequest{
		Name:        "Test Operator",
		Description: "A test operator",
	}

	operator, err := s.operatorService.CreateOperator(s.ctx, req)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), operator)
	assert.NotEqual(s.T(), uuid.Nil, operator.ID)
	assert.Equal(s.T(), req.Name, operator.Name)
	assert.Equal(s.T(), req.Description, operator.Description)
	assert.NotEmpty(s.T(), operator.PublicKey)
	assert.Equal(s.T(), byte('O'), operator.PublicKey[0]) // Operator key prefix
	assert.NotEmpty(s.T(), operator.EncryptedSeed)
	assert.NotEmpty(s.T(), operator.JWT)

	// Verify it was saved to repository
	retrieved, err := s.operatorRepo.GetByID(s.ctx, operator.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), operator.ID, retrieved.ID)
	assert.Equal(s.T(), operator.Name, retrieved.Name)
}

// TestCreateOperator_WithSystemAccount tests operator creation with automatic $SYS account
func (s *OperatorServiceTestSuite) TestCreateOperator_WithSystemAccount() {
	req := CreateOperatorRequest{
		Name: "Test Operator",
	}

	operator, err := s.operatorService.CreateOperator(s.ctx, req)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), operator.SystemAccountPubKey)
	assert.Equal(s.T(), byte('A'), operator.SystemAccountPubKey[0]) // Account key prefix
	assert.NotEmpty(s.T(), operator.JWT)

	// Verify $SYS account was created
	sysAccount, err := s.accountRepo.GetByName(s.ctx, operator.ID, "$SYS")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "$SYS", sysAccount.Name)
	assert.Equal(s.T(), operator.SystemAccountPubKey, sysAccount.PublicKey)
	assert.False(s.T(), sysAccount.JetStreamEnabled) // JetStream must be disabled on $SYS
}

// TestCreateOperator_DuplicateName tests that duplicate names are rejected
func (s *OperatorServiceTestSuite) TestCreateOperator_DuplicateName() {
	req := CreateOperatorRequest{
		Name: "Duplicate Operator",
	}

	// Create first operator
	_, err := s.operatorService.CreateOperator(s.ctx, req)
	require.NoError(s.T(), err)

	// Try to create second with same name
	_, err = s.operatorService.CreateOperator(s.ctx, req)
	assert.ErrorIs(s.T(), err, repositories.ErrAlreadyExists)
}

// TestCreateOperator_EmptyName tests validation
func (s *OperatorServiceTestSuite) TestCreateOperator_EmptyName() {
	req := CreateOperatorRequest{
		Name: "",
	}

	_, err := s.operatorService.CreateOperator(s.ctx, req)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "name is required")
}

// TestGetOperator tests retrieving an operator
func (s *OperatorServiceTestSuite) TestGetOperator() {
	// Create operator
	created, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Test Operator",
	})
	require.NoError(s.T(), err)

	// Get by ID
	operator, err := s.operatorService.GetOperator(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.ID, operator.ID)
	assert.Equal(s.T(), created.Name, operator.Name)
}

// TestGetOperatorByName tests retrieving an operator by name
func (s *OperatorServiceTestSuite) TestGetOperatorByName() {
	// Create operator
	created, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Test Operator",
	})
	require.NoError(s.T(), err)

	// Get by name
	operator, err := s.operatorService.GetOperatorByName(s.ctx, "Test Operator")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), created.ID, operator.ID)
}

// TestListOperators tests listing operators with pagination
func (s *OperatorServiceTestSuite) TestListOperators() {
	// Create multiple operators
	for i := 0; i < 5; i++ {
		_, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
			Name: fmt.Sprintf("Operator %d", i),
		})
		require.NoError(s.T(), err)
	}

	// List all
	operators, err := s.operatorService.ListOperators(s.ctx, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), operators, 5)

	// List with limit
	operators, err = s.operatorService.ListOperators(s.ctx, repositories.ListOptions{Limit: 2})
	require.NoError(s.T(), err)
	assert.Len(s.T(), operators, 2)
}

// TestUpdateOperator tests updating operator metadata
func (s *OperatorServiceTestSuite) TestUpdateOperator() {
	// Create operator
	created, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "Original Name",
		Description: "Original Description",
	})
	require.NoError(s.T(), err)
	originalJWT := created.JWT

	// Update name and description
	newName := "Updated Name"
	newDesc := "Updated Description"
	updated, err := s.operatorService.UpdateOperator(s.ctx, created.ID, UpdateOperatorRequest{
		Name:        &newName,
		Description: &newDesc,
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), newName, updated.Name)
	assert.Equal(s.T(), newDesc, updated.Description)
	assert.NotEqual(s.T(), originalJWT, updated.JWT) // JWT should be regenerated
}

// TestUpdateOperator_NoChanges tests updating without changes
func (s *OperatorServiceTestSuite) TestUpdateOperator_NoChanges() {
	// Create operator
	created, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Test Operator",
	})
	require.NoError(s.T(), err)
	originalJWT := created.JWT

	// Update with no changes
	updated, err := s.operatorService.UpdateOperator(s.ctx, created.ID, UpdateOperatorRequest{})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), originalJWT, updated.JWT) // JWT should not be regenerated
}

// TestSetSystemAccount tests setting system account
func (s *OperatorServiceTestSuite) TestSetSystemAccount() {
	// Create operator
	created, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Test Operator",
	})
	require.NoError(s.T(), err)
	originalJWT := created.JWT

	// Set system account
	updated, err := s.operatorService.SetSystemAccount(s.ctx, created.ID, "AABC123")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "AABC123", updated.SystemAccountPubKey)
	assert.NotEqual(s.T(), originalJWT, updated.JWT) // JWT should be regenerated
}

// TestSetSystemAccount_InvalidKey tests validation
func (s *OperatorServiceTestSuite) TestSetSystemAccount_InvalidKey() {
	// Create operator
	created, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Test Operator",
	})
	require.NoError(s.T(), err)

	// Try to set invalid system account key (should start with 'A')
	_, err = s.operatorService.SetSystemAccount(s.ctx, created.ID, "OABC123")
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "must start with 'A'")
}

// TestDeleteOperator tests operator deletion
func (s *OperatorServiceTestSuite) TestDeleteOperator() {
	// Create operator
	created, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Test Operator",
	})
	require.NoError(s.T(), err)

	// Delete operator
	err = s.operatorService.DeleteOperator(s.ctx, created.ID)
	require.NoError(s.T(), err)

	// Verify it's gone
	_, err = s.operatorService.GetOperator(s.ctx, created.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestDeleteOperator_NotFound tests deleting non-existent operator
func (s *OperatorServiceTestSuite) TestDeleteOperator_NotFound() {
	err := s.operatorService.DeleteOperator(s.ctx, uuid.New())
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}
