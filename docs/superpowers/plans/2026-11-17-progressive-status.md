# Progressive workspace status implementation plan

**Goal:** Bound repository inspection and display completed dashboard rows while other repositories are still loading; measure first useful result and total latency with real Git workspaces.

**Architecture:** Reuse catalog `defaults.jobs` (default 4, range 1–64). Keep `Status` and `Dashboard` compatible, adding callback variants whose callbacks run serially on the caller. Status returns sorted results regardless of completion order. Dashboard publishes immutable snapshots with pending rows, then completed rows. Bubble Tea consumes a cancellable stream and ignores superseded request IDs.

**Tech stack:** Go, existing Git CLI adapter, Bubble Tea; no new dependencies.

## Tasks

- [x] Add controlled concurrency tests for the status bound, early results, ordering, partial errors, cancellation, and empty workspaces. Implement the bounded read pipeline in `internal/app/status.go`.
- [x] Add immutable progressive snapshots in `internal/app/dashboard.go`, preserving journal problems and existing synchronous behavior.
- [x] Add a cancellable dashboard stream, incremental row updates and progress counts in `internal/tui`; test refresh, workspace switching, stale results and shutdown.
- [x] Benchmark real mixed-state Git worktrees at 12 and 60 repositories with jobs 1 and 4. Record repeat samples, fixture composition, first useful result, total time and reproducible command in development documentation.
- [x] Run focused tests, full suite, race detector and independent diff review. Resolve failures and report measurement limitations.

## Verification

Full race tests (`CGO_ENABLED=1`, `go test -race -json ./... -count=1 -timeout=20m`), `go vet ./...`, build, formatting and diff checks passed. Independent review found no further issues. Both historical and bounded five-sample benchmark runs passed; results and limits are in [status performance](../../development/status-performance.md).

Installed WinLibs GCC 16.2.0 / MinGW-w64 14.0.0 per user through winget and verified `libsynchronization.a`. The complete Windows race run passed: 15 packages, 1,179 test/subtest passes, zero failures or race reports. Five existing checks skipped because symlink privileges or shell adapters were unavailable, or because the scale test is opt-in. Compiler setup and reproduction are documented in the development guide. Existing Linux race CI provides separate platform coverage.

Review correction: both cancellation exits after row initialization now use the same finalizer as drained inspections. Regression tests reproduced and fixed successful/inspecting results after cancellation during definition loading or the initial dashboard callback, including empty workspaces and immutable initial snapshots.

## Review focus

- Cancellation must stop queued inspections and join active workers before returning.
- A slow or unreadable repository must not hold back another completed row.
- Saved progress snapshots must not change after callbacks return.
- Refresh and workspace switching must retain valid selection and reject old results.
- CLI result order, outcomes, error aggregation and read-only behavior must remain compatible.
