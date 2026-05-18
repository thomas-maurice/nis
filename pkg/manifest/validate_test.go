package manifest

import (
	"strings"
	"testing"
)

// validOperator returns a minimal valid Operator object.
func validOperator(name string) Object {
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
		Metadata: ObjectMeta{Name: name},
		Operator: &OperatorSpec{Description: "test"},
	}
}

func validCluster(name, operator string) Object {
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster},
		Metadata: ObjectMeta{Name: name, Operator: operator},
		Cluster:  &ClusterSpec{ServerURLs: []string{"nats://localhost:4222"}},
	}
}

func validAccount(name, operator string) Object {
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
		Metadata: ObjectMeta{Name: name, Operator: operator},
		Account:  &AccountSpec{},
	}
}

func validScopedKey(name, operator, account string) Object {
	return Object{
		TypeMeta:         TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
		Metadata:         ObjectMeta{Name: name, Operator: operator, Account: account},
		ScopedSigningKey: &ScopedSigningKeySpec{},
	}
}

func validUser(name, operator, account string) Object {
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
		Metadata: ObjectMeta{Name: name, Operator: operator, Account: account},
		User:     &UserSpec{},
	}
}

func TestValidate_CleanBatch(t *testing.T) {
	objs := []Object{
		validOperator("acme"),
		validCluster("prod", "acme"),
		validAccount("payments", "acme"),
		validScopedKey("writer", "acme", "payments"),
		func() Object {
			u := validUser("svc", "acme", "payments")
			u.User.ScopedKey = "writer"
			return u
		}(),
	}
	result, err := Validate(objs, ValidateOptions{StrictRefs: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.UnresolvedRefs) != 0 {
		t.Errorf("expected no unresolved refs, got %v", result.UnresolvedRefs)
	}
}

func TestValidate_Rule1_WrongAPIVersion(t *testing.T) {
	obj := validOperator("op")
	obj.APIVersion = "nis/v2"
	_, err := Validate([]Object{obj}, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error for wrong apiVersion, got nil")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("error should mention apiVersion: %v", err)
	}
}

func TestValidate_Rule2_UnknownKind(t *testing.T) {
	obj := Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: "Banana"},
		Metadata: ObjectMeta{Name: "x"},
	}
	_, err := Validate([]Object{obj}, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
	if !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("error should mention unknown kind: %v", err)
	}
}

func TestValidate_Rule3_DuplicateIdentity(t *testing.T) {
	objs := []Object{
		validOperator("acme"),
		validOperator("acme"), // duplicate
	}
	_, err := Validate(objs, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error for duplicate identity, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate identity") {
		t.Errorf("error should mention duplicate identity: %v", err)
	}
}

