package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ActorType string

const (
	ActorTypeUser     ActorType = "user"
	ActorTypeSystem   ActorType = "system"
	ActorTypeAPIToken ActorType = "api_token"
)

// Event type constants. Add more here as new event types are emitted.
const (
	EventTypeOperatorCreated      = "operator.created"
	EventTypeOperatorUpdated      = "operator.updated"
	EventTypeOperatorDeleted      = "operator.deleted"
	EventTypeAccountCreated       = "account.created"
	EventTypeAccountUpdated       = "account.updated"
	EventTypeAccountDeleted       = "account.deleted"
	EventTypeUserCreated          = "user.created"
	EventTypeUserUpdated          = "user.updated"
	EventTypeUserDeleted          = "user.deleted"
	EventTypeUserRevoked          = "user.revoked"
	EventTypeUserCredExpiringSoon = "user.cred.expiring_soon"
	EventTypeUserCredExpired      = "user.cred.expired"
	EventTypeUserCredRenewed      = "user.cred.renewed"
	EventTypeUserRevocationPruned = "user.revocation_pruned"
	EventTypeScopedKeyCreated     = "scoped_key.created"
	EventTypeScopedKeyUpdated     = "scoped_key.updated"
	EventTypeScopedKeyDeleted     = "scoped_key.deleted"
	EventTypeClusterCreated       = "cluster.created"
	EventTypeClusterUpdated       = "cluster.updated"
	EventTypeClusterDeleted       = "cluster.deleted"
	EventTypeClusterSynced        = "cluster.synced"
	EventTypeClusterSyncFailed    = "cluster.sync_failed"
	EventTypeClusterHealthChanged = "cluster.health_changed"
	EventTypeClusterAccountSynced = "cluster.account.synced"
	EventTypeWebhookTest          = "webhook.test"
	EventTypeAPITokenCreated      = "api_token.created"
	EventTypeAPITokenRevoked      = "api_token.revoked"
	EventTypeTemplateCreated      = "template.created"
	EventTypeTemplateUpdated      = "template.updated"
	EventTypeTemplateDeleted      = "template.deleted"
	// EventTypeTemplateApplied is emitted whenever an SKK's template
	// version is set or changed — initial create-from-template, explicit
	// bump, or detach (with payload.action="detach"). Distinct from
	// template.updated (which is template-side) so consumers can filter to
	// "permission rollouts that actually landed on an SKK".
	EventTypeTemplateApplied = "template.applied_to_scoped_key"
)

// Event is a single audit-log entry. Stored append-only in the events table.
type Event struct {
	ID           uuid.UUID
	OccurredAt   time.Time
	Type         string
	ActorType    ActorType
	ActorID      *uuid.UUID      // nil when ActorType == ActorTypeSystem
	OperatorID   *uuid.UUID      // nullable
	AccountID    *uuid.UUID      // nullable
	ResourceType string
	ResourceID   string
	Payload      json.RawMessage // nullable
}
