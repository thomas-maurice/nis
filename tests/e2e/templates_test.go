//go:build e2e

// templates_test.go — end-to-end coverage for P6 (permission templates).
// Scenarios: CRUD + versioning, create-from-template, bump applies new
// permissions (verified through the regenerated account JWT), drift
// flag, delete-blocked-by-dependents, detach, operator-admin
// cross-operator isolation, RBAC unauthenticated rejection.
//
// One scenario per top-level test. Tests that need to verify a NATS-side
// effect (bump rolls perms through) boot their own NATS via the
// standard harness; pure-RPC tests skip docker entirely.
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// TestE2E_Templates_CreateGetListUpdateDelete covers the RPC surface:
// CreateTemplate stamps v1, GetTemplate returns it, ListTemplates
// includes it, UpdateTemplate with permission diff bumps to v2,
// UpdateTemplate with no diff is a noop, DeleteTemplate succeeds on a
// template with no dependents.
func TestE2E_Templates_CreateGetListUpdateDelete(t *testing.T) {
	h := startStack(t)
	opID := h.createOperator(t, "tpl-crud-op")

	createResp, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId:  opID,
		Name:        "reader",
		Description: "Read-only service",
		Permissions: &nisv1.UserPermissions{
			SubAllow: []string{"events.>"},
		},
		ChangeNote: "initial",
	}))
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if createResp.Msg.Template.LatestVersion != 1 {
		t.Fatalf("expected latest_version=1, got %d", createResp.Msg.Template.LatestVersion)
	}
	if createResp.Msg.Version.VersionNumber != 1 {
		t.Fatalf("expected v1, got %d", createResp.Msg.Version.VersionNumber)
	}

	getResp, err := h.templateCli.GetTemplateByName(context.Background(), connect.NewRequest(&nisv1.GetTemplateByNameRequest{
		OperatorId: opID,
		Name:       "reader",
	}))
	if err != nil {
		t.Fatalf("GetTemplateByName: %v", err)
	}
	if getResp.Msg.Template.Id != createResp.Msg.Template.Id {
		t.Fatalf("GetTemplateByName returned different ID")
	}

	listResp, err := h.templateCli.ListTemplates(context.Background(), connect.NewRequest(&nisv1.ListTemplatesRequest{
		OperatorId: opID,
	}))
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(listResp.Msg.Templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(listResp.Msg.Templates))
	}

	// Permission edit ⇒ new version row + latest_version bump.
	upResp, err := h.templateCli.UpdateTemplate(context.Background(), connect.NewRequest(&nisv1.UpdateTemplateRequest{
		Id: createResp.Msg.Template.Id,
		Permissions: &nisv1.UserPermissions{
			SubAllow: []string{"events.>", "metrics.>"},
		},
		ChangeNote: "add metrics",
	}))
	if err != nil {
		t.Fatalf("UpdateTemplate (bump): %v", err)
	}
	if upResp.Msg.Template.LatestVersion != 2 {
		t.Fatalf("expected v2 after bump, got %d", upResp.Msg.Template.LatestVersion)
	}
	if upResp.Msg.Version == nil || upResp.Msg.Version.VersionNumber != 2 {
		t.Fatalf("expected new version row v2, got %+v", upResp.Msg.Version)
	}

	// Same permissions ⇒ no bump.
	up2Resp, err := h.templateCli.UpdateTemplate(context.Background(), connect.NewRequest(&nisv1.UpdateTemplateRequest{
		Id: createResp.Msg.Template.Id,
		Permissions: &nisv1.UserPermissions{
			SubAllow: []string{"events.>", "metrics.>"},
		},
	}))
	if err != nil {
		t.Fatalf("UpdateTemplate (no diff): %v", err)
	}
	if up2Resp.Msg.Template.LatestVersion != 2 {
		t.Fatalf("expected latest still v2, got %d", up2Resp.Msg.Template.LatestVersion)
	}

	// ListVersions surfaces both.
	versResp, err := h.templateCli.ListTemplateVersions(context.Background(), connect.NewRequest(&nisv1.ListTemplateVersionsRequest{
		TemplateId: createResp.Msg.Template.Id,
	}))
	if err != nil {
		t.Fatalf("ListTemplateVersions: %v", err)
	}
	if len(versResp.Msg.Versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versResp.Msg.Versions))
	}

	if _, err := h.templateCli.DeleteTemplate(context.Background(), connect.NewRequest(&nisv1.DeleteTemplateRequest{
		Id: createResp.Msg.Template.Id,
	})); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
}

