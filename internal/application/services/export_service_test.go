package services

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
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
	db, err := sql.NewDB("sqlite", ":memory:")
	require.NoError(s.T(), err)
	s.db = db

	// Run migrations
	sqlDB, err := db.DB()
	require.NoError(s.T(), err)

	goose.SetBaseFS(migrations.Migrations)
	err = goose.SetDialect("sqlite3")
	require.NoError(s.T(), err)

	err = goose.Up(sqlDB, "sqlite")
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

	factory := persistence.NewSQLRepositoryFactoryFromDB(s.db)

	// Create services
	s.accountService = NewAccountService(factory, s.jwtService, s.encryptor)
	s.operatorService = NewOperatorService(factory, s.accountService, s.jwtService, s.encryptor)
	s.userService = NewUserService(s.userRepo, s.accountRepo, s.scopedSigningKeyRepo, s.operatorRepo, s.jwtService, s.encryptor)
	s.scopedKeyService = NewScopedSigningKeyService(factory, s.jwtService, s.encryptor)
	s.clusterService = NewClusterService(s.clusterRepo, s.operatorRepo, s.accountRepo, s.userRepo, s.scopedSigningKeyRepo, s.encryptor, s.jwtService)
	s.exportService = NewExportService(
		factory,
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
	_ = sql.Close(s.db)
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
	exported, err := s.exportService.ExportOperator(s.ctx, operator.ID, SecretsEncrypted)
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
// TestExportOperatorJSON tests JSON export format
func (s *ExportServiceTestSuite) TestExportOperatorJSON() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "JSON Export Operator",
	})
	require.NoError(s.T(), err)

	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, SecretsEncrypted)
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
	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, SecretsEncrypted)
	require.NoError(s.T(), err)

	// Clean up the database to simulate importing into a fresh instance
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")

	// Import. Faithful restore: same UUIDs as the export, same NKey pubkeys, etc.
	err = s.exportService.ImportOperatorJSON(s.ctx, data, false)
	require.NoError(s.T(), err)

	// Verify the imported operator exists
	importedOperator, err := s.operatorService.GetOperatorByName(s.ctx, uuid.MustParse(entities.DefaultOrganizationID), "Import Test Operator")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "Import Test Operator", importedOperator.Name)
	assert.Equal(s.T(), "Operator for import testing", importedOperator.Description)
	// ID is preserved on faithful restore.
	assert.Equal(s.T(), operator.ID, importedOperator.ID)

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
	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, SecretsEncrypted)
	require.NoError(s.T(), err)

	// Try to import without deleting the existing operator. Now keyed on ID
	// (the export carries the same UUID as the existing row), the ID-match
	// path returns ErrOperatorImportExists when overwrite is not set.
	err = s.exportService.ImportOperatorJSON(s.ctx, data, false)
	require.Error(s.T(), err)
	assert.ErrorIs(s.T(), err, ErrOperatorImportExists)
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
	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, SecretsEncrypted)
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

// TestExportOperatorYAML asserts the YAML encoder produces parseable output
// with the same field names as JSON. yaml.v3 defaults to lowercased Go field
// names, which would diverge from JSON — the yaml struct tags I added are what
// keep both encodings aligned.
func (s *ExportServiceTestSuite) TestExportOperatorYAML() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "YAML Export Operator",
	})
	require.NoError(s.T(), err)

	data, err := s.exportService.ExportOperatorYAML(s.ctx, operator.ID, SecretsEncrypted)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), data)
	// First non-whitespace char must NOT be '{' — that would mean we accidentally
	// produced JSON. The auto-detector relies on this.
	assert.NotContains(s.T(), string(data[:5]), "{")

	var exported ExportedOperator
	err = yaml.Unmarshal(data, &exported)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "1.0", exported.Version)
	assert.Equal(s.T(), "YAML Export Operator", exported.Operator.Name)
	assert.NotEmpty(s.T(), exported.Operator.PublicKey)

	// Field names should match JSON: yaml.Unmarshal should see snake_case keys
	// at every nesting level. Cross-check by parsing as a generic map.
	var raw map[string]any
	require.NoError(s.T(), yaml.Unmarshal(data, &raw))
	assert.Contains(s.T(), raw, "exported_at")
	assert.Contains(s.T(), raw, "scoped_keys", "top-level key must be snake_case (regression: yaml.v3 would default to lowercased Go field name)")
	op, ok := raw["operator"].(map[string]any)
	require.True(s.T(), ok, "operator block missing or wrong type")
	assert.Contains(s.T(), op, "public_key")
	assert.Contains(s.T(), op, "system_account_pub_key")
}

