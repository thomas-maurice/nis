//go:build e2e

// import_backup_test.go — round-trip and conflict semantics of the export →
// import flow. The "backup" naming distinguishes this from import_nsc_test.go
// which exercises a separate code path (NSC archive parsing).
//
// Each scenario builds its own fresh operator so the destructive tests can
// safely delete state without stepping on each other.
package e2e

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// backupFixture builds operator + cluster + account + user. Used by the
// overwrite test which needs a cluster row (to verify it survives untouched).
// The cluster does NOT need a live NATS — the import path never tries to
// connect to it.
func backupFixture(t *testing.T, h *harness, prefix string) (operatorID, clusterID, accountID string) {
	t.Helper()
	operatorID = h.createOperator(t, prefix+"-operator")
	clusterID = h.createCluster(t, operatorID, prefix+"-cluster", "nats://stub:4222")
	accountID = h.createAccount(t, operatorID, prefix+"-account")
	h.createUser(t, accountID, prefix+"-user")
	return
}

// TestE2E_ImportBackup_RefusesExistingWithoutOverwrite proves the safety net:
// a re-import of an operator whose ID already exists must refuse unless
// overwrite=true. Silent overwrite from a stale backup would truncate
// accounts/users created since the export — opt-in is mandatory.
func TestE2E_ImportBackup_RefusesExistingWithoutOverwrite(t *testing.T) {
	h := startStack(t)
	operatorID, _, _ := backupFixture(t, h, "refuse")
	ctx := context.Background()

	exp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId: operatorID,
		Format:     "yaml",
	}))
	if err != nil {
		t.Fatalf("ExportOperator: %v", err)
	}

	_, err = h.exportCli.ImportOperator(ctx, connect.NewRequest(&nisv1.ImportOperatorRequest{
		Data: exp.Msg.Data,
	}))
	if err == nil {
		t.Fatal("ImportOperator without overwrite must refuse when ID exists")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected CodeFailedPrecondition, got %v: %v", connect.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "overwrite") {
		t.Fatalf("error should mention overwrite=true escape hatch; got: %v", err)
	}
}

