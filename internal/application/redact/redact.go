// Package redact centralises the rules for identifying credential-shaped
// field names. Used by:
//
//   - internal/application/services/config_service.go to redact env-only and
//     config-file leaf values in the running-config inspection view.
//   - internal/application/events/diff.go to scrub field-level audit diffs
//     of secret material before they hit the events table.
//
// One source of truth for what "looks like a secret" means in this codebase.
package redact

import "strings"

// Placeholder is the literal substituted for a redacted value across both
// callers. Kept distinct enough to be greppable in audit dumps.
const Placeholder = "***REDACTED***"

// sensitiveSuffixes is the suffix allowlist. "_key" is intentionally NOT
// here because legitimate non-secret config (encryption.current_key_id,
// encryption.key_id) and entity fields (public_key, scoped_signing_key_id)
// use it.
var sensitiveSuffixes = []string{
	"_secret",
	"_password",
	"_access_key",
}

// HasSensitiveSuffix returns true when name ends in a known credential
// suffix. Match is case-sensitive — every caller in this codebase already
// normalises to lowercase.
func HasSensitiveSuffix(name string) bool {
	for _, s := range sensitiveSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// Suffixes returns a copy of the canonical suffix list. Mutating the result
// does not affect the package state.
func Suffixes() []string {
	out := make([]string, len(sensitiveSuffixes))
	copy(out, sensitiveSuffixes)
	return out
}
