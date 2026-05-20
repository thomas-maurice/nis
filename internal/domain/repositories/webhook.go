package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// WebhookSubscriptionFilter filters ListSubscriptions.
type WebhookSubscriptionFilter struct {
	OperatorID *uuid.UUID // nil = all operators (admin only)
	Limit      int
	Offset     int
}

// WebhookDeliveryFilter filters ListDeliveries.
type WebhookDeliveryFilter struct {
	SubscriptionID *uuid.UUID
	Status         *entities.DeliveryStatus
	Limit          int
	Offset         int
}

type WebhookSubscriptionRepository interface {
	Create(ctx context.Context, sub *entities.WebhookSubscription) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WebhookSubscription, error)
	GetByOperatorAndName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.WebhookSubscription, error)
	List(ctx context.Context, filter WebhookSubscriptionFilter) ([]*entities.WebhookSubscription, error)
	// ListEnabledForEvent returns enabled subscriptions matching the given event type for the operator.
	// If operatorID is nil, matches across all operators (admin fanout path).
	ListEnabledForEvent(ctx context.Context, operatorID *uuid.UUID, eventType string) ([]*entities.WebhookSubscription, error)
	Update(ctx context.Context, sub *entities.WebhookSubscription) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type WebhookDeliveryRepository interface {
	Create(ctx context.Context, d *entities.WebhookDelivery) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WebhookDelivery, error)
	List(ctx context.Context, filter WebhookDeliveryFilter) ([]*entities.WebhookDelivery, error)
	Update(ctx context.Context, d *entities.WebhookDelivery) error
	// DeleteSucceededOlderThan removes succeeded deliveries with completed_at < cutoff.
	DeleteSucceededOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	CountByStatus(ctx context.Context, status entities.DeliveryStatus) (int64, error)
}
