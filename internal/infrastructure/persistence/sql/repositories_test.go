package sql

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/config"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

type RepositoryTestSuite struct {
	suite.Suite
	db          *gorm.DB
	operatorRepo *OperatorRepo
	accountRepo  *AccountRepo
	userRepo     *UserRepo
	scopedKeyRepo *ScopedSigningKeyRepo
	clusterRepo  *ClusterRepo
	apiUserRepo  *APIUserRepo
}

func (s *RepositoryTestSuite) SetupSuite() {
	// Create in-memory SQLite database
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		Path:   ":memory:",
	}

	db, err := NewDB(cfg)
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

	// Create repositories
	s.operatorRepo = NewOperatorRepo(db)
	s.accountRepo = NewAccountRepo(db)
	s.userRepo = NewUserRepo(db)
	s.scopedKeyRepo = NewScopedSigningKeyRepo(db)
	s.clusterRepo = NewClusterRepo(db)
	s.apiUserRepo = NewAPIUserRepo(db)
}

func (s *RepositoryTestSuite) TearDownSuite() {
	sqlDB, _ := s.db.DB()
	sqlDB.Close()
}

func (s *RepositoryTestSuite) SetupTest() {
	// Clean all tables before each test
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	s.db.Exec("DELETE FROM api_users")
}

func (s *RepositoryTestSuite) TestOperatorCRUD() {
	ctx := context.Background()

	// Create
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "test-operator",
		Description:   "Test operator",
		EncryptedSeed: "encrypted:key-1:abcdef",
		PublicKey:     "OABC123",
		JWT:           "jwt.token.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	err := s.operatorRepo.Create(ctx, operator)
	require.NoError(s.T(), err)

	// GetByID
	retrieved, err := s.operatorRepo.GetByID(ctx, operator.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), operator.Name, retrieved.Name)
	assert.Equal(s.T(), operator.PublicKey, retrieved.PublicKey)

	// GetByName
	retrieved, err = s.operatorRepo.GetByName(ctx, operator.Name)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), operator.ID, retrieved.ID)

	// GetByPublicKey
	retrieved, err = s.operatorRepo.GetByPublicKey(ctx, operator.PublicKey)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), operator.ID, retrieved.ID)

	// List
	operators, err := s.operatorRepo.List(ctx, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), operators, 1)

	// Update
	operator.Description = "Updated description"
	operator.UpdatedAt = time.Now()
	err = s.operatorRepo.Update(ctx, operator)
	require.NoError(s.T(), err)

	retrieved, err = s.operatorRepo.GetByID(ctx, operator.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "Updated description", retrieved.Description)

	// Delete
	err = s.operatorRepo.Delete(ctx, operator.ID)
	require.NoError(s.T(), err)

	_, err = s.operatorRepo.GetByID(ctx, operator.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

func (s *RepositoryTestSuite) TestAccountCRUD() {
	ctx := context.Background()

	// Create operator first
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "test-operator",
		EncryptedSeed: "encrypted:key-1:abcdef",
		PublicKey:     "OABC123",
		JWT:           "jwt.token.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	err := s.operatorRepo.Create(ctx, operator)
	require.NoError(s.T(), err)

	// Create account
	account := &entities.Account{
		ID:                    uuid.New(),
		OperatorID:            operator.ID,
		Name:                  "test-account",
		Description:           "Test account",
		EncryptedSeed:         "encrypted:key-1:xyz",
		PublicKey:             "AABC456",
		JWT:                   "account.jwt.here",
		JetStreamEnabled:      true,
		JetStreamMaxMemory:    1073741824,
		JetStreamMaxStorage:   10737418240,
		JetStreamMaxStreams:   10,
		JetStreamMaxConsumers: 100,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	err = s.accountRepo.Create(ctx, account)
	require.NoError(s.T(), err)

	// GetByID
	retrieved, err := s.accountRepo.GetByID(ctx, account.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), account.Name, retrieved.Name)
	assert.Equal(s.T(), account.JetStreamEnabled, retrieved.JetStreamEnabled)

	// GetByName
	retrieved, err = s.accountRepo.GetByName(ctx, operator.ID, account.Name)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), account.ID, retrieved.ID)

	// ListByOperator
	accounts, err := s.accountRepo.ListByOperator(ctx, operator.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), accounts, 1)

	// Update
	account.Description = "Updated account"
	err = s.accountRepo.Update(ctx, account)
	require.NoError(s.T(), err)

	retrieved, err = s.accountRepo.GetByID(ctx, account.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "Updated account", retrieved.Description)

	// Delete account
	err = s.accountRepo.Delete(ctx, account.ID)
	require.NoError(s.T(), err)
}

