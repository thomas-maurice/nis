package sql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// WebhookDeliveryRepo implements repositories.WebhookDeliveryRepository using GORM
type WebhookDeliveryRepo struct {
	db *gorm.DB
}

func NewWebhookDeliveryRepo(db *gorm.DB) *WebhookDeliveryRepo {
	return &WebhookDeliveryRepo{db: db}
}

func (r *WebhookDeliveryRepo) Create(ctx context.Context, d *entities.WebhookDelivery) error {
	model := WebhookDeliveryModelFromEntity(d)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create webhook delivery: %w", err)
	}
	return nil
}

func (r *WebhookDeliveryRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.WebhookDelivery, error) {
	var model WebhookDeliveryModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get webhook delivery: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *WebhookDeliveryRepo) List(ctx context.Context, filter repositories.WebhookDeliveryFilter) ([]*entities.WebhookDelivery, error) {
	var models []WebhookDeliveryModel
	query := r.db.WithContext(ctx)
	if filter.SubscriptionID != nil {
		query = query.Where("subscription_id = ?", filter.SubscriptionID.String())
	}
	if filter.Status != nil {
		query = query.Where("status = ?", string(*filter.Status))
	}
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}
	if err := query.Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list webhook deliveries: %w", err)
	}
	deliveries := make([]*entities.WebhookDelivery, len(models))
	for i, m := range models {
		deliveries[i] = m.ToEntity()
	}
	return deliveries, nil
}

func (r *WebhookDeliveryRepo) ClaimDue(ctx context.Context, now time.Time, limit int) ([]*entities.WebhookDelivery, error) {
	var models []WebhookDeliveryModel
	err := r.db.WithContext(ctx).
		Where("status = ? AND next_attempt_at <= ?", string(entities.DeliveryStatusPending), now).
		Order("next_attempt_at ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("failed to claim due deliveries: %w", err)
	}
	deliveries := make([]*entities.WebhookDelivery, len(models))
	for i, m := range models {
		deliveries[i] = m.ToEntity()
	}
	return deliveries, nil
}

func (r *WebhookDeliveryRepo) Update(ctx context.Context, d *entities.WebhookDelivery) error {
	model := WebhookDeliveryModelFromEntity(d)
	result := r.db.WithContext(ctx).Model(&WebhookDeliveryModel{}).
		Where("id = ?", model.ID).
		Select("*").Omit("CreatedAt").Updates(model)
	if result.Error != nil {
		return fmt.Errorf("failed to update webhook delivery: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return repositories.ErrNotFound
	}
	return nil
}

func (r *WebhookDeliveryRepo) DeleteSucceededOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	result := r.db.WithContext(ctx).
		Delete(&WebhookDeliveryModel{}, "status = ? AND completed_at < ?", string(entities.DeliveryStatusSucceeded), cutoff)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete old succeeded deliveries: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (r *WebhookDeliveryRepo) CountByStatus(ctx context.Context, status entities.DeliveryStatus) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&WebhookDeliveryModel{}).
		Where("status = ?", string(status)).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count webhook deliveries by status: %w", err)
	}
	return count, nil
}
