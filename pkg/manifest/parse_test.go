package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fullFixture = `
apiVersion: nis/v1
kind: Operator
metadata:
  name: acme
spec:
  description: ACME prod
  jwtPolicy:
    userJWTTTL: 90d
    accountJWTTTL: 1y
    warnWindow: 14d
    autoRenew: false
---
apiVersion: nis/v1
kind: Cluster
metadata:
  name: prod
  operator: acme
spec:
  description: prod NATS
  serverURLs:
    - nats://nats-1:4222
    - nats://nats-2:4222
---
apiVersion: nis/v1
kind: Account
metadata:
  name: payments
  operator: acme
spec:
  description: Payments service account
  jetStream:
    enabled: true
    maxMemory: 1Gi
    maxStorage: 10Gi
    maxStreams: 100
    maxConsumers: 1000
---
apiVersion: nis/v1
kind: ScopedSigningKey
metadata:
  name: writer
  operator: acme
  account: payments
spec:
  description: pub/sub on payments.>
  pubAllow: ["payments.>"]
  pubDeny: []
  subAllow: ["payments.>"]
  subDeny: []
  responseMaxMsgs: 0
  responseTTL: 0s
---
apiVersion: nis/v1
kind: User
metadata:
  name: payments-api
  operator: acme
  account: payments
spec:
  description: payments API service user
  scopedKey: writer
  jwtTTL: 30d
`

func TestParse_FullFixture(t *testing.T) {
	objects, err := Parse([]byte(fullFixture), "fixture")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(objects) != 5 {
		t.Fatalf("expected 5 objects, got %d", len(objects))
	}

	// Operator
	op := objects[0]
	if op.Kind != KindOperator {
		t.Errorf("objects[0].Kind: got %q, want %q", op.Kind, KindOperator)
	}
	if op.Metadata.Name != "acme" {
		t.Errorf("operator name: got %q", op.Metadata.Name)
	}
	if op.Operator == nil {
		t.Fatal("op.Operator is nil")
	}
	if op.Operator.JWTPolicy == nil {
		t.Fatal("op.Operator.JWTPolicy is nil")
	}
	if op.Operator.JWTPolicy.UserJWTTTL != 90*24*time.Hour {
		t.Errorf("UserJWTTTL: got %v", op.Operator.JWTPolicy.UserJWTTTL)
	}
	if op.Operator.JWTPolicy.AccountJWTTTL != 365*24*time.Hour {
		t.Errorf("AccountJWTTTL: got %v", op.Operator.JWTPolicy.AccountJWTTTL)
	}
	if op.Operator.JWTPolicy.AutoRenew {
		t.Error("AutoRenew should be false")
	}

	// Cluster
	cl := objects[1]
	if cl.Kind != KindCluster {
		t.Errorf("objects[1].Kind: got %q", cl.Kind)
	}
	if cl.Cluster == nil || len(cl.Cluster.ServerURLs) != 2 {
		t.Errorf("cluster serverURLs: got %v", cl.Cluster)
	}

	// Account
	ac := objects[2]
	if ac.Kind != KindAccount {
		t.Errorf("objects[2].Kind: got %q", ac.Kind)
	}
	if ac.Account == nil || ac.Account.JetStream == nil {
		t.Fatal("account.JetStream is nil")
	}
	if !ac.Account.JetStream.Enabled {
		t.Error("jetStream.enabled: expected true")
	}
	if ac.Account.JetStream.MaxMemory != 1<<30 {
		t.Errorf("maxMemory: got %d", ac.Account.JetStream.MaxMemory)
	}
	if ac.Account.JetStream.MaxStorage != 10*(1<<30) {
		t.Errorf("maxStorage: got %d", ac.Account.JetStream.MaxStorage)
	}

	// ScopedSigningKey
	sk := objects[3]
	if sk.Kind != KindScopedSigningKey {
		t.Errorf("objects[3].Kind: got %q", sk.Kind)
	}
	if sk.ScopedSigningKey == nil {
		t.Fatal("sk.ScopedSigningKey is nil")
	}
	if len(sk.ScopedSigningKey.PubAllow) != 1 || sk.ScopedSigningKey.PubAllow[0] != "payments.>" {
		t.Errorf("pubAllow: got %v", sk.ScopedSigningKey.PubAllow)
	}

	// User
	usr := objects[4]
	if usr.Kind != KindUser {
		t.Errorf("objects[4].Kind: got %q", usr.Kind)
	}
	if usr.User == nil {
		t.Fatal("usr.User is nil")
	}
	if usr.User.ScopedKey != "writer" {
		t.Errorf("scopedKey: got %q", usr.User.ScopedKey)
	}
	if usr.User.JWTTTL == nil || *usr.User.JWTTTL != 30*24*time.Hour {
		t.Errorf("jwtTTL: got %v", usr.User.JWTTTL)
	}
}

