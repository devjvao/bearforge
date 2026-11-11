# Implementation ledger - dashboard repository picker and add

Plan: ../superpowers/plans/2026-11-11-dashboard-add.md

Scope: a repository picker and preview/confirmation/results for adding registered repositories to the current workspace.

Ruling: this slice uses existing workspace branch and service base/tracking defaults, with all resolved values visible before confirmation. Overrides remain available through the existing CLI. Cost if wrong: add explicit override inputs in a subsequent UI slice.

Ruling: add approval gets the same ExpectedPlan pattern as removal, bound to root/workspace/revisions/resolved steps, because a dry-run followed by fresh execution must not silently apply a different plan. Cost if wrong: conservative re-preview after unrelated definition changes.

Ruling: preserve the supplied checkout and all earlier uncommitted sync/design work; leave this slice uncommitted for review.

Baseline: go test ./... passed before implementation.

## Implementation

- Task 1 complete: a read-only RepositoryChoices query lists sorted catalog repositories, membership and workspace defaults. Add previews now include destination, branch creation/reuse, base ref, starting OID and tracking policy.
- AddRequest.ExpectedPlan follows removal's existing approval pattern. Add loads catalog/workspace revisions once, resolves the plan and compares its root/workspace/revision/step digest under the existing mutation lock before journal or Git effects. CLI callers without ExpectedPlan retain their existing invocation behavior.
- Real-Git tests observed missing-contract failures, then passed. They verify preview/query content hashes are unchanged, successful approved execution, and refusal without effects after base-ref, workspace-definition, catalog-revision, workspace-identity or physical-root changes.
- Task 2 complete: a opens the picker; Space selects eligible repositories, / filters without discarding selections, Enter requests a preview and only y confirms. Empty/unavailable scopes, failed/incomplete previews, duplicate confirmation, stale messages and unreadable terminal sizes cannot execute.
- The modal captures workspace/selection, serializes I/O through the existing lifetime gate, and shares a bounded nonblocking progress bridge with sync. Final results override progress; unstarted and initial-journal-refusal items show not run. Results refresh observations, retain partial outcomes/recovery commands and reopen with v.
- Model tests failed on the absent action before implementation and pass afterward. Additional regressions cover journal-refusal planned items, resize/back invalidation, undrained progress, delayed work, stopping until drain, minimum-width full paths/OIDs and hostile terminal text in preview/results.
- Task 3 complete: the CLI adapter copies Service for operation-local observers, and real-service tests cover exact selection, dry-run/no journal, stale approval, independent failure, retained membership and explicit recovery. No dependencies were added for this slice.

## Independent review and remediation

- One independent read-only review found no Critical/Important implementation defects and identified one preexisting mixed-plan result-label issue plus a cancellation-validation gap.
- Final: Ruling: treat the result-label finding as material to this slice because a reviewed unchanged repository must not be reported as newly added. This directly affects the requested per-repository results. Cost if wrong: a result classification adjustment; Git effects are unchanged.
- Final: fixed mixed unchanged/new outcomes - TestDashboardServiceAddMixedPlanPreservesUnchangedOutcome RED -> GREEN. Successful steps already complete at execution start now report unchanged, including retained successful steps during recovery.
- TestDashboardServiceAddCancellationAfterGitSuccessRemainsRecoverable passes using real Git.Add followed immediately by context cancellation before upstream/membership completion. The test verifies a partial journal with a started step, a surviving unmapped checkout, then explicit Resume preserving HEAD and edits made after cancellation while completing both memberships.
- No deferred review findings.
- Final: Ruling: the reviewer's declined native/race/platform/prior-slice items are covered by executor evidence below, the previously completed sync/design reviews and the full regression suite. Cost if wrong: native Linux/macOS terminal behavior remains for CI/release validation; only Windows native execution was observed here.

## Native and visual verification

- Created an isolated local-Git fixture at .tmp/dashboard-add-smoke with api/web and an empty review-demo workspace; the user's existing bearforge-demo was not modified.
- Native color TUI: a loaded choices, Space selected both repositories, Enter showed complete resolved plans, Escape returned with selections intact. Before confirmation, membership was empty, the target branch was absent and journal count remained three.
- Re-preview and repeated yy produced exactly one add operation: journal count became four, both worktrees were clean on review-demo, and the completed journal had both memberships. Escape returned to the refreshed dashboard; v reopened retained results. q displayed stopping, restored the terminal and returned 0.
- Native NO_COLOR=please with --color=always retained the monochrome picker. Existing members were disabled, Space/Enter could not schedule a preview, and journal count remained four. Ctrl-C displayed stopping, restored the terminal and returned 130.
- Exported and inspected actual View output for picker/preview/results at 80x24, 120x28 and monochrome 36x12 under .tmp/dashboard-add-preview. Those images use explicitly synthetic presentation fixtures for partial-error content. Temporary preview tests were removed.
- No demo processes remain active.

## Final verification

- Final go test ./..., go vet ./..., Staticcheck, goimports checks on all changed Go files, git diff --check and go mod tidy -diff passed.
- Windows executable and Linux amd64/macOS arm64 cross-builds passed. Native non-Windows runs remain CI/release validation.
- go test -race ./internal/tui could not run because the installed toolchain has CGO disabled; no race-instrumented result is claimed.
- Rebuilt bin/bearforge.exe contains sync, Lip Gloss styling and the new add flow. All work remains uncommitted for review.
