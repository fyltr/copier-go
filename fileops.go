package copier

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"gopkg.in/yaml.v3"
)

// CopyFile copies a single file preserving permissions.
func CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer func() { _ = srcFile.Close() }()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	defer func() { _ = dstFile.Close() }()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copying to %s: %w", dst, err)
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", dst, err)
	}
	return nil
}

// CopyDir recursively copies a directory tree, preserving permissions and symlinks.
func CopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Handle symlinks.
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}

		return CopyFile(path, target)
	})
}

var lineBreakRe = regexp.MustCompile(`\r\n|\r|\n`)

// splitLines splits on any line-break convention, like upstream Copier does
// when expanding multi-line exclusion entries.
func splitLines(s string) []string { return lineBreakRe.Split(s, -1) }

// PatternMatcher matches relative paths against gitignore-style patterns, the
// same "gitwildmatch" semantics upstream Copier uses through PathSpec.
//
// Blank lines and lines starting with `#` are ignored, so a multi-line entry
// behaves like a complete gitignore file.
type PatternMatcher struct {
	matcher  gitignore.Matcher
	count    int
	negation bool
}

// NewPatternMatcher compiles the given gitignore-style patterns.
func NewPatternMatcher(patterns []string) *PatternMatcher {
	var ps []gitignore.Pattern
	negation := false
	for _, p := range patterns {
		for _, line := range splitLines(p) {
			if !strings.HasSuffix(line, "\\ ") {
				line = strings.TrimRight(line, " \t")
			}
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "!") {
				negation = true
			}
			ps = append(ps, gitignore.ParsePattern(line, nil))
		}
	}
	return &PatternMatcher{matcher: gitignore.NewMatcher(ps), count: len(ps), negation: negation}
}

// hasNegation reports whether any pattern re-includes paths (`!pattern`).
func (m *PatternMatcher) hasNegation() bool { return m != nil && m.negation }

// Matches reports whether the relative file path matches any pattern.
func (m *PatternMatcher) Matches(path string) bool { return m.match(path, false) }

// MatchesDir reports whether the relative directory path matches any pattern.
func (m *PatternMatcher) MatchesDir(path string) bool { return m.match(path, true) }

