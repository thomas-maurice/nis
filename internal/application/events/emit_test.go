package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
	"github.com/thomas-maurice/nis/internal/interfaces/grpc/middleware"
	"github.com/thomas-maurice/nis/migrations"
)

func newTestFactory(t *testing.T) persistence.RepositoryFactory {
	t.Helper()
	db, err := sql.NewDB("sqlite", ":memory:")
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	goose.SetBaseFS(migrations.Migrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(sqlDB, "sqlite"))
	return persistence.NewSQLRepositoryFactoryFromDB(db)
}

func makeOperator(t *testing.T, factory persistence.RepositoryFactory, name string) *entities.Operator {
	t.Helper()
	op := &entities.Operator{
		ID:             uuid.New(),
		Name:           name,
		Description:    "test",
		EncryptedSeed:  "not-real-but-non-empty",
		PublicKey:      "OAA" + uuid.New().String(),
		JWT:            "fake-jwt",
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
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
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
}

func createSubscription(t *testing.T, factory persistence.RepositoryFactory, sub *entities.WebhookSubscription) {
	t.Helper()
	require.NoError(t, factory.WebhookSubscriptionRepository().Create(context.Background(), sub))
}

func countDeliveries(t *testing.T, factory persistence.RepositoryFactory) int {
	t.Helper()
	ds, err := factory.WebhookDeliveryRepository().List(context.Background(), repositories.WebhookDeliveryFilter{Limit: 1000})
	require.NoError(t, err)
	return len(ds)
}

func baseEvent(operatorID *uuid.UUID) events.Event {
	return events.Event{
		Type:         entities.EventTypeAccountCreated,
		OperatorID:   operatorID,
		ResourceType: "account",
		ResourceID:   uuid.New().String(),
	}
}

// --- tests ---

func TestEmit_PersistsEventInsideTx_Commit(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator(t, factory, "op-commit")
	in := baseEvent(&op.ID)

	var eventID uuid.UUID
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := events.EmitTx(ctx, tx, in); err != nil {
			return err
		}
		// capture ID by listing — we don't know the ID until emit runs
		result, err := tx.EventRepository().List(ctx, repositories.EventFilter{Limit: 1})
		if err != nil {
			return err
		}
		if len(result.Events) == 1 {
			eventID = result.Events[0].ID
		}
		return nil
	})
	require.NoError(t, err)

	got, err := factory.EventRepository().GetByID(ctx, eventID)
	require.NoError(t, err)
	assert.Equal(t, in.Type, got.Type)
}

func TestEmit_PersistsEventInsideTx_Rollback(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator(t, factory, "op-rollback")
	in := baseEvent(&op.ID)
	sentinel := errors.New("force rollback")

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if err := events.EmitTx(ctx, tx, in); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	result, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, result.Events)
}

func TestEmit_NoMatchingSubscription_NoDelivery(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator(t, factory, "op-no-match")

	sub := makeSubscription(op.ID, "user-hook", []string{entities.EventTypeUserCreated}, true)
	createSubscription(t, factory, sub)

	in := baseEvent(&op.ID) // account.created — does not match user.created
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)
	assert.Equal(t, 0, countDeliveries(t, factory))
}

func TestEmit_MatchingSubscription_OneDelivery(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator(t, factory, "op-match")

	sub := makeSubscription(op.ID, "account-hook", []string{entities.EventTypeAccountCreated}, true)
	createSubscription(t, factory, sub)

	before := time.Now()
	in := baseEvent(&op.ID)
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)

	ds, err := factory.WebhookDeliveryRepository().List(ctx, repositories.WebhookDeliveryFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, entities.DeliveryStatusPending, ds[0].Status)
	assert.WithinDuration(t, before, ds[0].NextAttemptAt, time.Second)
}

func TestEmit_WildcardSubscription_MatchesAnyType(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator(t, factory, "op-wildcard")

	sub := makeSubscription(op.ID, "wildcard-hook", []string{entities.EventTypeWildcard}, true)
	createSubscription(t, factory, sub)

	in := events.Event{
		Type:         entities.EventTypeClusterSynced,
		OperatorID:   &op.ID,
		ResourceType: "cluster",
		ResourceID:   uuid.New().String(),
	}
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)
	assert.Equal(t, 1, countDeliveries(t, factory))
}

func TestEmit_DisabledSubscription_Skipped(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator(t, factory, "op-disabled")

	sub := makeSubscription(op.ID, "disabled-hook", []string{entities.EventTypeWildcard}, false)
	createSubscription(t, factory, sub)

	in := baseEvent(&op.ID)
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)
	assert.Equal(t, 0, countDeliveries(t, factory))
}