// TestE2E_ImportBackup_OverwritePreservesClusters proves the subtree-replace
// semantics: overwrite=true wipes accounts/users/scoped-keys to match the
// YAML, but the cluster rows under that operator are left alone — they model
// live NATS infra that the import doesn't get to second-guess from a
// possibly-stale backup.
//
// We prove both halves by mutating state between export and import (adding a
// stray account that shouldn't exist after restore) and snapshotting the
// cluster row before/after.
func TestE2E_ImportBackup_OverwritePreservesClusters(t *testing.T) {
	h := startStack(t)
	operatorID, clusterID, _ := backupFixture(t, h, "overwrite")
	ctx := context.Background()

	// Snapshot cluster row + account names pre-export.
	preCluster, err := h.clusterCli.GetCluster(ctx, connect.NewRequest(&nisv1.GetClusterRequest{Id: clusterID}))
	if err != nil {
		t.Fatalf("GetCluster pre-export: %v", err)
	}
	preClusterName := preCluster.Msg.Cluster.Name
	preClusterURLs := preCluster.Msg.Cluster.ServerUrls

	preAccs, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{OperatorId: operatorID}))
	if err != nil {
		t.Fatalf("ListAccounts pre-export: %v", err)
	}
	expected := map[string]bool{}
	for _, a := range preAccs.Msg.Accounts {
		expected[a.Name] = true
	}

	// (1) Export.
	exp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId: operatorID,
		Format:     "yaml",
	}))
	if err != nil {
		t.Fatalf("ExportOperator: %v", err)
	}

	// (2) Stray account that overwrite must wipe — proves replacement, not merge.
	const strayName = "overwrite-stray-account"
	strayResp, err := h.accountCli.CreateAccount(ctx, connect.NewRequest(&nisv1.CreateAccountRequest{
		OperatorId: operatorID,
		Name:       strayName,
	}))
	if err != nil {
		t.Fatalf("CreateAccount(stray): %v", err)
	}
	strayID := strayResp.Msg.Account.Id

	// (3) Import with overwrite=true. Atomic — partial overwrite is worse
	// than no-op.
	impResp, err := h.exportCli.ImportOperator(ctx, connect.NewRequest(&nisv1.ImportOperatorRequest{
		Data:      exp.Msg.Data,
		Overwrite: true,
	}))
	if err != nil {
		t.Fatalf("ImportOperator(overwrite=true): %v", err)
	}
	if impResp.Msg.OperatorId != operatorID {
		t.Fatalf("operator id changed under overwrite: got %s want %s", impResp.Msg.OperatorId, operatorID)
	}

	// (4) Stray account is gone.
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: strayID})); err == nil {
		t.Fatal("stray account should be wiped by overwrite import")
	}

	// (5) Pre-export accounts are back.
	postAccs, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{OperatorId: operatorID}))
	if err != nil {
		t.Fatalf("ListAccounts after overwrite: %v", err)
	}
	got := map[string]bool{}
	for _, a := range postAccs.Msg.Accounts {
		got[a.Name] = true
	}
	for name := range expected {
		if !got[name] {
			t.Fatalf("overwrite import missing account %q. got: %v", name, got)
		}
	}
	if got[strayName] {
		t.Fatalf("overwrite did not remove stray account %q", strayName)
	}

	// (6) Cluster row untouched.
	postCluster, err := h.clusterCli.GetCluster(ctx, connect.NewRequest(&nisv1.GetClusterRequest{Id: clusterID}))
	if err != nil {
		t.Fatalf("GetCluster after overwrite: %v", err)
	}
	if postCluster.Msg.Cluster.Id != clusterID {
		t.Fatalf("cluster id changed: got %s want %s", postCluster.Msg.Cluster.Id, clusterID)
	}
	if postCluster.Msg.Cluster.Name != preClusterName {
		t.Fatalf("cluster name changed: got %s want %s", postCluster.Msg.Cluster.Name, preClusterName)
	}
	if len(postCluster.Msg.Cluster.ServerUrls) != len(preClusterURLs) {
		t.Fatalf("cluster server urls changed: got %v want %v", postCluster.Msg.Cluster.ServerUrls, preClusterURLs)
	}
	for i := range preClusterURLs {
		if postCluster.Msg.Cluster.ServerUrls[i] != preClusterURLs[i] {
			t.Fatalf("cluster server url[%d] changed: got %s want %s", i, postCluster.Msg.Cluster.ServerUrls[i], preClusterURLs[i])
		}
	}
}

// TestE2E_ImportBackup_FullRoundTrip is the user-facing promise: export an
// operator to YAML, wipe the DB tree, restore from YAML, and end up with the
// same UUIDs and account names. Destructive — runs on its own dedicated
// operator so it doesn't interfere with anything.
//
// Procedure:
//  1. Export with secrets so the import can re-encrypt seeds.
//  2. Delete the cluster (FK constraint ON DELETE RESTRICT means operator
//     delete fails otherwise).
//  3. Delete the operator. The A1 cascade removes accounts, users, and
//     scoped keys atomically.
//  4. Import the YAML. Original IDs and names come back.
//  5. Verify operator + accounts are queryable again.
func TestE2E_ImportBackup_FullRoundTrip(t *testing.T) {
	h := startStack(t)
	operatorID, clusterID, _ := backupFixture(t, h, "roundtrip")
	ctx := context.Background()

	// (1) Export.
	exportResp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId: operatorID,
		Format:     "yaml",
	}))
	if err != nil {
		t.Fatalf("ExportOperator: %v", err)
	}
	body := exportResp.Msg.Data

	accsBefore, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{OperatorId: operatorID}))
	if err != nil {
		t.Fatalf("ListAccounts before wipe: %v", err)
	}
	expectedAccountNames := map[string]bool{}
	for _, a := range accsBefore.Msg.Accounts {
		expectedAccountNames[a.Name] = true
	}
	if !expectedAccountNames["roundtrip-account"] || !expectedAccountNames["$SYS"] {
		t.Fatalf("pre-wipe snapshot missing expected accounts: %v", expectedAccountNames)
	}

	// (2) Delete cluster — clusters.operator_id is ON DELETE RESTRICT.
	if _, err := h.clusterCli.DeleteCluster(ctx, connect.NewRequest(&nisv1.DeleteClusterRequest{Id: clusterID})); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	// (3) Delete operator. A1 cascade should remove accounts → users →
	// scoped keys inside a single tx.
	if _, err := h.operatorCli.DeleteOperator(ctx, connect.NewRequest(&nisv1.DeleteOperatorRequest{Id: operatorID})); err != nil {
		t.Fatalf("DeleteOperator: %v", err)
	}
	if _, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: "roundtrip-operator"})); err == nil {
		t.Fatal("operator should be gone after DeleteOperator")
	}

	// (4) Import.
	importResp, err := h.exportCli.ImportOperator(ctx, connect.NewRequest(&nisv1.ImportOperatorRequest{Data: body}))
	if err != nil {
		t.Fatalf("ImportOperator after wipe: %v", err)
	}
	if importResp.Msg.OperatorId != operatorID {
		t.Fatalf("restored operator id changed: got %s, want %s", importResp.Msg.OperatorId, operatorID)
	}

	// (5) Tree is back.
	byName, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: "roundtrip-operator"}))
	if err != nil {
		t.Fatalf("GetOperatorByName after restore: %v", err)
	}
	if byName.Msg.Operator.Id != operatorID {
		t.Fatalf("restored operator id mismatch: got %s, want %s", byName.Msg.Operator.Id, operatorID)
	}

	accsAfter, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{OperatorId: operatorID}))
	if err != nil {
		t.Fatalf("ListAccounts after restore: %v", err)
	}
	actualNames := map[string]bool{}
	for _, a := range accsAfter.Msg.Accounts {
		actualNames[a.Name] = true
	}
	for name := range expectedAccountNames {
		if !actualNames[name] {
			t.Fatalf("restore missing account %q. got: %v", name, actualNames)
		}
	}
}

