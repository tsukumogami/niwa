# Completeness Review

**Verdict:** FAIL

Every requirement now carries at least one criterion and all four BRIEF-deferred
questions are closed, but three user-visible behaviors the document depends on --
that the developer can see what they captured, that they can remove text after an
oversized refusal, and what to do when issue #225 has not landed -- are stated
only by implication or not at all.

## Requirement Coverage Map

Criteria are referenced by section and position: **U***n* = unit tests over the
injectable capture core, **C***n* = command-level unit tests, **F***n* =
`@critical` functional, **I***n* = verified by inspection, **M***n* = verified
manually.

| Req | Verifying criteria | Assessment |
|---|---|---|
| R1 (no-arg opens capture) | C1, F1 | Covered |
| R2 (arg path never consults terminal) | C5 | Covered |
| R3 (zero-or-one arity) | C6 | Covered |
| R4 (multiline captured whole) | U1, U2, U3, F1 | Covered |
| R5 (typed + pasted, boundary preserved) | U4 | Covered as written; the requirement itself is underspecified (Issue 6) |
| R6 (manual newline gesture) | U5 | Covered |
| R7 (abandonment creates nothing) | C7 | Covered |
| R8 (abandonment distinguishable from EOF) | U7 | Covered |
| R9 (terminal restored on every exit path) | F3, F4, I4 | Covered for submit, abandonment, SIGTERM/SIGHUP. The empty and oversized refusals, which R9 names explicitly, have no restoration criterion |
| R10 (byte-for-byte argv) | C1, C2, F1 | Covered |
| R11 (no capture off the interactive command path) | C11 | Covered |
| R12 (`dispatch ""` still errors) | C9 | Covered |
| R13 (`--detach` composes) | C10 | Covered |
| R14 (derived ceiling) | U8, U9 | Covered |
| R15 (single ceiling, no platform branch) | I1 | Covered |
| R16 (ceiling applies to captured text) | U9 | Covered |
| R17 (refuse at the crossing, keep the text) | U10, F5 | Partially covered -- "so the developer can reduce it" is unreachable (Issue 2) |
| R18 (refusal message content) | U11 | Covered |
| R19 (no per-line limit) | F6 | Covered |
| R20 (non-interactive refuses) | C3, F2 | **Partially covered** -- the "message naming the argument form that works" clause has no criterion (Issue 4) |
| R21 (stdin and stderr both TTY) | C3 | Covered |
| R22 (stdout redirectable) | C4 | Covered |
| R23 (no probe, no capability warning) | U2, U13 | Covered by U13; the R23 tag on U2 is not exercised (Issue 7) |
| R25 (positional path unchanged) | C13 | Covered, but the criterion's baseline wording is ambiguous (Issue 3b) |
| R26 (fail before provisioning) | C7, F5 | Covered |
| R27 (visible waiting indication) | U14 | Covered |
| R28 (EOF submits / ends) | U6 | Covered |
| R29 (empty submission errors) | C8 | Covered |
| R30 (payload preserved, render may sanitize) | U12 | Covered |
| R31 (non-zero exit on all refusals) | C12 | Covered |
| R32 (docs updated) | I2 | Covered |
| R33 (bounded functional timeout) | I3 | Covered |
| D1 (issue #225 dependency) | none | **UNCOVERED** -- no criterion verifies the corrected cap or the launcher backstop on the fallback path (Issue 3) |

**Retired numbers:** R24 is absent and the document never says so. R23 to R25 is
the only gap in the sequence; a reader cannot tell whether R24 was retired or
lost in editing.

**Untagged criteria:** the three "Verified manually before release" criteria name
no requirement, contradicting the section preamble ("Each criterion names the
requirements it verifies"). See Issue 7.

**Criteria tagged to a requirement they do not exercise:** one, U2 (see Issue 7).
The eight retags this revision claims to have made hold up otherwise -- U9's R16
tag, C1's R10 tag, and C7's R26 tag each land on something the test would
actually catch.

## Issues Found

1. **Nothing requires the developer to see what they have captured.** The Problem
   Statement names the workaround's blindness as a defect in its own right ("a
   capture that runs blind, since nothing echoes back what was taken before it is
   sent"), and the third user story depends on the input being "still on screen."
   No requirement states that captured input is rendered. R27 requires only a
   waiting indication *before the first read*; R30 says rendering "MAY sanitize
   control sequences," which presupposes a render that no requirement mandates.
   The only thing pinning it down is U12's second clause, which asserts against
   bytes on the render target -- an acceptance criterion carrying a requirement
   the Requirements section does not state. An implementer could build a capture
   that reads without echoing, satisfy every requirement, and reproduce the exact
   defect the feature exists to remove. *Fix:* add a requirement in Capture
   behavior that submitted-so-far input is rendered as it is entered, and make
   R30's "MAY sanitize" a qualification of that requirement rather than its only
   trace.

2. **R17 promises a recovery the requirements make impossible.** R17 says the
   capture retains entered text "so the developer can reduce it rather than lose
   it," and the third user journey turns on sending a smaller slice. Nothing in
   the document gives the capture any means of removing text -- no backspace, no
   line kill, no deletion of the paste that crossed the ceiling. U10 asserts only
   that the capture "is still accepting input," which is the opposite direction.
   R17 is also silent on what happens to the crossing bytes themselves: if the
   oversized paste is retained, the buffer sits permanently above the ceiling and
   abandonment is the developer's only exit; if it is discarded, the paste is lost
   and the requirement's promise is empty. *Fix:* state which of the two the
   refusal does, and state that the capture supports removing entered text (the
   gesture is DESIGN's, the capability is not).

3. **D1 does not tell an implementer what to do when #225 has not landed.**
   Three problems compound:
   (a) D1 says "this feature must carry the same correction," but the correction's
   content appears nowhere. The "launcher backstop" is named once, in D1, and is
   never defined -- not what it checks, not where it sits, not what it reports.
   R26 ("failures reachable before provisioning SHALL be raised before
   provisioning") is an ordering rule, not a backstop.
   (b) Out of Scope says "Correcting the prompt size cap itself, tracked as issue
   #225 and stated here as dependency D1" -- excluding exactly the work D1
   conditionally requires. An implementer facing an unlanded #225 has two
   sections telling them opposite things.
   (c) No acceptance criterion covers the corrected cap or the backstop on either
   branch, and R14's derivation is written over `maxArgStringBytes` and
   `dispatchPromptReserve`, which D1 admits are "terms introduced there" -- so on
   the fallback branch R14 names symbols that do not exist yet.
   *Fix:* state the correction's substance as a requirement conditioned on the
   dependency (validate against the derived ceiling before provisioning, and
   re-check immediately before exec), give it a criterion, and rewrite the Out of
   Scope line to say the *tracking* is elsewhere rather than the work.

4. **R20's message clause is unverified.** R20 requires failing "with a message
   naming the argument form that works." C3 checks only that three of four
   TTY combinations "refuse without reading." Compare R18, whose criterion checks
   message content explicitly and even asserts an absent word. *Fix:* extend C3
   to assert the refusal names the positional-argument form.

5. **The ceiling admits input sizes already measured not to complete.** The
   testability research recorded that echo cost through the obvious library path
   appears superlinear: a 20,000-byte single line did not finish inside 20 seconds
   under the probe, while 12,000 did. The ceiling is 130,433 bytes -- 6.5x the
   size that already failed to complete. No requirement bounds how long the
   capture may take to accept input up to the ceiling, Known Limitations does not
   record the measurement, and F6 can be satisfied at ~5,000 bytes (just past the
   line-discipline buffer) without ever approaching the hazard. The result is a
   ceiling the document commits to serving and a real chance that inputs well
   under it are unusable. *Fix:* either add a responsiveness requirement (input up
   to the ceiling is accepted without perceptible stall), or record the
   measurement as a Known Limitation and raise the size in F6 so the functional
   test exercises a payload in the range the feature actually targets.

6. **R5's boundary preservation has no stated mechanism and sits against R10.**
   R5 forbids typed text from being joined onto an unterminated final pasted line.
   The only way to satisfy that is to insert a separator the developer did not
   type -- which R10 ("no trimming, normalization, or re-encoding... byte-for-byte")
   and R30 ("preserved exactly") do not anticipate. R5 also does not say what the
   separator is, or whether a developer who *wants* to continue the last line (a
   paste ending mid-line, plus one appended word) is simply forbidden from doing
   so. *Fix:* say what is inserted and when, and add a sentence to R10 exempting
   the separator so the two requirements do not read as contradictory.

7. **Criterion tagging gaps.** U2 ("embedded newlines and no paste delimiters
   does not return after the first line") is tagged R4, R23 -- it exercises R4's
   unconditional guarantee but tests neither of R23's prohibitions (no probe, no
   warning); U13 does that. Separately, the three manual criteria carry no
   requirement tags at all, and one of them ("each terminal named in the supported
   set") points at an unresolved Open Question, making it unfalsifiable by the
   PRD's own admission. *Fix:* drop the R23 tag from U2; tag the manual criteria;
   name the terminal set before this PRD moves past Draft.

8. **R9 names five exit paths; two of them have no criterion.** F3 and F4 cover
   abandonment and submit, I4 covers SIGTERM and SIGHUP. The empty refusal and the
   oversized refusal are named in R9's own list and have no restoration criterion
   -- F5 checks the exit status and the absence of an instance, not the terminal
   mode. *Fix:* extend F5 to diff terminal state, or add the refusals to F3's
   assertion.

## Suggested Improvements

1. **Add a user story for the promptless non-interactive invocation.** The
   existing operator story covers the *argument* path ("calls `niwa dispatch` with
   a prompt argument"). The R20/R21/R22/R27 cluster -- four requirements and five
   criteria -- has no user in the stories. The Decisions section describes exactly
   the scenario ("a mis-written hook's redirect from `/dev/null`"); it belongs
   upstream as a story so the requirements have a stated user.

2. **Note the retirement of R24 explicitly.** A one-line note in the Status
   section keeps the numbering auditable against the prior jury round.

3. **Cover suspend and resume in R9's exit-path list.** A raw-mode capture that
   receives SIGTSTP and is resumed is the classic way to leave a terminal broken
   in a way the developer blames on the next command. It fits R9's list naturally
   and is cheaper to require now than to discover later.

4. **Reword C13's baseline.** "The pre-change baseline" is ambiguous once D1
   exists -- it could mean today's behavior or the post-#225 behavior. R25 already
   says "relative to the baseline established by issue #225"; the criterion should
   use the same phrase.

5. **Restate R19 in terms of the property, not the mechanism.** "SHALL disable
   canonical-mode line buffering" names a terminal-API operation that Out of Scope
   explicitly reserves for DESIGN. The property -- no per-line length limit applies
   to input -- is what the requirement needs, and F6 already tests the property.

## Summary

The revision is a substantial improvement: all 32 live requirements now carry at
least one criterion, the retagged criteria hold up under inspection, the criteria
are filed at levels whose harnesses can genuinely fail them, and all four
BRIEF-deferred questions are closed with reasoning and rejected alternatives. It
fails on three user-visible behaviors an implementer would have to invent --
whether captured input is displayed at all, how a developer gets back under the
ceiling after an oversized refusal, and what the fallback actually consists of
when issue #225 has not landed (where D1 and Out of Scope currently contradict
each other). A fourth concern is that the research's measured echo cost puts
inputs well below the stated ceiling at risk of not completing, which neither the
requirements nor Known Limitations acknowledge. Each of these is closable with a
requirement or two rather than a restructure.
