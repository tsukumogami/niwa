# Architecture Review: DESIGN-contextual-completion

**Doc:** `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/docs/designs/DESIGN-contextual-completion.md`
**Reviewer focus:** Solution Architecture, Implementation Approach, Implicit Decisions.
**Verdict:** Approve with minor edits.

## Summary

The design is well-scoped, the phasing is sensibly ordered (data before closures before functional tests), and the decision trail is traceable. The three implicit decisions each have a real alternative, not a strawman. The Solution Architecture is detailed enough that a new contributor can open it and start Phase 1 immediately.

However there are two concrete inconsistencies that will bite an implementer, plus a coverage-table count that doesn't reconcile with the prose. Fixing these is mechanical, not structural.

## Finding 1: `ListRegisteredWorkspaces` signature inconsistency (must-fix)

The design declares three different signatures for the same function:

- Decision 4 narrative (line 263): `config.ListRegisteredWorkspaces() []string is added, returning sorted registry keys` — no error return.
- Solution Architecture > Components (line 513): `config.ListRegisteredWorkspaces() []string` — sorted registry keys from `GlobalConfig.Registry`. No error return.
- Solution Architecture > Key Interfaces (line 553): `func ListRegisteredWorkspaces() ([]string, error) // sorted, nil on missing config`.

The Key Interfaces signature is the outlier. Which one does Phase 1 implement?

This matters because:
1. Closures swallow errors silently (Implicit Decision C), which slightly favors the `([]string, error)` shape — closures get to decide what to do with the error, consistent with `EnumerateRepos` and `EnumerateInstances`.
2. But call sites in `go.go` and `create.go` that migrate to the helper (per Phase 1 deliverables, line 643-645) are non-completion call sites that actually want errors surfaced. Returning `[]string` only would silently collapse "config missing" into "no workspaces registered" for production command paths, a behavior regression.

**Recommendation:** pick `([]string, error)`, fix the Decision 4 text and the Components bullet to match. The Considered Options analysis you flagged in instructions matches Key Interfaces' `([]string, error)`, which is almost certainly the intended signature.

## Finding 2: Coverage table is one row short of the "11 positions" claim (should-fix)

- Decision Outcome (line 424): "11 of 14 identifier positions get dynamic completion in v1 (skipping `create -r`, `config set global <repo>`, `create --name` — all free-form or pre-existence positions)."
- Coverage table (lines 604-613): 10 rows.

Counting: apply (2), create (1), destroy (1), go (3), reset (1), status (1), init (1) = 9 rows if you split `go [target]` as one row or 10 if you count it once. The table has 10 rows. The prose promises 11 covered positions.

Cross-referencing the coverage-map research (`wip/research/explore_contextual-completion_r1_lead-command-flag-coverage-map.md`), the 14 candidate positions include `niwa init --from <repo>` (row 11 in the research) as a free-form URL — correctly deferred. So the promised 11 positions minus 3 deferred (`create -r`, `create --name`, `config set global`) should match the research's 14 minus those 3 minus `init --from` = 10, not 11.

Either the prose count is wrong (actually 10 covered) or the table is missing a row. The likely cause: the prose says "14 candidate positions" but the research file enumerates 13 numbered rows; one of the counts drifted. The Context and Problem Statement (line 54) says "Fourteen identifier positions" while "Considered Options" silently works in terms of 13.

**Recommendation:** reconcile the count explicitly. Either update the prose to "10 of 13 positions" or add the missing row to the coverage table (if there is a real missing position). My reading of the research is that 10 is correct.

## Finding 3: Phase 2 doesn't restate sanitization (nit)

Phase 1 deliverables explicitly mention that "both enumeration helpers (including `EnumerateInstances`) filter out entries whose names contain `\t`, `\n`, or ASCII control characters (< 0x20)" (lines 638-640). Phase 2 doesn't re-state this.

This is acceptable because sanitization is a data-layer concern, not a closure concern. Phase 2's unit tests assume the enumeration helpers are already sanitized; they don't need to re-verify it. The instructions flagged this, but it reads correctly: Phase 1 owns sanitization, Phase 2 consumes sanitized output. No gap.

Optional polish: Phase 2's "Deliverables" list could add a one-line note that closures rely on Phase 1's sanitized helpers, but this is a stylistic nit, not a correctness gap.

## Finding 4: Phase 1's scope is bigger than it looks (should-fix)

Phase 1's deliverables include migrating four call sites in `go.go` and `create.go` to `ListRegisteredWorkspaces()`, plus migrating `findRepoDir` and its three callers to `EnumerateRepos`. That's a non-trivial refactor bundled into "data layer."

