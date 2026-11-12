# Bubbles command output

## Intent and agreed behavior

Use the appropriate components from `charm.land/bubbles/v2` across Bearforge's command output. The maintainer explicitly chose **print and exit** for ordinary commands. Existing interactive progress, confirmations and the dashboard keep their current lifecycle. Go 1.27.4 remains the only supported toolchain.

The implementation may select component composition and visual details. Keep the existing cyan focus, green success, amber attention and red failure palette, meaningful labels and monochrome support. Avoid adding a pager, a keypress to dismiss a result, a new command or a new flag.

## Available components and selection

The official v2.2.1 catalog includes table, viewport, list, spinner, progress, help/key, paginator, textinput, textarea, cursor, filepicker, timer and stopwatch components.

Use table for tabular results; viewport for wrapped complete documents and existing scrollable details; list for the dashboard's controlled selection views; spinner for unknown-duration work; progress for known completed/total counts; and help/key for existing keyboard actions. List may use its paginator internally. Text entry, file picking, countdown timers and editable documents are outside this output change. The existing elapsed-time clock remains deterministic and independent of work completion.

Primary references:

- https://github.com/charmbracelet/bubbles/tree/v2.2.1
- https://pkg.go.dev/charm.land/bubbles/v2

## Command coverage

| Commands/output | Component composition |
| --- | --- |
| `repo list`, `workspace list`, `operations list` | Complete static table; wrapped labeled records when columns do not fit |
| `repo inspect`, `repo retired`, `operations show` | Tables for records/worktrees and complete viewport documents for nested fields |
| `status`, `workspace show`/`ls`, `doctor` | Spinner while reading, then status tables and wrapped details |
| `repo add`, `sync`, `repo sync`, `workspace add`, `operations resume` | Existing inline progress using a Bubbles spinner and cancellation help, followed by tables/documents |
| `workspace create`/`init`, `workspace remove`/`remove-repo`, `repo remove`/`retire`/`restore` | Complete result tables/documents; preserve preview/confirmation ordering and existing operation progress |
| `exec` | Existing child output streams, spinner where already supported, and a result table including exit/duration/truncation information |
| `config validate`, `operations abandon`, empty states and diagnostics | Complete wrapped status documents with existing outcomes and recovery guidance |
| `version` | Compact metadata table with a narrow-width record fallback |
| Root/group help and `--help` | Existing Cobra content, sanitized and rendered as a complete wrapped document |
| `tui` | Bubbles list views, viewport details, contextual key help and known-total workspace progress bars within the existing controller |
| `workspace path`, `completion`, JSON/JSONL, pipes and child bytes | Preserve exact existing output contracts; these are consumed as paths, shell code or machine/child data |

## Architecture and invariants

- Keep Bubbles in `internal/cli` and `internal/tui`; application services, storage, Git operations and journals do not depend on it.
- Replace the shared CLI table primitive, preserving dynamic columns, stable row ordering, full IDs/paths/numbers and per-state labels. A static table must display every row, including more than the component's default viewport capacity.
- Determine terminal width once per rendering operation. Repository lists and version metadata use the same width-aware behavior as general results. If a table cannot fit, render every field as wrapped labeled records instead of truncating data.
- Complete document views use enough height to print every wrapped line. They never start a Bubble Tea program or read stdin. Retain final newlines and write-error propagation.
- Sanitize untrusted terminal text before layout. `--color=never`, nonempty `NO_COLOR` and `TERM=dumb` disable color/emphasis. Pipes and JSON paths bypass presentation components.
- Drive the Bubbles spinner from the existing bounded work/event poll. Preserve cancellation, draining and final results; do not create a competing animation/work loop.
- Dashboard selection/filtering, stable IDs, safety previews and confirmation state remain owned by the current controller. Bubbles views render the controlled state; internal list filtering/key handling must not add competing actions.
- A progress bar denotes completed items from a known total, never estimated Git transfer percentage. Empty/unknown totals have no percentage or fictitious completion. Failure/cancellation labels remain visible.
- Respect dashboard sizing and scrolling down to 36x12. Tiny inline-progress layouts keep stopping/cancellation feedback within bounds.

## Verification

Test complete static tables with more than 20 rows; narrow and Unicode layouts; long repository names and commit hashes; large/nested record values; unsafe ANSI/bidi data; no-color policy; empty/error/recovery states; stdout/stderr write failures; print-and-exit behavior; exact machine/pipe/path/completion contracts; spinner progression and cancellation/draining; dashboard selection visibility and reachable detail suffixes; and accurate zero/partial/failed progress counts.

Run Windows build/tests/vet, Linux race tests, module verification/tidiness, import/format checks, compatible golangci-lint, govulncheck and a fresh source review. Keep existing CI check names and Go 1.27.4 policy.
