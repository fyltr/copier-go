// Package main provides the copier CLI.
package main

import (
	"fmt"
	"os"

	"github.com/fyltr/copier-go/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	root := newRootCmd()
	root.AddCommand(newCopyCmd(), newUpdateCmd(), newRecopyCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copier",
		Short: "A tool for rendering project templates",
		Long:  "Copier scaffolds projects from templates, supports updates, and interactive questionnaires.",
		Version: version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return cmd
}
