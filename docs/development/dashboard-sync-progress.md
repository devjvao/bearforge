# Implementation ledger - dashboard sync

Plan: ../superpowers/plans/2026-11-11-dashboard-sync.md

Scope: explicit workspace sync in the Phase 2 dashboard, reusing Service.Sync.

Ruling: s syncs all unique repositories in the loaded workspace, irrespective of filters, because dashboard context is the workspace; choosing repository-only sync would require another action. Cost if wrong: adjust the action scope and controls.

Ruling: retain the supplied dedicated checkout and leave changes uncommitted for review, consistent with the previous Phase 2 execution preference. Do not push or publish.

Preflight: DashboardSource remains read-only; optional DashboardSyncer owns mutation capability. The CLI adapter uses a copy of Service so an operation's observer does not mutate a shared service. No dependency changes are required.

## Progress

- Baseline go test ./... passed before changes.
- Task 1 complete: explicit s action captures unique workspace repositories irrespective of filters, rejects unavailable/empty scope, prevents duplicate operations, tracks identity, refreshes on completion, and retains authoritative results. New behavior tests failed before implementation and pass afterward.
- Task 2 complete: live counts and per-repository states, scrollable safe diagnostics and v details/results toggle. CLI forwards configured jobs to the existing service with an operation-local observer. Help and README describe scope and controls.
- Real-Git adapter coverage verifies successful fetch alongside an unsafe-refspec failure, preservation of checkout HEAD/files, exclusion of catalog repositories outside the workspace, jobs validation and unfinished-operation refusal. The initial fixture incorrectly reused one remote for multiple registrations; distinct local remotes fixed the fixture without changing application policy.

## Independent review

- One independent read-only review identified blocking progress delivery and terminal restoration before sync finalization. Both were reproduced by tests before fixes.
- Final: fixed blocking progress delivery - TestDashboardSyncFinishesWithoutProgressConsumer RED -> GREEN. The bridge now coalesces one state per selected repository with a single nonblocking notification; terminal states are retained and final Result remains authoritative.
- Final: fixed early terminal restoration - TestDashboardSyncKeepsStoppingViewUntilCleanupFinishes RED -> GREEN. Quit cancels I/O and displays stopping until the gated service work drains. Parent cancellation and presentation use separate lifetimes; renderer failures retain deferred cleanup.
- Final: Ruling: treat clipped persistent counts as a material usability issue because progress must remain visible while scrolling at the supported 36x12 minimum. Put compact counts before workspace identity; keep full scope in scrollable details. Cost if wrong: a header formatting adjustment. TestDashboardSyncLongWorkspaceKeepsProgressCountsVisible RED -> GREEN.
- Final: Ruling: existing service timing, last-success metadata, timeout and retry policy remain outside this explicit-action slice. Reuse the existing service contract unchanged; additional telemetry/policy needs its own service design. Cost if wrong: those enhancements remain follow-up work.
- No deferred review findings within this slice.

## Verification

- Final go test ./..., go vet ./..., Windows executable build, Staticcheck (golangci-lint), goimports checks on changed files and git diff --check passed.
- Package-wide goimports diff reported only CRLF normalization in unrelated existing files; changed-file import checks passed without edits to unrelated files.
- go test -race ./internal/tui ./internal/cli could not run: the toolchain has CGO disabled and reports that -race requires CGO. No race-instrumented result is claimed.
- Native Windows PTY with a new disposable local-Git fixture: s showed queued/running/synced and refreshed observations; v returned to repository details. Remote tracking ref advanced while checkout HEAD remained unchanged.
- Rebuilt executable smoke: compact progress counts and retained results verified; q displayed stopping, restored the alternate screen/cursor and returned 0. Ctrl-C displayed stopping, restored the terminal and returned 130. No smoke-test process remains active.
- Harness TERM=dumb correctly refused initial PTY attempts. Setting a capable TERM for the isolated smoke-test process enabled terminal interaction; terminal policy was unchanged.
- Changes remain uncommitted in the supplied checkout for review.
