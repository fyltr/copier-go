package copier

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fyltr/copier-go/internal/pathutil"
	"gopkg.in/yaml.v3"
)

// Template represents a loaded copier template with its configuration.
type Template struct {
	// URL is the original template URL or local path.
	URL string

	// LocalPath is the absolute path to the template on disk (cloned or direct).
	LocalPath string

	// Temporary is true when LocalPath points to a checkout that should be removed.
	Temporary bool

	// Ref is the resolved git reference (tag, branch, commit).
	Ref string

	// CommitHash is the full git commit hash of the checked-out template.
	CommitHash string

	// CommitDescription is the `git describe --tags --always` output saved for updates.
	CommitDescription string

	// Config holds parsed copier.yml settings (keys prefixed with _).
	Config TemplateConfig

	// Questions holds parsed question definitions (other keys) in file order.
	Questions []QuestionDef

	// rawConfig preserves the merged configuration for reference.
	rawConfig map[string]any

	// mirror is the cached bare repository the checkout is a worktree of, if any.
	mirror string

	version       *templateVersion
	versionParsed bool
}

// TemplateConfig holds configuration from copier.yml underscore-prefixed keys.
type TemplateConfig struct {
	AnswersFile      string
	Subdirectory     string
	TemplateSuffix   string
	Exclude          []string
	ExcludeSet       bool // Whether `_exclude` was given explicitly.
	SkipIfExists     []string
	Tasks            []TaskDef
	Migrations       []MigrationDef
	JinjaExtensions  []string
	SecretQuestions  []string
	PreserveSymlinks bool
	MinCopierVersion string
	Envops           Envops
	ExternalData     map[string]string

	MessageBeforeCopy   string
	MessageAfterCopy    string
	MessageBeforeUpdate string
	MessageAfterUpdate  string
}

// TaskDef defines a command to run during template operations.
type TaskDef struct {
	// Cmd is the command: a string (run through the shell) or a []string
	// (executed directly, without a shell).
	Cmd any
	// Condition is the `when` condition: a string (Jinja) or a bool. Nil means true.
	Condition any
	// WorkingDirectory is the directory the command runs in, relative to the destination.
	WorkingDirectory string
	// ExtraVars are exposed as `_<name>` Jinja variables and `<NAME>` environment variables.
	ExtraVars map[string]any
}

// CmdString returns the command as a single string.
func (t TaskDef) CmdString() string {
	switch v := t.Cmd.(type) {
	case string:
		return v
	case []string:
		return strings.Join(v, " ")
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
	case []string:
		return v
	case []any:
		args := make([]string, len(v))
		for i, p := range v {
			args[i] = fmt.Sprintf("%v", p)
		}
		return args
	}
	return []string{"sh", "-c", fmt.Sprintf("%v", t.Cmd)}
}

// parseTaskDef accepts the upstream task formats: a string, a list of strings,
// or a mapping with `command`, `when` and `working_directory`.
func parseTaskDef(raw any) (TaskDef, error) {
	switch v := raw.(type) {
	case string:
		return TaskDef{Cmd: v}, nil
	case []any:
		return TaskDef{Cmd: anyListToStrings(v)}, nil
	case map[string]any:
		t := TaskDef{}
		cmd, ok := v["command"]
		if !ok {
			cmd, ok = v["cmd"] // Backward compatibility with earlier copier-go releases.
		}
		if !ok {
			return t, fmt.Errorf("%w: task requires a `command`", ErrConfig)
		}
		switch c := cmd.(type) {
		case string:
			t.Cmd = c
		case []any:
			t.Cmd = anyListToStrings(c)
		default:
			return t, fmt.Errorf("%w: task `command` must be a string or list", ErrConfig)
		}
		if w, ok := v["when"]; ok {
			t.Condition = w
		} else if w, ok := v["condition"]; ok {
			t.Condition = w
		}
		if wd, ok := v["working_directory"]; ok && wd != nil {
			t.WorkingDirectory = fmt.Sprintf("%v", wd)
		}
		return t, nil
	}
	return TaskDef{}, fmt.Errorf("%w: invalid task definition %v", ErrConfig, raw)
}

func anyListToStrings(v []any) []string {
	out := make([]string, len(v))
	for i, p := range v {
		out[i] = fmt.Sprintf("%v", p)
	}
	return out
}

