package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// OperatorBackupListFilter filters the keyset-paginated ListPage.
type OperatorBackupListFilter struct {
	Limit        int
	Cursor       string
	OperatorID   *uuid.UUID
	TriggerKind  string // "scheduled" | "manual"; "" disables
	CreatedSince *time.Time
	CreatedUntil *time.Time
}

// OperatorBackupRepository persists rows in the operator_backups table.
// The S3 object bytes are out-of-band; this repo only tracks metadata.
type OperatorBackupRepository interface {
	Create(ctx context.Context, b *entities.OperatorBackup) error
	Get(ctx context.Context, id uuid.UUID) (*entities.OperatorBackup, error)
	ListByOperator(ctx context.Context, operatorID uuid.UUID) ([]*entities.OperatorBackup, error)
	// ListPage returns one keyset-paginated page of backups visible under scope.
	// Order: (created_at DESC, id DESC).
	ListPage(ctx context.Context, scope authz.Scope, filter OperatorBackupListFilter) ([]*entities.OperatorBackup, string, error)
	Delete(ctx context.Context, id uuid.UUID) error

	// ListPrunable returns rows beyond the retention count for one operator,
	// oldest-first. Returns empty when retentionCount <= 0 or fewer rows
	// than retention exist. The sort uses (created_at DESC, id DESC) so
	// rows landing in the same SQLite-1-second granularity tie-break
	// deterministically.
	ListPrunable(ctx context.Context, operatorID uuid.UUID, retentionCount int) ([]*entities.OperatorBackup, error)
}
