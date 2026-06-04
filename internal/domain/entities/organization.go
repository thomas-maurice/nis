package entities

import (
	"time"

	"github.com/google/uuid"
)

// DefaultOrganizationID is the fixed UUID for the "default" organization created by the
// backfill migration (00009_add_organizations). All pre-existing operators and
// non-admin api_users are assigned to this org at migration time.
const DefaultOrganizationID = "00000000-0000-0000-0000-000000000001"

// Organization is the top-level tenant. Every operator (and transitively every
// account/user/cluster) belongs to exactly one organization. Platform admins
// (api_users with Role == RoleAdmin) are org-less (OrganizationID == nil).
type Organization struct {
	ID          uuid.UUID
	Name        string
	Slug        string // [a-z0-9-]; unique; used for OIDC SSO routing
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// OrganizationSSOConfig holds the per-org OIDC SSO configuration. 1:1 with Organization.
// EncryptedClientSecret uses the same storage-ref format as operator seeds:
// "encrypted:<key_id>:<base64_ciphertext>".
type OrganizationSSOConfig struct {
	ID                     uuid.UUID
	OrganizationID         uuid.UUID
	Enabled                bool
	IssuerURL              string
	ClientID               string
	EncryptedClientSecret  string
	Scopes                 string // space-separated; default "openid profile email groups"
	GroupClaim             string // claim name containing group list; default "groups"
	DefaultRole            *APIUserRole // nil → deny login when no group mapping matches
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// SSORoleMapping maps an IdP group value to a NIS role+scope for one organization.
// Priority is evaluated ascending (lower number = checked first); first match wins.
type SSORoleMapping struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	GroupValue      string      // IdP group name/value to match
	Role            APIUserRole // must be org-admin, operator-admin, or account-admin; never admin
	ScopeOperatorID *uuid.UUID  // required when Role == operator-admin
	ScopeAccountID  *uuid.UUID  // required when Role == account-admin
	Priority        int         // lower = evaluated first
	CreatedAt       time.Time
}

// OIDCLoginState is a durable (persisted) PKCE+state record created when the
// OIDC login flow is initiated. Stored in the DB so the flow survives process
// restarts and multi-replica deployments. Swept by a recurring job.
type OIDCLoginState struct {
	State          string  // high-entropy opaque string; primary key
	OrganizationID uuid.UUID
	Nonce          string
	PKCEVerifier   string
	RedirectAfter  *string   // optional SPA path to redirect to after successful login
	CreatedAt      time.Time
	ExpiresAt      time.Time
}
