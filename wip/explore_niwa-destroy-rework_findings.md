# Exploration Findings: niwa destroy rework

## Core Question

How should `niwa destroy` behave so it's contextual to where it's run — destroying
the enclosing instance from inside, offering a picker (or wiping the entire
workspace under `--force`) from the root, and using the existing shell-wrapper
landing-path protocol to drop the user out of any directory it just deleted?
What's the safest, least-surprising UX for the workspace-self-destroy path?

## Round 1

### Key Insights

- **The tsuku picker is genuinely reusable but blocked from a clean module import.** It's a 210-line, single-dep helper at `tsuku/internal/tui/picker.go` exposing `Pick(prompt, []Choice) (int, error)` plus `IsAvailable()` and `ErrCanceled`. Only dep is `golang.org/x/term`, which niwa already requires. The `internal/` location prevents cross-module import; the right path is **copy into `niwa/internal/tui/`**. (L1)
- **`niwa destroy` is NOT shell-wrapper-aware today.** The wrapper at `internal/cli/shell_init.go:52-71` whitelists `create|go|init|session create`. Destroy currently falls through to `command niwa "$@"`, so `NIWA_RESPONSE_FILE` is never set and adding a `writeLandingPath` call alone produces no auto-cd. The fix is a one-line wrapper change plus a couple of golden-string test updates — precedent already set by `init` and `session create` extending past the design docs. (L2)
- **Destroy and reset are siamese twins.** Every helper (`ResolveInstanceTarget`, `ValidateInstanceDir`, `CheckUncommittedChanges`, `DestroyInstance`) is shared with `niwa reset` with identical sequencing. The rework must land as **additive sibling helpers**, never in-place helper edits, or reset silently changes. (L3)
- **`ValidateInstanceDir`'s "refuses workspace root" guard is a critical safety invariant.** The new "wipe whole workspace" path under `--force` must NOT loosen this validator — it needs a separate sibling helper (e.g. `DestroyWorkspace`) with its own safety checks (registry cleanup, ManagedFiles cleanup, idempotency on partial failure). (L3)
- **Niwa has zero interactive prompts today** — no survey, no bubbletea, no bufio stdin readers. Destroy will be the first. The new patterns to establish: a `term.IsTerminal(os.Stdin.Fd())` check, the picker UI, and a small typed-confirmation reader. Everything else is conventions-as-they-are: stderr for interactive output, stdout for the final summary, lowercase-verb `fmt.Errorf` with `; use --force to override`, `hintShellInit` after success. (L4)
- **The non-pushed-work scan is feasible at sub-2s for realistic workspaces.** Three to five git plumbing commands per working tree (`status --porcelain`, `for-each-ref refs/heads`, `stash list`, `worktree list --porcelain`, optional detached-HEAD check), parallelized at the existing `cloneWorkers=8` pattern from `apply.go:1093`. New file `internal/workspace/scan.go` with `Loss`, `LossKind`, `RepoScan`, `InstanceScan` types. (L5)
- **Worktree scanning is mandatory, not optional.** Niwa itself creates session worktrees under `<instance>/.niwa/worktrees/<repo>-<session-id>/` via `internal/mcp/handlers_session.go:188`. Without scanning worktrees, an active session would silently vanish on workspace destroy. (L5)
- **Three PRDs need amendments and one new PRD is warranted.** Touch: `PRD-shell-integration.md` (R1, R11, R14 ACs, Out-of-Scope, D3), `PRD-cross-session-communication.md` (R38 multi-instance clause, AC-P11), `PRD-workspace-config-sources.md` (one-line softening on line 1001). New: `PRD-niwa-destroy.md` covering contextual mode selection, picker UX, and wrapper cd-out-of-deleted-dir as a coherent set. Plus three design-doc updates (instance-lifecycle, shell-navigation-protocol, contextual-completion Decision 3). (L6)
- **The "cd-eligible command list" has no canonical home.** It's distributed across PRD-shell-integration R1 ("create, go"), DESIGN-mesh-session-lifecycle (adds `session create`), DESIGN-niwa-init-creates-workspace-dir (adds `init <name>`), and the actual code in `shell_init.go`. Destroy will be the third feature in a row to extend it without consolidation. (L6)

### Tensions

- **Destroy vs reset.** Adding picker UX and contextual semantics to destroy alone leaves reset on the old surface. Either we extend reset (bigger scope, consistent UX, larger PR) or we accept divergence (focused PR, future drift). The user explicitly invoked "Rework `niwa destroy`" — implying scoped — but research surfaced this as a real fork in the road.
- **`--force` semantic overload.** Today `--force` only means "skip uncommitted-changes guard." The new design layers three more meanings: skip the picker, wipe the workspace, bypass the typed-confirmation gate when there's unpushed work. Help text and PRD must spell out the overload. The user already chose this consolidation in their last reply.
- **Untracked files: noise or signal.** Including them as a count line (`untracked: 47 files`) keeps the report honest without spam. Excluding them risks losing genuinely new code. v1 should include but collapse to a count.
- **Single-instance picker behavior.** When the workspace has exactly one instance, skipping the picker is the natural UX. Always showing it adds explicit confirmation but feels like ceremony. L4's table proposes "destroy that instance (no picker, no prompt)."

