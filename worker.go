package copier

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/fyltr/copier-go/internal/pathutil"
	"github.com/fyltr/copier-go/internal/version"
)

// worker is the internal execution engine for copier operations. It mirrors
// upstream Copier's `Worker` class.
type worker struct {
	cfg       Config
	tmpl      *Template
	answers   *AnswersMap
	renderer  *Renderer
	prompter  Prompter
	settings  *Settings
	phase     Phase
	operation Operation
	logger    *slog.Logger

	dstAbs       string
	lastAnswers  map[string]any
	ownsTemplate bool

	copyRoot        string
	excludeMatcher  *PatternMatcher
	skipMatcher     *PatternMatcher
	answersRel      string
	answersRelDone  bool
	answersRendered bool
}

func newWorker(cfg Config, op Operation) (*worker, error) {
	settings, err := LoadSettings()
	if err != nil {
		return nil, err
	}
	prompter := cfg.Prompter
	if prompter == nil {
		prompter = NewTerminalPrompter()
	}
	dstAbs, err := filepath.Abs(cfg.DstPath)
	if err != nil {
		return nil, err
	}
	return &worker{
		cfg:       cfg,
		answers:   NewAnswersMap(),
		prompter:  prompter,
		settings:  settings,
		phase:     PhaseUndefined,
		operation: op,
		logger:    slog.Default(),
		dstAbs:    dstAbs,
	}, nil
}

// subWorker creates a worker sharing this worker's settings and prompter but
// rendering an already loaded template into another destination.
func (w *worker) subWorker(tmpl *Template, dst string, cfg Config) (*worker, error) {
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	cfg.DstPath = dst
	cfg.Prompter = w.prompter
	sw := &worker{
		cfg:       cfg,
		tmpl:      tmpl,
		answers:   NewAnswersMap(),
		prompter:  w.prompter,
		settings:  w.settings,
		phase:     PhaseUndefined,
		operation: w.operation,
		logger:    w.logger,
		dstAbs:    dstAbs,
	}
	sw.renderer = NewRenderer(nil, tmpl.LocalPath, tmpl.Config.Envops)
	return sw, nil
}

// --- subproject -------------------------------------------------------------

// subprojectAnswersFile is the answers file used to locate previous answers:
// the user's choice or the Copier default (the template setting is not known
// before loading the template, exactly like upstream).
func (w *worker) subprojectAnswersFile() string {
	if w.cfg.AnswersFile != "" {
		return w.cfg.AnswersFile
	}
	return AnswersFileName
}

// loadSubproject reads the previous answers from the destination, if any.
func (w *worker) loadSubproject() error {
	raw, err := LoadAnswersFile(filepath.Join(w.dstAbs, w.subprojectAnswersFile()))
	if err != nil {
		return err
	}
	w.lastAnswers = make(map[string]any, len(raw))
	for k, v := range raw {
		if k == "_src_path" || k == "_commit" || !strings.HasPrefix(k, "_") {
			w.lastAnswers[k] = v
		}
	}
	return nil
}

