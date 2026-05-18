//go:build e2e

// manifest_test.go — end-to-end coverage for the P5 manifest feature.
// All tests boot NIS only (no live NATS). The manifest plan/apply/delete/dump
// paths operate purely on NIS DB state.
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
	"github.com/thomas-maurice/nis/pkg/manifest"
)

// buildAdminPlannerClient returns a manifest.PlannerClient wrapping the
// harness admin token.
func buildAdminPlannerClient(t *testing.T, h *harness) manifest.PlannerClient {
	t.Helper()
	c, err := client.NewClient(h.serverURL, h.authToken)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.NewClientAdapter(c)
}

// baseObjects returns a minimal batch: one Operator, one Account (with JS),
// one ScopedSigningKey named "writer", one User referencing "writer".
func baseObjects(opName, accName, keyName, userName string) []manifest.Object {
	return []manifest.Object{
		baseOperator(opName),
		baseAccount(opName, accName),
		baseKey(opName, accName, keyName),
		baseUser(opName, accName, userName, keyName),
	}
}

func baseOperator(name string) manifest.Object {
	return manifest.Object{
		TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindOperator},
		Metadata: manifest.ObjectMeta{Name: name},
		Operator: &manifest.OperatorSpec{Description: "e2e test operator"},
	}
}

func baseAccount(opName, accName string) manifest.Object {
	return manifest.Object{
		TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindAccount},
		Metadata: manifest.ObjectMeta{Name: accName, Operator: opName},
		Account: &manifest.AccountSpec{
			Description: "e2e test account",
			JetStream: &manifest.JetStreamSpec{
				Enabled:      true,
				MaxStorage:   1 << 30, // 1Gi
				MaxMemory:    512 << 20,
				MaxStreams:   10,
				MaxConsumers: 20,
			},
		},
	}
}

func baseKey(opName, accName, keyName string) manifest.Object {
	return manifest.Object{
		TypeMeta:         manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindScopedSigningKey},
		Metadata:         manifest.ObjectMeta{Name: keyName, Operator: opName, Account: accName},
		ScopedSigningKey: &manifest.ScopedSigningKeySpec{PubAllow: []string{"app.>"}},
	}
}

func baseUser(opName, accName, userName, keyName string) manifest.Object {
	return manifest.Object{
		TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindUser},
		Metadata: manifest.ObjectMeta{Name: userName, Operator: opName, Account: accName},
		User:     &manifest.UserSpec{ScopedKey: keyName},
	}
}

// applyBatch is a convenience wrapper: plan then apply, fataling on error.
func applyBatch(t *testing.T, pc manifest.PlannerClient, batch []manifest.Object) (*manifest.PlanResult, *manifest.ApplyResult) {
	t.Helper()
	ctx := context.Background()
	plan, err := manifest.Plan(ctx, pc, batch)
	if err != nil {
		t.Fatalf("manifest.Plan: %v", err)
	}
	result, err := manifest.Apply(ctx, pc, plan)
	if err != nil {
		t.Fatalf("manifest.Apply: %v", err)
	}
	return plan, result
}

// ---------------------------------------------------------------------------

func TestManifest_Apply_FromEmpty(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	pc := buildAdminPlannerClient(t, h)

	batch := baseObjects("mfe-op", "mfe-acc", "writer", "mfe-user")

	_, applyResult := applyBatch(t, pc, batch)

	for _, item := range applyResult.Items {
		if item.Outcome == manifest.OutcomeFailed {
			t.Fatalf("apply item %s/%s failed: %s", item.Object.Kind, item.Object.Metadata.Name, item.Note)
		}
	}

	// Verify operator exists.
	opResp, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: "mfe-op"}))
	if err != nil {
		t.Fatalf("GetOperatorByName: %v", err)
	}
	opID := opResp.Msg.GetOperator().GetId()

	// Verify account exists.
	accResp, err := h.accountCli.GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opID, Name: "mfe-acc",
	}))
	if err != nil {
		t.Fatalf("GetAccountByName: %v", err)
	}
	accID := accResp.Msg.GetAccount().GetId()

	// Verify scoped key named "writer" exists.
	keyResp, err := h.keyCli.GetScopedSigningKeyByName(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyByNameRequest{
		AccountId: accID, Name: "writer",
	}))
	if err != nil {
		t.Fatalf("GetScopedSigningKeyByName(writer): %v", err)
	}
	writerKeyID := keyResp.Msg.GetKey().GetId()

	// Verify user exists and its ScopedSigningKeyId matches "writer".
	userResp, err := h.userCli.GetUserByName(ctx, connect.NewRequest(&nisv1.GetUserByNameRequest{
		AccountId: accID, Name: "mfe-user",
	}))
	if err != nil {
		t.Fatalf("GetUserByName: %v", err)
	}
	if got := userResp.Msg.GetUser().GetScopedSigningKeyId(); got != writerKeyID {
		t.Fatalf("user ScopedSigningKeyId = %q, want %q", got, writerKeyID)
	}
}

