package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// WebhookSubscriptionFilter filters ListSubscriptions (legacy).
type WebhookSubscriptionFilter struct {
	OperatorID *uuid.UUID // nil = all operators (admin only)
	Limit      int
	Offset     int
}

// WebhookSubscriptionListFilter filters the new keyset-paginated ListPage.
type WebhookSubscriptionListFilter struct {
	Limit          int
	Cursor         string
	OperatorID     *uuid.UUID
	Enabled        *bool
	EventTypeMatch string // case-insensitive substring against the serialised event_types JSON
	CreatedSince   *time.Time
	CreatedUntil   *time.Time
}

// WebhookDeliveryFilter filters ListDeliveries (legacy).
type WebhookDeliveryFilter struct {
	SubscriptionID *uuid.UUID
	Status         *entities.DeliveryStatus
	Limit          int
	Offset         int
}

// WebhookDeliveryListFilter filters the new keyset-paginated ListPage.
//
// Order: (scheduled_at DESC, id DESC) — matches existing UI expectations and
// the runner's claim order. NOT created_at.
type WebhookDeliveryListFilter struct {
	Limit            int
	Cursor           string
	SubscriptionID   *uuid.UUID
	Status           *entities.DeliveryStatus
	AttemptedSince   *time.Time
	AttemptedUntil   *time.Time
}

type WebhookSubscriptionRepository interface {
	Create(ctx context.Context, sub *entities.WebhookSubscription) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WebhookSubscription, error)
	GetByOperatorAndName(ctx context.Context, operatorID uuid.UUID, name string) (*entities.WebhookSubscription, error)
	List(ctx context.Context, filter WebhookSubscriptionFilter) ([]*entities.WebhookSubscription, error)
	// ListPage returns one keyset-paginated page of subscriptions visible
	// under scope. Order: (created_at DESC, id DESC).
	ListPage(ctx context.Context, scope authz.Scope, filter WebhookSubscriptionListFilter) ([]*entities.WebhookSubscription, string, error)
	// ListEnabledForEvent returns enabled subscriptions matching the given
	// event type for the operator. INTERNAL FAN-OUT — capped to 1000 rows
	// with a warning log when the cap is hit; not API-paginated. If
	// operatorID is nil, matches across all operators (admin fanout path).
	ListEnabledForEvent(ctx context.Context, operatorID *uuid.UUID, eventType string) ([]*entities.WebhookSubscription, error)
	Update(ctx context.Context, sub *entities.WebhookSubscription) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type WebhookDeliveryRepository interface {
	Create(ctx context.Context, d *entities.WebhookDelivery) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WebhookDelivery, error)
	List(ctx context.Context, filter WebhookDeliveryFilter) ([]*entities.WebhookDelivery, error)
	// ListPage returns one keyset-paginated page of deliveries visible under
	// scope. Order: (scheduled_at DESC, id DESC).
	ListPage(ctx context.Context, scope authz.Scope, filter WebhookDeliveryListFilter) ([]*entities.WebhookDelivery, string, error)
	Update(ctx context.Context, d *entities.WebhookDelivery) error
	// DeleteSucceededOlderThan removes succeeded deliveries with completed_at < cutoff.
	DeleteSucceededOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	CountByStatus(ctx context.Context, status entities.DeliveryStatus) (int64, error)
}
