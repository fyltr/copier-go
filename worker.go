package copier

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/fyltr/copier-go/internal/pathutil"
	"github.com/fyltr/copier-go/internal/version"
)

// worker is the internal execution engine for copier operations.
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
}

func newWorker(cfg Config, op Operation) (*worker, error) {
	settings, err := LoadSettings()
	if err != nil {
		slog.Warn("failed to load settings", "error", err)
		settings = &Settings{}
	}

	return &worker{
		cfg:       cfg,
		answers:   NewAnswersMap(),
		prompter:  NewTerminalPrompter(),
		settings:  settings,
		phase:     PhaseUndefined,
		operation: op,
		logger:    slog.Default(),
	}, nil
}

// runCopy executes a fresh template copy.
func (w *worker) runCopy() error {
	tmpl, err := LoadTemplate(w.cfg.SrcPath, w.cfg.VcsRef, w.cfg.UsePreReleases)
	if err != nil {
		return err
	}
	w.tmpl = tmpl
	defer tmpl.Cleanup()

	if err := w.checkVersion(); err != nil {
		return err
	}
	if err := w.checkSafety(); err != nil {
		return err
	}

	w.printMessage(w.tmpl.Config.MessageBeforeCopy)

	// Merge template metadata.
	for k, v := range tmpl.Metadata() {
		w.answers.Metadata[k] = v
	}

	// Merge CLI-provided data.
	for k, v := range w.cfg.Data {
		w.answers.Init[k] = v
	}
	for k, v := range w.cfg.UserDefaults {
		w.answers.UserDefaults[k] = v
	}

	if err := w.initRenderer(); err != nil {
		return err
	}

	// Prompt phase.
	w.phase = PhasePrompt
	if err := w.askQuestions(); err != nil {
		return err
	}

	// Refresh renderer with complete answers.
	if err := w.initRenderer(); err != nil {
		return err
	}

	// Render phase.
	w.phase = PhaseRender
	dstCreated := !dirExists(w.cfg.DstPath)

	if err := w.renderTemplate(); err != nil {
		if dstCreated && w.cfg.CleanupOnError && !w.cfg.Pretend {
			_ = os.RemoveAll(w.cfg.DstPath)
		}
		return err
	}

	// Write answers file.
	if !w.cfg.Pretend {
		answersPath := filepath.Join(w.cfg.DstPath, w.tmpl.Config.AnswersFile)
		if err := WriteAnswersFile(answersPath, w.answers.Remembered(), w.tmpl.Metadata()); err != nil {
			return fmt.Errorf("writing answers file: %w", err)
		}
	}

	// Tasks phase.
	w.phase = PhaseTasks
	if !w.cfg.SkipTasks {
		if err := w.executeTasks(w.tmpl.Config.Tasks); err != nil {
			return err
		}
	}

	w.printMessage(w.tmpl.Config.MessageAfterCopy)
	return nil
}

