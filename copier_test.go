package copier

import (
	"errors"
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
	mustWriteFile(t, filepath.Join(tmplDir, "copier.yml"), []byte(config), 0o644)

	// Template file.
	mustWriteFile(t, filepath.Join(tmplDir, "README.md.jinja"), []byte("# {{ project_name }}\n"), 0o644)

	// Static file.
	mustWriteFile(t, filepath.Join(tmplDir, "LICENSE"), []byte("MIT"), 0o644)

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

	mustWriteFile(t, filepath.Join(tmplDir, "copier.yml"), []byte("name:\n  default: x\n"), 0o644)
	mustWriteFile(t, filepath.Join(tmplDir, "file.txt"), []byte("content"), 0o644)

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

	mustWriteFile(t, filepath.Join(tmplDir, "copier.yml"), []byte("_skip_if_exists:\n  - existing.txt\n"), 0o644)
	mustWriteFile(t, filepath.Join(tmplDir, "existing.txt"), []byte("new content"), 0o644)
	mustWriteFile(t, filepath.Join(dstDir, "existing.txt"), []byte("old content"), 0o644)

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

	mustWriteFile(t, filepath.Join(tmplDir, "copier.yml"), []byte("_exclude:\n  - '*.log'\n"), 0o644)
	mustWriteFile(t, filepath.Join(tmplDir, "keep.txt"), []byte("keep"), 0o644)
	mustWriteFile(t, filepath.Join(tmplDir, "debug.log"), []byte("log"), 0o644)

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

func TestCopy_RenderedPathEscapesDestination(t *testing.T) {
	tmplDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	mustWriteFile(t, filepath.Join(tmplDir, "copier.yml"), []byte("name:\n  default: ../evil\n"), 0o644)
	mustWriteFile(t, filepath.Join(tmplDir, "{{ name }}.txt.jinja"), []byte("bad"), 0o644)

	err := Copy(tmplDir, dstDir, WithDefaults(true), WithQuiet(true))
	if !errors.Is(err, ErrForbiddenPath) {
		t.Fatalf("expected ErrForbiddenPath, got %v", err)
	}
}

func TestCopy_ExternalDataInsideDestination(t *testing.T) {
	tmplDir := t.TempDir()
	dstDir := t.TempDir()

	mustWriteFile(t, filepath.Join(dstDir, "data.yml"), []byte("name: external\n"), 0o644)
	config := `_external_data:
  parent: data.yml
`
	mustWriteFile(t, filepath.Join(tmplDir, "copier.yml"), []byte(config), 0o644)
	mustWriteFile(t, filepath.Join(tmplDir, "out.txt.jinja"), []byte("{{ _external_data.parent.name }}"), 0o644)

	err := Copy(tmplDir, dstDir, WithDefaults(true), WithOverwrite(true), WithQuiet(true))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dstDir, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external" {
		t.Fatalf("expected external data, got %q", string(data))
	}
}

func TestCopy_ExternalDataOutsideDestinationRequiresTrust(t *testing.T) {
	tmplDir := t.TempDir()
	dstDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "data.yml")
	mustWriteFile(t, outsideFile, []byte("name: external\n"), 0o644)

	config := `_external_data:
  parent: "` + outsideFile + `"
`
	mustWriteFile(t, filepath.Join(tmplDir, "copier.yml"), []byte(config), 0o644)
	mustWriteFile(t, filepath.Join(tmplDir, "out.txt.jinja"), []byte("{{ _external_data.parent.name }}"), 0o644)

	err := Copy(tmplDir, dstDir, WithDefaults(true), WithQuiet(true))
	if !errors.Is(err, ErrUnsafeTemplate) {
		t.Fatalf("expected ErrUnsafeTemplate, got %v", err)
	}
}

func TestCopy_ExecutableModeOverwritesExisting(t *testing.T) {
	tmplDir := t.TempDir()
	dstDir := t.TempDir()
	src := filepath.Join(tmplDir, "script.sh")
	dst := filepath.Join(dstDir, "script.sh")

	mustWriteFile(t, src, []byte("#!/bin/sh\n"), 0o755)
	mustWriteFile(t, dst, []byte("old\n"), 0o644)

	err := Copy(tmplDir, dstDir, WithDefaults(true), WithOverwrite(true), WithQuiet(true))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected executable bit to be preserved, got %v", info.Mode().Perm())
	}
}
