package copier

import (
	"fmt"
	"os"
	"path/filepath"
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
		return &Settings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}
	var s Settings
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing settings %s: %w", path, err)
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
// An entry matches exactly, or as a prefix if it ends with "/".
func (s *Settings) IsTrusted(repo string) bool {
	for _, t := range s.Trust {
		if t == repo {
			return true
		}
		if strings.HasSuffix(t, "/") && strings.HasPrefix(repo, t) {
			return true
		}
	}
	return false
}

// DefaultFor returns the default value for a question, if one is configured.
func (s *Settings) DefaultFor(name string) (any, bool) {
	if s.Defaults == nil {
		return nil, false
	}
	v, ok := s.Defaults[name]
	return v, ok
}
