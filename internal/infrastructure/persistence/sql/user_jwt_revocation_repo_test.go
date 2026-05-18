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
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

// UserJWTRevocationRepoTestSuite exercises UserJWTRevocationRepo in isolation.
// It uses its own in-memory SQLite instance so it does not share state with the
// existing RepositoryTestSuite.
type UserJWTRevocationRepoTestSuite struct {
	suite.Suite
	db          *gorm.DB
	operatorRepo *OperatorRepo
	accountRepo  *AccountRepo
	revRepo      *UserJWTRevocationRepo
}

func (s *UserJWTRevocationRepoTestSuite) SetupSuite() {
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
	s.revRepo = NewUserJWTRevocationRepo(db)
}

func (s *UserJWTRevocationRepoTestSuite) TearDownSuite() {
	sqlDB, _ := s.db.DB()
	_ = sqlDB.Close()
}

func (s *UserJWTRevocationRepoTestSuite) SetupTest() {
	s.db.Exec("DELETE FROM user_jwt_revocations")
	s.db.Exec("DELETE FROM users")
	s.db.Exec("DELETE FROM scoped_signing_keys")
	s.db.Exec("DELETE FROM accounts")
	s.db.Exec("DELETE FROM operators")
}

func TestUserJWTRevocationRepoSuite(t *testing.T) {
	suite.Run(t, new(UserJWTRevocationRepoTestSuite))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// makeOperatorAndAccount inserts a minimal operator + account and returns both IDs.
func (s *UserJWTRevocationRepoTestSuite) makeOperatorAndAccount() (opID, accID uuid.UUID) {
	opID = uuid.New()
	err := s.operatorRepo.Create(context.Background(), &entities.Operator{
		ID:        opID,
		Name:      "test-op-" + opID.String()[:8],
		PublicKey: "O" + opID.String(),
	})
	require.NoError(s.T(), err)

	accID = uuid.New()
	err = s.accountRepo.Create(context.Background(), &entities.Account{
		ID:         accID,
		OperatorID: opID,
		Name:       "test-acc-" + accID.String()[:8],
		PublicKey:  "A" + accID.String(),
	})
	require.NoError(s.T(), err)
	return
}

// makeRevocation builds and persists a revocation row for the given account.
// jwtExp controls whether the row is "prunable" (past) or "active" (future).
func (s *UserJWTRevocationRepoTestSuite) makeRevocation(accID uuid.UUID, jwtExp time.Time) *entities.UserJWTRevocation {
	rev := &entities.UserJWTRevocation{
		ID:            uuid.New(),
		AccountID:     accID,
		UserPublicKey: "U" + uuid.New().String(),
		RevokedAt:     time.Now().Add(-time.Hour),
		JWTExp:        jwtExp,
		CreatedAt:     time.Now(),
	}
	require.NoError(s.T(), s.revRepo.Create(context.Background(), rev))
	return rev
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRevocationRepo_Create verifies basic insertion and round-trip.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_Create() {
	ctx := context.Background()
	_, accID := s.makeOperatorAndAccount()

	rev := &entities.UserJWTRevocation{
		ID:            uuid.New(),
		AccountID:     accID,
		UserPublicKey: "UABC123",
		RevokedAt:     time.Now().UTC().Truncate(time.Second),
		JWTExp:        time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second),
		Reason:        "test reason",
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
	}

	err := s.revRepo.Create(ctx, rev)
	require.NoError(s.T(), err)

	got, err := s.revRepo.GetByID(ctx, rev.ID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), rev.ID, got.ID)
	assert.Equal(s.T(), rev.AccountID, got.AccountID)
	assert.Equal(s.T(), rev.UserPublicKey, got.UserPublicKey)
	assert.Equal(s.T(), rev.Reason, got.Reason)
	assert.Nil(s.T(), got.PrunedAt, "new revocation must not be pruned")
}

// TestRevocationRepo_GetByID_NotFound verifies ErrNotFound for missing IDs.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_GetByID_NotFound() {
	ctx := context.Background()
	_, err := s.revRepo.GetByID(ctx, uuid.New())
	assert.ErrorIs(s.T(), err, repositories.ErrNotFound)
}

// TestRevocationRepo_ListActiveByAccount verifies that only non-pruned rows for
// the target account are returned.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_ListActiveByAccount() {
	ctx := context.Background()
	_, acc1 := s.makeOperatorAndAccount()
	_, acc2 := s.makeOperatorAndAccount()

	futureExp := time.Now().Add(48 * time.Hour)

	// Two active revocations for acc1.
	r1 := s.makeRevocation(acc1, futureExp)
	r2 := s.makeRevocation(acc1, futureExp)

	// One pruned revocation for acc1 — must NOT appear in ListActiveByAccount.
	r3 := s.makeRevocation(acc1, futureExp)
	prunedAt := time.Now()
	require.NoError(s.T(), s.revRepo.MarkPruned(ctx, []uuid.UUID{r3.ID}, prunedAt))

	// One active revocation for acc2 — must NOT appear in acc1's result.
	s.makeRevocation(acc2, futureExp)

	active, err := s.revRepo.ListActiveByAccount(ctx, acc1)
	require.NoError(s.T(), err)
	require.Len(s.T(), active, 2, "only the two non-pruned acc1 revocations should be returned")

	ids := []uuid.UUID{active[0].ID, active[1].ID}
	assert.Contains(s.T(), ids, r1.ID)
	assert.Contains(s.T(), ids, r2.ID)
}

