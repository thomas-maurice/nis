// Package clock is the single source of truth for "now" inside NIS.
//
// Hard rule: every time.Time value we store in the database or send on the
// wire is in UTC. Conversion to a viewer's local timezone happens only at the
// display boundary (the Vue UI uses toLocaleString; nisctl uses .Local()).
// Doing UTC everywhere internally means SQLite text comparisons, Postgres
// TIMESTAMPTZ round-trips, and JSON serialization all agree on a single
// instant — no double-offset bugs at the I/O boundary.
//
// Always use clock.Now() instead of time.Now() in service / persistence /
// handler code that produces a value destined for storage or response.
//
// Carve-out: code that pairs time.Now() with time.Since(...) to measure a
// duration is fine to use time.Now() directly — the offset cancels out and
// going through clock.Now() adds no value.
package clock

import "time"

// Now returns the current instant in UTC.
func Now() time.Time {
	return time.Now().UTC()
}
