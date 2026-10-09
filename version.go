package copier

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// templateVersion is a PEP 440-style version, as used by upstream Copier to
// sort template tags and to decide which migrations apply. It also accepts the
// common semver spellings (`v1.2.3`, `1.2.3-rc.1`).
type templateVersion struct {
	raw     string
	epoch   int
	release []int
	preL    string // normalized: "a", "b" or "rc"
	preN    int
	hasPre  bool
	post    int
	hasPost bool
	dev     int
	hasDev  bool
	local   string
}

var pep440Re = regexp.MustCompile(`(?i)^\s*v?` +
	`(?:(?P<epoch>\d+)!)?` +
	`(?P<release>\d+(?:\.\d+)*)` +
	`(?:[-_.]?(?P<preL>alpha|a|beta|b|preview|pre|c|rc)[-_.]?(?P<preN>\d*))?` +
	`(?P<post>(?:-(?P<postN1>\d+))|(?:[-_.]?(?:post|rev|r)[-_.]?(?P<postN2>\d*)))?` +
	`(?P<dev>[-_.]?dev[-_.]?(?P<devN>\d*))?` +
	`(?:\+(?P<local>[a-z0-9]+(?:[-_.][a-z0-9]+)*))?` +
	`\s*$`)

var gitDescribeRe = regexp.MustCompile(`^(.+)-(\d+)-g([0-9a-fA-F]+)$`)

// parseTemplateVersion parses a version string. It returns an error when the
// string is not a valid version.
func parseTemplateVersion(s string) (*templateVersion, error) {
	m := pep440Re.FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("invalid version %q", s)
	}
	get := func(name string) string { return m[pep440Re.SubexpIndex(name)] }
	v := &templateVersion{raw: s}
	if e := get("epoch"); e != "" {
		v.epoch, _ = strconv.Atoi(e)
	}
	for _, part := range strings.Split(get("release"), ".") {
		n, _ := strconv.Atoi(part)
		v.release = append(v.release, n)
	}
	if l := strings.ToLower(get("preL")); l != "" {
		v.hasPre = true
		switch l {
		case "a", "alpha":
			v.preL = "a"
		case "b", "beta":
			v.preL = "b"
		default:
			v.preL = "rc"
		}
		v.preN, _ = strconv.Atoi(get("preN"))
	}
	if get("post") != "" {
		v.hasPost = true
		if p := get("postN1"); p != "" {
			v.post, _ = strconv.Atoi(p)
		} else {
			v.post, _ = strconv.Atoi(get("postN2"))
		}
	}
	if get("dev") != "" {
		v.hasDev = true
		v.dev, _ = strconv.Atoi(get("devN"))
	}
	v.local = strings.ToLower(get("local"))
	return v, nil
}

// versionFromCommit converts a `git describe --tags --always` output into a
// version, mirroring upstream Copier: `<tag>-<count>-g<hash>` becomes
// `<tag>.post<count>+<hash>`. It returns nil when no version can be detected.
func versionFromCommit(commit string) *templateVersion {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return nil
	}
	if m := gitDescribeRe.FindStringSubmatch(commit); m != nil {
		if v, err := parseTemplateVersion(fmt.Sprintf("%s.post%s+g%s", m[1], m[2], m[3])); err == nil {
			return v
		}
	}
	if v, err := parseTemplateVersion(commit); err == nil {
		return v
	}
	return nil
}

// IsPrerelease reports whether the version is a pre-release or dev release.
func (v *templateVersion) IsPrerelease() bool { return v.hasPre || v.hasDev }

// String returns the normalized public version (plus local segment, if any).
func (v *templateVersion) String() string {
	var b strings.Builder
	if v.epoch != 0 {
		fmt.Fprintf(&b, "%d!", v.epoch)
	}
	parts := make([]string, len(v.release))
	for i, n := range v.release {
		parts[i] = strconv.Itoa(n)
	}
	b.WriteString(strings.Join(parts, "."))
	if v.hasPre {
		fmt.Fprintf(&b, "%s%d", v.preL, v.preN)
	}
	if v.hasPost {
		fmt.Fprintf(&b, ".post%d", v.post)
	}
	if v.hasDev {
		fmt.Fprintf(&b, ".dev%d", v.dev)
	}
	if v.local != "" {
		fmt.Fprintf(&b, "+%s", v.local)
	}
	return b.String()
}

// Compare returns -1, 0 or 1 following PEP 440 ordering rules.
func (v *templateVersion) Compare(o *templateVersion) int {
	if v.epoch != o.epoch {
		return cmpInt(v.epoch, o.epoch)
	}
	if c := cmpRelease(v.release, o.release); c != 0 {
		return c
	}
	// Pre-release key: dev-only releases sort before everything, final releases
	// sort after every pre-release.
	pk := func(x *templateVersion) (int, string, int) {
		switch {
		case !x.hasPre && !x.hasPost && x.hasDev:
			return -1, "", 0
		case !x.hasPre:
			return 1, "", 0
		default:
			return 0, x.preL, x.preN
		}
	}
	vk, vl, vn := pk(v)
	ok, ol, on := pk(o)
	if vk != ok {
		return cmpInt(vk, ok)
	}
	if vk == 0 {
		if vl != ol {
			return strings.Compare(vl, ol)
		}
		if vn != on {
			return cmpInt(vn, on)
		}
	}
	vp, op := -1, -1
	if v.hasPost {
		vp = v.post
	}
	if o.hasPost {
		op = o.post
	}
	if vp != op {
		return cmpInt(vp, op)
	}
	vd, od := int(^uint(0)>>1), int(^uint(0)>>1)
	if v.hasDev {
		vd = v.dev
	}
	if o.hasDev {
		od = o.dev
	}
	if vd != od {
		return cmpInt(vd, od)
	}
	return 0
}

// GreaterThan reports whether v > o.
func (v *templateVersion) GreaterThan(o *templateVersion) bool { return v.Compare(o) > 0 }

// LessThan reports whether v < o.
func (v *templateVersion) LessThan(o *templateVersion) bool { return v.Compare(o) < 0 }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpRelease(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return cmpInt(x, y)
		}
	}
	return 0
}
