package copier

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gobwas/glob"
)

// CopyFile copies a single file preserving permissions.
func CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer srcFile.Close()

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
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copying to %s: %w", dst, err)
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

// PatternMatcher compiles glob patterns and provides matching against relative paths.
type PatternMatcher struct {
	patterns []glob.Glob
	raw      []string
}

// NewPatternMatcher compiles the given glob patterns.
func NewPatternMatcher(patterns []string) *PatternMatcher {
	compiled := make([]glob.Glob, 0, len(patterns))
	for _, p := range patterns {
		g, err := glob.Compile(p, filepath.Separator)
		if err != nil {
			// If pattern fails to compile, store as literal.
			g, _ = glob.Compile(glob.QuoteMeta(p), filepath.Separator)
		}
		compiled = append(compiled, g)
	}
	return &PatternMatcher{patterns: compiled, raw: patterns}
}

// Matches reports whether path matches any of the compiled patterns.
func (m *PatternMatcher) Matches(path string) bool {
	// Normalise separators.
	path = filepath.ToSlash(path)
	for _, g := range m.patterns {
		if g.Match(path) {
			return true
		}
	}
	return false
}

// WalkTemplate walks the template directory and calls fn for each file/dir
// that is not excluded. Paths passed to fn are relative to root.
func WalkTemplate(root string, excludeMatcher *PatternMatcher, fn func(relPath string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		if excludeMatcher.Matches(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		return fn(rel, d)
	})
}

// WriteAnswersFile writes the copier answers to the destination.
func WriteAnswersFile(path string, answers map[string]any, metadata map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write header comment.
	if _, err := fmt.Fprintln(f, "# Changes here will be overwritten by Copier; NEVER EDIT MANUALLY"); err != nil {
		return err
	}

	// Merge metadata and answers.
	data := make(map[string]any, len(answers)+len(metadata))
	for k, v := range metadata {
		data[k] = v
	}
	for k, v := range answers {
		data[k] = v
	}

	// Sorted keys for deterministic output.
	keys := sortedKeys(data)
	for _, k := range keys {
		line, err := marshalYAMLValue(k, data[k])
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(f, line); err != nil {
			return err
		}
	}

	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func marshalYAMLValue(key string, val any) (string, error) {
	switch v := val.(type) {
	case string:
		if strings.ContainsAny(v, "\n:{}[]#&*!|>'\"%@`") || v == "" {
			return fmt.Sprintf("%s: %q", key, v), nil
		}
		return fmt.Sprintf("%s: %s", key, v), nil
	case bool:
		if v {
			return fmt.Sprintf("%s: true", key), nil
		}
		return fmt.Sprintf("%s: false", key), nil
	case int, int64:
		return fmt.Sprintf("%s: %d", key, v), nil
	case float64:
		return fmt.Sprintf("%s: %g", key, v), nil
	case nil:
		return fmt.Sprintf("%s: null", key), nil
	default:
		return fmt.Sprintf("%s: %v", key, v), nil
	}
}

// LoadAnswersFile reads a .copier-answers.yml file.
func LoadAnswersFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var answers map[string]any
	if err := yamlUnmarshal(data, &answers); err != nil {
		return nil, fmt.Errorf("parsing answers file %s: %w", path, err)
	}
	return answers, nil
}

func yamlUnmarshal(data []byte, v any) error {
	// Filter out comment lines for cleaner parsing.
	lines := strings.Split(string(data), "\n")
	var filtered []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		filtered = append(filtered, line)
	}

	return yamlUnmarshalRaw([]byte(strings.Join(filtered, "\n")), v)
}

// yamlUnmarshalRaw is a thin wrapper to allow import of gopkg.in/yaml.v3
// only once throughout the package.
func yamlUnmarshalRaw(data []byte, v any) error {
	// Imported in template.go; re-use indirectly.
	return yamlUnmarshalDirect(data, v)
}
