# Bearforge Local Core Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Deliver the first runnable local CLI milestone: register bare repositories, create and inspect workspaces, materialize worktrees, synchronize, report health, and safely remove worktrees with recoverable operations.

**Architecture:** Cobra and Bubble Tea are presentation adapters over application services. Git commands provide repository facts; strict YAML stores intent and local journals record mutation progress. One root lock serializes mutation invocations.

**Tech Stack:** Go 1.26, Cobra, Bubble Tea v2, YAML v3, gofrs/flock, x/sys and x/term.

**Spec:** ../../architecture/0001-bearforge-blueprint.md

## Global Constraints

- Never edit Git internals; execute installed Git using separate arguments.
- No workspace templates.
- Linux, macOS, Windows; minimum Git 2.40.
- Root defaults to ~/code; portable lowercase IDs are at most 64 characters.
- Preserve existing local branches and reject unsafe checkout removal.
- JSON is independent of interactive rendering; dry-run never mutates.
- This milestone omits the full dashboard, bulk exec, PR providers, repo retirement/restore, remote reconciliation and configuration migration. Commands not implemented are not advertised as available.

## Review Focus

- Path traversal and symlink/reparse-point substitution must not redirect writes/removals.
- An existing directory or wrong Git common directory must not be adopted implicitly.
- Crash after Git success before metadata commit must be recoverable without reset or deletion.
- Dirty/ignored/special-index content and detached history must never be discarded.
- JSON and noninteractive commands must never enter terminal mode or hang for input.

## Task 1: Domain and persistence

Files: go.mod, internal/domain/model.go, internal/domain/validate.go, internal/config/store.go, internal/config/atomic_{unix,windows}.go, internal/config/store_test.go.

Interfaces: domain Repository, Workspace, Mapping; config.New(root), LoadCatalog, LoadWorkspace, SaveCatalog, SaveWorkspace, ListWorkspaces; revision checked writes.

- [x] Write validation and YAML tests for portable IDs, unknown/duplicate fields, newer schemas, revisions, and unsafe paths; run and observe failure.
- [x] Implement strict bounded decoding, stable comment-preserving YAML updates and atomic replacement.
- [x] Run go test ./internal/domain ./internal/config; require all assertions pass.

## Task 2: Git adapter

Files: internal/gitcli/client.go, worktree.go, status.go, process_{unix,windows}.go, git_test.go.

Interfaces: Clone, Fetch, Worktrees, Resolve, BranchExists, Add, Inspect, Remove and VerifyCommon; all accept context and explicit paths. Observations use domain types.

- [x] Write local-Git integration tests for refspecs, short-name attachment, branch collision, dirty/ignored/index-flag refusal and cancellation.
- [x] Implement subprocess adapter, environment sanitization, parsing, safe refspec checks and conservative removal.
- [x] Run go test ./internal/gitcli; verify expected state through Git independently.

## Task 3: Application workflows and recovery

Files: internal/app/service.go, repo.go, workspace.go, sync.go, operations.go; internal/operation/store.go; internal/app/integration_test.go.

Interfaces: Service accepts Git and intent ports; AddRepository, CreateWorkspace, AddRepositories, RemoveRepositories, RemoveWorkspace, Sync, Status, Resume return structured Result. Operation steps record intent before execution and metadata after verified Git effects.

- [x] Write end-to-end tests for two repos/two workspaces, exact retries, mismatched destination, partial failure and resume after metadata failure.
- [x] Implement mutation lock, durable operation snapshots, bounded fetch workers and no-op/dry-run behavior.
- [x] Run go test ./internal/app ./internal/operation; require clean state and preserved edits after refusal.

## Task 4: CLI and Bubble Tea

Files: cmd/bearforge/main.go, internal/cli/root.go, commands.go, root_test.go, internal/tui/progress.go, progress_test.go.

Interfaces: cli.Run(context,args,stdin,stdout,stderr) int; presentation chooses plain/JSON/JSONL/inline before executing a service action. TUI consumes progress and final results without importing Git.

- [x] Test help without root creation, error exit codes, JSON on failure, confirmations, no-input and incompatible inline mode.
- [x] Implement commands, completion, cancellation and terminal-independent output.
- [x] Implement Bubble Tea inline progress with a separate presentation lifetime; test message handling and completion.
- [x] Run go test ./internal/cli ./internal/tui and build the executable.

## Task 5: Release-quality foundation and review

Files: README.md, .gitignore, .github/workflows/ci.yml, docs/development/local-core-progress.md.

- [x] Document supported commands, limitations, credentials, recovery, and build instructions.
- [x] Add native OS CI, vet and platform build checks.
- [x] Run go test ./..., go vet ./..., staticcheck and goimports; smoke-test built help/JSON and cross-compile Linux/macOS.
- [x] Request an independent review; reproduce important findings in tests and fix them before delivery.

## Execution rulings

- Approved blueprint plus “let's move forward” authorizes implementation; no additional approval gate is introduced.
- The project directory has no existing Git repository or implementation. Work in this dedicated directory; do not fabricate a remote or publishing identity. Use module path `bearforge` until the public repository URL is selected.
- Local source-control commits are deferred because no repository is initialized; progress is tracked in the implementation ledger.

## Completion notes

The local-core milestone is implemented and independently reviewed. See README and the implementation ledger for explicit deviations and follow-up scope. Native CI is configured; only Windows tests ran locally. No Git repository or remote was initialized.
