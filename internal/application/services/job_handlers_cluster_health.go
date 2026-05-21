package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
)

// Job types for the cluster-health migration (A15). Replaces the in-process
// 60s goroutine that called CheckAllClustersHealth — same cadence, but each
// per-cluster probe is now a durable, claim-once row on the jobs substrate.
//
// Multi-replica correctness comes from the substrate, not a separate leader-
// election layer: the partial unique index on
// `(type, dedup_key) WHERE status IN ('pending','running')` rejects duplicate
// enqueues per (sweep / cluster), and ClaimDue's FOR UPDATE SKIP LOCKED on
// Postgres ensures only one worker claims each row. SQLite stays correct via
// SetMaxOpenConns(1). A3 (process-level leader election) is therefore NOT a
// blocker — PROPOSALS.md A15 originally claimed it was; that claim is wrong.
const (
	// JobTypeClusterHealthSweep ticks on a recurring schedule, enumerates
	// every cluster row, and enqueues one cluster.health_check per row with
	// a per-cluster dedup key. Same shape as backup.sweep.
	JobTypeClusterHealthSweep = "cluster.health.sweep"

	// JobTypeClusterHealthCheck probes one cluster: NATS dial + JWT
	// resolver probe + cluster row update + cluster.health_changed event
	// emit on state transition. One-shot per (cluster, due window).
	JobTypeClusterHealthCheck = "cluster.health_check"
)

// ClusterHealthCheckPayload is the JSON shape stored in the job payload
// for cluster.health_check rows.
type ClusterHealthCheckPayload struct {
	ClusterID string `json:"cluster_id"`
}

// clusterHealthCheckDedupKey is the canonical dedup_key for a per-cluster
// health-check job. Exported indirectly via this helper so the cluster
// service can enqueue an eager check on CreateCluster without learning
// the substrate-internal key shape.
func clusterHealthCheckDedupKey(clusterID uuid.UUID) string {
	return "cluster.health:" + clusterID.String()
}

// ClusterHealthHandlerConfig is the operator-facing knob set the handlers
// read at registration time.
type ClusterHealthHandlerConfig struct {
	// Interval is how often the watchdog reschedules cluster.health.sweep.
	// Default 60s (cluster.health_check_interval_seconds in config).
	Interval time.Duration

	// LeaseDuration is the per-row lease for cluster.health_check. 60s
	// comfortably covers a NATS dial (3s timeout) + a JWT resolver probe
	// even on a slow network; a stuck handler is reclaimed by a peer
	// worker after the lease expires, which is the desired recovery mode
	// (health checks are idempotent and cheap to re-run).
	LeaseDuration time.Duration
}

func (c *ClusterHealthHandlerConfig) applyDefaults() {
	if c.Interval <= 0 {
		c.Interval = 60 * time.Second
	}
	if c.LeaseDuration <= 0 {
		c.LeaseDuration = 60 * time.Second
	}
}

// RegisterClusterHealthHandlers attaches the two cluster-health handlers to
// the runner. MUST be called before runner.Run starts (Register panics
// otherwise — see job_runner.go).
//
// Audit policy is AuditFailuresOnly on BOTH handlers: CheckClusterHealth
// emits its own cluster.health_changed event on real state transitions,
// which is the audit surface operators actually care about. Substrate-level
// job.started/succeeded on top would emit (clusters × sweep_interval)
// rows per day per replica into events — same calculus as the retention /
// backup / jwt-expiry handlers.
//
// Trade-off documented for future on-callers: with MaxAttempts=1 +
// AuditFailuresOnly, a resolver flake that sets health_check_error on the
// cluster row but returns nil from CheckClusterHealth produces NO job-level
// signal. That's intentional — durable state lives on the cluster row, not
// in the audit log. Do NOT raise MaxAttempts without also reconsidering
// the audit policy.
func RegisterClusterHealthHandlers(runner *JobRunner, clusterService *ClusterService, cfg ClusterHealthHandlerConfig) {
	cfg.applyDefaults()

	sweep := &clusterHealthSweepHandler{runner: runner, clusterService: clusterService}
	check := &clusterHealthCheckHandler{clusterService: clusterService}

	runner.Register(JobTypeClusterHealthSweep, HandlerSpec{
		Handler:     sweep.Run,
		MaxAttempts: 3,
		AuditPolicy: AuditFailuresOnly,
		RecurEvery:  cfg.Interval,
	})
	runner.Register(JobTypeClusterHealthCheck, HandlerSpec{
		Handler:       check.Run,
		MaxAttempts:   1,
		AuditPolicy:   AuditFailuresOnly,
		LeaseDuration: cfg.LeaseDuration,
	})
}

type clusterHealthSweepHandler struct {
	runner         *JobRunner
	clusterService *ClusterService
}

// Run enumerates all clusters and EnsureScheduled-s a cluster.health_check
// per row with dedup_key="cluster.health:<id>". The partial unique index
// makes the per-cluster enqueue idempotent across replicas; an EnsureScheduled
// for a cluster whose previous check is still pending/running is a cheap
// no-op (skipped: true).
func (h *clusterHealthSweepHandler) Run(ctx context.Context, _ []byte) error {
	// Hard 1000-cluster cap mirrors the previous in-process CheckAllClustersHealth.
	// TODO: replace with cursor pagination once A7 lands; until then the cap
	// is a fail-loud signal rather than silent data loss (a 1001st cluster's
	// health check is dropped on the floor by today's code too).
	clusters, err := h.clusterService.repo.List(ctx, repositories.ListOptions{Limit: 1000, Offset: 0})
	if err != nil {
		return fmt.Errorf("list clusters: %w", err)
	}
	log := logging.LogFromContext(ctx)
	now := clock.Now()
	for _, c := range clusters {
		payload := ClusterHealthCheckPayload{ClusterID: c.ID.String()}
		enqueued, err := h.runner.EnsureScheduled(
			ctx,
			JobTypeClusterHealthCheck,
			payload,
			now,
			clusterHealthCheckDedupKey(c.ID),
		)
		if err != nil {
			log.Warn("cluster health sweep: EnsureScheduled failed",
				"cluster_id", c.ID, "error", err)
			continue
		}
		if !enqueued {
			log.Debug("cluster health sweep: previous check still pending/running, skipping",
				"cluster_id", c.ID)
		}
	}
	return nil
}

type clusterHealthCheckHandler struct {
	clusterService *ClusterService
}

// Run probes one cluster. A cluster deleted between sweep enqueue and
// handler claim is normal lifecycle, NOT a job failure — treat as success.
// All other errors propagate so the substrate dead-letters the row.
func (h *clusterHealthCheckHandler) Run(ctx context.Context, payload []byte) error {
	var p ClusterHealthCheckPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("%w: unmarshal payload: %w", ErrPermanentJobFailure, err)
	}
	id, err := uuid.Parse(p.ClusterID)
	if err != nil {
		return fmt.Errorf("%w: parse cluster_id: %w", ErrPermanentJobFailure, err)
	}
	if err := h.clusterService.CheckClusterHealth(ctx, id); err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			logging.LogFromContext(ctx).Debug("cluster health check: cluster deleted before probe, skipping",
				"cluster_id", id)
			return nil
		}
		return err
	}
	return nil
}
