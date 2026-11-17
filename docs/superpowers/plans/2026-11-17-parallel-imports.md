# Parallel repository imports

**Goal:** Clone selected repositories concurrently under the existing catalog worker limit, honor the dashboard's `--jobs` override, and display each durable completion while other repositories continue.

**Design:** A bounded worker pool runs repository-specific verification, remote ref planning, cloning and retained-clone initialization. One coordinator owns the operation, catalog writes, credential binding and progress callbacks. Workers request and await durable `started` and ref-format checkpoints before their corresponding effects. Independent transfer failures retain successful imports. Cancellation or a shared persistence/safety failure stops admission, cancels active work and joins every worker before releasing the mutation lock. Resume uses the current catalog limit and reuses retained directories.

Scheduling uses `ImportRequest.Jobs` (zero selects catalog `defaults.jobs`; valid explicit values 1–64), without changing the reviewed destination/identity digest. Final results remain in journal step order; completion progress follows actual finish order. No catalog or journal schema changes are required.

## Tasks

- [x] Add controlled clone/planning tests that demonstrate bounded parallelism and an early durable completion while a slow repository remains blocked.
- [x] Split import execution into coordinator-owned preparation/checkpoints/finalization and repository-specific workers. Preserve fail-closed journal behavior, catalog revision checks and credential reassociation recovery.
- [x] Forward the existing dashboard jobs override and verify incremental import rendering without waiting for the batch.
- [x] Harden shared test doubles for concurrent Git calls and retain explicit single-worker sequencing assertions where needed.
- [x] Run focused import/recovery tests, full race tests, vet and build. Review the completed diff independently and document behavior and limitations.

## Review focus

- Persist started ownership before filesystem/ref probes, and selected ref format before cloning.
- Never let workers mutate shared journal steps or catalog state.
- A failed journal checkpoint prevents the corresponding transfer and stops further journal writes.
- Drain already admitted work on shared failures and cancellation; retain enough journal state for safe resume.
- A completed repository is reported only after binding, catalog persistence and its succeeded checkpoint.

## Verification

The full Windows race run (`CGO_ENABLED=1 go test -race -json ./... -count=1 -timeout=20m`) passed: 15 packages, 1,191 test/subtest passes, zero failures or race reports, and five existing environment/opt-in skips. `go vet ./...`, formatting and diff checks passed. The executable was rebuilt with `go build -trimpath -o bin/bearforge.exe ./cmd/bearforge` and its `version` command succeeded.

Seven public import regressions use real local Git repositories to exercise bounded clones and remote planning, early durable completion, ordered partial results, cancellation/drain, job overrides, invalid limits and retained-directory resume. The old sequential implementation failed the early-clone and early-planning tests while the first repository was blocked. Two worker tests reproduced entry into cloning after checkpoint cancellation and now pass with explicit phase-boundary cancellation checks. Dashboard tests verify per-repository results and completed/total progress before batch completion. Independent safety and fresh whole-diff reviews found no remaining material issues.
