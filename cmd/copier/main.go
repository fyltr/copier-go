// Package main provides the copier CLI.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/fyltr/copier-go/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	root := newRootCmd()
	root.AddCommand(newCopyCmd(), newUpdateCmd(), newRecopyCmd(), newCheckUpdateCmd())
	if err := root.Execute(); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			if exitErr.Message != "" {
				fmt.Fprintln(os.Stderr, exitErr.Message)
			}
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type exitError struct {
	Code    int
	Message string
}

func (e *exitError) Error() string { return e.Message }

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "copier",
		Short:         "A tool for rendering project templates",
		Long:          "Copier scaffolds projects from templates, supports updates, and interactive questionnaires.",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return cmd
}
