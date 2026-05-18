package manifest

import (
	"testing"
	"time"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

// buildDumpFixture returns a proto state for a single operator with one
// cluster, two accounts ($SYS filtered, app kept), one default SKK, one
// named SKK, one system user (filtered), and one app user.
func buildDumpFixture() (
	op *nisv1.Operator,
	clusters []*nisv1.Cluster,
	accounts []*nisv1.Account,
	skks []*nisv1.ScopedSigningKey,
	users []*nisv1.User,
) {
	op = &nisv1.Operator{
		Id:          "op-id-1",
		Name:        "acme",
		Description: "acme corp",
		// Non-zero JWT policy.
		UserJwtTtlSeconds:    int64(8760 * time.Hour.Seconds()),
		AccountJwtTtlSeconds: 0,
		JwtWarnWindowSeconds: int64(168 * time.Hour.Seconds()),
		JwtAutoRenew:         true,
	}

	clusters = []*nisv1.Cluster{
		{
			Id:          "cl-id-1",
			OperatorId:  "op-id-1",
			Name:        "prod",
			Description: "prod cluster",
			ServerUrls:  []string{"nats://localhost:4222"},
		},
	}

	sysAcc := &nisv1.Account{
		Id:         "acc-sys-id",
		OperatorId: "op-id-1",
		Name:       "$SYS",
	}
	appAcc := &nisv1.Account{
		Id:          "acc-app-id",
		OperatorId:  "op-id-1",
		Name:        "payments",
		Description: "payments account",
		JetstreamLimits: &nisv1.JetStreamLimits{
			Enabled:      true,
			MaxMemory:    1 << 30, // 1Gi
			MaxStorage:   4 << 30, // 4Gi
			MaxStreams:   10,
			MaxConsumers: 100,
		},
	}
	accounts = []*nisv1.Account{sysAcc, appAcc}

	defaultSKK := &nisv1.ScopedSigningKey{
		Id:        "skk-default-id",
		AccountId: "acc-app-id",
		Name:      "default",
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{">"},
			SubAllow: []string{">"},
		},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
	writerSKK := &nisv1.ScopedSigningKey{
		Id:        "skk-writer-id",
		AccountId: "acc-app-id",
		Name:      "writer",
		Permissions: &nisv1.UserPermissions{
			PubAllow: []string{"events.>"},
			SubAllow: []string{"_INBOX.>"},
		},
		ResponsePermission: &nisv1.ResponsePermission{},
	}
	skks = []*nisv1.ScopedSigningKey{defaultSKK, writerSKK}

	systemUser := &nisv1.User{
		Id:        "user-system-id",
		AccountId: "acc-app-id",
		Name:      "system",
	}
	apiUser := &nisv1.User{
		Id:                 "user-api-id",
		AccountId:          "acc-app-id",
		Name:               "payments-api",
		Description:        "API user",
		ScopedSigningKeyId: "skk-writer-id",
	}
	ttl := 24 * time.Hour
	ttlUser := &nisv1.User{
		Id:            "user-ttl-id",
		AccountId:     "acc-app-id",
		Name:          "ttl-user",
		JwtTtlSeconds: &[]int64{int64(ttl.Seconds())}[0],
	}
	users = []*nisv1.User{systemUser, apiUser, ttlUser}
	return
}

func TestDumpObjects_SYSAccountFiltered(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{
		KindOperator: true, KindCluster: true, KindAccount: true,
		KindScopedSigningKey: true, KindUser: true,
	}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	for _, obj := range objs {
		if obj.Kind == KindAccount && obj.Metadata.Name == "$SYS" {
			t.Error("$SYS account must not appear in dump output")
		}
	}
}

func TestDumpObjects_SystemUserFiltered(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{
		KindOperator: true, KindCluster: true, KindAccount: true,
		KindScopedSigningKey: true, KindUser: true,
	}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	for _, obj := range objs {
		if obj.Kind == KindUser && obj.Metadata.Name == "system" {
			t.Error("system user must not appear in dump output")
		}
	}
}

func TestDumpObjects_DefaultSKKIncluded(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{
		KindOperator: true, KindCluster: true, KindAccount: true,
		KindScopedSigningKey: true, KindUser: true,
	}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	found := false
	for _, obj := range objs {
		if obj.Kind == KindScopedSigningKey && obj.Metadata.Name == "default" {
			found = true
		}
	}
	if !found {
		t.Error("default ScopedSigningKey must be included in dump output")
	}
}

func TestDumpObjects_JWTPolicyEmittedWhenNonZero(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{KindOperator: true}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	if objs[0].Operator.JWTPolicy == nil {
		t.Error("JWTPolicy must be non-nil when operator has non-zero JWT fields")
	}
}

func TestDumpObjects_JWTPolicyOmittedWhenZero(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	op.UserJwtTtlSeconds = 0
	op.AccountJwtTtlSeconds = 0
	op.JwtWarnWindowSeconds = 0
	op.JwtAutoRenew = false

	kinds := map[string]bool{KindOperator: true}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	if objs[0].Operator.JWTPolicy != nil {
		t.Error("JWTPolicy must be nil when all operator JWT fields are zero")
	}
}

func TestDumpObjects_JetStreamOmittedWhenAllZero(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	// Replace app account with one that has no JetStream.
	for i, acc := range accounts {
		if acc.Name == "payments" {
			accounts[i] = &nisv1.Account{
				Id:         acc.Id,
				OperatorId: acc.OperatorId,
				Name:       acc.Name,
			}
		}
	}

	kinds := map[string]bool{KindAccount: true}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	for _, obj := range objs {
		if obj.Kind == KindAccount && obj.Account.JetStream != nil {
			t.Errorf("JetStream must be nil for account with no JetStream config, got %+v", obj.Account.JetStream)
		}
	}
}

func TestDumpObjects_UserScopedKeyResolved(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{KindUser: true}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	found := false
	for _, obj := range objs {
		if obj.Kind == KindUser && obj.Metadata.Name == "payments-api" {
			found = true
			if obj.User.ScopedKey != "writer" {
				t.Errorf("User payments-api.ScopedKey: want %q, got %q", "writer", obj.User.ScopedKey)
			}
		}
	}
	if !found {
		t.Error("payments-api user not found in output")
	}
}

func TestDumpObjects_UserJWTTTLIncludedWhenSet(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{KindUser: true}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	found := false
	for _, obj := range objs {
		if obj.Kind == KindUser && obj.Metadata.Name == "ttl-user" {
			found = true
			if obj.User.JWTTTL == nil {
				t.Error("ttl-user: JWTTTL must be non-nil")
			} else if *obj.User.JWTTTL != 24*time.Hour {
				t.Errorf("ttl-user: JWTTTL want 24h, got %s", *obj.User.JWTTTL)
			}
		}
	}
	if !found {
		t.Error("ttl-user not found in output")
	}
}

func TestDumpObjects_KindsFilter(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{KindOperator: true, KindCluster: true}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	for _, obj := range objs {
		if obj.Kind != KindOperator && obj.Kind != KindCluster {
			t.Errorf("unexpected kind %q in filtered output", obj.Kind)
		}
	}
	// Expect: 1 operator + 1 cluster.
	if len(objs) != 2 {
		t.Errorf("expected 2 objects (operator + cluster), got %d", len(objs))
	}
}

// TestDumpRoundTrip builds a full dump, encodes it to YAML, parses it back,
// and verifies the key fields survived the round-trip.
func TestDumpRoundTrip(t *testing.T) {
	op, clusters, accounts, skks, users := buildDumpFixture()
	kinds := map[string]bool{
		KindOperator: true, KindCluster: true, KindAccount: true,
		KindScopedSigningKey: true, KindUser: true,
	}
	objs := DumpObjects(op, clusters, accounts, skks, users, kinds)

	data, err := EncodeYAML(objs)
	if err != nil {
		t.Fatalf("EncodeYAML: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("EncodeYAML returned empty output")
	}

	parsed, err := Parse(data, "<test>")
	if err != nil {
		t.Fatalf("Parse: %v\nYAML:\n%s", err, data)
	}

	// Validate parse succeeded; reserved name checks are skipped by Parse,
	// but validate should still pass.
	if _, err := Validate(parsed, ValidateOptions{StrictRefs: false}); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Count kinds in parsed output.
	kindCount := make(map[string]int)
	for _, obj := range parsed {
		kindCount[obj.Kind]++
	}

	// $SYS and system should be filtered before encoding.
	if kindCount[KindAccount] != 1 {
		t.Errorf("expected 1 Account (payments), got %d", kindCount[KindAccount])
	}
	// system user filtered, ttl-user and payments-api remain.
	if kindCount[KindUser] != 2 {
		t.Errorf("expected 2 Users, got %d", kindCount[KindUser])
	}

	// Find the operator and check JWTPolicy round-trip.
	for _, obj := range parsed {
		if obj.Kind == KindOperator {
			if obj.Operator.JWTPolicy == nil {
				t.Error("parsed Operator: JWTPolicy is nil after round-trip")
			} else {
				want := time.Duration(op.UserJwtTtlSeconds) * time.Second
				if obj.Operator.JWTPolicy.UserJWTTTL != want {
					t.Errorf("JWTPolicy.UserJWTTTL: want %s, got %s", want, obj.Operator.JWTPolicy.UserJWTTTL)
				}
			}
		}
		if obj.Kind == KindAccount && obj.Metadata.Name == "payments" {
			if obj.Account.JetStream == nil {
				t.Error("parsed Account payments: JetStream is nil after round-trip")
			} else {
				if obj.Account.JetStream.MaxMemory != 1<<30 {
					t.Errorf("JetStream.MaxMemory: want %d, got %d", 1<<30, obj.Account.JetStream.MaxMemory)
				}
			}
		}
		if obj.Kind == KindUser && obj.Metadata.Name == "payments-api" {
			if obj.User.ScopedKey != "writer" {
				t.Errorf("User payments-api.ScopedKey: want %q, got %q", "writer", obj.User.ScopedKey)
			}
		}
	}
}
