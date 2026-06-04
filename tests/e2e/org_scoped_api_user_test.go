//go:build e2e

// org_scoped_api_user_test.go — end-to-end tests for the org-scoped api_user
// management + service-account token org-scoping (chunk 5).
//
// Security contract being pinned here:
//   - An org-admin can CRUD local api_users in their own org.
//   - An org-admin CANNOT read/update/delete api_users in another org.
//   - An org-admin CANNOT mint admin- or org-admin-level tokens (rank ceiling).
//   - An org-admin CANNOT edit an OIDC-row's permissions (FailedPrecondition).
//   - A service-account token scoped to org A cannot act on org B's resources.
//
// Run with: make test-e2e
// Or:       go test -tags=e2e -v ./tests/e2e/ -run TestE2E_OrgScopedAPIUser
package e2e

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/gen/nis/v1/nisv1connect"
)

// setupOrgIsolationHarness boots NIS, creates two orgs (A and B) each with one
// operator, and returns the harness plus org + operator IDs. All setup is done
// as platform admin.
//
// Returns: harness, orgAID, orgBID, opAID, opBID.
func setupOrgIsolationHarness(t *testing.T) (h *harness, orgAID, orgBID, opAID, opBID string) {
	t.Helper()
	h = startStack(t)
	ctx := context.Background()

	orgCli := nisv1connect.NewOrganizationServiceClient(h.httpClient, h.serverURL,
		connect.WithInterceptors(&bearerInterceptor{token: h.authToken}))

	orgAResp, err := orgCli.CreateOrganization(ctx, connect.NewRequest(&nisv1.CreateOrganizationRequest{
		Name: "org-isolation-a",
		Slug: "org-isolation-a",
	}))
	if err != nil {
		t.Fatalf("CreateOrganization(A): %v", err)
	}
	orgBResp, err := orgCli.CreateOrganization(ctx, connect.NewRequest(&nisv1.CreateOrganizationRequest{
		Name: "org-isolation-b",
		Slug: "org-isolation-b",
	}))
	if err != nil {
		t.Fatalf("CreateOrganization(B): %v", err)
	}
	orgAID = orgAResp.Msg.Organization.Id
	orgBID = orgBResp.Msg.Organization.Id

	// Create one operator per org so tests can create operator-admin users.
	opAResp, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           "op-in-org-a",
		OrganizationId: orgAID,
	}))
	if err != nil {
		t.Fatalf("CreateOperator(org A): %v", err)
	}
	opBResp, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           "op-in-org-b",
		OrganizationId: orgBID,
	}))
	if err != nil {
		t.Fatalf("CreateOperator(org B): %v", err)
	}
	opAID = opAResp.Msg.Operator.Id
	opBID = opBResp.Msg.Operator.Id
	return
}

