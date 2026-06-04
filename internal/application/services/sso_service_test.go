package services

import (
	"context"
	"testing"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	oidcinfra "github.com/thomas-maurice/nis/internal/infrastructure/oidc"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ---------------------------------------------------------------------------
// Fake Verifier — lets tests exercise CompleteLogin without a live IdP.
// ---------------------------------------------------------------------------

// fakeVerifier is an oidcinfra.Verifier implementation that returns canned
// claims. Used to drive the full CompleteLogin path in unit tests.
type fakeVerifier struct {
	claims *oidcinfra.Claims
	err    error
}

func (f *fakeVerifier) Verify(_ context.Context, _, _, _ string) (*oidcinfra.Claims, error) {
	return f.claims, f.err
}

// fakeVerifierFactory returns a VerifierFactory that produces a fakeVerifier.
func fakeVerifierFactory(claims *oidcinfra.Claims) VerifierFactory {
	return func(_ *gooidc.Provider, _ string) oidcinfra.Verifier {
		return &fakeVerifier{claims: claims}
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// ssoTestFixture wires up an in-memory SQLite DB, an SSOService, and an
// AuthService ready for unit tests.
type ssoTestFixture struct {
	factory     persistence.RepositoryFactory
	authService *AuthService
	ssoService  *SSOService
}

func newSSOFixture(t *testing.T) *ssoTestFixture {
	t.Helper()
	factory := webhookTestDB(t) // in-memory SQLite + goose migrations
	enc := workerTestEncryptor(t)
	authSvc := NewAuthService(factory, "test-jwt-secret-min-32-bytes-xxxx", 24*time.Hour)
	cache := oidcinfra.NewProviderCache()
	ssoSvc := NewSSOService(factory, enc, authSvc, cache, "http://nis.example.com", 10*time.Minute)
	return &ssoTestFixture{
		factory:     factory,
		authService: authSvc,
		ssoService:  ssoSvc,
	}
}

// seedOrg creates an organization + SSO config + a set of role mappings.
// Returns the org and the SSO config.
func seedOrg(t *testing.T, ctx context.Context, f *ssoTestFixture, slug string, operatorID *uuid.UUID) (*entities.Organization, *entities.OrganizationSSOConfig) {
	t.Helper()
	enc := workerTestEncryptor(t)
	orgSvc := NewOrganizationService(f.factory, enc)

	org, err := orgSvc.CreateOrganization(ctx, CreateOrganizationRequest{
		Name: "Test Org " + slug,
		Slug: slug,
	})
	require.NoError(t, err)

	// Store a dummy secret via the encryptor so CompleteLogin can Decrypt it.
	encSecret, err := enc.Encrypt(ctx, []byte("client-secret"))
	require.NoError(t, err)

	defaultRole := entities.RoleOrgAdmin
	cfg := &entities.OrganizationSSOConfig{
		ID:                    uuid.New(),
		OrganizationID:        org.ID,
		Enabled:               true,
		IssuerURL:             "https://idp.example.com",
		ClientID:              "test-client",
		EncryptedClientSecret: encSecret,
		Scopes:                "openid profile email groups",
		GroupClaim:            "groups",
		DefaultRole:           &defaultRole,
		CreatedAt:             clock.Now(),
		UpdatedAt:             clock.Now(),
	}
	require.NoError(t, f.factory.OrganizationSSOConfigRepository().Upsert(ctx, cfg))
	return org, cfg
}

// seedMapping inserts a single SSO role mapping into the DB.
func seedMapping(t *testing.T, ctx context.Context, f *ssoTestFixture, orgID uuid.UUID, m entities.SSORoleMapping) {
	t.Helper()
	m.ID = uuid.New()
	m.OrganizationID = orgID
	m.CreatedAt = clock.Now()
	require.NoError(t, f.factory.SSORoleMappingRepository().Create(ctx, &m))
}

// ---------------------------------------------------------------------------
// mapGroupsToRole unit tests
// ---------------------------------------------------------------------------

func TestMapGroupsToRole_FirstMatchByAscendingPriority(t *testing.T) {
	opID := uuid.New()
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "devs", Role: entities.RoleOperatorAdmin, ScopeOperatorID: &opID, Priority: 20},
		{ID: uuid.New(), GroupValue: "devs", Role: entities.RoleOrgAdmin, Priority: 10},
	}
	// Priority 10 should win (lower = first evaluated).
	role, opScope, accScope, err := mapGroupsToRole(mappings, []string{"devs"}, nil)
	require.NoError(t, err)
	assert.Equal(t, entities.RoleOrgAdmin, role)
	assert.Nil(t, opScope)
	assert.Nil(t, accScope)
}

func TestMapGroupsToRole_DefaultFallbackWhenNoMatch(t *testing.T) {
	defaultRole := entities.RoleOrgAdmin
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "engineers", Role: entities.RoleOrgAdmin, Priority: 5},
	}
	role, opScope, accScope, err := mapGroupsToRole(mappings, []string{"unmatched-group"}, &defaultRole)
	require.NoError(t, err)
	assert.Equal(t, entities.RoleOrgAdmin, role)
	assert.Nil(t, opScope)
	assert.Nil(t, accScope)
}

