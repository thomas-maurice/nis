package sql

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"gorm.io/gorm"
)

const (
	eventDefaultLimit = 100
	eventMaxLimit     = 1000
)

// eventCursor is the opaque pagination token for event list queries.
type eventCursor struct {
	OccurredAt time.Time `json:"occurred_at"`
	ID         string    `json:"id"`
}

func encodeEventCursor(c eventCursor) string {
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeEventCursor(s string) (eventCursor, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return eventCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	var c eventCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return eventCursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	return c, nil
}

// EventRepo implements repositories.EventRepository using GORM
type EventRepo struct {
	db *gorm.DB
}

func NewEventRepo(db *gorm.DB) *EventRepo {
	return &EventRepo{db: db}
}

func (r *EventRepo) Create(ctx context.Context, e *entities.Event) error {
	model := EventModelFromEntity(e)
	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return repositories.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create event: %w", err)
	}
	return nil
}

func (r *EventRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Event, error) {
	var model EventModel
	err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repositories.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get event: %w", err)
	}
	return model.ToEntity(), nil
}

func (r *EventRepo) List(ctx context.Context, filter repositories.EventFilter) (*repositories.EventListResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = eventDefaultLimit
	}
	if limit > eventMaxLimit {
		limit = eventMaxLimit
	}

	query := r.db.WithContext(ctx).Model(&EventModel{})

	if len(filter.Types) > 0 {
		query = query.Where("type IN ?", filter.Types)
	}
	if filter.ResourceType != "" {
		query = query.Where("resource_type = ?", filter.ResourceType)
	}
	if filter.ResourceID != "" {
		query = query.Where("resource_id = ?", filter.ResourceID)
	}
	if filter.OperatorID != nil {
		query = query.Where("operator_id = ?", filter.OperatorID.String())
	}
	if filter.AccountID != nil {
		query = query.Where("account_id = ?", filter.AccountID.String())
	}
	if filter.Since != nil {
		query = query.Where("occurred_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		query = query.Where("occurred_at <= ?", *filter.Until)
	}
	if filter.ActorType != "" {
		query = query.Where("actor_type = ?", filter.ActorType)
	}
	if filter.ActorID != nil {
		query = query.Where("actor_id = ?", filter.ActorID.String())
	}
	if q := filter.SearchQ; q != "" {
		// Substring match over type AND resource_id. LOWER() on both sides for
		// dialect-uniform case-insensitivity (Postgres LIKE is case-sensitive;
		// see search_service.go for the same pattern). Payload is intentionally
		// excluded — keeping the surface narrow and indexable. P1.
		pat := "%" + escapeLikeParam(q) + "%"
		query = query.Where("LOWER(type) LIKE LOWER(?) OR LOWER(resource_id) LIKE LOWER(?)", pat, pat)
	}

	if filter.Cursor != "" {
		cur, err := decodeEventCursor(filter.Cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor: %w", err)
		}
		// occurred_at DESC, id DESC — so "after cursor" means:
		// occurred_at < cur.OccurredAt OR (occurred_at = cur.OccurredAt AND id < cur.ID)
		query = query.Where(
			"(occurred_at < ?) OR (occurred_at = ? AND id < ?)",
			cur.OccurredAt, cur.OccurredAt, cur.ID,
		)
	}

	var models []EventModel
	if err := query.Order("occurred_at DESC, id DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}

	result := &repositories.EventListResult{
		Events: make([]*entities.Event, len(models)),
	}
	for i, m := range models {
		result.Events[i] = m.ToEntity()
	}

	if len(models) == limit {
		last := models[len(models)-1]
		result.NextCursor = encodeEventCursor(eventCursor{
			OccurredAt: last.OccurredAt,
			ID:         last.ID,
		})
	}

	return result, nil
}

func (r *EventRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Delete(&EventModel{}, "occurred_at < ?", cutoff)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to delete old events: %w", result.Error)
	}
	return result.RowsAffected, nil
}
