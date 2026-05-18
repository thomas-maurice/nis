package manifest

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestJWTPolicySpec_RoundTrip(t *testing.T) {
	input := `
userJWTTTL: 90d
accountJWTTTL: 1y
warnWindow: 14d
autoRenew: true
`
	var spec JWTPolicySpec
	if err := yaml.Unmarshal([]byte(input), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.UserJWTTTL != 90*24*time.Hour {
		t.Errorf("UserJWTTTL: got %v, want %v", spec.UserJWTTTL, 90*24*time.Hour)
	}
	if spec.AccountJWTTTL != 365*24*time.Hour {
		t.Errorf("AccountJWTTTL: got %v, want %v", spec.AccountJWTTTL, 365*24*time.Hour)
	}
	if spec.WarnWindow != 14*24*time.Hour {
		t.Errorf("WarnWindow: got %v, want %v", spec.WarnWindow, 14*24*time.Hour)
	}
	if !spec.AutoRenew {
		t.Error("AutoRenew: got false, want true")
	}

	// Round-trip: marshal back and re-parse.
	out, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var spec2 JWTPolicySpec
	if err := yaml.Unmarshal(out, &spec2); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if spec2.UserJWTTTL != spec.UserJWTTTL {
		t.Errorf("round-trip UserJWTTTL: got %v, want %v", spec2.UserJWTTTL, spec.UserJWTTTL)
	}
}

func TestJetStreamSpec_RoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantMem   int64
		wantStore int64
	}{
		{"gi_suffix", "enabled: true\nmaxMemory: 1Gi\nmaxStorage: 10Gi\nmaxStreams: 100\nmaxConsumers: 1000\n", 1 << 30, 10 * (1 << 30)},
		{"mi_suffix", "enabled: true\nmaxMemory: 512Mi\nmaxStorage: 1024Mi\nmaxStreams: 0\nmaxConsumers: 0\n", 512 << 20, 1024 << 20},
		{"bare_int", "enabled: true\nmaxMemory: \"1048576\"\nmaxStorage: \"2097152\"\nmaxStreams: 0\nmaxConsumers: 0\n", 1 << 20, 2 << 20},
		{"ki_suffix", "enabled: true\nmaxMemory: 1Ki\nmaxStorage: 2Ki\nmaxStreams: 0\nmaxConsumers: 0\n", 1 << 10, 2 << 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var spec JetStreamSpec
			if err := yaml.Unmarshal([]byte(tc.yaml), &spec); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if spec.MaxMemory != tc.wantMem {
				t.Errorf("MaxMemory: got %d, want %d", spec.MaxMemory, tc.wantMem)
			}
			if spec.MaxStorage != tc.wantStore {
				t.Errorf("MaxStorage: got %d, want %d", spec.MaxStorage, tc.wantStore)
			}
			// Marshal and re-parse.
			out, err := yaml.Marshal(spec)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var spec2 JetStreamSpec
			if err := yaml.Unmarshal(out, &spec2); err != nil {
				t.Fatalf("unmarshal round-trip: %v", err)
			}
			if spec2.MaxMemory != tc.wantMem {
				t.Errorf("round-trip MaxMemory: got %d, want %d", spec2.MaxMemory, tc.wantMem)
			}
		})
	}
}

func TestJetStreamSpec_RejectDecimalSuffix(t *testing.T) {
	cases := []string{"1K", "1M", "1G", "1T"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			input := "enabled: true\nmaxMemory: " + s + "\nmaxStorage: 0\nmaxStreams: 0\nmaxConsumers: 0\n"
			var spec JetStreamSpec
			if err := yaml.Unmarshal([]byte(input), &spec); err == nil {
				t.Errorf("expected error for decimal suffix %q, got none", s)
			}
		})
	}
}

func TestUserSpec_RoundTrip(t *testing.T) {
	input := `
description: svc user
scopedKey: writer
jwtTTL: 30d
`
	var spec UserSpec
	if err := yaml.Unmarshal([]byte(input), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.ScopedKey != "writer" {
		t.Errorf("ScopedKey: got %q, want %q", spec.ScopedKey, "writer")
	}
	if spec.JWTTTL == nil {
		t.Fatal("JWTTTL: got nil, want non-nil")
	}
	if *spec.JWTTTL != 30*24*time.Hour {
		t.Errorf("JWTTTL: got %v, want %v", *spec.JWTTTL, 30*24*time.Hour)
	}

	out, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var spec2 UserSpec
	if err := yaml.Unmarshal(out, &spec2); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if spec2.JWTTTL == nil || *spec2.JWTTTL != *spec.JWTTTL {
		t.Errorf("round-trip JWTTTL mismatch")
	}
}

func TestUserSpec_NilJWTTTL(t *testing.T) {
	var spec UserSpec
	if err := yaml.Unmarshal([]byte("description: bare\n"), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.JWTTTL != nil {
		t.Errorf("JWTTTL: expected nil, got %v", spec.JWTTTL)
	}
}

func TestScopedSigningKeySpec_ResponseTTL(t *testing.T) {
	input := `
pubAllow: ["payments.>"]
pubDeny: []
subAllow: ["payments.>"]
subDeny: []
responseMaxMsgs: 0
responseTTL: 5m
`
	var spec ScopedSigningKeySpec
	if err := yaml.Unmarshal([]byte(input), &spec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if spec.ResponseTTL != 5*time.Minute {
		t.Errorf("ResponseTTL: got %v, want 5m", spec.ResponseTTL)
	}
}

func TestScopedSigningKeySpec_ZeroResponseTTL(t *testing.T) {
	for _, ttl := range []string{"0s", "0", ""} {
		t.Run("ttl="+ttl, func(t *testing.T) {
			input := "responseTTL: " + ttl + "\npubAllow: []\npubDeny: []\nsubAllow: []\nsubDeny: []\nresponseMaxMsgs: 0\n"
			if ttl == "" {
				input = "pubAllow: []\npubDeny: []\nsubAllow: []\nsubDeny: []\nresponseMaxMsgs: 0\n"
			}
			var spec ScopedSigningKeySpec
			if err := yaml.Unmarshal([]byte(input), &spec); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if spec.ResponseTTL != 0 {
				t.Errorf("ResponseTTL: expected 0, got %v", spec.ResponseTTL)
			}
		})
	}
}
