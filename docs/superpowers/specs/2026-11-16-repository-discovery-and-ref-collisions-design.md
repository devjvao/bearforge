# Repository authentication, discovery, and safe case-distinct Git refs

Status: Approved for implementation on 2026-11-16. Implement Git ref safety first, shared GitHub/Bitbucket authentication next, then TUI repository discovery and bulk import.

## Intent and evidence

Daily use has exposed two related onboarding problems: adding a repository can fail on Windows when remote refs differ only by case, and adding many repositories requires repeating `repo add NAME URL`. The user selected the **current terminal dashboard** as the interface for repository discovery. The desktop application remains deferred.

The screenshot shows a company-hosted Bitbucket Server/Data Center instance, so supporting only bitbucket.org would not address the reported workflow.

The user also requested `bearforge auth` before repository discovery, with browser login where supported and a guided token fallback. This applies equally to **GitHub and Bitbucket**, including enterprise/self-hosted instances. The fallback and the full implementation sequence were approved explicitly.

Investigation on Windows with Git 2.47.0.windows.1 reproduced an additional failure mode: a files-backend fetch can exit successfully while retaining only one of two case-distinct remote-tracking refs. A new reftable bare clone retained both refs, and linked worktrees could use both branches. Converting the partially initialized files repository failed because this Git version cannot migrate its reflogs. Checking only fetch stderr or blindly retrying is insufficient.

## Scope and proposed defaults

Deliver three changes in order: protect repository initialization/fetch from ref collisions; add shared authentication commands for GitHub and Bitbucket; then add provider browsing and bulk import to the existing TUI. Authentication is a prerequisite for the saved-connection discovery experience. These are separate implementation units with shared regression coverage.

The defaults below are the implementer's proposal, not additional user decisions: automatic ref-format selection for affected new repositories; named saved connections backed by the operating system's credential store; preservation of existing environment-based credentials; HTTPS clone URLs with an SSH choice; a separate dashboard import action; and a single recoverable batch operation for each confirmed selection.

Browser authorization, guided token entry, secure credential persistence, and scoped HTTPS Git credential integration are included. A desktop frontend, adoption of existing ordinary clones, SSH key/agent setup, and a new hosted authentication service are outside this change. Existing SSH configuration remains usable.

## 1. Preserve case-distinct refs

### New repositories

Before creating the bare clone, inspect advertised heads and tags through the same Git executable and credential environment used for cloning. Map heads into both local-head and remote-tracking namespaces, and tags into the tag namespace. Detect case-equivalent ref names, case-folded file/directory prefix conflicts, and differently spelled shared path components such as `Group/one` and `group/two`, including collisions between refs with different object IDs. Never resolve a collision by dropping a branch, lowercasing refs, or silently selecting one spelling.

During journal-owned execution, probe the actual repository destination filesystem for case sensitivity using uniquely owned temporary probe files; clean up only those probe files and fail clearly on probe errors. Do not assume filesystem behavior from the operating system, and do not create probes during discovery or preview. Use the ordinary files backend where it can represent the refs exactly. Select reftable for an affected new repository on an incompatible filesystem when the installed Git supports it. Capability detection is required; the existing Git 2.40 minimum continues to support ordinary repositories. If the required backend is unavailable, return a concise error naming representative conflicting refs and explaining that Git must be upgraded or remote branch names reconciled. Ref storage applies to Bearforge's bare repository and its linked worktrees, not to global Git configuration.

Make this choice before the first fetch that writes tracking refs. Persist the chosen backend in the operation before repository creation so resume cannot silently choose another representation. Remote refs can change between advertisement and fetch: initialization must verify that all expected fetched branch identities are represented exactly, and return a failure if refs are missing or ambiguous even when Git exits zero. Do not compare against a stale advertised object ID as though the remote could not legitimately advance.

