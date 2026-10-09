package copier

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

// Settings holds user-level copier configuration loaded from disk.
type Settings struct {
	// Defaults maps question names to default values.
	Defaults map[string]any `yaml:"defaults,omitempty"`

	// Trust lists repository URLs or prefixes that are allowed to run unsafe features.
	Trust []string `yaml:"trust,omitempty"`
}

// LoadSettings reads the user settings file. It checks, in order:
//  1. $COPIER_SETTINGS_PATH
//  2. <XDG_CONFIG_HOME>/copier/settings.yml
//
// Returns an empty Settings (not an error) if no file exists.
func LoadSettings() (*Settings, error) {
	path := settingsPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if os.Getenv("COPIER_SETTINGS_PATH") != "" {
			fmt.Fprintf(os.Stderr, "Settings file not found at %s\n", path)
		}
		return &Settings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}
	var s Settings
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%w: parsing %s: %v", ErrSettings, path, err)
	}
	return &s, nil
}

func settingsPath() string {
	if p := os.Getenv("COPIER_SETTINGS_PATH"); p != "" {
		return p
	}
	return filepath.Join(xdg.ConfigHome, "copier", "settings.yml")
}

// IsTrusted checks whether repo matches any entry in the trust list.
// An entry matches exactly, or as a prefix if it ends with "/". Both sides are
// normalized first, and only unambiguous URLs (see isSafeTrustURL) are
// normalized; any other URL is trusted only by an exact, verbatim entry.
func (s *Settings) IsTrusted(repo string) bool {
	if s == nil {
		return false
	}
	return isTrustedRepository(s.Trust, repo)
}

// DefaultFor returns the default value for a question, if one is configured.
func (s *Settings) DefaultFor(name string) (any, bool) {
	if s == nil || s.Defaults == nil {
		return nil, false
	}
	v, ok := s.Defaults[name]
	return v, ok
}

func isTrustedRepository(trust []string, repo string) bool {
	repoIsSafe := isSafeTrustURL(repo)
	normalized := normalizeTrustURL(repo)
	for _, t := range trust {
		switch {
		case repoIsSafe && isSafeTrustURL(t):
			if strings.HasSuffix(t, "/") {
				// Safe prefix: trust anything nested under it.
				if strings.HasPrefix(normalized, normalizeTrustURL(t)) {
					return true
				}
			} else if normalized == normalizeTrustURL(t) {
				// Safe exact: trust only the exact normalized match.
				return true
			}
		case repo == t:
			// Unsafe: trust only an exact raw match.
			return true
		}
	}
	return false
}

// scpURLRe matches Git's SCP-like syntax: [user@]host:path
var scpURLRe = regexp.MustCompile(`^(?:[^/@:\s]+@)?[^/:\s]+:.+$`)

// safeURLPathSegmentRe matches RFC 3986 §2.3 "unreserved" characters: letters,
// digits, `-`, `.`, `_`, `~`.
var safeURLPathSegmentRe = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// isURLStyle reports whether u is an absolute URL or uses an alias prefix.
func isURLStyle(u string) bool {
	return strings.Contains(u, "://") || hasAliasPrefix(u)
}

// isSCPStyle reports whether u uses Git's SCP-like syntax ([user@]host:path).
func isSCPStyle(u string) bool {
	return !isWindowsAbsPath(u) && scpURLRe.MatchString(u)
}

// isSafeTrustURL mirrors upstream Copier's `_is_safe_url`: local paths are
// always safe, and remote references are safe when every path segment consists
// only of RFC 3986 "unreserved" characters. Any other character (percent
// encoding, backslashes, doubled slashes) may be resolved differently by a Git
// transport or server than by the trust check.
func isSafeTrustURL(u string) bool {
	var p string
	switch {
	case isURLStyle(u):
		p = urlSplitPath(u)
	case isSCPStyle(u):
		_, p, _ = strings.Cut(u, ":")
	default:
		return true
	}
	segments := strings.Split(p, "/")
	if len(segments) > 0 && segments[0] == "" {
		segments = segments[1:]
	}
	if len(segments) > 0 && segments[len(segments)-1] == "" {
		segments = segments[:len(segments)-1]
	}
	for _, segment := range segments {
		if !safeURLPathSegmentRe.MatchString(segment) {
			return false
		}
	}
	return true
}

// urlSplitPath returns the raw (still percent-encoded) path of u, like the
// path component of Python's urllib.parse.urlsplit.
func urlSplitPath(u string) string {
	rest := u
	if i := strings.IndexByte(rest, ':'); i > 0 && isURLScheme(rest[:i]) {
		rest = rest[i+1:]
	}
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		i := strings.IndexAny(rest, "/?#")
		if i < 0 {
			return ""
		}
		rest = rest[i:]
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func isURLScheme(s string) bool {
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return s != ""
}

// normalizeTrustURL mirrors upstream Copier's `_normalize` for trust checks:
// dot segments of URL and SCP-style paths are resolved with POSIX semantics,
// and local paths get `~` expanded and are cleaned with OS semantics.
func normalizeTrustURL(u string) string {
	if isURLStyle(u) {
		parsed, err := url.Parse(u)
		if err == nil {
			if parsed.Opaque != "" || (parsed.Host == "" && !strings.Contains(u, "://")) {
				// Alias form such as `gh:org/repo`.
				rest := parsed.Opaque
				if rest == "" {
					rest = parsed.Path
				}
				out := parsed.Scheme + ":" + normalizeURLPath(rest)
				if parsed.RawQuery != "" {
					out += "?" + parsed.RawQuery
				}
				if parsed.Fragment != "" {
					out += "#" + parsed.Fragment
				}
				return out
			}
			netloc := parsed.Host
			if parsed.User != nil {
				netloc = parsed.User.String() + "@" + netloc
			}
			out := parsed.Scheme + "://" + netloc + normalizeURLPath(parsed.EscapedPath())
			if parsed.RawQuery != "" {
				out += "?" + parsed.RawQuery
			}
			if parsed.Fragment != "" {
				out += "#" + parsed.Fragment
			}
			return out
		}
	}

	if isSCPStyle(u) {
		host, p, _ := strings.Cut(u, ":")
		return host + ":" + normalizeURLPath(p)
	}

	if strings.HasPrefix(u, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if u == "~" || strings.HasPrefix(u, "~/") || strings.HasPrefix(u, "~\\") {
				u = home + u[1:]
			}
		}
	}
	normalized := filepath.Clean(u)
	if (strings.HasSuffix(u, "/") || strings.HasSuffix(u, string(filepath.Separator))) &&
		!strings.HasSuffix(normalized, string(filepath.Separator)) {
		normalized += string(filepath.Separator)
	}
	return normalized
}

func hasAliasPrefix(u string) bool {
	for prefix := range shortcutReplacements {
		if strings.HasPrefix(u, prefix) {
			return true
		}
	}
	return false
}

func isWindowsAbsPath(p string) bool {
	if len(p) >= 3 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return strings.HasPrefix(p, `\\`)
}

// normalizeURLPath resolves `.`/`..` segments and collapses redundant `/` in
// p. This uses POSIX path semantics, not RFC 3986 path normalization.
func normalizeURLPath(p string) string {
	if p == "" {
		return p
	}
	out := path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return out
}