// TestExportYAMLAndImport verifies a full YAML round-trip through the service:
// export → wipe DB → import → operator and its children come back intact.
// This is the user-facing guarantee — "I can back up to a yaml file and
// restore from it later" — so it needs an end-to-end test, not just an
// encoder unit test.
func (s *ExportServiceTestSuite) TestExportYAMLAndImport() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name:        "YAML Roundtrip Operator",
		Description: "Description with quote ' and dash - chars",
	})
	require.NoError(s.T(), err)

	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{
		OperatorID: operator.ID,
		Name:       "$SYS-like-tricky-name", // tricky in yaml; should round-trip
	})
	require.NoError(s.T(), err)
	_ = account

	data, err := s.exportService.ExportOperatorYAML(s.ctx, operator.ID, SecretsEncrypted)
	require.NoError(s.T(), err)

	// Wipe everything.
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")

	// Re-import the YAML bytes via the auto-detecting path.
	require.NoError(s.T(), s.exportService.ImportOperatorBytes(s.ctx, data, false))

	imported, err := s.operatorService.GetOperatorByName(s.ctx, uuid.MustParse(entities.DefaultOrganizationID), "YAML Roundtrip Operator")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "Description with quote ' and dash - chars", imported.Description)

	importedAccounts, err := s.accountService.ListAccountsByOperator(s.ctx, imported.ID, repositories.ListOptions{})
	require.NoError(s.T(), err)
	// The original create added "$SYS-like-tricky-name" + "$SYS" (auto-created).
	// Two accounts total. The name with leading $ is the regression test for
	// yaml's tendency to mishandle '$'-prefixed bare scalars.
	names := make(map[string]bool)
	for _, a := range importedAccounts {
		names[a.Name] = true
	}
	assert.True(s.T(), names["$SYS-like-tricky-name"], "tricky $-prefixed name must round-trip through yaml")
	assert.True(s.T(), names["$SYS"], "auto-created $SYS account must survive import")
}

// TestImportOperatorBytes_AutoDetect proves the format sniffer dispatches
// correctly. Identical content, different encodings; both must produce the
// same imported state.
func (s *ExportServiceTestSuite) TestImportOperatorBytes_AutoDetect() {
	// Build an export once.
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Format Detect Operator",
	})
	require.NoError(s.T(), err)

	jsonData, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, SecretsEncrypted)
	require.NoError(s.T(), err)
	yamlData, err := s.exportService.ExportOperatorYAML(s.ctx, operator.ID, SecretsEncrypted)
	require.NoError(s.T(), err)

	// Wipe and import the JSON bytes.
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	require.NoError(s.T(), s.exportService.ImportOperatorBytes(s.ctx, jsonData, false))
	_, err = s.operatorService.GetOperatorByName(s.ctx, uuid.MustParse(entities.DefaultOrganizationID), "Format Detect Operator")
	require.NoError(s.T(), err, "JSON auto-detect import failed")

	// Wipe and import the YAML bytes.
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	require.NoError(s.T(), s.exportService.ImportOperatorBytes(s.ctx, yamlData, false))
	_, err = s.operatorService.GetOperatorByName(s.ctx, uuid.MustParse(entities.DefaultOrganizationID), "Format Detect Operator")
	require.NoError(s.T(), err, "YAML auto-detect import failed")
}