Expose the selected backend in human inspection and import results, with a short explanation when compatibility requires reftable. An explicit reftable option for `repo add` is useful for deliberate selection; exact option naming is left to implementation planning.

### Interrupted additions and existing repositories

Keep the current identity checks for started additions: the directory is bare, origin matches the journal, and there are no linked worktrees. Resume must recognize the existing backend and preserve all retained refs, objects, and reflogs.

If an interrupted files repository needs reftable, attempt conversion only when installed Git supports preserving its reflogs and the verified journal-owned repository has no linked worktrees. Snapshot and verify the exact existing refs, HEAD, and reflog identities around conversion; retain recovery evidence and refuse to proceed if they differ unexpectedly. Hold the Bearforge mutation lock and check for active Git locks, while documenting that external Git writers must be stopped during migration. Refuse unsafe or unsupported conversion with an actionable diagnostic and retain the original operation and directory. Never use reflog-discarding migration flags, delete the directory, or automatically abandon its journal. Document upgrading Git and retrying `operations resume ID` as the supported continuation when preservation-capable migration is unavailable.

For already registered repositories, sync must detect newly introduced remote ref collisions before writing through an incompatible backend. Report a specific repository failure and retain the current checkout. Automatic conversion of repositories with linked worktrees is outside this change; explain that remote branch reconciliation or a separately planned migration is required. Do not globally change `core.ignorecase` or fetch exclusions.

## 2. Authenticate GitHub and Bitbucket before discovery

Provide one authentication service shared by the CLI, provider clients, TUI, and managed HTTPS Git operations. Login must work before a Bearforge root or repository exists. Store connection metadata per operating-system user, independently of any one managed root; repository configuration records the selected connection reference, never its secret.

### Commands and status semantics

The proposed command surface is:

```sh
bearforge auth login --provider github
bearforge auth login --provider bitbucket
bearforge auth login --provider github --host github.example.com
bearforge auth login --provider bitbucket --host https://bitbucket.example.com
bearforge auth status
bearforge auth status --repo report-api
bearforge auth status --local
bearforge auth logout --connection work-bitbucket
```

Both providers have the same login, status, and logout capabilities. `--host` may supply an installation URL, including a Bitbucket context path; otherwise login prompts for it, offering the public-provider default. Confirm the provider kind and validated API origin before authorization. `bitbucket` covers both Cloud and Server/Data Center through that setup step. Exact secondary flag names and connection-file layout remain implementation-planning choices.

`auth login` chooses or creates a named connection, displays its intended host and capabilities, and prefers a supported, configured browser authorization flow. The user can choose guided token entry instead. After obtaining a grant, verify the credential and record the account identity when the provider can attest it. Opening a page, receiving a pasted token, or reading a public repository is insufficient to report an authenticated account. Report API verification separately from Git access; no repository-specific clone check is possible until a target is supplied or selected.

`auth status` lists saved connections even outside a managed root. Inside a root it also reports registered repository remotes and their selected credential source, grouping equivalent connection checks while preserving repository-specific permission results. `--repo NAME` narrows this to one repository. Normal status performs bounded, cancellable read-only checks; `--local` performs no network requests or credential refresh and labels observations as unverified or previously observed with their timestamp.

Report API authentication/identity, repository API access, and Git read access as distinct capabilities. Distinguish configured-but-unverified, verified, missing credentials, rejected credentials, insufficient permission, offline, expired/relogin-required, and rate-limited states. A public read or successful `git ls-remote` establishes reachability/read access, not account identity or push permission. Do not test status by pushing, fetching into local refs, or changing a checkout. Local remotes are not applicable; SSH access is checked through the existing SSH configuration, independently of saved HTTPS tokens. Follow existing human/JSON output, noninteractive, cancellation, and exit-code conventions without exposing secrets.

