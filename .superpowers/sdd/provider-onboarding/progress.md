# SDD ledger — plan: docs/superpowers/plans/2026-11-16-provider-onboarding.md

Approved spec: docs/superpowers/specs/2026-11-16-repository-discovery-and-ref-collisions-design.md
Implementation worktree: D:/Lab/bearforge/.tmp/worktrees/provider-onboarding
Original checkout: D:/Lab/bearforge
Base commit: 055d7bd196ec1371b00b568025be01f6eee02743
User work to preserve: README.md edits; untracked docs/desktop-app-discussion.md. Auth/ref/discovery spec belongs to this task and is approved.

## Preflight

| Tasks | Shared file/interface | Finding |
|---|---|---|
| 1 / 3 / 5 | gitcli clone optional planner, app journal fields | Keep app.Git backwards compatible; later tasks consume exact capability reported by Task 1. |
| 2 / 3 / 4 | domain.CredentialResolver | Auth core owns new domain credential types; publish signatures before adapter implementation. |
| 3 / 4 | provider clients and HTTP | Coordinate ownership; implement resolver integration before listing or work on separate new files against locked interfaces. |
| 5 / 6 | ImportRequest, result, observer | App core owns request/journal types; TUI consumes them. |
| 1 / 5 | app/service.go, operations.go, operation/store.go | Sequence changes and retain legacy journal tests. |
| 1 | ref tests versus algorithm | Exact names/OIDs, prefix/component collisions and conservative recovery agree. |
| 2 | secure store versus noninteractive checks | Native storage must not block on prompts; use context-aware adapters and metadata-only --local. |
| 3 | Git and API auth | Explicit bindings fail closed; generic status reports no push proof. |
| 4 | discovery versus credential routing | Origin validation and complete pagination required; preserve per-provider token scheme. |
| 5 | partial batch versus root locks | Single batch continues independent failures; only same-operation recovery may reassociate. |
| 6 | empty-root import versus old picker | Separate i modal; a remains workspace picker. |
| 7 | transfer versus user changes | Exclude user README/desktop discussion; compare original baselines before transfer. |

Ruling: User's explicit request to implement the approved design authorizes proceeding through the plan without another approval gate — no scope change — user can review saved plan while implementation runs.
Ruling: Create ignored project-local worktree for source isolation and transfer only reviewed implementation changes to the active checkout at finish — protects current main and existing edits — requires final root verification.
Ruling: Use task-scoped workers and review; independent read-only preparation can overlap implementation. Disjoint implementations may overlap when interfaces are settled and ownership is explicit, per active multi-agent developer direction — improves elapsed time — reconcile integration centrally.
Ruling: Do not push, merge shared branches, register real OAuth apps, modify real Git credentials, or use actual provider accounts. Test with disposable Git repositories and fake secret/browser/HTTP adapters — authorized local implementation scope — maintainer supplies runtime registrations.

## State

- Task 1: complete: 6d32f16 + c2b3b3f + ea04271; both scoped review rounds accepted, all symbolic/dangling/tag findings addressed. Matrix2.40/2.48+ still external evidence gaps.
- Task 2: implemented dd4cc20 and 0c86efd, scope/no-browser fix f127968; scoped re-review clean. Auth/domain tests+vet and Linux/macOS !cgo crossbuild pass; macOS native requires cgo/macOS CI.
- Task 3: root implemented/committed279bc33. Reviewer found missing-helper fallback, blocked-input cancellation, hidden devicecodes undermachine/quiet output. Fresh fix_auth_adapters agent active, owns CLIauth*/Githelper only, fixround1. Initial focused red/green+vet/build passed before findings.
- Task 4: complete f95f576 +7381504; scoped re-review accepted olderServerfilter fix. DefaultBranch empty explicitly unknown.
- Task 5: complete e448b5c +435537b; scoped re-review clean. ImportBindingHistory preserves repeatedrepairprovenance independentcatalogsnapshot, conflict-onlyhelperfallback; trailing .git/ legacyduplicate active/retired fixed. Focusedapp31.223/op0.741s; fullaffected174s.
- Task 6: complete e7f1394 +64803b5; scoped re-review clean. Resetlogin/promptoffset; newerresultownershipsupersedesoldimport. FullTUI1.38s/CLI82.31s; focusedfix0.843s.
- Task 7: pending

## Current interfaces / root adapters

domain.CredentialResolver.Resolve(ctx,CredentialRequest)(Credential,error); request Provider/Host/APIURL/GitURL/Repository/Connection/GrantID/TokenEnv strings, AllowRefresh bool. Credential.Authorize(*http.Request), GitCredential(*url.URL)(string,string,error), CacheKey().

Auth NewManager(Options) no I/O; DefaultRegistryPath()(string,error); NewSystemSecretStore(); NormalizeLoginRequest. Manager Login(ctx,LoginRequest,LoginHooks), Connections, Connection, Status, Logout, Resolve. LoginRequest.NoBrowser suppresses management page for automation; explicit OAuth remains supported. OAuthConfig.Scope read-only Server defaults.

App ImportRepository{ID,Repository,Source,Transport}; ImportRequest{Repositories,DryRun,ExpectedPlan,RefFormat}; RepositoryImportChoice{ID,Repository,Source,RegisteredID,RetiredID,Disabled,Problem}. Service Credentials, GitForConnection func(CredentialRequest)(Git,error), BindConnection func(ctx,path,req)error, RebindConnection func(ctx,path,previous,current)error. App ReassociateImport(ctx,ImportReassociationRequest{OperationID,Connection,GrantID,Repositories,DryRun,ExpectedPlan}).

