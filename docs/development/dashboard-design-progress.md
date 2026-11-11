# Implementation ledger - Lip Gloss dashboard design

Plan: ../superpowers/plans/2026-11-11-dashboard-design.md

Scope: presentation and color policy for the existing dashboard and sync action.

Ruling: choose a compact workbench design with terminal-native surfaces, cyan focus and semantic health colors, after the user explicitly delegated visual direction. Cost if wrong: revise the style tokens and layout.

Ruling: pin Lip Gloss v2.0.6 instead of v2.1.0, because the latter requires Go 1.26.7 and the repository currently supports Go 1.26.0. Cost if wrong: later dependency upgrade after a deliberate Go floor change.

Ruling: continue in the supplied checkout and preserve all uncommitted sync work. No commits or publication are part of this design request.

## Implementation

- Baseline go test ./internal/tui ./internal/cli passed before styling changes.
- Task 1 complete: model-local Lip Gloss styles, light/dark palettes, terminal-profile handling, CLI color forwarding and explicit NO_COLOR precedence. New color-policy tests failed for the absent capability, then passed after implementation. Theme/profile messages schedule no work.
- Lip Gloss is the only new direct library; colorprofile becomes a direct dependency at its existing version. Module tidy selects the newer go-udiff/golden/x/exp checksum entries required by Lip Gloss; the Go 1.26.0 floor and existing production dependency versions remain unchanged.
- Task 2 complete: rounded bordered panes, aligned repository/branch/health columns, full-row selection, semantic health and result colors, quieter metadata and persistent key hints. Layout lives in dashboard_layout.go to separate viewport geometry from row/detail content.
- New framing and long unbroken-path tests failed against the previous view, then passed. Colored and monochrome sizing tests cover 160x40 through 36x12 and smaller fallback sizes; existing scrolling, terminal-text sanitization, lifecycle and sync tests pass.

## Visual and native verification

- Exported actual View output using local demo snapshots at 120x32, 80x24 and 36x12. Rendered and inspected dark, light and monochrome PNG previews under .tmp/dashboard-design. The sync-error preview is an explicitly synthetic presentation fixture; no remote failure was induced.
- Refreshed and inspected previews after the final focus-marker fix. Preview harnesses were removed; production tests do not depend on D:/Lab/bearforge-demo or write preview files.
- Native Windows terminal against the user's demo: forced color, pane framing, selection and repository filtering verified; q showed stopping, restored the terminal and returned 0.
- Native NO_COLOR=please with --color=always showed the full monochrome layout with no application color/emphasis escapes. Ctrl-C showed stopping, restored the terminal and returned 130.
- No demo processes remain running. Browsing did not modify demo worktrees.

## Independent review

- One independent read-only review found a missing monochrome focus indicator when both panes were filtered; the trailing active label also clipped in the 90-column sidebar.
- Final: fixed filtered monochrome focus - TestDashboardMonochromeFilteredPanesKeepFocusVisible failed at 90 columns before the fix, then passed at 90 and 120. Active pane headings now carry a leading > marker before filter text, so clipping cannot remove the cue.
- No Critical or Minor findings and no deferred design findings.
- Final: Ruling: the reviewer's declined items are covered by prior independent sync review, executor-observed native smoke tests and the full repository checks below; no additional service changes are needed for this styling slice. Cost if wrong: additional platform validation may reveal terminal-specific issues; native execution here covered Windows, with Linux/macOS cross-builds only.

## Final verification

- go test ./..., go vet ./..., Windows executable build, Staticcheck via golangci-lint, goimports checks on all changed Go files and git diff --check passed after the review fix.
- Linux amd64 and macOS arm64 cross-builds passed after the fix; native runs on those systems remain CI/release validation.
- go test -race ./internal/tui cannot run with the installed CGO-disabled toolchain. No race-instrumented result is claimed.
- Rebuilt bin/bearforge.exe includes both the existing sync action and the new design. All changes remain uncommitted for review.
