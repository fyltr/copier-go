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
| `worker.go` | Execution engine: orchestrates prompt → render → tasks → migrate phases. Contains the `worker` struct, `runCopy()`, `runUpdate()`, and the 3-way merge algorithm. |
| `template.go` | Loads `copier.yml`/`copier.yaml`, parses config (underscore-prefixed keys) and questions (other keys). Handles `TemplateConfig`, `TaskDef`, `MigrationDef`. |
| `question.go` | `AnswersMap` with layered precedence (User > Init > Metadata > Last > UserDefaults > Builtin). Answer parsing, type coercion, validation, and conditional display (`ShouldAsk`, `ValidateAnswer`). |
| `prompt.go` | `TerminalPrompter` — interactive UI using charmbracelet/huh. Implements the `Prompter` interface for testability. |
| `render.go` | Jinja2-compatible template rendering via pongo2. `Renderer` handles string, file, and path rendering. Binary detection. |
| `vcs.go` | Git operations via go-git: clone, tag discovery, semver sorting, URL normalization (`gh:`, `gl:` shortcuts). Falls back to `git` CLI for apply/diff. |
| `settings.go` | User settings from `$XDG_CONFIG_HOME/copier/settings.yml`. Trust lists and default answers. |
| `fileops.go` | File copy, directory walk, glob-based pattern matching, answers file I/O. |
| `types.go` | Enums (`Phase`, `Operation`, `ConflictStrategy`, `QuestionType`), constants, `LazyMap` for deferred computation. |
| `errors.go` | Sentinel errors and typed error structs (`TemplateError`, `TaskExecError`, `QuestionError`, `ValidationError`). |

### CLI (`cmd/copier/`)

Built on cobra. Three subcommands mirror the library API:
- `copier copy TEMPLATE DESTINATION` — flags: `-d/--data`, `-l/--defaults`, `-f/--force`, `-w/--overwrite`, `-n/--pretend`
- `copier update [DESTINATION]` — flags: `-o/--conflict`, `-c/--context-lines`, `-A/--skip-answered`
- `copier recopy [DESTINATION]` — same as copy minus src argument

Common flags are shared via `commonFlags` struct in `flags.go`.

### Internal Packages

- `internal/version` — build-time version injection via ldflags
- `internal/textutil` — string helpers (`EnsureSuffix`, `ToBool`, `IsBlank`)
- `internal/pathutil` — path validation (`IsSubpath`), git path decoding

### Key Design Patterns

- **Functional options** (`Option` type) for clean API configuration
- **`Prompter` interface** decouples the UI from core logic for testability
- **`Renderer` abstraction** wraps pongo2 with a consistent context-merge pattern
- **Layered `AnswersMap`** — precedence chain replaces Python's `ChainMap`
- **`PatternMatcher`** compiles glob patterns once for reuse across file walks

## Dependencies

- `go-git/go-git` — pure-Go git (clone, tags, checkout); CLI `git` fallback for apply/diff
- `flosch/pongo2` — Jinja2-compatible template engine
- `charmbracelet/huh` — terminal forms/prompts
- `spf13/cobra` — CLI framework
- `Masterminds/semver` — semver parsing and sorting
- `gobwas/glob` — glob pattern matching
- `adrg/xdg` — XDG base directory paths
- `gopkg.in/yaml.v3` — YAML parsing
