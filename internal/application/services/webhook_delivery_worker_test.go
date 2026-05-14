package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlpkg "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
	"github.com/thomas-maurice/nis/pkg/webhooks"
	"gorm.io/gorm"
)

// workerTestDB sets up a fresh in-memory SQLite DB with migrations applied.
func workerTestDB(t *testing.T) (*gorm.DB, persistence.RepositoryFactory) {
	t.Helper()
	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "."))

	t.Cleanup(func() { _ = sqlpkg.Close(db) })
	return db, persistence.NewSQLRepositoryFactoryFromDB(db)
}

func workerTestEncryptor(t *testing.T) encryption.Encryptor {
	t.Helper()
	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)
	return enc
}

// insertWorkerFixtures inserts an operator, subscription, event, and a pending delivery.
// Returns the subscription and delivery.
func insertWorkerFixtures(t *testing.T, ctx context.Context, factory persistence.RepositoryFactory, enc encryption.Encryptor, subURL string) (*entities.WebhookSubscription, *entities.WebhookDelivery) {
	t.Helper()

	// Insert an operator (minimal).
	opID := uuid.New()
	op := &entities.Operator{
		ID:   opID,
		Name: "test-op-" + opID.String()[:8],
	}
	require.NoError(t, factory.OperatorRepository().Create(ctx, op))

	// Encrypt a secret.
	secret := []byte("supersecret-webhook-key")
	encRef, err := enc.Encrypt(ctx, secret)
	require.NoError(t, err)

	sub := &entities.WebhookSubscription{
		ID:              uuid.New(),
		OperatorID:      opID,
		Name:            "test-sub",
		URL:             subURL,
		EncryptedSecret: encRef,
		EventTypes:      []string{"*"},
		Enabled:         true,
	}
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))

	evt := &entities.Event{
		ID:           uuid.New(),
		OccurredAt:   time.Now().UTC(),
		Type:         entities.EventTypeAccountCreated,
		ActorType:    entities.ActorTypeSystem,
		OperatorID:   &opID,
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

// TestWorker_Backoff_Math validates that computeBackoff stays within bounds.
func TestWorker_Backoff_Math(t *testing.T) {
	base := 10 * time.Second
	cap := 10 * time.Minute

	for attempt := 1; attempt <= 7; attempt++ {
		got := computeBackoff(attempt, base, cap)
		assert.GreaterOrEqual(t, got, time.Duration(float64(base)*0.8), "attempt %d: too small", attempt)
		assert.LessOrEqual(t, got, cap, "attempt %d: exceeds cap", attempt)
	}

	// At high attempt counts the backoff should be at or near cap.
	high := computeBackoff(20, base, cap)
	assert.LessOrEqual(t, high, cap)
}

// TestWorker_SuccessPath_HTTP200 runs one processOne call against a 200 server.
func TestWorker_SuccessPath_HTTP200(t *testing.T) {
	ctx := context.Background()
	_, factory := workerTestDB(t)
	enc := workerTestEncryptor(t)

	var capturedBody []byte
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sub, d := insertWorkerFixtures(t, ctx, factory, enc, server.URL)

	worker := NewWebhookDeliveryWorker(factory, enc, WebhookDeliveryWorkerConfig{
		MaxAttempts: 3,
		BackoffBase: time.Second,
		BackoffCap:  time.Minute,
	})
	worker.processOne(ctx, d)

	// Reload delivery.
	updated, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusSucceeded, updated.Status)
	assert.Equal(t, 1, updated.Attempt)
	assert.NotNil(t, updated.CompletedAt)

	// Headers present.
	assert.NotEmpty(t, capturedHeaders.Get(webhooks.HeaderEvent))
	assert.NotEmpty(t, capturedHeaders.Get(webhooks.HeaderDelivery))
	assert.Equal(t, sub.ID.String(), capturedHeaders.Get(webhooks.HeaderSubscription))
	assert.NotEmpty(t, capturedHeaders.Get(webhooks.HeaderTimestamp))
	assert.Contains(t, capturedHeaders.Get(webhooks.HeaderSignature), "sha256=")

	// Verify HMAC using the SDK.
	secret := []byte("supersecret-webhook-key")
	sigHdr := capturedHeaders.Get(webhooks.HeaderSignature)
	tsHdr := capturedHeaders.Get(webhooks.HeaderTimestamp)
	expected := webhooks.Sign(secret, tsHdr, capturedBody)
	assert.Equal(t, expected, sigHdr)

	// Body is valid JSON with expected shape.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &payload))
	assert.Equal(t, entities.EventTypeAccountCreated, payload["type"])
}

