package entities

import (
	"time"

	"github.com/google/uuid"
)

// Template is an operator-scoped, versioned bundle of NATS subject
// permissions. SSKs are created or bumped from a template; the SSK row
// remains the authority at JWT-sign time (permissions are snapshotted),
// but the template ref + version lets the UI surface drift and enables
// bulk roll-out via explicit bumps. Reserved names: "default", "system".
type Template struct {
	ID            uuid.UUID
	OperatorID    uuid.UUID
	Name          string
	Description   string
	LatestVersion int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TemplateVersion is an immutable snapshot of a template's permission set
// at a point in time. Updates to a template create a new version row and
// bump latest_version; old versions are retained so an SSK pinned to v3
// stays valid even after v4 ships. CreatedByUserID is nullable so user
// deletion doesn't break the audit trail.
type TemplateVersion struct {
	ID              uuid.UUID
	TemplateID      uuid.UUID
	VersionNumber   int
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs int
	ResponseTTL     time.Duration
	ChangeNote      string
	CreatedAt       time.Time
	CreatedByUserID *uuid.UUID
}
