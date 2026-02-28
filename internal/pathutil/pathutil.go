// Package pathutil provides path validation and manipulation helpers.
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsSubpath reports whether candidate is contained within base,
// preventing symlink-escape and directory-traversal attacks.
func IsSubpath(base, candidate string) (bool, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false, err
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(absBase, absCandidate)
	if err != nil {
		return false, err
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..", nil
}

// EnsureDir creates a directory (and parents) if it does not exist.
func EnsureDir(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// Exists reports whether path exists on disk.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// NormalizeGitPath decodes git's quoted/octal-escaped path encoding.
// Git surrounds paths containing special chars in double-quotes and
// uses octal escapes for non-ASCII bytes.
func NormalizeGitPath(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			case '"':
				b.WriteByte('"')
				i++
			default:
				// Octal escape: \NNN
				if i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
					val := (s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')
					b.WriteByte(val)
					i += 3
				} else {
					b.WriteByte(s[i])
				}
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// RelPath returns the relative path from base to target, panicking on error.
// Use in places where both paths are known to be valid.
func RelPath(base, target string) string {
	r, err := filepath.Rel(base, target)
	if err != nil {
		panic(fmt.Sprintf("pathutil.RelPath(%q, %q): %v", base, target, err))
	}
	return r
}
