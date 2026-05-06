# Completeness Review

## Verdict: PASS

The PRD is implementable as-written; remaining gaps are minor (a few unaddressed UX research recommendations and one or two AC wording tightenings) and do not require another full revision pass.

## Issues Found

1. **Suggestion text for `ErrTargetDirExists` is unspecified.** Phase 2 UX (Implications #6) recommends pinning the suggestion wording (proposed: `"Pick a different name or remove <abs path> and retry."`). R5 says the error "MUST suggest a single remediation option" but never names what that option is. Implementer must guess. Suggested fix: add a sentence to R5 specifying the suggestion wording, or add an AC under "Conflict handling" verifying the suggestion mentions removing/renaming.

2. **Stderr override note (R4) lacks an "only when names differ" clause in the AC.** R4 fires only when "the cloned `--from` config's `[workspace] name` differs from the explicit positional `<name>`," but AC-8 only covers the differs-case. There's no AC verifying the converse: when the upstream name *equals* the positional, niwa MUST NOT emit the override note. Without this, an implementer could emit the note unconditionally and pass every AC. Suggested fix: add AC-8b: "When `niwa init my-name --from org/upstream` runs and the upstream config declares `[workspace] name = \"my-name\"`, niwa does NOT emit the override note."

3. **Phase 2 UX Implication #4 (muscle-memory heuristic) was rejected, but the rejection rationale is weaker than the research suggests.** Research called the silent-double-nesting failure mode "genuinely bad" and the false-positive rate "near-zero." The PRD rejects the heuristic citing scope creep / false-positive risk, leaning on the absolute-path success message (R9) as sufficient. Known Limitations #1 acknowledges the silent-nesting case persists. This is a defensible trade-off, but a reviewer who reads the research first will notice the PRD downgraded a "yes, include it" recommendation without addressing the research's actual argument. Suggested fix: add one sentence to the rejection rationale in "Decisions and Trade-offs" → "Backward compatibility" addressing the research's "near-zero false positive" claim head-on (e.g., "even with low false-positive rate, an informational note about pre-existing user behavior conflicts with the clean-break stance").

4. **AC-21 (`niwa go`) does not verify the `cd` happens "from any directory."** R10 says "from any directory" but AC-21 only states "from any directory" without specifying the test must include at least one directory other than the workspace's parent. This is a minor wording issue; functional tests typically run from a known cwd. Suggested fix: clarify AC-21 to require the test invokes `niwa go my-ws` from a directory that is NOT `/some/dir` and NOT `/some/dir/my-ws`.

5. **No AC verifies validation runs *before* filesystem writes for the conflict-detection path.** R7 requires upfront validation, and ACs 14-18 each say "exits non-zero before any filesystem write." Good. But R5 also says "before any filesystem writes" for the target-exists check, and AC-9/10/11 say "No directory is created" only for AC-9. AC-10 and AC-11 don't have the equivalent assertion. Suggested fix: add "No directory is created at `<cwd>/my-ws`" to AC-10 and AC-11 for symmetry.

## Suggested Improvements

1. **Address the `niwa apply` AC more concretely.** AC-6 says `niwa apply` "references `my-name` in its output, not `upstream`." Phase 2 research (user-researcher Lead B, Story 7) framed this as "system reflects my intent consistently." `niwa apply` output may have multiple references; the AC could pin a specific one (e.g., the success line, or the workspace-name banner) so the test is less ambiguous.

2. **Document the order of preflight checks.** The PRD says name validation runs before the target-exists check (R7) and the target-exists check runs before niwa-state validation (Decisions: "Pre-flight conflict shape"). Adding a one-line ordering summary in R7 or the decisions section ("Order: name validation → target-exists → niwa-state checks") would help the implementer and reviewer at a glance.

3. **Phase 2 UX Lead 1 Open Question 3** (should the unrelated-directory suggestion mention `niwa init` no-name as a recovery option?) is unaddressed. The PRD specifies the conflict error must "suggest a single remediation option" without picking which one. Consider explicitly listing what the single remediation is for each sub-case (file → "pick a different name"; unrelated dir → "pick a different name or remove the directory"; symlink → "pick a different name or remove the symlink"), or commit to a single uniform suggestion.

4. **CI/automation user story (US-7) lacks an AC.** US-7 says CI operators read help text and README to update pipeline scripts. AC-22 covers help text and AC-23/24 cover README, so the story is technically backed, but no AC explicitly verifies the help/README content is sufficient for a CI scripter (e.g., it shows a `niwa init <name> --from ... && cd <name> && niwa apply` shape). This is a soft gap; the AC list as-is is probably fine.

5. **Known Limitations #3 ("on-disk toml diverges from effective name") deserves a debugging-aid mention.** A user inspecting `.niwa/workspace.toml` after an override will see the upstream name and may file a confused issue. Consider whether `niwa status` should display both names (effective + on-disk) for transparency, or whether instance state should record the override explicitly so it's discoverable. Out of scope for this PRD, but worth a follow-up issue note.

## Summary

The PRD is implementable, well-structured, and addresses the bulk of the Phase 2 research recommendations (regex application, registry rebind warning, override semantics, README updates, conflict sub-case routing). The few gaps are tightening opportunities — specifying the new error suggestion wording (Issue 1), adding a "doesn't fire when names match" AC for the override note (Issue 2), and improving symmetry across the conflict-detection ACs (Issue 5). The deliberate rejection of the muscle-memory heuristic is defensible but should engage with the research's near-zero-false-positive argument more directly. None of these block implementation.
