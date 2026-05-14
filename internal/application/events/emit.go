// Package events provides the durable event substrate for NIS.
//
// Services emit events by calling EmitTx (inside an existing WithTx) or
// EmitSystem (from background tasks without a request context). Each Emit
// inserts an append-only row into the events table AND fans out matching
// webhook deliveries — both inside the same transaction — so the event log
// is consistent with the state change that produced it.
//
// Side-effect-bearing async work (HTTP POSTs to webhook receivers) runs in a
// separate worker; see internal/application/services/webhook_delivery_worker.go.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/authctx"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// Event is the input form used by callers — typed to make construction
// ergonomic. It is converted to entities.Event inside Emit. Fields ID and
// OccurredAt are filled in by Emit if unset.
type Event struct {
	Type         string
	OperatorID   *uuid.UUID
	AccountID    *uuid.UUID
	ResourceType string
	ResourceID   string
	Payload      any // marshalled to JSON; pass nil for no payload
}

// EmitTx writes the event AND fans out matching webhook deliveries inside the
// caller's transaction. Use this from service methods that already run inside
// factory.WithTx — the event row commits with the state change.
//
// Actor is derived from the request context (middleware.GetUserFromContext).
// If no user is present, the actor is "system" with NULL actor_id.
//
// Returns an error only if the event row or any delivery row fails to insert;
// the caller's tx must surface this and roll back. NEVER swallow the error.
func EmitTx(ctx context.Context, tx persistence.RepositoryFactory, in Event) error {
	return emit(ctx, tx, in, actorFromContext(ctx))
}

// EmitSystem is the variant for background tasks that have no request context
// (e.g. the 60s cluster health-check loop). It opens its own short transaction
// and emits with ActorType=system, ActorID=nil. Even if ctx happens to carry
// an authed user, the actor is forced to "system" — these are not user-driven.
//
// Callers MUST invoke EmitSystem AFTER any side-effect (NATS publish, HTTP)
// has completed, never before — by design EmitSystem opens a fresh tx so
// failure to emit cannot roll back the side-effect.
func EmitSystem(ctx context.Context, factory persistence.RepositoryFactory, in Event) error {
	actor := actorRef{Type: entities.ActorTypeSystem}
	return factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return emit(ctx, tx, in, actor)
	})
}

type actorRef struct {
	Type entities.ActorType
	ID   *uuid.UUID
}

func actorFromContext(ctx context.Context) actorRef {
	user, ok := authctx.GetUser(ctx)
	if !ok || user == nil {
		return actorRef{Type: entities.ActorTypeSystem}
	}
	id := user.ID
	return actorRef{Type: entities.ActorTypeUser, ID: &id}
}

func emit(ctx context.Context, tx persistence.RepositoryFactory, in Event, actor actorRef) error {
	if in.Type == "" {
		return fmt.Errorf("event type is required")
	}
	if in.ResourceType == "" || in.ResourceID == "" {
		return fmt.Errorf("event resource_type and resource_id are required")
	}

	var payloadRaw json.RawMessage
	if in.Payload != nil {
		b, err := json.Marshal(in.Payload)
		if err != nil {
			return fmt.Errorf("marshal event payload: %w", err)
		}
		payloadRaw = b
	}

	evt := &entities.Event{
		ID:           uuid.New(),
		OccurredAt:   time.Now().UTC(),
		Type:         in.Type,
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		OperatorID:   in.OperatorID,
		AccountID:    in.AccountID,
		ResourceType: in.ResourceType,
		ResourceID:   in.ResourceID,
		Payload:      payloadRaw,
	}

	if err := tx.EventRepository().Create(ctx, evt); err != nil {
		return fmt.Errorf("persist event: %w", err)
	}

	return fanoutDeliveries(ctx, tx, evt)
}

// fanoutDeliveries creates one webhook_deliveries row per matching subscription.
// Subscriptions are scoped to the operator on the event; events without an
// operator_id currently don't fan out.
func fanoutDeliveries(ctx context.Context, tx persistence.RepositoryFactory, evt *entities.Event) error {
	if evt.OperatorID == nil {
		return nil
	}
	subs, err := tx.WebhookSubscriptionRepository().ListEnabledForEvent(ctx, evt.OperatorID, evt.Type)
	if err != nil {
		return fmt.Errorf("list webhook subscriptions for event: %w", err)
	}
	now := time.Now().UTC()
	for _, sub := range subs {
		delivery := &entities.WebhookDelivery{
			ID:            uuid.New(),
			SubscriptionID: sub.ID,
			EventID:        evt.ID,
			Status:         entities.DeliveryStatusPending,
			NextAttemptAt:  now,
		}
		if err := tx.WebhookDeliveryRepository().Create(ctx, delivery); err != nil {
			return fmt.Errorf("enqueue webhook delivery for subscription %s: %w", sub.ID, err)
		}
	}
	return nil
}
