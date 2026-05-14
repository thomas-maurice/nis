//go:build e2e

// auth_rbac_test.go — authentication + RBAC boundary checks at the HTTP/RPC
// edge. These are security tests: a regression here is a data leak. They
// don't need NATS, just NIS.
package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_Auth_UnauthenticatedRequestRejected proves the middleware refuses
// requests with no bearer token. Connect-RPC maps CodeUnauthenticated to
// HTTP 401 on the JSON codec, so we hit a known endpoint with a bare POST
// and assert on the status code. Catches regressions where the auth
// middleware gets unwired from a service (e.g. when a new handler is added
// without the middleware chain).
func TestE2E_Auth_UnauthenticatedRequestRejected(t *testing.T) {
	h := startStack(t)

	// ListOperators is a representative read endpoint protected by the
	// auth middleware. Any other authenticated endpoint would do.
	req, err := http.NewRequest(http.MethodPost,
		h.serverURL+"/nis.v1.OperatorService/ListOperators",
		bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST without bearer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401 Unauthorized, got %d. body: %s", resp.StatusCode, string(body))
	}
}

// TestE2E_Auth_InvalidBearerRejected proves a malformed/garbage bearer also
// gets 401 (not 500). Distinct path from "no header at all" — the middleware
// runs the token-parsing code instead of bailing on missing header.
func TestE2E_Auth_InvalidBearerRejected(t *testing.T) {
	h := startStack(t)

	req, err := http.NewRequest(http.MethodPost,
		h.serverURL+"/nis.v1.OperatorService/ListOperators",
		bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer not-a-real-jwt")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST with bogus bearer: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401 Unauthorized for bogus token, got %d. body: %s", resp.StatusCode, string(body))
	}
}

// TestE2E_RBAC_OperatorAdminCannotReadOtherOperators is the security
// boundary test for the operator-admin role. An operator-admin scoped to
// operator A must be able to read A but must NOT be able to read operator B.
// Breaking this is a silent cross-tenant data leak — the exact failure mode
// the RBAC layer exists to prevent.
//
// The CanReadOperator check in PermissionService is the load-bearing
// function; this test pins the e2e end of it so a future refactor that
// drops the check from the handler gets caught at the boundary instead of
// at the unit test layer.
func TestE2E_RBAC_OperatorAdminCannotReadOtherOperators(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Two operators owned by admin.
	operatorAID := h.createOperator(t, "rbac-operator-a")
	operatorBID := h.createOperator(t, "rbac-operator-b")

	// Create an operator-admin API user scoped to operator A only.
	const opAdminUser = "rbac-op-admin"
	const opAdminPass = "rbac-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	// Log in as the operator-admin and try to act as them.
	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	// Read on their OWN operator (A) must succeed.
	if _, err := opAdmin.operatorCli.GetOperator(ctx, connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: operatorAID,
	})); err != nil {
		t.Fatalf("operator-admin should be able to read their own operator (A): %v", err)
	}

	// Read on the OTHER operator (B) must fail with PermissionDenied —
	// not NotFound, not Internal, not anything that would leak whether B
	// exists. The friendly-error contract is intentional.
	_, err := opAdmin.operatorCli.GetOperator(ctx, connect.NewRequest(&nisv1.GetOperatorRequest{
		Id: operatorBID,
	}))
	if err == nil {
		t.Fatal("operator-admin should NOT be able to read another operator (B), but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied reading other operator, got %v: %v", got, err)
	}
}

// TestE2E_RBAC_OperatorAdminCannotCreateOperators proves the non-read
// boundary: operator-admins manage state within their own operator, but
// only `admin` can create new operators. Lock-in for the role definition.
func TestE2E_RBAC_OperatorAdminCannotCreateOperators(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorAID := h.createOperator(t, "rbac-create-op-a")

	const opAdminUser = "rbac-create-op-admin"
	const opAdminPass = "rbac-create-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	_, err := opAdmin.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name: "operator-admin-tried-to-create-this",
	}))
	if err == nil {
		t.Fatal("operator-admin should NOT be able to create a new operator, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied for CreateOperator as operator-admin, got %v: %v", got, err)
	}

	// Sanity check that the failure message is non-empty — a future
	// regression that silently swallowed the deny reason would leave
	// operators debugging blind.
	if strings.TrimSpace(err.Error()) == "" {
		t.Fatal("PermissionDenied error must carry a message; got empty")
	}
}

// TestE2E_RBAC_FilterOperatorsHidesOthers proves that ListOperators is
// filtered for non-admins — operator-admin's list view returns only their
// operator, not the others. Filtering is a separate code path from
// CanReadOperator (it's enforcement at projection time), so it deserves its
// own pin.
func TestE2E_RBAC_FilterOperatorsHidesOthers(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorAID := h.createOperator(t, "rbac-list-op-a")
	_ = h.createOperator(t, "rbac-list-op-b")
	_ = h.createOperator(t, "rbac-list-op-c")

	const opAdminUser = "rbac-list-op-admin"
	const opAdminPass = "rbac-list-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	resp, err := opAdmin.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{}))
	if err != nil {
		t.Fatalf("ListOperators as operator-admin: %v", err)
	}
	names := make([]string, 0, len(resp.Msg.Operators))
	for _, op := range resp.Msg.Operators {
		names = append(names, op.Name)
	}
	if len(names) != 1 || names[0] != "rbac-list-op-a" {
		t.Fatalf("operator-admin should see only their own operator; got %v", names)
	}
}
