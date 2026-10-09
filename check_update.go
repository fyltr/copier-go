package copier

import (
	"fmt"
	"path/filepath"
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

	w, err := newWorker(cfg, OpUpdate)
	if err != nil {
		return CheckUpdateResult{}, err
	}
	if err := w.loadSubproject(); err != nil {
		return CheckUpdateResult{}, fmt.Errorf("reading previous answers: %w", err)
	}
	srcPath := w.lastString("_src_path")
	currentRef := w.lastString("_commit")
	if srcPath == "" || currentRef == "" {
		return CheckUpdateResult{}, fmt.Errorf("%w: cannot check because cannot obtain old template references from `%s`",
			ErrConfig, filepath.Join(dst, w.subprojectAnswersFile()))
	}
	current := versionFromCommit(currentRef)
	if current == nil {
		return CheckUpdateResult{}, fmt.Errorf("%w: cannot check: version from last update not detected", ErrVersionNotDetected)
	}

	latest, err := LoadTemplate(resolveStoredSourcePath(srcPath, w.dstAbs), "", cfg.UsePreReleases)
	if err != nil {
		return CheckUpdateResult{}, err
	}
	defer latest.Cleanup()
	if latest.CommitDescription == "" {
		return CheckUpdateResult{}, fmt.Errorf("%w: checking is only supported in git-tracked templates", ErrNotGitTracked)
	}
	latestVer := latest.Version()
	if latestVer == nil {
		return CheckUpdateResult{}, fmt.Errorf("%w: cannot check: version from template not detected", ErrVersionNotDetected)
	}

	return CheckUpdateResult{
		UpdateAvailable: latestVer.GreaterThan(current),
		CurrentVersion:  current.String(),
		LatestVersion:   latestVer.String(),
	}, nil
}
