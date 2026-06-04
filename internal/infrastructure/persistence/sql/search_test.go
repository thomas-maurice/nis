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
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

// SearchTestSuite isolates Search-method coverage on a dedicated fixture set so
// the assertions are tight (counts of matches against known seed data) and
// don't accidentally collide with other CRUD-test seeds.
type SearchTestSuite struct {
	suite.Suite
	db            *gorm.DB
	operatorRepo  *OperatorRepo
	accountRepo   *AccountRepo
	userRepo      *UserRepo
	scopedKeyRepo *ScopedSigningKeyRepo
	clusterRepo   *ClusterRepo

	// Seed IDs reused across tests.
	op1, op2     uuid.UUID
	acc1A, acc2A uuid.UUID
	usr1, usr2   uuid.UUID
	skMetrics    uuid.UUID
	skEvents     uuid.UUID
	cluster1     uuid.UUID
	cluster2     uuid.UUID
}

func (s *SearchTestSuite) SetupSuite() {
	db, err := NewDB("sqlite", ":memory:")
	require.NoError(s.T(), err)
	s.db = db

	sqlDB, err := db.DB()
	require.NoError(s.T(), err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(s.T(), goose.SetDialect("sqlite3"))
	require.NoError(s.T(), goose.Up(sqlDB, "sqlite"))

	s.operatorRepo = NewOperatorRepo(db)
	s.accountRepo = NewAccountRepo(db)
	s.userRepo = NewUserRepo(db)
	s.scopedKeyRepo = NewScopedSigningKeyRepo(db)
	s.clusterRepo = NewClusterRepo(db)
}

func (s *SearchTestSuite) TearDownSuite() {
	sqlDB, _ := s.db.DB()
	_ = sqlDB.Close()
}

// SetupTest reseeds the fixture every test so cases stay independent. The
// fixture deliberately has cross-operator entries with overlapping substrings
// so the per-test assertions also catch RBAC narrowing regressions when run
// alongside service-level tests.
func (s *SearchTestSuite) SetupTest() {
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM clusters")
	s.db.Exec("DELETE FROM operators")

	ctx := context.Background()
	now := time.Now().UTC()

	s.op1 = uuid.New()
	s.op2 = uuid.New()
	require.NoError(s.T(), s.operatorRepo.Create(ctx, &entities.Operator{
		ID: s.op1, Name: "acme-prod", Description: "ACME production operator",
		EncryptedSeed: "x", PublicKey: "OACME1XXXXXXXXXXXXXXXXX", JWT: "j",
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.operatorRepo.Create(ctx, &entities.Operator{
		ID: s.op2, Name: "globex-dev", Description: "Globex development operator",
		EncryptedSeed: "x", PublicKey: "OGLOBEXYYYYYYYYYYYYYYYY", JWT: "j",
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
		CreatedAt: now, UpdatedAt: now,
	}))

	s.acc1A = uuid.New()
	s.acc2A = uuid.New()
	require.NoError(s.T(), s.accountRepo.Create(ctx, &entities.Account{
		ID: s.acc1A, OperatorID: s.op1, Name: "payments", Description: "payments service",
		EncryptedSeed: "x", PublicKey: "APAYMENTS1ZZZZZZZZZZZZZ", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.accountRepo.Create(ctx, &entities.Account{
		ID: s.acc2A, OperatorID: s.op2, Name: "checkout", Description: "checkout flow",
		EncryptedSeed: "x", PublicKey: "ACHECKOUTQQQQQQQQQQQQQQ", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))

	s.usr1 = uuid.New()
	s.usr2 = uuid.New()
	require.NoError(s.T(), s.userRepo.Create(ctx, &entities.User{
		ID: s.usr1, AccountID: s.acc1A, Name: "alice-bot", Description: "deployment bot",
		EncryptedSeed: "x", PublicKey: "UALICE1WWWWWWWWWWWWWWWW", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.userRepo.Create(ctx, &entities.User{
		ID: s.usr2, AccountID: s.acc2A, Name: "bob-dev", Description: "human dev",
		EncryptedSeed: "x", PublicKey: "UBOBVVVVVVVVVVVVVVVVVVV", JWT: "j",
		CreatedAt: now, UpdatedAt: now,
	}))

	s.skMetrics = uuid.New()
	s.skEvents = uuid.New()
	require.NoError(s.T(), s.scopedKeyRepo.Create(ctx, &entities.ScopedSigningKey{
		ID: s.skMetrics, AccountID: s.acc1A, Name: "metrics-writer",
		Description: "publishes metrics", EncryptedSeed: "x", PublicKey: "AMETRICSAAAAAAAAAAAAAAAA",
		PubAllow: []string{"metrics.>"}, PubDeny: []string{"metrics.internal.>"},
		SubAllow: []string{"_INBOX.>"}, SubDeny: []string{},
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.scopedKeyRepo.Create(ctx, &entities.ScopedSigningKey{
		ID: s.skEvents, AccountID: s.acc2A, Name: "events-reader",
		Description: "reads event stream", EncryptedSeed: "x", PublicKey: "AEVENTSBBBBBBBBBBBBBBBBB",
		PubAllow: []string{}, PubDeny: []string{},
		SubAllow: []string{"events.>"}, SubDeny: []string{},
		CreatedAt: now, UpdatedAt: now,
	}))

	s.cluster1 = uuid.New()
	s.cluster2 = uuid.New()
	require.NoError(s.T(), s.clusterRepo.Create(ctx, &entities.Cluster{
		ID: s.cluster1, OperatorID: s.op1, Name: "us-east-prod",
		Description: "primary prod cluster", ServerURLs: []string{"nats://us-east.acme.example:4222"},
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.clusterRepo.Create(ctx, &entities.Cluster{
		ID: s.cluster2, OperatorID: s.op2, Name: "eu-west-dev",
		Description: "dev cluster", ServerURLs: []string{"nats://eu-west.globex.example:4222"},
		CreatedAt: now, UpdatedAt: now,
	}))
}

// ----- Operator -----

func (s *SearchTestSuite) TestOperatorSearch_ByName() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, authz.SystemScope(),"acme", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.op1, got[0].ID)
}

func (s *SearchTestSuite) TestOperatorSearch_ByDescriptionCaseInsensitive() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, authz.SystemScope(),"DEVELOPMENT", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.op2, got[0].ID)
}

func (s *SearchTestSuite) TestOperatorSearch_ByPublicKeyPrefix() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, authz.SystemScope(),"OGLOBEX", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.op2, got[0].ID)
}

func (s *SearchTestSuite) TestOperatorSearch_NoMatchReturnsEmpty() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, authz.SystemScope(),"no-such-org", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got)
}

// ----- Account -----

func (s *SearchTestSuite) TestAccountSearch_ByName() {
	ctx := context.Background()
	got, err := s.accountRepo.Search(ctx, authz.SystemScope(),"payments", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.acc1A, got[0].ID)
}

// ----- User -----

func (s *SearchTestSuite) TestUserSearch_ByDescription() {
	ctx := context.Background()
	got, err := s.userRepo.Search(ctx, authz.SystemScope(),"deployment", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.usr1, got[0].ID)
}

// ----- Scoped signing key — the headline use case (subject search) -----

func (s *SearchTestSuite) TestScopedKeySearch_PubAllowSubjectMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, authz.SystemScope(),"metrics.>", 10)
	require.NoError(s.T(), err)
	// Both name + pub_allow match the metrics-writer key; one row.
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skMetrics, got[0].ID)
}

func (s *SearchTestSuite) TestScopedKeySearch_SubAllowSubjectMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, authz.SystemScope(),"events.>", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skEvents, got[0].ID)
}

