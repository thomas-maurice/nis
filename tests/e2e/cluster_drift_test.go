//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// TestE2E_ClusterDrift_AllInSyncAfterSync is the headline case: a freshly
// synced cluster reports every account IN_SYNC. Tests that the comparison
// primitive doesn't false-positive drift after a successful push.
func TestE2E_ClusterDrift_AllInSyncAfterSync(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-ok")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     st.clusterID,
		IncludeInSync: true,
	}))
	if err != nil {
		t.Fatalf("GetClusterDriftStatus: %v", err)
	}
	if len(resp.Msg.Rows) == 0 {
		t.Fatalf("expected at least the $SYS account row, got empty rows")
	}
	for _, r := range resp.Msg.Rows {
		if r.Status != nisv1.DriftStatus_DRIFT_STATUS_IN_SYNC {
			t.Fatalf("expected IN_SYNC for %q, got %v (msg=%q)", r.AccountName, r.Status, r.ErrorMessage)
		}
	}
}

// TestE2E_ClusterDrift_MutateWithFailedPushReportsDBAhead covers the
// DB_AHEAD state under the new A13-lite world: P6 wired auto-sync so a
// plain UpdateAccount now pushes the regenerated JWT to NATS as part
// of the API call. To induce DB_AHEAD we need an auto-sync attempt
// that fails. Approach: stop NATS, mutate (auto-sync silently fails per
// best-effort semantics), restart NATS, scan — the DB JWT's iat is
// later than the resolver's, so drift classifies as DB_AHEAD.
//
// This replaces a pre-A13 test that mutated without calling sync and
// expected DB_AHEAD by default. That assumption no longer holds; the
// new shape of the test pins the same classification path through the
// new path-to-failure (push attempted, push failed).
func TestE2E_ClusterDrift_MutateWithFailedPushReportsDBAhead(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-dbahead")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// jwt v2's IssuedAt resolution is 1 second; sleep 1.1s so the
	// mutation's iat is strictly later than the resolver's stored iat
	// (otherwise compareJWTs classifies as OUT_OF_BAND not DB_AHEAD).
	time.Sleep(1100 * time.Millisecond)

	if err := exec.Command("docker", "stop", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker stop %s: %v", h.natsContainer, err)
	}

	newDesc := "drift bait (push will fail with NATS stopped)"
	if _, err := h.accountCli.UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
		Id:          st.accountID,
		Description: &newDesc,
	})); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}

	if err := exec.Command("docker", "start", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker start %s: %v", h.natsContainer, err)
	}
	// Wait for NATS to come back so the drift scan can reach it.
	waitDeadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
			ClusterId:     st.clusterID,
			IncludeInSync: true,
		}))
		if err == nil && hasReachableRow(resp.Msg.Rows) {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("resolver did not come back up within deadline")
		}
		time.Sleep(500 * time.Millisecond)
	}

	resp, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     st.clusterID,
		IncludeInSync: false,
	}))
	if err != nil {
		t.Fatalf("GetClusterDriftStatus: %v", err)
	}

	var found bool
	for _, r := range resp.Msg.Rows {
		if r.AccountName == st.accountName {
			found = true
			if r.Status != nisv1.DriftStatus_DRIFT_STATUS_DB_AHEAD {
				t.Fatalf("expected DB_AHEAD for %q after failed push, got %v (msg=%q)", r.AccountName, r.Status, r.ErrorMessage)
			}
		}
	}
	if !found {
		t.Fatalf("did not find mutated account %q in drift rows (include_in_sync=false): %+v", st.accountName, resp.Msg.Rows)
	}
}