func TestManifest_Apply_Idempotent(t *testing.T) {
	h := startStack(t)
	pc := buildAdminPlannerClient(t, h)
	batch := baseObjects("mfi-op", "mfi-acc", "writer", "mfi-user")

	applyBatch(t, pc, batch)

	// Second plan against the now-populated server.
	plan, err := manifest.Plan(context.Background(), pc, batch)
	if err != nil {
		t.Fatalf("second Plan: %v", err)
	}
	if plan.Summary.Create != 0 || plan.Summary.Update != 0 {
		t.Fatalf("expected idempotent plan (Create=0, Update=0), got Create=%d Update=%d",
			plan.Summary.Create, plan.Summary.Update)
	}
	if plan.Summary.Noop < 4 {
		t.Fatalf("expected Noop >= 4 (including auto-default SKK), got %d", plan.Summary.Noop)
	}
}

func TestManifest_Apply_DefaultScopedKeyRoundTrip(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	pc := buildAdminPlannerClient(t, h)

	opName := "mfd-op"
	accName := "mfd-acc"

	// First pass: create the operator and account to get the auto-generated
	// default SKK, then fetch its server-side description so the manifest spec
	// matches the server on the round-trip check.
	opID := h.createOperator(t, opName)
	accID := h.createAccount(t, opID, accName)
	defaultKeyResp, err := h.keyCli.GetScopedSigningKeyByName(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyByNameRequest{
		AccountId: accID, Name: "default",
	}))
	if err != nil {
		t.Fatalf("GetScopedSigningKeyByName(default) pre-apply: %v", err)
	}
	defaultKeyDesc := defaultKeyResp.Msg.GetKey().GetDescription()

	batch := []manifest.Object{
		baseOperator(opName),
		{
			TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindAccount},
			Metadata: manifest.ObjectMeta{Name: accName, Operator: opName},
			Account:  &manifest.AccountSpec{},
		},
		{
			TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindScopedSigningKey},
			Metadata: manifest.ObjectMeta{Name: "default", Operator: opName, Account: accName},
			ScopedSigningKey: &manifest.ScopedSigningKeySpec{
				Description: defaultKeyDesc,
				PubAllow:    []string{">"},
				SubAllow:    []string{">"},
				PubDeny:     []string{"secret.>"},
			},
		},
	}

	applyBatch(t, pc, batch)

	// Verify the default key has the deny we set.
	opResp, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: opName}))
	if err != nil {
		t.Fatalf("GetOperatorByName: %v", err)
	}
	accResp, err := h.accountCli.GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opResp.Msg.GetOperator().GetId(), Name: accName,
	}))
	if err != nil {
		t.Fatalf("GetAccountByName: %v", err)
	}
	keyResp2, err := h.keyCli.GetScopedSigningKeyByName(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyByNameRequest{
		AccountId: accResp.Msg.GetAccount().GetId(), Name: "default",
	}))
	if err != nil {
		t.Fatalf("GetScopedSigningKeyByName(default) post-apply: %v", err)
	}
	perms := keyResp2.Msg.GetKey().GetPermissions()
	if !stringSliceContains(perms.GetPubDeny(), "secret.>") {
		t.Fatalf("default key PubDeny should contain %q, got %v", "secret.>", perms.GetPubDeny())
	}

	// Second plan: default SKK should be Noop now that spec matches server.
	plan, err := manifest.Plan(context.Background(), pc, batch)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	for _, item := range plan.Items {
		if item.Object.Kind == manifest.KindScopedSigningKey && item.Object.Metadata.Name == "default" {
			if item.Action != manifest.ActionNoop {
				t.Fatalf("default SKK re-plan action = %v, want ActionNoop (updates: %v)", item.Action, item.Updates)
			}
		}
	}
}