// TestE2E_ImportBackup_EncryptedExportRejectedByWrongKeyServer is the
// counterpart to PlaintextSecretsRoundTrip. An encrypted-mode export carries
// the source server's storage refs verbatim — they're opaque ciphertext
// tagged with the source's encryption key id. Trying to import that into a
// destination server holding a DIFFERENT encryption key must fail loudly
// (the user's expectation: "wrong key → operation refused") and must not
// silently store garbage seeds that would later surface as cryptic
// GetUserCredentials errors.
//
// The test boots TWO independent NIS instances — same JWT secret, different
// encryption keys — exports from A in encrypted mode and imports into B.
// Failure path expectations:
//
//   - ImportOperator returns a non-nil error, OR
//   - ImportOperator succeeds but GetUserCredentials on the restored user
//     fails (the seed is unreadable with B's encryptor).
//
// Either is acceptable as long as the failure is observable; what is NOT
// acceptable is a green import followed by a silent-decrypt-to-garbage on
// later reads.
func TestE2E_ImportBackup_EncryptedExportRejectedByWrongKeyServer(t *testing.T) {
	// Source server: default key.
	hSrc := startStack(t)
	srcOpID := hSrc.createOperator(t, "wrongkey-source-op")
	srcAccID := hSrc.createAccount(t, srcOpID, "wrongkey-source-acc")
	srcUserID := hSrc.createUser(t, srcAccID, "wrongkey-source-user")

	ctx := context.Background()
	exp, err := hSrc.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId: srcOpID,
		Format:     "yaml",
		// PlaintextSecrets intentionally NOT set — encrypted mode is the
		// path under test.
	}))
	if err != nil {
		t.Fatalf("ExportOperator(encrypted) on source: %v", err)
	}

	// Destination server: same binary, fresh DB, DIFFERENT encryption key.
	hDst := newHarness(t)
	t.Cleanup(hDst.teardown)
	hDst.encryptionKeyOverride = "totally-different-key-32-bytes!!"
	hDst.start(t)

	impResp, importErr := hDst.exportCli.ImportOperator(ctx, connect.NewRequest(&nisv1.ImportOperatorRequest{
		Data: exp.Msg.Data,
	}))

	// Acceptable path 1: import refuses outright. This is the preferred
	// behavior — the destination notices the key mismatch and rolls back.
	if importErr != nil {
		return
	}

	// Acceptable path 2: import succeeds, but GetUserCredentials on the
	// restored user fails because the seed material is unreadable. This
	// still surfaces the problem to the user before they trust the data,
	// just one step later.
	if impResp.Msg.OperatorId == "" {
		t.Fatal("ImportOperator returned no operator id and no error — silent failure")
	}
	// The src user id round-trips through the export (faithful restore),
	// so the destination has the same uuid.
	_, credsErr := hDst.userCli.GetUserCredentials(ctx, connect.NewRequest(&nisv1.GetUserCredentialsRequest{
		Id: srcUserID,
	}))
	if credsErr == nil {
		t.Fatal("ImportOperator accepted a foreign-encrypted export AND GetUserCredentials succeeded — wrong-key path is producing garbage seeds silently")
	}
}

