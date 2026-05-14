//go:build e2e

// nats_live_test.go — assertions against a real JWT-authenticated NATS server.
// Each test boots its own NIS + NATS + identity tree so failures in one
// scenario don't poison the next. The trade-off is wall-clock time (each test
// spins up a docker container); the reward is that "live NATS" cases like
// scope-deny, sync-after-mutation, and account isolation each get their own
// blast radius.
package e2e

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/nats-io/nats.go"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_NATSLive_AuthorizedConnection proves the happy path: a freshly
// minted user's .creds authenticates and can publish/subscribe inside its
// account. Every later live-NATS test inherits this as an implicit
// precondition — if this fails, the rest is noise.
func TestE2E_NATSLive_AuthorizedConnection(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "auth-ok")

	nc := dial(t, h.natsURL, s.credsPath)
	defer nc.Close()
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush: %v", err)
	}
	sub, err := nc.SubscribeSync("e2e.ping")
	if err != nil {
		t.Fatalf("SubscribeSync: %v", err)
	}
	if err := nc.Publish("e2e.ping", []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("NextMsg: %v", err)
	}
	if string(msg.Data) != "hello" {
		t.Fatalf("unexpected payload: %q", msg.Data)
	}
}

// TestE2E_NATSLive_UnauthorizedConnectionRejected confirms NATS refuses to
// accept a connection without credentials when the JWT auth block is in
// effect. This catches a class of regressions where NATS is started with the
// wrong include path and silently allows "open" mode.
func TestE2E_NATSLive_UnauthorizedConnectionRejected(t *testing.T) {
	h := startStack(t)
	_ = h.bootStandardStack(t, "auth-fail")

	nc, err := nats.Connect(h.natsURL,
		nats.Timeout(3*time.Second),
		nats.MaxReconnects(0),
	)
	if err == nil {
		nc.Close()
		t.Fatal("expected NATS to refuse unauthenticated connection")
	}
}

// TestE2E_NATSLive_ScopedKeyPubDenyEnforced is the E1 regression test: a user
// signed under a scoped key with pub_deny=["secret.>"] is permitted on
// public.> but denied on secret.>, with the denial surfaced as an async
// permission-violation error on the NATS error handler channel.
func TestE2E_NATSLive_ScopedKeyPubDenyEnforced(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "scope-deny")

	denyKeyID := h.createScopedKey(t, s.accountID, "deny-secret", &nisv1.UserPermissions{
		PubDeny: []string{"secret.>"},
	})
	denyUserID := h.createScopedUser(t, s.accountID, "deny-user", denyKeyID)

	// Sync so the resolver picks up the re-signed account JWT (which now
	// declares the new scoped signer) and the deny rule actually applies.
	h.syncCluster(t, s.clusterID)

	denyCredsPath := h.fetchUserCreds(t, denyUserID, "deny-user")
	nc, errCh := dialWithErrCh(t, h.natsURL, denyCredsPath)
	defer nc.Close()

	// Allowed subject works — no async error should arrive.
	if err := nc.Publish("public.allowed", []byte("ok")); err != nil {
		t.Fatalf("publish public.allowed: %v", err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush after allowed publish: %v", err)
	}
	expectNoAsyncError(t, errCh, 300*time.Millisecond)

	// Denied subject: publish returns nil locally; NATS sends an async
	// permissions violation. Drain errCh after a short wait.
	_ = nc.Publish("secret.denied", []byte("nope"))
	_ = nc.FlushTimeout(2 * time.Second)
	expectPermissionViolation(t, errCh, 2*time.Second)
}

// TestE2E_NATSLive_ScopedKeySubDenyEnforced is the sub-side parallel to
// ScopedKeyPubDenyEnforced. The NATS subscription permission path is wholly
// independent of the publish permission path, so a regression that broke
// sub_deny while leaving pub_deny intact would slip past the pub-only test.
// A user signed by a scoped key with sub_deny=["secret.>"] should subscribe
// to public.> without issue and trigger an async permission-violation when
// subscribing to secret.>.
func TestE2E_NATSLive_ScopedKeySubDenyEnforced(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "sub-deny")

	denyKeyID := h.createScopedKey(t, s.accountID, "sub-deny-secret", &nisv1.UserPermissions{
		SubDeny: []string{"secret.>"},
	})
	denyUserID := h.createScopedUser(t, s.accountID, "sub-deny-scoped-user", denyKeyID)
	h.syncCluster(t, s.clusterID)

	credsPath := h.fetchUserCreds(t, denyUserID, "sub-deny-scoped-user")
	nc, errCh := dialWithErrCh(t, h.natsURL, credsPath)
	defer nc.Close()

	// Allowed subject: subscribe succeeds and no async error arrives.
	if _, err := nc.SubscribeSync("public.allowed"); err != nil {
		t.Fatalf("SubscribeSync(public.allowed): %v", err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush after allowed subscribe: %v", err)
	}
	expectNoAsyncError(t, errCh, 300*time.Millisecond)

	// Denied subject: SubscribeSync returns no error locally (the subscribe
	// is queued client-side), but NATS sends an async permissions violation
	// once it processes the SUB protocol line.
	if _, err := nc.SubscribeSync("secret.denied"); err != nil {
		t.Fatalf("SubscribeSync(secret.denied) returned local error: %v", err)
	}
	_ = nc.FlushTimeout(2 * time.Second)
	expectPermissionViolation(t, errCh, 2*time.Second)
}