func (s *SearchTestSuite) TestScopedKeySearch_PubDenyMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, authz.SystemScope(),"internal", 10)
	require.NoError(s.T(), err)
	// Only metrics-writer has "metrics.internal.>" in pub_deny.
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skMetrics, got[0].ID)
}

func (s *SearchTestSuite) TestScopedKeySearch_NameMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, authz.SystemScope(),"reader", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skEvents, got[0].ID)
}

// ----- Cluster -----

func (s *SearchTestSuite) TestClusterSearch_ByServerURL() {
	ctx := context.Background()
	got, err := s.clusterRepo.Search(ctx, authz.SystemScope(),"us-east.acme", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.cluster1, got[0].ID)
}

// ----- Limit & validation -----

func (s *SearchTestSuite) TestSearchHonoursLimit() {
	ctx := context.Background()
	// "x" matches all four encrypted_seed rows... actually no, we search name/desc/key.
	// Use "operator" which matches both operator descriptions.
	got, err := s.operatorRepo.Search(ctx, authz.SystemScope(),"operator", 1)
	require.NoError(s.T(), err)
	assert.Len(s.T(), got, 1, "limit of 1 must cap result size")
}

func (s *SearchTestSuite) TestSearchZeroLimitReturnsEmpty() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, authz.SystemScope(),"acme", 0)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got)
}

