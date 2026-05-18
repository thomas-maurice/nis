//go:build e2e

// api_tokens_test.go — end-to-end scenarios for the APITokenService and the
// auth-middleware fork that recognises `nis_pat_` bearers.
package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// tokenAuthedClients returns a client set whose Authorization header carries the
// given API token plaintext (i.e. simulating a CI runner using `NIS_TOKEN`).
func (h *harness) tokenAuthedClients(plaintext string) clientSet {
	authOpt := connect.WithInterceptors(&bearerInterceptor{token: plaintext})
	return clientSet{
		token:       plaintext,
		operatorCli: nisv1connect.NewOperatorServiceClient(h.httpClient, h.serverURL, authOpt),
		accountCli:  nisv1connect.NewAccountServiceClient(h.httpClient, h.serverURL, authOpt),
		userCli:     nisv1connect.NewUserServiceClient(h.httpClient, h.serverURL, authOpt),
		clusterCli:  nisv1connect.NewClusterServiceClient(h.httpClient, h.serverURL, authOpt),
		keyCli:      nisv1connect.NewScopedSigningKeyServiceClient(h.httpClient, h.serverURL, authOpt),
		exportCli:   nisv1connect.NewExportServiceClient(h.httpClient, h.serverURL, authOpt),
		authCli:     nisv1connect.NewAuthServiceClient(h.httpClient, h.serverURL, authOpt),
		eventCli:    nisv1connect.NewEventServiceClient(h.httpClient, h.serverURL, authOpt),
		webhookCli:  nisv1connect.NewWebhookServiceClient(h.httpClient, h.serverURL, authOpt),
		apiTokenCli: nisv1connect.NewAPITokenServiceClient(h.httpClient, h.serverURL, authOpt),
	}
}

// TestE2E_APIToken_CreateAndUse covers the happy path: admin mints a token,
// uses its plaintext to make an authenticated RPC, and that RPC succeeds.
// Locks the canonical "service-account-token instead of human-JWT" flow.
func TestE2E_APIToken_CreateAndUse(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	resp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "ci-runner",
		Role: "admin",
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if resp.Msg.Plaintext == "" {
		t.Fatal("expected plaintext to be returned exactly once on create")
	}

	// Use the plaintext token to drive an arbitrary read RPC — proves the
	// middleware fork accepted it and synthesized an admin-equivalent APIUser.
	tokenAuthed := h.tokenAuthedClients(resp.Msg.Plaintext)
	if _, err := tokenAuthed.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{})); err != nil {
		t.Fatalf("ListOperators using token: %v", err)
	}
}

// TestE2E_APIToken_RevokedTokenRejected proves that after Revoke, the token
// cannot be used. The middleware bucket for the failure is invalid_api_token
// regardless of the precise sub-reason.
func TestE2E_APIToken_RevokedTokenRejected(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	resp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "ephemeral",
		Role: "admin",
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	if _, err := h.apiTokenCli.RevokeAPIToken(ctx, connect.NewRequest(&nisv1.RevokeAPITokenRequest{
		Id: resp.Msg.Token.Id,
	})); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}

	tokenAuthed := h.tokenAuthedClients(resp.Msg.Plaintext)
	_, err = tokenAuthed.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{}))
	if err == nil {
		t.Fatal("revoked token must not authenticate, but ListOperators succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated for revoked token, got %v: %v", got, err)
	}
}

// TestE2E_APIToken_ExpiredTokenRejected proves that ExpiresAt is enforced and
// that a token expiring in the past is unusable regardless of its hash matching.
func TestE2E_APIToken_ExpiredTokenRejected(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	past := time.Now().Add(-1 * time.Minute)
	resp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name:      "already-expired",
		Role:      "admin",
		ExpiresAt: timestamppb.New(past),
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	tokenAuthed := h.tokenAuthedClients(resp.Msg.Plaintext)
	_, err = tokenAuthed.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{}))
	if err == nil {
		t.Fatal("expired token must not authenticate")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated for expired token, got %v: %v", got, err)
	}
}

// TestE2E_APIToken_NeverExpires verifies that a token with no ExpiresAt is
// usable indefinitely — pins the "nil = no expiry" semantics that the docs claim.
func TestE2E_APIToken_NeverExpires(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	resp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "no-expiry",
		Role: "admin",
		// ExpiresAt deliberately omitted.
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if resp.Msg.Token.ExpiresAt != nil {
		t.Fatalf("expected ExpiresAt to be nil for never-expiring token, got %v", resp.Msg.Token.ExpiresAt)
	}

	tokenAuthed := h.tokenAuthedClients(resp.Msg.Plaintext)
	if _, err := tokenAuthed.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{})); err != nil {
		t.Fatalf("never-expiring token rejected: %v", err)
	}
}

