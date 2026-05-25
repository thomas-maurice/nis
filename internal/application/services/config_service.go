package services

// Running-config inspection (admin-only).
//
// ConfigService renders the effective viper configuration as YAML with
// credential values redacted. It is the read-only inspection surface that
// backs the UI "Runtime config" page; there is intentionally no Set/Reload
// RPC — config changes go through the file + env + flag path on restart.
//
// Why we don't just call viper.AllSettings + yaml.Marshal:
//
//   1. viper.AllSettings() does NOT include keys that were set ONLY via
//      env vars. AutomaticEnv resolves env values lazily on viper.Get(key),
//      so a key with no SetDefault / no config-file presence is invisible
//      to AllSettings even when the env var is set. For a credential like
//      AUTH_JWT_SECRET (no default registered — boot fails if empty), that
//      means the UI page would silently MISS it rather than show a
//      redacted entry. We resolve that by iterating viper.AllKeys() (the
//      union of registered defaults + config-file + flags + Set) PLUS a
//      curated list of well-known sensitive keys that don't have defaults
//      (auth.jwt_secret, encryption.key), then calling viper.Get for each
//      so env values land in the output.
//
//   2. Some structured values (encryption.keys array, backups.s3 block)
//      need per-element redaction — yaml.Marshal of the raw map would
//      print the secret_access_key cleartext. The renderer rebuilds a
//      nested map[string]any and substitutes "***REDACTED***" at known
//      sensitive paths before marshalling.
//
// Redaction rules (exact-path + suffix-based):
//
//   - Exact paths: "auth.jwt_secret", "encryption.key",
//     "backups.s3.access_key_id", "backups.s3.secret_access_key".
//   - Inside encryption.keys[]: each element's "key" field (NOT "id").
//   - Suffix-based catch-all: any leaf path whose final segment ends in
//     "_secret", "_password", or "_access_key" is redacted regardless of
//     where it sits. New credentials added via env vars are caught
//     automatically as long as their names follow the convention.
//
// Adding a new credential? Either name it with one of the suffixes above,
// or add an explicit exact-path entry in redactedPaths.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/thomas-maurice/nis/internal/application/redact"
)

// redactionPlaceholder is the literal we substitute for any redacted leaf
// value. Empty-string credentials (unset env) still render as the
// placeholder so an admin sees the key exists and is unconfigured at the
// same place a configured-but-redacted key would appear.
const redactionPlaceholder = redact.Placeholder

// redactedPaths is the exact-path allowlist. Paths use viper dot notation.
// Wildcards aren't supported — encryption.keys array indices are handled
// inline (see redactValue). Suffix-based catch-alls below cover the rest.
var redactedPaths = map[string]bool{
	"auth.jwt_secret":              true,
	"encryption.key":               true,
	"encryption.keys.key":          true, // walker keeps "encryption.keys" stable across []any descent, then appends ".key" on the map elem
	"backups.s3.access_key_id":     true,
	"backups.s3.secret_access_key": true,
}

// extraKnownKeys is the list of viper keys that don't have a SetDefault
// registered but ARE sensitive — without these, an env-only AUTH_JWT_SECRET
// would be invisible to AllKeys and silently dropped from the UI page.
// Keep this list in lockstep with cmd/nis/commands/viper_overrides.go: any
// new sensitive key without a default belongs here.
var extraKnownKeys = []string{
	"auth.jwt_secret",
	"encryption.key",
}

// ConfigService is the read-only running-config surface.
type ConfigService struct{}

// NewConfigService returns a ConfigService. Stateless — viper is the
// global source of truth for the process; no constructor args.
func NewConfigService() *ConfigService {
	return &ConfigService{}
}

// GetRunningConfig returns the effective config as redacted YAML.
// ctx is accepted for symmetry with other services but is not used (no
// I/O is performed); cancellation has nothing to interrupt.
func (s *ConfigService) GetRunningConfig(_ context.Context) (string, error) {
	root := buildRedactedTree()
	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("marshal config to yaml: %w", err)
	}
	return string(out), nil
}

// buildRedactedTree assembles the union of registered-default keys, file/
// flag keys (via viper.AllKeys), and the curated extraKnownKeys list,
// then inserts each (key, redacted-value) pair into a nested map suitable
// for yaml.Marshal.
//
// Active-form-only filtering: `encryption.key` + `encryption.key_id`
// (single-key mode) and `encryption.keys` + `encryption.current_key_id`
// (multi-key mode) are mutually exclusive — `initEncryptionService`
// picks one and ignores the other. Showing both confuses admins about
// which one actually mints the encryptor. The filter drops the inactive
// form based on which fields carry non-empty values.
func buildRedactedTree() map[string]any {
	skip := inactiveEncryptionKeys()
	keys := dedupSortedKeys(append(viper.AllKeys(), extraKnownKeys...))
	root := map[string]any{}
	for _, key := range keys {
		if skip[key] {
			continue
		}
		val := viper.Get(key)
		val = redactValue(key, val)
		insertNested(root, strings.Split(key, "."), val)
	}
	return root
}

