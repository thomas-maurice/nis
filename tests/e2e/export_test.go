//go:build e2e

// export_test.go — encoding shape and default-format checks for the export
// endpoint. These tests are deliberately cheap: each one builds a single
// operator (no NATS server, no cluster), exports, and asserts on the wire
// shape. The destructive round-trip lives in import_backup_test.go.
package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// exportFixture builds an operator + one account + one user. Just enough state
// so the export has non-trivial accounts/users sections — but no cluster
// (cluster delete is the FK gymnastic that the round-trip test covers).
func exportFixture(t *testing.T, h *harness) string {
	t.Helper()
	operatorID := h.createOperator(t, "export-operator")
	accountID := h.createAccount(t, operatorID, "export-account")
	h.createUser(t, accountID, "export-user")
	return operatorID
}

// TestE2E_Export_YAMLEncoding asserts the YAML encoder produces snake_case
// top-level keys + nested operator block fields. yaml.v3 defaults to
// lowercased Go field names without explicit `yaml:` struct tags — this test
// locks in the tag fix.
func TestE2E_Export_YAMLEncoding(t *testing.T) {
	h := startStack(t)
	operatorID := exportFixture(t, h)
	ctx := context.Background()

	resp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId: operatorID,
		Format:     "yaml",
	}))
	if err != nil {
		t.Fatalf("ExportOperator(yaml): %v", err)
	}
	if resp.Msg.Format != "yaml" {
		t.Fatalf("format echo: got %q, want yaml", resp.Msg.Format)
	}
	body := resp.Msg.Data
	if len(body) == 0 || body[0] == '{' || body[0] == '[' {
		t.Fatalf("yaml bytes look like JSON: %q", string(body[:min2(len(body), 80)]))
	}
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode yaml export: %v", err)
	}
	for _, key := range []string{"version", "exported_at", "operator", "accounts", "scoped_keys", "users"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("yaml export missing key %q. top-level keys: %v", key, mapKeys(doc))
		}
	}
	op, ok := doc["operator"].(map[string]any)
	if !ok {
		t.Fatalf("operator block missing or wrong type")
	}
	for _, key := range []string{"public_key", "system_account_pub_key", "encrypted_seed"} {
		if _, ok := op[key]; !ok {
			t.Fatalf("yaml operator block missing key %q. keys: %v", key, mapKeys(op))
		}
	}
}

// TestE2E_Export_JSONEncoding mirrors the YAML test for the JSON path.
func TestE2E_Export_JSONEncoding(t *testing.T) {
	h := startStack(t)
	operatorID := exportFixture(t, h)
	ctx := context.Background()

	resp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId: operatorID,
		Format:     "json",
	}))
	if err != nil {
		t.Fatalf("ExportOperator(json): %v", err)
	}
	if resp.Msg.Format != "json" {
		t.Fatalf("format echo: got %q, want json", resp.Msg.Format)
	}
	body := resp.Msg.Data
	if len(body) == 0 || body[0] != '{' {
		t.Fatalf("json bytes don't start with '{': %q", string(body[:min2(len(body), 80)]))
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode json export: %v", err)
	}
	for _, key := range []string{"version", "exported_at", "operator", "accounts", "scoped_keys", "users"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("json export missing key %q", key)
		}
	}
}

// TestE2E_Export_PlaintextSecretsShape verifies the wire shape of a plaintext
// export: per-row `seed` set, `encrypted:` storage refs absent, seed values
// carry the NKey prefix ("S"). The DR round-trip itself lives in
// import_backup_test.go.
func TestE2E_Export_PlaintextSecretsShape(t *testing.T) {
	h := startStack(t)
	operatorID := exportFixture(t, h)
	ctx := context.Background()

	resp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId:       operatorID,
		Format:           "yaml",
		PlaintextSecrets: true,
	}))
	if err != nil {
		t.Fatalf("ExportOperator(plaintext): %v", err)
	}
	body := string(resp.Msg.Data)
	if strings.Contains(body, "encrypted:") {
		t.Fatalf("plaintext export must not contain 'encrypted:' refs; body sample: %q",
			body[:min2(len(body), 400)])
	}
	if !strings.Contains(body, "seed: S") {
		t.Fatalf("plaintext export must contain at least one NKey seed (seed: S...); body sample: %q",
			body[:min2(len(body), 400)])
	}
}

// TestE2E_Export_DefaultsToJSONWhenFormatUnset locks in back-compat: clients
// that predate the format field must keep getting JSON. Flipping the default
// must be a deliberate choice that breaks this test.
func TestE2E_Export_DefaultsToJSONWhenFormatUnset(t *testing.T) {
	h := startStack(t)
	operatorID := exportFixture(t, h)
	ctx := context.Background()

	resp, err := h.exportCli.ExportOperator(ctx, connect.NewRequest(&nisv1.ExportOperatorRequest{
		OperatorId: operatorID,
		// Format intentionally unset.
	}))
	if err != nil {
		t.Fatalf("ExportOperator(default): %v", err)
	}
	if resp.Msg.Format != "json" {
		t.Fatalf("default format should be json; got %q", resp.Msg.Format)
	}
	if len(resp.Msg.Data) == 0 || resp.Msg.Data[0] != '{' {
		t.Fatalf("default-format bytes don't look like JSON")
	}
}
