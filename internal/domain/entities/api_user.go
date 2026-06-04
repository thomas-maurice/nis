package entities

import (
	"time"

	"github.com/google/uuid"
)

// APIUserRole represents the role of an API user
type APIUserRole string

const (
	// RoleAdmin has full access to all operations (platform super-admin; org-less)
	RoleAdmin APIUserRole = "admin"

	// RoleOrgAdmin manages one organization: its SSO, local users, service-account
	// tokens, and everything beneath (operators read; accounts/users/clusters CRUD).
	RoleOrgAdmin APIUserRole = "org-admin"

	// RoleOperatorAdmin can read operators and manage accounts/users/keys
	RoleOperatorAdmin APIUserRole = "operator-admin"

	// RoleAccountAdmin can read accounts and manage users
	RoleAccountAdmin APIUserRole = "account-admin"
)

// APIUser represents a user of the NIS API
type APIUser struct {
	ID           uuid.UUID
	Username     string
	PasswordHash string // bcrypt hash
	Role         APIUserRole
	OperatorID   *uuid.UUID // Required for operator-admin role
	AccountID    *uuid.UUID // Required for account-admin role

	// Org tenancy (added in 00009_add_organizations).
	// OrganizationID is nil for platform admins (RoleAdmin); set for every
	// other role. OIDC-sourced users always have it set.
	OrganizationID  *uuid.UUID
	AuthSource      string     // "local" (default) | "oidc"
	ExternalSubject *string    // OIDC sub claim; nil for local users
	Email           *string    // sourced from OIDC or set manually

	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsValid checks if the role is a valid API user role
func (r APIUserRole) IsValid() bool {
	switch r {
	case RoleAdmin, RoleOrgAdmin, RoleOperatorAdmin, RoleAccountAdmin:
		return true
	default:
		return false
	}
}

// Rank returns the privilege level of the role. Higher = more privileged.
// Used for role-ceiling checks: a caller may not mint a credential whose Rank
// is >= the caller's own Rank. Unknown roles return 0, which is below every
// known role — they cannot mint anything.
//
// Total order: admin (4) > org-admin (3) > operator-admin (2) > account-admin (1) > unknown (0).
func (r APIUserRole) Rank() int {
	switch r {
	case RoleAdmin:
		return 4
	case RoleOrgAdmin:
		return 3
	case RoleOperatorAdmin:
		return 2
	case RoleAccountAdmin:
		return 1
	default:
		return 0
	}
}
