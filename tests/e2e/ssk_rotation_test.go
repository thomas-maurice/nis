//go:build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_SSKRotation_OldCredsRejectedNewCredsAccepted is the P3 canonical
// end-to-end proof.
//
// Flow:
//  1. Boot the standard stack (NIS + NATS + operator/cluster/account/default-SSK)
//     and create one extra SSK + user under it.
//  2. Connect with the user's .creds — works.
//  3. Rotate the SSK via the RPC.
//  4. Connect with the OLD .creds — must be rejected (account JWT no longer
//     trusts the old SSK pubkey AND old user JWT pubkey is in Revocations).
//  5. Fetch new .creds, connect — works again.
//  6. Decode the new user JWT and pin the -1s race fix from the design review:
//     the new JWT's iat must NOT be considered revoked by the account JWT,
//     while a pre-rotation iat for the same pubkey MUST be revoked.
func TestE2E_SSKRotation_OldCredsRejectedNewCredsAccepted(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "ssk-rot")
	ctx := context.Background()

	// Create a dedicated SSK + user under it so the default SSK stays
	// available to sign the system user. Permissions are permissive so
	// the connect-and-publish sanity checks don't trip a deny.
	sskID := h.createScopedKey(t, s.accountID, "rotation-key", nil)
	userID := h.createScopedUser(t, s.accountID, "rotation-user", sskID)
	h.syncCluster(t, s.clusterID)
	oldCreds := h.fetchUserCreds(t, userID, "rotation-user-old")

	// Phase 1: pre-rotation connection works.
	ncBefore := dial(t, h.natsURL, oldCreds)
	if err := ncBefore.Publish("demo.before-rotation", []byte("ok")); err != nil {
		t.Fatalf("pre-rotation publish: %v", err)
	}
	_ = ncBefore.FlushTimeout(2 * time.Second)
	ncBefore.Close()

	// Rotate.
	rotResp, err := h.keyCli.RotateScopedSigningKey(ctx, connect.NewRequest(&nisv1.RotateScopedSigningKeyRequest{
		Id:     sskID,
		Reason: "e2e-rotation-test",
	}))
	if err != nil {
		t.Fatalf("RotateScopedSigningKey: %v", err)
	}
	if rotResp.Msg.OldPublicKey == rotResp.Msg.Key.PublicKey {
		t.Fatalf("rotation must produce a new public key; got same: %s", rotResp.Msg.Key.PublicKey)
	}
	if rotResp.Msg.AffectedUsers != 1 {
		t.Fatalf("expected 1 affected user, got %d", rotResp.Msg.AffectedUsers)
	}

	// The post-commit push runs synchronously inside the RPC. By the time
	// it returns the resolver should have the new account JWT (with the
	// new SSK pubkey in signing_keys + the user pubkey in Revocations).
	// If any cluster push lagged, the rest of the assertions are racy;
	// fail explicitly so the cause is clear.
	for _, po := range rotResp.Msg.PushOutcomes {
		if !po.Ok {
			t.Fatalf("post-commit push to cluster %s failed: %s", po.ClusterName, po.ErrorMessage)
		}
	}

	// Phase 2: OLD .creds connection MUST be rejected.
	ncAfterOld, errAfterOld := nats.Connect(h.natsURL,
		nats.UserCredentials(oldCreds),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(0),
	)
	if errAfterOld == nil {
		ncAfterOld.Close()
		t.Fatal("expected post-rotation connect with OLD creds to fail; it succeeded")
	}

	// Phase 3: NEW .creds connection must work + publish OK.
	newCreds := h.fetchUserCreds(t, userID, "rotation-user-new")
	ncAfterNew, errAfterNew := nats.Connect(h.natsURL,
		nats.UserCredentials(newCreds),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(0),
	)
	if errAfterNew != nil {
		t.Fatalf("post-rotation connect with NEW creds failed: %v", errAfterNew)
	}
	if err := ncAfterNew.Publish("demo.after-rotation", []byte("ok")); err != nil {
		t.Fatalf("post-rotation publish with NEW creds: %v", err)
	}
	_ = ncAfterNew.FlushTimeout(2 * time.Second)
	ncAfterNew.Close()

	// Phase 4: decode the new user JWT and pin the -1s race fix.
	credsBlob, err := os.ReadFile(newCreds)
	if err != nil {
		t.Fatalf("read new creds: %v", err)
	}
	newUserJWT := extractUserJWT(t, string(credsBlob))
	newClaims, err := jwt.DecodeUserClaims(newUserJWT)
	if err != nil {
		t.Fatalf("decode new user JWT: %v", err)
	}
	if newClaims.Issuer != rotResp.Msg.Key.PublicKey {
		t.Fatalf("new user JWT issuer = %s; want %s (the new SSK pubkey)",
			newClaims.Issuer, rotResp.Msg.Key.PublicKey)
	}

	// Read the account JWT and check the Revocations map directly.
	accResp, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{
		Id: s.accountID,
	}))
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	accClaims, err := jwt.DecodeAccountClaims(accResp.Msg.Account.Jwt)
	if err != nil {
		t.Fatalf("decode account JWT: %v", err)
	}

	// The new user JWT (iat = now-ish) must NOT be revoked.
	newIAT := time.Unix(newClaims.IssuedAt, 0)
	if accClaims.Revocations.IsRevoked(newClaims.Subject, newIAT) {
		t.Fatalf("new user JWT (iat=%d) is being revoked by account JWT; race fix regressed", newClaims.IssuedAt)
	}

	// A pre-rotation iat (an hour ago) MUST be revoked. This is the
	// "old creds are dead" guarantee expressed at the JWT layer.
	preRotation := time.Now().Add(-1 * time.Hour)
	if !accClaims.Revocations.IsRevoked(newClaims.Subject, preRotation) {
		t.Fatalf("pre-rotation iat must be revoked; Revocations map missing entry for %s", newClaims.Subject)
	}
}

// extractUserJWT pulls the JWT line out of a NATS .creds blob. Mirrors
// extractNKeySeed in harness_test.go but for the JWT half.
func extractUserJWT(t *testing.T, creds string) string {
	t.Helper()
	const start = "-----BEGIN NATS USER JWT-----"
	const end = "------END NATS USER JWT------"
	i := strings.Index(creds, start)
	j := strings.Index(creds, end)
	if i < 0 || j < 0 || j <= i {
		t.Fatalf("extractUserJWT: markers not found in creds (start=%d end=%d)", i, j)
	}
	body := creds[i+len(start) : j]
	return strings.TrimSpace(body)
}
