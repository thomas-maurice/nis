//go:build e2e

// import_nsc_test.go — the "I'm migrating off `nsc`" user journey. Exercises a
// separate code path from the YAML/JSON backup importer: the NSC importer
// reads a tarball laid out the way `nsc` writes its key store on disk.
//
// The whole import is tx-wrapped (A1 follow-up), so the regression net here
// is that a failure mid-tree doesn't leave a half-imported operator behind.
package e2e

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/pkg/testutil"
)

// TestE2E_ImportNSC_MinimalArchive synthesises a minimal NSC archive
// (operator + account + user), POSTs it, and asserts the imported entities
// are queryable. The minimal archive is built via pkg/testutil so we don't
// depend on the `nsc` binary being installed.
func TestE2E_ImportNSC_MinimalArchive(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	const importedName = "e2e-nsc-imported-operator"
	archive := testutil.BuildMinimalNSCArchive(t, importedName, "nsc-account", "nsc-user")

	resp, err := h.exportCli.ImportFromNSC(ctx, connect.NewRequest(&nisv1.ImportFromNSCRequest{
		Data:         archive,
		OperatorName: importedName,
	}))
	if err != nil {
		t.Fatalf("ImportFromNSC: %v", err)
	}
	if resp.Msg.OperatorId == "" {
		t.Fatal("ImportFromNSC returned empty operator id")
	}

	byName, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name: importedName,
	}))
	if err != nil {
		t.Fatalf("GetOperatorByName after NSC import: %v", err)
	}
	if byName.Msg.Operator.Id != resp.Msg.OperatorId {
		t.Fatalf("NSC import operator id mismatch: response %s vs by-name %s", resp.Msg.OperatorId, byName.Msg.Operator.Id)
	}

	// Imported account + user must be queryable via the API. The archive
	// carries `nsc-account` and one user `nsc-user`.
	accs, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{
		OperatorId: byName.Msg.Operator.Id,
	}))
	if err != nil {
		t.Fatalf("ListAccounts on NSC-imported operator: %v", err)
	}
	var nscAccountID string
	for _, a := range accs.Msg.Accounts {
		if a.Name == "nsc-account" {
			nscAccountID = a.Id
			break
		}
	}
	if nscAccountID == "" {
		names := []string{}
		for _, a := range accs.Msg.Accounts {
			names = append(names, a.Name)
		}
		t.Fatalf("imported NSC tree missing nsc-account; got accounts: %v", names)
	}

	users, err := h.userCli.ListUsers(ctx, connect.NewRequest(&nisv1.ListUsersRequest{
		AccountId: nscAccountID,
	}))
	if err != nil {
		t.Fatalf("ListUsers on NSC-imported account: %v", err)
	}
	foundUser := false
	for _, u := range users.Msg.Users {
		if u.Name == "nsc-user" {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Fatalf("imported NSC tree missing nsc-user")
	}
}