Root gitcli auth.go ConnectionBinding{Connection,GrantID,URL,Executable,RegistryPath}; WithConnection returns copied client with commandConfig prefix (no persistent writes), BindConnection refuses other helpers, RebindConnection allows only reviewed exact old/current helper idempotently. Hidden CLI auth git-credential emits raw Git protocol; wrong grant/remote/logout returns quit=true to stop helper fallback. No secret argv/config. configureServiceAuthentication configures app callbacks on copied sources.

Root owns CLI auth*.go + root.go + commands.go resume flag; Git auth.go/tests + tiny client.commandConfig prefix; provider credentials.go/tests, clients/http/resolve and PR query connection/grant propagation. Those adapters committed279bc33; fresh fix_auth_adapters owns follow-up corrections. TUI worker ownership complete. No concurrent overlapping edits.

Root focused RED/GREEN evidence: provider tests failed missing connection fields/constructor then passed GH2.7s BB3.8s PR1s; auth CLI failed missing authManager then passed0.34s; helper routing failed missing ConnectionBinding then passed2.9s; rebind missing method then Git5.6s passed; resume --connection unknownflag then combined CLI auth/helper/dashboard/repair0.498s passed. Provider/CLI/git vet passed. Full baseline before changes all PASS; app115s, CLI58s. Repeat full suite only after integrations stable.

Outstanding controller work: finish/re-review task3 fix; commitdocs/onboarding+approvedspec+plan; final wholechange security/recovery/lifecycle review (requesting-code-review skill/reference); final post-fix tests/vet/lint/crossbuild/smokes; transfer reviewed code from worktree to original checkout (no overwrite of new user changes).

Final pre-fix integrated verification: go test ./... session81457 EXIT0 allpackages (app213.378s CLI94.835s Git86.537s); go vet ./... EXIT0; Linux amd64 and macOS amd64 CGO0 builds EXIT0; builtWindows authlogin/repoadd help + isolated APPDATA authstatus--local JSONsmoke EXIT0 and no root/authdirscreated. Race attempt EXIT1 CGOdisabled, no gcc/clang onPATH. Installedgolangci-lint2.12.2 builtGo1.26 cannotloadGo1.27project; successfully rebuilt sameversion locally usingGo1.27 into .cache/review-tools (session98045 EXIT0). Run localbinary next onstablesource; globalinstallationunchanged.

Task3 remainingfixfindings exact: generatedhelper mustemitquit=true ifexemissing/nonzero andactionablediagnostic; prompt/hiddenreadmustcancelandrestoreTTYbeforectxcancelreturn; JSON/JSONL/quietexplicitbrowsermustrejectbeforeauthorizationorprovideusablecodechannel. Fixer plansWindowsCancelSynchronousIo/CancelIoEx joinedreader, avoidscancelreaderWindowsflush issue.

Known integration concerns to address in final review: helper vs root-independent secure registry overrides, clone helper path stability; status is live read-only by default and --local no network/secrets; existing registered manual connection association is optional in spec but current auth login offers no --repo; imports persist selected profile. Root helpers use installed binary path for ordinary Git/GoLand; no real accounts/OAuth registrations/secret stores mutated.

## Auth preparation decisions

Prefer context-aware native storage behind SecretStore. If using go-keyring, prevent Linux unlock prompts and bound calls; credentials cannot enter argv. Windows secret values can be limited to 2560 bytes: separate generation-specific access/refresh/client-secret values and atomically commit registry references. Stable grant identity is independent of secret generation.

Runtime OAuth registration environment inputs are login-only: BEARFORGE_AUTH_OAUTH_CLIENT_ID, BEARFORGE_AUTH_OAUTH_CLIENT_SECRET, BEARFORGE_AUTH_OAUTH_REDIRECT_URI. Apply to displayed host only, never overwrite existing status/helper connections implicitly. Browser fallback must handle unsupported HTTPS callback policies without adding a hosted broker.

Bitbucket Cloud API tokens use REST Basic with account email; Git uses x-bitbucket-api-token-auth. OAuth/access tokens use Bearer and x-token-auth for Git. Legacy BITBUCKET_TOKEN remains Bearer. Server user token username must be supplied for Git; profile reads do not attest authenticated identity.

## Final integration and active-checkout handoff

- Whole-change review at e5affdb found four Important issues. Final fix wave: 59cc5b4 macOS Keychain release, 37842f0 anonymous GitHub, 1b1aacc synthetic origin/HEAD protection, 2c741a1 reviewable grant repair. Scoped rereview accepted all four, no remaining Important/Critical in scope.
- Final full suite: `go test ./...` EXIT0 (app423.175s, CLI198.665s, gitcli213.094s). Final catalog_previous additive repair field separately verified with focused app/CLI tests and vet. Whole-project vet and native Windows build passed after all edits.
- Linux/macOS amd64 CGO0 source cross-builds passed. Native macOS CGO1 release/runtime remains CI validation. Windows release snapshot packaging passed without publication. Race/lint tool limitations recorded in the plan; neither claimed successful.
- Applied handoff.patch to D:/Lab/bearforge, excluding scratch reports and already-present approved spec. Compared all94 transferred files with reviewed source after newline normalization: zero mismatches. Original README SHA256 78E99EA9BB8868D5BAD6B74053821A8AD836CC2C494A07DD0C531F4BE7615E2B and desktop doc SHA256 34E1ADD825F4114EA3E01684E9516EDC6D5A4CF15C35918601F40C74F0C31FC5 preserved.
- Active checkout go vet ./... EXIT0; bin/bearforge.exe build EXIT0. Auth/login/ref-format help, isolated JSON local status and empty token stdin checks passed; no auth registry/root state created. No real browser/provider/keyring operation, push, tag, merge or publication.
