package copier

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// gitPrefixes are URL patterns that indicate a Git repository.
var gitPrefixes = []string{
	"git@", "git://", "git+",
	"ssh://",
	"https://github.com/", "https://gitlab.com/",
}

const gitSuffix = ".git"

// shortcutReplacements maps URL shorthand prefixes to full URLs.
var shortcutReplacements = map[string]string{
	"gh:": "https://github.com/",
	"gl:": "https://gitlab.com/",
}

// shortcutRe matches gh:org/repo or gl:org/repo patterns.
var shortcutRe = regexp.MustCompile(`^(gh|gl):(.+)$`)

// NormalizeURL expands shorthand URLs and detects git repositories.
// Supported shorthands: gh:org/repo, gl:org/repo, git+<url>.
func NormalizeURL(rawURL string) (string, bool) {
	// Handle shorthand prefixes.
	if m := shortcutRe.FindStringSubmatch(rawURL); m != nil {
		prefix := m[1] + ":"
		replacement := shortcutReplacements[prefix]
		expanded := replacement + m[2]
		if !strings.HasSuffix(expanded, gitSuffix) {
			expanded += gitSuffix
		}
		return expanded, true
	}

	// Strip git+ prefix.
	if strings.HasPrefix(rawURL, "git+") {
		return rawURL[4:], true
	}

	// Detect git by known prefixes/suffixes.
	for _, p := range gitPrefixes {
		if strings.HasPrefix(rawURL, p) {
			return rawURL, true
		}
	}
	if strings.HasSuffix(rawURL, gitSuffix) {
		return rawURL, true
	}

	// Check if local path is a git repo.
	if isLocalGitRepo(rawURL) {
		abs, err := filepath.Abs(rawURL)
		if err == nil {
			return abs, true
		}
		return rawURL, true
	}

	return rawURL, false
}

func isLocalGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err == nil {
		return info.IsDir() || info.Mode().IsRegular()
	}
	return false
}

// IsGitURL reports whether url points to a Git repository.
func IsGitURL(url string) bool {
	_, isGit := NormalizeURL(url)
	return isGit
}

// CloneTemplate clones a git template to a temporary directory and checks out the
// specified ref. If ref is empty, the latest semver tag is used.
//
// Uses the git CLI for cloning so that SSH keys, credential helpers, and
// other user-configured auth mechanisms work transparently.
func CloneTemplate(url, ref string, usePreReleases bool) (localPath string, resolvedRef string, err error) {
	url, _ = NormalizeURL(url)

	tmpDir, err := os.MkdirTemp("", "copier-template-*")
	if err != nil {
		return "", "", fmt.Errorf("creating temp dir: %w", err)
	}

	if IsGitInstalled() {
		// Clone via CLI to inherit the user's SSH keys / credential helpers.
		cloneArgs := []string{"clone", "--no-checkout", url, tmpDir}
		cmd := exec.Command("git", cloneArgs...)
		if out, cloneErr := cmd.CombinedOutput(); cloneErr != nil {
			_ = os.RemoveAll(tmpDir)
			return "", "", fmt.Errorf("cloning %s: %s: %w", url, strings.TrimSpace(string(out)), cloneErr)
		}
	} else {
		_, cloneErr := git.PlainClone(tmpDir, false, &git.CloneOptions{
			URL:        url,
			NoCheckout: true,
		})
		if cloneErr != nil {
			_ = os.RemoveAll(tmpDir)
			return "", "", fmt.Errorf("cloning %s: %w", url, cloneErr)
		}
	}

	// Open the cloned repo with go-git for tag/ref resolution.
	repo, err := git.PlainOpen(tmpDir)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("opening cloned repo: %w", err)
	}

	if ref == "" {
		ref, err = latestTag(repo, usePreReleases)
		if err != nil && !errors.Is(err, errNoTags) {
			_ = os.RemoveAll(tmpDir)
			return "", "", err
		}
		// If no tags, use HEAD.
	}

	if ref != "" {
		if err := checkoutRef(repo, ref); err != nil {
			// Fallback: try CLI checkout (handles annotated tags, etc.).
			coCmd := exec.Command("git", "checkout", ref)
			coCmd.Dir = tmpDir
			if out, coErr := coCmd.CombinedOutput(); coErr != nil {
				_ = os.RemoveAll(tmpDir)
				return "", "", fmt.Errorf("checkout %s: %s: %w", ref, strings.TrimSpace(string(out)), coErr)
			}
		}
	} else {
		// Checkout HEAD.
		wt, wtErr := repo.Worktree()
		if wtErr != nil {
			_ = os.RemoveAll(tmpDir)
			return "", "", wtErr
		}
		if coErr := wt.Checkout(&git.CheckoutOptions{}); coErr != nil {
			_ = os.RemoveAll(tmpDir)
			return "", "", coErr
		}
	}

	resolvedRef = ref
	if resolvedRef == "" {
		resolvedRef = "HEAD"
	}

	return tmpDir, resolvedRef, nil
}

var errNoTags = errors.New("no tags found")

