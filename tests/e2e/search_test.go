//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// TestE2E_Search_AdminSeesAllKinds — sanity: admin's search hits every kind and
// returns results from both operators. Mirrors the unit-level fixture so a
// future server-side regression in the wiring (handler, casbin policy, server.go
// registration) gets caught at the boundary, not just in unit tests.
func TestE2E_Search_AdminSeesAllKinds(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorAID := h.createOperator(t, "search-ops-alpha")
	operatorBID := h.createOperator(t, "search-ops-beta")
	accountAID := h.createAccount(t, operatorAID, "search-alpha-account")
	accountBID := h.createAccount(t, operatorBID, "search-beta-account")
	_ = h.createUser(t, accountAID, "search-alpha-user")
	_ = h.createUser(t, accountBID, "search-beta-user")
	_ = h.createScopedKey(t, accountAID, "search-alpha-key", &nisv1.UserPermissions{
		PubAllow: []string{"metrics.>"},
	})
	_ = h.createScopedKey(t, accountBID, "search-beta-key", &nisv1.UserPermissions{
		PubAllow: []string{"events.>"},
	})

	resp, err := h.searchCli.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
		Query: "search",
		Limit: 50,
	}))
	if err != nil {
		t.Fatalf("Search(admin): %v", err)
	}

	if got, want := len(resp.Msg.Operators), 2; got != want {
		t.Fatalf("admin should see both operators matching 'search'; got %d want %d", got, want)
	}
	if got, want := len(resp.Msg.Accounts), 2; got != want {
		t.Fatalf("admin should see both accounts; got %d want %d", got, want)
	}
	if got, want := len(resp.Msg.Users), 2; got != want {
		t.Fatalf("admin should see both users; got %d want %d", got, want)
	}
	if got, want := len(resp.Msg.ScopedSigningKeys), 2; got != want {
		t.Fatalf("admin should see both scoped keys; got %d want %d", got, want)
	}
}

