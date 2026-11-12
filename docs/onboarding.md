# Start using Bearforge

Bearforge keeps one managed bare repository per identity and ordinary linked worktrees in named workspaces. Git 2.40 or newer must be on PATH. Use a local filesystem root; network shares, cloud synchronization, submodule provisioning and automatic adoption of existing clones are outside the current support contract.

## Install and choose a root

For a published release, download the archive for your OS/architecture, `checksums.txt`, and `checksums.txt.sigstore.json`. Follow [release verification](releases.md) before extracting or running it. Archives include the executable, license, this guide, release instructions and a binary-derived dependency inventory.

To evaluate a source checkout:

```powershell
go build -trimpath -o bin/bearforge.exe ./cmd/bearforge
.\bin\bearforge.exe version
```

On Linux/macOS, build with `go build -trimpath -o bin/bearforge ./cmd/bearforge` and put that executable on PATH. Source builds, CI and production releases require Go 1.27.4, the latest stable release pinned in `go.mod`. Older Go releases are unsupported.

Choose a new root for managed data. PowerShell:

```powershell
$env:BEARFORGE_ROOT = 'D:\Lab\bearforge-code'
bearforge version
```

POSIX shell:

```sh
export BEARFORGE_ROOT="$HOME/code"
bearforge version
```

An explicit `--root PATH` overrides this environment variable. Otherwise Bearforge searches upward from the current directory for `.bearforge/catalog.yaml`, then falls back to `~/code`. Keep the same root selection when inspecting or recovering an operation.

## Configuration formats

The repository catalog uses `schema_version: 2` and records active and retired repository identities.

After registering repositories, use `bearforge config validate` to check the catalog and workspace definitions, and `bearforge doctor` to inspect storage and Git health. Ordinary catalog updates preserve comments and extension data.

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

Each workspace gets a separate worktree per member. Branches default to the workspace name. `workspace add` supports `--branch`, `--branch-map`, `--base` and `--track` when defaults are insufficient. Existing branches are reused only after safety checks; a branch already checked out elsewhere is refused.

