package entities

import (
	"time"

	"github.com/google/uuid"
)

// ScopedSigningKey represents a permission template for signing users within an account
type ScopedSigningKey struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Name            string
	Description     string
	EncryptedSeed   string   // Storage reference format
	PublicKey       string   // NATS public key, starts with 'A' (account signing key)
	PubAllow        []string // Publish permissions (subject patterns)
	PubDeny         []string // Publish denials (subject patterns)
	SubAllow        []string // Subscribe permissions (subject patterns)
	SubDeny         []string // Subscribe denials (subject patterns)
	ResponseMaxMsgs int      // Max response messages for request-reply
	ResponseTTL     time.Duration // Time-to-live for responses
	// TemplateID + TemplateVersion are both nil OR both set; enforced by a
	// CHECK constraint in the migration. When set, this SKK was created or
	// bumped from templates[TemplateID]@TemplateVersion. The permission
	// columns above are a snapshot of that version at bump time, not a live
	// view — the JWT-regen path reads the columns directly.
	TemplateID      *uuid.UUID
	TemplateVersion *int
	// TemplateDrifted is true when the SKK's permission columns have been
	// edited directly (via UpdateScopedSigningKey) since the last template
	// bump. Surfaces in the UI as an "edited" badge so operators can decide
	// whether a future bump should overwrite or whether to detach.
	TemplateDrifted bool
	// TrackLatest opts this SKK into TemplateService.UpdateTemplate's
	// auto-propagation: when a new template_versions row is created, every
	// SKK with TrackLatest=true is auto-bumped to the new version and its
	// parent account JWT is re-signed + pushed. Service-layer invariants:
	// only valid when TemplateID != nil AND TemplateDrifted=false. Direct
	// edits via UpdateScopedSigningKey are rejected while TrackLatest=true
	// so the next auto-bump can't silently overwrite an operator's edits.
	TrackLatest bool
	// IsPlainSigner is true for SKKs whose parent account JWT lists the
	// key as a plain string in signing_keys (not a UserScope). NSC
	// imports populate this from the original account JWT shape. For
	// plain signers NIS:
	//   - emits them as plain strings on account-JWT regen, NOT as
	//     UserScope (preserves the perms semantics of any existing
	//     user JWTs we did not mint),
	//   - skips SetScoped(true) when minting new user JWTs signed by
	//     them (NATS treats the user JWT's own perms as authoritative
	//     for plain signers; a SetScoped-zeroed user has subs:0 /
	//     payload:0 = locked-out from the cluster).
	// Default false: NIS-native SKKs are always real UserScopes.
	IsPlainSigner bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
