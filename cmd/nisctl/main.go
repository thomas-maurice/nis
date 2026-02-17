package main

import (
	"os"

	"github.com/thomas-maurice/nis/cmd/nisctl/commands"
)

// version is set at build time via ldflags
var version = "dev"

func main() {
	commands.SetVersion(version)
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
