# OCR branch review remediation implementation plan

> **For agentic workers:** Use superpowers:subagent-driven-development or superpowers:executing-plans. Preserve file ownership between parallel tasks and verify the integrated tree only after implementation is stable.

**Goal:** Remediate all 14 validated findings from `.tmp/ocr-provider-onboarding-verified-review.md` on the existing `feat/provider-onboarding` branch.

**Architecture:** Keep existing authentication, journal, CLI, and TUI boundaries. Fix Git fetch selection/verification without weakening case-distinct or symbolic-ref safety; make registry publication failure-aware; bind UI choices and prompts to their current source and lifecycle. Reuse existing confirmation/progress adapters and safe repository paths.

**Tech stack:** Go 1.27.4, Git 2.40+ with capability-gated newer features, Cobra, Bubble Tea/Bubbles, native OS secret stores, existing catalog and journal adapters.

**Spec:** The user approved remediation of the 14 numbered findings in the verified OCR report for `main..d0cc00d`. The requirements below preserve that numbered scope; unsupported executable relocation and unverified username-bearing provider links remain outside it.

## Constraints and review focus

- Stay in the current checkout on `feat/provider-onboarding`; preserve unrelated work and do not commit or push without a request.
- Do not change real user credentials, repository bindings, refs, or worktrees while implementing or testing. Use isolated fixtures and rebuild the executable after validation.
- Preserve the Git 2.40 floor, exact ref spelling, collision refusal, dangling symbolic refs, unrelated helpers, and safe tag/refspec behavior.
- Keep custom provider/API/Git endpoint validation strict; only the actual canonical Bitbucket Cloud pair receives its documented SSH-host exception.
- Confirm before a progress UI starts reading input; preserve JSON/JSONL shapes and explicit credential-source precedence.
- Distinguish failed publication from uncertain durability after publication, retaining every possibly referenced secret generation.
- Preserve source-compatible selection reloads, normal typing/AltGr, worker cancellation/draining, and workspace selection when manifests remain.

## Task A: Git fetching (findings 6, 7, 8)

**Own:** `internal/gitcli/client.go`, `ref_storage.go`, and focused Git tests.

- [x] Reproduce remote branch deletion/creation races. Verify the actual fetched state or reconcile advertisement changes with bounded retry rather than requiring the union of unrelated snapshots.
- [x] Exclude `remote.<name>.skipFetchAll=true` from planning and transfer expectations using Git's effective boolean semantics; verify an unavailable skipped remote is not contacted.
- [x] Remove per-existing-ref subprocess cost: reuse inventory/batch inspection while retaining detection of dangling and case-aliased symbolic refs. Verify process-count scaling rather than a timing threshold.
- [x] Run the Git package and relevant app ref-format tests; review changed Git behavior and minimum-version compatibility.

## Task B: Credential resolution and storage (findings 9, 10, 14)

**Own:** `internal/auth/resolve.go`, `store.go`, `store_unix.go`, any necessary publication helper/platform signature, and focused auth tests.

- [x] Exercise `Manager.Resolve` with hostname-only metadata plus a saved port/context API scope; retain matching without changing omitted public-provider defaults or ambiguity handling.
- [x] Sync the containing directory on Unix before old-secret cleanup. Represent post-rename failure so both old/new generations are retained whenever either registry version may survive.
- [x] Apply one serialized registry size limit to writes and reads; reject oversized publication without corrupting the old registry or deleting its secret.
- [x] Test old/new secret retention at pre-publication and post-publication failure boundaries, replacement and logout behavior, and cross-compile platform variants.

## Task C: TUI state and prompts (findings 3, 4, 5, 13)

**Own:** import controller/login/view and operations controller files under `internal/tui`, their focused tests, and a minimal compatible dashboard refresh extension if needed.

- [x] Reset provider-dependent API/Git URL and username when switching provider after relogin.
- [x] Bind import choices to a successful source observation; changed/failed sources must not silently preview old choices. Preserve IDs/selections for compatible reloads.
- [x] Permit paging during secret input and reveal the prompt after the actual multiline guide at 36x12, preserving hidden input and cancellation.
- [x] Preserve a selected workspace after removal recovery fails/is abandoned while its manifest remains; fall back only after actual deletion, including deletion before a journal error.
- [x] Run the complete TUI package and relevant CLI dashboard integration checks.

## Task D: Provider discovery and CLI checks (findings 1, 2, 11, 12)

**Own:** discovery safety/Bitbucket provider tests, CLI `commands.go`, `auth_status.go`, and their regression tests.

- [x] Accept `ssh.bitbucket.org` clone links only for canonical Bitbucket Cloud while keeping HTTPS and custom endpoint boundaries intact; verify mixed-host provider responses through discovery and import normalization.
- [x] Obtain/confirm a partial import reassociation plan before progress input starts, then wrap mutation/resume only in `operationProgress`.
- [x] Run Git access checks in the registered bare repository using safely resolved paths and verified origin/identity, preserving local SSH/helper/TLS configuration.
- [x] Reject mismatched explicit `--repo` and `--connection` selectors before reporting successful authentication.
- [x] Run focused provider/CLI regressions and inspect human/machine output compatibility.

