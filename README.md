# GitHookDoctor: why is my Git hook not running?

**Diagnose Git hooks that silently do nothing: Husky, Lefthook, pre-commit, simple-git-hooks and plain `.git/hooks` scripts. GitHookDoctor checks `core.hooksPath`, missing installs, executable bits, CRLF line endings, shebangs, deprecated Husky lines and invalid configs, then explains each problem and how to fix it. It never changes anything without your approval.**

[![CI](https://github.com/6t9xstar/git-hook-doctor/actions/workflows/ci.yml/badge.svg)](https://github.com/6t9xstar/git-hook-doctor/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8)

A Git hook that does not run gives you no error. Lint-staged never formats anything, commit messages are never checked, and the first sign of trouble is a failed CI run. The cause is usually one of a dozen small things:

- `core.hooksPath` points to a folder that does not exist
- the hook manager was never installed after cloning
- the file is not executable in Git
- the file was saved with Windows line endings
- the hook name has a typo (`precommit`, `pre-commit.sh`)

**GitHookDoctor** checks all of them at once.

```bash
go install github.com/6t9xstar/git-hook-doctor/cmd/githookdoctor@latest
githookdoctor diagnose            # in your repository
githookdoctor diagnose pre-commit  # only what affects one hook
```

## Example

[`examples/make-broken-repo.sh`](examples/make-broken-repo.sh) creates a repository with the most common problems. Here is what GitHookDoctor reports for it (paths shortened):

```text
$ githookdoctor scan
GIT HOOKS REPORT
Repository: /home/dev/broken-hooks  ·  git 2.55.0
Hooks dir:  .githooks-missing  (core.hooksPath (local: .git/config))
Managers:   Husky v9 (package.json (^9.1.7)), Lefthook (lefthook.yml), pre-commit (.pre-commit-config.yaml)

HOOK           MANAGER     FILE                     STATUS
pre-commit     husky       .husky/pre-commit        ✖ CRLF line endings, deprecated husky.sh line
pre-commit     pre-commit  .pre-commit-config.yaml  ✖ not installed
commit-msg     husky       .husky/commit-msg        ✖ not installed
pre-push       lefthook    lefthook.yml             ✖ not installed
pre-push       pre-commit  .pre-commit-config.yaml  ✖ not installed
prepush        husky       .husky/prepush           ✖ not a hook name
commit-msg.sh  custom      .githooks/commit-msg.sh  ✖ not a hook name

7 error(s), 6 warning(s), 3 info. Run `githookdoctor diagnose` for details.
```

`diagnose` explains each finding and gives the fix:

```text
$ githookdoctor diagnose pre-commit
[ERROR]   core.hooksPath is ".githooks-missing" (local config, .git/config), but /home/dev/broken-hooks/.githooks-missing does not exist. Git silently runs no hooks at all.
          GH001
          Fix: create the directory, or remove the setting: git config --local --unset core.hooksPath
[ERROR]   package.json has a Husky v4 "husky.hooks" section (pre-commit), but Husky 5+ ignores it. Those hooks never run.
          GH007 package.json:8
          Fix: move each hook command to a file in .husky/ (e.g. .husky/pre-commit) and delete the old config
[ERROR]   .pre-commit-config.yaml needs the pre-commit, pre-push hook(s), but `pre-commit install` refuses to run while core.hooksPath is set (".githooks-missing"): "Cowardly refusing to install hooks with `core.hooksPath` set."
          GH027
          Fix: unset it (git config --local --unset core.hooksPath) and run `pre-commit install`, or call `pre-commit run --hook-stage <stage>` from a hook in .githooks-missing
[ERROR]   .husky/pre-commit has Windows (CRLF) line endings. The hook fails with errors like "/usr/bin/env: 'sh\r': No such file or directory" or "$'\r': command not found".
          GH010 .husky/pre-commit:1
          Fix: convert the file to LF and add `.husky/** text eol=lf` to .gitattributes
[WARNING] The prepare script runs `husky install`, which is deprecated in Husky v9 (it prints "install command is DEPRECATED").
          GH006 package.json:4
          Fix: use "prepare": "husky"
[WARNING] .husky/pre-commit still sources husky.sh. Husky v9 prints "DEPRECATED" for this line and Husky v10 will fail.
          GH005 .husky/pre-commit:2
          Fix: remove the `#!/usr/bin/env sh` and `. "$(dirname -- "$0")/_/husky.sh"` lines
...
```

`fix` previews safe changes. Nothing is modified until you run `fix --apply` and approve each change:

```text
$ githookdoctor fix
Planned changes (dry run: nothing was modified)

 1. [GH001] remove the broken core.hooksPath setting (local config)
      run: git config --local --unset-all core.hooksPath
 2. [GH010] convert .husky/pre-commit to LF line endings
      rewrite .husky/pre-commit with LF line endings
 3. [GH005] remove the deprecated husky.sh lines from .husky/pre-commit
      delete the #!/usr/bin/env sh and husky.sh lines in .husky/pre-commit
 4. [GH014] rename .husky/prepush to pre-push
      mv .husky/prepush .husky/pre-push
 5. [GH014] rename .githooks/commit-msg.sh to commit-msg
      mv .githooks/commit-msg.sh .githooks/commit-msg

Run these yourself (they install or reconfigure a hook manager):
  - [GH004] install the Husky hooks: npx husky
  - [GH016] install the Lefthook hooks: lefthook install

Apply with `githookdoctor fix --apply`. You will be asked to approve each change.
```

## What it checks

| Area | Checks |
| --- | --- |
| **core.hooksPath** | Points to a missing folder; set globally so it overrides every repository; leaves old hooks in `.git/hooks` ignored; Windows path used from WSL (or the other way round) |
| **Husky** | Hooks not installed (no `core.hooksPath`); v8 → v9 layout mismatch; deprecated `husky.sh` lines (fail in v10); missing or deprecated `prepare` script (`husky install`); ignored Husky v4 config in package.json or `.huskyrc`; `HUSKY=0` set |
| **Lefthook** | Config present but hooks not installed; misspelled hook keys (`precommit:`); invalid YAML; `LEFTHOOK=0` set |
| **pre-commit** | Hook not installed; stages (`commit-msg`, `pre-push`) whose hook type is not installed; deleted virtualenv in `INSTALL_PYTHON`; refusing to install because `core.hooksPath` is set; deprecated stage names |
| **simple-git-hooks** | Configured hooks missing or out of date (it only updates when you run it) |
| **Hook files** | Not executable on disk or in the Git index (`100644`); CRLF line endings; UTF-8 BOM; no `#!` line; interpreter not installed; misnamed files; empty files |
| **Environment** | Tools the hook calls (`npx`, `node`, `python`, `pnpm`, ...) missing from `PATH`, a common problem in GUI Git clients and with nvm; Git aliases with `--no-verify`; `core.autocrlf=true` without an `eol=lf` rule |
| **Conflicts** | Several hook managers configured at once, and which one currently owns the hooks |

## Install

**Go** (any OS with Go 1.22+):

```bash
go install github.com/6t9xstar/git-hook-doctor/cmd/githookdoctor@latest
```

**Prebuilt binaries** for Linux, macOS and Windows (amd64 and arm64) are attached to every [GitHub release](https://github.com/6t9xstar/git-hook-doctor/releases). Extract the archive and put `githookdoctor` on your `PATH`.

**From source:**

```bash
git clone https://github.com/6t9xstar/git-hook-doctor
cd git-hook-doctor && go build -o githookdoctor ./cmd/githookdoctor
```

## Usage

```text
githookdoctor scan                  List hooks, managers and their status
githookdoctor diagnose [hook]       Explain every problem (optionally for one hook, e.g. pre-commit)
githookdoctor fix [--dry-run]       Preview the fixes (default: nothing is changed)
githookdoctor fix --apply           Apply fixes, asking before each change
githookdoctor rules                 List all checks
```

| Option | Meaning |
| --- | --- |
| `-C, --dir <path>` | Repository to inspect (default: current directory) |
| `--static` | Only check committed files. Use this in CI, where hooks are never installed |
| `--format <fmt>` | `text`, `json`, `markdown`, `sarif` or `github` |
| `-o, --output <file>` | Write the report to a file |
| `--fail-on <level>` | `error` (default), `warning`, `info` or `none` |
| `--apply` / `--yes` | Apply fixes, asking before each one / approve all of them |
| `--only GH009,GH010` | Limit `fix` to some rules |

Exit codes: `0` no findings at `--fail-on`, `1` findings, `2` usage error.

### What `fix` changes, and what it never does

GitHookDoctor only fixes files and settings where the right change is clear: it adds the executable bit (on disk and with `git update-index --chmod=+x`), converts CRLF to LF, removes a BOM, adds a `#!/bin/sh` line, removes deprecated Husky lines, renames misnamed hooks, removes a broken `core.hooksPath` and adds `eol=lf` rules to `.gitattributes`.

Every change is shown before it is made and needs a `y`. An empty answer, `n`, or no terminal input means **no**. Installing a hook manager (`npx husky`, `lefthook install`, `pre-commit install`) is always left to you.

## GitHub Action

In CI, hooks are not installed, so the action runs in `--static` mode by default and checks what is committed: executable bits in Git, line endings, shebangs, Husky, Lefthook and pre-commit configuration. That catches the hooks that will break for the next person who clones the repository.

```yaml
name: Git hooks
on: [push, pull_request]

jobs:
  hooks:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      security-events: write   # only for the SARIF upload
    steps:
      - uses: actions/checkout@v7
      - uses: 6t9xstar/git-hook-doctor@v1
        with:
          fail-on: error
          sarif-output: githookdoctor.sarif
      - uses: github/codeql-action/upload-sarif@v4
        if: always()
        with:
          sarif_file: githookdoctor.sarif
```

| Input | Default | Description |
| --- | --- | --- |
| `path` | `.` | Repository directory |
| `static` | `true` | Only check committed files |
| `fail-on` | `error` | `error`, `warning`, `info` or `none` |
| `sarif-output` | | Also write a SARIF report |
| `args` | | Extra arguments for `githookdoctor diagnose` |

## Rules

| ID | Severity | CI (`--static`) | Description |
| --- | --- | --- | --- |
| GH001 | error | | `core.hooksPath` points to a directory that does not exist |
| GH002 | warning | | `core.hooksPath` is set globally and overrides every repository |
| GH003 | error | | Hooks in `.git/hooks` are ignored because `core.hooksPath` points elsewhere |
| GH004 | error | | Husky is configured but its hooks are not installed |
| GH005 | warning | ✓ | A Husky v9 hook still sources `husky.sh` |
| GH006 | warning | ✓ | No `prepare` script installs Husky, or it uses the deprecated `husky install` |
| GH007 | error | ✓ | Husky v4 configuration is ignored by Husky 5+ |
| GH008 | info | | `HUSKY=0`, `LEFTHOOK=0`, `SKIP=...` or `SKIP_SIMPLE_GIT_HOOKS=1` is set |
| GH009 | error | ✓ | Hook file not executable (disk or Git index) |
| GH010 | error | ✓ | Hook file has CRLF line endings |
| GH011 | error | ✓ | Hook file starts with a UTF-8 BOM |
| GH012 | warning | ✓ | Hook file has no `#!` line |
| GH013 | error | | The `#!` interpreter is not installed |
| GH014 | warning | ✓ | File name is not a Git hook (`precommit`, `pre-commit.sh`) |
| GH015 | info | | No hooks installed |
| GH016 | error | | Lefthook configured but not installed |
| GH017 | warning | ✓ | Unknown key in `lefthook.yml` |
| GH018 | error | | pre-commit configured but not installed |
| GH019 | error | | pre-commit hook points to a deleted Python |
| GH020 | warning | | A pre-commit stage's hook type is not installed |
| GH021 | info | ✓ | Deprecated pre-commit stage names |
| GH022 | warning | ✓ | Several hook managers configured |
| GH023 | error | | simple-git-hooks hook missing or outdated |
| GH024 | info | | A Git alias uses `--no-verify` |
| GH025 | warning | | `core.autocrlf=true` without `eol=lf` for hook scripts |
| GH026 | warning | | A hook calls a tool that is not on `PATH` |
| GH027 | error | | pre-commit refuses to install while `core.hooksPath` is set |
| GH028 | info | ✓ | Empty hook file |
| GH029 | error | ✓ | Hook manager config cannot be parsed |

## FAQ

### Why is my Husky pre-commit hook not running?

Run `githookdoctor diagnose pre-commit`. The usual causes are:

- **Hooks not installed.** Husky was never installed after cloning (`npm install` with a `"prepare": "husky"` script fixes it).
- **Wrong `core.hooksPath`.** It still points to `.husky` after an upgrade to v9 (v9 uses `.husky/_`), or it is overridden globally.
- **Hooks disabled.** `HUSKY=0` is set.
- **Broken hook file.** It has CRLF line endings, or on Husky v4–v8 it is not executable.

### "hint: The '.husky/pre-commit' hook was ignored because it's not set as executable"

Git only runs executable hook files. Run `chmod +x .husky/pre-commit`. If the file is committed, also run `git update-index --chmod=+x .husky/pre-commit`; otherwise everyone on macOS and Linux gets a non-executable copy. Windows users cannot set the bit on disk, so the Git index is what counts. `githookdoctor fix` does both.

### "/usr/bin/env: 'sh\r': No such file or directory"

The hook has Windows (CRLF) line endings. Convert it to LF. Then add `.husky/** text eol=lf` (or your hooks folder) to `.gitattributes`, so `core.autocrlf=true` cannot convert it back on checkout.

### "Cowardly refusing to install hooks with `core.hooksPath` set"

pre-commit will not install while `core.hooksPath` is set, usually by Husky or a global setting. Either unset it (`git config --unset core.hooksPath`) and run `pre-commit install`, or keep Husky and call `pre-commit run` from `.husky/pre-commit`.

### My hooks work in the terminal but not in VS Code, GitHub Desktop or SourceTree

GUI Git clients start hooks in a non-interactive shell without your `.bashrc`/`.zshrc`, so tools from nvm, fnm, asdf or pyenv are missing (`npx: command not found`). GitHookDoctor reports this as GH026. For Husky, put the PATH setup in `~/.config/husky/init.sh`.

### Does it work on Windows?

Yes. It runs natively on Windows, macOS and Linux (and WSL). On Windows it checks the executable bit in the Git index instead of the file system. It also warns about CRLF and about Windows paths in `core.hooksPath` used from WSL (and the other way round).

### Will it change my hooks or config?

Not unless you ask. `scan` and `diagnose` only read. `fix` is a dry run by default. `fix --apply` asks before every single change, and installing hook managers is left to you.

## More developer tools

- [WorkflowTriggerDoctor](https://github.com/6t9xstar/workflow-trigger-doctor): why a GitHub Actions workflow did not run
- [PackageProof](https://github.com/6t9xstar/packageproof): detect hallucinated and non-existent npm and PyPI packages
- [PaginationProbe](https://github.com/6t9xstar/pagination-probe): REST API pagination tester
- [GitHookDoctor](https://github.com/6t9xstar/git-hook-doctor): Git hooks, Husky, Lefthook and pre-commit troubleshooter
- [NpmTarballAudit](https://github.com/6t9xstar/npm-tarball-audit): npm package contents and secrets auditor
- [CachePilot](https://github.com/6t9xstar/github-actions-cache-optimizer): GitHub Actions cache analyzer and optimizer
- [ComposeDoctor](https://github.com/6t9xstar/docker-compose-doctor): Docker Compose validator and troubleshooter
- [DevContainer Doctor](https://github.com/6t9xstar/devcontainer-json-validator): devcontainer.json validator
- [OwnerScope](https://github.com/6t9xstar/codeowners-coverage-checker): CODEOWNERS validator and coverage checker
- [ScriptPort](https://github.com/6t9xstar/npm-scripts-cross-platform-checker): cross-platform npm scripts checker

## License

[MIT](LICENSE)
