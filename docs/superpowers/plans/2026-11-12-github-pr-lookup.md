# GitHub PR Lookup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task by task.

**Goal:** Ship read-only GitHub PR lookup in the CLI, then the dashboard.

**Architecture:** A standard-library HTTP adapter implements a consumer-owned application interface. One application use case observes verified actual worktree branches; CLI and explicit dashboard actions share it. Cache lives only inside the provider adapter.

**Tech Stack:** Go 1.27.4, net/http, installed Git, Cobra, Bubble Tea v2, existing Bubbles/Lip Gloss rendering.

**Spec:** `docs/superpowers/specs/2026-11-12-github-pr-lookup-design.md`

## Global Constraints

- Read-only: no Git fetch/checkout/ref mutation, locks, journals or PR-cache filesystem writes.
- Go 1.27.4; no new dependencies; existing human/JSON/JSONL and cancellation contracts.
- GitHub first; actual branch and source repository identity determine matches.
- Credentials are environment-only; default public-GitHub tokens never reach custom hosts.
- 30-second lookup deadline; 100 pages; 8 MiB per response; two transient retries; serial requests per API host.
- Memory cache: 60-second TTL, maximum 128 observations, credential-aware and defensively copied.
- Dashboard network lookup is explicit; stale replies are discarded; cancellation drains I/O.

## Review Focus

- Forks with the same branch name in different repositories must never cross-match.
- Custom API URLs, redirects, pagination links and error bodies must never leak credentials.
- A page failure, offline request or rate limit must not be rendered as no PRs.
- A branch/workspace switch while a lookup is active must invalidate/cancel old results.
- Narrow terminals and external titles must retain complete readable values without executing terminal controls.

### Task 1: Domain contracts and GitHub adapter

**Files:** Create `internal/domain/pull_request.go`; create `internal/github/{client,resolve,cache}.go` and focused `*_test.go` files (implementation may split HTTP helpers further by responsibility).

**Interfaces:** Produces the three domain types and `github.NewClient`, `Client.Resolve`, `Client.Lookup` exactly as specified in Shared interfaces. No app dependency. Error codes: `invalid`, `unsupported_provider`, `unauthenticated`, `unauthorized`, `rate_limited`, `offline`, `provider_failed`.

- [x] Write tests for remote inference/overrides, fork/branch identity, page accumulation and normalized metadata; observe missing behavior.
- [x] Implement the minimal domain DTOs, resolver and paginated GET client. Support public GitHub plus explicit enterprise metadata.
- [x] Add failing regression tests for credential boundaries, redirect/pagination origins, rate-limit cooldown, timeout/cancellation, bounded retries/size/pages and cache credential/expiry/copy behavior; implement them.
- [x] Verify `go test ./internal/github ./internal/domain` and `go vet ./internal/github ./internal/domain` pass.
- [x] Self-review and record exact RED/GREEN evidence; commit scoped changes using Conventional Commits.

### Task 2: Application service and CLI

**Files:** Modify `internal/app/service.go`, `internal/cli/{root,commands}.go`; create `internal/app/pull_requests.go`, `internal/app/pull_requests_test.go`, `internal/cli/pull_requests.go`, `internal/cli/pull_requests_output.go`, focused CLI tests. Use existing fixture/rendering helpers.

**Interfaces:** Consumes Task 1 DTOs and `github.NewClient`. Produces `app.PullRequestProvider`, `Service.PRs`, `app.PRRequest`, `Service.PullRequests` and `Item.PullRequests` exactly as specified. Wire a client per command service; preserve all existing interfaces and command behavior.

- [x] Write failing app tests for actual branch drift, actual remote, PR target override, mixed outcomes, detached/unborn/missing members, selectors before network, cancellation and read-only snapshots.
- [x] Implement the service using existing repository/worktree identity verification, sorted selected members and independent failure accumulation. Do not route through mutation locks or journals. Dry-run plans queries only.
- [x] Write CLI tests showing missing `pr list`, then wire `--repo`, `--base`, `--state`, `--refresh` and standard outputs. Use injectable provider seams in package-local options instead of global HTTP state.
- [x] Add human table/wrapped records with repository, actual/intended branch, PR metadata/URL, freshness, explicit empty/skipped/error states, and terminal sanitization. JSON/JSONL preserve structured observations and existing envelopes.
- [x] Verify `go test ./internal/app ./internal/cli`, `go vet ./internal/app ./internal/cli`; record evidence and commit scoped changes.

### Task 3: Dashboard integration and user documentation

**Files:** Modify `internal/cli/dashboard.go`, `internal/tui/{dashboard,dashboard_view,dashboard_layout}.go`; create `internal/tui/dashboard_pull_requests.go` and tests; update `README.md`, `docs/onboarding.md`; create `docs/development/github-pr-lookup-progress.md`.

**Interfaces:** Consumes `Service.PullRequests`. Adds optional `tui.DashboardPullRequester { PullRequests(context.Context, app.PRRequest) (app.Result,error) }`, preserving existing DashboardSource compatibility. Production dashboard receives one shared GitHub client.

- [x] Write failing tests proving startup/r stay local, p includes filtered members, P refreshes, partial/no-match/error states render distinctly and title controls are sanitized.
- [x] Implement cancellable, captured-workspace p/P commands tracked by the existing reader, request IDs and shutdown drain. Cancel/invalidate on local refresh/workspace change; reject stale completions. No new modal or automatic network refresh.
- [x] Append selected-repository PR observations to scrollable details with freshness/cached labels; expose discoverable keys and retain small-terminal layout bounds and full detail scrolling.
- [x] Test cancellation/navigation/stale messages, quit draining and narrow/Unicode/long URL rendering, plus CLI adapter wiring.
- [x] Document public/enterprise/fork metadata, environment credentials and permissions, CLI outputs/error semantics, cache/refresh behavior and explicit dashboard actions. Record exact verification, limitations and defaults.
- [x] Verify focused TUI/CLI tests; commit scoped changes.

### Task 4: Whole-change verification and review

**Files:** Review the whole feature branch; update the progress record with evidence/fixes only.

- [x] Run full `go test ./...`, `go vet ./...`, native CLI build and whitespace/module checks; run Linux race tests if the verified local WSL toolchain is available.
- [x] Exercise a disposable-root CLI lookup and dashboard action with terminal cancellation. Public GitHub GET smoke tests are allowed; no real-company roots or credentials are changed.
- [x] Obtain one broad independent review of the complete change, fix material findings with reproducing tests, and verify scoped fixes.
- [x] Leave the completed local feature branch reviewable. No remote push, PR creation, merge, version tag or publication is requested.