func latestTag(repo *git.Repository, includePreReleases bool) (string, error) {
	tags, err := repo.Tags()
	if err != nil {
		return "", fmt.Errorf("listing tags: %w", err)
	}

	var versions []*semver.Version
	tagMap := make(map[string]string) // version string → tag name
	err = tags.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()
		// Strip leading "v" for semver parsing.
		vStr := strings.TrimPrefix(name, "v")
		v, parseErr := semver.NewVersion(vStr)
		if parseErr != nil {
			return nil // skip non-semver tags
		}
		if !includePreReleases && v.Prerelease() != "" {
			return nil
		}
		versions = append(versions, v)
		tagMap[v.String()] = name
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", errNoTags
	}

	sort.Sort(semver.Collection(versions))
	latest := versions[len(versions)-1]
	return tagMap[latest.String()], nil
}

func checkoutRef(repo *git.Repository, ref string) error {
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}

	// Try as tag first.
	tagRef, err := repo.Tag(ref)
	if err == nil {
		return wt.Checkout(&git.CheckoutOptions{Hash: tagRef.Hash()})
	}

	// Try as branch.
	branchRef, err := repo.Reference(plumbing.NewBranchReferenceName(ref), true)
	if err == nil {
		return wt.Checkout(&git.CheckoutOptions{Hash: branchRef.Hash()})
	}

	// Try as remote branch.
	remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", ref), true)
	if err == nil {
		return wt.Checkout(&git.CheckoutOptions{Hash: remoteRef.Hash()})
	}

	// Try as commit hash.
	return wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(ref)})
}

// RepoCommitHash returns the HEAD commit hash for a local git repository.
func RepoCommitHash(repoPath string) (string, error) {
	repo, err := git.PlainOpenWithOptions(repoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}

// RepoCommitDescription returns a git describe-style reference for HEAD.
func RepoCommitDescription(repoPath string) (string, error) {
	if IsGitInstalled() {
		cmd := exec.Command("git", "describe", "--tags", "--always")
		cmd.Dir = repoPath
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return RepoCommitHash(repoPath)
}

// GitInit initialises a new git repository at the given path.
func GitInit(path string) error {
	_, err := git.PlainInit(path, false)
	return err
}

// GitAdd stages all files in a repository.
func GitAdd(repoPath string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	_, err = wt.Add(".")
	return err
}

// GitCommit creates a commit with the given message.
func GitCommit(repoPath, message string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	_, err = wt.Commit(message, &git.CommitOptions{
		AllowEmptyCommits: true,
	})
	return err
}

// GitApplyDiff applies a unified diff to a repository using the git command line.
// go-git does not support git apply, so we shell out.
func GitApplyDiff(repoPath string, diffContent []byte, contextLines int, reject bool) error {
	args := []string{"apply", fmt.Sprintf("-C%d", contextLines)}
	if reject {
		args = append(args, "--reject")
	}
	args = append(args, "-")

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	cmd.Stdin = strings.NewReader(string(diffContent))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply: %s: %w", string(out), err)
	}
	return nil
}

// GitDiff produces a unified diff between two paths using the git command line.
func GitDiff(repoPath string, contextLines int) ([]byte, error) {
	cmd := exec.Command("git", "diff", fmt.Sprintf("-U%d", contextLines), "--no-color")
	cmd.Dir = repoPath
	return cmd.Output()
}

// SyncGitIndexExecutableBit updates the destination git index mode when git is
// configured to ignore filesystem mode changes.
func SyncGitIndexExecutableBit(dstRoot, dstPath string, srcMode os.FileMode) {
	if !IsGitInstalled() {
		return
	}
	configCmd := exec.Command("git", "-C", dstRoot, "config", "--type=bool", "--get", "core.fileMode")
	configOut, err := configCmd.Output()
	if err != nil || strings.TrimSpace(string(configOut)) != "false" {
		return
	}
	topCmd := exec.Command("git", "-C", dstRoot, "rev-parse", "--show-toplevel")
	topOut, err := topCmd.Output()
	if err != nil {
		return
	}
	top := strings.TrimSpace(string(topOut))
	rel, err := filepath.Rel(top, dstPath)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return
	}
	rel = filepath.ToSlash(rel)
	lsCmd := exec.Command("git", "-C", top, "ls-files", "--stage", "--", rel)
	lsOut, err := lsCmd.Output()
	if err != nil {
		return
	}
	line := strings.TrimSpace(string(lsOut))
	if line == "" {
		return
	}
	meta := strings.Fields(strings.SplitN(line, "\t", 2)[0])
	if len(meta) < 2 {
		return
	}
	currentMode := meta[0]
	sha := meta[1]
	desiredExecutable := srcMode&0o111 != 0
	currentExecutable := strings.HasSuffix(currentMode, "755")
	if desiredExecutable == currentExecutable {
		return
	}
	newMode := "100644"
	if desiredExecutable {
		newMode = "100755"
	}
	_ = exec.Command("git", "-C", top, "update-index", "--cacheinfo", fmt.Sprintf("%s,%s,%s", newMode, sha, rel)).Run()
}

// IsGitInstalled reports whether git is available on PATH.
func IsGitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
}