// MigrationDef defines a migration step between template versions.
//
// The new upstream format is a task with optional `version`; the legacy format
// has `version` plus `before`/`after` task lists.
type MigrationDef struct {
	Version          string
	Command          any // string or []string (new format)
	When             any // nil means the default condition `_stage == "after"`
	WorkingDirectory string
	Before           []TaskDef // legacy format
	After            []TaskDef // legacy format
	Legacy           bool
}

func parseMigrationDef(raw any) (MigrationDef, error) {
	switch v := raw.(type) {
	case string:
		return MigrationDef{Command: v}, nil
	case []any:
		return MigrationDef{Command: anyListToStrings(v)}, nil
	case map[string]any:
		_, hasBefore := v["before"]
		_, hasAfter := v["after"]
		if hasBefore || hasAfter {
			m := MigrationDef{Legacy: true}
			if ver, ok := v["version"]; ok && ver != nil {
				m.Version = fmt.Sprintf("%v", ver)
			}
			for _, stage := range []string{"before", "after"} {
				list, _ := v[stage].([]any)
				for _, item := range list {
					t, err := parseTaskDef(item)
					if err != nil {
						return m, err
					}
					if stage == "before" {
						m.Before = append(m.Before, t)
					} else {
						m.After = append(m.After, t)
					}
				}
			}
			return m, nil
		}
		t, err := parseTaskDef(v)
		if err != nil {
			return MigrationDef{}, err
		}
		m := MigrationDef{Command: t.Cmd, WorkingDirectory: t.WorkingDirectory}
		if w, ok := v["when"]; ok {
			m.When = w
		}
		if ver, ok := v["version"]; ok && ver != nil {
			m.Version = fmt.Sprintf("%v", ver)
		}
		return m, nil
	}
	return MigrationDef{}, fmt.Errorf("%w: invalid migration definition %v", ErrConfig, raw)
}

// QuestionDef holds the raw question definition from copier.yml.
type QuestionDef struct {
	Name        string
	Type        string // May be templated. Empty means inferred from the default.
	Help        string
	Choices     any // []any of values or [name, value] pairs, or a string (Jinja)
	Multiselect bool
	Default     any
	HasDefault  bool
	Secret      bool
	Multiline   any // bool or string (Jinja)
	Placeholder string
	Validator   string
	When        any // bool or string (Jinja condition); nil means true
	Qmark       string
}

// LoadTemplate loads a template from a local path or Git URL.
// If url is a Git URL, it clones the repository. ref selects a specific version.
func LoadTemplate(url, ref string, usePreReleases bool) (*Template, error) {
	tmpl := &Template{URL: url}

	repo := resolveRepo(url)
	if repo.isGit {
		if repo.local {
			tmpl.URL = repo.url
		}
		checkout, err := cloneTemplate(repo, ref, usePreReleases)
		if err != nil {
			return nil, &TemplateError{Path: repo.url, Err: err}
		}
		tmpl.LocalPath = checkout.path
		tmpl.Temporary = true
		tmpl.Ref = checkout.ref
		tmpl.mirror = checkout.mirror

		if hash, err := RepoCommitHash(checkout.path); err == nil {
			tmpl.CommitHash = hash
		}
		if desc, err := RepoCommitDescription(checkout.path); err == nil {
			tmpl.CommitDescription = desc
		} else if tmpl.CommitHash != "" {
			tmpl.CommitDescription = tmpl.CommitHash
		}
	} else {
		absPath, err := filepath.Abs(url)
		if err != nil {
			return nil, &TemplateError{Path: url, Err: err}
		}
		info, err := os.Stat(absPath)
		if err != nil || !info.IsDir() {
			return nil, &TemplateError{Path: url, Err: errors.New("local template must be a directory")}
		}
		if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
			absPath = resolved
		}
		tmpl.URL = absPath
		tmpl.LocalPath = absPath
	}

	if err := tmpl.loadConfig(); err != nil {
		if tmpl.Temporary {
			tmpl.Cleanup()
		}
		return nil, err
	}

	return tmpl, nil
}

// Cleanup removes temporary template checkouts created while loading Git templates.
func (t *Template) Cleanup() {
	if t == nil || !t.Temporary || t.LocalPath == "" {
		return
	}
	if t.mirror != "" {
		removeWorktree(t.mirror, t.LocalPath)
	}
	_ = os.RemoveAll(t.LocalPath)
	t.Temporary = false
}

