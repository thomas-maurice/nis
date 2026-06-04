package services

import (
	"context"
	"fmt"
	"time"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// JobTypeOIDCStateSweep is the job type for sweeping expired OIDC login states.
const JobTypeOIDCStateSweep = "oidc.state_sweep"

// RegisterOIDCStateSweepHandler attaches the oidc.state_sweep handler to the
// runner. Must be called before runner.Run starts (Register panics otherwise).
//
// The handler deletes oidc_login_states rows whose expires_at < clock.Now(),
// preventing table bloat from abandoned login flows. sweepInterval controls how
// often the watchdog reschedules the handler.
func RegisterOIDCStateSweepHandler(runner *JobRunner, factory persistence.RepositoryFactory, sweepInterval time.Duration) {
	if sweepInterval <= 0 {
		sweepInterval = 15 * time.Minute
	}
	h := &oidcStateSweepHandler{factory: factory}
	runner.Register(JobTypeOIDCStateSweep, HandlerSpec{
		Handler:     h.Run,
		MaxAttempts: 3,
		AuditPolicy: AuditFailuresOnly,
		RecurEvery:  sweepInterval,
	})
}

// oidcStateSweepHandler implements the oidc.state_sweep job.
type oidcStateSweepHandler struct {
	factory persistence.RepositoryFactory
}

func (h *oidcStateSweepHandler) Run(ctx context.Context, _ []byte) error {
	cutoff := clock.Now()
	n, err := h.factory.OIDCLoginStateRepository().DeleteExpired(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("delete expired oidc login states: %w", err)
	}
	if n > 0 {
		logging.LogFromContext(ctx).Info("oidc state sweep: deleted expired states", "count", n, "cutoff", cutoff)
	}
	return nil
}
