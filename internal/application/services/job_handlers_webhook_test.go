package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// setupWebhookFixtures inserts the chain (operator → sub → event → pending
// delivery) the handler needs to reach the HTTP POST. Returns the encryption
// ref + the delivery row so individual tests can mutate state before invoking
// the handler.
func setupWebhookFixtures(
	t *testing.T,
	ctx context.Context,
	factory persistence.RepositoryFactory,
	enc encryption.Encryptor,
	subURL string,
	enabled bool,
) (*entities.WebhookSubscription, *entities.WebhookDelivery) {
	t.Helper()

	opID := insertOperator(t, ctx, factory)

	secret := []byte("supersecret-webhook-key-for-tests")
	encRef, err := enc.Encrypt(ctx, secret)
	require.NoError(t, err)

	sub := &entities.WebhookSubscription{
		ID:              uuid.New(),
		OperatorID:      opID,
		Name:            "test-sub-" + uuid.NewString()[:8],
		URL:             subURL,
		EncryptedSecret: encRef,
		EventTypes:      []string{"*"},
		Enabled:         enabled,
	}
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	evt := &entities.Event{
		ID:           uuid.New(),
		OccurredAt:   time.Now().UTC(),
		Type:         entities.EventTypeAccountCreated,
		ActorType:    entities.ActorTypeSystem,
		OperatorID:   &sub.OperatorID,
		ResourceType: "account",
		ResourceID:   uuid.New().String(),
	}
	require.NoError(t, factory.EventRepository().Create(ctx, evt))

	d := &entities.WebhookDelivery{
		ID:             uuid.New(),
		SubscriptionID: sub.ID,
		EventID:        evt.ID,
		Status:         entities.DeliveryStatusPending,
		NextAttemptAt:  time.Now().UTC().Add(-time.Second),
	}
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, d))

	return sub, d
}

func newWebhookHandler(factory persistence.RepositoryFactory, enc encryption.Encryptor, maxAttempts int) *webhookDeliverHandler {
	return &webhookDeliverHandler{
		factory:     factory,
		encryptor:   enc,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		maxAttempts: maxAttempts,
	}
}

func deliverPayloadJSON(t *testing.T, id uuid.UUID) []byte {
	t.Helper()
	b, err := json.Marshal(webhookDeliverPayload{DeliveryID: id})
	require.NoError(t, err)
	return b
}

// --- Success ---

// 2xx → handler returns nil, delivery row flips to succeeded with the
// response code recorded. Single-attempt happy path.
func TestWebhookHandler_Success_2xx_MarksSucceeded(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(202)
	}))
	defer srv.Close()

	_, d := setupWebhookFixtures(t, ctx, factory, enc, srv.URL, true)
	h := newWebhookHandler(factory, enc, 5)

	require.NoError(t, h.Run(ctx, deliverPayloadJSON(t, d.ID)))
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

	got, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusSucceeded, got.Status)
	assert.Equal(t, 202, got.LastResponseCode)
	assert.Equal(t, 1, got.Attempt)
	assert.NotNil(t, got.CompletedAt)
}

// --- Transient: retries before final attempt ---

// 5xx on a non-final attempt → handler returns a plain (non-permanent) error,
// delivery row flips to `failed` with attempt incremented. The substrate's
// retry path is what would re-fire; here we just check the handler's signal.
func TestWebhookHandler_Transient_NonFinalAttempt_ReturnsRetryableError(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	_, d := setupWebhookFixtures(t, ctx, factory, enc, srv.URL, true)
	h := newWebhookHandler(factory, enc, 3)

	err := h.Run(ctx, deliverPayloadJSON(t, d.ID))
	require.Error(t, err)
	// Plain transient error — NOT permanent.
	assert.False(t, errors.Is(err, ErrPermanentJobFailure))

	got, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusFailed, got.Status)
	assert.Equal(t, 503, got.LastResponseCode)
	assert.Equal(t, 1, got.Attempt)
	assert.Nil(t, got.CompletedAt, "non-terminal failure must leave completed_at unset")
}

// --- Transient: final attempt ---

