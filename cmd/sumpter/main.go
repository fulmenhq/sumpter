package main

import (
	"os"

	"github.com/fulmenhq/sumpter/cmd/sumpter/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}

// runMain is a testable version of the main logic
func runMain() error {
	return commands.Execute()
}
