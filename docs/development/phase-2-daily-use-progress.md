# Phase 2 daily use progress

## Scope

Implement bulk exec, doctor, safe repo retire/restore, explicit catalog migration, onboarding and signed release automation against ADR-0001. Continue on the existing feature branch. User explicitly waived company private-repository validation; use disposable scale fixtures and leave company testing to the maintainer after merge.

## Decisions

- CLI/service features first; retain the completed dashboard behavior.
- Exec defaults to one worker, direct argv after --, explicit shell adapters and bounded output; arbitrary commands are never replayed on resume.
- Catalog v2 adds retired identities and needs an explicit backed-up migration from v1. Workspace schema stays v1.
- Signed release automation uses GitHub OIDC; implementation and local artifact validation do not imply a release has been published.

## Progress and evidence

- Reconnaissance complete: clean feature branch at c49164e, Go 1.26.1, Git 2.47.0 Windows; existing process-tree cancellation, strict YAML/CAS replacement and journaling are reusable.
- Baseline/default CLI has no exec/doctor/retire/restore/migrate commands. Existing demo contains two repositories; it will remain unchanged.
- Implementation plan: `docs/superpowers/plans/2026-11-11-phase-2-daily-use.md`.
- Task 1 implemented: direct/shell exec CLI, verified worktree scope and queued-target revalidation, one-worker default, bounded concurrency/fail-fast/cancellation, binary-safe 64 KiB-per-stream capture, streamed sequenced JSONL events, interactive plain streams, per-repository metadata and execution journals.
- Process tests observed absent contracts before implementation and pass natively: literal argv/cwd/environment, both 2 MiB output streams, capture truncation, nonzero exit, installed shells, Windows wrapper paths with spaces, descendant termination and interactive stream handoff. App/CLI tests cover no-write preview/preflight, complete independent results, fail-fast, queued cancellation, stale worktree identity and structured stdout integrity.
- Ruling: a fully drained execution journal is complete even when a child failed or cancellation left queued jobs unstarted. The result still reports partial/cancelled outcomes. Only interruption/finalization failure leaves an unfinished journal; resume refuses to replay arbitrary code and instructs inspection/abandonment. This avoids blocking every later mutation after an ordinary failed build; cost if wrong: execution records remain visible for manual inspection.
- Task 1 full `go test ./...`, `go vet ./...` and new-code golangci-lint checks passed. Race instrumentation remains unavailable with this CGO-disabled toolchain.
- Task 2 implemented: doctor accumulates config/repository/worktree/recovery findings with stable check IDs and remediation, including readable pending operations alongside corrupt journals. It never fetches, repairs, locks or writes. Missing root/Git and a failed status observation cannot produce a healthy result. Real-Git content-hash tests confirm no file/ref/journal changes; focused app/CLI tests pass.
- Task 3 implemented: catalog v2 tracks retired identities, while workspace documents stay at v1. Migration has read-only/no-op planning, bound approval, exact source backups, changed-byte/future-version refusal and explicit resume after atomic replacement. Config/app/CLI tests failed on missing contracts then passed. Doctor identifies legacy catalogs and validates retired storage/records.
- Ruling: compatible ordinary writes preserve an existing v1 catalog's version instead of blocking every old command. Retired metadata requires explicit v2 migration before any rename. This meets the no-silent-upgrade rule and permits pre-upgrade unfinished operations to finish before migration; cost if wrong: v1 remains usable for its earlier subset of features.
- Task 4 implementation is in place: concrete source/destination previews bind root, action, operation ID, definitions and all Git refs/HEAD. Exclusive native renames preserve whole repositories, never replace occupied destinations and have no copy/delete fallback. Re-registration of known retired identities points to restore. Tests cover workspace/linked-worktree refusal, stale refs/definitions/destinations, history preservation, and recovery from metadata/journal/move errors and cancellation after rename. CLI preview/confirmation/list/restore tests pass.
- Task 5 implemented: onboarding/release guides, structured source/build version metadata, deterministic six-target archives/checksums, binary-derived CycloneDX inventories, exact Go 1.27.4 production toolchain and immutable CI/action pins. Tag publication requires a merged commit and successful reusable native/scale CI. Cosign signs/verifies checksums using GitHub OIDC, then provenance and verified assets are published. No tag/push/merge/signature/publication has been performed locally.
- First complete regression suite and vet passed. Initial aggregate lint found only four new archive/test API/style issues; those were corrected. Actionlint v1.7.12 validates both workflows.
- Disposable Windows scale run passed: 60 distinct local remotes registered in 1m48s, 60 worktrees provisioned in 38s, 60 commands with four workers in 12s, read-only doctor in 23s, plus remove/retire/restore history checks (192s overall). These are observed timings, not performance guarantees.
- Two independent six-target local snapshot builds produced byte-identical checksums for every archive, SBOM and manifest. They were explicitly marked dirty snapshots built with local Go 1.26.1, not production signed releases.