// inactiveEncryptionKeys returns the viper paths to omit from the output
// because the OTHER encryption-config form is the live one. Mirrors the
// branching in initEncryptionService: if encryption.keys has any entries
// the single-key fields are ignored; otherwise the multi-key fields are
// the inactive set. When neither has been configured (default state), we
// suppress nothing — the admin needs to see SOMETHING is configured
// (or, more usefully, that the binary would fail to boot).
func inactiveEncryptionKeys() map[string]bool {
	skip := map[string]bool{}
	multiKey, _ := viper.Get("encryption.keys").([]any)
	hasMultiKey := len(multiKey) > 0
	hasSingleKey := strings.TrimSpace(viper.GetString("encryption.key")) != ""

	switch {
	case hasMultiKey:
		// Multi-key form wins.
		skip["encryption.key"] = true
		skip["encryption.key_id"] = true
	case hasSingleKey:
		// Single-key form wins. Drop multi-key fields; current_key_id is
		// the multi-key counterpart of key_id and irrelevant here.
		skip["encryption.keys"] = true
		skip["encryption.current_key_id"] = true
	}
	return skip
}

// dedupSortedKeys is the union helper for the key-source merge above. Sort
// is required so the YAML output is deterministic between runs (otherwise
// admin diffs become noisy).
func dedupSortedKeys(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, k := range in {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// redactValue applies the redaction rules to one (path, value) pair.
// For non-leaf values (slices/maps) it recurses so encryption.keys[].key
// gets caught even though we don't know the array length at registration
// time.
func redactValue(path string, val any) any {
	// database.dsn carries a Postgres libpq password (or URI password)
	// inline. Whole-value redaction would hide driver/host/db too — useful
	// info for an admin. Smart-redact ONLY the password component so the
	// rest of the DSN stays inspectable. SQLite DSNs are filesystem paths
	// with neither pattern, so the smart redactor passes them through.
	if path == "database.dsn" {
		if s, ok := val.(string); ok {
			return redactDSNPassword(s)
		}
		return val
	}

	if redactedPaths[path] {
		return redactionPlaceholder
	}
	if leaf := lastSegment(path); leafMatchesSuffix(leaf) {
		return redactionPlaceholder
	}

	switch v := val.(type) {
	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			out[i] = redactValue(path, elem)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, elem := range v {
			childPath := path + "." + k
			out[k] = redactValue(childPath, elem)
		}
		return out
	case map[any]any:
		// yaml.v3 produces map[any]any when keys aren't strings. viper
		// usually normalizes to string-keyed maps, but we still handle
		// this defensively so a future config file with mixed keys
		// doesn't crash the renderer.
		out := make(map[string]any, len(v))
		for k, elem := range v {
			keyStr := fmt.Sprintf("%v", k)
			childPath := path + "." + keyStr
			out[keyStr] = redactValue(childPath, elem)
		}
		return out
	default:
		return val
	}
}

// dsnPasswordRegexes redact the password component of a database DSN while
// leaving the rest of the value visible. Three forms are covered:
//
//   1. libpq key=value, unquoted:  ...password=secret123 dbname=...
//   2. libpq key=value, quoted:    ...password='hunter 2' dbname=...
//   3. URI form:                   postgres://user:secret@host:5432/db
//
// SQLite DSNs are filesystem paths (./nis.db) — none of the patterns match,
// so the value passes through unchanged.
var dsnPasswordRegexes = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Quoted libpq form FIRST: the unquoted regex would otherwise consume
	// the opening quote and trailing chars greedily.
	{regexp.MustCompile(`(?i)\bpassword='[^']*'`), "password=***REDACTED***"},
	{regexp.MustCompile(`(?i)\bpassword=\S+`), "password=***REDACTED***"},
	// URI form: scheme://user:pw@host. The capturing group keeps user
	// and "@" visible so the admin can still see who's connecting.
	{regexp.MustCompile(`(://[^:/?@\s]+:)[^@\s]+(@)`), "${1}***REDACTED***${2}"},
}

func redactDSNPassword(dsn string) string {
	out := dsn
	for _, r := range dsnPasswordRegexes {
		out = r.re.ReplaceAllString(out, r.repl)
	}
	return out
}

func lastSegment(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func leafMatchesSuffix(leaf string) bool {
	return redact.HasSensitiveSuffix(leaf)
}

// insertNested writes value at the nested path inside root, creating
// intermediate maps as needed. Overwrites existing leaves silently — viper
// guarantees unique keys via AllKeys, and the extraKnownKeys union is
// idempotent because the same key resolves to the same value.
func insertNested(root map[string]any, path []string, value any) {
	if len(path) == 0 {
		return
	}
	cur := root
	for i, segment := range path {
		if i == len(path)-1 {
			cur[segment] = value
			return
		}
		child, ok := cur[segment].(map[string]any)
		if !ok {
			child = map[string]any{}
			cur[segment] = child
		}
		cur = child
	}
}