func (w *worker) lastString(key string) string {
	if v, ok := w.lastAnswers[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// resolvedVcsRef returns the VCS ref to use: `:current:` means the ref the
// subproject was last generated with.
func (w *worker) resolvedVcsRef() string {
	if w.cfg.VcsRef == string(VcsRefCurrent) {
		return w.lastString("_commit")
	}
	return w.cfg.VcsRef
}

func (w *worker) ensureTemplate(src, ref string) error {
	if w.tmpl != nil {
		return nil
	}
	tmpl, err := LoadTemplate(src, ref, w.cfg.UsePreReleases)
	if err != nil {
		return err
	}
	w.tmpl = tmpl
	w.ownsTemplate = true
	w.renderer = NewRenderer(nil, tmpl.LocalPath, tmpl.Config.Envops)
	return nil
}

func (w *worker) cleanupTemplate() {
	if w.ownsTemplate && w.tmpl != nil {
		w.tmpl.Cleanup()
	}
}

// --- main operations --------------------------------------------------------

// runCopy generates a subproject from the template.
func (w *worker) runCopy() error {
	if err := w.loadSubproject(); err != nil {
		return err
	}
	src := w.cfg.SrcPath
	if src == "" {
		src = resolveStoredSourcePath(w.lastString("_src_path"), w.dstAbs)
	}
	if src == "" {
		return fmt.Errorf("%w: cannot determine template source; provide a template or ensure _src_path in the answers file", ErrConfig)
	}
	if err := w.ensureTemplate(src, w.resolvedVcsRef()); err != nil {
		return err
	}
	defer w.cleanupTemplate()

	if err := w.checkVersion(); err != nil {
		return err
	}
	if err := w.checkUnsafe(OpCopy, nil); err != nil {
		return err
	}
	return w.doCopy()
}

// runRecopy re-applies the template recorded in the answers file.
func (w *worker) runRecopy() error {
	if err := w.loadSubproject(); err != nil {
		return err
	}
	src := w.lastString("_src_path")
	if src == "" {
		return fmt.Errorf("%w: cannot recopy because cannot obtain old template references from `%s`", ErrConfig, w.subprojectAnswersFile())
	}
	w.cfg.SrcPath = resolveStoredSourcePath(src, w.dstAbs)
	return w.runCopy()
}

// doCopy asks the questions, renders the template and runs its tasks. The
// template must already be loaded.
func (w *worker) doCopy() error {
	w.printMessage(w.tmpl.Config.MessageBeforeCopy)

	w.phase = PhasePrompt
	if err := w.ask(); err != nil {
		return err
	}

	wasExisting := dirExists(w.dstAbs)
	if !w.cfg.Quiet {
		fmt.Fprintf(os.Stderr, "\nCopying from template version %s\n", versionString(w.tmpl.Version()))
	}

	run := func() error {
		w.phase = PhaseRender
		if err := w.renderTemplate(); err != nil {
			return err
		}
		if !w.cfg.Quiet {
			fmt.Fprintln(os.Stderr)
		}
		if !w.cfg.SkipTasks {
			w.phase = PhaseTasks
			if err := w.executeTasks(w.tmpl.Config.Tasks); err != nil {
				return err
			}
		}
		return nil
	}
	if err := run(); err != nil {
		if !wasExisting && w.cfg.CleanupOnError && !w.cfg.Pretend {
			if rmErr := os.RemoveAll(w.dstAbs); rmErr != nil && !os.IsNotExist(rmErr) {
				fmt.Fprintf(os.Stderr, "Failed to clean up %s: %v\n", w.dstAbs, rmErr)
			}
		}
		return err
	}

	w.printMessage(w.tmpl.Config.MessageAfterCopy)
	if !w.cfg.Quiet {
		fmt.Fprintln(os.Stderr)
	}
	return nil
}

func versionString(v *templateVersion) string {
	if v == nil {
		return "None"
	}
	return v.String()
}

// runUpdate updates a subproject with the upstream 3-way merge algorithm.
func (w *worker) runUpdate() error {
	if err := w.loadSubproject(); err != nil {
		return err
	}
	if !isInGitRepo(w.dstAbs) {
		return ErrNotGitTracked
	}
	if gitIsDirty(w.dstAbs) {
		return ErrDirtyDestination
	}
	lastSrc := w.lastString("_src_path")
	lastCommit := w.lastString("_commit")
	if lastSrc == "" || lastCommit == "" {
		return fmt.Errorf("%w: cannot update because cannot obtain old template references from `%s`", ErrConfig, w.subprojectAnswersFile())
	}
	lastSrc = resolveStoredSourcePath(lastSrc, w.dstAbs)

	src := w.cfg.SrcPath
	if src == "" {
		src = lastSrc
	}
	if err := w.ensureTemplate(src, w.resolvedVcsRef()); err != nil {
		return err
	}
	defer w.cleanupTemplate()
	if w.tmpl.CommitDescription == "" {
		return fmt.Errorf("%w: updating is only supported in git-tracked templates", ErrNotGitTracked)
	}

	oldTmpl, err := LoadTemplate(lastSrc, lastCommit, w.cfg.UsePreReleases)
	if err != nil {
		return fmt.Errorf("loading previous template version: %w", err)
	}
	defer oldTmpl.Cleanup()

	oldVer, newVer := oldTmpl.Version(), w.tmpl.Version()
	if oldVer == nil {
		return fmt.Errorf("%w: cannot update: version from last update not detected", ErrVersionNotDetected)
	}
	if newVer == nil {
		return fmt.Errorf("%w: cannot update: version from template not detected", ErrVersionNotDetected)
	}
	if oldVer.GreaterThan(newVer) {
		return fmt.Errorf("%w: you are downgrading from %s to %s", ErrDowngrade, oldVer, newVer)
	}
	// Only git-tracked subprojects can be updated, so the user can review the
	// diff before committing; overwriting is therefore implied.
	w.cfg.Overwrite = true

	if err := w.checkVersion(); err != nil {
		return err
	}
	if err := w.checkUnsafe(OpUpdate, oldTmpl); err != nil {
		return err
	}
	w.printMessage(w.tmpl.Config.MessageBeforeUpdate)
	if !w.cfg.Quiet {
		if oldVer.Compare(newVer) == 0 {
			fmt.Fprintf(os.Stderr, "Keeping template version %s\n", newVer)
		} else {
			fmt.Fprintf(os.Stderr, "Updating to template version %s\n", newVer)
		}
	}
	if err := w.applyUpdate(oldTmpl, lastSrc); err != nil {
		return err
	}
	w.printMessage(w.tmpl.Config.MessageAfterUpdate)
	return nil
}

func (w *worker) applyUpdate(oldTmpl *Template, lastSrc string) error {
	top, err := gitTopLevel(w.dstAbs)
	if err != nil {
		return err
	}
	dstResolved, err := pathutil.Resolve(w.dstAbs)
	if err != nil {
		return err
	}
	subdir, err := filepath.Rel(top, dstResolved)
	if err != nil {
		return err
	}

	oldCopy, err := os.MkdirTemp("", "copier-old-copy-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(oldCopy) }()
	newCopy, err := os.MkdirTemp("", "copier-new-copy-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(newCopy) }()

	// Render the old template version into a temporary destination.
	oldCfg := w.cfg
	oldCfg.SrcPath = lastSrc
	oldCfg.VcsRef = oldTmpl.Ref
	oldCfg.Data = copyMap(w.lastAnswers)
	oldCfg.Defaults = true
	oldCfg.Quiet = true
	oldCfg.Ask = nil
	// Also exclude paths listed in the new template version, so they won't be
	// included in the diff as deleted paths to prevent deletion.
	oldCfg.Exclude = append(append([]string(nil), w.tmpl.Exclusions()...), w.cfg.Exclude...)
	oldWorker, err := w.subWorker(oldTmpl, filepath.Join(oldCopy, subdir), oldCfg)
	if err != nil {
		return err
	}
	if err := oldWorker.loadSubproject(); err != nil {
		return err
	}
	if err := oldWorker.doCopy(); err != nil {
		return fmt.Errorf("rendering old template version: %w", err)
	}

	// Run pre-migration tasks.
	w.answers = NewAnswersMap()
	w.phase = PhaseMigrate
	if err := w.executeTasks(w.tmpl.MigrationTasks("before", oldTmpl)); err != nil {
		return err
	}

	// Create a Git tree object from the current (possibly dirty) index.
	subprojectHead, err := gitRun(top, "write-tree")
	if err != nil {
		return err
	}
	if err := gitInitRepo(oldCopy); err != nil {
		return err
	}
	if err := setGitAlternates(oldCopy, top); err != nil {
		return err
	}
	// Files intentionally removed in the generated project must not be recreated,
	// unless they match skip-if-exists patterns.
	w.buildMatchers()
	removedOut, err := gitRun(oldCopy, "diff-tree", "-r", "--diff-filter=D", "--name-only", "HEAD", subprojectHead)
	if err != nil {
		return err
	}
	var filesRemoved []string
	for _, line := range gitLines(removedOut) {
		p := pathutil.NormalizeGitPath(line)
		if fileExists(filepath.Join(top, p)) || w.skipMatcher.Matches(p) {
			continue
		}
		filesRemoved = append(filesRemoved, p)
	}

	// Reload last answers (migrations may have changed them) unless answered
	// questions are skipped.
	if !w.cfg.SkipAnswered {
		if err := w.loadSubproject(); err != nil {
			return err
		}
	}

	// Do a normal copy in the final destination.
	quiet := w.cfg.Quiet
	w.cfg.Quiet = true
	err = w.doCopy()
	w.cfg.Quiet = quiet
	if err != nil {
		return err
	}
	for _, f := range filesRemoved {
		_ = os.Remove(filepath.Join(top, f))
	}

	// Render the new template version with the same answers in an empty dir.
	newData := make(map[string]any)
	for k, v := range w.answers.Combined() {
		if strings.HasPrefix(k, "_") || w.answers.Hidden[k] || !isJSONSerializable(v) {
			continue
		}
		newData[k] = v
	}
	newCfg := w.cfg
	newCfg.SrcPath = w.tmpl.URL
	newCfg.Data = newData
	newCfg.Defaults = true
	newCfg.Quiet = true
	newCfg.Ask = nil
	newWorker, err := w.subWorker(w.tmpl, filepath.Join(newCopy, subdir), newCfg)
	if err != nil {
		return err
	}
	if err := newWorker.loadSubproject(); err != nil {
		return err
	}
	if err := newWorker.doCopy(); err != nil {
		return fmt.Errorf("rendering new template version: %w", err)
	}
	for _, f := range filesRemoved {
		_ = os.Remove(filepath.Join(newCopy, f))
	}
	if err := gitInitRepo(newCopy); err != nil {
		return err
	}
	newCopyHead, err := gitRun(newCopy, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	// Extract the diff between the old copy and the real destination, with
	// special handling of files added in both the project and the new template.
	if err := setGitAlternates(oldCopy, top, newCopy); err != nil {
		return err
	}
	addedInProject, err := gitRun(oldCopy, "diff-tree", "-r", "--diff-filter=A", "--name-only", "HEAD", subprojectHead)
	if err != nil {
		return err
	}
	addedInTemplate, err := gitRun(oldCopy, "diff-tree", "-r", "--diff-filter=A", "--name-only", "HEAD", newCopyHead)
	if err != nil {
		return err
	}
	templateAdded := make(map[string]bool)
	for _, f := range gitLines(addedInTemplate) {
		templateAdded[f] = true
	}
	for _, f := range gitLines(addedInProject) {
		if !templateAdded[f] {
			continue
		}
		name := pathutil.NormalizeGitPath(f)
		target := filepath.Join(oldCopy, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(filepath.Join(top, name)); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(target, nil, mode); err != nil {
			return err
		}
		if _, err := gitRun(oldCopy, "add", "--force", "--", name); err != nil {
			return err
		}
	}
	if err := gitCommitAll(oldCopy, "add new empty files"); err != nil {
		return err
	}

	unified := fmt.Sprintf("--unified=%d", w.cfg.ContextLines)
	namesOut, err := gitRun(oldCopy, "diff-tree", unified, "HEAD", subprojectHead, "-r", "--no-commit-id", "--name-only", subdir)
	if err != nil {
		return err
	}
	var skipFiles []string
	for _, line := range gitLines(namesOut) {
		p := pathutil.NormalizeGitPath(line)
		rel, err := filepath.Rel(subdir, p)
		if err != nil {
			rel = p
		}
		if w.skipMatcher.Matches(rel) {
			skipFiles = append(skipFiles, escapeGitPath(p))
		}
	}
	diff, err := gitOutput(oldCopy, "diff-tree", unified, "HEAD", subprojectHead, "--inter-hunk-context=-1")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Make sure Git >= 2.24 is installed to improve updates.")
		diff, err = gitOutput(oldCopy, "diff-tree", unified, "HEAD", subprojectHead, "--inter-hunk-context=0")
		if err != nil {
			return err
		}
	}
	compared, err := compareDirs(oldCopy, newCopy)
	if err != nil {
		return err
	}

	// Apply the diff into the final destination.
	applyArgs := []string{"apply", "--reject", "--exclude", filepath.ToSlash(filepath.Join(subdir, w.answersRelpath()))}
	for _, f := range skipFiles {
		applyArgs = append(applyArgs, "--exclude", f)
	}
	ignoredOut, _ := gitRun(top, "status", "--ignored", "--porcelain")
	for _, line := range gitLines(ignoredOut) {
		if !strings.HasPrefix(line, "!! ") {
			continue
		}
		p := line[3:]
		// Template-generated files that happen to be gitignored must still be updated.
		if fileExists(filepath.Join(newCopy, pathutil.NormalizeGitPath(p))) {
			continue
		}
		applyArgs = append(applyArgs, "--exclude", p)
	}
	_, _ = gitRunInBytes(top, diff, applyArgs...)

	if w.cfg.Conflict == ConflictInline {
		if err := w.resolveInlineConflicts(top, oldCopy, newCopy); err != nil {
			return err
		}
	}

	// Remove files and directories only found in the old template version.
	removeOldFiles(top, compared, false)

	// Run post-migration tasks.
	w.phase = PhaseMigrate
	return w.executeTasks(w.tmpl.MigrationTasks("after", oldTmpl))
}

// resolveInlineConflicts converts `.rej` files into inline conflict markers.
func (w *worker) resolveInlineConflicts(top, oldCopy, newCopy string) error {
	statusOut, err := gitRun(top, "status", "--porcelain", "--ignored")
	if err != nil {
		return err
	}
	var conflicted []string
	for _, line := range gitLines(statusOut) {
		if !strings.HasPrefix(line, "?? ") && !strings.HasPrefix(line, "!! ") {
			continue
		}
		fname := pathutil.NormalizeGitPath(line[3:])
		if !strings.HasSuffix(fname, ".rej") {
			continue
		}
		fname = strings.TrimSuffix(fname, ".rej")
		// Undo possible non-rejected chunks, ignoring hooks.
		if _, err := gitRun(top, "-c", "core.hooksPath="+os.DevNull, "checkout", "--", fname); err != nil {
			return err
		}
		// 3-way-merge the file directly.
		_, _ = gitRun(top, "merge-file",
			"-L", "before updating", "-L", "last update", "-L", "after updating",
			fname, filepath.Join(oldCopy, fname), filepath.Join(newCopy, fname))
		_ = os.Remove(filepath.Join(top, fname+".rej"))
		if hasConflictMarkers(filepath.Join(top, fname)) {
			conflicted = append(conflicted, fname)
		}
	}
	if len(conflicted) == 0 {
		return nil
	}
	// Record the merge stages in the index so tools see the conflict.
	stageOut, err := gitRun(top, append([]string{"ls-files", "--stage", "--"}, conflicted...)...)
	if err != nil {
		return err
	}
	var input []string
	for _, line := range gitLines(stageOut) {
		meta, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			continue
		}
		perms, sha := fields[0], fields[1]
		input = append(input, fmt.Sprintf("0 %s\t%s", strings.Repeat("0", 40), p))
		input = append(input, fmt.Sprintf("%s %s 2\t%s", perms, sha, p))
		name := pathutil.NormalizeGitPath(p)
		if oldSha, err := gitRun(top, "hash-object", "-w", filepath.Join(oldCopy, name)); err == nil {
			input = append(input, fmt.Sprintf("%s %s 1\t%s", perms, oldSha, p))
		}
		if newSha, err := gitRun(top, "hash-object", "-w", filepath.Join(newCopy, name)); err == nil {
			input = append(input, fmt.Sprintf("%s %s 3\t%s", perms, newSha, p))
		}
	}
	_, err = gitRunIn(top, strings.Join(input, "\n")+"\n", "update-index", "--index-info")
	return err
}

func hasConflictMarkers(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		t := bytes.TrimRight(line, " \t\r")
		if bytes.Equal(t, []byte("<<<<<<< before updating")) || bytes.Equal(t, []byte(">>>>>>> after updating")) {
			return true
		}
	}
	return false
}

// --- questionnaire ----------------------------------------------------------

func (w *worker) matchesAsk(name string) bool {
	for _, pattern := range w.cfg.Ask {
		if fnmatchCase(name, pattern) {
			return true
		}
	}
	return false
}

// questionError attributes err to a question. Validation and invalid-choice
// errors already name the question, so they are returned as is, like upstream.
func questionError(name string, err error) error {
	var ve *ValidationError
	var ce *InvalidChoiceError
	if errors.As(err, &ve) || errors.As(err, &ce) {
		return err
	}
	return &QuestionError{Name: name, Err: err}
}

// ask runs the questionnaire and records the answers.
func (w *worker) ask() error {
	w.answers = NewAnswersMap()
	w.answers.UserDefaults = copyMap(w.cfg.UserDefaults)
	w.answers.Init = copyMap(w.cfg.Data)
	w.answers.Last = copyMap(w.lastAnswers)
	w.answers.Metadata = w.tmpl.Metadata()
	w.answersRelDone = false
	external, err := w.loadExternalData(false)
	if err != nil {
		return err
	}
	w.answers.External = external

	for _, def := range w.tmpl.Questions {
		name := def.Name
		if def.Secret && !def.HasDefault {
			return &QuestionError{Name: name, Err: fmt.Errorf("%w: secret question requires a default value", ErrConfig)}
		}
		q := newQuestion(def, w.answers, w.settings, w.renderer, w.renderContext(nil))

		// Delete last answer if it cannot be parsed or validated, so a new
		// valid answer can be provided.
		if last, ok := w.answers.Last[name]; ok {
			if parsed, err := q.ParseAnswer(last); err != nil {
				delete(w.answers.Last, name)
			} else if err := q.ValidateAnswer(parsed); err != nil {
				delete(w.answers.Last, name)
			}
		}

		ask, err := q.AskCondition()
		if err != nil {
			return questionError(name, err)
		}
		when, err := q.When()
		if err != nil {
			return questionError(name, err)
		}
		var computedDefault any
		if !when {
			// Omit its answer from the answers file and re-compute it from the default.
			w.answers.Hide(name)
			delete(w.answers.Last, name)
			def, present, err := q.Default()
			if err != nil {
				return questionError(name, err)
			}
			if !present {
				continue
			}
			computedDefault = def
		}

		if !w.matchesAsk(name) {
			if v, ok := w.answers.Init[name]; ok {
				// Answers given as data are parsed and validated like prompted ones.
				answer, err := q.ParseAnswer(v)
				if err != nil {
					return questionError(name, err)
				}
				if err := q.ValidateAnswer(answer); err != nil {
					return err
				}
				w.answers.User[name] = answer
				continue
			}
			if _, ok := w.answers.Last[name]; ok && (w.cfg.SkipAnswered || !ask) {
				continue
			}
			if !ask {
				// Not prompted (`ask: false`) and no previous answer: use the
				// default, if any.
				def, present, err := q.Default()
				if err != nil {
					return questionError(name, err)
				}
				if present {
					w.answers.User[name] = def
				}
				continue
			}
			if w.cfg.Defaults {
				// Default validates the value unless `when` is false or the
				// question is secret.
				def, present, err := q.Default()
				if err != nil {
					return questionError(name, err)
				}
				if !present {
					return &QuestionError{Name: name, Err: ErrQuestionRequired}
				}
				w.answers.User[name] = def
				continue
			}
		}
		if !when {
			// The question is skipped by its condition: use the computed default.
			w.answers.User[name] = computedDefault
			continue
		}

		answer, err := w.prompter.Ask(q)
		if err != nil {
			if errors.Is(err, ErrInteractiveNeeded) {
				return &QuestionError{Name: name, Err: fmt.Errorf("%w: use `--defaults` and/or `--data`/`--data-file`", ErrInteractiveNeeded)}
			}
			return questionError(name, err)
		}
		w.answers.User[name] = answer
	}

	// Reload external data, which may depend on answers.
	external, err = w.loadExternalData(true)
	if err != nil {
		return err
	}
	w.answers.External = external
	w.answersRelDone = false
	return nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// --- render context ---------------------------------------------------------

func osName() any {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	}
	return nil
}

// copierConf builds the `_copier_conf` mapping.
func (w *worker) copierConf(answersFile string) map[string]any {
	data := w.cfg.Data
	if data == nil {
		data = map[string]any{}
	}
	userDefaults := w.cfg.UserDefaults
	if userDefaults == nil {
		userDefaults = map[string]any{}
	}
	srcPath := ""
	vcsRefHash := ""
	if w.tmpl != nil {
		srcPath = w.tmpl.LocalPath
		vcsRefHash = w.tmpl.CommitHash
	}
	conf := map[string]any{
		"src_path":         srcPath,
		"dst_path":         w.cfg.DstPath,
		"answers_file":     answersFile,
		"vcs_ref":          w.resolvedVcsRef(),
		"vcs_ref_hash":     vcsRefHash,
		"data":             data,
		"settings":         map[string]any{"defaults": w.settings.Defaults, "trust": w.settings.Trust},
		"exclude":          w.cfg.Exclude,
		"use_prereleases":  w.cfg.UsePreReleases,
		"skip_if_exists":   w.cfg.Skip,
		"cleanup_on_error": w.cfg.CleanupOnError,
		"defaults":         w.cfg.Defaults,
		"user_defaults":    userDefaults,
		"overwrite":        w.cfg.Overwrite,
		"pretend":          w.cfg.Pretend,
		"quiet":            w.cfg.Quiet,
		"conflict":         string(w.cfg.Conflict),
		"context_lines":    w.cfg.ContextLines,
		"unsafe":           w.cfg.Unsafe,
		"skip_answered":    w.cfg.SkipAnswered,
		"skip_tasks":       w.cfg.SkipTasks,
		"sep":              string(filepath.Separator),
		"os":               osName(),
	}
	return conf
}

// renderContext produces the Jinja render context.
func (w *worker) renderContext(extra map[string]any) map[string]any {
	return w.renderContextWith(w.answersRelpath(), extra)
}

func (w *worker) renderContextWith(answersFile string, extra map[string]any) map[string]any {
	ctx := w.answers.Combined()
	ctx["_copier_answers"] = w.answersToRemember()
	ctx["_copier_conf"] = w.copierConf(answersFile)
	ctx["_folder_name"] = filepath.Base(w.dstAbs)
	ctx["_copier_phase"] = string(w.phase)
	ctx["_copier_operation"] = string(w.operation)
	ctx["pathjoin"] = pathJoin
	for k, v := range extra {
		ctx[k] = v
	}
	return ctx
}

// answersRelpath returns the answers file path relative to the destination:
// the user's choice, the template default or the Copier default, rendered.
func (w *worker) answersRelpath() string {
	if w.answersRelDone {
		return w.answersRel
	}
	p := w.cfg.AnswersFile
	if p == "" && w.tmpl != nil {
		p = w.tmpl.Config.AnswersFile
	}
	if p == "" {
		p = AnswersFileName
	}
	if w.renderer != nil && strings.Contains(p, "{") {
		if rendered, err := w.renderer.RenderString(p, w.renderContextWith("", nil)); err == nil {
			p = rendered
		}
	}
	w.answersRel = filepath.Clean(p)
	w.answersRelDone = true
	return w.answersRel
}

// answersToRemember returns the answers that will be saved in the answers file.
func (w *worker) answersToRemember() map[string]any {
	out := make(map[string]any)
	if w.tmpl == nil {
		return out
	}
	if w.tmpl.CommitDescription != "" {
		out["_commit"] = w.tmpl.CommitDescription
	}
	out["_src_path"] = w.tmpl.URL
	names := w.tmpl.QuestionNames()
	for k, v := range w.answers.Combined() {
		if strings.HasPrefix(k, "_") || w.answers.Hidden[k] || w.tmpl.IsSecret(k) || !names[k] || !isJSONSerializable(v) {
			continue
		}
		out[k] = v
	}
	return out
}

// loadExternalData loads the YAML files listed in `_external_data`.
func (w *worker) loadExternalData(strict bool) (map[string]any, error) {
	external := make(map[string]any, len(w.tmpl.Config.ExternalData))
	if len(w.tmpl.Config.ExternalData) == 0 {
		return external, nil
	}
	phase := w.phase
	w.phase = PhaseUndefined
	defer func() { w.phase = phase }()
	ctx := w.renderContext(nil)
	for name, pathTemplate := range w.tmpl.Config.ExternalData {
		renderedPath, err := w.renderer.RenderString(pathTemplate, ctx)
		if err != nil {
			if !strict {
				external[name] = map[string]any{}
				continue
			}
			return nil, fmt.Errorf("rendering _external_data.%s path: %w", name, err)
		}
		target := renderedPath
		if !filepath.IsAbs(target) {
			target = filepath.Join(w.dstAbs, target)
		}
		target = filepath.Clean(target)
		ok, err := pathutil.IsWithin(w.dstAbs, target)
		if err != nil {
			return nil, err
		}
		if !ok && !w.isTrusted() {
			return nil, fmt.Errorf("%w: _external_data.%s reads %s outside %s.\nIf you trust this path, you can override the check:\n  - CLI: `--trust`/`--UNSAFE`\n  - API: WithUnsafe(true)",
				ErrForbiddenPath, name, target, w.dstAbs)
		}
		data, err := LoadAnswersFile(target)
		if err != nil {
			return nil, fmt.Errorf("loading _external_data.%s from %s: %w", name, target, err)
		}
		if data == nil {
			if strict && !w.cfg.Quiet {
				fmt.Fprintf(os.Stderr, "File not found; returning empty dict: %s\n", renderedPath)
			}
			data = map[string]any{}
		}
		external[name] = data
	}
	return external, nil
}

// --- rendering --------------------------------------------------------------

// templateCopyRoot returns the absolute path from where to start copying: the
// template root plus the rendered `_subdirectory`, which must stay inside it.
func (w *worker) templateCopyRoot() (string, error) {
	subdir := w.tmpl.Config.Subdirectory
	if subdir != "" {
		rendered, err := w.renderer.RenderString(subdir, w.renderContext(nil))
		if err != nil {
			return "", fmt.Errorf("rendering _subdirectory: %w", err)
		}
		subdir = rendered
	}
	root, err := pathutil.Resolve(filepath.Join(w.tmpl.LocalPath, subdir))
	if err != nil {
		return "", err
	}
	ok, err := pathutil.IsWithin(w.tmpl.LocalPath, root)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: _subdirectory %q escapes template root", ErrForbiddenPath, subdir)
	}
	return root, nil
}

// buildMatchers compiles the exclude and skip-if-exists matchers, rendering
// each pattern with the current context.
func (w *worker) buildMatchers() {
	ctx := w.renderContext(map[string]any{"_copier_operation": string(w.operation)})
	var excludes []string
	for _, pattern := range append(append([]string(nil), w.tmpl.Exclusions()...), w.cfg.Exclude...) {
		rendered, err := w.renderer.RenderString(pattern, ctx)
		if err != nil {
			rendered = pattern
		}
		excludes = append(excludes, rendered)
	}
	w.excludeMatcher = NewPatternMatcher(excludes)

	var skips []string
	for _, pattern := range append(append([]string(nil), w.cfg.Skip...), w.tmpl.Config.SkipIfExists...) {
		rendered, err := w.renderer.RenderString(pattern, ctx)
		if err != nil {
			rendered = pattern
		}
		skips = append(skips, rendered)
	}
	w.skipMatcher = NewPatternMatcher(skips)
}

// renderTemplate renders the template into the destination.
func (w *worker) renderTemplate() error {
	copyRoot, err := w.templateCopyRoot()
	if err != nil {
		return err
	}
	w.copyRoot = copyRoot
	w.buildMatchers()
	w.answersRendered = false

	dstRoot, err := pathutil.Resolve(w.dstAbs)
	if err != nil {
		return err
	}
	preserve := w.tmpl.Config.PreserveSymlinks

	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			srcAbs := filepath.Join(dir, e.Name())
			lst, err := os.Lstat(srcAbs)
			if err != nil {
				return err
			}
			isSymlink := lst.Mode()&os.ModeSymlink != 0
			isDir := lst.IsDir()
			if isSymlink {
				if !preserve {
					resolved, err := filepath.EvalSymlinks(srcAbs)
					if err != nil {
						return fmt.Errorf("%w: broken symlink %s", ErrForbiddenPath, srcAbs)
					}
					ok, err := pathutil.IsWithin(w.tmpl.LocalPath, resolved)
					if err != nil {
						return err
					}
					if !ok {
						rel, _ := filepath.Rel(copyRoot, srcAbs)
						return fmt.Errorf("%w: %s points outside the template", ErrForbiddenPath, rel)
					}
					if st, err := os.Stat(srcAbs); err == nil {
						isDir = st.IsDir()
					}
				} else {
					isDir = false
				}
			}
			srcRel, err := filepath.Rel(w.tmpl.LocalPath, srcAbs)
			if err != nil {
				return err
			}
			copyRel, err := filepath.Rel(copyRoot, srcAbs)
			if err != nil {
				return err
			}

			rendered, err := w.renderPath(copyRel)
			if err != nil {
				return err
			}
			anyRendered := false
			for _, rp := range rendered {
				dstPath := filepath.Join(dstRoot, rp.Path)
				var dstReal string
				if isSymlinkPath(dstPath) {
					// A destination symlink may point outside the subproject while
					// itself existing within it, so do not resolve the link itself.
					parent, err := pathutil.Resolve(filepath.Dir(dstPath))
					if err != nil {
						return err
					}
					dstReal = filepath.Join(parent, filepath.Base(dstPath))
				} else {
					dstReal, err = pathutil.Resolve(dstPath)
					if err != nil {
						return err
					}
				}
				ok, err := pathutil.IsSubpath(dstRoot, dstReal)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("%w: rendered path %q escapes destination", ErrForbiddenPath, rp.Path)
				}
				if isDir && !isSymlink {
					if w.excludeMatcher.MatchesDir(rp.Path) {
						continue
					}
				} else if w.excludeMatcher.Matches(rp.Path) {
					continue
				}
				anyRendered = true
				switch {
				case isSymlink && preserve:
					if err := w.renderSymlink(srcRel, rp.Path); err != nil {
						return err
					}
				case isDir:
					if err := w.renderFolder(rp.Path); err != nil {
						return err
					}
				default:
					if err := w.renderFile(srcRel, rp.Path, rp.Context); err != nil {
						return err
					}
				}
			}
			if isDir && (anyRendered || (len(rendered) > 0 && w.excludeMatcher.hasNegation())) {
				if err := walk(srcAbs); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(copyRoot); err != nil {
		return err
	}

	// Templates without an answers file template still get one written, so
	// that the generated project can be updated later.
	if !w.answersRendered && !w.cfg.Pretend {
		answersPath := filepath.Join(w.dstAbs, w.answersRelpath())
		if w.excludeMatcher.Matches(w.answersRelpath()) {
			return nil
		}
		remembered := w.answersToRemember()
		meta := map[string]any{}
		for _, k := range []string{"_commit", "_src_path"} {
			if v, ok := remembered[k]; ok {
				meta[k] = v
				delete(remembered, k)
			}
		}
		if err := WriteAnswersFile(answersPath, remembered, meta); err != nil {
			return fmt.Errorf("writing answers file: %w", err)
		}
	}
	return nil
}

func isSymlinkPath(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// renderPath renders a path relative to the copy root into destination paths.
func (w *worker) renderPath(rel string) ([]RenderedPath, error) {
	suffix := w.tmpl.Config.TemplateSuffix
	base := filepath.Base(rel)
	isTemplate := strings.HasSuffix(base, suffix)
	// With a non-empty suffix, the templated sibling always wins.
	if suffix != "" && fileExists(filepath.Join(w.copyRoot, rel+suffix)) {
		return nil, nil
	}
	if suffix != "" && isTemplate {
		rel = strings.TrimSuffix(rel, suffix)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	answersRel := w.answersRelpath()
	special := func(rendered string, soFar []string) (string, bool) {
		if rendered != answersRel {
			return "", false
		}
		if len(soFar) > 0 && !w.cfg.Quiet {
			fmt.Fprintln(os.Stderr, "Answers file template locations other than the template root directory are deprecated. "+
				"Specify a non-default answers file name/location via the `_answers_file` setting in the `copier.yaml` file instead.")
		}
		return answersRel, true
	}
	rendered, err := w.renderer.RenderPathParts(parts, w.renderContext(nil), special)
	if err != nil {
		return nil, err
	}
	out := rendered[:0]
	for _, rp := range rendered {
		if !isTemplate && suffix != "" && fileExists(filepath.Join(w.tmpl.LocalPath, rp.Path+suffix)) {
			continue
		}
		out = append(out, rp)
	}
	return out, nil
}

// renderFile renders one file. srcRel is relative to the template root,
// dstRel to the destination.
func (w *worker) renderFile(srcRel, dstRel string, extra map[string]any) error {
	srcAbs := filepath.Join(w.tmpl.LocalPath, srcRel)
	raw, err := os.ReadFile(srcAbs)
	if err != nil {
		return err
	}
	content := raw
	if strings.HasSuffix(filepath.Base(srcRel), w.tmpl.Config.TemplateSuffix) && bytes.IndexByte(raw, 0) < 0 {
		out, state, err := w.renderer.RenderStringYield(string(raw), w.renderContext(extra))
		if err != nil {
			return fmt.Errorf("rendering %s: %w", srcRel, err)
		}
		if state.set {
			return fmt.Errorf("%w: file %s contains a yield tag, but it is not allowed", ErrYieldInFile, srcRel)
		}
		content = []byte(out)
	}
	info, err := os.Stat(srcAbs)
	if err != nil {
		return err
	}
	srcMode := info.Mode().Perm()

	allowed, err := w.renderAllowed(dstRel, false, false, content, "", &srcMode)
	if err != nil || !allowed {
		return err
	}
	if dstRel == w.answersRelpath() {
		w.answersRendered = true
	}
	if w.cfg.Pretend {
		return nil
	}
	dstAbs := filepath.Join(w.dstAbs, dstRel)
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return err
	}
	if isSymlinkPath(dstAbs) {
		// Writing to a symlink writes to its target; replace the link instead.
		_ = os.Remove(dstAbs)
	}
	if err := os.WriteFile(dstAbs, content, srcMode|0o200); err != nil {
		return err
	}
	if st, err := os.Stat(dstAbs); err == nil && st.Mode().Perm() != srcMode {
		if err := os.Chmod(dstAbs, srcMode); err != nil {
			fmt.Fprintf(os.Stderr, "Path permissions for %s cannot be changed: %v\n", dstAbs, err)
		}
	}
	SyncGitIndexExecutableBit(w.dstAbs, dstAbs, srcMode)
	return nil
}

// renderSymlink renders one symlink, keeping it a symlink.
func (w *worker) renderSymlink(srcRel, dstRel string) error {
	srcAbs := filepath.Join(w.tmpl.LocalPath, srcRel)
	target, err := os.Readlink(srcAbs)
	if err != nil {
		return err
	}
	if strings.HasSuffix(filepath.Base(srcRel), w.tmpl.Config.TemplateSuffix) {
		rendered, err := w.renderer.RenderString(target, w.renderContext(nil))
		if err != nil {
			return fmt.Errorf("rendering symlink %s: %w", srcRel, err)
		}
		target = rendered
	}
	allowed, err := w.renderAllowed(dstRel, false, true, nil, target, nil)
	if err != nil || !allowed {
		return err
	}
	if w.cfg.Pretend {
		return nil
	}
	dstAbs := filepath.Join(w.dstAbs, dstRel)
	if _, err := os.Lstat(dstAbs); err == nil {
		if err := os.RemoveAll(dstAbs); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, dstAbs)
}

// renderFolder creates one folder (without content).
func (w *worker) renderFolder(dstRel string) error {
	if w.cfg.Pretend {
		return nil
	}
	allowed, err := w.renderAllowed(dstRel, true, false, nil, "", nil)
	if err != nil || !allowed {
		return err
	}
	return os.MkdirAll(filepath.Join(w.dstAbs, dstRel), 0o755)
}

// renderAllowed determines whether a path can be rendered, printing the
// action taken. Identical files are skipped; differing ones are conflicts.
func (w *worker) renderAllowed(dstRel string, isDir, isSymlink bool, expected []byte, expectedLink string, expectedMode *os.FileMode) (bool, error) {
	dstAbs := filepath.Join(w.dstAbs, dstRel)
	lst, err := os.Lstat(dstAbs)
	if err != nil {
		if os.IsNotExist(err) {
			w.printf("create", dstRel, styleOK)
			return true, nil
		}
		return false, err
	}
	prevIsSymlink := lst.Mode()&os.ModeSymlink != 0
	var prevContent []byte
	prevLink := ""
	prevIsDir := false
	switch {
	case prevIsSymlink:
		prevLink, _ = os.Readlink(dstAbs)
	case lst.IsDir():
		prevIsDir = true
	default:
		prevContent, err = os.ReadFile(dstAbs)
		if err != nil {
			return false, err
		}
	}
	modeMatches := true
	if expectedMode != nil && !prevIsSymlink && !prevIsDir {
		modeMatches = lst.Mode().Perm()&0o111 == *expectedMode&0o111
	}
	identical := false
	switch {
	case isDir:
		identical = true
	case prevIsDir:
		identical = false
	case isSymlink:
		identical = prevIsSymlink && prevLink == expectedLink
	default:
		identical = !prevIsSymlink && bytes.Equal(prevContent, expected) && modeMatches
	}
	if identical {
		w.printf("identical", dstRel, styleIgnore)
		return isDir, nil
	}
	return w.solveRenderConflict(dstRel)
}

// solveRenderConflict resolves a render conflict, asking the user when interactive.
func (w *worker) solveRenderConflict(dstRel string) (bool, error) {
	w.printf("conflict", dstRel, styleDanger)
	if w.skipMatcher.Matches(dstRel) {
		w.printf("skip", dstRel, styleOK)
		return false, nil
	}
	if w.cfg.Overwrite || dstRel == w.answersRelpath() {
		w.printf("overwrite", dstRel, styleWarning)
		return true, nil
	}
	ok, err := w.prompter.Confirm(fmt.Sprintf(" Overwrite %s?", dstRel), true)
	if err != nil {
		if errors.Is(err, ErrInteractiveNeeded) {
			return false, fmt.Errorf("%w: consider using `--overwrite`", ErrInteractiveNeeded)
		}
		return false, err
	}
	return ok, nil
}

// --- tasks ------------------------------------------------------------------

// executeTasks runs the given tasks in order.
func (w *worker) executeTasks(tasks []TaskDef) error {
	for i, task := range tasks {
		extra := make(map[string]any, len(task.ExtraVars)+1)
		for k, v := range task.ExtraVars {
			extra["_"+k] = v
		}
		extra["_copier_operation"] = string(w.operation)
		ctx := w.renderContext(extra)

		// Evaluate condition.
		cond := task.Condition
		if cond == nil {
			cond = true
		}
		if s, ok := cond.(string); ok {
			rendered, err := w.renderer.RenderString(s, ctx)
			if err != nil {
				return &TaskExecError{Cmd: task.CmdString(), Err: fmt.Errorf("rendering condition: %w", err)}
			}
			cond = rendered
		}
		if !castToBool(cond) {
			continue
		}

		var args []string
		var display string
		switch c := task.Cmd.(type) {
		case string:
			rendered, err := w.renderer.RenderString(c, ctx)
			if err != nil {
				return &TaskExecError{Cmd: c, Err: err}
			}
			display = rendered
			args = shellCommand(rendered)
		default:
			parts := task.CmdArgs()
			args = make([]string, 0, len(parts))
			for _, part := range parts {
				rendered, err := w.renderer.RenderString(part, ctx)
				if err != nil {
					return &TaskExecError{Cmd: task.CmdString(), Err: err}
				}
				args = append(args, rendered)
			}
			display = fmt.Sprintf("%q", args)
		}

		if !w.cfg.Quiet {
			fmt.Fprintf(os.Stderr, " > Running task %d of %d: %s\n", i+1, len(tasks), display)
		}
		if w.cfg.Pretend {
			continue
		}

		workDir := w.dstAbs
		if task.WorkingDirectory != "" {
			wd, err := w.renderer.RenderString(task.WorkingDirectory, ctx)
			if err != nil {
				return &TaskExecError{Cmd: display, Err: err}
			}
			if filepath.IsAbs(wd) {
				workDir = wd
			} else {
				workDir = filepath.Join(w.dstAbs, wd)
			}
		}

		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = workDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Env = os.Environ()
		for k, v := range extra {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%v", strings.ToUpper(strings.TrimPrefix(k, "_")), v))
		}
		// copier-go extension: answers are also exported as upper-cased variables.
		for k, v := range w.answers.Combined() {
			if strings.HasPrefix(k, "_") || !isJSONSerializable(v) {
				continue
			}
			if _, builtin := builtinDefaults()[k]; builtin {
				continue
			}
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%v", strings.ToUpper(k), envValue(v)))
		}

		if err := cmd.Run(); err != nil {
			exitCode := 1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			}
			return &TaskExecError{Cmd: display, ExitCode: exitCode, Err: ErrTaskFailed}
		}
	}
	return nil
}

func envValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	}
	return fmt.Sprintf("%v", v)
}

