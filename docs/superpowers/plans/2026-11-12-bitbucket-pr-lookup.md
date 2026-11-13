# Bitbucket Pull-Request Lookup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Support read-only Bitbucket Cloud and Server/Data Center PR lookup alongside GitHub in the CLI and dashboard.

**Architecture:** Add a Bitbucket adapter with separate coordinate, Cloud/Server response and bounded HTTP responsibilities. A small provider dispatcher owns reusable GitHub/Bitbucket clients and stamps an additive query discriminator. Existing application verification, presentation and dashboard lifetimes remain shared.

**Tech Stack:** Go 1.27.4, standard net/http, Cobra, Bubble Tea, isolated TLS/Git fixtures.

**Spec:** `docs/superpowers/specs/2026-11-12-bitbucket-pr-lookup-design.md` (approved by the user).

## Global Constraints

- GET-only lookups; no Git/catalog/workspace mutations, SDK or new dependency.
- Actual verified worktree remote/branch; exact branch and source/target repository matching.
- Kinds `github`, `bitbucket`, `bitbucket-server`; infer only public known hosts.
- HTTPS API roots, environment-only Bearer credentials; default `BITBUCKET_TOKEN` only for the default Cloud API.
- 30-second deadline, 100 pages, 8 MiB per response, two transient retries, per-origin serialization/cooldown.
- In-memory successful cache: 60-second TTL, maximum 128 observations, copied slices, credential isolation.
- Existing CLI formats and dashboard `p`/`P`, visible feedback, cancellation and local-only refresh remain intact.

## Review Focus

- HTTPS remotes with usernames, custom HTTPS ports/context paths and SSH aliases must resolve without reusing Git credentials.
- Forks sharing branch names must not match the wrong repository or base.
- Pagination must not turn a malformed/incomplete response into a cached empty success or leak credentials.
- Mixed-provider requests and refreshes must preserve provider choice and shared caches through production composition.
- Malformed matched metadata, missing page controls and cancellation while queued must produce explicit independent errors.

---

### Task 1: Bitbucket coordinate resolution

**Files:** Create `internal/bitbucket/resolve.go`, `resolve_test.go`; modify `internal/domain/pull_request.go`.

**Interfaces:** Produce `bitbucket.NewClient() *Client`, `(*Client).Resolve(domain.Repository, string, string) (domain.PullRequestQuery, error)`. Add `PullRequestQuery.Provider string` with `json:"provider,omitempty"`.

- [x] Write `TestResolveCloudRemotesAndFork`, `TestResolveServerRemotesAndOverrides`, `TestResolveRejectsUnsafeCoordinates`, covering public inference, context/port/personal project inference, complete explicit metadata and rejected passwords/traversal/unknown kind/source-host mismatch.
- [x] Run `go test ./internal/bitbucket` and observe missing adapter/behavior failures.
- [x] Implement the constructor and resolution/validation helpers. Keep transport state in `client.go`; strict shared query validation precedes lookup.
- [x] Run `go test ./internal/bitbucket ./internal/domain`; expect PASS.
- [x] Commit `feat(bitbucket): resolve Cloud and company repository coordinates`.

### Task 2: Cloud/Server API, transport and cache

**Files:** Create `internal/bitbucket/client.go`, `http.go`, `cloud.go`, `server.go`, `cache.go`, and focused `*_test.go` files.

**Interfaces:** Consume Task 1 queries. Produce `(*Client).Lookup(context.Context, domain.PullRequestQuery, bool) (domain.PullRequestObservation, error)`; the existing `app.PullRequestProvider` interface is unchanged.

- [x] Write TLS tests for each dialect's GET paths, escaped branch/base/state parameters, fork filtering, normalized metadata, descending update order and pagination. Observe failures before implementation.
- [x] Implement dialect-specific pages and bounded GET transport, sanitized errors and environment credentials; malformed or incomplete pages never return success.
- [x] Write/observe failing tests for unsafe page/redirect paths, credential isolation/rotation, 429/Retry-After including redirect responses, cancellation/queueing, transient retry bounds, invalid metadata/envelopes and oversized bodies.
- [x] Implement those transport guards, per-origin request state and successful cache with TTL/capacity/copy tests watched failing before their implementations.
- [x] Run `go test ./internal/bitbucket ./internal/github`; expect PASS with unchanged GitHub contracts.
- [x] Commit `feat(bitbucket): add bounded read-only pull request lookup`.

### Task 3: Production dispatch and CLI/dashboard integration

**Files:** Create `internal/pullrequests/provider.go`, `provider_test.go`, `internal/cli/bitbucket_pull_requests_test.go`; modify `internal/cli/root.go`, `dashboard.go`, `dashboard_pull_requests_test.go`.

**Interfaces:** Produce `pullrequests.NewProvider() *Provider` implementing `app.PullRequestProvider`; explicit kind wins, unknown kind fails, empty lookup discriminator remains GitHub-compatible. Consumers share one provider instance with its concrete clients/cache.

- [x] Add failing production-composition tests: mixed GitHub/Cloud/Server actual worktrees through CLI human/JSON/JSONL, default dashboard dispatch/cache and forced refresh, and dry-run zero HTTP.
- [x] Implement dispatcher, optional query discriminator and service defaults; change provider-specific help to include Bitbucket.
- [x] Test explicit-kind precedence and unknown-host refusal through the real dispatcher; ensure forwarded queries cannot silently switch dialects.
- [x] Run `go test ./internal/pullrequests ./internal/cli ./internal/tui ./internal/app`; expect PASS, including visible feedback/cancellation regressions.
- [x] Commit `feat(pr): route GitHub and Bitbucket lookup in CLI and dashboard`.

### Task 4: Documentation, native verification and review

**Files:** Modify `README.md`, `docs/onboarding.md`; create `docs/development/bitbucket-pr-lookup-progress.md`.

**Interfaces:** Document existing commands plus Cloud/default token and explicit Server/fork/context/API/token-env configuration; no new flags.

- [x] Document concrete catalog examples, read scopes, public/private support, authentication boundaries and error remediation.
- [x] Run `gofmt`, `git diff --check`, `go test -mod=readonly ./...`, `go vet ./...`, native build and the available Linux CGO race suite. Expect PASS; record exact limits if a platform/tool is unavailable.
- [x] Exercise compiled CLI and dashboard against controlled TLS Bitbucket fixtures using disposable Git roots. Verify visible `p`/`P` feedback and that lookup leaves files/refs/catalogs unchanged.
- [x] Commit documentation/verification, then request one fresh whole-branch review against the spec and Review Focus. Fix material findings with reproducing failing tests and verify the affected/full suites.
- [x] Leave the completed feature branch available in this checkout; publishing/merging is a separate user action.