// TestDetectExportFormat covers the byte sniffer in isolation. Cheap and
// explicit — anyone touching the dispatcher should be able to read this.
func TestDetectExportFormat(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		want   ExportFormat
	}{
		{"empty", "", ""},
		{"whitespace only", "   \n\t  ", ""},
		{"json object", `{"version":"1.0"}`, FormatJSON},
		{"json with leading whitespace", "  \n  {\"v\":1}", FormatJSON},
		{"json array", "[]", FormatJSON},
		{"yaml plain", "version: 1.0\noperator: x", FormatYAML},
		{"yaml with leading whitespace", "  \n  version: 1.0", FormatYAML},
		{"yaml dash list", "- name: foo", FormatYAML},
		{"yaml directive", "%YAML 1.2\n---\nx: y", FormatYAML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectExportFormat([]byte(tc.input))
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestExportOperatorBytes_DefaultsToJSON locks down the back-compat promise
// that empty format == JSON. If we ever want to flip the default, this test
// has to be deliberately changed.
func (s *ExportServiceTestSuite) TestExportOperatorBytes_DefaultsToJSON() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{
		Name: "Default Format Operator",
	})
	require.NoError(s.T(), err)

	data, err := s.exportService.ExportOperatorBytes(s.ctx, operator.ID, SecretsEncrypted, "")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), FormatJSON, detectExportFormat(data))
}

// helpers for the plaintext-secret round-trip tests
// -----------------------------------------------------------------------------

// freshExportService rebuilds an ExportService bound to the same DB but with
// a different encryptor. Used to simulate a destination NIS instance whose
// encryption key differs from the source's.
func (s *ExportServiceTestSuite) freshExportService(enc encryption.Encryptor) *ExportService {
	factory := persistence.NewSQLRepositoryFactoryFromDB(s.db)
	jwtSvc := NewJWTService(enc)
	accountSvc := NewAccountService(factory, jwtSvc, enc)
	operatorSvc := NewOperatorService(factory, accountSvc, jwtSvc, enc)
	userSvc := NewUserService(s.userRepo, s.accountRepo, s.scopedSigningKeyRepo, s.operatorRepo, jwtSvc, enc)
	scopedSvc := NewScopedSigningKeyService(factory, jwtSvc, enc)
	clusterSvc := NewClusterService(s.clusterRepo, s.operatorRepo, s.accountRepo, s.userRepo, s.scopedSigningKeyRepo, enc, jwtSvc)
	return NewExportService(
		factory,
		s.operatorRepo, s.accountRepo, s.userRepo, s.scopedSigningKeyRepo, s.clusterRepo,
		operatorSvc, accountSvc, userSvc, scopedSvc, clusterSvc,
		enc,
	)
}

// secondEncryptor is a distinct encryption key — used as the "destination
// server's" encryptor to prove that imports re-encrypt seeds with the
// destination's key, not the source's.
func (s *ExportServiceTestSuite) secondEncryptor() encryption.Encryptor {
	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"dest-key": "AAAA00000000000000000000000000000000000000A=",
	}, "dest-key")
	require.NoError(s.T(), err)
	return enc
}

// -----------------------------------------------------------------------------
// Plaintext export
// -----------------------------------------------------------------------------

