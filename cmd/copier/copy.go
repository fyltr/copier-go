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
		Short: "Copy from a template source to a destination",
		Long:  "Scaffold a new project from a template. TEMPLATE is a local path or Git URL.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := flags.options()
			if err != nil {
				return err
			}
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
	cmd.Flags().BoolVarP(&defaults, "defaults", "l", false, "use default answers to questions, which might be null if not specified")
	cmd.Flags().BoolVarP(&overwrite, "overwrite", "w", false, "overwrite files that already exist, without asking")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "same as `--defaults --overwrite`")
	cmd.Flags().BoolVarP(&noCleanup, "no-cleanup", "C", false, "on error, do not delete destination if it was created by Copier")

	return cmd
}