// 5xx on the last allowed attempt → handler returns wrapped
// ErrPermanentJobFailure so the substrate dead-letters the job, and the
// delivery row also flips to dead_letter (UI doesn't show a stuck `failed`).
func TestWebhookHandler_Transient_FinalAttempt_DeadLetters(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	_, d := setupWebhookFixtures(t, ctx, factory, enc, srv.URL, true)
	// Simulate this being the last allowed attempt by pre-bumping Attempt.
	d.Attempt = 4
	require.NoError(t, factory.WebhookDeliveryRepository().Update(ctx, d))

	h := newWebhookHandler(factory, enc, 5)
	err := h.Run(ctx, deliverPayloadJSON(t, d.ID))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentJobFailure), "final attempt must signal permanent so substrate dead-letters")

	got, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusDeadLetter, got.Status)
	assert.Equal(t, 5, got.Attempt)
	assert.NotNil(t, got.CompletedAt)
}

// --- Transport-level failure ---

// Connection-refused (no server) → same retry semantics as a 5xx.
func TestWebhookHandler_TransportError_NonFinal_Retries(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	_, d := setupWebhookFixtures(t, ctx, factory, enc, "http://127.0.0.1:1", true)
	h := newWebhookHandler(factory, enc, 3)

	err := h.Run(ctx, deliverPayloadJSON(t, d.ID))
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrPermanentJobFailure))

	got, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusFailed, got.Status)
	assert.Equal(t, 0, got.LastResponseCode, "transport error leaves response code at zero")
	assert.NotEmpty(t, got.LastError)
}

// --- Permanent: subscription disabled between enqueue and claim ---

// Subscription disabled at dispatch time → handler signals permanent and the
// delivery is dead-lettered without an HTTP POST. The whole point of
// dead-lettering (vs silently dropping) is so the delivery log shows
// receivers that the system tried.
func TestWebhookHandler_Permanent_SubscriptionDisabled(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, d := setupWebhookFixtures(t, ctx, factory, enc, srv.URL, false /* disabled */)
	h := newWebhookHandler(factory, enc, 5)

	err := h.Run(ctx, deliverPayloadJSON(t, d.ID))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentJobFailure))
	assert.False(t, called, "disabled sub must not POST")

	got, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusDeadLetter, got.Status)
	assert.Contains(t, got.LastError, "subscription_disabled")
}

// --- Permanent: decrypt failure auto-disables the subscription ---

// Decrypt error → handler signals permanent + auto-disables the subscription
// with the diagnostic reason set. Mirrors the pre-A16 worker behavior.
func TestWebhookHandler_Permanent_DecryptFailure_AutoDisablesSubscription(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sub, d := setupWebhookFixtures(t, ctx, factory, enc, srv.URL, true)

	// Replace the encrypted secret with a string the encryptor can't decrypt.
	sub.EncryptedSecret = "not-a-valid-encrypted-blob"
	require.NoError(t, factory.WebhookSubscriptionRepository().Update(ctx, sub))

	h := newWebhookHandler(factory, enc, 5)
	err := h.Run(ctx, deliverPayloadJSON(t, d.ID))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentJobFailure))

	got, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusDeadLetter, got.Status)
	assert.Contains(t, got.LastError, "decrypt_failed")

	subGot, err := factory.WebhookSubscriptionRepository().GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.False(t, subGot.Enabled, "decrypt failure must auto-disable the subscription")
	assert.Equal(t, "secret_unreadable", subGot.DisabledReason)
}

// --- Idempotency ---

// Re-claim after a successful run (lease expired before MarkSucceeded landed)
// → handler does nothing. Critical: two HTTP POSTs for one delivery is the
// receiver-side bug we're paid to prevent.
func TestWebhookHandler_Idempotent_AlreadySucceeded(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, d := setupWebhookFixtures(t, ctx, factory, enc, srv.URL, true)
	completed := time.Now().UTC()
	d.Status = entities.DeliveryStatusSucceeded
	d.CompletedAt = &completed
	require.NoError(t, factory.WebhookDeliveryRepository().Update(ctx, d))

	h := newWebhookHandler(factory, enc, 5)
	require.NoError(t, h.Run(ctx, deliverPayloadJSON(t, d.ID)))
	assert.False(t, called, "succeeded delivery must not be re-POSTed")
}

// Delivery row gone (e.g. webhook subscription deleted, FK CASCADE) → handler
// returns nil so the substrate marks succeeded rather than retrying forever.
func TestWebhookHandler_Idempotent_DeliveryRowGone(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	h := newWebhookHandler(factory, enc, 5)
	require.NoError(t, h.Run(ctx, deliverPayloadJSON(t, uuid.New())))
}

// --- Permanent payload malformed ---

// Garbage payload → permanent failure (no point retrying garbage).
func TestWebhookHandler_Permanent_MalformedPayload(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	h := newWebhookHandler(factory, enc, 5)

	err := h.Run(ctx, []byte("{not json"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentJobFailure))
}

