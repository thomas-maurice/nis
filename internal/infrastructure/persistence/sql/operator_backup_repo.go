package sql

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

type OperatorBackupRepo struct {
	db *gorm.DB
}

func NewOperatorBackupRepo(db *gorm.DB) *OperatorBackupRepo {
	return &OperatorBackupRepo{db: db}
}

// ListPage returns one keyset-paginated page of backups visible under scope.
// Order: (created_at DESC, id DESC).
//
// Scope mapping (backups are operator-scoped):
//   - admin/system: no narrowing.
//   - operator-admin: WHERE operator_id = scope_op.
//   - account-admin: resolve owning operator from the scoped account.
//   - zero scope: no rows.
func (r *OperatorBackupRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.OperatorBackupListFilter) ([]*entities.OperatorBackup, string, error) {
	if scope.IsZero() {
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)
	query := r.db.WithContext(ctx)

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOrgAdmin():
		if scope.ScopeOrganizationID == nil {
			return nil, "", nil
		}
		query = query.Where("operator_id IN (SELECT id FROM operators WHERE organization_id = ?)", scope.ScopeOrganizationID.String())
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return nil, "", nil
		}
		query = query.Where("operator_id = ?", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return nil, "", nil
		}
		var acc AccountModel
		if err := r.db.WithContext(ctx).Select("operator_id").First(&acc, "id = ?", scope.ScopeAccountID.String()).Error; err != nil {
			return nil, "", nil
		}
		query = query.Where("operator_id = ?", acc.OperatorID)
	}

	if filter.OperatorID != nil {
		if scope.IsOperatorAdmin() && scope.ScopeOperatorID != nil && *filter.OperatorID != *scope.ScopeOperatorID {
			return nil, "", nil
		}
		query = query.Where("operator_id = ?", filter.OperatorID.String())
	}
	if filter.TriggerKind != "" {
		query = query.Where("trigger_kind = ?", filter.TriggerKind)
	}
	if filter.CreatedSince != nil {
		query = query.Where("created_at >= ?", filter.CreatedSince.UTC())
	}
	if filter.CreatedUntil != nil {
		query = query.Where("created_at < ?", filter.CreatedUntil.UTC())
	}

	if filter.Cursor != "" {
		cursorTime, cursorID, err := DecodeCursor(filter.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", repositories.ErrInvalidCursor, err)
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)",
			cursorTime.UTC(), cursorTime.UTC(), cursorID.String())
	}

	var models []OperatorBackupModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list operator backups page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	out := make([]*entities.OperatorBackup, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nextCursor, nil
}

func (r *OperatorBackupRepo) Create(ctx context.Context, b *entities.OperatorBackup) error {
	m := OperatorBackupModelFromEntity(b)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return fmt.Errorf("OperatorBackup.Create: %w", err)
	}
	return nil
}

func (r *OperatorBackupRepo) Get(ctx context.Context, id uuid.UUID) (*entities.OperatorBackup, error) {
	var m OperatorBackupModel
	if err := r.db.WithContext(ctx).First(&m, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("OperatorBackup.Get: %w", err)
	}
	return m.ToEntity(), nil
}

func (r *OperatorBackupRepo) ListByOperator(ctx context.Context, operatorID uuid.UUID) ([]*entities.OperatorBackup, error) {
	var rows []OperatorBackupModel
	if err := r.db.WithContext(ctx).
		Where("operator_id = ?", operatorID.String()).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("OperatorBackup.ListByOperator: %w", err)
	}
	out := make([]*entities.OperatorBackup, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].ToEntity())
	}
	return out, nil
}

func (r *OperatorBackupRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&OperatorBackupModel{})
	if res.Error != nil {
		return fmt.Errorf("OperatorBackup.Delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *OperatorBackupRepo) ListPrunable(ctx context.Context, operatorID uuid.UUID, retentionCount int) ([]*entities.OperatorBackup, error) {
	if retentionCount <= 0 {
		return nil, nil
	}
	var rows []OperatorBackupModel
	if err := r.db.WithContext(ctx).
		Where("operator_id = ?", operatorID.String()).
		Order("created_at DESC, id DESC").
		Offset(retentionCount).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("OperatorBackup.ListPrunable: %w", err)
	}
	out := make([]*entities.OperatorBackup, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].ToEntity())
	}
	return out, nil
}
