package sql

// migration_org_test.go — round-trip test for migration 00009_add_organizations.
//
// Why this test matters: migration 00009 is hand-edited and contains data
// backfill steps. The backfill inserts a default organization and migrates
// pre-existing operators, api_users, and api_tokens to reference it. A
// schema-only test would not catch silent data loss. This test seeds a DB
// through migration 00008, then applies 00009, and asserts:
//   - the default organization row exists with the canonical UUID
//   - pre-existing operator rows carry the default org ID (NOT NULL)
//   - pre-existing non-admin api_user rows carry the default org ID
//   - platform-admin api_users remain org-scoped NULL (their role == 'admin')
//   - pre-existing api_token rows tied to non-admin users carry the default org ID
//   - the new helper repos (OrganizationRepo, OIDCLoginStateRepo, etc.) can
//     write and read against the migrated schema

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/migrations"
)

// ---- shared helpers -------------------------------------------------------

// openInMemorySQLite returns a connected in-memory SQLite with foreign_keys ON.
func openInMemorySQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on&_loc=UTC")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---- SQLite round-trip test ------------------------------------------------

func TestMigration009_SQLite_BackfillAndConstraints(t *testing.T) {
	db := openInMemorySQLite(t)

	// Step 1: apply migrations 1–8.
	fs, _, ok := migrations.FSForDriver("sqlite")
	require.True(t, ok)
	goose.SetBaseFS(fs)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.UpTo(db, "sqlite", 8))

	// Step 2: seed pre-migration data.
	opID := uuid.New().String()
	op2ID := uuid.New().String()

	// Two operators.
	_, err := db.Exec(`INSERT INTO operators (id, name, description, encrypted_seed, public_key, jwt, system_account_pub_key, user_jwt_ttl_seconds, account_jwt_ttl_seconds, jwt_warn_window_seconds, jwt_auto_renew, backup_enabled, created_at, updated_at) VALUES (?, 'op-one', '', 'seed', 'O1111', 'jwt1', '', 0, 0, 0, 0, 0, datetime('now'), datetime('now'))`, opID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO operators (id, name, description, encrypted_seed, public_key, jwt, system_account_pub_key, user_jwt_ttl_seconds, account_jwt_ttl_seconds, jwt_warn_window_seconds, jwt_auto_renew, backup_enabled, created_at, updated_at) VALUES (?, 'op-two', '', 'seed', 'O2222', 'jwt2', '', 0, 0, 0, 0, 0, datetime('now'), datetime('now'))`, op2ID)
	require.NoError(t, err)

	// Three api_users: admin (role='admin'), two non-admins.
	adminID := uuid.New().String()
	userID := uuid.New().String()
	user2ID := uuid.New().String()
	_, err = db.Exec(`INSERT INTO api_users (id, username, password_hash, role, created_at, updated_at) VALUES (?, 'admin-user', 'h', 'admin', datetime('now'), datetime('now'))`, adminID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO api_users (id, username, password_hash, role, created_at, updated_at) VALUES (?, 'op-admin', 'h', 'operator-admin', datetime('now'), datetime('now'))`, userID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO api_users (id, username, password_hash, role, created_at, updated_at) VALUES (?, 'acc-admin', 'h', 'account-admin', datetime('now'), datetime('now'))`, user2ID)
	require.NoError(t, err)

	// Two api_tokens: one owned by a non-admin, one by the admin.
	tokenByNonAdmin := uuid.New().String()
	tokenByAdmin := uuid.New().String()
	_, err = db.Exec(`INSERT INTO api_tokens (id, name, token_hash, prefix, description, created_by_user_id, role, created_at, updated_at) VALUES (?, 'tok-op', 'h1', 'nis_pat_aaa', '', ?, 'operator-admin', datetime('now'), datetime('now'))`, tokenByNonAdmin, userID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO api_tokens (id, name, token_hash, prefix, description, created_by_user_id, role, created_at, updated_at) VALUES (?, 'tok-adm', 'h2', 'nis_pat_bbb', '', ?, 'admin', datetime('now'), datetime('now'))`, tokenByAdmin, adminID)
	require.NoError(t, err)

	// Step 3: apply migration 00009.
	require.NoError(t, goose.UpTo(db, "sqlite", 9))

	// Step 4: assert default organization was created.
	var orgName string
	err = db.QueryRow(`SELECT name FROM organizations WHERE id = ?`, entities.DefaultOrganizationID).Scan(&orgName)
	require.NoError(t, err, "default organization must exist after migration")
	assert.Equal(t, "default", orgName)

	// Step 5: assert operators have the default org.
	var orgIDForOp string
	err = db.QueryRow(`SELECT organization_id FROM operators WHERE id = ?`, opID).Scan(&orgIDForOp)
	require.NoError(t, err)
	assert.Equal(t, entities.DefaultOrganizationID, orgIDForOp, "operator must be backfilled with default org")

	err = db.QueryRow(`SELECT organization_id FROM operators WHERE id = ?`, op2ID).Scan(&orgIDForOp)
	require.NoError(t, err)
	assert.Equal(t, entities.DefaultOrganizationID, orgIDForOp, "second operator must be backfilled")

	// Step 6: assert non-admin api_users got default org; admin stays NULL.
	var opAdminOrg sql.NullString
	err = db.QueryRow(`SELECT organization_id FROM api_users WHERE id = ?`, userID).Scan(&opAdminOrg)
	require.NoError(t, err)
	assert.True(t, opAdminOrg.Valid, "non-admin user must have organization_id")
	assert.Equal(t, entities.DefaultOrganizationID, opAdminOrg.String)

	var adminOrg sql.NullString
	err = db.QueryRow(`SELECT organization_id FROM api_users WHERE id = ?`, adminID).Scan(&adminOrg)
	require.NoError(t, err)
	assert.False(t, adminOrg.Valid, "platform admin must remain org-NULL after backfill")

	// Step 7: assert api_tokens owned by non-admin got default org; admin-owned stays NULL.
	var tokOrgNonAdmin sql.NullString
	err = db.QueryRow(`SELECT organization_id FROM api_tokens WHERE id = ?`, tokenByNonAdmin).Scan(&tokOrgNonAdmin)
	require.NoError(t, err)
	assert.True(t, tokOrgNonAdmin.Valid, "token owned by non-admin must be org-scoped")
	assert.Equal(t, entities.DefaultOrganizationID, tokOrgNonAdmin.String)

	var tokOrgAdmin sql.NullString
	err = db.QueryRow(`SELECT organization_id FROM api_tokens WHERE id = ?`, tokenByAdmin).Scan(&tokOrgAdmin)
	require.NoError(t, err)
	assert.False(t, tokOrgAdmin.Valid, "token owned by admin must remain org-NULL")

	// Step 8: verify repo round-trips work against the migrated schema.
	// NewDB appends ?_foreign_keys=on&_loc=UTC internally; pass plain ":memory:".
	gormDB, err := NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	// Run migrations on the new GORM db.
	gormSQLDB, _ := gormDB.DB()
	fs2, _, ok2 := migrations.FSForDriver("sqlite")
	require.True(t, ok2)
	goose.SetBaseFS(fs2)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(gormSQLDB, "sqlite"))

	ctx := context.Background()
	orgRepo := NewOrganizationRepo(gormDB)
	ssoRepo := NewOrganizationSSOConfigRepo(gormDB)
	mappingRepo := NewSSORoleMappingRepo(gormDB)
	oidcRepo := NewOIDCLoginStateRepo(gormDB)

	// Org repo: the default org (from migration) is already there; create a new one.
	newOrg := &entities.Organization{
		ID:          uuid.New(),
		Name:        "test-org",
		Slug:        "test-org",
		Description: "test",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, orgRepo.Create(ctx, newOrg))

	got, err := orgRepo.GetByID(ctx, newOrg.ID)
	require.NoError(t, err)
	assert.Equal(t, newOrg.Name, got.Name)

	got2, err := orgRepo.GetBySlug(ctx, "test-org")
	require.NoError(t, err)
	assert.Equal(t, newOrg.ID, got2.ID)

	// SSO config repo.
	cfg := &entities.OrganizationSSOConfig{
		ID:                     uuid.New(),
		OrganizationID:         newOrg.ID,
		Enabled:                true,
		IssuerURL:              "https://idp.example.com",
		ClientID:               "client-123",
		EncryptedClientSecret:  "enc:key:secret",
		Scopes:                 "openid profile email groups",
		GroupClaim:             "groups",
		CreatedAt:              time.Now().UTC(),
		UpdatedAt:              time.Now().UTC(),
	}
	require.NoError(t, ssoRepo.Upsert(ctx, cfg))

	gotCfg, err := ssoRepo.GetByOrganizationID(ctx, newOrg.ID)
	require.NoError(t, err)
	assert.Equal(t, cfg.IssuerURL, gotCfg.IssuerURL)
	assert.Equal(t, cfg.Enabled, gotCfg.Enabled)

	// SSO role mapping repo. Need an operator in the migrated gormDB for the FK.
	opForMapping := &OperatorModel{
		ID:             uuid.New().String(),
		Name:           "map-op",
		EncryptedSeed:  "s",
		PublicKey:      "OMAPOP1111111111111111111",
		JWT:            "j",
		OrganizationID: newOrg.ID.String(),
	}
	require.NoError(t, gormDB.Create(opForMapping).Error)

	mapping := &entities.SSORoleMapping{
		ID:              uuid.New(),
		OrganizationID:  newOrg.ID,
		GroupValue:      "platform-admins",
		Role:            entities.RoleAdmin,
		Priority:        10,
		CreatedAt:       time.Now().UTC(),
	}
	require.NoError(t, mappingRepo.Create(ctx, mapping))

	mappings, err := mappingRepo.ListByOrganization(ctx, newOrg.ID, repositories.SSORoleMappingListFilter{})
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	assert.Equal(t, mapping.GroupValue, mappings[0].GroupValue)

	// OIDC login state repo.
	redirectTo := "/dashboard"
	state := &entities.OIDCLoginState{
		State:          uuid.New().String(),
		OrganizationID: newOrg.ID,
		Nonce:          "nonce-abc",
		PKCEVerifier:   "verifier-xyz",
		RedirectAfter:  &redirectTo,
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
	require.NoError(t, oidcRepo.Create(ctx, state))

	gotState, err := oidcRepo.GetAndDelete(ctx, state.State)
	require.NoError(t, err)
	assert.Equal(t, state.Nonce, gotState.Nonce)
	assert.Equal(t, state.OrganizationID, gotState.OrganizationID)

	// Attempting to read again must return ErrNotFound (consumed).
	_, err = oidcRepo.GetAndDelete(ctx, state.State)
	assert.ErrorIs(t, err, repositories.ErrNotFound, "state must be consumed after GetAndDelete")

	// DeleteExpired removes expired states and returns count.
	expiredState := &entities.OIDCLoginState{
		State:          uuid.New().String(),
		OrganizationID: newOrg.ID,
		Nonce:          "n",
		PKCEVerifier:   "v",
		CreatedAt:      time.Now().UTC().Add(-2 * time.Hour),
		ExpiresAt:      time.Now().UTC().Add(-1 * time.Hour),
	}
	require.NoError(t, oidcRepo.Create(ctx, expiredState))
	n, err := oidcRepo.DeleteExpired(ctx, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

// ---- Postgres round-trip test (skipped when NIS_TEST_PG_DSN is unset) ------

func TestMigration009_Postgres_BackfillAndConstraints(t *testing.T) {
	dsn := os.Getenv("NIS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set NIS_TEST_PG_DSN to run postgres migration test")
	}

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())

	// Use a fresh goose run against the postgres migrations.
	fs, _, ok := migrations.FSForDriver("postgres")
	require.True(t, ok)
	goose.SetBaseFS(fs)
	require.NoError(t, goose.SetDialect("postgres"))

	// Roll back to clean state first.
	_ = goose.DownTo(db, "postgres", 0)

	// Apply migrations 1–8.
	require.NoError(t, goose.UpTo(db, "postgres", 8))

	// Seed pre-migration data (postgres uses $1 placeholders).
	opID := uuid.New().String()
	userID := uuid.New().String()
	adminID := uuid.New().String()
	tokenID := uuid.New().String()

	_, err = db.Exec(`INSERT INTO operators (id,name,description,encrypted_seed,public_key,jwt,system_account_pub_key,user_jwt_ttl_seconds,account_jwt_ttl_seconds,jwt_warn_window_seconds,jwt_auto_renew,backup_enabled,created_at,updated_at) VALUES ($1,'pg-op','','seed','OPGT1111','jwt','',0,0,0,false,false,NOW(),NOW())`, opID)
	require.NoError(t, err)

	_, err = db.Exec(`INSERT INTO api_users (id,username,password_hash,role,created_at,updated_at) VALUES ($1,'pg-nonadmin','h','operator-admin',NOW(),NOW())`, userID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO api_users (id,username,password_hash,role,created_at,updated_at) VALUES ($1,'pg-admin','h','admin',NOW(),NOW())`, adminID)
	require.NoError(t, err)

	_, err = db.Exec(`INSERT INTO api_tokens (id,name,token_hash,prefix,description,created_by_user_id,role,created_at,updated_at) VALUES ($1,'pg-tok','h3','nis_pat_ccc','',$2,'operator-admin',NOW(),NOW())`, tokenID, userID)
	require.NoError(t, err)

	// Apply migration 00009.
	require.NoError(t, goose.UpTo(db, "postgres", 9))

	// Assertions match the SQLite test.
	var orgName string
	err = db.QueryRow(`SELECT name FROM organizations WHERE id = $1`, entities.DefaultOrganizationID).Scan(&orgName)
	require.NoError(t, err, "default org must exist")
	assert.Equal(t, "default", orgName)

	var opOrgID string
	err = db.QueryRow(`SELECT organization_id FROM operators WHERE id = $1`, opID).Scan(&opOrgID)
	require.NoError(t, err)
	assert.Equal(t, entities.DefaultOrganizationID, opOrgID)

	var userOrgID sql.NullString
	err = db.QueryRow(`SELECT organization_id FROM api_users WHERE id = $1`, userID).Scan(&userOrgID)
	require.NoError(t, err)
	assert.True(t, userOrgID.Valid)
	assert.Equal(t, entities.DefaultOrganizationID, userOrgID.String)

	var adminOrgID sql.NullString
	err = db.QueryRow(`SELECT organization_id FROM api_users WHERE id = $1`, adminID).Scan(&adminOrgID)
	require.NoError(t, err)
	assert.False(t, adminOrgID.Valid, "admin must remain org-NULL")

	var tokOrgID sql.NullString
	err = db.QueryRow(`SELECT organization_id FROM api_tokens WHERE id = $1`, tokenID).Scan(&tokOrgID)
	require.NoError(t, err)
	assert.True(t, tokOrgID.Valid)
	assert.Equal(t, entities.DefaultOrganizationID, tokOrgID.String)

	// Tear down.
	_ = goose.DownTo(db, "postgres", 0)
}