// --- In-tx atomicity: enqueue failure rolls back the delivery row ---

// If the job-enqueue callback fails, the entire EmitTx rolls back — no
// orphan webhook_deliveries row, no orphan event. This is the load-bearing
// guarantee of the A16 in-tx enqueue design (vs the pre-A16 worker's
// "delivery exists in DB without a dispatcher" anomaly).
func TestEmit_JobEnqueueFailure_RollsBackDelivery(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	_, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: opID,
		Name:       "rb-sub",
		URL:        "https://example.com/hook",
		EventTypes: []string{"*"},
	})
	require.NoError(t, err)

	// Capture baseline counts after setup (CreateSubscription now emits
	// webhook.subscription.created which fans out to the wildcard sub).
	baseDeliveries, err := factory.WebhookDeliveryRepository().List(ctx, repositories.WebhookDeliveryFilter{Limit: 100})
	require.NoError(t, err)
	baseEvents, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 100})
	require.NoError(t, err)

	// Install a faulty enqueuer that always errors. Restore at end so we
	// don't poison other tests in the same binary.
	sentinel := errors.New("forced enqueue failure")
	events.SetJobEnqueuer(func(ctx context.Context, tx persistence.RepositoryFactory, deliveryID uuid.UUID) error {
		return sentinel
	})
	t.Cleanup(func() { events.SetJobEnqueuer(nil) })

	emitErr := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, events.Event{
			Type:         entities.EventTypeAccountCreated,
			OperatorID:   &opID,
			ResourceType: "account",
			ResourceID:   uuid.New().String(),
		})
	})
	require.ErrorIs(t, emitErr, sentinel)

	// Neither the new event nor the new delivery should have committed.
	deliveries, err := factory.WebhookDeliveryRepository().List(ctx, repositories.WebhookDeliveryFilter{Limit: 100})
	require.NoError(t, err)
	assert.Len(t, deliveries, len(baseDeliveries), "delivery row must roll back when companion job enqueue fails")

	res, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 100})
	require.NoError(t, err)
	assert.Len(t, res.Events, len(baseEvents.Events), "event row must roll back when companion job enqueue fails")
}

// --- Catch-up scan ---

// EnqueueCatchUpDeliveries enqueues one webhook.deliver job for each
// non-terminal delivery without an existing live job (covers upgrade from
// pre-A16 + the early-startup window before SetJobEnqueuer is installed).
func TestEnqueueCatchUpDeliveries_EnqueuesOrphans(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)

	// Two orphan rows: one pending, one previously transient-failed.
	_, dPending := setupWebhookFixtures(t, ctx, factory, enc, "http://example.com/h1", true)
	_, dFailed := setupWebhookFixtures(t, ctx, factory, enc, "http://example.com/h2", true)
	dFailed.Status = entities.DeliveryStatusFailed
	dFailed.Attempt = 1
	require.NoError(t, factory.WebhookDeliveryRepository().Update(ctx, dFailed))

	// One terminal row that should NOT be enqueued.
	_, dDone := setupWebhookFixtures(t, ctx, factory, enc, "http://example.com/h3", true)
	doneAt := time.Now().UTC()
	dDone.Status = entities.DeliveryStatusSucceeded
	dDone.CompletedAt = &doneAt
	require.NoError(t, factory.WebhookDeliveryRepository().Update(ctx, dDone))

	n, err := EnqueueCatchUpDeliveries(ctx, factory, 5)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Idempotent re-run: existing jobs are dedup'd by the partial unique index.
	n2, err := EnqueueCatchUpDeliveries(ctx, factory, 5)
	require.NoError(t, err)
	assert.Equal(t, 0, n2, "second run must not re-enqueue live jobs")

	// Spot-check that the job rows were created for the two orphans.
	jobs, _, err := factory.JobRepository().List(ctx, repositories.JobFilter{Types: []string{JobTypeWebhookDeliver}, Limit: 10})
	require.NoError(t, err)
	gotIDs := map[string]bool{}
	for _, j := range jobs {
		var p webhookDeliverPayload
		require.NoError(t, json.Unmarshal(j.Payload, &p))
		gotIDs[p.DeliveryID.String()] = true
	}
	assert.True(t, gotIDs[dPending.ID.String()], "pending delivery must be enqueued")
	assert.True(t, gotIDs[dFailed.ID.String()], "failed delivery must be enqueued")
	assert.False(t, gotIDs[dDone.ID.String()], "succeeded delivery must NOT be enqueued")
}
