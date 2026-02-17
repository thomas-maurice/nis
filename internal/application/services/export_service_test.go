package services

import (
	"context"
	"encoding/json"
	"testing"

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

type ExportServiceTestSuite struct {
	suite.Suite
	ctx                  context.Context
	db                   *gorm.DB
	encryptor            encryption.Encryptor
	jwtService           *JWTService
	operatorRepo         repositories.OperatorRepository
	accountRepo          repositories.AccountRepository
	userRepo             repositories.UserRepository
	scopedSigningKeyRepo repositories.ScopedSigningKeyRepository
	clusterRepo          repositories.ClusterRepository
	accountService       *AccountService
	operatorService      *OperatorService
	userService          *UserService
	scopedKeyService     *ScopedSigningKeyService
	clusterService       *ClusterService
	exportService        *ExportService
}

func (s *ExportServiceTestSuite) SetupSuite() {
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

	// Create repos
	s.jwtService = NewJWTService(s.encryptor)
	s.operatorRepo = sql.NewOperatorRepo(s.db)
	s.accountRepo = sql.NewAccountRepo(s.db)
	s.userRepo = sql.NewUserRepo(s.db)
	s.scopedSigningKeyRepo = sql.NewScopedSigningKeyRepo(s.db)
	s.clusterRepo = sql.NewClusterRepo(s.db)

	// Create services
	s.accountService = NewAccountService(s.accountRepo, s.operatorRepo, s.scopedSigningKeyRepo, s.jwtService, s.encryptor)
	s.operatorService = NewOperatorService(s.operatorRepo, s.accountRepo, s.userRepo, s.accountService, s.jwtService, s.encryptor)
	s.userService = NewUserService(s.userRepo, s.accountRepo, s.scopedSigningKeyRepo, s.jwtService, s.encryptor)
	s.scopedKeyService = NewScopedSigningKeyService(s.scopedSigningKeyRepo, s.accountRepo, s.encryptor)
	s.clusterService = NewClusterService(s.clusterRepo, s.operatorRepo, s.accountRepo, s.userRepo, s.scopedSigningKeyRepo, s.encryptor, s.jwtService)
	s.exportService = NewExportService(
		s.operatorRepo,
		s.accountRepo,
		s.userRepo,
		s.scopedSigningKeyRepo,
		s.clusterRepo,
		s.operatorService,
		s.accountService,
		s.userService,
		s.scopedKeyService,
		s.clusterService,
		s.encryptor,
	)
}

func (s *ExportServiceTestSuite) TearDownSuite() {
	sql.Close(s.db)
}

func (s *ExportServiceTestSuite) TearDownTest() {
	// Clean up database after each test
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	s.db.Exec("DELETE FROM api_users")
}

func TestExportServiceSuite(t *testing.T) {
	suite.Run(t, new(ExportServiceTestSuite))
}

// TestExportOperator tests exporting an operator with accounts and users
func (s *ExportServiceTestSuite) TestExportOperator() {
	// Create operator
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "Export Test Operator",
		Description: "Operator for export testing",
	})
	require.NoError(s.T(), err)

	// Create an account
	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID:       operator.ID,
		Name:             "Export Account",
		Description:      "Account for export testing",
		JetStreamEnabled: true,
		JetStreamMaxMemory: 1024 * 1024,
	})
	require.NoError(s.T(), err)

	// Get the default scoped signing key
	scopedKeys, err := s.scopedSigningKeyRepo.ListByAccount(s.ctx, account.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), scopedKeys)

	// Create a user under the account
	user, err := s.userService.CreateUser(s.ctx, CreateUserRequest{
		AccountID:          account.ID,
		Name:               "Export User",
		Description:        "User for export testing",
		ScopedSigningKeyID: &scopedKeys[0].ID,
	})
	require.NoError(s.T(), err)

	// Export with secrets
	exported, err := s.exportService.ExportOperator(s.ctx, operator.ID, true)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), exported)

	// Verify export structure
	assert.Equal(s.T(), "1.0", exported.Version)
	assert.NotZero(s.T(), exported.ExportedAt)

	// Verify operator data
	assert.Equal(s.T(), operator.ID, exported.Operator.ID)
	assert.Equal(s.T(), operator.Name, exported.Operator.Name)
	assert.Equal(s.T(), operator.Description, exported.Operator.Description)
	assert.Equal(s.T(), operator.PublicKey, exported.Operator.PublicKey)
	assert.NotEmpty(s.T(), exported.Operator.EncryptedSeed) // Included because includeSecrets=true
	assert.NotEmpty(s.T(), exported.Operator.JWT)

	// Verify accounts (should include $SYS + Export Account)
	assert.Len(s.T(), exported.Accounts, 2)

	// Find the non-system account in the export
	var exportedAccount *ExportedAccountData
	for _, a := range exported.Accounts {
		if a.Name == "Export Account" {
			exportedAccount = a
			break
		}
	}
	require.NotNil(s.T(), exportedAccount)
	assert.Equal(s.T(), account.ID, exportedAccount.ID)
	assert.Equal(s.T(), account.Name, exportedAccount.Name)
	assert.True(s.T(), exportedAccount.JetStreamEnabled)
	assert.Equal(s.T(), int64(1024*1024), exportedAccount.JetStreamMaxMemory)
	assert.NotEmpty(s.T(), exportedAccount.EncryptedSeed)

	// Verify users exist in export (at least the created user + system user)
	var exportedUser *ExportedUserData
	for _, u := range exported.Users {
		if u.Name == "Export User" {
			exportedUser = u
			break
		}
	}
	require.NotNil(s.T(), exportedUser)
	assert.Equal(s.T(), user.ID, exportedUser.ID)
	assert.Equal(s.T(), user.Name, exportedUser.Name)
	assert.NotEmpty(s.T(), exportedUser.EncryptedSeed)

	// Verify scoped keys exist in export
	assert.NotEmpty(s.T(), exported.ScopedKeys)
}

