# Phase 2 daily use implementation plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan inline. Use one fresh independent review after the complete implementation.

**Goal:** Finish the remaining Phase 2 implementation: bulk exec, diagnostics, safe repository retirement/restoration, configuration migration, onboarding and signed release automation.

**Architecture:** Extend the existing application services and narrow adapters. Keep command parsing/output in CLI, subprocess management in a dedicated process adapter, YAML migration and opaque directory moves in config, and lifecycle orchestration/journaling in app. Preserve the existing dashboard and CLI contracts.

**Tech stack:** Go 1.26 module, Cobra, installed Git 2.40+, existing YAML/operation adapters, GitHub Actions and Sigstore keyless release signatures.

**Spec:** `docs/architecture/0001-bearforge-blueprint.md`, especially sections on retirement, execution, cancellation, output, migrations and Phase 2 exit criteria.

## Global constraints and decisions

- The user authorized all remaining Phase 2 implementation. Continue on `feat/phase-2-dashboard-actions`; preserve the existing three feature commits. Conventional Commits remain the integration convention.
- The user explicitly waived validation against the company's private 50+ repositories, reserving that testing until Phase 2 is merged. Use disposable 60-repository scale fixtures; do not access company sources or modify the existing demo.
- Direct exec passes argv after `--` unchanged. Exactly one command expression without `--` selects the platform shell; explicit sh/pwsh/cmd adapters are documented. Default jobs is one; interactive execution is sequential, plain and opt-in.
- Exec uses the existing root mutation lock, verifies every selected worktree/common directory, caps captured output, emits structured progress/output events, stops scheduling on cancellation/fail-fast and never reruns arbitrary commands through resume.
- Doctor is read-only, accumulates independent findings, performs no fetch/prune/repair and returns a failing check status for actionable findings.
- Retirement preserves the entire bare repository via an opaque same-root rename, refuses all references/linked worktrees, journals before effects and has no automatic purge. Restore uses the retired identity and explicit retirement operation ID, refuses conflicts and never clones.
- Introduce catalog schema v2 to record retired repository identities durably; workspace schema remains v1. Existing v1 catalogs remain readable. Explicit `config migrate --to 2` previews/revalidates, backs up exact source bytes and supports recovery. Ordinary writes must not silently rewrite an existing v1 catalog.
- Release automation builds reproducible archives/checksums with version/commit metadata, signs checksums with GitHub OIDC, verifies the signature and supplies verification instructions. No invented signing identity or local production signing key. Actual publication/version tagging is a separate concrete release step after implementation and checks.

## Review focus

1. Child argv, shell boundaries, binary/long output and process-tree cancellation preserve data and structured stdout.
2. Corrupt definitions, unexpected paths and unfinished/corrupt operations remain visible without doctor changing disk state.
3. Rename succeeds but metadata/journal fails: retirement/restore recover by probing and never overwrite another directory or lose registration/history.
4. Migration preserves extensions/comments, refuses future versions or changed bytes, retains a usable backup and does not create files on preview/no-op.
5. Release permissions, immutable action pins, toolchain inputs, archive naming and published verification identity agree.

## Task 1: Bulk execution

Files: `internal/domain/exec.go`, `internal/process/*`, `internal/app/exec.go`, `internal/app/service.go`, `internal/cli/exec.go`, CLI observer/render integration and focused tests.

- [x] Write failing process tests for literal argv/cwd/environment, stdout+stderr/binary/large output, nonzero exit, shells, cancellation and descendant termination.
- [x] Implement bounded capture and streaming, supported shell adapters and platform process supervision.
- [x] Write failing app tests for scope/worktree validation, lock/preflight, jobs/fail-fast, dry-run with no writes, result completeness and non-resumable execution journals.
- [x] Implement `Service.Exec(ctx, ExecRequest)`, process-runner injection, per-repository execution records and explicit abandoned/interrupted exec recovery guidance.
- [x] Wire `exec WORKSPACE [COMMAND] -- [ARGV...]`, selection/jobs/fail-fast/interactive flags and human/JSON/JSONL output. Verify real command boundaries and machine-output integrity.

## Task 2: Doctor

Files: `internal/app/doctor.go`, `internal/cli/doctor.go`, tests and storage/Git capabilities where needed.

- [x] Write failing read-only tests for clean/missing/corrupt roots, repository/remote/worktree drift, unfinished operations, unknown directories and selected/all workspaces.
- [x] Implement accumulated findings with actionable messages and stable check IDs; inspect only with supported Git commands.
- [x] Add CLI check/result integration and verify no files, locks, refs or journals change.

## Task 3: Catalog migration

Files: `internal/domain/model.go`, `internal/domain/migration.go`, `internal/config/migrate.go`, config DTO/load/save changes, `internal/app/migrate.go`, journal/CLI integration and tests.

- [x] Pin catalog v2/retired identity DTOs and strict validation in failing tests; preserve v1 versions during compatible writes and require explicit migration before retired records.
- [x] Test read-only/no-op plans, future/malformed versions, extension/comment preservation, stale confirmation, backup bytes and interrupted apply/recovery.
- [x] Implement preview, root-bound approval, explicit backup and CAS replacement under the mutation lock; expose `config migrate --to 2` with dry-run/confirmation and explicit recovery.

## Task 4: Repository retire/restore

Files: `internal/config/lifecycle.go`, `internal/app/retire.go`, `internal/app/restore.go`, journal operation validation/recovery, `internal/cli/repositories.go`, registration guards, tests.

- [x] Write failing tests for workspace references, registered external worktrees, identity/refs drift, occupied destinations, no-write previews and plan-bound confirmation.
- [x] Implement `repo remove NAME` and alias `retire`, `repo restore NAME --operation ID`, and retired listing. Preserve the exact repository metadata and database; block duplicate re-registration of retired identities/remotes.
- [x] Inject metadata/journal/move failures on each side of rename, exercise explicit resume and verify all local refs/commits survive. Revalidate after interruption; no deletion/clone fallback.

## Task 5: Onboarding, releases and scale validation

Files: README, `docs/onboarding.md`, `docs/releases.md`, release builder/version package, `.github/workflows/release.yml`, CI/test additions and Phase 2 progress ledger.

- [x] Document installation/root setup, migration, two-workspace workflows, exec shells/output, doctor, retirement/restoration, recovery and shell completion on Windows/POSIX.
- [x] Implement and test deterministic cross-platform archive/checksum generation and version reporting; confirm official release/signature tool syntax and immutable workflow pins from primary sources.
- [x] Add least-privilege tag-triggered build/sign/verify/release automation with artifacts/provenance and clear local snapshot validation. Do not claim a signed release was published unless actually observed.
- [x] Exercise 60 disposable repositories, bounded execution and diagnostics, lifecycle/recovery drills and existing TUI regressions. Record the user's waiver of company-repository validation.
- [x] Run full tests, vet, static analysis, native smoke checks and cross-builds; one independent review; fix material findings with reproducing tests; record exact evidence and any remaining external release gates.