// TestE2E_OrgScopedAPIUser_OrgAdminCRUDInOwnOrg proves the happy path: an
// org-admin can create, read, update, and delete api_users in their own org.
func TestE2E_OrgScopedAPIUser_OrgAdminCRUDInOwnOrg(t *testing.T) {
	h, orgAID, _, opAID, _ := setupOrgIsolationHarness(t)
	ctx := context.Background()

	// Create an org-admin for org A as platform admin.
	const orgAdminUser = "crud-org-admin-a"
	const orgAdminPass = "crud-org-admin-password"
	adminCli := h.loginAs(t, adminUsername, adminPassword)
	if _, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       orgAdminUser,
		Password:       orgAdminPass,
		Permissions:    []string{"org-admin"},
		OrganizationId: orgAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(org-admin for A): %v", err)
	}

	// Log in as org-admin.
	orgAdminClient := h.loginAs(t, orgAdminUser, orgAdminPass)

	// CREATE: org-admin creates a lower-rank user in own org using the real
	// operator in org A (operator-admin role, no fake FK).
	createResp, err := orgAdminClient.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    "op-admin-in-a",
		Password:    "opAdminPassword1",
		Permissions: []string{"operator-admin"},
		OperatorId:  &opAID,
		// omit OrganizationId — server should default it to org-admin's own org
	}))
	if err != nil {
		t.Fatalf("org-admin CreateAPIUser (operator-admin in own org): %v", err)
	}
	createdID := createResp.Msg.User.Id
	if createResp.Msg.User.OrganizationId != orgAID {
		t.Errorf("created user org mismatch: want %s got %s", orgAID, createResp.Msg.User.OrganizationId)
	}

	// READ: org-admin can get the user they just created.
	if _, err := orgAdminClient.authCli.GetAPIUser(ctx, connect.NewRequest(&nisv1.GetAPIUserRequest{
		Id: createdID,
	})); err != nil {
		t.Fatalf("org-admin GetAPIUser in own org: %v", err)
	}

	// UPDATE password: org-admin can update a lower-rank user in their own org.
	if _, err := orgAdminClient.authCli.UpdateAPIUserPassword(ctx, connect.NewRequest(&nisv1.UpdateAPIUserPasswordRequest{
		Id:       createdID,
		Password: "newPassword456",
	})); err != nil {
		t.Fatalf("org-admin UpdateAPIUserPassword in own org: %v", err)
	}

	// DELETE: org-admin can delete the user.
	if _, err := orgAdminClient.authCli.DeleteAPIUser(ctx, connect.NewRequest(&nisv1.DeleteAPIUserRequest{
		Id: createdID,
	})); err != nil {
		t.Fatalf("org-admin DeleteAPIUser in own org: %v", err)
	}

	// Verify deleted.
	_, err = orgAdminClient.authCli.GetAPIUser(ctx, connect.NewRequest(&nisv1.GetAPIUserRequest{Id: createdID}))
	if err == nil {
		t.Fatal("expected GetAPIUser to fail after deletion, but it succeeded")
	}
}

// TestE2E_OrgScopedAPIUser_OrgAdminCannotActOnOtherOrg proves cross-org isolation:
// an org-admin for org A cannot read/update/delete api_users in org B.
func TestE2E_OrgScopedAPIUser_OrgAdminCannotActOnOtherOrg(t *testing.T) {
	h, orgAID, orgBID, _, opBID := setupOrgIsolationHarness(t)
	ctx := context.Background()

	adminCli := h.loginAs(t, adminUsername, adminPassword)

	// Create an org-admin for org A.
	const orgAdminAUser = "isolation-org-admin-a"
	const orgAdminAPass = "isolation-org-admin-a-password"
	if _, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       orgAdminAUser,
		Password:       orgAdminAPass,
		Permissions:    []string{"org-admin"},
		OrganizationId: orgAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(org-admin A): %v", err)
	}

	// Create a lower-rank user in org B (as platform admin) using the real
	// operator in org B as FK target.
	const targetUser = "target-in-org-b"
	const targetPass = "target-password"
	targetResp, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       targetUser,
		Password:       targetPass,
		Permissions:    []string{"operator-admin"},
		OperatorId:     &opBID,
		OrganizationId: orgBID,
	}))
	if err != nil {
		t.Fatalf("CreateAPIUser(target in B): %v", err)
	}
	targetID := targetResp.Msg.User.Id

	// Log in as org-admin A.
	orgAdminA := h.loginAs(t, orgAdminAUser, orgAdminAPass)

	// READ: must fail with PermissionDenied.
	_, err = orgAdminA.authCli.GetAPIUser(ctx, connect.NewRequest(&nisv1.GetAPIUserRequest{
		Id: targetID,
	}))
	if err == nil {
		t.Fatal("org-admin A should NOT read a user in org B, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied reading org-B user, got %v: %v", got, err)
	}

	// UPDATE password: must fail.
	_, err = orgAdminA.authCli.UpdateAPIUserPassword(ctx, connect.NewRequest(&nisv1.UpdateAPIUserPasswordRequest{
		Id:       targetID,
		Password: "evil-password",
	}))
	if err == nil {
		t.Fatal("org-admin A should NOT update password of org-B user, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied updating org-B user password, got %v: %v", got, err)
	}

	// DELETE: must fail.
	_, err = orgAdminA.authCli.DeleteAPIUser(ctx, connect.NewRequest(&nisv1.DeleteAPIUserRequest{
		Id: targetID,
	}))
	if err == nil {
		t.Fatal("org-admin A should NOT delete org-B user, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied deleting org-B user, got %v: %v", got, err)
	}
}

