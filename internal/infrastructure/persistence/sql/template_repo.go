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

// TemplateRepo implements repositories.TemplateRepository using GORM.
type TemplateRepo struct {
	db *gorm.DB
}

func NewTemplateRepo(db *gorm.DB) *TemplateRepo { return &TemplateRepo{db: db} }

func (r *TemplateRepo) Create(ctx context.Context, t *entities.Template) error {
	model := TemplateModelFromEntity(t)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create template: %w", err)
	}
	return nil
}

func (r *TemplateRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Template, error) {
	var model TemplateModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get template: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *TemplateRepo) GetByName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.Template, error) {
	var model TemplateModel
	err := r.db.WithContext(ctx).First(&model, "operator_id = ? AND name = ?", operatorID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get template by name: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *TemplateRepo) ListByOperator(ctx context.Context, operatorID uuid.UUID, opts repositories.ListOptions) ([]*entities.Template, error) {
	var models []TemplateModel
	q := r.db.WithContext(ctx).Where("operator_id = ?", operatorID.String())
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.Order("name ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list templates by operator: %w", err)
	}
	out := make([]*entities.Template, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

func (r *TemplateRepo) List(ctx context.Context, opts repositories.ListOptions) ([]*entities.Template, error) {
	var models []TemplateModel
	q := r.db.WithContext(ctx)
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	out := make([]*entities.Template, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

func (r *TemplateRepo) Update(ctx context.Context, t *entities.Template) error {
	model := TemplateModelFromEntity(t)
	result := r.db.WithContext(ctx).Model(&TemplateModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)
	if result.Error != nil {
		return fmt.Errorf("failed to update template: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *TemplateRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&TemplateModel{}, "id = ?", id.String())
	if result.Error != nil {
		return fmt.Errorf("failed to delete template: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *TemplateRepo) CountDependentScopedKeys(ctx context.Context, templateID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&ScopedSigningKeyModel{}).
		Where("template_id = ?", templateID.String()).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count dependent scoped keys: %w", err)
	}
	return n, nil
}

func (r *TemplateRepo) ListDependentScopedKeys(ctx context.Context, templateID uuid.UUID) ([]*entities.ScopedSigningKey, error) {
	var models []ScopedSigningKeyModel
	err := r.db.WithContext(ctx).
		Where("template_id = ?", templateID.String()).
		Order("name ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list dependent scoped keys: %w", err)
	}
	out := make([]*entities.ScopedSigningKey, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}

// ListTrackingScopedKeys returns SSKs pinned to this template AND
// flagged track_latest=true. Ordered by (account_id, name) so the
// TemplateService.UpdateTemplate fan-out can batch per-account JWT
// regens without resorting.
func (r *TemplateRepo) ListTrackingScopedKeys(ctx context.Context, templateID uuid.UUID) ([]*entities.ScopedSigningKey, error) {
	var models []ScopedSigningKeyModel
	err := r.db.WithContext(ctx).
		Where("template_id = ? AND track_latest = ?", templateID.String(), true).
		Order("account_id ASC, name ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list tracking scoped keys: %w", err)
	}
	out := make([]*entities.ScopedSigningKey, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}
