package copier

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// CheckUpdateResult describes whether a rendered project has a newer template version.
type CheckUpdateResult struct {
	UpdateAvailable bool   `json:"update_available"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
}

// CheckUpdate checks whether dst was generated from an older template version.
func CheckUpdate(dst string, opts ...Option) (CheckUpdateResult, error) {
	cfg := applyOptions(opts)
	cfg.DstPath = dst

	answersPath := cfg.resolvedAnswersFile()
	lastAnswers, err := LoadAnswersFile(answersPath)
	if err != nil {
		return CheckUpdateResult{}, fmt.Errorf("reading previous answers: %w", err)
	}
	if lastAnswers == nil {
		return CheckUpdateResult{}, fmt.Errorf("no previous answers found at %s", answersPath)
	}

	srcPath, _ := lastAnswers["_src_path"].(string)
	if srcPath == "" {
		return CheckUpdateResult{}, fmt.Errorf("cannot determine template source; provide _src_path in answers")
	}
	srcPath = resolveStoredSourcePath(srcPath, dst)

	currentRef := ""
	if cr, ok := lastAnswers["_commit"]; ok {
		currentRef = fmt.Sprintf("%v", cr)
	}
	if currentRef == "" {
		return CheckUpdateResult{}, fmt.Errorf("cannot determine current template version; _commit missing in answers")
	}

	latest, err := LoadTemplate(srcPath, "", cfg.UsePreReleases)
	if err != nil {
		return CheckUpdateResult{}, err
	}
	defer latest.Cleanup()

	latestRef := latest.CommitDescription
	if latestRef == "" {
		latestRef = latest.Ref
	}
	if latestRef == "" {
		latestRef = latest.CommitHash
	}

	currentVersion := displayVersion(currentRef)
	latestVersion := displayVersion(latestRef)
	updateAvailable := isNewerVersion(currentRef, latestRef)
	if !updateAvailable && latest.CommitHash != "" && isCommitHash(currentRef) {
		updateAvailable = currentRef != latest.CommitHash
	}

	return CheckUpdateResult{
		UpdateAvailable: updateAvailable,
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
	}, nil
}

func resolveStoredSourcePath(srcPath, dst string) string {
	if srcPath == "" || filepath.IsAbs(srcPath) || strings.Contains(srcPath, "://") || strings.HasPrefix(srcPath, "git@") {
		return srcPath
	}
	if strings.HasPrefix(srcPath, "gh:") || strings.HasPrefix(srcPath, "gl:") || strings.HasPrefix(srcPath, "git+") {
		return srcPath
	}
	candidate := filepath.Join(dst, srcPath)
	if _, err := filepath.Abs(candidate); err == nil {
		if _, statErr := filepath.EvalSymlinks(candidate); statErr == nil {
			return candidate
		}
	}
	return srcPath
}

func displayVersion(ref string) string {
	ref = strings.TrimSpace(ref)
	if v, err := parseSemverRef(ref); err == nil {
		return v.String()
	}
	return strings.TrimPrefix(ref, "v")
}

func isNewerVersion(currentRef, latestRef string) bool {
	current, currentErr := parseSemverRef(currentRef)
	latest, latestErr := parseSemverRef(latestRef)
	if currentErr == nil && latestErr == nil {
		return latest.GreaterThan(current)
	}
	return currentRef != latestRef
}

func parseSemverRef(ref string) (*semver.Version, error) {
	ref = strings.TrimSpace(strings.TrimPrefix(ref, "v"))
	if idx := strings.Index(ref, "-"); idx >= 0 && strings.Contains(ref[idx+1:], "-g") {
		ref = ref[:idx]
	}
	return semver.NewVersion(ref)
}

var commitHashRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func isCommitHash(ref string) bool {
	return commitHashRe.MatchString(ref)
}
