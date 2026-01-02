// Package main provides the entry point for the pvc-migrator CLI tool.
// It initializes and executes the root command.
package main

import (
	"github.com/cesarempathy/pv-zone-migrator/cmd"
)

func main() {
	cmd.Execute()
}
