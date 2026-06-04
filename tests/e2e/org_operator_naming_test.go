//go:build e2e

// org_operator_naming_test.go — end-to-end coverage for per-org operator names
// and the admin-must-specify-org guard (2026-06-04 feature).
//
// Contract being pinned here:
//   - A platform admin MUST supply organization_id on CreateOperator; an empty
//     field is InvalidArgument, not a silent fall-through to the default org.
//   - The SAME operator name may exist in two different orgs; a duplicate name
//     WITHIN one org is rejected.
//   - GetOperatorByName for a platform admin REQUIRES organization_id — names
//     are per-org so a name alone is meaningless; an empty org is
//     InvalidArgument, never a lenient unique-name guess.
//   - A manifest apply by a platform admin with no target org fails when it must
//     create an operator; an org-scoped token is pinned to its own org and the
//     adapter's org field is ignored entirely.
//
// Run with: make test-e2e
// Or:       go test -tags=e2e -v ./tests/e2e/ -run TestE2E_Operator
package e2e

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
	"github.com/thomas-maurice/nis/pkg/manifest"
)

// TestE2E_Operator_AdminMustSpecifyOrg pins the core guard: a platform admin
// cannot create an operator without naming a target org.
func TestE2E_Operator_AdminMustSpecifyOrg(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Empty org → InvalidArgument.
	_, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name: "needs-an-org",
	}))
	if err == nil {
		t.Fatal("admin CreateOperator with empty organization_id should fail")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument; err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "organization_id is required") {
		t.Fatalf("err = %q, want it to mention 'organization_id is required'", err.Error())
	}

	// Valid (default) org → success.
	if _, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           "has-an-org",
		OrganizationId: defaultOrgID,
	})); err != nil {
		t.Fatalf("admin CreateOperator with default org should succeed: %v", err)
	}
}

// TestE2E_Operator_SameNameAcrossOrgs proves operator names are unique per org,
// not globally: the same name succeeds in two orgs but collides within one.
func TestE2E_Operator_SameNameAcrossOrgs(t *testing.T) {
	h, orgAID, orgBID, _, _ := setupOrgIsolationHarness(t)
	ctx := context.Background()

	const sharedName = "shared-op-name"

	if _, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           sharedName,
		OrganizationId: orgAID,
	})); err != nil {
		t.Fatalf("CreateOperator(%q, org A): %v", sharedName, err)
	}

	// Same name, different org → allowed.
	if _, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           sharedName,
		OrganizationId: orgBID,
	})); err != nil {
		t.Fatalf("CreateOperator(%q, org B) should succeed (per-org uniqueness): %v", sharedName, err)
	}

	// Same name, same org → rejected.
	if _, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           sharedName,
		OrganizationId: orgAID,
	})); err == nil {
		t.Fatalf("CreateOperator(%q, org A) twice should fail on duplicate within org", sharedName)
	}
}

// TestE2E_Operator_GetByNameRequiresOrgForAdmin proves a platform admin MUST
// supply organization_id on a by-name lookup — names are per-org, so a name
// alone is ambiguous and is rejected with InvalidArgument rather than guessed.
// Passing organization_id resolves to that org's operator.
func TestE2E_Operator_GetByNameRequiresOrgForAdmin(t *testing.T) {
	h, orgAID, orgBID, _, _ := setupOrgIsolationHarness(t)
	ctx := context.Background()

	const dupName = "per-org-op"

	opA, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           dupName,
		OrganizationId: orgAID,
	}))
	if err != nil {
		t.Fatalf("CreateOperator(org A): %v", err)
	}
	if _, err := h.operatorCli.CreateOperator(ctx, connect.NewRequest(&nisv1.CreateOperatorRequest{
		Name:           dupName,
		OrganizationId: orgBID,
	})); err != nil {
		t.Fatalf("CreateOperator(org B): %v", err)
	}

	// No org as platform admin → InvalidArgument, regardless of uniqueness.
	_, err = h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name: dupName,
	}))
	if err == nil {
		t.Fatal("admin GetOperatorByName with no org should fail")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument; err = %v", got, err)
	}
	if !strings.Contains(err.Error(), "organization_id is required") {
		t.Fatalf("err = %q, want it to mention 'organization_id is required'", err.Error())
	}

	// Org A supplied → resolves to org A's operator.
	resp, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name:           dupName,
		OrganizationId: orgAID,
	}))
	if err != nil {
		t.Fatalf("GetOperatorByName(org A): %v", err)
	}
	if got := resp.Msg.GetOperator().GetId(); got != opA.Msg.GetOperator().GetId() {
		t.Fatalf("resolved operator id = %s, want org A's %s", got, opA.Msg.GetOperator().GetId())
	}
}

