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
	// EventTypeScopedKeyRotated is emitted by ScopedSigningKeyService.RotateScopedSigningKey
	// after the rotation tx commits. Payload carries old/new public keys,
	// the count of affected users, and the list of user public keys whose
	// old JWTs were added to the parent account's revocation map. Per-user
	// user.revoked events are also emitted (with payload.triggered_by =
	// "ssk_rotation") so webhook subscribers filtering on user.revoked see
	// rotation-induced revocations without resubscribing.
	EventTypeScopedKeyRotated = "scoped_key.rotated"
	EventTypeClusterCreated       = "cluster.created"
	EventTypeClusterUpdated       = "cluster.updated"
	EventTypeClusterDeleted       = "cluster.deleted"
	EventTypeClusterSynced        = "cluster.synced"
	EventTypeClusterSyncFailed    = "cluster.sync_failed"
	EventTypeClusterHealthChanged = "cluster.health_changed"
	// EventTypeClusterAccountSynced is emitted when an account JWT is
	// pushed to a NATS resolver. Payload carries cluster_id, cluster_name,
	// account_id, account_name, account_public_key, jwt_iat, and a
	// trigger field ("auto" for substrate-driven A13-full pushes, "manual"
	// for operator-initiated P9 reconciles). Used by webhook subscribers
	// who want to follow "did my change land on this cluster?".
	EventTypeClusterAccountSynced = "cluster.account.synced"
	// EventTypeClusterAccountDeletedFromResolver is emitted when an
	// account's resolver entry is removed via $SYS.REQ.CLAIMS.DELETE.
	// Payload carries cluster_id, cluster_name, account_pubkey. Fires
	// from the substrate-driven cluster.account.delete handler after a
	// successful delete-claim round-trip; failures live in the job row's
	// last_error and surface via JobsView + the drift dashboard.
	EventTypeClusterAccountDeletedFromResolver = "cluster.account.deleted_from_resolver"
	EventTypeWebhookTest          = "webhook.test"
	EventTypeAPITokenCreated      = "api_token.created"
	EventTypeAPITokenRevoked      = "api_token.revoked"
	EventTypeTemplateCreated      = "template.created"
	EventTypeTemplateUpdated      = "template.updated"
	EventTypeTemplateDeleted      = "template.deleted"
	// EventTypeTemplateApplied is emitted whenever an SSK's template
	// version is set or changed — initial create-from-template, explicit
	// bump, or detach (with payload.action="detach"). Distinct from
	// template.updated (which is template-side) so consumers can filter to
	// "permission rollouts that actually landed on an SSK".
	EventTypeTemplateApplied = "template.applied_to_scoped_key"
	// Job lifecycle (A2). Emission is gated by HandlerSpec.AuditPolicy
	// in JobRunner — recurring sweeps default to AuditFailuresOnly to
	// avoid flooding the events table.
	EventTypeJobEnqueued     = "job.enqueued"
	EventTypeJobStarted      = "job.started"
	EventTypeJobSucceeded    = "job.succeeded"
	EventTypeJobFailed       = "job.failed"
	EventTypeJobDeadLettered = "job.dead_lettered"
	EventTypeJobCancelled    = "job.cancelled"
	EventTypeJobRetried      = "job.retried"
	// Backups (P12). Lifecycle + scheduled run audit. The substrate-level
	// job.* events (job.started/succeeded/failed) are suppressed for the
	// backup.execute handler (AuditFailuresOnly) so a daily run of N
	// operators doesn't triple-flood the audit log; these semantic events
	// are emitted by BackupService directly and carry the operator/object
	// context.
	EventTypeOperatorBackupEnabled   = "operator.backup.enabled"
	EventTypeOperatorBackupDisabled  = "operator.backup.disabled"
	EventTypeOperatorBackupSucceeded = "operator.backup.succeeded"
	EventTypeOperatorBackupFailed    = "operator.backup.failed"
	EventTypeOperatorBackupDeleted   = "operator.backup.deleted"
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
