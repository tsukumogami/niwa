# Clarity Review

## Verdict: PASS
The PRD's requirements and acceptance criteria are concrete, binary, and grounded in observable `git status --porcelain` output, so two developers would build substantially the same thing; the few ambiguities are minor and do not threaten convergence.

## Ambiguities Found
1. R1 / Acceptance criterion 1 ("niwa-authored file" vs. fixture pattern): R1 forbids any "niwa-authored file" from appearing, but acceptance criterion 1 only fixes the `.gitignore`-has-no-`*.local*` case. The notion of "niwa-authored" is never enumerated or defined as a closed set -> A reviewer can't independently determine which untracked files count as niwa's without reading code, though R7's "empty git status" assertion sidesteps this for the test. -> Add a one-line definition: "niwa-authored = any file niwa writes into a managed working tree or worktree," and note the test asserts a fully-empty status (not a niwa-only subset), so the distinction never needs adjudication.

2. R3 ("any re-sync of an existing worktree"): "re-sync" is named as a covered operation but never tied to a concrete command, unlike `niwa apply` and `niwa session create` elsewhere. -> A developer doesn't know which command/flow triggers a re-sync or how to exercise it in a test. -> Name the command or flow that constitutes a re-sync, or state explicitly (as Decisions does: "covered transitively because it uses the same materialization path") that no separate test path is required.

3. R2 / R4 ("a location that is not committed to the managed repository"): The constraint is clear, but the phrase "not committed" admits two readings -- a file inside the repo that is itself gitignored, vs. a location physically outside the repo working tree. -> Two developers could pick structurally different mechanisms (e.g., `.git/info/exclude` vs. an out-of-tree store). -> This is deliberately deferred to design (Out of Scope item 3 says so), so it's acceptable; consider an explicit forward-reference from R2 to that Out-of-Scope note so the openness reads as intentional, not as a gap.

4. Acceptance criterion 5 ("user content is preserved"): R5 says niwa "adds its entries without discarding or reordering content it did not write." "Preserved" is verifiable, but whether niwa's own entries must land in a fixed position (top, bottom, sorted) is unspecified. -> Idempotency (R6) plus ordering-preservation could still allow two compliant-but-different placements of niwa's block. -> State where niwa's entries go relative to pre-existing content (e.g., "appended after existing content"), so R6's no-duplication check is unambiguous.

## Suggested Improvements
1. Define "niwa-authored file" once in the Requirements preamble: rationale -- it appears in R1, R3, R4, R7, and acceptance criteria, and the whole guarantee hinges on it; a single definition removes any read-time interpretation.
2. Tie "re-sync" (R3) to a concrete command or explicitly state it shares the materialization path and needs no separate assertion: rationale -- every other operation in the PRD is anchored to an invocable command, making this the one untestable-as-written clause.
3. Specify entry placement for R5/R6 (append vs. sorted-merge): rationale -- makes the idempotency assertion mechanically checkable and prevents two implementations from producing different-but-"valid" coverage files.

## Summary
This PRD is unusually concrete for a clarity review: nearly every requirement and acceptance criterion reduces to an objectively verifiable `git status --porcelain` outcome, and R7's "assert empty status, don't enumerate file names" framing makes the central guarantee binary and regression-proof by construction. The handful of soft spots -- an undefined "niwa-authored file," an unbound "re-sync" operation, and unspecified entry placement -- are minor and don't put two developers on divergent paths, especially since the recording mechanism is explicitly and intentionally deferred to design. It passes.
