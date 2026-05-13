package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// faultEncryptor wraps a real Encryptor and forces an error on the Nth (or
// later) Encrypt call. Tests use it to simulate a mid-flow failure deep
// inside a transactional service method and assert that the surrounding
// tx rolls back every prior write.
type faultEncryptor struct {
	inner      encryption.Encryptor
	failAfter  int64 // 1-based; pass 0 to disable injection
	count      atomic.Int64
	failureErr error
}

func newFaultEncryptor(inner encryption.Encryptor, failAfter int) *faultEncryptor {
	return &faultEncryptor{
		inner:      inner,
		failAfter:  int64(failAfter),
		failureErr: errors.New("injected encrypt fault"),
	}
}

func (f *faultEncryptor) Encrypt(ctx context.Context, plaintext []byte) (string, error) {
	n := f.count.Add(1)
	if f.failAfter > 0 && n >= f.failAfter {
		return "", f.failureErr
	}
	return f.inner.Encrypt(ctx, plaintext)
}

func (f *faultEncryptor) Decrypt(ctx context.Context, storageRef string) ([]byte, error) {
	return f.inner.Decrypt(ctx, storageRef)
}

func (f *faultEncryptor) CurrentKeyID() string {
	return f.inner.CurrentKeyID()
}

func (f *faultEncryptor) RotateKey(ctx context.Context, oldRef string) (string, error) {
	return f.inner.RotateKey(ctx, oldRef)
}

// setupFaultRollbackHarness wires a fresh in-memory SQLite + factory + the
// three multi-write services under a fault-injecting encryptor. We don't
// reuse the existing suite's SetupSuite because we need a clean DB AND the
// fault encryptor swapped in.
func setupFaultRollbackHarness(t *testing.T, failAfter int) (*OperatorService, *AccountService, *UserService, *ScopedSigningKeyService, *ClusterService, *ExportService, persistence.RepositoryFactory, *faultEncryptor) {
	t.Helper()

	db, err := sql.NewDB("sqlite", ":memory:")
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "."))

	realEnc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)
	faulty := newFaultEncryptor(realEnc, failAfter)

	factory := persistence.NewSQLRepositoryFactoryFromDB(db)
	jwtSvc := NewJWTService(faulty)

	accountSvc := NewAccountService(factory, jwtSvc, faulty)
	operatorSvc := NewOperatorService(factory, accountSvc, jwtSvc, faulty)
	userSvc := NewUserService(
		factory.UserRepository(),
		factory.AccountRepository(),
		factory.ScopedSigningKeyRepository(),
		jwtSvc, faulty,
	)
	scopedKeySvc := NewScopedSigningKeyService(factory, jwtSvc, faulty)
	clusterSvc := NewClusterService(
		factory.ClusterRepository(),
		factory.OperatorRepository(),
		factory.AccountRepository(),
		factory.UserRepository(),
		factory.ScopedSigningKeyRepository(),
		faulty, jwtSvc,
	)
	exportSvc := NewExportService(
		factory,
		factory.OperatorRepository(), factory.AccountRepository(), factory.UserRepository(),
		factory.ScopedSigningKeyRepository(), factory.ClusterRepository(),
		operatorSvc, accountSvc, userSvc, scopedKeySvc, clusterSvc,
		faulty,
	)

	return operatorSvc, accountSvc, userSvc, scopedKeySvc, clusterSvc, exportSvc, factory, faulty
}

