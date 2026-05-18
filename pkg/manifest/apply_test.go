package manifest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// ---- helpers ----

func mustApply(t *testing.T, c PlannerClient, plan *PlanResult) *ApplyResult {
	t.Helper()
	res, err := Apply(context.Background(), c, plan)
	if err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}
	return res
}

func assertAllApplied(t *testing.T, res *ApplyResult) {
	t.Helper()
	for _, it := range res.Items {
		if it.Outcome == OutcomeFailed {
			t.Errorf("item %s/%s outcome=Failed note=%q", it.Object.Kind, it.Object.Metadata.Name, it.Note)
		}
	}
}

func assertCallsContain(t *testing.T, f *fakePlannerClient, calls ...string) {
	t.Helper()
	got := make(map[string]int)
	for _, c := range f.callLog {
		got[c]++
	}
	for _, want := range calls {
		if got[want] == 0 {
			t.Errorf("expected call %q in log %v", want, f.callLog)
		}
	}
}

func assertCallsNotContain(t *testing.T, f *fakePlannerClient, calls ...string) {
	t.Helper()
	got := make(map[string]int)
	for _, c := range f.callLog {
		got[c]++
	}
	for _, notWant := range calls {
		if got[notWant] > 0 {
			t.Errorf("unexpected call %q in log %v", notWant, f.callLog)
		}
	}
}

// buildFullBatch builds a minimal 4-object batch: Op + Acc + SKK + User
// (no prior server state — all creates).
func buildFullBatch(opName, accName, skkName, userName string) []Object {
	return []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
			Metadata: ObjectMeta{Name: opName},
			Operator: &OperatorSpec{Description: "test operator"},
		},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
			Metadata: ObjectMeta{Name: accName, Operator: opName},
			Account:  &AccountSpec{Description: "test account"},
		},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
			Metadata: ObjectMeta{Name: skkName, Operator: opName, Account: accName},
			ScopedSigningKey: &ScopedSigningKeySpec{
				PubAllow: []string{"foo.>"},
				SubAllow: []string{"foo.>"},
			},
		},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
			Metadata: ObjectMeta{Name: userName, Operator: opName, Account: accName},
			User:     &UserSpec{ScopedKey: skkName},
		},
	}
}

// newUUID returns a fresh random UUID string for test fixtures.
func newUUID() string { return uuid.New().String() }

// ---- tests ----

// TestApply_CreateOnlyPath verifies that a fresh server gets all four entities
// created and the SKK reference is resolved from cache.
func TestApply_CreateOnlyPath(t *testing.T) {
	f := newFakePlannerClient()
	batch := buildFullBatch("op1", "acc1", "writer", "alice")
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	res := mustApply(t, f, plan)
	assertAllApplied(t, res)

	assertCallsContain(t, f, "CreateOperator", "CreateAccount", "CreateScopedSigningKey", "CreateUser")

	// Verify cache-based SKK reference: the user should have the SKK ID, not "".
	var createdUser *nisv1.User
	for _, users := range f.users {
		for _, u := range users {
			if u.GetName() == "alice" {
				createdUser = u
			}
		}
	}
	if createdUser == nil {
		t.Fatal("user 'alice' not found in fake state")
	}
	if createdUser.GetScopedSigningKeyId() == "" {
		t.Error("user alice has empty ScopedSigningKeyId — cache resolution failed")
	}
}

// TestApply_CreateOperatorWithJWTPolicy verifies that CreateOperator is followed
// by SetJWTPolicy when the manifest declares a jwtPolicy.
func TestApply_CreateOperatorWithJWTPolicy(t *testing.T) {
	f := newFakePlannerClient()
	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
			Metadata: ObjectMeta{Name: "op-policy"},
			Operator: &OperatorSpec{
				Description: "with policy",
				JWTPolicy: &JWTPolicySpec{
					UserJWTTTL: 24 * time.Hour,
					AutoRenew:  true,
				},
			},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "CreateOperator", "SetJWTPolicy")

	// Verify the policy was applied.
	op := f.findOperatorByName("op-policy")
	if op == nil {
		t.Fatal("operator not found")
	}
	if op.GetUserJwtTtlSeconds() != int64((24 * time.Hour).Seconds()) {
		t.Errorf("userJwtTtlSeconds = %d, want %d", op.GetUserJwtTtlSeconds(), int64((24 * time.Hour).Seconds()))
	}
	if !op.GetJwtAutoRenew() {
		t.Error("jwtAutoRenew not set")
	}
}

