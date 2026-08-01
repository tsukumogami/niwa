# Clarity Review

**Verdict:** FAIL

The prose is unusually disciplined and most requirements are sharp, but six
requirements admit readings that produce materially different implementations --
and one (R18) is internally contradictory in the exact place the PRD says the
wording carries the weight.

## Ambiguous Requirements

### R18 -- "name a concrete alternative" contradicts the rest of the document

> The refusal message SHALL state the size of the input, state the limit, and
> name a concrete alternative rather than instructing the developer to shorten
> the text.

Reading A: the message points the developer at a specific other way to get the
text to the worker. Reading B: the message tells them to send a smaller slice --
which is what the BRIEF's third journey says the developer does, and what the
Goals section says ("send a smaller slice instead of losing it"), but which is
precisely the "shorten the text" phrasing R18 forbids.

Reading A has no referent inside this PRD. Every alternative a message could
name -- `$EDITOR`, a clipboard flag, a prompt-file flag, stdin -- is in Out of
Scope. So an implementer taking Reading A has nothing to write, and one taking
Reading B writes a message the requirement rejects. Two implementers produce
different messages, and neither can be said to have failed the requirement.

The answer exists upstream and did not make it into the PRD:
`wip/research/prd_dispatch-paste-prompt_phase2_size-ceiling.md` (line 364) says
"put the text in a file and dispatch a prompt that points at it." That is not
the excluded `--prompt-file` flag -- it is ordinary prompt text referencing a
path -- so it survives the scope boundary. It should be stated.

**Suggested rewording:** "The refusal message SHALL state the byte size of the
input, state the byte limit, and direct the developer to write the text to a
file and dispatch a prompt referencing that path. It SHALL NOT rely on
instructing the developer to shorten or re-select the text."

Note also that the same research recommends presenting the limit in round terms
("about 127 KB") while enforcing the exact byte count. R18 says nothing about
units or rounding; two implementers will differ on whether the message reads
"130433 bytes" or "about 127 KB".

### R10 -- "unchanged" contradicts R14's prepend

> The captured text SHALL reach the worker unchanged, as the same single argv
> element the positional path produces.

Reading A: the worker's argv element equals the captured bytes exactly. Reading
B: the captured bytes appear verbatim inside the argv element, which is
constructed the same way the positional path constructs it.

R14 defines `dispatchPromptReserve` as "the length of everything niwa may
prepend to the prompt after validation," so under Reading A the requirement is
unsatisfiable -- niwa always prepends. The acceptance criterion for R10 says the
worker's argv "contains the pasted text verbatim," which is Reading B, so the
intent is clear, but the requirement text is not. R10 is also silent on two
transformations an implementer will otherwise decide unilaterally: whether a
trailing newline is trimmed, and whether the terminal's own escape framing is
stripped (it obviously must be, yet "unchanged" is absolute).

**Suggested rewording:** "The bytes the developer submitted SHALL appear
byte-for-byte in the worker's argv, in the same single argv element the
positional path produces, subject only to the prepend accounted for in R14. No
trimming, normalization, or re-encoding SHALL be applied to the submitted
bytes."

### R2 / R25 vs R16 -- which baseline does "unchanged" measure against?

R25 says no behavior on the positional-argument path changes: "same exit codes,
same messages, same argv construction." R16 says the ceiling applies "not only
against a positional argument," which asserts it applies to the argument path
too. R14's derived ceiling is lower than today's mis-set cap, so applying it to
the argument path *does* change that path's behavior for prompts in the affected
band.

Reading A: the baseline is post-#225 main, so the corrected ceiling is already
there and R25 is about everything else. Reading B: the baseline is current main,
in which case R25 forbids what R14 and R16 together require.

The Decisions section says "This PRD states its requirement over that baseline,"
which settles it -- but the settlement lives in prose 120 lines away from the
requirement, and neither R2 nor R25 carries the qualifier. An implementer
working from the Requirements section alone hits a direct conflict, and their
resolution depends on whether #225 happens to have landed yet.