func TestEmit_DifferentOperator_NotMatched(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	opA := makeOperator(t, factory, "op-a-diff")
	opB := makeOperator(t, factory, "op-b-diff")

	sub := makeSubscription(opA.ID, "hook-a", []string{entities.EventTypeWildcard}, true)
	createSubscription(t, factory, sub)

	in := baseEvent(&opB.ID) // event belongs to opB
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)
	assert.Equal(t, 0, countDeliveries(t, factory))
}

func TestEmit_NilOperatorID_NoFanout(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()

	in := events.Event{
		Type:         entities.EventTypeOperatorCreated,
		OperatorID:   nil,
		ResourceType: "operator",
		ResourceID:   uuid.New().String(),
	}
	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)

	result, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, result.Events, 1)
	assert.Equal(t, 0, countDeliveries(t, factory))
}

func TestEmitTx_ActorFromContext_User(t *testing.T) {
	factory := newTestFactory(t)
	userID := uuid.New()
	user := &entities.APIUser{ID: userID, Role: entities.RoleAdmin}
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, user)

	op := makeOperator(t, factory, "op-actor-user")
	in := baseEvent(&op.ID)

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)

	result, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Events, 1)
	got := result.Events[0]
	assert.Equal(t, entities.ActorTypeUser, got.ActorType)
	require.NotNil(t, got.ActorID)
	assert.Equal(t, userID, *got.ActorID)
}

func TestEmitTx_NoContextUser_DefaultsToSystem(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background() // no user in context
	op := makeOperator(t, factory, "op-actor-system")
	in := baseEvent(&op.ID)

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return events.EmitTx(ctx, tx, in)
	})
	require.NoError(t, err)

	result, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Events, 1)
	got := result.Events[0]
	assert.Equal(t, entities.ActorTypeSystem, got.ActorType)
	assert.Nil(t, got.ActorID)
}

func TestEmitSystem_ForcesSystemEvenWhenUserInContext(t *testing.T) {
	factory := newTestFactory(t)
	userID := uuid.New()
	user := &entities.APIUser{ID: userID, Role: entities.RoleAdmin}
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, user)

	op := makeOperator(t, factory, "op-system-force")
	in := baseEvent(&op.ID)

	err := events.EmitSystem(ctx, factory, in)
	require.NoError(t, err)

	result, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Events, 1)
	got := result.Events[0]
	assert.Equal(t, entities.ActorTypeSystem, got.ActorType)
	assert.Nil(t, got.ActorID)
}

func TestEmitSystem_OpensOwnTx_PersistsAndFansOut(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	op := makeOperator(t, factory, "op-system-own-tx")

	sub := makeSubscription(op.ID, "sys-hook", []string{entities.EventTypeWildcard}, true)
	createSubscription(t, factory, sub)

	in := baseEvent(&op.ID)
	err := events.EmitSystem(ctx, factory, in)
	require.NoError(t, err)

	result, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, result.Events, 1)
	assert.Equal(t, 1, countDeliveries(t, factory))
}

func TestEmit_InvalidInput_Rejected(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	opID := uuid.New()

	cases := []struct {
		name string
		in   events.Event
	}{
		{
			name: "empty type",
			in:   events.Event{Type: "", ResourceType: "operator", ResourceID: uuid.New().String(), OperatorID: &opID},
		},
		{
			name: "empty resource_type",
			in:   events.Event{Type: entities.EventTypeOperatorCreated, ResourceType: "", ResourceID: uuid.New().String(), OperatorID: &opID},
		},
		{
			name: "empty resource_id",
			in:   events.Event{Type: entities.EventTypeOperatorCreated, ResourceType: "operator", ResourceID: "", OperatorID: &opID},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
				return events.EmitTx(ctx, tx, tc.in)
			})
			assert.Error(t, err, "expected error for %s", tc.name)
		})
	}
}

// TestEmit_DeliveryFailure_RollsBackEvent: verifying rollback of the event row
// when a delivery insert fails requires fault-injection into the repo layer,
// which isn't supported by the real SQLite repo without a mock or wrapper.
// This property is already covered transitively: emit() calls tx.EventRepository().Create
// then fanoutDeliveries inside the same tx; if fanoutDeliveries returns an
// error, the caller's WithTx rolls back both. The WithTx rollback semantics are
// tested in TestEmit_PersistsEventInsideTx_Rollback and in
// internal/infrastructure/persistence/withtx_test.go. No fault-injection
// mechanism exists in the real repo, so this case is documented but not
// exercised as a separate test.