// loadConfig reads and parses copier.yml/copier.yaml from the template root.
func (t *Template) loadConfig() error {
	root := t.LocalPath

	var confPaths []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return &TemplateError{Path: root, Err: err}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "copier.") {
			ext := strings.ToLower(strings.TrimPrefix(name, "copier"))
			if ext == ".yml" || ext == ".yaml" {
				confPaths = append(confPaths, filepath.Join(root, name))
			}
		}
	}
	if len(confPaths) > 1 {
		return &TemplateError{Path: root, Err: ErrMultipleConfigs}
	}

	raw := map[string]any{}
	var keys []string
	if len(confPaths) == 1 {
		keys, raw, err = loadTemplateConfig(confPaths[0])
		if err != nil {
			return &TemplateError{Path: confPaths[0], Err: err}
		}
	}
	t.rawConfig = raw

	if err := t.applyConfig(raw); err != nil {
		p := root
		if len(confPaths) == 1 {
			p = confPaths[0]
		}
		return &TemplateError{Path: p, Err: err}
	}

	// Parse questions in file order.
	t.Questions = nil
	for _, k := range keys {
		if strings.HasPrefix(k, "_") {
			continue
		}
		q := QuestionDef{Name: k}
		switch v := raw[k].(type) {
		case map[string]any:
			fillQuestionDef(&q, v)
		default:
			q.Default = raw[k]
			q.HasDefault = true
		}
		if t.IsSecretConfigured(k) {
			q.Secret = true
		}
		t.Questions = append(t.Questions, q)
	}
	return nil
}

func (t *Template) applyConfig(cfg map[string]any) error {
	c := TemplateConfig{
		TemplateSuffix: DefaultTemplateSuffix,
		AnswersFile:    AnswersFileName,
	}
	if v, ok := cfgString(cfg, "_answers_file"); ok && v != "" {
		if path.IsAbs(filepath.ToSlash(v)) || filepath.IsAbs(v) {
			return fmt.Errorf("%w: _answers_file must be a relative path", ErrConfig)
		}
		c.AnswersFile = v
	}
	if v, ok := cfgString(cfg, "_subdirectory"); ok {
		c.Subdirectory = v
	}
	if v, ok := cfg["_templates_suffix"]; ok {
		if v == nil {
			c.TemplateSuffix = DefaultTemplateSuffix
		} else {
			c.TemplateSuffix = fmt.Sprintf("%v", v)
		}
	}
	if v, ok := cfgStringList(cfg, "_exclude"); ok {
		c.Exclude = v
		c.ExcludeSet = true
	}
	if v, ok := cfgStringList(cfg, "_skip_if_exists"); ok {
		c.SkipIfExists = v
	}
	if v, ok := cfgStringList(cfg, "_jinja_extensions"); ok {
		c.JinjaExtensions = v
	}
	if v, ok := cfgStringList(cfg, "_secret_questions"); ok {
		c.SecretQuestions = v
	}
	if v, ok := cfgBool(cfg, "_preserve_symlinks"); ok {
		c.PreserveSymlinks = v
	}
	if v, ok := cfgString(cfg, "_min_copier_version"); ok {
		c.MinCopierVersion = v
	}
	for key, dst := range map[string]*string{
		"_message_before_copy":   &c.MessageBeforeCopy,
		"_message_after_copy":    &c.MessageAfterCopy,
		"_message_before_update": &c.MessageBeforeUpdate,
		"_message_after_update":  &c.MessageAfterUpdate,
	} {
		if v, ok := cfgString(cfg, key); ok {
			*dst = v
		}
	}
	if v, ok := cfg["_envops"]; ok && v != nil {
		b, err := yaml.Marshal(v)
		if err != nil {
			return fmt.Errorf("%w: invalid _envops: %v", ErrConfig, err)
		}
		if err := yaml.Unmarshal(b, &c.Envops); err != nil {
			return fmt.Errorf("%w: invalid _envops: %v", ErrConfig, err)
		}
	}
	if v, ok := cfg["_external_data"].(map[string]any); ok {
		c.ExternalData = make(map[string]string, len(v))
		for k, p := range v {
			c.ExternalData[k] = fmt.Sprintf("%v", p)
		}
	}
	if v, ok := cfg["_tasks"]; ok && v != nil {
		list, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%w: _tasks must be a list", ErrConfig)
		}
		for _, item := range list {
			task, err := parseTaskDef(item)
			if err != nil {
				return err
			}
			task.ExtraVars = map[string]any{"stage": "task"}
			c.Tasks = append(c.Tasks, task)
		}
	}
	if v, ok := cfg["_migrations"]; ok && v != nil {
		list, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%w: _migrations must be a list", ErrConfig)
		}
		for _, item := range list {
			m, err := parseMigrationDef(item)
			if err != nil {
				return err
			}
			c.Migrations = append(c.Migrations, m)
		}
	}
	t.Config = c

	if err := t.validateSubdirectory(); err != nil {
		return err
	}
	return t.validateEnvops()
}

