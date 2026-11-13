# Bitbucket PR lookup verification

## Delivered behavior

The existing `pr list WORKSPACE` command and dashboard `p`/`P` now dispatch between GitHub, Bitbucket Cloud and explicitly configured Bitbucket Server/Data Center. Cloud detection uses the actual `bitbucket.org` remote. Company endpoints require `kind: bitbucket-server`, with optional target/source coordinates, HTTPS API root and token environment variable. GitHub contracts remain covered by their existing tests.

Both Bitbucket dialects verify actual source/target identities, case-sensitive branches and optional base, normalize open/closed/merged/draft metadata, and produce independent per-member results in human, JSON and JSONL output. Requests are GET-only and use the established bounds: a 30-second lookup deadline, 100 pages, 8 MiB per response, two eligible transient retries, per-origin serialization/cooldown, and a copied successful cache with a 60-second TTL and 128-entry limit. Cloud and Server share one Bitbucket client/cache; GitHub has its own client/cache.

Credentials are read only from environment variables. The default Cloud API can use `BITBUCKET_TOKEN`; custom/company APIs need explicit `token_env` to receive a Bearer credential. Default GitHub/Cloud credentials are never forwarded to a company API. Existing catalog registration still rejects HTTPS userinfo; username-only HTTPS parsing applies to the actual Git remote observed during lookup. Onboarding documents the registration form and both deployment configurations.

## Automated evidence

Go 1.27.4 was used on native Windows and Ubuntu 24.04 WSL with CGO/GCC. Relevant test groups cover:

- Cloud HTTPS/SSH/SCP remotes and forks; Server HTTPS contexts/ports, SSH ports, personal projects, explicit aliases and coordinate overrides.
- Correct API paths, escaped source/base/state filters, strict fork identity, state normalization, update ordering and both pagination dialects.
- Credential isolation and rotation, shared/copy-safe cache, TTL/capacity/forced refresh, context cancellation while queued, retry limits and reset/aborted connection recovery.
- Unsafe redirects/pages, redirect cooldowns, 429/Retry-After, invalid or oversized responses, incomplete envelopes and the 100-page limit. Failed observations never become cached empty results.
- Production CLI composition for mixed GitHub/Cloud and GitHub/Server members in all output modes; dry runs perform no HTTP. Production dashboard composition reuses each provider's cache and honors forced refresh.
- Existing dashboard visible-header, lifecycle, cancellation, stale-message and shutdown regressions.

`go test -mod=readonly ./...`, `go vet ./...` and the Linux `CGO_ENABLED=1 go test -race -mod=readonly ./...` passed before the final review. The same checks passed after the final review fixes; see final verification below.

The installed golangci-lint binary was built with Go 1.26 and could not load this Go 1.27 module. An isolated v2.15.0 binary built with Go 1.27.4 reported **zero introduced issues** with `--new-from-rev=052a208`. A full-repository lint run still reports pre-existing errcheck/staticcheck findings outside this feature. The changed files also pass the tool's goimports formatting check.

## Compiled application check

A disposable Linux fixture used the compiled `cmd/bearforge` binary, two distinct local Git origins and real workspace worktrees. After setup, their actual remotes identified a Cloud fork and a personal-project Server fork. An isolated HTTPS server supplied both documented API dialects; its CA was trusted through `SSL_CERT_FILE`, without disabling certificate verification. Company authentication used a synthetic named environment credential; synthetic default Cloud/GitHub tokens were not sent to the custom Cloud API.

The compiled CLI passed human, JSON and JSONL lookup, and dry run performed no HTTP. A real 100-column by 30-row PTY dashboard showed lookup completion without scrolling, reused cached results on `p`, forced both providers through HTTP on `P`, and exited/restored the terminal with `q`. Ten HTTP requests covered the three CLI formats plus fresh/forced dashboard observations. Before/after SHA-256 hashes of all 77 files under the disposable root were identical, including worktrees, bare Git files, catalogs, workspace mappings and journals.

The smoke driver waits for the ready workspace and completed cached header before sending keys. Searching for a generic `cached` substring would also match the previous detail snapshot while another lookup is running; the completed-header synchronization avoids that driver race.

## Independent review

A fresh whole-branch review found no critical issues and two material edge cases. Permanent regressions reproduced both before their fixes:

- Explicit real provider coordinates could not override SSH/SCP aliases containing underscores. `TestResolveServerRemotesAndOverrides` now covers both alias forms; strict API DNS/path validation remains intact.
- Cloud responses advertising outstanding results but omitting `next` were accepted as empty successes. `TestLookupRejectsIncompleteAndUnsafePages` now rejects contradictory totals/page controls without caching failure; `TestCloudCompletePaginationMetadata` accepts complete valid pages. Optional pagination controls remain optional.

The review also identified ambiguous onboarding around username-only remotes. Documentation now distinguishes actual Git remote parsing from the existing username-free catalog registration requirement. There are no deferred review findings.

## Final verification and deployment limits

Final post-review verification passed at code commit `56a6b07`: Windows `go test -mod=readonly ./...` (CLI 76.087s) and `go vet ./...`; Ubuntu 24.04 CGO `go test -race -mod=readonly ./...` (Bitbucket 4.039s, CLI 10.899s); goimports formatting; whitespace checks; and zero introduced golangci-lint findings. The rebuilt Linux binary passed the complete CLI/PTY smoke check with 10 HTTP requests and all 77 fixture files unchanged. The native Windows artifact is built as `bin/bearforge.exe`.

No live private/company Bitbucket deployment or maintainer credentials were available. Company endpoint routing, context paths, pagination and authentication were verified with controlled HTTPS fixtures. This does not claim compatibility with a particular company's proxy/SSO policy or custom deployment. Native macOS and live public Cloud requests were not exercised for this change.
