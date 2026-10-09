package copier

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gitSave(t testing.TB, dir, tag string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "core.hooksPath="+os.DevNull, "commit", "-q", "--allow-empty", "-m", "save", "--no-gpg-sign")
	if tag != "" {
		runGit(t, dir, "tag", tag)
	}
}

const answersTemplate = "# Changes here will be overwritten by Copier; NEVER EDIT MANUALLY\n{{ _copier_answers|to_nice_yaml -}}\n"

func setupUpdateTemplate(t testing.TB) (src, dst string) {
	t.Helper()
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}
	src = t.TempDir()
	dst = t.TempDir()
	writeTree(t, src, map[string]string{
		"copier.yml":                            "name:\n    type: str\n    default: demo\n",
		"{{ _copier_conf.answers_file }}.jinja": answersTemplate,
		"README.md.jinja":                       "# {{ name }}\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n",
		"removed.txt":                           "to be removed in v2",
		".gitignore":                            "*.log\n",
	})
	runGit(t, src, "init", "-q")
	gitSave(t, src, "v1.0.0")

	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true), WithData(map[string]any{"name": "foo"})); err != nil {
		t.Fatal(err)
	}
	runGit(t, dst, "init", "-q")
	gitSave(t, dst, "")
	return src, dst
}

func TestUpdate_ThreeWayMerge(t *testing.T) {
	src, dst := setupUpdateTemplate(t)

	// The user evolves the project.
	readme := readFile(t, filepath.Join(dst, "README.md"))
	mustWriteFile(t, filepath.Join(dst, "README.md"), []byte(strings.Replace(readme, "line8\n", "line8 modified by user\n", 1)), 0o644)
	mustWriteFile(t, filepath.Join(dst, "user.txt"), []byte("mine"), 0o644)
	if err := os.Remove(filepath.Join(dst, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	gitSave(t, dst, "")

	// The template evolves.
	writeTree(t, src, map[string]string{
		"copier.yml":      "name:\n    type: str\n    default: demo\n_migrations:\n    - command: touch after-{{ _version_pep440_from }}-{{ _version_pep440_to }}.txt\n    - version: v2.0.0\n      command: touch before-$VERSION_CURRENT.txt\n      when: \"{{ _stage == 'before' }}\"\n",
		"README.md.jinja": "# {{ name }}\nline1 v2\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n",
		"NEW.txt":         "new file",
		"removed.txt":     "still in template",
	})
	gitSave(t, src, "v2.0.0")

	err := Update(dst, WithQuiet(true), WithDefaults(true))
	var unsafeErr *UnsafeTemplateError
	if !errors.As(err, &unsafeErr) || strings.Join(unsafeErr.Features, ",") != "migrations" {
		t.Fatalf("expected unsafe error for migrations, got %v", err)
	}
	if err := Update(dst, WithQuiet(true), WithDefaults(true), WithUnsafe(true)); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, filepath.Join(dst, "README.md"))
	want := "# foo\nline1 v2\nline2\nline3\nline4\nline5\nline6\nline7\nline8 modified by user\n"
	if got != want {
		t.Fatalf("unexpected merge result:\n%s", got)
	}
	if !exists(filepath.Join(dst, "NEW.txt")) || !exists(filepath.Join(dst, "user.txt")) {
		t.Fatal("new template file or user file missing")
	}
	if exists(filepath.Join(dst, "removed.txt")) {
		t.Fatal("file removed by the user should not be recreated")
	}
	if exists(filepath.Join(dst, "README.md.rej")) {
		t.Fatal("no .rej files expected after a clean inline merge")
	}
	answers, err := LoadAnswersFile(filepath.Join(dst, ".copier-answers.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if answers["_commit"] != "v2.0.0" || answers["name"] != "foo" {
		t.Fatalf("unexpected answers: %v", answers)
	}
	if !exists(filepath.Join(dst, "after-1.0.0-2.0.0.txt")) {
		t.Error("after migration did not run")
	}
	if !exists(filepath.Join(dst, "before-v2.0.0.txt")) {
		t.Error("before migration did not run")
	}
}

func TestUpdate_InlineConflictMarkers(t *testing.T) {
	src, dst := setupUpdateTemplate(t)
	readme := readFile(t, filepath.Join(dst, "README.md"))
	mustWriteFile(t, filepath.Join(dst, "README.md"), []byte(strings.Replace(readme, "line2\n", "line2 user\n", 1)), 0o644)
	gitSave(t, dst, "")

	writeTree(t, src, map[string]string{
		"README.md.jinja": "# {{ name }}\nline1\nline2 template\nline3\nline4\nline5\nline6\nline7\nline8\n",
	})
	gitSave(t, src, "v2.0.0")

	if err := Update(dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dst, "README.md"))
	if !strings.Contains(got, "<<<<<<< before updating") || !strings.Contains(got, ">>>>>>> after updating") {
		t.Fatalf("expected inline conflict markers, got:\n%s", got)
	}
	if !strings.Contains(got, "line2 user") || !strings.Contains(got, "line2 template") {
		t.Fatalf("both sides should be present:\n%s", got)
	}
	if exists(filepath.Join(dst, "README.md.rej")) {
		t.Fatal(".rej file should be removed in inline mode")
	}
	unmerged, err := gitRun(dst, "ls-files", "--unmerged")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unmerged, "README.md") {
		t.Fatalf("expected README.md to be recorded as unmerged, got %q", unmerged)
	}
}

