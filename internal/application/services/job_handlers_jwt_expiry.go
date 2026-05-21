package services

import (
	"context"
	"time"
)

// JobTypeJWTExpirySweep is the job-runner type for the recurring JWT
// expiry sweeper (A14). The handler is a thin wrapper around
// JWTExpirySweeper.Tick — Tick is the load-bearing logic, the substrate
// only owns scheduling + durability + audit.
const JobTypeJWTExpirySweep = "jwt.expiry_sweep"

// JWTExpiryHandlerConfig configures the recurring schedule + lease.
type JWTExpiryHandlerConfig struct {
	// SweepInterval is how often the watchdog reschedules the handler.
	// 0 picks 1h (mirrors the goroutine path's default).
	SweepInterval time.Duration

	// LeaseDuration overrides the global jobs.lease_duration_seconds for
	// this handler. The sweeper can legitimately run for minutes when
	// auto-renew is enabled and there are many users (each renew is one
	// JWT mint + one account-JWT regen + N cluster pushes). 0 picks 15m
	// (same shape as backup.execute).
	LeaseDuration time.Duration
}

func (c *JWTExpiryHandlerConfig) applyDefaults() {
	if c.SweepInterval <= 0 {
		c.SweepInterval = time.Hour
	}
	if c.LeaseDuration <= 0 {
		c.LeaseDuration = 15 * time.Minute
	}
}

// RegisterJWTExpiryHandler attaches the jwt.expiry_sweep handler to the
// runner. MUST be called before runner.Run starts (Register panics
// otherwise — see job_runner.go).
//
// Why AuditFailuresOnly: the sweeper emits its own semantic events
// (user.cred.expiring_soon / expired / renewed, user.revocation_pruned)
// with the per-user payload that an audit consumer actually needs. The
// substrate-level job.started/succeeded triplet on top would triple-emit
// per machine-day per operator — same calculus as the retention + backup
// handlers.
//
// The runner's per-tick watchdog auto-dedupes on dedup_key="recur-"+jobType,
// so there is no separate singleton lock to wire here. JWTExpirySweeper's
// in-process sync.Mutex still serialises this handler tick against the
// admin out-of-band tick path (OperatorHandler.RunJWTExpirySweep calls
// sweeper.Tick directly).
func RegisterJWTExpiryHandler(runner *JobRunner, sweeper *JWTExpirySweeper, cfg JWTExpiryHandlerConfig) {
	cfg.applyDefaults()
	h := &jwtExpirySweepHandler{sweeper: sweeper}
	runner.Register(JobTypeJWTExpirySweep, HandlerSpec{
		Handler:       h.Run,
		MaxAttempts:   3,
		AuditPolicy:   AuditFailuresOnly,
		RecurEvery:    cfg.SweepInterval,
		LeaseDuration: cfg.LeaseDuration,
	})
}

type jwtExpirySweepHandler struct {
	sweeper *JWTExpirySweeper
}

// Run delegates to the existing Tick. The SweepResult counts are
// observability only — Tick already records per-phase metrics
// (nis_user_jwt_revocations_pruned_total, …_expiring_soon_events_total,
// …_expired_events_total, …_auto_renewals_total) and emits the per-user
// semantic events. The aggregate counts are surfaced to admins via the
// synchronous OperatorHandler.RunJWTExpirySweep RPC, which bypasses the
// substrate.
func (h *jwtExpirySweepHandler) Run(ctx context.Context, _ []byte) error {
	_, err := h.sweeper.Tick(ctx)
	return err
}
