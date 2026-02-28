package copier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettings_IsTrusted(t *testing.T) {
	s := &Settings{
		Trust: []string{
			"https://github.com/trusted/repo.git",
			"https://github.com/org/",
		},
	}

	if !s.IsTrusted("https://github.com/trusted/repo.git") {
		t.Error("exact match should be trusted")
	}
	if !s.IsTrusted("https://github.com/org/any-repo") {
		t.Error("prefix match should be trusted")
	}
	if s.IsTrusted("https://github.com/untrusted/repo") {
		t.Error("unmatched should not be trusted")
	}
}

func TestSettings_DefaultFor(t *testing.T) {
	s := &Settings{Defaults: map[string]any{"name": "default-name"}}
	v, ok := s.DefaultFor("name")
	if !ok || v != "default-name" {
		t.Fatalf("expected default-name, got %v", v)
	}

	_, ok = s.DefaultFor("missing")
	if ok {
		t.Fatal("expected false for missing key")
	}
}

func TestLoadSettings_Missing(t *testing.T) {
	t.Setenv("COPIER_SETTINGS_PATH", "/nonexistent/settings.yml")
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("expected empty settings, got nil")
	}
}

func TestLoadSettings_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yml")
	content := `defaults:
  project_name: myproject
trust:
  - https://github.com/trusted/
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("COPIER_SETTINGS_PATH", path)
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Defaults["project_name"] != "myproject" {
		t.Fatalf("expected myproject, got %v", s.Defaults["project_name"])
	}
	if len(s.Trust) != 1 {
		t.Fatalf("expected 1 trust entry, got %d", len(s.Trust))
	}
}
