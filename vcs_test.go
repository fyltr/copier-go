package copier

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input   string
		wantURL string
		wantGit bool
	}{
		{"gh:user/repo", "https://github.com/user/repo.git", true},
		{"gl:org/project", "https://gitlab.com/org/project.git", true},
		{"git+https://example.com/repo", "https://example.com/repo", true},
		{"ssh://git@example.com/org/repo", "ssh://git@example.com/org/repo", true},
		{"https://github.com/user/repo.git", "https://github.com/user/repo.git", true},
		{"git@github.com:user/repo.git", "git@github.com:user/repo.git", true},
		{"/some/local/path", "/some/local/path", false},
	}

	for _, tt := range tests {
		url, isGit := NormalizeURL(tt.input)
		if url != tt.wantURL {
			t.Errorf("NormalizeURL(%q) url = %q, want %q", tt.input, url, tt.wantURL)
		}
		if isGit != tt.wantGit {
			t.Errorf("NormalizeURL(%q) isGit = %v, want %v", tt.input, isGit, tt.wantGit)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}
	tmplDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "dst")

	mustWriteFile(t, filepath.Join(tmplDir, "README.md"), []byte("v1\n"), 0o644)
	runGit(t, tmplDir, "init")
	runGit(t, tmplDir, "add", ".")
	runGit(t, tmplDir, "commit", "-m", "v1")
	runGit(t, tmplDir, "tag", "v1.0.0")

	mustWriteFile(t, filepath.Join(tmplDir, "README.md"), []byte("v2\n"), 0o644)
	runGit(t, tmplDir, "add", ".")
	runGit(t, tmplDir, "commit", "-m", "v2")
	runGit(t, tmplDir, "tag", "v2.0.0")

	err := Copy(tmplDir, dstDir,
		WithVcsRef("v1.0.0"),
		WithDefaults(true),
		WithOverwrite(true),
		WithQuiet(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := CheckUpdate(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable {
		t.Fatal("expected update to be available")
	}
	if result.CurrentVersion != "1.0.0" || result.LatestVersion != "2.0.0" {
		t.Fatalf("unexpected versions: %+v", result)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-c", "user.name=Test User", "-c", "user.email=test@example.com"}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %s: %v", args, string(out), err)
	}
}

func TestIsGitURL(t *testing.T) {
	if !IsGitURL("gh:user/repo") {
		t.Error("expected gh: to be git URL")
	}
	if IsGitURL("/tmp/local") {
		t.Error("expected /tmp/local to not be git URL")
	}
}
