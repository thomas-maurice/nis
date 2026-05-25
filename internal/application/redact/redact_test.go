package redact

import "testing"

func TestHasSensitiveSuffix(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"jwt_secret", true},
		{"secret_access_key", true},
		{"aws_access_key", true},
		{"user_password", true},
		// Bare "access_key" without the leading underscore does NOT match — the
		// suffix is "_access_key", and config_service.go relies on this so the
		// dotted-path "backups.s3.access_key_id" is caught by the explicit
		// allowlist entry, not by suffix.
		{"access_key", false},
		{"public_key", false},
		{"encryption.current_key_id", false},
		{"encryption.key_id", false},
		{"name", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := HasSensitiveSuffix(tt.name); got != tt.want {
			t.Errorf("HasSensitiveSuffix(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSuffixes_Copy(t *testing.T) {
	a := Suffixes()
	a[0] = "_mutated"
	b := Suffixes()
	if b[0] == "_mutated" {
		t.Fatalf("Suffixes() returned shared slice — caller mutation leaked into package state")
	}
}