func cfgString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", ok && v == nil
	}
	return fmt.Sprintf("%v", v), true
}

func cfgStringList(m map[string]any, key string) ([]string, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	switch l := v.(type) {
	case nil:
		return []string{}, true
	case []any:
		return anyListToStrings(l), true
	case []string:
		return l, true
	case string:
		return []string{l}, true
	}
	return []string{fmt.Sprintf("%v", v)}, true
}

func cfgBool(m map[string]any, key string) (bool, bool) {
	v, ok := m[key]
	if !ok {
		return false, false
	}
	return castToBool(v), true
}

func fillQuestionDef(q *QuestionDef, m map[string]any) {
	if v, ok := m["type"]; ok && v != nil {
		q.Type = fmt.Sprintf("%v", v)
	}
	if v, ok := m["help"]; ok && v != nil {
		q.Help = fmt.Sprintf("%v", v)
	}
	if v, ok := m["choices"]; ok {
		q.Choices = v
	}
	if v, ok := m["multiselect"]; ok {
		q.Multiselect = castToBool(v)
	}
	if v, ok := m["default"]; ok {
		q.Default = v
		q.HasDefault = true
	}
	if v, ok := m["secret"]; ok {
		q.Secret = castToBool(v)
	}
	if v, ok := m["multiline"]; ok {
		q.Multiline = v
	}
	if v, ok := m["placeholder"]; ok && v != nil {
		q.Placeholder = fmt.Sprintf("%v", v)
	}
	if v, ok := m["validator"]; ok && v != nil {
		q.Validator = fmt.Sprintf("%v", v)
	}
	if v, ok := m["when"]; ok {
		q.When = v
	}
	if v, ok := m["qmark"]; ok && v != nil {
		q.Qmark = fmt.Sprintf("%v", v)
	}
}

// inferType mirrors upstream: the Python type name of the default when it is a
// bool/int/float/str, otherwise "yaml".
func inferType(val any) QuestionType {
	switch val.(type) {
	case bool:
		return TypeBool
	case int, int64:
		return TypeInt
	case float64:
		return TypeFloat
	case string:
		return TypeStr
	default:
		return TypeYAML
	}
}

// CopyRoot returns the root directory for template files, accounting for
// the raw (unrendered) subdirectory.
func (t *Template) CopyRoot() string {
	if t.Config.Subdirectory != "" {
		return filepath.Clean(filepath.Join(t.LocalPath, t.Config.Subdirectory))
	}
	return t.LocalPath
}

func (t *Template) validateSubdirectory() error {
	if t.Config.Subdirectory == "" || strings.Contains(t.Config.Subdirectory, "{") {
		return nil
	}
	if filepath.IsAbs(t.Config.Subdirectory) {
		return fmt.Errorf("%w: _subdirectory %q must be relative", ErrForbiddenPath, t.Config.Subdirectory)
	}
	ok, err := pathutil.IsWithin(t.LocalPath, filepath.Join(t.LocalPath, t.Config.Subdirectory))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: _subdirectory %q escapes template root", ErrForbiddenPath, t.Config.Subdirectory)
	}
	return nil
}

func (t *Template) validateEnvops() error {
	switch t.Config.Envops.Undefined {
	case "", "jinja2.Undefined", "jinja2.StrictUndefined":
		return nil
	default:
		return fmt.Errorf("%w: unsupported envops.undefined value %q; supported values are \"jinja2.Undefined\" and \"jinja2.StrictUndefined\"", ErrConfig, t.Config.Envops.Undefined)
	}
}

// Exclusions returns the exclude patterns from the template config, or the
// defaults. Like upstream, the defaults are empty when `_subdirectory` points
// to a real subdirectory.
func (t *Template) Exclusions() []string {
	if t.Config.ExcludeSet {
		return t.Config.Exclude
	}
	sub := filepath.ToSlash(t.Config.Subdirectory)
	if sub == "" || path.Clean(sub) == "." {
		return DefaultExclude
	}
	return []string{}
}

// Metadata returns template metadata to embed in the answers file.
func (t *Template) Metadata() map[string]any {
	m := map[string]any{
		"_src_path": t.URL,
	}
	if t.CommitDescription != "" {
		m["_commit"] = t.CommitDescription
	}
	return m
}

