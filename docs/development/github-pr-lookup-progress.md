# GitHub PR lookup verification

Verified on 2026-11-12 on `feat/github-pr-lookup`, including final code commit `826aec524c96e093823512b63fa855cebc780282`. Implementation, independent review and local integration verification are complete. This change has not been published; private-enterprise validation remains maintainer-owned.

## Implemented scope and defaults

- Read-only GitHub lookup is shared by `pr list WORKSPACE` and explicit dashboard `p` / `P` actions. Open PRs are the default; the CLI also selects closed/all, repositories and target base. Actual verified worktree branches determine queries.
- Production dashboard construction creates one shared GitHub client. Its cache is process-local, credential-aware, limited to 128 observations and a 60-second TTL. Dashboard details always call observations timestamped snapshots, including retained observations while another explicit lookup runs. No background refresh/timer contacts GitHub.
- Each provider lookup has a 30-second deadline, 100-page and 8 MiB-per-response bounds, at most two transient retries, and per-API-host serialization/cooldown. Refresh bypasses successful cache entries, not rate-limit cooldowns.
- Public API credentials use `GH_TOKEN`, then `GITHUB_TOKEN`; a custom API requires explicit `token_env` to send credentials. Configuration contains variable names only. Public reads can be anonymous. Fine-grained tokens require repository access and Pull requests read permission.
- Dashboard lookup captures every loaded member, including filtered rows. It preserves local status/filter/focus/selection. Local refresh, workspace changes and opening mutation actions cancel/invalidate PR state. Request IDs reject stale completions; a cancelled run blocks repeated lookup commands until drained. Existing reader shutdown owns and drains the I/O.

## Available evidence

- Application tests use real disposable Git repositories to cover actual/intended branch drift, worktree-local remotes, target/state/selection overrides, missing/foreign worktrees, detached/unborn HEAD, cancellation and unchanged file/ref/journal snapshots. CLI tests cover complete human/JSON/JSONL output, dry-run queries, partial failures, terminal sanitization and exit 130.
- The parent built committed snapshot `fb19f2ad7b46ea84281bc0980917c14df93582ce`, after final application/CLI cancellation hardening and the adapter redirect/cooldown fix. Independent scoped review of the redirect fix passed.
- The parent reran a public GitHub smoke against that snapshot using a disposable local root: `--state all` found merged PR #4; default open returned no match; dry-run, JSONL and human output worked; all root file SHA256 hashes were unchanged. This is public read-only evidence, not private/company testing.
- The parent also ran the CLI in a real Windows PTY against that post-hardening snapshot. It rendered a complete wrapped labeled record with title, state, draft, source, target, update time, URL, observation time and cache fields, and exited 0.
- Task 3 observed behavioral RED tests before implementation: `p` scheduled no lookup, and unconfigured dashboard composition returned provider unavailable. It also caught and corrected a narrow-help regression that hid Page Up/Down.
- Task 3 `go test ./internal/tui ./internal/cli -count=1` passed. Coverage includes explicit actions, full filtered membership capture, cache bypass, local-state preservation, per-repository matching/error/skipped states, terminal-control sanitization, old snapshot labels, 36x12/60x18/90x24 layouts, long Unicode/URL scrolling, responsive repository navigation, refresh/workspace cancellation, stale-completion rejection, repeated-key bounds and quit draining. Existing TUI/CLI behavior tests remain green.
- The production dashboard composition test uses disposable Git fixtures and the real application/GitHub adapter against an isolated HTTPS server. Repeated lookup produces `cached=true` without another HTTP request; explicit refresh reads HTTP again. No personal credentials or company endpoints are used.
- Independent scoped reviews approved the adapter/domain, application/CLI and dashboard/documentation stages. Adapter review findings about equivalent HTTPS host spellings and redirect cooldown headers were reproduced in tests and fixed. Broad whole-feature review found the repository-rename case below; its scoped fix review approved the correction with no remaining findings.

## Final review fix evidence: repository redirects

