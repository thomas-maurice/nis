//go:build e2e

// revocations_test.go — verifies AccountService.ListAccountJWTRevocations (P13)
// surfaces what is actually flattened into the account JWT's NATS Revocations
// map, and that UserStillFlagged correctly diverges from the revocation row's
// existence after RegenerateUserCredentials clears users.revoked_at.
package e2e

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_ListAccountJWTRevocations_FlagToggleAcrossRegenerate walks the full
// P13-bug scenario: an operator revokes a user, sees still_flagged=true; then
// regenerates the credential, and must see the revocation row REMAIN active
// with still_flagged=false. The row stays because NATS will still reject the
// old creds until JWTExp.
func TestE2E_ListAccountJWTRevocations_FlagToggleAcrossRegenerate(t *testing.T) {
	h := startStack(t)

	opID := h.createOperator(t, "rv-op")
	accID := h.createAccount(t, opID, "rv-acc")
	userID := h.createUser(t, accID, "rv-user")

	// Initially empty.
	resp, err := h.accountCli.ListAccountJWTRevocations(context.Background(),
		connect.NewRequest(&nisv1.ListAccountJWTRevocationsRequest{AccountId: accID}))
	if err != nil {
		t.Fatalf("ListAccountJWTRevocations (pre-revoke): %v", err)
	}
	if got := len(resp.Msg.Revocations); got != 0 {
		t.Fatalf("expected 0 revocations before revoke, got %d", got)
	}

	// Revoke → row appears, user_still_flagged=true.
	h.revokeUser(t, userID, "p13-e2e")

	resp, err = h.accountCli.ListAccountJWTRevocations(context.Background(),
		connect.NewRequest(&nisv1.ListAccountJWTRevocationsRequest{AccountId: accID}))
	if err != nil {
		t.Fatalf("ListAccountJWTRevocations (post-revoke): %v", err)
	}
	if got := len(resp.Msg.Revocations); got != 1 {
		t.Fatalf("expected 1 revocation after revoke, got %d", got)
	}
	rev := resp.Msg.Revocations[0]
	if rev.UserId != userID {
		t.Errorf("user_id mismatch: want %s, got %s", userID, rev.UserId)
	}
	if rev.UserName != "rv-user" {
		t.Errorf("user_name mismatch: want %q, got %q", "rv-user", rev.UserName)
	}
	if rev.Reason != "p13-e2e" {
		t.Errorf("reason mismatch: want %q, got %q", "p13-e2e", rev.Reason)
	}
	if !rev.UserStillFlagged {
		t.Errorf("user_still_flagged must be true immediately after RevokeUser")
	}
	if rev.UserPublicKey == "" || rev.UserPublicKey[0] != 'U' {
		t.Errorf("user_public_key must be a U-prefix NATS key, got %q", rev.UserPublicKey)
	}

	// Regenerate creds → row stays, user_still_flagged flips to false.
	h.regenerateUserCredentials(t, userID)

	resp, err = h.accountCli.ListAccountJWTRevocations(context.Background(),
		connect.NewRequest(&nisv1.ListAccountJWTRevocationsRequest{AccountId: accID}))
	if err != nil {
		t.Fatalf("ListAccountJWTRevocations (post-regen): %v", err)
	}
	if got := len(resp.Msg.Revocations); got != 1 {
		t.Fatalf("revocation row must persist after RegenerateUserCredentials (NATS still rejects until JWTExp); got %d rows", got)
	}
	if resp.Msg.Revocations[0].UserStillFlagged {
		t.Errorf("user_still_flagged must be false after RegenerateUserCredentials clears users.revoked_at")
	}
}