func shellCommand(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", command}
	}
	return []string{"sh", "-c", command}
}

// --- checks -----------------------------------------------------------------

func (w *worker) checkVersion() error {
	minVer := w.tmpl.MinVersion()
	if minVer == nil {
		return nil
	}
	// `_min_copier_version` names an upstream Copier version, so compare it
	// with the upstream version this port tracks.
	current, err := parseTemplateVersion(version.Upstream)
	if err != nil {
		return err
	}
	if current.LessThan(minVer) {
		return fmt.Errorf("%w: this template requires Copier version >= %s, while your version of Copier is %s",
			ErrUnsupportedVersion, w.tmpl.Config.MinCopierVersion, current)
	}
	return nil
}

// checkUnsafe fails when the template uses unsafe features without consent.
func (w *worker) checkUnsafe(mode Operation, oldTmpl *Template) error {
	if w.isTrusted() {
		return nil
	}
	features := map[string]bool{}
	if len(w.tmpl.Config.JinjaExtensions) > 0 {
		features["jinja_extensions"] = true
	}
	if len(w.tmpl.Config.Tasks) > 0 && !w.cfg.SkipTasks {
		features["tasks"] = true
	}
	if mode == OpUpdate && oldTmpl != nil {
		if len(oldTmpl.Config.JinjaExtensions) > 0 {
			features["jinja_extensions"] = true
		}
		if len(oldTmpl.Config.Tasks) > 0 {
			features["tasks"] = true
		}
		for _, stage := range []string{"before", "after"} {
			if len(w.tmpl.MigrationTasks(stage, oldTmpl)) > 0 {
				features["migrations"] = true
				break
			}
		}
	}
	if len(features) == 0 {
		return nil
	}
	list := make([]string, 0, len(features))
	for f := range features {
		list = append(list, f)
	}
	sort.Strings(list)
	return &UnsafeTemplateError{Features: list}
}

