# Implementation ledger — local core

Plan: ../superpowers/plans/2026-11-10-local-core.md

Approved scope: first local-core milestone; no templates. Subsequent roadmap milestones remain separate.

Preflight: domain/config feed Git and application services; CLI and TUI consume application results. No existing code or Git repository to preserve. Work in the current dedicated directory. Module path is provisional `bearforge`.

## Progress

- Domain/config complete: portable ID validation, strict YAML, revision conflicts, atomic replacement and comment retention tested.
- Git adapter complete for the milestone: bare clone/refspec setup, fetch guards, short-name branch attachment, live status and conservative removal tested against real Git.
- Application workflows complete: repo add, empty workspace creation, add/remove worktrees, status, bounded fetch concurrency and operation recovery.
- CLI/inline UI complete for the milestone: Cobra commands, JSON/JSONL, plain fallback, Bubble Tea progress, confirmation and cancellation handling.
- Independent review found three issues: removal recovery after manifest deletion, create recovery adopting unexplained directories, and cancellation at confirmation. All addressed; recovery regressions reproduced before fixes.

## Rulings and limits

- Work is in the original dedicated directory; no repository, remote, commit, or publishing identity was invented.
- `workspace create` currently creates an empty workspace; select repositories with `workspace add`. Combined `--repo`/`--all` creation and richer inline confirmation are deferred.
- Full-window UI, Bubbles/Lip Gloss styling, bulk exec, PR lookup, repo retirement/restore, configuration migrations, and doctor remain later milestones.
- Fetch is repeatable and not journaled; lifecycle mutations are journaled.
- Ambiguous creation recovery refuses an unexplained existing directory, including a directory created just before a crash. Manual inspection is required.
- Native execution validated on Windows. Linux/macOS builds and native CI configuration do not substitute for executed native tests. Race instrumentation requires a C compiler/CGO and was unavailable in this environment.

## Verification

Windows tests, go vet, Staticcheck v0.7.0 and goimports v0.43.0 passed. Windows executable help/version and JSON smoke checks passed. Linux amd64 and macOS arm64 cross-builds passed. Native terminal/ConPTY behavior and native Linux/macOS runs remain release validation work.
