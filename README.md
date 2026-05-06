# copier-go

`copier-go` is a Go implementation of [Copier](https://github.com/copier-org/copier), the Python project templating and update tool.

The goal of this repository is a 1-to-1 behavior port of upstream Copier in the Go language. Templates should keep using Copier's existing template format, `copier.yml` configuration, answers files, Git tag based updates, and CLI workflow. This project is not intended to define a different template system.

Compatibility with upstream Copier is the target, not a claim that every upstream feature is already complete. Known differences and implementation gaps are listed below.

## Relationship To Upstream Copier

Upstream Copier is the source of truth for behavior and compatibility. This Go port tracks upstream Copier changes and ports the behavior where it applies to a native Go implementation.

Use upstream Copier documentation when writing templates unless this README explicitly says otherwise:

https://copier.readthedocs.io/

## Why A Go Port

- Single native binary distribution.
- Embeddable Go library API for Go programs.
- No Python runtime requirement for users of the Go CLI.
- Same Copier concepts for copying, recopying, updating, questionnaires, tasks, and answers files.

## Current Compatibility

Implemented core behavior includes:

- `copy`, `recopy`, `update`, and `check-update` CLI commands.
- Go library API for `Copy`, `Recopy`, `Update`, and `CheckUpdate`.
- Local path and Git template sources, including GitHub and GitLab shortcuts.
- Latest semver Git tag selection, prerelease handling, pinned refs, and stored template metadata.
- `copier.yml` and `copier.yaml` template configuration.
- Interactive and defaulted questions with layered answer precedence.
- Jinja-like rendering through `pongo2`, including common Copier filters such as `to_yaml`, `to_nice_yaml`, `to_json`, `bool`, and `basename`.
- Configurable template delimiters through `_envops`.
- `_envops.undefined: jinja2.StrictUndefined` error behavior for missing top-level variables.
- `_subdirectory`, `_exclude`, `_skip_if_exists`, `_answers_file`, `_secret_questions`, `_external_data`, `_preserve_symlinks`, and template messages.
- Core task and migration execution support.
- Unsafe-feature gating for tasks, migrations, Jinja extensions, and external data reads outside the destination.
- Three-way update flow using Git diffs.
- Executable-bit preservation during copy and update.

## Differences From Upstream Copier

The intended user-facing behavior is the same, but this is not the same codebase. Practical differences are:

| Area | Upstream Copier | copier-go |
| --- | --- | --- |
| Implementation language | Python | Go |
| Distribution | Python package and CLI | Go module and native CLI binary |
| Library API | Python functions/classes | Go functions and functional options |
| Template engine | Jinja2 | `pongo2` Jinja-like engine |
| Python Jinja extensions | Loadable Python extensions | Not executed as Python extensions in Go |
| Plugin ecosystem | Python package ecosystem | Go implementation only |
| Exact edge cases | Defined by upstream Copier and Jinja2 | Ported where practical, but renderer edge cases can differ |
| Configuration loader | Supports upstream YAML includes and multi-document merging | Basic `copier.yml`/`copier.yaml` parsing today |
| Pattern matching | PathSpec/gitignore behavior | Glob-based matching today |
| Update algorithm | Upstream Python update algorithm | Go implementation with Git diff based merge |
| CLI surface | Full upstream Python CLI | Core commands and flags implemented |

Templates that use standard Copier configuration and ordinary Jinja syntax should be the compatibility target. Templates that depend on custom Python Jinja extensions, Python-only filters, or very specific Jinja2 internals may need equivalent Go support before they work here.

## Known Gaps

These are compatibility gaps, not intended product differences:

- Custom Python Jinja extensions are not loaded or executed by the Go renderer.
- Only a subset of upstream Copier's built-in filters and Jinja environment behavior is implemented.
- The YAML configuration loader does not yet implement upstream `!include` handling or multi-document merge semantics.
- Exclude and skip matching use glob behavior, not the full upstream PathSpec/gitignore semantics.
- Some newer upstream task and migration schema details may need additional porting.
- The update algorithm is implemented in Go but is not yet guaranteed to match every upstream conflict and edge-case behavior.
- The CLI exposes the core workflow but not every upstream Python CLI option.

## Install And Build

Build the CLI from this repository:

```sh
make build
```

Run tests:

```sh
make test
```

Run linting:

```sh
make lint
```

## CLI Usage

Copy a template:

```sh
copier copy gh:org/template ./my-project
```

Update an existing project from its recorded template:

```sh
copier update ./my-project
```

Check whether a project has a newer template version:

```sh
copier check-update ./my-project
```

Recopy a project from its template using existing answers:

```sh
copier recopy ./my-project
```

## Go Library Usage

```go
package main

import copier "github.com/fyltr/copier-go"

func main() {
	_ = copier.Copy(
		"gh:org/template",
		"./my-project",
		copier.WithData(map[string]any{"project_name": "my-project"}),
		copier.WithDefaults(true),
	)
}
```

## Sync Policy

When upstream Copier changes behavior, the Go port should prefer matching upstream semantics over inventing Go-specific behavior. Differences should be documented here and reduced over time when they are implementation gaps rather than intentional Go API differences.

## License

This project uses the MIT license, matching upstream Copier.
