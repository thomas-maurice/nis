package sql

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
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
