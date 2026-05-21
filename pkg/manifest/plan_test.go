package manifest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// helpers for building proto entities in tests

func testOperator(id, name, desc string) *nisv1.Operator {
	return &nisv1.Operator{Id: id, Name: name, Description: desc}
}

func testAccount(id, opID, name, desc string) *nisv1.Account {
	return &nisv1.Account{Id: id, OperatorId: opID, Name: name, Description: desc}
}

func testCluster(id, opID, name string, urls []string) *nisv1.Cluster {
	return &nisv1.Cluster{Id: id, OperatorId: opID, Name: name, ServerUrls: urls}
}

func testSSK(id, accID, name, desc string) *nisv1.ScopedSigningKey {
	return &nisv1.ScopedSigningKey{
		Id: id, AccountId: accID, Name: name, Description: desc,
		Permissions:        &nisv1.UserPermissions{},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
}

func testUser(id, accID, name, desc string) *nisv1.User {
	return &nisv1.User{Id: id, AccountId: accID, Name: name, Description: desc}
}

const (
	opID  = "11111111-1111-1111-1111-111111111111"
	accID = "22222222-2222-2222-2222-222222222222"
	sskID = "33333333-3333-3333-3333-333333333333"
	usrID = "44444444-4444-4444-4444-444444444444"
	clID  = "55555555-5555-5555-5555-555555555555"
)

// minimalBatch returns a batch with one of each kind pointing at "acme" / "billing".
func minimalBatch() []Object {
	return []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{Description: "my op"}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster}, Metadata: ObjectMeta{Name: "prod", Operator: "acme"}, Cluster: &ClusterSpec{ServerURLs: []string{"nats://localhost:4222"}}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{Description: "billing acc"}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey}, Metadata: ObjectMeta{Name: "writer", Operator: "acme", Account: "billing"}, ScopedSigningKey: &ScopedSigningKeySpec{PubAllow: []string{"events.>"}}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser}, Metadata: ObjectMeta{Name: "api", Operator: "acme", Account: "billing"}, User: &UserSpec{Description: "api user"}},
	}
}

func TestPlan_AllCreate_EmptyServer(t *testing.T) {
	fc := newFakePlannerClient()
	result, err := Plan(context.Background(), fc, minimalBatch())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if result.Summary.Create != 5 {
		t.Errorf("Create=%d want 5", result.Summary.Create)
	}
	if result.Summary.Update != 0 {
		t.Errorf("Update=%d want 0", result.Summary.Update)
	}
	if result.Summary.Noop != 0 {
		t.Errorf("Noop=%d want 0", result.Summary.Noop)
	}
}

func TestPlan_AllNoop_SameManifest(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "my op")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "billing acc")
	fc.addAccount(acc)
	cl := testCluster(clID, opID, "prod", []string{"nats://localhost:4222"})
	fc.addCluster(cl)
	sk := testSSK(sskID, accID, "writer", "")
	sk.Permissions = &nisv1.UserPermissions{PubAllow: []string{"events.>"}}
	fc.addScopedKey(sk)
	u := testUser(usrID, accID, "api", "api user")
	fc.addUser(u)

	result, err := Plan(context.Background(), fc, minimalBatch())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if result.Summary.Noop != 5 {
		t.Errorf("Noop=%d want 5", result.Summary.Noop)
	}
	if result.Summary.Create != 0 || result.Summary.Update != 0 {
		t.Errorf("Create=%d Update=%d want 0", result.Summary.Create, result.Summary.Update)
	}
}