// TestApply_CreateAccountWithJetStream verifies JetStream limits are passed in
// the CreateAccount call (single-shot, no separate UpdateJetStreamLimits).
func TestApply_CreateAccountWithJetStream(t *testing.T) {
	f := newFakePlannerClient()
	f.addOperator(&nisv1.Operator{Id: newUUID(), Name: "myop"})

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
			Metadata: ObjectMeta{Name: "jsacc", Operator: "myop"},
			Account: &AccountSpec{
				JetStream: &JetStreamSpec{
					Enabled:    true,
					MaxMemory:  1 << 30,
					MaxStorage: 2 << 30,
					MaxStreams: 10,
				},
			},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "CreateAccount")
	assertCallsNotContain(t, f, "UpdateJetStreamLimits")

	// Retrieve the created account's operatorId.
	op := f.findOperatorByName("myop")
	var acc *nisv1.Account
	for _, a := range f.accounts[op.GetId()] {
		if a.GetName() == "jsacc" {
			acc = a
		}
	}
	if acc == nil {
		t.Fatal("account not found")
	}
	if !acc.GetJetstreamLimits().GetEnabled() {
		t.Error("jetstream not enabled")
	}
	if acc.GetJetstreamLimits().GetMaxMemory() != 1<<30 {
		t.Errorf("maxMemory = %d", acc.GetJetstreamLimits().GetMaxMemory())
	}
}

// TestApply_UpdateAccountDescription verifies that an existing account with a
// changed description triggers UpdateAccount.
func TestApply_UpdateAccountDescription(t *testing.T) {
	f := newFakePlannerClient()
	opID := newUUID()
	accID := newUUID()
	op := &nisv1.Operator{Id: opID, Name: "upop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: accID, OperatorId: opID, Name: "upacc", Description: "old desc"}
	f.addAccount(acc)

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
			Metadata: ObjectMeta{Name: "upacc", Operator: "upop"},
			Account:  &AccountSpec{Description: "new desc"},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "UpdateAccount")

	if acc.GetDescription() != "new desc" {
		t.Errorf("description not updated, got %q", acc.GetDescription())
	}
}

// TestApply_UpdateJetStreamLimits verifies that drift in JetStream limits triggers
// UpdateJetStreamLimits.
func TestApply_UpdateJetStreamLimits(t *testing.T) {
	f := newFakePlannerClient()
	opID := newUUID()
	accID := newUUID()
	op := &nisv1.Operator{Id: opID, Name: "jsop"}
	f.addOperator(op)
	acc := &nisv1.Account{
		Id:          accID,
		OperatorId:  opID,
		Name:        "jsacc2",
		Description: "desc",
		JetstreamLimits: &nisv1.JetStreamLimits{
			Enabled:    false,
			MaxMemory:  0,
			MaxStorage: 0,
		},
	}
	f.addAccount(acc)

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
			Metadata: ObjectMeta{Name: "jsacc2", Operator: "jsop"},
			Account: &AccountSpec{
				JetStream: &JetStreamSpec{
					Enabled:   true,
					MaxMemory: 512 * (1 << 20),
				},
			},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "UpdateJetStreamLimits")

	if !acc.GetJetstreamLimits().GetEnabled() {
		t.Error("JetStream still disabled after update")
	}
}

// TestApply_UpdateSKKPermissions verifies drift in permissions triggers UpdatePermissions.
func TestApply_UpdateSKKPermissions(t *testing.T) {
	f := newFakePlannerClient()
	opID := newUUID()
	accID := newUUID()
	skkID := newUUID()
	op := &nisv1.Operator{Id: opID, Name: "permop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: accID, OperatorId: opID, Name: "permacc"}
	f.addAccount(acc)
	sk := &nisv1.ScopedSigningKey{
		Id:        skkID,
		AccountId: accID,
		Name:      "writer",
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">"},
			SubAllow: []string{">"},
		},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
	f.addScopedKey(sk)

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
			Metadata: ObjectMeta{Name: "writer", Operator: "permop", Account: "permacc"},
			ScopedSigningKey: &ScopedSigningKeySpec{
				PubAllow: []string{"foo.>"},
				SubAllow: []string{"foo.>"},
			},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "UpdatePermissions")
}