func TestParse_SourceTracking(t *testing.T) {
	objs, err := Parse([]byte(fullFixture), "myfile.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for i, obj := range objs {
		if obj.SourceFile != "myfile.yaml" {
			t.Errorf("objects[%d].SourceFile: got %q", i, obj.SourceFile)
		}
		if obj.DocIndex != i {
			t.Errorf("objects[%d].DocIndex: got %d", i, obj.DocIndex)
		}
	}
}

func TestParse_MalformedYAML(t *testing.T) {
	_, err := Parse([]byte(":\tbadtabs\n"), "bad.yaml")
	if err == nil {
		t.Error("expected error for malformed YAML, got nil")
	}
}

func TestParse_EmptyInput(t *testing.T) {
	objs, err := Parse([]byte(""), "empty.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 0 {
		t.Errorf("expected 0 objects, got %d", len(objs))
	}
}

func TestLoad_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(fullFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	objs, err := Load([]string{path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(objs) != 5 {
		t.Errorf("expected 5 objects, got %d", len(objs))
	}
}

func TestLoad_Directory(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0o750); err != nil {
		t.Fatal(err)
	}

	op := `apiVersion: nis/v1
kind: Operator
metadata:
  name: op1
spec:
  description: op1
`
	cl := `apiVersion: nis/v1
kind: Cluster
metadata:
  name: cl1
  operator: op1
spec:
  description: cl1
  serverURLs: [nats://localhost:4222]
`
	if err := os.WriteFile(filepath.Join(dir, "op.yaml"), []byte(op), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "cl.yml"), []byte(cl), 0o600); err != nil {
		t.Fatal(err)
	}
	// a non-yaml file that should be ignored
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	objs, err := Load([]string{dir})
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	if len(objs) != 2 {
		t.Errorf("expected 2 objects, got %d: %v", len(objs), objs)
	}
}

func TestLoad_Stdin(t *testing.T) {
	// Replace stdin with a pipe for the duration of this test.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		_ = r.Close()
	})

	go func() {
		doc := `apiVersion: nis/v1
kind: Operator
metadata:
  name: stdin-op
spec:
  description: from stdin
`
		_, _ = w.WriteString(doc)
		_ = w.Close()
	}()

	objs, err := Load([]string{"-"})
	if err != nil {
		t.Fatalf("Load stdin: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	if objs[0].Metadata.Name != "stdin-op" {
		t.Errorf("name: got %q", objs[0].Metadata.Name)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load([]string{"/no/such/file.yaml"})
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoad_MalformedYAMLFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte(":\tbad"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load([]string{bad})
	if err == nil {
		t.Error("expected error for malformed file, got nil")
	}
	if !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("error should mention filename, got: %v", err)
	}
}

func TestParse_NilJWTPolicy(t *testing.T) {
	doc := `apiVersion: nis/v1
kind: Operator
metadata:
  name: bare-op
spec:
  description: no policy
`
	objs, err := Parse([]byte(doc), "test")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if objs[0].Operator.JWTPolicy != nil {
		t.Error("JWTPolicy should be nil when omitted")
	}
}

func TestParse_NilJetStream(t *testing.T) {
	doc := `apiVersion: nis/v1
kind: Account
metadata:
  name: no-js
  operator: op
spec:
  description: no jetstream
`
	objs, err := Parse([]byte(doc), "test")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if objs[0].Account.JetStream != nil {
		t.Error("JetStream should be nil when omitted")
	}
}
