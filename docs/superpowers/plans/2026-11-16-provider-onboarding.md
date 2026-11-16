# Provider Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Preserve remote ref identities on Windows and let users authenticate, discover, select, and import GitHub/Bitbucket repositories in the terminal dashboard.

**Architecture:** Keep Git effects in `gitcli`, connection/grant resolution in a new `auth` package, and journaled batch orchestration in `app`. Extend the existing provider clients for repository listing and use a separate import modal in the TUI. Preserve old constructors, environment-token behavior, catalog data, and single-repository recovery.

**Tech Stack:** Go 1.27.4, Git 2.40+, Cobra, Bubble Tea v2, existing guarded HTTP clients, native OS secret storage through an adapter.

**Spec:** `docs/superpowers/specs/2026-11-16-repository-discovery-and-ref-collisions-design.md` (approved).

## Global Constraints

- Preserve successful clone steps, exact refs, all existing reflogs, and user worktrees; never discard/reclone retained state as recovery.
- Ordinary Git 2.40+ repositories remain supported; probe reftable capabilities when required.
- Both GitHub and Bitbucket support login/status/logout and guided tokens; browser login requires a configured supported provider registration.
- No secret values in URLs, argv, logs, journals, catalog metadata, or plaintext credential files.
- API and Git access are separate observations; anonymous read success is not proof of authenticated identity.
- The TUI remains offline until an explicit network action; import is usable without a workspace.
- Root mutations use the existing lock/journal/revision checks; user-level auth repair must work while an import is partial.
- Preserve original checkout README.md and docs/desktop-app-discussion.md edits during implementation and handoff. The subsequent commit request explicitly reverts those two earlier edits. Work in `.tmp/worktrees/provider-onboarding`, then transfer only reviewed implementation files back to the active checkout.

## Review Focus

- Git 2.47 returning zero with silently aliased refs: enumerate exact names/OIDs, including component case differences.
- Bitbucket Cloud API-token Basic versus legacy Bearer credentials: isolate schemes and test both API and Git usernames.
- A saved explicit connection disappears or changes accounts: fail closed, permit reviewed repair of partial imports.
- A provider page/redirect changes origin or query scope: refuse credential leakage and incomplete results.
- Cancelling a modal or shutting down during browser/network/clone work: invalidate messages, drain I/O, preserve recovery.

## Task 1: Git ref storage and recovery

**Files:** `internal/gitcli/ref_storage.go`, `ref_storage_test.go`; `client.go`; `internal/app/repo.go`, `service.go`; `internal/operation/store.go`; relevant tests.

**Interfaces:** Keep the existing `app.Git` interface. Optional clone-planning capability produces a chosen `files`/`reftable` backend before clone, and accepts that backend for clone/resume. Store nonsecret chosen backend in each repo-add journal; expose inspection through `gitcli.ReferenceFormat`. Exact optional interface names may be settled by this task and reported before later tasks integrate it.

- [x] Add failing local-remote regressions for case-only heads/tags, `Group/one` versus `group/two`, prefix conflicts, and later sync; assert exact enumerated refs and distinct OIDs.
- [x] Run new tests and record failing evidence on files storage.
- [x] Inspect advertised refs and actual destination filesystem, select/probe backend before clone, persist selection, and verify initialized tracking names. Validate all remotes before fetch; preserve normal safe fetch mappings.
- [x] Preserve identity/worktree checks on resume; migrate only an owned unlinked repository using reflog-preserving Git, verify before/after state, and refuse unsupported migration with upgrade/resume guidance. Add an explicit CLI ref-format selection and inspection output if needed through later integration.
- [x] Run `go test ./internal/gitcli ./internal/app ./internal/operation`; record platform/capability skips and supported migration refusal. Task-scoped review before auth implementation.

## Task 2: Secure shared authentication core

**Files:** new `internal/auth/{model,store,secrets,login,browser,resolve,status,git}.go` with platform-specific secret storage; new `internal/domain/credentials.go`; auth tests; `go.mod`, `go.sum` only if needed.

**Interfaces:** `domain.CredentialRequest` describes provider, host/API origin, repository, connection and token-env selection. `domain.Credential` carries nonserializable username/secret/scheme and an opaque identity key. `domain.CredentialResolver` resolves credentials by context and request. `auth.Manager` provides saved `Connection` listing, `Login`, `Status`, `Logout`, connection lookup and resolver access. Public constructors perform no network, registry writes, or secret-store reads. Export typed requests/results and browser/prompt hooks for CLI/TUI. Final exact signatures are published in the task report and consumed by Tasks 3-6.

