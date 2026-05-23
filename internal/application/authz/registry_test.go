package authz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nisv1connect "github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// TestProcedures_KindsAreValid pins that every entry uses one of the four
// real Kind values — KindUnknown is reserved for the zero value and should
// never appear explicitly.
func TestProcedures_KindsAreValid(t *testing.T) {
	for path, p := range Procedures {
		require.NotEqual(t, KindUnknown, p.Kind, "procedure %s has KindUnknown (must be one of PerRow/ScopedList/RoleOnly/Public)", path)
		require.NotEmpty(t, p.Resource, "procedure %s has empty Resource", path)
		// KindPublic doesn't need Action enforced, but the field is still set
		// for consistency with the rest of the table.
		if p.Kind != KindPublic {
			require.NotEmpty(t, p.Action, "procedure %s has empty Action", path)
		}
	}
}

// TestResolveProcedure_HappyPath verifies the lookup returns the row for a
// known procedure path.
func TestResolveProcedure_HappyPath(t *testing.T) {
	p, ok := ResolveProcedure(nisv1connect.AccountServiceCreateAccountProcedure)
	require.True(t, ok)
	assert.Equal(t, ResourceAccount, p.Resource)
	assert.Equal(t, ActionCreate, p.Action)
	assert.Equal(t, KindPerRow, p.Kind)
}

// TestResolveProcedure_Unknown returns ok=false for any path not in the
// registry. Middleware turns this into CodePermissionDenied — the lint
// catches the build-time half.
func TestResolveProcedure_Unknown(t *testing.T) {
	_, ok := ResolveProcedure("/nis.v1.FakeService/Nope")
	assert.False(t, ok)
	_, ok = ResolveProcedure("")
	assert.False(t, ok)
	_, ok = ResolveProcedure("/SomeOther/Path")
	assert.False(t, ok)
}

// TestRolePermits_AdminPermitsEverythingItOwns walks the admin grants and
// asserts each (resource, action) is permitted. Catches policy table
// regressions.
func TestRolePermits_AdminPermitsEverythingItOwns(t *testing.T) {
	for _, g := range RolePolicy {
		if g.Role != entities.RoleAdmin {
			continue
		}
		for _, a := range g.Actions {
			assert.True(t, RolePermits(entities.RoleAdmin, g.Resource, a),
				"admin should be permitted %s.%s", g.Resource, a)
		}
	}
}

// TestRolePermits_DefaultDeny checks that triples NOT in the policy table
// return false. These specific cases were verified manually against the
// pre-A17 casbin_policy.csv.
func TestRolePermits_DefaultDeny(t *testing.T) {
	cases := []struct {
		name     string
		role     entities.APIUserRole
		resource string
		action   string
	}{
		// operator-admin cannot mutate the parent operator (read-only).
		{"operator-admin operator update", entities.RoleOperatorAdmin, ResourceOperator, ActionUpdate},
		{"operator-admin operator delete", entities.RoleOperatorAdmin, ResourceOperator, ActionDelete},

		// operator-admin cannot delete accounts (data-loss guard).
		{"operator-admin account delete", entities.RoleOperatorAdmin, ResourceAccount, ActionDelete},

		// operator-admin cannot delete scoped keys.
		{"operator-admin scoped_key delete", entities.RoleOperatorAdmin, ResourceScopedKey, ActionDelete},

		// operator-admin has no event, job, config, api_user access.
		{"operator-admin event read", entities.RoleOperatorAdmin, ResourceEvent, ActionRead},
		{"operator-admin job read", entities.RoleOperatorAdmin, ResourceJob, ActionRead},
		{"operator-admin config read", entities.RoleOperatorAdmin, ResourceConfig, ActionRead},
		{"operator-admin api_user read", entities.RoleOperatorAdmin, ResourceAPIUser, ActionRead},

		// account-admin is even more constrained.
		{"account-admin scoped_key read", entities.RoleAccountAdmin, ResourceScopedKey, ActionRead},
		{"account-admin user delete", entities.RoleAccountAdmin, ResourceUser, ActionDelete},
		{"account-admin webhook read", entities.RoleAccountAdmin, ResourceWebhook, ActionRead},
		{"account-admin template read", entities.RoleAccountAdmin, ResourceTemplate, ActionRead},
		{"account-admin backup read", entities.RoleAccountAdmin, ResourceBackup, ActionRead},

		// Empty / unknown resource always denies.
		{"admin empty resource", entities.RoleAdmin, "", ActionRead},
		{"admin unknown resource", entities.RoleAdmin, "nonexistent", ActionRead},
		{"admin unknown action", entities.RoleAdmin, ResourceAccount, "exfiltrate"},

		// Unknown role denies.
		{"empty role", entities.APIUserRole(""), ResourceAccount, ActionRead},
		{"made-up role", entities.APIUserRole("super-admin"), ResourceAccount, ActionCreate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.False(t, RolePermits(tc.role, tc.resource, tc.action))
		})
	}
}

// TestRolePermits_KeyExamples pins specific grants that the e2e suite +
// integration tests rely on. Regressions here will cascade into auth_rbac
// e2e failures, which would surface the issue — but failing here is faster.
func TestRolePermits_KeyExamples(t *testing.T) {
	assert.True(t, RolePermits(entities.RoleOperatorAdmin, ResourceAccount, ActionCreate))
	assert.True(t, RolePermits(entities.RoleOperatorAdmin, ResourceCluster, ActionRead))
	assert.True(t, RolePermits(entities.RoleAccountAdmin, ResourceUser, ActionUpdate))
	assert.True(t, RolePermits(entities.RoleAccountAdmin, ResourceAPIToken, ActionCreate))
	assert.True(t, RolePermits(entities.RoleAdmin, ResourceBackup, ActionDelete))
}
