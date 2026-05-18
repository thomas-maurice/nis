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
	EventTypeScopedKeyCreated     = "scoped_key.created"
	EventTypeScopedKeyUpdated     = "scoped_key.updated"
	EventTypeScopedKeyDeleted     = "scoped_key.deleted"
	EventTypeClusterCreated       = "cluster.created"
	EventTypeClusterUpdated       = "cluster.updated"
	EventTypeClusterDeleted       = "cluster.deleted"
	EventTypeClusterSynced        = "cluster.synced"
	EventTypeClusterSyncFailed    = "cluster.sync_failed"
	EventTypeClusterHealthChanged = "cluster.health_changed"
	EventTypeWebhookTest          = "webhook.test"
	EventTypeAPITokenCreated      = "api_token.created"
	EventTypeAPITokenRevoked      = "api_token.revoked"
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