// TestE2E_ClusterDrift_ReconcileClearsDrift completes the loop: after
// inducing DB_AHEAD via a failed auto-sync push, calling
// ReconcileAccountOnCluster pushes the new JWT and the next scan
// reports IN_SYNC. Pins the contract that the reconcile RPC actually
// does what the UI says it does. Stop-then-restart-NATS is required
// to manufacture the drifted state — with A13-lite a plain mutation
// would otherwise auto-sync and skip the drifted intermediate state.
func TestE2E_ClusterDrift_ReconcileClearsDrift(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-fix")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	time.Sleep(1100 * time.Millisecond)

	if err := exec.Command("docker", "stop", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker stop %s: %v", h.natsContainer, err)
	}
	newDesc := "drift bait pre-reconcile (push will fail)"
	if _, err := h.accountCli.UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
		Id:          st.accountID,
		Description: &newDesc,
	})); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if err := exec.Command("docker", "start", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker start %s: %v", h.natsContainer, err)
	}
	waitDeadline := time.Now().Add(15 * time.Second)
	for {
		_, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
			ClusterId:     st.clusterID,
			IncludeInSync: true,
		}))
		if err == nil {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatalf("resolver did not come back up within deadline")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// GetClusterDriftStatus answering once doesn't guarantee NATS is stably
	// accepting fresh connections — right after `docker start` the server can
	// still close a new connection mid-handshake (EOF). ReconcileAccountOnCluster
	// opens its own connection, so retry it through that transient window.
	reconcileDeadline := time.Now().Add(15 * time.Second)
	for {
		_, err := h.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
			ClusterId: st.clusterID,
			AccountId: st.accountID,
		}))
		if err == nil {
			break
		}
		if time.Now().After(reconcileDeadline) {
			t.Fatalf("ReconcileAccountOnCluster: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	resp, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     st.clusterID,
		IncludeInSync: true,
	}))
	if err != nil {
		t.Fatalf("GetClusterDriftStatus: %v", err)
	}
	for _, r := range resp.Msg.Rows {
		if r.AccountName == st.accountName && r.Status != nisv1.DriftStatus_DRIFT_STATUS_IN_SYNC {
			t.Fatalf("post-reconcile: expected IN_SYNC for %q, got %v (msg=%q)", r.AccountName, r.Status, r.ErrorMessage)
		}
	}
}

// TestE2E_ClusterDrift_NewAccountReportsMissingOnResolver: a brand-new
// account whose initial auto-sync push failed (NATS unreachable at
// create time) should show as MISSING_ON_RESOLVER on the next scan —
// the resolver has literally no JWT to compare against. Under A13-lite
// a plain createAccount would auto-sync; we stop NATS first to force
// the push failure.
func TestE2E_ClusterDrift_NewAccountReportsMissingOnResolver(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-missing")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := exec.Command("docker", "stop", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker stop %s: %v", h.natsContainer, err)
	}
	// Create a new account while NATS is down — auto-sync push will
	// fail silently (best-effort), and the DB row lands without a
	// matching resolver entry.
	freshAccountID := h.createAccount(t, st.operatorID, "drift-missing-fresh")
	if err := exec.Command("docker", "start", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker start %s: %v", h.natsContainer, err)
	}

	// Poll until the fresh account appears with a non-UNREACHABLE status.
	// Two race conditions to bridge: (1) the cluster row may still be
	// marked unhealthy from a health-check that fired while NATS was
	// stopped — until the next health-check sweep marks it healthy again,
	// drift returns UNREACHABLE for every row; (2) the resolver's lazy
	// re-load of its on-disk JWT store can return incomplete answers
	// briefly. The harness pins CLUSTER_HEALTH_CHECK_INTERVAL_SECONDS=2 so
	// the re-mark-healthy step completes within a couple seconds.
	row := waitForDriftRow(t, ctx, h, st.clusterID, false, time.Now().Add(30*time.Second),
		func(r *nisv1.AccountDriftRow) bool {
			return r.AccountId == freshAccountID && r.Status != nisv1.DriftStatus_DRIFT_STATUS_UNREACHABLE
		})
	if row.Status != nisv1.DriftStatus_DRIFT_STATUS_MISSING_ON_RESOLVER {
		t.Fatalf("expected MISSING_ON_RESOLVER for new account, got %v (msg=%q)", row.Status, row.ErrorMessage)
	}

	// And reconcile fixes it.
	if _, err := h.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: st.clusterID,
		AccountId: freshAccountID,
	})); err != nil {
		t.Fatalf("ReconcileAccountOnCluster: %v", err)
	}
	resp2, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     st.clusterID,
		IncludeInSync: true,
	}))
	if err != nil {
		t.Fatalf("GetClusterDriftStatus(post-reconcile): %v", err)
	}
	for _, r := range resp2.Msg.Rows {
		if r.AccountId == freshAccountID && r.Status != nisv1.DriftStatus_DRIFT_STATUS_IN_SYNC {
			t.Fatalf("post-reconcile expected IN_SYNC for fresh account, got %v", r.Status)
		}
	}
}

