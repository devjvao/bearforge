# Final integration fix: native macOS release authentication

Finding: release builds forced `CGO_ENABLED=0`, selecting the deliberately unavailable macOS secret store rather than the Security.framework Keychain implementation.

Changes:
- Darwin archives now enable cgo with `/usr/bin/clang`; Linux/Windows retain pure Go builds.
- Requests for Darwin release targets fail before source/output access on non-macOS hosts, with an actionable `--targets` alternative.
- Compiler, cgo and SDK overrides are removed from inherited build environment; fixed flags are supplied for Darwin.
- The existing release job runs on macOS, logs selected compiler/SDK inputs, and verifies checksums with native `shasum`.
- Release docs explain native build requirements and compiler/SDK reproducibility inputs. The onboarding Keychain paragraph is left for the controller's combined documentation commit.

Evidence:
- RED: both new regressions failed against the original implementation (cgo disabled and no early host error).
- GREEN: `go test ./internal/release -count=1` passed.
- `go vet ./internal/release` passed.
- `git diff --check` passed (normal line-ending warnings only).
- PyYAML parsed `.github/workflows/release.yml`; runner and checksum command assertions passed.
- Disposable Windows snapshot packaging smoke started separately; controller will receive its completion result.

Limitations: this Windows environment cannot compile/run the native macOS Keychain backend or execute the hosted release workflow. No tag, push, signing or publication was performed. Native macOS CI remains required.