// TestE2E_ImportBackup_PlaintextSecretsRoundTrip is the disaster-recovery
// scenario for the plaintext-secrets path: export with plaintext secrets,
// wipe, re-import. Functional == GetUserCredentials succeeds post-restore,
// which only works if the import's re-encrypt step ran and produced valid
// bytes for the destination encryptor.
//
// (Side note: the previous design considered a "no secrets" export mode for
// this scenario. It was dropped — an export without seeds is an unrecoverable
// half-restore because the JWT's baked-in public key can't be reproduced from
// a regenerated NKey pair.)
func TestE2E_ImportBackup_PlaintextSecretsRoundTrip(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	const opName = "plaintext-dr-op"
	const accName = "plaintext-dr-acc"
	const userName = "plaintext-dr-user"

	drOpID := h.createOperator(t, opName)
	drAccID := h.createAccount(t, drOpID, accName)
	drUserID := h.createUser(t, drAccID, userName)

	// Pre-wipe baseline: GetUserCredentials returns a valid .creds.
	credsBefore, err := h.userCli.GetUserCredentials(ctx, connect.NewRequest(&nisv1.GetUserCredentialsRequest{Id: drUserID}))
	if err != nil {
		t.Fatalf("GetUserCredentials pre-export: %v", err)
	}
	if !strings.Contains(credsBefore.Msg.Credentials, "BEGIN USER NKEY SEED") {
		t.Fatalf("pre-export creds missing NKEY SEED block")
	}

	// Export plaintext.
	exp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId:       drOpID,
		Format:           "yaml",
		PlaintextSecrets: true,
	}))
	if err != nil {
		t.Fatalf("ExportOperator(plaintext): %v", err)
	}
	body := exp.Msg.Data
	if strings.Contains(string(body), "encrypted:") {
		t.Fatalf("plaintext export must not contain 'encrypted:' refs")
	}

	// Wipe. No cluster attached → DeleteOperator alone is enough.
	if _, err := h.operatorCli.DeleteOperator(ctx, connect.NewRequest(&nisv1.DeleteOperatorRequest{Id: drOpID})); err != nil {
		t.Fatalf("DeleteOperator: %v", err)
	}
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: drUserID})); err == nil {
		t.Fatal("user should be gone after operator delete")
	}

	// Re-import. Server re-encrypts every seed with its current encryptor.
	impResp, err := h.exportCli.ImportOperator(ctx, connect.NewRequest(&nisv1.ImportOperatorRequest{Data: body}))
	if err != nil {
		t.Fatalf("ImportOperator(plaintext): %v", err)
	}
	if impResp.Msg.OperatorId != drOpID {
		t.Fatalf("restored operator id changed: got %s, want %s", impResp.Msg.OperatorId, drOpID)
	}

	// Smoking gun: GetUserCredentials must work post-restore.
	credsAfter, err := h.userCli.GetUserCredentials(ctx, connect.NewRequest(&nisv1.GetUserCredentialsRequest{Id: drUserID}))
	if err != nil {
		t.Fatalf("GetUserCredentials post-restore (re-encryption broken?): %v", err)
	}
	if !strings.Contains(credsAfter.Msg.Credentials, "BEGIN USER NKEY SEED") {
		t.Fatalf("post-restore creds missing NKEY SEED block")
	}
	// Content-addressable check: the recovered seed must match pre-export.
	if extractNKeySeed(t, credsAfter.Msg.Credentials) != extractNKeySeed(t, credsBefore.Msg.Credentials) {
		t.Fatalf("post-restore NKey seed does not match pre-export seed — round-trip lost or mutated bytes")
	}
}