## Independent review and fixes

- One fresh read-only review found two Important process-supervision issues and no Critical/Minor findings. Remaining lifecycle/migration/doctor/release reading found no further material issues. No second review was requested.
- Windows launch-boundary regression failed because child user code could run before job assignment. Creation now uses CREATE_SUSPENDED; the process is assigned to its kill-on-close job before its primary thread is resumed. Native regression plus existing descendant/cancellation/shell tests pass.
- Real Ubuntu WSL PTY regression failed on missing terminal foreground handoff. Interactive Unix children now acquire the terminal foreground process group and the controller restores its prior group afterward. The native PTY read/restoration regression passes.
- A follow-up real PTY interrupt test and app scheduling regression failed: Ctrl-C in a foreground child was not classified as batch cancellation and queued work continued. Interrupted interactive children now return cancellation; the service cancels/drains active work and leaves queued jobs not_run. Both regressions pass, alongside the full native Linux process adapter suite.
- Ruling: use creack/pty v1.1.24 only for real Unix terminal regression tests. It is excluded from product binaries and their inventories; cost if wrong: one additional test dependency. Production subprocess logic remains in the existing process adapter and native OS APIs.
- Review declined hosted signing/publication, company repository tests and aggregate/platform evidence: company validation was explicitly waived; local evidence is recorded here; hosted release/merge remains a maintainer action after review and CI.

## Final validation

- After the supervision fixes, the complete Windows `go test ./...`, `go vet ./...` and native CLI build passed on Go 1.26.1. Staticcheck and new-code golangci-lint reported zero issues; changed-file goimports, module tidy diff, patch whitespace and actionlint v1.7.12 checks passed. Aggregate lint retains 26 pre-existing errcheck findings outside this change's new issues.
- Downloaded the official Linux Go 1.27.4 SDK into an ignored local cache and verified SHA-256 `7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840` before use. Full native Ubuntu 24.04 WSL `go test -race -mod=readonly ./...` passed with CGO/GCC enabled. This supersedes the earlier local race-instrumentation limitation; Windows still uses its CGO-disabled SDK.
- Final disposable Windows scale run passed after the launch-boundary changes: 60 distinct remotes registered in 87s, 60 worktrees provisioned in 38s, 60 commands with four workers in 14s and read-only doctor in 23s, followed by a history-preserving retire/restore drill (173s overall).
- Rebuilt all six target snapshots twice after the process fixes. The two checksum files match byte for byte; all 13 archive/SBOM/manifest entries were independently hashed and verified. Snapshots explicitly record local Go 1.26.1, dirty source and source commit c49164e; they are not production releases.
- Extracted the Windows amd64 snapshot, verified its executable against the manifest and ran it natively in a new disposable root. Version metadata, two repositories/two workspaces, read-only migration preview, exact legacy backup, parallel exec and doctor passed. Real ConPTY Ctrl-C cancelled the active interactive child, stopped the queued repository and left a complete journal. Retirement/restoration previews made no changes, retired listing retained the operation ID, restore preserved the local branch OID and final doctor was healthy. The dashboard opened against the v2 catalog, displayed local status and restored the terminal on quit with exit zero.
- Existing demo and company repositories were untouched. macOS execution and hosted CI remain external validation gates; cross-builds do not establish native macOS behavior. No merge, tag, remote push, production signature or published release has been performed. Implementation is ready for review/merge and the tag-triggered signed release workflow.

## Local integration

- Preserved the prior dashboard commits on `feat/phase-2-dashboard-actions`.
- `454f35d feat(cli): add bulk execution and repository maintenance` contains the services, CLI and focused/scale regressions.
- `3cfb574 feat(release): build and sign reproducible release artifacts` contains the release builder, CI/signing workflow and onboarding/verification guides.
- A separate documentation commit records the final usage and validation evidence. No remote integration was requested or performed.