func TestManifest_Apply_UpdatesAccountDescription(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	pc := buildAdminPlannerClient(t, h)

	batch := baseObjects("mua-op", "mua-acc", "writer", "mua-user")
	applyBatch(t, pc, batch)

	// Modify description in the batch.
	for i := range batch {
		if batch[i].Kind == manifest.KindAccount {
			batch[i].Account.Description = "updated description"
		}
	}

	plan, err := manifest.Plan(ctx, pc, batch)
	if err != nil {
		t.Fatalf("plan after description change: %v", err)
	}

	// Find the account item and verify it's classified as Update with the right op.
	var accItem *manifest.PlanItem
	for i := range plan.Items {
		if plan.Items[i].Object.Kind == manifest.KindAccount {
			accItem = &plan.Items[i]
			break
		}
	}
	if accItem == nil {
		t.Fatal("account plan item not found")
	}
	if accItem.Action != manifest.ActionUpdate {
		t.Fatalf("account action = %v, want ActionUpdate", accItem.Action)
	}
	foundUpdateOp := false
	for _, op := range accItem.Updates {
		if op.RPC == "UpdateAccount" && stringSliceContains(op.Fields, "description") {
			foundUpdateOp = true
		}
	}
	if !foundUpdateOp {
		t.Fatalf("expected UpdateOp{RPC:UpdateAccount, Fields:[description]}, got %v", accItem.Updates)
	}

	_, applyResult := applyBatch(t, pc, batch)
	for _, item := range applyResult.Items {
		if item.Outcome == manifest.OutcomeFailed {
			t.Fatalf("apply item %s/%s failed: %s", item.Object.Kind, item.Object.Metadata.Name, item.Note)
		}
	}

	// Verify description updated on server.
	opResp, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: "mua-op"}))
	if err != nil {
		t.Fatalf("GetOperatorByName: %v", err)
	}
	accResp, err := h.accountCli.GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opResp.Msg.GetOperator().GetId(), Name: "mua-acc",
	}))
	if err != nil {
		t.Fatalf("GetAccountByName: %v", err)
	}
	if got := accResp.Msg.GetAccount().GetDescription(); got != "updated description" {
		t.Fatalf("account description = %q, want %q", got, "updated description")
	}
}

func TestManifest_Apply_UpdatesJetStreamLimits(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	pc := buildAdminPlannerClient(t, h)

	batch := baseObjects("muj-op", "muj-acc", "writer", "muj-user")
	applyBatch(t, pc, batch)

	// Bump maxStorage from 1Gi to 2Gi.
	for i := range batch {
		if batch[i].Kind == manifest.KindAccount && batch[i].Account.JetStream != nil {
			batch[i].Account.JetStream.MaxStorage = 2 << 30 // 2Gi
		}
	}

	plan, err := manifest.Plan(ctx, pc, batch)
	if err != nil {
		t.Fatalf("plan after JS limit change: %v", err)
	}

	var accItem *manifest.PlanItem
	for i := range plan.Items {
		if plan.Items[i].Object.Kind == manifest.KindAccount {
			accItem = &plan.Items[i]
			break
		}
	}
	if accItem == nil {
		t.Fatal("account plan item not found")
	}
	if accItem.Action != manifest.ActionUpdate {
		t.Fatalf("account action = %v, want ActionUpdate", accItem.Action)
	}
	foundJSOp := false
	for _, op := range accItem.Updates {
		if op.RPC == "UpdateJetStreamLimits" {
			foundJSOp = true
		}
	}
	if !foundJSOp {
		t.Fatalf("expected UpdateOp{RPC:UpdateJetStreamLimits}, got %v", accItem.Updates)
	}

	_, applyResult := applyBatch(t, pc, batch)
	for _, item := range applyResult.Items {
		if item.Outcome == manifest.OutcomeFailed {
			t.Fatalf("apply item %s/%s failed: %s", item.Object.Kind, item.Object.Metadata.Name, item.Note)
		}
	}

	// Verify limits on server.
	opResp, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: "muj-op"}))
	if err != nil {
		t.Fatalf("GetOperatorByName: %v", err)
	}
	accResp, err := h.accountCli.GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opResp.Msg.GetOperator().GetId(), Name: "muj-acc",
	}))
	if err != nil {
		t.Fatalf("GetAccountByName: %v", err)
	}
	got := accResp.Msg.GetAccount().GetJetstreamLimits().GetMaxStorage()
	want := int64(2 << 30)
	if got != want {
		t.Fatalf("MaxStorage = %d, want %d", got, want)
	}
}

