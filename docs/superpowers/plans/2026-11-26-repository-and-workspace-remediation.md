# Bearforge repository and workspace remediation plan

All six fixes from the 26 November 2026 investigation are on `main`. The investigation started at `e7a4e16` using Git 2.47.0 for Windows and disposable local remotes. The merge order was #21, #23, #26, #22, #25 and #24. Ubuntu, macOS, Windows and the 60-repository scale job passed on the combined revision, `971bc60`. [Integrated CI run](https://github.com/devjvao/bearforge/actions/runs/45907479736)

## 1. Reconcile a manually deleted folder

A missing checkout left a workspace entry that failed every refresh and could still reserve its branch in Git. Status and doctor now identify the affected path. If the checkout was moved, run `git worktree repair` from its new location. If it was deleted, preview `bearforge workspace remove-repo WORKSPACE REPO --dry-run`. Confirmation preserves recoverable Git history, releases the matching registration where safe, and clears the workspace entry. A locked registration, staged work or a directory that reappears during review needs attention before cleanup. [PR #22](https://github.com/devjvao/bearforge/pull/22)

A missing **shared bare repository** affects every workspace that uses it. Doctor lists those workspaces. Restore the original store from backup to recover unpublished history. For an unused store, `bearforge repo reclone REPO --dry-run` previews rebuilding it from the registered remote. Check for external worktrees first because the lost Git metadata held their registrations. Real-Git tests cover a missing checkout with and without a stale registration, survival of a reflog-only commit after garbage collection, reuse of the released branch, and two subsequent status refreshes. [Checkout recovery tests](../../../internal/app/missing_checkout_test.go) · [Reclone tests](../../../internal/cli/reclone_test.go)

## 2. Finish removal when the Git link is gone

Deleting the `.git` file from a linked checkout left files on disk and prevented removal from completing. The removal preview now shows where Bearforge will keep those files. It moves an identified directory intact under `.bearforge/trash/worktrees/` and records the move so an interrupted removal can resume. The CLI and dashboard report where to recover the files. [PR #25](https://github.com/devjvao/bearforge/pull/25)

For an older checkout with a Git registration but no recorded directory identity, the default leaves the files and registration in place. The CLI flag `--move-unverified` and dashboard key `m` offer a new preview for an explicit move. An unidentified, unregistered directory stays in place. Tests cover both registration states, replacement directories, surviving untracked files and interruptions before and after the directory move. The sharing-failure test simulates the failure after real Git unregistration. [Recovery tests](../../../internal/app/residual_worktree_test.go) · [Dashboard recovery tests](../../../internal/tui/dashboard_residual_test.go)

## 3. Correct the company Bitbucket token link

The old link omitted the account name. Guided login and logout now use `<installation>/plugins/servlet/access-tokens/users/<username>/manage` for Bitbucket Server and Data Center. The link preserves the installation's port and context path and encodes the username as one path segment. Login validates the username before opening a browser. On an installation with SSO, the manual route is **Profile picture → Manage account → HTTP access tokens**. [PR #21](https://github.com/devjvao/bearforge/pull/21) · [Atlassian’s documented route](https://jira.atlassian.com/browse/BSERV-18812)

Tests assert literal URLs, including a non-ASCII username and a custom installation path. Bitbucket Cloud continues to use the Atlassian account API tokens page. A click-through on the affected company installation remains unverified. [Token URL tests](../../../internal/auth/management_url_test.go)

## 4. Preserve the Bitbucket project in clone and workspace paths

Two Data Center projects could each contain `Service1`, and both imports proposed the same catalog ID and flat destination. A new import of `TEST/Service1` now proposes ID `test-service1`, clones to `repos/test/service1.git` and checks out at `<workspace>/test/service1`. The corresponding repository in `OPS` gets its own ID and directories. Existing registrations, worktrees and pending operations keep their recorded paths. [PR #24](https://github.com/devjvao/bearforge/pull/24)

Manual registration accepts `--folder test/service1`. Workspace add inherits that layout and supports `--flat`, `--parent NAME`, or `--folder-map ID=parent/leaf`. In the dashboard picker, `p` toggles grouping and `d` edits the selected destination. GitHub owner grouping and Bitbucket Cloud project or workspace grouping are optional. Tests cover same-slug repositories across projects, conflicting paths, sibling preservation and recloning into the original nested location. [Onboarding guide](../../onboarding.md) · [Project folder tests](../../../internal/app/project_folders_test.go)

## 5. Check out the intended existing branch

Workspace add could attach a stale local tip or create a same-name branch from the default with no upstream. The preview now looks up the requested branch on the selected remote and records its exact commit. Apply verifies that commit and attaches the local branch with the expected upstream. A matching local tracking branch can fast-forward when behind and keeps its commits when ahead. Divergence, a branch reserved by another worktree, or a remote tip that moved after review stops the operation. Use `--offline` to select from cached refs deliberately. [PR #23](https://github.com/devjvao/bearforge/pull/23)

Real-Git tests check the attached branch, commit and upstream after the remote advances. They also cover divergent local history, an upstream change between preview and fetch, and a branch reserved during bisect. [Workspace branch tests](../../../internal/app/workspace_branch_test.go)

## 6. Materialize local branches on demand

The old bare clone created a local head for every advertised branch and configured their tracking individually. New registrations retain the remote-tracking branches and tags and create at most one local head, for the remote’s advertised default. Adding a feature to a workspace creates its local branch on demand. Existing stores and older interrupted operations keep their local branches and tracking configuration. [PR #26](https://github.com/devjvao/bearforge/pull/26) · [Branch layout tests](../../../internal/gitcli/branch_layout_test.go) · [Clone and resume tests](../../../internal/app/clone_layout_test.go)

The original Windows reproduction took a median of 1.81 s with one branch and 5.10 s with 31 branches over three fresh registrations each. A separate before/after comparison used local branches sharing the same history and measured each configuration once. [Recorded comparison](https://github.com/devjvao/bearforge/commit/445f33cf5b7a4c8bb0353f8cbc69344d36a34876)

| Advertised branches | Git process starts, before → after | Registration time, before → after |
| --- | --- | --- |
| 1 | 55 → 59 | 4.95 s → 4.71 s |
| 31 | 175 → 89 | 13.81 s → 7.05 s |

Both 31-branch runs transferred the same 19,269-byte pack. The measured saving was in local setup. Clone time and transfer volume on the company network remain unmeasured.
