// Package textutil provides text manipulation helpers.
package textutil

import (
	"strings"
	"unicode"
)

// EnsureSuffix returns s ending with suffix, appending it only if absent.
func EnsureSuffix(s, suffix string) string {
	if strings.HasSuffix(s, suffix) {
		return s
	}
	return s + suffix
}

// EnsureNewline returns s ending with a newline.
func EnsureNewline(s string) string { return EnsureSuffix(s, "\n") }

// ToBool converts common boolean string representations to bool.
// Recognises: "true","yes","on","1" (case-insensitive) → true;
// "false","no","off","0","" → false. All else returns false, false.
func ToBool(s string) (val bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1":
		return true, true
	case "false", "no", "off", "0", "":
		return false, true
	}
	return false, false
}

// IsBlank reports whether s consists entirely of whitespace (or is empty).
func IsBlank(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
