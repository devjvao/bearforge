# Task 1 implementation report

Status: DONE_WITH_CONCERNS. Ref safety, durable selection, inspection, and preservation-first recovery are implemented. Native Windows tests exercise the reported silent alias bug. Successful reflog migration cannot be exercised on installed Git 2.47.0.windows.1; its safe refusal is tested. Auth CLI files are being implemented independently, so final CLI revalidation belongs to integration.

## Public integration contract

`app.Git` is unchanged. `gitcli.Client` structurally implements optional `app.ClonePlanner`:

```go
type ClonePlanner interface {
    PlanClone(context.Context, string, string, string) (string, error)
    CloneWithFormat(context.Context, string, string, string) error
    InitializeWithFormat(context.Context, string, string) error
}
```

Arguments are `(ctx, remote, destination, requestedFormat)` for planning, `(ctx, remote, destination, selectedFormat)` for cloning, and `(ctx, repositoryPath, selectedFormat)` for resume. Planning accepts empty/auto/files/reftable and returns only files/reftable. `app.ValidateRefFormat(string) error` is side-effect-free. `Service.AddRepositoryWithFormat(ctx,id,remote,format,dry)` is additive; old `AddRepository` delegates with auto. `Client.ReferenceFormat(ctx,path) (string,error)` reports actual local repository storage, with legacy files fallback.

`app.Step.RefFormat` (`ref_format,omitempty`) stores the selected format. `RequestedRefFormat` (`requested_ref_format,omitempty`) retains explicit selection across a crash before planning. `app.Item.RefFormat` exposes actual selection in results. Operation storage accepts legacy missing fields, validates selected files/reftable and requested empty/auto/files/reftable. Import should persist its selection before CloneWithFormat/InitializeWithFormat, hold the root mutation lock, and perform origin/bare/worktree checks before resume. Never call PlanClone from discovery/preview: it advertises refs and probes the filesystem.

The app writes the operation before planning, ensures the repos parent during execution, persists the selected backend, then marks the step started before clone. An auto-selected files repository can be replanned to reftable on resume, with that updated choice durable before migration. Existing reftable is retained. Explicit files remains explicit and refuses incompatible refs.

## Behavior and decisions

- Advertise heads and tags using the same Client.Run invocation environment as clone/fetch. Heads map into both local and tracking namespaces for new clones.
- Probe the actual destination directory (or its parent before creation) using exclusively owned, uniquely named temporary files. Probe errors fail closed. No operating-system assumption or global ignorecase mutation.
- A component trie detects complete case aliases, differently cased shared directory components, and folded prefix conflicts. Compatible files storage remains the default. Required/explicit reftable uses an actual disposable Git init capability probe.
- Explicit files uses `--ref-format=files` when capability probing supports it, overriding both init.defaultRefFormat and GIT_DEFAULT_REF_FORMAT. Older Git lacking the option retains the files-only fallback.
- Ruling: exact same-case ref directory/file prefixes are rejected during planning because installed Git rejects them even with reftable. Case-folded prefixes are preserved using reftable. No exclusions, branch renaming, or dropped refs.
- Fetch first validates existing safe mappings/settings, then advertises every remote and checks their combined destination refs plus retained local refs before any fetch. It advertises again after transfer and verifies exact enumerated names; it never assumes rev-parse case-insensitive lookup proves identity and never rejects a legitimate advance by comparing stale advertised OIDs.
- Already registered files repos fail closed on newly incompatible refs, including with linked worktrees; no automatic conversion occurs through Fetch.
- Journal-owned resume retains bare/origin/no-linked-worktree checks before planning/migration. Migration refuses active Git locks, first probes a populated-reflog conversion in a disposable repository, and never uses reflog-discarding flags. Successful-capability paths snapshot exact refs/symrefs, HEAD, and reflog selectors/OIDs/messages/authors before and after conversion and retain a repository-local snapshot as recovery evidence. External Git writers must be stopped.
- Installed Git 2.47 cannot migrate populated reflogs. The diagnostic directs an upgrade to preservation-capable Git, stopping external writers, and retrying `operations resume ID`, while preserving the directory and journal.
- `repo add --ref-format=auto|files|reftable` and `repo inspect` expose selection. Human results use the existing DETAILS column with a short reftable explanation; existing worktree Data remains intact.

## TDD evidence

1. Initial `go test ./internal/gitcli -run 'TestClonePreservesCaseDistinctRefs|TestFetchRefusesNewCaseCollisionBeforeWriting' -count=1` failed as intended. Git 2.47 returned clone/fetch success while exact tracking enumeration lost Feature/feature and Group/one/group/two identities; the folded prefix fixture failed updating tracking refs; later sync accepted a new collision.
2. After preflight/backend/fetch verification, the same cases passed (9.973s). Case-only tags, shared component casing, folded prefixes, distinct commits and exact enumeration are covered with disposable packed local remotes.
3. App optional-planner regression failed with `optional clone planner was bypassed`, then passed once selected storage was durable before cloning. Preview never calls the planner and creates no journal. Invalid requested formats fail before effects.
4. Exact-prefix planning regression failed with nil error, then passed with actionable preflight refusal.
5. CLI help and human-backend tests initially failed because the flag/detail was missing, then both passed (0.162s).
6. Explicit files regression with GIT_DEFAULT_REF_FORMAT=reftable failed reporting actual reftable versus planned files, then passed with explicit capability-gated clone flags (2.692s).

