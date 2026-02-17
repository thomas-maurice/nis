package main

import (
	"fmt"
	"os"

	"github.com/thomas-maurice/nis/cmd/nis/commands"
)

// version is set at build time via ldflags
var version = "dev"

func main() {
	commands.SetVersion(version)
	if err := commands.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