// TestWorker_RetryThenSuccess: first call returns 500, second returns 200.
func TestWorker_RetryThenSuccess(t *testing.T) {
	ctx := context.Background()
	_, factory := workerTestDB(t)
	enc := workerTestEncryptor(t)

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, d := insertWorkerFixtures(t, ctx, factory, enc, server.URL)

	worker := NewWebhookDeliveryWorker(factory, enc, WebhookDeliveryWorkerConfig{
		MaxAttempts: 3,
		BackoffBase: time.Millisecond,
		BackoffCap:  time.Second,
	})

	// First tick.
	worker.processOne(ctx, d)
	after1, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusPending, after1.Status)
	assert.Equal(t, 1, after1.Attempt)
	assert.True(t, after1.NextAttemptAt.After(time.Now().Add(-time.Second)))

	// Second tick (re-fetch from DB to get the current attempt count).
	worker.processOne(ctx, after1)
	after2, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusSucceeded, after2.Status)
	assert.Equal(t, 2, after2.Attempt)
}

// TestWorker_DeadLetterAtMax: MaxAttempts failures → dead_letter.
func TestWorker_DeadLetterAtMax(t *testing.T) {
	ctx := context.Background()
	_, factory := workerTestDB(t)
	enc := workerTestEncryptor(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, d := insertWorkerFixtures(t, ctx, factory, enc, server.URL)

	maxAttempts := 3
	worker := NewWebhookDeliveryWorker(factory, enc, WebhookDeliveryWorkerConfig{
		MaxAttempts: maxAttempts,
		BackoffBase: time.Millisecond,
		BackoffCap:  time.Second,
	})

	cur := d
	for i := 0; i < maxAttempts; i++ {
		worker.processOne(ctx, cur)
		var err error
		cur, err = factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
		require.NoError(t, err)
	}

	assert.Equal(t, entities.DeliveryStatusDeadLetter, cur.Status)
	assert.Equal(t, maxAttempts, cur.Attempt)
	assert.Equal(t, http.StatusInternalServerError, cur.LastResponseCode)
	assert.NotNil(t, cur.CompletedAt)
}

// TestWorker_DisabledSubscription_DeadLetters_DeliveryRow verifies that a
// disabled subscription causes the delivery to be dead-lettered (not retried),
// making the problem visible in the delivery log.
func TestWorker_DisabledSubscription_DeadLetters_DeliveryRow(t *testing.T) {
	ctx := context.Background()
	_, factory := workerTestDB(t)
	enc := workerTestEncryptor(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sub, d := insertWorkerFixtures(t, ctx, factory, enc, server.URL)

	// Disable the subscription after enqueue.
	sub.Enabled = false
	sub.DisabledReason = "manually disabled"
	require.NoError(t, factory.WebhookSubscriptionRepository().Update(ctx, sub))

	worker := NewWebhookDeliveryWorker(factory, enc, WebhookDeliveryWorkerConfig{
		MaxAttempts: 3,
		BackoffBase: time.Second,
		BackoffCap:  time.Minute,
	})
	worker.processOne(ctx, d)

	updated, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusDeadLetter, updated.Status)
	assert.Equal(t, "subscription_disabled", updated.LastError)
}

// TestWorker_DecryptFailure_AutoDisablesSubscription corrupts the encrypted
// secret and verifies the subscription is auto-disabled.
func TestWorker_DecryptFailure_AutoDisablesSubscription(t *testing.T) {
	ctx := context.Background()
	_, factory := workerTestDB(t)
	enc := workerTestEncryptor(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sub, d := insertWorkerFixtures(t, ctx, factory, enc, server.URL)

	// Corrupt the encrypted secret.
	sub.EncryptedSecret = "encrypted:test-key:NOTVALIDBASE64!!!"
	require.NoError(t, factory.WebhookSubscriptionRepository().Update(ctx, sub))

	worker := NewWebhookDeliveryWorker(factory, enc, WebhookDeliveryWorkerConfig{
		MaxAttempts: 3,
		BackoffBase: time.Second,
		BackoffCap:  time.Minute,
	})
	worker.processOne(ctx, d)

	// Delivery is dead-lettered.
	updated, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusDeadLetter, updated.Status)
	assert.Contains(t, updated.LastError, "decrypt_failed")

	// Subscription auto-disabled.
	updatedSub, err := factory.WebhookSubscriptionRepository().GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.False(t, updatedSub.Enabled)
	assert.Equal(t, "secret_unreadable", updatedSub.DisabledReason)
}

// TestWorker_TransportError_Retries: target at a closed port → transport error.
func TestWorker_TransportError_Retries(t *testing.T) {
	ctx := context.Background()
	_, factory := workerTestDB(t)
	enc := workerTestEncryptor(t)

	// Start and immediately close a server so the port is definitely not listening.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	closedURL := server.URL
	server.Close()

	_, d := insertWorkerFixtures(t, ctx, factory, enc, closedURL)

	worker := NewWebhookDeliveryWorker(factory, enc, WebhookDeliveryWorkerConfig{
		MaxAttempts:     5,
		BackoffBase:     time.Millisecond,
		BackoffCap:      time.Second,
		DeliveryTimeout: 200 * time.Millisecond,
	})
	worker.processOne(ctx, d)

	updated, err := factory.WebhookDeliveryRepository().GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.DeliveryStatusPending, updated.Status)
	assert.Equal(t, 1, updated.Attempt)
	assert.NotEmpty(t, updated.LastError)
	assert.True(t, updated.NextAttemptAt.After(time.Now().Add(-time.Second)))
}
