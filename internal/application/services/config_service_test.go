package services

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// Each subtest re-initialises viper so prior subtests don't leak state. We
// can't t.Parallel() because viper is a process-global singleton.

func TestRedactValue_ExactPaths(t *testing.T) {
	for _, path := range []string{
		"auth.jwt_secret",
		"encryption.key",
		"backups.s3.access_key_id",
		"backups.s3.secret_access_key",
	} {
		got := redactValue(path, "real-secret-value")
		if got != redactionPlaceholder {
			t.Fatalf("path %q: expected redaction, got %v", path, got)
		}
	}
}

func TestRedactDSNPassword(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		mustNot string // substring that MUST be gone after redaction
	}{
		{
			name:    "libpq unquoted",
			in:      "host=localhost port=5432 user=nis password=secret123 dbname=nis sslmode=disable",
			want:    "host=localhost port=5432 user=nis password=***REDACTED*** dbname=nis sslmode=disable",
			mustNot: "secret123",
		},
		{
			name:    "libpq quoted with space",
			in:      "host=localhost user=nis password='hunter 2' dbname=nis",
			want:    "host=localhost user=nis password=***REDACTED*** dbname=nis",
			mustNot: "hunter 2",
		},
		{
			name:    "URI form",
			in:      "postgres://nis:supersecret@db.internal:5432/nis?sslmode=require",
			want:    "postgres://nis:***REDACTED***@db.internal:5432/nis?sslmode=require",
			mustNot: "supersecret",
		},
		{
			name:    "postgresql:// alt scheme",
			in:      "postgresql://nis:supersecret@db.internal/nis",
			want:    "postgresql://nis:***REDACTED***@db.internal/nis",
			mustNot: "supersecret",
		},
		{
			name: "sqlite path unchanged",
			in:   "./nis.db",
			want: "./nis.db",
		},
		{
			name: "sqlite absolute path unchanged",
			in:   "/var/lib/nis/nis.db",
			want: "/var/lib/nis/nis.db",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactDSNPassword(tc.in)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if tc.mustNot != "" && strings.Contains(got, tc.mustNot) {
				t.Fatalf("CRITICAL: password leaked in output %q (substring %q)", got, tc.mustNot)
			}
		})
	}
}

func TestRedactValue_DatabaseDSN(t *testing.T) {
	// Top-level integration: the redactValue dispatcher must route
	// database.dsn through the DSN-smart redactor, not the placeholder
	// fallback.
	in := "host=localhost user=nis password=secret123 dbname=nis"
	got := redactValue("database.dsn", in)
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if strings.Contains(gotStr, "secret123") {
		t.Fatalf("password leaked through redactValue dispatch: %q", gotStr)
	}
	if !strings.Contains(gotStr, "host=localhost") {
		t.Fatalf("non-secret DSN parts must remain visible: %q", gotStr)
	}
}

func TestRedactValue_NonSensitiveKeysPreserved(t *testing.T) {
	cases := []struct {
		path string
		in   any
	}{
		{"encryption.key_id", "default"},
		{"encryption.current_key_id", "key-2025-01"},
		{"server.address", ":8080"},
		{"auth.jwt_ttl", "24h"},
		{"tracing.endpoint", "localhost:4317"},
	}
	for _, tc := range cases {
		got := redactValue(tc.path, tc.in)
		if got == redactionPlaceholder {
			t.Fatalf("path %q: unexpected redaction, value should round-trip", tc.path)
		}
	}
}

func TestRedactValue_SuffixCatchAll(t *testing.T) {
	// Hypothetical future-added credentials with the convention suffixes.
	cases := []string{
		"backups.gcs.client_secret",
		"webhooks.outbound_password",
		"some.future.api_access_key",
	}
	for _, path := range cases {
		got := redactValue(path, "real-value")
		if got != redactionPlaceholder {
			t.Fatalf("path %q: expected suffix-based redaction, got %v", path, got)
		}
	}
}

func TestRedactValue_EncryptionKeysArray(t *testing.T) {
	// encryption.keys is a slice of map[string]any{"id": ..., "key": ...}.
	// The walker must descend the slice and redact each "key" while
	// leaving "id" intact.
	in := []any{
		map[string]any{"id": "key-2025-01", "key": "secret-bytes-1"},
		map[string]any{"id": "key-2024-12", "key": "secret-bytes-2"},
	}
	out := redactValue("encryption.keys", in).([]any)
	if len(out) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(out))
	}
	for i, elem := range out {
		m := elem.(map[string]any)
		if m["id"] == redactionPlaceholder {
			t.Fatalf("elem %d: id should not be redacted", i)
		}
		if m["key"] != redactionPlaceholder {
			t.Fatalf("elem %d: key must be redacted (got %v) — leaking encryption key material via the array element is the bug this test pins", i, m["key"])
		}
	}
}

