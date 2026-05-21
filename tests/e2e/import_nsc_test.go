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
	"time"

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

// TestE2E_ImportNSC_MixedSigningKeys exercises the bit of the import
// path that classifies each entry in the account JWT's signing_keys
// map. NSC supports BOTH shapes simultaneously: raw strings (plain
// signers — NATS uses the user JWT's own perms) and UserScope objects
// (scoped — NATS applies the embedded template). Pre-fix, NIS treated
// every entry as plain and dropped the scope template silently, which
// would surface as a silent permission regression on first account-JWT
// regen. This test imports an archive carrying one plain + two scoped
// signers and asserts:
//   - both kinds get their own SSK row,
//   - is_plain_signer is true for the plain one and false for scoped,
//   - the scoped SSK rows preserved the template's pub/sub/resp
//     contents — not the empty defaults the bug shipped.
func TestE2E_ImportNSC_MixedSigningKeys(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	const importedName = "e2e-nsc-mixed-operator"
	archive, plainPubs, scopedPubs := testutil.BuildNSCArchiveWithSigningKeys(
		t, importedName, "mixed-account", "mixed-user",
		1, // one plain signer
		[]testutil.ScopedSignerSpec{
			{
				Role:     "service-reader",
				PubAllow: []string{"svc.read.>"},
				SubAllow: []string{"svc.events.>"},
				SubDeny:  []string{"svc.events.secret.>"},
			},
			{
				Role:     "request-responder",
				PubAllow: []string{"_INBOX.>"},
				SubAllow: []string{"req.>"},
				RespMax:  3,
				RespTTL:  90 * time.Second,
			},
		},
	)

	resp, err := h.exportCli.ImportFromNSC(ctx, connect.NewRequest(&nisv1.ImportFromNSCRequest{
		Data:         archive,
		OperatorName: importedName,
	}))
	if err != nil {
		t.Fatalf("ImportFromNSC: %v", err)
	}

	accs, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{
		OperatorId: resp.Msg.OperatorId,
	}))
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	var accountID string
	for _, a := range accs.Msg.Accounts {
		if a.Name == "mixed-account" {
			accountID = a.Id
			break
		}
	}
	if accountID == "" {
		t.Fatal("mixed-account not found post-import")
	}

	ssks, err := h.keyCli.ListScopedSigningKeys(ctx, connect.NewRequest(&nisv1.ListScopedSigningKeysRequest{
		AccountId: accountID,
	}))
	if err != nil {
		t.Fatalf("ListScopedSigningKeys: %v", err)
	}
	byPub := make(map[string]*nisv1.ScopedSigningKey, len(ssks.Msg.Keys))
	for _, k := range ssks.Msg.Keys {
		byPub[k.PublicKey] = k
	}
	// Sanity: every signing key in the archive must round-trip into an SSK.
	for _, pub := range append(append([]string{}, plainPubs...), scopedPubs...) {
		if _, ok := byPub[pub]; !ok {
			t.Fatalf("expected SSK for signing key %s in imported account, got pubs: %v", pub, mapPubs(byPub))
		}
	}

	// Plain signer must be flagged so jwt_service emits it as a raw
	// string on regen and skips SetScoped on its user JWTs. Without
	// that flag the system user under it gets subs:0 / payload:0 and
	// the cluster healthcheck silently times out.
	plain := byPub[plainPubs[0]]
	if !plain.TrackLatest == false { // sanity — unrelated field, not set on import
		t.Fatalf("plain signer SSK unexpectedly has track_latest=true")
	}
	// IsPlainSigner is the load-bearing assertion for the plain case.
	// (Proto field name is also is_plain_signer; surfaced via mapper.)
	if got := plain; !sskIsPlainSigner(t, h, got.Id) {
		t.Fatalf("expected is_plain_signer=true for plain signer SSK %s; got false", got.Id)
	}

	// Scoped signers must NOT be flagged plain, AND must have the
	// template's pub/sub/resp contents copied verbatim into the SSK
	// row. The pre-fix bug stripped these on the way in.
	reader := byPub[scopedPubs[0]]
	if sskIsPlainSigner(t, h, reader.Id) {
		t.Fatalf("scoped signer SSK %s unexpectedly has is_plain_signer=true", reader.Id)
	}
	if reader.Name != "service-reader" {
		t.Fatalf("scoped signer SSK name = %q, want %q (role from NSC UserScope)", reader.Name, "service-reader")
	}
	if !sliceEqual(reader.Permissions.GetPubAllow(), []string{"svc.read.>"}) {
		t.Fatalf("scoped signer SSK pub_allow = %v, want [svc.read.>]", reader.Permissions.GetPubAllow())
	}
	if !sliceEqual(reader.Permissions.GetSubDeny(), []string{"svc.events.secret.>"}) {
		t.Fatalf("scoped signer SSK sub_deny = %v, want [svc.events.secret.>]", reader.Permissions.GetSubDeny())
	}

	responder := byPub[scopedPubs[1]]
	if sskIsPlainSigner(t, h, responder.Id) {
		t.Fatalf("scoped signer SSK %s unexpectedly has is_plain_signer=true", responder.Id)
	}
	if rp := responder.GetResponsePermission(); rp == nil || rp.MaxMsgs != 3 || rp.Expires != int64(90*time.Second) {
		t.Fatalf("scoped signer SSK response_permission = %+v, want max=3 expires=90s", rp)
	}
}

func mapPubs(m map[string]*nisv1.ScopedSigningKey) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sskIsPlainSigner re-fetches the SSK and reads the is_plain_signer
// proto field directly. Used to assert that the importer correctly
// classifies plain vs scoped signers from a mixed NSC archive.
func sskIsPlainSigner(t *testing.T, h *harness, id string) bool {
	t.Helper()
	resp, err := h.keyCli.GetScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{Id: id}))
	if err != nil {
		t.Fatalf("GetScopedSigningKey(%s): %v", id, err)
	}
	return resp.Msg.Key.GetIsPlainSigner()
}
