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
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlpkg "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

// apiTokenTestDB builds a fresh in-memory SQLite DB with all migrations applied.
// Mirrors webhookTestDB to keep the shape of the unit suite consistent.
func apiTokenTestDB(t *testing.T) persistence.RepositoryFactory {
	t.Helper()
	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))

	t.Cleanup(func() { _ = sqlpkg.Close(db) })
	return persistence.NewSQLRepositoryFactoryFromDB(db)
}

func TestCreateToken_GeneratesPlaintextOnce_StoresHash(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	token, plaintext, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name: "ci",
		Role: entities.RoleAdmin,
	})
	require.NoError(t, err)
	require.NotEmpty(t, plaintext)
	require.True(t, strings.HasPrefix(plaintext, entities.APITokenPrefix),
		"plaintext should carry the canonical prefix so the middleware can fork")
	require.NotEqual(t, plaintext, token.TokenHash,
		"hash must not equal plaintext — that would defeat the point of hashing")
	require.True(t, strings.HasPrefix(token.Prefix, entities.APITokenPrefix),
		"display prefix should also carry nis_pat_ for recognisability")
	require.NotContains(t, token.Prefix, plaintext[len(entities.APITokenPrefix)+8:],
		"display prefix must not leak the full secret")
}

func TestAuthenticate_RoundTrip_Succeeds(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	_, plaintext, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name: "ci",
		Role: entities.RoleAdmin,
	})
	require.NoError(t, err)

	authed, err := svc.Authenticate(ctx, plaintext)
	require.NoError(t, err)
	require.Equal(t, "ci", authed.Name)
}

func TestAuthenticate_UnknownPlaintext_ReturnsInvalid(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	_, err := svc.Authenticate(ctx, entities.APITokenPrefix+"deadbeef")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAPITokenInvalid),
		"unknown tokens must surface as Invalid so the middleware can bucket metrics")
}

func TestAuthenticate_RevokedToken_ReturnsRevoked(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	token, plaintext, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name: "ci",
		Role: entities.RoleAdmin,
	})
	require.NoError(t, err)

	require.NoError(t, svc.RevokeToken(ctx, token.ID))

	_, err = svc.Authenticate(ctx, plaintext)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAPITokenRevoked),
		"revoked tokens must be distinguishable from invalid ones for audit / metrics")
}

func TestAuthenticate_ExpiredToken_ReturnsExpired(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	past := time.Now().Add(-1 * time.Hour)
	_, plaintext, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name:      "ci",
		Role:      entities.RoleAdmin,
		ExpiresAt: &past,
	})
	require.NoError(t, err)

	_, err = svc.Authenticate(ctx, plaintext)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAPITokenExpired),
		"expired tokens must be distinguishable from revoked or invalid ones")
}

func TestRevokeToken_Idempotent(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	token, _, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name: "ci",
		Role: entities.RoleAdmin,
	})
	require.NoError(t, err)

	require.NoError(t, svc.RevokeToken(ctx, token.ID))
	// Revoking a second time must not error — protects callers from a TOCTOU race
	// between "is this revoked?" and "revoke this".
	require.NoError(t, svc.RevokeToken(ctx, token.ID))
}

func TestCreateToken_Validation_RoleScopeMismatch(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	// operator-admin without operator_id
	_, _, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name: "bad-op",
		Role: entities.RoleOperatorAdmin,
	})
	require.Error(t, err)

	// account-admin without account_id
	_, _, err = svc.CreateToken(ctx, CreateAPITokenRequest{
		Name: "bad-acc",
		Role: entities.RoleAccountAdmin,
	})
	require.Error(t, err)

	// admin with stray scope
	opID := uuid.New()
	_, _, err = svc.CreateToken(ctx, CreateAPITokenRequest{
		Name:       "bad-admin",
		Role:       entities.RoleAdmin,
		OperatorID: &opID,
	})
	require.Error(t, err)
}