func TestBuildRedactedTree_EnvVarVisible(t *testing.T) {
	viper.Reset()
	registerTestDefaults()

	// Simulate AUTH_JWT_SECRET set via env (the documented prod path).
	// Even though no SetDefault("auth.jwt_secret") was registered, the
	// extraKnownKeys list ensures the renderer picks it up.
	t.Setenv("AUTH_JWT_SECRET", "secret-from-env")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	tree := buildRedactedTree()
	authMap, ok := tree["auth"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'auth' map in tree, got %T", tree["auth"])
	}
	v, ok := authMap["jwt_secret"]
	if !ok {
		t.Fatalf("expected 'auth.jwt_secret' in tree (env-only credential must not vanish)")
	}
	if v != redactionPlaceholder {
		t.Fatalf("auth.jwt_secret must be redacted (got %q) — leaking the env-set credential is the bug this test pins", v)
	}
}

func TestBuildRedactedTree_MultiKeyHidesSingleKeyFields(t *testing.T) {
	viper.Reset()
	registerTestDefaults()
	// Simulate a prod config: multi-key mode populated, single-key empty.
	viper.Set("encryption.keys", []any{
		map[string]any{"id": "key-2025-01", "key": "secret-bytes"},
	})
	viper.Set("encryption.current_key_id", "key-2025-01")

	tree := buildRedactedTree()
	enc, ok := tree["encryption"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'encryption' map, got %T", tree["encryption"])
	}
	if _, present := enc["key"]; present {
		t.Fatalf("encryption.key must be omitted when encryption.keys is populated (it's ignored at runtime)")
	}
	if _, present := enc["key_id"]; present {
		t.Fatalf("encryption.key_id must be omitted when encryption.keys is populated")
	}
	if _, present := enc["keys"]; !present {
		t.Fatalf("encryption.keys must be present when populated")
	}
	if _, present := enc["current_key_id"]; !present {
		t.Fatalf("encryption.current_key_id must be present when keys is populated")
	}
}

func TestBuildRedactedTree_SingleKeyHidesMultiKeyFields(t *testing.T) {
	viper.Reset()
	registerTestDefaults()
	// Single-key mode (env var path).
	viper.Set("encryption.key", "secret-bytes")
	// current_key_id remains at its (unset) default — the test only
	// configures the single-key branch.

	tree := buildRedactedTree()
	enc, ok := tree["encryption"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'encryption' map, got %T", tree["encryption"])
	}
	if _, present := enc["keys"]; present {
		t.Fatalf("encryption.keys must be omitted in single-key mode")
	}
	if _, present := enc["current_key_id"]; present {
		t.Fatalf("encryption.current_key_id must be omitted in single-key mode (it's a multi-key field)")
	}
	if enc["key"] != redactionPlaceholder {
		t.Fatalf("encryption.key must be present and redacted, got %v", enc["key"])
	}
}

func TestGetRunningConfig_NonCredentialsRoundTrip(t *testing.T) {
	viper.Reset()
	registerTestDefaults()

	svc := NewConfigService()
	out, err := svc.GetRunningConfig(nil)
	if err != nil {
		t.Fatalf("GetRunningConfig: %v", err)
	}
	if !strings.Contains(out, "server:") {
		t.Fatalf("expected 'server:' in yaml output: %s", out)
	}
	if !strings.Contains(out, ":8080") {
		t.Fatalf("expected server.address ':8080' in yaml output: %s", out)
	}
}

// registerTestDefaults seeds the subset of defaults the running-config
// tests need. We don't call the real registerConfigDefaults to avoid an
// import cycle with cmd/nis/commands.
func registerTestDefaults() {
	viper.SetDefault("server.address", ":8080")
	viper.SetDefault("server.enable_ui", true)
	viper.SetDefault("database.driver", "sqlite")
	viper.SetDefault("database.dsn", "nis.db")
	viper.SetDefault("encryption.key_id", "default")
	viper.SetDefault("auth.jwt_ttl", "24h")
	viper.SetDefault("tracing.endpoint", "localhost:4317")
	viper.SetDefault("backups.s3.endpoint", "")
	viper.SetDefault("backups.s3.access_key_id", "")
	viper.SetDefault("backups.s3.secret_access_key", "")
}
