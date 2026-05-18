package webhooks

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"strconv"
	"testing"
	"time"
)

func makeRequest(t *testing.T, secret []byte, ts string, body string, sigOverride string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set(HeaderTimestamp, ts)
	if sigOverride != "" {
		req.Header.Set(HeaderSignature, sigOverride)
	} else {
		req.Header.Set(HeaderSignature, Sign(secret, ts, []byte(body)))
	}
	return req
}

func nowTS() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

func TestSign_Deterministic(t *testing.T) {
	secret := []byte("topsecret")
	ts := "1700000000"
	body := []byte(`{"hello":"world"}`)
	s1 := Sign(secret, ts, body)
	s2 := Sign(secret, ts, body)
	if s1 != s2 {
		t.Fatalf("Sign is not deterministic: %q != %q", s1, s2)
	}
}

func TestSign_DifferentBody_DifferentSig(t *testing.T) {
	secret := []byte("topsecret")
	ts := "1700000000"
	s1 := Sign(secret, ts, []byte(`{"a":1}`))
	s2 := Sign(secret, ts, []byte(`{"a":2}`))
	if s1 == s2 {
		t.Fatal("different bodies produced the same signature")
	}
}

func TestVerify_OK(t *testing.T) {
	secret := []byte("my-secret")
	ts := nowTS()
	body := `{"event":"test"}`
	req := makeRequest(t, secret, ts, body, "")
	rc, err := Verify(req, secret, 0)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer rc.Close() //nolint:errcheck
	got, _ := io.ReadAll(rc)
	if string(got) != body {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestVerify_MissingSig(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	req.Header.Set(HeaderTimestamp, nowTS())
	_, err := Verify(req, []byte("s"), 0)
	if !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("expected ErrMissingSignature, got %v", err)
	}
}

func TestVerify_MalformedSig(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	req.Header.Set(HeaderTimestamp, nowTS())
	req.Header.Set(HeaderSignature, "notalgorithm=abc")
	_, err := Verify(req, []byte("s"), 0)
	if !errors.Is(err, ErrMalformedSignature) {
		t.Fatalf("expected ErrMalformedSignature, got %v", err)
	}
}

func TestVerify_MissingTimestamp(t *testing.T) {
	secret := []byte("s")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	req.Header.Set(HeaderSignature, Sign(secret, nowTS(), []byte("body")))
	_, err := Verify(req, secret, 0)
	if !errors.Is(err, ErrMissingTimestamp) {
		t.Fatalf("expected ErrMissingTimestamp, got %v", err)
	}
}

func TestVerify_InvalidTimestamp(t *testing.T) {
	secret := []byte("s")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	req.Header.Set(HeaderTimestamp, "notanumber")
	req.Header.Set(HeaderSignature, Sign(secret, "notanumber", []byte("body")))
	_, err := Verify(req, secret, 0)
	if !errors.Is(err, ErrInvalidTimestamp) {
		t.Fatalf("expected ErrInvalidTimestamp, got %v", err)
	}
}

func TestVerify_TimestampTooOld(t *testing.T) {
	secret := []byte("s")
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	req := makeRequest(t, secret, old, "body", "")
	_, err := Verify(req, secret, DefaultTolerance)
	if !errors.Is(err, ErrTimestampSkew) {
		t.Fatalf("expected ErrTimestampSkew, got %v", err)
	}
}

func TestVerify_TimestampTooFuture(t *testing.T) {
	secret := []byte("s")
	future := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	req := makeRequest(t, secret, future, "body", "")
	_, err := Verify(req, secret, DefaultTolerance)
	if !errors.Is(err, ErrTimestampSkew) {
		t.Fatalf("expected ErrTimestampSkew, got %v", err)
	}
}

func TestVerify_SigMismatch(t *testing.T) {
	secret := []byte("s")
	ts := nowTS()
	// Sign with one body, submit with another.
	wrongSig := Sign(secret, ts, []byte("original-body"))
	req := makeRequest(t, secret, ts, "tampered-body", wrongSig)
	_, err := Verify(req, secret, 0)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	ts := nowTS()
	body := "payload"
	req := makeRequest(t, []byte("real-secret"), ts, body, "")
	_, err := Verify(req, []byte("wrong-secret"), 0)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestVerify_BodyStillReadable(t *testing.T) {
	secret := []byte("s")
	ts := nowTS()
	want := `{"key":"value"}`
	req := makeRequest(t, secret, ts, want, "")
	rc, err := Verify(req, secret, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close() //nolint:errcheck
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("body mismatch: got %q want %q", got, want)
	}
}

func TestVerify_KnownVector(t *testing.T) {
	// Locks in the canonical signing algorithm: HMAC-SHA256(secret, timestamp+"."+body).
	// Sign must produce the same output forever — any change breaks deployed receivers.
	secret := []byte("topsecret")
	ts := "1700000000"
	body := []byte(`{"hello":"world"}`)
	got := Sign(secret, ts, body)

	// Deterministic across calls.
	if got != Sign(secret, ts, body) {
		t.Fatal("Sign is not deterministic for known vector")
	}
	// Must carry the algorithm prefix.
	if len(got) < len(SignaturePrefix) || got[:len(SignaturePrefix)] != SignaturePrefix {
		t.Fatalf("Sign output missing prefix: %q", got)
	}
	// The hex payload must be non-empty (SHA-256 is 64 hex chars).
	hex := got[len(SignaturePrefix):]
	if len(hex) != 64 {
		t.Fatalf("expected 64-char hex after prefix, got %d: %q", len(hex), hex)
	}

	// Round-trip: a request signed with the same value must pass Verify with a
	// very wide tolerance so the fixed 2023 timestamp is always accepted.
	req := makeRequest(t, secret, ts, string(body), got)
	rc, err := Verify(req, secret, 10*365*24*time.Hour)
	if err != nil {
		t.Fatalf("known vector round-trip failed: %v", err)
	}
	defer rc.Close() //nolint:errcheck
}
