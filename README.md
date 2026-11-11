# Bearforge

Manage a single bare Git repository per project and materialize ordinary Git worktrees inside named workspaces.

**Status: local core plus the first Phase 2 dashboard.** The CLI is usable for evaluating local workflows. The read-only dashboard browses workspace health. Dashboard mutations, bulk execution, PR integrations, repository retirement, and schema migration are later milestones. The approved design lives in [the blueprint](docs/architecture/0001-bearforge-blueprint.md).

## Build

Requires Go 1.26+ and Git 2.40+.

```sh
go build -o bin/bearforge ./cmd/bearforge
```

On Windows, use `-o bin/bearforge.exe`. The module path is temporarily `bearforge` until the public repository URL is selected.

## First workspace

```sh
bearforge repo add report-api git@github.com:company/report-api.git
bearforge repo add report-ui git@github.com:company/report-ui.git
bearforge workspace create audit-feature
bearforge workspace add audit-feature report-api report-ui
bearforge status audit-feature
bearforge sync --jobs 4
```

The default root is `~/code`. Override with `--root PATH` or `BEARFORGE_ROOT`. Definitions live under `.bearforge`, bare repositories under `repos`, and worktrees under the workspace name. Workspace names are portable directory names; use `--branch feature/audit` or `--branch-map report-ui=team/audit` for branch overrides.

`workspace create` currently creates an empty workspace; use `workspace add` to select repositories. There are no templates.

Local source paths such as `./remote` are resolved to absolute paths at registration, so sync and recovery keep the same repository identity after directory changes.

```sh
bearforge workspace add audit-feature report-api --dry-run
bearforge status audit-feature --json
bearforge workspace path audit-feature
bearforge workspace remove-repo audit-feature report-api --dry-run
bearforge workspace remove-repo audit-feature report-api --yes
```

Removal refuses tracked changes, untracked/ignored content, special index flags, sparse checkouts, locked worktrees, unfinished rebase/merge/cherry-pick/revert operations and submodules. It never recursively deletes a checkout as a fallback. Branches and otherwise-unreferenced surviving HEAD reflog tips are preserved through Git recovery branches. There is no force/discard option.

## Commands

- `repo add`, `repo list`, `repo inspect`, `repo sync`
- `workspace create` / `init`, `add`, `list`, `show` / `ls`, `path`, `remove-repo`, `remove`
- `sync`, `status` (`--check` for automation)
- `tui [WORKSPACE]` (read-only interactive dashboard)
- `operations list`, `show`, `resume`, `abandon`
- `config validate`, `completion`, `version`

`--output=json` emits one result; `--output=jsonl` emits progress and a final result. Bubble Tea provides inline progress for supported commands on interactive terminals. Use `--ui=plain` to disable it; pipes, CI, quiet mode and `--no-input` always remain plain. The dashboard uses text labels without color or animation.

## Workspace dashboard

```sh
bearforge tui
bearforge tui audit-feature
bearforge --root /path/to/code tui audit-feature
```

The full-window dashboard lists workspaces and inspects only the selected workspace. Repository details show actual/intended branches, staged/modified/untracked/conflicted counts, upstream and locally observed ahead/behind, detached/unborn HEAD, and per-repository errors. Unfinished operations are reported for inspection with `bearforge operations list` or `show`; the dashboard never resumes or abandons them.

- Tab or Shift-Tab changes the active pane; arrows or `j`/`k` move selection.
- `/` filters the active list by name, Enter accepts, and Escape clears.
- `r` refreshes local observations. It never fetches or changes checkouts.
- Page Up/Down scroll repository details and operation notices.
- `q` exits successfully; Ctrl-C cancels. Both cancel and drain active reads.

At widths below 90 columns, the active list appears above the details; Tab switches lists. The minimum usable terminal is 36 columns by 12 rows. The alternate screen is restored on exit. Empty roots/workspaces have explicit empty states and browsing creates no directories, locks or journals. Observations are snapshots; external Git/editor activity may change state during inspection.

`tui` requires terminal stdin/stdout/stderr, `--ui=auto`, human output and interactive input. It refuses JSON/JSONL, quiet/no-input/dry-run, CI and `TERM=dumb`; use `status WORKSPACE --json` for automation. `--root` and `BEARFORGE_ROOT` work as for other commands. Starting without a workspace selects the first workspace alphabetically.

Exit codes: 0 success; 1 operation failure; 2 invalid input; 3 safety/conflict/confirmation refusal; 4 missing Git; 130 cancellation. Fetch updates remote tracking refs and never merges or resets checked-out branches. Status ahead/behind reflects locally fetched refs, not a live server query.

## Recovery

Mutations write journals before Git effects. An unfinished mutation blocks further mutations in the same root until it is resolved:

```sh
bearforge operations list
bearforge operations show OPERATION_ID
bearforge operations resume OPERATION_ID
```

Resume verifies paths, repository identity, branches and commit IDs. It refuses changed or ambiguous state. `operations abandon ID --yes` leaves all existing files and Git effects intact and stops automatic recovery tracking; inspect and reconcile the retained state manually before proceeding. Fetch can be repeated safely and is not journaled.

If workspace creation was interrupted after making its directory but before saving its manifest, recovery conservatively refuses that unexplained directory. Inspect it and move it aside (or remove it yourself only if empty) before resuming. Bearforge does not guess that an existing directory is disposable.

Configure SSH agents or Git credential helpers before invoking network commands. Git terminal prompts are disabled for managed operations; credential managers may have their own UI. Do not put passwords or tokens in remote URLs. Work in a local filesystem root, not a network share or cloud-synced directory. Avoid manual simultaneous edits to configuration or worktrees during mutations.

## Development

```sh
go test ./...
go vet ./...
```

Integration tests create disposable local repositories. Native tests are needed on Windows, Linux and macOS; cross-compilation alone does not prove filesystem or terminal behavior. Some Windows restricted environments block Git transport helpers or atomic file replacement; run tests in an ordinary developer terminal in that case.

This milestone does not promise transactional behavior across repositories. Successful steps are retained if later steps fail. Manual root relocation, adopting existing clones/worktrees, custom remote-helper protocols, and submodule provisioning are not supported.
