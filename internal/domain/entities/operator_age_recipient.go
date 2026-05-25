package entities

import (
	"time"

	"github.com/google/uuid"
)

// OperatorAgeRecipient is one age recipient public key authorised to decrypt
// scheduled backups for one operator (P15, 2026-05-25). The private half lives
// wherever the operator chooses — yubikey, encrypted disk, secret manager —
// and is never seen by NIS.
//
// Multiple recipients per operator are explicitly supported and the canonical
// shape: ops team + DR location + per-engineer keys all encrypted in one pass
// so any single identity can decrypt independently. age.Encrypt accepts a
// slice; each recipient is wrapped independently in the artifact header.
//
// PublicKey carries the wire form of an age recipient — either
// `age1<bech32>` (X25519) or `ssh-ed25519 ...` (SSH). Validation lives in
// the service layer via age.ParseRecipient; the entity itself is just a
// transport struct.
//
// CreatedByUserID is FK SET NULL — offboarding the api_user that added the
// recipient does not invalidate the recipient (parallel to api_tokens.created_by_user_id).
type OperatorAgeRecipient struct {
	ID              uuid.UUID
	OperatorID      uuid.UUID
	PublicKey       string
	Label           string
	CreatedAt       time.Time
	CreatedByUserID *uuid.UUID
}
