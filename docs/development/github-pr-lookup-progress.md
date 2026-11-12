# GitHub PR lookup verification

Status recorded on 2026-11-12. This document records the evidence available at the Task 3 handoff; it is not a claim that release or private-enterprise validation has completed.

## Implemented scope and defaults

- Read-only GitHub lookup is shared by `pr list WORKSPACE` and explicit dashboard `p` / `P` actions. Open PRs are the default; the CLI also selects closed/all, repositories and target base. Actual verified worktree branches determine queries.
- Production dashboard construction creates one shared GitHub client. Its cache is process-local, credential-aware, limited to 128 observations and a 60-second TTL. Dashboard details always call observations timestamped snapshots, including retained observations while another explicit lookup runs. No background refresh/timer contacts GitHub.
- Each provider lookup has a 30-second deadline, 100-page and 8 MiB-per-response bounds, at most two transient retries, and per-API-host serialization/cooldown. Refresh bypasses successful cache entries, not rate-limit cooldowns.
- Public API credentials use `GH_TOKEN`, then `GITHUB_TOKEN`; a custom API requires explicit `token_env` to send credentials. Configuration contains variable names only. Public reads can be anonymous. Fine-grained tokens require repository access and Pull requests read permission.
- Dashboard lookup captures every loaded member, including filtered rows. It preserves local status/filter/focus/selection. Local refresh, workspace changes and opening mutation actions cancel/invalidate PR state. Request IDs reject stale completions; a cancelled run blocks repeated lookup commands until drained. Existing reader shutdown owns and drains the I/O.

## Available evidence

- Application and CLI package tests and vet passed during Tasks 1/2. The parent ran an app-focused Linux race test successfully before the final single-member cancellation hardening. That earlier race run does not validate the later hardening or completed dashboard integration.
- The parent built committed snapshot `fb19f2ad7b46ea84281bc0980917c14df93582ce`, after final application/CLI cancellation hardening and the adapter redirect/cooldown fix. Independent scoped review of the redirect fix passed.
- The parent reran a public GitHub smoke against that snapshot using a disposable local root: `--state all` found merged PR #4; default open returned no match; dry-run, JSONL and human output worked; all root file SHA256 hashes were unchanged. This is public read-only evidence, not private/company testing.
- The parent also ran the CLI in a real Windows PTY against that post-hardening snapshot. It rendered a complete wrapped labeled record with title, state, draft, source, target, update time, URL, observation time and cache fields, and exited 0.
- Task 3 observed behavioral RED tests before implementation: `p` scheduled no lookup, and unconfigured dashboard composition returned provider unavailable. It also caught and corrected a narrow-help regression that hid Page Up/Down.
- Task 3 `go test ./internal/tui ./internal/cli -count=1` passed. Coverage includes explicit actions, full filtered membership capture, cache bypass, local-state preservation, per-repository matching/error/skipped states, terminal-control sanitization, old snapshot labels, 36x12/60x18/90x24 layouts, long Unicode/URL scrolling, responsive repository navigation, refresh/workspace cancellation, stale-completion rejection, repeated-key bounds and quit draining. Existing TUI/CLI behavior tests remain green.
- The production dashboard composition test uses disposable Git fixtures and the real application/GitHub adapter against an isolated HTTPS server. Repeated lookup produces `cached=true` without another HTTP request; explicit refresh reads HTTP again. No personal credentials or company endpoints are used.
- Task 3 `go vet ./internal/tui ./internal/cli` and `git diff --check` passed. This task performed a scoped author self-review; whole-feature independent review remains a separate parent step.

## Final review fix evidence: repository redirects

- On base `5691bfe26ba5c722f081e276c9fd07be06c3af95`, isolated TLS rename/obscured-coordinate fixtures reproduced the Important finding: matching source/head PRs with a renamed target were skipped, and the second lookup returned a cached empty success. `TestRepositoryChangingRedirectFailsWithoutCaching` observed runtime RED for `/repos/upstream/renamed/pulls`, `/repositories/12345/pulls` and an opaque `/redirected` route.
- Redirects now retain the original configured PR-list path (case-insensitive repository spelling) or fail before the follow-up request with sanitized `provider_failed` remediation to update remotes/provider metadata. Failed lookups return no partial observation and are not cached. Strict source repository/branch and target/base matching remain unchanged; no canonical repository-ID lookup or extra provider request was introduced.
- `TestSameOriginCoordinatePreservingRedirectSucceeds` confirms a same-path query-only redirect still returns the matching PR and retains an explicit credential. The former arbitrary-path success fixture was narrowed because an unrelated route no longer establishes target coordinates. Cooldown fixtures now also retain the PR-list path so they independently test rate-limit enforcement.
- Covering rename/coordinate-preserving/origin/credential/cooldown command passed (`github 1.370s`): `go test ./internal/github -run 'TestRepositoryChangingRedirectFailsWithoutCaching|TestSameOriginCoordinatePreservingRedirectSucceeds|TestCrossOriginRedirectAndPaginationAreRejectedBeforeTransmission|TestCredentialsStayWithinExplicitBoundary|TestRedirectRateLimitStopsFollowupWithoutCaching' -count=1 -v`.
- Focused `go test ./internal/github ./internal/domain` passed (`github 2.896s`, domain cached); `go vet ./internal/github ./internal/domain` passed. Parent final-head integration verification follows separately.

## Pending and maintainer-owned checks

- Parent final whole-feature tests, full Linux race run, native build and dashboard PTY/UI smoke are pending at this handoff. Unit/model layout tests do not replace interactive terminal validation.
- Company/private-repository and GitHub Enterprise live validation remain maintainer-owned and have not been run. Configuration examples and synthetic transport tests are not evidence of access to a company installation.
- The optional installed `golangci-lint` was built with Go 1.26 and cannot analyze this project's Go 1.27.4 target. No replacement tool was installed; required Go tests/vet remain the available checks.
- No PR mutation, checks/reviews integration, browser opening, persistent cache, GitLab or Azure DevOps support is included. There was no push, merge, publish or deployment in this task.
