package sql

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/migrations"
	"gorm.io/gorm"
)

// newListPageTestDB returns a migrated in-memory SQLite DB for listpage tests.
func newListPageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))
	t.Cleanup(func() { _ = Close(db) })
	return db
}

// TestListPageOperator covers scope isolation, cursor pagination, NameLike, and limit clamping.
func TestListPageOperator(t *testing.T) {
	db := newListPageTestDB(t)
	repo := NewOperatorRepo(db)
	ctx := context.Background()

	adminScope := authz.SystemScope()

	base := time.Now().UTC().Truncate(time.Second)

	newOp := func(name string, at time.Time) *entities.Operator {
		op := &entities.Operator{
			ID:            uuid.New(),
			Name:          name,
			EncryptedSeed: "seed",
			PublicKey:     "O" + uuid.New().String(),
			JWT:           "jwt",
			CreatedAt:     at.UTC(),
			UpdatedAt:     at.UTC(),
		}
		require.NoError(t, repo.Create(ctx, op))
		return op
	}

	// Insert 5 operators with distinct created_at values.
	ops := make([]*entities.Operator, 5)
	for i := 0; i < 5; i++ {
		ops[i] = newOp("lp-op-"+uuid.New().String()[:8], base.Add(time.Duration(i)*time.Second))
	}

	t.Run("cursor_round_trip", func(t *testing.T) {
		var collected []*entities.Operator
		cursor := ""
		for round := 0; ; round++ {
			page, next, err := repo.ListPage(ctx, adminScope, repositories.OperatorListFilter{
				Limit:  2,
				Cursor: cursor,
			})
			require.NoError(t, err)
			collected = append(collected, page...)
			if next == "" {
				break
			}
			cursor = next
			if round > 20 {
				t.Fatal("infinite loop in cursor pagination")
			}
		}
		ids := make(map[uuid.UUID]bool)
		for _, op := range collected {
			ids[op.ID] = true
		}
		for _, op := range ops {
			assert.True(t, ids[op.ID], "expected op %s in cursor results", op.ID)
		}
		assert.Equal(t, len(collected), len(ids), "no duplicates")
	})

	t.Run("zero_scope_empty", func(t *testing.T) {
		result, next, err := repo.ListPage(ctx, authz.Scope{}, repositories.OperatorListFilter{})
		require.NoError(t, err)
		assert.Empty(t, result)
		assert.Empty(t, next)
	})

	t.Run("operator_admin_isolation", func(t *testing.T) {
		opA := ops[0]
		opB := ops[1]
		scopeA := authz.Scope{Role: string(entities.RoleOperatorAdmin), ScopeOperatorID: &opA.ID}
		result, _, err := repo.ListPage(ctx, scopeA, repositories.OperatorListFilter{Limit: 100})
		require.NoError(t, err)
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		assert.True(t, ids[opA.ID])
		assert.False(t, ids[opB.ID])
	})

	t.Run("account_admin_sees_own_operator", func(t *testing.T) {
		// Account-admin: set up an account under ops[0].
		accRepo := NewAccountRepo(db)
		acc := &entities.Account{
			ID: uuid.New(), OperatorID: ops[0].ID, Name: "lp-accdm-acc-" + uuid.New().String()[:6],
			EncryptedSeed: "s", PublicKey: "A" + uuid.New().String(), JWT: "j",
			CreatedAt: base, UpdatedAt: base,
		}
		require.NoError(t, accRepo.Create(ctx, acc))
		scope := authz.Scope{Role: string(entities.RoleAccountAdmin), ScopeAccountID: &acc.ID}
		result, _, err := repo.ListPage(ctx, scope, repositories.OperatorListFilter{Limit: 100})
		require.NoError(t, err)
		// Should see exactly ops[0].
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		assert.True(t, ids[ops[0].ID])
		assert.False(t, ids[ops[1].ID])
	})

	t.Run("name_like", func(t *testing.T) {
		prefix := "lp-findme-" + uuid.New().String()[:6]
		opM1 := newOp(prefix+"-a", base.Add(20*time.Second))
		opM2 := newOp(prefix+"-b", base.Add(21*time.Second))

		result, _, err := repo.ListPage(ctx, adminScope, repositories.OperatorListFilter{
			Limit:    100,
			NameLike: prefix,
		})
		require.NoError(t, err)
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		assert.True(t, ids[opM1.ID])
		assert.True(t, ids[opM2.ID])
	})

	t.Run("name_like_special_chars", func(t *testing.T) {
		// Names with SQL LIKE metacharacters must be treated literally.
		special := "lp-special-" + uuid.New().String()[:6] + "_%end"
		_ = newOp(special, base.Add(30*time.Second))

		result, _, err := repo.ListPage(ctx, adminScope, repositories.OperatorListFilter{
			Limit:    100,
			NameLike: "_%end",
		})
		require.NoError(t, err)
		// Should find this operator (substring match including literal _ and %).
		found := false
		for _, r := range result {
			if r.Name == special {
				found = true
			}
		}
		assert.True(t, found, "operator with literal _%end in name not found")
	})

	t.Run("limit_clamp_zero_to_default", func(t *testing.T) {
		// Limit 0 should default to DefaultListLimit (50), not return all.
		// With only 5 ops there's no truncation, but clampListLimit behavior is tested.
		_, _, err := repo.ListPage(ctx, adminScope, repositories.OperatorListFilter{Limit: 0})
		require.NoError(t, err)
	})

	t.Run("limit_clamp_large_to_max", func(t *testing.T) {
		_, _, err := repo.ListPage(ctx, adminScope, repositories.OperatorListFilter{Limit: 9999})
		require.NoError(t, err)
	})

	t.Run("id_tiebreak_stable_across_page_boundary", func(t *testing.T) {
		// Insert 2 operators with identical created_at to test id tie-break.
		sameTime := base.Add(100 * time.Second)
		o1 := newOp("lp-tie-"+uuid.New().String()[:6], sameTime)
		o2 := newOp("lp-tie-"+uuid.New().String()[:6], sameTime)

		// Fetch page 1 (limit=1).
		p1, next, err := repo.ListPage(ctx, adminScope, repositories.OperatorListFilter{Limit: 1, NameLike: "lp-tie-"})
		require.NoError(t, err)
		require.Len(t, p1, 1)
		require.NotEmpty(t, next)

		// Fetch page 2.
		p2, _, err := repo.ListPage(ctx, adminScope, repositories.OperatorListFilter{Limit: 1, NameLike: "lp-tie-", Cursor: next})
		require.NoError(t, err)
		require.Len(t, p2, 1)

		// Two pages must contain different items.
		assert.NotEqual(t, p1[0].ID, p2[0].ID)
		ids := map[uuid.UUID]bool{p1[0].ID: true, p2[0].ID: true}
		assert.True(t, ids[o1.ID] || ids[o2.ID])
	})
}

