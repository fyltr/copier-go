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

// TestIsTrustedRepository_Normalization ports upstream's test_is_trusted.
func TestIsTrustedRepository_Normalization(t *testing.T) {
	home, _ := os.UserHomeDir()
	type tc struct {
		repo  string
		trust []string
		want  bool
	}
	var cases []tc
	for _, base := range []string{"https://github.com", "ssh://git@github.com", "git@github.com:", "gh:", "gl:"} {
		b := base + "/"
		cases = append(cases,
			// Plain URLs with no dot segments: normal prefix/equality matching.
			tc{b + "user/repo.git", nil, false},
			tc{b + "user/repo.git", []string{b + "user"}, false},
			tc{b + "user/repo.git", []string{b + "user/"}, true},
			tc{b + "user/repo.git", []string{b + "user/repo"}, false},
			tc{b + "user/repo.git", []string{b + "user/repo.git"}, true},
			tc{b + "user/repo.git", []string{b}, true},
			tc{b + "user/repo.git", []string{base}, false},
			tc{b + "user/repo", []string{b + "user/repo.git"}, false},
			// Literal `..` traversal is collapsed, so a trusted prefix still
			// matches (or not) as expected.
			tc{b + "user/../evil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/../evil/repo.git", []string{b + "user/../evil/repo.git"}, true},
			tc{b + "x/../user/repo.git", []string{b + "user/"}, true},
			// Ambiguous repository URLs (percent-encoding, backslashes, doubled
			// slashes) never satisfy a trust prefix.
			tc{b + "user/%2e%2e/evil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/%2E%2E/evil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/%2e%2E/evil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/%2e./evil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/.%2e/evil/repo.git", []string{b + "user/"}, false},
			tc{b + "user%2f%2e%2e%2fevil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/%2e%2e%5cevil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/%2e%2e%5Cevil/repo.git", []string{b + "user/"}, false},
			tc{b + "user/..%5cevil/repo.git", []string{b + "user/"}, false},
			tc{b + `user/..\evil/repo.git`, []string{b + "user/"}, false},
			tc{b + "user%2fsub/../evil/repo.git", []string{b + "user/"}, false},
			tc{b + "user%5csub/../evil/repo.git", []string{b + "user/"}, false},
			tc{b + `user\sub/../evil/repo.git`, []string{b + "user/"}, false},
			tc{b + "user%2fsub/../repo.git", []string{b + "user/repo.git"}, false},
			tc{b + "x/%2e%2e/user/repo.git", []string{b + "user/"}, false},
			tc{b + "x/%2E%2E/user/repo.git", []string{b + "user/"}, false},
			tc{b + "attacker/repo/%2e%2e/%2e%2e/user/repo.git", []string{b + "user/"}, false},
			tc{b + "attacker/evil/..//user/user", []string{b + "user/"}, false},
			// An ambiguous repository can still be trusted via an exact,
			// verbatim trust entry.
			tc{b + "user/%2e%2e/evil/repo.git", []string{b + "user/%2e%2e/evil/repo.git"}, true},
			tc{b + "user/%2e%2e/evil/repo.git", []string{b + "user/../evil/repo.git"}, false},
			tc{b + "user/%2e%2e%5cevil/repo.git", []string{b + "user/%2e%2e%5cevil/repo.git"}, true},
			tc{b + "user/%2e%2e%5cevil/repo.git", []string{b + `user/..\evil/repo.git`}, false},
			tc{b + "user/%2e%2e%5cevil/repo.git", []string{b + "user/../evil/repo.git"}, false},
		)
	}
	cases = append(cases,
		// Local filesystem paths: normal prefix/equality matching, with `~`
		// expansion and literal `..` traversal collapse.
		tc{home + "/template", nil, false},
		tc{home + "/template", []string{home + "/template"}, true},
		tc{home + "/template", []string{"~/template"}, true},
		tc{home + "/path/to/template", []string{"~/path/to/template"}, true},
		tc{home + "/path/to/template", []string{"~/path/to/"}, true},
		tc{home + "/path/to/template", []string{"~/path/to"}, false},
		tc{home + "/trusted/../attacker/template", []string{home + "/trusted/"}, false},
		tc{home + "/trusted/../attacker/template", []string{"~/trusted/"}, false},
		// Percent-encoded segments are never decoded for local paths.
		tc{home + "/user/%2e%2e/repo", []string{home + "/user/"}, true},
		tc{home + "/user/%2e%2e/repo", []string{home + "/user/%2e%2e/repo"}, true},
		tc{home + "/user/%2e%2e/repo", []string{home + "/user/repo"}, false},
		tc{home + "/trusted/template", []string{"~/trusted/"}, true},
		tc{"/tmp/tpl/", []string{"/tmp/tpl/"}, true},
		tc{`C:\Users\me\tpl`, []string{`C:\Users\me\tpl`}, true},
	)
	for _, c := range cases {
		if got := isTrustedRepository(c.trust, c.repo); got != c.want {
			t.Errorf("isTrustedRepository(%v, %q) = %v, want %v (safe %v, normalized %q)", c.trust, c.repo, got, c.want, isSafeTrustURL(c.repo), normalizeTrustURL(c.repo))
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