**Suggested rewording (R25):** "Relative to the baseline established by issue
#225, no behavior on the positional-argument path SHALL change: same exit codes,
same messages, same argv construction. The size ceiling of R14 applies to both
paths."

R2 and R25 also substantially duplicate each other (R2: "SHALL behave exactly as
it does today ... byte-identical to current behavior"; R25: "No behavior on the
positional-argument path SHALL change"). One of them should carry the
preservation guarantee and the other should be dropped or narrowed -- as written
they are two statements of the same rule that can be independently amended out
of sync.

### R8 -- end-of-input is load-bearing but never defined

> Abandonment SHALL be distinguishable from end-of-input in the captured result,
> so the command reports a cancelled capture rather than treating it as an empty
> prompt.

The requirement makes "end-of-input" a first-class outcome and then never says
what the command does with it when the buffer is non-empty. Reading A:
end-of-input submits whatever has accumulated (the `cat` convention, and the
behavior of the workaround the BRIEF documents). Reading B: end-of-input is a
terminating condition equivalent to submitting nothing, and only the submit
gesture dispatches.

These are different products. Under A, Ctrl-D is a second submit gesture, which
sits against R5's "no mode to choose" only awkwardly; under B, a developer who
reaches for the muscle memory the workaround taught them loses their paste. The
PRD delegates "how cancel is signalled" to DESIGN, which is right, but *whether
end-of-input dispatches* is a requirement-level question about what the user
gets, not a gesture choice.

Related: R9 enumerates "normal submit, abandonment, error, and receipt of
SIGINT" as four distinct exit paths, which implies SIGINT is not abandonment. If
that is intended, R7 and R8 do not cover SIGINT and the PRD never says whether a
SIGINT-terminated capture reports a cancellation or provisions nothing. If it is
not intended -- if SIGINT *is* the abandonment signal under most plausible
designs -- then R9's enumeration is misleading.

**Suggested rewording:** add a requirement stating what end-of-input on a
non-empty buffer does, and rewrite R9's list as "normal submit, abandonment
(however signalled, including by SIGINT), error, and receipt of SIGTERM or
SIGHUP."

### R17 -- refusal at paste time or at submit time?

> Input exceeding the ceiling SHALL be refused while the capture is still
> accepting input, before any instance is created, leaving the developer's
> session recoverable.

Reading A: the capture measures as bytes arrive and rejects the paste at the
moment it lands -- which is what the BRIEF's journey describes ("they are told at
the moment of the paste") and what the Goals section describes ("states its size
limit while the input is still recoverable"). Reading B: the check runs when the
developer submits, and on failure the capture returns to accepting input rather
than exiting -- which satisfies R17's literal words and the acceptance criterion
("The refusal leaves the capture accepting input rather than returning") exactly
as well.

Materially different UX and materially different implementations. R17 also does
not say what happens to the oversized bytes: are they retained in the buffer so
the developer can trim, or discarded? "Leaving the developer's session
recoverable" does not decide it, and the two choices are the difference between
a usable refusal and a lost paste.

**Suggested rewording:** "Input exceeding the ceiling SHALL be refused at the
moment the input crosses the ceiling, before any instance is created. The
capture SHALL remain open and SHALL retain the already-entered text so the
developer can reduce it."

### R24 -- conditional on a fact R23 forbids the implementation from learning

> On a terminal lacking paste-boundary support, multiline input SHALL NOT be
> silently truncated at its first newline, and the developer SHALL be able to
> see what was captured before any instance is created.

R23 forbids probing for capability, so the implementation can never know it is
on such a terminal. The condition is therefore unsatisfiable as a branch, and
the Decisions section says so explicitly ("the requirement becomes a property of
the submit rule instead"). But R24 is still written as a conditional, so an
implementer can read it as scoping the guarantee to a case they cannot detect --
i.e. as vacuous.

Second ambiguity in the same sentence: "the developer SHALL be able to see what
was captured before any instance is created." Reading A is satisfied by ordinary
terminal echo -- the text is on screen because they pasted it. Reading B requires
an explicit confirmation or review step before dispatch. The PRD's own Open
Questions list asks exactly this ("Whether the developer is shown a confirmation
of captured content before dispatch, which would strengthen R24"), which means a
requirement in the contract depends on a question the contract leaves open. That
is not a bounded deferral to DESIGN; it is an undecided requirement.

Third: "silently" weakens the prohibition to the point of admitting the failure
it exists to prevent. As written, truncating at the first newline *with a
message* satisfies R24.

**Suggested rewording:** "Multiline input SHALL NOT be truncated at any embedded
newline, regardless of whether the terminal supports paste boundaries." Then
resolve the confirmation question and state the answer as its own requirement.

### R4 vs R24 -- overlapping scope

R4 ("A single paste of multiline text SHALL be captured whole. Newlines inside
the pasted text SHALL NOT terminate the capture") reads as unconditional. If it
is, R24's first clause is redundant. If R4 is implicitly scoped to terminals
with paste-boundary support -- which nothing in its text says -- then R4 and R24
are the two halves of one rule and should be adjacent and explicitly related.
An implementer needs to know which, because it determines whether the
no-truncation guarantee is a property of the submit rule or a property of the
paste-detection mechanism.

### R11 -- states where code lives, not what the user observes

> Capture SHALL be reachable only through the command's own run path, never
> through the launcher, so callers that invoke the launcher directly cannot
> inherit an interactive read.

The observable requirement is the trailing clause: a caller that dispatches
without going through the interactive command path must never open a capture --
`niwa watch` on a cron timer being the case that matters. The leading clause
specifies where in the tree the reader may not live, which is exactly one of the
four things the Status section promises DESIGN owns ("where the reader lives").
"The launcher" is also an undefined internal proper noun; a reader outside this
codebase cannot evaluate compliance, and its acceptance criterion ("The launcher
entry point cannot reach the capture") is a white-box structural assertion.

**Suggested rewording:** "Dispatches initiated by any path other than an
interactive `niwa dispatch` invocation -- including `niwa watch` -- SHALL NOT
open a capture or read from standard input, and this SHALL hold structurally
rather than by a runtime check in the shared path."

### R19 -- names a mechanism and gives no measurable threshold

> A single line longer than the terminal's line-discipline buffer SHALL be
> captured without hanging.

"The terminal's line-discipline buffer" only exists in canonical mode; a
raw-mode reader has no such buffer and the requirement becomes vacuous. So R19
either presupposes a canonical-mode capture or is trivially satisfied depending
on a decision the PRD says is DESIGN's. The threshold is also unstated (4096 on
Linux, but the PRD never says so), which makes both the requirement and its
acceptance criterion unfalsifiable as written -- a test author has no number to
test at. "Without hanging" has no bound either.

**Suggested rewording:** "A single line of at least 64 KiB with no embedded
newline SHALL be captured in full and SHALL NOT block the capture." The second
sentence ("A minified stack frame or a long serialized payload reaches this
before it reaches R14's ceiling") is rationale and belongs in Decisions.

### R23 -- "probe" is undefined

Does reading `$TERM` count? Reading terminfo? Sending a device-attributes query
and waiting for a response? Emitting the enable sequence itself (a write, not a
query)? Checking for `TERM=dumb`? An implementer who reads terminfo and one who
touches nothing both believe they comply.

**Suggested rewording:** "The command SHALL NOT issue any terminal query whose
response gates or alters the capture, and SHALL NOT emit any warning about
terminal capability."

### R26 -- circular

> Failures reachable before provisioning SHALL be raised before provisioning.

The predicate defines its own scope: whatever you raise before provisioning was,
by construction, reachable before provisioning. Reading A is strong (every check
that *can* be done pre-provision *must* be); Reading B is trivially satisfied by
any implementation. Enumerate instead: the interactivity check, the empty-prompt
check, the ceiling check, and abandonment all resolve before any instance is
created.

### R27 -- subjective, and not covered by any acceptance criterion

> The capture SHALL make it evident that the command is waiting for input.

"Evident" is unmeasurable, and no acceptance criterion in any of the four
sections references R27 -- including the manual list, where a subjective
criterion would at least be acknowledged as such. Since Known Limitations leans
on R27 as one of the two mitigations for the script-inherits-a-terminal hazard,
it should be testable.

**Suggested rewording:** "Before the capture begins reading, it SHALL write a
visible indicator to standard error identifying that `niwa dispatch` is waiting
for prompt input."

### R21 -- the gate is a requirement; the render target is smuggled in the rationale

> The interactivity test SHALL require both standard input and standard error to
> be terminals, since the capture reads the former and renders to the latter
> while standard output carries the command's existing session hints.

The gate itself is legitimately requirements-altitude: it has an observable
consequence (redirecting stderr from an interactive terminal disables the
capture), the Decisions section justifies it against `niwa destroy` precedent,
and it does not name a terminal API. But the "since" clause mandates that the
capture render to standard error, which is a placement decision presented as
justification rather than as a requirement. If that mandate is intended, state
it as its own requirement; if it is not, the gate on stderr needs a different
justification, because as written a DESIGN that renders elsewhere would falsify
the reason the gate exists.

R22 has a related wording problem: "Standard output SHALL remain redirectable
without affecting the capture's behavior beyond R21's gate" -- but R21's gate
does not test standard output at all, so "beyond R21's gate" is vacuous and
invites a reader to hunt for a stdout condition that is not there.

### R6 -- "manually" is undefined

"A means of entering a newline manually, distinct from the submit gesture." The
intent (as opposed to newlines arriving inside pasted text) is inferable but
unstated. Suggest: "The capture SHALL provide a keyboard means of inserting a
newline into the captured text that does not submit."

### R14 -- encodes the current call order into the definition

`dispatchPromptReserve` is defined as "the length of everything niwa may prepend
to the prompt **after validation**." That clause states where the prepend sits
in today's implementation. If DESIGN moves the prepend before validation, the
reserve is zero by this definition and the derivation collapses -- and Known
Limitations already contemplates exactly that restructuring ("Recovering it
would require resolving whether the prepend applies before provisioning").
Define the reserve by what it covers, not by when it runs: "the maximum total
length of any text niwa prepends to the prompt before exec."

Two smaller gaps in R14/R15: "the tightest supported platform" and "every
supported platform" never name the platforms (linux and darwin, per the scope
doc), and the ceiling does not say whether the terminating NUL counts against
`maxArgStringBytes` -- a one-byte difference two implementers will resolve
differently, and the acceptance criterion tests exactly that boundary ("Input at
exactly the ceiling is accepted; one byte over is refused").

## Issues Found

1. **"Prompt" carries three meanings.** The task text (R14 "prompt size
   ceiling", R12 "empty-prompt error", R10 "the prompt reaches the worker"), the
   interactive UI (R1 "open a prompt that captures multiline text", AC "the
   rendered prompt"), and in the BRIEF the shell prompt. R1 and R14 are eleven
   lines apart and use the word for different things. Fix: reserve "prompt" for
   the task text and call the UI "the capture" throughout.

2. **"Session" carries four meanings.** The dispatched niwa session (Goals
   "attached to a session already working on it", R7 "no session mapping"), the
   shell/terminal session (R20 "the session is not interactive", AC "an
   interactive session"), the developer's in-progress input (R17 "leaving the
   developer's session recoverable"), and the stdout hints about the created
   session (R21 "session hints"). R17's use is the damaging one -- in a document
   about a command that creates sessions, "the developer's session" reads as the
   dispatched session, which inverts the requirement's meaning. Fix: R17 should
   say "leaving the developer's input recoverable."

3. **"Gesture" shifts between the whole flow and a single chord.** Goals:
   "Pasting a failure and sending it takes one gesture" (the flow). R5: "the same
   submit gesture as a bare paste" (a chord). AC: "The manual-newline gesture
   inserts a newline" (a chord). The Goals usage is inherited from the BRIEF and
   is fine there; inside a requirements document the word should mean one thing.
   Fix: use "gesture" only for a discrete input action, and say "one step" or
   "one action" for the flow-level claim.

4. **"Capture" drifts from a mode into a named component.** R17 "the capture is
   still accepting input", AC "the capture is invoked", AC headings "an
   injectable capture core" and "a capture seam". The last two name an
   architectural artifact that DESIGN has not chosen; the acceptance-criteria
   section headings presuppose a particular decomposition (an injectable core
   plus a command-level seam). That is a design shape, and it is asserted in the
   section titles rather than argued anywhere.

5. **"Abandonment", "cancel", and "back out" are three words for one concept,
   none defined.** Status says DESIGN owns "how cancel is signalled"; R7/R8/R9
   say "abandoning"/"abandonment"; the fourth user story says "back out". Since
   R8 makes cancellation a distinguishable return value, the term should be
   fixed and defined once.

6. **Undefined internal terms.** "The launcher" (R11, AC) and "the terminal's
   line-discipline buffer" (R19, AC) are both used as though defined. A reviewer
   without the codebase open cannot check compliance with either.

7. **Rationale is embedded inside requirement bodies.** R3's second sentence
   ("Because arity selects the path, no invocation can request both..."), R19's
   second sentence, R21's "since" clause, and R2's "The argument path is
   byte-identical to current behavior" are assertions and justifications rather
   than obligations. Each duplicates material already in Decisions and
   Trade-offs, where it belongs. R2's sentence in particular is a claim about
   the outcome, not a testable obligation.

8. **The manual-verification criteria are unfalsifiable, and the PRD knows it.**
   "Capture behaves acceptably on a terminal without paste-boundary support" --
   "acceptably" is precisely the phrasing to hunt for, though the clause after
   the colon bounds it, so the fix is simply to delete the word and keep the
   bounded clause. "Capture works inside a terminal multiplexer" names no
   multiplexer, no version, and no definition of "works". "A large paste renders
   without visible corruption" quantifies neither "large" nor "corruption". Open
   Question 2 acknowledges the gap ("Without a named set those criteria are
   unfalsifiable") -- so the subjectivity is *noticed* but not *bounded*, which
   is the worse of the two states: the PRD would ship with criteria it has
   already declared unusable. Fix: name the terminals and the multiplexer, set
   "large" to the ceiling, and define corruption as "no submitted bytes lost and
   no rendering artifacts remaining after submit".

9. **The acceptance criteria smuggle a design decision the requirements
   avoided.** "An escape sequence split across two reads is reassembled
   correctly (R4)" presupposes a raw-mode reader parsing terminal escape
   sequences -- i.e. bracketed paste. R4 says nothing about escape sequences; it
   says newlines must not terminate the capture. A DESIGN that satisfies every
   requirement without parsing escapes would fail this criterion. Given that the
   PRD's Out of Scope explicitly reserves "the capture mechanism" and "the choice
   of terminal API" for DESIGN, this criterion contradicts the document's own
   promise. Fix: restate as a property of the capture ("input delivered across
   multiple reads is accumulated without loss or duplication") and let DESIGN
   add the escape-specific test.

10. **One acceptance criterion is not parseable as a test.** "The captured
    result and the rendered prompt are separable: what is returned is not what
    is drawn (R10, R22)." Taken literally it asserts inequality, which is false
    whenever the drawn text equals the pasted text. The intent is presumably
    that the returned string excludes rendering decoration. It is also cited
    against R22, which is about stdout redirection and which it does not test.

11. **Requirements with no acceptance criterion.** R3 (two or more positional
    arguments remain an error), R15 (a single ceiling on every platform), R21
    (the stdin+stderr gate, only indirectly via R20), R23 (no probe, no warning
    -- the manual criterion cites R23 but its text tests R24 only), and R27 (the
    waiting indicator). R9 is cited once, for abandonment only, leaving error,
    SIGTERM, and SIGHUP restoration unverified.

12. **User stories are specific but two state a solution as the want.** All five
    have a real who / what / why, and the operator story is a genuinely
    different persona rather than a fifth developer -- that is better than most.
    But story 3's want ("I want to be told my paste is too large") and story 4's
    ("I want to back out and find my terminal working normally") state the
    mechanism rather than the need; the needs are "not to lose my input" and
    "the next command behaves". Story 4 already carries the real why in its
    trailing clause, so the fix is small.

13. **No user story covers the case R20 and Known Limitations both care about.**
    The operator story covers a cron job that *passes* a prompt argument. The
    hazard the PRD spends its Known Limitations on -- a promptless invocation
    from a script, with and without an inherited terminal -- has no story, so R20
    and R27 float without a stated need behind them.

## Suggested Improvements

1. **Move the #225 baseline qualifier into R2 and R25 themselves.** The
   Decisions section resolves the conflict; the requirements do not carry the
   resolution, and the requirements are what gets implemented from.

2. **Add a requirement for end-of-input on a non-empty buffer.** This is the
   single largest behavioral hole. It is a user-visible outcome, not a gesture,
   so it is the PRD's to decide, and the workaround the BRIEF documents has
   already taught the target user a Ctrl-D habit.

3. **Resolve Open Question 3 before the PRD leaves Draft.** R24's second clause
   is undecidable until the confirmation question is answered, and confirmation
   is a user-visible product decision rather than a DESIGN one. Leaving it open
   while a requirement depends on it means DESIGN will decide it by default.

4. **Split R21 into gate and render target.** Two requirements, each testable,
   with the justification moved to Decisions where the `niwa destroy` precedent
   already lives.

5. **State units and rounding for the size messages.** R18 should say bytes for
   enforcement and specify how the limit is presented to the user; the phase 2
   research recommends round terms in the message against an exact byte check,
   and that split is a requirement-level decision about what the user reads.

6. **Rename the acceptance-criteria section headings.** "Verifiable as unit
   tests over an injectable capture core" and "over a capture seam" assert a
   decomposition. "Verifiable without a terminal" and "verifiable at the command
   boundary" say the same thing about testability without prescribing shape.

7. **Add a glossary or first-use definitions for prompt, capture, session, and
   gesture.** Four overloaded terms in a document whose whole job is precision
   about a text-handling contract.

## Summary

The PRD is well-argued and its Decisions section does real work -- most of what
looks ambiguous at the requirement level turns out to be settled in prose forty
to a hundred lines away. That is the document's central weakness: the
Requirements section, which is what an implementer reads and a test author tests
against, does not stand on its own, and three of its resolutions (the #225
baseline, the unconditional no-truncation rule, the render target) live only in
the rationale. Six requirements admit readings that build different things --
R18's refusal message contradicts the surrounding document and has no
in-scope referent, R10's "unchanged" contradicts R14's prepend, R2/R25 conflicts
with R16, R8 leaves end-of-input undefined, R17 does not say whether refusal
happens at paste or at submit, and R24 depends on an open question the PRD has
not closed. Separately, R11 and one acceptance criterion (escape-sequence
reassembly) foreclose the exact DESIGN decisions the PRD promises not to make,
and the manual criteria are unfalsifiable in a way the document has already
noticed but not fixed.