// runUpdate executes a template update with 3-way merge.
func (w *worker) runUpdate() error {
	// Load existing answers to find the source template.
	answersPath := filepath.Join(w.cfg.DstPath, w.cfg.AnswersFile)
	lastAnswers, err := LoadAnswersFile(answersPath)
	if err != nil {
		return fmt.Errorf("reading previous answers: %w", err)
	}
	if lastAnswers == nil {
		return fmt.Errorf("no previous answers found at %s; use copy instead", answersPath)
	}

	for k, v := range lastAnswers {
		w.answers.Last[k] = v
	}

	// Determine source from previous answers.
	srcPath := w.cfg.SrcPath
	if srcPath == "" {
		if sp, ok := lastAnswers["_src_path"]; ok {
			srcPath = fmt.Sprintf("%v", sp)
		}
	}
	if srcPath == "" {
		return fmt.Errorf("cannot determine template source; provide --src or ensure _src_path in answers")
	}
	srcPath = resolveStoredSourcePath(srcPath, w.cfg.DstPath)

	// Determine old ref from previous answers.
	oldRef := ""
	if cr, ok := lastAnswers["_commit"]; ok {
		oldRef = fmt.Sprintf("%v", cr)
	}

	// Load new template.
	newTmpl, err := LoadTemplate(srcPath, w.cfg.VcsRef, w.cfg.UsePreReleases)
	if err != nil {
		return err
	}
	w.tmpl = newTmpl
	defer newTmpl.Cleanup()

	if err := w.checkVersion(); err != nil {
		return err
	}
	if err := w.checkSafety(); err != nil {
		return err
	}

	w.printMessage(w.tmpl.Config.MessageBeforeUpdate)

	for k, v := range newTmpl.Metadata() {
		w.answers.Metadata[k] = v
	}
	for k, v := range w.cfg.Data {
		w.answers.Init[k] = v
	}
	for k, v := range w.cfg.UserDefaults {
		w.answers.UserDefaults[k] = v
	}

	if err := w.initRenderer(); err != nil {
		return err
	}

	// Prompt phase.
	w.phase = PhasePrompt
	if err := w.askQuestions(); err != nil {
		return err
	}
	if err := w.initRenderer(); err != nil {
		return err
	}

	// Render phase — 3-way merge.
	w.phase = PhaseRender
	if err := w.applyUpdate(srcPath, oldRef); err != nil {
		return err
	}

	// Write updated answers.
	if !w.cfg.Pretend {
		afPath := filepath.Join(w.cfg.DstPath, w.tmpl.Config.AnswersFile)
		if err := WriteAnswersFile(afPath, w.answers.Remembered(), w.tmpl.Metadata()); err != nil {
			return fmt.Errorf("writing answers file: %w", err)
		}
	}

	// Migration tasks.
	w.phase = PhaseMigrate
	if !w.cfg.SkipTasks {
		oldVer, newVer := parseVersions(oldRef, w.tmpl.Ref)
		beforeTasks := w.tmpl.MigrationTasks("before", oldVer, newVer)
		if err := w.executeTasks(beforeTasks); err != nil {
			return err
		}
	}

	// Post-update tasks.
	w.phase = PhaseTasks
	if !w.cfg.SkipTasks {
		if err := w.executeTasks(w.tmpl.Config.Tasks); err != nil {
			return err
		}
		oldVer, newVer := parseVersions(oldRef, w.tmpl.Ref)
		afterTasks := w.tmpl.MigrationTasks("after", oldVer, newVer)
		if err := w.executeTasks(afterTasks); err != nil {
			return err
		}
	}

	w.printMessage(w.tmpl.Config.MessageAfterUpdate)
	return nil
}

