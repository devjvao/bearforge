# GitHub pull-request lookup

## Intent and authorization

Implement the user's approved Phase 3 scope: GitHub read-only PR lookup in the CLI first, then the dashboard. Keep the existing Go/hexagonal architecture, installed Git adapter, Cobra, Bubble Tea and terminal presentation. No provider SDK, daemon or new dependency is needed.

## User behavior

`bearforge pr list WORKSPACE` lists open pull requests for the actual branch of each workspace member. Repeatable `--repo` selects members; `--base` overrides the mapping's `pr_target`; `--state open|closed|all` defaults to open. `--refresh` bypasses the short cache. Existing human, JSON and JSONL output contracts apply. Every repository gets an explicit result, including no matches, detached/unborn HEAD, configuration errors and provider failures. Branch drift remains visible. An empty workspace succeeds. Invalid selectors are refused before network requests. Dry runs resolve queries without contacting GitHub.

The dashboard gains `p` to look up PRs for the entire loaded workspace, including filtered-out members; `P` bypasses the cache. The selected repository's scrollable details show results, freshness and errors. Local refresh and ordinary startup/navigation do not contact GitHub. Workspace/local-status changes cancel and invalidate the current lookup. Request IDs prevent stale completions from replacing current observations. Quit and cancellation drain active requests through the existing reader lifecycle. Existing mutation workflows and key bindings continue to work.

## Provider coordinates and credentials

Read the actual configured default Git remote after repository/worktree identity checks. Recognize github.com HTTPS, SSH URL and SCP-style remotes. Explicit repository `provider` metadata takes precedence: `kind: github`, `host`, `repository: owner/name` (the PR target), `source_repository: owner/name` (the head repository when needed), `api_url` and `token_env`. For a fork, infer the source from its actual remote and configure the upstream as `repository`. Unknown hosts require explicit GitHub configuration. Enterprise defaults to `https://HOST/api/v3`; github.com defaults to `https://api.github.com`. Provider URL/host/repository/environment-name values are strictly validated. No schema migration is required: the catalog already preserves provider maps and workspace `pr_target`.

Use `GH_TOKEN`, then `GITHUB_TOKEN`, only for the default public GitHub API. An explicit `token_env` chooses a credential for an enterprise/custom API; an unset explicitly requested variable produces an unauthenticated error. Anonymous public reads are supported. Never copy a default GitHub token to another host. No tokens in configuration, journal, CLI arguments, cache keys exposed to callers, logs or error messages. No login flow or credential-store integration in this slice.

## API, bounded work and freshness

Use the GitHub REST list-PR endpoint, head owner/actual branch, optional base, requested state, 100 entries per page and updated-descending order. Validate every returned head repository/branch and target repository/base; matching a branch string alone cannot identify a fork PR. Normalize number, title, URL, open/closed/merged state, draft, source/target repositories and branches, update time. Nil/deleted source repositories cannot establish a match.

Use cancellable GET requests only, a 30-second total lookup deadline, serial requests per API host, no more than 100 pages or 8 MiB per response, and bounded transient retries (two retries for eligible network/502/503/504 failures). Honor rate-limit headers with a per-host cooldown and a rate-limited result; never immediately retry during Retry-After/reset. Follow pagination and redirects only within the configured HTTPS API origin; reject cross-origin redirects/links without transmitting credentials. Error text uses safe status/remediation, never arbitrary response bodies or request URLs containing secrets.

Use an in-memory cache with 60-second TTL and at most 128 observations, keyed by full query and credential identity. Never write PR lookup state to disk. Copy cached slices so callers cannot mutate shared observations. Failed reads cannot become fresh empty results. Expose observed_at and cached in all presentations; expired observations are not silently shown as current.

## Shared interfaces

Domain types in `internal/domain/pull_request.go`:

- `PullRequestQuery { Host, APIURL, Repository, SourceRepository, Branch, Base, State, TokenEnv string }` with snake_case JSON tags.
- `PullRequest { Number int; Title, URL, State string; Draft bool; SourceRepository, TargetRepository, SourceBranch, TargetBranch string; UpdatedAt time.Time }` with snake_case JSON tags.
- `PullRequestObservation { Query PullRequestQuery; PullRequests []PullRequest; ObservedAt time.Time; Cached bool }` with snake_case JSON tags. Successful empty lists serialize as `[]`.

`internal/github.NewClient() *Client` implements the consumer-owned `app.PullRequestProvider` interface: `Resolve(domain.Repository, string /* actual remote URL */, string /* actual branch */) (domain.PullRequestQuery, error)` and `Lookup(context.Context, domain.PullRequestQuery, bool /* refresh */) (domain.PullRequestObservation, error)`.

Application additions: `Service.PRs PullRequestProvider`, `PRRequest { Workspace string; Repositories []string; Base, State string; Refresh, DryRun bool }`, `Service.PullRequests(context.Context, PRRequest) (Result, error)` and optional `Item.PullRequests *domain.PullRequestObservation` (`json:"pull_requests,omitempty"`). Item outcomes are `found`, `none`, `detached`, `unborn`, `planned`, `failed`; errors remain per-repository and independent work continues. Top-level partial results have a nonzero exit; cancellation remains 130. No lock, journal, fetch or filesystem writes occur.

## Verification and limits

HTTP tests use isolated httptest servers/transports with no personal credentials. Test fork identity, branch escaping, pagination, closed/merged state, explicit enterprise coordinates, credential isolation, redirects, rate limits, cancellation, retry bounds, malformed/oversized responses and cache expiry/copying. App tests include actual/intended branch drift, verified worktree scope, detached/missing worktrees, mixed failures, selectors and a before/after filesystem/ref/journal snapshot. CLI tests cover human/JSON/JSONL and terminal sanitization. Dashboard tests cover explicit action, stale replies, navigation/refresh cancellation, shutdown draining and narrow layouts. Run full tests, vet, native build and Linux race tests when the available local toolchain permits them.

The implementation choices above are the implementer's defaults within the approved scope. Azure DevOps/GitLab, PR mutation, browser launching, checks/reviews and persistent caching are later work. Company/private-repository testing remains a maintainer-owned exercise; this task may use public read-only smoke requests and disposable local Git fixtures.

## Primary references

- https://docs.github.com/en/rest/pulls/pulls
- https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
- https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
