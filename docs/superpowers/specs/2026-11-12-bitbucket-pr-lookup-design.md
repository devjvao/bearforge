# Bitbucket pull-request lookup

## Intent and scope

Extend the existing read-only PR lookup to Bitbucket while retaining GitHub support. The user asked to implement Bitbucket lookup and said repositories could be public or belong to a private company. Cover Bitbucket Cloud (`bitbucket.org`, including private repositories) and explicitly configured company-hosted Bitbucket Server/Data Center. Cover the existing CLI first, then its dashboard composition. Continue using Go, the standard HTTP library, Cobra and Bubble Tea; no SDK or new dependency is needed.

This is a feature with a written design. The deployment coverage and defaults below are the implementer's interpretation of that request, subject to the user's design approval.

## User behavior

Use the existing `bearforge pr list WORKSPACE` command, repeatable `--repo`, `--base`, `--state open|closed|all`, `--refresh`, dry runs, human/JSON/JSONL formats and independent repository outcomes. Mixed GitHub/Bitbucket workspaces work in one invocation. Resolve the actual default Git remote and actual worktree branch only after the existing identity checks; preserve intended-versus-actual branch reporting. Detached/unborn worktrees remain explicit skipped results.

Dashboard `p` and `P` use the same provider dispatch and process-local cache. Keep the visible progress/result header, complete workspace scope, detail snapshots, forced refresh, cancellation and stale-response handling. Startup, navigation and local refresh remain local observations. Update GitHub-only help and onboarding text to describe the supported providers. No extra provider-specific keys or panels are required.

Lookup never creates, edits, approves or merges PRs, fetches Git refs, switches branches, pushes, or changes catalogs/workspace mappings. Authentication remains separate from Git authentication. No login/browser flow, OS credential-store integration, persistent PR cache, custom TLS bypass or company SSO login is included.

## Provider routing and coordinates

Introduce a small provider dispatcher implementing `app.PullRequestProvider`. Construct one dispatcher with reusable GitHub and Bitbucket clients per CLI/dashboard service. Explicit `provider.kind` takes precedence: `github`, `bitbucket` (Cloud), or `bitbucket-server` (Server/Data Center). With no kind, recognize only the actual remote hosts `github.com` and `bitbucket.org`; unknown hosts return the existing per-repository unsupported-provider error rather than guessing a company service.

Add an optional `provider` discriminator to `domain.PullRequestQuery` so lookup dispatch remains unambiguous across custom API origins. Existing GitHub callers/queries with an empty discriminator retain GitHub semantics; the dispatcher stamps resolved queries with their kind. Keep the existing provider-independent PR/result structure and JSON fields, with this additive optional query field.

Reuse catalog metadata keys `host`, `repository`, `source_repository`, `api_url` and `token_env`. `repository` is the PR target; `source_repository` is the actual head repository, inferred from the verified remote when possible. Require an explicit source when its remote host differs from the configured provider host or its remote coordinates cannot be inferred. Fork matches verify both repository identities and both branch names; branch names are case-sensitive, repository coordinates compare without case sensitivity.

Cloud coordinates use `workspace/repository-slug`. Recognize HTTPS, SSH URL and SCP-style `bitbucket.org` Git remotes. The default API root is `https://api.bitbucket.org/2.0`. A fork may configure an upstream target through `repository` without repeating its inferred source.

Server/Data Center coordinates use `PROJECT/repository-slug`, including personal-project keys such as `~username`. Recognize explicitly selected Server remotes in HTTPS `/scm/PROJECT/repo.git` form (including an installation context before `/scm`), SSH `ssh://git@HOST:7999/PROJECT/repo.git` and SCP-style `git@HOST:PROJECT/repo.git`. HTTPS remote inference retains its HTTPS port and installation context for the default API root; otherwise default to `https://HOST/rest/api/1.0`. SSH ports do not imply HTTP ports. `api_url` overrides inference for proxies, context paths and separate API hosts, and denotes the full REST API root, for example `https://git.company.example/bitbucket/rest/api/1.0`. Explicit complete metadata also supports SSH aliases. No endpoint probing is needed.

Validate hosts, ports, repository components, branches, environment variable names and API paths before HTTP. Accept a username-only HTTPS Git remote, a normal Bitbucket clone form, but never extract it as API credentials; reject passwords, control characters and unsafe URL/path components. API roots require HTTPS and reject userinfo, query, fragments, traversal and encoded controls. Do not relax existing GitHub coordinate or credential validation.

## Credentials

Bitbucket requests use Bearer authentication. Current Atlassian documentation supports Bearer API tokens for Cloud and HTTP access tokens for Server/Data Center. Support anonymous reads where the endpoint allows them. Use `BITBUCKET_TOKEN` only for the exact default Cloud API root and Cloud host. An explicit `token_env` selects a credential for any configured Bitbucket API; an unset or empty explicitly named variable returns an unauthenticated result. A company endpoint without `token_env` is anonymous and never receives a default Cloud or GitHub token.