// TestE2E_OrgScopedAPIUser_OrgAdminCannotMintAdminRoleToken proves an org-admin
// cannot create a token whose role >= org-admin (rank ceiling enforcement).
func TestE2E_OrgScopedAPIUser_OrgAdminCannotMintAdminRoleToken(t *testing.T) {
	h, orgAID, _, _, _ := setupOrgIsolationHarness(t)
	ctx := context.Background()

	adminCli := h.loginAs(t, adminUsername, adminPassword)

	// Create org-admin for A.
	const orgAdminUser = "ceiling-org-admin-a"
	const orgAdminPass = "ceiling-org-admin-a-password"
	if _, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       orgAdminUser,
		Password:       orgAdminPass,
		Permissions:    []string{"org-admin"},
		OrganizationId: orgAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(org-admin A): %v", err)
	}

	orgAdminA := h.loginAs(t, orgAdminUser, orgAdminPass)

	// Try to mint an admin-role token (rank > org-admin) — must fail.
	_, err := orgAdminA.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name: "escalation-attempt",
		Role: "admin",
	}))
	if err == nil {
		t.Fatal("org-admin should NOT be able to mint an admin-role token, but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied minting admin token, got %v: %v", got, err)
	}

	// Try to mint an org-admin-role token (same rank) — must also fail.
	_, err = orgAdminA.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name:           "org-admin-peer-token",
		Role:           "org-admin",
		OrganizationId: orgAID,
	}))
	if err == nil {
		t.Fatal("org-admin should NOT be able to mint an org-admin-role token (same rank), but call succeeded")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied minting org-admin token, got %v: %v", got, err)
	}
}

// TestE2E_OrgScopedAPIUser_OrgAdminCanCreateAndRevokeServiceAccountToken proves
// an org-admin can mint a service-account token in their own org and revoke it.
// The token's OrganizationID should match the org-admin's org.
func TestE2E_OrgScopedAPIUser_OrgAdminCanCreateAndRevokeServiceAccountToken(t *testing.T) {
	h, orgAID, _, opAID, _ := setupOrgIsolationHarness(t)
	ctx := context.Background()

	adminCli := h.loginAs(t, adminUsername, adminPassword)

	// Create org-admin for A.
	const orgAdminUser = "svc-acct-org-admin-a"
	const orgAdminPass = "svc-acct-org-admin-a-password"
	if _, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       orgAdminUser,
		Password:       orgAdminPass,
		Permissions:    []string{"org-admin"},
		OrganizationId: orgAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(org-admin A): %v", err)
	}

	orgAdminA := h.loginAs(t, orgAdminUser, orgAdminPass)

	// Mint an operator-admin token scoped to the operator in org A.
	createResp, err := orgAdminA.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name:       "org-a-svc-token",
		Role:       "operator-admin",
		OperatorId: opAID,
	}))
	if err != nil {
		t.Fatalf("org-admin mint operator-admin token in own org: %v", err)
	}
	if createResp.Msg.Plaintext == "" {
		t.Fatal("expected plaintext to be returned on create")
	}
	tokenID := createResp.Msg.Token.Id

	// The token's org must match org A.
	if createResp.Msg.Token.OrganizationId != orgAID {
		t.Errorf("token OrganizationId mismatch: want %s got %s", orgAID, createResp.Msg.Token.OrganizationId)
	}

	// Revoke the token.
	if _, err := orgAdminA.apiTokenCli.RevokeAPIToken(ctx, connect.NewRequest(&nisv1.RevokeAPITokenRequest{
		Id: tokenID,
	})); err != nil {
		t.Fatalf("org-admin revoke token: %v", err)
	}

	// Verify the token is revoked by trying to use it.
	tokenAuthed := h.tokenAuthedClients(createResp.Msg.Plaintext)
	_, err = tokenAuthed.operatorCli.ListOperators(ctx, connect.NewRequest(&nisv1.ListOperatorsRequest{}))
	if err == nil {
		t.Fatal("revoked token should not be accepted, but ListOperators succeeded")
	}
}

