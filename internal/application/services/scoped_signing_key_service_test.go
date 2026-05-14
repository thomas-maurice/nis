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
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
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
	db, err := sql.NewDB("sqlite", ":memory:")
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

	factory := persistence.NewSQLRepositoryFactoryFromDB(s.db)

	s.accountService = NewAccountService(factory, s.jwtService, s.encryptor)
	s.operatorService = NewOperatorService(factory, s.accountService, s.jwtService, s.encryptor)
	s.scopedKeyService = NewScopedSigningKeyService(factory, s.jwtService, s.encryptor)
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

// TestCreateScopedSigningKey_PermissionsPersistViaRepo verifies that allow/deny
// lists supplied at create time round-trip through the repository — not just
// the in-memory return value. Guards against a serializer regression where the
// JSON column silently swallows non-empty slices.
func (s *ScopedSigningKeyServiceTestSuite) TestCreateScopedSigningKey_PermissionsPersistViaRepo() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "fbb",
		PubAllow:  []string{"foo.>", "bar.>", "baz.>"},
		PubDeny:   []string{"_INBOX.>"},
		SubAllow:  []string{"events.>"},
		SubDeny:   []string{"admin.>"},
	})
	require.NoError(s.T(), err)

	// Re-read through the repo (not the service) so we hit the DB serializer path.
	loaded, err := s.scopedSigningKeyRepo.GetByID(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"foo.>", "bar.>", "baz.>"}, loaded.PubAllow)
	assert.Equal(s.T(), []string{"_INBOX.>"}, loaded.PubDeny)
	assert.Equal(s.T(), []string{"events.>"}, loaded.SubAllow)
	assert.Equal(s.T(), []string{"admin.>"}, loaded.SubDeny)
}

// TestUpdateScopedSigningKey_HandlerStyle_PersistsAllowDeny is a regression
// test for the UI bug where edits silently dropped permission changes.
//
// The UpdatePermissions handler (interfaces/grpc/handlers/scoped_key_handler.go)
// always passes non-nil slices through to the service — converting nil to
// `[]string{}` so the service's "nil means leave alone" semantics don't cause
// the update to no-op. This test exercises that exact call shape.
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey_HandlerStyle_PersistsAllowDeny() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "edit-me",
	})
	require.NoError(s.T(), err)
	require.Empty(s.T(), created.PubAllow, "fresh key should have no pub_allow rules")

	// Mirror exactly what the UpdatePermissions handler does: non-nil slices,
	// even when the caller "didn't touch" a given list.
	updated, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, created.ID, UpdateScopedSigningKeyRequest{
		PubAllow: []string{"foo.>", "bar.>", "baz.>"},
		PubDeny:  []string{},
		SubAllow: []string{},
		SubDeny:  []string{},
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"foo.>", "bar.>", "baz.>"}, updated.PubAllow)

	// And the persisted state — the UI bug was that the *server-side* update
	// didn't run at all, so check the repo, not just the returned entity.
	loaded, err := s.scopedSigningKeyRepo.GetByID(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"foo.>", "bar.>", "baz.>"}, loaded.PubAllow)
}

// TestUpdateScopedSigningKey_OverwritesPriorAllowDeny verifies that a second
// UpdatePermissions-style call REPLACES the prior list (does not append/merge).
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey_OverwritesPriorAllowDeny() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "overwrite-me",
		PubAllow:  []string{"foo.>", "bar.>"},
	})
	require.NoError(s.T(), err)

	updated, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, created.ID, UpdateScopedSigningKeyRequest{
		PubAllow: []string{"qux.>"},
		PubDeny:  []string{},
		SubAllow: []string{},
		SubDeny:  []string{},
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"qux.>"}, updated.PubAllow, "second update must replace, not merge")

	loaded, err := s.scopedSigningKeyRepo.GetByID(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"qux.>"}, loaded.PubAllow)
}

// TestUpdateScopedSigningKey_EmptySlicesClearAllowDeny verifies the "clear all
// rules" semantics of the UpdatePermissions handler: an explicit `[]string{}`
// (not nil) blanks the column.
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey_EmptySlicesClearAllowDeny() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "clear-me",
		PubAllow:  []string{"foo.>"},
		SubAllow:  []string{"events.>"},
	})
	require.NoError(s.T(), err)

	updated, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, created.ID, UpdateScopedSigningKeyRequest{
		PubAllow: []string{},
		PubDeny:  []string{},
		SubAllow: []string{},
		SubDeny:  []string{},
	})
	require.NoError(s.T(), err)
	assert.Empty(s.T(), updated.PubAllow)
	assert.Empty(s.T(), updated.SubAllow)

	loaded, err := s.scopedSigningKeyRepo.GetByID(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), loaded.PubAllow)
	assert.Empty(s.T(), loaded.SubAllow)
}

