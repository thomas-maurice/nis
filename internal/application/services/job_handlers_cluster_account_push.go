// Package services — job_handlers_cluster_account_push.go is the A13-full
// runtime: per-cluster, per-account JWT push (and delete) handlers running
// on the A2 jobs substrate.
//
// These handlers are the post-A13-lite path. The old A13-lite design pushed
// inline AFTER the mutation tx committed, in-process, best-effort. The bug
// it had: NIS crashing between commit and push left silent drift only the
// P9 drift dashboard could surface. The A13-full path enqueues the push
// rows INSIDE the mutation tx (cluster_sync_enqueue.go), so commit ⇒ job is
// atomic. Process death is now survivable: a peer worker (or the restarted
// process) claims the pending row and pushes.
//
// Staleness-race fix:
//   - Mutation A commits at T0 with JWT_v1; enqueues J1 (pending).
//   - Worker claims J1 at T1 (status → running).
//   - Mutation B commits at T2 (T2 > T1) with JWT_v2.
//   - B's EnqueueIfAbsent on dedup_key account-push:A:C tries to insert J2;
//     partial unique index sees J1 still 'running' and suppresses it.
//   - J1 had already loaded the account at some point ≥ T1 — it has JWT_v1.
//   - Push lands on cluster C → resolver has JWT_v1.
//   - J1 marks succeeded → no further enqueue → resolver permanently stale.
//
// The handler closes that window by re-reading account.JWT AFTER the push
// completes. If the post-push JWT differs from what was pushed, a follow-up
// row is enqueued with dedup_key account-push-followup:A:C:<hash> — the hash
// suffix sidesteps the primary dedup key's "still running" suppression
// because the row's hash is per-revision unique. The follow-up itself does
// the same post-push check, so chains converge once the JWT stabilises.
//
// Stay-synchronous carve-outs (not migrated to substrate):
//   - RotateScopedSigningKey (P3) — returns per-cluster outcomes in the RPC.
//   - SyncCluster (manual nisctl cluster sync) — user waits for outcomes.
//   - ReconcileAccountOnCluster (P9 manual reconcile) — same as SyncCluster.
//
// See SKILL.md §2 "Auto-sync via substrate (A13-full)" for the full design.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// ClusterAccountSyncConfig configures the cluster.account.push and
// cluster.account.delete handlers.
//
// LeaseDuration matches A15 (cluster.health_check): 60s. TLS dial + a single
// JWT push round-trip is well under that; the lease is generous for
// reclaim-after-dead-worker without unfairly long head-of-line blocking.
type ClusterAccountSyncConfig struct {
	MaxAttempts   int
	BackoffBase   time.Duration
	BackoffCap    time.Duration
	LeaseDuration time.Duration
}

func (c *ClusterAccountSyncConfig) applyDefaults() {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = accountPushMaxAttempts
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 10 * time.Second
	}
	if c.BackoffCap <= 0 {
		c.BackoffCap = 10 * time.Minute
	}
	if c.LeaseDuration <= 0 {
		c.LeaseDuration = 60 * time.Second
	}
}

// clusterAccountSyncer is the per-cluster push/delete surface the handlers
// depend on. *ClusterService satisfies it; tests can fake it. Declared at
// the package level because both handlers consume the same dep.
type clusterAccountSyncer interface {
	PushAccountToCluster(ctx context.Context, clusterID uuid.UUID, account *entities.Account) error
	DeleteAccountFromCluster(ctx context.Context, clusterID, operatorID uuid.UUID, accountPubkey string) error
}

// clusterAccountPushHandler dispatches cluster.account.push jobs.
type clusterAccountPushHandler struct {
	factory  persistence.RepositoryFactory
	syncer   clusterAccountSyncer
	registry *handlerRegistry // for emitting follow-up enqueue (currently uses factory directly)
}

// clusterAccountDeleteHandler dispatches cluster.account.delete jobs.
type clusterAccountDeleteHandler struct {
	factory persistence.RepositoryFactory
	syncer  clusterAccountSyncer
}

// handlerRegistry is a thin indirection so the push handler can stamp the
// staleness follow-up row's MaxAttempts from the registered HandlerSpec
// rather than hardcoding. Kept private — the handler is the only consumer.
type handlerRegistry struct {
	maxAttempts int
}