func TestPlan_AccountDescriptionDrift(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "my op")
	fc.addOperator(op)
	// Server has different description
	acc := testAccount(accID, opID, "billing", "old description")
	fc.addAccount(acc)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{Description: "my op"}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{Description: "new description"}},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	var accItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindAccount {
			accItem = &result.Items[i]
		}
	}
	if accItem == nil {
		t.Fatal("no Account item in result")
	}
	if accItem.Action != ActionUpdate {
		t.Errorf("Action=%v want ActionUpdate", accItem.Action)
	}
	if len(accItem.Updates) != 1 || accItem.Updates[0].RPC != "UpdateAccount" {
		t.Errorf("Updates=%v want [{UpdateAccount [description]}]", accItem.Updates)
	}
	if len(accItem.Updates[0].Fields) != 1 || accItem.Updates[0].Fields[0] != "description" {
		t.Errorf("Fields=%v want [description]", accItem.Updates[0].Fields)
	}
}

func TestPlan_AccountJetStreamDrift(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "my op")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "billing acc")
	acc.JetstreamLimits = &nisv1.JetStreamLimits{Enabled: true, MaxMemory: 1 << 30} // 1Gi
	fc.addAccount(acc)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{
			Description: "billing acc",
			JetStream:   &JetStreamSpec{Enabled: true, MaxMemory: 2 << 30}, // 2Gi
		}},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var accItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindAccount {
			accItem = &result.Items[i]
		}
	}
	if accItem == nil {
		t.Fatal("no Account item")
	}
	if accItem.Action != ActionUpdate {
		t.Errorf("Action=%v want ActionUpdate", accItem.Action)
	}
	found := false
	for _, op := range accItem.Updates {
		if op.RPC == "UpdateJetStreamLimits" {
			found = true
			hasField := false
			for _, f := range op.Fields {
				if f == "jetStream.maxMemory" {
					hasField = true
				}
			}
			if !hasField {
				t.Errorf("UpdateJetStreamLimits fields %v missing jetStream.maxMemory", op.Fields)
			}
		}
	}
	if !found {
		t.Errorf("no UpdateJetStreamLimits op, got %v", accItem.Updates)
	}
}

func TestPlan_AccountDescAndJetStreamDrift_TwoOps(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "old desc")
	acc.JetstreamLimits = &nisv1.JetStreamLimits{Enabled: true, MaxMemory: 1 << 30}
	fc.addAccount(acc)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{
			Description: "new desc",
			JetStream:   &JetStreamSpec{Enabled: true, MaxMemory: 2 << 30},
		}},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var accItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindAccount {
			accItem = &result.Items[i]
		}
	}
	if len(accItem.Updates) != 2 {
		t.Errorf("Updates count=%d want 2, got %v", len(accItem.Updates), accItem.Updates)
	}
	rpcs := make(map[string]bool)
	for _, u := range accItem.Updates {
		rpcs[u.RPC] = true
	}
	if !rpcs["UpdateAccount"] || !rpcs["UpdateJetStreamLimits"] {
		t.Errorf("RPCs=%v want both UpdateAccount and UpdateJetStreamLimits", rpcs)
	}
}

func TestPlan_NilJetStream_NoopDespiteServerHaving(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "billing acc")
	acc.JetstreamLimits = &nisv1.JetStreamLimits{Enabled: true, MaxMemory: 1 << 30}
	fc.addAccount(acc)

	// nil JetStream in manifest = don't touch
	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{
			Description: "billing acc",
			JetStream:   nil,
		}},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var accItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindAccount {
			accItem = &result.Items[i]
		}
	}
	if accItem.Action != ActionNoop {
		t.Errorf("Action=%v want ActionNoop (nil JetStream = don't touch)", accItem.Action)
	}
}

func TestPlan_OperatorJWTPolicyDrift(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	// Server has userJwtTtlSeconds=0
	fc.addOperator(op)

	ttl := 24 * time.Hour
	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
			Metadata: ObjectMeta{Name: "acme"},
			Operator: &OperatorSpec{
				JWTPolicy: &JWTPolicySpec{UserJWTTTL: ttl},
			},
		},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	item := result.Items[0]
	if item.Action != ActionUpdate {
		t.Errorf("Action=%v want ActionUpdate", item.Action)
	}
	found := false
	for _, u := range item.Updates {
		if u.RPC == "SetJWTPolicy" {
			found = true
			hasField := false
			for _, f := range u.Fields {
				if f == "jwtPolicy.userJWTTTL" {
					hasField = true
				}
			}
			if !hasField {
				t.Errorf("SetJWTPolicy fields=%v missing jwtPolicy.userJWTTTL", u.Fields)
			}
		}
	}
	if !found {
		t.Errorf("no SetJWTPolicy op, got %v", item.Updates)
	}
}

