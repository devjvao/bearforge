# Read-only Workspace Dashboard Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Start Phase 2 with `bearforge tui [WORKSPACE]`, a read-only, full-window workspace dashboard.

**Architecture:** Cobra constructs the existing application service and hands a narrow read-only source to Bubble Tea. A typed application snapshot lists workspaces, observes only the selected workspace through Status, and reports unfinished journals. The terminal adapter owns navigation, filtering, request cancellation and rendering.

**Tech Stack:** Go 1.26, Cobra, Bubble Tea v2, existing Git/config/journal adapters and ANSI display-width utilities.

**Spec:** ../../architecture/0001-bearforge-blueprint.md, sections 8, 10 and 11; this milestone narrows the dashboard to inspection.

## Global Constraints

- Git 2.40+, existing root selection and application safety rules.
- No writes, fetches, provisioning, removals, recovery execution, PR lookup or background polling.
- Require suitable terminal streams and human output; reject JSON/JSONL, quiet, no-input, CI and TERM=dumb.
- Alternate screen restored on exit. All I/O runs in commands outside Update/View.
- Superseded reads are cancelled; request IDs reject stale replies. Exit waits for outstanding reads.
- Untrusted text is sanitized and clipped by display width. Text labels work without color.

## Review Focus

- Late replies after rapid workspace switching must not replace the current workspace.
- Partial/unreadable status must keep readable rows and show actionable diagnostics.
- Missing/empty roots and invalid workspace names must not create metadata.
- Terminal escape sequences, control characters and long Unicode text must not escape layout bounds.
- Quit and parent cancellation must cancel and drain active readers before returning.

## Task 1: Application snapshot

Files: internal/app/dashboard.go, internal/app/dashboard_test.go.

Interface: Service.Dashboard(context.Context, string) (DashboardSnapshot, error), with root, sorted workspace names, selected workspace, status items, unfinished operation summaries and observation problems. Empty selection picks the first workspace; an explicit missing workspace returns not_found. Only the selected workspace is inspected. Partial status and journal errors are visible without hiding usable results.

- [x] Write integration tests for selection, partial observations, pending journals, cancellation and no filesystem/Git changes.
- [x] Run `go test ./internal/app -run Dashboard` and observe missing-feature failure.
- [x] Implement the snapshot using existing store, Status and Journal.List.
- [x] Run `go test ./internal/app` and require success.

## Task 2: Terminal dashboard

Files: internal/tui/dashboard.go, internal/tui/dashboard_view.go, internal/tui/dashboard_test.go.

Interface: DashboardSource exposes Dashboard(context.Context, string) (app.DashboardSnapshot, error); RunDashboard(context.Context, io.Reader, io.Writer, DashboardSource, string) error.

Layout: workspace navigation, repository health list, selected repository details and unfinished-operation notice. Tab/shift-tab change focus; arrows/j/k move; / filters the focused list; Enter accepts a filter, Escape clears it; r refreshes local observations; q quits, Ctrl-C cancels. Detail scrolling uses page keys. Missing upstream, unknown ahead/behind, detached/unborn HEAD, mapping drift and unreadable worktrees have explicit labels. Empty workspaces have a clear empty state. Small terminals use a compact layout.

- [x] Write model tests for asynchronous requests, stale replies, refresh, selection/filtering, details, resize, sanitization and cancellation/draining.
- [x] Run `go test ./internal/tui` and observe missing-feature failure.
- [x] Implement Bubble Tea commands with cancellable request contexts and bounded presentation state; reuse pinned ANSI utilities, adding no new dependency versions.
- [x] Run `go test ./internal/tui` and require success.

## Task 3: CLI and documentation

Files: internal/cli/dashboard.go, internal/cli/commands.go, internal/cli/root_test.go, README.md, docs/development/dashboard-progress.md.

Interface: tui [WORKSPACE], at most one workspace, existing root discovery, existing exit codes. Validate terminal/output policy before constructing Git or touching the root. Preflight an explicit workspace definition through the validated store before taking over the terminal; status observations remain asynchronous. Keep no-argument help and inline progress behavior.

- [x] Add CLI tests for command help, argument count and unsuitable flags/streams, with no root creation or terminal control output.
- [x] Run `go test ./internal/cli -run 'TUI|Help'` and observe missing-command failure.
- [x] Wire the dashboard source and runner; document controls and local snapshot freshness.
- [x] Run `go test ./...`, `go vet ./...`, build and smoke-test help/error behavior. Independently review the whole change.

## Execution rulings

- The approved blueprint and request to start Phase 2 authorize implementation, consistent with the local-core plan's execution ruling; there is no additional design approval gate.
- Work in the supplied dedicated checkout on a feature branch. Initial implementation preserves changes for review; the subsequent maintainer request authorizes committing, pushing, opening a PR to main and following CI through fixes.
- Implement directly in this session and use one independent final review. Baseline and final verification are recorded in the dashboard ledger.