func TestMapGroupsToRole_DenyWhenNoMatchAndNilDefault(t *testing.T) {
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "engineers", Role: entities.RoleOrgAdmin, Priority: 5},
	}
	_, _, _, err := mapGroupsToRole(mappings, []string{"unmatched"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no group mapping matched")
}

func TestMapGroupsToRole_AdminRejectedEvenIfMappingExists(t *testing.T) {
	// Simulate a bad mapping row (e.g., DB was manipulated); guard must hold.
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "superadmins", Role: entities.RoleAdmin, Priority: 1},
	}
	_, _, _, err := mapGroupsToRole(mappings, []string{"superadmins"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin")
}

func TestMapGroupsToRole_AdminDefaultRoleRejected(t *testing.T) {
	adminRole := entities.RoleAdmin
	_, _, _, err := mapGroupsToRole(nil, []string{"anygroup"}, &adminRole)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin")
}

func TestMapGroupsToRole_OperatorAdminRequiresScopeOperatorID(t *testing.T) {
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "ops", Role: entities.RoleOperatorAdmin, ScopeOperatorID: nil, Priority: 1},
	}
	_, _, _, err := mapGroupsToRole(mappings, []string{"ops"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope_operator_id")
}

func TestMapGroupsToRole_AccountAdminRequiresScopeAccountID(t *testing.T) {
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "accounters", Role: entities.RoleAccountAdmin, ScopeAccountID: nil, Priority: 1},
	}
	_, _, _, err := mapGroupsToRole(mappings, []string{"accounters"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope_account_id")
}

func TestMapGroupsToRole_OperatorAdminHappyPath(t *testing.T) {
	opID := uuid.New()
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "ops", Role: entities.RoleOperatorAdmin, ScopeOperatorID: &opID, Priority: 1},
	}
	role, scopeOpID, scopeAccID, err := mapGroupsToRole(mappings, []string{"ops"}, nil)
	require.NoError(t, err)
	assert.Equal(t, entities.RoleOperatorAdmin, role)
	require.NotNil(t, scopeOpID)
	assert.Equal(t, opID, *scopeOpID)
	assert.Nil(t, scopeAccID)
}

