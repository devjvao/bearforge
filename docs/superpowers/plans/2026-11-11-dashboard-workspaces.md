# Dashboard workspace actions implementation plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task by task.

**Goal:** Create empty workspaces and safely remove individual worktrees or whole workspaces from the dashboard.

**Architecture:** Keep Git, validation, locking, journaling and recovery in the existing application services. Add an optional dashboard capability and a modal controller/view using the existing serialized I/O gate, cancellation ownership and coalesced progress bridge. Bind every confirmation to a concrete, read-only preview.

**Tech stack:** Go, Bubble Tea v2, Lip Gloss v2, existing config/Git/journal adapters.

Work in the supplied checkout, preserving prior uncommitted work. Leave changes uncommitted.

- [x] Add service tests for creation previews, stale approvals, removal identity/drift, partial results and recovery. Run them failing before implementing.
- [x] Add guarded creation alongside the compatible existing API; strengthen removal preview identity and expose truthful progress/results.
- [x] Test and implement `n` creation form, `d` selected repository removal, `D` whole workspace removal, preview, `y` confirmation, results/recovery, cancellation and stale-message guards. Reuse service adapters without mutating their observer.
- [x] Update help/README and record the remaining Phase 2 backlog.
- [x] Run the full suite, vet/build/static analysis, focused native terminal and visual checks; obtain one independent review, fix material findings, and record evidence.

Creation uses the workspace name as the default branch unless explicitly supplied. It creates no repository branches or worktrees. Removal preserves history through the existing service, refuses unsafe worktrees and retains unrelated container files. Filters affect only the selected repository action; whole-workspace removal always previews every member. Recovery stays explicit through `operations` commands.

Review focus: approval cannot migrate between roots, targets or action kinds; dirty/drifted worktrees cannot be removed; results distinguish completed, uncertain and unstarted work; quitting cancels and drains finalization; smallest supported terminal still exposes confirmation/recovery details.
