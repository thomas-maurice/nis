// Package services — cluster_sync_enqueue.go contains the in-tx enqueue
// helpers that A13-full uses to schedule per-cluster account-JWT pushes (and
// deletes) on the A2 jobs substrate.
//
// Why in-tx, not post-commit:
//   - The job row commits atomically with the account/SSK/template mutation
//     that caused the push. If the process dies between commit and substrate
//     pickup, the job survives — a peer worker (or this process after restart)
//     claims it. The pre-A13-full "in-process push after commit" path lost
//     pushes on crash.
//   - There is no orphan-pending-row window. No catch-up scan is needed.
//
// Why one job per (account, cluster), not one job per (operator, cluster):
//   - Per-cluster retry granularity: a single bad cluster doesn't block
//     pushes to its peers, and the substrate's per-row backoff handles
//     transient cluster-side flakes independently.
//   - Burst coalescing happens via the partial unique index on
//     (type, dedup_key) WHERE status IN ('pending','running'): two mutations
//     to the same account in tight succession enqueue one row per cluster,
//     and the second attempt is silently dropped (the running handler will
//     re-read the latest account JWT when it picks up the row, so no work is
//     lost).
//   - There is one subtle staleness window though: mutation B that commits
//     AFTER the handler claimed J1 (running) is suppressed by the unique
//     index, and J1 may have already read the pre-B JWT. The handler closes
//     that window with a post-push re-read + content-hashed follow-up
//     enqueue (see job_handlers_cluster_account_push.go).
//
// Why NOT in `events` package (alongside SetJobEnqueuer):
//   - A cluster push is not an audit event. The event substrate is generic;
//     mixing in domain-specific enqueue logic there would invert dependencies.
//   - Keeping these helpers next to the handlers keeps "what we enqueue" and
//     "what we run" co-located.
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// Job type registry constants for the A13-full handlers.
const (
	JobTypeClusterAccountPush   = "cluster.account.push"
	JobTypeClusterAccountDelete = "cluster.account.delete"
)

// accountPushMaxAttempts is the substrate retry count for cluster account
// push/delete jobs. 3 is deliberately lower than webhook.deliver's 5: a
// persistent push failure IS drift, and the P9 drift dashboard is the
// recovery surface — not a higher retry ceiling. The cluster-health handler
// uses 1 for the same reason (state lives on the cluster row); push lives
// in the middle because transient TLS / NATS-reply flakes do warrant a few
// retries.
const accountPushMaxAttempts = 3

// accountPushPayload is what the enqueue helpers marshal into the job row.
// The handler decodes this, then re-reads the account from the DB so it
// picks up the latest JWT (not the one we had at enqueue time).
//
// JWTHash is the sha256-hex of account.JWT at enqueue time. The handler
// computes the post-push hash and compares; on mismatch, a follow-up job is
// enqueued with the new hash in its dedup key, breaking the staleness race
// described in the package doc.
type accountPushPayload struct {
	OperatorID uuid.UUID `json:"operator_id"`
	AccountID  uuid.UUID `json:"account_id"`
	ClusterID  uuid.UUID `json:"cluster_id"`
	JWTHash    string    `json:"jwt_hash"`
}

// accountDeletePayload is the delete-claim variant. AccountPubkey (not ID)
// because the account row is gone from the DB by the time the handler runs.
type accountDeletePayload struct {
	OperatorID    uuid.UUID `json:"operator_id"`
	ClusterID     uuid.UUID `json:"cluster_id"`
	AccountPubkey string    `json:"account_pubkey"`
}

// accountPushDedupKey is the primary dedup key for burst coalescing. Two
// mutations to the same account that haven't yet claimed get collapsed into
// one pending row per cluster.
func accountPushDedupKey(accountID, clusterID uuid.UUID) string {
	return fmt.Sprintf("account-push:%s:%s", accountID, clusterID)
}

// accountPushFollowupDedupKey is the staleness-race follow-up key. Includes
// a short hash prefix of the fresh JWT so each JWT revision gets its own
// row even when an earlier (now-running) job for the same (account, cluster)
// is in flight. Truncated to keep the column short.
func accountPushFollowupDedupKey(accountID, clusterID uuid.UUID, jwtHash string) string {
	if len(jwtHash) > 16 {
		jwtHash = jwtHash[:16]
	}
	return fmt.Sprintf("account-push-followup:%s:%s:%s", accountID, clusterID, jwtHash)
}

// accountDeleteDedupKey collapses bursts of delete attempts for the same
// (account_pubkey, cluster). Uniqueness on pubkey is enforced by the NKey
// signature scheme — the chance of pubkey reuse across account lifetimes
// is astronomical and ignored.
func accountDeleteDedupKey(accountPubkey string, clusterID uuid.UUID) string {
	return fmt.Sprintf("account-delete:%s:%s", accountPubkey, clusterID)
}