- [x] Add failing tests with temporary registries/fake native stores/TLS fixtures covering secure persistence, root-independent login, account/grant references and explicit-source precedence.
- [x] Implement an atomic versioned nonsecret registry with interprocess lock; native keyring adapter, with a hard error on unavailable secure storage and no plaintext fallback.
- [x] Implement guided token verification and scheme-specific credentials for GitHub, Bitbucket Cloud, and Server/DC. Legacy public env credentials preserve current precedence and Bearer contract.
- [x] Implement browser/device authorization with external application configuration, state/PKCE/callback checks, deadlines and cancellation; support refresh rotation and cross-process serialization. If a host lacks suitable OAuth configuration, direct token setup is a supported functional path.
- [x] Implement local and live status without claiming anonymous reachability proves identity; logout is local and targets one connection. Define scoped helper resolution that fails closed on disconnected/replaced explicit bindings.
- [x] Run `go test ./internal/auth ./internal/domain`, native Windows tests and cross-platform compile checks for adapters. Review core before discovery depends on it.

## Task 3: Auth CLI, provider integration, and Git helper

**Files:** `internal/cli/auth*.go`, `root.go`, `commands.go`, `dashboard.go`; `internal/github/{client,resolve,http}.go`, Bitbucket equivalents; `internal/pullrequests/provider.go`; `internal/gitcli` credential adapter; domain query metadata and associated tests.

**Interfaces:** Existing provider constructors keep environment-only defaults; add constructors accepting `domain.CredentialResolver`. Propagate a `connection` provider-metadata reference through PR queries. CLI production wiring constructs one shared manager/resolver, reused by TUI and provider clients. Repository-scoped helper routing carries only nonsecret connection references.

- [x] Add failing CLI tests for root-independent `auth login --provider github|bitbucket`, `status --repo NAME|--local`, `logout --connection NAME`, noninteractive token stdin, and machine-output redaction.
- [x] Add prompts with hidden token input, host/provider validation, browser opening fallback and named connections. Implement status grouping and independent API/Git checks, keeping invalid/forbidden/offline/cancelled states distinct.
- [x] Add resolver-aware provider requests with correct Basic/Bearer behavior and credential-aware cache identity, preserving exact-origin restrictions and explicit unset-env errors.
- [x] Implement invocation-scoped cloning credentials and persistent remote-scoped helper integration with stable executable path; preserve unrelated Git settings. An explicit disconnected binding stops helper fallback to another account.
- [x] Support reviewed associations to registered remotes under the root lock, without requiring auth-only operations to mutate roots.
- [x] Run `go test ./internal/cli ./internal/github ./internal/bitbucket ./internal/pullrequests ./internal/gitcli` and scoped review.

## Task 4: Provider repository discovery

**Files:** `internal/domain/repository_discovery.go`; `internal/github/repositories.go`; `internal/bitbucket/repositories.go`; new `internal/discovery/provider.go`; HTTP fixture tests.

**Interfaces:** `domain.RepositorySource` holds provider, host/API, connection/token-env and optional scope. `domain.RemoteRepository` holds stable provider identity, namespace/name/description/default branch, visibility, and validated HTTPS/SSH clone URLs. Discovery provider exposes `ListRepositories(context.Context, RepositorySource) ([]RemoteRepository,error)` and Bitbucket workspace choices. Use Task 2 resolver and existing guarded HTTP transport.

- [x] Add failing multipage fixtures for GitHub `/user/repos`, Bitbucket Cloud workspace/repository pages, and Server/DC `/repos` with `nextPageStart`.
- [x] Implement listing, scope filters, bounded safe pagination, cloned link validation, context paths and SSH ports. Never represent partial results as a complete successful list.
- [x] Cover email/API-token auth, legacy Bearer, unsafe pages/redirects, cooldowns, oversized replies and cancellation in tests.
- [x] Run `go test ./internal/github ./internal/bitbucket ./internal/discovery`; scoped review.

## Task 5: Previewed recoverable batch import

**Files:** `internal/app/import*.go`, `service.go`, `operations.go`, `dashboard.go`; `internal/operation/store.go`; domain/provider metadata validation; app/journal tests.

**Interfaces:** `app.ImportRequest` carries selected remote repositories/local IDs, connection identity, `DryRun`, and `ExpectedPlan`; `Service.ImportRepositories(context.Context, ImportRequest) (Result,error)`. `Service.RepositoryImportChoices` combines discovery observations with active/retired catalog identity state. Journal steps carry immutable repository remote/provider/connection metadata, chosen backend and durable state. Use optional clone planner from Task 1.