// TestSearchEscapesLikeMetacharacters guards against a user-supplied `%` or `_`
// turning into a wildcard. The fixture has no entry containing the literal `%`
// character, so a search for `%` MUST return zero rows — if it returned any
// row, the `%` was being interpreted as a wildcard.
func (s *SearchTestSuite) TestSearchEscapesLikeMetacharacters() {
	ctx := context.Background()

	got, err := s.operatorRepo.Search(ctx, authz.SystemScope(),"%", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "%% must be treated as a literal, not a wildcard")

	got, err = s.operatorRepo.Search(ctx, authz.SystemScope(),"_", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "_ must be treated as a literal, not a wildcard")
}

// ----- Scope narrowing (A20) -----
//
// These pin the SQL-level RBAC enforcement that replaced the post-fetch
// PermissionService.Filter* helpers. If a repo Search ever forgets to apply
// scope, the LIKE query would silently return rows the caller isn't
// authorized to see — the same leak class A5 addressed for mutations.

func (s *SearchTestSuite) TestOperatorSearch_OperatorAdminScopedToOwnOperator() {
	ctx := context.Background()
	scope := authz.Scope{
		Role:            string(entities.RoleOperatorAdmin),
		ScopeOperatorID: &s.op1,
	}
	got, err := s.operatorRepo.Search(ctx, scope, "development", 10)
	require.NoError(s.T(), err)
	// op2's description matches "development" but op2 is foreign — must be
	// excluded by the scope WHERE narrowing.
	assert.Empty(s.T(), got, "operator-admin must NOT see other operators even when LIKE matches")
}

func (s *SearchTestSuite) TestAccountSearch_AccountAdminScopedToOwnAccount() {
	ctx := context.Background()
	scope := authz.Scope{
		Role:           string(entities.RoleAccountAdmin),
		ScopeAccountID: &s.acc1A,
	}
	// "checkout" matches acc2A's name only; acc1A is the scoped account,
	// so the query must come back empty.
	got, err := s.accountRepo.Search(ctx, scope, "checkout", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "account-admin must NOT see accounts outside their scoped account")
}

func (s *SearchTestSuite) TestUserSearch_OperatorAdminCannotSeeForeignOperatorUsers() {
	ctx := context.Background()
	scope := authz.Scope{
		Role:            string(entities.RoleOperatorAdmin),
		ScopeOperatorID: &s.op1,
	}
	// usr2 (bob-dev) belongs to op2's account — operator-admin on op1
	// must not see it even though it matches the "bob" substring.
	got, err := s.userRepo.Search(ctx, scope, "bob", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "operator-admin must NOT see users under foreign operators")
}

func (s *SearchTestSuite) TestScopedKeySearch_AccountAdminScopedToOwnAccount() {
	ctx := context.Background()
	scope := authz.Scope{
		Role:           string(entities.RoleAccountAdmin),
		ScopeAccountID: &s.acc1A,
	}
	// "events" matches skEvents (under acc2A) only; account-admin on acc1A
	// must come back empty.
	got, err := s.scopedKeyRepo.Search(ctx, scope, "events", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "account-admin must NOT see SSKs outside their account")
}

func (s *SearchTestSuite) TestClusterSearch_OperatorAdminCannotSeeForeignCluster() {
	ctx := context.Background()
	scope := authz.Scope{
		Role:            string(entities.RoleOperatorAdmin),
		ScopeOperatorID: &s.op1,
	}
	// cluster2 is on op2 — must be excluded.
	got, err := s.clusterRepo.Search(ctx, scope, "eu-west", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "operator-admin must NOT see clusters under foreign operators")
}

func (s *SearchTestSuite) TestOperatorSearch_ZeroScopeReturnsEmpty() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, authz.Scope{}, "acme", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "zero scope must short-circuit to empty, never return rows")
}

func TestSearchSuite(t *testing.T) {
	suite.Run(t, new(SearchTestSuite))
}
