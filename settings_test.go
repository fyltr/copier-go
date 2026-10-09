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

func TestIsTrustedRepository_Normalization(t *testing.T) {
	home, _ := os.UserHomeDir()
	type tc struct {
		repo  string
		trust []string
		want  bool
	}
	var cases []tc
	for _, base := range []string{"https://github.com", "ssh://git@github.com", "git@github.com:", "gh:", "gl:"} {
		sep := "/"
		if base == "git@github.com:" || base == "gh:" || base == "gl:" {
			sep = ""
		}
		cases = append(cases,
			tc{base + sep + "user/repo.git", nil, false},
			tc{base + sep + "user/repo.git", []string{base + sep + "user/repo.git"}, true},
			tc{base + sep + "user/repo", []string{base + sep + "user/repo.git"}, false},
			tc{base + sep + "user/repo.git", []string{base + sep + "user/"}, true},
			tc{base + sep + "user/repo.git", []string{base + sep + "user/repo"}, false},
			tc{base + sep + "user/repo.git", []string{base + sep + "user"}, false},
			tc{base + sep + "user/../evil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user/../evil/repo.git", []string{base + sep + "user/../evil/repo.git"}, true},
			tc{base + sep + "user/%2e%2e/evil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user/%2E%2E/evil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user/.%2e/evil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user%2f%2e%2e%2fevil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user/%2e%2e/evil/repo.git", []string{base + sep + "user/../evil/repo.git"}, true},
			tc{base + sep + "user/%2e%2e%5cevil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user/..%5cevil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user/..\\evil/repo.git", []string{base + sep + "user/"}, false},
			tc{base + sep + "user/%2e%2e%5cevil/repo.git", []string{base + sep + "user/../evil/repo.git"}, true},
		)
	}
	cases = append(cases,
		tc{home + "/trusted/../attacker/template", []string{home + "/trusted/"}, false},
		tc{home + "/trusted/../attacker/template", []string{"~/trusted/"}, false},
		tc{home + "/trusted/template", []string{"~/trusted/"}, true},
		tc{"/tmp/tpl/", []string{"/tmp/tpl/"}, true},
		tc{`C:\Users\me\tpl`, []string{`C:\Users\me\tpl`}, true},
	)
	for _, c := range cases {
		if got := isTrustedRepository(c.trust, c.repo); got != c.want {
			t.Errorf("isTrustedRepository(%v, %q) = %v, want %v (normalized %q)", c.trust, c.repo, got, c.want, normalizeTrustURL(c.repo))
		}
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
