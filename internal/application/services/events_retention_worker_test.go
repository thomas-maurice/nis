package services

import (
	"context"
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
	"gorm.io/gorm"
)

func retentionTestDB(t *testing.T) (*gorm.DB, persistence.RepositoryFactory) {
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

func retentionTestEncryptor(t *testing.T) encryption.Encryptor {
	t.Helper()
	enc, err := encryption.NewChaChaEncryptor(map[string]string{
		"test-key": "Lj9yxga5k/zCwSw76UUklT8Jkzgu7ChfY3zUEH8iBM8=",
	}, "test-key")
	require.NoError(t, err)
	return enc
}

// insertRetentionOperatorAndSub inserts a bare-minimum operator and subscription for
// creating deliveries against.
func insertRetentionOperatorAndSub(t *testing.T, ctx context.Context, factory persistence.RepositoryFactory, enc encryption.Encryptor) (*entities.Operator, *entities.WebhookSubscription) {
	t.Helper()
	opID := uuid.New()
	op := &entities.Operator{ID: opID, Name: "ret-op-" + opID.String()[:8]}
	require.NoError(t, factory.OperatorRepository().Create(ctx, op))

	encRef, err := enc.Encrypt(ctx, []byte("secret"))
	require.NoError(t, err)
	sub := &entities.WebhookSubscription{
		ID:              uuid.New(),
		OperatorID:      opID,
		Name:            "ret-sub",
		URL:             "http://localhost:9999/webhook",
		EncryptedSecret: encRef,
		EventTypes:      []string{"*"},
		Enabled:         true,
	}
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(ctx, sub))
	return op, sub
}

func insertEvent(t *testing.T, ctx context.Context, factory persistence.RepositoryFactory, opID uuid.UUID, occurredAt time.Time) *entities.Event {
	t.Helper()
	evt := &entities.Event{
		ID:           uuid.New(),
		OccurredAt:   occurredAt,
		Type:         entities.EventTypeAccountCreated,
		ActorType:    entities.ActorTypeSystem,
		OperatorID:   &opID,
		ResourceType: "account",
		ResourceID:   uuid.New().String(),
	}
	require.NoError(t, factory.EventRepository().Create(ctx, evt))
	return evt
}

func insertDelivery(t *testing.T, ctx context.Context, factory persistence.RepositoryFactory, subID, evtID uuid.UUID, status entities.DeliveryStatus, completedAt *time.Time) *entities.WebhookDelivery {
	t.Helper()
	d := &entities.WebhookDelivery{
		ID:             uuid.New(),
		SubscriptionID: subID,
		EventID:        evtID,
		Status:         status,
		NextAttemptAt:  time.Now().UTC(),
		CompletedAt:    completedAt,
	}
	require.NoError(t, factory.WebhookDeliveryRepository().Create(ctx, d))
	return d
}

// TestRetention_DeletesOldEvents_PreservesNew inserts old and new events,
// runs a sweep with a 30-day cutoff, and checks only the old one is gone.
func TestRetention_DeletesOldEvents_PreservesNew(t *testing.T) {
	ctx := context.Background()
	_, factory := retentionTestDB(t)
	enc := retentionTestEncryptor(t)
	op, _ := insertRetentionOperatorAndSub(t, ctx, factory, enc)

	old := insertEvent(t, ctx, factory, op.ID, time.Now().UTC().AddDate(0, 0, -100))
	recent := insertEvent(t, ctx, factory, op.ID, time.Now().UTC().AddDate(0, 0, -1))

	worker := NewEventsRetentionWorker(factory, 30, 7, time.Hour)
	worker.sweep(ctx)

	// Old event gone.
	_, err := factory.EventRepository().GetByID(ctx, old.ID)
	assert.Error(t, err, "old event should be deleted")

	// Recent event still present.
	_, err = factory.EventRepository().GetByID(ctx, recent.ID)
	assert.NoError(t, err, "recent event should survive")
}

// TestRetention_DeletesSucceededDeliveries_PreservesDeadLetter checks that
// only succeeded deliveries past the cutoff are removed.
func TestRetention_DeletesSucceededDeliveries_PreservesDeadLetter(t *testing.T) {
	ctx := context.Background()
	_, factory := retentionTestDB(t)
	enc := retentionTestEncryptor(t)
	op, sub := insertRetentionOperatorAndSub(t, ctx, factory, enc)

	// Need events for the deliveries to reference.
	evt1 := insertEvent(t, ctx, factory, op.ID, time.Now().UTC())
	evt2 := insertEvent(t, ctx, factory, op.ID, time.Now().UTC())
	evt3 := insertEvent(t, ctx, factory, op.ID, time.Now().UTC())

	old30d := time.Now().UTC().AddDate(0, 0, -30)
	old1d := time.Now().UTC().AddDate(0, 0, -1)
	old100d := time.Now().UTC().AddDate(0, 0, -100)

	succeededOld := insertDelivery(t, ctx, factory, sub.ID, evt1.ID, entities.DeliveryStatusSucceeded, &old30d)
	succeededRecent := insertDelivery(t, ctx, factory, sub.ID, evt2.ID, entities.DeliveryStatusSucceeded, &old1d)
	deadLetterOld := insertDelivery(t, ctx, factory, sub.ID, evt3.ID, entities.DeliveryStatusDeadLetter, &old100d)

	// Retention: events=0 (disabled), succeeded deliveries=7 days.
	worker := NewEventsRetentionWorker(factory, 0, 7, time.Hour)
	worker.sweep(ctx)

	// succeeded@30d removed.
	_, err := factory.WebhookDeliveryRepository().GetByID(ctx, succeededOld.ID)
	assert.Error(t, err, "old succeeded delivery should be deleted")

	// succeeded@1d survives.
	_, err = factory.WebhookDeliveryRepository().GetByID(ctx, succeededRecent.ID)
	assert.NoError(t, err, "recent succeeded delivery should survive")

	// dead_letter@100d survives (only succeeded rows are auto-deleted).
	_, err = factory.WebhookDeliveryRepository().GetByID(ctx, deadLetterOld.ID)
	assert.NoError(t, err, "dead-letter delivery should never be auto-deleted")
}
