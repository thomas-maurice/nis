package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

// SearchServiceTestSuite covers SearchService's orchestration + RBAC narrowing.
// The fixture deliberately seeds two parallel operator subtrees so the assertion
// for cross-operator isolation has something to fail against.
type SearchServiceTestSuite struct {
	suite.Suite
	ctx     context.Context
	db      *gorm.DB
	factory persistence.RepositoryFactory
	perm    *PermissionService
	svc     *SearchService

	op1, op2     uuid.UUID
	acc1, acc2   uuid.UUID
	usr1, usr2   uuid.UUID
	sk1, sk2     uuid.UUID
	cluster1, cluster2 uuid.UUID

	admin       *entities.APIUser
	opAdmin1    *entities.APIUser // operator-admin for op1 only
	opAdmin2    *entities.APIUser // operator-admin for op2 only
	acctAdmin1  *entities.APIUser // account-admin for acc1 only
}

func (s *SearchServiceTestSuite) SetupSuite() {
	s.ctx = context.Background()

	db, err := sql.NewDB("sqlite", ":memory:")
	require.NoError(s.T(), err)
	s.db = db

	sqlDB, err := db.DB()
	require.NoError(s.T(), err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(s.T(), goose.SetDialect("sqlite3"))
	require.NoError(s.T(), goose.Up(sqlDB, "sqlite"))

	s.factory = persistence.NewSQLRepositoryFactoryFromDB(db)
	s.perm = NewPermissionService(s.factory.OperatorRepository(), s.factory.AccountRepository(), s.factory.UserRepository())
	s.svc = NewSearchService(s.factory)
}

func (s *SearchServiceTestSuite) TearDownSuite() {
	_ = sql.Close(s.db)
}

func (s *SearchServiceTestSuite) SetupTest() {
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")
	s.db.Exec("DELETE FROM api_users")

	now := time.Now().UTC()

	// Two operator trees that share the "metrics" / "shared" substrings so a
	// raw repo search returns both; the cross-operator-isolation tests assert
	// that RBAC narrowing removes the wrong-tenant rows.
	s.op1 = uuid.New()
	s.op2 = uuid.New()
	require.NoError(s.T(), s.factory.OperatorRepository().Create(s.ctx, &entities.Operator{
		ID: s.op1, Name: "metrics-co", Description: "shared infrastructure",
		EncryptedSeed: "x", PublicKey: "O1XXXXXXXXXXXXXXXXXXXXX", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.factory.OperatorRepository().Create(s.ctx, &entities.Operator{
		ID: s.op2, Name: "metrics-rival", Description: "shared infrastructure",
		EncryptedSeed: "x", PublicKey: "O2YYYYYYYYYYYYYYYYYYYYY", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))

	s.acc1 = uuid.New()
	s.acc2 = uuid.New()
	require.NoError(s.T(), s.factory.AccountRepository().Create(s.ctx, &entities.Account{
		ID: s.acc1, OperatorID: s.op1, Name: "metrics-account-a", Description: "",
		EncryptedSeed: "x", PublicKey: "AA111111111111111111111", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.factory.AccountRepository().Create(s.ctx, &entities.Account{
		ID: s.acc2, OperatorID: s.op2, Name: "metrics-account-b", Description: "",
		EncryptedSeed: "x", PublicKey: "AB222222222222222222222", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))

	s.usr1 = uuid.New()
	s.usr2 = uuid.New()
	require.NoError(s.T(), s.factory.UserRepository().Create(s.ctx, &entities.User{
		ID: s.usr1, AccountID: s.acc1, Name: "metrics-bot-1", Description: "",
		EncryptedSeed: "x", PublicKey: "UA111111111111111111111", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.factory.UserRepository().Create(s.ctx, &entities.User{
		ID: s.usr2, AccountID: s.acc2, Name: "metrics-bot-2", Description: "",
		EncryptedSeed: "x", PublicKey: "UB222222222222222222222", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))

	s.sk1 = uuid.New()
	s.sk2 = uuid.New()
	require.NoError(s.T(), s.factory.ScopedSigningKeyRepository().Create(s.ctx, &entities.ScopedSigningKey{
		ID: s.sk1, AccountID: s.acc1, Name: "metrics-writer-a",
		EncryptedSeed: "x", PublicKey: "ASKA1111111111111111111",
		PubAllow:  []string{"metrics.>"},
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.factory.ScopedSigningKeyRepository().Create(s.ctx, &entities.ScopedSigningKey{
		ID: s.sk2, AccountID: s.acc2, Name: "metrics-writer-b",
		EncryptedSeed: "x", PublicKey: "ASKB2222222222222222222",
		PubAllow:  []string{"metrics.>"},
		CreatedAt: now, UpdatedAt: now,
	}))

	s.cluster1 = uuid.New()
	s.cluster2 = uuid.New()
	require.NoError(s.T(), s.factory.ClusterRepository().Create(s.ctx, &entities.Cluster{
		ID: s.cluster1, OperatorID: s.op1, Name: "metrics-cluster-a",
		ServerURLs: []string{"nats://a.example:4222"},
		CreatedAt:  now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.factory.ClusterRepository().Create(s.ctx, &entities.Cluster{
		ID: s.cluster2, OperatorID: s.op2, Name: "metrics-cluster-b",
		ServerURLs: []string{"nats://b.example:4222"},
		CreatedAt:  now, UpdatedAt: now,
	}))

	opAdmin1ScopedOp := s.op1
	opAdmin2ScopedOp := s.op2
	acctAdmin1ScopedAcc := s.acc1
	s.admin = &entities.APIUser{ID: uuid.New(), Username: "admin", Role: entities.RoleAdmin}
	s.opAdmin1 = &entities.APIUser{ID: uuid.New(), Username: "op-admin-1", Role: entities.RoleOperatorAdmin, OperatorID: &opAdmin1ScopedOp}
	s.opAdmin2 = &entities.APIUser{ID: uuid.New(), Username: "op-admin-2", Role: entities.RoleOperatorAdmin, OperatorID: &opAdmin2ScopedOp}
	s.acctAdmin1 = &entities.APIUser{ID: uuid.New(), Username: "acct-admin-1", Role: entities.RoleAccountAdmin, AccountID: &acctAdmin1ScopedAcc}
}

// TestAdmin_SeesAllKinds — sanity: admin gets the full result set for every kind.
func (s *SearchServiceTestSuite) TestAdmin_SeesAllKinds() {
	got, err := s.svc.Search(s.ctx, s.admin, "metrics", nil, 50)
	require.NoError(s.T(), err)
	assert.Len(s.T(), got.Operators, 2, "admin sees both operators matching 'metrics'")
	assert.Len(s.T(), got.Accounts, 2, "admin sees both accounts")
	assert.Len(s.T(), got.Users, 2, "admin sees both users")
	assert.Len(s.T(), got.ScopedSigningKeys, 2, "admin sees both scoped keys")
	assert.Len(s.T(), got.Clusters, 2, "admin sees both clusters")
}

// TestOperatorAdmin_OnlySeesOwnOperatorTree — the headline RBAC assertion.
// operator-admin scoped to op1 must see ONLY op1's tree; op2's matching rows
// must be filtered out even though the LIKE query "metrics" matches both.
// This is the cross-operator isolation requirement the user called out
// explicitly.
func (s *SearchServiceTestSuite) TestOperatorAdmin_OnlySeesOwnOperatorTree() {
	got, err := s.svc.Search(s.ctx, s.opAdmin1, "metrics", nil, 50)
	require.NoError(s.T(), err)

	require.Len(s.T(), got.Operators, 1, "operator-admin must see ONLY their own operator")
	assert.Equal(s.T(), s.op1, got.Operators[0].ID)

	require.Len(s.T(), got.Accounts, 1, "operator-admin must see ONLY accounts under their operator")
	assert.Equal(s.T(), s.acc1, got.Accounts[0].ID)

	require.Len(s.T(), got.Users, 1, "operator-admin must see ONLY users under their operator's accounts")
	assert.Equal(s.T(), s.usr1, got.Users[0].ID)

	require.Len(s.T(), got.ScopedSigningKeys, 1, "operator-admin must see ONLY scoped keys under their operator's accounts")
	assert.Equal(s.T(), s.sk1, got.ScopedSigningKeys[0].ID)

	require.Len(s.T(), got.Clusters, 1, "operator-admin must see ONLY clusters under their operator")
	assert.Equal(s.T(), s.cluster1, got.Clusters[0].ID)
}

// TestOperatorAdmin_SymmetricIsolation — same assertion for op2's admin, in the
// opposite direction. Catches a class of bugs where the filter accidentally
// passes "owned by ANY operator-admin" instead of "owned by THIS one".
func (s *SearchServiceTestSuite) TestOperatorAdmin_SymmetricIsolation() {
	got, err := s.svc.Search(s.ctx, s.opAdmin2, "metrics", nil, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), got.Operators, 1)
	assert.Equal(s.T(), s.op2, got.Operators[0].ID)
	require.Len(s.T(), got.Accounts, 1)
	assert.Equal(s.T(), s.acc2, got.Accounts[0].ID)
	require.Len(s.T(), got.Clusters, 1)
	assert.Equal(s.T(), s.cluster2, got.Clusters[0].ID)
	require.Len(s.T(), got.ScopedSigningKeys, 1)
	assert.Equal(s.T(), s.sk2, got.ScopedSigningKeys[0].ID)
}

// TestAccountAdmin_NarrowedToOwnSubtree — account-admin scoped to acc1 sees
// their parent operator (op1) and clusters under it (per the existing ownsOperator
// semantics that grant read on the parent) BUT must NOT see acc2 or its
// dependents — those belong to a different account, even though they're
// matching the LIKE query and are under a different operator.
func (s *SearchServiceTestSuite) TestAccountAdmin_NarrowedToOwnSubtree() {
	got, err := s.svc.Search(s.ctx, s.acctAdmin1, "metrics", nil, 50)
	require.NoError(s.T(), err)

	// Parent operator IS visible (read-only access via ownsOperator branch).
	require.Len(s.T(), got.Operators, 1)
	assert.Equal(s.T(), s.op1, got.Operators[0].ID, "account-admin sees parent operator, not foreign ones")

	require.Len(s.T(), got.Accounts, 1, "account-admin must see ONLY their own account")
	assert.Equal(s.T(), s.acc1, got.Accounts[0].ID)

	require.Len(s.T(), got.Users, 1, "account-admin must see ONLY users in their account")
	assert.Equal(s.T(), s.usr1, got.Users[0].ID)

	require.Len(s.T(), got.ScopedSigningKeys, 1, "account-admin must see ONLY scoped keys in their account")
	assert.Equal(s.T(), s.sk1, got.ScopedSigningKeys[0].ID)

	// Clusters narrowed by ownsOperator → parent operator's cluster visible,
	// other operator's cluster hidden.
	require.Len(s.T(), got.Clusters, 1)
	assert.Equal(s.T(), s.cluster1, got.Clusters[0].ID, "account-admin sees own-operator's cluster, not foreign cluster")
}

// TestOperatorContext_PopulatedForAllRows — pins the side-band lookup maps so
// the UI can label every row with its owning operator. SYS-like name collisions
// across operators are the original motivation for this — without these maps
// "SYS" or "system" rows render indistinguishably.
func (s *SearchServiceTestSuite) TestOperatorContext_PopulatedForAllRows() {
	got, err := s.svc.Search(s.ctx, s.admin, "metrics", nil, 50)
	require.NoError(s.T(), err)

	assert.Equal(s.T(), "metrics-co", got.OperatorNames[s.op1.String()])
	assert.Equal(s.T(), "metrics-rival", got.OperatorNames[s.op2.String()])

	assert.Equal(s.T(), s.op1.String(), got.AccountOperators[s.acc1.String()])
	assert.Equal(s.T(), s.op2.String(), got.AccountOperators[s.acc2.String()])
}

// TestKindsFilter_ScopesQuery — the `kinds` argument MUST narrow which surfaces
// are hit, regardless of RBAC. Asking only for OPERATOR returns operators only,
// even when other matches exist.
func (s *SearchServiceTestSuite) TestKindsFilter_ScopesQuery() {
	got, err := s.svc.Search(s.ctx, s.admin, "metrics", []SearchKind{SearchKindOperator}, 50)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), got.Operators)
	assert.Empty(s.T(), got.Accounts)
	assert.Empty(s.T(), got.Users)
	assert.Empty(s.T(), got.ScopedSigningKeys)
	assert.Empty(s.T(), got.Clusters)
}

// TestNilApiUser_PermissionDenied — defense in depth: a nil caller (which
// should never happen in production thanks to the auth middleware) must hit a
// permission denied rather than panicking or returning data.
func (s *SearchServiceTestSuite) TestNilApiUser_PermissionDenied() {
	_, err := s.svc.Search(s.ctx, nil, "metrics", nil, 50)
	require.Error(s.T(), err)
	assert.True(s.T(), errors.Is(err, ErrPermissionDenied))
}

// TestQueryValidation — too short / too long / whitespace-only queries return
// the typed sentinel so the handler can map to InvalidArgument.
func (s *SearchServiceTestSuite) TestQueryValidation_TooShort() {
	_, err := s.svc.Search(s.ctx, s.admin, "x", nil, 50)
	require.Error(s.T(), err)
	assert.True(s.T(), errors.Is(err, ErrSearchQueryInvalid))
}

func (s *SearchServiceTestSuite) TestQueryValidation_WhitespaceOnly() {
	_, err := s.svc.Search(s.ctx, s.admin, "   ", nil, 50)
	require.Error(s.T(), err)
	assert.True(s.T(), errors.Is(err, ErrSearchQueryInvalid))
}

func (s *SearchServiceTestSuite) TestQueryValidation_TooLong() {
	_, err := s.svc.Search(s.ctx, s.admin, strings.Repeat("a", 129), nil, 50)
	require.Error(s.T(), err)
	assert.True(s.T(), errors.Is(err, ErrSearchQueryInvalid))
}

// TestLimitDefaultAndCap — limit clamping is exercised here so the handler
// doesn't need to know the numerics. limit=0 ⇒ default 20; limit=10000 ⇒ cap 100.
// We don't seed >20 rows; just assert the call doesn't error.
func (s *SearchServiceTestSuite) TestLimitDefaultAndCap() {
	_, err := s.svc.Search(s.ctx, s.admin, "metrics", nil, 0)
	require.NoError(s.T(), err)
	_, err = s.svc.Search(s.ctx, s.admin, "metrics", nil, 10000)
	require.NoError(s.T(), err)
}

func TestSearchServiceSuite(t *testing.T) {
	suite.Run(t, new(SearchServiceTestSuite))
}
