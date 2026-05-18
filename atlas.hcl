// Atlas config. The same set of GORM models drives two migration trees —
// one per supported dialect — because Atlas emits dialect-specific SQL
// (sqlite uses inline FK + backtick identifiers; postgres uses ALTER TABLE
// ADD CONSTRAINT + double-quote identifiers). The Go embed picks the right
// tree at runtime based on the database driver.
//
// The external_schema feature is gated behind the non-community Atlas
// binary, so we use file://-based src by pre-generating the desired schema
// via `go run ./tools/atlas <dialect>` into /tmp before calling
// `atlas migrate diff`. The Makefile targets wrap this.

env "sqlite" {
  url = "sqlite://dev?mode=memory"
  dev = "sqlite://dev?mode=memory"
  migration {
    dir = "file://migrations/sqlite?format=goose"
  }
}

env "postgres" {
  url = "docker://postgres/16/dev?search_path=public"
  dev = "docker://postgres/16/dev?search_path=public"
  migration {
    dir = "file://migrations/postgres?format=goose"
  }
}