### Gaps

- **Destroy-from-inside no-arg confirmation.** Today's behavior is silent destroy. The spec preserves this implicitly — but research surfaced it as a deliberate UX choice worth confirming, since the new flow adds prompts elsewhere.
- **Registry cleanup.** Today `DestroyInstance` removes only the instance dir. The new workspace-self-destroy should also clean up the workspace registry entry in `~/.config/niwa/config.toml`. Not yet specified.
- **Empty-workspace edge cases.** "Lax" definition is settled, but: what if a workspace has only `.niwa/` (config) plus an instance directory whose `.niwa/instance.json` is corrupt? The detector should treat orphans as dirty (force user to confirm) per L5's edge-case table.
- **Workspace-wipe order.** Sequential (clean output, 5s × N grace) vs concurrent (fast, output races). PRD-cross-session-communication R38 doesn't constrain this. Recommend sequential, deterministic order in the design doc.
- **Typed-confirmation vs landing-path timing.** The typed prompt MUST happen before any `writeLandingPath` call so a user who hits ESC isn't `cd`-ed away from a workspace they didn't actually destroy. (L6 Open Q #3.) Easy to encode in the design doc; flag for crystallize.

### Resolved (auto-mode decisions, Round 1)

<!-- decision:start id="reset-scope" status="confirmed" -->
### Decision: Reset scope

**Question:** Should this rework also apply to `niwa reset`, or stay focused on destroy?

**Evidence:** User's original task framing was "Rework `niwa destroy`" with no mention of reset. Reset is structurally a different verb (destroy-then-recreate from config) — its workspace-wipe semantic doesn't apply. Reset shares all four destroy helpers (`ResolveInstanceTarget`, `ValidateInstanceDir`, `CheckUncommittedChanges`, `DestroyInstance`) per L3 findings. The rework was already constrained to additive sibling helpers (no in-place edits) precisely to keep reset working unchanged.

**Choice:** Destroy only.

**Alternatives considered:**
- Destroy + reset together: doubles surface, doubles tests, doubles PRD scope. The user's explicit framing didn't ask for this.

**Assumptions:**
- Reset's existing UX (name-or-cwd resolution) remains acceptable to users.
- A future "reset rework" follow-up is feasible if needed; helpers stay shared.

**Consequences:** Reset will not get the picker UX. Destroy and reset diverge on contextual semantics. The shared helpers stay untouched, so reset's behavior is preserved by construction.

**Reversibility:** medium — extending to reset later means another PR but no new architecture.
<!-- decision:end -->

<!-- decision:start id="single-instance-picker" status="confirmed" -->
### Decision: Single-instance picker behavior

**Question:** When the workspace has exactly one instance, does `niwa destroy` (no arg, at root) skip the picker and destroy directly, or always show the picker?

**Evidence:** L4 findings establish niwa's house style as flag-driven and non-interactive when the action is unambiguous. The picker exists to disambiguate — when there's nothing to disambiguate, a picker is ceremony. The dirty-repo gate still fires per existing rules.

**Choice:** Skip-and-go. With exactly one instance, destroy that instance directly (still subject to the existing dirty-repo `--force` gate).

**Alternatives considered:**
- Always-show picker: forces an explicit pick step even when there's only one option. Adds friction without adding safety.

**Assumptions:**
- The dirty-repo gate is sufficient safety for the single-instance case (consistent with today).

**Consequences:** Single-instance workspace UX matches today's "no arg → destroy enclosing" feel; user types `niwa destroy` once and the only instance is removed.
<!-- decision:end -->

<!-- decision:start id="confirmation-token" status="confirmed" -->
### Decision: Typed-confirmation token

**Question:** When workspace-self-destroy detects unpushed work, what does the user type to confirm — the workspace name, or a fixed string like `DESTROY`?

**Evidence:** Industry convention (GitHub repository deletion, Heroku app deletion, Stripe account deletion) all use the resource name. Niwa already has `EffectiveConfigName` / `resolveEffectiveWorkspaceName` to derive the override-aware name (per L4 findings on `effective_name.go`). A fixed token like `DESTROY` would train muscle memory and defeat the safety guard.

**Choice:** Workspace name (override-aware via `EffectiveConfigName`).

**Alternatives considered:**
- Fixed string `DESTROY`: easy to type, easy to muscle-memory through. Defeats the purpose.
- `y/N` prompt: standard but doesn't carry the "I really mean THIS workspace" signal.

**Assumptions:**
- Workspace names are short enough to type once. Names are reasonable identifiers (no special chars that defeat shell quoting).

**Consequences:** The prompt resolves the effective workspace name and demands an exact match. Mismatch → abort with "confirmation did not match; aborting" and exit non-zero.
<!-- decision:end -->

<!-- decision:start id="prd-r2-cleanup" status="confirmed" -->
### Decision: PRD-shell-integration R2 cleanup

**Question:** Should this PR retire PRD-shell-integration R2's stale "stdout protocol" wording while we're amending the PRD anyway?

**Evidence:** L6 findings confirm R2 has been obsolete since the NIWA_RESPONSE_FILE protocol replaced it (DESIGN-shell-navigation-protocol). Cleanup is independent of destroy semantics. Bundling unrelated cleanup with a feature PR makes review harder.

**Choice:** Out of scope. Leave R2 alone in this PR; flag for a follow-up doc cleanup.

**Alternatives considered:**
- Bundle the R2 cleanup: tempting because we're already touching the PRD, but adds review surface unrelated to destroy.

**Assumptions:**
- A doc-only cleanup PR is feasible later.

**Consequences:** PRD-shell-integration retains an obsolete R2 line until a separate cleanup PR. Destroy's R1 amendment will note "see also R2 (deprecated)" so the inconsistency is at least called out.
<!-- decision:end -->

<!-- decision:start id="workspace-wipe-order" status="confirmed" -->
### Decision: Workspace-wipe destroy-instance ordering

**Question:** When `niwa destroy --force` from the workspace root wipes N instances, do we destroy them sequentially or concurrently?

**Evidence:** PRD-cross-session-communication R38 doesn't constrain order. L6 raised this as an open. Each instance's `TerminateDaemon` call has up to 5s grace (`NIWA_DESTROY_GRACE_SECONDS`). For realistic N (≤5), sequential is bounded at ~25s worst case, which is acceptable at a confirmation prompt. Concurrent execution introduces output races (instances log progress to stderr in interleaved fragments) and complicates error handling.

**Choice:** Sequential, deterministic order (alphabetical by instance name).

**Alternatives considered:**
- Concurrent with bounded worker pool: faster but interleaves output and complicates error reporting. Realistic gain (~1.5x) doesn't justify the UX cost.

**Assumptions:**
- Realistic workspaces have ≤5 instances, so 5s × N is tolerable.
- Users prefer clean, predictable output to faster wall-clock time at this rare prompt.

**Consequences:** The output for `niwa destroy --force` (workspace wipe) is a clean per-instance progress narration. Total time scales linearly with N but is bounded in practice.
<!-- decision:end -->

<!-- decision:start id="inside-instance-no-prompt" status="confirmed" -->
### Decision: Confirmation prompt when destroying from inside an instance

**Question:** When the user runs `niwa destroy` (no arg) from inside an instance, do we add a confirmation prompt or preserve today's silent behavior?

**Evidence:** Today's destroy is silent (no prompt). L4 findings establish niwa's principle of minimum surprise: the user typed the destructive command from inside the target — they meant it. Adding a prompt here while preserving silence elsewhere is inconsistent. The existing dirty-repo gate (with `--force` bypass) still fires.

**Choice:** No new prompt. Preserve today's silent destroy from inside an instance, gated only by the existing dirty-repo check.

**Alternatives considered:**
- Add a `y/N` prompt for symmetry with the workspace-self-destroy case: feels redundant (instance-from-inside is a single explicit action) and changes today's well-understood UX.

**Assumptions:**
- The existing dirty-repo gate is sufficient safety for instance destruction.
- Users running `niwa destroy` from inside an instance are intentional.

**Consequences:** Two new prompts (picker, typed-confirmation) appear only in the new branches; the today-equivalent path stays silent.
<!-- decision:end -->

### Open Questions

None blocking. All Round 1 open questions resolved above; remaining design-doc-level details (e.g., how the picker renders the Description column for each instance) are best settled in the design doc.

## Decision: Crystallize

Round 1 produced sufficient evidence and resolved all open questions via the lightweight decision protocol. Proceeding to artifact-type selection.

## Accumulated Understanding

The reworked destroy is structurally a **medium-sized, focused change**:

- **Code surface**: rewrite `internal/cli/destroy.go` (linear → branched control flow), copy a picker into `internal/tui/`, add `internal/workspace/scan.go` (new helper, new types) and `internal/workspace/destroy_workspace.go` (new helper for the workspace-wipe path). Keep all four existing destroy helpers untouched (they're shared with reset).
- **Wrapper change**: one line in `internal/cli/shell_init.go` to add `destroy` to the `case` whitelist; two golden-string assertions to update.
- **Testing**: at least one `@critical` Gherkin scenario (the spec touches `init → create → destroy` lifecycle), unit tests for the new helpers, integration tests that mirror `go_test.go`'s response-file pattern.
- **Documentation**: amend three PRDs, write one new PRD (`PRD-niwa-destroy.md`), amend three design docs (or write a fresh `DESIGN-niwa-destroy.md` and cross-link).

The **biggest design choice still on the table** is whether `niwa reset` rides along. The technical research is complete enough to crystallize a design doc; the only blocker is settling that scope question and the four secondary opens above.

The **biggest implementation risk** is dual: (1) accidentally changing reset's behavior by editing shared helpers in place — must be enforced by code review, since the test suite covers reset's current behavior but not the seams, and (2) wrapper landing-path order — destroy must `writeLandingPath` BEFORE the final RemoveAll, otherwise the wrapper's `[ -d "$dir" ]` guard will silently skip the cd and strand the user in a deleted directory.
