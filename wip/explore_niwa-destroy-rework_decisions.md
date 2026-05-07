# Exploration Decisions: niwa destroy rework

## Round 1 (settled before research)

- Trailed-off "These are probably the only" sentence in the original ask was an accident; ignore.
  Why: user's explicit clarification.
- Empty workspace + `niwa destroy` (no arg) deletes the workspace without `--force`, lands user one level up.
  Why: empty case has nothing to lose; foot-gun risk is minimal.
- Non-empty workspace + `niwa destroy` (no arg) shows a picker.
  Why: better UX than today's terse error listing instance names.
- `niwa destroy <name>` is only valid from the workspace root, never from inside an instance.
  Why: ergonomic asymmetry that prevents accidental destruction of the wrong instance.
- Per-instance destroy keeps today's `--force` semantics (skip uncommitted-changes guard).
  Why: scope creep avoided; the broader non-pushed-work check is workspace-self-destroy only.
- Empty-workspace definition is **lax**: no instance directories present is sufficient; other files (`.niwa/`, leftover `wip/`, stray editor files) are deleted along with it.
  Why: requiring strict cleanup is more friction than the foot-gun risk warrants.
- Workspace-self-destroy under `--force` scans every instance for non-pushed work, including across git worktrees. Clean → delete silently. Dirty → list affected instances/branches/worktrees and require typed confirmation.
  Why: irreversible workspace deletion deserves a confirmation path that's bypassable when work is already pushed.

## Round 1 (settled by research)

- Picker reuse strategy: **copy** the tsuku picker into `niwa/internal/tui/`.
  Why: tsuku's picker is under `internal/`, blocking module import. Copy is cheaper than reorganizing tsuku for cross-module export. (L1.)
- Wrapper update is a one-line change adding `destroy` to the case whitelist plus golden-string test updates.
  Why: precedent already set by `init` and `session create`; protocol primitives need no changes. (L2.)
- New `internal/workspace/scan.go` is the home for the comprehensive non-pushed-work detector.
  Why: keeps existing `CheckUncommittedChanges` (used by reset) untouched; isolates new detection logic. (L3, L5.)
- New sibling helper `DestroyWorkspace(workspaceRoot)` for the workspace-wipe path; existing `ValidateInstanceDir` "refuses workspace root" invariant preserved.
  Why: critical safety guard; loosening it would break reset's protection too. (L3.)
- Worktree scanning is mandatory in the non-pushed-work detector.
  Why: niwa itself creates session worktrees under `.niwa/worktrees/`; missing them risks silent loss of in-flight session work. (L5.)
- Sequential per-instance destroy under workspace `--force`; deterministic order.
  Why: the user is waiting at a prompt, output races are worse than wall-clock; 5s × N grace is acceptable for the realistic N. (L6 open question, recommended in findings.)
- Typed-confirmation prompt fires BEFORE `writeLandingPath`.
  Why: user must be able to ESC out without their shell being `cd`-ed away from a workspace they didn't actually destroy. (L6.)
- New PRD warranted: `PRD-niwa-destroy.md`.
  Why: rework introduces three substantial new behaviors (contextual mode selection, picker UX, wrapper cd-out-of-deleted-dir) that span three existing PRD surfaces with no canonical home. (L6.)
- Design doc artifact: `DESIGN-niwa-destroy.md` (new), with amendments to existing design docs cross-linking it.
  Why: same coherence argument as the new PRD.
