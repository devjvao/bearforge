# Develop Bearforge

The public repository and Go module are [`github.com/devjvao/bearforge`](https://github.com/devjvao/bearforge). The executable lives in `cmd/bearforge`. For installation and daily use, start with [onboarding](../onboarding.md); for release artifacts and signing, see [release verification](../releases.md).

## Build from source

Use Go 1.27.4, the latest stable release pinned in `go.mod`, and Git 2.40 or newer on PATH. Source builds, CI and production releases support the pinned Go release; older Go releases are unsupported.

```sh
git clone https://github.com/devjvao/bearforge.git
cd bearforge
go build -trimpath -o bin/bearforge ./cmd/bearforge
./bin/bearforge version
```

On Windows:

```powershell
go build -trimpath -o bin/bearforge.exe ./cmd/bearforge
.\bin\bearforge.exe version
```

Add the checkout's `bin` directory to PATH to use the `bearforge` commands in the guides, or invoke the executable by its full path. Keep the installed executable at a stable absolute path when using saved HTTPS credentials: Git's scoped credential helper invokes that path after the dashboard closes.

Saved authentication uses Windows Credential Manager, Linux Secret Service or macOS Keychain. macOS source builds require cgo, Apple clang and the macOS SDK to include the native Keychain backend. A macOS build without cgo retains environment-token workflows and reports saved-secret storage as unavailable. Linux needs an unlocked Secret Service collection and session bus. See [token storage](../onboarding.md#token-storage-and-automation) for operational details.

The currently published `v0.2.0` tag predates the canonical module path. Use a verified release archive or build a checkout; do not rely on `go install github.com/devjvao/bearforge/cmd/bearforge@latest` until a release containing the module-path change is published.

## Tests and CI

Run the standard checks from the repository root:

```sh
go test ./...
go vet ./...
go build ./cmd/bearforge
```

On Windows, race tests need a MinGW-w64 C compiler with the runtime described in
[Go's race detector requirements](https://go.dev/doc/articles/race_detector#Requirements).
For example, install the WinLibs GCC toolchain for your user:

```powershell
winget install --id BrechtSanders.WinLibs.POSIX.UCRT --exact --source winget --scope user
```

Open a new terminal to pick up the compiler on `PATH`, then run:

```powershell
gcc --print-file-name libsynchronization.a
$env:CGO_ENABLED = '1'
go test -race ./...
```

The compiler check must print a full library path. Setting `CGO_ENABLED` here
applies to the current terminal session.

Integration tests create disposable local Git repositories. They do not need company repository credentials. Native tests are required on Windows, Linux and macOS; cross-compilation alone does not prove filesystem, process, credential-store or terminal behavior. Some restricted Windows environments block Git transport helpers or atomic file replacement; run checks in an ordinary developer terminal when those restrictions prevent execution.

The [CI workflow](https://github.com/devjvao/bearforge/blob/main/.github/workflows/ci.yml) reads its Go version from `go.mod`, runs tests/vet/build on all three operating systems, and runs race tests on Linux with cgo enabled. The opt-in scale job exercises 60 disposable repositories:

```sh
BEARFORGE_SCALE_TEST=1 go test ./internal/app -run TestPhase2Scale60 -count=1 -timeout=12m
```

In PowerShell, set `$env:BEARFORGE_SCALE_TEST = '1'` before running the same `go test` command. Scale fixtures provide controlled coverage; they do not establish compatibility with a company's private repositories, proxy, SSO policy or custom deployment. Live private/enterprise validation remains maintainer-owned. The repository's [GitHub](https://github.com/devjvao/bearforge/blob/main/docs/development/github-pr-lookup-progress.md) and [Bitbucket](https://github.com/devjvao/bearforge/blob/main/docs/development/bitbucket-pr-lookup-progress.md) verification records document provider-specific evidence and limits.

See [status performance measurements](status-performance.md) for repeatable 12- and 60-repository Git workspace benchmarks, including total observation time and delivery of the first inspected repository.

The [Git inspection report](git-inspection-performance.md) counts child processes on up to 200 real repositories. The [dashboard refresh report](dashboard-refresh-performance.md) measures the first visible result and full refresh on the real 60-repository fixture and rendering costs through 1,000 rows.

The [journal history benchmark](journal-performance.md) measures time to the dashboard's initial rows on a first and repeat refresh with up to 1,000 completed or abandoned operations.

The [sync benchmark](sync-performance.md) measures time to the first fetch and total sync time with 12, 60 and 200 local repositories.

The [workspace PR lookup benchmark](pr-lookup-performance.md) measures local preparation, time to the first lookup, HTTP requests and total time with one or four API hosts.

GitHub PRs use [OpenCodeReview with GPT-6.1 Sol at high effort](../pr-reviews.md), with automatic owner reviews and owner-approved contributor reviews through `/open-code-review`. That guide covers contributor CI approval, OpenAI settings and GitHub App credentials.

## Implementation boundaries

The [architecture blueprint](../architecture/0001-bearforge-blueprint.md) records the design and planned evolution. It includes future capabilities; use the [onboarding guide](../onboarding.md) and command help for the currently implemented interface.

Bearforge stores declarative intent in the catalog/workspace definitions and observes Git state through the installed Git executable. Application services expose typed progress events; the CLI and Bubble Tea dashboard present the same underlying actions. Bubbles tables, lists and viewports and Lip Gloss styling adapt presentation without making provider results or UI progress authoritative. Progress buffering is bounded/coalesced so slow rendering cannot block journal finalization.

Mutations journal intent before Git effects and hold the root's mutation lock. Reviewed plans are checked again against current definitions and Git state before execution. Successful steps survive later failures; operations do not promise a transaction across repositories. Recovery verifies retained state rather than blindly replaying it, and arbitrary child commands are never rerun automatically. See [recovery behavior](../onboarding.md#recovery-and-completion).

Provider observations are bounded, timestamped snapshots held in process memory. API lookup is separate from Git transport authentication, and explicit user actions initiate network work. Saved provider connections support token authentication only. OAuth applications, token refresh and browser-authorization setup are outside the current flow.

Use a local filesystem root. Network shares/cloud-synced roots, manual root relocation, automatic adoption of existing clones/worktrees, custom remote-helper protocols, workspace templates and submodule provisioning are unsupported. Existing linked worktrees are not automatically migrated between ref backends. [Case-distinct refs](../onboarding.md#case-distinct-git-refs-on-windows-and-other-filesystems) documents filesystem-sensitive initialization/fetch behavior.

Release builds have additional clean-source, native macOS, toolchain and reproducibility requirements. Follow the [release guide](../releases.md#local-snapshot-and-reproducibility-checks) instead of treating a local build or cross-build as a production release.
