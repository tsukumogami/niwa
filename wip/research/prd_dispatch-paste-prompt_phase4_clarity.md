# Clarity Review

**Verdict:** FAIL

The revision fixed the coverage gaps it set out to fix, but it introduced a hard
contradiction over whether the oversized refusal terminates the command, and
four requirements (R5, R17, R23, R28) still admit two readings that produce
materially different implementations.

## Ambiguous Requirements

### R5 -- the pasted/typed boundary clause

> The submitted text SHALL preserve the boundary between pasted and typed
> content: typed text SHALL NOT be joined onto the final line of a pasted block
> that did not end in a newline.

**Reading A (active).** The capture must detect where the paste ends and the
typing begins, and insert a separator when the paste's last line is
unterminated. This requires paste-boundary support from the terminal --
precisely the capability R4 says the guarantee must not depend on and R23
forbids probing for. On a terminal without bracketed paste the implementation
cannot know a boundary exists, so R5 becomes unsatisfiable there while being
stated unconditionally.

**Reading B (passive).** The capture must not *destroy* a boundary the
developer created. If the developer types immediately after an unterminated
paste with no intervening newline, the resulting single line is the developer's
own doing and is correct. Under this reading R5 adds nothing beyond R10's
byte-for-byte guarantee and is close to vacuous.

