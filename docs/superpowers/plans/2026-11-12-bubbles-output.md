# Bubbles Output Implementation Plan

> **For agentic workers:** Execute inline using superpowers:executing-plans. Read the specification first and keep each step behind its focused tests.

**Goal:** Apply appropriate Bubbles components to every command output category while preserving print-and-exit and machine contracts.

**Architecture:** Replace shared CLI rendering primitives and compose Bubbles views into the existing progress/dashboard controllers. Keep application and storage behavior unchanged.

**Tech Stack:** Go 1.27.4, Cobra, Bubble Tea v2.1.1, Bubbles v2.2.1 and Lip Gloss v2.1.0.

**Spec:** `docs/superpowers/specs/2026-11-12-bubbles-output-design.md`

## Global Constraints

- Ordinary results print and exit; no new pager, dismissal key, command or flag.
- Preserve pipes, JSON/JSONL, child output, workspace paths and shell completion bytes.
- Preserve cancellation/draining, output streams, safe previews and confirmation ordering.
- Preserve every record/field and terminal sanitization; honor terminal width and color policy.
- Keep Go 1.27.4 and existing CI/required-check names.
- Work on `feat/cli-presentation-go127`; commit and push the changes, update PR #4, and follow its CI checks as requested.

## Review Focus

- Default table height silently hiding rows: cover more than 20 rows.
- Narrow tables losing full IDs or commits: cover 36/60 columns and record fallback.
- Complete viewport documents clipping lines: cover long help and nested records.
- Spinner messages or help altering shutdown: cover bounded draining and late messages.
- Controlled lists/bars losing selected rows or misrepresenting totals: cover selection, scrolling, failures and zero totals.

## Task 1: Pin and inspect the dependency

- [x] Verify the official v2.2.1 APIs and record command/component coverage in the spec.
- [x] Add `charm.land/bubbles/v2@v2.2.1` without downgrading existing production modules or Go.
- [x] Verify the dependency graph, checksum integrity and a green baseline.

## Task 2: Complete static CLI components

Files: `internal/cli/presentation.go`, `table_output.go`, `document_output.go`, `result_output.go`, `data_output.go`, `repository_list.go`, `version.go`, `help_output.go` and their focused tests.

- [x] Write failing tests for complete tables, width-aware repository/version output, complete wrapped documents and safe styles.
- [x] Implement Bubbles table/viewport primitives; retain complete records and existing stream/error handling.
- [x] Route every result/data/help/version/empty-state terminal path through those primitives while retaining machine bypasses.
- [x] Run focused CLI tests, including existing output-contract tests.

## Task 3: Inline spinner and keyboard help

Files: `internal/tui/progress.go`, `key_help.go`, progress tests and CLI progress integration tests.

- [x] Pin spinner/help behavior with tests, including animation, resize, no color, late messages and cancellation/draining.
- [x] Replace the handwritten spinner with Bubbles spinner state driven by the current poll; use help/key for existing cancellation actions.
- [x] Run progress and CLI gating tests with race detection where supported.

## Task 4: Dashboard output components

Files: `internal/tui/dashboard_view.go`, dashboard layout/workspace views, `key_help.go`, `progress_bar.go` and dashboard tests.

- [x] Add coverage for selected-row visibility, wrapped detail reachability, contextual help and accurate known-total bars.
- [x] Compose Bubbles controlled list/viewport/help views without changing the controller's selection/filter/action state.
- [x] Use Bubbles progress for known workspace counts, with explicit failure states and no invented percentage for zero/unknown totals.
- [x] Verify the existing 36x12 layout, modal safety and cancellation suites.

## Task 5: Coverage, documentation and verification

- [x] Exercise every command/output family, aliases, empty/error outcomes and the raw-output exceptions from the spec.
- [x] Document selected components and print-and-exit behavior in README; record implementation/verification in the progress ledger.
- [x] Run full Windows build/tests/vet and Linux race tests; verify modules, formatting/imports, lint and vulnerabilities.
- [x] Request one fresh whole-diff review, fix substantive findings and rerun affected checks.

## Progress

Use `.cache/bubbles-output/progress.md` as the ignored execution ledger. The native PowerShell workspace uses the repository's existing ignored cache directory. Publish the completed changes through the existing `feat/cli-presentation-go127` branch and PR #4.
