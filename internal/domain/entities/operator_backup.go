package entities

import (
	"time"

	"github.com/google/uuid"
)

// BackupTriggerKind labels what initiated a backup. The actor identity
// (user / api-token) lives in the corresponding event payload — keeping
// the row schema-clean and queryable on this one enum.
type BackupTriggerKind string

const (
	BackupTriggerScheduled BackupTriggerKind = "scheduled"
	BackupTriggerManual    BackupTriggerKind = "manual"
)

// OperatorBackup is a single uploaded backup artifact for one operator.
// The actual bytes live in S3 at ObjectKey; this row carries the
// metadata + integrity hash + provenance.
type OperatorBackup struct {
	ID          uuid.UUID
	OperatorID  uuid.UUID
	ObjectKey   string
	SizeBytes   int64
	Sha256      string
	TriggerKind BackupTriggerKind
	CreatedAt   time.Time
}
