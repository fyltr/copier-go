# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Copier-go is a Go reimplementation of [Copier](https://github.com/copier-org/copier) — a library and CLI for rendering project templates. It supports scaffolding new projects from templates (local paths or Git URLs), updating existing projects via 3-way merge, and interactive questionnaires. Templates use Jinja2/pongo2 syntax.

The project is split into a **library** (root package `copier`) usable by other Go projects, and a **CLI** (`cmd/copier`) that wraps the library.

## Commands

```bash
make build                  # Build the CLI binary
make test                   # Run all tests with race detector
make test-v                 # Verbose test output
make cover                  # Generate HTML coverage report
make lint                   # Run go vet + golangci-lint
make fmt                    # Format code (gofmt + goimports)

# Run a single test
go test -run TestCopy_LocalTemplate -v -count=1 ./...

# Run tests for a specific package
go test -v -count=1 ./internal/pathutil/
```

## Architecture

### Library (root package `copier` — `github.com/fyltr/copier-go`)

Public API in **`copier.go`** — three entry points:
- `Copy(src, dst string, opts ...Option)` — scaffold a new project
- `Update(dst string, opts ...Option)` — 3-way merge update to newer template version
- `Recopy(dst string, opts ...Option)` — re-apply template with existing answers

Configuration is via functional options (`WithData`, `WithDefaults`, `WithUnsafe`, etc.) defined in **`options.go`**.

### Core Files

| File | Responsibility |
|------|---------------|
| `worker.go` | Execution engine mirroring upstream's `Worker`: prompt → render → tasks phases (`doCopy`), the questionnaire (`ask`), render context (`_copier_conf`, `_copier_answers`), and the upstream update algorithm (`applyUpdate`: old/new renders, `git apply --reject`, inline conflict markers, deleted-file handling, migrations). |
| `template.go` | Loads `copier.yml`/`copier.yaml` (multi-document, `!include` with globs restricted to the template root, upstream merge rules), parses config (underscore-prefixed keys) and questions (other keys, in file order). Handles `TemplateConfig`, `TaskDef`, `MigrationDef` (new and legacy formats), `MigrationTasks`. |
| `question.go` | `AnswersMap` with layered precedence (User > Init > Metadata > Last > UserDefaults > External > Builtin) and `Question`, a port of upstream's question logic: type inference, casting, choices (dict/tuple/dynamic, disabled via validator), defaults (`UNSET`), `when`, `ask`, validators, multiselect parsing. |
| `prompt.go` | `TerminalPrompter` — interactive UI using `charm.land/huh/v2`. Implements the `Prompter` interface; custom prompters can be injected with `WithPrompter`. |
| `render.go` | Jinja2-compatible template rendering via pongo2. `Renderer` handles string and path rendering (with the `{% yield %}` tag), a loader sandboxed to the template root for `{% include %}`, custom delimiters, and a StrictUndefined approximation. |
| `filters.go` | Jinja / jinja2-ansible-filters style filters registered in pongo2 (`to_nice_yaml`, `to_json`, `hash`, `strftime`, ...). |
| `vcs.go` | Git operations through the `git` CLI (go-git only as a fallback when git is missing): URL resolution (`gh:`, `gl:`, `git+`), the bare-mirror cache with temporary worktrees for remote templates, local clones including dirty changes, PEP 440 tag selection, and the helpers the update algorithm needs (alternates, diff-tree, apply, merge-file, index stages). |
| `version.go` | PEP 440-style version parsing/ordering (`templateVersion`), used for tags, migrations and check-update; converts `git describe` output like upstream. |
| `settings.go` | User settings from `$XDG_CONFIG_HOME/copier/settings.yml`. Trust lists with upstream's URL checks (dot-segment normalization of URL/SCP/alias paths; ambiguous URLs only match exact entries) and default answers. |
| `fileops.go` | File copy, gitignore-style `PatternMatcher` (go-git's gitignore package), fnmatch for `--ask`, answers file I/O, directory comparison/removal for updates, `**` globbing for includes. |
| `types.go` | Enums (`Phase`, `Operation`, `ConflictStrategy`, `QuestionType`), constants, `LazyMap` for deferred computation. |
| `errors.go` | Sentinel errors and typed error structs (`TemplateError`, `TaskExecError`, `QuestionError`, `ValidationError`, `InvalidChoiceError`, `UnsafeTemplateError`). |

### CLI (`cmd/copier/`)

Built on cobra. Three subcommands mirror the library API:
- `copier copy TEMPLATE DESTINATION` — flags: `-d/--data`, `--data-file`, `--ask`, `-l/--defaults`, `-f/--force`, `-w/--overwrite`, `-n/--pretend`
- `copier update [DESTINATION]` — flags: `-o/--conflict`, `-c/--context-lines`, `-A/--skip-answered`, `--ask` (overwrite is implied)
- `copier recopy [DESTINATION]` — same as copy minus src argument
- `copier check-update [DESTINATION]` — reports whether a newer template version exists

Common flags are shared via `commonFlags` struct in `flags.go`.

### Internal Packages

- `internal/version` — build-time version injection via ldflags (falling back to the module build info) and `Upstream`, the tracked upstream Copier version
- `internal/textutil` — string helpers (`EnsureSuffix`, `ToBool`, `IsBlank`)
- `internal/pathutil` — path validation (`IsSubpath`, symlink-aware `IsWithin`/`Resolve`), git path decoding

### Key Design Patterns

- **Functional options** (`Option` type) for clean API configuration
- **`Prompter` interface** decouples the UI from core logic for testability
- **`Renderer` abstraction** wraps pongo2 with a consistent context-merge pattern
- **Layered `AnswersMap`** — precedence chain replaces Python's `ChainMap`
- **`PatternMatcher`** compiles gitignore-style patterns once for reuse across file walks (same semantics as upstream's PathSpec)
- **Upstream sync** — README.md records the upstream version the port tracks, and `internal/version.Upstream` holds it for `_min_copier_version` checks (bump both when syncing a new upstream release); when porting upstream changes, prefer upstream semantics and document deliberate differences there

## Dependencies

- `git` CLI — primary VCS backend (clone/mirror/worktree, describe, diff, apply, merge-file)
- `go-git/go-git` — fallback clone when git is unavailable; its `gitignore` package powers pattern matching
- `flosch/pongo2` v6.1 — Jinja2-like template engine (single-argument `|filter:arg` syntax)
- `charm.land/huh/v2` (on Bubble Tea v2 / Lip Gloss v2 / Bubbles v2) — terminal forms/prompts. Keep the charm major versions aligned with other binaries embedding this library so only one copy is linked.
- `spf13/cobra` — CLI framework
- `adrg/xdg` — XDG base directory paths (settings and the Git mirror cache)
- `gopkg.in/yaml.v3` — YAML parsing (Node API is used for `!include` and ordered questions)

Requires Go 1.25.8+ (from `charm.land/huh/v2`).