`auth logout` removes only the selected saved grant and its nonsecret connection metadata. Require an unambiguous connection choice when several accounts or grants share a host. Leave repository associations visible as disconnected so another account is not silently selected. Remove or disable only Bearforge-owned credential routing for that connection; preserve unrelated helpers, credentials, repositories, and worktrees. Explain that local logout is not provider-side token revocation and provide the provider's management-page link.

### Browser authorization and guided token fallback

Browser authorization requires a usable provider application registration, not merely an existing browser session. Never scrape browser cookies or embed a confidential OAuth client secret in distributed binaries. Public client IDs may ship as application metadata. Required host-specific client secrets are externally provisioned and stored securely. If a provider flow would require a hosted broker or unsupported callback arrangement, offer token setup instead; adding an authentication service is a separate scope decision.

| Provider | Preferred browser route when configured | Guided fallback |
|---|---|---|
| GitHub.com | Device authorization through a registered application with device flow enabled; open the verification page and display its user code. | Open personal-access-token setup and accept a supported token through hidden input. |
| GitHub Enterprise | Use the instance's supported authorization flow and its configured application registration; do not assume github.com registration works on another host. | Open that instance's personal-access-token setup and accept a supported token through hidden input. |
| Bitbucket Cloud | Browser OAuth authorization-code flow through a registered consumer with a supported callback and token exchange. | Open API-token setup and collect the required account identifier plus token through hidden input. |
| Bitbucket Server/Data Center | Browser OAuth through an administrator-created incoming application link, with its client configuration and supported callback. | Open the installation's personal/HTTP-access-token page and collect the required username plus user token through hidden input. |

The guided fallback applies to **both GitHub and Bitbucket**, not just company Bitbucket servers. Offer it when browser integration is unavailable, disabled by policy, or explicitly declined. Explain provider authorization or organization-approval failures instead of silently retrying through another identity. Do not promise that a token bypasses organization approval requirements. If browser opening fails or the terminal has no browser, print the validated setup/verification URL and let the user complete the same supported flow manually.

Token guidance explains permissions needed for discovery, repository read/clone, and PR lookup, along with organization/SSO authorization where applicable. Request write access only when the user deliberately wants it. Respect selected-repository grants and do not describe them as access to every repository. New Bitbucket Cloud setup uses supported API tokens rather than deprecated app passwords. Encode the token kind and authentication scheme explicitly: GitHub tokens, Bitbucket OAuth/access tokens, Bitbucket Cloud API tokens, and Server user tokens must not all be treated as interchangeable Bearer credentials. Use the provider's required account identifier and authentication scheme separately for REST and Git.

Browser sessions use state validation, PKCE where supported, bounded expiry/polling, and callback validation. Bind local callbacks to loopback and use only callback arrangements accepted by the provider registration/policy. Cancellation or denial stops polling/listeners and does not replace an existing usable connection. Save rotating access/refresh tokens atomically and serialize refresh per connection, including concurrent CLI/helper processes. A failed or expired refresh becomes a clear reauthentication state, not an endless login loop. Ordinary status can refresh an existing grant without opening a browser; `--local` cannot.

### Saved connections and shared credential resolution

Keep access tokens, refresh tokens, and confidential client configuration in native secure storage: Windows Credential Manager, macOS Keychain, or an available Linux Secret Service backend. Save only names, provider kind, validated host/API/authorization endpoints, verified account identifier, grant type, expiry/observation metadata, and opaque secret references in the connection registry. Distinct grants for the same account or host remain separate named connections. If secure storage is unavailable or locked, fail clearly and retain existing environment-token workflows; never silently write plaintext tokens to configuration.

For API operations, an explicitly configured `token_env` keeps its current meaning and takes precedence; an unset explicitly named variable remains an error. Otherwise use the repository's or import session's explicitly selected connection. For unbound legacy public-host repositories, retain `GH_TOKEN` then `GITHUB_TOKEN`, or `BITBUCKET_TOKEN`, before considering an unambiguous saved default for that provider/host/API base. The legacy `BITBUCKET_TOKEN` contract remains a Bearer credential; creating a saved API-token connection does not reinterpret it. Fall back to anonymous public API reads only when no explicit source is selected and the endpoint supports them. Never fall through a missing/rejected explicit connection to a different account.

