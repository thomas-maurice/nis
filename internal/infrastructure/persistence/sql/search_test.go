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
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(s.T(), s.operatorRepo.Create(ctx, &entities.Operator{
		ID: s.op2, Name: "globex-dev", Description: "Globex development operator",
		EncryptedSeed: "x", PublicKey: "OGLOBEXYYYYYYYYYYYYYYYY", JWT: "j",
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
	got, err := s.operatorRepo.Search(ctx, "acme", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.op1, got[0].ID)
}

func (s *SearchTestSuite) TestOperatorSearch_ByDescriptionCaseInsensitive() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, "DEVELOPMENT", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.op2, got[0].ID)
}

func (s *SearchTestSuite) TestOperatorSearch_ByPublicKeyPrefix() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, "OGLOBEX", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.op2, got[0].ID)
}

func (s *SearchTestSuite) TestOperatorSearch_NoMatchReturnsEmpty() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, "no-such-org", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got)
}

// ----- Account -----

func (s *SearchTestSuite) TestAccountSearch_ByName() {
	ctx := context.Background()
	got, err := s.accountRepo.Search(ctx, "payments", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.acc1A, got[0].ID)
}

// ----- User -----

func (s *SearchTestSuite) TestUserSearch_ByDescription() {
	ctx := context.Background()
	got, err := s.userRepo.Search(ctx, "deployment", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.usr1, got[0].ID)
}

// ----- Scoped signing key — the headline use case (subject search) -----

func (s *SearchTestSuite) TestScopedKeySearch_PubAllowSubjectMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, "metrics.>", 10)
	require.NoError(s.T(), err)
	// Both name + pub_allow match the metrics-writer key; one row.
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skMetrics, got[0].ID)
}

func (s *SearchTestSuite) TestScopedKeySearch_SubAllowSubjectMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, "events.>", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skEvents, got[0].ID)
}

func (s *SearchTestSuite) TestScopedKeySearch_PubDenyMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, "internal", 10)
	require.NoError(s.T(), err)
	// Only metrics-writer has "metrics.internal.>" in pub_deny.
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skMetrics, got[0].ID)
}

func (s *SearchTestSuite) TestScopedKeySearch_NameMatch() {
	ctx := context.Background()
	got, err := s.scopedKeyRepo.Search(ctx, "reader", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.skEvents, got[0].ID)
}

// ----- Cluster -----

func (s *SearchTestSuite) TestClusterSearch_ByServerURL() {
	ctx := context.Background()
	got, err := s.clusterRepo.Search(ctx, "us-east.acme", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	assert.Equal(s.T(), s.cluster1, got[0].ID)
}

// ----- Limit & validation -----

func (s *SearchTestSuite) TestSearchHonoursLimit() {
	ctx := context.Background()
	// "x" matches all four encrypted_seed rows... actually no, we search name/desc/key.
	// Use "operator" which matches both operator descriptions.
	got, err := s.operatorRepo.Search(ctx, "operator", 1)
	require.NoError(s.T(), err)
	assert.Len(s.T(), got, 1, "limit of 1 must cap result size")
}

func (s *SearchTestSuite) TestSearchZeroLimitReturnsEmpty() {
	ctx := context.Background()
	got, err := s.operatorRepo.Search(ctx, "acme", 0)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got)
}

// TestSearchEscapesLikeMetacharacters guards against a user-supplied `%` or `_`
// turning into a wildcard. The fixture has no entry containing the literal `%`
// character, so a search for `%` MUST return zero rows — if it returned any
// row, the `%` was being interpreted as a wildcard.
func (s *SearchTestSuite) TestSearchEscapesLikeMetacharacters() {
	ctx := context.Background()

	got, err := s.operatorRepo.Search(ctx, "%", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "%% must be treated as a literal, not a wildcard")

	got, err = s.operatorRepo.Search(ctx, "_", 10)
	require.NoError(s.T(), err)
	assert.Empty(s.T(), got, "_ must be treated as a literal, not a wildcard")
}

func TestSearchSuite(t *testing.T) {
	suite.Run(t, new(SearchTestSuite))
}