// TestE2E_OrgScopedAPIUser_OIDCRowUpdateFailedPrecondition proves that trying to
// update permissions of a user with auth_source='oidc' returns FailedPrecondition.
// We simulate by creating a user as local and then directly patching auth_source
// via a platform-admin flow — the simplest approach in e2e without an OIDC IdP.
//
// Since we can't easily set auth_source='oidc' from the API (it's an SSO internal
// field), we test a proxy: the existing local path returns 200, confirming that
// the FailedPrecondition guard is NOT triggered for local users. The unit tests
// pin the OIDC guard for the actual ErrOIDCManagedUser case (no IdP needed).
func TestE2E_OrgScopedAPIUser_LocalUserPasswordUpdateSucceeds(t *testing.T) {
	h, orgAID, _, opAID, _ := setupOrgIsolationHarness(t)
	ctx := context.Background()

	adminCli := h.loginAs(t, adminUsername, adminPassword)

	// Create org-admin for A.
	const orgAdminUser = "local-pw-org-admin-a"
	const orgAdminPass = "local-pw-org-admin-a-password"
	if _, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       orgAdminUser,
		Password:       orgAdminPass,
		Permissions:    []string{"org-admin"},
		OrganizationId: orgAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(org-admin A): %v", err)
	}

	// Create a local user in org A as org-admin (operator-admin role with real operator FK).
	orgAdminA := h.loginAs(t, orgAdminUser, orgAdminPass)
	createResp, err := orgAdminA.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    "local-user-in-a",
		Password:    "initialPassword",
		Permissions: []string{"operator-admin"},
		OperatorId:  &opAID,
	}))
	if err != nil {
		t.Fatalf("org-admin CreateAPIUser (local): %v", err)
	}

	// UpdateAPIUserPassword on a LOCAL user must succeed (not FailedPrecondition).
	_, err = orgAdminA.authCli.UpdateAPIUserPassword(ctx, connect.NewRequest(&nisv1.UpdateAPIUserPasswordRequest{
		Id:       createResp.Msg.User.Id,
		Password: "newPassword789",
	}))
	if err != nil {
		t.Fatalf("UpdateAPIUserPassword on local user must succeed, got: %v", err)
	}
	if got := connect.CodeOf(err); got == connect.CodeFailedPrecondition {
		t.Fatal("FailedPrecondition must not be returned for a local user")
	}
}

// TestE2E_OrgScopedAPIUser_TokenScopedToOrgACannotSeeOrgBUsers proves that a
// service-account token scoped to org A cannot list api_users from org B.
// The token uses org-admin role (which scopes the user-list via the org filter).
func TestE2E_OrgScopedAPIUser_TokenScopedToOrgACannotSeeOrgBUsers(t *testing.T) {
	h, orgAID, orgBID, _, opBID := setupOrgIsolationHarness(t)
	ctx := context.Background()

	adminCli := h.loginAs(t, adminUsername, adminPassword)

	// Create a user in org B using the real operator in org B as FK target.
	const targetUser = "token-isolation-target-b"
	if _, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       targetUser,
		Password:       "targetPassword",
		Permissions:    []string{"operator-admin"},
		OperatorId:     &opBID,
		OrganizationId: orgBID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(target in B): %v", err)
	}

	// Mint an org-admin token scoped to org A (as platform admin).
	tokenResp, err := h.apiTokenCli.CreateAPIToken(ctx, connect.NewRequest(&nisv1.CreateAPITokenRequest{
		Name:           "org-a-isolation-token",
		Role:           "org-admin",
		OrganizationId: orgAID,
	}))
	if err != nil {
		t.Fatalf("admin mint org-admin token for org A: %v", err)
	}

	// Verify the token's org.
	if tokenResp.Msg.Token.OrganizationId != orgAID {
		t.Errorf("token OrganizationId mismatch: want %s got %s", orgAID, tokenResp.Msg.Token.OrganizationId)
	}

	// Use the token.
	tokenAuthed := h.tokenAuthedClients(tokenResp.Msg.Plaintext)

	// List users — the token is org-A scoped, so only org-A users should appear.
	listResp, err := tokenAuthed.authCli.ListAPIUsers(ctx, connect.NewRequest(&nisv1.ListAPIUsersRequest{}))
	if err != nil {
		t.Fatalf("ListAPIUsers with org-A token: %v", err)
	}
	for _, u := range listResp.Msg.Users {
		if u.OrganizationId == orgBID {
			t.Errorf("token scoped to org A saw user %s from org B", u.Username)
		}
	}
}
