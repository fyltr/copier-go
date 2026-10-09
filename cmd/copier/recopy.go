package main

import (
	copier "github.com/fyltr/copier-go"
	"github.com/spf13/cobra"
)

func newRecopyCmd() *cobra.Command {
	var (
		flags        commonFlags
		defaults     bool
		overwrite    bool
		force        bool
		skipAnswered bool
	)

	cmd := &cobra.Command{
		Use:   "recopy [DESTINATION]",
		Short: "Recopy the template without updating",
		Long:  "Re-apply the template discarding project evolution, keeping previous answers.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dst := "."
			if len(args) > 0 {
				dst = args[0]
			}

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
				copier.WithSkipAnswered(skipAnswered),
			)
			return copier.Recopy(dst, opts...)
		},
	}

	flags.register(cmd)
	cmd.Flags().BoolVarP(&defaults, "defaults", "l", false, "use default answers to questions, which might be null if not specified")
	cmd.Flags().BoolVarP(&overwrite, "overwrite", "w", false, "overwrite files that already exist, without asking")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "same as `--defaults --overwrite`")
	cmd.Flags().BoolVarP(&skipAnswered, "skip-answered", "A", false, "skip questions that have already been answered")

	return cmd
}
