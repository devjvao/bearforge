# Bearforge

Manage a single bare Git repository per project and materialize ordinary Git worktrees inside named workspaces.

**Status: Phase 2 daily-use implementation.** The CLI supports bulk execution, read-only doctor checks, recoverable repository retirement/restoration and explicit catalog migration alongside the interactive workspace dashboard. [Onboarding](docs/onboarding.md) covers setup and daily workflows; [release verification](docs/releases.md) covers deterministic artifacts, checksums and CI signing. Company private-repository validation is deferred to the maintainer after merge; disposable scale tests cover 60 repositories. Signed publication requires a reviewed, merged version tag. PR integrations belong to Phase 3. The design lives in [the blueprint](docs/architecture/0001-bearforge-blueprint.md).

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

- `repo add`, `list`, `inspect`, `sync`, `remove` / `retire`, `retired`, `restore --operation ID`
- `workspace create` / `init`, `add`, `list`, `show` / `ls`, `path`, `remove-repo`, `remove`
- `sync`, `status` (`--check` for automation)
- `exec WORKSPACE -- COMMAND ARG...` (`--repo`, `--jobs`, `--fail-fast`, `--shell`, `--interactive`)
- `doctor [WORKSPACE]` (read-only accumulated health/recovery checks)
- `tui [WORKSPACE]` (interactive dashboard with sync, repository picker, and previewed workspace actions)
- `operations list`, `show`, `resume`, `abandon`
- `config validate`, `config migrate --to 2`, `completion`, `version`

`--output=json` emits one result; `--output=jsonl` emits progress and a final result. Bubble Tea provides inline progress for supported commands on interactive terminals. Use `--ui=plain` to disable it; pipes, CI, quiet mode and `--no-input` always remain plain. The dashboard uses Lip Gloss for pane borders, focus and health colors, with explicit text labels and a complete monochrome layout.

Bulk exec runs direct argv after `--` in verified worktree directories and defaults to one worker. A single quoted expression without `--` selects the platform shell; explicit `--shell sh|pwsh|powershell|cmd` is available. JSON captures bounded base64 output; JSONL streams sequenced events. Interrupted commands are never replayed by recovery. See [execution and automation](docs/onboarding.md#run-a-command-across-a-workspace).

New roots use catalog schema 2; compatible writes preserve existing v1 catalogs until explicit migration. Review `config migrate --to 2 --dry-run`, then use `--yes` to save an exact backup and upgrade. Retirement requires v2, no workspace references and no linked worktrees; it moves the entire bare database into local trash without deletion or automatic purge. Restore requires the original retirement operation. See [safe lifecycle workflows](docs/onboarding.md#diagnose-and-retire-safely).

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
- `n` opens the workspace creation form. Enter a portable name and optional default branch (blank uses the name); Tab switches fields, Enter previews, and `y` confirms. Creation makes an empty workspace; use `a` afterward to add repositories. Escape returns to edit the form. While typing, `q` is text; Ctrl-C cancels.
- `d` previews removal of the selected repository in the repository pane. `D` previews removal of every member and the workspace definition, including members hidden by filters. Only `y` confirms. Dirty or unsafe worktrees are refused, branches/history are preserved, and unrelated workspace container files are retained. A changed preview requires review again.
- `s` fetches/prunes all repositories in the current workspace, including rows hidden by filters. It uses the existing sync service, catalog worker limit (or `--jobs`), mutation lock and fetch safety checks. Only one sync runs at a time; workspace navigation does not retarget it. Sync leaves checked-out branches and files unchanged.
- Live progress shows each repository as queued, running, synced or failed. Progress updates are bounded and coalesced so a slow UI cannot block sync finalization. Completion refreshes local status and retains per-repository results and errors, including partial failures or repositories not run after a preflight refusal.
- `a` opens **Add repositories** for the loaded current workspace. The picker lists registered repositories; existing workspace members are visible and disabled. Arrows or `j`/`k` move, Space toggles a repository, and `/` filters. Enter applies a filter; Escape clears it. Selections survive filtering, including selected rows that become hidden.
- Enter in the picker previews the selected repositories using the workspace's default branch and the existing service's base/tracking defaults. The scrollable preview shows each destination path, branch creation/reuse, base ref, starting commit and tracking policy. Only `y` confirms execution; Enter does not confirm. Escape or `n` returns to selection, and `r` requests a fresh preview. Branch/base/tracking overrides remain available through `workspace add` in the CLI.
- Confirmation replans under the existing mutation lock and checks the reviewed plan's root, workspace, definition revisions and resolved Git steps. If they changed, execution refuses before Git/journal effects; close the result and reopen the picker for a new preview. Successful repositories survive partial failures, and results show the operation's inspection/recovery commands. Recovery remains an explicit CLI action.
- Escape closes the picker before execution or returns from completed results to the dashboard; `v` reopens the latest add results. While adding, the modal keeps its captured workspace and selection, and sync is unavailable. `q` or Ctrl-C cancels and drains active work through the normal stopping screen.
- `v` reopens or hides the latest action's results. Page Up/Down scroll details, results and operation notices.
- `q` exits successfully; Ctrl-C cancels. Both show a stopping state while cancelling and draining active reads and sync, then restore the terminal.

At 90 columns and wider, workspace and repository panes appear side by side above the details. Narrower terminals show the active list; Tab switches lists. Pane borders and padding give way to a compact layout below 60 columns or 18 rows. Detail and operation-notice text wraps inside the available content width, so Page Up/Down can reveal full values and inspection commands. The minimum usable terminal is 36 columns by 12 rows. The alternate screen is restored on exit. Empty roots/workspaces have explicit empty states and browsing creates no directories, locks or journals. Observations are snapshots; external Git/editor activity may change state during inspection.

The dashboard adapts its palette to light or dark terminal backgrounds. Cyan indicates focus, green healthy states, amber attention and red failures; text labels and leading focus/selection markers remain available in every mode. `--color=auto` follows terminal capabilities, `--color=always` forces color, and `--color=never` disables color and text emphasis. Any nonempty `NO_COLOR` value also disables color and emphasis and takes precedence over `--color=always`. Terminal font and background remain under your terminal's control; the dashboard adds no animation.

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

Migration and repository lifecycle operations resume by probing their backups/source/destination and metadata. Bulk exec is deliberately non-replayable: inspect an interrupted execution and explicitly abandon it after reviewing arbitrary child effects. A fully drained execution is complete even after a child command failed, so an ordinary failed test/build does not block later mutations.

If workspace creation was interrupted after making its directory but before saving its manifest, recovery conservatively refuses that unexplained directory. Inspect it and move it aside (or remove it yourself only if empty) before resuming. Bearforge does not guess that an existing directory is disposable.

Configure SSH agents or Git credential helpers before invoking network commands. Git terminal prompts are disabled for managed operations; credential managers may have their own UI. Do not put passwords or tokens in remote URLs. Work in a local filesystem root, not a network share or cloud-synced directory. Avoid manual simultaneous edits to configuration or worktrees during mutations.

## Development

```sh
go test ./...
go vet ./...
```

Integration tests create disposable local repositories. Native tests are needed on Windows, Linux and macOS; cross-compilation alone does not prove filesystem or terminal behavior. Some Windows restricted environments block Git transport helpers or atomic file replacement; run tests in an ordinary developer terminal in that case.

This milestone does not promise transactional behavior across repositories. Successful steps are retained if later steps fail. Manual root relocation, adopting existing clones/worktrees, custom remote-helper protocols, and submodule provisioning are not supported.