func TestPlan_SSKPermissionsDrift(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "")
	fc.addAccount(acc)
	sk := testSSK(sskID, accID, "writer", "")
	sk.Permissions = &nisv1.UserPermissions{PubAllow: []string{"events.>"}}
	fc.addScopedKey(sk)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{}},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
			Metadata: ObjectMeta{Name: "writer", Operator: "acme", Account: "billing"},
			ScopedSigningKey: &ScopedSigningKeySpec{
				PubAllow: []string{"events.>", "other.>"},
			},
		},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var sskItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindScopedSigningKey {
			sskItem = &result.Items[i]
		}
	}
	if sskItem == nil {
		t.Fatal("no ScopedSigningKey item")
	}
	if sskItem.Action != ActionUpdate {
		t.Errorf("Action=%v want ActionUpdate", sskItem.Action)
	}
	found := false
	for _, u := range sskItem.Updates {
		if u.RPC == "UpdatePermissions" {
			found = true
		}
	}
	if !found {
		t.Errorf("no UpdatePermissions op, got %v", sskItem.Updates)
	}
}

func TestPlan_UserScopedKeyReassignment_HardError(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "")
	fc.addAccount(acc)
	// Server user has scoped key "writer" (by ID sskID)
	sk := testSSK(sskID, accID, "writer", "")
	fc.addScopedKey(sk)
	u := testUser(usrID, accID, "api", "")
	u.ScopedSigningKeyId = sskID
	fc.addUser(u)

	// Manifest wants to change scopedKey to "reader"
	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey}, Metadata: ObjectMeta{Name: "writer", Operator: "acme", Account: "billing"}, ScopedSigningKey: &ScopedSigningKeySpec{}},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
			Metadata: ObjectMeta{Name: "api", Operator: "acme", Account: "billing"},
			User:     &UserSpec{ScopedKey: "reader"},
		},
	}
	_, err := Plan(context.Background(), fc, batch)
	if err == nil {
		t.Fatal("expected error on scopedKey reassignment, got nil")
	}
	if !strings.Contains(err.Error(), "reassignment requires delete+recreate") {
		t.Errorf("error message %q doesn't mention 'reassignment requires delete+recreate'", err.Error())
	}
}

func TestPlan_ClusterDrift_NoopWithNote(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	cl := testCluster(clID, opID, "prod", []string{"nats://old:4222"})
	fc.addCluster(cl)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster}, Metadata: ObjectMeta{Name: "prod", Operator: "acme"}, Cluster: &ClusterSpec{ServerURLs: []string{"nats://new:4222"}}},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var clItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindCluster {
			clItem = &result.Items[i]
		}
	}
	if clItem == nil {
		t.Fatal("no Cluster item")
	}
	if clItem.Action != ActionNoop {
		t.Errorf("Action=%v want ActionNoop", clItem.Action)
	}
	if !strings.Contains(clItem.Note, "cluster updates via manifest are not supported") {
		t.Errorf("Note=%q missing expected message", clItem.Note)
	}
}

func TestPlan_DefaultSSKOnNewAccount_UpdateWithNilID(t *testing.T) {
	fc := newFakePlannerClient()
	// Operator exists, but account does not → it's a CREATE.
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	// No account "billing" in server state.

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{}},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
			Metadata: ObjectMeta{Name: "default", Operator: "acme", Account: "billing"},
			ScopedSigningKey: &ScopedSigningKeySpec{
				PubAllow: []string{"payments.>"},
			},
		},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var sskItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindScopedSigningKey {
			sskItem = &result.Items[i]
		}
	}
	if sskItem == nil {
		t.Fatal("no ScopedSigningKey item")
	}
	if sskItem.Action != ActionUpdate {
		t.Errorf("Action=%v want ActionUpdate (default key on new account)", sskItem.Action)
	}
	if sskItem.ExistingID != uuid.Nil {
		t.Errorf("ExistingID=%v want uuid.Nil", sskItem.ExistingID)
	}
	if !strings.Contains(sskItem.Note, "auto-created by server") {
		t.Errorf("Note=%q missing 'auto-created by server'", sskItem.Note)
	}
}

