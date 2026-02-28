package main

import (
	"fmt"
	"strings"

	copier "github.com/fyltr/copier-go"
	"github.com/spf13/cobra"
)

// commonFlags groups flags shared by copy, update, and recopy commands.
type commonFlags struct {
	answersFile    string
	vcsRef         string
	data           []string
	exclude        []string
	skip           []string
	quiet          bool
	pretend        bool
	unsafe         bool
	skipTasks      bool
	preReleases    bool
}

func (f *commonFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.answersFile, "answers-file", "a", "", "path to answers file")
	cmd.Flags().StringVarP(&f.vcsRef, "vcs-ref", "r", "", "git reference (tag/branch/commit)")
	cmd.Flags().StringSliceVarP(&f.data, "data", "d", nil, "answer data as KEY=VALUE")
	cmd.Flags().StringSliceVarP(&f.exclude, "exclude", "x", nil, "exclude patterns")
	cmd.Flags().StringSliceVarP(&f.skip, "skip", "s", nil, "skip-if-exists patterns")
	cmd.Flags().BoolVarP(&f.quiet, "quiet", "q", false, "suppress output")
	cmd.Flags().BoolVarP(&f.pretend, "pretend", "n", false, "dry run, no files written")
	cmd.Flags().BoolVar(&f.unsafe, "UNSAFE", false, "allow unsafe template features")
	cmd.Flags().BoolVar(&f.unsafe, "trust", false, "allow unsafe template features")
	cmd.Flags().BoolVarP(&f.skipTasks, "skip-tasks", "T", false, "skip task execution")
	cmd.Flags().BoolVarP(&f.preReleases, "prereleases", "g", false, "include pre-release versions")
}

func (f *commonFlags) options() []copier.Option {
	var opts []copier.Option
	if f.answersFile != "" {
		opts = append(opts, copier.WithAnswersFile(f.answersFile))
	}
	if f.vcsRef != "" {
		opts = append(opts, copier.WithVcsRef(f.vcsRef))
	}
	if len(f.data) > 0 {
		data, err := parseData(f.data)
		if err == nil {
			opts = append(opts, copier.WithData(data))
		}
	}
	if len(f.exclude) > 0 {
		opts = append(opts, copier.WithExclude(f.exclude...))
	}
	if len(f.skip) > 0 {
		opts = append(opts, copier.WithSkip(f.skip...))
	}
	opts = append(opts,
		copier.WithQuiet(f.quiet),
		copier.WithPretend(f.pretend),
		copier.WithUnsafe(f.unsafe),
		copier.WithSkipTasks(f.skipTasks),
		copier.WithPreReleases(f.preReleases),
	)
	return opts
}

func parseData(pairs []string) (map[string]any, error) {
	data := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid data format %q; expected KEY=VALUE", pair)
		}
		data[k] = v
	}
	return data, nil
}
