package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/config"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

type UserServiceTestSuite struct {
	suite.Suite
	db                 *gorm.DB
	userService        *UserService
	operatorService    *OperatorService
	accountService     *AccountService
	encryptor          encryption.Encryptor
	ctx                context.Context
	userRepo           repositories.UserRepository
	accountRepo        repositories.AccountRepository
	operatorRepo       repositories.OperatorRepository
	scopedKeyRepo      repositories.ScopedSigningKeyRepository
}

func (s *UserServiceTestSuite) SetupSuite() {
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

	// Create repositories
	s.operatorRepo = sql.NewOperatorRepo(s.db)
	s.accountRepo = sql.NewAccountRepo(s.db)
	s.userRepo = sql.NewUserRepo(s.db)
	s.scopedKeyRepo = sql.NewScopedSigningKeyRepo(s.db)

	// Create services
	jwtService := NewJWTService(s.encryptor)

	s.operatorService = NewOperatorService(
		s.operatorRepo,
		s.accountRepo,
		s.userRepo,
		jwtService,
		s.encryptor,
	)

	s.accountService = NewAccountService(
		s.accountRepo,
		s.operatorRepo,
		s.scopedKeyRepo,
		jwtService,
		s.encryptor,
	)

	s.userService = NewUserService(
		s.userRepo,
		s.accountRepo,
		s.scopedKeyRepo,
		jwtService,
		s.encryptor,
	)
}

func (s *UserServiceTestSuite) TearDownSuite() {
	sql.Close(s.db)
}

func (s *UserServiceTestSuite) TearDownTest() {
	// Clean up database after each test
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
}

// TestDeleteSystemUser_Protected tests that system users cannot be deleted directly
func (s *UserServiceTestSuite) TestDeleteSystemUser_Protected() {
	// Create operator (which creates $SYS account and system user automatically)
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "test-operator",
		Description: "Test operator",
	})
	s.Require().NoError(err)

	// Get the $SYS account
	sysAccount, err := s.accountService.GetAccountByName(s.ctx, operator.ID, "$SYS")
	s.Require().NoError(err)

	// Get the system user
	users, err := s.userService.ListUsersByAccount(s.ctx, sysAccount.ID, repositories.ListOptions{})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	systemUser := users[0]
	s.Equal("system", systemUser.Name)

	// Attempt to delete system user directly
	err = s.userService.DeleteUser(s.ctx, systemUser.ID)
	s.Error(err)
	s.Contains(err.Error(), "cannot delete system user")

	// Verify user still exists
	user, err := s.userService.GetUser(s.ctx, systemUser.ID)
	s.NoError(err)
	s.NotNil(user)
}

// TestDeleteSystemAccount_Protected tests that $SYS account cannot be deleted directly
func (s *UserServiceTestSuite) TestDeleteSystemAccount_Protected() {
	// Create operator (which creates $SYS account and system user automatically)
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "test-operator",
		Description: "Test operator",
	})
	s.Require().NoError(err)

	// Get the $SYS account
	sysAccount, err := s.accountService.GetAccountByName(s.ctx, operator.ID, "$SYS")
	s.Require().NoError(err)

	// Get the system user
	users, err := s.userService.ListUsersByAccount(s.ctx, sysAccount.ID, repositories.ListOptions{})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	systemUser := users[0]

	// Attempt to delete the $SYS account directly (should fail because it's the system account)
	err = s.accountService.DeleteAccount(s.ctx, sysAccount.ID)
	s.Error(err)
	s.Contains(err.Error(), "cannot delete system account")

	// Verify system user still exists (because account deletion was blocked)
	user, err := s.userService.GetUser(s.ctx, systemUser.ID)
	s.NoError(err)
	s.NotNil(user)
}

// TestDeleteSystemUser_CascadeOnOperatorDelete tests that system users are deleted when operator is deleted
func (s *UserServiceTestSuite) TestDeleteSystemUser_CascadeOnOperatorDelete() {
	// Create operator (which creates $SYS account and system user automatically)
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "test-operator",
		Description: "Test operator",
	})
	s.Require().NoError(err)

	// Get the $SYS account
	sysAccount, err := s.accountService.GetAccountByName(s.ctx, operator.ID, "$SYS")
	s.Require().NoError(err)

	// Get the system user
	users, err := s.userService.ListUsersByAccount(s.ctx, sysAccount.ID, repositories.ListOptions{})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	systemUser := users[0]

	// Delete the operator (this should cascade delete account and system user)
	err = s.operatorService.DeleteOperator(s.ctx, operator.ID)
	s.NoError(err)

	// Verify system user was cascade deleted
	user, err := s.userService.GetUser(s.ctx, systemUser.ID)
	s.Error(err)
	s.Equal(repositories.ErrNotFound, err)
	s.Nil(user)

	// Verify account was cascade deleted
	account, err := s.accountService.GetAccount(s.ctx, sysAccount.ID)
	s.Error(err)
	s.Equal(repositories.ErrNotFound, err)
	s.Nil(account)
}

// TestDeleteRegularUser tests that regular users can be deleted normally
func (s *UserServiceTestSuite) TestDeleteRegularUser() {
	// Create operator
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "test-operator",
		Description: "Test operator",
	})
	s.Require().NoError(err)

	// Create a regular account
	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID:  operator.ID,
		Name:        "test-account",
		Description: "Test account",
	})
	s.Require().NoError(err)

	// Create a regular user
	user, err := s.userService.CreateUser(s.ctx, CreateUserRequest{
		AccountID:   account.ID,
		Name:        "test-user",
		Description: "Test user",
	})
	s.Require().NoError(err)

	// Delete the regular user (should succeed)
	err = s.userService.DeleteUser(s.ctx, user.ID)
	s.NoError(err)

	// Verify user was deleted
	deletedUser, err := s.userService.GetUser(s.ctx, user.ID)
	s.Error(err)
	s.Equal(repositories.ErrNotFound, err)
	s.Nil(deletedUser)
}

// TestDeleteUser_NotFound tests deleting a non-existent user
func (s *UserServiceTestSuite) TestDeleteUser_NotFound() {
	// Attempt to delete non-existent user
	err := s.userService.DeleteUser(s.ctx, uuid.New())
	s.Error(err)
	s.Equal(repositories.ErrNotFound, err)
}

func TestUserServiceTestSuite(t *testing.T) {
	suite.Run(t, new(UserServiceTestSuite))
}