func TestManifest_Apply_RejectsReservedNames(t *testing.T) {
	t.Run("reserved_account_sys", func(t *testing.T) {
		batch := []manifest.Object{
			baseOperator("mrn-op"),
			{
				TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindAccount},
				Metadata: manifest.ObjectMeta{Name: "$SYS", Operator: "mrn-op"},
				Account:  &manifest.AccountSpec{},
			},
		}
		_, err := manifest.Validate(batch, manifest.ValidateOptions{})
		if err == nil {
			t.Fatal("Validate should return error for reserved Account $SYS")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "reserved") {
			t.Fatalf("error should mention 'reserved', got: %v", err)
		}
	})

	t.Run("reserved_user_system", func(t *testing.T) {
		batch := []manifest.Object{
			baseOperator("mrn2-op"),
			baseAccount("mrn2-op", "mrn2-acc"),
			{
				TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindUser},
				Metadata: manifest.ObjectMeta{Name: "system", Operator: "mrn2-op", Account: "mrn2-acc"},
				User:     &manifest.UserSpec{},
			},
		}
		_, err := manifest.Validate(batch, manifest.ValidateOptions{})
		if err == nil {
			t.Fatal("Validate should return error for reserved User 'system'")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "reserved") {
			t.Fatalf("error should mention 'reserved', got: %v", err)
		}
	})
}

func TestManifest_Apply_RejectsUserScopedKeyChange(t *testing.T) {
	h := startStack(t)
	pc := buildAdminPlannerClient(t, h)

	batch := []manifest.Object{
		baseOperator("mrsk-op"),
		baseAccount("mrsk-op", "mrsk-acc"),
		baseKey("mrsk-op", "mrsk-acc", "alpha"),
		baseKey("mrsk-op", "mrsk-acc", "beta"),
		baseUser("mrsk-op", "mrsk-acc", "mrsk-user", "alpha"),
	}
	applyBatch(t, pc, batch)

	// Change the user's scopedKey from "alpha" to "beta".
	for i := range batch {
		if batch[i].Kind == manifest.KindUser {
			batch[i].User.ScopedKey = "beta"
		}
	}

	_, err := manifest.Plan(context.Background(), pc, batch)
	if err == nil {
		t.Fatal("Plan should return error when user scopedKey is changed")
	}
	msg := err.Error()
	if !strings.Contains(msg, "reassignment") && !strings.Contains(msg, "delete+recreate") {
		t.Fatalf("error should mention reassignment/delete+recreate, got: %v", err)
	}
}

func TestManifest_Delete_FromFile(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	pc := buildAdminPlannerClient(t, h)

	opName := "mdf-op"
	accName := "mdf-acc"
	keyName := "writer"
	userName := "mdf-user"

	batch := baseObjects(opName, accName, keyName, userName)
	applyBatch(t, pc, batch)

	// Fetch IDs before deleting so we can check 404 afterwards.
	opResp, err := h.operatorCli.GetOperatorByName(ctx, connect.NewRequest(&nisv1.GetOperatorByNameRequest{Name: opName}))
	if err != nil {
		t.Fatalf("GetOperatorByName pre-delete: %v", err)
	}
	opID := opResp.Msg.GetOperator().GetId()

	accResp, err := h.accountCli.GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opID, Name: accName,
	}))
	if err != nil {
		t.Fatalf("GetAccountByName pre-delete: %v", err)
	}
	accID := accResp.Msg.GetAccount().GetId()

	keyResp, err := h.keyCli.GetScopedSigningKeyByName(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyByNameRequest{
		AccountId: accID, Name: keyName,
	}))
	if err != nil {
		t.Fatalf("GetScopedSigningKeyByName pre-delete: %v", err)
	}
	keyID := keyResp.Msg.GetKey().GetId()

	userResp, err := h.userCli.GetUserByName(ctx, connect.NewRequest(&nisv1.GetUserByNameRequest{
		AccountId: accID, Name: userName,
	}))
	if err != nil {
		t.Fatalf("GetUserByName pre-delete: %v", err)
	}
	userID := userResp.Msg.GetUser().GetId()

	result, err := manifest.DeleteAll(ctx, pc, batch)
	if err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	for _, item := range result.Items {
		if item.Outcome == manifest.OutcomeFailed {
			t.Fatalf("delete item %s/%s failed: %s", item.Object.Kind, item.Object.Metadata.Name, item.Note)
		}
	}

	// Verify all gone.
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: userID})); err == nil {
		t.Fatal("user should be gone after DeleteAll")
	}
	if _, err := h.keyCli.GetScopedSigningKey(ctx, connect.NewRequest(&nisv1.GetScopedSigningKeyRequest{Id: keyID})); err == nil {
		t.Fatal("scoped signing key should be gone after DeleteAll")
	}
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: accID})); err == nil {
		t.Fatal("account should be gone after DeleteAll")
	}
	if _, err := h.operatorCli.GetOperator(ctx, connect.NewRequest(&nisv1.GetOperatorRequest{Id: opID})); err == nil {
		t.Fatal("operator should be gone after DeleteAll")
	}
}

