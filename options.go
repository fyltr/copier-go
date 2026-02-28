package copier

import "path/filepath"

// Config holds all configuration for a copier operation.
// Use functional [Option] values to construct one via [Copy], [Update], or [Recopy].
type Config struct {
	// SrcPath is the template source (local path or Git URL).
	SrcPath string

	// DstPath is the destination directory.
	DstPath string

	// AnswersFile overrides the default answers file path (.copier-answers.yml).
	AnswersFile string

	// VcsRef selects a specific Git tag, branch, or commit. Empty means latest tag.
	VcsRef string

	// Data provides pre-set answers that skip interactive prompting.
	Data map[string]any

	// Defaults uses default values for all questions without prompting.
	Defaults bool

	// UserDefaults supplies fallback defaults for questions (lower priority than Data).
	UserDefaults map[string]any

	// Overwrite replaces existing files without asking.
	Overwrite bool

	// Skip lists patterns of files to leave untouched if they already exist.
	Skip []string

	// Exclude lists additional patterns of template files to ignore.
	Exclude []string

	// Pretend performs a dry run without writing any files.
	Pretend bool

	// Quiet suppresses informational output.
	Quiet bool

	// Unsafe allows templates that use tasks or other potentially dangerous features.
	Unsafe bool

	// SkipTasks skips execution of pre/post-copy tasks.
	SkipTasks bool

	// SkipAnswered skips questions whose answers are already known (update/recopy).
	SkipAnswered bool

	// UsePreReleases includes pre-release Git tags when selecting the latest version.
	UsePreReleases bool

	// CleanupOnError removes the destination directory if an error occurs during copy.
	CleanupOnError bool

	// Conflict sets the merge conflict strategy for updates ("inline" or "rej").
	Conflict ConflictStrategy

	// ContextLines sets the number of context lines in diffs for updates.
	ContextLines int
}

func defaultConfig() Config {
	return Config{
		AnswersFile:    AnswersFileName,
		CleanupOnError: true,
		Conflict:       ConflictInline,
		ContextLines:   3,
	}
}

// Option configures a copier operation.
type Option func(*Config)

func applyOptions(opts []Option) Config {
	c := defaultConfig()
	for _, o := range opts {
		o(&c)
	}
	return c
}

// WithAnswersFile sets a custom path for the copier answers file.
func WithAnswersFile(path string) Option { return func(c *Config) { c.AnswersFile = path } }

// WithVcsRef pins the template to a specific Git reference (tag, branch, commit).
func WithVcsRef(ref string) Option { return func(c *Config) { c.VcsRef = ref } }

// WithData provides answers that bypass interactive prompting.
func WithData(data map[string]any) Option { return func(c *Config) { c.Data = data } }

// WithDefaults uses all default values instead of prompting.
func WithDefaults(v bool) Option { return func(c *Config) { c.Defaults = v } }

// WithUserDefaults supplies fallback defaults for questions.
func WithUserDefaults(defaults map[string]any) Option {
	return func(c *Config) { c.UserDefaults = defaults }
}

// WithOverwrite allows overwriting existing files without confirmation.
func WithOverwrite(v bool) Option { return func(c *Config) { c.Overwrite = v } }

// WithSkip adds patterns for files to skip if they already exist.
func WithSkip(patterns ...string) Option { return func(c *Config) { c.Skip = patterns } }

// WithExclude adds patterns for template files to exclude from rendering.
func WithExclude(patterns ...string) Option { return func(c *Config) { c.Exclude = patterns } }

// WithPretend enables dry-run mode (no files written).
func WithPretend(v bool) Option { return func(c *Config) { c.Pretend = v } }

// WithQuiet suppresses informational output.
func WithQuiet(v bool) Option { return func(c *Config) { c.Quiet = v } }

// WithUnsafe allows templates with tasks or other potentially dangerous features.
func WithUnsafe(v bool) Option { return func(c *Config) { c.Unsafe = v } }

// WithSkipTasks disables execution of template tasks.
func WithSkipTasks(v bool) Option { return func(c *Config) { c.SkipTasks = v } }

// WithSkipAnswered skips questions that already have answers from a previous run.
func WithSkipAnswered(v bool) Option { return func(c *Config) { c.SkipAnswered = v } }

// WithPreReleases includes pre-release tags when selecting the latest version.
func WithPreReleases(v bool) Option { return func(c *Config) { c.UsePreReleases = v } }

// WithCleanupOnError removes the destination on failure (default: true).
func WithCleanupOnError(v bool) Option { return func(c *Config) { c.CleanupOnError = v } }

// WithConflict sets the merge conflict strategy for updates.
func WithConflict(s ConflictStrategy) Option { return func(c *Config) { c.Conflict = s } }

// WithContextLines sets the number of diff context lines for updates.
func WithContextLines(n int) Option { return func(c *Config) { c.ContextLines = n } }

// resolvedAnswersFile returns the answers file path relative to DstPath.
func (c *Config) resolvedAnswersFile() string {
	if filepath.IsAbs(c.AnswersFile) {
		return c.AnswersFile
	}
	return filepath.Join(c.DstPath, c.AnswersFile)
}
