package services

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

func TestOIDCStateSweep_DeletesExpiredNotFresh(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)

	repo := factory.OIDCLoginStateRepository()

	// Insert an expired state.
	expiredState := &entities.OIDCLoginState{
		State:          "expired-" + uuid.New().String(),
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
		Nonce:          "nonce1",
		PKCEVerifier:   "verifier1",
		CreatedAt:      clock.Now().Add(-20 * time.Minute),
		ExpiresAt:      clock.Now().Add(-5 * time.Minute), // already expired
	}
	require.NoError(t, repo.Create(ctx, expiredState))

	// Insert a fresh state (not yet expired).
	freshState := &entities.OIDCLoginState{
		State:          "fresh-" + uuid.New().String(),
		OrganizationID: uuid.MustParse(entities.DefaultOrganizationID),
		Nonce:          "nonce2",
		PKCEVerifier:   "verifier2",
		CreatedAt:      clock.Now(),
		ExpiresAt:      clock.Now().Add(10 * time.Minute), // still valid
	}
	require.NoError(t, repo.Create(ctx, freshState))

	// Run the sweep handler directly (cutoff = clock.Now()).
	h := &oidcStateSweepHandler{factory: factory}
	err := h.Run(ctx, nil)
	require.NoError(t, err)

	// Expired row should be gone.
	_, err = repo.GetAndDelete(ctx, expiredState.State)
	assert.Error(t, err, "expired state should have been swept")

	// Fresh row should still exist.
	fresh, err := repo.GetAndDelete(ctx, freshState.State)
	require.NoError(t, err, "fresh state should survive the sweep")
	assert.Equal(t, freshState.State, fresh.State)
}

func TestOIDCStateSweep_NoRows_NoError(t *testing.T) {
	ctx := context.Background()
	factory := webhookTestDB(t)
	h := &oidcStateSweepHandler{factory: factory}
	err := h.Run(ctx, nil)
	require.NoError(t, err)
}

func TestRegisterOIDCStateSweepHandler_RegistersWithRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	factory := webhookTestDB(t)
	runner := NewJobRunner(factory, JobRunnerConfig{
		PollInterval:    5 * time.Second,
		ClaimBatch:      5,
		LeaseDuration:   30 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	})
	// Must not panic: handler is registered before Run starts.
	RegisterOIDCStateSweepHandler(runner, factory, 15*time.Minute)
	_ = ctx // runner not started; registration is the gate
}