// TestE2E_Templates_CreateScopedKeyFromTemplate creates an SKK with a
// TemplateRef, verifies the resulting SKK carries the template binding +
// the snapshot's permissions, and confirms DetachFromTemplate clears
// the binding without touching the permission columns.
func TestE2E_Templates_CreateScopedKeyFromTemplate(t *testing.T) {
	h := startStack(t)
	opID := h.createOperator(t, "tpl-create-op")
	accID := h.createAccount(t, opID, "tpl-create-acc")

	tplResp, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId: opID,
		Name:       "writer",
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{"writes.>"},
			SubAllow: []string{"_INBOX.>"},
		},
	}))
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	skkResp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: accID,
		Name:      "writer-skk",
		Template: &nisv1.TemplateRef{
			OperatorId:   opID,
			TemplateName: "writer",
		},
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey with template: %v", err)
	}
	if skkResp.Msg.Key.TemplateId != tplResp.Msg.Template.Id {
		t.Fatalf("expected SKK.template_id=%s, got %s", tplResp.Msg.Template.Id, skkResp.Msg.Key.TemplateId)
	}
	if skkResp.Msg.Key.TemplateVersion != 1 {
		t.Fatalf("expected SKK.template_version=1, got %d", skkResp.Msg.Key.TemplateVersion)
	}
	if skkResp.Msg.Key.TemplateDrifted {
		t.Fatal("expected fresh templated SKK to not be drifted")
	}
	// Snapshot landed in permission columns.
	if len(skkResp.Msg.Key.Permissions.PubAllow) != 1 || skkResp.Msg.Key.Permissions.PubAllow[0] != "writes.>" {
		t.Fatalf("expected pub_allow=[writes.>], got %v", skkResp.Msg.Key.Permissions.PubAllow)
	}

	// Detach: clears binding, leaves permissions.
	detachResp, err := h.keyCli.DetachFromTemplate(context.Background(), connect.NewRequest(&nisv1.DetachFromTemplateRequest{
		Id: skkResp.Msg.Key.Id,
	}))
	if err != nil {
		t.Fatalf("DetachFromTemplate: %v", err)
	}
	if detachResp.Msg.Key.TemplateId != "" {
		t.Fatalf("expected SKK.template_id cleared after detach, got %q", detachResp.Msg.Key.TemplateId)
	}
	if detachResp.Msg.Key.TemplateVersion != 0 {
		t.Fatalf("expected SKK.template_version cleared after detach, got %d", detachResp.Msg.Key.TemplateVersion)
	}
	if len(detachResp.Msg.Key.Permissions.PubAllow) != 1 || detachResp.Msg.Key.Permissions.PubAllow[0] != "writes.>" {
		t.Fatalf("expected permissions preserved after detach, got %v", detachResp.Msg.Key.Permissions.PubAllow)
	}
}