func (m *PatternMatcher) match(path string, isDir bool) bool {
	if m == nil || m.count == 0 {
		return false
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == "" {
		return false
	}
	return m.matcher.Match(strings.Split(path, "/"), isDir)
}

// fnmatchCase implements Python's fnmatch.fnmatchcase: `*` matches anything
// (including separators), `?` one character, `[seq]`/`[!seq]` character classes.
func fnmatchCase(name, pattern string) bool {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		i++
		switch c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			j := i
			if j < len(pattern) && pattern[j] == '!' {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j >= len(pattern) {
				b.WriteString(`\[`)
			} else {
				stuff := strings.ReplaceAll(pattern[i:j], `\`, `\\`)
				i = j + 1
				if strings.HasPrefix(stuff, "!") {
					stuff = "^" + stuff[1:]
				} else if strings.HasPrefix(stuff, "^") {
					stuff = `\` + stuff
				}
				b.WriteString("[" + stuff + "]")
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return name == pattern
	}
	return re.MatchString(name)
}

// escapeGitPath escapes a literal path so it can be used as a gitwildmatch
// pattern (for example with `git apply --exclude`).
func escapeGitPath(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '\\', '[', ']', '*', '?', '!', '#':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	s := b.String()
	// Escape leading/trailing whitespace.
	lead := len(s) - len(strings.TrimLeft(s, " \t"))
	trail := len(s) - len(strings.TrimRight(s, " \t"))
	if lead == len(s) {
		trail = 0
	}
	var out strings.Builder
	for i, r := range s {
		if (i < lead || i >= len(s)-trail) && (r == ' ' || r == '\t') {
			out.WriteRune('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// answersFileHeader is the comment upstream Copier's default answers file carries.
const answersFileHeader = "# Changes here will be overwritten by Copier; NEVER EDIT MANUALLY\n"

// WriteAnswersFile writes the copier answers to the destination as YAML.
func WriteAnswersFile(path string, answers map[string]any, metadata map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data := make(map[string]any, len(answers)+len(metadata))
	for k, v := range metadata {
		data[k] = v
	}
	for k, v := range answers {
		data[k] = v
	}
	body, err := marshalNiceYAML(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(answersFileHeader+body), 0o644)
}

// marshalNiceYAML marshals a value with the layout `to_nice_yaml` produces
// (block style, sorted keys, 4-space indent).
func marshalNiceYAML(v any) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	out := buf.String()
	if out == "{}\n" {
		out = ""
	}
	return out, nil
}

// LoadAnswersFile reads a .copier-answers.yml file. It returns nil when the
// file does not exist.
func LoadAnswersFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var answers map[string]any
	if err := yaml.Unmarshal(data, &answers); err != nil {
		return nil, fmt.Errorf("parsing answers file %s: %w", path, err)
	}
	if answers == nil {
		answers = map[string]any{}
	}
	return answers, nil
}

// dirComparison holds the names found in two directories being compared.
type dirComparison struct {
	left, right string
	leftOnly    []string
	commonFiles []string
	commonDirs  []string
}

var dirCompareIgnores = map[string]bool{
	"RCS": true, "CVS": true, "tags": true, ".git": true, ".hg": true, ".bzr": true, "_darcs": true, "__pycache__": true,
}

func compareDirs(left, right string) (*dirComparison, error) {
	cmp := &dirComparison{left: left, right: right}
	leftEntries, err := os.ReadDir(left)
	if err != nil {
		return nil, err
	}
	rightEntries, err := os.ReadDir(right)
	if err != nil {
		return nil, err
	}
	rightSet := make(map[string]fs.DirEntry, len(rightEntries))
	for _, e := range rightEntries {
		rightSet[e.Name()] = e
	}
	for _, e := range leftEntries {
		name := e.Name()
		if dirCompareIgnores[name] {
			continue
		}
		r, ok := rightSet[name]
		if !ok {
			cmp.leftOnly = append(cmp.leftOnly, name)
			continue
		}
		if e.IsDir() && r.IsDir() {
			cmp.commonDirs = append(cmp.commonDirs, name)
		} else if !e.IsDir() && !r.IsDir() {
			cmp.commonFiles = append(cmp.commonFiles, name)
		}
	}
	sort.Strings(cmp.leftOnly)
	sort.Strings(cmp.commonFiles)
	sort.Strings(cmp.commonDirs)
	return cmp, nil
}

// removeOldFiles removes from prefix everything that exists only in the "old"
// side of the comparison, recursing into subdirectories. It mirrors upstream
// Copier's `_remove_old_files`.
func removeOldFiles(prefix string, cmp *dirComparison, rmCommon bool) {
	if cmp == nil {
		return
	}
	toRm := append([]string(nil), cmp.leftOnly...)
	if rmCommon {
		toRm = append(toRm, cmp.commonFiles...)
		toRm = append(toRm, cmp.commonDirs...)
	}
	for _, name := range toRm {
		target := filepath.Join(prefix, name)
		info, err := os.Lstat(target)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			// Symlinks are kept, so a renamed directory containing one never
			// deletes files through it.
			continue
		}
		if !info.IsDir() {
			_ = os.Remove(target)
			continue
		}
		sub, err := compareDirs(filepath.Join(cmp.left, name), target)
		if err == nil {
			removeOldFiles(target, sub, true)
		}
		_ = os.Remove(target) // Only succeeds when empty.
	}
	for _, name := range cmp.commonDirs {
		sub, err := compareDirs(filepath.Join(cmp.left, name), filepath.Join(cmp.right, name))
		if err != nil {
			continue
		}
		subdir := filepath.Join(prefix, name)
		if isSymlinkPath(subdir) {
			continue
		}
		removeOldFiles(subdir, sub, false)
		_ = os.Remove(subdir) // Only succeeds when empty.
	}
}

// globRelative expands a glob pattern relative to root, supporting `**` for
// recursive matches like Python's `Path.glob`. Returned paths are absolute.
func globRelative(root, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	if !strings.Contains(pattern, "**") {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		return matches, nil
	}
	segments := strings.Split(pattern, "/")
	var out []string
	var walk func(dir string, segs []string) error
	walk = func(dir string, segs []string) error {
		if len(segs) == 0 {
			out = append(out, dir)
			return nil
		}
		seg := segs[0]
		if seg == "**" {
			if err := walk(dir, segs[1:]); err != nil {
				return err
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil
			}
			for _, e := range entries {
				if e.IsDir() {
					if err := walk(filepath.Join(dir, e.Name()), segs); err != nil {
						return err
					}
				}
			}
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			ok, err := filepath.Match(seg, e.Name())
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			next := filepath.Join(dir, e.Name())
			if len(segs) > 1 && !e.IsDir() {
				continue
			}
			if err := walk(next, segs[1:]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, segments); err != nil {
		return nil, err
	}
	sort.Strings(out)
	// Deduplicate (`**` may reach the same path twice).
	uniq := out[:0]
	for i, p := range out {
		if i == 0 || p != out[i-1] {
			uniq = append(uniq, p)
		}
	}
	return uniq, nil
}