func TestManifest_Delete_RefusesReserved(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	pc := buildAdminPlannerClient(t, h)

	// Create an operator so the reserved entities ($SYS account, system user,
	// default SKK) actually exist on the server.
	opID := h.createOperator(t, "mdr-op")

	// Get the $SYS account ID for post-check.
	accResp, err := h.accountCli.GetAccountByName(ctx, connect.NewRequest(&nisv1.GetAccountByNameRequest{
		OperatorId: opID, Name: "$SYS",
	}))
	if err != nil {
		t.Fatalf("GetAccountByName($SYS): %v", err)
	}
	sysAccID := accResp.Msg.GetAccount().GetId()

	// Get the system user ID.
	userResp, err := h.userCli.GetUserByName(ctx, connect.NewRequest(&nisv1.GetUserByNameRequest{
		AccountId: sysAccID, Name: "system",
	}))
	if err != nil {
		t.Fatalf("GetUserByName(system): %v", err)
	}
	systemUserID := userResp.Msg.GetUser().GetId()

	// DeleteAll against these reserved entities. Deletion is in reverse topo
	// order: User first, then ScopedSigningKey, Account, Operator.
	// The "system" user is encountered first and should fail immediately.
	batch := []manifest.Object{
		{
			TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindAccount},
			Metadata: manifest.ObjectMeta{Name: "$SYS", Operator: "mdr-op"},
			Account:  &manifest.AccountSpec{},
		},
		{
			TypeMeta:         manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindScopedSigningKey},
			Metadata:         manifest.ObjectMeta{Name: "default", Operator: "mdr-op", Account: "$SYS"},
			ScopedSigningKey: &manifest.ScopedSigningKeySpec{},
		},
		{
			TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersion, Kind: manifest.KindUser},
			Metadata: manifest.ObjectMeta{Name: "system", Operator: "mdr-op", Account: "$SYS"},
			User:     &manifest.UserSpec{},
		},
	}

	result, err := manifest.DeleteAll(ctx, pc, batch)
	if err == nil {
		t.Fatal("DeleteAll should return error for reserved entities")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "reserved") {
		t.Fatalf("error should mention 'reserved', got: %v", err)
	}

	// First failed item should be the "system" user (it comes first in delete order).
	if len(result.Items) == 0 {
		t.Fatal("expected at least one result item")
	}
	firstFailed := result.Items[0]
	if firstFailed.Outcome != manifest.OutcomeFailed {
		t.Fatalf("first item outcome = %v, want OutcomeFailed", firstFailed.Outcome)
	}
	if !strings.Contains(strings.ToLower(firstFailed.Note), "reserved") {
		t.Fatalf("first item note should mention 'reserved', got: %q", firstFailed.Note)
	}

	// The system user and $SYS account should still exist.
	if _, err := h.userCli.GetUser(ctx, connect.NewRequest(&nisv1.GetUserRequest{Id: systemUserID})); err != nil {
		t.Fatalf("system user should still exist after refused delete: %v", err)
	}
	if _, err := h.accountCli.GetAccount(ctx, connect.NewRequest(&nisv1.GetAccountRequest{Id: sysAccID})); err != nil {
		t.Fatalf("$SYS account should still exist after refused delete: %v", err)
	}
}