// TestApply_DefaultSKKAfterCreateAccount tests the case where a new account
// has a "default" key with custom permissions — the planner routes it as
// Update(uuid.Nil), and the apply engine must list keys to find the auto-created default.
func TestApply_DefaultSKKAfterCreateAccount(t *testing.T) {
	f := newFakePlannerClient()
	f.addOperator(&nisv1.Operator{Id: newUUID(), Name: "newaccop"})

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
			Metadata: ObjectMeta{Name: "newacc", Operator: "newaccop"},
			Account:  &AccountSpec{},
		},
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
			Metadata: ObjectMeta{Name: "default", Operator: "newaccop", Account: "newacc"},
			ScopedSigningKey: &ScopedSigningKeySpec{
				PubAllow: []string{"custom.>"},
				SubAllow: []string{"custom.>"},
			},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Verify plan classified the default key as Update with uuid.Nil
	var defaultItem *PlanItem
	for i := range plan.Items {
		if plan.Items[i].Object.Kind == KindScopedSigningKey {
			defaultItem = &plan.Items[i]
		}
	}
	if defaultItem == nil {
		t.Fatal("no ScopedSigningKey item in plan")
	}
	if defaultItem.ExistingID != uuid.Nil {
		t.Errorf("expected ExistingID=uuid.Nil, got %v", defaultItem.ExistingID)
	}

	res := mustApply(t, f, plan)
	assertAllApplied(t, res)

	// CreateAccount was called → fake auto-created "default" key.
	// UpdatePermissions was called on the found key.
	assertCallsContain(t, f, "CreateAccount", "UpdatePermissions")
}

// TestApply_UserScopedKeyResolvedFromServer verifies that when the SKK isn't
// in the cache (it already exists server-side), GetScopedSigningKeyByName is used.
func TestApply_UserScopedKeyResolvedFromServer(t *testing.T) {
	f := newFakePlannerClient()
	opID := newUUID()
	accID := newUUID()
	op := &nisv1.Operator{Id: opID, Name: "srvop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: accID, OperatorId: opID, Name: "srvacc"}
	f.addAccount(acc)
	skkID := newUUID()
	sk := &nisv1.ScopedSigningKey{
		Id:        skkID,
		AccountId: accID,
		Name:      "existing-key",
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">"},
			SubAllow: []string{">"},
		},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
	f.addScopedKey(sk)

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
			Metadata: ObjectMeta{Name: "bob", Operator: "srvop", Account: "srvacc"},
			User:     &UserSpec{ScopedKey: "existing-key"},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)

	var bob *nisv1.User
	for _, u := range f.users[accID] {
		if u.GetName() == "bob" {
			bob = u
		}
	}
	if bob == nil {
		t.Fatal("user 'bob' not found")
	}
	if bob.GetScopedSigningKeyId() != skkID {
		t.Errorf("ScopedSigningKeyId = %q, want %q", bob.GetScopedSigningKeyId(), skkID)
	}
}

// TestApply_JWTTTLUpdatePath verifies UpdateUser sets jwt_ttl_seconds when
// manifest has a TTL and server has none.
func TestApply_JWTTTLUpdatePath(t *testing.T) {
	f := newFakePlannerClient()
	opID := newUUID()
	accID := newUUID()
	op := &nisv1.Operator{Id: opID, Name: "ttlop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: accID, OperatorId: opID, Name: "ttlacc"}
	f.addAccount(acc)
	u := &nisv1.User{Id: newUUID(), AccountId: accID, Name: "charlie"}
	f.addUser(u)

	ttl := 2 * time.Hour
	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
			Metadata: ObjectMeta{Name: "charlie", Operator: "ttlop", Account: "ttlacc"},
			User:     &UserSpec{JWTTTL: &ttl},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "UpdateUser")

	if u.GetJwtTtlSeconds() != int64(ttl.Seconds()) {
		t.Errorf("JwtTtlSeconds = %d, want %d", u.GetJwtTtlSeconds(), int64(ttl.Seconds()))
	}
}

// TestApply_JWTTTLClearPath verifies UpdateUser sets clear_jwt_ttl=true when
// manifest has nil JWTTTL and server has a TTL set.
func TestApply_JWTTTLClearPath(t *testing.T) {
	f := newFakePlannerClient()
	opID := newUUID()
	accID := newUUID()
	op := &nisv1.Operator{Id: opID, Name: "clrop"}
	f.addOperator(op)
	acc := &nisv1.Account{Id: accID, OperatorId: opID, Name: "clracc"}
	f.addAccount(acc)
	existing := int64(3600)
	u := &nisv1.User{Id: newUUID(), AccountId: accID, Name: "dave", JwtTtlSeconds: &existing}
	f.addUser(u)

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
			Metadata: ObjectMeta{Name: "dave", Operator: "clrop", Account: "clracc"},
			// JWTTTL=nil but description drift triggers the update.
			User: &UserSpec{Description: "updated-desc"},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "UpdateUser")

	// JWTTTL is nil in manifest → ClearJwtTtl=true in request → fake sets JwtTtlSeconds=nil.
	if u.JwtTtlSeconds != nil {
		t.Errorf("expected JwtTtlSeconds to be cleared, got %d", *u.JwtTtlSeconds)
	}
}

