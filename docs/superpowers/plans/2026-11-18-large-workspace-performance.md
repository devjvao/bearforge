# Large workspace performance implementation plan

**Goal.** Shorten the wait for a usable dashboard, a completed import, the first fetch, and workspace PR results. Keep repository identity checks, recoverable operations, and deterministic results.

**Starting point.** At `9fa2c94`, status and cloning already use bounded workers, and the dashboard already shows completed inspections. The [status benchmark](../../development/status-performance.md) recorded 2.940 seconds total and 203 ms to the first successful callback for 60 members with four jobs. Those historical measurements exclude terminal rendering. The five changes below are proposals. Their speedups have yet to be measured.

**Delivery order.** Establish a baseline before each change. Start with 1 and 2, which can proceed independently and have an existing workload. Apply 3 after 2 so journal and rendering costs can be measured separately. Task 4 can proceed independently. Apply 5 after 1 so the effect of concurrency can be distinguished from fewer Git launches. Sync and PR inspection have different failure contracts and can ship separately.

## Measurement and acceptance

- [ ] Compare the immediate parent and candidate using the same benchmark harness, fixtures, machine, Go/Git versions and job counts. Record both commit IDs. Exclude setup, alternate runs, and report at least five samples with medians and spread. The existing status benchmark explicitly warms its fixture. Measure first use separately by starting a fresh process against a prepared fixture without a warm-up scan. Record the OS cache conditions. Use jobs 1 and 4 for comparisons and exercise the supported bounds 1 and 64 in correctness tests.
- [ ] Use 60 and 200 real local repositories, with 1,000 as an opt-in scale run. Extend the existing mixed-state fixture to handle incomplete groups of six. Use deterministic 60/200/1,000-row fixtures for renderer timings. Include the 12-member control to catch small-workspace regressions. Keep heavy runs opt-in and use existing CI for correctness.
- [ ] Report wall time alongside the count of the work removed. Use Go benchmark allocation reporting for Go code and separate CPU profiles when investigating CPU time. Git subprocess CPU is outside the Go profile. Distinguish first successful Git callback, first rendered completed row, and final refresh. For terminal latency, capture a Bubble Tea frame from a fixed-size terminal or test writer and state which boundary was measured.
- [ ] For each proposed change, require matching results plus a repeatable improvement in its target metric. Investigate regressions beyond baseline variability. Record raw samples and commands alongside the existing performance report when implementing. Run the affected packages, then `go test -race ./...`, `go vet ./...` and the existing opt-in `TestPhase2Scale60` before integrating.

## 1. Reduce Git launches during repository inspection

Results and reproduction commands are in the [Git inspection report](../../development/git-inspection-performance.md).

**Where.** `internal/gitcli/worktree.go` (`VerifyCommon`), `internal/gitcli/client.go` (`VerifyBare`, `Config`), `internal/app/service.go` (`verifyRepositoryAt`). Verify with `internal/gitcli/git_test.go`, `internal/app/status_test.go` and `internal/app/status_benchmark_test.go`.

- [x] Count child launches by command in the benchmark outside the timed runs. A healthy one-remote repository ordinarily needs five launches: bare check, remote URL, common directory, worktree root and status. Count missing worktrees and the optional upstream probe separately.
- [x] Request both absolute paths in one `rev-parse --path-format=absolute --git-common-dir --show-toplevel` call. Preserve both filesystem identity comparisons. Use separate probes for ambiguous output, including legal Unix paths containing newlines, and for failures where the existing diagnostic would differ.
- [x] For multiple catalog remotes, read effective remote URL values with one bounded, NUL-delimited Git config query. Preserve exact remote-name matching, the effective value for duplicate keys, missing values and config includes. Retain the one-key path for one remote, and keep worktree-specific PR config reads in the worktree context.
- [x] Test linked, nested, missing and foreign worktrees and multiple remotes with one mismatched URL. Compare final ordered items and partial errors with the baseline. The normal one-remote case should fall from five launches to four. Measure total and first-callback time separately with `go test ./internal/app -run '^$' -bench '^BenchmarkStatusWorkspace$' -benchtime=1x -count=5 -timeout=20m` for the existing fixture sizes.

## 2. Update one dashboard row per completion

Results and reproduction commands are in the [dashboard refresh report](../../development/dashboard-refresh-performance.md).

**Where.** `internal/app/dashboard.go`, `internal/tui/dashboard_stream.go`, `dashboard.go`, `dashboard_layout.go`, `dashboard_view.go` and `list_view.go` in the same TUI package. Extend the dashboard, stream, and layout tests.

- [x] Add an optional service method, `DashboardWithUpdates(ctx, workspace, onStart, onItem)`, returning the final `DashboardSnapshot` and error. Give the TUI an initial snapshot with its own item slice, followed by one completed `Item` per callback. Derive existing whole-snapshot and batch methods from the same observation path. Keep independent snapshots available to existing callers.
- [x] In the TUI, prefer row updates and keep both existing source fallbacks. Build a repository-ID-to-index map at the initial snapshot. Apply each item on the model's update path, count each transition out of inspecting once, and reconcile the final snapshot with its aggregate problems and observation time. Preserve request IDs, cancellable backpressure, and reader shutdown.
- [x] Cache matching row indices when the repository list or name filter changes. A status update cannot change the repository name. Preserve selection by ID, calculate the visible page using the existing pane geometry, and format only that page. Maintain the header's completed, clean, changed and attention counts from row transitions. Use the same selected item for actions and details.
- [x] Use a blocked inspection to prove the first completed row renders while another is pending. Exercise filtering and navigation during updates, resizing across layouts, selection on later pages, empty results, partial failures, stale responses and cancellation during delivery. Compare displayed text and selected actions with current behavior.
- [x] Benchmark a whole refresh and single-row update plus view at a fixed terminal size. Count copied items, formatted rows and allocated bytes. Expected shape: one initial and one final full snapshot on the TUI path, constant row payload per completion, and formatting bounded by visible height. Measure first frame and total time separately.

