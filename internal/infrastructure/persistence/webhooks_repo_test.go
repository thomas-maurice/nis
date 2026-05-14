package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// helpers

func makeOperatorForWebhook(t *testing.T, factory interface {
	OperatorRepository() repositories.OperatorRepository
}, name string) *entities.Operator {
	t.Helper()
	op := makeOperator(name)
	require.NoError(t, factory.OperatorRepository().Create(context.Background(), op))
	return op
}

func makeSubscription(operatorID uuid.UUID, name string, eventTypes []string, enabled bool) *entities.WebhookSubscription {
	return &entities.WebhookSubscription{
		ID:              uuid.New(),
		OperatorID:      operatorID,
		Name:            name,
		Description:     "test subscription",
		URL:             "https://example.com/hook",
		EncryptedSecret: "enc:key1:abc",
		EventTypes:      eventTypes,
		Enabled:         enabled,
		DisabledReason:  "",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

func makeDelivery(subscriptionID, eventID uuid.UUID, status entities.DeliveryStatus, nextAttemptAt time.Time) *entities.WebhookDelivery {
	return &entities.WebhookDelivery{
		ID:               uuid.New(),
		SubscriptionID:   subscriptionID,
		EventID:          eventID,
		Attempt:          0,
		Status:           status,
		NextAttemptAt:    nextAttemptAt,
		LastError:        "",
		LastResponseCode: 0,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
}

func makeEventForDelivery(t *testing.T, factory interface {
	EventRepository() repositories.EventRepository
}) *entities.Event {
	t.Helper()
	e := makeEvent(entities.EventTypeOperatorCreated, time.Now().UTC())
	require.NoError(t, factory.EventRepository().Create(context.Background(), e))
	return e
}

// subscription tests

func TestSubscriptionRepo_CreateAndGet(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-sub-create")

	sub := makeSubscription(op.ID, "my-hook", []string{"operator.created", "account.created"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	got, err := factory.WebhookSubscriptionRepository().GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, got.ID)
	assert.Equal(t, sub.OperatorID, got.OperatorID)
	assert.Equal(t, sub.Name, got.Name)
	assert.Equal(t, sub.URL, got.URL)
	assert.Equal(t, sub.EncryptedSecret, got.EncryptedSecret)
	assert.Equal(t, sub.EventTypes, got.EventTypes)
	assert.True(t, got.Enabled)
}

func TestSubscriptionRepo_DuplicateNameForOperator_Rejected(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-dup")

	sub1 := makeSubscription(op.ID, "same-name", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub1))

	sub2 := makeSubscription(op.ID, "same-name", []string{"operator.created"}, true)
	err := factory.WebhookSubscriptionRepository().Create(ctx, sub2)
	assert.ErrorIs(t, err, repositories.ErrAlreadyExists)
}

func TestSubscriptionRepo_ListEnabledForEvent_ExactMatch(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-exact")

	sub := makeSubscription(op.ID, "exact-hook", []string{"operator.created"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	results, err := factory.WebhookSubscriptionRepository().ListEnabledForEvent(ctx, &op.ID, "operator.created")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, sub.ID, results[0].ID)

	// Different type — should not match.
	results, err = factory.WebhookSubscriptionRepository().ListEnabledForEvent(ctx, &op.ID, "account.created")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSubscriptionRepo_ListEnabledForEvent_Wildcard(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-wildcard")

	sub := makeSubscription(op.ID, "wildcard-hook", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	for _, eventType := range []string{"operator.created", "account.deleted", "cluster.synced"} {
		results, err := factory.WebhookSubscriptionRepository().ListEnabledForEvent(ctx, &op.ID, eventType)
		require.NoError(t, err)
		assert.Len(t, results, 1, "wildcard should match %s", eventType)
	}
}

func TestSubscriptionRepo_ListEnabledForEvent_DisabledExcluded(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-disabled")

	sub := makeSubscription(op.ID, "disabled-hook", []string{"*"}, false)
	sub.DisabledReason = "secret_unreadable"
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	results, err := factory.WebhookSubscriptionRepository().ListEnabledForEvent(ctx, &op.ID, "operator.created")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSubscriptionRepo_ListEnabledForEvent_OperatorScoped(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	opA := makeOperatorForWebhook(t, factory, "op-a-scoped")
	opB := makeOperatorForWebhook(t, factory, "op-b-scoped")

	subA := makeSubscription(opA.ID, "hook-a", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, subA))

	// Query for opB — should not return opA's subscription.
	results, err := factory.WebhookSubscriptionRepository().ListEnabledForEvent(ctx, &opB.ID, "operator.created")
	require.NoError(t, err)
	assert.Empty(t, results)
}

// delivery tests

func TestDeliveryRepo_CreateAndGet(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-del-create")
	sub := makeSubscription(op.ID, "del-hook", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))
	ev := makeEventForDelivery(t, factory)

	now := time.Now().UTC()
	d := makeDelivery(sub.ID, ev.ID, entities.DeliveryStatusPending, now)
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, d))

	got, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, d.ID, got.ID)
	assert.Equal(t, d.SubscriptionID, got.SubscriptionID)
	assert.Equal(t, d.EventID, got.EventID)
	assert.Equal(t, entities.DeliveryStatusPending, got.Status)
}

func TestDeliveryRepo_ClaimDue_OrdersByNextAttempt(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-claim-order")
	sub := makeSubscription(op.ID, "claim-hook", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	now := time.Now().UTC()
	ev1 := makeEventForDelivery(t, factory)
	ev2 := makeEventForDelivery(t, factory)
	ev3 := makeEventForDelivery(t, factory)

	d1 := makeDelivery(sub.ID, ev1.ID, entities.DeliveryStatusPending, now.Add(-3*time.Minute))
	d2 := makeDelivery(sub.ID, ev2.ID, entities.DeliveryStatusPending, now.Add(-1*time.Minute))
	d3 := makeDelivery(sub.ID, ev3.ID, entities.DeliveryStatusPending, now.Add(-2*time.Minute))

	for _, d := range []*entities.WebhookDelivery{d1, d2, d3} {
		require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, d))
	}

	claimed, err := factory.WebhookDeliveryRepository().ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 3)
	// Ordered by next_attempt_at ASC: d1 (-3m), d3 (-2m), d2 (-1m)
	assert.Equal(t, d1.ID, claimed[0].ID)
	assert.Equal(t, d3.ID, claimed[1].ID)
	assert.Equal(t, d2.ID, claimed[2].ID)
}