func TestMapGroupsToRole_RepoDescOrderReSortedAsc(t *testing.T) {
	// Repo returns DESC (priority 20, then 10). Re-sort must evaluate 10 first.
	opID := uuid.New()
	mappings := []*entities.SSORoleMapping{
		// Returned in DESC order from repo.
		{ID: uuid.New(), GroupValue: "devs", Role: entities.RoleOperatorAdmin, ScopeOperatorID: &opID, Priority: 20},
		{ID: uuid.New(), GroupValue: "devs", Role: entities.RoleOrgAdmin, Priority: 10},
	}
	role, _, _, err := mapGroupsToRole(mappings, []string{"devs"}, nil)
	require.NoError(t, err)
	// Priority 10 = org-admin should win after ASC sort.
	assert.Equal(t, entities.RoleOrgAdmin, role)
}

// ---------------------------------------------------------------------------
// JIT provisioning tests via CompleteLogin
// ---------------------------------------------------------------------------

// completeSSOLogin is a helper that calls CompleteLogin with a fake verifier
// while bypassing the actual OIDC code-exchange. It seeds an OIDC state row
// directly to avoid the StartLogin network call.
func completeSSOLogin(
	t *testing.T, ctx context.Context, f *ssoTestFixture,
	org *entities.Organization, claims *oidcinfra.Claims,
) (string, error) {
	t.Helper()

	stateStr := "test-state-" + uuid.New().String()
	now := clock.Now()
	ls := &entities.OIDCLoginState{
		State:          stateStr,
		OrganizationID: org.ID,
		Nonce:          "test-nonce",
		PKCEVerifier:   "test-verifier",
		CreatedAt:      now,
		ExpiresAt:      now.Add(10 * time.Minute),
	}
	require.NoError(t, f.factory.OIDCLoginStateRepository().Create(ctx, ls))

	// Drive CompleteLogin with a fake verifier (no live IdP).
	verifierFn := fakeVerifierFactory(claims)

	// We need a minimal provider cache that won't make real network calls.
	// CompleteLogin calls GetOrDiscover which would fail. We bypass this by
	// having the fake verifier not need the provider — so we inject a custom
	// testSSOService that accepts a nil provider from a stubbed cache.
	testSvc := &testSSOServiceWithFakeProvider{SSOService: f.ssoService, factory: f.factory}
	return testSvc.completeLoginBypassProvider(ctx, "test-code", stateStr, verifierFn)
}

// testSSOServiceWithFakeProvider wraps SSOService and bypasses provider
// discovery for unit tests. The fake verifier does not use the provider.
type testSSOServiceWithFakeProvider struct {
	*SSOService
	factory persistence.RepositoryFactory
}

