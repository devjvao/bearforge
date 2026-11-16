# Final integration fix: anonymous GitHub discovery

Finding: the dashboard offered anonymous browsing but always requested the authenticated `/user/repos` endpoint.

Implemented a required anonymous owner scope and a bounded, guarded public owner identity lookup. User and organization accounts route to their documented public repository endpoints; authenticated grants retain `/user/repos` and affiliation filtering. The public path keeps pagination scope, clone validation, complete-result semantics and request cooldowns, and rejects private metadata. The dashboard and onboarding guide explain the scope requirement.

Regression evidence: new public user/organization and missing-scope tests failed against the original implementation, which requested `/user/repos` and reached the API without scope. They pass after the fix. Additional cases reject owner redirects, mismatched identities, unsupported account types, scope-changing pagination, private metadata and late-page failures.

Validation: `go test ./internal/github -count=1`, `go vet ./internal/github`, and affected discovery/TUI tests/vet passed on Windows. Tests use local TLS fixtures and fake credentials; no real provider account was contacted or changed. Endpoint behavior checked against GitHub's official REST repository/user documentation.

The same docs commit includes the macOS agent's secure-store onboarding correction for release commit `59cc5b4`.