// TestExportOperator_PlaintextSecrets_EmitsPlainAndElidesEncrypted asserts the
// shape of a plaintext export: per-row `Seed` populated with a printable NKey
// seed, per-row `EncryptedSeed` empty. The two seed fields are mutually
// exclusive at the export layer.
func (s *ExportServiceTestSuite) TestExportOperator_PlaintextSecrets_EmitsPlainAndElidesEncrypted() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{Name: "plaintext-op"})
	require.NoError(s.T(), err)

	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{OperatorID: operator.ID, Name: "plaintext-acc"})
	require.NoError(s.T(), err)

	_, err = s.userService.CreateUser(s.ctx, CreateUserRequest{AccountID: account.ID, Name: "plaintext-user"})
	require.NoError(s.T(), err)

	exported, err := s.exportService.ExportOperator(s.ctx, operator.ID, SecretsPlaintext)
	require.NoError(s.T(), err)

	// Operator
	assert.Empty(s.T(), exported.Operator.EncryptedSeed, "encrypted_seed must be elided in plaintext export")
	require.NotEmpty(s.T(), exported.Operator.Seed, "plaintext seed must be populated")
	assert.Equal(s.T(), byte('S'), exported.Operator.Seed[0], "NKey seed always starts with 'S'")

	// Accounts (including the auto-created default scoped key under each)
	require.NotEmpty(s.T(), exported.Accounts)
	for _, acc := range exported.Accounts {
		assert.Empty(s.T(), acc.EncryptedSeed, "account encrypted_seed must be elided")
		assert.NotEmpty(s.T(), acc.Seed, "account plaintext seed must be populated")
		assert.Equal(s.T(), byte('S'), acc.Seed[0])
	}

	require.NotEmpty(s.T(), exported.ScopedKeys)
	for _, sk := range exported.ScopedKeys {
		assert.Empty(s.T(), sk.EncryptedSeed)
		assert.NotEmpty(s.T(), sk.Seed)
	}

	require.NotEmpty(s.T(), exported.Users)
	for _, u := range exported.Users {
		assert.Empty(s.T(), u.EncryptedSeed)
		assert.NotEmpty(s.T(), u.Seed)
	}
}

// TestExportOperator_PlaintextSecrets_JSONBytesContainSeedFieldOnly is a
// belt-and-braces check on the wire encoding: a plaintext export's JSON
// must contain `"seed":` and must NOT contain `"encrypted_seed":` populated
// with a non-empty value. (The field uses omitempty so an empty
// encrypted_seed is dropped entirely.)
func (s *ExportServiceTestSuite) TestExportOperator_PlaintextSecrets_JSONBytesContainSeedFieldOnly() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{Name: "wire-shape"})
	require.NoError(s.T(), err)

	data, err := s.exportService.ExportOperatorJSON(s.ctx, operator.ID, SecretsPlaintext)
	require.NoError(s.T(), err)

	body := string(data)
	assert.Contains(s.T(), body, `"seed":`)
	assert.NotContains(s.T(), body, `"encrypted_seed": "encrypted:`)
}

// -----------------------------------------------------------------------------
// Plaintext import (re-encryption)
// -----------------------------------------------------------------------------

// TestImportOperator_PlaintextSecrets_ReEncryptsAgainstDestinationKey is the
// core disaster-recovery test. Export with the source encryptor in plaintext
// mode, wipe the DB, then import via an ExportService bound to a DIFFERENT
// encryptor. The persisted `encrypted_seed` must be decryptable by the new
// encryptor and must round-trip back to the original plaintext.
func (s *ExportServiceTestSuite) TestImportOperator_PlaintextSecrets_ReEncryptsAgainstDestinationKey() {
	// (1) Build a small tree under the source's encryptor.
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{Name: "dr-op"})
	require.NoError(s.T(), err)
	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{OperatorID: operator.ID, Name: "dr-acc"})
	require.NoError(s.T(), err)
	user, err := s.userService.CreateUser(s.ctx, CreateUserRequest{AccountID: account.ID, Name: "dr-user"})
	require.NoError(s.T(), err)

	// (2) Export plaintext. Capture the seeds so we can assert post-import equality.
	exported, err := s.exportService.ExportOperator(s.ctx, operator.ID, SecretsPlaintext)
	require.NoError(s.T(), err)

	originalOpSeed := exported.Operator.Seed
	originalAccSeed := ""
	for _, a := range exported.Accounts {
		if a.ID == account.ID {
			originalAccSeed = a.Seed
		}
	}
	originalUserSeed := ""
	for _, u := range exported.Users {
		if u.ID == user.ID {
			originalUserSeed = u.Seed
		}
	}
	require.NotEmpty(s.T(), originalOpSeed)
	require.NotEmpty(s.T(), originalAccSeed)
	require.NotEmpty(s.T(), originalUserSeed)

	// (3) Wipe DB and rebuild ExportService against a fresh encryption key.
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")

	destEnc := s.secondEncryptor()
	destSvc := s.freshExportService(destEnc)

	// (4) Import. Plaintext seeds get re-encrypted with destEnc.
	require.NoError(s.T(), destSvc.ImportOperator(s.ctx, exported, false))

	// (5) Verify by decrypting the stored storage refs with destEnc — the
	// plaintext we get back must equal the seeds we originally exported.
	loadedOp, err := s.operatorRepo.GetByID(s.ctx, operator.ID)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), loadedOp.EncryptedSeed)
	decryptedOp, err := destEnc.Decrypt(s.ctx, loadedOp.EncryptedSeed)
	require.NoError(s.T(), err, "destination encryptor must be able to decrypt the re-encrypted operator seed")
	assert.Equal(s.T(), originalOpSeed, string(decryptedOp))

	loadedAcc, err := s.accountRepo.GetByID(s.ctx, account.ID)
	require.NoError(s.T(), err)
	decryptedAcc, err := destEnc.Decrypt(s.ctx, loadedAcc.EncryptedSeed)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), originalAccSeed, string(decryptedAcc))

	loadedUser, err := s.userRepo.GetByID(s.ctx, user.ID)
	require.NoError(s.T(), err)
	decryptedUser, err := destEnc.Decrypt(s.ctx, loadedUser.EncryptedSeed)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), originalUserSeed, string(decryptedUser))

	// (6) And — critically — the SOURCE encryptor must NOT be able to decrypt
	// the re-encrypted seed (different key). This proves the seed was
	// genuinely re-encrypted, not stored verbatim.
	_, err = s.encryptor.Decrypt(s.ctx, loadedOp.EncryptedSeed)
	assert.Error(s.T(), err, "source encryptor must not decrypt seed re-encrypted with destination key")
}

