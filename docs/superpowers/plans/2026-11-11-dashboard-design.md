# Lip Gloss Dashboard Design Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Give the existing workspace dashboard a coherent Lip Gloss presentation with clear focus, health and sync results.

**Architecture:** Keep styles and responsive layout in internal/tui. CLI forwards the established color option; Bubble Tea supplies color-profile and background messages. Existing application services and sync lifecycle stay behind the same interfaces.

**Tech Stack:** Go 1.26, Bubble Tea v2.1.0, charm.land/lipgloss/v2 v2.0.6, existing ANSI width utilities.

**Spec:** User request to adopt Charmbracelet Lip Gloss and improve the design; user authorized choosing the direction and proceeding. Blueprint sections 11/12 supply terminal and presentation boundaries.

## Design direction

A compact terminal workbench for managing many repositories. Use the terminal's own surface, restrained rounded pane borders, a clear selected row and aligned repository/branch/health columns. At wide widths workspace and repository panels sit beside each other; details or sync results occupy the lower panel. Compact terminals retain the focused list with scrollable details and persistent controls.

Core dark palette: ink #D8E0EB, steel #6C819B, focus #78C2D4, healthy #8FC7A4, attention #E6B972, failure #EA929C. Use corresponding darker ink/accent values on light terminals. Preserve terminal font; use weight and spacing for hierarchy. Color reinforces text states, selection markers and focus labels. No animation or forced whole-screen background.

## Global constraints

- Preserve all existing key bindings, explicit sync scope, request/operation identity, cancellation/draining, and application safety checks.
- NO_COLOR with any nonempty value and --color=never provide a complete monochrome design; --color=always forces color unless NO_COLOR is set; auto follows the terminal profile.
- No filesystem/Git/terminal detection inside View or layout helpers.
- Sanitize external data before styling; wrapped diagnostics remain fully reachable; no overflow at 36x12 or with Unicode.
- Pin v2.0.6 to retain the current Go 1.26.0 floor; v2.1.0 requires Go 1.26.7.
- Preserve existing uncommitted sync work and leave this change uncommitted in the supplied checkout.

## Review focus

- Light/dark and monochrome styles must preserve readable focus and health information.
- Padding and borders must not hide controls or detail suffixes at minimum dimensions.
- ANSI styles must not allow hostile terminal text through or corrupt display-width accounting.
- Hidden/inactive panes and filtered lists must not change sync behavior.
- Late profile/background messages and shutdown must not start I/O or undo cancellation.

## Task 1: Styles and terminal color policy

Files: go.mod, go.sum, internal/tui/dashboard_styles.go, internal/tui/dashboard_styles_test.go, internal/tui/dashboard.go, internal/cli/dashboard.go.

- [x] Verify the baseline and add the compatible Lip Gloss dependency.
- [x] Write failing behavior tests for explicit color policy, terminal profile and light/dark adaptation; verify RED.
- [x] Implement model-local styles and color options without global renderer state; forward CLI --color and respect NO_COLOR.
- [x] Run targeted tests and verify GREEN.

## Task 2: Responsive presentation and visual verification

Files: internal/tui/dashboard_view.go, internal/tui/dashboard_layout.go, internal/tui/dashboard_sync.go (result styling only), internal/tui/dashboard_design_test.go, relevant existing dashboard tests, README.md, docs/development/dashboard-design-progress.md.

- [x] Write failing tests for framed panes, useful selected-row information, colored/monochrome width limits, and scrollable full diagnostics; verify RED.
- [x] Implement Lip Gloss borders, focused rows, semantic status styling, header/footer and responsive content widths.
- [x] Render actual dashboard previews at multiple sizes and inspect them; run a native demo smoke test.
- [x] Run full tests, vet, Staticcheck, build, formatting and diff checks; retain the known CGO race-test limitation.
- [x] Obtain one independent review, reproduce and fix material findings, and record evidence.
