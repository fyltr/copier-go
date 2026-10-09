package copier

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// scriptedPrompter answers questions from a map, falling back to defaults.
type scriptedPrompter struct {
	answers  map[string]any
	asked    []string
	confirm  bool
	confirms []string
}

func (p *scriptedPrompter) Ask(q *Question) (any, error) {
	p.asked = append(p.asked, q.Name())
	if v, ok := p.answers[q.Name()]; ok {
		return q.ParseAnswer(v)
	}
	def, present, err := q.Default()
	if err != nil {
		return nil, err
	}
	if present {
		return def, nil
	}
	return nil, ErrInteractiveNeeded
}

func (p *scriptedPrompter) Confirm(msg string, def bool) (bool, error) {
	p.confirms = append(p.confirms, msg)
	return p.confirm, nil
}

func writeTree(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWriteFile(t, path, []byte(content), 0o644)
	}
}

func readFile(t testing.TB, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestLoadTemplate_TaskAndMigrationFormats(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"copier.yml": `
_tasks:
  - "echo hi"
  - ["echo", "there"]
  - command: "echo cond"
    when: "{{ true }}"
    working_directory: sub
_migrations:
  - version: v1.0.0
    before: ["echo b"]
    after: ["echo a"]
  - command: "echo new"
    when: "{{ _stage == 'before' }}"
  - "echo plain"
`})
	tmpl, err := LoadTemplate(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	tasks := tmpl.Config.Tasks
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].Cmd != "echo hi" {
		t.Errorf("unexpected task 0: %v", tasks[0].Cmd)
	}
	if list, ok := tasks[1].Cmd.([]string); !ok || len(list) != 2 || list[0] != "echo" {
		t.Errorf("unexpected task 1: %v", tasks[1].Cmd)
	}
	if tasks[2].Condition != "{{ true }}" || tasks[2].WorkingDirectory != "sub" {
		t.Errorf("unexpected task 2: %+v", tasks[2])
	}
	migrations := tmpl.Config.Migrations
	if len(migrations) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(migrations))
	}
	if !migrations[0].Legacy || migrations[0].Version != "v1.0.0" || len(migrations[0].Before) != 1 || len(migrations[0].After) != 1 {
		t.Errorf("unexpected legacy migration: %+v", migrations[0])
	}
	if migrations[1].Legacy || migrations[1].When != "{{ _stage == 'before' }}" || migrations[1].Command != "echo new" {
		t.Errorf("unexpected migration 1: %+v", migrations[1])
	}
	if migrations[2].Command != "echo plain" || migrations[2].When != nil {
		t.Errorf("unexpected migration 2: %+v", migrations[2])
	}
}

func TestTemplate_MigrationTasks(t *testing.T) {
	from := &Template{CommitDescription: "v1.0.0"}
	to := &Template{CommitDescription: "v2.0.0"}
	to.Config.Migrations = []MigrationDef{
		{Version: "v1.5.0", Command: "echo in-range"},
		{Version: "v0.5.0", Command: "echo too-old"},
		{Version: "v2.0.0", Command: "echo boundary", When: "{{ _stage == 'before' }}"},
		{Version: "v2.1.0", Command: "echo too-new"},
		{Command: "echo always"},
		{Version: "v1.1.0", Legacy: true, Before: []TaskDef{{Cmd: "echo legacy-before"}}, After: []TaskDef{{Cmd: "echo legacy-after"}}},
	}
	names := func(tasks []TaskDef) []string {
		out := make([]string, 0, len(tasks))
		for _, task := range tasks {
			out = append(out, task.CmdString())
		}
		return out
	}
	before := names(to.MigrationTasks("before", from))
	after := names(to.MigrationTasks("after", from))
	wantBefore := []string{"echo in-range", "echo boundary", "echo always", "echo legacy-before"}
	wantAfter := []string{"echo in-range", "echo boundary", "echo always", "echo legacy-after"}
	if strings.Join(before, "|") != strings.Join(wantBefore, "|") {
		t.Errorf("before: got %v, want %v", before, wantBefore)
	}
	if strings.Join(after, "|") != strings.Join(wantAfter, "|") {
		t.Errorf("after: got %v, want %v", after, wantAfter)
	}
	tasks := to.MigrationTasks("after", from)
	if tasks[0].Condition != `{{ _stage == "after" }}` {
		t.Errorf("unexpected default condition: %v", tasks[0].Condition)
	}
	if tasks[0].ExtraVars["version_current"] != "v1.5.0" || tasks[0].ExtraVars["version_pep440_from"] != "1.0.0" || tasks[0].ExtraVars["stage"] != "after" {
		t.Errorf("unexpected extra vars: %v", tasks[0].ExtraVars)
	}
}

