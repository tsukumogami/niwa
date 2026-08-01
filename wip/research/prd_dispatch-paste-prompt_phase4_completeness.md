# Completeness Review

**Verdict:** FAIL

Four requirements have no acceptance criterion at all (R3, R15, R21, R27), eight criteria claim requirements they do not exercise, R9's six enumerated exit paths are verified for one, and three behaviors an implementer must pick (empty capture, EOF mid-capture, exit codes) are undefined.

## Requirement Coverage Map

Criterion labels: `U1-U11` = unit tests over the injectable core, in listed order;
`C1-C7` = command-level tests over the capture seam; `F1-F4` = `@critical`
functional scenarios; `M1-M3` = manual verification.

| Req | Criteria claiming it | Assessment |
|---|---|---|
| R1 no-arg + interactive opens capture | C1, F1 | Covered |
| R2 one-arg path unchanged, no terminal consult | C3 | Covered |
| R3 zero or one arg; two or more remain an error | — | **UNCOVERED** |
| R4 paste captured whole, newlines don't terminate | U1, U2, U5, F1 | Covered (U5 weak, see Issue 6) |
| R5 typed text alongside paste, same gesture | U2, U3 | Covered |
| R6 manual newline distinct from submit | U4 | Covered |
| R7 abandonment creates no durable state | C4 | Partial — instance only; mapping and "other durable state" unasserted |
| R8 abandonment distinguishable from end-of-input | U9 | Partial — sentinel covered; "command reports a cancelled capture" unasserted |
| R9 terminal mode restored on every exit path | F3 | **Partial — 1 of 6 paths.** Normal submit, error, SIGINT, SIGTERM, SIGHUP unverified |
| R10 text reaches worker unchanged, one argv element | U11, C1, F1 | Covered by C1/F1; U11 is a mis-tag; no adversarial-metacharacter case |
| R11 capture unreachable from the launcher | C7 | Covered |
| R12 `niwa dispatch ""` keeps existing error | C5 | Covered |
| R13 `--detach` composes | C6 | Covered; "no combination newly rejected" unasserted |
| R14 ceiling = maxArgStringBytes − reserve | U6, U7 | Partial — value covered; "expressed as that derivation, not a literal" unverifiable as listed |
| R15 single ceiling on every supported platform | — | **UNCOVERED** |
| R16 ceiling enforced against captured text | U7 | Mis-tag — U7 verifies reserve arithmetic, not capture-path coverage |
| R17 refused while capture still accepting input | U6, U8, F2 | Covered |
| R18 message states size, states limit, names alternative | F2 | Partial — sizes covered; "names a concrete alternative" unasserted |
| R19 over-long single line captured without hanging | U10 | **Wrong level** — a unit harness has no line discipline (see Issue 4) |
| R20 non-interactive refuses, does not read stdin | C2, F4 | Covered |
| R21 gate requires stdin **and** stderr terminals | — | **UNCOVERED** |
| R22 stdout stays redirectable | U11 | Mis-tag — U11 tests return-value vs writer, not stdout redirection |
| R23 no capability probe, no capability warning | M1 | Weak — a manual scenario cannot show absence of a probe; "shall not warn" unasserted |
| R24 no truncation at first newline; developer sees capture | M1 | Partial — truncation only; the "see what was captured" clause is both uncovered and contradicted by Open Question 3 |
| R25 positional path: same exit codes, messages, argv | C3 | Weak — C3 verifies capture bypass, none of the three named identities |
| R26 pre-provisioning failures raised pre-provisioning | C4, F2 | Covered |
| R27 capture visibly waiting for input | — | **UNCOVERED** |

M2 (multiplexer) and M3 (large-paste rendering) carry no requirement tag and
verify nothing stated above.

## Issues Found

1. **Four requirements have no acceptance criterion.**
   - *R3* (two-or-more positional arguments remain an error). The flag-surface
     research notes that relaxing `ExactArgs(1)` to `MaximumNArgs(1)` changes the
     multi-arg message, and that a developer who forgot to quote a multi-word
     prompt must get an error rather than a capture. Nothing checks this.
   - *R15* (one ceiling on every platform). CI is Linux-only, so this may not be
     automatable — but then it belongs in the manual list, not nowhere.
   - *R21* (gate covers stdin **and** stderr). The stderr half is the one novel
     part of the gate and no criterion exercises it.
   - *R27* (capture visibly waiting). Known Limitations leans on R27 as the
     mitigation for a script that inherits a terminal, so it is load-bearing.
   Fix: add a command-level criterion for R3 and R21, a unit criterion for R27
   (the writer receives a visible prompt before the first read), and move R15 to
   manual verification or drop the requirement.

