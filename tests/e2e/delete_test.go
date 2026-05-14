//go:build e2e

// delete_test.go — deletion semantics across operator / account / user. The
// theme here is "destructive operations must either succeed atomically or
// fail with a friendly error". The FK schema is `clusters.operator_id ON
// DELETE RESTRICT`, so the service layer is responsible for the friendly
// preflight check; everything else is ON DELETE CASCADE through the A1 tx.
package e2e

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_Delete_OperatorBlockedByAttachedCluster is the regression net for
// the FK-leak bug. DeleteOperator must refuse cleanly when clusters are still
// attached — not let SQLSTATE 23503 surface from the FK constraint. The
// schema (`clusters.operator_id ON DELETE RESTRICT`) is deliberate: clusters
// track live NATS infra and shouldn't vanish silently. State must be intact
// after the failed delete (the tx rolled back).
func TestE2E_Delete_OperatorBlockedByAttachedCluster(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "blocked-operator")
	clusterID := h.createCluster(t, operatorID, "blocked-cluster", "nats://stub:4222")
	accountID := h.createAccount(t, operatorID, "blocked-account")
	h.createUser(t, accountID, "blocked-user")

	_, err := h.operatorCli.DeleteOperator(ctx, connect.NewRequest(&nisv1.DeleteOperatorRequest{Id: operatorID}))
	if err == nil {
		t.Fatal("DeleteOperator with attached cluster must fail, got nil")
	}
	// Friendly message must name the offending cluster so the user knows
	// what to delete first.
	if !strings.Contains(err.Error(), "blocked-cluster") {
		t.Fatalf("error should mention attached cluster name; got: %v", err)
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got %v: %v", connect.CodeOf(err), err)
	}

	// Tx must have rolled back: every row is still present.
	if _, err := h.operatorCli.GetOperator(ctx, connect.NewRequest(&nisv1.GetOperatorRequest{Id: operatorID})); err != nil {
		t.Fatalf("operator should still exist after blocked delete: %v", err)
	}
	if _, err := h.clusterCli.GetCluster(ctx, connect.NewRequest(&nisv1.GetClusterRequest{Id: clusterID})); err != nil {
		t.Fatalf("cluster should still exist after blocked delete: %v", err)
	}
	accs, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{OperatorId: operatorID}))
	if err != nil {
		t.Fatalf("ListAccounts after blocked delete: %v", err)
	}
	if len(accs.Msg.Accounts) == 0 {
		t.Fatal("accounts list empty after blocked delete — tx did not roll back")
	}
}

// TestE2E_Delete_OperatorCascadesAccountsAndUsers proves the happy path: once
// the cluster is gone, DeleteOperator removes the whole subtree atomically.
// This is the A1 tx-wrapped cascade.
func TestE2E_Delete_OperatorCascadesAccountsAndUsers(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "cascade-operator")
	accountID := h.createAccount(t, operatorID, "cascade-account")
	userID := h.createUser(t, accountID, "cascade-user")
	keyID := h.createScopedKey(t, accountID, "cascade-key", nil)

	if _, err := h.operatorCli.DeleteOperator(ctx, connect.NewRequest(&nisv1.DeleteOperatorRequest{Id: operatorID})); err != nil {
		t.Fatalf("DeleteOperator: %v", err)
	}

	if _, err := h.operatorCli.GetOperator(ctx, connect.NewRequest(&nisv1.GetOperatorRequest{Id: operatorID})); err == nil {
		t.Fatal("operator should be gone after cascade delete")
	}
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: accountID})); err == nil {
		t.Fatal("account should be gone after operator cascade delete")
	}
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: userID})); err == nil {
		t.Fatal("user should be gone after operator cascade delete")
	}
	if _, err := h.keyCli.GetScopedSigningKey(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{Id: keyID})); err == nil {
		t.Fatal("scoped key should be gone after operator cascade delete")
	}
}

// TestE2E_Delete_AccountCascadesUsersAndKeys proves the same atomicity at the
// account level — deleting an account wipes its users and scoped keys without
// disturbing other accounts on the same operator.
func TestE2E_Delete_AccountCascadesUsersAndKeys(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "acct-cascade-operator")
	doomedAccountID := h.createAccount(t, operatorID, "doomed")
	survivorAccountID := h.createAccount(t, operatorID, "survivor")

	doomedUserID := h.createUser(t, doomedAccountID, "doomed-user")
	doomedKeyID := h.createScopedKey(t, doomedAccountID, "doomed-key", nil)
	survivorUserID := h.createUser(t, survivorAccountID, "survivor-user")

	if _, err := h.accountCli.DeleteAccount(ctx, connect.NewRequest(&nisv1.DeleteAccountRequest{Id: doomedAccountID})); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: doomedAccountID})); err == nil {
		t.Fatal("doomed account should be gone")
	}
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: doomedUserID})); err == nil {
		t.Fatal("doomed user should be gone after account cascade")
	}
	if _, err := h.keyCli.GetScopedSigningKey(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{Id: doomedKeyID})); err == nil {
		t.Fatal("doomed scoped key should be gone after account cascade")
	}

	// Survivor account and its user are untouched.
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: survivorAccountID})); err != nil {
		t.Fatalf("survivor account should remain: %v", err)
	}
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: survivorUserID})); err != nil {
		t.Fatalf("survivor user should remain: %v", err)
	}
}

// TestE2E_Delete_UserIsScoped proves that deleting a single user doesn't
// touch siblings or the parent account. Trivial-sounding but it's the only
// guard against an off-by-one in the WHERE clause of the user repo's Delete.
func TestE2E_Delete_UserIsScoped(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "userdel-operator")
	accountID := h.createAccount(t, operatorID, "userdel-account")
	doomedID := h.createUser(t, accountID, "doomed-user")
	survivorID := h.createUser(t, accountID, "survivor-user")

	if _, err := h.userCli.DeleteUser(ctx, connect.NewRequest(&nisv1.DeleteUserRequest{Id: doomedID})); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: doomedID})); err == nil {
		t.Fatal("doomed user should be gone")
	}
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: survivorID})); err != nil {
		t.Fatalf("survivor user should remain: %v", err)
	}
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: accountID})); err != nil {
		t.Fatalf("account should remain after a user delete: %v", err)
	}
}