func TestLoadTemplate_QuestionAndChoiceOrder(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"copier.yml": `
zeta:
  type: str
  default: z
alpha:
  type: str
  choices:
    Zed: z
    Alpha: a
    Mid: m
  default: a
`})
	tmpl, err := LoadTemplate(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.Questions) != 2 || tmpl.Questions[0].Name != "zeta" || tmpl.Questions[1].Name != "alpha" {
		t.Fatalf("questions out of order: %+v", tmpl.Questions)
	}
	q := newQuestion(tmpl.Questions[1], NewAnswersMap(), &Settings{}, NewRenderer(nil, dir), map[string]any{})
	choices, err := q.Choices()
	if err != nil {
		t.Fatal(err)
	}
	got := resolveChoiceLabels(choices)
	if strings.Join(got, ",") != "Zed,Alpha,Mid" {
		t.Fatalf("choices out of order: %v", got)
	}
}

func TestLoadTemplate_MultiDocumentAndInclude(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"copier.yml": `---
!include shared/*.yml
---
_skip_if_exists:
  - .password.txt
custom: default answer
_exclude:
  - "*.bak"
`,
		"shared/common.yml": `
version:
  type: str
  default: "1.0"
_skip_if_exists:
  - pyproject.toml
_exclude:
  - "*.tmp"
`,
	})
	tmpl, err := LoadTemplate(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.Questions) != 2 || tmpl.Questions[0].Name != "version" || tmpl.Questions[1].Name != "custom" {
		t.Fatalf("unexpected questions: %+v", tmpl.Questions)
	}
	if strings.Join(tmpl.Config.SkipIfExists, ",") != "pyproject.toml,.password.txt" {
		t.Fatalf("unexpected skip_if_exists: %v", tmpl.Config.SkipIfExists)
	}
	if !tmpl.Config.ExcludeSet || strings.Join(tmpl.Config.Exclude, ",") != "*.tmp,*.bak" {
		t.Fatalf("unexpected exclude: %v", tmpl.Config.Exclude)
	}
}

func TestLoadTemplate_IncludeOutsideRootForbidden(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "tpl")
	writeTree(t, parent, map[string]string{
		"outside.yml":    "leaked: true\n",
		"tpl/copier.yml": "!include ../outside.yml\n",
	})
	_, err := LoadTemplate(dir, "", false)
	if !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("expected ErrForbiddenPath, got %v", err)
	}
	writeTree(t, dir, map[string]string{"copier.yml": "!include /etc/passwd\n"})
	if _, err := LoadTemplate(dir, "", false); !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig for absolute include, got %v", err)
	}
}

func TestCopy_ExcludeMultilineConditional(t *testing.T) {
	for _, skip := range []bool{true, false} {
		src := t.TempDir()
		dst := t.TempDir()
		writeTree(t, src, map[string]string{
			"copier.yml": `
skip:
    type: bool

_exclude:
    - copier.yml
    - |
        {% if skip %}
        /a.txt
        /b.txt
        /sub/c.txt
        {% endif %}
`,
			"a.txt":     "",
			"b.txt":     "",
			"sub/c.txt": "",
			"keep.txt":  "",
		})
		if err := Copy(src, dst, WithQuiet(true), WithData(map[string]any{"skip": skip}), WithDefaults(true)); err != nil {
			t.Fatal(err)
		}
		if !exists(filepath.Join(dst, "keep.txt")) {
			t.Fatal("keep.txt should always be copied")
		}
		for _, f := range []string{"a.txt", "b.txt", "sub/c.txt"} {
			if exists(filepath.Join(dst, f)) == skip {
				t.Errorf("skip=%v: unexpected presence of %s", skip, f)
			}
		}
		if exists(filepath.Join(dst, "copier.yml")) {
			t.Error("copier.yml should be excluded")
		}
	}
}

func TestCopy_ExcludeOptionExtendsTemplate(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"a.txt": "a", "b.txt": "b", "c.log": "c"})
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithExclude("b.txt", "*.log")); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dst, "a.txt")) || exists(filepath.Join(dst, "b.txt")) || exists(filepath.Join(dst, "c.log")) {
		t.Fatal("WithExclude patterns were not applied")
	}
}

func TestCopy_TemplatedSubdirectory(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml":        "_subdirectory: \"{{ 'tpl' }}\"\n",
		"tpl/file.txt":      "content",
		"tpl/README.md":     "# readme",
		"docs/notes.txt":    "not copied",
		"tpl/inner/x.jinja": "{{ _copier_conf.answers_file }}",
	})
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dst, "file.txt")) || exists(filepath.Join(dst, "docs")) {
		t.Fatal("subdirectory not honoured")
	}
	if got := readFile(t, filepath.Join(dst, "inner", "x")); got != ".copier-answers.yml" {
		t.Fatalf("unexpected rendered content: %q", got)
	}
}

