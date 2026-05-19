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

// TestE2E_ClusterDrift_MutateWithoutSyncReportsDBAhead covers the standard
// "I regenerated an account JWT but didn't push" state. The drift scan must
// classify the modified account as DB_AHEAD so the UI can offer a one-click
// reconcile.
func TestE2E_ClusterDrift_MutateWithoutSyncReportsDBAhead(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-dbahead")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// jwt v2's IssuedAt resolution is 1 second. If we mutate within the same
	// second as the initial sync, the resulting JWT can land with iat ==
	// resolver-iat, which compareJWTs reports as OUT_OF_BAND rather than
	// DB_AHEAD. Sleep 1.1s to guarantee a later iat.
	time.Sleep(1100 * time.Millisecond)

	newDesc := "drift bait"
	if _, err := h.accountCli.UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
		Id:          st.accountID,
		Description: &newDesc,
	})); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
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
				t.Fatalf("expected DB_AHEAD for %q, got %v (msg=%q)", r.AccountName, r.Status, r.ErrorMessage)
			}
		}
	}
	if !found {
		t.Fatalf("did not find mutated account %q in drift rows (include_in_sync=false): %+v", st.accountName, resp.Msg.Rows)
	}
}

// TestE2E_ClusterDrift_ReconcileClearsDrift completes the loop: after
// detecting DB_AHEAD via a mutation, calling ReconcileAccountOnCluster pushes
// the new JWT and the next scan reports IN_SYNC. Pins the contract that the
// reconcile RPC actually does what the UI says it does.
func TestE2E_ClusterDrift_ReconcileClearsDrift(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-fix")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	time.Sleep(1100 * time.Millisecond)
	newDesc := "drift bait pre-reconcile"
	if _, err := h.accountCli.UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
		Id:          st.accountID,
		Description: &newDesc,
	})); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}

	if _, err := h.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: st.clusterID,
		AccountId: st.accountID,
	})); err != nil {
		t.Fatalf("ReconcileAccountOnCluster: %v", err)
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

// TestE2E_ClusterDrift_NewAccountReportsMissingOnResolver: a brand-new account
// that hasn't been synced should show as MISSING_ON_RESOLVER, not DB_AHEAD —
// the resolver has literally no JWT to compare against.
func TestE2E_ClusterDrift_NewAccountReportsMissingOnResolver(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "drift-missing")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Create a new account WITHOUT syncing.
	freshAccountID := h.createAccount(t, st.operatorID, "drift-missing-fresh")

	resp, err := h.clusterCli.GetClusterDriftStatus(ctx, connect.NewRequest(&nisv1.GetClusterDriftStatusRequest{
		ClusterId:     st.clusterID,
		IncludeInSync: false,
	}))
	if err != nil {
		t.Fatalf("GetClusterDriftStatus: %v", err)
	}

	var found bool
	for _, r := range resp.Msg.Rows {
		if r.AccountId == freshAccountID {
			found = true
			if r.Status != nisv1.DriftStatus_DRIFT_STATUS_MISSING_ON_RESOLVER {
				t.Fatalf("expected MISSING_ON_RESOLVER for new account, got %v (msg=%q)", r.Status, r.ErrorMessage)
			}
		}
	}
	if !found {
		t.Fatalf("fresh account %s not present in drift rows", freshAccountID)
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
