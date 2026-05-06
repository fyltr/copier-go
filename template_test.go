package copier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTemplate_LocalDir(t *testing.T) {
	dir := t.TempDir()

	// Create a minimal copier.yml.
	config := `project_name:
  type: str
  default: myproject
  help: Name of the project

use_ci:
  type: bool
  default: true

_templates_suffix: .jinja
_exclude:
  - "*.tmp"
`
	if err := os.WriteFile(filepath.Join(dir, "copier.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl, err := LoadTemplate(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}

	if tmpl.LocalPath != dir {
		t.Fatalf("expected local path %s, got %s", dir, tmpl.LocalPath)
	}
	if tmpl.Config.TemplateSuffix != ".jinja" {
		t.Fatalf("expected .jinja suffix, got %s", tmpl.Config.TemplateSuffix)
	}
	if len(tmpl.Config.Exclude) != 1 || tmpl.Config.Exclude[0] != "*.tmp" {
		t.Fatalf("unexpected exclude: %v", tmpl.Config.Exclude)
	}
	if len(tmpl.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(tmpl.Questions))
	}
}

func TestLoadTemplate_NoConfig(t *testing.T) {
	dir := t.TempDir()

	tmpl, err := LoadTemplate(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}

	if tmpl.Config.TemplateSuffix != DefaultTemplateSuffix {
		t.Fatalf("expected default suffix, got %s", tmpl.Config.TemplateSuffix)
	}
}

func TestLoadTemplate_BothConfigs(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "copier.yml"), []byte("x: 1"), 0o644)
	mustWriteFile(t, filepath.Join(dir, "copier.yaml"), []byte("x: 1"), 0o644)

	_, err := LoadTemplate(dir, "", false)
	if err == nil {
		t.Fatal("expected error for both copier.yml and copier.yaml")
	}
}

func TestTemplate_CopyRoot(t *testing.T) {
	tmpl := &Template{LocalPath: "/templates/mytemplate"}
	if tmpl.CopyRoot() != "/templates/mytemplate" {
		t.Fatalf("expected /templates/mytemplate, got %s", tmpl.CopyRoot())
	}

	tmpl.Config.Subdirectory = "project"
	if tmpl.CopyRoot() != "/templates/mytemplate/project" {
		t.Fatalf("expected /templates/mytemplate/project, got %s", tmpl.CopyRoot())
	}
}

func TestLoadTemplate_SubdirectoryEscapesRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "copier.yml"), []byte("_subdirectory: ../outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadTemplate(dir, "", false)
	if err == nil {
		t.Fatal("expected error for escaping _subdirectory")
	}
}

func TestLoadTemplate_InvalidUndefined(t *testing.T) {
	dir := t.TempDir()
	config := `_envops:
  undefined: jinja2.ChainableUndefined
`
	if err := os.WriteFile(filepath.Join(dir, "copier.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadTemplate(dir, "", false)
	if err == nil {
		t.Fatal("expected error for unsupported envops.undefined")
	}
}

func TestTemplate_Exclusions(t *testing.T) {
	tmpl := &Template{}
	exclusions := tmpl.Exclusions()
	if len(exclusions) != len(DefaultExclude) {
		t.Fatalf("expected default exclusions, got %v", exclusions)
	}

	tmpl.Config.Exclude = []string{"custom"}
	exclusions = tmpl.Exclusions()
	if len(exclusions) != 1 || exclusions[0] != "custom" {
		t.Fatalf("expected [custom], got %v", exclusions)
	}
}

func TestInferType(t *testing.T) {
	tests := []struct {
		val  any
		want QuestionType
	}{
		{true, TypeBool},
		{false, TypeBool},
		{42, TypeInt},
		{3.14, TypeFloat},
		{"hello", TypeStr},
		{nil, TypeStr},
	}
	for _, tt := range tests {
		got := inferType(tt.val)
		if got != tt.want {
			t.Errorf("inferType(%v) = %s, want %s", tt.val, got, tt.want)
		}
	}
}

func TestTaskDef_CmdArgs(t *testing.T) {
	td := TaskDef{Cmd: "echo hello"}
	args := td.CmdArgs()
	if len(args) != 3 || args[0] != "sh" || args[1] != "-c" || args[2] != "echo hello" {
		t.Fatalf("unexpected CmdArgs: %v", args)
	}

	td2 := TaskDef{Cmd: []any{"echo", "hello"}}
	args2 := td2.CmdArgs()
	if len(args2) != 2 || args2[0] != "echo" || args2[1] != "hello" {
		t.Fatalf("unexpected CmdArgs for list: %v", args2)
	}
}