// TestBackupPipeline_AgeEnvelope_CrossKeyRestore pins the change that made
// scheduled backups portable across NIS instances (2026-05-25 — P12 DR
// follow-up). It exercises EXACTLY the shape RunBackup uses:
//
//  1. ExportOperatorBytes(SecretsPlaintext, FormatYAML) under encryptor-A
//  2. encryptForBackup(plaintext, [age recipient])
//  3. age.Decrypt with the matching identity
//  4. ImportOperatorBytes(plaintext) on a fresh ExportService bound to
//     encryptor-B
//
// Then verifies the destination DB row's encrypted_seed decrypts under
// encryptor-B and round-trips back to the original NKey seed. Without
// this end-to-end pin, a future "let's go back to SecretsEncrypted for
// defense in depth" change would silently break the cross-key DR property
// — the existing TestImportOperator_PlaintextSecrets_… test would still
// pass because it doesn't touch the backup-pipeline arg shape.
func (s *ExportServiceTestSuite) TestBackupPipeline_AgeEnvelope_CrossKeyRestore() {
	operator, err := s.operatorService.CreateOperator(s.ctx, CreateOperatorRequest{Name: "dr-pipeline-op"})
	require.NoError(s.T(), err)
	account, err := s.accountService.CreateAccount(s.ctx, CreateAccountRequest{OperatorID: operator.ID, Name: "dr-pipeline-acc"})
	require.NoError(s.T(), err)
	user, err := s.userService.CreateUser(s.ctx, CreateUserRequest{AccountID: account.ID, Name: "dr-pipeline-user"})
	require.NoError(s.T(), err)

	// Decrypt the source-side encrypted seeds once so we can compare against
	// the destination-side re-encrypted seeds below.
	srcOp, err := s.operatorRepo.GetByID(s.ctx, operator.ID)
	require.NoError(s.T(), err)
	originalOpSeed, err := s.encryptor.Decrypt(s.ctx, srcOp.EncryptedSeed)
	require.NoError(s.T(), err)
	srcAcc, err := s.accountRepo.GetByID(s.ctx, account.ID)
	require.NoError(s.T(), err)
	originalAccSeed, err := s.encryptor.Decrypt(s.ctx, srcAcc.EncryptedSeed)
	require.NoError(s.T(), err)
	srcUser, err := s.userRepo.GetByID(s.ctx, user.ID)
	require.NoError(s.T(), err)
	originalUserSeed, err := s.encryptor.Decrypt(s.ctx, srcUser.EncryptedSeed)
	require.NoError(s.T(), err)

	// (1) Same call RunBackup makes.
	plaintextPayload, err := s.exportService.ExportOperatorBytes(s.ctx, operator.ID, SecretsPlaintext, FormatYAML)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(plaintextPayload), "seed:", "plaintext-mode export must carry `seed:` fields")
	require.NotContains(s.T(), string(plaintextPayload), "encrypted_seed:", "plaintext-mode export must NOT carry `encrypted_seed:` fields")

	// (2) Same call RunBackup makes (via encryptForBackup from backup_age.go).
	id, err := age.GenerateX25519Identity()
	require.NoError(s.T(), err)
	ciphertext, err := encryptForBackup(plaintextPayload, []age.Recipient{id.Recipient()})
	require.NoError(s.T(), err)
	require.True(s.T(), bytes.HasPrefix(ciphertext, []byte("age-encryption.org/v1\n")),
		"ciphertext must start with age header")

	// (3) Wipe the DB to simulate "total loss" + key rotation.
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")

	// (4) age-decrypt with the identity. This is what
	// `nisctl backup decrypt -i ... -f ...` does on the restore side.
	r, err := age.Decrypt(bytes.NewReader(ciphertext), id)
	require.NoError(s.T(), err)
	decryptedYAML, err := io.ReadAll(r)
	require.NoError(s.T(), err)
	require.Equal(s.T(), string(plaintextPayload), string(decryptedYAML))

	// (5) ImportOperatorBytes under a different encryptor.
	destEnc := s.secondEncryptor()
	destSvc := s.freshExportService(destEnc)
	require.NoError(s.T(), destSvc.ImportOperatorBytes(s.ctx, decryptedYAML, false))

	// (6) Verify the restored rows: decryptable by destEnc, equal to originals.
	loadedOp, err := s.operatorRepo.GetByID(s.ctx, operator.ID)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), loadedOp.EncryptedSeed)
	gotOp, err := destEnc.Decrypt(s.ctx, loadedOp.EncryptedSeed)
	require.NoError(s.T(), err, "destination encryptor must decrypt the re-encrypted operator seed")
	assert.Equal(s.T(), originalOpSeed, gotOp)

	loadedAcc, err := s.accountRepo.GetByID(s.ctx, account.ID)
	require.NoError(s.T(), err)
	gotAcc, err := destEnc.Decrypt(s.ctx, loadedAcc.EncryptedSeed)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), originalAccSeed, gotAcc)

	loadedUser, err := s.userRepo.GetByID(s.ctx, user.ID)
	require.NoError(s.T(), err)
	gotUser, err := destEnc.Decrypt(s.ctx, loadedUser.EncryptedSeed)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), originalUserSeed, gotUser)

	// (7) And the source encryptor must NOT decrypt the new ciphertext —
	// proves the seed was genuinely re-encrypted at import time, not stored
	// verbatim.
	_, err = s.encryptor.Decrypt(s.ctx, loadedOp.EncryptedSeed)
	assert.Error(s.T(), err, "source encryptor must not decrypt seed re-encrypted with destination key")
}

// TestImportOperator_BothSeedAndEncryptedSet_Rejected guards against an
// ambiguous export. An importer presented with both encrypted_seed AND seed
// for the same entity cannot tell which to trust — refuse rather than guess.
func (s *ExportServiceTestSuite) TestImportOperator_BothSeedAndEncryptedSet_Rejected() {
	exported := &ExportedOperator{
		Version: "1.0",
		Operator: &ExportedOperatorData{
			ID:            uuidNamed("00000000-0000-0000-0000-000000000001"),
			Name:          "ambiguous-op",
			PublicKey:     "OAAAA",
			EncryptedSeed: "encrypted:something",
			Seed:          "SOMESEED",
		},
	}
	err := s.exportService.ImportOperator(s.ctx, exported, false)
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "both encrypted_seed and seed")
}

// uuidNamed parses a UUID literal in tests where a specific value matters
// for assertions but not collision avoidance.
func uuidNamed(s string) uuid.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
