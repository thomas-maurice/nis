package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// orgTestDB reuses the same in-memory SQLite harness as webhookTestDB.
// Defined separately to keep each test's DB isolated.
func orgTestDB(t *testing.T) *orgFixture {
	t.Helper()
	factory := webhookTestDB(t) // same goose migrations, fresh in-memory SQLite
	enc := workerTestEncryptor(t)
	svc := NewOrganizationService(factory, enc)
	return &orgFixture{factory: factory, svc: svc, enc: enc}
}

type orgFixture struct {
	factory interface{ OrganizationRepository() repositories.OrganizationRepository }
	svc     *OrganizationService
	enc     interface{}
}

func TestOrganization_CreateAndGet_Roundtrip(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{
		Name:        "Acme Corp",
		Slug:        "acme",
		Description: "A test org",
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, org.ID)
	assert.Equal(t, "Acme Corp", org.Name)
	assert.Equal(t, "acme", org.Slug)

	got, err := f.svc.GetOrganization(ctx, org.ID)
	require.NoError(t, err)
	assert.Equal(t, org.ID, got.ID)
	assert.Equal(t, org.Name, got.Name)
	assert.Equal(t, org.Slug, got.Slug)
}

func TestOrganization_Create_RequiresNameAndSlug(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	_, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "x"})
	assert.Error(t, err, "empty name should fail")

	_, err = f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "X"})
	assert.Error(t, err, "empty slug should fail")
}

func TestOrganization_SlugValidation_RejectsBadSlugs(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	badSlugs := []string{
		"Has_Underscores",
		"Has Spaces",
		"UPPER",
		"",
		"has.dot",
	}
	for _, slug := range badSlugs {
		_, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "X", Slug: slug})
		assert.Errorf(t, err, "slug %q should be rejected", slug)
	}

	goodSlugs := []string{"abc", "abc-123", "a-b-c"}
	for i, slug := range goodSlugs {
		_, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{
			Name: "Org" + slug,
			Slug: slug + "-" + string(rune('0'+i)), // keep slugs unique
		})
		require.NoErrorf(t, err, "slug %q should be accepted", slug)
	}
}

func TestOrganization_List(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	_, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "OrgA", Slug: "org-a"})
	require.NoError(t, err)
	_, err = f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "OrgB", Slug: "org-b"})
	require.NoError(t, err)

	orgs, err := f.svc.ListOrganizations(ctx, repositories.ListOptions{Limit: 100})
	require.NoError(t, err)
	// At least the two we created (plus the default from migrations).
	assert.GreaterOrEqual(t, len(orgs), 2)
}

func TestOrganization_Update(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "Old Name", Slug: "update-test"})
	require.NoError(t, err)

	updated, err := f.svc.UpdateOrganization(ctx, org.ID, UpdateOrganizationRequest{
		Name:        "New Name",
		Description: "New Desc",
	})
	require.NoError(t, err)
	assert.Equal(t, "New Name", updated.Name)
	assert.Equal(t, "New Desc", updated.Description)
	assert.Equal(t, "update-test", updated.Slug, "slug must be immutable")
}

func TestOrganization_Delete_RefusesDefaultOrg(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	err := f.svc.DeleteOrganization(ctx, uuid.MustParse(entities.DefaultOrganizationID))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete the default organization")
}

func TestOrganization_Delete_Works(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "Del Me", Slug: "del-me"})
	require.NoError(t, err)

	err = f.svc.DeleteOrganization(ctx, org.ID)
	require.NoError(t, err)

	_, err = f.svc.GetOrganization(ctx, org.ID)
	assert.Error(t, err, "deleted org should not be retrievable")
}

func TestOrganization_SetSSOConfig_EncryptsSecret(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "SSO Org", Slug: "sso-org"})
	require.NoError(t, err)

	cfg, err := f.svc.SetSSOConfig(ctx, SetSSOConfigRequest{
		OrganizationID: org.ID,
		Enabled:        true,
		IssuerURL:      "https://idp.example.com",
		ClientID:       "my-client",
		ClientSecret:   "super-secret",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.EncryptedClientSecret)
	assert.NotEqual(t, "super-secret", cfg.EncryptedClientSecret, "secret must be stored encrypted")
}