2. **R9 enumerates six exit paths and one is verified.** F3 covers abandonment.
   Normal submit, error, SIGINT, SIGTERM, and SIGHUP are unchecked — and the
   signal paths are the ones that hurt, since a leaked bracketed-paste or raw
   termios state lands on the developer's *next* command, not on niwa. The
   testability research verified that `stty -g` before/after under `script` is
   assertable and that it produced identical output after both a normal submit
   and a Ctrl-C. Fix: extend F3 to submit and at least SIGINT, and state
   explicitly which of the three signals are verified versus asserted by
   construction.

3. **Eight criteria claim requirements they do not exercise.** Beyond the two
   R9/R19 cases: U11 ("captured result and rendered prompt are separable") is
   tagged R10 and R22, but it tests return-value-versus-writer separation — not
   argv delivery and not stdout redirection. U7 is tagged R16, but reserve
   arithmetic is not the same claim as "the ceiling applies to the capture path."
   C3 is tagged R25 but verifies only that the capture is bypassed, not the three
   identities R25 names (exit codes, messages, argv construction). M1 is tagged
   R23, but "does not probe" and "does not warn" are absence claims a manual
   terminal check cannot establish — both are unit-assertable against the writer.
   F2 is tagged R18 but asserts only the two sizes, omitting the "names a concrete
   alternative" clause that the PRD's own Decisions section calls the part that
   matters more than the number. Fix: retag, and add criteria for the clauses left
   behind.

4. **R19 is assigned to a harness that structurally cannot exercise it.** The
   over-long-line hang is a line-discipline phenomenon: the testability research
   measured it at 4090 bytes plus a 6-byte marker hitting the N_TTY canonical
   4096-byte limit *before* the child reaches `MakeRaw`. An injectable core driven
   by `bytes.NewReader` has no line discipline, so U10 will pass whatever the
   implementation does. Fix: move R19's verification to the PTY level, and note
   the research's finding that the same probe needed a pre-feed delay to pass —
   the criterion needs to say whether it is testing the pre-raw-mode window or
   only post-raw-mode reads.

