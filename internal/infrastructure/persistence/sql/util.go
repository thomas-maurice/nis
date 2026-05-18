package sql

import "strings"

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

