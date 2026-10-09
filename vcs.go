package copier

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/adrg/xdg"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// gitPrefixes are URL patterns that indicate a Git repository. `ssh://` is a
// copier-go addition to the upstream list.
var gitPrefixes = []string{
	"git@", "git://", "git+", "ssh://",
	"https://github.com/", "https://gitlab.com/",
}

const gitSuffix = ".git"

// shortcutReplacements maps URL shorthand prefixes to full URLs.
var shortcutReplacements = map[string]string{
	"gh:": "https://github.com",
	"gl:": "https://gitlab.com",
}

// shortcutRe matches gh:org/repo or gl:org/repo patterns (an optional slash
// after the alias is tolerated, like upstream).
var shortcutRe = regexp.MustCompile(`^(gh|gl):/?(.*)$`)

// cacheDirEnvVar overrides the on-disk location of the git mirror cache.
const cacheDirEnvVar = "COPIER_CACHE_DIR"

const (
	gitUserName  = "Copier"
	gitUserEmail = "copier@copier"
)

// repoRef is the result of resolving a template source into a git origin.
type repoRef struct {
	url   string // Git-parseable URL, or local path.
	isGit bool
	local bool // True when url is a local repository path.
}

// NormalizeURL expands shorthand URLs and detects git repositories.
// Supported shorthands: gh:org/repo, gl:org/repo, git+<url>.
func NormalizeURL(rawURL string) (string, bool) {
	r := resolveRepo(rawURL)
	return r.url, r.isGit
}

// resolveRepo mirrors upstream `get_repo`.
func resolveRepo(raw string) repoRef {
	u := raw
	if m := shortcutRe.FindStringSubmatch(u); m != nil {
		rest := m[2]
		if !strings.HasSuffix(rest, gitSuffix) {
			rest += gitSuffix
		}
		u = shortcutReplacements[m[1]+":"] + "/" + rest
	}
	if strings.HasSuffix(u, gitSuffix) || hasAnyPrefix(u, gitPrefixes) {
		if strings.HasPrefix(u, "git+") {
			return repoRef{url: u[4:], isGit: true}
		}
		if strings.HasPrefix(u, "https://") && !strings.HasSuffix(u, gitSuffix) {
			return repoRef{url: u + gitSuffix, isGit: true}
		}
		return repoRef{url: u, isGit: true}
	}

	p := u
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil && (p == "~" || strings.HasPrefix(p, "~/")) {
			p = home + p[1:]
		}
	}
	if isGitRepoRoot(p) || isGitBundle(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		return repoRef{url: abs, isGit: true, local: true}
	}
	return repoRef{url: raw}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// IsGitURL reports whether url points to a Git repository.
func IsGitURL(url string) bool {
	return resolveRepo(url).isGit
}

// IsGitInstalled reports whether git is available on PATH.
func IsGitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// gitEnv returns the environment for git commands, with a fixed committer
// identity so internal commits never depend on user configuration.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME="+gitUserName,
		"GIT_AUTHOR_EMAIL="+gitUserEmail,
		"GIT_COMMITTER_NAME="+gitUserName,
		"GIT_COMMITTER_EMAIL="+gitUserEmail,
	)
}

