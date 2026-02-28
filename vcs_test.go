package copier

import "testing"

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input   string
		wantURL string
		wantGit bool
	}{
		{"gh:user/repo", "https://github.com/user/repo.git", true},
		{"gl:org/project", "https://gitlab.com/org/project.git", true},
		{"git+https://example.com/repo", "https://example.com/repo", true},
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

func TestIsGitURL(t *testing.T) {
	if !IsGitURL("gh:user/repo") {
		t.Error("expected gh: to be git URL")
	}
	if IsGitURL("/tmp/local") {
		t.Error("expected /tmp/local to not be git URL")
	}
}
