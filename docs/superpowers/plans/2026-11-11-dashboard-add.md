# Dashboard Repository Picker and Add Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Select registered repositories in the TUI, preview their resolved worktree plan, confirm it, and retain per-repository results.

**Architecture:** A read-only application query supplies catalog choices and current membership. AddRepositories retains its existing execution/journal path and gains an optional ExpectedPlan digest checked after replanning under its mutation lock. A narrow optional dashboard adapter supplies that query and operation-local progress. The TUI owns picker, preview, confirmation and results states, using the existing lifetime gate and bounded progress bridge.

**Tech Stack:** Go 1.26, existing Bubble Tea v2/Lip Gloss v2, existing Git/config/journal adapters; no new dependencies.

**Spec:** Maintainer request to continue Phase 2; blueprint sections 8, 11 and 16; existing add service defaults and recovery rules.

## Global constraints

- a opens Add repositories for the loaded current workspace. Use its existing default branch and the service's repository/base/tracking defaults.
- Sorted registered repositories are filterable and selectable with Space; existing workspace members remain visible and disabled. Filters never discard selections.
- Enter requests a read-only preview. Show every selected repository, destination path, branch creation/reuse, base, starting OID and tracking policy in scrollable content.
- Only y on a successful preview confirms execution. Enter, redraw, resize, stale replies, repeat keys and failed previews cannot execute.
- Bind approval to root, workspace, catalog/workspace revisions and resolved Git steps. Refuse drift before journal or Git writes; CLI calls without ExpectedPlan retain their existing behavior.
- Modal navigation captures workspace and selection. Add and sync cannot overlap; all query/preview/execution I/O shares the existing cancellation/draining gate.
- Retain partial results, errors and operation inspection/resume commands after refresh; never automatically retry or resume a mutation.
- Preserve Lip Gloss, NO_COLOR, terminal sanitization, minimum 36x12 and fully reachable details.
- Preserve prior uncommitted work in the supplied checkout; leave all changes uncommitted and do not publish.

## Review focus

- Changed base refs, definitions, root or workspace cannot reuse an earlier approval.
- Filtered selections, disabled members and empty/loading/error catalogs must never provision unintended repositories.
- Duplicate confirmation, late replies and rapid reopen/close must not start overlapping work.
- Cancellation after Git success must retain journal recovery information and drain before terminal restoration.
- Long IDs, paths, OIDs and diagnostics must remain safe and reachable at 36x12 in color and monochrome.

## Task 1: Application query and approval-bound add plans

Files: internal/app/repository_choices.go, internal/app/repository_choices_test.go, internal/app/workspace.go, internal/app/add_plan.go, internal/app/add_plan_test.go, internal/app/service.go.

Interfaces: Service.RepositoryChoices(context.Context, string) (RepositoryChoices, error); RepositoryChoices contains Workspace, DefaultBranch and sorted Items with Repository, Base and InWorkspace. AddRequest gains ExpectedPlan; Result.PlanDigest identifies the preview. Item gains optional Base, Start, Tracking and CreateBranch for concrete preview/result presentation. AddRepositories loads revisions once, replans and compares a digest under its existing mutation lock before effects.

- [x] Write real-Git tests for read-only choices/previews, resolved values, approved execution and drift refusal with no effects; observe RED.
- [x] Implement the query, typed preview metadata and digest using the existing add planner/executor.
- [x] Run application tests and verify GREEN.

## Task 2: Picker, preview, confirmation and results

Files: internal/tui/dashboard_add.go, internal/tui/dashboard_add_view.go, internal/tui/dashboard_add_test.go, internal/tui/dashboard_progress.go, internal/tui/dashboard.go, internal/tui/dashboard_sync.go, internal/tui/dashboard_layout.go, internal/tui/dashboard_styles.go.

Interfaces: optional DashboardAdder exposes RepositoryChoices(ctx, workspace) and AddRepositories(ctx, app.AddRequest, observe). Reuse a shared bounded dashboardProgress bridge for sync and add; keep separate operation identities/messages. Modal stages cover catalog loading, selection, preview loading/review, execution and results. Esc backs out safely before execution or closes retained results; q/Ctrl-C use existing stop/drain behavior.

- [x] Write model tests for selection across filters, disabled members, empty selection, exact preview/approval requests, explicit y confirmation, stale replies, duplicate prevention, per-repository partial results and lifecycle cancellation; observe RED.
- [x] Implement the flow and responsive Lip Gloss presentation without I/O in Update/View.
- [x] Verify full diagnostics, paths and recovery commands remain reachable at minimum dimensions.
- [x] Run all TUI tests and verify GREEN.

## Task 3: CLI adapter, documentation and end-to-end validation

Files: internal/cli/dashboard.go, internal/cli/dashboard_test.go, README.md, docs/development/dashboard-add-progress.md.

- [x] Add real-service adapter coverage for preview/no writes, explicit selection, execution, stale approval and observer isolation; observe RED for the missing adapter.
- [x] Wire the existing service through an operation-local observer and document all modal controls/defaults.
- [x] Run full tests, vet, Staticcheck, formatting, module consistency and native/cross builds. Attempt race checks and report toolchain limits.
- [x] Exercise preview, back/cancel, confirmation and results in a disposable native-terminal fixture; inspect rendered picker/preview/results in color and monochrome.
- [x] Obtain one independent final review and reproduce/fix material findings.