// TestE2E_Search_OperatorAdminCannotSeeOtherOperator is the headline isolation
// guarantee called out by the user when commissioning this work: operator-admin
// A must NEVER see operator B's tree in search results, even when the LIKE
// query matches B's entities directly. This is the e2e pin for the cross-
// operator boundary; a regression in casbin, the handler, or the service's
// post-filter loop would surface here.
func TestE2E_Search_OperatorAdminCannotSeeOtherOperator(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Two parallel trees with overlapping substrings.
	opAID := h.createOperator(t, "isolated-ops-alpha")
	opBID := h.createOperator(t, "isolated-ops-beta")
	accAID := h.createAccount(t, opAID, "isolated-alpha-account")
	accBID := h.createAccount(t, opBID, "isolated-beta-account")
	_ = h.createUser(t, accAID, "isolated-alpha-user")
	_ = h.createUser(t, accBID, "isolated-beta-user")
	// Both operators get a scoped key with metrics.> in pub_allow. If the
	// per-row filter ever regresses to "any operator-admin can see any scoped
	// key", searching for "metrics." would return both — this test would catch
	// that.
	_ = h.createScopedKey(t, accAID, "isolated-alpha-metrics", &nisv1.UserPermissions{
		PubAllow: []string{"metrics.>"},
	})
	_ = h.createScopedKey(t, accBID, "isolated-beta-metrics", &nisv1.UserPermissions{
		PubAllow: []string{"metrics.>"},
	})

	// Operator-admin scoped to A only.
	const opAdminUser = "search-isolated-op-a-admin"
	const opAdminPass = "search-isolated-op-a-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin for A): %v", err)
	}

	asA := h.loginAs(t, opAdminUser, opAdminPass)

	// Sanity: A's admin sees A's own subtree under the "isolated" query.
	resp, err := asA.searchCli.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
		Query: "isolated",
		Limit: 50,
	}))
	if err != nil {
		t.Fatalf("Search(opAdminA): %v", err)
	}
	if got, want := len(resp.Msg.Operators), 1; got != want {
		t.Fatalf("opAdminA should see exactly their own operator in search; got %d want %d (names=%v)",
			got, want, operatorNames(resp.Msg.Operators))
	}
	if resp.Msg.Operators[0].Id != opAID {
		t.Fatalf("opAdminA saw operator %s in search; expected %s", resp.Msg.Operators[0].Id, opAID)
	}
	if got, want := len(resp.Msg.Accounts), 1; got != want {
		t.Fatalf("opAdminA should see exactly one account in search; got %d want %d", got, want)
	}
	if resp.Msg.Accounts[0].Id != accAID {
		t.Fatalf("opAdminA saw account %s; expected only %s", resp.Msg.Accounts[0].Id, accAID)
	}
	if got, want := len(resp.Msg.Users), 1; got != want {
		t.Fatalf("opAdminA should see exactly one user in search; got %d want %d", got, want)
	}
	if got, want := len(resp.Msg.ScopedSigningKeys), 1; got != want {
		t.Fatalf("opAdminA should see exactly one scoped key in search; got %d want %d", got, want)
	}

	// The critical assertion: a deliberately targeted search for B's subject
	// match must NOT leak B's scoped key to A. The repo Search would return
	// B's key as a raw match; the service-level Filter must drop it.
	resp, err = asA.searchCli.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
		Query: "metrics.>",
		Kinds: []nisv1.SearchKind{nisv1.SearchKind_SEARCH_KIND_SCOPED_SIGNING_KEY},
		Limit: 50,
	}))
	if err != nil {
		t.Fatalf("Search(opAdminA, metrics.>): %v", err)
	}
	if got := len(resp.Msg.ScopedSigningKeys); got != 1 {
		t.Fatalf("CROSS-OPERATOR LEAK: opAdminA searching 'metrics.>' returned %d scoped keys; must be 1 (only their own). names=%v",
			got, scopedKeyNames(resp.Msg.ScopedSigningKeys))
	}
	if resp.Msg.ScopedSigningKeys[0].AccountId != accAID {
		t.Fatalf("CROSS-OPERATOR LEAK: opAdminA's 'metrics.>' search returned key with account_id=%s; expected %s (own account)",
			resp.Msg.ScopedSigningKeys[0].AccountId, accAID)
	}

	// Targeted search for the OTHER operator's entity by name. Must return
	// zero rows — leaking even the existence of operator B's tree is a
	// boundary violation.
	resp, err = asA.searchCli.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
		Query: "isolated-beta",
		Limit: 50,
	}))
	if err != nil {
		t.Fatalf("Search(opAdminA, isolated-beta): %v", err)
	}
	if got := len(resp.Msg.Operators); got != 0 {
		t.Fatalf("CROSS-OPERATOR LEAK: opAdminA searching 'isolated-beta' saw %d operators; must be 0", got)
	}
	if got := len(resp.Msg.Accounts); got != 0 {
		t.Fatalf("CROSS-OPERATOR LEAK: opAdminA searching 'isolated-beta' saw %d accounts; must be 0", got)
	}
	if got := len(resp.Msg.ScopedSigningKeys); got != 0 {
		t.Fatalf("CROSS-OPERATOR LEAK: opAdminA searching 'isolated-beta' saw %d scoped keys; must be 0", got)
	}

	// Reverse direction — sanity that the negative assertion above wasn't a
	// false positive caused by a filter that universally returns empty. Boot
	// operator B's admin, search for B's account name, confirm they see it.
	const opBAdminUser = "search-isolated-op-b-admin"
	const opBAdminPass = "search-isolated-op-b-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opBAdminUser,
		Password:    opBAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opBID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin for B): %v", err)
	}
	asB := h.loginAs(t, opBAdminUser, opBAdminPass)
	resp, err = asB.searchCli.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
		Query: "isolated-beta",
		Limit: 50,
	}))
	if err != nil {
		t.Fatalf("Search(opAdminB): %v", err)
	}
	// "isolated-beta" matches accounts named "isolated-beta-account" and the
	// scoped key "isolated-beta-metrics" — both in B's tree. B's admin must
	// see them; the operator itself is named "isolated-ops-beta" so doesn't
	// match this query (which is intentional — we want to confirm symmetric
	// visibility, not just non-emptiness).
	if got := len(resp.Msg.Accounts); got != 1 {
		t.Fatalf("symmetry check: opAdminB should see exactly their own account; got %d (names=%v)",
			got, accountNames(resp.Msg.Accounts))
	}
	if resp.Msg.Accounts[0].Id != accBID {
		t.Fatalf("symmetry check: opAdminB saw account %s; expected %s", resp.Msg.Accounts[0].Id, accBID)
	}
	if got := len(resp.Msg.ScopedSigningKeys); got != 1 {
		t.Fatalf("symmetry check: opAdminB should see exactly their own matching scoped key; got %d", got)
	}
}

