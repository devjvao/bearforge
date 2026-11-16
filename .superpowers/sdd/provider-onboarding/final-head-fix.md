# Final integration fix: synthetic remote HEAD

## Finding and root cause

Fresh initialization verified fetched direct refs, then ran `remote set-head origin -a`. On a case-insensitive files backend this replaced a legitimate `origin/head` ref, and an exact `origin/HEAD` branch was replaced on either backend. `HEAD/child` also prevented the synthetic alias and left default-base lookup unresolved.

## Change

- Inspect the actual retained namespace/backend before creating the optional symbolic `origin/HEAD`. Skip the alias when it would replace a direct ref or overlap its case/prefix namespace.
- Keep default-base lookup read-only. When the alias was suppressed by a real ref, map the bare clone's retained HEAD to an exactly verified origin tracking ref.
- Configure branch remote/merge settings by literal ref name. Git's branch porcelain interprets `HEAD` as the current branch even when `refs/heads/HEAD` is a distinct direct ref.

## Verification

- RED: `go test ./internal/gitcli -run '^TestFreshClone' -count=1` failed for `head`, `HEAD`, and `HEAD/child`. The first two lost their distinct tracking OIDs and subsequent fetch refused the synthetic alias; the last lost default-base lookup.
- GREEN: the initial fresh-clone regressions passed after conditional alias creation/default-base fallback. Existing case-distinct and symbolic-destination coverage passed in the focused 73.876s run.
- RED: adding literal upstream assertions failed for `HEAD` with an empty `branch.HEAD.merge`, proving the porcelain ambiguity.
- Final coverage additionally includes explicit reftable clone with a literal `HEAD` branch, continued fetch, and ordinary unoccupied symbolic alias behavior.
- `go vet ./internal/gitcli` and scoped `git diff --check` passed.
- `go test ./internal/gitcli -count=1` passed (204.521s), including literal upstream configuration, explicit reftable HEAD preservation, and subsequent fetch.

Validation runs use disposable repositories, isolated Git configuration, and Git 2.47.0.windows.1 on the native case-insensitive Windows filesystem. Linux/macOS runtime validation remains for CI; the reftable test skips with a clear reason when installed Git lacks that capability. Root owns the final integrated full-suite run.
