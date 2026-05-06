package main

import (
	"encoding/json"
	"fmt"
	"os"

	copier "github.com/fyltr/copier-go"
	"github.com/spf13/cobra"
)

func newCheckUpdateCmd() *cobra.Command {
	var (
		answersFile string
		preReleases bool
		quiet       bool
		format      string
	)

	cmd := &cobra.Command{
		Use:   "check-update [DESTINATION]",
		Short: "Check if a project has a newer template version",
		Long:  "Check whether a project was generated from the latest version of its original template.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dst := "."
			if len(args) > 0 {
				dst = args[0]
			}

			var opts []copier.Option
			if answersFile != "" {
				opts = append(opts, copier.WithAnswersFile(answersFile))
			}
			opts = append(opts, copier.WithPreReleases(preReleases), copier.WithQuiet(quiet))

			result, err := copier.CheckUpdate(dst, opts...)
			if err != nil {
				return err
			}

			if quiet {
				if result.UpdateAvailable {
					return &exitError{Code: 2}
				}
				return nil
			}

			switch format {
			case "plain":
				if result.UpdateAvailable {
					if _, err := fmt.Fprintf(os.Stdout, "New template version available.\nCurrent version is %s, latest version is %s.\n", result.CurrentVersion, result.LatestVersion); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintln(os.Stdout, "Project is up-to-date!"); err != nil {
						return err
					}
				}
			case "json":
				payload, err := json.Marshal(result)
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintln(os.Stdout, string(payload)); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported output format %q", format)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&answersFile, "answers-file", "a", "", "path to answers file")
	cmd.Flags().BoolVarP(&preReleases, "prereleases", "g", false, "include pre-release versions")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress output and exit 2 when an update exists")
	cmd.Flags().StringVar(&format, "output-format", "plain", "output format: plain or json")

	return cmd
}