- On base `5691bfe26ba5c722f081e276c9fd07be06c3af95`, isolated TLS rename/obscured-coordinate fixtures reproduced the Important finding: matching source/head PRs with a renamed target were skipped, and the second lookup returned a cached empty success. `TestRepositoryChangingRedirectFailsWithoutCaching` observed runtime RED for `/repos/upstream/renamed/pulls`, `/repositories/12345/pulls` and an opaque `/redirected` route.
- Redirects now retain the original configured PR-list path (case-insensitive repository spelling) or fail before the follow-up request with sanitized `provider_failed` remediation to update remotes/provider metadata. Failed lookups return no partial observation and are not cached. Strict source repository/branch and target/base matching remain unchanged; no canonical repository-ID lookup or extra provider request was introduced.
- `TestSameOriginCoordinatePreservingRedirectSucceeds` confirms a same-path query-only redirect still returns the matching PR and retains an explicit credential. The former arbitrary-path success fixture was narrowed because an unrelated route no longer establishes target coordinates. Cooldown fixtures now also retain the PR-list path so they independently test rate-limit enforcement.
- Covering rename/coordinate-preserving/origin/credential/cooldown command passed (`github 1.370s`): `go test ./internal/github -run 'TestRepositoryChangingRedirectFailsWithoutCaching|TestSameOriginCoordinatePreservingRedirectSucceeds|TestCrossOriginRedirectAndPaginationAreRejectedBeforeTransmission|TestCredentialsStayWithinExplicitBoundary|TestRedirectRateLimitStopsFollowupWithoutCaching' -count=1 -v`.
- Focused `go test ./internal/github ./internal/domain` passed (`github 2.896s`, domain cached); `go vet ./internal/github ./internal/domain` passed. Final-head integration checks below also passed after this fix.

## Final integration verification

- Go 1.27.4 on Windows: `go test -mod=readonly ./...` passed on final code `826aec5` (CLI 66.678s; other package results cached). The preceding complete feature run also passed, including application, process and TUI regressions.
- Go 1.27.4 on Ubuntu 24.04 WSL with CGO/GCC: `go test -race -mod=readonly ./...` passed on final code (CLI 8.760s, GitHub 2.937s; other package results cached). The preceding full feature run passed uncached app/CLI/Git/process/TUI race tests as well.
- Full `go vet ./...`, `go mod tidy -diff`, native CLI build, changed-Go-file `gofmt -l` and feature-range `git diff --check` passed.
- Final-code public CLI smoke again found merged Bearforge PR #4 with `--state all`, returned explicit no-match for open PRs, preserved dry-run/JSONL/human output, and left every disposable-root SHA256 hash unchanged.
- A real Windows PTY dashboard found open public Bubble Tea PR #1894 using actual branch `fix/exec-query-replies` while retaining visible intended-branch drift. Full metadata and URL were reachable by paging. `P` produced an uncached observation; immediate `p` reused it with `cached=true`. Local `r` cleared PR data without automatically looking it up again.
- Normal `q` showed stopping, restored the alternate screen/cursor and exited 0. A separate `P` plus Ctrl-C session restored the terminal and exited 130. Gated unit tests cover active-provider cancellation and draining; native smoke does not claim exact in-flight network timing.
- After final redirect hardening, native dashboard lookup and clean exit were repeated successfully. Every dashboard-root file hash still matched its before-state. Only public GET requests and local fixtures were used; no external repository code was checked out for execution.

## Remaining limits

- Company/private-repository and GitHub Enterprise live validation remain maintainer-owned and have not been run. Configuration examples and synthetic transport tests are not evidence of access to a company installation.
- Native macOS execution and hosted CI for this unpushed branch have not run locally.
- The optional installed `golangci-lint` was built with Go 1.26 and cannot analyze this project's Go 1.27.4 target. No replacement tool was installed; required Go tests/vet remain the available checks.
- No PR mutation, checks/reviews integration, browser opening, persistent cache, GitLab or Azure DevOps support is included. There was no push, merge, publish or deployment in this task.