func TestSyntheticAPIUser_CarriesRoleAndScope(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	opID := uuid.New()
	insertOperator2(t, ctx, factory, opID)

	token, _, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name:       "scoped",
		Role:       entities.RoleOperatorAdmin,
		OperatorID: &opID,
	})
	require.NoError(t, err)

	synth := svc.SyntheticAPIUser(token)
	require.Equal(t, entities.RoleOperatorAdmin, synth.Role)
	require.NotNil(t, synth.OperatorID)
	require.Equal(t, opID, *synth.OperatorID)
	require.Equal(t, token.ID, synth.ID,
		"synthetic user's ID must mirror the token ID so events.actorFromContext can route correctly")
}

// insertOperator2 mirrors webhook_service_test's helper but lets the caller pin
// the ID (we need to use it as scope on the token).
func insertOperator2(t *testing.T, ctx context.Context, factory persistence.RepositoryFactory, opID uuid.UUID) {
	t.Helper()
	require.NoError(t, factory.OperatorRepository().Create(ctx, &entities.Operator{
		ID:             opID,
		Name:           "test-op-" + opID.String()[:8],
		PublicKey:      opID.String(),
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
	}))
}

// --- Permission tests -------------------------------------------------------

func TestPermission_CanCreateAPIToken_EnforcesRoleCeiling(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	perm := NewPermissionService(
		factory.OperatorRepository(),
		factory.AccountRepository(),
		factory.UserRepository(),
		factory.OrganizationRepository(),
	)

	opID := uuid.New()
	insertOperator2(t, ctx, factory, opID)

	operatorAdmin := &entities.APIUser{
		ID:         uuid.New(),
		Role:       entities.RoleOperatorAdmin,
		OperatorID: &opID,
	}

	// operator-admin trying to mint an admin token: refused.
	err := perm.CanCreateAPIToken(ctx, operatorAdmin, entities.RoleAdmin, nil, nil)
	require.Error(t, err, "operator-admin must not mint admin tokens")

	// operator-admin minting operator-admin token in OWN operator: allowed.
	err = perm.CanCreateAPIToken(ctx, operatorAdmin, entities.RoleOperatorAdmin, &opID, nil)
	require.NoError(t, err)

	// operator-admin minting operator-admin token in OTHER operator: refused.
	otherOp := uuid.New()
	err = perm.CanCreateAPIToken(ctx, operatorAdmin, entities.RoleOperatorAdmin, &otherOp, nil)
	require.Error(t, err, "operator-admin must not mint tokens scoped to a different operator")
}

