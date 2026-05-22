package sql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
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

// ListPage returns one keyset-paginated page of deliveries visible under
// scope. Order: (created_at DESC, id DESC) — matches the existing List path
// and lines up with operator expectations ("show recent deliveries first").
//
// Scope mapping joins deliveries → subscriptions → operators:
//   - admin/system: no narrowing.
//   - operator-admin: WHERE subscription_id IN (SELECT id FROM webhook_subscriptions WHERE operator_id = scope_op).
//   - account-admin: resolve owning operator from scoped account, then same EXISTS form.
//   - zero scope: no rows.
func (r *WebhookDeliveryRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.WebhookDeliveryListFilter) ([]*entities.WebhookDelivery, string, error) {
	if scope.IsZero() {
		return nil, "", nil
	}

	limit := clampListLimit(filter.Limit)
	query := r.db.WithContext(ctx)

	switch {
	case scope.IsAdmin():
		// no narrowing
	case scope.IsOperatorAdmin():
		if scope.ScopeOperatorID == nil {
			return nil, "", nil
		}
		query = query.Where("subscription_id IN (SELECT id FROM webhook_subscriptions WHERE operator_id = ?)", scope.ScopeOperatorID.String())
	case scope.IsAccountAdmin():
		if scope.ScopeAccountID == nil {
			return nil, "", nil
		}
		var acc AccountModel
		if err := r.db.WithContext(ctx).Select("operator_id").First(&acc, "id = ?", scope.ScopeAccountID.String()).Error; err != nil {
			return nil, "", nil
		}
		query = query.Where("subscription_id IN (SELECT id FROM webhook_subscriptions WHERE operator_id = ?)", acc.OperatorID)
	}

	if filter.SubscriptionID != nil {
		query = query.Where("subscription_id = ?", filter.SubscriptionID.String())
	}
	if filter.Status != nil {
		query = query.Where("status = ?", string(*filter.Status))
	}
	if filter.AttemptedSince != nil {
		query = query.Where("next_attempt_at >= ?", filter.AttemptedSince.UTC())
	}
	if filter.AttemptedUntil != nil {
		query = query.Where("next_attempt_at < ?", filter.AttemptedUntil.UTC())
	}

	if filter.Cursor != "" {
		cursorTime, cursorID, err := DecodeCursor(filter.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", repositories.ErrInvalidCursor, err)
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)",
			cursorTime.UTC(), cursorTime.UTC(), cursorID.String())
	}

	var models []WebhookDeliveryModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list webhook deliveries page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	out := make([]*entities.WebhookDelivery, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nextCursor, nil
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
