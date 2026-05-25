package sql

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// OperatorAgeRecipientRepo persists per-operator age recipient pubkeys (P15).
type OperatorAgeRecipientRepo struct {
	db *gorm.DB
}

func NewOperatorAgeRecipientRepo(db *gorm.DB) *OperatorAgeRecipientRepo {
	return &OperatorAgeRecipientRepo{db: db}
}

func (r *OperatorAgeRecipientRepo) Create(ctx context.Context, e *entities.OperatorAgeRecipient) error {
	m := OperatorAgeRecipientModelFromEntity(e)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		// Map the per-dialect unique-constraint message into the shared
		// ErrAlreadyExists sentinel so the handler maps it to
		// CodeAlreadyExists rather than CodeInternal. The match strings
		// cover SQLite ("UNIQUE constraint failed") and Postgres
		// ("duplicate key value violates unique constraint").
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "unique constraint") || strings.Contains(low, "duplicate key") {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("OperatorAgeRecipient.Create: %w", err)
	}
	return nil
}

func (r *OperatorAgeRecipientRepo) Get(ctx context.Context, id uuid.UUID) (*entities.OperatorAgeRecipient, error) {
	var m OperatorAgeRecipientModel
	if err := r.db.WithContext(ctx).First(&m, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("OperatorAgeRecipient.Get: %w", err)
	}
	return m.ToEntity(), nil
}

func (r *OperatorAgeRecipientRepo) ListByOperator(ctx context.Context, operatorID uuid.UUID) ([]*entities.OperatorAgeRecipient, error) {
	var rows []OperatorAgeRecipientModel
	if err := r.db.WithContext(ctx).
		Where("operator_id = ?", operatorID.String()).
		Order("created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("OperatorAgeRecipient.ListByOperator: %w", err)
	}
	out := make([]*entities.OperatorAgeRecipient, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].ToEntity())
	}
	return out, nil
}

func (r *OperatorAgeRecipientRepo) CountByOperator(ctx context.Context, operatorID uuid.UUID) (int, error) {
	var n int64
	if err := r.db.WithContext(ctx).
		Model(&OperatorAgeRecipientModel{}).
		Where("operator_id = ?", operatorID.String()).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("OperatorAgeRecipient.CountByOperator: %w", err)
	}
	return int(n), nil
}

func (r *OperatorAgeRecipientRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&OperatorAgeRecipientModel{})
	if res.Error != nil {
		return fmt.Errorf("OperatorAgeRecipient.Delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}