// RegisterClusterAccountPushHandlers wires the two A13-full handlers on the
// runner. Must be called before runner.Run starts (Register panics
// otherwise).
//
// The function takes *ClusterService directly (not the narrower
// clusterAccountSyncer interface) because the handlers also need the same
// service's encryptor + JWT plumbing transitively; passing the concrete is
// simpler and matches RegisterClusterHealthHandlers' shape.
func RegisterClusterAccountPushHandlers(
	runner *JobRunner,
	factory persistence.RepositoryFactory,
	clusterService *ClusterService,
	cfg ClusterAccountSyncConfig,
) {
	cfg.applyDefaults()

	push := &clusterAccountPushHandler{
		factory:  factory,
		syncer:   clusterService,
		registry: &handlerRegistry{maxAttempts: cfg.MaxAttempts},
	}
	del := &clusterAccountDeleteHandler{
		factory: factory,
		syncer:  clusterService,
	}

	runner.Register(JobTypeClusterAccountPush, HandlerSpec{
		Handler:       push.Run,
		MaxAttempts:   cfg.MaxAttempts,
		BackoffBase:   cfg.BackoffBase,
		BackoffCap:    cfg.BackoffCap,
		AuditPolicy:   AuditFailuresOnly,
		LeaseDuration: cfg.LeaseDuration,
	})
	runner.Register(JobTypeClusterAccountDelete, HandlerSpec{
		Handler:       del.Run,
		MaxAttempts:   cfg.MaxAttempts,
		BackoffBase:   cfg.BackoffBase,
		BackoffCap:    cfg.BackoffCap,
		AuditPolicy:   AuditFailuresOnly,
		LeaseDuration: cfg.LeaseDuration,
	})
}

// Run is the cluster.account.push JobHandler. Idempotent and crash-safe:
// re-claim of a half-completed row triggers a fresh DB read + fresh push,
// and the NATS resolver treats two pushes of the same JWT identically.
func (h *clusterAccountPushHandler) Run(ctx context.Context, payload []byte) error {
	var p accountPushPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w: %w", err, ErrPermanentJobFailure)
	}
	if p.AccountID == uuid.Nil || p.ClusterID == uuid.Nil || p.OperatorID == uuid.Nil {
		return fmt.Errorf("payload missing required id field: %w", ErrPermanentJobFailure)
	}

	log := logging.LogFromContext(ctx)

	cluster, err := h.factory.ClusterRepository().GetByID(ctx, p.ClusterID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			// Cluster deleted between enqueue and claim — normal lifecycle.
			log.Debug("account push: cluster gone, dropping job",
				"cluster_id", p.ClusterID, "account_id", p.AccountID)
			return nil
		}
		return fmt.Errorf("load cluster: %w", err)
	}
	if cluster.EncryptedCreds == "" {
		// Creds removed between enqueue and claim — same as never-set;
		// nothing to push to.
		return nil
	}

	account, err := h.factory.AccountRepository().GetByID(ctx, p.AccountID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			// Account deleted between enqueue and claim. The corresponding
			// cluster.account.delete jobs (enqueued by DeleteAccount) will
			// remove the JWT from the resolver; nothing for us to do.
			log.Debug("account push: account gone, dropping job",
				"account_id", p.AccountID, "cluster_id", p.ClusterID)
			return nil
		}
		return fmt.Errorf("load account: %w", err)
	}
	if account.JWT == "" {
		// Account row exists but has no JWT — code bug, not lifecycle.
		// Surface in JobsView as a permanent failure that needs human
		// investigation (the substrate would otherwise retry MaxAttempts
		// times before dead-lettering anyway).
		return fmt.Errorf("account %s has empty JWT: %w", p.AccountID, ErrPermanentJobFailure)
	}

	pushedJWT := account.JWT

	// Push. Transient on failure — substrate retries with backoff.
	if err := h.syncer.PushAccountToCluster(ctx, p.ClusterID, account); err != nil {
		return fmt.Errorf("push to cluster %s: %w", cluster.Name, err)
	}

	// Success — emit cluster.account.synced with trigger:"auto" so webhook
	// subscribers can distinguish substrate-driven pushes from operator-
	// initiated reconciles (which carry trigger:"manual").
	jwtIAT := int64(0)
	if account.UpdatedAt.Unix() > 0 {
		jwtIAT = account.UpdatedAt.Unix()
	}
	if err := events.EmitSystem(ctx, h.factory, events.Event{
		Type:         entities.EventTypeClusterAccountSynced,
		OperatorID:   &p.OperatorID,
		AccountID:    &p.AccountID,
		ResourceType: "cluster",
		ResourceID:   p.ClusterID.String(),
		Payload: map[string]any{
			"cluster_id":         p.ClusterID.String(),
			"cluster_name":       cluster.Name,
			"account_id":         p.AccountID.String(),
			"account_name":       account.Name,
			"account_public_key": account.PublicKey,
			"jwt_iat":            jwtIAT,
			"trigger":            "auto",
		},
	}); err != nil {
		// Audit-emit failure shouldn't poison the push outcome — log it
		// loudly but return success so we don't retry the push itself.
		log.Warn("account push: emit success event failed",
			"account_id", p.AccountID, "cluster_id", p.ClusterID, "error", err)
	}

	// Staleness-race check. If the JWT changed during our push, the dedup
	// index suppressed any concurrent enqueue; close the window with a
	// content-hashed follow-up.
	fresh, err := h.factory.AccountRepository().GetByID(ctx, p.AccountID)
	if err != nil || fresh == nil || fresh.JWT == "" {
		// Account vanished during our push — the delete-job path handles it.
		return nil
	}
	if fresh.JWT == pushedJWT {
		return nil
	}
	if err := h.enqueueFollowup(ctx, p.OperatorID, fresh, p.ClusterID); err != nil {
		// Logged + ignored. The P9 drift dashboard remains a recovery surface.
		log.Warn("account push: enqueue follow-up failed; relying on drift dashboard",
			"account_id", p.AccountID, "cluster_id", p.ClusterID, "error", err)
	}
	return nil
}