These build different things: Reading A requires bracketed-paste plumbing and a
synthetic separator that appears in the payload but was never typed (which sits
badly against R10's "no trimming, normalization, or re-encoding"); Reading B
requires nothing. The acceptance criterion ("returns paste plus typed text, with
the boundary preserved and the typed text not joined onto an unterminated final
pasted line") is a unit test over an injectable core fed synthetic input, so it
does not distinguish the two -- the test author picks the reading and the test
passes.

**Suggested rewording.** Decide which it is and say so. If Reading A: "When the
terminal delimits pasted blocks, the capture SHALL treat the end of a pasted
block as a line boundary, so typed text begins on a new line even when the
pasted block's final line was unterminated. On terminals that do not delimit
pasted blocks this behavior is unavailable and the developer supplies the
newline via R6." That scopes it honestly and stops contradicting R4's
unconditional framing. If Reading B: drop the clause entirely; R10 already says
the submitted bytes are preserved.

### R17 -- what "the text already entered" retains

> The capture SHALL remain open and SHALL retain the text already entered, so
> the developer can reduce it rather than lose it.

A developer pastes 200 KB into an empty capture. Three readings of what is in
the buffer afterwards:

**Reading A.** The buffer holds nothing from the oversized paste; "text already
entered" means text entered *before* the paste that crossed the ceiling. The
paste is discarded wholesale -- which is exactly the "losing the paste" outcome
the requirement exists to prevent, and makes the user story about sending a
smaller slice impossible.

**Reading B.** The buffer holds the ceiling-length prefix of the paste and the
remainder is dropped. The developer now has a silently truncated buffer that
looks complete -- the truncation R4 forbids, arriving through a different door.

**Reading C.** The buffer holds all 200 KB and is simply flagged as over the
ceiling; the developer deletes until it fits. This is the only reading under
which "reduce it rather than lose it" is literally achievable, and it is the
one the phrase implies -- but it means the capture holds a payload it has
already refused, and nothing states whether the refusal re-fires on every
keystroke or once.

**Suggested rewording.** "When input would carry the buffer past the ceiling,
the capture SHALL refuse the input, SHALL keep the full buffer contents
including the input that crossed the ceiling, and SHALL remain open so the
developer can delete text until the buffer fits. The refusal SHALL be raised
once per crossing, not per subsequent keystroke."

**Related tension with R18.** R17's stated purpose is "so the developer can
reduce it", and R18 forbids the message from instructing the developer "to
shorten or re-select the text". So the requirement pair says: retain the text
so it can be shortened, but never suggest shortening it. One implementer builds
an editable buffer whose whole point is trimming; another shows a
write-it-to-a-file message and treats the retained text as vestigial. Pick one
recovery path. If the file-reference is the real answer, R17's retention needs a
different justification (not losing the paste while you copy it out) and should
say that instead.

### R23 -- what "probe" forbids

> The command SHALL NOT probe the terminal for capability, and SHALL NOT warn
> about a missing capability.

**Reading A (narrow).** Forbids query/response round-trips -- sending a request
and reading the terminal's answer (DA1, DECRQM, and friends). Unconditionally
*enabling* a capability is permitted, since per the Decisions section "enabling
the capability is a silent no-op" on terminals that lack it. This reading leaves
bracketed paste available to DESIGN.

**Reading B (broad).** Forbids touching terminal capability at all, including
emitting an enable sequence. This forecloses bracketed paste entirely, which
would make Reading A of R5 unimplementable and would decide a DESIGN question
the PRD says it is not deciding.

The acceptance criterion makes it worse, not better: "Nothing written to the
render target is a capability query sequence" leaves "capability query sequence"
undefined. An implementer asserting on the enable sequence `ESC[?2004h` has to
decide for themselves whether a set counts as a query.

**Suggested rewording.** "The command SHALL NOT issue any terminal capability
query that requires reading a response, and SHALL NOT emit a warning or
diagnostic about a capability the terminal lacks. Unconditionally enabling a
capability whose absence is a silent no-op is permitted." Then the criterion can
name the forbidden class concretely rather than gesturing at it.

### R28 -- what end-of-input on an empty buffer exits with

> End-of-input on an empty buffer SHALL end the command without dispatching.

**Reading A.** Exit zero. The user story is explicit -- "submitting nothing to
end the command cleanly rather than dispatch an empty task" -- and "cleanly"
plus the deliberate avoidance of R29's "fail with the existing empty-prompt
error" both point at success.

**Reading B.** Exit non-zero. It is an empty prompt that did not dispatch, so it
behaves like R29's empty-capture refusal.

R31 was written to close exactly this class of question and does not close this
one: it enumerates the non-TTY refusal, the oversized refusal, the empty-capture
refusal, and abandonment, and omits end-of-input-on-empty. The acceptance
criterion ("on an empty buffer it returns the end-of-input outcome") tests the
capture core's return value, not the command's exit status, so neither reading
fails a test. A developer scripting around `niwa dispatch` gets different
behavior depending on which the implementer chose.

**Suggested rewording.** Add the case to R31 explicitly: "End-of-input on an
empty buffer SHALL end the command with exit status zero, having dispatched
nothing" (or non-zero, if that is the intent) -- and add a command-level
criterion asserting the status.

### R27 -- what "a visible indication" is

> The capture SHALL render a visible indication that the command is waiting for
> input before its first read.

**Reading A.** Any output before the first read. The criterion as written ("A
visible waiting indication is written to the render target before the first
read") is satisfied by a single space character.

**Reading B.** Human-readable text that communicates the command is waiting --
the thing that actually rescues the Known Limitation about a script inheriting
a terminal.

**Suggested rewording.** "...SHALL write to the render target, before its first
read, text naming the command and stating that it is waiting for input." Then
the criterion can assert non-empty output containing a fixed substring, which
can fail.

## Issues Found

1. **R9 and R31 contradict R17 on whether the oversized refusal is an exit
   path.** R17: on crossing the ceiling "the capture SHALL remain open". R9
   lists "the empty and oversized refusals" among "every exit path". R31: "the
   oversized refusal (R17) ... SHALL each exit non-zero with the command's
   ordinary error exit status." Both cannot hold -- a refusal that leaves the
   capture open does not exit. The acceptance criteria inherit the contradiction
   directly: "After a refusal the capture is still accepting input and the
   previously entered text is retained (R17)" against "the oversized refusal ...
   return[s] a non-zero status (R31)". These two tests cannot both pass against
   one implementation. **Fix:** remove the oversized refusal from R9's and R31's
   enumerations, and state instead that a capture abandoned or ended after an
   oversized refusal exits non-zero (which is what the functional criterion "An
   oversized paste followed by abandonment exits non-zero" already assumes).

2. **R30 says MAY; its acceptance criterion tests SHALL.** R30: "Rendering of
   the capture MAY sanitize control sequences for display." The criterion:
   "Submitted text containing terminal control sequences is returned unmodified,
   **while the bytes written to the render target are sanitized** (R30)." An
   implementation that echoes raw satisfies R30 and fails the criterion.
   **Fix:** either make sanitization a SHALL in R30 (it is the display-corruption
   mitigation the Decisions section argues for, so this looks like the intent),
   or drop the sanitization half of the criterion and test only payload fidelity.

3. **R19 names a mechanism and forecloses the terminal API choice.** "The
   capture SHALL disable canonical-mode line buffering before its first read" is
   a termios instruction, not a requirement. The PRD's Out of Scope promises "the
   choice of terminal API" to DESIGN; R19 mandates a specific POSIX line-discipline
   manipulation. The observable property underneath is what belongs here.
   **Fix:** "No per-line length limit SHALL apply to captured input. A single
   line longer than the terminal's line-discipline buffer SHALL be captured
   intact without truncation or hanging." The functional criterion already tests
   exactly that and needs no change.

4. **The R9 inspection criterion presumes design decisions the requirements
   leave open -- twice.** "Keyboard interrupt is not covered by a signal test
   because a capture in raw mode receives it as an input byte rather than as a
   signal; it is covered by the abandonment criterion instead." This assumes
   (a) DESIGN chooses raw mode with ISIG cleared -- a termios configuration that
   keeps ISIG would deliver SIGINT as a signal and make it testable -- and
   (b) that keyboard interrupt *is* the abandonment gesture, which no requirement
   states and which the Status section explicitly hands to DESIGN ("how
   cancellation is signalled"). The same presumption appears in Known
   Limitations. **Fix:** state the requirement (SIGINT during capture SHALL
   restore the terminal and abandon cleanly) and let DESIGN's mode choice
   determine whether the test is a signal test or an input-byte test; drop the
   pre-judged rationale.

5. **The SIGTERM/SIGHUP restoration criterion is misfiled under inspection.**
   Two `@critical` functional criteria already compare terminal mode before and
   after under a PTY ("The terminal's mode after an abandoned capture matches its
   mode before"; same for a normal submit). A harness that can do that can send
   SIGTERM to the process and compare the same two values. Filing it under "by
   inspection" parks something the existing harness demonstrably automates.
   **Fix:** move "The terminal's mode after the capture receives SIGTERM matches
   its mode before" and the SIGHUP equivalent into the functional bucket.

6. **R9 lists exit paths that no criterion verifies.** R9 covers submit,
   abandonment, the empty refusal, the oversized refusal, SIGTERM, and SIGHUP.
   Criteria exist for submit, abandonment, and (misfiled) the signals. Nothing
   verifies terminal restoration after the empty refusal. **Fix:** add a
   functional criterion, or narrow R9's enumeration.

7. **R26 is a tautology, not a requirement.** "Failures reachable before
   provisioning SHALL be raised before provisioning" defines its subject by the
   property it then demands. An implementer learns nothing about which failures
   are in the class. Its acceptance criteria (R7/R26 abandonment provisions
   nothing; R17/R26 oversized-then-abandon leaves no instance) are already
   carried by R7 and R17. **Fix:** either delete R26 and let R7 and R17 carry it,
   or enumerate: "The interactivity check, the argument-arity check, the
   empty-prompt check, and the size-ceiling check SHALL all be evaluated before
   any instance is provisioned."

8. **The three manual criteria carry no requirement tags and one is
   self-declared unfalsifiable.** Every other criterion in the document names the
   requirements it verifies. "Capture works inside a terminal multiplexer", "A
   large paste renders without visible corruption", and "Capture behaves
   correctly in each terminal named in the supported set" name none -- so they
   verify nothing traceable, and "works" / "visible corruption" / "correctly"
   have no pass condition. The third is admitted in Open Questions to be
   unfalsifiable without a named terminal set. **Fix:** tag them (the multiplexer
   and terminal-set criteria are R4's only evidence on terminals lacking paste
   boundaries, which is the whole reason R4 claims to be unconditional), give
   each a concrete pass condition ("a 50 KB pasted block submits and the worker's
   argv matches the source byte-for-byte"), and resolve the terminal set before
   the PRD leaves Draft rather than shipping a criterion the PRD says cannot fail.

9. **R18's acceptance criterion tests a literal string, not the requirement.**
   "it does not contain 'shorten' (R18)". R18 forbids instructing the developer
   to shorten *or re-select*; a message reading "trim your paste and try again"
   passes the test and violates the requirement. **Fix:** test the positive half
   (the message contains both byte counts and names the file-and-reference
   approach) and drop the negative substring assertion, which cannot carry the
   semantic requirement.

10. **The requirement that the capture renders to standard error exists only
    inside R21's rationale clause.** R21 requires stderr to be a terminal
    "since the capture ... renders to the latter"; the criterion under R22 then
    asserts "its rendering goes to the error stream". So the render target is a
    tested behavior with no requirement stating it. **Fix:** state it -- "The
    capture SHALL render to standard error" -- and let R21 be purely the gate.

11. **"session" carries two meanings; "prompt" carries two meanings.** "Session"
    means the developer's interactive shell in R1 ("on an interactive session")
    and R20, and the dispatched worker's session in the goals ("attached to a
    session already working on it"), R7 ("no session mapping"), and R21 ("the
    command's existing session hints"). "Prompt" means the text sent to the
    worker in R12/R14/R29 ("empty-prompt error", "prompt size ceiling") and the
    interactive UI in R1 ("SHALL open a prompt that captures multiline text") and
    R21 ("niwa renders prompts to standard error"). **Fix:** use "interactive
    invocation" or "terminal" for the former sense of session, and "capture" for
    the UI sense of prompt -- the document already has that word and uses it
    everywhere else.

12. **"Cancellation" is a third name for abandonment.** The Status section gives
    DESIGN "how cancellation is signalled"; R9 says "abandonment (however
    signalled)"; R8 requires the command to report "a cancelled capture". Three
    labels for one concept, in a document that leans on the
    abandonment/end-of-input distinction to carry R8, R28, and R31. The
    distinction between abandonment and end-of-input *is* held consistently --
    that part works -- but the extra synonym undercuts it. **Fix:** use
    "abandonment" throughout, including in the Status section.

13. **R8 requires a report with no stated content and no criterion.** "the
    command reports a cancelled capture rather than dispatching or reporting an
    empty prompt" -- nothing states what is reported, and the only R8 criterion
    tests the capture core's sentinel value, not the command's output. Two
    implementers ship different messages, or one ships none (a bare non-zero
    exit arguably "reports"). **Fix:** either state the message requirement or
    drop "reports" and keep R8 purely about the distinguishable result.

14. **R24 does not exist.** Requirements run R1-R23 and R25-R33. A reader cannot
    tell whether R24 was removed deliberately or lost in revision. **Fix:** add a
    one-line note that R24 was withdrawn, or renumber. (Related: R10 is filed
    under "Size ceiling" though it is a payload-fidelity requirement, and reads
    oddly after R19.)

## Suggested Improvements

1. **Resolve the D1 fallback against R25's acceptance criterion.** D1 says that
   if issue #225 does not land first, "this feature must carry the same
   correction". R25's criterion says "On the positional path, exit codes,
   messages, and argv construction match the pre-change baseline". Under the
   fallback, this feature changes the positional path's ceiling, so the
   pre-change baseline and the #225 baseline are different documents and the
   criterion becomes ambiguous about which one it means. State it: "the baseline
   is the behavior after #225's correction, whether that correction lands
   separately or inside this feature."

2. **Name the supported platform set.** R14 derives from "the tightest supported
   platform" and R15 forbids platform-conditional definitions, but the supported
   set is never enumerated -- and the scope notes say linux and darwin differ
   materially on argument length. Without the set, 130,433 is not checkable
   against the derivation.

3. **Say whether the R14 prepend is unconditional.** R14 says "everything niwa
   *may* prepend"; R10 says the bytes appear byte-for-byte "subject only to the
   prepend accounted for in R14"; Known Limitations says "the reserve costs
   headroom on invocations where nothing is prepended". So the prepend is
   sometimes absent, and R10 reads as though it always applies. An implementer
   writing the argv-equality test needs to know which invocations get it.

4. **Add a user story for the `niwa watch` caller.** R11 is load-bearing (the
   Decisions section says so) and the cron story covers only the
   prompt-argument path. A story for a caller that reaches the launcher
   directly would make R11's necessity visible from the stories rather than only
   from the Decisions section. The six existing stories are otherwise specific
   and well-tied to requirements -- each names a concrete situation and a
   consequence, and the "nothing to paste" story maps cleanly onto R28.

5. **Move R3's last sentence out of the requirement.** "Because arity selects the
   path, no invocation can request both a positional prompt and a capture" is a
   consequence of R3's first two sentences, not an additional obligation. Same
   for R19's second sentence, which is rationale. Both belong in Decisions and
   Trade-offs.

## Summary

This revision is substantially tighter than what the FAIL verdict describes --
every requirement now carries a criterion, the criteria name what they exercise,
and the abandonment/end-of-input distinction that R8, R28, and R31 depend on is
held consistently across all three. It fails on a hard contradiction the
revision introduced: R31 and R9 declare the oversized refusal an exit path that
returns non-zero while R17 requires the capture to stay open, and two acceptance
criteria encode both sides so they cannot both pass. Alongside that, four
requirements admit implementation-changing double readings (R5's pasted/typed
boundary, R17's retention scope, R23's definition of probing, R28's exit status),
R30 tests a MAY as a SHALL, R19 mandates a termios mechanism the PRD promised to
DESIGN, and the SIGTERM/SIGHUP restoration criterion is parked under inspection
despite a PTY harness that already performs the identical before/after
comparison twice.
