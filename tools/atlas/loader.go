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
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	_, _ = io.WriteString(os.Stdout, stmts)
}