// TestE2E_Manifest_AdminApplyRequiresOrg proves a platform admin's manifest
// apply fails when it must create an operator but no target org is set.
func TestE2E_Manifest_AdminApplyRequiresOrg(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	c, err := client.NewClient(h.serverURL, h.authToken)
	if err != nil {
		t.Fatal(err)
	}
	// Admin adapter with NO target org.
	pc := manifest.NewClientAdapter(c, "")

	batch := baseObjects("noorg-op", "noorg-acc", "writer", "noorg-user")

	// The org guard trips as soon as the planner resolves current state via
	// GetOperatorByName; if Plan somehow succeeds, Apply must still fail. Either
	// way the error must name the missing org.
	plan, err := manifest.Plan(ctx, pc, batch)
	if err == nil {
		_, err = manifest.Apply(ctx, pc, plan)
	}
	if err == nil {
		t.Fatal("admin plan/apply that creates an operator with no target org should fail")
	}
	if !strings.Contains(err.Error(), "organization_id is required") {
		t.Fatalf("err = %q, want it to mention 'organization_id is required'", err.Error())
	}
}

// TestE2E_Manifest_OrgScopedTokenPinnedToOwnOrg proves an org-scoped token's
// manifest apply lands in the token's own org and the adapter org field is
// ignored — even when set to a different org.
func TestE2E_Manifest_OrgScopedTokenPinnedToOwnOrg(t *testing.T) {
	h, orgAID, orgBID, _, _ := setupOrgIsolationHarness(t)
	ctx := context.Background()

	adminCli := h.loginAs(t, adminUsername, adminPassword)

	const orgAdminUser = "manifest-org-admin-a"
	const orgAdminPass = "manifest-org-admin-a-password"
	if _, err := adminCli.authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:       orgAdminUser,
		Password:       orgAdminPass,
		Permissions:    []string{"org-admin"},
		OrganizationId: orgAID,
	})); err != nil {
		t.Fatalf("CreateAPIUser(org-admin A): %v", err)
	}
	orgAdminA := h.loginAs(t, orgAdminUser, orgAdminPass)

	// Build a planner with the org-A admin's session credential, but point the
	// adapter at org B to prove the request-side org is ignored for an
	// org-scoped caller (they are pinned to their own org).
	tokenClient, err := client.NewClient(h.serverURL, orgAdminA.token)
	if err != nil {
		t.Fatal(err)
	}
	pc := manifest.NewClientAdapter(tokenClient, orgBID)

	const opName = "pinned-op"
	batch := baseObjects(opName, "pinned-acc", "writer", "pinned-user")
	plan, err := manifest.Plan(ctx, pc, batch)
	if err != nil {
		t.Fatalf("manifest.Plan: %v", err)
	}
	if _, err := manifest.Apply(ctx, pc, plan); err != nil {
		t.Fatalf("org-scoped apply should succeed in own org: %v", err)
	}

	// The operator must have landed in org A (token's org), not org B.
	resp, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{
		Name:           opName,
		OrganizationId: orgAID,
	}))
	if err != nil {
		t.Fatalf("GetOperatorByName(%q, org A): %v", opName, err)
	}
	if got := resp.Msg.GetOperator().GetOrganizationId(); got != orgAID {
		t.Fatalf("operator OrganizationId = %s, want org A %s", got, orgAID)
	}
}
