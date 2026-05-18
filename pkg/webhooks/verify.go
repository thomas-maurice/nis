package webhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header names used by NIS webhook deliveries.
const (
	HeaderEvent        = "X-NIS-Event"
	HeaderDelivery     = "X-NIS-Delivery"
	HeaderSubscription = "X-NIS-Subscription"
	HeaderTimestamp    = "X-NIS-Timestamp"
	HeaderSignature    = "X-NIS-Signature"
)

// SignaturePrefix is the algorithm tag in the X-NIS-Signature header value.
const SignaturePrefix = "sha256="

// DefaultTolerance is the default allowed clock skew between the dispatcher
// and the receiver. Deliveries older than this are rejected to prevent
// trivial replay attacks.
const DefaultTolerance = 5 * time.Minute

// Common verification errors. Callers can distinguish these with errors.Is.
var (
	ErrMissingSignature   = errors.New("webhooks: missing X-NIS-Signature header")
	ErrMissingTimestamp   = errors.New("webhooks: missing X-NIS-Timestamp header")
	ErrInvalidTimestamp   = errors.New("webhooks: invalid X-NIS-Timestamp header")
	ErrSignatureMismatch  = errors.New("webhooks: signature mismatch")
	ErrTimestampSkew      = errors.New("webhooks: timestamp outside tolerance window")
	ErrMalformedSignature = errors.New("webhooks: malformed X-NIS-Signature header")
)

// Sign computes the canonical NIS HMAC-SHA256 signature for (timestamp, body).
// The header value is "sha256=" + hex(hmac). Exported so producers (and the
// e2e tests) can use the same canonical algorithm.
func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks an incoming http.Request against the supplied secret. It
// rejects requests with missing/malformed headers, signatures that don't match,
// and timestamps outside the tolerance window. The request body is read into
// memory and made available via the returned io.ReadCloser so the caller can
// still json.Decode it afterwards (since the original body is consumed).
//
// Pass tolerance=0 to use DefaultTolerance.
func Verify(r *http.Request, secret []byte, tolerance time.Duration) (io.ReadCloser, error) {
	if tolerance == 0 {
		tolerance = DefaultTolerance
	}
	sig := r.Header.Get(HeaderSignature)
	if sig == "" {
		return nil, ErrMissingSignature
	}
	if !strings.HasPrefix(sig, SignaturePrefix) {
		return nil, ErrMalformedSignature
	}
	tsStr := r.Header.Get(HeaderTimestamp)
	if tsStr == "" {
		return nil, ErrMissingTimestamp
	}
	tsUnix, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return nil, ErrInvalidTimestamp
	}
	ts := time.Unix(tsUnix, 0)
	if d := time.Since(ts); d > tolerance || d < -tolerance {
		return nil, ErrTimestampSkew
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()

	expected := Sign(secret, tsStr, body)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return nil, ErrSignatureMismatch
	}
	return readCloser{bytes.NewReader(body)}, nil
}

// readCloser wraps a bytes.Reader as an io.ReadCloser so Verify can hand the
// consumed body back to the caller.
type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }
