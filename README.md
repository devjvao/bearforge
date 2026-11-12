# Bearforge

Manage a single bare Git repository per project and materialize ordinary Git worktrees inside named workspaces.

**Status: Phase 2 daily-use workflows with Phase 3 GitHub PR lookup.** The CLI supports bulk execution, read-only doctor checks, GitHub PR snapshots and recoverable repository retirement/restoration alongside the interactive workspace dashboard. [Onboarding](docs/onboarding.md) covers setup and daily workflows; [release verification](docs/releases.md) covers deterministic artifacts, checksums and CI signing. Company private-repository validation is deferred to the maintainer after merge; disposable scale tests cover 60 repositories. Signed publication requires a reviewed, merged version tag. The design lives in [the blueprint](docs/architecture/0001-bearforge-blueprint.md).

## Build

Requires Go 1.27.4 and Git 2.40+. Source builds, CI and releases support only the latest stable Go release, pinned in `go.mod`.

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
- `pr list WORKSPACE` (read-only GitHub PR lookup; `--repo`, `--base`, `--state`, `--refresh`)
- `tui [WORKSPACE]` (interactive dashboard with sync, repository picker, and previewed workspace actions)
- `operations list`, `show`, `resume`, `abandon`
- `config validate`, `completion`, `version`

`--output=json` emits one result; `--output=jsonl` emits progress and a final result. Bubble Tea provides inline progress for supported commands on interactive terminals. Use `--ui=plain` to disable it; pipes, CI, quiet mode and `--no-input` always remain plain. The dashboard uses Lip Gloss for pane borders, focus and health colors, with explicit text labels and a complete monochrome layout.