func TestManifest_Dump_RoundTrip(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()
	pc := buildAdminPlannerClient(t, h)

	opName := "mdrt-op"
	accName := "mdrt-acc"
	keyName := "mdrt-key"
	userName := "mdrt-user"

	// Build server state via harness helpers.
	opID := h.createOperator(t, opName)
	accID := h.createAccount(t, opID, accName)

	// Update JetStream limits so the account has a non-trivial spec.
	if _, err := h.accountCli.UpdateJetStreamLimits(ctx, connect.NewRequest(&nisv1.UpdateJetStreamLimitsRequest{
		Id: accID,
		Limits: &nisv1.JetStreamLimits{
			Enabled:      true,
			MaxStorage:   1 << 30,
			MaxMemory:    512 << 20,
			MaxStreams:   5,
			MaxConsumers: 10,
		},
	})); err != nil {
		t.Fatalf("UpdateJetStreamLimits: %v", err)
	}

	keyID := h.createScopedKey(t, accID, keyName, &nisv1.UserPermissions{
		PubAllow: []string{"app.>"},
	})

	// Create a user with a per-user JWT TTL.
	createUserResp, err := h.userCli.CreateUser(ctx, connect.NewRequest(&nisv1.CreateUserRequest{
		AccountId:          accID,
		Name:               userName,
		ScopedSigningKeyId: keyID,
	}))
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	userID := createUserResp.Msg.GetUser().GetId()

	ttlSecs := int64((24 * time.Hour).Seconds())
	if _, err := h.userCli.UpdateUser(ctx, connect.NewRequest(&nisv1.UpdateUserRequest{
		Id:            userID,
		JwtTtlSeconds: &ttlSecs,
	})); err != nil {
		t.Fatalf("UpdateUser(jwtTTL): %v", err)
	}

	// Fetch full server state for this operator.
	opResp, err := h.operatorCli.GetOperator(ctx, connect.NewRequest(&nisv1.GetOperatorRequest{Id: opID}))
	if err != nil {
		t.Fatalf("GetOperator: %v", err)
	}

	accListResp, err := h.accountCli.ListAccounts(ctx, connect.NewRequest(&nisv1.ListAccountsRequest{OperatorId: opID}))
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}

	var allSKKs []*nisv1.ScopedSigningKey
	var allUsers []*nisv1.User
	for _, acc := range accListResp.Msg.GetAccounts() {
		skkResp, err := h.keyCli.ListScopedSigningKeys(ctx, connect.NewRequest(&nisv1.ListScopedSigningKeysRequest{
			AccountId: acc.GetId(),
		}))
		if err != nil {
			t.Fatalf("ListScopedSigningKeys(acc=%s): %v", acc.GetName(), err)
		}
		allSKKs = append(allSKKs, skkResp.Msg.GetKeys()...)

		userListResp, err := h.userCli.ListUsers(ctx, connect.NewRequest(&nisv1.ListUsersRequest{
			AccountId: acc.GetId(),
		}))
		if err != nil {
			t.Fatalf("ListUsers(acc=%s): %v", acc.GetName(), err)
		}
		allUsers = append(allUsers, userListResp.Msg.GetUsers()...)
	}

	allKinds := map[string]bool{
		manifest.KindOperator:         true,
		manifest.KindAccount:          true,
		manifest.KindScopedSigningKey: true,
		manifest.KindUser:             true,
	}
	objs := manifest.DumpObjects(
		opResp.Msg.GetOperator(),
		nil, // no clusters to dump
		accListResp.Msg.GetAccounts(),
		allSKKs,
		allUsers,
		allKinds,
	)

	yamlBytes, err := manifest.EncodeYAML(objs)
	if err != nil {
		t.Fatalf("EncodeYAML: %v", err)
	}

	parsed, err := manifest.Parse(yamlBytes, "dump-roundtrip")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Re-plan against the same server — everything should be Noop.
	plan, err := manifest.Plan(ctx, pc, parsed)
	if err != nil {
		t.Fatalf("Plan after dump round-trip: %v", err)
	}

	for _, item := range plan.Items {
		if item.Action != manifest.ActionNoop {
			t.Errorf("expected Noop for %s/%s, got %v (updates: %v)",
				item.Object.Kind, item.Object.Metadata.Name, item.Action, item.Updates)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func stringSliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