- [x] Add failing integration tests for stale preview, portable-ID conflicts, equivalent HTTPS/SSH duplicates, retired identities, partial success and retry.
- [x] Generate deterministic IDs with editable conflicts; bind preview to root/revision/selection/connection identity. Revalidate under lock before journal/Git effects.
- [x] Execute one sequential batch journal, continue independent clone failures, serialize catalog persistence and stop on shared durability errors/cancellation. Register provider metadata and scoped Git routing per success.
- [x] Resume verifies successes without cloning, preserves existing failed directories and processes unfinished steps. Credential repair uses the same operation's reviewed reassociation; never bypass root-lock safety or select another account implicitly.
- [x] Preserve old journal/catalog readers in the new binary, validate additive fields, and run `go test ./internal/app ./internal/operation ./internal/config` plus scoped review.

## Task 6: TUI import and shared login flow

**Files:** `internal/tui/dashboard_import*.go`, `dashboard.go`, `dashboard_view.go`, `dashboard_layout.go`; `internal/cli/dashboard.go`; existing help/empty-state views and TUI tests.

**Interfaces:** A separate `DashboardImporter` capability supplies connections/login/discovery, preview/import, and per-operation observers. Use Task 5 request/result shapes, Task 2 typed prompt/browser hooks, and existing reader cancellation/progress patterns. Keep existing `a` registered-to-workspace picker.

- [x] Add failing model tests for `i` on empty roots, connection/source form and shared login, filter-stable selections, per-item ID editing, disabled existing/retired rows, review-only Enter and explicit `y` confirmation.
- [x] Implement a distinct import state machine with source/connection selection, asynchronous login/discovery, picking/review/execution/results. Load only on explicit action; display API and Git auth failures independently.
- [x] Integrate catalog/workspace refresh, result toggle, cancellation/draining and stale reply invalidation; show recovery commands after partial imports.
- [x] Cover progress, narrow/monochrome output, browser-unavailable guided-token fallback, and quit/cancel during I/O.
- [x] Run `go test ./internal/tui ./internal/cli`; scoped review.

## Task 7: Documentation, complete validation, and integration

**Files:** `docs/onboarding.md`, implementation progress/plan ledger, command help; preserve original README edits when transferring changes.

- [x] Document ref storage/recovery, auth configuration and both provider login methods, secure-store prerequisites, scoped Git/GoLand behavior, discovery and partial batch recovery; explain OAuth registrations as release/runtime configuration.
- [x] Format changed Go files; pass `go test ./...`, `go vet ./...`, Windows ref/auth regressions, and Linux/macOS source cross-builds. Record attempted race/lint checks and native-platform limits below.
- [x] Build CLI and smoke-test auth/login help, local status, token fallback help, repo ref-format help and empty-token safety without real credentials.
- [x] Obtain whole-change review for security boundaries, recovery and TUI lifecycle. Fix actionable findings and rerun affected checks.
- [x] Transfer reviewed implementation changes to the original checkout without replacing user README/desktop discussion changes; verify root diff and final relevant checks. Report actual limitations, especially external provider registrations and private-host validation.

## Final local validation — 2026-11-16

The complete suite passed after the integration fixes. Focused app/CLI tests also passed after the final additive repair-preview field. The active checkout passes `go vet ./...`, builds `bin/bearforge.exe`, and passes isolated command/help, read-only auth-status and empty-token checks. Changed Go files pass formatting and Git whitespace checks. Linux and macOS amd64 source cross-builds with cgo disabled pass, as does Windows release snapshot packaging. The scoped final rereview accepted all four integration fixes: synthetic default-ref protection, native macOS release configuration, reviewable grant repair, and anonymous GitHub owner discovery.

All 94 transferred implementation/documentation files matched the reviewed worktree after newline normalization. Original README and desktop-discussion edits retained their pre-handoff hashes. The subsequent commit request restores the README and removes the untracked desktop discussion before committing the implementation locally. The isolated feature branch retains the original implementation commits. No tags, pushes, production publication, or real provider/keyring changes were performed.

Race execution is unavailable locally because cgo is disabled and no C compiler is installed. The available golangci-lint 2.12.2 tooling cannot analyze Go 1.27 export data, including after rebuilding it with the project toolchain; its incomplete analysis is not a lint pass. `go vet` passed independently. Native macOS cgo/release builds, both Darwin architectures and Keychain runtime need macOS CI; source cross-compilation does not establish them. Linux Secret Service runtime, Unix terminal behavior, older/newer Git migration matrices, real OAuth registrations/private hosts, and GoLand interoperability remain environment-specific validation. Disposable fixtures cover the implemented contracts without asserting those external results.