Bind each saved connection to its approved API and Git origins and installation context, including intentional public-provider API/web host differences. A matching hostname alone is insufficient to send credentials. Provider-supplied links and redirects cannot expand that binding. Reuse the existing bounded HTTP behavior and credential-aware cache separation; account/grant changes invalidate affected observations. Tokens, refresh grants, and token exchange responses must not appear in URLs, command arguments, output, diagnostics, catalog/journal documents, or cached result data. Interactive token entry is hidden; any noninteractive secret-input mechanism must use stdin or an existing environment source.

### Git cloning, ordinary Git, and GoLand

Connect saved HTTPS grants to Git through a Bearforge credential-helper entry point backed by the same resolver. Clone preflight and transfer can use invocation-scoped routing before the bare repository exists; persist repository/remote-scoped routing after registration so ordinary Git and GoLand can use the selected connection. Keep secret values out of helper command lines. Give the helper a stable installed executable path, and diagnose a missing/moved executable rather than silently selecting another account.

Preview any required persistent Git integration changes as part of login association or import review. Configure only the selected repository/remote URL and preserve unrelated settings and helper chains. Inspect conflicts rather than silently replacing them; for an explicitly bound remote, ensure another helper's cached account cannot override the selection. A helper request without a matching explicit binding returns no credential. Existing unbound Git repositories continue to use their configured helpers and SSH agents. Reusing a capable existing credential manager as an internal adapter is allowed, but it must not change the selected account or the documented credential boundaries.

Login may offer to associate a saved connection with selected existing registered remotes after reviewing that mapping and any scoped Git changes. Such catalog changes use the root lock and revalidation; saving a user-level connection alone does not require a root mutation or clear unfinished repository journals. Existing explicitly configured `token_env` settings are not silently removed. Selected connections apply to all workspaces for that repository remote. HTTPS authentication does not configure SSH keys or prove SSH identity.

## 3. Browse remote repositories in the dashboard

Add an **Import repositories** action, proposed key `i`, available from a valid loaded root even when it has no workspaces. Keep `a` for adding registered repositories to the selected workspace. Startup, navigation, and local refresh remain offline; discovery starts only through the explicit import action.

The import flow is:

1. Choose a saved GitHub or Bitbucket connection, or start the same shared login flow to create one. Preserve advanced environment-token and anonymous-public browsing options where supported. Show the connection's provider kind, host/API base, and verification state; accept an optional scope filter and HTTPS or SSH clone transport. Infer reusable nonsecret settings from existing provider metadata where unambiguous, but allow correction. An explicit Load action starts discovery; startup and navigation never trigger login or provider requests.
2. List accessible repositories with owner/project, repository name, description, visibility, and registration state. Show the selected repository's clone URL and default branch in details. Arrows navigate, `/` filters, Space selects, and Enter reviews. Selections survive filtering and page loading. No clone starts while browsing.
3. Mark already registered repositories unavailable for duplicate import, comparing provider identity as well as clone URL so HTTPS/SSH variants are not cloned twice. Mark retired repositories with restore guidance. Propose valid portable local IDs, permit editing a selected item's ID, and require resolution of case-insensitive ID/destination conflicts before confirmation. Show all resulting paths and clone URLs in the review.
4. Confirm the reviewed selection with `y`. Display each item as queued, cloning, registered, failed, or not run. Successful imports become available to the existing workspace picker after a local refresh. Import does not automatically add them to a workspace.
5. Show completion or partial results, retained successes, and inspection/resume commands. Preserve results through the existing results toggle. Escape can close pre-execution screens; cancellation during execution drains running work before returning.

