package main

import (
	copier "github.com/fyltr/copier-go"
	"github.com/spf13/cobra"
)

func newCopyCmd() *cobra.Command {
	var (
		flags     commonFlags
		defaults  bool
		overwrite bool
		force     bool
		noCleanup bool
	)

	cmd := &cobra.Command{
		Use:   "copy TEMPLATE DESTINATION",
		Short: "Copy a template to a new project",
		Long:  "Scaffold a new project from a template. TEMPLATE is a local path or Git URL.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := flags.options()
			if force {
				defaults = true
				overwrite = true
			}
			opts = append(opts,
				copier.WithDefaults(defaults),
				copier.WithOverwrite(overwrite),
				copier.WithCleanupOnError(!noCleanup),
			)
			return copier.Copy(args[0], args[1], opts...)
		},
	}

	flags.register(cmd)
	cmd.Flags().BoolVarP(&defaults, "defaults", "l", false, "use default answers")
	cmd.Flags().BoolVarP(&overwrite, "overwrite", "w", false, "overwrite existing files")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "shortcut for --defaults --overwrite")
	cmd.Flags().BoolVarP(&noCleanup, "no-cleanup", "C", false, "do not delete destination on error")

	return cmd
}