// enqueueFollowup writes a fresh cluster.account.push row dedup'd on the
// post-push JWT hash. Runs OUTSIDE the substrate's auto-detached write ctx —
// we don't need the caller's tx because there's no atomicity requirement
// (a missed follow-up degrades to "P9 surfaces drift", not "lost work").
func (h *clusterAccountPushHandler) enqueueFollowup(
	ctx context.Context,
	operatorID uuid.UUID,
	account *entities.Account,
	clusterID uuid.UUID,
) error {
	hash := hashJWT(account.JWT)
	body, err := json.Marshal(accountPushPayload{
		OperatorID: operatorID,
		AccountID:  account.ID,
		ClusterID:  clusterID,
		JWTHash:    hash,
	})
	if err != nil {
		return fmt.Errorf("marshal follow-up payload: %w", err)
	}
	now := clock.Now()
	j := &entities.Job{
		ID:           uuid.New(),
		Type:         JobTypeClusterAccountPush,
		Payload:      body,
		Status:       entities.JobStatusPending,
		ScheduledFor: now,
		MaxAttempts:  h.registry.maxAttempts,
		DedupKey:     accountPushFollowupDedupKey(account.ID, clusterID, hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_, err = h.factory.JobRepository().EnqueueIfAbsent(ctx, j)
	return err
}

// Run is the cluster.account.delete JobHandler. Idempotent: NATS responds
// "ok" both for "I deleted it" and "it wasn't there." Re-claim is safe.
func (h *clusterAccountDeleteHandler) Run(ctx context.Context, payload []byte) error {
	var p accountDeletePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w: %w", err, ErrPermanentJobFailure)
	}
	if p.ClusterID == uuid.Nil || p.OperatorID == uuid.Nil || p.AccountPubkey == "" {
		return fmt.Errorf("payload missing required field: %w", ErrPermanentJobFailure)
	}

	log := logging.LogFromContext(ctx)

	cluster, err := h.factory.ClusterRepository().GetByID(ctx, p.ClusterID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			log.Debug("account delete: cluster gone, dropping job",
				"cluster_id", p.ClusterID, "account_pubkey", p.AccountPubkey)
			return nil
		}
		return fmt.Errorf("load cluster: %w", err)
	}
	if cluster.EncryptedCreds == "" {
		return nil
	}

	if err := h.syncer.DeleteAccountFromCluster(ctx, p.ClusterID, p.OperatorID, p.AccountPubkey); err != nil {
		return fmt.Errorf("delete on cluster %s: %w", cluster.Name, err)
	}

	if err := events.EmitSystem(ctx, h.factory, events.Event{
		Type:         entities.EventTypeClusterAccountDeletedFromResolver,
		OperatorID:   &p.OperatorID,
		ResourceType: "cluster",
		ResourceID:   p.ClusterID.String(),
		Payload: map[string]any{
			"cluster_id":         p.ClusterID.String(),
			"cluster_name":       cluster.Name,
			"account_public_key": p.AccountPubkey,
		},
	}); err != nil {
		log.Warn("account delete: emit success event failed",
			"cluster_id", p.ClusterID, "account_pubkey", p.AccountPubkey, "error", err)
	}
	return nil
}
