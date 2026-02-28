package pathutil

import "testing"

func TestIsSubpath(t *testing.T) {
	ok, err := IsSubpath("/base/dir", "/base/dir/child/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("child should be subpath")
	}

	ok, err = IsSubpath("/base/dir", "/other/path")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("different root should not be subpath")
	}

	ok, err = IsSubpath("/base/dir", "/base/dir/../other")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("traversal should not be subpath")
	}
}

func TestNormalizeGitPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`plain`, "plain"},
		{`"hello"`, "hello"},
		{`"tab\there"`, "tab\there"},
		{`"new\nline"`, "new\nline"},
		{`"back\\slash"`, "back\\slash"},
	}
	for _, tt := range tests {
		got := NormalizeGitPath(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeGitPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