// hashJWT returns the sha256-hex of the JWT bytes. Used as a content
// fingerprint for the staleness-race follow-up enqueue path.
func hashJWT(jwt string) string {
	sum := sha256.Sum256([]byte(jwt))
	return hex.EncodeToString(sum[:])
}

// EnqueueAccountPush inserts one cluster.account.push job row per cluster
// attached to the operator. Skips clusters with empty EncryptedCreds (no
// system creds means nothing to push to — typical for a freshly-created
// cluster before nisctl uploads creds). Idempotent: the partial unique
// index suppresses duplicate inserts for the same (account, cluster) while
// a previous row is still pending or running.
//
// Must be called INSIDE the caller's factory.WithTx, with tx being the
// tx-scoped factory. The job rows commit atomically with the mutation that
// produced them; a process death between commit and worker pickup leaves a
// durable resumable job.
//
// account.JWT is captured into the payload's JWTHash so the post-push
// staleness check has a comparison baseline. If account.JWT is empty
// (account is half-created mid-tx — shouldn't happen with the current call
// sites but defensive), no jobs are enqueued and the helper returns nil.
func EnqueueAccountPush(ctx context.Context, tx persistence.RepositoryFactory, operatorID uuid.UUID, account *entities.Account) error {
	if account == nil || account.JWT == "" {
		return nil
	}
	clusters, err := tx.ClusterRepository().ListByOperator(ctx, operatorID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return fmt.Errorf("list clusters for account push: %w", err)
	}
	if len(clusters) == 0 {
		return nil
	}

	jwtHash := hashJWT(account.JWT)
	now := clock.Now()

	for _, cluster := range clusters {
		if cluster.EncryptedCreds == "" {
			// No system creds → nothing to push. nisctl cluster sync is the
			// recovery once creds are uploaded; this is the same as today's
			// A13-lite behavior.
			continue
		}
		body, err := json.Marshal(accountPushPayload{
			OperatorID: operatorID,
			AccountID:  account.ID,
			ClusterID:  cluster.ID,
			JWTHash:    jwtHash,
		})
		if err != nil {
			return fmt.Errorf("marshal account push payload: %w", err)
		}
		j := &entities.Job{
			ID:           uuid.New(),
			Type:         JobTypeClusterAccountPush,
			Payload:      body,
			Status:       entities.JobStatusPending,
			ScheduledFor: now,
			MaxAttempts:  accountPushMaxAttempts,
			DedupKey:     accountPushDedupKey(account.ID, cluster.ID),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if _, err := tx.JobRepository().EnqueueIfAbsent(ctx, j); err != nil {
			return fmt.Errorf("enqueue account push for cluster %s: %w", cluster.ID, err)
		}
	}
	return nil
}

// EnqueueAccountDelete inserts one cluster.account.delete job row per
// cluster attached to the operator. Same per-cluster semantics as
// EnqueueAccountPush. Called from inside AccountService.DeleteAccount's tx,
// after the account row is removed but before commit; the operator delete
// path's FK CASCADE bypasses this helper (same gap A6 documents for
// per-row events).
func EnqueueAccountDelete(ctx context.Context, tx persistence.RepositoryFactory, operatorID uuid.UUID, accountPubkey string) error {
	if accountPubkey == "" {
		return nil
	}
	clusters, err := tx.ClusterRepository().ListByOperator(ctx, operatorID, repositories.ListOptions{Limit: 1000})
	if err != nil {
		return fmt.Errorf("list clusters for account delete: %w", err)
	}
	if len(clusters) == 0 {
		return nil
	}

	now := clock.Now()

	for _, cluster := range clusters {
		if cluster.EncryptedCreds == "" {
			continue
		}
		body, err := json.Marshal(accountDeletePayload{
			OperatorID:    operatorID,
			ClusterID:     cluster.ID,
			AccountPubkey: accountPubkey,
		})
		if err != nil {
			return fmt.Errorf("marshal account delete payload: %w", err)
		}
		j := &entities.Job{
			ID:           uuid.New(),
			Type:         JobTypeClusterAccountDelete,
			Payload:      body,
			Status:       entities.JobStatusPending,
			ScheduledFor: now,
			MaxAttempts:  accountPushMaxAttempts,
			DedupKey:     accountDeleteDedupKey(accountPubkey, cluster.ID),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if _, err := tx.JobRepository().EnqueueIfAbsent(ctx, j); err != nil {
			return fmt.Errorf("enqueue account delete for cluster %s: %w", cluster.ID, err)
		}
	}
	return nil
}