// TestE2E_NATSLive_SyncAfterMutation_NewScopeTakesEffect proves that mutating
// a scoped key's permissions and re-syncing the cluster takes effect on the
// NEXT reconnect. This is the "sync actually pushes" regression net.
func TestE2E_NATSLive_SyncAfterMutation_NewScopeTakesEffect(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "scope-mutate")
	ctx := context.Background()

	mutKeyID := h.createScopedKey(t, s.accountID, "mutating-key", nil)
	mutUserID := h.createScopedUser(t, s.accountID, "mutating-user", mutKeyID)
	h.syncCluster(t, s.clusterID)
	credsPath := h.fetchUserCreds(t, mutUserID, "mutating-user")

	// Phase 1: permissive scope — publish to secret.> works (no async error).
	nc1, errCh1 := dialWithErrCh(t, h.natsURL, credsPath)
	if err := nc1.Publish("secret.before-mutation", []byte("ok-now")); err != nil {
		t.Fatalf("permissive publish: %v", err)
	}
	_ = nc1.FlushTimeout(2 * time.Second)
	expectNoAsyncError(t, errCh1, 400*time.Millisecond)
	nc1.Close()

	// Mutate: add pub_deny=["secret.>"] to the scoped key. This re-signs the
	// account JWT in the NIS DB; the resolver still has the OLD JWT until sync.
	if _, err := h.keyCli.UpdatePermissions(ctx, connect.NewRequest(&nisv1.UpdatePermissionsRequest{
		Id: mutKeyID,
		Permissions: &nisv1.UserPermissions{
			PubDeny: []string{"secret.>"},
		},
	})); err != nil {
		t.Fatalf("UpdatePermissions: %v", err)
	}

	// Push the freshly re-signed account JWT to the resolver. This is the
	// line that proves "sync after mutation" works end-to-end.
	h.syncCluster(t, s.clusterID)

	// Phase 2: reconnect — NATS reads the updated scope from the resolver
	// and now enforces the new deny rule on secret.>.
	nc2, errCh2 := dialWithErrCh(t, h.natsURL, credsPath)
	defer nc2.Close()

	_ = nc2.Publish("secret.after-mutation", []byte("should-be-denied"))
	_ = nc2.FlushTimeout(2 * time.Second)
	expectPermissionViolation(t, errCh2, 2*time.Second)
}

// TestE2E_NATSLive_SyncAfterMutation_DeleteRevokesAccess proves that deleting
// a scoped key (which re-signs the account JWT without that signer in
// signing_keys) and re-syncing rejects further connections that present a
// .creds signed by the deleted key.
func TestE2E_NATSLive_SyncAfterMutation_DeleteRevokesAccess(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "scope-revoke")
	ctx := context.Background()

	delKeyID := h.createScopedKey(t, s.accountID, "to-be-deleted", nil)
	ephUserID := h.createScopedUser(t, s.accountID, "ephemeral-user", delKeyID)
	h.syncCluster(t, s.clusterID)
	credsPath := h.fetchUserCreds(t, ephUserID, "ephemeral-user")

	// Sanity: connection works before we delete the scoped key.
	ncBefore, err := nats.Connect(h.natsURL,
		nats.UserCredentials(credsPath),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(0),
	)
	if err != nil {
		t.Fatalf("pre-delete connect: %v", err)
	}
	ncBefore.Close()

	if _, err := h.keyCli.DeleteScopedSigningKey(ctx, connect.NewRequest(&nisv1.DeleteScopedSigningKeyRequest{
		Id: delKeyID,
	})); err != nil {
		t.Fatalf("DeleteScopedSigningKey: %v", err)
	}
	h.syncCluster(t, s.clusterID)

	// Reconnect attempt should fail — the signing key is no longer trusted.
	ncAfter, err := nats.Connect(h.natsURL,
		nats.UserCredentials(credsPath),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(0),
	)
	if err == nil {
		ncAfter.Close()
		t.Fatal("expected post-delete connection to be rejected; it succeeded")
	}
}

// TestE2E_NATSLive_AccountIsolation_CrossAccountSubjectsDoNotLeak proves that
// two accounts under the same operator can't see each other's traffic, even
// on the same subject. This is THE invariant the multi-account model exists
// to provide.
func TestE2E_NATSLive_AccountIsolation_CrossAccountSubjectsDoNotLeak(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "isolation")

	// Second account in the same operator with its own user.
	accountBID := h.createAccount(t, s.operatorID, "isolation-account-b")
	userBID := h.createUser(t, accountBID, "user-b")
	h.syncCluster(t, s.clusterID)
	credsB := h.fetchUserCreds(t, userBID, "user-b")

	// Subscriber on account B.
	ncB := dial(t, h.natsURL, credsB)
	defer ncB.Close()
	subB, err := ncB.SubscribeSync("crossaccount.probe")
	if err != nil {
		t.Fatalf("SubscribeSync on B: %v", err)
	}
	if err := ncB.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush B: %v", err)
	}

	// Publisher on account A (the default user from the standard stack).
	ncA := dial(t, h.natsURL, s.credsPath)
	defer ncA.Close()
	if err := ncA.Publish("crossaccount.probe", []byte("should not cross")); err != nil {
		t.Fatalf("Publish from A: %v", err)
	}
	if err := ncA.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush A: %v", err)
	}

	if msg, err := subB.NextMsg(750 * time.Millisecond); err == nil {
		t.Fatalf("account isolation broken: user in account B received %q from account A", msg.Data)
	}

	// Within account B, pub/sub still works normally — proves we didn't
	// just break the subscription.
	if err := ncB.Publish("crossaccount.probe", []byte("from-B")); err != nil {
		t.Fatalf("Publish within B: %v", err)
	}
	msg, err := subB.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("within-account-B NextMsg: %v", err)
	}
	if string(msg.Data) != "from-B" {
		t.Fatalf("unexpected payload within account B: %q", msg.Data)
	}
}
