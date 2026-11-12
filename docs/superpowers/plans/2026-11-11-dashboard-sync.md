# Dashboard Sync Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Add an explicit workspace sync action with live progress and inspectable per-repository results.

**Architecture:** Keep dashboard observation separate from an optional sync capability. The CLI adapter invokes the existing Service.Sync with configured jobs and an operation-local observer. The dashboard serializes reads and sync, tracks command lifetimes, and refreshes local observations after completion.

**Tech Stack:** Go 1.26, Bubble Tea v2, existing application sync service and ANSI utilities; no new dependencies.

**Spec:** ../../architecture/0001-bearforge-blueprint.md sections 8, 10, 11; maintainer request to continue Phase 2 with explicit sync, progress and repository results.

## Global Constraints

- Browsing and r refresh remain local; s explicitly fetches/prunes workspace repositories.
- Sync every unique repository in the loaded workspace, including repositories hidden by filters; never pass an empty selection to Service.Sync, where it means the entire catalog.
- Reuse existing locking, fetch configuration checks, cancellation and independent-failure semantics.
- I/O runs outside Update/View. Quitting cancels and drains reads and sync, including delayed commands.
- One active sync; progress and completion carry operation identity. Navigation cannot retarget active sync or overwrite results.
- All external display text is sanitized and wrapped; results remain reachable at 36x12.
- Implement in the supplied checkout, leave uncommitted for review, and do not publish.

## Review Focus

- Empty, loading or failed observations must never trigger catalog-wide sync.
- Filters and workspace navigation must not change the captured sync scope.
- Slow fetches, rapid repeated keys, delayed events and quit must not leak work or launch overlapping syncs.
- Partial failures and preflight errors must remain visible after automatic refresh.
- Long diagnostics, Unicode and terminal control text must remain safe and fully reachable.

## Task 1: Sync orchestration and lifecycle

Files: internal/tui/dashboard.go, internal/tui/dashboard_sync.go, internal/tui/dashboard_sync_test.go.

Interfaces: optional DashboardSyncer.Sync(context.Context, []string, func(app.Event)) (app.Result, error); existing DashboardSource remains read-only. Source I/O shares the dashboard reader's lifetime gate. s captures a deduplicated workspace selection; operation-scoped event messages feed progress, completion retains authoritative Result and requests local refresh.

- [x] Write tests proving explicit scope, no implicit/empty sync, progress, partial results, duplicate prevention, stale-message rejection and cancellation/draining.
- [x] Run go test ./internal/tui -run Sync and observe missing-feature failures.
- [x] Implement orchestration and lifetime gating, with a bounded, coalescing progress handoff.
- [x] Run go test ./internal/tui and require success.

## Task 2: Presentation and CLI adapter

Files: internal/tui/dashboard_view.go, internal/tui/dashboard_sync_test.go, internal/cli/dashboard.go, internal/cli/dashboard_test.go, internal/cli/root_test.go, README.md.

Interfaces: CLI dashboardService adapter embeds *app.Service and exposes operation-local Sync with configured jobs; RunDashboard receives this adapter through DashboardSource. s is documented as workspace sync. Progress counts are always visible; per-repository states and problems appear in scrollable sync results, with v returning to repository details. Sync result scope is labeled independently of current navigation.

- [x] Add failing presentation and real-service adapter tests, covering filtered repositories, partial results, safety refusal, refresh, full diagnostics and scope isolation.
- [x] Implement progress/results rendering and wire the existing sync service through a copied Service with a local observer.
- [x] Update help and README controls/scope.
- [x] Run go test ./..., go vet ./..., build, formatting and git diff --check. Attempt race tests and report toolchain limits.
- [x] Obtain one independent final review; reproduce and fix material findings with regression tests.

## Execution rulings

The blueprint, prior Phase 2 execution rulings and request to continue authorize this implementation without another approval gate. The supplied dedicated-directory preference continues; no worktree creation is needed. The existing checkout is on main, so changes remain uncommitted there for the user's review.
