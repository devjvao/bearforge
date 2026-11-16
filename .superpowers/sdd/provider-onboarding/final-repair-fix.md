# Final integration fix: review authentication reassociation

Finding: an unfinished import could be reassociated after a generic repository/path confirmation. Dry-run output omitted the previous and replacement connection/grant and scoped Git change.

Changes:
- Added a typed, nonsecret `ImportReassociationPlan` in result data, including each journal binding transition, actual catalog predecessor when present, unchanged origin/destination, and HTTPS helper URL/action. Retained reviewed helper identities remain visible during repeated repairs.
- Human dry-run output and the interactive confirmation use the same sanitized review. The replacement account is included when the selected saved connection has provider-attested identity; an unavailable identity is labeled unverified. SSH review explicitly leaves its helper/authentication settings unchanged.
- JSON/JSONL retain the structured plan. Existing plan digest revalidation, root locking, journal persistence and explicit grant resolution are unchanged.

Verification:
- RED: the new app regression failed because result data contained no binding review; then passed with the typed plan.
- RED: the human-output regression failed because it printed a generic JSON object instead of the old/new mapping; then passed with the shared review renderer.
- RED: a repeated-repair preview regression failed because the catalog predecessor was absent; then passed after adding it separately from the pending journal predecessor.
- Focused tests cover HTTPS/SSH plan JSON, exact origin/helper scope, account enrichment through the real CLI dry-run path, retained old journal grant, redirected-output sanitization and existing repeated-repair/refresh invariants.
- `go test ./internal/app ./internal/cli -run 'TestImport(Reassociation|ResumeDryRun|ReviewedGrant|RepeatedRepair|Repair)' -count=1`: PASS (app 20.462s; CLI 0.276s). `go vet ./internal/app ./internal/cli` and scoped `git diff --check`: PASS.
- Root agent owns the final whole-project suite, vet and platform builds; native Unix interactive terminal testing is not available on this Windows runner.

No live accounts, credential stores, external Git configuration or user repositories were changed; fixtures use local temporary repositories, fake secure storage and local HTTPS servers.
