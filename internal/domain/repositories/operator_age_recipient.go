package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// OperatorAgeRecipientRepository persists per-operator age recipient pubkeys
// (P15). One row per (operator, recipient). The unique constraint on
// (operator_id, public_key) is enforced at the schema level so a double-add
// surfaces as ErrAlreadyExists, not as a silently-duplicated row.
//
// There is no ListPage / scope-aware list method because recipients are
// intentionally not visible in the global search surface (see SearchService
// header for the rationale alongside webhooks / events / api_tokens / api_users).
// The per-operator List below is gated by PermissionService.CanReadBackup at
// the handler boundary.
type OperatorAgeRecipientRepository interface {
	Create(ctx context.Context, r *entities.OperatorAgeRecipient) error
	Get(ctx context.Context, id uuid.UUID) (*entities.OperatorAgeRecipient, error)
	ListByOperator(ctx context.Context, operatorID uuid.UUID) ([]*entities.OperatorAgeRecipient, error)
	CountByOperator(ctx context.Context, operatorID uuid.UUID) (int, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