// TestRevocationRepo_CountActiveByAccount verifies the count helper.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_CountActiveByAccount() {
	ctx := context.Background()
	_, accID := s.makeOperatorAndAccount()

	futureExp := time.Now().Add(48 * time.Hour)
	s.makeRevocation(accID, futureExp)
	s.makeRevocation(accID, futureExp)

	n, err := s.revRepo.CountActiveByAccount(ctx, accID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), int64(2), n)

	// Prune one; count should drop.
	active, err := s.revRepo.ListActiveByAccount(ctx, accID)
	require.NoError(s.T(), err)
	require.Len(s.T(), active, 2)
	require.NoError(s.T(), s.revRepo.MarkPruned(ctx, []uuid.UUID{active[0].ID}, time.Now()))

	n, err = s.revRepo.CountActiveByAccount(ctx, accID)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), int64(1), n)
}

// TestRevocationRepo_ListPrunable verifies that only rows with jwt_exp <= cutoff
// and pruned_at IS NULL are returned, up to the supplied limit.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_ListPrunable() {
	ctx := context.Background()
	_, accID := s.makeOperatorAndAccount()

	now := time.Now()

	// Already-expired JWT exp: should be prunable.
	r1 := s.makeRevocation(accID, now.Add(-2*time.Hour))
	r2 := s.makeRevocation(accID, now.Add(-1*time.Hour))

	// Future JWT exp: should NOT be prunable yet.
	s.makeRevocation(accID, now.Add(24*time.Hour))

	// Already pruned row (past exp but pruned_at set): must NOT appear.
	r4 := s.makeRevocation(accID, now.Add(-30*time.Minute))
	require.NoError(s.T(), s.revRepo.MarkPruned(ctx, []uuid.UUID{r4.ID}, time.Now()))

	prunable, err := s.revRepo.ListPrunable(ctx, now, 100)
	require.NoError(s.T(), err)
	require.Len(s.T(), prunable, 2, "only past-exp, non-pruned rows should be returned")
	ids := []uuid.UUID{prunable[0].ID, prunable[1].ID}
	assert.Contains(s.T(), ids, r1.ID)
	assert.Contains(s.T(), ids, r2.ID)
}

// TestRevocationRepo_ListPrunable_RespectsLimit verifies the limit parameter.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_ListPrunable_RespectsLimit() {
	ctx := context.Background()
	_, accID := s.makeOperatorAndAccount()

	now := time.Now()
	for i := 0; i < 5; i++ {
		s.makeRevocation(accID, now.Add(-time.Duration(i+1)*time.Hour))
	}

	prunable, err := s.revRepo.ListPrunable(ctx, now, 2)
	require.NoError(s.T(), err)
	assert.Len(s.T(), prunable, 2, "limit must cap the result set")
}

// TestRevocationRepo_MarkPruned verifies that multiple IDs can be marked pruned
// in one call and that only those IDs get PrunedAt set.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_MarkPruned() {
	ctx := context.Background()
	_, accID := s.makeOperatorAndAccount()

	pastExp := time.Now().Add(-time.Hour)
	r1 := s.makeRevocation(accID, pastExp)
	r2 := s.makeRevocation(accID, pastExp)
	r3 := s.makeRevocation(accID, pastExp) // will NOT be pruned in this call

	prunedAt := time.Now()
	err := s.revRepo.MarkPruned(ctx, []uuid.UUID{r1.ID, r2.ID}, prunedAt)
	require.NoError(s.T(), err)

	got1, err := s.revRepo.GetByID(ctx, r1.ID)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), got1.PrunedAt, "r1 must be pruned")

	got2, err := s.revRepo.GetByID(ctx, r2.ID)
	require.NoError(s.T(), err)
	assert.NotNil(s.T(), got2.PrunedAt, "r2 must be pruned")

	got3, err := s.revRepo.GetByID(ctx, r3.ID)
	require.NoError(s.T(), err)
	assert.Nil(s.T(), got3.PrunedAt, "r3 must remain un-pruned")
}

// TestRevocationRepo_MarkPruned_EmptySlice verifies no-op behaviour on empty input.
func (s *UserJWTRevocationRepoTestSuite) TestRevocationRepo_MarkPruned_EmptySlice() {
	ctx := context.Background()
	err := s.revRepo.MarkPruned(ctx, []uuid.UUID{}, time.Now())
	assert.NoError(s.T(), err, "MarkPruned with empty slice must not error")
}