Discovery, PR lookup, and import use the shared credential resolver described above. Token values are entered only in the dedicated hidden login prompt, never in discovery filters or ordinary form fields. Existing environment-token selections use variable names, not values. The review records the selected connection and any scoped Git integration changes alongside the clone destinations. API access and Git clone authentication remain separate capabilities; show targeted errors if discovery works but Git access fails, with a route to login or status for that connection.

### Discovery coverage and limits

GitHub lists repositories accessible to the authenticated user within the selected grant, including organization membership where authorized. Use `/user/repos` with its documented affiliations and pagination. GitHub Enterprise uses an explicit validated API base and selected saved connection or explicit environment credential.

Bitbucket Cloud discovers the user's accessible workspaces and allows selecting a workspace to browse its repositories. A directly entered workspace is also supported for credentials that can read repositories but cannot enumerate workspaces. Distinguish a Bearforge workspace from a Bitbucket workspace in every label.

Bitbucket Server/Data Center lists readable repositories from `/rest/api/latest/repos`, with project/name filtering where supported and pagination via `nextPageStart`. Support an installation context path in the configured host/API base. Preserve API-provided HTTPS/SSH clone coordinates after validation, including SSH ports.

Reuse the existing providers' request protections: deadlines, response/page bounds, cancellation, bounded transient retries, cooldowns, safe redirects and pagination, sanitized diagnostics, and credentials restricted to the selected API origin. Treat incomplete pagination and access failures as errors, never as a complete empty repository list. Do not send tokens to clone URLs or provider-supplied pagination hosts. Show loading/error/empty states and an explicit retry action. Discovery and previews write no catalog or journal.

## 4. Batch import and recovery

Implement a shared application import use case rather than looping over the public single-repository `AddRepository` method: its first partial operation currently blocks all subsequent mutations.

Build a deterministic preview containing the root, catalog revision, selected provider identities, nonsecret connection/grant references, local IDs, sanitized remotes, provider metadata, scoped Git integration changes, and destinations. Bind confirmation to this plan. Revalidate catalog, destinations, identities, connection selection, and grant ownership under the existing root mutation lock before any clone or import journal is created. Normal refresh of a grant's access token does not change the selected connection; replacing that grant with a different account or authorization requires review again. Network observations can expire; do not promise remote availability from a preview.

A confirmed batch owns one root lock and one journal. Each selected item persists its remote, provider metadata, connection/grant reference, chosen ref format, scoped Git integration intent/state, and error before relevant Git effects. Store no credentials in this journal. Clone concurrency is bounded; a sequential first implementation is acceptable and is the proposed default. Independent item failures do not stop later selected items, while cancellation stops scheduling new work. Stop on a failure to persist the journal or other shared state, since continuing without recovery records is unsafe.

Register each verified successful clone immediately, including provider metadata and the selected connection so enterprise/server PR lookup and later Git access work after import. Keep successful imports when another item fails. A batch with failed or interrupted items remains partial and blocks unrelated mutations until resumed or explicitly abandoned, consistent with current recovery rules. Authentication commands that only repair user-level credentials remain available to resolve that failure.

Resume validates each item's destination, origin, connection/grant identity, and owned Git configuration, verifies registered successes instead of cloning them again, and processes only unfinished items. Resolve credentials afresh, allowing normal token renewal; if the reviewed grant has been deleted or replaced with another identity, refuse credential use and require a reviewed reassociation rather than silently picking an account. Retry failures through this operation. Externally changed or ambiguous state is refused. Never overwrite an existing unrelated destination or replay a completed clone. Old single-repository journals and existing catalogs/workspace definitions remain readable by the updated application. Document added connection references and any schema/version compatibility boundary explicitly; do not promise that an older binary understands new auth/import fields.

## Validation and acceptance

