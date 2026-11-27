# Workspace dashboard

For your first import or workspace, create or enter the directory where you want Bearforge to store its data. Once it contains `.bearforge`, you can open the dashboard from that directory or any managed worktree. See the [PowerShell and POSIX setup examples](onboarding.md#install-and-choose-a-root).

Open the dashboard to browse workspaces, or give a workspace name to select it directly.

```sh
bearforge tui
bearforge tui audit-feature
```

Starting without a workspace selects the first workspace alphabetically. The dashboard inspects only the selected workspace. The repository table shows working-tree health and a separate **Base** column. For example, a clean feature branch showing `↑3 ↓2` has three commits of its own and is missing two commits from the configured base. `↑0 ↓0` means the tips are synchronized. Counts use local Git history and show `unknown` when the comparison is unavailable. Press `s` to fetch the latest remote refs.

Repository details name the base ref and give exact counts for both the base and the current branch’s upstream. They also show actual and intended branches, staged, modified, untracked and conflicted files, detached or unborn HEAD, and per-repository errors.

Local inspection uses `defaults.jobs` in `<root>/.bearforge/catalog.yaml` (default 4, range 1–64). Rows start as `inspecting` and update as each repository finishes; the header counts completed repositories, including failures. Repository order and selection remain stable. Refreshing or switching workspaces cancels the previous inspection; quitting waits for active checks to stop. Refresh stays local and does not fetch.

For first-time setup, follow [authentication](onboarding.md#authenticate-github-or-bitbucket) and [repository discovery/import](onboarding.md#discover-and-import-several-repositories). Import registers repositories; adding them to a workspace is a separate action.

## Controls

| Key | Action |
| --- | --- |
| Tab / Shift-Tab | Change the active pane |
| Arrows / `j` / `k` | Move selection |
| `/` | Filter the active list by name; Enter accepts and Escape clears |
| `r` | Refresh local observations without fetching |
| `p` / `P` | Look up open pull requests / bypass cached successes |
| `i` | Discover and import repositories from a provider |
| `n` | Create an empty workspace |
| `a` | Add registered repositories to the current workspace |
| `d` / `D` | Preview selected-repository / whole-workspace removal |
| `s` | Fetch/prune repositories in the current workspace |
| `u` | Preview fast-forwarding the selected repository’s local branches from their remotes |
| `b` | Preview updating the selected feature branch and rebasing it onto the repository default |
| `o` | Inspect unfinished operations and review recovery actions |
| `v` | Show or hide the latest action results |
| Page Up / Page Down | Scroll details, previews, results and operation notices |
| `q` / Ctrl-C | Quit / cancel and drain active work |

Keys are contextual inside forms and dialogs. In a text field, `q` is text; Ctrl-C cancels. Workspace/import/recovery previews require `y` to execute. Escape returns without executing before confirmation.

## Import repositories

Press `i` even when the root has no workspaces. Choose a saved GitHub or Bitbucket connection, use the shared guided token setup, or select the environment/anonymous option where supported. Choose the provider scope and HTTPS or SSH transport, then explicitly load repositories. Space selects repositories, `/` filters, and `e` edits portable local IDs. Selections survive filtering. Already registered entries are disabled; retired repositories point to restoration.

Enter previews clone URLs, destination paths, connection identity and scoped HTTPS credential integration. Only `y` starts the reviewed batch. Independent successes remain if a clone fails. Once repositories are registered, use `n` to create a workspace and `a` to add its members.

New import batches clone concurrently up to the catalog's `defaults.jobs` limit (default 4, range 1–64), or the dashboard's `--jobs` override. Resuming an unfinished import uses the current catalog limit. Each repository's row updates as it finishes, while the completed/total count advances independently of slower clones. Catalog and recovery-journal updates remain serialized. Ctrl-C stops queued work, cancels active clones, and waits for active clones and journal updates to drain; retained clones remain available for recovery.

Changing the source requires a successful reload before another preview. Guided setup uses provider tokens with hidden input; it requires no OAuth application or callback setup. The [import guide](onboarding.md#discover-and-import-several-repositories) covers provider scopes, token replacement, Git credential routing and partial-import recovery.

## Create and change workspaces

Press `n` to open the creation form. Enter a portable name and optional default branch; a blank branch uses the name. Tab switches fields, Enter previews, and `y` confirms. Creation makes an empty workspace; press `a` afterward to add repositories. Escape from the preview returns to edit the form.

Press `a` to open **Add repositories** for the loaded workspace. Existing workspace members remain visible but disabled. Arrows or `j`/`k` move, Space toggles a repository, and `/` filters. Enter applies a filter; Escape clears it. Selections survive filtering, including selected rows that become hidden.

Enter looks up the workspace branch on the selected remote and previews the selected repositories. Press `o` in the picker to use cached refs offline or to keep a local tip after a divergence. The review names the previous commit for a proposed fast-forward. The scrollable preview shows each destination path, branch creation/reuse, base ref, starting commit and tracking policy. Only `y` confirms execution. Escape or `n` returns to selection; `r` requests a fresh preview. For branch/base/tracking overrides, use `bearforge workspace add` in the CLI.

Confirmation replans under the mutation lock and checks the reviewed root, workspace, definition revisions and resolved Git steps. Changed state causes a refusal before Git or journal effects; close the result and reopen the picker for a fresh preview. Successful repositories survive partial failures. Results include operation inspection/recovery commands.

Escape closes the picker before execution or returns from completed results to the dashboard. While adding, the dialog retains its captured workspace and selection, and sync is unavailable. `v` reopens the latest results.

A missing checkout has a removal preview that identifies whether Git still holds its registration. If the checkout was moved, use Git's worktree repair before clearing the old registration.

If a checkout loses its `.git` link, preview its removal to see where the remaining files will be kept. Bearforge moves an identified checkout intact under `.bearforge/trash/worktrees` and shows the recovery path. For older checkouts whose identity was never recorded, the default keeps the files and registration in place. Press **m** in the removal preview or pass `--move-unverified` to `workspace remove-repo` or `workspace remove` to review an explicit move. An unregistered, unidentified folder stays in place. If deletion stopped partway through, open the pending operation and review its recovery action before resuming.

Press `d` in the repository pane to preview removing the selected member. Press `D` to preview removal of every member and the workspace definition, including members hidden by filters. Only `y` confirms. Dirty or unsafe worktrees are refused, branches/history are preserved, and unrelated workspace container files remain. A changed preview requires review again. See [safe lifecycle workflows](onboarding.md#diagnose-and-retire-safely).

## Sync and pull requests

Select a repository and press `u` to review fast-forward updates to its current branch and local default branch. Press `b` to also rebase the current feature branch onto the repository default. Both previews show which branches will move. Press `y` to apply the reviewed update locally. Rebasing can change the IDs of commits already on the feature branch. Publishing those commits is a separate Git operation.

The checkout must be clean. The preview names the checked-out branch and shows the workspace’s intended branch if they differ. If a feature branch and its own published feature branch have diverged, the update reports which branches need reconciliation. A default branch checked out in a different worktree stays there. The selected feature can still rebase onto the fetched remote default.

Press `s` to fetch/prune all repositories in the current workspace, including rows hidden by filters. Sync uses the catalog worker limit or `--jobs`, the mutation lock and fetch safety checks. Only one sync runs at a time; navigating to another workspace does not retarget it. Checked-out branches and files remain unchanged.

Live progress labels each repository as queued, running, synced or failed. Updates are bounded and coalesced so a slow UI cannot block finalization. Completion refreshes local status and retains per-repository results/errors, including partial failures and repositories not run after a preflight refusal. Completion bars appear only when the total is known; failed items count as completed work. Empty workspace creation has no percentage.

Press `p` to look up open GitHub and Bitbucket PRs for every loaded workspace member, including filtered members. `P` bypasses cached successes. The header immediately shows lookup/refresh progress, then a summary including no matches, failures and cache reuse. Selected-repository details show metadata, skipped states, errors and observation time; Page Up/Down reveals complete titles and URLs.

Lookup preserves local status, filters and selection, and keys remain usable during HTTP. Startup, navigation and `r` do not contact provider APIs. Local refresh or workspace changes cancel and invalidate PR results. Repeated `p`/`P` does not queue requests while a lookup is active or draining. Provider clients retain bounded, timestamped observations in memory; refresh still respects rate-limit cooldowns. See [PR authentication, configuration and limits](onboarding.md#look-up-pull-requests).

## Operations and recovery

Unfinished mutations block unrelated writes. Press `o` to open **Operations**, including when a partial import prevents other changes. Select an operation to inspect failed steps. Press `r` to review retrying or `a` to review stopping recovery tracking; only `y` executes either action. Retry reuses successful clones and retained directories. Stopping tracking retains registrations, files and Git effects, clears the write block, and prevents resuming that operation afterward.

A retry that fails again remains available in Operations. Navigation, local refresh and inspection remain available while writes are blocked. Results show the operation's directory alongside the recovery command, even after you select a different workspace. For CLI recovery, enter that directory in your shell, then inspect the operation before resuming it. An interrupted `exec` requires inspection and an explicit decision to abandon it.

```sh
bearforge operations list
bearforge operations show OPERATION_ID
bearforge operations resume OPERATION_ID
```

See [import recovery](onboarding.md#discover-and-import-several-repositories) for reviewing a replacement connection and [general recovery](onboarding.md#recovery-and-completion) for resume/abandon behavior. Recovering or abandoning workspace removal keeps that workspace selected while its manifest exists; otherwise refresh selects another available workspace.

## Terminal layout and color

At 90 columns and wider, workspace and repository panes appear side by side above details. Narrower terminals show the active list; Tab switches lists. Borders and padding give way to a compact layout below 60 columns or 18 rows. Text wraps within the available width, and Page Up/Down reveals complete values and inspection commands. The minimum usable terminal is 36 columns by 12 rows.

The palette adapts to light/dark backgrounds. Cyan indicates focus, green healthy states, amber attention and red failures. Explicit labels and focus/selection markers remain available without color. `--color=auto` follows terminal capabilities, `--color=always` forces color, and `--color=never` disables color and text emphasis. Any nonempty `NO_COLOR` value takes precedence over `--color=always`. Terminal font/background remain under your terminal's control; the dashboard adds no animation.

`tui` requires terminal stdin/stdout/stderr, `--ui=auto`, human output and interactive input. It refuses JSON/JSONL, quiet/no-input/dry-run, CI and `TERM=dumb`. For automation, set the command's working directory to the managed root and use `bearforge status WORKSPACE --json`.

Empty roots/workspaces have explicit empty states, and browsing creates no directories, locks or journals. `q` exits successfully; Ctrl-C cancels. Both display a stopping state while cancelling and draining active reads, provider requests and mutations, then restore the alternate screen and terminal.

### Project folders

In the import picker, **p** toggles grouping for the highlighted repository. Bitbucket Server and Data Center start with project grouping enabled. GitHub offers the owner folder. Bitbucket Cloud offers the project folder when the API supplies a project key, or the workspace folder otherwise. **e** edits the catalog ID and **d** edits the clone folder independently.

When adding workspace repositories, **p** toggles **Use parent folder** for the selection. **d** overrides the highlighted worktree's destination with a leaf or `parent/leaf`. The preview shows the resulting path for each worktree before confirmation.
