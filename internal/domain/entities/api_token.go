package entities

import (
	"time"

	"github.com/google/uuid"
)

// APITokenPrefix is the literal prefix on every plaintext API token.
// The middleware uses this to fork to token auth before attempting JWT validation.
// Leaked tokens are also easy to scan for in source code / leaked logs.
const APITokenPrefix = "nis_pat_"

// APIToken is a long-lived opaque token for service-account authentication.
// Plaintext is shown ONCE on creation and never recoverable; only the SHA-256
// hash is stored. Role + optional operator/account scope are decided at creation
// time and are NOT coupled to the creator's current role — a token outlives
// changes to its creator's permissions, which is the desired property for CI.
type APIToken struct {
	ID              uuid.UUID
	Name            string
	TokenHash       string     // hex(sha256(plaintext)); never the plaintext
	Prefix          string     // "nis_pat_xxxxxxxx" — display-only
	Description     string
	CreatedByUserID *uuid.UUID // nullable: NULL when creator was deleted (FK SET NULL)
	Role            APIUserRole
	OperatorID      *uuid.UUID // required when Role == operator-admin
	AccountID       *uuid.UUID // required when Role == account-admin
	ExpiresAt       *time.Time // nil = never expires
	LastUsedAt      *time.Time // nil = never used
	RevokedAt       *time.Time // non-nil = revoked
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// IsExpired reports whether the token's expiry has passed. Returns false when ExpiresAt is nil.
func (t *APIToken) IsExpired(now time.Time) bool {
	if t.ExpiresAt == nil {
		return false
	}
	return !now.Before(*t.ExpiresAt)
}

// IsRevoked reports whether the token has been revoked.
func (t *APIToken) IsRevoked() bool {
	return t.RevokedAt != nil
}

// IsActive reports whether the token is currently usable.
func (t *APIToken) IsActive(now time.Time) bool {
	return !t.IsRevoked() && !t.IsExpired(now)
}
