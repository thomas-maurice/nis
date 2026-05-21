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