// TestPermission_CanCreateAPIToken_NoCrossTenantEscalation is the load-bearing
// security test for P8. It enumerates every privilege-escalation path we care
// about and asserts each one is denied:
//
//	caller                         attempts                       expected
//	-----------------------------  -----------------------------  --------
//	operator-admin(op X)           role=admin (global)            DENY  (role ceiling)
//	operator-admin(op X)           role=operator-admin, op Y      DENY  (cross-operator)
//	operator-admin(op X)           role=operator-admin, op X      ALLOW
//	operator-admin(op X)           role=account-admin, acct(op Y) DENY  (cross-operator via account)
//	operator-admin(op X)           role=account-admin, acct(op X) ALLOW
//	account-admin(acct A)          role=admin (global)            DENY  (role ceiling)
//	account-admin(acct A)          role=operator-admin, op X      DENY  (role ceiling)
//	account-admin(acct A)          role=account-admin, acct B     DENY  (cross-account)
//	account-admin(acct A)          role=account-admin, acct A     ALLOW
//	admin (no scope)               role=admin (global)            ALLOW
//	admin (no scope)               role=operator-admin, op Y      ALLOW
//	admin (no scope)               role=account-admin, acct B     ALLOW
//
// Adding a new role or relaxing one of the helpers (roleRank, ownsOperator,
// ownsAccount) without updating this table is the failure mode we want to
// catch. If a row here flips from DENY to ALLOW, it is a privilege escalation.
func TestPermission_CanCreateAPIToken_NoCrossTenantEscalation(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	perm := NewPermissionService(
		factory.OperatorRepository(),
		factory.AccountRepository(),
		factory.UserRepository(),
		factory.OrganizationRepository(),
	)

	// Two operators, each with one account. Operator X belongs to the operator-
	// admin caller; operator Y is the "other tenant" we must not be able to touch.
	opX := uuid.New()
	opY := uuid.New()
	insertOperator2(t, ctx, factory, opX)
	insertOperator2(t, ctx, factory, opY)

	acctAInOpX := insertAccountFor(t, ctx, factory, opX, "acct-a-in-x")
	acctBInOpY := insertAccountFor(t, ctx, factory, opY, "acct-b-in-y")

	// Independent of the operator tree: a second account for the account-admin
	// cross-account scenario. Reuses opX so the only difference between the
	// caller's account and the target is the account id itself.
	acctCInOpX := insertAccountFor(t, ctx, factory, opX, "acct-c-in-x")

	adminCaller := &entities.APIUser{ID: uuid.New(), Role: entities.RoleAdmin}
	opXAdmin := &entities.APIUser{
		ID:         uuid.New(),
		Role:       entities.RoleOperatorAdmin,
		OperatorID: &opX,
	}
	acctAAdmin := &entities.APIUser{
		ID:        uuid.New(),
		Role:      entities.RoleAccountAdmin,
		AccountID: &acctAInOpX,
	}

	cases := []struct {
		name       string
		caller     *entities.APIUser
		role       entities.APIUserRole
		operatorID *uuid.UUID
		accountID  *uuid.UUID
		wantAllow  bool
		why        string
	}{
		// -- operator-admin caller (scoped to opX) --
		{"opadmin->admin (global)", opXAdmin, entities.RoleAdmin, nil, nil, false,
			"role ceiling: operator-admin must not mint a global admin token"},
		{"opadmin->opadmin own op", opXAdmin, entities.RoleOperatorAdmin, &opX, nil, true,
			"operator-admin can mint operator-admin tokens for OWN operator"},
		{"opadmin->opadmin other op", opXAdmin, entities.RoleOperatorAdmin, &opY, nil, false,
			"operator-admin must not mint operator-admin tokens for ANOTHER operator (cross-tenant)"},
		{"opadmin->acctadmin acct in own op", opXAdmin, entities.RoleAccountAdmin, nil, &acctAInOpX, true,
			"operator-admin can mint account-admin tokens for accounts in OWN operator"},
		{"opadmin->acctadmin acct in other op", opXAdmin, entities.RoleAccountAdmin, nil, &acctBInOpY, false,
			"operator-admin must not mint account-admin tokens reaching into ANOTHER operator's accounts"},

		// -- account-admin caller (scoped to acctAInOpX) --
		{"acctadmin->admin (global)", acctAAdmin, entities.RoleAdmin, nil, nil, false,
			"role ceiling: account-admin must not mint a global admin token"},
		{"acctadmin->opadmin own op", acctAAdmin, entities.RoleOperatorAdmin, &opX, nil, false,
			"role ceiling: account-admin must not mint operator-admin tokens even within their own operator"},
		{"acctadmin->opadmin other op", acctAAdmin, entities.RoleOperatorAdmin, &opY, nil, false,
			"role ceiling AND cross-operator: account-admin must not mint operator-admin tokens at all"},
		{"acctadmin->acctadmin own account", acctAAdmin, entities.RoleAccountAdmin, nil, &acctAInOpX, true,
			"account-admin can mint account-admin tokens for OWN account"},
		{"acctadmin->acctadmin sibling account same op", acctAAdmin, entities.RoleAccountAdmin, nil, &acctCInOpX, false,
			"account-admin must not mint tokens for a different account, even one in the same operator"},
		{"acctadmin->acctadmin account in other op", acctAAdmin, entities.RoleAccountAdmin, nil, &acctBInOpY, false,
			"account-admin must not mint tokens for an account in a different operator"},

		// -- admin caller (no scope) — sanity that the guard does not over-block --
		{"admin->admin", adminCaller, entities.RoleAdmin, nil, nil, true,
			"global admin can mint anything"},
		{"admin->opadmin", adminCaller, entities.RoleOperatorAdmin, &opY, nil, true,
			"global admin can mint operator-admin tokens for any operator"},
		{"admin->acctadmin", adminCaller, entities.RoleAccountAdmin, nil, &acctBInOpY, true,
			"global admin can mint account-admin tokens for any account"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := perm.CanCreateAPIToken(ctx, tc.caller, tc.role, tc.operatorID, tc.accountID)
			if tc.wantAllow {
				require.NoError(t, err, tc.why)
			} else {
				require.Error(t, err, tc.why)
			}
		})
	}
}