// TestListPageAccount covers scope isolation for accounts.
func TestListPageAccount(t *testing.T) {
	db := newListPageTestDB(t)
	opRepo := NewOperatorRepo(db)
	repo := NewAccountRepo(db)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Second)

	mkOp := func(name string, at time.Time) *entities.Operator {
		op := &entities.Operator{
			ID:            uuid.New(),
			Name:          name,
			EncryptedSeed: "s",
			PublicKey:     "O" + uuid.New().String(),
			JWT:           "j",
			CreatedAt:     at.UTC(),
			UpdatedAt:     at.UTC(),
		}
		require.NoError(t, opRepo.Create(ctx, op))
		return op
	}

	opA := mkOp("lp-acc-opA-"+uuid.New().String()[:8], base)
	opB := mkOp("lp-acc-opB-"+uuid.New().String()[:8], base.Add(time.Second))

	newAcc := func(opID uuid.UUID, name string, at time.Time) *entities.Account {
		acc := &entities.Account{
			ID:            uuid.New(),
			OperatorID:    opID,
			Name:          name,
			EncryptedSeed: "s",
			PublicKey:     "A" + uuid.New().String(),
			JWT:           "j",
			CreatedAt:     at.UTC(),
			UpdatedAt:     at.UTC(),
		}
		require.NoError(t, repo.Create(ctx, acc))
		return acc
	}

	accsA := []*entities.Account{
		newAcc(opA.ID, "acc-a-1-"+uuid.New().String()[:6], base),
		newAcc(opA.ID, "acc-a-2-"+uuid.New().String()[:6], base.Add(time.Second)),
	}
	accsB := []*entities.Account{
		newAcc(opB.ID, "acc-b-1-"+uuid.New().String()[:6], base),
	}

	t.Run("operator_admin_isolation", func(t *testing.T) {
		scope := authz.Scope{Role: string(entities.RoleOperatorAdmin), ScopeOperatorID: &opA.ID}
		result, _, err := repo.ListPage(ctx, scope, repositories.AccountListFilter{Limit: 100})
		require.NoError(t, err)
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		for _, a := range accsA {
			assert.True(t, ids[a.ID])
		}
		for _, a := range accsB {
			assert.False(t, ids[a.ID])
		}
	})

	t.Run("account_admin_isolation", func(t *testing.T) {
		scope := authz.Scope{Role: string(entities.RoleAccountAdmin), ScopeAccountID: &accsA[0].ID}
		result, _, err := repo.ListPage(ctx, scope, repositories.AccountListFilter{Limit: 100})
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, accsA[0].ID, result[0].ID)
	})

	t.Run("zero_scope_empty", func(t *testing.T) {
		result, _, err := repo.ListPage(ctx, authz.Scope{}, repositories.AccountListFilter{})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("admin_sees_all", func(t *testing.T) {
		scope := authz.SystemScope()
		result, _, err := repo.ListPage(ctx, scope, repositories.AccountListFilter{Limit: 200})
		require.NoError(t, err)
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		for _, a := range accsA {
			assert.True(t, ids[a.ID])
		}
		for _, a := range accsB {
			assert.True(t, ids[a.ID])
		}
	})

	t.Run("operator_id_filter_mismatch_empty", func(t *testing.T) {
		scope := authz.Scope{Role: string(entities.RoleOperatorAdmin), ScopeOperatorID: &opA.ID}
		result, _, err := repo.ListPage(ctx, scope, repositories.AccountListFilter{
			Limit:      100,
			OperatorID: &opB.ID,
		})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("cursor_round_trip_25_rows", func(t *testing.T) {
		// Stand-alone DB for this subtest to avoid cross-test data interference.
		db2 := newListPageTestDB(t)
		opRepo2 := NewOperatorRepo(db2)
		repo2 := NewAccountRepo(db2)
		op := &entities.Operator{ID: uuid.New(), Name: "lp-cr-op-" + uuid.New().String()[:6],
			EncryptedSeed: "s", PublicKey: "O" + uuid.New().String(), JWT: "j",
			CreatedAt: base, UpdatedAt: base}
		require.NoError(t, opRepo2.Create(ctx, op))

		inserted := make([]*entities.Account, 25)
		for i := 0; i < 25; i++ {
			acc := &entities.Account{
				ID: uuid.New(), OperatorID: op.ID,
				Name:          "cr-acc-" + uuid.New().String()[:6],
				EncryptedSeed: "s", PublicKey: "A" + uuid.New().String(), JWT: "j",
				CreatedAt: base.Add(time.Duration(i) * time.Second), UpdatedAt: base,
			}
			require.NoError(t, repo2.Create(ctx, acc))
			inserted[i] = acc
		}

		scope := authz.SystemScope()
		var all []*entities.Account
		cursor := ""
		for round := 0; ; round++ {
			page, next, err := repo2.ListPage(ctx, scope, repositories.AccountListFilter{Limit: 10, Cursor: cursor})
			require.NoError(t, err)
			all = append(all, page...)
			cursor = next
			if next == "" {
				break
			}
			if round > 10 {
				t.Fatal("infinite loop")
			}
		}

		ids := make(map[uuid.UUID]bool)
		for _, a := range all {
			ids[a.ID] = true
		}
		for _, a := range inserted {
			assert.True(t, ids[a.ID], "missing %s", a.ID)
		}
		assert.Equal(t, len(all), len(ids), "duplicates present")
	})
}

// TestListPageUser covers scope isolation for users.
func TestListPageUser(t *testing.T) {
	db := newListPageTestDB(t)
	opRepo := NewOperatorRepo(db)
	accRepo := NewAccountRepo(db)
	repo := NewUserRepo(db)
	ctx := context.Background()

	adminScope := authz.SystemScope()
	base := time.Now().UTC().Truncate(time.Second)

	mkOp := func(name string) *entities.Operator {
		op := &entities.Operator{ID: uuid.New(), Name: name, EncryptedSeed: "s",
			PublicKey: "O" + uuid.New().String(), JWT: "j", CreatedAt: base, UpdatedAt: base}
		require.NoError(t, opRepo.Create(ctx, op))
		return op
	}
	mkAcc := func(opID uuid.UUID, name string) *entities.Account {
		acc := &entities.Account{ID: uuid.New(), OperatorID: opID, Name: name,
			EncryptedSeed: "s", PublicKey: "A" + uuid.New().String(), JWT: "j",
			CreatedAt: base, UpdatedAt: base}
		require.NoError(t, accRepo.Create(ctx, acc))
		return acc
	}
	mkUser := func(accID uuid.UUID, name string, at time.Time) *entities.User {
		u := &entities.User{ID: uuid.New(), AccountID: accID, Name: name,
			EncryptedSeed: "s", PublicKey: "U" + uuid.New().String(), JWT: "j",
			CreatedAt: at.UTC(), UpdatedAt: at.UTC()}
		require.NoError(t, repo.Create(ctx, u))
		return u
	}

	opA := mkOp("lp-usr-opA-" + uuid.New().String()[:8])
	opB := mkOp("lp-usr-opB-" + uuid.New().String()[:8])
	accA := mkAcc(opA.ID, "lp-usr-accA-"+uuid.New().String()[:8])
	accB := mkAcc(opB.ID, "lp-usr-accB-"+uuid.New().String()[:8])

	usersA := []*entities.User{
		mkUser(accA.ID, "usr-a1-"+uuid.New().String()[:6], base),
		mkUser(accA.ID, "usr-a2-"+uuid.New().String()[:6], base.Add(time.Second)),
	}
	usersB := []*entities.User{
		mkUser(accB.ID, "usr-b1-"+uuid.New().String()[:6], base),
	}

	t.Run("operator_admin_isolation", func(t *testing.T) {
		scope := authz.Scope{Role: string(entities.RoleOperatorAdmin), ScopeOperatorID: &opA.ID}
		result, _, err := repo.ListPage(ctx, scope, repositories.UserListFilter{Limit: 100})
		require.NoError(t, err)
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		for _, u := range usersA {
			assert.True(t, ids[u.ID])
		}
		for _, u := range usersB {
			assert.False(t, ids[u.ID])
		}
	})

	t.Run("account_admin_isolation", func(t *testing.T) {
		scope := authz.Scope{Role: string(entities.RoleAccountAdmin), ScopeAccountID: &accB.ID}
		result, _, err := repo.ListPage(ctx, scope, repositories.UserListFilter{Limit: 100})
		require.NoError(t, err)
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		for _, u := range usersB {
			assert.True(t, ids[u.ID])
		}
		for _, u := range usersA {
			assert.False(t, ids[u.ID])
		}
	})

	t.Run("zero_scope_empty", func(t *testing.T) {
		result, _, err := repo.ListPage(ctx, authz.Scope{}, repositories.UserListFilter{})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("admin_sees_all", func(t *testing.T) {
		result, _, err := repo.ListPage(ctx, adminScope, repositories.UserListFilter{Limit: 200})
		require.NoError(t, err)
		ids := make(map[uuid.UUID]bool)
		for _, r := range result {
			ids[r.ID] = true
		}
		for _, u := range usersA {
			assert.True(t, ids[u.ID])
		}
		for _, u := range usersB {
			assert.True(t, ids[u.ID])
		}
	})
}

// TestEncodeCursorRoundTrip tests cursor encode/decode round-trip.
func TestEncodeCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	ts := time.Now().UTC().Truncate(time.Millisecond)

	cursor := EncodeCursor(ts, id)
	assert.NotEmpty(t, cursor)

	gotTime, gotID, err := DecodeCursor(cursor)
	require.NoError(t, err)
	assert.Equal(t, id, gotID)
	assert.Equal(t, ts.UTC().Unix(), gotTime.UTC().Unix())
}

// TestDecodeCursorErrors tests error cases.
func TestDecodeCursorErrors(t *testing.T) {
	_, _, err := DecodeCursor("notbase64!!!")
	assert.Error(t, err)

	_, _, err = DecodeCursor("")
	assert.Error(t, err)
}

// TestClampListLimit tests the limit clamping helper.
func TestClampListLimit(t *testing.T) {
	assert.Equal(t, DefaultListLimit, clampListLimit(0))
	assert.Equal(t, DefaultListLimit, clampListLimit(-5))
	assert.Equal(t, MaxListLimit, clampListLimit(9999))
	assert.Equal(t, MaxListLimit, clampListLimit(MaxListLimit+1))
	assert.Equal(t, 50, clampListLimit(50))
	assert.Equal(t, 200, clampListLimit(200))
}