func (s *RepositoryTestSuite) TestUserCRUD() {
	ctx := context.Background()

	// Setup operator and account
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "test-operator",
		EncryptedSeed: "encrypted:key-1:abcdef",
		PublicKey:     "OABC123",
		JWT:           "jwt.token.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	err := s.operatorRepo.Create(ctx, operator)
	require.NoError(s.T(), err)

	account := &entities.Account{
		ID:            uuid.New(),
		OperatorID:    operator.ID,
		Name:          "test-account",
		EncryptedSeed: "encrypted:key-1:xyz",
		PublicKey:     "AABC456",
		JWT:           "account.jwt.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	err = s.accountRepo.Create(ctx, account)
	require.NoError(s.T(), err)

	// Create user
	user := &entities.User{
		ID:            uuid.New(),
		AccountID:     account.ID,
		Name:          "test-user",
		Description:   "Test user",
		EncryptedSeed: "encrypted:key-1:user123",
		PublicKey:     "UABC789",
		JWT:           "user.jwt.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	err = s.userRepo.Create(ctx, user)
	require.NoError(s.T(), err)

	// GetByID
	retrieved, err := s.userRepo.GetByID(ctx, user.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), user.Name, retrieved.Name)

	// GetByName
	retrieved, err = s.userRepo.GetByName(ctx, account.ID, user.Name)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), user.ID, retrieved.ID)

	// ListByAccount
	users, err := s.userRepo.ListByAccount(ctx, account.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), users, 1)

	// Delete
	err = s.userRepo.Delete(ctx, user.ID)
	require.NoError(s.T(), err)
}

func (s *RepositoryTestSuite) TestCascadeDelete() {
	ctx := context.Background()

	// Create operator
	operator := &entities.Operator{
		ID:            uuid.New(),
		Name:          "cascade-operator",
		EncryptedSeed: "encrypted:key-1:abcdef",
		PublicKey:     "OABC123",
		JWT:           "jwt.token.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	err := s.operatorRepo.Create(ctx, operator)
	require.NoError(s.T(), err)

	// Create account
	account := &entities.Account{
		ID:            uuid.New(),
		OperatorID:    operator.ID,
		Name:          "cascade-account",
		EncryptedSeed: "encrypted:key-1:xyz",
		PublicKey:     "AABC456",
		JWT:           "account.jwt.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	err = s.accountRepo.Create(ctx, account)
	require.NoError(s.T(), err)

	// Create user
	user := &entities.User{
		ID:            uuid.New(),
		AccountID:     account.ID,
		Name:          "cascade-user",
		EncryptedSeed: "encrypted:key-1:user123",
		PublicKey:     "UABC789",
		JWT:           "user.jwt.here",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	err = s.userRepo.Create(ctx, user)
	require.NoError(s.T(), err)

	// Delete operator (should cascade to account and user)
	err = s.operatorRepo.Delete(ctx, operator.ID)
	require.NoError(s.T(), err)

	// Verify account was deleted
	_, err = s.accountRepo.GetByID(ctx, account.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)

	// Verify user was deleted
	_, err = s.userRepo.GetByID(ctx, user.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

func (s *RepositoryTestSuite) TestAPIUserCRUD() {
	ctx := context.Background()

	// Create API user
	apiUser := &entities.APIUser{
		ID:           uuid.New(),
		Username:     "admin",
		PasswordHash: "$2a$10$abc123...",
		Role:         entities.RoleAdmin,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	err := s.apiUserRepo.Create(ctx, apiUser)
	require.NoError(s.T(), err)

	// GetByID
	retrieved, err := s.apiUserRepo.GetByID(ctx, apiUser.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), apiUser.Username, retrieved.Username)
	assert.Equal(s.T(), apiUser.Role, retrieved.Role)

	// GetByUsername
	retrieved, err = s.apiUserRepo.GetByUsername(ctx, apiUser.Username)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), apiUser.ID, retrieved.ID)

	// List
	users, err := s.apiUserRepo.List(ctx, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.Len(s.T(), users, 1)

	// Delete
	err = s.apiUserRepo.Delete(ctx, apiUser.ID)
	require.NoError(s.T(), err)
}

func (s *RepositoryTestSuite) TestPagination() {
	ctx := context.Background()

	// Create multiple operators
	for i := 0; i < 5; i++ {
		operator := &entities.Operator{
			ID:            uuid.New(),
			Name:          "operator-" + string(rune('a'+i)),
			EncryptedSeed: "encrypted:key-1:abcdef",
			PublicKey:     "O" + string(rune('A'+i)) + "123",
			JWT:           "jwt.token.here",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		err := s.operatorRepo.Create(ctx, operator)
		require.NoError(s.T(), err)
		time.Sleep(time.Millisecond) // Ensure different timestamps
	}

	// Test limit
	operators, err := s.operatorRepo.List(ctx, repositories.ListOptions{Limit: 2})
	require.NoError(s.T(), err)
	assert.Len(s.T(), operators, 2)

	// Test offset
	operators, err = s.operatorRepo.List(ctx, repositories.ListOptions{Offset: 2, Limit: 2})
	require.NoError(s.T(), err)
	assert.Len(s.T(), operators, 2)
}

func TestRepositoryTestSuite(t *testing.T) {
	suite.Run(t, new(RepositoryTestSuite))
}