func TestDeliveryRepo_ClaimDue_SkipsFutureAndNonPending(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-claim-skip")
	sub := makeSubscription(op.ID, "skip-hook", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	now := time.Now().UTC()
	ev1 := makeEventForDelivery(t, factory)
	ev2 := makeEventForDelivery(t, factory)
	ev3 := makeEventForDelivery(t, factory)

	due := makeDelivery(sub.ID, ev1.ID, entities.DeliveryStatusPending, now.Add(-1*time.Minute))
	future := makeDelivery(sub.ID, ev2.ID, entities.DeliveryStatusPending, now.Add(1*time.Hour))
	succeeded := makeDelivery(sub.ID, ev3.ID, entities.DeliveryStatusSucceeded, now.Add(-5*time.Minute))

	for _, d := range []*entities.WebhookDelivery{due, future, succeeded} {
		require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, d))
	}

	claimed, err := factory.WebhookDeliveryRepository().ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, due.ID, claimed[0].ID)
}

func TestDeliveryRepo_DeleteSucceededOlderThan_PreservesDeadLetter(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-del-succeeded")
	sub := makeSubscription(op.ID, "del-succ-hook", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	now := time.Now().UTC()
	cutoff := now.Add(-1 * time.Hour)

	ev1 := makeEventForDelivery(t, factory)
	ev2 := makeEventForDelivery(t, factory)
	ev3 := makeEventForDelivery(t, factory)

	oldSucceeded := makeDelivery(sub.ID, ev1.ID, entities.DeliveryStatusSucceeded, cutoff.Add(-1*time.Minute))
	completedAt := cutoff.Add(-30 * time.Minute)
	oldSucceeded.CompletedAt = &completedAt
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, oldSucceeded))

	deadLetter := makeDelivery(sub.ID, ev2.ID, entities.DeliveryStatusDeadLetter, cutoff.Add(-2*time.Minute))
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, deadLetter))

	newSucceeded := makeDelivery(sub.ID, ev3.ID, entities.DeliveryStatusSucceeded, now)
	recentCompleted := now.Add(-5 * time.Minute)
	newSucceeded.CompletedAt = &recentCompleted
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, newSucceeded))

	deleted, err := factory.WebhookDeliveryRepository().DeleteSucceededOlderThan(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// old succeeded is gone
	_, err = factory.WebhookDeliveryRepository().GetByID(ctx, oldSucceeded.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound)

	// dead letter preserved
	_, err = factory.WebhookDeliveryRepository().GetByID(ctx, deadLetter.ID)
	assert.NoError(t, err)

	// new succeeded preserved
	_, err = factory.WebhookDeliveryRepository().GetByID(ctx, newSucceeded.ID)
	assert.NoError(t, err)
}

func TestDeliveryRepo_CascadeFromSubscription(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-cascade-sub")
	sub := makeSubscription(op.ID, "cascade-sub-hook", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	ev := makeEventForDelivery(t, factory)
	d := makeDelivery(sub.ID, ev.ID, entities.DeliveryStatusPending, time.Now().UTC())
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, d))

	// Delete the subscription — delivery should cascade.
	require.NoError(t, factory.WebhookSubscriptionRepository().Delete(ctx, sub.ID))

	_, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound)
}

func TestDeliveryRepo_CascadeFromEvent(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperatorForWebhook(t, factory, "op-cascade-ev")
	sub := makeSubscription(op.ID, "cascade-ev-hook", []string{"*"}, true)
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	ev := makeEventForDelivery(t, factory)
	d := makeDelivery(sub.ID, ev.ID, entities.DeliveryStatusPending, time.Now().UTC())
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, d))

	// Delete the event — delivery should cascade.
	deleted, err := factory.EventRepository().DeleteOlderThan(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(1))

	_, err = factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound)
}
