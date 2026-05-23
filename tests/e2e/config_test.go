//go:build e2e

// config_test.go — running-config inspection RPC. Admin-only, redacts
// credentials by exact path + suffix rule. The critical pin: an
// env-only credential (AUTH_JWT_SECRET, the documented prod path)
// MUST appear in the YAML and MUST be redacted, not absent. Viper's
// AllSettings() is blind to env-only keys; the service works around
// that with an extraKnownKeys list — this test fails if a future
// refactor regresses that workaround.

package e2e

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
)

func TestE2E_RunningConfig_AdminSeesRedactedYAML(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	resp, err := h.configCli.GetRunningConfig(ctx, connect.NewRequest(&nisv1.GetRunningConfigRequest{}))
	if err != nil {
		t.Fatalf("GetRunningConfig: %v", err)
	}
	yaml := resp.Msg.GetYaml()
	if yaml == "" {
		t.Fatal("expected non-empty yaml output")
	}

	// Top-level structure sanity: the well-known sections should be present.
	for _, expected := range []string{
		"server:",
		"database:",
		"auth:",
		"jobs:",
		"events:",
		"cluster:",
	} {
		if !strings.Contains(yaml, expected) {
			t.Fatalf("expected %q in yaml output, full output:\n%s", expected, yaml)
		}
	}

	// The actual jwt_secret value passed via env MUST be redacted, not present.
	// jwtSecret is the harness's literal — anything containing it indicates a leak.
	if strings.Contains(yaml, jwtSecret) {
		t.Fatalf("CRITICAL: jwt_secret value leaked in running-config output")
	}

	// auth.jwt_secret comes from AUTH_JWT_SECRET (env-only — no SetDefault).
	// It MUST appear in the output (visible-and-redacted), NOT silently absent.
	// This is the env-var blind-spot regression check.
	// YAML quotes "***REDACTED***" because '*' is a YAML alias indicator;
	// match either form to be robust against marshalling style changes.
	if !containsRedactedKey(yaml, "jwt_secret") {
		t.Fatalf("expected redacted 'jwt_secret' in yaml (env-set credential must be visible-and-redacted, not absent). full output:\n%s", yaml)
	}

	// encryption.key (single-key mode) is also env-only and MUST be redacted.
	if !containsRedactedKey(yaml, "key") {
		t.Fatalf("expected redacted encryption 'key' in yaml output:\n%s", yaml)
	}
}

// containsRedactedKey returns true if yaml contains any of the canonical
// "<keyName>: ***REDACTED***" forms (quoted or unquoted). yaml.v3 quotes
// "***REDACTED***" because '*' is an alias indicator — match either.
func containsRedactedKey(yaml, keyName string) bool {
	candidates := []string{
		keyName + ": ***REDACTED***",
		keyName + ": '***REDACTED***'",
		keyName + ": \"***REDACTED***\"",
	}
	for _, c := range candidates {
		if strings.Contains(yaml, c) {
			return true
		}
	}
	return false
}

func TestE2E_RunningConfig_NonAdminForbidden(t *testing.T) {
	h := startStack(t)
	ctx := context.Background()

	// Create an operator + operator-admin scoped to it.
	opID := h.createOperator(t, "config-rbac-op")
	const opAdminUser = "config-op-admin"
	const opAdminPass = "config-op-admin-password"
	if _, err := h.loginAs(t, adminUsername, adminPassword).authCli.CreateAPIUser(ctx, connect.NewRequest(&nisv1.CreateAPIUserRequest{
		Username:    opAdminUser,
		Password:    opAdminPass,
		Permissions: []string{"operator-admin"},
		OperatorId:  &opID,
	})); err != nil {
		t.Fatalf("create operator-admin: %v", err)
	}

	// operator-admin must NOT be able to read running config — admin-only
	// per the authz registry's RolePolicy.
	opAdminClients := h.loginAs(t, opAdminUser, opAdminPass)
	_, err := opAdminClients.configCli.GetRunningConfig(ctx, connect.NewRequest(&nisv1.GetRunningConfigRequest{}))
	if err == nil {
		t.Fatal("expected GetRunningConfig to be denied for operator-admin")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied for operator-admin, got %v: %v", got, err)
	}
}
