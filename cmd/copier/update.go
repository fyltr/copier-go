package main

import (
	copier "github.com/fyltr/copier-go"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		flags        commonFlags
		defaults     bool
		force        bool
		conflict     string
		contextLines int
		skipAnswered bool
	)

	cmd := &cobra.Command{
		Use:   "update [DESTINATION]",
		Short: "Update a subproject from its original template",
		Long: `Update a subproject from its original template.

The copy must have a valid answers file which contains info from the last
Copier execution, including the source template (it must be a key called
` + "`_src_path`" + `).

If that file contains also ` + "`_commit`" + `, and DESTINATION is a git
repository, this command will do its best to respect the diff that you have
generated since the last copier execution. To avoid that, use ` + "`copier recopy`" + `
instead.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dst := "."
			if len(args) > 0 {
				dst = args[0]
			}

			opts, err := flags.options()
			if err != nil {
				return err
			}
			opts = append(opts,
				copier.WithDefaults(defaults || force),
				copier.WithOverwrite(true),
				copier.WithConflict(copier.ConflictStrategy(conflict)),
				copier.WithContextLines(contextLines),
				copier.WithSkipAnswered(skipAnswered),
			)
			return copier.Update(dst, opts...)
		},
	}

	flags.register(cmd)
	cmd.Flags().BoolVarP(&defaults, "defaults", "l", false, "use default answers to questions, which might be null if not specified")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "same as `--defaults`")
	_ = cmd.Flags().MarkHidden("force")
	cmd.Flags().StringVarP(&conflict, "conflict", "o", "inline", "behavior on conflict: create .rej files, or add inline conflict markers (rej or inline)")
	cmd.Flags().IntVarP(&contextLines, "context-lines", "c", 3, "lines of context to use for detecting conflicts; increase for accuracy, decrease for resilience")
	cmd.Flags().BoolVarP(&skipAnswered, "skip-answered", "A", false, "skip questions that have already been answered")

	return cmd
}
