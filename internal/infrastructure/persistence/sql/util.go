package sql

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

const (
	// DefaultListLimit is the default page size for ListPage calls.
	DefaultListLimit = 50
	// MaxListLimit is the maximum page size for ListPage calls.
	MaxListLimit = 200
)

// ErrInvalidCursor is the sentinel returned when a cursor string cannot be
// decoded. It re-exports repositories.ErrInvalidCursor so the SQL layer can
// return it without a wrap, while callers above the repo boundary still see
// the same domain-level error via errors.Is.
var ErrInvalidCursor = repositories.ErrInvalidCursor

// cursorPayload is the internal representation encoded into the opaque cursor string.
type cursorPayload struct {
	CreatedAt time.Time `json:"c"`
	ID        string    `json:"i"`
}

// EncodeCursor encodes a (created_at, id) pair into an opaque cursor string.
// The returned string is URL-safe base64-encoded JSON.
func EncodeCursor(createdAt time.Time, id uuid.UUID) string {
	p := cursorPayload{
		CreatedAt: createdAt.UTC(),
		ID:        id.String(),
	}
	data, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeCursor decodes a cursor string previously produced by EncodeCursor.
// Returns ErrInvalidCursor if the string is malformed or empty.
func DecodeCursor(s string) (time.Time, uuid.UUID, error) {
	if s == "" {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	var p cursorPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.Parse(p.ID)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	return p.CreatedAt.UTC(), id, nil
}

// clampListLimit normalises a raw limit from a filter:
//   - 0 or negative → DefaultListLimit
//   - > MaxListLimit → MaxListLimit
//   - otherwise returned as-is
func clampListLimit(n int) int {
	if n <= 0 {
		return DefaultListLimit
	}
	if n > MaxListLimit {
		return MaxListLimit
	}
	return n
}

// escapeLikeParam escapes the SQL LIKE meta-characters %, _ and the backslash
// escape character itself so a user-supplied substring is treated as a literal
// instead of a wildcard. The caller is still responsible for wrapping the
// returned value in %...% bindings.
//
// Use everywhere user input is interpolated into a LIKE pattern. Both engines
// honor backslash as the escape by default for the patterns we use; if that
// ever changes, the per-column LIKE clauses should add `ESCAPE '\'` explicitly.
func escapeLikeParam(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// jsonHTMLEscape mirrors `encoding/json`'s default escaping of the three
// HTML-sensitive characters inside string values: `<` becomes the six-character
// literal `<`, `>` becomes `>`, `&` becomes `&`. NATS subject
// wildcards (`>`) and the boolean `&` end up encoded this way when our
// scoped-key permission lists are serialized via `serializer:json`. The search
// path uses this to also match the stored form when a user types the raw
// character.
//
// Note: the output contains literal backslashes (one per escaped char). The
// LIKE pattern must be built WITHOUT escapeLikeParam's backslash-doubling for
// these to compare equal against the stored bytes — the helper escapeLikePercent
// covers the safe subset of escaping needed here.
func jsonHTMLEscape(s string) string {
	// Double-quoted strings (not raw) so the escape sequences materialize as
	// literal six-char sequences `<` / `>` / `&` — Go raw
	// strings would leave them as a single `<` / `>` / `&` and the helper
	// would be a no-op.
	r := strings.NewReplacer(
		"<", "\\u003c",
		">", "\\u003e",
		"&", "\\u0026",
	)
	return r.Replace(s)
}

