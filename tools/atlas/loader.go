// Atlas loader: emits the CREATE TABLE / INDEX SQL that GORM would produce
// for the models in internal/infrastructure/persistence/sql. atlas-provider-gorm
// walks the registered structs, runs them through the same statement builder
// AutoMigrate uses, and writes the result to stdout for Atlas to consume.
//
// Invoked by atlas.hcl via:
//   go run ./tools/atlas <dialect>
//
// Not part of the server binary — separate main package compiled on demand.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"ariga.io/atlas-provider-gorm/gormschema"

	sqlmodels "github.com/thomas-maurice/nis/internal/infrastructure/persistence/sql"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: loader <dialect>   (sqlite|postgres)")
		os.Exit(1)
	}
	dialect := os.Args[1]

	stmts, err := gormschema.New(dialect).Load(
		&sqlmodels.OperatorModel{},
		&sqlmodels.AccountModel{},
		&sqlmodels.ScopedSigningKeyModel{},
		&sqlmodels.UserModel{},
		&sqlmodels.ClusterModel{},
		&sqlmodels.APIUserModel{},
		&sqlmodels.APITokenModel{},
		&sqlmodels.EventModel{},
		&sqlmodels.WebhookSubscriptionModel{},
		&sqlmodels.WebhookDeliveryModel{},
		&sqlmodels.UserJWTRevocationModel{},
		&sqlmodels.TemplateModel{},
		&sqlmodels.TemplateVersionModel{},
		&sqlmodels.JobModel{},
		&sqlmodels.OperatorBackupModel{},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}

	var sb strings.Builder
	sb.WriteString(stmts)
	sb.WriteString("\n")
	sb.WriteString(manualExtras(dialect))

	_, _ = io.WriteString(os.Stdout, sb.String())
}

// manualExtras returns dialect-specific DDL that atlas-provider-gorm cannot
// emit from GORM struct tags. Appending these to the desired-schema dump
// makes Atlas see them as part of the canonical state, so `make atlas-diff`
// will NOT emit a DROP for them in the next migration.
//
// Add new entries here whenever you hand-edit a migration to add a feature
// that atlas-provider-gorm can't synthesise (partial indexes, deferred
// constraints, expression indexes, etc.) — otherwise the next schema
// regeneration will silently nuke it.
func manualExtras(dialect string) string {
	switch dialect {
	case "sqlite":
		return strings.Join([]string{
			// A2 jobs substrate — partial unique index. Keeps the dedup
			// guarantee on (type, dedup_key) only for in-flight rows;
			// terminal-state rows can share keys (next recurring tick).
			// atlas-provider-gorm has no GORM tag for `WHERE` on indexes.
			"CREATE UNIQUE INDEX `idx_jobs_dedup_active` ON `jobs` (`type`, `dedup_key`) WHERE `status` IN ('pending', 'running');",
			"",
		}, "\n")
	case "postgres":
		return strings.Join([]string{
			`CREATE UNIQUE INDEX "idx_jobs_dedup_active" ON "jobs" ("type", "dedup_key") WHERE "status" IN ('pending', 'running');`,
			"",
		}, "\n")
	default:
		return ""
	}
}