// TestE2E_ClusterDrift_StoppedNATSReportsUnreachable: kill the NATS container
// and assert the scan still returns (with every row UNREACHABLE) rather than
// erroring out the whole RPC. Operator UX requirement — we want to see the
// account list even when the cluster is down.
func TestE2E_ClusterDrift_StoppedNATSReportsUnreachable(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-down")

	// Tear down NATS. The harness records the container name so we can
	// reach in and stop it directly.
	if err := exec.Command("docker", "stop", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker stop %s: %v", h.natsContainer, err)
	}

	// The 60s health-check loop will eventually mark this cluster unhealthy,
	// which short-circuits the scan via a different code path. To pin the
	// dial-failure branch we run BEFORE the health tick fires. Parent ctx
	// budgets the whole scan against the per-cluster dial timeout (the
	// drift scan caps at 30s; we just need it to finish).
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	resp, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     st.clusterID,
		IncludeInSync: true,
	}))
	if err != nil {
		t.Fatalf("GetClusterDriftStatus with NATS down: %v", err)
	}
	if len(resp.Msg.Rows) == 0 {
		t.Fatalf("expected per-account rows with UNREACHABLE status even when NATS is down, got empty")
	}
	for _, r := range resp.Msg.Rows {
		if r.Status != nisv1.DriftStatus_DRIFT_STATUS_UNREACHABLE {
			t.Fatalf("expected UNREACHABLE for %q with NATS down, got %v (msg=%q)", r.AccountName, r.Status, r.ErrorMessage)
		}
		if r.ErrorMessage == "" {
			t.Fatalf("UNREACHABLE row %q has no error message", r.AccountName)
		}
	}
}

// TestE2E_ClusterDrift_CrossOperatorReconcileRejected: an account from
// operator A pushed to a cluster from operator B must be rejected. SyncCluster
// is structurally safe (iterates accounts-by-operator); per-account reconcile
// has to enforce the equivalent boundary explicitly. Pin the guard so future
// refactors can't drop it silently.
func TestE2E_ClusterDrift_CrossOperatorReconcileRejected(t *testing.T) {
	h := startStack(t)
	stA := h.bootStandardStack(t, "drift-opA")

	// Second operator with its own account; no NATS bootstrap needed since
	// the reject lands before any dial.
	opB := h.createOperator(t, "drift-opB")
	clusterB := h.createCluster(t, opB, "drift-opB-cluster", "nats://127.0.0.1:1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := h.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: clusterB,
		AccountId: stA.accountID, // opA's account into opB's cluster
	}))
	if err == nil {
		t.Fatalf("expected cross-operator reconcile to fail, got nil")
	}
	if !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("expected 'does not belong' error, got %v", err)
	}
}

// TestE2E_ClusterDrift_ReconcileEmitsEvent: a successful reconcile must
// produce a cluster.account.synced event with cluster + account metadata.
// Audit story for this feature relies on this — without the event, an
// operator can't tell from the audit log whether a reconcile happened.
func TestE2E_ClusterDrift_ReconcileEmitsEvent(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-event")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := h.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: st.clusterID,
		AccountId: st.accountID,
	})); err != nil {
		t.Fatalf("ReconcileAccountOnCluster: %v", err)
	}

	// EventService.ListEvents is admin-only; the harness's primary client is
	// the admin user.
	resp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			Types: []string{"cluster.account.synced"},
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found bool
	for _, e := range resp.Msg.Events {
		if e.ResourceId == st.clusterID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not find cluster.account.synced event for cluster %s; got %d events", st.clusterID, len(resp.Msg.Events))
	}
}