## Verification

- Full `go test ./internal/gitcli -count=1`: PASS, 45.323s. Includes initial case/tags/component/prefix cases, later OID swaps preserving local branches, exact enumeration, active-lock refusal, worktree migration refusal, environment defaults, and Git 2.47 reflog-preservation refusal.
- Full `go test ./internal/app -timeout=180s -count=1 -v`: PASS, 141.563s. The optional 60-repository scale test retained its existing BEARFORGE_SCALE_TEST skip. An earlier combined Go driver stopped producing progress and was terminated; the bounded standalone rerun completed normally.
- Latest focused app planning/preview/invalid-format/relative-resume run: PASS, 4.783s.
- Latest full `go test ./internal/operation -count=1`: PASS, 0.967s.
- Latest focused linked-worktree collision-refusal test: PASS, 2.408s; exact retained refs and tracked content survive.
- Latest attempted CLI rerun is blocked by independently unfinished auth integration (`authBrowser`, `authenticationStatus`, `renderAuthentication` undefined). Earlier scoped CLI tests passed before those new files arrived. These files were not edited by Task 1.
- gofmt applied to all owned Go files. `git diff --check` on owned paths passed.

No real remote account, credentials, global configuration changes, root-checkout README changes, or desktop discussion changes were used. All Git fixtures are disposable local repositories. No Linux/macOS execution was claimed. Successful migration on newer preservation-capable Git and legacy Git 2.40 fallback still need integration-matrix execution.

## Owned files

`internal/gitcli/{client.go,ref_storage.go,ref_storage_test.go}`; `internal/app/{repo.go,service.go,repo_ref_format_test.go}`; `internal/operation/{store.go,ref_format_test.go}`; `internal/cli/{commands.go,result_output.go,repo_inspection.go,repo_ref_format_test.go}`; this report. Root-owned design/plan and other agents' auth/provider/discovery changes are excluded.

## Fix round 1/5: symbolic destinations and configured tag selection

Both Important review findings were verified and addressed. Read the fix-loop and receiving-code-review guidance before the round. Only `internal/gitcli/ref_storage.go`, `ref_storage_test.go`, and this report changed; shared `client.go`, auth files, and auth tests were preserved.

### Symbolic refs

Ref inventory now retains symbolic names and their targets instead of excluding them. Preflight protects symbolic destinations against both exact-name overlap (all backends) and case aliases (case-insensitive files storage). Symbolic names also participate in component/prefix collision checks. Required fetched branch identities must be direct refs, so a symbolic alias cannot satisfy exact-ref verification.

Covering regression: `TestFetchRefusesWritesThroughSymbolicRefs` in `internal/gitcli/ref_storage_test.go`. It creates a linked worktree, retains `origin/HEAD`, adds a distinct remote commit under `head` and exact `HEAD`, requires refusal directly from `fetchRefPlan` before transfer, then compares the complete ref/HEAD/reflog snapshot and linked tracked content around `Fetch`.

RED command: `go test ./internal/gitcli -run TestFetchRefusesWritesThroughSymbolicRefs -count=1 -v`.

Observed output before the fix:

```text
head: preflight did not protect symbolic destination: <nil>
HEAD: preflight did not protect symbolic destination: <nil>
FAIL bearforge/internal/gitcli 4.430s
```

The old Fetch reached its post-check and reported missing exact tracking names. This particular fixture did not show a changed final snapshot, but the RED assertion proved preflight had allowed the symbolic destination through; the amended test enforces refusal before any fetch command.

GREEN refusal now names both destination and retained symbolic ref, e.g. `fetch destination "refs/remotes/origin/head" overlaps retained symbolic ref "refs/remotes/origin/HEAD"; reconcile remote names before syncing`.

### Tag selection

Separated potential writes from required fetched identities. With normal auto-follow, advertised tags remain in collision checks but are not all required to exist afterward. `remote.NAME.tagOpt=--no-tags` excludes tags from both sets; `--tags` includes all advertised tags in required verification. The actual fetch command/configuration is unchanged.

Covering regressions: `TestFetchHonorsTagSelection` tests no-tags, auto-follow of a tag outside fetched branch history, normal reachable-tag auto-follow, and explicit all-tags fetching of the otherwise unreachable tag. `TestFetchNoTagsIgnoresUnselectedTagCollisions` confirms no-tags permits an unselected differently cased remote tag while preserving the retained local tag and ref/log snapshot.

RED command: `go test ./internal/gitcli -run 'TestFetchRefusesWritesThroughSymbolicRefs|TestFetchHonorsTagSelection' -count=1 -v`.

The two original tag-selection cases failed with `fetch did not retain exact ref "refs/tags/intentionally-unfetched"`, despite successfully advancing the tracking branch and correctly not fetching the tag (overall FAIL, 9.032s).

GREEN command: the same focused command passed after the fix (`ok bearforge/internal/gitcli 9.095s`).

Final expanded package command: `go test ./internal/gitcli -count=1`.

```text
ok bearforge/internal/gitcli 50.907s
```

gofmt and `git diff --check` on the two changed Go files passed. No broad whole-project suite was run during ongoing integrations. The already documented newer-Git migration and Git 2.40 capability-fallback matrix gaps remain unchanged.
