package entities

import (
	"time"

	"github.com/google/uuid"
)

// UserJWTRevocation is a NIS-side bookkeeping row for one entry in the parent
// account JWT's NATS Revocations map. The active rows (PrunedAt == nil) are
// flattened into AccountClaims.Revocations on every account-JWT regen; once
// JWTExp has elapsed the sweeper marks PrunedAt and re-signs the account JWT
// without the entry (NATS rejects on `exp` anyway, so the entry would be dead
// weight).
type UserJWTRevocation struct {
	ID            uuid.UUID
	AccountID     uuid.UUID
	UserID        *uuid.UUID // nullable: kept so revocation survives hard-deletion of the user row
	UserPublicKey string     // NATS U-prefix key — what goes into AccountClaims.Revocations
	RevokedAt     time.Time
	JWTExp        time.Time // `exp` of the revoked user JWT; the sweeper compares against now()
	Reason        string
	PrunedAt      *time.Time
	CreatedAt     time.Time
}
