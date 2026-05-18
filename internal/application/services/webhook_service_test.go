package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	sqlpkg "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/migrations"
)

func webhookTestDB(t *testing.T) persistence.RepositoryFactory {
	t.Helper()
	db, err := sqlpkg.NewDB("sqlite", ":memory:")
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))

	t.Cleanup(func() { _ = sqlpkg.Close(db) })
	return persistence.NewSQLRepositoryFactoryFromDB(db)
}

// insertOperator inserts a minimal operator row so FK constraints pass.
// PublicKey is set to the ID string to satisfy the UNIQUE constraint when
// multiple operators are created in the same DB.
func insertOperator(t *testing.T, ctx context.Context, factory persistence.RepositoryFactory) uuid.UUID {
	t.Helper()
	opID := uuid.New()
	require.NoError(t, factory.OperatorRepository().Create(ctx, &entities.Operator{
		ID:        opID,
		Name:      "test-op-" + opID.String()[:8],
		PublicKey: opID.String(),
	}))
	return opID
}

func TestCreateSubscription_GeneratesSecret_EncryptsAndStores(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	sub, secret, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: opID,
		Name:       "test-sub",
		URL:        "https://example.com/hook",
		EventTypes: []string{"operator.created"},
	})
	require.NoError(t, err)
	require.NotNil(t, sub)

	assert.Len(t, secret, 64, "plaintext secret must be 64 hex chars (32 bytes)")
	assert.NotEmpty(t, sub.EncryptedSecret)
	assert.NotEqual(t, secret, sub.EncryptedSecret, "encrypted form must differ from plaintext")

	got, err := factory.WebhookSubscriptionRepository().GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, got.ID)
	assert.Equal(t, sub.EncryptedSecret, got.EncryptedSecret)
}

func TestCreateSubscription_RequiresFields(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	tests := []struct {
		name string
		req  CreateWebhookSubscriptionRequest
	}{
		{
			name: "empty name",
			req:  CreateWebhookSubscriptionRequest{OperatorID: opID, Name: "", URL: "https://x.com", EventTypes: []string{"*"}},
		},
		{
			name: "empty url",
			req:  CreateWebhookSubscriptionRequest{OperatorID: opID, Name: "sub", URL: "", EventTypes: []string{"*"}},
		},
		{
			name: "empty event_types",
			req:  CreateWebhookSubscriptionRequest{OperatorID: opID, Name: "sub", URL: "https://x.com", EventTypes: []string{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := svc.CreateSubscription(ctx, tt.req)
			assert.Error(t, err)
		})
	}
}

func TestUpdateSubscription_PartialFields(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	sub, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID:  opID,
		Name:        "original",
		Description: "desc",
		URL:         "https://orig.com",
		EventTypes:  []string{"operator.created"},
	})
	require.NoError(t, err)

	newName := "updated-name"
	updated, err := svc.UpdateSubscription(ctx, sub.ID, UpdateWebhookSubscriptionRequest{
		Name: &newName,
	})
	require.NoError(t, err)

	assert.Equal(t, "updated-name", updated.Name)
	assert.Equal(t, "desc", updated.Description, "description must be unchanged")
	assert.Equal(t, "https://orig.com", updated.URL, "url must be unchanged")
	assert.Equal(t, []string{"operator.created"}, updated.EventTypes, "event_types must be unchanged")
}

func TestUpdateSubscription_ReenableClearsDisabledReason(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	sub, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: opID,
		Name:       "sub",
		URL:        "https://x.com",
		EventTypes: []string{"*"},
	})
	require.NoError(t, err)

	// Disable and set a reason directly in the repo to simulate worker auto-disable.
	sub.Enabled = false
	sub.DisabledReason = "too many failures"
	require.NoError(t, factory.WebhookSubscriptionRepository().Update(ctx, sub))

	enabled := true
	updated, err := svc.UpdateSubscription(ctx, sub.ID, UpdateWebhookSubscriptionRequest{
		Enabled: &enabled,
	})
	require.NoError(t, err)
	assert.True(t, updated.Enabled)
	assert.Empty(t, updated.DisabledReason, "re-enabling must clear DisabledReason")
}