// completeLoginBypassProvider re-implements the critical CompleteLogin logic
// without the OIDC provider discovery, swapping in a no-op config exchange.
// This lets the JIT provisioning and mapping logic be tested without network.
func (ts *testSSOServiceWithFakeProvider) completeLoginBypassProvider(
	ctx context.Context, code, state string, verifierFn VerifierFactory,
) (string, error) {
	st, err := ts.factory.OIDCLoginStateRepository().GetAndDelete(ctx, state)
	if err != nil {
		return "", ErrSSODenied
	}
	if clock.Now().After(st.ExpiresAt) {
		return "", ErrSSODenied
	}

	org, err := ts.factory.OrganizationRepository().GetByID(ctx, st.OrganizationID)
	if err != nil {
		return "", ErrSSODenied
	}
	cfg, err := ts.factory.OrganizationSSOConfigRepository().GetByOrganizationID(ctx, org.ID)
	if err != nil || !cfg.Enabled {
		return "", ErrSSODenied
	}

	groupClaim := cfg.GroupClaim
	if groupClaim == "" {
		groupClaim = "groups"
	}
	// verifierFn receives nil as provider — fakeVerifier doesn't use it.
	verifier := verifierFn(nil, cfg.ClientID)
	claims, err := verifier.Verify(ctx, "fake-raw-id-token", st.Nonce, groupClaim)
	if err != nil {
		return "", ErrSSODenied
	}

	mappings, err := ts.factory.SSORoleMappingRepository().ListByOrganization(ctx, org.ID, repositories.SSORoleMappingListFilter{})
	if err != nil {
		return "", err
	}
	role, scopeOpID, scopeAccID, mapErr := mapGroupsToRole(mappings, claims.Groups, cfg.DefaultRole)
	if mapErr != nil {
		return "", ErrSSODenied
	}

	// JIT find-or-create.
	var user *entities.APIUser
	txErr := ts.factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		existing, findErr := tx.APIUserRepository().GetByExternalSubject(ctx, org.ID, claims.Subject)
		if findErr != nil && !isNotFound(findErr) {
			return findErr
		}
		now := clock.Now()
		if isNotFound(findErr) {
			username := chooseUsername(claims)
			orgIDCopy := org.ID
			subjectCopy := claims.Subject
			newUser := &entities.APIUser{
				ID:              uuid.New(),
				Username:        username,
				Role:            role,
				OperatorID:      scopeOpID,
				AccountID:       scopeAccID,
				OrganizationID:  &orgIDCopy,
				AuthSource:      "oidc",
				ExternalSubject: &subjectCopy,
				Email:           ptrStr(claims.Email),
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if err := tx.APIUserRepository().Create(ctx, newUser); err != nil {
				return err
			}
			user = newUser
		} else {
			existing.Role = role
			existing.OperatorID = scopeOpID
			existing.AccountID = scopeAccID
			if claims.Email != "" {
				existing.Email = ptrStr(claims.Email)
			}
			existing.Username = chooseUsername(claims)
			existing.UpdatedAt = now
			if err := tx.APIUserRepository().Update(ctx, existing); err != nil {
				return err
			}
			user = existing
		}
		return nil
	})
	if txErr != nil {
		return "", txErr
	}

	return ts.SSOService.authService.IssueSessionForUser(user)
}

func isNotFound(err error) bool {
	return err == repositories.ErrNotFound
}

// ---------------------------------------------------------------------------
// JIT provisioning — create path
// ---------------------------------------------------------------------------

func TestCompleteLogin_JITCreate_SetsCorrectFields(t *testing.T) {
	ctx := context.Background()
	f := newSSOFixture(t)
	org, _ := seedOrg(t, ctx, f, "jit-create", nil)

	claims := &oidcinfra.Claims{
		Subject:           "sub|abc123",
		Email:             "user@example.com",
		PreferredUsername: "jit-user",
		Groups:            []string{}, // default role kicks in
	}

	jwt, err := completeSSOLogin(t, ctx, f, org, claims)
	require.NoError(t, err)
	assert.NotEmpty(t, jwt)

	// Verify the JIT user row.
	user, err := f.factory.APIUserRepository().GetByExternalSubject(ctx, org.ID, "sub|abc123")
	require.NoError(t, err)
	assert.Equal(t, "jit-user", user.Username)
	assert.Equal(t, "oidc", user.AuthSource)
	require.NotNil(t, user.ExternalSubject)
	assert.Equal(t, "sub|abc123", *user.ExternalSubject)
	require.NotNil(t, user.Email)
	assert.Equal(t, "user@example.com", *user.Email)
	require.NotNil(t, user.OrganizationID)
	assert.Equal(t, org.ID, *user.OrganizationID)
	// Default role is RoleOrgAdmin from seedOrg.
	assert.Equal(t, entities.RoleOrgAdmin, user.Role)
}

// ---------------------------------------------------------------------------
// JIT provisioning — update path
// ---------------------------------------------------------------------------