- Disposable local remotes with case-distinct heads and tags verify exact refs and object IDs, including a Windows regression for successful fetch exit with a missing ref. Cover unsupported reftable capability, ordinary files repositories, ref changes during discovery, collision introduced during sync, interrupted initialization, and reflog-preserving migration refusal/success.
- Native Windows integration covers reftable initialization, linked worktree creation, upstream tracking, status, fetch, and recovery. Existing Linux/macOS CI remains required. Actual company Bitbucket/GitHub Enterprise access and GoLand use are maintainer validation items; do not claim private production verification from fixtures.
- Auth tests cover login/status/logout for both GitHub and Bitbucket, including public and enterprise/self-hosted variants, browser-registration absence, guided token setup, provider-specific REST/Git token schemes, organization restrictions, and verification before reporting success. Use fake browser openers and credential stores, isolated Git configuration, and TLS HTTP fixtures; do not require real accounts in CI.
- Browser/credential tests cover state and callback validation, device polling deadlines, cancellation, denied consent, secret-store failure without plaintext fallback, expiry/refresh rotation across processes, credential redaction, multiple accounts/grants on one host, environment-source precedence, host/context binding, logout, and disconnected repository references. Status tests distinguish anonymous reachability from authenticated identity and Git read access from write permission; `--local` performs no network or refresh.
- Git auth integration tests cover invocation-scoped clone routing, persisted per-remote helper settings, helper-chain conflicts, stable executable paths, ordinary Git reuse, existing SSH behavior, root-independent login, and credential repair while an import journal is unfinished. Replacing a reviewed grant's identity invalidates the preview/resume association; rotating the same grant's token does not.
- Provider HTTP fixtures cover all provider kinds, multiple pages/workspaces, context paths, SSH ports, credential routing, redirects, rate limiting, malformed/oversized/incomplete responses, and cancellation.
- App tests cover portable-ID and provider-identity conflicts, changed previews, metadata persistence, partial success, cancellation, root locking, journal failures, and resume without duplicate clones.
- TUI tests cover empty roots, saved-connection selection/shared login, targeted authentication errors, source forms, filtering and retained selection, ID editing, disabled existing/retired repositories, review-only Enter, explicit confirmation, progress/results, retry, cancellation, stale messages, narrow terminals, and monochrome rendering.
- Run the affected package suites, `go test ./...`, and `go vet ./...`; broaden testing only for new failures or material unresolved concerns. Update onboarding/help with both providers' auth commands, browser-registration prerequisites, guided token fallback, secure storage, scoped Git integration, import, and recovery workflows.

## References checked during investigation

- [Git ref storage options](https://git-scm.com/docs/git-init)
- [Git refs migration and worktree limitations](https://git-scm.com/docs/git-refs)
- [Git 2.47 ref migration limitations](https://git-scm.com/docs/git-refs/2.47.0)
- [GitHub browser and device authorization](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)
- [GitHub personal access token setup](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)
- [Bitbucket Cloud authentication, OAuth, and API tokens](https://developer.atlassian.com/cloud/bitbucket/rest/intro/)
- [Bitbucket Server/Data Center incoming OAuth application links](https://confluence.atlassian.com/bitbucketserver/configure-an-incoming-link-1108483657.html)
- [Bitbucket Data Center OAuth provider flows](https://confluence.atlassian.com/bitbucketserver096/bitbucket-oauth-2-0-provider-api-1627097966.html)
- [Bitbucket Server/Data Center HTTP access tokens](https://confluence.atlassian.com/bitbucketserver/http-access-tokens-939515499.html)
- [Git credential-helper interface](https://git-scm.com/docs/gitcredentials)
- [GitHub accessible repository listing](https://docs.github.com/en/rest/repos/repos#list-repositories-for-the-authenticated-user)
- [Bitbucket Cloud workspace discovery](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-workspaces/)
- [Bitbucket Cloud repository listing](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-repositories/)
- [Bitbucket Data Center repository search](https://developer.atlassian.com/server/bitbucket/rest/v1000/api-group-repository/)