func (w *worker) isTrusted() bool {
	return w.cfg.Unsafe || (w.settings != nil && w.tmpl != nil && w.settings.IsTrusted(w.tmpl.URL))
}

// --- output -----------------------------------------------------------------

// Style constants for output.
type style string

const (
	styleOK      style = "\033[32;1m" // green bold
	styleWarning style = "\033[33;1m" // yellow bold
	styleIgnore  style = "\033[36m"   // cyan
	styleDanger  style = "\033[31;1m" // red bold
	styleReset   style = "\033[0m"
)

func (w *worker) printf(action, msg string, s style) {
	if w.cfg.Quiet {
		return
	}
	fmt.Fprintf(os.Stderr, "%s%10s%s  %s\n", s, action, styleReset, msg)
}

func (w *worker) printMessage(msg string) {
	if msg == "" || w.cfg.Quiet {
		return
	}
	rendered, err := w.renderer.RenderString(msg, w.renderContext(nil))
	if err != nil {
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	fmt.Fprintln(os.Stderr, rendered)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// resolveStoredSourcePath resolves a relative local `_src_path` recorded in an
// answers file against the destination directory.
func resolveStoredSourcePath(srcPath, dst string) string {
	if srcPath == "" || filepath.IsAbs(srcPath) || strings.Contains(srcPath, "://") || strings.HasPrefix(srcPath, "git@") {
		return srcPath
	}
	if strings.HasPrefix(srcPath, "gh:") || strings.HasPrefix(srcPath, "gl:") || strings.HasPrefix(srcPath, "git+") {
		return srcPath
	}
	if strings.HasPrefix(srcPath, "~") {
		return srcPath
	}
	candidate := filepath.Join(dst, srcPath)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return srcPath
}