func (w *worker) applyUpdate(srcPath, oldRef string) error {
	// Render old template with previous answers into a temp directory.
	oldTmpl, err := LoadTemplate(srcPath, oldRef, w.cfg.UsePreReleases)
	if err != nil {
		return fmt.Errorf("loading old template version: %w", err)
	}
	defer oldTmpl.Cleanup()

	oldDir, err := os.MkdirTemp("", "copier-old-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(oldDir) }()

	newDir, err := os.MkdirTemp("", "copier-new-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(newDir) }()

	// Render old template.
	oldWorker := &worker{
		cfg:      Config{SrcPath: srcPath, DstPath: oldDir, Quiet: true, SkipTasks: true},
		tmpl:     oldTmpl,
		answers:  w.answers,
		renderer: NewRenderer(w.answers.Combined(), oldTmpl.CopyRoot(), w.resolveEnvops()),
		settings: w.settings,
		phase:    PhaseRender,
		logger:   w.logger,
	}
	if err := oldWorker.renderTemplate(); err != nil {
		return fmt.Errorf("rendering old template: %w", err)
	}

	// Render new template.
	newWorker := &worker{
		cfg:      Config{SrcPath: srcPath, DstPath: newDir, Quiet: true, SkipTasks: true},
		tmpl:     w.tmpl,
		answers:  w.answers,
		renderer: w.renderer,
		settings: w.settings,
		phase:    PhaseRender,
		logger:   w.logger,
	}
	if err := newWorker.renderTemplate(); err != nil {
		return fmt.Errorf("rendering new template: %w", err)
	}

	// Use git to compute and apply the diff.
	if err := w.threeWayMerge(oldDir, newDir); err != nil {
		return err
	}

	return nil
}

func (w *worker) threeWayMerge(oldDir, newDir string) error {
	// Initialize git repo in old dir with current dst content.
	if err := GitInit(oldDir); err != nil {
		return fmt.Errorf("git init (old): %w", err)
	}
	if err := GitAdd(oldDir); err != nil {
		return fmt.Errorf("git add (old): %w", err)
	}
	if err := GitCommit(oldDir, "old template"); err != nil {
		return fmt.Errorf("git commit (old): %w", err)
	}

	// Copy current project files over old dir.
	if err := CopyDir(w.cfg.DstPath, oldDir); err != nil {
		return fmt.Errorf("copying current state: %w", err)
	}
	if err := GitAdd(oldDir); err != nil {
		return err
	}
	if err := GitCommit(oldDir, "current state"); err != nil {
		return err
	}

	// Copy new template files over old dir.
	if err := CopyDir(newDir, oldDir); err != nil {
		return fmt.Errorf("copying new template: %w", err)
	}

	// Get the diff.
	diff, err := GitDiff(oldDir, w.cfg.ContextLines)
	if err != nil {
		return fmt.Errorf("computing diff: %w", err)
	}

	if len(diff) == 0 {
		w.printf("identical", "No changes to apply", styleOK)
		return nil
	}

	// Apply diff to destination.
	reject := w.cfg.Conflict == ConflictReject
	if err := GitApplyDiff(w.cfg.DstPath, diff, w.cfg.ContextLines, reject); err != nil {
		w.printf("conflict", "Some changes could not be applied cleanly", styleWarning)
		// Non-fatal: conflicts are expected during updates.
	}

	return nil
}

func (w *worker) initRenderer() error {
	tmplDir := ""
	if w.tmpl != nil {
		tmplDir = w.tmpl.CopyRoot()
	}
	ctx := w.answers.Combined()
	ctx["_copier_phase"] = string(w.phase)
	ctx["_copier_operation"] = string(w.operation)
	ctx["_external_data"] = map[string]any{}
	if w.tmpl != nil {
		ctx["_copier_conf"] = map[string]any{
			"src_path":     w.tmpl.URL,
			"dst_path":     w.cfg.DstPath,
			"answers_file": w.cfg.AnswersFile,
			"vcs_ref":      w.tmpl.Ref,
			"vcs_ref_hash": w.tmpl.CommitHash,
			"sep":          string(filepath.Separator),
		}
	}
	ctx["_folder_name"] = filepath.Base(w.cfg.DstPath)
	eo := w.resolveEnvops()
	if w.tmpl != nil && len(w.tmpl.Config.ExternalData) > 0 {
		external, err := w.loadExternalData(ctx, tmplDir, eo)
		if err != nil {
			return err
		}
		ctx["_external_data"] = external
	}
	w.renderer = NewRenderer(ctx, tmplDir, eo)
	return nil
}

func (w *worker) askQuestions() error {
	for _, q := range w.tmpl.Questions {
		if !ShouldAsk(q, w.renderer, w.answers.Combined()) {
			continue
		}

		// Skip if already answered and skip_answered is set.
		if w.cfg.SkipAnswered {
			if _, ok := w.answers.Last[q.Name]; ok {
				w.answers.User[q.Name] = w.answers.Last[q.Name]
				continue
			}
		}

		dflt := ResolveDefault(q, w.answers, w.settings)
		q.Default = dflt

		// Render default if it's a template string.
		if s, ok := dflt.(string); ok && strings.Contains(s, "{{") {
			rendered, err := w.renderer.RenderString(s, w.answers.Combined())
			if err == nil {
				q.Default = rendered
			}
		}

		// Use default without prompting if --defaults.
		if w.cfg.Defaults {
			parsed, err := ParseAnswer(q, q.Default)
			if err != nil {
				return &QuestionError{Name: q.Name, Err: err}
			}
			w.answers.User[q.Name] = parsed
			if err := w.initRenderer(); err != nil {
				return err
			}
			continue
		}

		// Also use default if data was provided via CLI.
		if _, ok := w.answers.Init[q.Name]; ok {
			w.answers.User[q.Name] = w.answers.Init[q.Name]
			if err := w.initRenderer(); err != nil {
				return err
			}
			continue
		}

		answer, err := w.prompter.Ask(q, w.answers.Combined())
		if err != nil {
			return &QuestionError{Name: q.Name, Err: err}
		}

		// Validate.
		if err := ValidateAnswer(q, answer, w.renderer, w.answers.Combined()); err != nil {
			return err
		}

		w.answers.User[q.Name] = answer

		// Mark secret questions as hidden.
		if q.Secret || w.tmpl.IsSecret(q.Name) {
			w.answers.Hidden[q.Name] = true
		}

		// Re-init renderer so subsequent questions can reference this answer.
		if err := w.initRenderer(); err != nil {
			return err
		}
	}
	return nil
}

func (w *worker) renderTemplate() error {
	root := w.tmpl.CopyRoot()
	excludeMatcher := NewPatternMatcher(w.tmpl.Exclusions())
	skipMatcher := NewPatternMatcher(append(w.tmpl.Config.SkipIfExists, w.cfg.Skip...))

	return WalkTemplate(root, excludeMatcher, func(relPath string, d fs.DirEntry) error {
		srcPath := filepath.Join(root, relPath)

		// Render the destination path (expand Jinja expressions).
		renderedPaths, err := w.renderer.RenderPath(relPath, w.answers.Combined())
		if err != nil {
			return fmt.Errorf("rendering path %s: %w", relPath, err)
		}

		for _, renderedRel := range renderedPaths {
			if renderedRel == "" {
				continue
			}
			dstPath, err := safeJoin(w.cfg.DstPath, renderedRel)
			if err != nil {
				return err
			}

			if d.IsDir() {
				if !w.cfg.Pretend {
					if err := os.MkdirAll(dstPath, 0o755); err != nil {
						return err
					}
				}
				w.printf("create", renderedRel+"/", styleOK)
				continue
			}

			// Check skip_if_exists.
			if fileExists(dstPath) && skipMatcher.Matches(renderedRel) {
				w.printf("skip", renderedRel, styleIgnore)
				continue
			}

			// Handle symlinks.
			info, err := os.Lstat(srcPath)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return w.renderSymlink(srcPath, dstPath, renderedRel)
			}

			// Determine if this is a template file.
			suffix := w.tmpl.Config.TemplateSuffix
			if IsTemplateSuffix(relPath, suffix) {
				dstPath = StripTemplateSuffix(dstPath, suffix)
				renderedRel = StripTemplateSuffix(renderedRel, suffix)
			}

			if err := w.renderFile(srcPath, dstPath, renderedRel, IsTemplateSuffix(relPath, suffix)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *worker) renderFile(srcPath, dstPath, relPath string, isTemplate bool) error {
	// Check overwrite.
	if fileExists(dstPath) && !w.cfg.Overwrite {
		if !w.cfg.Defaults {
			ok, err := w.prompter.Confirm(
				fmt.Sprintf("Overwrite %s?", relPath), false)
			if err != nil || !ok {
				w.printf("skip", relPath, styleIgnore)
				return nil
			}
		} else {
			w.printf("skip", relPath, styleIgnore)
			return nil
		}
	}

	if w.cfg.Pretend {
		w.printf("create", relPath, styleOK)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	srcMode := srcInfo.Mode().Perm()

	binary, err := IsBinary(srcPath)
	if err != nil {
		return err
	}

	if !isTemplate || binary {
		if err := CopyFile(srcPath, dstPath); err != nil {
			return err
		}
		SyncGitIndexExecutableBit(w.cfg.DstPath, dstPath, srcMode)
		w.printf("create", relPath, styleOK)
		return nil
	}

	if err := w.renderer.RenderFile(srcPath, dstPath, w.answers.Combined()); err != nil {
		return fmt.Errorf("rendering %s: %w", relPath, err)
	}
	SyncGitIndexExecutableBit(w.cfg.DstPath, dstPath, srcMode)
	w.printf("create", relPath, styleOK)
	return nil
}

func (w *worker) renderSymlink(srcPath, dstPath, relPath string) error {
	link, err := os.Readlink(srcPath)
	if err != nil {
		return err
	}

	if w.tmpl.Config.PreserveSymlinks {
		if w.cfg.Pretend {
			w.printf("create", relPath+" -> "+link, styleOK)
			return nil
		}
		_ = os.Remove(dstPath) // Remove existing if any.
		if err := os.Symlink(link, dstPath); err != nil {
			return err
		}
		w.printf("create", relPath+" -> "+link, styleOK)
		return nil
	}

	// Resolve symlink and copy target.
	resolved, err := filepath.EvalSymlinks(srcPath)
	if err != nil {
		return err
	}
	ok, err := pathutil.IsSubpath(w.tmpl.LocalPath, resolved)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: symlink %s points outside template root", ErrForbiddenPath, relPath)
	}
	return w.renderFile(resolved, dstPath, relPath, IsTemplateSuffix(resolved, w.tmpl.Config.TemplateSuffix))
}

func (w *worker) loadExternalData(ctx map[string]any, tmplDir string, eo Envops) (map[string]any, error) {
	external := make(map[string]any, len(w.tmpl.Config.ExternalData))
	renderer := NewRenderer(ctx, tmplDir, eo)
	base := w.cfg.DstPath
	if base == "" {
		base = "."
	}
	for name, pathTemplate := range w.tmpl.Config.ExternalData {
		renderedPath, err := renderer.RenderString(pathTemplate, ctx)
		if err != nil {
			return nil, fmt.Errorf("rendering _external_data.%s path: %w", name, err)
		}
		if renderedPath == "" {
			external[name] = map[string]any{}
			continue
		}
		target := renderedPath
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
		}
		target = filepath.Clean(target)
		ok, err := pathutil.IsSubpath(base, target)
		if err != nil {
			return nil, err
		}
		if !ok && !w.isTrusted() {
			return nil, fmt.Errorf("%w: _external_data.%s reads %s outside %s", ErrUnsafeTemplate, name, target, base)
		}
		data, err := LoadAnswersFile(target)
		if err != nil {
			return nil, fmt.Errorf("loading _external_data.%s from %s: %w", name, target, err)
		}
		if data == nil {
			data = map[string]any{}
		}
		external[name] = data
	}
	return external, nil
}

func safeJoin(base, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: rendered path %q must be relative", ErrForbiddenPath, rel)
	}
	target := filepath.Clean(filepath.Join(base, rel))
	ok, err := pathutil.IsSubpath(base, target)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: rendered path %q escapes destination", ErrForbiddenPath, rel)
	}
	return target, nil
}

func (w *worker) executeTasks(tasks []TaskDef) error {
	if len(tasks) == 0 {
		return nil
	}

	for _, task := range tasks {
		// Evaluate condition.
		if !w.evalTaskCondition(task) {
			continue
		}

		cmdStr := task.CmdString()

		// Render the command through the template engine.
		rendered, err := w.renderer.RenderString(cmdStr, w.answers.Combined())
		if err != nil {
			return &TaskExecError{Cmd: cmdStr, Err: err}
		}

		w.printf("run", rendered, styleOK)

		if w.cfg.Pretend {
			continue
		}

		workDir := w.cfg.DstPath
		if task.WorkingDirectory != "" {
			wd, err := w.renderer.RenderString(task.WorkingDirectory, w.answers.Combined())
			if err == nil && wd != "" {
				if filepath.IsAbs(wd) {
					workDir = wd
				} else {
					workDir = filepath.Join(w.cfg.DstPath, wd)
				}
			}
		}

		cmd := exec.Command("sh", "-c", rendered)
		cmd.Dir = workDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		// Set environment: answers as uppercased vars.
		cmd.Env = os.Environ()
		for k, v := range w.answers.Combined() {
			if strings.HasPrefix(k, "_") {
				continue
			}
			envKey := strings.ToUpper(k)
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%v", envKey, v))
		}

		if err := cmd.Run(); err != nil {
			exitCode := 1
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			return &TaskExecError{Cmd: rendered, ExitCode: exitCode, Err: ErrTaskFailed}
		}
	}
	return nil
}

func (w *worker) evalTaskCondition(task TaskDef) bool {
	switch v := task.Condition.(type) {
	case nil:
		return true
	case bool:
		return v
	case string:
		if v == "" {
			return true
		}
		result, err := w.renderer.RenderString(v, w.answers.Combined())
		if err != nil {
			return false
		}
		trimmed := strings.TrimSpace(strings.ToLower(result))
		return trimmed != "" && trimmed != "false" && trimmed != "0"
	default:
		return true
	}
}

func (w *worker) checkVersion() error {
	minVer := w.tmpl.MinVersion()
	if minVer == nil {
		return nil
	}
	current, err := semver.NewVersion(version.Version)
	if err != nil {
		// Dev version — skip check.
		return nil
	}
	if current.LessThan(minVer) {
		return fmt.Errorf("%w: template requires copier >= %s, current is %s",
			ErrUnsupportedVersion, minVer, current)
	}
	return nil
}

func (w *worker) checkSafety() error {
	hasTasks := len(w.tmpl.Config.Tasks) > 0
	hasMigrations := len(w.tmpl.Config.Migrations) > 0
	hasExtensions := len(w.tmpl.Config.JinjaExtensions) > 0
	if !hasTasks && !hasMigrations && !hasExtensions {
		return nil
	}
	if w.isTrusted() {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnsafeTemplate, w.tmpl.URL)
}

func (w *worker) isTrusted() bool {
	return w.cfg.Unsafe || (w.settings != nil && w.settings.IsTrusted(w.tmpl.URL))
}

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
	fmt.Fprintf(os.Stderr, "%s%12s%s %s\n", s, action, styleReset, msg)
}

func (w *worker) printMessage(msg string) {
	if msg == "" || w.cfg.Quiet {
		return
	}
	rendered, err := w.renderer.RenderString(msg, w.answers.Combined())
	if err != nil {
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	fmt.Fprintln(os.Stderr, rendered)
}

func (w *worker) resolveEnvops() Envops {
	if w.tmpl == nil {
		return DefaultEnvops()
	}
	eo := w.tmpl.Config.Envops
	d := DefaultEnvops()
	// Fill in any unset fields with defaults.
	if eo.BlockStartString == "" {
		eo.BlockStartString = d.BlockStartString
	}
	if eo.BlockEndString == "" {
		eo.BlockEndString = d.BlockEndString
	}
	if eo.VariableStartString == "" {
		eo.VariableStartString = d.VariableStartString
	}
	if eo.VariableEndString == "" {
		eo.VariableEndString = d.VariableEndString
	}
	if eo.CommentStartString == "" {
		eo.CommentStartString = d.CommentStartString
	}
	if eo.CommentEndString == "" {
		eo.CommentEndString = d.CommentEndString
	}
	return eo
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func parseVersions(oldRef, newRef string) (old, new *semver.Version) {
	if oldRef != "" {
		old, _ = semver.NewVersion(strings.TrimPrefix(oldRef, "v"))
	}
	if newRef != "" {
		new, _ = semver.NewVersion(strings.TrimPrefix(newRef, "v"))
	}
	return
}