// TestExportOperator_WithoutSecrets tests exporting without including secrets
func (s *ExportServiceTestSuite) TestExportOperator_WithoutSecrets() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "No Secrets Operator",
	})
	require.NoError(s.T(), err)

	exported, err := s.exportService.ExportOperator(s.ctx, operator.ID, false)
	require.NoError(s.T(), err)

	// Verify secrets are not included
	assert.Empty(s.T(), exported.Operator.EncryptedSeed)

	// Accounts should also not have encrypted seeds
	for _, account := range exported.Accounts {
		assert.Empty(s.T(), account.EncryptedSeed)
	}
}

// TestExportOperatorJSON tests JSON export format
func (s *ExportServiceTestSuite) TestExportOperatorJSON() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "JSON Export Operator",
	})
	require.NoError(s.T(), err)

	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, true)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), data)

	// Verify it's valid JSON
	var exported ExportedOperator
	err = json.Unmarshal(data, &exported)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "1.0", exported.Version)
	assert.Equal(s.T(), "JSON Export Operator", exported.Operator.Name)
}

// TestExportAndImport tests the full export/import cycle
func (s *ExportServiceTestSuite) TestExportAndImport() {
	// Create operator with account and user
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "Import Test Operator",
		Description: "Operator for import testing",
	})
	require.NoError(s.T(), err)

	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID:       operator.ID,
		Name:             "Import Account",
		JetStreamEnabled: true,
		JetStreamMaxStreams: 50,
	})
	require.NoError(s.T(), err)

	scopedKeys, err := s.scopedSigningKeyRepo.ListByAccount(s.ctx, account.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), scopedKeys)

	_, err = s.userService.CreateUser(s.ctx, CreateUserRequest{
		AccountID:          account.ID,
		Name:               "Import User",
		ScopedSigningKeyID: &scopedKeys[0].ID,
	})
	require.NoError(s.T(), err)

	// Export to JSON
	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, true)
	require.NoError(s.T(), err)

	// Clean up the database to simulate importing into a fresh instance
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")

	// Import with regenerated IDs
	err = s.exportService.ImportOperatorJSON(s.ctx, data, true)
	require.NoError(s.T(), err)

	// Verify the imported operator exists
	importedOperator, err := s.operatorService.GetOperatorByName(s.ctx, "Import Test Operator")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "Import Test Operator", importedOperator.Name)
	assert.Equal(s.T(), "Operator for import testing", importedOperator.Description)
	// ID should be different since we regenerated IDs
	assert.NotEqual(s.T(), operator.ID, importedOperator.ID)

	// Verify the imported account
	importedAccounts, err := s.accountService.ListAccountsByOperator(s.ctx, importedOperator.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), importedAccounts)

	var importedAccount *ExportedAccountData
	for _, a := range importedAccounts {
		if a.Name == "Import Account" {
			importedAccount = &ExportedAccountData{
				Name:             a.Name,
				JetStreamEnabled: a.JetStreamEnabled,
				JetStreamMaxStreams: a.JetStreamMaxStreams,
			}
			break
		}
	}
	require.NotNil(s.T(), importedAccount)
	assert.True(s.T(), importedAccount.JetStreamEnabled)
	assert.Equal(s.T(), int64(50), importedAccount.JetStreamMaxStreams)
}

// TestImportOperator_DuplicateName tests that importing an operator with an existing name fails
func (s *ExportServiceTestSuite) TestImportOperator_DuplicateName() {
	// Create operator
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Duplicate Import Operator",
	})
	require.NoError(s.T(), err)

	// Export
	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, true)
	require.NoError(s.T(), err)

	// Try to import without deleting the existing operator
	err = s.exportService.ImportOperatorJSON(s.ctx, data, true)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "already exists")
}

// TestImportOperator_InvalidVersion tests importing with unsupported version
func (s *ExportServiceTestSuite) TestImportOperator_InvalidVersion() {
	exported := &ExportedOperator{
		Version: "99.0",
	}

	err := s.exportService.ImportOperator(s.ctx, exported, false)
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "unsupported export version")
}

// TestExportOutputStructure tests the export output structure in detail
func (s *ExportServiceTestSuite) TestExportOutputStructure() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Structure Test Operator",
	})
	require.NoError(s.T(), err)

	// Export to JSON
	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, true)
	require.NoError(s.T(), err)

	// Parse the JSON and verify all expected fields exist
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	require.NoError(s.T(), err)

	// Verify top-level fields
	assert.Contains(s.T(), raw, "version")
	assert.Contains(s.T(), raw, "exported_at")
	assert.Contains(s.T(), raw, "operator")
	assert.Contains(s.T(), raw, "accounts")
	assert.Contains(s.T(), raw, "scoped_keys")
	assert.Contains(s.T(), raw, "users")

	// Verify operator fields
	operatorData, ok := raw["operator"].(map[string]interface{})
	require.True(s.T(), ok)
	assert.Contains(s.T(), operatorData, "id")
	assert.Contains(s.T(), operatorData, "name")
	assert.Contains(s.T(), operatorData, "description")
	assert.Contains(s.T(), operatorData, "public_key")
	assert.Contains(s.T(), operatorData, "encrypted_seed")
	assert.Contains(s.T(), operatorData, "jwt")
	assert.Contains(s.T(), operatorData, "created_at")
	assert.Contains(s.T(), operatorData, "updated_at")
}
