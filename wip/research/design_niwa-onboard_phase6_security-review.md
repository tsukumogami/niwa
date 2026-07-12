**Verdict:** PASS

# Phase 6 Security Review (round 2): niwa-onboard design

Re-review of `docs/designs/DESIGN-niwa-onboard.md` against round 1
(`wip/research/design_niwa-onboard_phase6_security-review-round1.md`), which
returned FAIL on F1/F2/F3 with R1/R2/R3 as strong recommendations. Every must-fix
is now present, consistent across all the places it has to appear, and sufficient.
The three recommendations are also incorporated. No new inconsistency was introduced
by the edits.

## Must-fix verification

### F1 — api_url gate hoisted ahead of the detection call: FIXED, consistent

The round-1 contradiction (gate bolted onto the setup/topology confirm, which runs
*after* the detection GET-identity that carries the bearer) is resolved. The gate is
now a standalone entry-time step that runs right after `resolveAPIURL` and strictly
before any bearer-carrying call, and every location says so with the same framing —
that the detection GET is itself the first bearer-carrying call, so a confirm-folded
guard would fire too late:

- Decision 3, step 0 (lines 423–434): explicit "before any bearer-carrying call,
  including detection," decoupled from the setup/topology confirm.
- Decision 4 (lines 587–612): "own gate at wizard entry, run right after
  `resolveAPIURL` and before any bearer-carrying call — including the detection
  GET-identity of Decision 3."
- Sequence diagram (lines 1008–1012): the `resolveAPIURL` + gate step precedes
  `ReadIdentity`, which is annotated "first bearer-carrying call, only after the gate."
  Prose ordering and diagram ordering now agree.
- Security section (lines 1272–1278) and Decision Outcome (lines 812–816): same
  ordering, same rationale.

The reordering is stated in the design text, so a faithful plan ships the gate before
the bearer moves. Sufficient.

### F2 — non-TTY / scripted contract: FIXED, no "warn" softening left

- Non-`https` is an **unconditional hard reject in every mode** (Decision 3 step 0,
  Decision 4 lines 603–605, Security lines 1278–1281, Decision Outcome). The round-1
  "rejected (or warned on)" phrasing is gone.
- A non-default `https` `api_url` over non-TTY **fails fast (exit 2) unless
  `--accept-api-url`** is supplied, and is "never silently accepted" (Decision 2
  lines 327–342, Decision 3 line 431, Decision 4 lines 605–612, sequence diagram
  line 1009, Phase 3 test surface lines 1129–1132).
- Grep for "warn" confirms the only occurrences either explicitly reject the
  warn-and-proceed softening (lines 604, 1280) or concern R20 revocation (lines 881,
  941), which is unrelated. No softening remains anywhere.

The one mode where the attack is most valuable (scripted CI/bootstrap, no human at the
confirm line) now has the strictest contract, not undefined behavior. Sufficient.

### F3 — encode-or-validate normative rule + hostile-character fixtures: FIXED

- A dedicated normative section, "Injection surfaces: encode-or-validate before
  embedding" (lines 1293–1313), states the MUST rule for every REST-returned or
  config-sourced value into a TOML body **or a URL path**, naming all four sinks:
  the stored credential body, the committed overlay block, and the `secret_id` /
  identity-id URL paths (RevokeClientSecret DELETE, ReadIdentity GET).
- Restated at the point of use in the Decision Outcome (lines 849–852) and Key
  function surfaces (lines 971–976).
- Hostile-character fixtures (`"`, newline, `]`) are added to the test surfaces:
  Phase 5 store-shape and the `secret_id` revoke path (lines 1148–1153), Phase 6
  config-authoring (lines 1163–1166). "Correct by construction" is now enforced, not
  merely asserted.

### R1 — terminal sanitization + punycode host display: INCORPORATED

Shared display-sanitizer in the prompt kit (Decision 3 lines 477–485), a dedicated
"Terminal-output safety" security subsection (lines 1316–1328), component-map and
key-function entries, and a Phase 2 test asserting ANSI/CR/control-byte neutralization
and homoglyph/punycode legibility (lines 1119–1123). Correctly framed as a distinct
axis from R17 secret-scrubbing, and as the thing that makes the F1/F2 last-look defense
real.

### R2 — atomic 0600 temp-then-rename overlay write, single-writer: INCORPORATED

Key function surfaces (lines 968–971) and component map (line 911) specify the overlay
`niwa.toml` write uses the same `0600`-temp-in-dir-then-rename discipline as the R20
record, no in-place truncate, with the single-writer assumption stated explicitly.
Phase 6 test surface asserts it (line 1166).

### R3 — AC-10 lint honestly scoped: INCORPORATED

The Custody-boundary section (lines 1208–1217) states the static lint is a
**direct-call-site** check that does not catch indirect dispatch, and the **runtime
request recorder is the load-bearing check**. Echoed in Decision 4 (line 636), the
Phase 1 test surface (line 1107), and the Consequences/Mitigations sections. No one
is invited to over-trust the lint.

## New-inconsistency scan

- **Sequence diagram vs prose ordering:** consistent — gate precedes `ReadIdentity`
  in both.
- **`--accept-api-url` vs Decision 2's flag table:** no collision. The flag is added
  to Decision 2's flag list (lines 327–333); the table at lines 348–357 is the
  exit-code vocabulary, not a flag table. The flag is an independent boolean, not part
  of any mutual-exclusion pair, and its interactive-optional / non-TTY-required
  semantics are stated consistently in Decision 2, Decision 3, and Decision 4.
- **Exit-code reuse:** the non-TTY api_url fail-fast reuses code 2 (R18 precondition
  fail-fast); this is deliberate and stated (lines 339–342), not a clash.
- The Consequences "Interactive only" note (line 1410) is consistent with F2: the flag
  is the explicit override that keeps the scripted path from silently proceeding.

All three must-fixes are closed in the design text and the two recommendations plus R3
are folded in coherently. This is a PASS.
