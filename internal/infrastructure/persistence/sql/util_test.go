package sql

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	want := time.Date(2026, 5, 22, 14, 30, 15, 123456789, time.UTC)
	wantID := uuid.New()

	c := EncodeCursor(want, wantID)
	if c == "" {
		t.Fatal("EncodeCursor returned empty string")
	}

	gotT, gotID, err := DecodeCursor(c)
	if err != nil {
		t.Fatalf("DecodeCursor returned error: %v", err)
	}
	if !gotT.Equal(want) {
		t.Errorf("time mismatch: want %v, got %v", want, gotT)
	}
	if gotID != wantID {
		t.Errorf("id mismatch: want %v, got %v", wantID, gotID)
	}
}

func TestEncodeCursor_NormalizesToUTC(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	localTime := time.Date(2026, 5, 22, 20, 0, 0, 0, loc)

	c := EncodeCursor(localTime, uuid.New())
	gotT, _, err := DecodeCursor(c)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if gotT.Location() != time.UTC {
		t.Errorf("decoded time is not UTC: %v", gotT.Location())
	}
	if !gotT.Equal(localTime) {
		t.Errorf("instant mismatch: want %v, got %v", localTime.UTC(), gotT)
	}
}

func TestDecodeCursor_EmptyErrors(t *testing.T) {
	// Empty cursor is the caller's "first page" signal — callers MUST check
	// for "" before invoking DecodeCursor. Reaching DecodeCursor with an
	// empty string is a programming error and surfaces as ErrInvalidCursor.
	_, _, err := DecodeCursor("")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("expected ErrInvalidCursor for empty cursor, got %v", err)
	}
}

func TestDecodeCursor_InvalidBase64(t *testing.T) {
	_, _, err := DecodeCursor("!!!not-base64!!!")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("expected ErrInvalidCursor, got %v", err)
	}
}

func TestDecodeCursor_InvalidJSON(t *testing.T) {
	// Valid base64 of "not-json" but not valid JSON payload
	_, _, err := DecodeCursor("bm90LWpzb24")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("expected ErrInvalidCursor, got %v", err)
	}
}

func TestEncodeCursor_URLSafe(t *testing.T) {
	// RawURLEncoding must not emit +, / or = — those break in URL query
	// params and bare JSON strings.
	c := EncodeCursor(time.Now(), uuid.New())
	for _, ch := range []string{"+", "/", "="} {
		if strings.Contains(c, ch) {
			t.Errorf("cursor contains %q (not URL-safe): %s", ch, c)
		}
	}
}

func TestClampListLimit_Boundaries(t *testing.T) {
	if got := clampListLimit(0); got != DefaultListLimit {
		t.Errorf("0 → %d, want %d", got, DefaultListLimit)
	}
	if got := clampListLimit(-1); got != DefaultListLimit {
		t.Errorf("-1 → %d, want %d", got, DefaultListLimit)
	}
	if got := clampListLimit(MaxListLimit + 1); got != MaxListLimit {
		t.Errorf("MaxListLimit+1 → %d, want %d", got, MaxListLimit)
	}
	if got := clampListLimit(50); got != 50 {
		t.Errorf("50 → %d, want 50", got)
	}
}
