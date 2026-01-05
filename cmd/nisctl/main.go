package main

import (
	"os"

	"github.com/thomas-maurice/nis/cmd/nisctl/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
