package pathutil

import (
	"os"
	"path/filepath"
)

// Resolve returns the absolute path with symlinks evaluated. When the path (or
// part of it) does not exist yet, the deepest existing ancestor is resolved
// and the remaining components are appended unchanged.
func Resolve(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	dir, base := filepath.Split(filepath.Clean(abs))
	dir = filepath.Clean(dir)
	if dir == abs {
		return abs, nil
	}
	if _, err := os.Lstat(dir); err != nil {
		parent, err := Resolve(dir)
		if err != nil {
			return "", err
		}
		return filepath.Join(parent, base), nil
	}
	parent, err := Resolve(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, base), nil
}

// IsWithin reports whether candidate, after resolving symlinks, is contained
// in root (also resolved). A path equal to root counts as within.
func IsWithin(root, candidate string) (bool, error) {
	r, err := Resolve(root)
	if err != nil {
		return false, err
	}
	c, err := Resolve(candidate)
	if err != nil {
		return false, err
	}
	return IsSubpath(r, c)
}
