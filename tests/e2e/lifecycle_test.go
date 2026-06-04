//go:build e2e

// lifecycle_test.go — operator/cluster/account/user CRUD over Connect-RPC. No
// NATS server is involved here; these tests just exercise the API surface and
// the FK-cascade behavior in the SQLite-backed repository. The point of having
// this as its own file is so a CRUD regression localises without needing to
// boot Docker for NATS.
package e2e

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_Lifecycle_CreateAndReadFullTree walks an operator from creation
// through account → user → scoped key → user-with-scoped-key, then re-reads
// every entity by id and by name. It's the basic "the API is wired up and
// repository round-trips work" smoke test.
func TestE2E_Lifecycle_CreateAndReadFullTree(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "lifecycle-operator")

	// Operator readable by id and by name.
	if _, err := h.operatorCli.GetOperator(ctx, connect.NewRequest(&nisv1.GetOperatorRequest{Id: operatorID})); err != nil {
		t.Fatalf("GetOperator: %v", err)
	}
	byName, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: "lifecycle-operator", OrganizationId: defaultOrgID}))
	if err != nil {
		t.Fatalf("GetOperatorByName: %v", err)
	}
	if byName.Msg.Operator.Id != operatorID {
		t.Fatalf("GetOperatorByName id mismatch: %s vs %s", byName.Msg.Operator.Id, operatorID)
	}

	accountID := h.createAccount(t, operatorID, "lifecycle-account")
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: accountID})); err != nil {
		t.Fatalf("GetAccount: %v", err)
	}

	userID := h.createUser(t, accountID, "lifecycle-user")
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: userID})); err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	// Scoped key + user signed by it. Confirms the scoped-key proto path
	// and the FK to accounts both work.
	keyID := h.createScopedKey(t, accountID, "lifecycle-key", &nisv1.UserPermissions{
		PubAllow: []string{"public.>"},
	})
	scopedUserID := h.createScopedUser(t, accountID, "lifecycle-scoped-user", keyID)
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: scopedUserID})); err != nil {
		t.Fatalf("GetUser(scoped): %v", err)
	}

	// List endpoints must surface what we just created.
	accs, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{OperatorId: operatorID}))
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if !containsAccount(accs.Msg.Accounts, "lifecycle-account") {
		t.Fatalf("ListAccounts missing lifecycle-account; got %v", accountNames(accs.Msg.Accounts))
	}
	users, err := h.userCli.ListUsers(ctx, connect.NewRequest(&nisv1.ListUsersRequest{AccountId: accountID}))
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users.Msg.Users) < 2 {
		t.Fatalf("ListUsers should report both users; got %d", len(users.Msg.Users))
	}
}

// TestE2E_Lifecycle_CredentialsBlob fetches a freshly minted user's `.creds`
// and checks the block markers without touching NATS. The roundtrip ("can the
// server emit valid creds at all") is the cheap precondition that the live
// NATS tests then build on.
func TestE2E_Lifecycle_CredentialsBlob(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "creds-operator")
	accountID := h.createAccount(t, operatorID, "creds-account")
	userID := h.createUser(t, accountID, "creds-user")

	resp, err := h.userCli.GetUserCredentials(ctx, connect.NewRequest(&nisv1.GetUserCredentialsRequest{Id: userID}))
	if err != nil {
		t.Fatalf("GetUserCredentials: %v", err)
	}
	creds := resp.Msg.Credentials
	for _, marker := range []string{"BEGIN NATS USER JWT", "BEGIN USER NKEY SEED"} {
		if !strings.Contains(creds, marker) {
			t.Fatalf("creds blob missing %q marker:\n%s", marker, creds)
		}
	}
}

func containsAccount(accs []*nisv1.Account, name string) bool {
	for _, a := range accs {
		if a.Name == name {
			return true
		}
	}
	return false
}

func accountNames(accs []*nisv1.Account) []string {
	names := make([]string, 0, len(accs))
	for _, a := range accs {
		names = append(names, a.Name)
	}
	return names
}
