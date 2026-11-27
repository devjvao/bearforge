# Start using Bearforge

Bearforge keeps one managed bare repository per identity and ordinary linked worktrees in named workspaces. Git 2.40 or newer must be on PATH. Use a local filesystem root; network shares, cloud synchronization, submodule provisioning and automatic adoption of existing clones are outside the current support contract.

## Install and choose a root

For a published release, download the archive for your OS/architecture, `checksums.txt`, and `checksums.txt.sigstore.json`. Follow [release verification](releases.md) before extracting or running it. Archives include the executable, license, this guide, release instructions and a binary-derived dependency inventory.

To evaluate a source checkout, follow [the source build guide](development/README.md#build-from-source). The canonical module is `github.com/devjvao/bearforge`; the currently published `v0.2.0` tag predates that module-path change, so use a release archive or a checkout build for now.

Before your first import or workspace creation, enter the directory where you want Bearforge to store repositories and workspaces. To start a separate installation, choose a directory outside any existing Bearforge root.

**PowerShell**

```powershell
New-Item -ItemType Directory -Force -Path "$HOME/managed-code" | Out-Null
Set-Location -LiteralPath "$HOME/managed-code"
bearforge repo list
```

**POSIX shell**

```sh
mkdir -p "$HOME/managed-code"
cd "$HOME/managed-code"
bearforge repo list
```

Bearforge uses the nearest directory containing a real `.bearforge` directory, starting in the current directory and walking up through its parents. If you entered through a symlink or Windows junction, Bearforge walks the parents of its target. This works from inside a managed worktree, including before the first catalog is written. If no marker exists, Bearforge uses the directory where you ran the command. Listing repositories and previewing a change leave an empty root untouched.

When upgrading, change to your previous root. For the former default, use `Set-Location -LiteralPath "$HOME/code"` in PowerShell or `cd "$HOME/code"` in a POSIX shell. If you used `--root` or `BEARFORGE_ROOT`, enter the directory you specified there. Your catalog, workspaces and operation journals are usable in place.

Definitions live under `.bearforge`, bare repositories under `repos`, and worktrees under the workspace name. Workspace names must be portable directory names. For `repo add`, a local source such as `./remote` is relative to the directory where you run the command. Bearforge records its absolute path for sync and recovery and puts the managed clone under the discovered root.

New Bitbucket Server and Data Center imports group folders by project key. For example, `TEST/Service1` gets the catalog ID `test-service1`, a bare clone at `repos/test/service1.git`, and a checkout at `review/test/service1` in the `review` workspace. The import picker also supports grouping by GitHub owner and Bitbucket Cloud project or workspace.

For a local source or URL, `bearforge repo add test-service1 URL --folder test/service1` sets the same layout. Workspace add inherits the registered folder. Use `--flat` for `review/service1`, `--parent services` for `review/services/service1`, or `--folder-map test-service1=custom/service-one` for a specific destination. Use `--dry-run` to review the paths before adding worktrees. Existing catalogs and workspaces retain their recorded locations. When the last checkout leaves a project folder that Bearforge created, it checks the ownership marker and removes the empty folder. You can also select an existing parent, which Bearforge preserves.


## Configuration formats

The repository catalog uses `schema_version: 2` and records active and retired repository identities.

After registering repositories, use `bearforge config validate` to check the catalog and workspace definitions, and `bearforge doctor` to inspect storage and Git health. Ordinary catalog updates preserve comments and extension data.

## Command reference

Use `bearforge COMMAND --help` for flags and examples.

| Command | Purpose |
| --- | --- |
| `auth login`, `status`, `logout` | Save, inspect or remove a provider token connection |
| `repo add`, `list`, `inspect`, `sync` | Register and inspect managed repositories |
| `repo rebind` | Review a replacement saved connection for registered repositories |
| `repo remove` / `retire`, `retired`, `restore --operation ID` | Retire and restore bare repositories without deletion |
| `workspace create` / `init`, `add`, `list`, `show` / `ls`, `path` | Create workspaces and manage membership |
| `workspace remove-repo`, `remove` | Review safe worktree/workspace removal |
| `sync`, `status` | Fetch registered repositories or inspect local workspace state |
| `exec WORKSPACE -- COMMAND ARG...` | Run a command across verified worktrees |
| `doctor [WORKSPACE]` | Run read-only accumulated health/recovery checks |
| `pr list WORKSPACE` | Look up GitHub/Bitbucket pull requests |
| `tui [WORKSPACE]` | Open the interactive dashboard, including provider discovery/import |
| `operations list`, `show`, `resume`, `abandon` | Inspect and resolve unfinished mutations |
| `config validate`, `completion`, `version` | Validate definitions, generate shell completion or inspect the executable |

`workspace create` makes an empty workspace; use `workspace add` or the dashboard's `a` picker to select members. There are no workspace templates. Use `workspace path NAME` to obtain a path for your shell or editor; the command cannot change its parent shell's working directory.

## Authenticate GitHub or Bitbucket

Create a named connection before browsing private repositories. Authentication is stored per operating-system user and works before a managed root exists:

```sh
bearforge auth login --provider github --connection personal-github
bearforge auth login --provider bitbucket --connection work-bitbucket
bearforge auth login --provider bitbucket --host https://bitbucket.example.com --connection company-bitbucket
bearforge auth status
bearforge auth status --repo api
bearforge auth status --local
```

Interactive login uses guided token setup for both GitHub and Bitbucket. It asks for the server URL, opens the provider's token-creation page, and accepts the token through hidden input. If the page cannot be opened, follow the displayed setup URL manually. GitHub Enterprise uses that installation's host; Bitbucket setup distinguishes Cloud from Server/Data Center and preserves installation context paths. `--api-url` supplies a custom HTTPS API base when necessary.

The setup page and required permissions depend on the provider. Use the instructions below before pasting a token. Bearforge uses each provider's required token authentication scheme for REST and Git. A token's access to some repositories does not establish access to every repository.

```sh
bearforge auth login --provider github --connection work-github
bearforge auth login --provider bitbucket --username person@example.com --connection work-cloud
bearforge auth login --provider bitbucket --host https://bitbucket.example.com --username my-login --connection work-server
```

### GitHub token permissions

On GitHub.com, guided setup opens the [prefilled Bearforge fine-grained token form](https://github.com/settings/personal-access-tokens/new?name=Bearforge&contents=read&pull_requests=read). Choose an expiration, set **Resource owner** to the account or organization owning the repositories, and under **Repository access** choose **Only select repositories** and select the intended repositories. Each fine-grained token covers one resource owner; use separate named connections for different owners. Organization approval may be required before private repositories are accessible. [GitHub token setup and URL parameters](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)

Under **Repository permissions**, use:

| Permission | Access | Used by Bearforge for |
| --- | --- | --- |
| **Contents** | **Read-only** | HTTPS clone, fetch, and sync |
| **Metadata** | **Read-only** (automatically included) | Repository discovery and API access checks |
| **Pull requests** | **Read-only** | Listing pull requests |

Leave **Account permissions** empty. The login identity check needs no additional account permission. The repository and PR endpoints require [Metadata read](https://docs.github.com/en/rest/repos/repos#fine-grained-access-tokens-for-list-repositories-for-the-authenticated-user) and [Pull requests read](https://docs.github.com/en/rest/pulls/pulls#fine-grained-access-tokens-for-list-pull-requests), respectively; [the user endpoint](https://docs.github.com/en/rest/users/users#get-the-authenticated-user) returns the identity Bearforge needs without account permissions.

For GitHub Enterprise, guided setup uses the configured installation host for the same prefilled path. If that installation does not support fine-grained tokens, open its `/settings/tokens` page. For a classic token, private repository access requires the top-level **`repo`** scope, which also grants write access. Bearforge's current calls do not need additional `user`, `read:org`, `workflow`, or admin scopes. Classic tokens may require SSO authorization, and organization policy may block them. [Classic scope definitions](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps)

### Bitbucket Cloud API token permissions

Create an [Atlassian API token with scopes](https://id.atlassian.com/manage-profile/security/api-tokens), select **Bitbucket**, and select these four read permissions explicitly:

| Permission | API scope | Used by Bearforge for |
| --- | --- | --- |
| **User: Read** | `read:user:bitbucket` | Verifying the signed-in account |
| **Workspaces: Read** | `read:workspace:bitbucket` | Listing accessible Bitbucket workspaces |
| **Repositories: Read** | `read:repository:bitbucket` | Repository discovery, API access checks, and HTTPS clone/fetch |
| **Pull requests: Read** | `read:pullrequest:bitbucket` | Listing pull requests |

These scopes are independent: pull-request read does not include repository read. Bearforge does not require write, admin, project, pipeline, or webhook permissions. [Atlassian's permission names and scope definitions](https://support.atlassian.com/bitbucket-cloud/docs/api-token-permissions/), [account verification](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-users/#api-user-get), and [workspace listing](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-workspaces/#api-user-workspaces-get) document these requirements.

Supply the **Atlassian account email** with `--username` for a saved Cloud API-token connection. Bearforge uses email plus token for REST Basic authentication and the provider's API-token username for Git HTTPS. The account must already have access to the intended workspace and repositories; token scopes do not grant membership. [Using API tokens](https://support.atlassian.com/bitbucket-cloud/docs/using-api-tokens/)

### Bitbucket Server / Data Center token permissions

On the company server, go to **Manage account > HTTP access tokens** and create a **user** token. Select **Repository read**; if the form includes a project permission selector, use **Project read**. These permissions cover repository discovery, HTTPS clone/fetch, and PR listing; write/admin permissions are unnecessary for Bearforge's current operations. Supply your **Git username** with `--username`. Organization policy may require approval or limit token expiration. [HTTP access token permissions](https://confluence.atlassian.com/bitbucketserver093/http-access-tokens-1472430864.html) and [PR-listing access (`REPO_READ`)](https://developer.atlassian.com/server/bitbucket/rest/v902/api-group-pull-requests/) describe the required access. Cloud API-token scopes do not apply to Server/Data Center.

### Token storage and automation

For automation, choose `--token-stdin` or `--token-env VARIABLE_NAME` explicitly. Token values must not appear in command arguments, URLs, or catalog files. These modes suppress opening a token-management page and work with human, JSON, or JSONL output. Existing API environment credentials remain supported: `GH_TOKEN` then `GITHUB_TOKEN` on public GitHub, `BITBUCKET_TOKEN` as a Bearer credential on public Bitbucket Cloud, and explicit `token_env` metadata for custom APIs. An explicit `token_env` takes precedence over a saved connection; an unset explicit variable is an error. A Bitbucket Cloud API-token connection does not reinterpret an existing `BITBUCKET_TOKEN` value.

Saved tokens use Windows Credential Manager, Linux Secret Service, or macOS Keychain. The registry under the user's configuration directory contains only connection metadata and opaque secret references. Locked or unavailable secure storage produces an error rather than a plaintext fallback. Published macOS archives enable cgo and include the native Keychain backend. macOS source builds need cgo, Apple clang and the macOS SDK; builds without cgo retain environment-token workflows and report saved-secret storage as unavailable. Linux needs an unlocked Secret Service collection and session bus. Status and helper lookups do not unlock a store or open an authentication dialog.

`auth status` performs read-only checks and separates account/API verification from repository API access and Git read access. A public read or successful `ls-remote` is not proof of account identity or push permission. `--local` reads metadata without network requests or secret-store access, so its values are unverified or previously observed. A Server/Data Center API may not attest account identity; Bearforge reports that explicitly rather than trusting the supplied username.

Repository Git checks use the registered bare repository's configuration, including its SSH command, URL rewrites, credential helpers, and TLS settings. A missing repository or an origin that differs from the catalog cannot pass this check. When combining `--repo NAME` with `--connection NAME`, the connection must match that repository's saved association; otherwise the command reports an error. To inspect a replacement connection before rebinding, use `auth status --connection NAME` separately.

`bearforge auth logout --connection NAME` removes that local saved grant. It does not revoke the token at the provider. Existing repository references remain disconnected and cannot silently choose another account; use the displayed management link for provider-side revocation. `--yes` is required to replace an existing connection grant noninteractively.

### Token-only setup and renewal

No OAuth application, consumer, client ID, client secret, or callback configuration is required. Opening the token-creation page only helps you obtain a token; Bearforge verifies the token after hidden entry. Cancellation leaves an existing usable connection intact. The CLI and TUI share this guided setup, with no authentication-method chooser. Older scripts can still pass `--method token`; this compatibility option is hidden from help. Requests for the removed browser authorization method are rejected.

Existing personal/API-token connections continue to work. Saved connections created through the former OAuth flow remain inspectable and can be logged out, but cannot authenticate or refresh: log in again with a provider token. Bearforge does not renew tokens automatically. Replace an expired or revoked token through `auth login`, then review and rebind registered repositories as described below. For an unfinished import, review its replacement connection before resuming that import. Replacing a saved connection creates a new grant identity and never silently reuses an existing repository binding, even when the connection name stays the same.

### Rotate a token for registered repositories

Save the replacement token in a named connection, then select the already registered repositories whose authentication should change. For example, to move `api` and `web` from the old `github` connection to `github-github.com`:

```sh
bearforge auth login --provider github --connection github-github.com
bearforge repo rebind api web --connection github-github.com --dry-run
bearforge repo rebind api web --connection github-github.com
bearforge auth status --repo api
bearforge auth status --repo web
```

If the replacement connection is already saved, start with the dry-run. Repository names are required explicitly. Review the old and replacement connection/grant identities, repository paths, retained origins, and HTTPS credential-helper scopes before confirming. For noninteractive use, review `--dry-run` first and then pass `--yes` to apply the rebind. Use the same managed root for the preview and execution.

Rebinding updates the catalog association and the Git credential helper scoped to each HTTPS origin. It reuses the existing bare repositories and preserves refs, worktrees, and unrelated Git configuration without cloning or fetching. SSH authentication continues to use its existing keys/agent. A repository using explicit `token_env` configuration cannot be rebound to a saved connection through this command. A different provider or origin scope, changed repository metadata, or an unrelated scoped helper produces a conflict for review.

The rebind is recorded as a `repo.rebind` operation. If it is interrupted or a step fails, inspect the displayed operation and run `bearforge operations resume OPERATION_ID` to continue the recorded changes. This recovery does not reclone repositories. The separate `operations resume OPERATION_ID --connection NAME` workflow below is for replacing credentials in an unfinished **import**.

Once every repository using the old connection has been moved and its new access verified, remove the old local connection with `bearforge auth logout --connection github` if it is no longer needed. Keeping both connections can make automatic connection selection ambiguous for repositories without an explicit association. Logging out alone does not repair repositories still bound to the old grant.

## Discover and import several repositories

Start `bearforge tui` and press `i` for **Import repositories**. This action works with an empty root and no workspace. Select a saved connection or use the shared login flow to create one for GitHub or Bitbucket. Choose a repository scope and HTTPS or SSH transport, then explicitly load the list. GitHub scopes use an owner/organization, Bitbucket Cloud scopes use a Bitbucket workspace, and Server/Data Center scopes use a project key. Cloud workspace enumeration and a directly entered workspace are supported. A Bitbucket workspace is separate from a Bearforge workspace.

Changing the source requires a successful reload before previewing repositories. A failed reload leaves the error visible and prevents importing a previous list as though it belonged to the changed source. Compatible reloads preserve selections and edited local IDs. During guided login, switching provider resets its dependent endpoints and username. Use PgUp/PgDown to scroll token instructions while entering a hidden token; the token prompt is brought into view when input is requested.

The environment/anonymous option also supports public GitHub browsing. Without a token, enter an owner or organization scope; Bearforge uses the matching public listing endpoint. A saved connection or environment token lists repositories accessible to that credential, optionally filtered by owner. See [GitHub's repository listing API](https://docs.github.com/en/rest/repos/repos#list-repositories-for-a-user).

Filter and select the repositories to import, edit any conflicting portable local IDs, and review clone URLs, destination paths, the selected connection, and scoped HTTPS Git integration. Already registered repositories are disabled; retired repositories point to restoration. Selections survive filtering. Enter previews; only `y` starts the reviewed batch. Import adds repositories to the catalog; use the existing `a` picker afterward to add them to a Bearforge workspace.

Clones run concurrently using the catalog's `defaults.jobs` limit (default 4, range 1–64). Pass `bearforge --jobs 2 tui` to override it for new import batches and fetches in that dashboard session. Resuming an unfinished import uses the current catalog limit. Each repository's completion appears as it finishes, including failures, and the completed/total count advances while other clones continue. Catalog and journal writes stay serialized. Cancellation stops queued work, cancels active clones, and waits for finalization.

Progress and results retain independent successes if a clone fails. Inspect and resume the displayed operation before starting an unrelated mutation. If its saved grant has been replaced or removed, log in again and review the replacement through the same operation:

```sh
bearforge operations show OPERATION_ID
bearforge auth login --provider bitbucket --connection work-bitbucket
bearforge operations resume OPERATION_ID --connection work-bitbucket --dry-run
bearforge operations resume OPERATION_ID --connection work-bitbucket
```

An unfinished operation blocks unrelated writes until it is resumed or explicitly abandoned. In the dashboard, press `o` to open **Operations**, select the unfinished batch, and inspect its failed steps. Press `r` to review retrying, or `a` to review stopping recovery tracking; only `y` executes either action. Retry reuses successful clones and retained directories. Stopping tracking retains successful registrations, files, and Git effects, clears the write block, and prevents resuming that operation afterward. A retry that fails again remains available in Operations. Navigation, local refresh, and inspection remain available while writes are blocked.

The repair preview is confirmed before changing the journal and resuming. Terminal progress starts only after that confirmation, so the review prompt owns terminal input. Successful clones are verified and reused during resume. Bearforge never deletes retained clone state to retry. Authentication-only repair remains available while an import is partial. For token replacement or manual repair, enter the directory shown with the CLI recovery command.

Recovering or abandoning a workspace removal keeps the current workspace selected while its manifest remains. If removal has deleted the manifest, refresh selects another available workspace, including when a later journal update fails.

HTTPS imports route credentials through a helper scoped to the selected remote, including after the dashboard closes. Ordinary Git and GoLand can use that routing when they use the same installed Bearforge executable and operating-system credential store. Keep the executable at a stable absolute path. Existing unrelated helpers and SSH setup are preserved; a conflicting scoped helper requires review. An explicit disconnected binding stops fallback to another cached account. SSH imports continue to use the existing agent/keys independently of API login.

### Case-distinct Git refs on Windows and other filesystems

A new registration creates only the server's default local branch and fetches all remote-tracking branches and tags. Adding a workspace member creates its requested local branch on demand. Native clones use a private staging directory beside the final bare store. If the process stops before an origin exists, resume preserves the incomplete attempt under `repos/.bearforge-clone-*` and publishes a fresh, verified clone. Existing local branches and older operation journals retain their original behavior.

Repository initialization checks the actual destination filesystem and advertised ref names before choosing storage. Repositories with incompatible case-distinct names use reftable when Git supports it; ordinary repositories keep files storage. You can deliberately choose a backend with `bearforge repo add NAME URL --ref-format reftable` or `--ref-format files`, and inspect the actual backend with `bearforge repo inspect NAME`. Explicit files storage refuses conflicts instead of dropping a branch. The normal Git 2.40 minimum remains sufficient for ordinary repositories; affected repositories may require a newer Git.

If an interrupted addition needs migration, resume preserves refs, HEAD, and reflogs and refuses unsupported conversion. Git 2.47 cannot migrate populated reflogs: upgrade Git to a version supporting preservation, stop external Git writers, inspect the operation, and resume. Bearforge never uses reflog-discarding migration options. Existing repositories with linked worktrees are not automatically converted; an incompatible later fetch is refused before writing, with guidance to reconcile remote names or plan migration separately. Reftable compatibility with other tools depends on the Git implementation they use.

Fetch planning respects remotes excluded from fetch-all by Git configuration. If remote ref names change during a fetch, Bearforge retries reconciliation a bounded number of times before reporting a conflict; it continues to verify exact ref spelling and reject unsafe symbolic refs.

## Register repositories and create two workspaces

Replace these example URLs with repositories you can access. Configure SSH agents or Git credential helpers first; do not embed credentials in URLs.

```sh
bearforge repo add api https://git.example.com/team/api.git
bearforge repo add web https://git.example.com/team/web.git
bearforge workspace create audit-feature
bearforge workspace add audit-feature api web
bearforge workspace create urgent-fix
bearforge workspace add urgent-fix api web
bearforge status audit-feature
bearforge tui audit-feature
```

Each workspace gets a separate worktree per member. Branches default to the workspace name. `workspace add` supports `--branch`, `--branch-map`, `--base` and `--track` when defaults are insufficient. When a repository first joins the workspace, Bearforge looks up the requested branch on the selected remote. The preview pins its commit. A remote-only branch gets a local tracking branch at that commit. An available local branch tracking the same remote fast-forwards when behind and keeps its own commits when ahead. Diverged tips or a different upstream require a choice. Use `--track preserve` to keep the existing local branch, or select a different branch. A branch in use by another worktree stays there.

Use `bearforge workspace add WORKSPACE REPO --offline` to use existing local tips and cached remote refs without contacting the server. An explicit `--base` also uses locally available refs. A failed online lookup stops the add. If the remote moves after preview, review the new plan. If an apply-time conflict left a partial operation, inspect it in Operations and stop tracking that operation before submitting the revised request.

The dashboard offers provider import, workspace creation, repository selection, sync, pull-request lookup and recovery inspection. Mutation previews require `y`; Escape returns without executing. See [dashboard controls and behavior](dashboard.md).

Sync fetches/prunes remote-tracking refs. It does not rebase, merge, reset, switch branches or push. Refresh/status/doctor use local observations and do not fetch.

Local status inspection uses the catalog's `defaults.jobs` worker limit (default 4, range 1–64). The dashboard displays repository results as they finish, with a completed/total count. CLI status retains one final result in repository order, including readable repositories when another check fails. Set `defaults.jobs: 1` in `<root>/.bearforge/catalog.yaml` for sequential inspection.

## Look up pull requests

```sh
bearforge pr list audit-feature
bearforge pr list audit-feature --repo api --repo web --state all
bearforge pr list audit-feature --base main --refresh
bearforge --dry-run pr list audit-feature
bearforge --output=json pr list audit-feature
bearforge --output=jsonl pr list audit-feature
```

The default state is `open`; `closed` includes merged PRs, and `all` includes both. Lookup follows actual verified worktree branches, even when they differ from the intended mapping. It reads the catalog's configured default remote from Git and checks both source and target repository identity, so identical fork branch names do not cross-match. Detached/unborn worktrees are skipped. Missing/unsafe worktrees, unsupported providers, API/rate-limit/authentication errors and incomplete pages are reported per member; other members continue. No matches is a successful, distinct result. Partial failures exit nonzero; Ctrl-C cancels and drains requests with exit 130. Empty workspaces succeed. Invalid selectors are refused before HTTP.

For ordinary public GitHub remotes (`https://github.com/team/api.git`, `ssh://git@github.com/team/api.git`, or `git@github.com:team/api.git`), no provider block is required. Public resources allow anonymous requests. To authenticate, use a saved connection or set `GH_TOKEN` or, if absent/empty, `GITHUB_TOKEN` in the process environment. Existing Git helpers and SSH agents are independent of the environment REST credential; saved HTTPS imports explicitly configure their scoped shared helper. For private access, grant access to the target repository with **Pull requests: read** on a fine-grained token; [GitHub documents the endpoint's token permissions](https://docs.github.com/en/rest/pulls/pulls#fine-grained-access-tokens-for-list-pull-requests). Organization policies may require authorization by your administrator.

The following snippets extend existing repository entries in `.bearforge/catalog.yaml`; retain their other identity/remotes fields. Explicit provider coordinates override inference. For a fork whose actual remote is `git@github.com:alice/api.git`, target the upstream PR repository:

```yaml
repositories:
  api:
    provider:
      kind: github
      host: github.com
      repository: team/api
      source_repository: alice/api # Optional when inferred from the actual remote
```

For GitHub Enterprise, configure the provider explicitly, even when its hostname appears in the remote. The default API is `https://HOST/api/v3`; an explicit `api_url` supports a custom HTTPS API origin:

```yaml
repositories:
  api:
    provider:
      kind: github
      host: github.company.example
      repository: team/api
      source_repository: alice/api
      api_url: https://github.company.example/api/v3
      token_env: COMPANY_GITHUB_TOKEN
```

For Enterprise authentication, use a saved connection scoped to that installation or set the named environment variable through your normal secret-management process. `token_env` is a variable name, never a token value; it overrides a saved connection, and an unset/empty explicitly named variable fails authentication. Without that override, an explicitly bound saved connection is used; an unbound repository can use a single matching saved connection. Multiple matches require an explicit connection selection. With no applicable credential, requests are anonymous. Default public `GH_TOKEN`/`GITHUB_TOKEN` credentials are never forwarded to custom APIs.

API URLs must use HTTPS without embedded credentials, query or fragment. Cross-origin pagination/redirects are refused. Configure `source_repository` explicitly when the actual remote cannot identify the head repository or its host differs from the provider host. Use `github.com` with `api_url: https://api.github.com` for explicit public configuration.

Pagination links and same-origin redirects must retain the configured repository's PR-list path. A rename, redirect or pagination link that changes or obscures those coordinates returns a provider error without caching an empty result. Update the actual Git remote or explicit provider repository metadata to the current repository coordinates, then retry.

### Bitbucket Cloud

Public and private Bitbucket Cloud repositories use the same commands and dashboard keys. Actual Git remotes such as `https://bitbucket.org/team/api.git`, `https://alice@bitbucket.org/team/api.git`, `ssh://git@bitbucket.org/team/api.git` and `git@bitbucket.org:team/api.git` are recognized without provider metadata. Cloud's canonical SSH clone host, `ssh.bitbucket.org`, is also recognized, including `git@ssh.bitbucket.org:team/api.git`. Discovery accepts that SSH host alongside Cloud's HTTPS host, `bitbucket.org`; custom installations retain their configured endpoint restrictions. A username in a Git remote is not reused for API authentication; embedded passwords are refused.

When using HTTPS, repository registration and catalog entries require URLs without userinfo. Register with `bearforge repo add api https://bitbucket.org/team/api.git` and use your Git credential helper for clone/fetch authentication, or use an SSH remote. Username-only HTTPS support above applies when PR lookup observes an actual Git remote that differs from its registered URL. Keep catalog URLs free of usernames and passwords.

Public resources can be read anonymously. For guided API-token setup, use a saved connection with the [four permissions above](#bitbucket-cloud-api-token-permissions). The legacy `BITBUCKET_TOKEN` environment option authenticates requests to the default `https://api.bitbucket.org/2.0` API as Bearer authentication; use a Bearer-compatible access token with pull-request read permission and access to the target repository. It does not provide the email needed for API-token Basic authentication. Git credential helpers, SSH agents and GitHub tokens do not authenticate Bitbucket REST lookups. [Atlassian authentication examples](https://support.atlassian.com/bitbucket-cloud/docs/using-api-tokens/)

For a fork, configure the target repository. The source is inferred from the actual remote, or can be explicit:

```yaml
repositories:
  api:
    provider:
      kind: bitbucket
      host: bitbucket.org
      repository: team/api
      source_repository: alice/api # Optional when inferred from the actual remote
      token_env: TEAM_BITBUCKET_TOKEN # Optional explicit credential override
```

Cloud closed results include merged, declined and superseded PRs. Merged results display `merged`; declined/superseded results display `closed`. The [Cloud endpoint reference](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/) describes its read scope and state filters.

### Company-hosted Bitbucket Server / Data Center

After you sign in to your company Bitbucket Data Center installation with `bearforge auth login`, the dashboard and `pr list` can look up repositories previously added by clone URL. HTTPS remotes must match the saved Git host, port and installation path. SSH remotes use the saved Git hostname and the connection's HTTPS API endpoint. If several Data Center accounts or installations match, select the intended connection with `bearforge repo rebind`.

You can also configure the provider explicitly. Coordinates use `PROJECT/repository-slug`, including personal projects such as `~alice/api`. For example, this catalog entry looks up PRs from a personal fork into the TEAM project.

```yaml
repositories:
  api:
    provider:
      kind: bitbucket-server
      host: git.company.example
      repository: TEAM/api
      source_repository: ~alice/api # Optional when inferred from the actual remote
      api_url: https://git.company.example/bitbucket/rest/api/1.0
      token_env: COMPANY_BITBUCKET_TOKEN
```

Use an HTTP access token with repository read permission; the adapter sends Bearer authentication. [Atlassian documents Server/Data Center HTTP access tokens](https://confluence.atlassian.com/bitbucketserver093/http-access-tokens-1472430864.html). A saved connection scoped to the installation can supply it. Alternatively, `token_env` names an environment variable, never its value; it takes precedence over saved credentials, and an unset explicitly selected variable fails authentication. With neither a bound/matching saved connection nor an explicit environment credential, requests are anonymous. Default Cloud and GitHub credentials are never forwarded to company endpoints.

The API URL is the REST root, without a repository path. HTTPS remotes such as `https://git.company.example:8443/bitbucket/scm/TEAM/api.git` infer the HTTPS port and installation context. Without a matching saved connection, SSH remotes such as `ssh://git@git.company.example:7999/TEAM/api.git` or `git@git.company.example:TEAM/api.git` default to `https://HOST/rest/api/1.0`; an SSH port does not identify the HTTPS port. Set `api_url` for a proxy, different API host or installation context. If the Git remote uses an SSH alias, provide the real `host`, `repository` and `source_repository` explicitly.

API URLs require HTTPS and the system trust store; no TLS-verification bypass is provided. Pagination and redirects retain the configured API origin and exact repository PR-list path. Renames, unsafe pages, access/rate-limit errors and incomplete responses fail per repository without caching an empty result. Mixed GitHub, Cloud and Server members work in one request. The query in JSON/JSONL includes the selected `provider` kind. The repository's [Bitbucket verification evidence](https://github.com/devjvao/bearforge/blob/main/docs/development/bitbucket-pr-lookup-progress.md) records local coverage and deployment limits.

To narrow PR matches to a target branch by default, extend the member in the workspace definition with `pr_target` (distinct from worktree creation `base`):

```yaml
repositories:
  api:
    branch: audit-feature
    pr_target: main
```

`--base` overrides that target for one CLI request. Dry runs verify local identity and resolve the query without contacting provider APIs. Lookup never fetches/checks out, changes refs/files, takes a mutation lock or creates a journal. Human output includes titles, URLs, state/draft, source/target, update time, `observed_at` and `cached`. JSON returns one result document, and JSONL a sequenced final result; successful no-match observations carry an empty PR array.

Workspace PR lookup uses the catalog's `defaults.jobs` limit (default 4) across local Git checks and provider lookups. Each provider client runs one lookup per API host and port at a time. While one host is busy, other repositories can be checked locally or queried on another host. A queued lookup's 30-second timeout starts when it enters the provider. Set `defaults.jobs` to 1 to process members sequentially. Results are always listed in repository order.

The provider bounds each lookup to 30 seconds, 100 pages and 8 MiB per response, with two transient retries at most. Rate limits produce errors and a host cooldown; refresh does not bypass that cooldown. Successful observations stay in process memory for 60 seconds, up to 128 entries per provider client, separated by complete query and credential identity. No cache is written to disk; separate CLI processes start empty. The dashboard reuses its provider clients: `p` may return a cached snapshot, and `P` bypasses cached successes. Details label observation time/cache status and remain historical snapshots as they age; use `P` for a new observation. Local refresh and workspace changes discard old PR results. The repository's [GitHub verification](https://github.com/devjvao/bearforge/blob/main/docs/development/github-pr-lookup-progress.md) and [Bitbucket verification](https://github.com/devjvao/bearforge/blob/main/docs/development/bitbucket-pr-lookup-progress.md) distinguish fixture coverage, public smoke testing and maintainer-owned private/enterprise validation.

## Run a command across a workspace

Portable direct execution passes the executable and each argument after `--`:

```sh
bearforge exec audit-feature -- git status --short
bearforge exec audit-feature --repo api -- go test ./...
bearforge exec audit-feature --jobs 4 --fail-fast -- go test ./...
bearforge exec audit-feature --dry-run -- go test ./...
```

The default is one worker. `--jobs` opts into parallel work. Fail-fast stops launching new jobs after an observed failure and lets running jobs finish; Ctrl-C cancels their process trees and drains them. The root mutation lock is held throughout. Every selected worktree is checked before any command starts, and queued targets are checked again before launch.

Exactly one quoted command expression without `--` selects the platform shell: `sh` on Linux/macOS and `cmd` on Windows. Explicit shells make shared scripts clearer:

```sh
bearforge exec audit-feature --shell sh -- 'printf "%s\n" "$PWD"'
bearforge exec audit-feature --shell pwsh -- 'Write-Output $PWD'
bearforge exec audit-feature --shell cmd -- 'mvn.cmd test'
```

The adapters are `sh -c`, `pwsh/powershell -NoProfile -Command`, and `cmd.exe /D /S /C`. Shell expressions use that shell's quoting and expansion rules. Direct `.cmd`/`.bat` invocation is refused; select `--shell cmd`. An executable after `--` is never selected by whitespace heuristics.

Child stdin is closed by default. `--interactive` requires terminal streams, human output and a single worker; it passes the terminal streams directly and disables output capture. Exec runs user-authorized arbitrary code with the user's environment and is not a sandbox. Bearforge does not inspect or undo arbitrary child effects.

## Output and automation

Set the command's working directory to the intended Bearforge root in CI jobs and scheduled tasks. In a shell script, change to that directory before invoking Bearforge.

`--output=json` emits one final result; `--output=jsonl` emits sequenced progress and a final result. Use `status WORKSPACE --check` for automation that must fail on worktree changes or drift, or `status WORKSPACE --json` for structured observations. Ahead/behind values describe locally fetched refs rather than a live server query.

```sh
bearforge --output=json exec audit-feature -- git status --short
bearforge --output=jsonl exec audit-feature -- git status --short
bearforge --quiet exec audit-feature -- git status --short
```

Human output prefixes captured chunks with the repository name. Quiet suppresses progress, not child output or failures. JSON emits one final document; child bytes appear in per-repository execution records as base64. Captured stdout and stderr are capped independently at 64 KiB per repository and carry explicit truncation flags. Duration is in nanoseconds; child exit status, cancellation and signals are recorded separately.

JSONL emits sequenced started/output/completed events and a final result. Output chunks contain an explicit base64 encoding and at most 4096 bytes. Both streams are drained concurrently; ordering is preserved within each stream, with no promised total ordering between stdout and stderr. Interactive output is sent directly to the terminal and is not captured.

Independent jobs finish by default. Successful repositories are retained after failures. A fully drained exec journal is complete even when a child failed, so a failed build does not block future mutations. If Bearforge itself was interrupted, inspect its execution journal and explicitly abandon it after reviewing child effects. `operations resume` never reruns arbitrary commands.

### Human terminal output

Interactive commands use tables and complete wrapped documents, then exit without a pager or dismissal key. Status shows change counts and upstream drift; inspection, retired repositories, operations, dry-run plans and summaries use readable tables. Long names, hashes, paths and operation IDs wrap without losing data, and static tables print every row. Narrow tables switch to labeled records to preserve full values. Empty lists include guidance where applicable; successful validation is explicit.

Interactive `repo add`, `sync`, `workspace add` and `operations resume` show a compact spinner with an operation title, elapsed time and repository states. Status, workspace inspection and doctor show local checking feedback. Removal/restoration start progress only after confirmation. Labels distinguish synced, skipped, not-run and cancelled states; the spinner indicates activity rather than Git transfer percentage. Ctrl-C or `q` cancels and waits for cleanup. Use `--ui=plain` to disable inline progress. Pipes, CI, quiet/no-input and machine-output modes remain plain.

Human styling follows the [dashboard's color policy](dashboard.md#terminal-layout-and-color). `--color=never`, any nonempty `NO_COLOR`, or `TERM=dumb` disables color and emphasis. Redirected output stays plain even with `--color=always`. JSON/JSONL, shell completions, child-process output and `workspace path` remain undecorated.

| Exit code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Operation failure |
| 2 | Invalid input |
| 3 | Safety, conflict or confirmation refusal |
| 4 | Missing Git |
| 130 | Cancellation |

## Diagnose and retire safely

```sh
bearforge doctor
bearforge doctor audit-feature --json
bearforge operations list
bearforge operations show OPERATION_ID
```

If a checkout folder disappeared, `status` identifies its path and the recovery action. If you moved the checkout, run `git worktree repair` from its new location first. To retire a missing checkout, preview `bearforge workspace remove-repo WORKSPACE REPO --dry-run` or use the dashboard removal preview. Confirmation releases only the matching unlocked Git registration and clears workspace membership. Recoverable detached commits and worktree reflog history stay in the shared repository. Bearforge also retains objects recorded in the checkout's `FETCH_HEAD` and `ORIG_HEAD`. To list their recovery refs, run `git -C PATH_TO_BARE_REPO for-each-ref refs/bearforge/recovery/`. If the preview names a private ref such as `SAVED`, inspect it with `git --git-dir=ADMIN show-ref --verify SAVED` using the reported administrative directory. For a commit, `git --git-dir=ADMIN branch recovered-saved SAVED` creates a shared branch. After verifying the branch points to the original commit, remove the private ref with `git --git-dir=ADMIN update-ref --no-deref -d SAVED VERIFIED_OID` and preview again. Keep a backup of the bare store when the reported metadata cannot be read.

If the shared bare directory under `repos` disappeared, run `bearforge doctor` to find affected workspaces. Restore the original bare directory from backup to recover local-only history and linked-worktree metadata. For an unused store, review `bearforge repo reclone REPO --dry-run` and confirm with `--yes`. Reclone uses the registered origin and repopulates the store with history available from that remote. Check for external linked worktrees first. Bearforge can enumerate its own workspace mappings after the shared Git metadata is lost.

Doctor is read-only and accumulates independent findings: catalog/schema problems, malformed workspace definitions, remote/common-directory drift, changed worktrees, missing/prunable registrations, unregistered destinations, preserved retired storage and unfinished/corrupt journals. It does not fetch, prune, repair, clone or change files. Healthy returns zero; actionable health findings return three.

Before retiring a repository, remove its clean worktrees and workspace references. Safe removal refuses tracked changes, untracked/ignored content, special index flags, sparse checkouts, locked worktrees, unfinished rebase/merge/cherry-pick/revert operations and submodules. It preserves branches and otherwise-unreferenced surviving HEAD reflog tips through Git recovery branches. It never recursively deletes a checkout as a fallback and has no force/discard option:

```sh
bearforge workspace remove-repo audit-feature api --dry-run
bearforge workspace remove-repo audit-feature api --yes
bearforge workspace remove-repo urgent-fix api --yes
bearforge repo remove api --dry-run
bearforge repo remove api --yes
bearforge repo retired
bearforge repo restore api --operation RETIREMENT_OPERATION_ID --dry-run
bearforge repo restore api --operation RETIREMENT_OPERATION_ID --yes
```

`repo retire` aliases `repo remove`. Retirement moves the entire unreferenced bare directory into `.bearforge/trash/OPERATION_ID/api.git` and records its identity. All linked worktrees, including external or prunable registrations, prevent the move. A sharing violation leaves registration intact. No directory is overwritten, copied as a fallback, recursively deleted or automatically purged. Restore checks the original operation, preserved identity and destination, then moves it back without cloning. Known retired identities/remotes cannot silently be registered as a second clone.

These retained directories are local recovery data, not an external backup. Keep an independent backup policy. If rename succeeds but metadata/journal replacement fails, inspect and explicitly resume; recovery probes the two paths and refuses changed refs or ambiguous state.

## Recovery and completion

When returning in a new shell, enter the directory shown with the recovery command so Bearforge opens the journal for that operation.

```sh
bearforge operations show OPERATION_ID
bearforge operations resume OPERATION_ID
bearforge operations abandon OPERATION_ID --yes
```

Resume verifies observed state and never blindly repeats destructive effects. Abandon leaves all files and Git effects intact and records that you will reconcile manually. Exec is the exception to replay: inspect and abandon an interrupted exec, then run a new command explicitly if appropriate.

Mutations write journals before Git effects. An unfinished mutation blocks further mutations in the same root until resolved. Resume checks paths, repository identity, branches and commit IDs, and refuses changed or ambiguous state. Lifecycle recovery probes its source/destination and metadata. Fetch can be safely repeated and is not journaled.

If workspace creation stops after making its directory but before saving its manifest, recovery refuses that unexplained directory. Inspect it and move it aside, or remove it yourself only if empty, before resuming. Bearforge does not assume that an existing directory is disposable.

Configure SSH agents or Git credential helpers before network commands unless using the scoped HTTPS helper from a saved-connection import. Managed Git operations disable terminal prompts; credential managers may have their own UI. Keep passwords/tokens out of remote URLs, and avoid simultaneous manual configuration/worktree changes during mutations.

Generate completion for your shell:

```sh
bearforge completion bash > bearforge.bash
bearforge completion zsh > _bearforge
bearforge completion fish > bearforge.fish
bearforge completion powershell > bearforge-completion.ps1
```

Load the generated script according to your shell's normal completion setup. In PowerShell, dot-source it with `. ./bearforge-completion.ps1`. Use `--no-input`, `--ui=plain` and JSON/JSONL for automation. Pipes, CI, quiet/no-input modes remain plain; full-window TUI requires interactive terminal streams.