// TestE2E_Templates_BumpAppliesNewPermissionsToNATS is the live-NATS
// integration: an SKK is created from v1 (sub_allow=events.>), a user
// is signed by it and connects, the template is bumped to v2
// (sub_deny=events.secret.>), Apply rolls the new scope into the SKK
// and the auto-sync push lands it on NATS — a fresh connect under the
// same user is now denied on events.secret.>. Without the auto-sync
// portion of P6 the new perms would only land after a manual cluster
// sync; this test pins that we don't regress to that behaviour.
func TestE2E_Templates_BumpAppliesNewPermissionsToNATS(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "tpl-bump")

	// Create a template + a SKK from it + a user signed by the SKK.
	tplResp, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId: s.operatorID,
		Name:       "bump-tpl",
		Permissions: &nisv1.UserPermissions{
			SubAllow: []string{"events.>"},
			PubAllow: []string{">"},
		},
	}))
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	skkResp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: s.accountID,
		Name:      "bump-skk",
		Template: &nisv1.TemplateRef{
			OperatorId:   s.operatorID,
			TemplateName: "bump-tpl",
		},
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey from template: %v", err)
	}
	userID := h.createScopedUser(t, s.accountID, "bump-user", skkResp.Msg.Key.Id)
	// Auto-sync wired in serve.go pushes after the SKK + user creates;
	// no manual sync required.
	credsPath := h.fetchUserCreds(t, userID, "bump-user")

	// v1 allows events.* — initial connect can subscribe without errors.
	{
		nc, errCh := dialWithErrCh(t, h.natsURL, credsPath)
		defer nc.Close()
		if _, err := nc.SubscribeSync("events.public"); err != nil {
			t.Fatalf("v1 subscribe events.public: %v", err)
		}
		if err := nc.FlushTimeout(2 * time.Second); err != nil {
			t.Fatalf("flush: %v", err)
		}
		expectNoAsyncError(t, errCh, 300*time.Millisecond)
		nc.Close()
	}

	// Bump template to v2 — add sub_deny on events.secret.>.
	if _, err := h.templateCli.UpdateTemplate(context.Background(), connect.NewRequest(&nisv1.UpdateTemplateRequest{
		Id: tplResp.Msg.Template.Id,
		Permissions: &nisv1.UserPermissions{
			SubAllow: []string{"events.>"},
			SubDeny:  []string{"events.secret.>"},
			PubAllow: []string{">"},
		},
		ChangeNote: "lock down secrets",
	})); err != nil {
		t.Fatalf("UpdateTemplate to v2: %v", err)
	}

	// Apply v2 to the SKK. This is the explicit-roll-out path —
	// template update alone never auto-cascades.
	bumpResp, err := h.templateCli.ApplyTemplateToScopedKey(context.Background(), connect.NewRequest(&nisv1.ApplyTemplateToScopedKeyRequest{
		ScopedSigningKeyId: skkResp.Msg.Key.Id,
	}))
	if err != nil {
		t.Fatalf("ApplyTemplateToScopedKey: %v", err)
	}
	if bumpResp.Msg.Key.TemplateVersion != 2 {
		t.Fatalf("expected SKK pinned to v2 after bump, got v%d", bumpResp.Msg.Key.TemplateVersion)
	}

	// New connect with the SAME credentials — the bump auto-pushed the
	// re-signed account JWT to NATS, so the new scope template applies
	// immediately. events.public still works; events.secret.foo is
	// denied via async permission violation.
	nc, errCh := dialWithErrCh(t, h.natsURL, credsPath)
	defer nc.Close()
	if _, err := nc.SubscribeSync("events.public"); err != nil {
		t.Fatalf("v2 subscribe events.public: %v", err)
	}
	if _, err := nc.SubscribeSync("events.secret.foo"); err != nil {
		t.Fatalf("v2 subscribe events.secret.foo (expected to attempt locally): %v", err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("flush after v2 subscribe: %v", err)
	}
	expectAsyncPermissionError(t, errCh, "events.secret.foo", 2*time.Second)
}