Bulk exec runs direct argv after `--` in verified worktree directories and defaults to one worker. A single quoted expression without `--` selects the platform shell; explicit `--shell sh|pwsh|powershell|cmd` is available. JSON captures bounded base64 output; JSONL streams sequenced events. Interrupted commands are never replayed by recovery. See [execution and automation](docs/onboarding.md#run-a-command-across-a-workspace).

Catalogs use `schema_version: 2`. Retirement requires no workspace references and no linked worktrees; it moves the entire bare database into local trash without deletion or automatic purge. Restore requires the original retirement operation. See [safe lifecycle workflows](docs/onboarding.md#diagnose-and-retire-safely).

## GitHub pull requests

```sh
bearforge pr list audit-feature
bearforge pr list audit-feature --repo report-api --state all --refresh
bearforge --output=json pr list audit-feature --base main
bearforge --output=jsonl pr list audit-feature
bearforge --dry-run pr list audit-feature
```

Lookup uses each verified worktree's actual branch and configured default remote, with open PRs by default. `--repo` is repeatable; `--base` overrides the workspace mapping's `pr_target`; `--state closed` includes closed/merged PRs. A fork match verifies source repository, branch, target repository and optional base. GitHub HTTPS, SSH and SCP-style remotes are recognized. Enterprise/custom hosts require explicit `provider` metadata; forks can set the upstream target with `repository` and infer `source_repository` from the actual remote. See [configuration examples](docs/onboarding.md#look-up-github-pull-requests).

Public reads may be anonymous. Credentials come only from environment variables: `GH_TOKEN`, then `GITHUB_TOKEN`, for the default public API. Custom APIs require an explicit `token_env` to send a credential; an unset explicitly named variable is an authentication error. Git credentials and SSH agents do not authenticate this REST lookup. Fine-grained tokens need access to the target repository and **Pull requests: read**, as described in [GitHub's endpoint permissions](https://docs.github.com/en/rest/pulls/pulls#fine-grained-access-tokens-for-list-pull-requests). Never put token values in catalog metadata, URLs or command arguments.

Human output includes PR metadata, `observed_at` and `cached`; JSON emits a final result and JSONL emits its sequenced final result. Each member reports matches, no matches, detached/unborn skips or an error. Failed/rate-limited/offline lookups are errors, never successful empty lists; independent members continue, with partial results and a nonzero exit on failure. Ctrl-C cancels/drains active I/O and exits 130. `--dry-run` resolves queries without HTTP. Lookup does not fetch, change refs/checkouts, acquire a mutation lock, or write a journal/cache file.

The shared provider keeps at most 128 observations for 60 seconds in process memory, keyed by query and credential identity. A new CLI invocation starts empty; dashboard lookups reuse one client. `--refresh` (or dashboard `P`) bypasses cached successes, while rate-limit cooldowns still apply. Each provider lookup has a 30-second deadline, up to 100 pages, an 8 MiB response limit and at most two transient retries. Observations are timestamped snapshots; old displayed values are not a live promise. Company/private-enterprise validation remains maintainer-owned; see [verification status](docs/development/github-pr-lookup-progress.md).

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
- `p` looks up open GitHub PRs for the entire loaded workspace, including members hidden by filters; `P` bypasses the memory cache. Selected-repository details show metadata, no matches, skipped states, errors, observation time and cache status. Page Up/Down reveals complete titles and URLs. Lookup preserves local status, filters and selection, and keys remain usable during HTTP. Startup, navigation and `r` do not contact GitHub. Local refresh/workspace changes cancel and invalidate PR results; repeated `p`/`P` keys do not queue more requests while one is active or draining.
- `n` opens the workspace creation form. Enter a portable name and optional default branch (blank uses the name); Tab switches fields, Enter previews, and `y` confirms. Creation makes an empty workspace; use `a` afterward to add repositories. Escape returns to edit the form. While typing, `q` is text; Ctrl-C cancels.
- `d` previews removal of the selected repository in the repository pane. `D` previews removal of every member and the workspace definition, including members hidden by filters. Only `y` confirms. Dirty or unsafe worktrees are refused, branches/history are preserved, and unrelated workspace container files are retained. A changed preview requires review again.
- `s` fetches/prunes all repositories in the current workspace, including rows hidden by filters. It uses the existing sync service, catalog worker limit (or `--jobs`), mutation lock and fetch safety checks. Only one sync runs at a time; workspace navigation does not retarget it. Sync leaves checked-out branches and files unchanged.
- Live progress shows each repository as queued, running, synced or failed. Progress updates are bounded and coalesced so a slow UI cannot block sync finalization. Completion refreshes local status and retains per-repository results and errors, including partial failures or repositories not run after a preflight refusal.
- `a` opens **Add repositories** for the loaded current workspace. The picker lists registered repositories; existing workspace members are visible and disabled. Arrows or `j`/`k` move, Space toggles a repository, and `/` filters. Enter applies a filter; Escape clears it. Selections survive filtering, including selected rows that become hidden.
- Enter in the picker previews the selected repositories using the workspace's default branch and the existing service's base/tracking defaults. The scrollable preview shows each destination path, branch creation/reuse, base ref, starting commit and tracking policy. Only `y` confirms execution; Enter does not confirm. Escape or `n` returns to selection, and `r` requests a fresh preview. Branch/base/tracking overrides remain available through `workspace add` in the CLI.
- Confirmation replans under the existing mutation lock and checks the reviewed plan's root, workspace, definition revisions and resolved Git steps. If they changed, execution refuses before Git/journal effects; close the result and reopen the picker for a new preview. Successful repositories survive partial failures, and results show the operation's inspection/recovery commands. Recovery remains an explicit CLI action.
- Escape closes the picker before execution or returns from completed results to the dashboard; `v` reopens the latest add results. While adding, the modal keeps its captured workspace and selection, and sync is unavailable. `q` or Ctrl-C cancels and drains active work through the normal stopping screen.
- `v` reopens or hides the latest action's results. Page Up/Down scroll details, results and operation notices.
- `q` exits successfully; Ctrl-C cancels. Both show a stopping state while cancelling and draining active reads, PR lookups and sync, then restore the terminal.

At 90 columns and wider, workspace and repository panes appear side by side above the details. Narrower terminals show the active list; Tab switches lists. Pane borders and padding give way to a compact layout below 60 columns or 18 rows. Detail and operation-notice text wraps inside the available content width, so Page Up/Down can reveal full values and inspection commands. The minimum usable terminal is 36 columns by 12 rows. The alternate screen is restored on exit. Empty roots/workspaces have explicit empty states and browsing creates no directories, locks or journals. Observations are snapshots; external Git/editor activity may change state during inspection.

The dashboard adapts its palette to light or dark terminal backgrounds. Cyan indicates focus, green healthy states, amber attention and red failures; text labels and leading focus/selection markers remain available in every mode. `--color=auto` follows terminal capabilities, `--color=always` forces color, and `--color=never` disables color and text emphasis. Any nonempty `NO_COLOR` value also disables color and emphasis and takes precedence over `--color=always`. Terminal font and background remain under your terminal's control; the dashboard adds no animation.

`tui` requires terminal stdin/stdout/stderr, `--ui=auto`, human output and interactive input. It refuses JSON/JSONL, quiet/no-input/dry-run, CI and `TERM=dumb`; use `status WORKSPACE --json` for automation. `--root` and `BEARFORGE_ROOT` work as for other commands. Starting without a workspace selects the first workspace alphabetically.

CLI commands use Bubbles tables and complete wrapped documents with Lip Gloss styling when stdout is a terminal. Results print and exit without a pager or dismissal key. Workspace status includes change counts and upstream drift; repository inspection, retired repositories, operation history, dry-run plans and execution summaries have readable tables instead of raw JSON. Empty lists include guidance where applicable, and successful validation is explicit. Help headings, version metadata, progress, errors and confirmations use the same terminal color policy. Long names, commit hashes, paths and operation IDs wrap without losing data; static tables print every row. `--color=never`, any nonempty `NO_COLOR`, or `TERM=dumb` disables color and emphasis. Redirected output retains its plain format even with `--color=always`; JSON/JSONL, shell completions, child-process output and `workspace path` remain undecorated.

Interactive `repo add`, `sync`, `workspace add` and `operations resume` use a compact Bubbles spinner with an operation title, elapsed time and repository states. `status`, `workspace show`/`ls` and `doctor` show checking feedback while reading local state. Workspace removal and repository removal/restoration start their execution progress only after confirmation, so the progress view never consumes confirmation input. Result tables switch to wrapped, labeled records when they cannot fit the terminal width, preserving full paths and status details. Progress labels consistently distinguish synced, skipped, not-run and cancelled states. The spinner shows activity, not an estimated Git transfer percentage. Ctrl-C or `q` cancels and waits for cleanup before returning; `--ui=plain`, quiet, noninteractive and machine modes retain their existing behavior. The dashboard uses Bubbles lists, detail viewports and contextual key help. Adding, syncing and removing workspace repositories show static completion bars only when the total is known, including failed items in completed work; empty workspace creation has no invented percentage.

Exit codes: 0 success; 1 operation failure; 2 invalid input; 3 safety/conflict/confirmation refusal; 4 missing Git; 130 cancellation. Fetch updates remote tracking refs and never merges or resets checked-out branches. Status ahead/behind reflects locally fetched refs, not a live server query.

## Recovery

Mutations write journals before Git effects. An unfinished mutation blocks further mutations in the same root until it is resolved:

```sh
bearforge operations list
bearforge operations show OPERATION_ID
bearforge operations resume OPERATION_ID
```

Resume verifies paths, repository identity, branches and commit IDs. It refuses changed or ambiguous state. `operations abandon ID --yes` leaves all existing files and Git effects intact and stops automatic recovery tracking; inspect and reconcile the retained state manually before proceeding. Fetch can be repeated safely and is not journaled.

Repository lifecycle operations resume by probing their source/destination and metadata. Bulk exec is deliberately non-replayable: inspect an interrupted execution and explicitly abandon it after reviewing arbitrary child effects. A fully drained execution is complete even after a child command failed, so an ordinary failed test/build does not block later mutations.

If workspace creation was interrupted after making its directory but before saving its manifest, recovery conservatively refuses that unexplained directory. Inspect it and move it aside (or remove it yourself only if empty) before resuming. Bearforge does not guess that an existing directory is disposable.

Configure SSH agents or Git credential helpers before invoking network commands. Git terminal prompts are disabled for managed operations; credential managers may have their own UI. Do not put passwords or tokens in remote URLs. Work in a local filesystem root, not a network share or cloud-synced directory. Avoid manual simultaneous edits to configuration or worktrees during mutations.

## Development

```sh
go test ./...
go vet ./...
```

Integration tests create disposable local repositories. Native tests are needed on Windows, Linux and macOS; cross-compilation alone does not prove filesystem or terminal behavior. Some Windows restricted environments block Git transport helpers or atomic file replacement; run tests in an ordinary developer terminal in that case.

This milestone does not promise transactional behavior across repositories. Successful steps are retained if later steps fail. Manual root relocation, adopting existing clones/worktrees, custom remote-helper protocols, and submodule provisioning are not supported.