func TestTestSubscription_EnqueuesExactlyOneDeliveryForSub(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	sub, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: opID,
		Name:       "sub",
		URL:        "https://x.com",
		EventTypes: []string{entities.EventTypeWebhookTest},
	})
	require.NoError(t, err)

	deliveryID, err := svc.TestSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, deliveryID)

	deliveries, err := factory.WebhookDeliveryRepository().List(ctx, repositories.WebhookDeliveryFilter{
		SubscriptionID: &sub.ID,
		Limit:          10,
	})
	require.NoError(t, err)
	assert.Len(t, deliveries, 1)
	assert.Equal(t, deliveryID, deliveries[0].ID)

	events, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, events.Events)
	assert.Equal(t, entities.EventTypeWebhookTest, events.Events[0].Type)
}

func TestTestSubscription_DisabledSub_Refuses(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	sub, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: opID,
		Name:       "sub",
		URL:        "https://x.com",
		EventTypes: []string{"*"},
	})
	require.NoError(t, err)

	disabled := false
	_, err = svc.UpdateSubscription(ctx, sub.ID, UpdateWebhookSubscriptionRequest{
		Enabled: &disabled,
	})
	require.NoError(t, err)

	_, err = svc.TestSubscription(ctx, sub.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestTestSubscription_WildcardSub_StillEnqueues(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)
	opID := insertOperator(t, ctx, factory)

	sub, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: opID,
		Name:       "wildcard-sub",
		URL:        "https://x.com",
		EventTypes: []string{"*"},
	})
	require.NoError(t, err)

	deliveryID, err := svc.TestSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, deliveryID)

	deliveries, err := factory.WebhookDeliveryRepository().List(ctx, repositories.WebhookDeliveryFilter{
		SubscriptionID: &sub.ID,
		Limit:          10,
	})
	require.NoError(t, err)
	assert.Len(t, deliveries, 1)
}

func TestListDeliveries_FiltersBySubscription(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	enc := workerTestEncryptor(t)
	svc := NewWebhookService(factory, enc)

	// Use separate operators so each sub only receives its own test event.
	op1ID := insertOperator(t, ctx, factory)
	op2ID := insertOperator(t, ctx, factory)

	sub1, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: op1ID,
		Name:       "sub1",
		URL:        "https://x.com/1",
		EventTypes: []string{entities.EventTypeWebhookTest},
	})
	require.NoError(t, err)

	sub2, _, err := svc.CreateSubscription(ctx, CreateWebhookSubscriptionRequest{
		OperatorID: op2ID,
		Name:       "sub2",
		URL:        "https://x.com/2",
		EventTypes: []string{entities.EventTypeWebhookTest},
	})
	require.NoError(t, err)

	// Enqueue one delivery on each subscription via TestSubscription.
	_, err = svc.TestSubscription(ctx, sub1.ID)
	require.NoError(t, err)
	_, err = svc.TestSubscription(ctx, sub2.ID)
	require.NoError(t, err)

	deliveries1, err := svc.ListDeliveries(ctx, repositories.WebhookDeliveryFilter{
		SubscriptionID: &sub1.ID,
		Limit:          10,
	})
	require.NoError(t, err)
	assert.Len(t, deliveries1, 1)
	assert.Equal(t, sub1.ID, deliveries1[0].SubscriptionID)

	deliveries2, err := svc.ListDeliveries(ctx, repositories.WebhookDeliveryFilter{
		SubscriptionID: &sub2.ID,
		Limit:          10,
	})
	require.NoError(t, err)
	assert.Len(t, deliveries2, 1)
	assert.Equal(t, sub2.ID, deliveries2[0].SubscriptionID)
}
