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
	m := NewPatternMatcher([]string{"*.pyc", "__pycache__", ".git", "/top.txt", "docs/", "build/**", "!keep.pyc"})

	cases := map[string]bool{
		"foo.pyc":              true,
		"a/b/foo.pyc":          true,
		"__pycache__":          true,
		"pkg/__pycache__/x.py": true,
		".git":                 true,
		".git/config":          true,
		"main.go":              false,
		"top.txt":              true,
		"sub/top.txt":          false,
		"docs/index.md":        true,
		"build/out/a.o":        true,
		"keep.pyc":             false,
	}
	for path, want := range cases {
		if got := m.Matches(path); got != want {
			t.Errorf("Matches(%q) = %v, want %v", path, got, want)
		}
	}
	if !m.MatchesDir("docs") {
		t.Error("expected docs/ to match the docs directory")
	}
	if !m.MatchesDir("a/__pycache__") {
		t.Error("expected __pycache__ dir to match")
	}
}

func TestPatternMatcher_MultilineAndComments(t *testing.T) {
	m := NewPatternMatcher([]string{"# comment\n\n/a.txt\n/b.txt\n", ""})
	if !m.Matches("a.txt") || !m.Matches("b.txt") {
		t.Fatal("expected multi-line entry patterns to match")
	}
	if m.Matches("# comment") || m.Matches("c.txt") {
		t.Fatal("unexpected match")
	}
	empty := NewPatternMatcher(nil)
	if empty.Matches("anything") {
		t.Fatal("empty matcher should never match")
	}
}

func TestFnmatchCase(t *testing.T) {
	cases := []struct {
		name, pattern string
		want          bool
	}{
		{"what_does_it_eat", "*it*", true},
		{"can_it_fly", "*it*", true},
		{"animal", "*it*", false},
		{"a/b", "a?b", true},
		{"abc", "a[bc]c", true},
		{"adc", "a[!bc]c", true},
		{"abc", "a[!bc]c", false},
		{"name", "name", true},
		{"Name", "name", false},
	}
	for _, c := range cases {
		if got := fnmatchCase(c.name, c.pattern); got != c.want {
			t.Errorf("fnmatchCase(%q, %q) = %v, want %v", c.name, c.pattern, got, c.want)
		}
	}
}

func TestEscapeGitPath(t *testing.T) {
	if got := escapeGitPath("a[b]*?.txt"); got != `a\[b\]\*\?.txt` {
		t.Fatalf("unexpected escape: %s", got)
	}
	if got := escapeGitPath(" lead"); got != `\ lead` {
		t.Fatalf("unexpected escape: %q", got)
	}
}

func TestWriteAndLoadAnswersFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".copier-answers.yml")

	answers := map[string]any{
		"name":    "myproject",
		"version": "1.0",
		"list":    []any{"a", "b"},
	}
	metadata := map[string]any{
		"_src_path": "gh:user/template",
		"_commit":   "abc123",
	}

	if err := WriteAnswersFile(path, answers, metadata); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:len(answersFileHeader)]) != answersFileHeader {
		t.Fatalf("missing header: %q", string(raw))
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
	if l, ok := loaded["list"].([]any); !ok || len(l) != 2 {
		t.Fatalf("expected list answer to round-trip, got %v", loaded["list"])
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

func TestRemoveOldFiles(t *testing.T) {
	old := t.TempDir()
	newer := t.TempDir()
	dst := t.TempDir()
	mustWriteFile(t, filepath.Join(old, "gone.txt"), []byte("x"), 0o644)
	mustWriteFile(t, filepath.Join(old, "keep.txt"), []byte("x"), 0o644)
	if err := os.MkdirAll(filepath.Join(old, "olddir"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(old, "olddir", "a.txt"), []byte("x"), 0o644)
	mustWriteFile(t, filepath.Join(newer, "keep.txt"), []byte("x"), 0o644)

	mustWriteFile(t, filepath.Join(dst, "gone.txt"), []byte("x"), 0o644)
	mustWriteFile(t, filepath.Join(dst, "keep.txt"), []byte("x"), 0o644)
	mustWriteFile(t, filepath.Join(dst, "user.txt"), []byte("x"), 0o644)
	if err := os.MkdirAll(filepath.Join(dst, "olddir"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dst, "olddir", "a.txt"), []byte("x"), 0o644)
	mustWriteFile(t, filepath.Join(dst, "olddir", "mine.txt"), []byte("x"), 0o644)

	cmp, err := compareDirs(old, newer)
	if err != nil {
		t.Fatal(err)
	}
	removeOldFiles(dst, cmp, false)

	if _, err := os.Stat(filepath.Join(dst, "gone.txt")); !os.IsNotExist(err) {
		t.Error("gone.txt should be removed")
	}
	if _, err := os.Stat(filepath.Join(dst, "olddir", "a.txt")); !os.IsNotExist(err) {
		t.Error("olddir/a.txt should be removed")
	}
	for _, keep := range []string{"keep.txt", "user.txt", "olddir/mine.txt"} {
		if _, err := os.Stat(filepath.Join(dst, keep)); err != nil {
			t.Errorf("%s should be kept: %v", keep, err)
		}
	}
}

func TestGlobRelative(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a/x.yml", "a/b/y.yml", "z.yml", "a/n.txt"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWriteFile(t, filepath.Join(root, p), nil, 0o644)
	}
	got, err := globRelative(root, "**/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 matches, got %v", got)
	}
	got, err = globRelative(root, "a/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "x.yml" {
		t.Fatalf("unexpected: %v", got)
	}
}
