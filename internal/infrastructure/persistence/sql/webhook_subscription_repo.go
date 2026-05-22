package sql

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

// webhookFanoutCap is the hard ceiling on the number of subscriptions returned
// by ListEnabledForEvent. The path is called per emitted event and is not
// user-paginated; without the cap a runaway fan-out would silently grow over
// time. See SKILL §15 ("internal fan-out needing a cap").
const webhookFanoutCap = 1000

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
	// Hard cap + warn on hit: this is the canonical "internal fan-out needing
	// a cap" — called per emitted event, never user-paginated. Order by
	// created_at so the same N subscribers are picked deterministically when
	// the cap fires.
	if err := query.Order("created_at ASC").Limit(webhookFanoutCap + 1).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list enabled webhook subscriptions for event: %w", err)
	}
	if len(models) > webhookFanoutCap {
		opLabel := "<all>"
		if operatorID != nil {
			opLabel = operatorID.String()
		}
		slog.Warn("webhook fanout cap hit; subscribers past the cap will NOT be notified for this event",
			"event_type", eventType, "operator_id", opLabel, "cap", webhookFanoutCap)
		models = models[:webhookFanoutCap]
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

// ListPage returns one keyset-paginated page of subscriptions visible under
// scope. Order: (created_at DESC, id DESC).
//
// Scope mapping (same shape as templates — operator-scoped resource):
//   - admin/system: no narrowing.
//   - operator-admin: WHERE operator_id = scope.ScopeOperatorID.
//   - account-admin: resolve the owning operator from the scoped account;
//     subscriptions are operator-wide, account-admin sees only the parent op's.
//   - zero scope: no rows.
func (r *WebhookSubscriptionRepo) ListPage(ctx context.Context, scope authz.Scope, filter repositories.WebhookSubscriptionListFilter) ([]*entities.WebhookSubscription, string, error) {
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
	if filter.Enabled != nil {
		query = query.Where("enabled = ?", *filter.Enabled)
	}
	if filter.EventTypeMatch != "" {
		// event_types is a JSON-serialised []string column. Substring match
		// against the raw serialised bytes is good enough for filtering —
		// MatchesEventType isn't applied here because the filter is operator
		// convenience, not a fanout-correctness path.
		pat := "%" + escapeLikeParam(strings.TrimSpace(filter.EventTypeMatch)) + "%"
		query = query.Where("LOWER(event_types) LIKE LOWER(?) ESCAPE '\\'", pat)
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

	var models []WebhookSubscriptionModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, "", fmt.Errorf("failed to list webhook subscriptions page: %w", err)
	}

	var nextCursor string
	if len(models) > limit {
		last := models[limit-1]
		id, _ := uuid.Parse(last.ID)
		nextCursor = EncodeCursor(last.CreatedAt, id)
		models = models[:limit]
	}

	out := make([]*entities.WebhookSubscription, len(models))
	for i, m := range models {
		out[i] = m.ToEntity()
	}
	return out, nextCursor, nil
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
