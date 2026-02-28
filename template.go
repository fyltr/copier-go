package copier

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// Template represents a loaded copier template with its configuration.
type Template struct {
	// URL is the original template URL or local path.
	URL string

	// LocalPath is the absolute path to the template on disk (cloned or direct).
	LocalPath string

	// Ref is the resolved git reference (tag, branch, commit).
	Ref string

	// CommitHash is the full git commit hash of the checked-out template.
	CommitHash string

	// Config holds parsed copier.yml settings (keys prefixed with _).
	Config TemplateConfig

	// Questions holds parsed question definitions (non-prefixed keys).
	Questions []QuestionDef

	// rawConfig preserves the full parsed YAML for reference.
	rawConfig map[string]any
}

// TemplateConfig holds configuration from copier.yml underscore-prefixed keys.
type TemplateConfig struct {
	AnswersFile      string     `yaml:"_answers_file"`
	Subdirectory     string     `yaml:"_subdirectory"`
	TemplateSuffix   string     `yaml:"_templates_suffix"`
	Exclude          []string   `yaml:"_exclude"`
	SkipIfExists     []string   `yaml:"_skip_if_exists"`
	Tasks            []TaskDef  `yaml:"_tasks"`
	JinjaExtensions  []string   `yaml:"_jinja_extensions"`
	SecretQuestions  []string   `yaml:"_secret_questions"`
	PreserveSymlinks bool       `yaml:"_preserve_symlinks"`
	MinCopierVersion string     `yaml:"_min_copier_version"`
	Envops           Envops         `yaml:"_envops"`

	MessageBeforeCopy   string `yaml:"_message_before_copy"`
	MessageAfterCopy    string `yaml:"_message_after_copy"`
	MessageBeforeUpdate string `yaml:"_message_before_update"`
	MessageAfterUpdate  string `yaml:"_message_after_update"`

	Migrations []MigrationDef `yaml:"_migrations"`
}

// TaskDef defines a command to run during template operations.
type TaskDef struct {
	Cmd              any    `yaml:"cmd"`       // string or []string
	Condition        any    `yaml:"condition"` // string or bool, default true
	WorkingDirectory string `yaml:"working_directory"`
}

// CmdString returns the command as a single string.
func (t TaskDef) CmdString() string {
	switch v := t.Cmd.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, len(v))
		for i, p := range v {
			parts[i] = fmt.Sprintf("%v", p)
		}
		return strings.Join(parts, " ")
	}
	return fmt.Sprintf("%v", t.Cmd)
}

// CmdArgs returns the command as a slice of arguments.
func (t TaskDef) CmdArgs() []string {
	switch v := t.Cmd.(type) {
	case string:
		return []string{"sh", "-c", v}
	case []any:
		args := make([]string, len(v))
		for i, p := range v {
			args[i] = fmt.Sprintf("%v", p)
		}
		return args
	}
	return []string{"sh", "-c", fmt.Sprintf("%v", t.Cmd)}
}

// MigrationDef defines a migration step between template versions.
type MigrationDef struct {
	Version string    `yaml:"version"`
	Before  []TaskDef `yaml:"before"`
	After   []TaskDef `yaml:"after"`
}

// QuestionDef holds the raw question definition from copier.yml.
type QuestionDef struct {
	Name        string
	Type        QuestionType   `yaml:"type"`
	Help        string         `yaml:"help"`
	Choices     any            `yaml:"choices"` // []any or map[string]any or string (Jinja)
	Multiselect bool           `yaml:"multiselect"`
	Default     any            `yaml:"default"`
	Secret      bool           `yaml:"secret"`
	Multiline   any            `yaml:"multiline"` // bool or string
	Placeholder string         `yaml:"placeholder"`
	Validator   string         `yaml:"validator"`
	When        any            `yaml:"when"` // bool or string (Jinja condition)
}

// LoadTemplate loads a template from a local path or Git URL.
// If url is a Git URL, it clones the repository. ref selects a specific version.
func LoadTemplate(url, ref string, usePreReleases bool) (*Template, error) {
	tmpl := &Template{URL: url}

	url, isGit := NormalizeURL(url)
	if isGit {
		localPath, resolvedRef, err := CloneTemplate(url, ref, usePreReleases)
		if err != nil {
			return nil, &TemplateError{Path: url, Err: err}
		}
		tmpl.LocalPath = localPath
		tmpl.Ref = resolvedRef

		hash, err := RepoCommitHash(localPath)
		if err == nil {
			tmpl.CommitHash = hash
		}
	} else {
		absPath, err := filepath.Abs(url)
		if err != nil {
			return nil, &TemplateError{Path: url, Err: err}
		}
		tmpl.LocalPath = absPath
		if isLocalGitRepo(absPath) {
			hash, _ := RepoCommitHash(absPath)
			tmpl.CommitHash = hash
		}
	}

	if err := tmpl.loadConfig(); err != nil {
		return nil, err
	}

	return tmpl, nil
}