// TestE2E_Templates_DirectEditFlagsDrifted edits the permission columns
// of a templated SKK directly (via UpdateScopedSigningKey) and confirms
// template_drifted flips true. The pinned template_id/version are left
// alone — the operator can still see what the SKK was based on.
func TestE2E_Templates_DirectEditFlagsDrifted(t *testing.T) {
	h := startStack(t)
	opID := h.createOperator(t, "tpl-drift-op")
	accID := h.createAccount(t, opID, "tpl-drift-acc")

	if _, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId:  opID,
		Name:        "edit-target",
		Permissions: &nisv1.UserPermissions{PubAllow: []string{">"}},
	})); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	skkResp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: accID,
		Name:      "edit-skk",
		Template: &nisv1.TemplateRef{
			OperatorId:   opID,
			TemplateName: "edit-target",
		},
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey: %v", err)
	}
	if skkResp.Msg.Key.TemplateDrifted {
		t.Fatal("expected drifted=false on fresh templated SKK")
	}

	// Direct permission edit via UpdatePermissions — flips the drift flag.
	if _, err := h.keyCli.UpdatePermissions(context.Background(), connect.NewRequest(&nisv1.UpdatePermissionsRequest{
		Id: skkResp.Msg.Key.Id,
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">", "custom.>"},
		},
	})); err != nil {
		t.Fatalf("UpdatePermissions: %v", err)
	}

	getResp, err := h.keyCli.GetScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{
		Id: skkResp.Msg.Key.Id,
	}))
	if err != nil {
		t.Fatalf("GetScopedSigningKey: %v", err)
	}
	if !getResp.Msg.Key.TemplateDrifted {
		t.Fatal("expected drifted=true after direct permission edit")
	}
	if getResp.Msg.Key.TemplateId == "" {
		t.Fatal("template_id should remain set on drifted SKK (not auto-detached)")
	}
}

// TestE2E_Templates_DeleteBlockedByDependents asserts that DeleteTemplate
// refuses (FailedPrecondition) when any SKK still pins the template,
// and succeeds after the SKK is detached.
func TestE2E_Templates_DeleteBlockedByDependents(t *testing.T) {
	h := startStack(t)
	opID := h.createOperator(t, "tpl-del-op")
	accID := h.createAccount(t, opID, "tpl-del-acc")

	tplResp, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId:  opID,
		Name:        "del-target",
		Permissions: &nisv1.UserPermissions{PubAllow: []string{">"}},
	}))
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	skkResp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: accID,
		Name:      "del-skk",
		Template: &nisv1.TemplateRef{
			OperatorId:   opID,
			TemplateName: "del-target",
		},
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey: %v", err)
	}

	// With a dependent, delete must fail with FailedPrecondition.
	_, err = h.templateCli.DeleteTemplate(context.Background(), connect.NewRequest(&nisv1.DeleteTemplateRequest{
		Id: tplResp.Msg.Template.Id,
	}))
	if err == nil {
		t.Fatal("expected DeleteTemplate to fail when SKK pins it")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v (%v)", connect.CodeOf(err), err)
	}

	// Detach the SKK ⇒ delete now succeeds.
	if _, err := h.keyCli.DetachFromTemplate(context.Background(), connect.NewRequest(&nisv1.DetachFromTemplateRequest{
		Id: skkResp.Msg.Key.Id,
	})); err != nil {
		t.Fatalf("DetachFromTemplate: %v", err)
	}
	if _, err := h.templateCli.DeleteTemplate(context.Background(), connect.NewRequest(&nisv1.DeleteTemplateRequest{
		Id: tplResp.Msg.Template.Id,
	})); err != nil {
		t.Fatalf("DeleteTemplate after detach: %v", err)
	}
}

// TestE2E_Templates_ReservedNamesRejected pins that the server refuses
// "default" and "system" as template names — mirroring the manifest +
// service-layer reserved-names guards.
func TestE2E_Templates_ReservedNamesRejected(t *testing.T) {
	h := startStack(t)
	opID := h.createOperator(t, "tpl-reserved-op")

	for _, name := range []string{"default", "system"} {
		_, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
			OperatorId:  opID,
			Name:        name,
			Permissions: &nisv1.UserPermissions{},
		}))
		if err == nil {
			t.Fatalf("expected template name %q to be refused", name)
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("expected InvalidArgument for reserved name %q, got %v", name, connect.CodeOf(err))
		}
	}
}