// TestE2E_ClusterDrift_OrphanOnResolverDetected: an account JWT sitting on
// the resolver that NIS has no row for must surface as ORPHAN_ON_RESOLVER.
// This is the second half of the delete-syncs-to-NATS story: when the NATS
// push fails (network/dial error) DeleteAccount still removes the DB row
// (DB is the source of truth, NATS reconciled best-effort) and an orphan
// is left behind. The drift dashboard surfaces it; the per-row Delete-from-
// resolver action cleans it up.
func TestE2E_ClusterDrift_OrphanOnResolverDetected(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-orph")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Create + sync an extra account so its JWT lands on the resolver.
	doomedID := h.createAccount(t, st.operatorID, "doomed-orphan")
	h.syncCluster(t, st.clusterID)
	doomedAcc, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: doomedID}))
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	doomedPK := doomedAcc.Msg.Account.PublicKey

	// 2. Stop NATS so DeleteAccount's resolver push fails. DB delete still
	// succeeds (best-effort semantic) — that's the bug we want to surface.
	if err := exec.Command("docker", "stop", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker stop %s: %v", h.natsContainer, err)
	}

	if _, err := h.accountCli.DeleteAccount(ctx, connect.NewRequest(&nisv1.DeleteAccountRequest{Id: doomedID})); err != nil {
		t.Fatalf("DeleteAccount with NATS down: %v", err)
	}
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: doomedID})); err == nil {
		t.Fatalf("doomed account should be gone from NIS DB despite NATS push failure")
	}

	// 3. Bring NATS back. JetStream resolver state is persisted on the
	// container's filesystem, so the orphan JWT survives the restart.
	if err := exec.Command("docker", "start", h.natsContainer).Run(); err != nil {
		t.Fatalf("docker start %s: %v", h.natsContainer, err)
	}

	// 4. Poll the drift scan until the orphan appears. The resolver loads
	// its persisted JWT store asynchronously after process startup, so a
	// generic "is NATS up" check can return before the orphan JWT is in
	// the resolver's in-memory account list. Waiting on the assertion
	// target (orphan pubkey present in rows) makes the readiness signal
	// the same as the test's expectation — no race window.
	orphan := waitForDriftRow(t, ctx, h, st.clusterID, false, time.Now().Add(30*time.Second),
		func(r *nisv1.AccountDriftRow) bool { return r.AccountPublicKey == doomedPK })
	if orphan.Status != nisv1.DriftStatus_DRIFT_STATUS_ORPHAN_ON_RESOLVER {
		t.Fatalf("expected ORPHAN_ON_RESOLVER for %q, got %v", doomedPK, orphan.Status)
	}
	if orphan.AccountId != "" {
		t.Fatalf("orphan row must have empty account_id (no NIS row exists), got %q", orphan.AccountId)
	}

	// 5. Per-row cleanup via DeleteResolverAccount — the UI's "Delete from
	// resolver" button calls this. After cleanup, the orphan is gone.
	if _, err := h.clusterCli.DeleteResolverAccount(ctx, connect.NewRequest(&nisv1.DeleteResolverAccountRequest{
		ClusterId: st.clusterID,
		PublicKey: doomedPK,
	})); err != nil {
		t.Fatalf("DeleteResolverAccount: %v", err)
	}

	resp2, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     st.clusterID,
		IncludeInSync: false,
	}))
	if err != nil {
		t.Fatalf("GetClusterDriftStatus (post-cleanup): %v", err)
	}
	for _, r := range resp2.Msg.Rows {
		if r.AccountPublicKey == doomedPK {
			t.Fatalf("orphan still present after DeleteResolverAccount: %+v", r)
		}
	}
}

// hasReachableRow returns true if at least one row is something other than
// UNREACHABLE — used as a "resolver is back" sentinel during the NATS
// restart window in the orphan test.
func hasReachableRow(rows []*nisv1.AccountDriftRow) bool {
	for _, r := range rows {
		if r.Status != nisv1.DriftStatus_DRIFT_STATUS_UNREACHABLE {
			return true
		}
	}
	return false
}

// waitForDriftRow polls GetClusterDriftStatus until `predicate` matches a row
// or the deadline expires. Returns the matched row.
//
// After `docker start <nats-container>` the NATS process accepts connections
// almost immediately, but the delegated JWT resolver lazily re-hydrates its
// account store from disk and there's no event for "store fully loaded".
// During that window GetClusterDriftStatus succeeds, yet the resolver-side
// LIST returns an incomplete picture: a known account may be missing, or an
// expected orphan may not appear yet. The `hasReachableRow` heuristic above
// is satisfied as soon as the system account answers, which is well before
// the rest of the store is enumerated — hence the historical flake on the
// orphan + missing-on-resolver tests.
//
// Wait on the assertion target directly. The predicate IS the readiness
// signal — if the row we care about isn't in the response yet, we're not
// done waiting.
func waitForDriftRow(t *testing.T, ctx context.Context, h *harness, clusterID string, includeInSync bool, deadline time.Time, predicate func(*nisv1.AccountDriftRow) bool) *nisv1.AccountDriftRow {
	t.Helper()
	var lastRows []*nisv1.AccountDriftRow
	for {
		resp, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
			ClusterId:     clusterID,
			IncludeInSync: includeInSync,
		}))
		if err == nil {
			lastRows = resp.Msg.Rows
			for _, r := range resp.Msg.Rows {
				if predicate(r) {
					return r
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected drift row did not appear within deadline; last rows: %+v", lastRows)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestE2E_ClusterDrift_RequiresAuth: unauthenticated call rejected before
// reaching the handler. Pins that the new RPC sits behind the auth
// interceptor like every other ClusterService RPC.
func TestE2E_ClusterDrift_RequiresAuth(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-auth")

	unauth := nisv1connect.NewClusterServiceClient(h.httpClient, h.serverURL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := unauth.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId: st.clusterID,
	}))
	if err == nil {
		t.Fatalf("expected error for unauthenticated call, got nil")
	}
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated, got %v (err=%v)", code, err)
	}
}
