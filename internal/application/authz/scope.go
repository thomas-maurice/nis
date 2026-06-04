// Package authz carries the authorization scope of a caller through the
// service and repository layers. A Scope is a small, value-typed bundle of
// the caller's role and the tenant identifiers (operator / account) the
// caller is bound to.
//
// The package exists to enforce one rule, mechanically: every List*
// repository method takes a Scope as a required parameter — never read from
// context. Omitting it is a compile error, the only reliable way to prevent
// future code from silently returning cross-tenant rows. This is the
// structural replacement for the PermissionService.Filter{Operators,
// Accounts,Users} post-fetch helpers that handlers had to remember to call.
//
// Scope is produced in exactly two ways:
//
//   - ScopeFromAPIUser at the service boundary, when a request comes in via
//     gRPC and authctx.GetUser has returned an authenticated APIUser.
//
//   - SystemScope for background jobs, sweepers, restore paths, and any
//     other internal caller without a user. SystemScope grants admin-
//     equivalent visibility — there is no implicit fallthrough, every
//     internal caller must opt in explicitly.
//
// Repos translate Scope to SQL WHERE clauses; they do not consult ctx for
// auth state. See SKILL §15 (Pagination & repo-layer scope) for the full
// mechanism and the carve-outs.
//
// # Adding a new role
//
// The role is stored as a string (not a bag of bool flags) so a new role
// is one constant + optionally one IsX helper, with no changes required at
// the call sites of existing helpers. For example, adding a "read-only"
// role:
//
//  1. Add `const RoleReadOnly entities.APIUserRole = "read-only"` in
//     entities/api_user.go.
//  2. Extend the switch in ScopeFromAPIUser to map it to Scope{Role:
//     string(RoleReadOnly), ScopeOperatorID: u.OperatorID, ...}.
//  3. Add `func (s Scope) IsReadOnly() bool { return s.Role ==
//     string(entities.RoleReadOnly) }`.
//  4. Each repo's ListPage decides whether read-only sees admin-equivalent
//     rows, operator-scoped rows, or nothing — by adding a new case to its
//     existing switch on scope. Compile-time exhaustiveness is not
//     enforced, but the bool-flag alternative was strictly worse: it would
//     require a new struct field, every constructor would need to remember
//     to set it, and every consumer's true/false branch logic would need
//     auditing.
//
// The bool-flag-per-role design that briefly existed during the initial A7
// implementation was rejected for exactly this reason — it does not scale.
package authz

import (
	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// SystemRole is the synthetic role assigned to internal callers (background
// jobs, sweepers, restores). It is intentionally distinct from the values
// of entities.APIUserRole so it cannot be assigned to a stored APIUser row.
const SystemRole = "system"

// Scope is the authorization context carried into every repository List call.
//
// The zero value is NOT a valid scope — passing Scope{} to a repo will deny
// every row (IsZero returns true and the repo short-circuits to an empty
// result). Always construct via SystemScope or ScopeFromAPIUser.
type Scope struct {
	// Role is one of entities.RoleAdmin / RoleOrgAdmin / RoleOperatorAdmin /
	// RoleAccountAdmin or SystemRole. Compared by string equality — SystemRole
	// and the entities.APIUserRole values share a string space deliberately so
	// the IsAdmin helper can collapse "real admin OR background system" without
	// callers branching on the distinction.
	Role string

	// CallerUserID identifies the authenticated APIUser. Zero (uuid.Nil) for
	// SystemScope. Used by repos that enforce per-caller self-scope on top
	// of role-based scope — most notably APIToken's "I see only my tokens"
	// rule.
	CallerUserID uuid.UUID

	// ScopeOrganizationID narrows visibility to a single organization. Set
	// for org-admin; nil for SystemScope/admin/operator-admin/account-admin.
	// Repos with an IsOrgAdmin() branch filter by this ID via the operators
	// table (operator_repo directly; all others via operator_id FK).
	ScopeOrganizationID *uuid.UUID

	// ScopeOperatorID narrows visibility to a single operator. Required for
	// operator-admin; nil for SystemScope/admin. For account-admin the
	// owning operator is derived at filter-build time inside the repo.
	ScopeOperatorID *uuid.UUID

	// ScopeAccountID narrows visibility to a single account. Required for
	// account-admin; nil otherwise.
	ScopeAccountID *uuid.UUID
}

// IsZero reports whether the scope grants no access at all. Returned by the
// nil-APIUser path and by ScopeFromAPIUser on an unknown role; repos use it
// to short-circuit to an empty result before issuing any query.
func (s Scope) IsZero() bool { return s.Role == "" }

// IsSystem reports whether the scope was produced by SystemScope.
func (s Scope) IsSystem() bool { return s.Role == SystemRole }

// IsAdmin reports whether the scope grants admin-equivalent visibility —
// either a real admin user or a system caller. Used by repos that branch on
// "no narrowing".
func (s Scope) IsAdmin() bool {
	return s.Role == SystemRole || s.Role == string(entities.RoleAdmin)
}

// IsOperatorAdmin reports whether the scope is bound to a single operator.
func (s Scope) IsOperatorAdmin() bool {
	return s.Role == string(entities.RoleOperatorAdmin)
}

// IsOrgAdmin reports whether the scope is bound to a single organization.
func (s Scope) IsOrgAdmin() bool {
	return s.Role == string(entities.RoleOrgAdmin)
}

// IsAccountAdmin reports whether the scope is bound to a single account.
func (s Scope) IsAccountAdmin() bool {
	return s.Role == string(entities.RoleAccountAdmin)
}

// SystemScope returns the scope used by background callers (job handlers,
// sweepers, restores). Equivalent to admin visibility — repos apply no
// WHERE narrowing. Every internal caller that lists rows must pass this
// explicitly; there is no default.
func SystemScope() Scope {
	return Scope{Role: SystemRole}
}

// ScopeFromAPIUser derives a Scope from an authenticated APIUser. Returns
// the zero Scope (which denies everything at the repo layer) if apiUser is
// nil — the caller should treat a nil APIUser as an auth failure before
// reaching this helper, but the defensive denial avoids accidental
// escalation if it slips through.
//
// An APIUser with an unknown role also yields the zero Scope; this surfaces
// as "no rows" at the repo, which is the safe failure mode.
func ScopeFromAPIUser(apiUser *entities.APIUser) Scope {
	if apiUser == nil {
		return Scope{}
	}
	switch apiUser.Role {
	case entities.RoleAdmin:
		return Scope{Role: string(entities.RoleAdmin), CallerUserID: apiUser.ID}
	case entities.RoleOrgAdmin:
		return Scope{
			Role:                string(entities.RoleOrgAdmin),
			CallerUserID:        apiUser.ID,
			ScopeOrganizationID: apiUser.OrganizationID,
		}
	case entities.RoleOperatorAdmin:
		return Scope{
			Role:            string(entities.RoleOperatorAdmin),
			CallerUserID:    apiUser.ID,
			ScopeOperatorID: apiUser.OperatorID,
		}
	case entities.RoleAccountAdmin:
		return Scope{
			Role:           string(entities.RoleAccountAdmin),
			CallerUserID:   apiUser.ID,
			ScopeAccountID: apiUser.AccountID,
		}
	default:
		return Scope{}
	}
}