// TestApply_FailureMidBatch verifies that a failure on a mid-batch item stops
// the batch: items before it succeed, it is OutcomeFailed, subsequent items absent.
func TestApply_FailureMidBatch(t *testing.T) {
	f := newFakePlannerClient()
	op := &nisv1.Operator{Id: newUUID(), Name: "failop"}
	f.addOperator(op)

	opExists := Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
		Metadata: ObjectMeta{Name: "failop"},
		Operator: &OperatorSpec{},
	}
	accGood := Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
		Metadata: ObjectMeta{Name: "good-acc", Operator: "failop"},
		Account:  &AccountSpec{},
	}
	// Account references a non-existent operator — will fail at GetOperatorByName.
	accBad := Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
		Metadata: ObjectMeta{Name: "bad-acc", Operator: "nonexistent-op"},
		Account:  &AccountSpec{},
	}
	userUnreached := Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
		Metadata: ObjectMeta{Name: "eve", Operator: "failop", Account: "good-acc"},
		User:     &UserSpec{},
	}

	batch := []Object{opExists, accGood, accBad, userUnreached}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	result, err := Apply(context.Background(), f, plan)
	if err == nil {
		t.Fatal("expected error from Apply, got nil")
	}

	// Find the bad account item.
	var badItem *ApplyItem
	for i := range result.Items {
		if result.Items[i].Object.Metadata.Name == "bad-acc" {
			badItem = &result.Items[i]
		}
	}
	if badItem == nil {
		t.Fatal("bad-acc item not found in result")
	}
	if badItem.Outcome != OutcomeFailed {
		t.Errorf("bad-acc outcome = %v, want OutcomeFailed", badItem.Outcome)
	}

	// The user after the bad account must not be in the result.
	for _, it := range result.Items {
		if it.Object.Metadata.Name == "eve" {
			t.Error("user 'eve' appeared in result but should have been aborted")
		}
	}
}

// TestApply_UpdateOperatorJWTPolicy verifies that an existing operator with
// drifted JWT policy triggers SetJWTPolicy.
func TestApply_UpdateOperatorJWTPolicy(t *testing.T) {
	f := newFakePlannerClient()
	f.addOperator(&nisv1.Operator{Id: newUUID(), Name: "updop", UserJwtTtlSeconds: 0})

	newTTL := 48 * time.Hour
	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
			Metadata: ObjectMeta{Name: "updop"},
			Operator: &OperatorSpec{
				JWTPolicy: &JWTPolicySpec{UserJWTTTL: newTTL},
			},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "SetJWTPolicy")

	op := f.findOperatorByName("updop")
	if op.GetUserJwtTtlSeconds() != int64(newTTL.Seconds()) {
		t.Errorf("userJwtTtlSeconds = %d, want %d", op.GetUserJwtTtlSeconds(), int64(newTTL.Seconds()))
	}
}

// TestApply_NoopItemsPassThrough verifies that noop items in the plan
// are reflected as OutcomeNoop in ApplyResult without calling any RPCs.
func TestApply_NoopItemsPassThrough(t *testing.T) {
	f := newFakePlannerClient()
	op := &nisv1.Operator{Id: newUUID(), Name: "noopop"}
	f.addOperator(op)

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
			Metadata: ObjectMeta{Name: "noopop"},
			Operator: &OperatorSpec{}, // no drift
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Summary.Noop != 1 {
		t.Fatalf("expected 1 noop, got %v", plan.Summary)
	}
	res := mustApply(t, f, plan)
	if len(res.Items) != 1 || res.Items[0].Outcome != OutcomeNoop {
		t.Errorf("expected single noop item, got %+v", res.Items)
	}
	assertCallsNotContain(t, f, "UpdateOperator", "CreateOperator")
}

// TestApply_ClusterCreate verifies that a Cluster create dispatches CreateCluster
// with the correct operatorID.
func TestApply_ClusterCreate(t *testing.T) {
	f := newFakePlannerClient()
	opID := newUUID()
	f.addOperator(&nisv1.Operator{Id: opID, Name: "clop"})

	batch := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster},
			Metadata: ObjectMeta{Name: "testcluster", Operator: "clop"},
			Cluster: &ClusterSpec{
				ServerURLs: []string{"nats://localhost:4222"},
			},
		},
	}
	plan, err := Plan(context.Background(), f, batch)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res := mustApply(t, f, plan)
	assertAllApplied(t, res)
	assertCallsContain(t, f, "CreateCluster")

	cls := f.clusters[opID]
	if len(cls) != 1 || cls[0].GetName() != "testcluster" {
		t.Errorf("cluster not created: %+v", cls)
	}
}