// TestCreateOperator_PartialFailureRollsBack is the regression test for A1:
// CreateOperator does many encrypt + write operations. Forcing an encrypt
// failure mid-flow used to leave a half-created operator (some accounts but
// no system user, or a system user attached to no operator). With WithTx
// the entire tree is rolled back atomically — this test proves it.
//
// failAfter = 3 lets the first two encrypts succeed (operator seed, default
// scoped key seed for $SYS) then fails the third (system user seed). That
// fault happens AFTER several rows have been written inside the tx, which
// is exactly the regression we want to catch.
func TestCreateOperator_PartialFailureRollsBack(t *testing.T) {
	operatorSvc, _, _, _, _, _, factory, _ := setupFaultRollbackHarness(t, 3)
	ctx := context.Background()

	_, err := operatorSvc.CreateOperator(ctx, CreateOperatorRequest{
		Name: "partial-failure-operator",
	})
	require.Error(t, err, "CreateOperator must propagate the injected fault")
	assert.Contains(t, err.Error(), "injected encrypt fault")

	// Nothing should have made it to disk.
	ops, err := factory.OperatorRepository().List(ctx, repositories.ListOptions{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, ops, "tx rollback must leave the operators table empty")

	accs, err := factory.AccountRepository().List(ctx, repositories.ListOptions{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, accs, "tx rollback must also revert the $SYS account created via the nested service call")

	users, err := factory.UserRepository().List(ctx, repositories.ListOptions{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, users, "no system user should exist after rollback")

	keys, err := factory.ScopedSigningKeyRepository().List(ctx, repositories.ListOptions{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, keys, "no scoped signing key should exist after rollback")
}

// TestCreateScopedSigningKey_PartialFailureRollsBack covers the
// ScopedSigningKeyService tx scope: a fault between the scoped key Create
// and the regenerateAccountJWT must roll both back, otherwise the account
// JWT would reference a key that doesn't exist (or vice versa, depending
// on which side commits first).
func TestCreateScopedSigningKey_PartialFailureRollsBack(t *testing.T) {
	operatorSvc, _, _, scopedKeySvc, _, _, factory, faulty := setupFaultRollbackHarness(t, 0)
	ctx := context.Background()

	op, err := operatorSvc.CreateOperator(ctx, CreateOperatorRequest{
		Name: "host-operator",
	})
	require.NoError(t, err)

	// Find an account to attach the new key to.
	accs, err := factory.AccountRepository().ListByOperator(ctx, op.ID, repositories.ListOptions{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, accs)
	accountID := accs[0].ID

	// Count keys before the failed create.
	keysBefore, err := factory.ScopedSigningKeyRepository().ListByAccount(ctx, accountID, repositories.ListOptions{Limit: 100})
	require.NoError(t, err)

	// Snapshot the account's JWT before the failed mutation. The tx should
	// roll back the regenerateAccountJWT update too, so this is what we
	// expect to see afterwards.
	accBefore, err := factory.AccountRepository().GetByID(ctx, accountID)
	require.NoError(t, err)
	jwtBefore := accBefore.JWT

	// Now arm the fault. We're past CreateOperator, so the counter has been
	// reset to where it can target the very next Encrypt — which happens
	// inside CreateScopedSigningKey when encrypting the new key's seed.
	faulty.count.Store(0)
	faulty.failAfter = 1

	_, err = scopedKeySvc.CreateScopedSigningKey(ctx, CreateScopedSigningKeyRequest{
		AccountID: accountID,
		Name:      "would-be-key",
	})
	require.Error(t, err, "expected injected fault to propagate")

	// Disarm so subsequent assertions can use the real encryptor freely.
	faulty.failAfter = 0

	// Key count must be unchanged AND the account JWT must NOT have been
	// regenerated.
	keysAfter, err := factory.ScopedSigningKeyRepository().ListByAccount(ctx, accountID, repositories.ListOptions{Limit: 100})
	require.NoError(t, err)
	assert.Len(t, keysAfter, len(keysBefore), "scoped key count must not have changed after rolled-back create")

	accAfter, err := factory.AccountRepository().GetByID(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, jwtBefore, accAfter.JWT, "account JWT must not have been regenerated when the tx rolled back")
}

// TestImportFromNSC_PartialFailureRollsBack proves ImportFromNSC tx-wraps
// dozens of writes atomically. We hand-build a minimal NSC archive (one
// operator + one non-SYS account + one user) and force an encrypt failure
// partway through the import. The test then asserts the DB is empty.
//
// Without WithTx around the import, this scenario used to leak a created
// operator and possibly an account/user, then the operator-name uniqueness
// would block any retry until the orphans were hand-cleaned.
func TestImportFromNSC_PartialFailureRollsBack(t *testing.T) {
	_, _, _, _, _, exportSvc, factory, faulty := setupFaultRollbackHarness(t, 0)
	ctx := context.Background()

	archive := buildMinimalNSCArchive(t)

	// Allow several encrypts (operator seed encrypts cleanly) then fail —
	// targeting one of the later account/user seed encrypts. failAfter=2
	// is sufficient to fail AFTER the operator row was already persisted
	// inside the tx.
	faulty.failAfter = 2

	_, err := exportSvc.ImportFromNSC(ctx, archive, "test-imported-operator")
	require.Error(t, err, "ImportFromNSC must propagate the injected fault")

	// Disarm the fault so assertions don't trip it.
	faulty.failAfter = 0

	// Nothing should be in the DB. Specifically, the operator that would
	// have been written first must NOT survive.
	ops, err := factory.OperatorRepository().List(ctx, repositories.ListOptions{Limit: 100})
	require.NoError(t, err)
	assert.Empty(t, ops, "operator row must be rolled back when a downstream encrypt fails")

	accs, err := factory.AccountRepository().List(ctx, repositories.ListOptions{Limit: 100})
	require.NoError(t, err)
	assert.Empty(t, accs, "any account written before the fault must be rolled back")

	users, err := factory.UserRepository().List(ctx, repositories.ListOptions{Limit: 100})
	require.NoError(t, err)
	assert.Empty(t, users, "any user written before the fault must be rolled back")
}

// buildMinimalNSCArchive constructs a gzipped tarball of the smallest NSC
// store layout that ImportFromNSC accepts: one operator JWT, one account JWT
// with one user, plus the nkey seed files at the expected paths. Returns the
// archive as bytes ready for ImportFromNSC.
func buildMinimalNSCArchive(t *testing.T) []byte {
	t.Helper()

	// Generate operator key + JWT.
	opKP, err := nkeys.CreateOperator()
	require.NoError(t, err)
	opSeed, err := opKP.Seed()
	require.NoError(t, err)
	opPub, err := opKP.PublicKey()
	require.NoError(t, err)

	opClaims := jwt.NewOperatorClaims(opPub)
	opClaims.Name = "minimal-operator"
	opClaims.IssuedAt = time.Now().Unix()
	opJWT, err := opClaims.Encode(opKP)
	require.NoError(t, err)

	// Generate account key + JWT.
	accKP, err := nkeys.CreateAccount()
	require.NoError(t, err)
	accSeed, err := accKP.Seed()
	require.NoError(t, err)
	accPub, err := accKP.PublicKey()
	require.NoError(t, err)

	accClaims := jwt.NewAccountClaims(accPub)
	accClaims.Name = "minimal-account"
	accClaims.IssuedAt = time.Now().Unix()
	accJWT, err := accClaims.Encode(opKP)
	require.NoError(t, err)

	// Generate user key + JWT.
	userKP, err := nkeys.CreateUser()
	require.NoError(t, err)
	userSeed, err := userKP.Seed()
	require.NoError(t, err)
	userPub, err := userKP.PublicKey()
	require.NoError(t, err)

	userClaims := jwt.NewUserClaims(userPub)
	userClaims.Name = "minimal-user"
	userClaims.IssuedAt = time.Now().Unix()
	userJWT, err := userClaims.Encode(accKP)
	require.NoError(t, err)

	// Lay out files in the NSC store structure inside a tar.gz.
	files := map[string][]byte{
		filepath.Join("operator", "operator.jwt"):                                 []byte(opJWT),
		filepath.Join("operator", "accounts", "minimal-account", "minimal-account.jwt"):   []byte(accJWT),
		filepath.Join("operator", "accounts", "minimal-account", "users", "minimal-user.jwt"): []byte(userJWT),
		filepath.Join("nkeys", "keys", "O", opPub[1:3], opPub+".nk"):                opSeed,
		filepath.Join("nkeys", "keys", "A", accPub[1:3], accPub+".nk"):              accSeed,
		filepath.Join("nkeys", "keys", "U", userPub[1:3], userPub+".nk"):            userSeed,
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for name, contents := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o600,
			Size: int64(len(contents)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(contents)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	if buf.Len() == 0 {
		t.Fatal(fmt.Errorf("empty archive generated"))
	}
	return buf.Bytes()
}
