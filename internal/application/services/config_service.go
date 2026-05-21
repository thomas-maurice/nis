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
	"sort"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// redactionPlaceholder is the literal we substitute for any redacted leaf
// value. Empty-string credentials (unset env) still render as the
// placeholder so an admin sees the key exists and is unconfigured at the
// same place a configured-but-redacted key would appear.
const redactionPlaceholder = "***REDACTED***"

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

// redactedSuffixes is the safety net for credentials added later. Any leaf
// whose final dotted segment ends in one of these suffixes is redacted.
// "_key" is intentionally NOT here because legitimate non-secret config
// uses it (encryption.current_key_id, encryption.key_id).
var redactedSuffixes = []string{
	"_secret",
	"_password",
	"_access_key",
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
func buildRedactedTree() map[string]any {
	keys := dedupSortedKeys(append(viper.AllKeys(), extraKnownKeys...))
	root := map[string]any{}
	for _, key := range keys {
		val := viper.Get(key)
		val = redactValue(key, val)
		insertNested(root, strings.Split(key, "."), val)
	}
	return root
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

func lastSegment(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func leafMatchesSuffix(leaf string) bool {
	for _, suf := range redactedSuffixes {
		if strings.HasSuffix(leaf, suf) {
			return true
		}
	}
	return false
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