// Version returns the PEP 440-style version detected from the checked-out
// commit, or nil when the template is not versioned.
func (t *Template) Version() *templateVersion {
	if t == nil {
		return nil
	}
	if !t.versionParsed {
		t.versionParsed = true
		if t.CommitDescription != "" {
			t.version = versionFromCommit(t.CommitDescription)
		}
	}
	return t.version
}

// MinVersion returns the parsed minimum copier version, or nil if not set.
func (t *Template) MinVersion() *templateVersion {
	if t.Config.MinCopierVersion == "" {
		return nil
	}
	v, err := parseTemplateVersion(t.Config.MinCopierVersion)
	if err != nil {
		return nil
	}
	return v
}

// QuestionNames returns the set of question names defined by the template.
func (t *Template) QuestionNames() map[string]bool {
	names := make(map[string]bool, len(t.Questions))
	for _, q := range t.Questions {
		names[q.Name] = true
	}
	return names
}

// MigrationTasks returns the migration tasks for a stage ("before" or "after")
// when updating from the given template version.
func (t *Template) MigrationTasks(stage string, from *Template) []TaskDef {
	if from == nil || t.Version() == nil || from.Version() == nil {
		return nil
	}
	toVer, fromVer := t.Version(), from.Version()
	baseVars := map[string]any{
		"stage":               stage,
		"version_from":        from.CommitDescription,
		"version_to":          t.CommitDescription,
		"version_pep440_from": fromVer.String(),
		"version_pep440_to":   toVer.String(),
	}
	eo := fillEnvopsDefaults(t.Config.Envops)
	defaultCondition := fmt.Sprintf(`%s _stage == "after" %s`, eo.VariableStartString, eo.VariableEndString)

	var result []TaskDef
	for _, m := range t.Config.Migrations {
		extra := make(map[string]any, len(baseVars)+2)
		for k, v := range baseVars {
			extra[k] = v
		}
		if m.Version != "" {
			current, err := parseTemplateVersion(m.Version)
			if err != nil {
				continue
			}
			// Only when new version >= migration version > old version.
			if toVer.Compare(current) < 0 || !current.GreaterThan(fromVer) {
				continue
			}
			extra["version_current"] = m.Version
			extra["version_pep440_current"] = current.String()
		}
		if m.Legacy {
			fmt.Fprintln(os.Stderr, "This migration configuration is deprecated. Please switch to the new format.")
			var tasks []TaskDef
			if stage == "before" {
				tasks = m.Before
			} else {
				tasks = m.After
			}
			for _, task := range tasks {
				task.ExtraVars = extra
				result = append(result, task)
			}
			continue
		}
		cond := m.When
		if cond == nil {
			cond = defaultCondition
		}
		wd := m.WorkingDirectory
		if wd == "" {
			wd = "."
		}
		result = append(result, TaskDef{Cmd: m.Command, Condition: cond, WorkingDirectory: wd, ExtraVars: extra})
	}
	return result
}

// IsSecretConfigured reports whether the name is listed in `_secret_questions`.
func (t *Template) IsSecretConfigured(name string) bool {
	for _, s := range t.Config.SecretQuestions {
		if s == name {
			return true
		}
	}
	return false
}

