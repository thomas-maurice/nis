//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	natsgo "github.com/nats-io/nats.go"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// TestE2E_JetStreamUsage_LiveStreamReportsBytes is the headline scenario:
// JS is enabled on the account, a real stream is created via the app-user's
// .creds, a message is published, and the live-usage RPC reports non-zero
// streams + storage. Catches regressions in:
//   (a) the JSZ subject is correct ($SYS.REQ.ACCOUNT.<key>.JSZ);
//   (b) the response envelope is parsed (NATS wraps in {server, data});
//   (c) the system-user creds can read the account's JS stats (it can).
//
// The cluster MUST be re-synced AFTER enabling JS, otherwise the resolver's
// account JWT still has JS disabled and js.AddStream will be rejected before
// we ever get to the probe.
func TestE2E_JetStreamUsage_LiveStreamReportsBytes(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "jsusage")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Enable JetStream on the account with non-trivial limits.
	if _, err := h.accountCli.UpdateJetStreamLimits(ctx, connect.NewRequest(&nisv1.UpdateJetStreamLimitsRequest{
		Id: st.accountID,
		Limits: &nisv1.JetStreamLimits{
			Enabled:      true,
			MaxStorage:   100 << 20, // 100 MiB
			MaxMemory:    50 << 20,  // 50 MiB
			MaxStreams:   5,
			MaxConsumers: 10,
		},
	})); err != nil {
		t.Fatalf("UpdateJetStreamLimits: %v", err)
	}

	// 2. Resync the cluster so the updated account JWT (with JS enabled) lands
	// on the resolver. Without this step, js.AddStream below would fail with
	// "JetStream not enabled for account".
	h.syncCluster(t, st.clusterID)

	// 3. Connect AS the app user and create a real stream + publish a message.
	nc := dial(t, h.natsURL, st.credsPath)
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("nc.JetStream(): %v", err)
	}
	if _, err := js.AddStream(&natsgo.StreamConfig{
		Name:     "USAGE_TEST",
		Subjects: []string{"usage.test.>"},
		Storage:  natsgo.FileStorage,
	}); err != nil {
		t.Fatalf("js.AddStream: %v", err)
	}
	if _, err := js.Publish("usage.test.hello", []byte("hello jetstream")); err != nil {
		t.Fatalf("js.Publish: %v", err)
	}

	// 4. Call the new RPC and assert per-cluster OK status with live numbers.
	// The NATS server may need a tick to register the stream; retry briefly.
	var resp *connect.Response[nisv1.GetAccountJetStreamUsageResponse]
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err = h.accountCli.GetAccountJetStreamUsage(ctx, connect.NewRequest(&nisv1.GetAccountJetStreamUsageRequest{
			AccountId: st.accountID,
		}))
		if err != nil {
			t.Fatalf("GetAccountJetStreamUsage: %v", err)
		}
		if len(resp.Msg.Clusters) == 1 && resp.Msg.Clusters[0].Status == nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_OK && resp.Msg.Clusters[0].Usage.Streams > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for OK + Streams>0; last response: %+v", resp.Msg)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if got := len(resp.Msg.Clusters); got != 1 {
		t.Fatalf("expected 1 cluster, got %d", got)
	}
	c := resp.Msg.Clusters[0]
	if c.Status != nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_OK {
		t.Fatalf("status: want OK, got %v (msg=%q)", c.Status, c.ErrorMessage)
	}
	if c.Usage == nil {
		t.Fatalf("Usage is nil despite OK status")
	}
	if c.Usage.Streams < 1 {
		t.Fatalf("expected Streams >= 1, got %d", c.Usage.Streams)
	}
	if c.Usage.StorageUsed == 0 {
		t.Fatalf("expected StorageUsed > 0 after publishing, got 0")
	}
}

// TestE2E_JetStreamUsage_AccountWithoutJSReturnsNoJetStream confirms that when
// the account has never had JS enabled, the probe surfaces NO_JETSTREAM
// (or ACCOUNT_NOT_FOUND for resolvers that don't initialize empty JS state),
// not a top-level error. The UI relies on this to render a clean "not enabled"
// chip rather than a fault dialog.
func TestE2E_JetStreamUsage_AccountWithoutJSReturnsClassifiedStatus(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "jsoff")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := h.accountCli.GetAccountJetStreamUsage(ctx, connect.NewRequest(&nisv1.GetAccountJetStreamUsageRequest{
		AccountId: st.accountID,
	}))
	if err != nil {
		t.Fatalf("GetAccountJetStreamUsage: %v", err)
	}
	if len(resp.Msg.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(resp.Msg.Clusters))
	}

	got := resp.Msg.Clusters[0].Status
	// Either NO_JETSTREAM (server returned disabled=true) or ACCOUNT_NOT_FOUND
	// (server has no JS account state at all) is acceptable here — both
	// classify a graceful "no JS for this account" answer and the UI handles
	// them identically. The point of the assertion is that the RPC did NOT
	// return a top-level error or ERROR/UNREACHABLE status.
	if got != nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_NO_JETSTREAM &&
		got != nisv1.JetStreamProbeStatus_JET_STREAM_PROBE_STATUS_ACCOUNT_NOT_FOUND {
		t.Fatalf("expected NO_JETSTREAM or ACCOUNT_NOT_FOUND, got %v (msg=%q)", got, resp.Msg.Clusters[0].ErrorMessage)
	}
}

// TestE2E_JetStreamUsage_RequiresAuth: unauthed call gets rejected by the
// middleware before reaching the handler. Catches a regression where the new
// RPC accidentally landed outside the auth interceptor's enforcement.
func TestE2E_JetStreamUsage_RequiresAuth(t *testing.T) {
	h := startStack(t)
	st := h.bootStandardStack(t, "jsauth")

	// Construct a client without the bearer-token interceptor and verify the
	// call is rejected with Unauthenticated.
	unauth := nisv1connect.NewAccountServiceClient(h.httpClient, h.serverURL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := unauth.GetAccountJetStreamUsage(ctx, connect.NewRequest(&nisv1.GetAccountJetStreamUsageRequest{
		AccountId: st.accountID,
	}))
	if err == nil {
		t.Fatalf("expected error for unauthenticated call, got nil")
	}
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated, got %v (err=%v)", code, err)
	}
}
