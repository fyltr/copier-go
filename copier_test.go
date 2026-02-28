package copier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopy_LocalTemplate(t *testing.T) {
	// Create a minimal template.
	tmplDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	// copier.yml
	config := `project_name:
  type: str
  default: testproject
`
	os.WriteFile(filepath.Join(tmplDir, "copier.yml"), []byte(config), 0o644)

	// Template file.
	os.WriteFile(filepath.Join(tmplDir, "README.md.jinja"), []byte("# {{ project_name }}\n"), 0o644)

	// Static file.
	os.WriteFile(filepath.Join(tmplDir, "LICENSE"), []byte("MIT"), 0o644)

	err := Copy(tmplDir, dstDir,
		WithData(map[string]any{"project_name": "myapp"}),
		WithDefaults(true),
		WithQuiet(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Verify static file.
	data, err := os.ReadFile(filepath.Join(dstDir, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "MIT" {
		t.Fatalf("expected MIT, got %q", string(data))
	}

	// Verify rendered template.
	data, err = os.ReadFile(filepath.Join(dstDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# myapp\n" {
		t.Fatalf("expected '# myapp\\n', got %q", string(data))
	}

	// Verify answers file.
	answers, err := LoadAnswersFile(filepath.Join(dstDir, ".copier-answers.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if answers["project_name"] != "myapp" {
		t.Fatalf("expected myapp in answers, got %v", answers["project_name"])
	}
}

func TestCopy_Pretend(t *testing.T) {
	tmplDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	os.WriteFile(filepath.Join(tmplDir, "copier.yml"), []byte("name:\n  default: x\n"), 0o644)
	os.WriteFile(filepath.Join(tmplDir, "file.txt"), []byte("content"), 0o644)

	err := Copy(tmplDir, dstDir,
		WithDefaults(true),
		WithPretend(true),
		WithQuiet(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Destination should not exist in pretend mode.
	if _, err := os.Stat(filepath.Join(dstDir, "file.txt")); !os.IsNotExist(err) {
		t.Fatal("file should not exist in pretend mode")
	}
}

func TestCopy_SkipIfExists(t *testing.T) {
	tmplDir := t.TempDir()
	dstDir := t.TempDir()

	os.WriteFile(filepath.Join(tmplDir, "copier.yml"), []byte("_skip_if_exists:\n  - existing.txt\n"), 0o644)
	os.WriteFile(filepath.Join(tmplDir, "existing.txt"), []byte("new content"), 0o644)
	os.WriteFile(filepath.Join(dstDir, "existing.txt"), []byte("old content"), 0o644)

	err := Copy(tmplDir, dstDir,
		WithDefaults(true),
		WithOverwrite(true),
		WithQuiet(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old content" {
		t.Fatalf("expected old content (skipped), got %q", string(data))
	}
}

func TestCopy_Exclude(t *testing.T) {
	tmplDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	os.WriteFile(filepath.Join(tmplDir, "copier.yml"), []byte("_exclude:\n  - '*.log'\n"), 0o644)
	os.WriteFile(filepath.Join(tmplDir, "keep.txt"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(tmplDir, "debug.log"), []byte("log"), 0o644)

	err := Copy(tmplDir, dstDir,
		WithDefaults(true),
		WithOverwrite(true),
		WithQuiet(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dstDir, "keep.txt")); os.IsNotExist(err) {
		t.Fatal("keep.txt should exist")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "debug.log")); !os.IsNotExist(err) {
		t.Fatal("debug.log should be excluded")
	}
}
