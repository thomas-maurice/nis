//go:build e2e

// response_permission_test.go — pins the behaviour that NIS always emits
// the Resp field in the account JWT's scoped-signer template so NATS
// applies its server-side defaults (1 msg / 2 min from
// server/const.go:DEFAULT_ALLOW_RESPONSE_*) by default. Without this
// behaviour a service that wants to reply to a request would need
// `_INBOX.>` explicitly in its pub_allow, which is a footgun.
//
// The shape of the test: create a scoped key with a RESTRICTED pub_allow
// that does NOT include _INBOX.>, create a user signed by it, sync, and
// confirm that the user can complete a NATS request/reply round-trip
// anyway (because NATS's response-permission auto-grant covers the
// reply on the request's specific inbox subject).
//
// If the jwt_service.go always-emit-Resp change ever regresses to "only
// emit when explicit non-zero", this test fails — the responder hits a
// permission violation publishing to the reply inbox.
package e2e

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/nats-io/nats.go"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_ResponsePermission_AutoGrantEnabledByDefault verifies that a
// scoped signing key with no explicit response_permission still gives its
// users the auto-grant for request/reply — i.e. NATS's server-side
// defaults (1 msg / 2 min) apply because NIS emits the Resp struct
// unconditionally.
func TestE2E_ResponsePermission_AutoGrantEnabledByDefault(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "resp-default")

	// A restricted SKK: pub_allow does NOT include _INBOX.>. If NIS
	// stopped emitting Resp, a user signed by this key would hit a
	// permission violation when trying to reply to a request. Both
	// response_permission fields are unset (0/0) — the whole point is
	// that NATS's defaults kick in without us specifying them.
	respKeyID, err := adminCreateRestrictedRespKey(t, h, s.accountID, "resp-only")
	if err != nil {
		t.Fatalf("create restricted SKK: %v", err)
	}
	respUserID := h.createScopedUser(t, s.accountID, "resp-user", respKeyID)
	h.syncCluster(t, s.clusterID)
	respCredsPath := h.fetchUserCreds(t, respUserID, "resp-user")

	// Requester uses the bootStandardStack's plain user (no scoped key
	// ⇒ full ">") so it can both publish on the request subject and
	// subscribe on its inbox — no template trickery on its side.
	reqCredsPath := s.credsPath

	// Wire the responder: subscribe to demo.echo and reply with the
	// request payload. This is the test's load-bearing assertion —
	// the reply Publish() happens against `_INBOX.<random>` which the
	// SKK's pub_allow does NOT cover, so it can only succeed if the
	// NATS response-permission auto-grant covered it.
	respNC, respErrCh := dialWithErrCh(t, h.natsURL, respCredsPath)
	defer respNC.Close()

	if _, err := respNC.Subscribe("demo.echo", func(m *nats.Msg) {
		_ = respNC.Publish(m.Reply, m.Data)
	}); err != nil {
		t.Fatalf("responder Subscribe: %v", err)
	}
	if err := respNC.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("responder Flush: %v", err)
	}

	// Requester sends a request and waits for the reply. If the
	// responder hit a permission violation on Publish(), the request
	// will time out instead.
	reqNC := dial(t, h.natsURL, reqCredsPath)
	defer reqNC.Close()
	reply, err := reqNC.Request("demo.echo", []byte("hello"), 3*time.Second)
	if err != nil {
		t.Fatalf("request/reply timed out — auto-grant not working: %v", err)
	}
	if string(reply.Data) != "hello" {
		t.Fatalf("unexpected reply payload: %q", reply.Data)
	}

	// Belt-and-braces: confirm the responder didn't get an async
	// permission violation either (it might still have replied
	// successfully but generated an error if the auto-grant was
	// somehow misapplied; we want a clean run).
	expectNoAsyncError(t, respErrCh, 200*time.Millisecond)
}

// adminCreateRestrictedRespKey creates a SKK on the given account with
// permissions explicit enough to PUBLISH on demo.echo (the requester's
// side wouldn't reach the responder otherwise), and SUBSCRIBE on
// demo.echo (so it can receive the request), but NO pub_allow on
// _INBOX.> — the responder's Publish() of the reply has to be covered
// by the response-permission auto-grant or it fails.
func adminCreateRestrictedRespKey(t *testing.T, h *harness, accountID, name string) (string, error) {
	t.Helper()
	resp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: accountID,
		Name:      name,
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{"demo.echo"},
			SubAllow: []string{"demo.echo"},
		},
		// ResponsePermission deliberately unset; the test exists to
		// prove that 0/0 still gives auto-grant via NATS defaults.
	}))
	if err != nil {
		return "", err
	}
	return resp.Msg.Key.Id, nil
}