// TestE2E_Templates_OperatorAdminCannotSeeOtherOperator pins the
// cross-operator scope check — operator-admin A's GetTemplateByName
// for a template owned by operator B must return PermissionDenied.
// Without CanReadTemplate's ownsOperator narrowing, operator-admins
// could enumerate every operator's templates.
func TestE2E_Templates_OperatorAdminCannotSeeOtherOperator(t *testing.T) {
	h := startStack(t)
	opA := h.createOperator(t, "tpl-iso-a")
	opB := h.createOperator(t, "tpl-iso-b")

	// Admin creates a template in B.
	if _, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId:  opB,
		Name:        "b-secret",
		Permissions: &nisv1.UserPermissions{PubAllow: []string{">"}},
	})); err != nil {
		t.Fatalf("CreateTemplate in B: %v", err)
	}

	// Create operator-admin A and try to read B's template.
	const adminAUser = "tpl-iso-opadmin-a"
	const adminAPass = "tpl-iso-opadmin-a-pw"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(context.Background(), connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    adminAUser,
		Password:    adminAPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opA,
	})); err != nil {
		t.Fatalf("CreateAPIUser(operator-admin for A): %v", err)
	}
	clientA := h.loginAs(t, adminAUser, adminAPass)

	_, err := clientA.templateCli.GetTemplateByName(context.Background(), connect.NewRequest(&nisv1.GetTemplateByNameRequest{
		OperatorId: opB,
		Name:       "b-secret",
	}))
	if err == nil {
		t.Fatal("expected operator-admin A to be denied template in operator B")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v (%v)", connect.CodeOf(err), err)
	}
}