func TestOrganization_SetSSOConfig_EmptySecretPreservesExisting(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "SSO2 Org", Slug: "sso2-org"})
	require.NoError(t, err)

	// First set with a secret.
	first, err := f.svc.SetSSOConfig(ctx, SetSSOConfigRequest{
		OrganizationID: org.ID,
		Enabled:        true,
		IssuerURL:      "https://idp.example.com",
		ClientID:       "cli",
		ClientSecret:   "original-secret",
	})
	require.NoError(t, err)
	originalEnc := first.EncryptedClientSecret

	// Second set with empty secret — must preserve the original encrypted value.
	second, err := f.svc.SetSSOConfig(ctx, SetSSOConfigRequest{
		OrganizationID: org.ID,
		Enabled:        false,
		IssuerURL:      "https://idp.example.com",
		ClientID:       "cli",
		ClientSecret:   "", // no change
	})
	require.NoError(t, err)
	assert.Equal(t, originalEnc, second.EncryptedClientSecret, "empty client_secret must preserve prior encrypted value")
}

func TestOrganization_SetSSOConfig_RejectsAdminDefaultRole(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "SSO3 Org", Slug: "sso3-org"})
	require.NoError(t, err)

	adminRole := entities.RoleAdmin
	_, err = f.svc.SetSSOConfig(ctx, SetSSOConfigRequest{
		OrganizationID: org.ID,
		DefaultRole:    &adminRole,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be admin")
}

func TestOrganization_SetSSORoleMappings_RejectsAdminRole(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "Mapping Org", Slug: "map-org"})
	require.NoError(t, err)

	_, err = f.svc.SetSSORoleMappings(ctx, org.ID, []SSORoleMappingInput{
		{GroupValue: "admins", Role: entities.RoleAdmin, Priority: 0},
	})
	assert.Error(t, err, "admin role should be rejected in mappings")
}

func TestOrganization_SetSSORoleMappings_RejectsMissingScopeOperatorID(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "Map2 Org", Slug: "map2-org"})
	require.NoError(t, err)

	_, err = f.svc.SetSSORoleMappings(ctx, org.ID, []SSORoleMappingInput{
		{GroupValue: "devs", Role: entities.RoleOperatorAdmin, Priority: 0},
		// missing ScopeOperatorID
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scope_operator_id")
}

func TestOrganization_SetSSORoleMappings_ReplaceAll(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "Map3 Org", Slug: "map3-org"})
	require.NoError(t, err)

	// Set initial mappings.
	_, err = f.svc.SetSSORoleMappings(ctx, org.ID, []SSORoleMappingInput{
		{GroupValue: "g1", Role: entities.RoleOrgAdmin, Priority: 10},
		{GroupValue: "g2", Role: entities.RoleOrgAdmin, Priority: 20},
	})
	require.NoError(t, err)

	mappings, err := f.svc.ListSSORoleMappings(ctx, org.ID)
	require.NoError(t, err)
	assert.Len(t, mappings, 2)

	// Replace with a single mapping.
	_, err = f.svc.SetSSORoleMappings(ctx, org.ID, []SSORoleMappingInput{
		{GroupValue: "g3", Role: entities.RoleOrgAdmin, Priority: 5},
	})
	require.NoError(t, err)

	mappings, err = f.svc.ListSSORoleMappings(ctx, org.ID)
	require.NoError(t, err)
	assert.Len(t, mappings, 1, "replace-all should leave exactly one mapping")
	assert.Equal(t, "g3", mappings[0].GroupValue)
}

func TestOrganization_SetSSORoleMappings_ClearAll(t *testing.T) {
	ctx := context.Background()
	f := orgTestDB(t)

	org, err := f.svc.CreateOrganization(ctx, CreateOrganizationRequest{Name: "Map4 Org", Slug: "map4-org"})
	require.NoError(t, err)

	_, err = f.svc.SetSSORoleMappings(ctx, org.ID, []SSORoleMappingInput{
		{GroupValue: "g1", Role: entities.RoleOrgAdmin, Priority: 1},
	})
	require.NoError(t, err)

	// Clear all.
	_, err = f.svc.SetSSORoleMappings(ctx, org.ID, nil)
	require.NoError(t, err)

	mappings, err := f.svc.ListSSORoleMappings(ctx, org.ID)
	require.NoError(t, err)
	assert.Len(t, mappings, 0, "empty input should clear all mappings")
}