func TestUpdate_RejectStrategy(t *testing.T) {
	src, dst := setupUpdateTemplate(t)
	readme := readFile(t, filepath.Join(dst, "README.md"))
	mustWriteFile(t, filepath.Join(dst, "README.md"), []byte(strings.Replace(readme, "line2\n", "line2 user\n", 1)), 0o644)
	gitSave(t, dst, "")
	writeTree(t, src, map[string]string{
		"README.md.jinja": "# {{ name }}\nline1\nline2 template\nline3\nline4\nline5\nline6\nline7\nline8\n",
	})
	gitSave(t, src, "v2.0.0")

	if err := Update(dst, WithQuiet(true), WithDefaults(true), WithConflict(ConflictReject)); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dst, "README.md.rej")) {
		t.Fatal("expected a .rej file with the rej strategy")
	}
}

func TestUpdate_Preconditions(t *testing.T) {
	src, dst := setupUpdateTemplate(t)
	mustWriteFile(t, filepath.Join(dst, "dirty.txt"), []byte("x"), 0o644)
	if err := Update(dst, WithQuiet(true), WithDefaults(true)); !errors.Is(err, ErrDirtyDestination) {
		t.Fatalf("expected ErrDirtyDestination, got %v", err)
	}
	_ = os.Remove(filepath.Join(dst, "dirty.txt"))

	plain := t.TempDir()
	if err := Copy(src, plain, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if err := Update(plain, WithQuiet(true), WithDefaults(true)); !errors.Is(err, ErrNotGitTracked) {
		t.Fatalf("expected ErrNotGitTracked, got %v", err)
	}

	// Same version: update keeps the version and succeeds.
	if err := Update(dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
}

func TestUpdate_SkipIfExistsAndGitignoredTemplateFiles(t *testing.T) {
	src, dst := setupUpdateTemplate(t)
	writeTree(t, src, map[string]string{
		"copier.yml":      "_skip_if_exists:\n    - keep.txt\nname:\n    type: str\n    default: demo\n",
		"keep.txt":        "template v2",
		"debug.log.jinja": "generated but gitignored v2",
	})
	gitSave(t, src, "v2.0.0")
	writeTree(t, dst, map[string]string{"keep.txt": "user version", "debug.log": "old generated"})
	gitSave(t, dst, "")

	if err := Update(dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "keep.txt")); got != "user version" {
		t.Fatalf("skip_if_exists file should be kept, got %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "debug.log")); got != "generated but gitignored v2" {
		t.Fatalf("template-managed gitignored file should be updated, got %q", got)
	}
}

func TestCloneTemplate_RemoteMirrorCache(t *testing.T) {
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}
	cache := t.TempDir()
	t.Setenv("COPIER_CACHE_DIR", cache)
	src := t.TempDir()
	writeTree(t, src, map[string]string{"copier.yml": "v:\n    default: one\n", "out.jinja": "{{ v }}"})
	runGit(t, src, "init", "-q")
	gitSave(t, src, "v1.0.0")

	url := "git+file://" + src
	dst := t.TempDir()
	if err := Copy(url, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "out")); got != "one" {
		t.Fatalf("unexpected render: %q", got)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	var mirrors []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".git") {
			mirrors = append(mirrors, e.Name())
		}
	}
	if len(mirrors) != 1 {
		t.Fatalf("expected one cached mirror, got %v", mirrors)
	}
	worktrees, err := gitRun("", "--git-dir", filepath.Join(cache, mirrors[0]), "worktree", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(worktrees, "\n") != 0 {
		t.Fatalf("temporary worktrees should be cleaned up, got:\n%s", worktrees)
	}
	answers, err := LoadAnswersFile(filepath.Join(dst, ".copier-answers.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if answers["_src_path"] != url || answers["_commit"] != "v1.0.0" {
		t.Fatalf("unexpected answers: %v", answers)
	}

	// A new tag upstream is picked up through the refreshed mirror.
	writeTree(t, src, map[string]string{"copier.yml": "v:\n    default: two\n"})
	gitSave(t, src, "v2.0.0")
	dst2 := t.TempDir()
	if err := Copy(url, dst2, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst2, "out")); got != "two" {
		t.Fatalf("expected refreshed template, got %q", got)
	}
	result, err := CheckUpdate(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable || result.CurrentVersion != "1.0.0" || result.LatestVersion != "2.0.0" {
		t.Fatalf("unexpected check-update result: %+v", result)
	}
}

func TestCopy_LocalDirtyTemplateIncluded(t *testing.T) {
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	writeTree(t, src, map[string]string{"a.txt": "committed"})
	runGit(t, src, "init", "-q")
	gitSave(t, src, "")
	writeTree(t, src, map[string]string{"a.txt": "dirty", "b.txt": "untracked"})

	dst := t.TempDir()
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dst, "a.txt")) != "dirty" || !exists(filepath.Join(dst, "b.txt")) {
		t.Fatal("dirty changes of a local template should be included when using HEAD")
	}
	// The template repository itself must stay untouched.
	status, err := gitRun(src, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "b.txt") {
		t.Fatalf("template repo should still be dirty, got %q", status)
	}
}

func TestCopy_PrereleaseTags(t *testing.T) {
	if !IsGitInstalled() {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	writeTree(t, src, map[string]string{"v.txt": "1"})
	runGit(t, src, "init", "-q")
	gitSave(t, src, "v1.0.0")
	writeTree(t, src, map[string]string{"v.txt": "2rc"})
	gitSave(t, src, "v2.0.0rc1")

	dst := t.TempDir()
	if err := Copy(src, dst, WithQuiet(true), WithDefaults(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst, "v.txt")); got != "1" {
		t.Fatalf("prerelease should be skipped by default, got %q", got)
	}
	dst2 := t.TempDir()
	if err := Copy(src, dst2, WithQuiet(true), WithDefaults(true), WithPreReleases(true)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dst2, "v.txt")); got != "2rc" {
		t.Fatalf("prerelease should be used when requested, got %q", got)
	}
}