// gitRun runs git in dir and returns its trimmed standard output.
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitRunIn runs git with stdin content and returns its trimmed output; the
// exit code is reported through the error.
func gitRunIn(dir, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(stdin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// isGitRepoRoot reports whether path is the root of a git working tree.
func isGitRepoRoot(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	if !IsGitInstalled() {
		gitPath := filepath.Join(path, ".git")
		st, err := os.Stat(gitPath)
		return err == nil && (st.IsDir() || st.Mode().IsRegular())
	}
	top, err := gitRun(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	topResolved, err1 := filepath.EvalSymlinks(top)
	pathResolved, err2 := filepath.EvalSymlinks(path)
	if err1 != nil || err2 != nil {
		return false
	}
	return topResolved == pathResolved
}

// isGitBundle reports whether path is a git bundle file.
func isGitBundle(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || !IsGitInstalled() {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	tmp, err := os.MkdirTemp("", "copier-is-bundle-*")
	if err != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if _, err := gitRun(tmp, "init", "-q"); err != nil {
		return false
	}
	_, err = gitRun(tmp, "bundle", "verify", abs)
	return err == nil
}

// isInGitRepo reports whether path is inside a git working tree.
func isInGitRepo(path string) bool {
	if !IsGitInstalled() {
		return false
	}
	_, err := gitRun(path, "rev-parse", "--show-toplevel")
	return err == nil
}

// gitTopLevel returns the root of the working tree containing path.
func gitTopLevel(path string) (string, error) {
	top, err := gitRun(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		return resolved, nil
	}
	return top, nil
}

// gitIsDirty reports whether the working tree at path has uncommitted changes.
func gitIsDirty(path string) bool {
	out, err := gitRun(path, "status", path, "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

// --- template checkout ------------------------------------------------------

// templateCheckout is a temporary working copy of a template.
type templateCheckout struct {
	path   string
	ref    string
	mirror string
}

// CloneTemplate clones a git template to a temporary directory and checks out the
// specified ref. If ref is empty, the latest version tag is used (or HEAD).
func CloneTemplate(url, ref string, usePreReleases bool) (localPath string, resolvedRef string, err error) {
	co, err := cloneTemplate(resolveRepo(url), ref, usePreReleases)
	if err != nil {
		return "", "", err
	}
	return co.path, co.ref, nil
}

func cloneTemplate(repo repoRef, ref string, usePreReleases bool) (*templateCheckout, error) {
	if !repo.isGit {
		repo = resolveRepo(repo.url)
	}
	if !IsGitInstalled() {
		return cloneWithGoGit(repo.url, ref, usePreReleases)
	}

	location, err := os.MkdirTemp("", "copier-template-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	// Work with the resolved path so that relative paths computed against the
	// template root stay consistent (e.g. /var vs /private/var on macOS).
	if resolved, err := filepath.EvalSymlinks(location); err == nil {
		location = resolved
	}
	cleanup := func() { _ = os.RemoveAll(location) }

	if isRemoteURL(repo) {
		mirror, err := getOrCreateMirror(repo.url)
		if err != nil {
			cleanup()
			return nil, err
		}
		if ref == "" {
			ref = latestTag(mirror, usePreReleases)
		}
		if err := cloneViaCache(ref, location, mirror); err != nil {
			cleanup()
			return nil, err
		}
		return &templateCheckout{path: location, ref: ref, mirror: mirror}, nil
	}

	if ref == "" {
		target := repo.url
		if abs, err := filepath.Abs(target); err == nil {
			target = abs
		}
		ref = latestTag(target, usePreReleases)
	}
	if err := cloneLocal(repo.url, ref, location); err != nil {
		cleanup()
		return nil, err
	}
	return &templateCheckout{path: location, ref: ref}, nil
}

// isRemoteURL tells whether the repository should be cached as a mirror.
// Local repositories keep the plain clone behavior (including dirty changes).
func isRemoteURL(repo repoRef) bool {
	if repo.local {
		return false
	}
	if _, err := os.Stat(repo.url); err == nil {
		return false
	}
	return true
}

// latestTag returns the latest version tag of the repository at target (a URL
// or a local path), or "HEAD" when it has no valid version tags.
func latestTag(target string, usePreReleases bool) string {
	out, err := gitRun("", "ls-remote", "--tags", "--refs", target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No git tags found in template; using HEAD as ref")
		return "HEAD"
	}
	type tagged struct {
		name string
		ver  *templateVersion
	}
	var tags []tagged
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/tags/")
		v, err := parseTemplateVersion(name)
		if err != nil {
			continue
		}
		if !usePreReleases && v.IsPrerelease() {
			continue
		}
		tags = append(tags, tagged{name: name, ver: v})
	}
	if len(tags) == 0 {
		fmt.Fprintln(os.Stderr, "No git tags found in template; using HEAD as ref")
		return "HEAD"
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].ver.GreaterThan(tags[j].ver) })
	return tags[0].name
}

// cacheDir returns the directory where cached git mirrors are stored.
func cacheDir() string {
	if override := os.Getenv(cacheDirEnvVar); override != "" {
		return override
	}
	return filepath.Join(xdg.CacheHome, "copier", "git")
}

// stripCredentials removes any embedded `user:password@` from the URL so that
// the cache key never depends on secrets.
func stripCredentials(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

func mirrorPath(raw string) string {
	digest := sha256.Sum256([]byte(stripCredentials(raw)))
	return filepath.Join(cacheDir(), hex.EncodeToString(digest[:])+".git")
}

func isValidMirror(mirror string) bool {
	info, err := os.Stat(filepath.Join(mirror, "objects"))
	if err != nil || !info.IsDir() {
		return false
	}
	out, err := gitRun("", "--git-dir", mirror, "rev-parse", "--is-bare-repository")
	return err == nil && out == "true"
}

// getOrCreateMirror returns a cached `--mirror` clone of the URL, creating or
// refreshing it.
func getOrCreateMirror(raw string) (string, error) {
	mirror := mirrorPath(raw)
	if isValidMirror(mirror) {
		if _, err := gitRun("", "--git-dir", mirror, "remote", "update", "--prune"); err != nil {
			return "", fmt.Errorf("refreshing template cache: %w", err)
		}
		_, _ = gitRun("", "--git-dir", mirror, "worktree", "prune")
		return mirror, nil
	}
	if _, err := os.Stat(mirror); err == nil {
		_ = os.RemoveAll(mirror)
	}
	parent := filepath.Dir(mirror)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, "copier-clone-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	stagingRepo := filepath.Join(staging, "repo.git")
	if _, err := gitRun("", "clone", "--mirror", raw, stagingRepo); err != nil {
		return "", fmt.Errorf("cloning %s: %w", raw, err)
	}
	if err := os.Rename(stagingRepo, mirror); err != nil {
		if !isValidMirror(mirror) {
			return "", err
		}
	}
	return mirror, nil
}

// cloneViaCache creates a temporary worktree of the mirror at ref.
func cloneViaCache(ref, location, mirror string) error {
	_ = os.Remove(location) // `git worktree add` refuses an existing path.
	if _, err := gitRun("", "--git-dir", mirror, "worktree", "add", "--detach", "--force", location, ref); err != nil {
		return fmt.Errorf("checking out %s: %w", ref, err)
	}
	// Worktrees share the mirror's config, so `git submodule update --init`
	// from an earlier checkout may have registered `submodule.<name>.url`
	// entries that override the current `.gitmodules` (e.g. after a submodule
	// moved to a new repository). Synchronizing the URLs uses the current one.
	if _, err := gitRun(location, "submodule", "sync", "--recursive"); err != nil {
		return err
	}
	if _, err := gitRun(location, "submodule", "update", "--checkout", "--init", "--recursive", "--force"); err != nil {
		return err
	}
	return nil
}

// removeWorktree unregisters and removes a worktree created from a mirror.
func removeWorktree(mirror, location string) {
	if IsGitInstalled() {
		_, _ = gitRun("", "--git-dir", mirror, "worktree", "remove", "--force", location)
		_, _ = gitRun("", "--git-dir", mirror, "worktree", "prune")
	}
}

// cloneLocal clones a local repository, including dirty changes when checking
// out HEAD, like upstream Copier.
func cloneLocal(src, ref, location string) error {
	args := []string{"clone", "--no-checkout", src, location}
	if !isShallowRepo(src) {
		args = append(args, "--filter=blob:none")
	} else {
		fmt.Fprintf(os.Stderr, "The repository '%s' is a shallow clone, this might lead to unexpected failure or unusually high resource consumption.\n", src)
	}
	if _, err := gitRun("", args...); err != nil {
		return fmt.Errorf("cloning %s: %w", src, err)
	}

	srcAbs, err := filepath.Abs(src)
	if err == nil && ref == "HEAD" {
		if info, statErr := os.Stat(srcAbs); statErr == nil && info.IsDir() {
			if dirty, _ := gitRun(srcAbs, "status", "--porcelain"); strings.TrimSpace(dirty) != "" {
				if _, err := gitRun(location, "--git-dir=.git", "--work-tree="+srcAbs, "add", "-A"); err != nil {
					return err
				}
				if _, err := gitRun(location, "--git-dir=.git", "--work-tree="+srcAbs, "commit", "-m",
					"Copier automated commit for draft changes", "--no-verify", "--no-gpg-sign"); err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr, "Dirty template changes included automatically.")
			}
		}
	}

	if _, err := gitRun(location, "-c", "core.fsmonitor=false", "checkout", "-f", ref); err != nil {
		return fmt.Errorf("checkout %s: %w", ref, err)
	}
	if _, err := gitRun(location, "submodule", "update", "--checkout", "--init", "--recursive", "--force"); err != nil {
		return err
	}
	return nil
}

func isShallowRepo(path string) bool {
	p := strings.TrimPrefix(path, "file://")
	out, err := gitRun(p, "rev-parse", "--is-shallow-repository")
	return err == nil && out == "true"
}

// cloneWithGoGit is the fallback used when the git CLI is unavailable. It only
// supports plain clones (no cache, no dirty-changes inclusion, no submodules).
func cloneWithGoGit(rawURL, ref string, usePreReleases bool) (*templateCheckout, error) {
	location, err := os.MkdirTemp("", "copier-template-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(location); err == nil {
		location = resolved
	}
	fail := func(err error) (*templateCheckout, error) {
		_ = os.RemoveAll(location)
		return nil, err
	}
	repo, err := git.PlainClone(location, false, &git.CloneOptions{URL: rawURL, NoCheckout: true})
	if err != nil {
		return fail(fmt.Errorf("cloning %s: %w", rawURL, err))
	}
	if ref == "" {
		ref, err = latestTagGoGit(repo, usePreReleases)
		if err != nil && !errors.Is(err, errNoTags) {
			return fail(err)
		}
		if ref == "" {
			ref = "HEAD"
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fail(err)
	}
	if ref == "HEAD" {
		if err := wt.Checkout(&git.CheckoutOptions{}); err != nil {
			return fail(err)
		}
	} else if err := checkoutRefGoGit(repo, wt, ref); err != nil {
		return fail(fmt.Errorf("checkout %s: %w", ref, err))
	}
	return &templateCheckout{path: location, ref: ref}, nil
}

var errNoTags = errors.New("no tags found")

func latestTagGoGit(repo *git.Repository, includePreReleases bool) (string, error) {
	tags, err := repo.Tags()
	if err != nil {
		return "", fmt.Errorf("listing tags: %w", err)
	}
	var best string
	var bestVer *templateVersion
	err = tags.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()
		v, parseErr := parseTemplateVersion(name)
		if parseErr != nil {
			return nil
		}
		if !includePreReleases && v.IsPrerelease() {
			return nil
		}
		if bestVer == nil || v.GreaterThan(bestVer) {
			best, bestVer = name, v
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if best == "" {
		return "", errNoTags
	}
	return best, nil
}

func checkoutRefGoGit(repo *git.Repository, wt *git.Worktree, ref string) error {
	if tagRef, err := repo.Tag(ref); err == nil {
		hash := tagRef.Hash()
		if tagObj, err := repo.TagObject(hash); err == nil {
			hash = tagObj.Target
		}
		return wt.Checkout(&git.CheckoutOptions{Hash: hash})
	}
	if branchRef, err := repo.Reference(plumbing.NewBranchReferenceName(ref), true); err == nil {
		return wt.Checkout(&git.CheckoutOptions{Hash: branchRef.Hash()})
	}
	if remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", ref), true); err == nil {
		return wt.Checkout(&git.CheckoutOptions{Hash: remoteRef.Hash()})
	}
	return wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(ref)})
}

// RepoCommitHash returns the HEAD commit hash for a local git repository.
func RepoCommitHash(repoPath string) (string, error) {
	if IsGitInstalled() {
		return gitRun(repoPath, "rev-parse", "HEAD")
	}
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

// RepoCommitDescription returns the `git describe --tags --always` output for HEAD.
func RepoCommitDescription(repoPath string) (string, error) {
	if IsGitInstalled() {
		return gitRun(repoPath, "describe", "--tags", "--always")
	}
	return RepoCommitHash(repoPath)
}

// --- update helpers ---------------------------------------------------------

// gitObjectsDir returns the absolute objects directory of the repository at path.
func gitObjectsDir(path string) (string, error) {
	out, err := gitRun(path, "rev-parse", "--git-path", "objects")
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(out) {
		return out, nil
	}
	return filepath.Abs(filepath.Join(path, out))
}

// setGitAlternates lets the repository at path borrow objects from other repositories.
func setGitAlternates(path string, repos ...string) error {
	objects, err := gitObjectsDir(path)
	if err != nil {
		return err
	}
	var lines []string
	for _, r := range repos {
		dir, err := gitObjectsDir(r)
		if err != nil {
			return err
		}
		lines = append(lines, dir)
	}
	infoDir := filepath.Join(objects, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(infoDir, "alternates"), []byte(strings.Join(lines, "\n")), 0o644)
}

// gitInitRepo initializes a repository at dir and commits its contents.
func gitInitRepo(dir string) error {
	if _, err := gitRun(dir, "init", "-q"); err != nil {
		return err
	}
	if _, err := gitRun(dir, "add", "."); err != nil {
		return err
	}
	return gitCommitAll(dir, "dumb commit")
}

// gitCommitAll commits everything, bypassing hooks and signing.
func gitCommitAll(dir, message string) error {
	_, err := gitRun(dir, "-c", "core.hooksPath="+os.DevNull, "commit", "--allow-empty", "-am", message, "--no-gpg-sign", "--no-verify")
	return err
}

// gitLines splits git output into non-empty lines.
func gitLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// SyncGitIndexExecutableBit updates the destination git index mode when git is
// configured to ignore filesystem mode changes.
func SyncGitIndexExecutableBit(dstRoot, dstPath string, srcMode os.FileMode) {
	if !IsGitInstalled() {
		return
	}
	configOut, err := gitRun(dstRoot, "config", "--type=bool", "--get", "core.fileMode")
	if err != nil || configOut != "false" {
		return
	}
	top, err := gitRun(dstRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	rel, err := filepath.Rel(top, dstPath)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return
	}
	rel = filepath.ToSlash(rel)
	line, err := gitRun(top, "ls-files", "--stage", "--", rel)
	if err != nil || line == "" {
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
	_, _ = gitRun(top, "update-index", "--cacheinfo", fmt.Sprintf("%s,%s,%s", newMode, sha, rel))
}

// gitOutput runs git and returns its raw standard output (not trimmed).
func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

// gitRunInBytes runs git with raw stdin content.
func gitRunInBytes(dir string, stdin []byte, args ...string) (string, error) {
	return gitRunIn(dir, string(stdin), args...)
}