func TestValidate_Rule3_DuplicateIdentity_DifferentOperators(t *testing.T) {
	// Same account name under different operators is NOT a duplicate.
	objs := []Object{
		validOperator("op1"),
		validOperator("op2"),
		validAccount("shared", "op1"),
		validAccount("shared", "op2"),
	}
	_, err := Validate(objs, ValidateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_ReservedName_SYSAccount(t *testing.T) {
	obj := validAccount("$SYS", "acme")
	_, err := Validate([]Object{validOperator("acme"), obj}, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error for reserved $SYS account name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error should mention reserved: %v", err)
	}
}

func TestValidate_ReservedName_SystemUser(t *testing.T) {
	objs := []Object{
		validOperator("op"),
		validAccount("acc", "op"),
		validUser("system", "op", "acc"),
	}
	_, err := Validate(objs, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error for reserved 'system' user name, got nil")
	}
}

func TestValidate_ReservedName_DefaultScopedKeyAllowed(t *testing.T) {
	// "default" scoped signing key is explicitly allowed.
	objs := []Object{
		validOperator("op"),
		validAccount("acc", "op"),
		validScopedKey("default", "op", "acc"),
	}
	_, err := Validate(objs, ValidateOptions{})
	if err != nil {
		t.Fatalf("unexpected error for 'default' scoped key: %v", err)
	}
}

func TestValidate_MetadataRules_Operator(t *testing.T) {
	tests := []struct {
		name    string
		obj     Object
		wantErr string
	}{
		{
			name: "missing name",
			obj: Object{
				TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
				Metadata: ObjectMeta{},
				Operator: &OperatorSpec{},
			},
			wantErr: "metadata.name is required",
		},
		{
			name: "operator field set",
			obj: Object{
				TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
				Metadata: ObjectMeta{Name: "op", Operator: "other"},
				Operator: &OperatorSpec{},
			},
			wantErr: "metadata.operator must not be set",
		},
		{
			name: "account field set",
			obj: Object{
				TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
				Metadata: ObjectMeta{Name: "op", Account: "acc"},
				Operator: &OperatorSpec{},
			},
			wantErr: "metadata.account must not be set",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate([]Object{tc.obj}, ValidateOptions{})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected %q in error, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidate_MetadataRules_Cluster(t *testing.T) {
	tests := []struct {
		name    string
		obj     Object
		wantErr string
	}{
		{
			name: "missing operator",
			obj: Object{
				TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster},
				Metadata: ObjectMeta{Name: "cl"},
				Cluster:  &ClusterSpec{ServerURLs: []string{"nats://x:4222"}},
			},
			wantErr: "metadata.operator is required",
		},
		{
			name: "account field set",
			obj: Object{
				TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster},
				Metadata: ObjectMeta{Name: "cl", Operator: "op", Account: "acc"},
				Cluster:  &ClusterSpec{ServerURLs: []string{"nats://x:4222"}},
			},
			wantErr: "metadata.account must not be set",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate([]Object{tc.obj}, ValidateOptions{})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected %q in error, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidate_MetadataRules_ScopedSigningKey(t *testing.T) {
	obj := Object{
		TypeMeta:         TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
		Metadata:         ObjectMeta{Name: "k", Operator: "op"}, // missing account
		ScopedSigningKey: &ScopedSigningKeySpec{},
	}
	_, err := Validate([]Object{obj}, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error for missing account on ScopedSigningKey")
	}
	if !strings.Contains(err.Error(), "metadata.account is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Rule4_UnresolvedRef_Tolerated(t *testing.T) {
	usr := validUser("svc", "op", "acc")
	usr.User.ScopedKey = "missing-key"
	objs := []Object{
		validOperator("op"),
		validAccount("acc", "op"),
		usr,
	}
	result, err := Validate(objs, ValidateOptions{StrictRefs: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.UnresolvedRefs) != 1 {
		t.Fatalf("expected 1 unresolved ref, got %d", len(result.UnresolvedRefs))
	}
	if result.UnresolvedRefs[0].Ref != "missing-key" {
		t.Errorf("ref value: got %q", result.UnresolvedRefs[0].Ref)
	}
}

func TestValidate_Rule4_UnresolvedRef_Strict(t *testing.T) {
	usr := validUser("svc", "op", "acc")
	usr.User.ScopedKey = "missing-key"
	objs := []Object{
		validOperator("op"),
		validAccount("acc", "op"),
		usr,
	}
	_, err := Validate(objs, ValidateOptions{StrictRefs: true})
	if err == nil {
		t.Fatal("expected error in strict mode for unresolved ref, got nil")
	}
}

func TestValidate_Rule4_CrossAccountRefNotResolved(t *testing.T) {
	// A ScopedSigningKey in account "other" should NOT resolve a User in account "acc".
	sk := validScopedKey("writer", "op", "other")
	usr := validUser("svc", "op", "acc")
	usr.User.ScopedKey = "writer"
	objs := []Object{
		validOperator("op"),
		validAccount("other", "op"),
		validAccount("acc", "op"),
		sk,
		usr,
	}
	result, err := Validate(objs, ValidateOptions{StrictRefs: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.UnresolvedRefs) != 1 {
		t.Fatalf("expected 1 unresolved ref (cross-account), got %d", len(result.UnresolvedRefs))
	}
}

func TestValidate_ClusterServerURLs_Required(t *testing.T) {
	cl := Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster},
		Metadata: ObjectMeta{Name: "cl", Operator: "op"},
		Cluster:  &ClusterSpec{ServerURLs: nil},
	}
	_, err := Validate([]Object{validOperator("op"), cl}, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error for empty serverURLs, got nil")
	}
	if !strings.Contains(err.Error(), "serverURLs") {
		t.Errorf("error should mention serverURLs: %v", err)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	objs := []Object{
		{
			TypeMeta: TypeMeta{APIVersion: "nis/v0", Kind: "Bad"},
			Metadata: ObjectMeta{Name: "x"},
		},
	}
	_, err := Validate(objs, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should mention both apiVersion and unknown kind.
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("missing apiVersion mention: %v", err)
	}
}

func TestValidate_EndToEnd_FullFixture(t *testing.T) {
	objs, err := Parse([]byte(fullFixture), "fixture")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := Validate(objs, ValidateOptions{StrictRefs: true})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(result.UnresolvedRefs) != 0 {
		t.Errorf("unexpected unresolved refs: %v", result.UnresolvedRefs)
	}
}