// loadConfig reads and parses copier.yml/copier.yaml from the template directory.
func (t *Template) loadConfig() error {
	root := t.CopyRoot()

	ymlPath := filepath.Join(root, "copier.yml")
	yamlPath := filepath.Join(root, "copier.yaml")
	ymlExists := fileExists(ymlPath)
	yamlExists := fileExists(yamlPath)

	if ymlExists && yamlExists {
		return &TemplateError{Path: root, Err: ErrMultipleConfigs}
	}

	configPath := ymlPath
	if yamlExists {
		configPath = yamlPath
	}

	if !ymlExists && !yamlExists {
		// No config file — template with defaults only.
		t.Config.TemplateSuffix = DefaultTemplateSuffix
		t.Config.AnswersFile = AnswersFileName
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &TemplateError{Path: configPath, Err: err}
	}

	raw := make(map[string]any)
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return &TemplateError{Path: configPath, Err: fmt.Errorf("%w: %v", ErrConfig, err)}
	}
	t.rawConfig = raw

	// Separate config keys (prefixed with _) from questions.
	configData := make(map[string]any)
	questionData := make(map[string]any)
	for k, v := range raw {
		if strings.HasPrefix(k, "_") {
			configData[k] = v
		} else {
			questionData[k] = v
		}
	}

	// Re-marshal config keys and decode into TemplateConfig.
	if len(configData) > 0 {
		cfgBytes, err := yaml.Marshal(configData)
		if err != nil {
			return &TemplateError{Path: configPath, Err: err}
		}
		if err := yaml.Unmarshal(cfgBytes, &t.Config); err != nil {
			return &TemplateError{Path: configPath, Err: err}
		}
	}

	// Apply defaults.
	if t.Config.TemplateSuffix == "" {
		t.Config.TemplateSuffix = DefaultTemplateSuffix
	}
	if t.Config.AnswersFile == "" {
		t.Config.AnswersFile = AnswersFileName
	}

	// Parse questions.
	t.Questions = parseQuestions(questionData)

	return nil
}

func parseQuestions(data map[string]any) []QuestionDef {
	questions := make([]QuestionDef, 0, len(data))
	for name, raw := range data {
		q := QuestionDef{Name: name}
		switch v := raw.(type) {
		case map[string]any:
			fillQuestionDef(&q, v)
		default:
			// Simple key: value → question with a default.
			q.Default = raw
			q.Type = inferType(raw)
		}
		questions = append(questions, q)
	}

	// Sort by name for deterministic order.
	sortQuestions(questions)
	return questions
}

func fillQuestionDef(q *QuestionDef, m map[string]any) {
	if v, ok := m["type"]; ok {
		q.Type = QuestionType(fmt.Sprintf("%v", v))
	}
	if v, ok := m["help"]; ok {
		q.Help = fmt.Sprintf("%v", v)
	}
	if v, ok := m["choices"]; ok {
		q.Choices = v
	}
	if v, ok := m["multiselect"]; ok {
		if b, isBool := v.(bool); isBool {
			q.Multiselect = b
		}
	}
	if v, ok := m["default"]; ok {
		q.Default = v
	}
	if v, ok := m["secret"]; ok {
		if b, isBool := v.(bool); isBool {
			q.Secret = b
		}
	}
	if v, ok := m["multiline"]; ok {
		q.Multiline = v
	}
	if v, ok := m["placeholder"]; ok {
		q.Placeholder = fmt.Sprintf("%v", v)
	}
	if v, ok := m["validator"]; ok {
		q.Validator = fmt.Sprintf("%v", v)
	}
	if v, ok := m["when"]; ok {
		q.When = v
	}
	if q.Type == "" && q.Default != nil {
		q.Type = inferType(q.Default)
	}
	if q.Type == "" {
		q.Type = TypeStr
	}
}

func inferType(val any) QuestionType {
	switch val.(type) {
	case bool:
		return TypeBool
	case int, int64:
		return TypeInt
	case float64:
		return TypeFloat
	default:
		return TypeStr
	}
}

func sortQuestions(qs []QuestionDef) {
	// Stable sort to preserve YAML order where possible.
	// In practice, Go map iteration is random so we sort by name.
	for i := 0; i < len(qs); i++ {
		for j := i + 1; j < len(qs); j++ {
			if qs[i].Name > qs[j].Name {
				qs[i], qs[j] = qs[j], qs[i]
			}
		}
	}
}

// CopyRoot returns the root directory for template files, accounting for subdirectory.
func (t *Template) CopyRoot() string {
	if t.Config.Subdirectory != "" {
		return filepath.Join(t.LocalPath, t.Config.Subdirectory)
	}
	return t.LocalPath
}

// Exclusions returns the combined list of exclude patterns (defaults + template config).
func (t *Template) Exclusions() []string {
	if len(t.Config.Exclude) > 0 {
		return t.Config.Exclude
	}
	return DefaultExclude
}

// Metadata returns template metadata to embed in the answers file.
func (t *Template) Metadata() map[string]any {
	m := map[string]any{
		"_src_path": t.URL,
	}
	if t.CommitHash != "" {
		m["_commit"] = t.CommitHash
	}
	return m
}

// MinVersion returns the parsed minimum copier version, or nil if not set.
func (t *Template) MinVersion() *semver.Version {
	if t.Config.MinCopierVersion == "" {
		return nil
	}
	v, err := semver.NewVersion(strings.TrimPrefix(t.Config.MinCopierVersion, "v"))
	if err != nil {
		return nil
	}
	return v
}

// MigrationTasks returns tasks for a given migration stage (before/after)
// filtered by version range.
func (t *Template) MigrationTasks(stage string, fromVer, toVer *semver.Version) []TaskDef {
	var tasks []TaskDef
	for _, m := range t.Config.Migrations {
		migVer, err := semver.NewVersion(strings.TrimPrefix(m.Version, "v"))
		if err != nil {
			continue
		}
		// Include migrations where fromVer < migVer <= toVer.
		if fromVer != nil && !migVer.GreaterThan(fromVer) {
			continue
		}
		if toVer != nil && migVer.GreaterThan(toVer) {
			continue
		}
		switch stage {
		case "before":
			tasks = append(tasks, m.Before...)
		case "after":
			tasks = append(tasks, m.After...)
		}
	}
	return tasks
}

// IsSecret reports whether the named question is marked as secret.
func (t *Template) IsSecret(name string) bool {
	for _, s := range t.Config.SecretQuestions {
		if s == name {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