// TestE2E_APIToken_LoginRejectsBearerToken proves that the Login endpoint —
// the only public endpoint — cannot be used to convert a service-account API
// token into a fresh JWT session. Login takes username+password; a token has
// no path to bootstrap into a different credential type.
func TestE2E_APIToken_LoginRejectsBearerToken(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	resp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "no-login",
		Role: "admin",
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	// Login is public — it doesn't read the Authorization header at all. But
	// it also requires username+password, so even with a token in the header
	// it must reject any "empty credentials" attempt.
	tokenAuthed := h.tokenAuthedClients(resp.Msg.Plaintext)
	_, err = tokenAuthed.authCli.Login(ctx, connect.NewRequest(&nisv1.LoginRequest{
		Username: "", Password: "",
	}))
	if err == nil {
		t.Fatal("Login must not succeed with empty creds even when token is in header")
	}
}

// TestE2E_APIToken_TokenCannotMintOtherTokens proves the chained-privilege
// guard: a token-authed CreateAPIToken call is refused regardless of role.
// Stops a leaked token from bootstrapping itself into a fresh long-lived
// credential and surviving its parent's revocation.
func TestE2E_APIToken_TokenCannotMintOtherTokens(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	resp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "no-children",
		Role: "admin",
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	tokenAuthed := h.tokenAuthedClients(resp.Msg.Plaintext)
	_, err = tokenAuthed.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "child",
		Role: "admin",
	}))
	if err == nil {
		t.Fatal("token-authed caller must NOT be able to mint another token")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied for token-authed mint, got %v: %v", got, err)
	}
}

// TestE2E_APIToken_OperatorAdminScopedToOwnOperator proves the privilege
// escalation guard. An operator-admin token must NOT be able to mint a token
// scoped to a different operator. An admin can mint anywhere.
func TestE2E_APIToken_OperatorAdminScopedToOwnOperator(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorAID := h.createOperator(t, "p8-op-a")
	operatorBID := h.createOperator(t, "p8-op-b")

	// Create an operator-admin api_user scoped to operator A.
	const opAdminUser = "p8-op-a-admin"
	const opAdminPass = "p8-op-a-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}
	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	// Minting an operator-admin token for OWN operator: allowed.
	if _, err := opAdmin.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name:       "ci-for-a",
		Role:       "operator-admin",
		OperatorId: operatorAID,
	})); err != nil {
		t.Fatalf("operator-admin must be able to mint a token for OWN operator: %v", err)
	}

	// Minting an operator-admin token for OTHER operator: refused.
	_, err := opAdmin.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name:       "ci-for-b",
		Role:       "operator-admin",
		OperatorId: operatorBID,
	}))
	if err == nil {
		t.Fatal("operator-admin must NOT mint a token scoped to a different operator")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v: %v", got, err)
	}

	// Minting an admin-role token: refused regardless of scope.
	_, err = opAdmin.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "ci-as-admin",
		Role: "admin",
	}))
	if err == nil {
		t.Fatal("operator-admin must NOT mint an admin token (role ceiling)")
	}
}

// TestE2E_APIToken_ListScopedToOwnTokens proves that the List filter is
// defense-in-depth: a non-admin sees ONLY their own tokens, not anyone else's,
// regardless of any created_by_user_id they put in the request.
func TestE2E_APIToken_ListScopedToOwnTokens(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Admin's own token.
	if _, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "admin-token-1",
		Role: "admin",
	})); err != nil {
		t.Fatalf("CreateAPIToken(admin): %v", err)
	}

	// Create an operator-admin api_user with its own token.
	operatorID := h.createOperator(t, "p8-list-op")
	const opAdminUser = "p8-list-op-admin"
	const opAdminPass = "p8-list-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}
	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)
	if _, err := opAdmin.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name:       "op-token-1",
		Role:       "operator-admin",
		OperatorId: operatorID,
	})); err != nil {
		t.Fatalf("op-admin CreateAPIToken: %v", err)
	}

	// Operator-admin lists tokens — must see only their own.
	listResp, err := opAdmin.apiTokenCli.ListAPITokens(ctx, connect.NewRequest(&nisv1.ListAPITokensRequest{}))
	if err != nil {
		t.Fatalf("ListAPITokens(op-admin): %v", err)
	}
	for _, tok := range listResp.Msg.Tokens {
		if tok.Name == "admin-token-1" {
			t.Fatal("operator-admin must not see admin-created tokens in its list")
		}
	}

	// Admin lists — must see both.
	adminListResp, err := h.apiTokenCli.ListAPITokens(ctx, connect.NewRequest(&nisv1.ListAPITokensRequest{}))
	if err != nil {
		t.Fatalf("ListAPITokens(admin): %v", err)
	}
	names := map[string]bool{}
	for _, tok := range adminListResp.Msg.Tokens {
		names[tok.Name] = true
	}
	if !names["admin-token-1"] || !names["op-token-1"] {
		t.Fatalf("admin must see both tokens; got %v", names)
	}
}