Document Cloud `read:pullrequest:bitbucket` API-token scope (or the corresponding access-token pull-request read permission) and Server/Data Center repository read permission. Tokens remain environment values, never catalog literals, URLs, CLI flags, logs, journals or returned query fields. Validate token header format and keep HTTP errors sanitized. Fingerprint credentials privately for cache isolation and read the environment again for each lookup.

## API behavior and normalization

Cloud: GET `/repositories/{workspace}/{repo}/pullrequests` under its API root, request up to 100 entries per page, sort by newest update, and use an escaped `q` filter for actual source branch and optional destination branch. `open` requests `OPEN`; `closed` requests the terminal states `MERGED`, `DECLINED` and `SUPERSEDED`; `all` requests those states plus `OPEN`, using repeated `state` query parameters. Follow the response's `next` URL, resolving it against the response URL and validating it before sending. Read PR ID, title, `links.html.href`, state, optional draft, `source`/`destination` repository `full_name` and branch `name`, and `updated_on`.

Server/Data Center: GET `/projects/{project}/repos/{repo}/pull-requests` under its API root with `direction=INCOMING`, `limit=100`, `start=0`, `order=NEWEST` and `state=OPEN` or `ALL`. Closed queries use `ALL` and discard open results locally. If a base is requested, use `at=refs/heads/BASE`; still verify every returned target branch. Filter the actual source branch and repository locally. Read ID, title, `links.self[].href`, state, optional draft, `fromRef`/`toRef` repository project key and slug, full `refs/heads/...` IDs, and millisecond `updatedDate`. Paginate with `isLastPage` and the returned `nextPageStart`, never by guessing `start + size`; reject missing, repeated, negative or non-advancing next starts.

Normalize `OPEN` to `open`, `MERGED` to `merged`, and declined/superseded terminal states to `closed`. Existing `closed` means non-open, including merged PRs. Omitted draft metadata means false. Missing/deleted repository identity cannot establish a match. Validate matched metadata; an invalid envelope, malformed matched PR or incomplete pagination returns an error, never a fresh empty success. Sort the final observations by update time descending with stable ID ordering for ties.

## Bounded HTTP, cache and error behavior

Implement Bitbucket transport in `internal/bitbucket`, retaining the established GitHub policies without a broad GitHub transport rewrite. Small internal helpers may be shared if this reduces duplication without changing existing contracts; layout and factoring are builder decisions.

Use cancellable GETs only, a 30-second total lookup deadline, serial requests per normalized API origin, at most 100 pages, 8 MiB per response and two transient retries for eligible network/502/503/504 failures. Respect `429` and `Retry-After` with a shared origin cooldown, including on redirect responses and when following pages. Forced refresh bypasses cache, never cooldown. Use sanitized existing error codes (`invalid`, `unsupported_provider`, `unauthenticated`, `unauthorized`, `offline`, `rate_limited`, `provider_failed`) and preserve context cancellation.

Pagination and redirects must retain HTTPS, the configured API origin and the exact repository list path; refuse cross-origin requests, credentials in links, repository-changing redirects and ambiguous/repeated pagination. Never forward credentials to another origin. Same-origin redirect rate-limit headers are observed before any follow-up. Failures return no partial observation and are not cached.

Maintain at most 128 successful in-memory observations per Bitbucket client with a 60-second TTL, keyed by the full query (including provider kind) and credential fingerprint. Copy returned slices, expose `observed_at` and `cached`, and share client instances throughout dashboard requests. No on-disk format/schema migration is needed.

## Verification

Use isolated TLS HTTP fixtures and disposable local Git/workspace fixtures, without personal credentials or writes to company repositories. Cover both dialects' remotes, explicit overrides/context paths/ports/personal projects, source-versus-target fork identity, escaped branches, base/state filters and normalization, two-page responses, pagination failures, auth isolation, credentials changing between reads, cache expiry/copying/forced refresh, cancellation, bounded retries, cooldowns, redirects and malformed/oversized responses.

Exercise the real dispatcher through production CLI and dashboard composition, not only injected providers. Cover mixed providers, explicit unknown kinds, dry runs making no HTTP requests, human/JSON/JSONL output, visible dashboard lookup/result feedback and retained shared cache. Keep existing GitHub and dashboard lifecycle regression coverage. Verify relevant tests first, then the full Go suite, vet, native build and Linux race suite. Exercise the compiled CLI and dashboard with controlled Bitbucket fixtures; live anonymous public Cloud lookup is optional if a suitable public repository is available. Document that live private-company deployment/authentication is not verified without a supplied endpoint and authorized credentials.

## Primary references checked during design

- [Bitbucket Cloud pull-request API](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/)
- [Cloud filtering and pagination](https://developer.atlassian.com/cloud/bitbucket/rest/intro/)
- [Cloud API tokens, including Bearer authentication](https://support.atlassian.com/bitbucket-cloud/docs/using-api-tokens/)
- [Bitbucket Data Center pull-request API](https://developer.atlassian.com/server/bitbucket/rest/v1005/api-group-pull-requests/)
- [Data Center HTTP access tokens](https://confluence.atlassian.com/bitbucketserver093/http-access-tokens-1472430864.html)