A new contributor reading "Phase 1: Data layer" expects to add two functions and unit-test them. They will discover mid-phase that they also need to rewrite `findRepoDir` (whose current semantics — short-circuit on first match, return "ambiguous" on duplicate — differ from `EnumerateRepos`'s return-all-deduped contract). The existing callers of `findRepoDir` want the current semantics, so the migration is actually "keep `findRepoDir` but have it call `EnumerateRepos` internally" — which should be stated.

**Recommendation:** split Phase 1 into 1a (add the two helpers + unit tests) and 1b (migrate call sites), or explicitly note in Phase 1 that `findRepoDir` is rewritten to delegate to `EnumerateRepos` while preserving its short-circuit/ambiguous-name semantics. Otherwise an implementer will either (a) break callers by changing `findRepoDir` semantics, or (b) leave the duplication in place and silently narrow Phase 1's scope.

## Finding 5: `EnumerateRepos` contract is under-specified (should-fix)

Solution Architecture (line 562-565) says `EnumerateRepos` skips `.niwa` and `.claude` control directories and dedupes names that appear in multiple groups. Implicit Decision A says "names only, dedupe on collision."

Questions an implementer will ask:
1. Does the sort happen before or after dedup? (After — Phase 1 says "sorted".)
2. Is the sort stable or does it use `sort.Strings`? (Assume `sort.Strings`; please say so.)
3. What happens if `instanceRoot` doesn't exist, isn't readable, or contains no group dirs? Solution Architecture doesn't say. `EnumerateInstances` probably returns `([]string{}, nil)` for "empty workspace" but closures swallow errors anyway.
4. Is a "group" any top-level directory except `.niwa` / `.claude`, or must it have a specific marker? The coverage-map research (`findRepoDir` at `repo_resolve.go:13-51`) treats every non-control top-level dir as a group. The design should reference this explicitly.
5. Control-byte sanitization: Phase 1 says both enumeration helpers filter control bytes. Is the filter applied to group names, repo names, or both? (Should be both, since a group name with a tab would confuse `findRepoDir`'s display layer too.)

**Recommendation:** add a "Contract" subsection under the `EnumerateRepos` interface that pins down empty-result handling, sort semantics, and the definition of "group dir."

## Finding 6: `completeGoTarget` as specialized closure is the right call (no change)

Instructions asked whether `completeGoTarget` could be replaced by composing `completeRepoNames` and `completeWorkspaceNames`. Implicit Decision B already addresses this — the alternative was wiring an inline lambda that calls both helpers and concatenates. The chosen path (dedicated closure) keeps decoration and collision handling in one named function.

I agree with the chosen approach. Composition at the wiring layer would scatter decoration logic (Decision 8) across a lambda in `go.go`'s `init()`, which is less discoverable than a named function in `completion.go`. The decision is sound and the alternative is real, not a strawman.

## Finding 7: Two-tier test strategy is justified (no change)

Instructions asked whether unit tests alone would catch the same bugs. Decision 5's rationale addresses this: "wiring bugs (closure attached to wrong flag, directive lost in middleware)" specifically need the functional tier. A closure that returns correct candidates but is attached to `RegisterFlagCompletionFunc("workspaces", ...)` instead of `("workspace", ...)` will pass every unit test and fail every shell invocation.

The counter-argument would be "use a table-driven test that builds the full command tree and invokes `__complete` against it" — essentially in-process functional testing. This is viable and is what cobra's own `completions_test.go` does. The design could mention this as a considered alternative in Decision 5, but the functional tier catches strictly more (e.g., godog sandbox env-var propagation, real process exec latency). Keeping both tiers is the safer call. No change needed.

## Finding 8: `EnumerateRepos` could reuse existing helper (nit, no change)

Instructions asked whether `EnumerateRepos` extraction could be skipped by using an existing helper. `findRepoDir` is the only candidate, and its short-circuit semantics make it wrong for completion. The coverage-map research (`repo_resolve.go:13-51`) confirms this. Extraction is unavoidable.

No simpler alternative exists without changing `findRepoDir`'s contract, which would ripple into three non-completion callers.

## Implicit Decisions: all three have real alternatives

- **A (names vs paths/tuples):** real alternative. Paths would genuinely enable disambiguation for cross-group name collisions. Chosen is justified because the user types the repo name, not the path.
- **B (specialized closure vs composition):** real alternative. An inline lambda would work; the chosen option trades LOC for discoverability.
- **C (silent errors vs `ShellCompDirectiveError`):** real alternative. Cobra's `__complete` protocol really does support `Error` directive; the chosen rationale (UX disruption on transient errors) is grounded in cobra's actual behavior.

None are strawmen. The section is well-executed.

## Inconsistencies summary

| Location | Section A | Section B | Severity |
|---|---|---|---|
| `ListRegisteredWorkspaces` signature | Decision 4: `() []string` | Key Interfaces: `() ([]string, error)` | must-fix |
| v1 position count | Outcome: "11 of 14" | Coverage table: 10 rows | should-fix |
| Context count | Intro: "Fourteen identifier positions" / "eleven positions" | Problem rationale: varies between 11/14 | nit (follow from above) |

## What a new contributor can do today

- Phase 1a (add `EnumerateRepos` + `ListRegisteredWorkspaces`): yes, with the signature fix from Finding 1.
- Phase 1b (migrate `findRepoDir` and four `config.Registry` iterations): partial — needs the `findRepoDir` migration semantics spelled out per Finding 4.
- Phase 2 (closures + wiring): yes, straightforward once Phase 1 is in.
- Phase 3 (functional tests): yes, the sketch is concrete.
- Phase 4 (polish): yes.

Overall the design is implementable. The above fixes would remove the remaining ambiguity.