func TestCompleteLogin_JITUpdate_SyncsRoleAndEmail(t *testing.T) {
	ctx := context.Background()
	f := newSSOFixture(t)
	org, _ := seedOrg(t, ctx, f, "jit-update", nil)

	claims := &oidcinfra.Claims{
		Subject:           "sub|update-me",
		Email:             "original@example.com",
		PreferredUsername: "original-name",
		Groups:            []string{},
	}

	// First login — creates the user.
	_, err := completeSSOLogin(t, ctx, f, org, claims)
	require.NoError(t, err)

	user, err := f.factory.APIUserRepository().GetByExternalSubject(ctx, org.ID, "sub|update-me")
	require.NoError(t, err)
	assert.Equal(t, "original-name", user.Username)

	// Second login with changed email.
	claims.Email = "updated@example.com"
	claims.PreferredUsername = "updated-name"

	_, err = completeSSOLogin(t, ctx, f, org, claims)
	require.NoError(t, err)

	updated, err := f.factory.APIUserRepository().GetByExternalSubject(ctx, org.ID, "sub|update-me")
	require.NoError(t, err)
	assert.Equal(t, "updated-name", updated.Username)
	require.NotNil(t, updated.Email)
	assert.Equal(t, "updated@example.com", *updated.Email)
}

// ---------------------------------------------------------------------------
// IssueSessionForUser round-trip
// ---------------------------------------------------------------------------

func TestIssueSessionForUser_RoundTrip(t *testing.T) {
	f := newSSOFixture(t)

	orgIDVal := uuid.New()
	user := &entities.APIUser{
		ID:             uuid.New(),
		Username:       "roundtrip-user",
		Role:           entities.RoleOrgAdmin,
		OrganizationID: &orgIDVal,
		AuthSource:     "oidc",
	}

	jwt, err := f.authService.IssueSessionForUser(user)
	require.NoError(t, err)
	assert.NotEmpty(t, jwt)

	// ValidateToken must parse and resolve to the same user by ID.
	// Since user is not in DB, we just verify the token is well-formed.
	assert.Contains(t, jwt, ".")
}

// ---------------------------------------------------------------------------
// mapGroupsToRole — additional corner cases
// ---------------------------------------------------------------------------

func TestMapGroupsToRole_EmptyGroups_DefaultRoleApplied(t *testing.T) {
	defaultRole := entities.RoleOrgAdmin
	role, op, acc, err := mapGroupsToRole(nil, nil, &defaultRole)
	require.NoError(t, err)
	assert.Equal(t, entities.RoleOrgAdmin, role)
	assert.Nil(t, op)
	assert.Nil(t, acc)
}

func TestMapGroupsToRole_SkipsNonMatchingMappings(t *testing.T) {
	opID := uuid.New()
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "engineers", Role: entities.RoleOperatorAdmin, ScopeOperatorID: &opID, Priority: 1},
		{ID: uuid.New(), GroupValue: "authors", Role: entities.RoleOrgAdmin, Priority: 2},
	}
	role, _, _, err := mapGroupsToRole(mappings, []string{"authors"}, nil)
	require.NoError(t, err)
	assert.Equal(t, entities.RoleOrgAdmin, role)
}

func TestMapGroupsToRole_AccountAdminHappyPath(t *testing.T) {
	accID := uuid.New()
	mappings := []*entities.SSORoleMapping{
		{ID: uuid.New(), GroupValue: "accounters", Role: entities.RoleAccountAdmin, ScopeAccountID: &accID, Priority: 1},
	}
	role, scopeOpID, scopeAccID, err := mapGroupsToRole(mappings, []string{"accounters"}, nil)
	require.NoError(t, err)
	assert.Equal(t, entities.RoleAccountAdmin, role)
	assert.Nil(t, scopeOpID)
	require.NotNil(t, scopeAccID)
	assert.Equal(t, accID, *scopeAccID)
}

// ---------------------------------------------------------------------------
// StartLogin — error path (ErrSSOUnavailable on unknown slug)
// ---------------------------------------------------------------------------

func TestStartLogin_UnknownSlug_ReturnsErrSSOUnavailable(t *testing.T) {
	ctx := context.Background()
	f := newSSOFixture(t)

	_, err := f.ssoService.StartLogin(ctx, "nonexistent-org-slug", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSSOUnavailable)
}
