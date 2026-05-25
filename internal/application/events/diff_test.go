package events

import (
	"reflect"
	"testing"

	"github.com/thomas-maurice/nis/internal/application/redact"
)

func TestDiffBuilder_NoChangesYieldsNil(t *testing.T) {
	var b DiffBuilder
	b.Set("name", "alice", "alice")
	b.Set("count", 7, 7)
	if got := b.Finalize(); got != nil {
		t.Fatalf("expected nil diff, got %v", got)
	}
}

func TestDiffBuilder_Set_RecordsChange(t *testing.T) {
	var b DiffBuilder
	b.Set("name", "alice", "bob")
	d := b.Finalize()
	want := Diff{"name": {"alice", "bob"}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %v want %v", d, want)
	}
}

func TestDiffBuilder_Set_RedactsSensitiveSuffix(t *testing.T) {
	var b DiffBuilder
	b.Set("api_secret", "old", "new")
	d := b.Finalize()
	if d["api_secret"][0] != redact.Placeholder || d["api_secret"][1] != redact.Placeholder {
		t.Fatalf("expected redacted pair, got %v", d["api_secret"])
	}
}

func TestDiffBuilder_SetRedacted_OnlyWhenChanged(t *testing.T) {
	var b DiffBuilder
	b.SetRedacted("encrypted_seed", false)
	b.SetRedacted("jwt", true)
	d := b.Finalize()
	if _, ok := d["encrypted_seed"]; ok {
		t.Errorf("encrypted_seed should be absent when unchanged")
	}
	if d["jwt"][0] != redact.Placeholder || d["jwt"][1] != redact.Placeholder {
		t.Errorf("jwt should be redacted-pair, got %v", d["jwt"])
	}
}

func TestDiffBuilder_Set_DeepEqualForSlices(t *testing.T) {
	var b DiffBuilder
	b.Set("urls", []string{"a", "b"}, []string{"a", "b"})
	b.Set("perms", []string{"x"}, []string{"x", "y"})
	d := b.Finalize()
	if _, ok := d["urls"]; ok {
		t.Errorf("urls unchanged but recorded")
	}
	if _, ok := d["perms"]; !ok {
		t.Errorf("perms changed but not recorded")
	}
}

func TestDiffBuilder_ZeroValueReady(t *testing.T) {
	// A zero-value DiffBuilder must be usable without an explicit constructor.
	// Mirrors the call-site pattern: `var diff events.DiffBuilder; diff.Set(...)`.
	var b DiffBuilder
	b.Set("k", 1, 2)
	if b.Finalize() == nil {
		t.Fatal("zero-value builder failed to record a change")
	}
}
