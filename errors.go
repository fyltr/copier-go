// Package copier provides a Go port of Copier for rendering project templates.
//
// It tracks the behavior of the upstream Python project at
// https://github.com/copier-org/copier while exposing a native Go library API.
// Copier templates, answers files, Git-based updates, and questionnaires are
// intended to remain compatible with upstream Copier wherever practical.
//
// Use [Copy], [Update], and [Recopy] as the main entry points.
package copier

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for type-checking with errors.Is.
var (
	ErrUnsupportedVersion = errors.New("copier version not supported by template")
	ErrConfig             = errors.New("template configuration error")
	ErrMultipleConfigs    = errors.New("both copier.yml and copier.yaml found")
	ErrUnsafeTemplate     = errors.New("template uses unsafe features; use WithUnsafe(true) or trust the repository")
	ErrExtensionNotFound  = errors.New("jinja extension not found")
	ErrInteractiveNeeded  = errors.New("interactive session required but not available")
	ErrInvalidType        = errors.New("invalid question type")
	ErrPathNotAbsolute    = errors.New("path must be absolute")
	ErrPathNotRelative    = errors.New("path must be relative")
	ErrForbiddenPath      = errors.New("path escapes destination directory")
	ErrYieldInFile        = errors.New("yield tag found in file content")
	ErrMultipleYields     = errors.New("multiple yield tags in a single path segment")
	ErrTaskFailed         = errors.New("task execution failed")
	ErrInterrupted        = errors.New("operation interrupted by user")
	ErrQuestionRequired   = errors.New("question is required")
	ErrInvalidChoice      = errors.New("invalid choice")
	ErrDirtyDestination   = errors.New("destination repository is dirty; cannot continue. Please commit or stash your local changes and retry")
	ErrNotGitTracked      = errors.New("updating is only supported in git-tracked subprojects")
	ErrVersionNotDetected = errors.New("template version not detected")
	ErrDowngrade          = errors.New("downgrades are not supported")
	ErrSettings           = errors.New("invalid settings")
)

// TemplateError wraps errors related to template loading or configuration.
type TemplateError struct {
	Path string
	Err  error
}

func (e *TemplateError) Error() string { return fmt.Sprintf("template %s: %v", e.Path, e.Err) }
func (e *TemplateError) Unwrap() error { return e.Err }

// TaskExecError wraps errors from task execution with the command and exit code.
type TaskExecError struct {
	Cmd      string
	ExitCode int
	Err      error
}

func (e *TaskExecError) Error() string {
	return fmt.Sprintf("task %q failed with exit code %d: %v", e.Cmd, e.ExitCode, e.Err)
}

func (e *TaskExecError) Unwrap() error { return e.Err }

// QuestionError wraps errors related to question processing.
type QuestionError struct {
	Name string
	Err  error
}

func (e *QuestionError) Error() string { return fmt.Sprintf("question %q: %v", e.Name, e.Err) }
func (e *QuestionError) Unwrap() error { return e.Err }

// ValidationError is returned when a question answer fails validation.
type ValidationError struct {
	Question string
	Message  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("Validation error for question '%s': %s", e.Question, e.Message)
}

// InvalidChoiceError is returned when an answer is not among the valid choices.
type InvalidChoiceError struct {
	Question string
	Detail   string
}

func (e *InvalidChoiceError) Error() string {
	return fmt.Sprintf("Invalid choice for '%s': %s", e.Question, e.Detail)
}

func (e *InvalidChoiceError) Unwrap() error { return ErrInvalidChoice }

// UnsafeTemplateError reports which unsafe features a template uses.
type UnsafeTemplateError struct {
	Features []string
}

func (e *UnsafeTemplateError) Error() string {
	s := ""
	if len(e.Features) > 1 {
		s = "s"
	}
	return fmt.Sprintf(
		"Template uses potentially unsafe feature%s: %s.\n"+
			"If you trust this template, consider adding the `--trust` option when running `copier copy/update`.",
		s, strings.Join(e.Features, ", "))
}

func (e *UnsafeTemplateError) Unwrap() error { return ErrUnsafeTemplate }

// InterruptError carries partial answers when the user interrupts a prompt session.
type InterruptError struct {
	PartialAnswers map[string]any
}

func (e *InterruptError) Error() string { return "operation interrupted by user" }
func (e *InterruptError) Unwrap() error { return ErrInterrupted }
