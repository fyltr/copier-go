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
// normalized first so that encoded or traversing paths cannot bypass a prefix.
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
	normalized := normalizeTrustURL(repo)
	for _, t := range trust {
		if strings.HasSuffix(t, "/") {
			if strings.HasPrefix(normalized, normalizeTrustURL(t)) {
				return true
			}
		} else if normalized == normalizeTrustURL(t) {
			return true
		}
	}
	return false
}

// scpURLRe matches Git's SCP-like syntax: [user@]host:path
var scpURLRe = regexp.MustCompile(`^(?:[^/@:\s]+@)?[^/:\s]+:.+$`)

// normalizeTrustURL mirrors upstream Copier's `_normalize` for trust checks.
func normalizeTrustURL(u string) string {
	if strings.Contains(u, "://") || hasAliasPrefix(u) {
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

	if !isWindowsAbsPath(u) && scpURLRe.MatchString(u) {
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

// normalizeURLPath percent-decodes, folds backslashes to slashes and
// collapses dot segments so encoded traversal cannot bypass a trust prefix.
func normalizeURLPath(p string) string {
	decoded := p
	if d, err := url.PathUnescape(p); err == nil {
		decoded = d
	}
	decoded = strings.ReplaceAll(decoded, "\\", "/")
	if decoded == "" {
		return decoded
	}
	out := path.Clean(decoded)
	if strings.HasSuffix(decoded, "/") && !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return out
}
