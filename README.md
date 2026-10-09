# copier-go

`copier-go` is a Go implementation of [Copier](https://github.com/copier-org/copier), the Python project templating and update tool.

The goal of this repository is a 1-to-1 behavior port of upstream Copier in the Go language. Templates should keep using Copier's existing template format, `copier.yml` configuration, answers files, Git tag based updates, and CLI workflow. This project is not intended to define a different template system.

Compatibility with upstream Copier is the target, not a claim that every upstream feature is already complete. Known differences and implementation gaps are listed below.

## Relationship To Upstream Copier

Upstream Copier is the source of truth for behavior and compatibility. This Go port tracks upstream Copier changes and ports the behavior where it applies to a native Go implementation.

Use upstream Copier documentation when writing templates unless this README explicitly says otherwise:

https://copier.readthedocs.io/

**Sync status:** the port currently tracks upstream Copier **v9.18.2** plus the unreleased upstream `master` changes through `04a8619` (October 2026): the `ask` question setting and keeping symlinks when a renamed directory is updated.

## Why A Go Port

- Single native binary distribution.
- Embeddable Go library API for Go programs.
- No Python runtime requirement for users of the Go CLI.
- Same Copier concepts for copying, recopying, updating, questionnaires, tasks, and answers files.

## Requirements

- Go 1.25.8 or newer to build (required by `charm.land/huh/v2`).
- `git` on `PATH` for Git templates, `update`, `recopy` of Git templates and `check-update`. Local directory templates work without git.

## Current Compatibility

Implemented behavior includes:

- `copy`, `recopy`, `update`, and `check-update` CLI commands, with the upstream flags (`--data`, `--data-file`, `--ask`, `--skip-answered`, `--exclude`, `--skip`, `--vcs-ref`, `--prereleases`, `--trust`/`--UNSAFE`, `--conflict`, `--context-lines`, ...).
- Go library API for `Copy`, `Recopy`, `Update`, and `CheckUpdate`, including a `Prompter` interface for embedding custom UIs and `EvaluateWhen` to evaluate a question's `when` condition outside the questionnaire (e.g. for an input form).
- Local path and Git template sources, including GitHub and GitLab shortcuts, `git+` URLs, bundles and `~` expansion.
- Remote Git templates are cached as bare mirrors under `$COPIER_CACHE_DIR` (default: the user cache dir, e.g. `~/.cache/copier/git`) and checked out as temporary worktrees, so repeated use avoids full re-downloads.
- Latest version tag selection using PEP 440 ordering (also accepting semver spellings), pre-release handling, pinned refs (`--vcs-ref`, `:current:`), and template metadata (`_src_path`, `_commit`) in the answers file.
- Dirty changes of local Git templates are included when rendering `HEAD`, like upstream.
- `copier.yml` / `copier.yaml` loading with multiple YAML documents, the `!include` tag (with globs, restricted to the template root), and upstream merge rules (`_exclude`, `_skip_if_exists`, `_jinja_extensions`, `_secret_questions` are concatenated; other keys are overridden by later documents).
- Questions asked in definition order, with typed answers (`str`, `int`, `float`, `bool`, `yaml`, `json`, `path`), type inference from defaults, `default`, `help`, `placeholder`, `when`, `ask`, `validator`, `secret`, `multiline`, `multiselect`, list/dict/tuple-style and dynamic (templated) `choices`, conditional choices via `validator`, and the `UNSET` default marker. Validators run on prompted answers, on answers given as data, and on defaults used with `--defaults`; computed defaults of questions skipped by `when` and defaults of secret questions are not validated, like upstream.
- Layered answer precedence (user > `--data` > metadata > last answers > user defaults > external data), `--skip-answered`, `--ask` patterns, and settings defaults from `settings.yml`.
- Answers file rendered from the template's `{{ _copier_conf.answers_file }}.jinja` using `_copier_answers`; when a template has no such file, copier-go writes one itself.
- Jinja-like rendering through `pongo2`, with the render context upstream provides (`_copier_conf`, `_copier_answers`, `_copier_phase`, `_copier_operation`, `_folder_name`, `_external_data`, `pathjoin`) and common Jinja / `jinja2-ansible-filters` filters (`to_yaml`, `to_nice_yaml`, `to_json`, `to_nice_json`, `from_json`, `from_yaml`, `bool`, `int`, `hash`, `b64encode`, `strftime`, `basename`, `dirname`, `regex_search`, `unique`, `sort`, `combine`, `dict2items`, ...).
- The `{% yield item from list %}` tag in file and directory names to generate multiple files from one template path.
- `{% include %}` restricted to the template root (paths or symlinks escaping it are rejected).
- Configurable template delimiters through `_envops` and `_envops.undefined: jinja2.StrictUndefined` error behavior.
- `_subdirectory` (templated), `_templates_suffix` (including an empty suffix), `_answers_file`, `_secret_questions`, `_external_data`, `_preserve_symlinks`, `_min_copier_version`, and the `_message_*` settings.
- gitignore-style (`gitwildmatch`) pattern matching for `_exclude`, `--exclude`, `_skip_if_exists` and `--skip`, evaluated against destination paths and rendered as Jinja (an entry may render to several newline-separated patterns).
- Tasks in every upstream format (string, argument list, or mapping with `command`, `when`, `working_directory`) with `_stage`/`$STAGE` and `_copier_operation` available.
- Migrations in the current upstream format (`command`, `version`, `when`, `working_directory`) and the legacy `before`/`after` format, with `_version_from`, `_version_to`, `_version_current` and the `_version_pep440_*` variables.
- Unsafe-feature gating for tasks, migrations, Jinja extensions, and `_external_data` reads outside the destination, with `--trust`/`--UNSAFE` and the settings trust list. Trust checks resolve dot segments in URL, SCP-style and alias paths; a repository URL whose path has anything other than RFC 3986 unreserved characters (percent-encoding, backslashes, doubled slashes) only matches an exact, verbatim trust entry, never a prefix.
- The upstream update algorithm: the old and new template versions are rendered with the recorded answers, the project's own changes are extracted as a diff and re-applied with `git apply --reject`, rejected hunks are turned into inline conflict markers (`--conflict inline`, recorded as unmerged in the index) or left as `.rej` files (`--conflict rej`), intentionally deleted files are not recreated, template-managed gitignored files are still updated, and files removed by the new template version are deleted (symlinks are kept, so files are never deleted through them).
- Executable-bit preservation during copy and update, including `core.fileMode=false` repositories.

## Differences From Upstream Copier

The intended user-facing behavior is the same, but this is not the same codebase. Practical differences are:

| Area | Upstream Copier | copier-go |
| --- | --- | --- |
| Implementation language | Python | Go |
| Distribution | Python package and CLI | Go module and native CLI binary |
| Library API | Python functions/classes | Go functions and functional options |
| Template engine | Jinja2 | `pongo2` Jinja-like engine (see gaps below) |
| Python Jinja extensions | Loadable Python extensions | Not executed; templates listing `_jinja_extensions` are still flagged as unsafe |
| Local template `_src_path` | Recorded as typed (may be relative) | Recorded as an absolute path |
| Answers file | Only written when the template provides `{{ _copier_conf.answers_file }}.jinja` | Also written by copier-go when the template has none |
| Task environment | Only `STAGE`, `VERSION_*`, `COPIER_OPERATION` | Additionally every answer as an upper-cased variable (Go extension) |
| `ssh://` URLs | Need `git+` or a `.git` suffix | Recognized as Git URLs directly |
| Binary `.jinja` files | Error (undecodable) | Copied verbatim |
| `Update` in the library | Requires `overwrite=True` | Overwrite is implied |
| `{% include %}` sandbox | Template root | Template root; the Go library option `WithIncludeRoot` can widen it to a containing directory (names still resolve against the template root) |
| `_min_copier_version` | Compared with the installed Copier version | Compared with the upstream Copier version the port tracks (`internal/version.Upstream`, shown by `copier --version`) |

Templates that use standard Copier configuration and ordinary Jinja syntax should be the compatibility target. Templates that depend on custom Python Jinja extensions, Python-only filters, or very specific Jinja2 internals may need equivalent Go support before they work here.

## Known Gaps

These are compatibility gaps, not intended product differences:

- `pongo2` is Django-flavoured: filters take a single argument with the `{{ x|filter:arg }}` syntax, so Jinja calls like `{{ x|replace('a', 'b') }}`, `{{ x|default('y', true) }}` or multi-argument `regex_replace` do not parse. Tests such as `is defined`, list/dict literals in expressions, and some Jinja-only tags are also unavailable.
- Custom Python Jinja extensions are not loaded or executed by the Go renderer.
- `jinja2.StrictUndefined` is approximated by scanning expressions for undefined top-level names.
- Only a subset of the `jinja2-ansible-filters` filters is implemented.
- Executable bits are read from the filesystem, not from the template's git index (matters on Windows only).
- Updating requires the template to have a version tag: upstream derives a version for untagged commits through dunamai (e.g. `0.0.0.post3.dev0+abc1234`), copier-go reports "version from last update not detected".
- `--data` values and interactive input are parsed per question type, but JSON/YAML questions have no syntax-highlighted editor.
- The `Prompter` UI (`charm.land/huh/v2`) is not a pixel-perfect clone of the `questionary` prompts.

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

Force asking selected questions even when answers are known:

```sh
copier update --skip-answered --ask 'database_*' ./my-project
```

Environment variables: `COPIER_SETTINGS_PATH` (settings file), `COPIER_CACHE_DIR` (Git mirror cache).

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

Embedding applications can supply their own questionnaire UI by implementing the `Prompter` interface and passing it with `copier.WithPrompter(p)`.

## Sync Policy

When upstream Copier changes behavior, the Go port should prefer matching upstream semantics over inventing Go-specific behavior. Differences should be documented here and reduced over time when they are implementation gaps rather than intentional Go API differences.

## License

This project uses the MIT license, matching upstream Copier.
