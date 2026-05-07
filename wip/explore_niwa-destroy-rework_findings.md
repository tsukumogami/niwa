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

### Open Questions

- Should the rework also apply to `niwa reset`? (Highest-leverage open question.)
- Should single-instance picker behavior be skip-and-go or always-show?
- What does the typed-confirmation prompt ask the user to type — the workspace name (override-aware via `EffectiveConfigName`), or a fixed string like `DESTROY`?
- Should the rework retire PRD-shell-integration R2's stale "stdout protocol" wording while we're amending the PRD anyway, or keep that scope-creep out?

## Accumulated Understanding

The reworked destroy is structurally a **medium-sized, focused change**:

- **Code surface**: rewrite `internal/cli/destroy.go` (linear → branched control flow), copy a picker into `internal/tui/`, add `internal/workspace/scan.go` (new helper, new types) and `internal/workspace/destroy_workspace.go` (new helper for the workspace-wipe path). Keep all four existing destroy helpers untouched (they're shared with reset).
- **Wrapper change**: one line in `internal/cli/shell_init.go` to add `destroy` to the `case` whitelist; two golden-string assertions to update.
- **Testing**: at least one `@critical` Gherkin scenario (the spec touches `init → create → destroy` lifecycle), unit tests for the new helpers, integration tests that mirror `go_test.go`'s response-file pattern.
- **Documentation**: amend three PRDs, write one new PRD (`PRD-niwa-destroy.md`), amend three design docs (or write a fresh `DESIGN-niwa-destroy.md` and cross-link).

The **biggest design choice still on the table** is whether `niwa reset` rides along. The technical research is complete enough to crystallize a design doc; the only blocker is settling that scope question and the four secondary opens above.

The **biggest implementation risk** is dual: (1) accidentally changing reset's behavior by editing shared helpers in place — must be enforced by code review, since the test suite covers reset's current behavior but not the seams, and (2) wrapper landing-path order — destroy must `writeLandingPath` BEFORE the final RemoveAll, otherwise the wrapper's `[ -d "$dir" ]` guard will silently skip the cd and strand the user in a deleted directory.
