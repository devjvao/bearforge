# Registered repository token rotation implementation plan

> **For agentic workers:** Use superpowers:subagent-driven-development or superpowers:executing-plans to implement the tasks below.

**Goal:** Rebind existing registered repositories to a selected saved connection without cloning, and preserve readable joined authentication errors.

**Architecture:** Add `repo rebind REPOSITORY... --connection NAME` using the existing plan/confirmation and scoped-helper replacement capabilities. Record old/new repository snapshots in a `repo.rebind` operation before writing helpers or the catalog; ordinary operation recovery resumes partial changes. Reuse the nonsecret authentication repair review for CLI output.

**Tech stack:** Existing Go, Cobra, Bubbles, catalog CAS, operation journal, and Git CLI adapters.

**Spec:** User request in this session: fix rotation for imported repositories, rebind the current user's repositories to `github-github.com` without recloning, fix concatenated auth errors, and remain on `feat/provider-onboarding`.

## Constraints and review focus

- Preserve the five existing uncommitted permission-guidance files and work on `feat/provider-onboarding` in the current checkout.
- Select repository names explicitly; reject missing repositories, explicit `token_env`, incompatible connection/provider scopes, changed origins, stale previews, and unrelated local scoped helpers.
- Resolve only the replacement grant; revoked/deleted historical credentials must not be needed for repair.
- Verify bare repositories and their current origin before writes. Preserve all remote coordinates, refs, worktrees, and unrelated provider/helper settings.
- Journal helper/catalog writes for safe recovery after either persistence boundary; refuse unexpected catalog changes on resume.
- Preserve diagnostic line boundaries while stripping terminal controls, including when piped; keep JSON/JSONL shapes unchanged.

## Tasks

- [x] Add `app.RepositoryRebindRequest` and `Service.RebindRepositories`, plus `repo.rebind` resume dispatch and app tests for old/unbound associations, no clone/fetch, rejected stale plans/scopes, and interrupted helper/catalog recovery.
- [x] Add the CLI command, reuse a clear old/new binding review in dry-run and confirmation, and test explicit selection, confirmation, JSON output, and real local Git helper replacement without recloning.
- [x] Reproduce concatenated joined diagnostics in a test; preserve readable sanitized lines in human output and verify terminal/pipe behavior.
- [x] Document token rotation for completed repositories, including the different unfinished-import repair command.
- [x] Run relevant tests, vet, build, and a scoped review; preview and apply the authorized repair to the five current GitHub repositories, then verify API/Git read and unchanged repository/worktree identity.

## Verification

The final unchanged source passed `go test ./...`, `go vet ./...`, and the executable build. Focused regressions verified interrupted rebind recovery and explicit-provider local-origin refusal. A scoped review found no remaining issues.

The authorized five-repository rebind completed without cloning. Live API/Git read checks passed, and before/after comparisons retained refs, HEAD, origin, worktree registrations, and directory creation identity. The revoked local connection was removed after confirming no catalog references remained; normal `auth status` succeeded and no unfinished operation remained.

No commit or push is requested in this task.
