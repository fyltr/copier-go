package copier

import "testing"

func TestParseTemplateVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
		pre  bool
	}{
		{"v1.2.3", "1.2.3", false},
		{"1.2", "1.2", false},
		{"1.0.0rc1", "1.0.0rc1", true},
		{"1.0.0-rc.1", "1.0.0rc1", true},
		{"1.0.0-beta.2", "1.0.0b2", true},
		{"1.0.0a1", "1.0.0a1", true},
		{"1.0.0.dev3", "1.0.0.dev3", true},
		{"1.0.0.post2+gabc123", "1.0.0.post2+gabc123", false},
		{"2024.1", "2024.1", false},
	}
	for _, c := range cases {
		v, err := parseTemplateVersion(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if v.String() != c.want {
			t.Errorf("%s: got %s, want %s", c.in, v.String(), c.want)
		}
		if v.IsPrerelease() != c.pre {
			t.Errorf("%s: prerelease=%v, want %v", c.in, v.IsPrerelease(), c.pre)
		}
	}
	for _, bad := range []string{"", "abc", "1.0.0-alpha.beta", "gabc1234", "abc1234"} {
		if _, err := parseTemplateVersion(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestTemplateVersionOrdering(t *testing.T) {
	ordered := []string{
		"0.9", "1.0.0.dev1", "1.0.0a1", "1.0.0b1", "1.0.0rc1", "1.0.0rc2", "1.0.0", "1.0.0.post1", "1.0.1", "1.1", "2.0.0",
	}
	for i := 1; i < len(ordered); i++ {
		a, _ := parseTemplateVersion(ordered[i-1])
		b, _ := parseTemplateVersion(ordered[i])
		if !a.LessThan(b) {
			t.Errorf("expected %s < %s", ordered[i-1], ordered[i])
		}
	}
	a, _ := parseTemplateVersion("1.0")
	b, _ := parseTemplateVersion("1.0.0")
	if a.Compare(b) != 0 {
		t.Errorf("1.0 should equal 1.0.0")
	}
}

func TestVersionFromCommit(t *testing.T) {
	if v := versionFromCommit("v1.2.3-4-gabc1234"); v == nil || v.String() != "1.2.3.post4+gabc1234" {
		t.Fatalf("unexpected: %v", v)
	}
	if v := versionFromCommit("v1.2.3"); v == nil || v.String() != "1.2.3" {
		t.Fatalf("unexpected: %v", v)
	}
	if v := versionFromCommit("abc1234"); v != nil {
		t.Fatalf("bare hash should not be a version, got %v", v)
	}
	base, _ := parseTemplateVersion("1.2.3")
	next, _ := parseTemplateVersion("1.2.4")
	post := versionFromCommit("v1.2.3-4-gabc1234")
	if !post.GreaterThan(base) || !post.LessThan(next) {
		t.Fatalf("describe version should sort between tags")
	}
}