// TestE2E_Search_ScopedKeySubjectMatchUseCase is the headline use case the
// proposal called out: "which scoped key allows pub on metrics.>". Operators
// type the raw NATS subject into the search bar and expect to find the key
// regardless of how it's encoded under the hood (JSON > escaping).
func TestE2E_Search_ScopedKeySubjectMatchUseCase(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorID := h.createOperator(t, "subject-search-op")
	accountID := h.createAccount(t, operatorID, "subject-search-account")
	keyID := h.createScopedKey(t, accountID, "metrics-publisher", &nisv1.UserPermissions{
		PubAllow: []string{"metrics.>"},
	})
	_ = h.createScopedKey(t, accountID, "events-only", &nisv1.UserPermissions{
		SubAllow: []string{"events.>"},
	})

	resp, err := h.searchCli.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
		Query: "metrics.>",
		Kinds: []nisv1.SearchKind{nisv1.SearchKind_SEARCH_KIND_SCOPED_SIGNING_KEY},
		Limit: 50,
	}))
	if err != nil {
		t.Fatalf("Search(metrics.>): %v", err)
	}
	if got := len(resp.Msg.ScopedSigningKeys); got != 1 {
		t.Fatalf("expected exactly 1 scoped key matching 'metrics.>'; got %d (names=%v)",
			got, scopedKeyNames(resp.Msg.ScopedSigningKeys))
	}
	if resp.Msg.ScopedSigningKeys[0].Id != keyID {
		t.Fatalf("expected key %s; got %s", keyID, resp.Msg.ScopedSigningKeys[0].Id)
	}
}

// TestE2E_Search_QueryValidation pins the InvalidArgument boundary so callers
// learn the constraints from a typed error and not a 500.
func TestE2E_Search_QueryValidation(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"empty", ""},
		{"whitespace-only", "    "},
		{"one-char", "x"},
		{"too-long", strings.Repeat("a", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.searchCli.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
				Query: tc.query,
				Limit: 50,
			}))
			if err == nil {
				t.Fatalf("expected error for query %q; got nil", tc.query)
			}
			if got, want := connect.CodeOf(err), connect.CodeInvalidArgument; got != want {
				t.Fatalf("expected %v for query %q; got %v (%v)", want, tc.query, got, err)
			}
		})
	}
}

// TestE2E_Search_RequiresAuth confirms the route isn't accidentally listed in
// publicMethods. A search RPC with no Authorization header must be rejected
// upstream of the handler.
func TestE2E_Search_RequiresAuth(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Build an unauthenticated client (no bearer interceptor) against the
	// running server.
	unauthed := nisv1connect.NewSearchServiceClient(h.httpClient, h.serverURL)
	_, err := unauthed.Search(ctx, connect.NewRequest(&nisv1.SearchRequest{
		Query: "anything",
	}))
	if err == nil {
		t.Fatal("unauthenticated Search must be rejected; got nil error")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("expected CodeUnauthenticated for anonymous Search; got %v: %v", got, err)
	}
}

func operatorNames(ops []*nisv1.Operator) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.Name)
	}
	return out
}

func scopedKeyNames(keys []*nisv1.ScopedSigningKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Name)
	}
	return out
}

