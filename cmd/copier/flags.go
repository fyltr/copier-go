package main

import (
	"fmt"
	"os"
	"strings"

	copier "github.com/fyltr/copier-go"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// commonFlags groups flags shared by copy, update, and recopy commands.
type commonFlags struct {
	answersFile string
	vcsRef      string
	data        []string
	dataFile    string
	exclude     []string
	skip        []string
	ask         []string
	quiet       bool
	pretend     bool
	unsafe      bool
	skipTasks   bool
	preReleases bool
}

func (f *commonFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.answersFile, "answers-file", "a", "", "update using this path (relative to destination) to find the answers file")
	cmd.Flags().StringVarP(&f.vcsRef, "vcs-ref", "r", "", "git reference to checkout in the template (tag/branch/commit); defaults to the latest tag")
	cmd.Flags().StringArrayVarP(&f.data, "data", "d", nil, "make VARIABLE available as VALUE when rendering the template (VARIABLE=VALUE)")
	cmd.Flags().StringVar(&f.dataFile, "data-file", "", "load data from a YAML file")
	cmd.Flags().StringArrayVarP(&f.exclude, "exclude", "x", nil, "gitignore-style patterns for files/folders that must not be copied")
	cmd.Flags().StringArrayVarP(&f.skip, "skip", "s", nil, "skip specified files if they exist already; may be given multiple times")
	cmd.Flags().StringArrayVar(&f.ask, "ask", nil, "ask the questions matching the given glob-pattern, even if they would be skipped by other options")
	cmd.Flags().BoolVarP(&f.quiet, "quiet", "q", false, "suppress status output")
	cmd.Flags().BoolVarP(&f.pretend, "pretend", "n", false, "run but do not make any changes")
	cmd.Flags().BoolVar(&f.unsafe, "UNSAFE", false, "allow templates with unsafe features (Jinja extensions, tasks, migrations)")
	cmd.Flags().BoolVar(&f.unsafe, "trust", false, "allow templates with unsafe features (Jinja extensions, tasks, migrations)")
	cmd.Flags().BoolVarP(&f.skipTasks, "skip-tasks", "T", false, "skip template tasks execution")
	cmd.Flags().BoolVarP(&f.preReleases, "prereleases", "g", false, "use prereleases to compare template VCS tags")
}

func (f *commonFlags) options() ([]copier.Option, error) {
	var opts []copier.Option
	if f.answersFile != "" {
		opts = append(opts, copier.WithAnswersFile(f.answersFile))
	}
	if f.vcsRef != "" {
		opts = append(opts, copier.WithVcsRef(f.vcsRef))
	}
	data, err := parseData(f.data)
	if err != nil {
		return nil, err
	}
	if f.dataFile != "" {
		fileData, err := loadDataFile(f.dataFile)
		if err != nil {
			return nil, err
		}
		for k, v := range fileData {
			if _, ok := data[k]; !ok {
				data[k] = v
			}
		}
	}
	if len(data) > 0 {
		opts = append(opts, copier.WithData(data))
	}
	if len(f.exclude) > 0 {
		opts = append(opts, copier.WithExclude(f.exclude...))
	}
	if len(f.skip) > 0 {
		opts = append(opts, copier.WithSkip(f.skip...))
	}
	if len(f.ask) > 0 {
		opts = append(opts, copier.WithAsk(f.ask...))
	}
	opts = append(opts,
		copier.WithQuiet(f.quiet),
		copier.WithPretend(f.pretend),
		copier.WithUnsafe(f.unsafe),
		copier.WithSkipTasks(f.skipTasks),
		copier.WithPreReleases(f.preReleases),
	)
	return opts, nil
}

func parseData(pairs []string) (map[string]any, error) {
	data := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid data format %q; expected VARIABLE=VALUE", pair)
		}
		data[k] = v
	}
	return data, nil
}

func loadDataFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := yaml.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parsing data file %s: %w", path, err)
	}
	return data, nil
}
