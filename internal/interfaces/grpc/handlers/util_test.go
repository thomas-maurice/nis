package handlers

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/domain/entities"
)

func TestResolveEffectiveOrg(t *testing.T) {
	tokenOrg := uuid.New()
	requestedOrg := uuid.New()

	t.Run("org-scoped caller is pinned to own org and ignores request field", func(t *testing.T) {
		user := &entities.APIUser{Role: entities.RoleOrgAdmin, OrganizationID: &tokenOrg}
		// Even when the request asks for a different org, the token's org wins.
		got, err := resolveEffectiveOrg(user, requestedOrg.String())
		require.NoError(t, err)
		assert.Equal(t, tokenOrg, got)

		// Empty request field is also fine — pinned regardless.
		got, err = resolveEffectiveOrg(user, "")
		require.NoError(t, err)
		assert.Equal(t, tokenOrg, got)
	})

	t.Run("platform admin must specify organization_id", func(t *testing.T) {
		admin := &entities.APIUser{Role: entities.RoleAdmin} // OrganizationID nil
		_, err := resolveEffectiveOrg(admin, "")
		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("platform admin with valid org uses it", func(t *testing.T) {
		admin := &entities.APIUser{Role: entities.RoleAdmin}
		got, err := resolveEffectiveOrg(admin, requestedOrg.String())
		require.NoError(t, err)
		assert.Equal(t, requestedOrg, got)
	})

	t.Run("platform admin with malformed org is rejected", func(t *testing.T) {
		admin := &entities.APIUser{Role: entities.RoleAdmin}
		_, err := resolveEffectiveOrg(admin, "not-a-uuid")
		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