The dashboard offers `p` open PR lookup, `P` uncached PR refresh, `s` sync, `a` repository selection, `n` workspace creation, `d` selected repository removal and `D` whole workspace removal. PR lookup includes filtered members, remains keyboard-responsive and puts timestamped snapshots/errors in scrollable repository details. Startup, navigation and `r` stay local; local refresh/workspace changes cancel and invalidate PR results. Mutation previews require `y`; Escape returns without executing. `v` reopens the latest results. Ctrl-C cancels and drains active work before restoring the terminal. See [dashboard controls](../README.md#workspace-dashboard).

Sync fetches/prunes remote-tracking refs. It does not rebase, merge, reset, switch branches or push. Refresh/status/doctor use local observations and do not fetch.

## Look up GitHub pull requests

```sh
bearforge pr list audit-feature
bearforge pr list audit-feature --repo api --repo web --state all
bearforge pr list audit-feature --base main --refresh
bearforge --dry-run pr list audit-feature
bearforge --output=json pr list audit-feature
bearforge --output=jsonl pr list audit-feature
```

The default state is `open`; `closed` includes merged PRs, and `all` includes both. Lookup follows actual verified worktree branches, even when they differ from the intended mapping. It reads the catalog's configured default remote from Git and checks both source and target repository identity, so identical fork branch names do not cross-match. Detached/unborn worktrees are skipped. Missing/unsafe worktrees, unsupported providers, API/rate-limit/authentication errors and incomplete pages are reported per member; other members continue. No matches is a successful, distinct result. Partial failures exit nonzero; Ctrl-C cancels and drains requests with exit 130. Empty workspaces succeed. Invalid selectors are refused before HTTP.

For ordinary public GitHub remotes (`https://github.com/team/api.git`, `ssh://git@github.com/team/api.git`, or `git@github.com:team/api.git`), no provider block is required. Public resources allow anonymous requests. To authenticate, set `GH_TOKEN` or, if absent/empty, `GITHUB_TOKEN` in the process environment. Git credential helpers and SSH agents are independent of this REST credential. For private access, grant access to the target repository with **Pull requests: read** on a fine-grained token; [GitHub documents the endpoint's token permissions](https://docs.github.com/en/rest/pulls/pulls#fine-grained-access-tokens-for-list-pull-requests). Organization policies may require authorization by your administrator.

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

Set the named environment variable using your normal secret-management process. `token_env` is a variable name, never a token value. Explicitly naming an unset/empty variable fails authentication. Default public `GH_TOKEN`/`GITHUB_TOKEN` credentials are never forwarded to custom APIs; without `token_env`, a custom API is accessed anonymously. API URLs must use HTTPS without embedded credentials, query or fragment. Cross-origin pagination/redirects are refused. Configure `source_repository` explicitly when the actual remote cannot identify the head repository or its host differs from the provider host. Use `github.com` with `api_url: https://api.github.com` for explicit public configuration.

To narrow PR matches to a target branch by default, extend the member in the workspace definition with `pr_target` (distinct from worktree creation `base`):

```yaml
repositories:
  api:
    branch: audit-feature
    pr_target: main
```

`--base` overrides that target for one CLI request. Dry runs verify local identity and resolve the query without contacting GitHub. Lookup never fetches/checks out, changes refs/files, takes a mutation lock or creates a journal. Human output includes titles, URLs, state/draft, source/target, update time, `observed_at` and `cached`. JSON returns one result document, and JSONL a sequenced final result; successful no-match observations carry an empty PR array.

The provider bounds each lookup to 30 seconds, 100 pages and 8 MiB per response, with two transient retries at most. Rate limits produce errors and a host cooldown; refresh does not bypass that cooldown. Successful observations stay in process memory for 60 seconds, up to 128 entries, separated by complete query and credential identity. No cache is written to disk; separate CLI processes start empty. The dashboard reuses one client: `p` may return a cached snapshot, and `P` bypasses cached successes. Details label observation time/cache status and remain historical snapshots as they age; use `P` for a new observation. Local refresh and workspace changes discard old PR results. [Verification evidence and remaining limits](development/github-pr-lookup-progress.md) distinguish fixture coverage, public smoke testing and maintainer-owned private/enterprise validation.

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

```sh
bearforge --output=json exec audit-feature -- git status --short
bearforge --output=jsonl exec audit-feature -- git status --short
bearforge --quiet exec audit-feature -- git status --short
```

Human output prefixes captured chunks with the repository name. Quiet suppresses progress, not child output or failures. JSON emits one final document; child bytes appear in per-repository execution records as base64. Captured stdout and stderr are capped independently at 64 KiB per repository and carry explicit truncation flags. Duration is in nanoseconds; child exit status, cancellation and signals are recorded separately.

JSONL emits sequenced started/output/completed events and a final result. Output chunks contain an explicit base64 encoding and at most 4096 bytes. Both streams are drained concurrently; ordering is preserved within each stream, with no promised total ordering between stdout and stderr. Interactive output is sent directly to the terminal and is not captured.

Independent jobs finish by default. Successful repositories are retained after failures. A fully drained exec journal is complete even when a child failed, so a failed build does not block future mutations. If Bearforge itself was interrupted, inspect its execution journal and explicitly abandon it after reviewing child effects. `operations resume` never reruns arbitrary commands.

## Diagnose and retire safely

```sh
bearforge doctor
bearforge doctor audit-feature --json
bearforge operations list
bearforge operations show OPERATION_ID
```

Doctor is read-only and accumulates independent findings: catalog/schema problems, malformed workspace definitions, remote/common-directory drift, changed worktrees, missing/prunable registrations, unregistered destinations, preserved retired storage and unfinished/corrupt journals. It does not fetch, prune, repair, clone or change files. Healthy returns zero; actionable health findings return three.

Before retiring a repository, remove its clean worktrees and workspace references. Safe removal refuses local/untracked/ignored data and retains branches/history:

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

```sh
bearforge operations show OPERATION_ID
bearforge operations resume OPERATION_ID
bearforge operations abandon OPERATION_ID --yes
```

Resume verifies observed state and never blindly repeats destructive effects. Abandon leaves all files and Git effects intact and records that you will reconcile manually. Exec is the exception to replay: inspect and abandon an interrupted exec, then run a new command explicitly if appropriate.

Generate completion for your shell:

```sh
bearforge completion bash > bearforge.bash
bearforge completion zsh > _bearforge
bearforge completion fish > bearforge.fish
bearforge completion powershell > bearforge-completion.ps1
```

Load the generated script according to your shell's normal completion setup. In PowerShell, dot-source it with `. ./bearforge-completion.ps1`. Use `--no-input`, `--ui=plain` and JSON/JSONL for automation. Pipes, CI, quiet/no-input modes remain plain; full-window TUI requires interactive terminal streams.
