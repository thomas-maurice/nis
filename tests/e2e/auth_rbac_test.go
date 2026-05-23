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

// TestE2E_RBAC_ListAccountJWTRevocationsScope pins the P13 panel's authz
// boundary: an operator-admin scoped to operator A must not be able to list
// revocations for an account belonging to operator B. The handler runs the
// same CanReadAccount gate as GetAccount; this test exists so a future
// refactor that drops the gate gets caught at the boundary.
func TestE2E_RBAC_ListAccountJWTRevocationsScope(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorAID := h.createOperator(t, "rbac-rev-op-a")
	operatorBID := h.createOperator(t, "rbac-rev-op-b")
	accountBID := h.createAccount(t, operatorBID, "rbac-rev-acc-b")

	const opAdminUser = "rbac-rev-op-admin"
	const opAdminPass = "rbac-rev-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	_, err := opAdmin.accountCli.ListAccountJWTRevocations(ctx, connect.NewRequest(&nisv1.ListAccountJWTRevocationsRequest{
		AccountId: accountBID,
	}))
	if err == nil {
		t.Fatal("operator-admin of A must NOT list revocations on B's account, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %v: %v", got, err)
	}
}

// TestE2E_RBAC_OperatorAdminCannotUpdateOtherOperatorAccount is the e2e
// smoke for the A5 fix: an operator-admin scoped to operator A must NOT be
// able to mutate an account in operator B. Pre-fix (2026-05-23) the
// AccountHandler.UpdateAccount path was missing the permService check, so
// the call succeeded silently — cross-tenant write, the exact bug class A5
// was filed to prevent. This test pins the handler wire-up; the broader
// table-driven coverage lives in
// internal/integration/handler_rbac_isolation_test.go.
func TestE2E_RBAC_OperatorAdminCannotUpdateOtherOperatorAccount(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorAID := h.createOperator(t, "rbac-update-op-a")
	operatorBID := h.createOperator(t, "rbac-update-op-b")
	accountBID := h.createAccount(t, operatorBID, "op-b-account")

	const opAdminUser = "rbac-update-op-admin"
	const opAdminPass = "rbac-update-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)

	renamed := "renamed-by-attacker"
	desc := "should never apply"
	_, err := opAdmin.accountCli.UpdateAccount(ctx, connect.NewRequest(&nisv1.UpdateAccountRequest{
		Id:          accountBID,
		Name:        &renamed,
		Description: &desc,
	}))
	if err == nil {
		t.Fatal("operator-admin of A must NOT update an account in operator B, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied for cross-operator UpdateAccount, got %v: %v", got, err)
	}
}

// TestE2E_RBAC_OperatorAdminCanSyncOwnCluster_RejectsForeign is the A18
// regression test. Before 2026-05-23, ClusterHandler.SyncCluster and
// ClusterHandler.ReconcileAccountOnCluster routed through
// PermissionService.CanUpdateOperator (admin-only), so operator-admins
// couldn't sync their own clusters — inconsistent with the rest of their
// role. Switched to CanSyncCluster (admin OR operator-admin scoped to the
// cluster's operator). Three assertions per RPC:
//
//  1. operator-admin A on operator A's cluster: must get past the permission
//     gate. (The call still fails further down because the cluster has no
//     credentials configured — that's a FailedPrecondition / Internal, NOT
//     PermissionDenied. The point is the gate.)
//  2. operator-admin A on operator B's cluster: PermissionDenied.
//  3. account-admin on operator A's cluster: PermissionDenied (CanSyncCluster
//     explicitly denies account-admins).
func TestE2E_RBAC_OperatorAdminCanSyncOwnCluster_RejectsForeign(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	operatorAID := h.createOperator(t, "rbac-sync-op-a")
	operatorBID := h.createOperator(t, "rbac-sync-op-b")
	clusterAID := h.createCluster(t, operatorAID, "rbac-sync-cluster-a", "nats://127.0.0.1:14222")
	clusterBID := h.createCluster(t, operatorBID, "rbac-sync-cluster-b", "nats://127.0.0.1:14223")
	accountAID := h.createAccount(t, operatorAID, "rbac-sync-acc-a")

	const opAdminUser = "rbac-sync-op-admin"
	const opAdminPass = "rbac-sync-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &operatorAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin): %v", err)
	}

	// Account-admin scoped to an account in operator A, used for the
	// third assertion below.
	const accAdminUser = "rbac-sync-acc-admin"
	const accAdminPass = "rbac-sync-acc-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    accAdminUser,
		Password:    accAdminPass,
		Permissions: []string{"account-admin"},
		AccountId:   &accountAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(account-admin): %v", err)
	}

	opAdmin := h.loginAs(t, opAdminUser, opAdminPass)
	accAdmin := h.loginAs(t, accAdminUser, accAdminPass)

	// (1) operator-admin A on own cluster — must get past the gate.
	_, err := opAdmin.clusterCli.SyncCluster(ctx, connect.NewRequest(&nisv1.SyncClusterRequest{Id: clusterAID}))
	if err != nil {
		if got := connect.CodeOf(err); got == connect.CodePermissionDenied {
			t.Fatalf("operator-admin should be able to sync their own cluster (past the permission gate); got PermissionDenied: %v", err)
		}
		// Any other error is acceptable — the cluster has no credentials
		// configured so the service-layer push will fail with
		// FailedPrecondition / Internal. We only care about the gate here.
	}

	// (2) operator-admin A on operator B's cluster — PermissionDenied.
	_, err = opAdmin.clusterCli.SyncCluster(ctx, connect.NewRequest(&nisv1.SyncClusterRequest{Id: clusterBID}))
	if err == nil {
		t.Fatal("operator-admin of A must NOT sync operator B's cluster, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied for cross-operator SyncCluster, got %v: %v", got, err)
	}

	// (3) account-admin on operator A's cluster — PermissionDenied (cluster
	// sync is above account-admin's pay grade, even on their own operator).
	_, err = accAdmin.clusterCli.SyncCluster(ctx, connect.NewRequest(&nisv1.SyncClusterRequest{Id: clusterAID}))
	if err == nil {
		t.Fatal("account-admin must NOT sync any cluster, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied for account-admin SyncCluster, got %v: %v", got, err)
	}

	// Same three assertions for ReconcileAccountOnCluster — paths share the
	// CanSyncCluster check now but they're separate handler methods. Pin
	// both so a future regression to one doesn't silently take both down.

	// (1) operator-admin A reconciling their own account on their own cluster.
	_, err = opAdmin.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: clusterAID,
		AccountId: accountAID,
	}))
	if err != nil {
		if got := connect.CodeOf(err); got == connect.CodePermissionDenied {
			t.Fatalf("operator-admin should be able to reconcile their own account on their own cluster; got PermissionDenied: %v", err)
		}
	}

	// (2) operator-admin A reconciling against operator B's cluster.
	_, err = opAdmin.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: clusterBID,
		AccountId: accountAID,
	}))
	if err == nil {
		t.Fatal("operator-admin of A must NOT reconcile on operator B's cluster, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied for cross-operator ReconcileAccountOnCluster, got %v: %v", got, err)
	}

	// (3) account-admin on operator A's cluster.
	_, err = accAdmin.clusterCli.ReconcileAccountOnCluster(ctx, connect.NewRequest(&nisv1.ReconcileAccountOnClusterRequest{
		ClusterId: clusterAID,
		AccountId: accountAID,
	}))
	if err == nil {
		t.Fatal("account-admin must NOT reconcile on any cluster, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied for account-admin ReconcileAccountOnCluster, got %v: %v", got, err)
	}
}