// TestE2E_Templates_AutoTrackPropagates is the load-bearing test for
// the auto-track feature: create a template + a tracking SKK + a user,
// confirm the user can publish on a subject the template doesn't deny,
// then update the template to add a sub_deny, and confirm the SKK was
// auto-bumped + the parent account JWT re-signed + pushed without the
// operator calling ApplyTemplateToScopedKey.
func TestE2E_Templates_AutoTrackPropagates(t *testing.T) {
	h := startStack(t)
	s := h.bootStandardStack(t, "tpl-auto")

	tplResp, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId: s.operatorID,
		Name:       "auto-tpl",
		Permissions: &nisv1.UserPermissions{
			SubAllow: []string{"events.>"},
			PubAllow: []string{">"},
		},
	}))
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	skkResp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: s.accountID,
		Name:      "auto-skk",
		Template: &nisv1.TemplateRef{
			OperatorId:   s.operatorID,
			TemplateName: "auto-tpl",
		},
		TrackLatest: true,
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey from template with TrackLatest: %v", err)
	}
	if !skkResp.Msg.Key.TrackLatest {
		t.Fatal("expected TrackLatest=true on created SKK")
	}
	if skkResp.Msg.Key.TemplateVersion != 1 {
		t.Fatalf("expected v1 pin at create, got v%d", skkResp.Msg.Key.TemplateVersion)
	}

	userID := h.createScopedUser(t, s.accountID, "auto-user", skkResp.Msg.Key.Id)
	credsPath := h.fetchUserCreds(t, userID, "auto-user")

	// Phase 1: v1 perms allow subscribing to events.secret.foo (no deny).
	{
		nc, errCh := dialWithErrCh(t, h.natsURL, credsPath)
		defer nc.Close()
		if _, err := nc.SubscribeSync("events.secret.foo"); err != nil {
			t.Fatalf("v1 SubscribeSync: %v", err)
		}
		if err := nc.FlushTimeout(2 * time.Second); err != nil {
			t.Fatalf("v1 flush: %v", err)
		}
		expectNoAsyncError(t, errCh, 300*time.Millisecond)
		nc.Close()
	}

	// Update the template to v2: add sub_deny on events.secret.>.
	// Because the SKK has track_latest=true, TemplateService.UpdateTemplate
	// should snapshot the new version onto the SKK, regen the parent
	// account JWT, and push to clusters — all WITHOUT a manual
	// ApplyTemplateToScopedKey call.
	if _, err := h.templateCli.UpdateTemplate(context.Background(), connect.NewRequest(&nisv1.UpdateTemplateRequest{
		Id: tplResp.Msg.Template.Id,
		Permissions: &nisv1.UserPermissions{
			SubAllow: []string{"events.>"},
			SubDeny:  []string{"events.secret.>"},
			PubAllow: []string{">"},
		},
		ChangeNote: "lock down secrets",
	})); err != nil {
		t.Fatalf("UpdateTemplate to v2: %v", err)
	}

	// Confirm the SKK was auto-bumped at the DB layer.
	getResp, err := h.keyCli.GetScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{
		Id: skkResp.Msg.Key.Id,
	}))
	if err != nil {
		t.Fatalf("GetScopedSigningKey after auto-track: %v", err)
	}
	if getResp.Msg.Key.TemplateVersion != 2 {
		t.Fatalf("expected SKK auto-bumped to v2, got v%d", getResp.Msg.Key.TemplateVersion)
	}
	if !getResp.Msg.Key.TrackLatest {
		t.Fatal("track_latest should remain true after auto-bump")
	}
	if getResp.Msg.Key.TemplateDrifted {
		t.Fatal("auto-bumped SKK must not be flagged drifted")
	}
	gotDeny := false
	for _, s := range getResp.Msg.Key.Permissions.GetSubDeny() {
		if s == "events.secret.>" {
			gotDeny = true
			break
		}
	}
	if !gotDeny {
		t.Fatalf("expected sub_deny[events.secret.>] in auto-bumped perms, got %v", getResp.Msg.Key.Permissions.GetSubDeny())
	}

	// Phase 2: reconnect — NATS resolver should now have the re-signed
	// account JWT (auto-pushed by the auto-track fan-out). Subscribing
	// to events.secret.foo must trigger an async permission violation.
	nc, errCh := dialWithErrCh(t, h.natsURL, credsPath)
	defer nc.Close()
	if _, err := nc.SubscribeSync("events.secret.foo"); err != nil {
		t.Fatalf("v2 SubscribeSync local: %v", err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("v2 flush: %v", err)
	}
	expectAsyncPermissionError(t, errCh, "events.secret.foo", 2*time.Second)
}

// TestE2E_Templates_TrackLatest_RejectsDirectEdit confirms the
// service-layer guard: editing permissions on a tracking SKK returns
// FailedPrecondition rather than silently being overwritten on the
// next template bump.
func TestE2E_Templates_TrackLatest_RejectsDirectEdit(t *testing.T) {
	h := startStack(t)
	opID := h.createOperator(t, "tpl-reject-op")
	accID := h.createAccount(t, opID, "tpl-reject-acc")

	if _, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId:  opID,
		Name:        "reject-tpl",
		Permissions: &nisv1.UserPermissions{PubAllow: []string{">"}},
	})); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	skkResp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: accID,
		Name:      "reject-skk",
		Template: &nisv1.TemplateRef{
			OperatorId:   opID,
			TemplateName: "reject-tpl",
		},
		TrackLatest: true,
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey TrackLatest: %v", err)
	}

	_, err = h.keyCli.UpdatePermissions(context.Background(), connect.NewRequest(&nisv1.UpdatePermissionsRequest{
		Id: skkResp.Msg.Key.Id,
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">", "custom.>"},
		},
	}))
	if err == nil {
		t.Fatal("expected UpdatePermissions to be rejected while tracking, got nil")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v (%v)", connect.CodeOf(err), err)
	}

	// Disabling tracking lets the same edit succeed.
	if _, err := h.keyCli.SetTrackLatest(context.Background(), connect.NewRequest(&nisv1.SetTrackLatestRequest{
		Id:      skkResp.Msg.Key.Id,
		Enabled: false,
	})); err != nil {
		t.Fatalf("SetTrackLatest off: %v", err)
	}
	if _, err := h.keyCli.UpdatePermissions(context.Background(), connect.NewRequest(&nisv1.UpdatePermissionsRequest{
		Id: skkResp.Msg.Key.Id,
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">", "custom.>"},
		},
	})); err != nil {
		t.Fatalf("UpdatePermissions after tracking off: %v", err)
	}
}

