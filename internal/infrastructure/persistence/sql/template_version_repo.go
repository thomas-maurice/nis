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

// TemplateVersionRepo implements repositories.TemplateVersionRepository.
// Versions are immutable; only Create + Read methods are exposed.
type TemplateVersionRepo struct {
	db *gorm.DB
}

func NewTemplateVersionRepo(db *gorm.DB) *TemplateVersionRepo { return &TemplateVersionRepo{db: db} }

func (r *TemplateVersionRepo) Create(ctx context.Context, v *entities.TemplateVersion) error {
	model := TemplateVersionModelFromEntity(v)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create template version: %w", err)
	}
	return nil
}

func (r *TemplateVersionRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.TemplateVersion, error) {
	var model TemplateVersionModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get template version: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *TemplateVersionRepo) GetByTemplateAndNumber(ctx context.Context, templateID uuid.UUID, version int) (*entities.TemplateVersion, error) {
	var model TemplateVersionModel
	err := r.db.WithContext(ctx).
		First(&model, "template_id = ? AND version_number = ?", templateID.String(), version).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get template version by number: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *TemplateVersionRepo) ListByTemplate(ctx context.Context, templateID uuid.UUID) ([]*entities.TemplateVersion, error) {
	var models []TemplateVersionModel
	err := r.db.WithContext(ctx).
		Where("template_id = ?", templateID.String()).
		Order("version_number DESC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list template versions: %w", err)
	}
	out := make([]*entities.TemplateVersion, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nil
}
