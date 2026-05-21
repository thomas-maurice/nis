package manifest

import (
	"context"
	"testing"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// ---- helpers ----

func mustDeleteAll(t *testing.T, c PlannerClient, batch []Object) *DeleteResult {
	t.Helper()
	res, err := DeleteAll(context.Background(), c, batch)
	if err != nil {
		t.Fatalf("DeleteAll returned unexpected error: %v", err)
	}
	return res
}

func assertAllDeleted(t *testing.T, res *DeleteResult) {
	t.Helper()
	for _, it := range res.Items {
		if it.Outcome == OutcomeFailed {
			t.Errorf("item %s/%s outcome=Failed note=%q", it.Object.Kind, it.Object.Metadata.Name, it.Note)
		}
	}
}

// ---- tests ----

// TestDeleteAll_ReverseTopoOrder verifies that deletion happens in
// User → SSK → Account → Cluster → Operator order.
func TestDeleteAll_ReverseTopoOrder(t *testing.T) {
	f := newFakePlannerClient()
	op := &nisv1.Operator{Id: "op-del", Name: "delop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: "acc-del", OperatorId: "op-del", Name: "delacc"}
	f.addAccount(acc)
	sk := &nisv1.ScopedSigningKey{
		Id:                 "ssk-del",
		AccountId:          "acc-del",
		Name:               "writer",
		Permissions:        &nisv1.UserPermissions{},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
	f.addScopedKey(sk)
	u := &nisv1.User{Id: "u-del", AccountId: "acc-del", Name: "frank"}
	f.addUser(u)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "delop"}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "delacc", Operator: "delop"}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey}, Metadata: ObjectMeta{Name: "writer", Operator: "delop", Account: "delacc"}},
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser}, Metadata: ObjectMeta{Name: "frank", Operator: "delop", Account: "delacc"}},
	}

	res := mustDeleteAll(t, f, batch)
	assertAllDeleted(t, res)

	// All four should be OutcomeApplied.
	if len(res.Items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(res.Items))
	}
	for _, it := range res.Items {
		if it.Outcome != OutcomeApplied {
			t.Errorf("item %s/%s outcome=%v", it.Object.Kind, it.Object.Metadata.Name, it.Outcome)
		}
	}

	// Verify call order: User → SSK → Account → Operator
	wantOrder := []string{"DeleteUser", "DeleteScopedSigningKey", "DeleteAccount", "DeleteOperator"}
	// Extract only delete calls from the log.
	var deleteCalls []string
	for _, c := range f.callLog {
		if len(c) > 6 && c[:6] == "Delete" {
			deleteCalls = append(deleteCalls, c)
		}
	}
	if len(deleteCalls) != len(wantOrder) {
		t.Fatalf("delete call count = %d, want %d: %v", len(deleteCalls), len(wantOrder), deleteCalls)
	}
	for i, want := range wantOrder {
		if deleteCalls[i] != want {
			t.Errorf("delete call[%d] = %q, want %q", i, deleteCalls[i], want)
		}
	}
}

// TestDeleteAll_ReservedAccountSYS verifies that $SYS account is refused.
func TestDeleteAll_ReservedAccountSYS(t *testing.T) {
	f := newFakePlannerClient()
	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount}, Metadata: ObjectMeta{Name: "$SYS", Operator: "op"}},
	}
	_, err := DeleteAll(context.Background(), f, batch)
	if err == nil {
		t.Fatal("expected error for $SYS account, got nil")
	}
}

// TestDeleteAll_ReservedUserSystem verifies that "system" user is refused.
func TestDeleteAll_ReservedUserSystem(t *testing.T) {
	f := newFakePlannerClient()
	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser}, Metadata: ObjectMeta{Name: "system", Operator: "op", Account: "acc"}},
	}
	_, err := DeleteAll(context.Background(), f, batch)
	if err == nil {
		t.Fatal("expected error for system user, got nil")
	}
}

// TestDeleteAll_ReservedSSKDefault verifies that "default" SSK is refused.
func TestDeleteAll_ReservedSSKDefault(t *testing.T) {
	f := newFakePlannerClient()
	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey}, Metadata: ObjectMeta{Name: "default", Operator: "op", Account: "acc"}},
	}
	_, err := DeleteAll(context.Background(), f, batch)
	if err == nil {
		t.Fatal("expected error for default SSK, got nil")
	}
}

// TestDeleteAll_IdempotentNotFound verifies that a 404 on lookup → OutcomeNoop.
func TestDeleteAll_IdempotentNotFound(t *testing.T) {
	f := newFakePlannerClient()
	// No operator seeded → lookup will 404.
	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "ghostop"}},
	}
	res := mustDeleteAll(t, f, batch)
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(res.Items))
	}
	if res.Items[0].Outcome != OutcomeNoop {
		t.Errorf("outcome = %v, want OutcomeNoop", res.Items[0].Outcome)
	}
}