// TestE2E_APIToken_AuditEventActorIsToken proves that events emitted by RPCs
// driven via a token are attributed to the token (actor_type='api_token',
// actor_id=token.id), not to the api_user that minted the token. The
// distinction is the whole point of the Actor abstraction in authctx.
func TestE2E_APIToken_AuditEventActorIsToken(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	tokResp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "audit-test",
		Role: "admin",
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	tokenAuthed := h.tokenAuthedClients(tokResp.Msg.Plaintext)

	// Drive a mutation that emits an event we can find later.
	opResp, err := tokenAuthed.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name: "audit-op",
	}))
	if err != nil {
		t.Fatalf("CreateOperator using token: %v", err)
	}

	// Look up the operator.created event for our new operator and assert the
	// actor. We use the admin client for the read because event listing is
	// admin-only and we want to introspect what the system recorded.
	listResp, err := h.eventCli.ListEvents(ctx, connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: &nisv1.EventFilter{
			Types: []string{"operator.created"},
		},
	}))
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, evt := range listResp.Msg.Events {
		if evt.ResourceId != opResp.Msg.Operator.Id {
			continue
		}
		if evt.ActorType != "api_token" {
			t.Fatalf("expected actor_type=api_token for token-authed mutation, got %q", evt.ActorType)
		}
		if evt.ActorId != tokResp.Msg.Token.Id {
			t.Fatalf("expected actor_id=%s (token id), got %q", tokResp.Msg.Token.Id, evt.ActorId)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("did not find operator.created event for token-authed CreateOperator")
	}
}

// TestE2E_APIToken_InvalidPrefixIsUnauthenticated proves that a random string
// shaped like a token (correct prefix, wrong hash) fails with Unauthenticated
// and does NOT leak whether such a token was once issued.
func TestE2E_APIToken_InvalidPrefixIsUnauthenticated(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	junk := "nis_pat_" + "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	tokenAuthed := h.tokenAuthedClients(junk)
	_, err := tokenAuthed.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{}))
	if err == nil {
		t.Fatal("garbage nis_pat_ value must not authenticate")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("expected Unauthenticated, got %v: %v", got, err)
	}
}

// TestE2E_APIToken_DeletedCreatorPreservesToken proves the ON DELETE SET NULL
// behaviour: offboarding the human api_user who minted a CI token does NOT
// silently nuke the token. The token stays usable; the audit row records a
// null created_by_user_id.
func TestE2E_APIToken_DeletedCreatorPreservesToken(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Create a fresh admin api_user, log in as them, mint a token, log back
	// in as the bootstrapped admin, delete the new api_user, and assert the
	// token still works.
	adminCli := h.loginAs(t, adminUsername, adminPassword)
	const tempAdmin = "p8-temp-admin"
	const tempPass = "p8-temp-admin-password"
	tempResp, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    tempAdmin,
		Password:    tempPass,
		Permissions: []string{"admin"},
	}))
	if err != nil {
		t.Fatalf("CreateAPIUser(temp admin): %v", err)
	}

	temp := h.loginAs(t, tempAdmin, tempPass)
	tokResp, err := temp.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "outlives-creator",
		Role: "admin",
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	// Offboard the temp admin.
	if _, err := adminCli.authCli.DeleteAPIUser(ctx, connect.NewRequest(&nisv1.DeleteAPIUserRequest{
		Id: tempResp.Msg.User.Id,
	})); err != nil {
		t.Fatalf("DeleteAPIUser(temp): %v", err)
	}

	// Use the token — must still work.
	tokenAuthed := h.tokenAuthedClients(tokResp.Msg.Plaintext)
	if _, err := tokenAuthed.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{})); err != nil {
		t.Fatalf("token must outlive its creator (ON DELETE SET NULL): %v", err)
	}
}

// TestE2E_APIToken_NisCtl_NIS_TOKEN_EnvVar verifies the contract that nisctl
// users rely on: an HTTP client carrying the API token as a Bearer header can
// hit every standard endpoint. We test this at the HTTP level (not by spawning
// nisctl) because nisctl ergonomics are tested elsewhere and the SDK contract
// is what matters here.
func TestE2E_APIToken_NIS_TOKEN_EnvVar(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	resp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "env-var",
		Role: "admin",
	}))
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	// Hit /readyz with the token in the Authorization header to prove the
	// non-RPC path is unaffected (it's public; this is a smoke check).
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.serverURL+"/readyz", nil)
	req.Header.Set("Authorization", "Bearer "+resp.Msg.Plaintext)
	got, err := h.httpClient.Do(req)
	if err != nil {
		t.Fatalf("GET /readyz with token: %v", err)
	}
	got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /readyz, got %d", got.StatusCode)
	}

	// And drive an actual RPC the way nisctl would.
	tokenAuthed := h.tokenAuthedClients(resp.Msg.Plaintext)
	if _, err := tokenAuthed.apiTokenCli.ListAPITokens(ctx, connect.NewRequest(&nisv1.ListAPITokensRequest{})); err != nil {
		t.Fatalf("ListAPITokens using token: %v", err)
	}
}