// TestUpdateScopedSigningKey_NameOnly_LeavesPermissionsIntact codifies the
// "nil slice means leave alone" contract that the dedicated UpdateScopedSigningKey
// RPC depends on (it only carries name + description). If a future refactor folds
// permissions into the same call, the same contract must be preserved.
func (s *ScopedSigningKeyServiceTestSuite) TestUpdateScopedSigningKey_NameOnly_LeavesPermissionsIntact() {
	accountID := s.createTestAccountForScopedKey()

	created, err := s.scopedKeyService.CreateScopedSigningKey(s.ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "rename-only",
		PubAllow:  []string{"foo.>", "bar.>"},
		SubAllow:  []string{"events.>"},
	})
	require.NoError(s.T(), err)

	newName := "rename-only-v2"
	updated, err := s.scopedKeyService.UpdateScopedSigningKey(s.ctx, created.ID, UpdateScopedSigningKeyRequest{
		Name: &newName,
		// All permission slices left nil — must be a no-op for permissions.
	})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), newName, updated.Name)
	assert.Equal(s.T(), []string{"foo.>", "bar.>"}, updated.PubAllow)
	assert.Equal(s.T(), []string{"events.>"}, updated.SubAllow)

	loaded, err := s.scopedSigningKeyRepo.GetByID(s.ctx, created.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), []string{"foo.>", "bar.>"}, loaded.PubAllow)
	assert.Equal(s.T(), []string{"events.>"}, loaded.SubAllow)
}

// TestDeleteScopedSigningKey_DefaultKey documents what happens when the
// auto-created "default" scoped key for an account is deleted.
//
// Outcome: the delete succeeds. There is NO special guard on the name "default" —
// it is structurally indistinguishable from any other scoped key. The account
// JWT is re-signed inside the same tx so NATS sees the smaller signer set on
// the next push; the account itself is unaffected. A subsequent run of
// `nis repair` will recreate a "default" key only if the account has zero
// scoped keys left (see cmd/nis/commands/repair.go).
func (s *ScopedSigningKeyServiceTestSuite) TestDeleteScopedSigningKey_DefaultKey() {
	accountID := s.createTestAccountForScopedKey()

	defaultKey, err := s.scopedKeyService.GetScopedSigningKeyByName(s.ctx, accountID, "default")
	require.NoError(s.T(), err, "account creation should have auto-provisioned a default scoped key")

	err = s.scopedKeyService.DeleteScopedSigningKey(s.ctx, defaultKey.ID)
	require.NoError(s.T(), err, "deleting the default key must be permitted")

	_, err = s.scopedKeyService.GetScopedSigningKey(s.ctx, defaultKey.ID)
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)

	// Account itself must still be there with a regenerated JWT.
	account, err := s.accountRepo.GetByID(s.ctx, accountID)
	require.NoError(s.T(), err, "account must survive deletion of its default key")
	assert.NotEmpty(s.T(), account.JWT, "account JWT must have been re-signed inside the delete tx")
}

// TestDeleteScopedSigningKey_DefaultWithUserReference_FKSetsNull verifies the
// ON DELETE SET NULL FK behavior on users.scoped_signing_key_id. When a user
// was created with an explicit scoped signing key and that key is later
// deleted, the user is preserved and its ScopedSigningKeyID is nulled out —
// the user does NOT cascade-delete.
//
// Operational consequence: the user's existing .creds file (containing a JWT
// signed by the now-deleted scoped key) will be rejected by NATS on next
// reconnect, because the deleted key is no longer in the account JWT's
// signers list. The user row sticks around so the operator can re-mint a
// fresh JWT (e.g. via UpdateUser) signed by the account master or another
// scoped key.
func (s *ScopedSigningKeyServiceTestSuite) TestDeleteScopedSigningKey_DefaultWithUserReference_FKSetsNull() {
	accountID := s.createTestAccountForScopedKey()

	defaultKey, err := s.scopedKeyService.GetScopedSigningKeyByName(s.ctx, accountID, "default")
	require.NoError(s.T(), err)

	// Persist a user that points at the default scoped key. We use the repo
	// directly to side-step JWT generation — the FK behaviour we want to
	// exercise lives in the DB schema, not the application layer.
	user := &entities.User{
		ID:                 uuid.New(),
		AccountID:          accountID,
		Name:               "fk-test-user",
		EncryptedSeed:      "dummy-seed",
		PublicKey:          "UTESTPUBKEYTESTPUBKEYTESTPUBKEYTESTPUBKEYTESTPUBKEYTEST",
		JWT:                "dummy-jwt",
		ScopedSigningKeyID: &defaultKey.ID,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	require.NoError(s.T(), s.userRepo.Create(s.ctx, user))

	// Delete the default key out from under the user.
	require.NoError(s.T(), s.scopedKeyService.DeleteScopedSigningKey(s.ctx, defaultKey.ID))

	// User must still be there...
	loaded, err := s.userRepo.GetByID(s.ctx, user.ID)
	require.NoError(s.T(), err, "user must NOT cascade-delete when its scoped signing key is removed")
	assert.Equal(s.T(), user.ID, loaded.ID)

	// ...but the FK pointer must have been nulled out by ON DELETE SET NULL.
	assert.Nil(s.T(), loaded.ScopedSigningKeyID, "users.scoped_signing_key_id must be NULL after the referenced key is deleted")
}
