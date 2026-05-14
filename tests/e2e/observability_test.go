//go:build e2e

// observability_test.go — probe endpoints + Prometheus scrape. Lives in its
// own file so a missing /metrics body or a flapping /readyz fails fast and
// loud, without dragging a docker container along for the ride.
package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestE2E_Observability_ProbeAndMetricsEndpoints verifies /livez, /healthz,
// /readyz, /metrics all return 200, and that /metrics has the rpc and gauge
// series after the suite has driven RPC traffic (creating an operator does
// the trick — that's an RPC).
func TestE2E_Observability_ProbeAndMetricsEndpoints(t *testing.T) {
	h := startStack(t)
	// One RPC so the meter provider has something to emit.
	_ = h.createOperator(t, "obs-operator")

	check := func(path string) {
		resp, err := http.Get(h.serverURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
	}
	check("/livez")
	check("/healthz")
	check("/readyz")
	check("/metrics")

	resp, err := http.Get(h.serverURL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	bodyStr := string(body)
	n := len(bodyStr)
	if n > 400 {
		n = 400
	}
	// otelconnect exports rpc.server.duration as rpc_server_duration_*; the
	// nis_operators_total gauge is wired through the domain-gauge refresher.
	// Both being present proves the meter provider + Prometheus registry are
	// wired and the background gauge loop has run at least once.
	if !strings.Contains(bodyStr, "rpc_server_duration") {
		t.Fatalf("expected /metrics to contain rpc_server_duration_* series after RPC traffic. body sample: %q", bodyStr[:n])
	}
	if !strings.Contains(bodyStr, "nis_operators_total") {
		t.Fatalf("expected /metrics to contain nis_operators_total gauge. body sample: %q", bodyStr[:n])
	}
}
