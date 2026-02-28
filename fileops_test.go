package copier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "sub", "dst.txt")

	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected hello, got %q", string(data))
	}
}

func TestPatternMatcher(t *testing.T) {
	m := NewPatternMatcher([]string{"*.pyc", "__pycache__", ".git"})

	if !m.Matches("foo.pyc") {
		t.Error("expected *.pyc to match foo.pyc")
	}
	if !m.Matches("__pycache__") {
		t.Error("expected __pycache__ to match")
	}
	if !m.Matches(".git") {
		t.Error("expected .git to match")
	}
	if m.Matches("main.go") {
		t.Error("expected main.go to not match")
	}
}

func TestWriteAndLoadAnswersFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".copier-answers.yml")

	answers := map[string]any{
		"name":    "myproject",
		"version": "1.0",
	}
	metadata := map[string]any{
		"_src_path": "gh:user/template",
		"_commit":   "abc123",
	}

	if err := WriteAnswersFile(path, answers, metadata); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadAnswersFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded["name"] != "myproject" {
		t.Fatalf("expected myproject, got %v", loaded["name"])
	}
	if loaded["_src_path"] != "gh:user/template" {
		t.Fatalf("expected gh:user/template, got %v", loaded["_src_path"])
	}
}

func TestLoadAnswersFile_Missing(t *testing.T) {
	answers, err := LoadAnswersFile("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if answers != nil {
		t.Fatal("expected nil for missing file")
	}
}