## Integration and verification

- [x] Reconcile any shared interfaces, review each workstream, and address concrete review feedback without unrelated refactoring.
- [x] Update onboarding documentation for any user-visible behavior clarified by these fixes and record remediation evidence for all 14 findings.
- [x] Freeze implementation, then run `go test ./...`, `go vet ./...`, and build. Run platform cross-builds appropriate to registry changes; record unavailable race/native-platform checks accurately.
- [x] Rebuild `bin/bearforge.exe`, verify the branch/diff, and report all findings remediated only after the checks succeed. Leave changes uncommitted unless requested.

## Remediation evidence

Focused Windows checks and integrated verification have passed, with regression coverage for all 14 findings. Independent reviews approved the Git, auth registry, and TUI/CLI/provider workstreams without outstanding findings. Native Unix verification limits are recorded below.

| Finding | Change | Regression evidence |
| --- | --- | --- |
| 1 | Accept the canonical Cloud SSH host only within the canonical Cloud endpoint boundary; normalize its provider identity. | Discovery safety boundary cases, real mixed-host Bitbucket response fixture, provider resolution, and already-registered import detection. |
| 2 | Confirm import credential reassociation before starting progress; wrap only mutation/resume in progress. | Portable no-input refusal leaves the journal unchanged; Unix PTY regression verifies a declined review never starts progress. |
| 3 | Rebuild dependent login fields when toggling provider after relogin. | `TestDashboardImportReloginProviderSwitchResetsEndpoints`. |
| 4 | Require a successful source observation matching current import settings before preview. | Changed-source, failed-reload, compatible-reload, and ID/selection tests. |
| 5 | Handle paging before secret editing and reveal the requested token prompt. | The actual shared token guide followed by hidden input at 36×12 for GitHub and Bitbucket. |
| 6 | Reconcile changing advertisements with at most three transfers instead of requiring their union. | Native Git deletion, creation, delete/recreate, bounded churn, and persistent missing-exact-ref cases. |
| 7 | Use Git's effective fetch-all skip booleans before validation and advertisement. | Unavailable/unsafe skipped remotes and effective boolean/alias/order fixtures. |
| 8 | Reuse direct-ref inventory and loose-path spelling metadata; conservatively probe unknown or alias/symlink destinations. | 300 existing refs need zero `symbolic-ref` probes; packed/dangling case aliases, symlink shadows, and unfetched tags retain protection. |
| 9 | Preserve explicit hostname-only matching while normalizing provider defaults separately. | `Manager.Resolve` tests with saved ports/context paths, exact API scope, mismatches, and ambiguous grants. |
| 10 | Sync the Unix parent directory after rename and retain both generations if publication durability is uncertain. | Pre-publication and post-publication failures, replacement/logout secret retention, and the rename/directory-sync boundary. |
| 11 | Safely resolve and verify the registered bare repository and origin before running its Git read probe. | Repository-local URL rewrite changes Git status from unavailable to readable; origin drift is refused. |
| 12 | Reject an explicitly requested repository whose connection does not match the explicit selector. | Bound-to-other, environment, and default-source repository cases. |
| 13 | Refresh the selected workspace first; fall back only when observation proves its manifest is absent. | Failed resume, abandon, deletion before journal failure, and unrelated read failures. |
| 14 | Enforce one 2 MiB serialized limit before writing and while reading. | Exact-limit round trip, oversized replacement rejection, old grant retention, and unpublished secret cleanup. |

Platform limits: Windows cannot run the Unix PTY regression or native Unix directory durability path. Linux amd64 and macOS arm64 auth/CLI test binaries compile; publication failure boundaries are simulated on Windows. The native Git symlink-shadow fixture skips when Windows symlink creation is unavailable. Race testing requires cgo and was unavailable in this environment.

## Final verification

Verified the frozen implementation with Go 1.27.4 on Windows amd64:

- `go test -timeout 15m ./...` passed. Application integration: 584.715s; CLI: 271.131s; Git: 390.390s. All remaining packages passed or had no tests.
- `go vet ./...` and `go build ./...` passed.
- `go build -trimpath -o bin/bearforge.exe ./cmd/bearforge` passed; the pre-commit binary reports the `d0cc00d` checkout with `dirty=true` and its version/help commands work.
- Linux amd64 and macOS arm64 CLI/auth test binaries and CLI executables cross-compiled with cgo disabled. This verifies compilation, not native secure-store behavior.
- Changed Go files are formatted; `git diff --check` passed.

Prepared for the user's requested commit on `feat/provider-onboarding` after verification. `main` remains at `055d7bd196ec1371b00b568025be01f6eee02743`. User credentials and repository bindings were not modified; tests used isolated fixtures. README and desktop discussion were not changed.
