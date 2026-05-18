// Package migrations embeds dialect-specific Goose migration trees into the
// binary. Atlas emits dialect-specific SQL (sqlite uses inline FK and
// backtick identifiers; postgres uses double-quote identifiers and a
// different FK declaration style), so we maintain one tree per driver and
// pick at runtime.
package migrations

import "embed"

//go:embed sqlite/*.sql
var SQLite embed.FS

//go:embed postgres/*.sql
var Postgres embed.FS

// Migrations is a back-compat alias for the sqlite tree, used by unit and
// integration tests that bring their own in-memory sqlite. Production code
// must use FSForDriver(driver) instead.
var Migrations = SQLite

// FSForDriver returns the embed.FS that contains migrations for the named
// driver. Caller must use the matching subdirectory name when invoking
// goose, e.g. goose.RunContext(ctx, "up", db, "sqlite", ...).
func FSForDriver(driver string) (embed.FS, string, bool) {
	switch driver {
	case "sqlite":
		return SQLite, "sqlite", true
	case "postgres", "postgresql":
		return Postgres, "postgres", true
	}
	return embed.FS{}, "", false
}
