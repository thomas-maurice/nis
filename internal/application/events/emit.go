// Package events provides the durable event substrate for NIS.
//
// Services emit events by calling EmitTx (inside an existing WithTx) or
// EmitSystem (from background tasks without a request context). Each Emit
// inserts an append-only row into the events table AND fans out matching
// webhook deliveries — both inside the same transaction — so the event log
// is consistent with the state change that produced it.
//
// Side-effect-bearing async work (HTTP POSTs to webhook receivers) runs on
// the generic jobs substrate. fanoutDeliveries inserts both the
// webhook_deliveries row AND its companion `webhook.deliver` job inside the
// caller's tx via the JobEnqueueFn callback registered by SetJobEnqueuer.
// Atomic with the row that drives it — a crash between the two is impossible.
// The handler lives in internal/application/services/job_handlers_webhook.go.
package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/authctx"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// JobEnqueueFn is the callback that fanoutDeliveries invokes for each
// freshly-inserted webhook_deliveries row to enqueue its companion
// `webhook.deliver` job in the same tx. Wired at startup by serve.go via
// SetJobEnqueuer — the indirection avoids an events → services import
// cycle (services already imports events for EmitTx). If no enqueuer is
// set (tests, fresh boot before wiring) fanoutDeliveries skips the call
// and the delivery row sits pending; the startup catch-up scan will pick
// it up on next boot.
type JobEnqueueFn func(ctx context.Context, tx persistence.RepositoryFactory, deliveryID uuid.UUID) error

var jobEnqueueFn JobEnqueueFn

// SetJobEnqueuer registers the callback that fanoutDeliveries calls to
// enqueue companion `webhook.deliver` jobs. Idempotent — call once at
// startup. Pass nil to unregister (tests).
func SetJobEnqueuer(fn JobEnqueueFn) { jobEnqueueFn = fn }

// Event is the input form used by callers — typed to make construction
// ergonomic. It is converted to entities.Event inside Emit. Fields ID and
// OccurredAt are filled in by Emit if unset.
type Event struct {
	Type         string
	OperatorID   *uuid.UUID
	AccountID    *uuid.UUID
	ResourceType string
	ResourceID   string
	Payload      any  // marshalled to JSON; pass nil for no payload
	Diff         Diff // P1 field-level before/after diff; nil for non-update events
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

// actorFromContext derives the audit Actor for an event. Resolution order:
//
//  1. Explicit Actor set via authctx.SetActor (token-authed requests).
//  2. APIUser set via authctx.SetUser (ordinary user-authed requests).
//  3. Fall back to ActorTypeSystem (background tasks, no context user).
//
// Step 1 is what keeps the audit log truthful for token-authed traffic: the
// middleware also synthesizes an APIUser so PermissionService still works, but
// we MUST record the token as the actor, not the synthetic user.
func actorFromContext(ctx context.Context) actorRef {
	if actor, ok := authctx.GetActor(ctx); ok && actor.Type != "" {
		return actorRef{Type: actor.Type, ID: actor.ID}
	}
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

	var diffRaw json.RawMessage
	if len(in.Diff) > 0 {
		b, err := json.Marshal(in.Diff)
		if err != nil {
			return fmt.Errorf("marshal event diff: %w", err)
		}
		diffRaw = b
	}

	evt := &entities.Event{
		ID:           uuid.New(),
		OccurredAt:   clock.Now(),
		Type:         in.Type,
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		OperatorID:   in.OperatorID,
		AccountID:    in.AccountID,
		ResourceType: in.ResourceType,
		ResourceID:   in.ResourceID,
		Payload:      payloadRaw,
		Diff:         diffRaw,
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
	now := clock.Now()
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
		if jobEnqueueFn != nil {
			if err := jobEnqueueFn(ctx, tx, delivery.ID); err != nil {
				return fmt.Errorf("enqueue webhook.deliver job for delivery %s: %w", delivery.ID, err)
			}
		}
	}
	return nil
}
