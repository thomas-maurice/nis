package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// User represents an individual NATS connection credential
type User struct {
	ID                 uuid.UUID
	AccountID          uuid.UUID
	Name               string
	Description        string
	EncryptedSeed      string     // Storage reference format
	PublicKey          string     // NATS public key, starts with 'U'
	JWT                string     // User JWT (signed by account or scoped key)
	ScopedSigningKeyID *uuid.UUID // Optional: if signed by a scoped signing key

	// JWT lifecycle metadata (P2). Together they say "this user's current JWT was
	// issued at JWTIssuedAt and expires at JWTExpiresAt; if the operator default
	// is overridden for this user, JWTTTL holds the override."
	//
	// JWTTTL                : per-user TTL override (nil = use operator default). 0 in
	//                         the pointer (i.e. &0) explicitly means "never expire for
	//                         this user even if the operator default is non-zero."
	// JWTIssuedAt           : `iat` of the current JWT.
	// JWTExpiresAt          : `exp` of the current JWT (nil = no exp).
	// RevokedAt             : non-nil = soft-revoked. NATS rejects the user JWT via the
	//                         parent account JWT's Revocations map; this field is the
	//                         NIS-side bookkeeping. Use RegenerateUserJWT to reinstate.
	// RevocationReason      : free-text reason captured at RevokeUser time. Audit trail.
	// LastExpiringWarnIAT   : pinned to the JWT's iat when an expiring_soon event was
	//                         emitted, so the sweeper does NOT re-fire across ticks for
	//                         the same JWT. Cleared on renewal (new iat).
	// LastExpiredAlertIAT   : same primitive but for user.cred.expired (the
	//                         "system was down, JWT already past exp" case).
	JWTTTL              *time.Duration
	JWTIssuedAt         *time.Time
	JWTExpiresAt        *time.Time
	RevokedAt           *time.Time
	RevocationReason    string
	LastExpiringWarnIAT *time.Time
	LastExpiredAlertIAT *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// EffectiveJWTTTL resolves the TTL to apply at JWT mint time for this user.
// Order: explicit per-user override > operator default > zero (no expiry).
func (u *User) EffectiveJWTTTL(op *Operator) time.Duration {
	if u.JWTTTL != nil {
		return *u.JWTTTL
	}
	if op != nil {
		return op.UserJWTTTL
	}
	return 0
}

// GenerateCredsFile returns the full .creds file content for this user
// The seed parameter must be the decrypted NKey seed
func (u *User) GenerateCredsFile(seed string) string {
	return fmt.Sprintf(`-----BEGIN NATS USER JWT-----
%s
------END NATS USER JWT------

************************* IMPORTANT *************************
NKEY Seed printed below can be used to sign and prove identity.
NKEYs are sensitive and should be treated as secrets.

-----BEGIN USER NKEY SEED-----
%s
------END USER NKEY SEED------

*************************************************************
`, u.JWT, seed)
}
