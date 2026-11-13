# Review Findings Implementation Plan

> **For agentic workers:** Use subagents with disjoint file ownership and regression tests before implementation.

**Goal:** Fix GO-01 through GO-07 from the comprehensive review while preserving APIs and successful behavior.

**Architecture:** Keep existing stores, renderers, provider adapters and dashboard state machines. Make localized corrections at their existing boundaries; introduce no dependencies or public interfaces.

**Tech Stack:** Go 1.27.4, existing standard-library tests and repository fixtures.

**Spec:** The review and the user's instruction to fix all seven findings in this conversation.

## Global Constraints

- Preserve public signatures, normal output bytes, business rules and safety checks.
- Write and run regression tests before production changes; verify the expected failures.
- Work in the current checkout on `fix/review-findings`, with disjoint agent ownership. Do not commit, publish, contact external services, or alter dependencies.
- Root runs final `go test ./... -timeout 240s` and `go vet ./...`; workers run focused tests only.

## Review Focus

- Oversized journals must remain readable or preserve the previous readable snapshot; completed execution must not wedge future mutations.
- Plain rendering must propagate stdout and stderr failures without changing successful text.
- Early JSONL errors need one sequenced final-result envelope while JSON keeps its existing shape.
- Explicit GitHub metadata must support safe uninferable remotes without permitting credentials or control characters.
- Every pagination request must retain its repository path; duplicate PR numbers yield one result; malformed pagination is never cached.
- Dashboard recovery text must retain the captured root, including quoting and results retained after refresh.

## Task 1: Journal persistence (root; GO-01)

**Files:** `internal/operation/store.go`, `internal/operation/store_test.go`, and focused `internal/app/exec_test.go` coverage if needed.

- [x] Add round-trip and failure-preservation regressions for oversized operations, plus execution/recovery coverage for repeated large diagnostics.
- [x] Run the regressions and confirm the size mismatch fails.
- [x] Enforce symmetric bounded journal serialization with readable finalization; keep live result diagnostics intact and successful snapshots unchanged.
- [x] Run focused storage and execution tests and review API/error compatibility.

## Task 2: CLI output (subagent; GO-02, GO-03)

**Files:** `internal/cli/root.go` and focused CLI tests.

- [x] Add failing-writer tests for plain items/data/diagnostics and early JSONL envelope regression.
- [x] Run them red, implement minimal write-error propagation and reuse the result renderer for early machine errors.
- [x] Run focused output tests; preserve successful plain/JSON bytes and existing error exit classifications.

## Task 3: GitHub contracts (subagent; GO-04, GO-06, GO-07)

**Files:** `internal/github/resolve.go`, `http.go`, and their existing tests.

- [x] Add regressions for complete metadata with SSH aliases and local paths, unsafe-input rejection, path-changing next links, uncached failures, and overlapping PR pages.
- [x] Run each regression red; then permit safe explicit fallback, enforce the configured list path before pagination transmission, and retain the first occurrence of each PR number.
- [x] Update fixtures that use an unrelated pagination path and run the whole GitHub package.

## Task 4: Dashboard recovery (subagent; GO-05)

**Files:** `internal/tui/dashboard_add.go`, `dashboard_add_view.go`, `dashboard_view.go`, related view/helper and regression tests as needed.

- [x] Add regressions for custom-root add results and pending notices, including spaces/apostrophes and retained results.
- [x] Run them red; capture the operation root and reuse quoted root-qualified TUI recovery construction.
- [x] Run focused dashboard tests without changing async lifecycle or confirmation semantics.

## Task 5: Review and validation (root plus independent reviewer)

- [x] Review each task against the seven findings and inspect test failures/passes reported by workers.
- [x] Independently review the combined diff and address concrete issues.
- [x] Run formatting checks, `go test ./... -timeout 240s`, and `go vet ./...`.
- [x] Inspect final diff/status and report results and material limitations.

### Review follow-ups

- Added safe hostless `file://` source fallback with positive and unsafe-input regressions.
- Reserved the bytes needed to abandon a nonterminal journal; verified refusal without headroom and real abandonment at the boundary.
- Independent combined review found no remaining issues after both corrections.

### Final validation

- `go test ./... -timeout 240s`: passed after the review corrections (application package 131.355 seconds).
- `go vet ./...`: passed.
- Formatting and `git diff --check`: passed.
- Public signatures and dependencies unchanged; implemented on `fix/review-findings`.