// IsSecret reports whether the named question must not be saved to the answers file.
func (t *Template) IsSecret(name string) bool {
	if t.IsSecretConfigured(name) {
		return true
	}
	for _, q := range t.Questions {
		if q.Name == name {
			return q.Secret
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// --- copier.yml loading -----------------------------------------------------

const maxIncludeDepth = 32

// mergedListOptions are concatenated across documents instead of overridden.
var mergedListOptions = []string{"_exclude", "_jinja_extensions", "_secret_questions", "_skip_if_exists"}

// loadTemplateConfig loads a copier.yml file like upstream Copier: it supports
// multiple YAML documents and the `!include` tag (with globs, restricted to
// the template root), concatenates list options across documents and lets
// later documents override earlier ones otherwise. Keys are returned in order
// of first appearance so questions are asked in definition order.
func loadTemplateConfig(confPath string) ([]string, map[string]any, error) {
	root, err := pathutil.Resolve(filepath.Dir(confPath))
	if err != nil {
		return nil, nil, err
	}
	nodes, err := loadYAMLDocNodes(confPath, root, 0)
	if err != nil {
		return nil, nil, err
	}

	var keys []string
	values := make(map[string]any)
	merged := make(map[string][]any)
	seen := make(map[string]bool)

	for _, n := range flattenYAMLNodes(nodes) {
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		}
		if n.Kind != yaml.MappingNode {
			return nil, nil, fmt.Errorf("%w: each configuration document must be a mapping", ErrConfig)
		}
		reorderChoiceMappings(n)
		doc := make(map[string]any)
		if err := n.Decode(&doc); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrConfig, err)
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if k == "<<" {
				continue
			}
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		for k, v := range doc {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
			values[k] = v
		}
		for _, opt := range mergedListOptions {
			if v, ok := doc[opt]; ok {
				switch l := v.(type) {
				case []any:
					merged[opt] = append(merged[opt], l...)
				case nil:
				default:
					merged[opt] = append(merged[opt], l)
				}
			}
		}
	}
	for opt, list := range merged {
		values[opt] = list
	}
	return keys, values, nil
}

func loadYAMLDocNodes(file, root string, depth int) ([]*yaml.Node, error) {
	if depth > maxIncludeDepth {
		return nil, fmt.Errorf("%w: too many nested YAML includes", ErrConfig)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var nodes []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrConfig, file, err)
		}
		if n.Kind == 0 {
			continue
		}
		content := &n
		if n.Kind == yaml.DocumentNode {
			if len(n.Content) == 0 {
				continue
			}
			content = n.Content[0]
		}
		if err := expandIncludes(content, root, depth); err != nil {
			return nil, err
		}
		nodes = append(nodes, content)
	}
	return nodes, nil
}

// expandIncludes replaces `!include <glob>` scalars with a sequence holding
// the documents of every matched file.
func expandIncludes(n *yaml.Node, root string, depth int) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!include" {
		pattern := n.Value
		if path.IsAbs(filepath.ToSlash(pattern)) || filepath.IsAbs(pattern) {
			return fmt.Errorf("%w: YAML include file path must be a relative path", ErrConfig)
		}
		matches, err := globRelative(root, pattern)
		if err != nil {
			return fmt.Errorf("%w: invalid include pattern %q: %v", ErrConfig, pattern, err)
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, m := range matches {
			resolved, err := pathutil.Resolve(m)
			if err != nil {
				return err
			}
			ok, err := pathutil.IsSubpath(root, resolved)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: YAML include file path %s must be inside the template directory", ErrForbiddenPath, resolved)
			}
			info, err := os.Stat(resolved)
			if err != nil || info.IsDir() {
				continue
			}
			docs, err := loadYAMLDocNodes(resolved, root, depth+1)
			if err != nil {
				return err
			}
			seq.Content = append(seq.Content, flattenYAMLNodes(docs)...)
		}
		*n = *seq
		return nil
	}
	for _, c := range n.Content {
		if err := expandIncludes(c, root, depth); err != nil {
			return err
		}
	}
	return nil
}

// flattenYAMLNodes flattens nested sequences and drops null documents, like
// upstream's `lflatten(filter(None, ...))`.
func flattenYAMLNodes(nodes []*yaml.Node) []*yaml.Node {
	var out []*yaml.Node
	for _, n := range nodes {
		if n == nil {
			continue
		}
		target := n
		if target.Kind == yaml.AliasNode && target.Alias != nil {
			target = target.Alias
		}
		switch target.Kind {
		case yaml.SequenceNode:
			out = append(out, flattenYAMLNodes(target.Content)...)
		case yaml.ScalarNode:
			if target.Tag == "!!null" || (target.Tag == "" && target.Value == "") {
				continue
			}
			out = append(out, n)
		default:
			out = append(out, n)
		}
	}
	return out
}

// reorderChoiceMappings converts dict-style question choices into ordered
// [name, value] pairs so their definition order survives decoding into Go maps.
func reorderChoiceMappings(doc *yaml.Node) {
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i].Value
		if strings.HasPrefix(key, "_") {
			continue
		}
		q := doc.Content[i+1]
		if q.Kind == yaml.AliasNode && q.Alias != nil {
			q = q.Alias
		}
		if q.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(q.Content); j += 2 {
			if q.Content[j].Value != "choices" {
				continue
			}
			choices := q.Content[j+1]
			src := choices
			if src.Kind == yaml.AliasNode && src.Alias != nil {
				src = src.Alias
			}
			if src.Kind != yaml.MappingNode {
				continue
			}
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for k := 0; k+1 < len(src.Content); k += 2 {
				pair := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{src.Content[k], src.Content[k+1]}}
				seq.Content = append(seq.Content, pair)
			}
			q.Content[j+1] = seq
		}
	}
}

// sortedStrings returns a sorted copy of the given slice.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