// TestDeleteAll_FailureAbortsBatch verifies that a failure stops processing
// subsequent items.
func TestDeleteAll_FailureAbortsBatch(t *testing.T) {
	f := newFakePlannerClient()
	// Seed an operator so the second delete can succeed (it won't be reached).
	f.addOperator(&nisv1.Operator{Id: "op-abort", Name: "abortop"})

	// First object is a user with a non-existent account (operator exists but account doesn't).
	// The lookup chain will: find operator OK → GetAccountByName → 404.
	// 404 on the account → parent not found → OutcomeNoop (already gone).
	// Then operator delete will succeed.
	// To get a real failure (not noop), we need something that errors (not 404).
	// Easiest: use an operator that doesn't exist so the second item triggers
	// a failure. But 404 on operator also gives Noop. So let's trigger a
	// reserved-name check which gives OutcomeFailed + error, aborting the batch.
	batch := []Object{
		// Reserved name → OutcomeFailed + abort.
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser}, Metadata: ObjectMeta{Name: "system", Operator: "abortop", Account: "someacc"}},
		// This operator should never be touched.
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator}, Metadata: ObjectMeta{Name: "abortop"}},
	}

	result, err := DeleteAll(context.Background(), f, batch)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The "system" user item should appear as Failed.
	if len(result.Items) < 1 {
		t.Fatal("expected at least 1 item in result")
	}
	if result.Items[0].Outcome != OutcomeFailed {
		t.Errorf("first item outcome = %v, want OutcomeFailed", result.Items[0].Outcome)
	}
	// The operator should not have been touched.
	assertCallsNotContain(t, f, "DeleteOperator")
}

// TestDeleteAll_UserAndAccountChain verifies that deleting a user resolves
// the operator→account chain correctly when they're in the cache.
func TestDeleteAll_UserAndAccountChain(t *testing.T) {
	f := newFakePlannerClient()
	op := &nisv1.Operator{Id: "op-chain", Name: "chainop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: "acc-chain", OperatorId: "op-chain", Name: "chainacc"}
	f.addAccount(acc)
	u := &nisv1.User{Id: "u-chain", AccountId: "acc-chain", Name: "gina"}
	f.addUser(u)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser}, Metadata: ObjectMeta{Name: "gina", Operator: "chainop", Account: "chainacc"}},
	}
	res := mustDeleteAll(t, f, batch)
	assertAllDeleted(t, res)
	assertCallsContain(t, f, "DeleteUser")

	if len(f.users["acc-chain"]) != 0 {
		t.Error("user still present in fake state after delete")
	}
}

// TestDeleteAll_ClusterDelete verifies cluster delete resolves operator and deletes.
func TestDeleteAll_ClusterDelete(t *testing.T) {
	f := newFakePlannerClient()
	op := &nisv1.Operator{Id: "op-cl2", Name: "clop2"}
	f.addOperator(op)
	cl := &nisv1.Cluster{Id: "cl-id", OperatorId: "op-cl2", Name: "mycluster"}
	f.addCluster(cl)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster}, Metadata: ObjectMeta{Name: "mycluster", Operator: "clop2"}},
	}
	res := mustDeleteAll(t, f, batch)
	assertAllDeleted(t, res)
	assertCallsContain(t, f, "DeleteCluster")

	if len(f.clusters["op-cl2"]) != 0 {
		t.Error("cluster still present in fake state after delete")
	}
}

// TestDeleteAll_SSKDelete verifies SSK delete resolves the full operator→account→ssk chain.
func TestDeleteAll_SSKDelete(t *testing.T) {
	f := newFakePlannerClient()
	op := &nisv1.Operator{Id: "op-ssk2", Name: "sskop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: "acc-ssk2", OperatorId: "op-ssk2", Name: "sskacc"}
	f.addAccount(acc)
	sk := &nisv1.ScopedSigningKey{
		Id:                 "ssk-id2",
		AccountId:          "acc-ssk2",
		Name:               "mykey",
		Permissions:        &nisv1.UserPermissions{},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
	f.addScopedKey(sk)

	batch := []Object{
		{TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey}, Metadata: ObjectMeta{Name: "mykey", Operator: "sskop", Account: "sskacc"}},
	}
	res := mustDeleteAll(t, f, batch)
	assertAllDeleted(t, res)
	assertCallsContain(t, f, "DeleteScopedSigningKey")
}