func TestPlan_DefaultSSKOnExistingAccount_NormalUpdate(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "")
	fc.addAccount(acc)
	// Server has default key with pub=[">"]
	sk := testSSK(sskID, accID, "default", "")
	sk.Permissions = &nisv1.UserPermissions{PubAllow: []string{">"}, SubAllow: []string{">"}}
	fc.addScopedKey(sk)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{}},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
			Metadata: ObjectMeta{Name: "default", Operator: "acme", Account: "billing"},
			ScopedSigningKey: &ScopedSigningKeySpec{
				PubAllow: []string{"payments.>"},
				SubAllow: []string{">"},
			},
		},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var sskItem *PlanItem
	for i := range result.Items {
		if result.Items[i].Object.Kind == KindScopedSigningKey {
			sskItem = &result.Items[i]
		}
	}
	if sskItem.Action != ActionUpdate {
		t.Errorf("Action=%v want ActionUpdate", sskItem.Action)
	}
	if sskItem.ExistingID.String() != sskID {
		t.Errorf("ExistingID=%v want %v", sskItem.ExistingID, sskID)
	}
	if sskItem.Note != "" {
		t.Errorf("Note should be empty for normal update, got %q", sskItem.Note)
	}
}

func TestPlan_TopoOrder(t *testing.T) {
	fc := newFakePlannerClient()
	// All creates — server is empty.

	// Batch in random order: User, ScopedSigningKey, Cluster, Account, Operator
	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser}, Metadata: ObjectMeta{Name: "u", Operator: "acme", Account: "acc"}, User: &UserSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey}, Metadata: ObjectMeta{Name: "sk", Operator: "acme", Account: "acc"}, ScopedSigningKey: &ScopedSigningKeySpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster}, Metadata: ObjectMeta{Name: "cl", Operator: "acme"}, Cluster: &ClusterSpec{ServerURLs: []string{"nats://localhost:4222"}}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "acc", Operator: "acme"}, Account: &AccountSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantOrder := []string{KindOperator, KindCluster, KindAccount, KindScopedSigningKey, KindUser}
	for i, item := range result.Items {
		if item.Object.Kind != wantOrder[i] {
			t.Errorf("Items[%d].Kind=%q want %q", i, item.Object.Kind, wantOrder[i])
		}
	}
}

func TestPlan_OrderInsensitiveSetEquality(t *testing.T) {
	fc := newFakePlannerClient()
	op := testOperator(opID, "acme", "")
	fc.addOperator(op)
	acc := testAccount(accID, opID, "billing", "")
	fc.addAccount(acc)
	sk := testSSK(sskID, accID, "writer", "")
	sk.Permissions = &nisv1.UserPermissions{PubAllow: []string{"a", "b", "c"}}
	fc.addScopedKey(sk)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "acme"}, Operator: &OperatorSpec{}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "billing", Operator: "acme"}, Account: &AccountSpec{}},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
			Metadata: ObjectMeta{Name: "writer", Operator: "acme", Account: "billing"},
			// Different order but same set.
			ScopedSigningKey: &ScopedSigningKeySpec{PubAllow: []string{"c", "a", "b"}},
		},
	}
	result, err := Plan(context.Background(), fc, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, item := range result.Items {
		if item.Object.Kind == KindScopedSigningKey && item.Action != ActionNoop {
			t.Errorf("Action=%v want ActionNoop (set-equal pubAllow)", item.Action)
		}
	}
}