func TestCopy_AskForcesPrompt(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": `
animal_name:
    type: str
    default: mouse
animal_sound:
    type: str
    default: squeak
what_does_it_eat:
    type: str
    default: cheese
`})
	prompter := &scriptedPrompter{answers: map[string]any{"what_does_it_eat": "meat"}}
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithAsk("*it*"), WithPrompter(prompter),
		WithData(map[string]any{"animal_sound": "woof"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(prompter.asked, ",") != "what_does_it_eat" {
		t.Fatalf("unexpected prompts: %v", prompter.asked)
	}
	answers, err := LoadAnswersFile(filepath.Join(dst, ".copier-answers.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if answers["what_does_it_eat"] != "meat" || answers["animal_name"] != "mouse" || answers["animal_sound"] != "woof" {
		t.Fatalf("unexpected answers: %v", answers)
	}
}

func TestCopy_InvalidChoiceError(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": `
cloud:
    type: str
    choices: [Any, AWS]
iac:
    type: str
    default: tf
    choices:
        Terraform: tf
        Cloud Formation:
            value: cf
            validator: "{% if cloud != 'AWS' %}Requires AWS{% endif %}"
`})
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(map[string]any{"cloud": "GCP"}))
	if !errors.Is(err, ErrInvalidChoice) {
		t.Fatalf("expected ErrInvalidChoice, got %v", err)
	}
	if !strings.Contains(err.Error(), "Invalid choice for 'cloud': 'GCP' is not in ['Any', 'AWS']") {
		t.Fatalf("unexpected message: %v", err)
	}
	err = Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(map[string]any{"cloud": "Any", "iac": "cf"}))
	if err == nil || !strings.Contains(err.Error(), "Requires AWS") {
		t.Fatalf("expected disabled choice error, got %v", err)
	}
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(map[string]any{"cloud": "AWS", "iac": "cf"})); err != nil {
		t.Fatal(err)
	}
}

func TestCopy_WhenFalseHidesAnswer(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml": `
use_db:
    type: bool
    default: false
db_name:
    type: str
    default: mydb
    when: "{{ use_db }}"
token:
    type: str
    secret: true
    default: s3cret
`,
		"out.txt.jinja": "{{ db_name }}:{{ token }}",
	})
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "out.txt")); got != "mydb:s3cret" {
		t.Fatalf("unexpected content: %q", got)
	}
	answers := readFile(t, filepath.Join(dst, ".copier-answers.yml"))
	if strings.Contains(answers, "db_name") || strings.Contains(answers, "token") {
		t.Fatalf("hidden/secret answers leaked: %s", answers)
	}
	if !strings.Contains(answers, "use_db: false") {
		t.Fatalf("expected use_db in answers: %s", answers)
	}
}

func TestCopy_RequiredQuestionWithDefaults(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": "name:\n    type: str\n"})
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true))
	if !errors.Is(err, ErrQuestionRequired) {
		t.Fatalf("expected ErrQuestionRequired, got %v", err)
	}
}

func TestCopy_AnswersFileTemplate(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml":                            "_answers_file: .config/answers.yml\nname:\n    type: str\n    default: demo\nlist:\n    type: yaml\n    default: [a, b]\n",
		"{{ _copier_conf.answers_file }}.jinja": "# custom header\n{{ _copier_answers|to_nice_yaml -}}\n",
	})
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(dst, ".config", "answers.yml"))
	if !strings.HasPrefix(content, "# custom header\n") {
		t.Fatalf("template answers file not used: %q", content)
	}
	if !strings.Contains(content, "_src_path:") || !strings.Contains(content, "name: demo") || !strings.Contains(content, "list:\n    - a\n") {
		t.Fatalf("unexpected answers content: %q", content)
	}
	if exists(filepath.Join(dst, ".copier-answers.yml")) {
		t.Fatal("default answers file should not be written when a custom one is configured")
	}
	// Recopy locates the answers file through the option, like upstream.
	if err := Recopy(dst, WithQuiet(true), WithDefaults(true), WithOverwrite(true), WithAnswersFile(".config/answers.yml")); err != nil {
		t.Fatal(err)
	}
}

func TestCopy_YieldPaths(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml": `
commands:
    type: yaml
    default:
      - name: init
        subs: [config, database]
      - name: run
        subs: [server]
`,
		"commands/{% yield cmd from commands %}{{ cmd.name }}{% endyield %}/{% yield sub from cmd.subs %}{{ sub }}{% endyield %}.py.jinja": `print("{{ sub }} in {{ cmd.name }}")`,
		"commands/{% yield cmd from commands %}{{ cmd.name }}{% endyield %}/__init__.py":                                                   "",
	})
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "commands", "init", "config.py")); got != `print("config in init")` {
		t.Fatalf("unexpected content: %q", got)
	}
	for _, f := range []string{"commands/init/database.py", "commands/run/server.py", "commands/init/__init__.py", "commands/run/__init__.py"} {
		if !exists(filepath.Join(dst, filepath.FromSlash(f))) {
			t.Errorf("missing %s", f)
		}
	}
}

func TestCopy_YieldInFileForbidden(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"x.txt.jinja": "{% yield a from items %}{{ a }}{% endyield %}"})
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true))
	if !errors.Is(err, ErrYieldInFile) {
		t.Fatalf("expected ErrYieldInFile, got %v", err)
	}
}

func TestCopy_TasksRunWithContext(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": `
name:
    type: str
    default: demo
_tasks:
    - "echo $STAGE > stage.txt"
    - ["sh", "-c", "echo {{ name }}-{{ _copier_operation }} > name.txt"]
    - command: "touch skipped.txt"
      when: "{{ false }}"
    - command: "touch inner.txt"
      working_directory: sub
`, "sub/.keep": ""})
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true))
	var unsafeErr *UnsafeTemplateError
	if !errors.As(err, &unsafeErr) || strings.Join(unsafeErr.Features, ",") != "tasks" {
		t.Fatalf("expected unsafe template error for tasks, got %v", err)
	}
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithUnsafe(true)); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dst, "stage.txt"))); got != "task" {
		t.Errorf("unexpected STAGE: %q", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dst, "name.txt"))); got != "demo-copy" {
		t.Errorf("unexpected name.txt: %q", got)
	}
	if exists(filepath.Join(dst, "skipped.txt")) {
		t.Error("conditional task should not have run")
	}
	if !exists(filepath.Join(dst, "sub", "inner.txt")) {
		t.Error("working_directory not honoured")
	}
}

func TestCopy_TrustedTemplateFromSettings(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": "_tasks:\n  - touch ran.txt\n"})
	resolved, _ := filepath.EvalSymlinks(src)
	settings := filepath.Join(t.TempDir(), "settings.yml")
	mustWriteFile(t, settings, []byte("trust:\n  - "+resolved+"\n"), 0o644)
	t.Setenv("COPIER_SETTINGS_PATH", settings)
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dst, "ran.txt")) {
		t.Fatal("trusted task did not run")
	}
}

func TestCopy_IncludeOutsideTemplateForbidden(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "tpl")
	writeTree(t, parent, map[string]string{
		"secret.txt":        "SECRET",
		"tpl/partial.txt":   "partial",
		"tpl/ok.txt.jinja":  `{% include "partial.txt" %}`,
		"tpl/bad.txt.jinja": `{% include "../secret.txt" %}`,
	})
	dst := t.TempDir()
	// Exclude patterns match rendered destination paths (without the template suffix).
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithExclude("bad.txt")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "ok.txt")); got != "partial" {
		t.Fatalf("unexpected include output: %q", got)
	}
	err := Copy(src, t.TempDir(), WithQuiet(true), WithDefaults(true))
	if !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("expected ErrForbiddenPath for ../ include, got %v", err)
	}
}

func TestCopy_SymlinkOutsideTemplateForbidden(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "tpl")
	writeTree(t, parent, map[string]string{
		"secret.txt":         "SECRET",
		"tpl/copier.yml":     "_preserve_symlinks: false\n",
		"tpl/link.txt.jinja": `{% include "link.txt" %}`,
	})
	if err := os.Symlink(filepath.Join(parent, "secret.txt"), filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	// Without preserved symlinks, a symlink escaping the template is rejected outright.
	err := Copy(src, t.TempDir(), WithQuiet(true), WithDefaults(true))
	if !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("expected ErrForbiddenPath for escaping symlink, got %v", err)
	}
	// With preserved symlinks the link itself is fine, but including it is not.
	writeTree(t, src, map[string]string{"copier.yml": "_preserve_symlinks: true\n"})
	err = Copy(src, t.TempDir(), WithQuiet(true), WithDefaults(true))
	if !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("expected ErrForbiddenPath for symlinked include, got %v", err)
	}
	dst := t.TempDir()
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithExclude("link.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestCopy_IdenticalAndConflictHandling(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"same.txt": "same", "diff.txt": "new"})
	writeTree(t, dst, map[string]string{"same.txt": "same", "diff.txt": "old"})

	prompter := &scriptedPrompter{confirm: false}
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithPrompter(prompter)); err != nil {
		t.Fatal(err)
	}
	if len(prompter.confirms) != 1 || !strings.Contains(prompter.confirms[0], "diff.txt") {
		t.Fatalf("expected one overwrite prompt for diff.txt, got %v", prompter.confirms)
	}
	if readFile(t, filepath.Join(dst, "diff.txt")) != "old" {
		t.Fatal("file should not be overwritten when declined")
	}
	prompter.confirm = true
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithPrompter(prompter)); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dst, "diff.txt")) != "new" {
		t.Fatal("file should be overwritten when accepted")
	}
	// Skip-if-exists patterns win over prompting.
	writeTree(t, dst, map[string]string{"diff.txt": "old"})
	prompter.confirms = nil
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithPrompter(prompter), WithSkip("diff.txt")); err != nil {
		t.Fatal(err)
	}
	if len(prompter.confirms) != 0 || readFile(t, filepath.Join(dst, "diff.txt")) != "old" {
		t.Fatal("skip-if-exists file should be kept without prompting")
	}
}

func TestCopy_DefaultsRequirePromptForConflicts(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"diff.txt": "new"})
	writeTree(t, dst, map[string]string{"diff.txt": "old"})
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithPrompter(nonInteractivePrompter{}))
	if !errors.Is(err, ErrInteractiveNeeded) {
		t.Fatalf("expected ErrInteractiveNeeded, got %v", err)
	}
}

type nonInteractivePrompter struct{}

func (nonInteractivePrompter) Ask(q *Question) (any, error) { return nil, ErrInteractiveNeeded }
func (nonInteractivePrompter) Confirm(string, bool) (bool, error) {
	return false, ErrInteractiveNeeded
}

func TestCopy_SkipAnsweredAndLastAnswers(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": "name:\n    type: str\n    default: first\n", "out.jinja": "{{ name }}"})
	if err := Copy(src, dst, WithQuiet(true), WithData(map[string]any{"name": "custom"})); err != nil {
		t.Fatal(err)
	}
	// A second copy into the same destination reuses the recorded answer as default.
	prompter := &scriptedPrompter{}
	if err := Copy(src, dst, WithQuiet(true), WithOverwrite(true), WithPrompter(prompter)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "out")); got != "custom" {
		t.Fatalf("expected last answer to be reused, got %q", got)
	}
	prompter = &scriptedPrompter{}
	if err := Recopy(dst, WithQuiet(true), WithOverwrite(true), WithSkipAnswered(true), WithPrompter(prompter)); err != nil {
		t.Fatal(err)
	}
	if len(prompter.asked) != 0 {
		t.Fatalf("skip-answered should not prompt, asked %v", prompter.asked)
	}
}

func TestQuestion_CastSemantics(t *testing.T) {
	r := NewRenderer(nil, "")
	mk := func(def QuestionDef) *Question {
		return newQuestion(def, NewAnswersMap(), &Settings{}, r, map[string]any{})
	}
	if v, err := mk(QuestionDef{Name: "b", Type: "bool"}).ParseAnswer(" "); err != nil || v != false {
		t.Errorf("blank bool should be false, got %v %v", v, err)
	}
	if v, err := mk(QuestionDef{Name: "b", Type: "bool"}).ParseAnswer("Y"); err != nil || v != true {
		t.Errorf("Y should be true, got %v %v", v, err)
	}
	if v, err := mk(QuestionDef{Name: "i", Type: "int"}).ParseAnswer("42"); err != nil || v != int64(42) {
		t.Errorf("int parse failed: %v %v", v, err)
	}
	if _, err := mk(QuestionDef{Name: "i", Type: "int"}).ParseAnswer("4.2"); !errors.Is(err, ErrInvalidType) {
		t.Errorf("expected invalid int, got %v", err)
	}
	if v, err := mk(QuestionDef{Name: "y"}).ParseAnswer("[1, two]"); err != nil {
		t.Errorf("yaml parse failed: %v", err)
	} else if l, ok := v.([]any); !ok || len(l) != 2 || l[0] != 1 {
		t.Errorf("unexpected yaml value: %#v", v)
	}
	if _, err := mk(QuestionDef{Name: "s", Type: "str"}).ParseAnswer(nil); !errors.Is(err, ErrInvalidType) {
		t.Errorf("nil str answer should be invalid, got %v", err)
	}
	q := mk(QuestionDef{Name: "m", Type: "str", Multiselect: true, Choices: []any{"[", "]", "x"}})
	v, err := q.ParseAnswer(`["[", "]"]`)
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := v.([]any); !ok || len(l) != 2 || l[0] != "[" || l[1] != "]" {
		t.Errorf("unexpected multiselect: %#v", v)
	}
	unset := mk(QuestionDef{Name: "u", Type: "str", Default: "{{ UNSET }}", HasDefault: true})
	if _, present, err := unset.Default(); err != nil || present {
		t.Errorf("UNSET default should be missing, present=%v err=%v", present, err)
	}
}

const validatorTemplate = `
runtime_mode:
    type: str
    choices: [process, docker]
    default: process
serve_mode:
    type: str
    default: development
    validator: "{% if serve_mode == 'production' and runtime_mode == 'process' %}production requires runtime_mode=docker{% endif %}"
operator_home:
    type: str
    default: relative/home
    validator: "{% if operator_home|first != '/' %}must be an absolute path{% endif %}"
`

func requireValidationError(t *testing.T, err error, question, message string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Question != question || ve.Message != message {
		t.Fatalf("unexpected validation error: %+v", ve)
	}
	if want := "Validation error for question '" + question + "': " + message; err.Error() != want {
		t.Fatalf("error message = %q, want %q", err.Error(), want)
	}
}

// Answers given as data are validated like prompted ones, with or without
// --defaults, like upstream `Worker._ask`.
func TestCopy_ValidatorRunsOnDataAnswers(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": validatorTemplate, "out.txt.jinja": "{{ serve_mode }}:{{ operator_home }}"})
	data := map[string]any{"serve_mode": "production", "operator_home": "/srv/op"}

	dst := t.TempDir()
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(data))
	requireValidationError(t, err, "serve_mode", "production requires runtime_mode=docker")
	if exists(filepath.Join(dst, "out.txt")) {
		t.Fatal("nothing should be rendered after a validation error")
	}

	prompter := &scriptedPrompter{}
	data["runtime_mode"] = "process"
	err = Copy(src, t.TempDir(), WithQuiet(true), WithPrompter(prompter), WithData(data))
	requireValidationError(t, err, "serve_mode", "production requires runtime_mode=docker")
	if len(prompter.asked) != 0 {
		t.Fatalf("data answers should not be prompted: %v", prompter.asked)
	}

	data["runtime_mode"] = "docker"
	dst = t.TempDir()
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(data)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "out.txt")); got != "production:/srv/op" {
		t.Fatalf("unexpected content: %q", got)
	}
}

// Under --defaults the default is validated, like upstream
// `Question.get_default`.
func TestCopy_ValidatorRunsOnDefaults(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": validatorTemplate, "out.txt.jinja": "{{ serve_mode }}:{{ operator_home }}"})

	dst := t.TempDir()
	err := Copy(src, dst, WithQuiet(true), WithDefaults(true))
	requireValidationError(t, err, "operator_home", "must be an absolute path")

	err = Copy(src, dst, WithQuiet(true), WithDefaults(true), WithUserDefaults(map[string]any{"operator_home": "relative/again"}))
	requireValidationError(t, err, "operator_home", "must be an absolute path")

	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithUserDefaults(map[string]any{"operator_home": "/srv/op"})); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "out.txt")); got != "development:/srv/op" {
		t.Fatalf("unexpected content: %q", got)
	}
}

// A question skipped by `when` is not validated on its computed default, nor
// is a secret question's default. An answer given as data for it still is,
// like upstream.
func TestCopy_ValidatorWhenFalseAndSecret(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml": `
use_db:
    type: bool
    default: false
db_name:
    type: str
    default: ""
    when: "{{ use_db }}"
    validator: "{% if not db_name %}db_name is required{% endif %}"
token:
    type: str
    secret: true
    default: ""
    validator: "{% if not token %}token is required{% endif %}"
`,
		"out.txt.jinja": "[{{ db_name }}]",
	})

	dst := t.TempDir()
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "out.txt")); got != "[]" {
		t.Fatalf("unexpected content: %q", got)
	}
	if answers := readFile(t, filepath.Join(dst, ".copier-answers.yml")); strings.Contains(answers, "db_name") {
		t.Fatalf("skipped question recorded: %s", answers)
	}

	err := Copy(src, t.TempDir(), WithQuiet(true), WithDefaults(true), WithData(map[string]any{"db_name": ""}))
	requireValidationError(t, err, "db_name", "db_name is required")

	err = Copy(src, t.TempDir(), WithQuiet(true), WithDefaults(true), WithData(map[string]any{"use_db": true}))
	requireValidationError(t, err, "db_name", "db_name is required")
}

// Previously recorded answers that no longer validate are dropped, so the
// default (or a prompt) replaces them.
func TestRecopy_InvalidLastAnswerDropped(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml":                            "name:\n    type: str\n    default: fine\n    validator: \"{% if name == 'bad' %}bad name{% endif %}\"\n",
		"{{ _copier_conf.answers_file }}.jinja": "{{ _copier_answers|to_nice_yaml }}",
		"out.txt.jinja":                         "{{ name }}",
	})
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(map[string]any{"name": "custom"})); err != nil {
		t.Fatal(err)
	}
	answersPath := filepath.Join(dst, ".copier-answers.yml")
	mustWriteFile(t, answersPath, []byte(strings.Replace(readFile(t, answersPath), "name: custom", "name: bad", 1)), 0o644)

	if err := Recopy(dst, WithQuiet(true), WithDefaults(true), WithOverwrite(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "out.txt")); got != "fine" {
		t.Fatalf("invalid recorded answer should be replaced by the default, got %q", got)
	}
}

// TestCopy_KeepAnswerForUnaskQuestion ports upstream's
// test_keep_answer_for_unask_question.
func TestCopy_KeepAnswerForUnaskQuestion(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml": `
disabled:
    type: str
    ask: false

disabled_with_default:
    type: str
    default: hello
    ask: false
`,
		"{{ _copier_conf.answers_file }}.jinja": "{{ _copier_answers|to_nice_yaml }}",
		"context.yml.jinja":                     "disabled: {{ disabled }}\ndisabled_with_default: {{ disabled_with_default }}\n",
	})
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(map[string]any{"disabled": "hello"})); err != nil {
		t.Fatal(err)
	}
	answers, err := LoadAnswersFile(filepath.Join(dst, ".copier-answers.yml"))
	if err != nil {
		t.Fatal(err)
	}
	srcPath, err := filepath.EvalSymlinks(src)
	if err != nil {
		t.Fatal(err)
	}
	if answers["disabled"] != "hello" || answers["disabled_with_default"] != "hello" || answers["_src_path"] != srcPath || len(answers) != 3 {
		t.Fatalf("unexpected answers: %v", answers)
	}
	if got := readFile(t, filepath.Join(dst, "context.yml")); got != "disabled: hello\ndisabled_with_default: hello\n" {
		t.Fatalf("unexpected context: %q", got)
	}
}

func askSettingTemplate(t *testing.T, when, ask bool) string {
	t.Helper()
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml":                            "foo:\n    type: str\n    default: bar\n    when: " + strconv.FormatBool(when) + "\n    ask: " + strconv.FormatBool(ask) + "\n",
		"{{ _copier_conf.answers_file }}.jinja": "{{ _copier_answers|to_nice_yaml }}",
	})
	return src
}

// TestCopy_AskSetting ports upstream's test_copy_with_ask_setting.
func TestCopy_AskSetting(t *testing.T) {
	for _, tc := range []struct {
		when, ask bool
		askFlag   []string
		prompted  bool
	}{
		{true, true, nil, true},
		{true, true, []string{"foo"}, true},
		{true, true, []string{"f*"}, true},
		{true, true, []string{"baz"}, true},
		{true, false, nil, false},
		{true, false, []string{"foo"}, true},
		{true, false, []string{"f*"}, true},
		{true, false, []string{"baz"}, false},
		{false, true, nil, false},
		{false, true, []string{"foo"}, false},
		{false, true, []string{"f*"}, false},
		{false, true, []string{"baz"}, false},
		{false, false, nil, false},
		{false, false, []string{"foo"}, false},
		{false, false, []string{"f*"}, false},
		{false, false, []string{"baz"}, false},
	} {
		src := askSettingTemplate(t, tc.when, tc.ask)
		dst := t.TempDir()
		prompter := &scriptedPrompter{answers: map[string]any{"foo": "barx"}}
		if err := Copy(src, dst, WithQuiet(true), WithPrompter(prompter), WithAsk(tc.askFlag...)); err != nil {
			t.Fatal(err)
		}
		if prompted := len(prompter.asked) > 0; prompted != tc.prompted {
			t.Errorf("when=%v ask=%v --ask=%v: prompted=%v, want %v", tc.when, tc.ask, tc.askFlag, prompted, tc.prompted)
		}
		answers, err := LoadAnswersFile(filepath.Join(dst, ".copier-answers.yml"))
		if err != nil {
			t.Fatal(err)
		}
		want, present := "bar", tc.when
		if tc.prompted {
			want = "barx"
		}
		if got, ok := answers["foo"]; ok != present || (present && got != want) {
			t.Errorf("when=%v ask=%v --ask=%v: foo=%v (present %v), want %v (present %v)", tc.when, tc.ask, tc.askFlag, got, ok, want, present)
		}
	}
}

// TestUpdate_AskSetting ports upstream's test_update_with_ask_setting.
func TestUpdate_AskSetting(t *testing.T) {
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}
	for _, tc := range []struct {
		when, ask, skipAnswered bool
		askFlag                 []string
		prompted                bool
	}{
		{true, true, false, nil, true},
		{true, true, false, []string{"foo"}, true},
		{true, true, false, []string{"baz"}, true},
		{true, true, true, nil, false},
		{true, true, true, []string{"foo"}, true},
		{true, true, true, []string{"baz"}, false},
		{true, false, false, nil, false},
		{true, false, false, []string{"foo"}, true},
		{true, false, false, []string{"baz"}, false},
		{true, false, true, nil, false},
		{true, false, true, []string{"foo"}, true},
		{true, false, true, []string{"baz"}, false},
		{false, true, false, nil, false},
		{false, false, true, []string{"foo"}, false},
	} {
		src := askSettingTemplate(t, tc.when, tc.ask)
		runGit(t, src, "init", "-q")
		gitSave(t, src, "v1")
		dst := t.TempDir()
		if err := Copy(src, dst, WithQuiet(true), WithData(map[string]any{"foo": "prev"})); err != nil {
			t.Fatal(err)
		}
		runGit(t, dst, "init", "-q")
		gitSave(t, dst, "")

		prompter := &scriptedPrompter{answers: map[string]any{"foo": "prevx"}}
		if err := Update(dst, WithQuiet(true), WithPrompter(prompter), WithSkipAnswered(tc.skipAnswered), WithAsk(tc.askFlag...)); err != nil {
			t.Fatal(err)
		}
		if prompted := len(prompter.asked) > 0; prompted != tc.prompted {
			t.Errorf("when=%v ask=%v skip=%v --ask=%v: prompted=%v, want %v", tc.when, tc.ask, tc.skipAnswered, tc.askFlag, prompted, tc.prompted)
		}
		answers, err := LoadAnswersFile(filepath.Join(dst, ".copier-answers.yml"))
		if err != nil {
			t.Fatal(err)
		}
		want, present := "prev", tc.when
		if tc.prompted {
			want = "prevx"
		}
		if got, ok := answers["foo"]; ok != present || (present && got != want) {
			t.Errorf("when=%v ask=%v skip=%v --ask=%v: foo=%v (present %v), want %v (present %v)", tc.when, tc.ask, tc.skipAnswered, tc.askFlag, got, ok, want, present)
		}
	}
}

// TestCopy_DestinationSymlinkOutsideDestinationRoot ports upstream's
// test_destination_symlink_outside_destination_root.
func TestCopy_DestinationSymlinkOutsideDestinationRoot(t *testing.T) {
	for _, preserve := range []bool{true, false} {
		src := t.TempDir()
		dst := t.TempDir()
		writeTree(t, src, map[string]string{
			"copier.yaml": "_preserve_symlinks: " + strconv.FormatBool(preserve),
			"file.txt":    "from template",
		})
		writeTree(t, dst, map[string]string{"other/target.txt": "external"})
		project := filepath.Join(dst, "project")
		if err := os.MkdirAll(project, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "other", "target.txt"), filepath.Join(project, "file.txt")); err != nil {
			t.Fatal(err)
		}
		if err := Copy(src, project, WithQuiet(true), WithOverwrite(true)); err != nil {
			t.Fatalf("preserve=%v: %v", preserve, err)
		}
		if isSymlinkPath(filepath.Join(project, "file.txt")) {
			t.Fatalf("preserve=%v: destination symlink should be replaced", preserve)
		}
		if got := readFile(t, filepath.Join(project, "file.txt")); got != "from template" {
			t.Fatalf("preserve=%v: unexpected content %q", preserve, got)
		}
		if got := readFile(t, filepath.Join(dst, "other", "target.txt")); got != "external" {
			t.Fatalf("preserve=%v: symlink target modified: %q", preserve, got)
		}
	}
}

// EvaluateWhen must agree with the questionnaire: a question is skipped (its
// answer hidden) exactly when EvaluateWhen reports false.
func TestEvaluateWhen(t *testing.T) {
	for _, tc := range []struct {
		when    any
		answers map[string]any
		want    bool
	}{
		{nil, nil, true},
		{true, nil, true},
		{false, nil, false},
		{"", nil, false},
		{"{{ runtime_mode == 'docker' }}", map[string]any{"runtime_mode": "docker"}, true},
		{"{{ runtime_mode == 'docker' }}", map[string]any{"runtime_mode": "process"}, false},
		{"{{ use_db }}", map[string]any{"use_db": false}, false},
		{"{{ use_db }}", map[string]any{"use_db": true}, true},
		{"{{ count }}", map[string]any{"count": int64(0)}, false},
		{"{{ count }}", map[string]any{"count": int64(2)}, true},
		{"no", nil, false},
		{"{{ _copier_phase == 'prompt' }}", nil, true},
	} {
		got, err := EvaluateWhen(QuestionDef{Name: "q", When: tc.when}, tc.answers)
		if err != nil {
			t.Fatalf("when=%v: %v", tc.when, err)
		}
		if got != tc.want {
			t.Errorf("EvaluateWhen(when=%v, %v) = %v, want %v", tc.when, tc.answers, got, tc.want)
		}

		// The questionnaire hides the answer exactly when the condition is false.
		src := t.TempDir()
		dst := t.TempDir()
		cfg := map[string]any{"q": map[string]any{"type": "str", "default": "x"}}
		for k, v := range tc.answers {
			cfg[k] = map[string]any{"default": v}
		}
		if tc.when != nil {
			cfg["q"].(map[string]any)["when"] = tc.when
		}
		var yml strings.Builder
		for _, k := range []string{"runtime_mode", "use_db", "count", "q"} {
			if v, ok := cfg[k]; ok {
				b, err := yamlMarshalForTest(map[string]any{k: v})
				if err != nil {
					t.Fatal(err)
				}
				yml.WriteString(b)
			}
		}
		writeTree(t, src, map[string]string{"copier.yml": yml.String(), "{{ _copier_conf.answers_file }}.jinja": "{{ _copier_answers|to_nice_yaml }}"})
		if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
			t.Fatalf("when=%v: %v", tc.when, err)
		}
		answers, err := LoadAnswersFile(filepath.Join(dst, ".copier-answers.yml"))
		if err != nil {
			t.Fatal(err)
		}
		if _, asked := answers["q"]; asked != tc.want {
			t.Errorf("when=%v %v: questionnaire recorded q=%v, EvaluateWhen=%v", tc.when, tc.answers, asked, tc.want)
		}
	}
}

func TestEvaluateWhen_Envops(t *testing.T) {
	got, err := EvaluateWhen(QuestionDef{Name: "q", When: "[[ flag ]]"}, map[string]any{"flag": true},
		Envops{VariableStartString: "[[", VariableEndString: "]]", BlockStartString: "[%", BlockEndString: "%]"})
	if err != nil || !got {
		t.Fatalf("EvaluateWhen with custom delimiters = %v, %v; want true", got, err)
	}
}

func yamlMarshalForTest(v any) (string, error) {
	b, err := yaml.Marshal(v)
	return string(b), err
}
