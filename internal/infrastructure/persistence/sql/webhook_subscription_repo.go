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

func isWebhookDuplicate(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// WebhookSubscriptionRepo implements repositories.WebhookSubscriptionRepository using GORM
type WebhookSubscriptionRepo struct {
	db *gorm.DB
}

func NewWebhookSubscriptionRepo(db *gorm.DB) *WebhookSubscriptionRepo {
	return &WebhookSubscriptionRepo{db: db}
}

func (r *WebhookSubscriptionRepo) Create(ctx context.Context, sub *entities.WebhookSubscription) error {
	model := WebhookSubscriptionModelFromEntity(sub)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if isWebhookDuplicate(err) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create webhook subscription: %w", err)
	}
	return nil
}

func (r *WebhookSubscriptionRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.WebhookSubscription, error) {
	var model WebhookSubscriptionModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get webhook subscription: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *WebhookSubscriptionRepo) GetByOperatorAndName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.WebhookSubscription, error) {
	var model WebhookSubscriptionModel
	err := r.db.WithContext(ctx).First(&model, "operator_id = ? AND name = ?", operatorID.String(), name).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get webhook subscription by operator and name: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *WebhookSubscriptionRepo) List(ctx context.Context, filter repositories.WebhookSubscriptionFilter) ([]*entities.WebhookSubscription, error) {
	var models []WebhookSubscriptionModel
	query := r.db.WithContext(ctx)
	if filter.OperatorID != nil {
		query = query.Where("operator_id = ?", filter.OperatorID.String())
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}
	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list webhook subscriptions: %w", err)
	}
	subs := make([]*entities.WebhookSubscription, len(models))
	for i, m := range models {
		subs[i] = m.ToEntity()
	}
	return subs, nil
}

func (r *WebhookSubscriptionRepo) ListEnabledForEvent(ctx context.Context, operatorID *uuid.UUID, eventType string) ([]*entities.WebhookSubscription, error) {
	var models []WebhookSubscriptionModel
	query := r.db.WithContext(ctx).Where("enabled = ?", true)
	if operatorID != nil {
		query = query.Where("operator_id = ?", operatorID.String())
	}
	escaped := escapeLikeParam(eventType)
	query = query.Where(
		`(event_types LIKE '%"*"%' OR event_types LIKE ?)`,
		`%"`+escaped+`"%`,
	)
	if err := query.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list enabled webhook subscriptions for event: %w", err)
	}
	// Post-filter using MatchesEventType to handle any LIKE false-positives.
	subs := make([]*entities.WebhookSubscription, 0, len(models))
	for _, m := range models {
		sub := m.ToEntity()
		if sub.MatchesEventType(eventType) {
			subs = append(subs, sub)
		}
	}
	return subs, nil
}

func (r *WebhookSubscriptionRepo) Update(ctx context.Context, sub *entities.WebhookSubscription) error {
	model := WebhookSubscriptionModelFromEntity(sub)
	result := r.db.WithContext(ctx).Model(&WebhookSubscriptionModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)
	if result.Error != nil {
		return fmt.Errorf("failed to update webhook subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *WebhookSubscriptionRepo) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&WebhookSubscriptionModel{}, "id = ?", id.String())
	if result.Error != nil {
		return fmt.Errorf("failed to delete webhook subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}
