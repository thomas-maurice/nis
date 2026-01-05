package main

import (
	"fmt"
	"os"

	"github.com/thomas-maurice/nis/cmd/nis/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