5. **Three behaviors an implementer must choose are undefined.**
   - *An empty capture.* R12 pins `niwa dispatch ""`, but nothing says what
     happens when the developer submits an empty or whitespace-only capture.
     Empty-prompt error? Cancel? Re-prompt? All three are defensible.
   - *EOF mid-capture.* R8 implies end-of-input is a distinct outcome from
     abandonment, but no requirement says what the command does on it. The
     testability research lists "immediate EOF on empty input returns a clean
     error, not a hang" as a needed behavior; the PRD never states it.
   - *Exit codes.* The PRD names exit codes only in R25, for the path that does
     not change. The non-TTY refusal, the oversized refusal, and abandonment all
     need one. The tty-behavior research recommends exit 1 for the refusal
     (matching `destroy`'s plain-error path and today's baseline, and
     deliberately not `init`'s typed exit 4) — that reasoning should land in the
     PRD rather than be re-derived.

6. **U5 encodes a DESIGN decision the PRD says it does not make.** "An escape
   sequence split across two reads is reassembled correctly" presumes an
   escape-sequence-based capture mechanism, which Out of Scope explicitly hands
   to DESIGN ("the choice of terminal API"). Fix: restate as the property — a
   paste arriving across multiple reads is captured whole — or move it to DESIGN.

7. **R24's second clause is contradicted by the PRD's own Open Question 3.** R24
   requires that "the developer SHALL be able to see what was captured before any
   instance is created." Open Question 3 asks "whether the developer is shown a
   confirmation of captured content before dispatch." A requirement cannot mandate
   a behavior the document simultaneously lists as undecided. Fix: either close
   the question and keep R24 whole, or weaken R24 to the truncation property alone
   and let the confirmation be a DESIGN affordance.

8. **The ceiling's magnitude never appears in the document.** R14 states the
   derivation, and Known Limitations mentions the 638-byte reserve, but no reader
   can tell from this PRD whether the ceiling is 130 KB or 130 bytes. That matters
   because R18 requires the refusal message to *state the limit*, and because the
   Decisions section argues at length about which payload classes fall on which
   side of it (5.6-9.2 KB failure pastes versus 326-582 KB whole runs) without
   ever naming the line. The size-ceiling research recommends stating the
   derivation and presenting it in round terms ("about 127 KB"). Fix: add the
   computed value parenthetically to R14.

9. **The Problem Statement carries forward a mechanism claim the research
   refuted.** Lines 50-51: "that path closes once stdin has been spent feeding
   the prompt." The flag-surface research checked this directly — `dispatchAttach`
   hands `os.Stdin` through unmodified and the launcher never reads it, so nothing
   in niwa closes stdin. The workaround's attach problem is shell or terminal
   state. The research explicitly warns that carrying this forward would misdirect
   DESIGN into thinking it must recover a consumed fd. The conclusion (the
   workaround is bad, `-d` is what people type) survives; the mechanism should be
   dropped.

10. **Out of Scope does not rule on two decisions an implementer will hit.**
    Neither documentation updates nor test-harness changes are placed in or out.
    The flag-surface research identifies six documentation sites that state or
    imply the mandatory-argument shape (`README.md:130`, the `Use` and `Long`
    strings, the generated workspace CLAUDE.md, `/dispatch`'s SKILL.md) and flags
    the generated CLAUDE.md as a deliberate product call about what agents in new
    workspaces are told. The testability research names four harness prerequisites
    (`\e` expansion, a generated-payload PTY step, a DocString argv assertion, a
    timeout on the PTY step) and says the PRD should state them. Silence on both
    invites either scope creep or a PR that ships without its own docs.

11. **The dependency on the corrected cap is stated as prose, not as an
    assumption or requirement.** The PRD says #225/#226 is the baseline it states
    a requirement over. Nothing says what happens if that PR does not land first,
    and R14's derivation names two terms (`maxArgStringBytes`,
    `dispatchPromptReserve`) that exist only in that PR. Fix: state it as an
    explicit dependency with the consequence of it not landing.

12. **Nothing addresses escape sequences inside pasted content on the echo
    path.** R10 settles that they survive into the prompt ("unchanged"). The
    testability research flags that whether they are stripped from the *echo* is a
    separate decision "currently written as one word." A developer pasting a log
    that contains terminal control sequences into a raw-mode reader that echoes
    them is a real display-corruption path, and a mild injection surface. R10's
    "unchanged" does not answer it.

13. **No criterion exercises the quoting hazard the problem statement is built
    on.** The Problem Statement's core complaint is text "full of quotes,
    backslashes, and dollar signs." R10 requires the text arrive unchanged as one
    argv element, but no criterion pastes such a payload. The repo already has the
    model — `dispatch_launcher_test.go:32-48` asserts a metacharacter-laden prompt
    stays one argv element. Fix: add an adversarial-payload criterion at the
    command level.

## Suggested Improvements

1. **Add a user story for the empty or abandoned-by-accident capture.** Five
   stories cover all four BRIEF journeys plus the operator, which is good
   coverage — but the developer who hits Enter on nothing has no story, which is
   part of why Issue 5's first gap exists.

2. **Name the terminals for the manual criteria, or delete them.** Known
   Limitations already concedes these are checked "by a person or not at all," and
   Open Question 2 concedes they are unfalsifiable without a named set. M1, M2,
   and M3 are currently three unverifiable criteria. The tty-behavior research
   supplies a usable set (the real gaps are old GNU screen, pre-2005 xterm, VTE
   < 0.23.3, legacy conhost).

3. **State where the capture renders.** R21 mentions stderr as a rationale and
   R22 forbids stdout by implication, but no requirement says it outright. One
   sentence removes the inference step.

4. **Resolve Open Question 1 before this PRD leaves Draft.** Whether a
   Linux-only PTY scenario is acceptable determines whether F1, F2, and F3 exist
   at all — three of the four functional criteria. The testability research
   measured that `iRunUnderPTYWithInput` errors rather than skips when `script`
   is not util-linux, so the choice is between a broken `make test-functional` on
   macOS and silent coverage loss. That is a scoping decision, not an open
   question a downstream DESIGN can absorb.

5. **Add the PTY-step timeout as a stated prerequisite.** The testability
   research measured that `script` does not propagate stdin EOF, so a PTY
   scenario missing its submit gesture hangs until `go test`'s 10-minute panic
   and takes the whole suite with it. Three new PTY scenarios triple that
   exposure. It is a one-line change and it belongs where the PRD can point at it.

6. **Say that the existing 37 unit call sites and 9 functional scenarios are
   R25's regression guard.** R25 is the strongest no-change claim in the document
   and C3 barely touches it; the existing corpus already does the work, and
   naming it converts a weak criterion into a strong one for free.

## Summary

The PRD is well-argued and closes all four questions the BRIEF deferred — the
ceiling as a derivation, the refuse-versus-degrade split, detach composition, and
the structural non-interactive guarantee — with only the ceiling's magnitude
missing from an otherwise complete answer. It fails on verification coverage
rather than on reasoning: R3, R15, R21, and R27 have no criterion, R9 is verified
for one of the six exit paths it enumerates, R19 is assigned to a harness that
cannot reproduce its failure mode, and eight criteria claim requirements they do
not exercise. Three behaviors an implementer must choose — what an empty capture
does, what EOF mid-capture does, and which exit codes the new failure paths use —
are not stated anywhere, and Out of Scope is silent on both documentation and
test-harness work, leaving the boundary open in the two places this change will
actually push on it.
