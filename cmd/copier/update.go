package main

import (
	copier "github.com/fyltr/copier-go"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		flags        commonFlags
		defaults     bool
		conflict     string
		contextLines int
		skipAnswered bool
	)

	cmd := &cobra.Command{
		Use:   "update [DESTINATION]",
		Short: "Update a project to a newer template version",
		Long:  "Smartly update a project by computing a 3-way diff between old template, current state, and new template.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dst := "."
			if len(args) > 0 {
				dst = args[0]
			}

			opts := flags.options()
			opts = append(opts,
				copier.WithDefaults(defaults),
				copier.WithConflict(copier.ConflictStrategy(conflict)),
				copier.WithContextLines(contextLines),
				copier.WithSkipAnswered(skipAnswered),
			)
			return copier.Update(dst, opts...)
		},
	}

	flags.register(cmd)
	cmd.Flags().BoolVarP(&defaults, "defaults", "l", false, "use default answers")
	cmd.Flags().StringVarP(&conflict, "conflict", "o", "inline", "conflict strategy: inline or rej")
	cmd.Flags().IntVarP(&contextLines, "context-lines", "c", 3, "number of diff context lines")
	cmd.Flags().BoolVarP(&skipAnswered, "skip-answered", "A", false, "skip previously answered questions")

	return cmd
}