// insertAccountFor creates an account row in the given operator. Used by the
// cross-tenant test above; the entity needs a unique PublicKey so we derive
// one from the ID.
func insertAccountFor(t *testing.T, ctx context.Context, factory persistence.RepositoryFactory, operatorID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, factory.AccountRepository().Create(ctx, &entities.Account{
		ID:         id,
		OperatorID: operatorID,
		Name:       name + "-" + id.String()[:6],
		PublicKey:  "A-" + id.String(),
	}))
	return id
}

func TestPermission_CanReadAPIToken_AdminVsOwner(t *testing.T) {
	factory := apiTokenTestDB(t)
	perm := NewPermissionService(
		factory.OperatorRepository(),
		factory.AccountRepository(),
		factory.UserRepository(),
		factory.OrganizationRepository(),
	)

	adminID := uuid.New()
	otherID := uuid.New()
	admin := &entities.APIUser{ID: adminID, Role: entities.RoleAdmin}
	other := &entities.APIUser{ID: otherID, Role: entities.RoleOperatorAdmin}

	tok := &entities.APIToken{ID: uuid.New(), CreatedByUserID: &adminID}
	require.NoError(t, perm.CanReadAPIToken(admin, tok))
	require.NoError(t, perm.CanReadAPIToken(admin, &entities.APIToken{ID: uuid.New(), CreatedByUserID: &otherID}),
		"admin reads any token")
	require.Error(t, perm.CanReadAPIToken(other, tok),
		"non-admin must not read other users' tokens")
}

// --- Sanity ----------------------------------------------------------------

func TestCreateAndAuthenticate_LastUsedAtNotSetSynchronously(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	token, plaintext, err := svc.CreateToken(ctx, CreateAPITokenRequest{
		Name: "ci",
		Role: entities.RoleAdmin,
	})
	require.NoError(t, err)

	_, err = svc.Authenticate(ctx, plaintext)
	require.NoError(t, err)

	// Service Authenticate must not touch last_used_at — that's the flusher's job,
	// and writing here would defeat the coalescing rationale.
	fresh, err := factory.APITokenRepository().GetByID(ctx, token.ID)
	require.NoError(t, err)
	assert.Nil(t, fresh.LastUsedAt,
		"Authenticate must defer last_used_at to the flusher")
}

// Repo-level sanity: filter excludes revoked tokens by default.
func TestList_DefaultExcludesRevoked(t *testing.T) {
	ctx := context.Background()
	factory := apiTokenTestDB(t)
	svc := NewAPITokenService(factory)

	keep, _, err := svc.CreateToken(ctx, CreateAPITokenRequest{Name: "keep", Role: entities.RoleAdmin})
	require.NoError(t, err)
	rev, _, err := svc.CreateToken(ctx, CreateAPITokenRequest{Name: "rev", Role: entities.RoleAdmin})
	require.NoError(t, err)
	require.NoError(t, svc.RevokeToken(ctx, rev.ID))

	tokens, err := svc.ListTokens(ctx, repositories.APITokenFilter{})
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	require.Equal(t, keep.ID, tokens[0].ID)

	tokens, err = svc.ListTokens(ctx, repositories.APITokenFilter{IncludeRevoked: true})
	require.NoError(t, err)
	require.Len(t, tokens, 2)
}
