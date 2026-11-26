# Current-directory root implementation plan

**Goal** — Run Bearforge from the directory whose repositories and workspaces you want to manage. From a managed worktree, the same commands should find its enclosing Bearforge root.

## Behavior

- Starting at the process working directory, walk toward the filesystem or volume root. The first directory containing a real `.bearforge` directory wins. If the walk finds none, use the original working directory.
- Use `.bearforge` as the marker. Creating an empty workspace writes its manifest and operation journal before a catalog exists. An interrupted first registration can likewise leave a journal as the only record of work. Both need to be discoverable from below.
- Remove the persistent `--root` flag and the `BEARFORGE_ROOT` lookup. Cobra should reject the former as an unknown flag. A stale environment setting should have no effect. Remove the automatic fallback to `~/code`.
- Discovery only reads the filesystem. Existing commands still control when state is created. Help, version and completion work without opening a store. Local paths passed to `repo add` are relative to the directory where the user ran the command.

## Implementation

- [x] **Implement discovery and retire the overrides.** Add `config.DiscoverRoot(start string) (string, error)` in `internal/config/directory.go`. Probe each candidate with `os.Lstat`, using the existing platform-specific `isLink` check. Continue only for a missing marker. Report a malformed marker, link/reparse point or inspection error at its path. In `internal/cli/root.go`, obtain the working directory in `options.store()`, call the resolver and pass the result to `config.New`. Remove the option field and flag registration. Use the same path for ordinary commands, `workspace path`, the dashboard and `auth status` catalog checks. Keep user credential storage and the process working directory independent of discovery.
- [x] **Move CLI fixtures to real working directories.** Use the canonical temporary paths in `internal/cli/fixtures_test.go`, `t.Chdir` for serial in-process tests, and `exec.Cmd.Dir` for subprocesses. Update direct `options{root: …}` fixtures too. Read-only and rejected-command tests should assert the absence of `.bearforge` inside an existing directory. Keep a negative CLI test for `--root` and an environment test that points `BEARFORGE_ROOT` at a second populated root.
- [x] **Make recovery instructions usable after the flag removal.** In `internal/cli/recovery.go`, the CLI result renderers and the dashboard views using `dashboardRecoveryCommand`, show the operation's captured directory as a labeled path alongside a command such as `bearforge operations resume ID`. A user returning in another shell can change to that directory first. Preserve inspection-only guidance for interrupted `exec`, the flags on recovery previews and confirmations, and terminal sanitization of displayed paths. Update the root diagnostic in `internal/app/doctor.go`. Retain absolute roots in journals and their cross-root validation.
- [x] **Update the public workflow.** Revise `README.md`, `docs/onboarding.md`, `docs/dashboard.md` and `docs/architecture/0001-bearforge-blueprint.md`. Show how to create or enter the chosen directory before the first import or workspace creation, with PowerShell and POSIX examples. For upgrades, enter the former root (including `~/code` for previous default installations). For automation, set the command's working directory. Existing catalogs, workspaces and journals can be used in place.

## Acceptance checks

Discovery walks the physical working directory so entering a worktree through a symlink or Windows junction selects the target's root. For legacy journals recorded with different capitalization or Windows short names, verify that the differing path components identify the same directory entries. This preserves recovery for existing roots and rejects journals copied from another installation.

| Scenario | Expected result |
| --- | --- |
| Fresh directory alongside an existing root | `repo list` and a dry run leave it untouched. A first workspace creation writes there. The other root and the former home default stay untouched. |
| First workspace created before any repository | From inside the workspace, `workspace path NAME` prints the original workspace path and `operations list` sees its creation. |
| Two enclosing markers, or an unrelated Git checkout | The nearest Bearforge marker wins. A `.git` entry alone has no effect. A malformed or linked inner marker produces an error before any write. |
| Interrupted first registration | From a descendant, `operations show ID` can read the pre-catalog journal. Existing journals still reject operations owned by another root. |
| Relative repository path supplied from a subdirectory | The registered source resolves against the invocation directory and the managed clone lands under the discovered root. |
| Recovery output in the CLI or dashboard | The captured directory remains identifiable with spaces and apostrophes. The displayed command works from that directory, including after the dashboard selection changes. |

Run `go test ./internal/config ./internal/cli ./internal/tui ./internal/app ./internal/operation`, followed by `go test ./...`, `go vet ./...` and `go build ./cmd/bearforge`. Exercise filesystem cases on the supported Windows, macOS and Linux CI jobs.