// TestE2E_Templates_TrackLatest_RequiresCleanTemplate pins that
// enabling tracking on a drifted or untemplated SKK is rejected.
// Operator must bump-to-latest (clearing drift) or detach-and-recreate
// before they can opt in.
func TestE2E_Templates_TrackLatest_RequiresCleanTemplate(t *testing.T) {
	h := startStack(t)
	opID := h.createOperator(t, "tpl-clean-op")
	accID := h.createAccount(t, opID, "tpl-clean-acc")

	if _, err := h.templateCli.CreateTemplate(context.Background(), connect.NewRequest(&nisv1.CreateTemplateRequest{
		OperatorId:  opID,
		Name:        "clean-tpl",
		Permissions: &nisv1.UserPermissions{PubAllow: []string{">"}},
	})); err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	// Case 1: untemplated SKK. SetTrackLatest must reject.
	plainSKK := h.createScopedKey(t, accID, "plain-skk", &nisv1.UserPermissions{PubAllow: []string{">"}})
	_, err := h.keyCli.SetTrackLatest(context.Background(), connect.NewRequest(&nisv1.SetTrackLatestRequest{
		Id:      plainSKK,
		Enabled: true,
	}))
	if err == nil {
		t.Fatal("expected SetTrackLatest(true) on untemplated SKK to fail, got nil")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition for untemplated, got %v (%v)", connect.CodeOf(err), err)
	}

	// Case 2: templated + drifted. Set tracking off, edit, then try to
	// re-enable. Should reject because the SKK is now drifted.
	driftedResp, err := h.keyCli.CreateScopedSigningKey(context.Background(), connect.NewRequest(&nisv1.CreateScopedSigningKeyRequest{
		AccountId: accID,
		Name:      "drifted-skk",
		Template: &nisv1.TemplateRef{
			OperatorId:   opID,
			TemplateName: "clean-tpl",
		},
		// not tracking — so we can drift it
	}))
	if err != nil {
		t.Fatalf("CreateScopedSigningKey: %v", err)
	}
	if _, err := h.keyCli.UpdatePermissions(context.Background(), connect.NewRequest(&nisv1.UpdatePermissionsRequest{
		Id: driftedResp.Msg.Key.Id,
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">", "custom.>"},
		},
	})); err != nil {
		t.Fatalf("UpdatePermissions to drift: %v", err)
	}
	_, err = h.keyCli.SetTrackLatest(context.Background(), connect.NewRequest(&nisv1.SetTrackLatestRequest{
		Id:      driftedResp.Msg.Key.Id,
		Enabled: true,
	}))
	if err == nil {
		t.Fatal("expected SetTrackLatest(true) on drifted SKK to fail, got nil")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition for drifted, got %v (%v)", connect.CodeOf(err), err)
	}
}

// expectAsyncPermissionError waits for a NATS async error mentioning the
// subject. NATS reports permission violations asynchronously so a local
// SubscribeSync doesn't return them directly.
func expectAsyncPermissionError(t *testing.T, errCh <-chan error, subject string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case err := <-errCh:
			if err == nil {
				continue
			}
			if strings.Contains(err.Error(), subject) || strings.Contains(err.Error(), "Permissions Violation") {
				return
			}
		case <-deadline:
			t.Fatalf("expected async permission error for %q within %v", subject, timeout)
		}
	}
}

