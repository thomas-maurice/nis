package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

func makeEvent(eventType string, occurredAt time.Time) *entities.Event {
	return &entities.Event{
		ID:           uuid.New(),
		OccurredAt:   occurredAt,
		Type:         eventType,
		ActorType:    entities.ActorTypeSystem,
		ResourceType: "operator",
		ResourceID:   uuid.New().String(),
	}
}

func TestEventRepo_CreateAndGet(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()

	actorID := uuid.New()
	operatorID := uuid.New()
	accountID := uuid.New()
	payload := []byte(`{"key":"value"}`)
	now := time.Now().Truncate(time.Millisecond).UTC()

	e := &entities.Event{
		ID:           uuid.New(),
		OccurredAt:   now,
		Type:         entities.EventTypeOperatorCreated,
		ActorType:    entities.ActorTypeUser,
		ActorID:      &actorID,
		OperatorID:   &operatorID,
		AccountID:    &accountID,
		ResourceType: "operator",
		ResourceID:   operatorID.String(),
		Payload:      payload,
	}

	err := factory.EventRepository().Create(ctx, e)
	require.NoError(t, err)

	got, err := factory.EventRepository().GetByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, e.ID, got.ID)
	assert.Equal(t, e.Type, got.Type)
	assert.Equal(t, e.ActorType, got.ActorType)
	require.NotNil(t, got.ActorID)
	assert.Equal(t, actorID, *got.ActorID)
	require.NotNil(t, got.OperatorID)
	assert.Equal(t, operatorID, *got.OperatorID)
	require.NotNil(t, got.AccountID)
	assert.Equal(t, accountID, *got.AccountID)
	assert.JSONEq(t, string(payload), string(got.Payload))
}

func TestEventRepo_GetByID_NotFound(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()

	_, err := factory.EventRepository().GetByID(ctx, uuid.New())
	assert.ErrorIs(t, err, repositories.ErrNotFound)
}

func TestEventRepo_ListByType(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	now := time.Now().UTC()

	e1 := makeEvent(entities.EventTypeOperatorCreated, now.Add(-2*time.Second))
	e2 := makeEvent(entities.EventTypeAccountCreated, now.Add(-1*time.Second))
	e3 := makeEvent(entities.EventTypeOperatorCreated, now)

	for _, e := range []*entities.Event{e1, e2, e3} {
		require.NoError(t, factory.EventRepository().Create(ctx, e))
	}

	result, err := factory.EventRepository().List(ctx, repositories.EventFilter{
		Types: []string{entities.EventTypeOperatorCreated},
	})
	require.NoError(t, err)
	assert.Len(t, result.Events, 2)
	for _, ev := range result.Events {
		assert.Equal(t, entities.EventTypeOperatorCreated, ev.Type)
	}
}

func TestEventRepo_ListCursor(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Insert 5 events with distinct timestamps so ordering is deterministic.
	for i := range 5 {
		e := makeEvent(entities.EventTypeClusterSynced, now.Add(time.Duration(i)*time.Second))
		require.NoError(t, factory.EventRepository().Create(ctx, e))
	}

	// First page: limit 2, ordered DESC so newest first.
	page1, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, page1.Events, 2)
	assert.NotEmpty(t, page1.NextCursor)

	// Second page.
	page2, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 2, Cursor: page1.NextCursor})
	require.NoError(t, err)
	assert.Len(t, page2.Events, 2)
	assert.NotEmpty(t, page2.NextCursor)

	// Third page (1 remaining).
	page3, err := factory.EventRepository().List(ctx, repositories.EventFilter{Limit: 2, Cursor: page2.NextCursor})
	require.NoError(t, err)
	assert.Len(t, page3.Events, 1)
	assert.Empty(t, page3.NextCursor)

	// No overlap between pages.
	seen := map[uuid.UUID]bool{}
	for _, p := range []*repositories.EventListResult{page1, page2, page3} {
		for _, ev := range p.Events {
			assert.False(t, seen[ev.ID], "duplicate event across pages: %s", ev.ID)
			seen[ev.ID] = true
		}
	}
	assert.Len(t, seen, 5)
}

func TestEventRepo_DeleteOlderThan(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	now := time.Now().UTC()

	old1 := makeEvent(entities.EventTypeOperatorCreated, now.Add(-2*time.Hour))
	old2 := makeEvent(entities.EventTypeAccountCreated, now.Add(-1*time.Hour))
	new1 := makeEvent(entities.EventTypeUserCreated, now.Add(-30*time.Minute))
	new2 := makeEvent(entities.EventTypeClusterSynced, now)

	for _, e := range []*entities.Event{old1, old2, new1, new2} {
		require.NoError(t, factory.EventRepository().Create(ctx, e))
	}

	cutoff := now.Add(-45 * time.Minute)
	deleted, err := factory.EventRepository().DeleteOlderThan(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)

	_, err = factory.EventRepository().GetByID(ctx, old1.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound)
	_, err = factory.EventRepository().GetByID(ctx, old2.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound)

	_, err = factory.EventRepository().GetByID(ctx, new1.ID)
	assert.NoError(t, err)
	_, err = factory.EventRepository().GetByID(ctx, new2.ID)
	assert.NoError(t, err)
}

func TestEventRepo_CreateInsideTx_CommitPersists(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	e := makeEvent(entities.EventTypeOperatorCreated, time.Now().UTC())

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		return tx.EventRepository().Create(ctx, e)
	})
	require.NoError(t, err)

	got, err := factory.EventRepository().GetByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, e.ID, got.ID)
}

func TestEventRepo_CreateInsideTx_RollbackDiscards(t *testing.T) {
	factory := newTestFactory(t)
	ctx := context.Background()
	e := makeEvent(entities.EventTypeOperatorCreated, time.Now().UTC())
	sentinel := errors.New("force rollback")

	err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
		if createErr := tx.EventRepository().Create(ctx, e); createErr != nil {
			return createErr
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	_, err = factory.EventRepository().GetByID(ctx, e.ID)
	assert.ErrorIs(t, err, repositories.ErrNotFound)
}
