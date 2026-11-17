# Build and verify Bearforge releases

Production releases are created by `.github/workflows/release.yml` after a version tag is pushed. The tag must identify a commit merged into `main`. The reusable CI workflow first tests Linux, Windows and native ARM64 macOS 26 (`macos-26`) on the Go release pinned in `go.mod` and runs the disposable 60-repository validation. The release workflow enables `macos-arm64: true`; ordinary PR/main CI uses Intel macOS 26 instead. Existing published releases are never overwritten.

The canonical source module is `github.com/devjvao/bearforge`. The currently published `v0.2.0` tag still declares the earlier module path; install a verified archive or follow [source build instructions](development/README.md#build-from-source) until a tag containing the module-path change is published. Do not assume `go install github.com/devjvao/bearforge/cmd/bearforge@latest` works against the existing release.

The release job runs on ARM64 macOS 26 (`macos-26`) with exact Go 1.27.4, `-trimpath`, `-mod=readonly` and `-buildvcs=false`, with version/commit/dirty metadata injected explicitly. Darwin builds enable cgo and link Apple's Security.framework for saved Keychain credentials; Linux and Windows remain pure Go cross-builds with cgo disabled. The workflow records the selected Apple clang and macOS SDK versions and paths. Production source must be clean and the version must match a tag at HEAD. Archives use the commit timestamp and fixed metadata; no wall-clock build date is embedded. Workflow actions and Cosign are pinned. [Go release downloads](https://go.dev/dl/), [Sigstore signing](https://docs.sigstore.dev/cosign/signing/signing_with_blobs/) and [GitHub provenance](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations) describe the upstream tools.

## Artifacts

Linux and macOS use `.tar.gz`; Windows uses `.zip`. Both amd64 and arm64 are built. Each archive contains the executable, README, MIT license, user/developer guides, PR-review setup and architecture reference, plus `sbom.cdx.json` derived from modules actually linked into that binary. Standalone CycloneDX SBOMs, `manifest.json` and all archives are listed in sorted `checksums.txt` entries.

CI signs the exact checksums using Cosign and GitHub OIDC and verifies the resulting `checksums.txt.sigstore.json` bundle before publication. No production signing key is stored locally or in a repository secret. Build provenance attestations cover the checksummed artifacts. The manifest records the source commit, dirty/snapshot status, toolchain, target names and binary hashes.

## Verify before extracting

Download your archive, `checksums.txt` and `checksums.txt.sigstore.json` from the same release. Install a trusted Cosign distribution and verify the certificate against the exact repository workflow and tag. Replace `v0.2.0` with the release you downloaded; do not widen the identity to an arbitrary workflow or tag.

```sh
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity 'https://github.com/devjvao/bearforge/.github/workflows/release.yml@refs/tags/v0.2.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt
sha256sum --check --ignore-missing checksums.txt
```

On macOS, use `shasum -a 256 -c checksums.txt` after downloading all listed files, or compare the selected archive's hash to its signed entry. PowerShell verification for a single Windows archive:

```powershell
$version = 'v0.2.0'
$archive = 'bearforge_0.2.0_windows_amd64.zip'
cosign verify-blob --bundle checksums.txt.sigstore.json `
  --certificate-identity "https://github.com/devjvao/bearforge/.github/workflows/release.yml@refs/tags/$version" `
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' checksums.txt
if ($LASTEXITCODE -ne 0) { throw 'Signature verification failed' }
$entry = Get-Content checksums.txt | Where-Object { $_.EndsWith("  $archive") }
if (@($entry).Count -ne 1) { throw 'Missing or duplicate signed checksum' }
$actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $entry.Split(' ')[0]) { throw 'Archive checksum mismatch' }
```

An additional provenance check is `gh attestation verify ARCHIVE --repo devjvao/bearforge`. A checksum without a verified signature only detects transfer errors; it does not establish who published it. [Sigstore verification](https://docs.sigstore.dev/cosign/verifying/verify/) explains the certificate/bundle checks.

## Local snapshot and reproducibility checks

The release helper only builds files; it never creates tags, pushes, publishes or requests a production signing identity. Building all six default targets requires a macOS host with Apple clang and the macOS SDK selected through `xcode-select`. Darwin release targets fail early on other hosts rather than shipping an executable without saved authentication. Use a new or empty output directory; existing artifacts are never overwritten or recursively cleaned:

```sh
go run ./cmd/release --snapshot --version v0.2.0-snapshot --out .tmp/release-one
go run ./cmd/release --snapshot --version v0.2.0-snapshot --out .tmp/release-two
```

Compare both `checksums.txt` files. With identical source, toolchain, targets and inputs, they should match byte for byte. Darwin reproducibility also requires the same Apple clang, selected SDK and runner image recorded by the build run; a matching Go version alone is insufficient. The helper fixes the compiler to `/usr/bin/clang`, controls cgo flags and discards inherited compiler/SDK overrides. On Linux or Windows, add `--targets windows/amd64,linux/amd64` to each command for a supported local smoke build. Snapshot mode allows dirty source or a different supported Go patch and marks both explicitly in the manifest; snapshots have no production signature bundle. Local cross-builds do not prove native behavior on another OS.

`bearforge version --json` reports the injected version, commit, dirty state, actual Go toolchain and platform. Production rebuilds need the exact pinned Go version and clean tagged checkout. Source builds, CI and production releases use the latest stable Go release pinned in `go.mod`; older Go releases are unsupported. CI and release workflows read that version directly from `go.mod`, and the production release toolchain requirement must match it.

## Publish a version

After review, merge, native CI and the scale test succeed, choose a new unused semantic version, create its tag on the merged commit and push that tag. The workflow signs, verifies and publishes the artifacts. Inspect its run and verify a downloaded archive before announcing the release. Tag publication and merge are explicit maintainer actions; adding this workflow does not imply either has happened.

Company private-repository validation was deferred by the maintainer until after Phase 2 is merged. Disposable scale tests provide controlled coverage; they do not represent company repository contents or policies.