## 3. Keep completed journals out of repeated dashboard decoding

Results and reproduction commands are in the [journal history report](../../development/journal-performance.md).

**Where.** `internal/operation/store.go` (`List`, `Load`), `internal/app/dashboard.go`, `internal/operation/store_test.go` and `internal/app/dashboard_test.go`.

- [x] Benchmark a root with 0, 100 and 1,000 completed or abandoned operations, a few pending operations across workspaces, and one corrupt file. Include large multi-step journals. Separate directory enumeration, bytes read, full decodes and time to initial dashboard rows.
- [x] Add an optional `PendingSummaries(context.Context) ([]app.OperationSummary, error)` capability to the concrete journal. Use it for dashboard reads, with the current full-list path as the fallback. Keep `List` and `Load` authoritative for history, doctor and recovery. Return every readable unfinished operation in ID order alongside any errors.
- [x] Start with a bounded, process-local index of strictly validated terminal journals, keyed by operation ID, root and content digest. Enumerate canonical paths on every query. Read the file once under the existing size and path checks, compare its digest, and skip decoding on an exact terminal hit. Decode and validate those same bytes on a miss. Recheck pending files every time. Never cache failures. Publish entries safely across concurrent readers and bound the retained entries (proposed initial capacity 2,048 per store).
- [x] Test cold open on a legacy root, repeated reads, eviction, deletion, concurrent replacement, a same-size edit with a preserved timestamp, and a terminal file replaced with an invalid or pending one. Preserve partial diagnostics and read-only startup even on an empty root. A restart loses only the optimization.
- [x] Report the cold scan and warm cache hit rate. This design still reads and hashes historical bytes. Ship it if eliminating full JSON validation materially improves repeat refreshes. If file reads dominate, use the recorded breakdown to evaluate a writer-maintained index separately, including legacy writers and crash recovery, before changing the storage format.

## 4. Reduce catalog work per completed import

**Where.** `internal/config/store.go` (`LoadCatalog`, `SaveCatalog`, `save`, `mergeComments`), `internal/app/import_execute.go` (admission and registration). Use `internal/config/store_test.go`, `internal/app/import_parallel_test.go` and `internal/app/import_execute_test.go`.

- [ ] Time a single catalog save at 60, 200 and 1,000 entries with retired identities, comments and extensions. Time a batch of small local imports separately. Count catalog reads, YAML decodes, durable writes, and the delay from clone completion to the success event.
- [ ] Index each new YAML mapping by key once in `mergeComments`, then walk the old mapping in order. Preserve key and value comments, entry order and the existing treatment of extensions. Assert these properties for catalog and workspace saves, which share the merge helper.
- [ ] Reuse the strict validation result and parsed YAML node for the exact bytes of a catalog revision. Keep a fresh revision comparison close to durable replacement and recheck after any intervening expensive serialization. Preserve invalid-schema and stale-revision errors, document-size limits and atomic file replacement. Test an external edit between reading and saving.
- [ ] Keep admission and completion checks on current state and retain the per-repository sequence of credential binding, catalog persistence, succeeded journal checkpoint and progress event. Test simultaneous clone completions, external registration or retirement during transfer, failing saves and resume from retained clones. Accept fewer redundant parses and lower registration time with the same durable completion boundaries.

## 5. Bound sync preflight and PR inspection

**Where.** `internal/app/sync.go`, `internal/app/pull_requests.go`, `internal/app/pull_requests_test.go`, and the GitHub and Bitbucket provider clients in `internal/github/client.go` and `internal/bitbucket/client.go`. Use blocking test doubles for ordering and a local HTTP server for provider timing.

- [ ] **Sync.** Run each selected repository's lookup and `checkRepository` in a bounded pool using the existing sync job override or catalog default. Collect paths and errors by sorted input index. Join the pool and return the first erroneous index before starting any fetch or emitting a running event. Preserve dry-run results and hold the mutation lock through worker shutdown for a real sync.
- [ ] Block one validation, fail a later one, and assert zero fetches. Check deterministic error selection with two failures, cancellation, empty selection and a concurrency cap of one or four. Benchmark time to first fetch and total sync using local remotes with no incoming changes.
- [ ] **PRs.** Validate all selectors first. Use the catalog job limit for local identity, branch and query preparation. Keep the actual worktree branch and URL, dry-run query resolution, and detached/unborn outcomes. Store results and failures by sorted member index. A cancelled result must describe unstarted members and preserve cancellation for the CLI.
- [ ] Schedule prepared lookups across distinct provider API hosts with one active lookup per provider client and effective host/port in this batch. Share the existing host normalization. Use a single job budget across preparation and lookup, with jobs=1 preserving current sequential behavior. Same-host requests wait in the coordinator with the caller's context before entering the provider's 30-second lookup timeout. Retain provider gates, cooldowns and credential-scoped cache checks for other callers.
- [ ] Test slow same-host pagination and cross-host overlap, warm cache versus explicit refresh, mixed provider failures, dry runs and cancellation before admission and during lookup. Make capture fakes safe under the race detector. Record local preparation time, first provider lookup, peak concurrent jobs, HTTP requests and total duration for one-host and mixed-host fixtures.
