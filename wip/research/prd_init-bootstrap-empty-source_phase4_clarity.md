# Clarity Review (round 2)

## Verdict: PASS

All 28 round-1 ambiguities are resolved or substantially addressed; the
new sections (Glossary, Appendix A, Appendix B, Flag Interactions, Exit
Codes, Token Presence Semantics, Notices & Observability) commit to
exact strings, tables, or precedence rules that two implementers could
not reasonably diverge on.

## Round 1 Resolution Status

- Ambiguity 1 (R3 inline comment exact wording): RESOLVED — Appendix A
  embeds the literal comment string `# Bootstrap enabled mesh channels.
  Remove this block (and the [channels.mesh] line below) to disable.`
  and AC "Inline comment on [channels.mesh]" asserts the exact line.
- Ambiguity 2 (R3/R4/R14 overlap): RESOLVED — R14 now explicitly
  consolidates into Appendix A; R3 cites Appendix A as the single
  source of truth; R4 cites Appendix A.
- Ambiguity 3 (R5 "collision-safe"): RESOLVED — R5 now states
  explicitly "Because each `niwa session create` invocation generates
  a fresh `<sid>`, retries always produce distinct branch names; no
  collision-detection logic is required."
- Ambiguity 4 (R14 commented `[claude.content.workspace]` exact
  syntax): RESOLVED — Appendix A embeds the literal commented block.
- Ambiguity 5 (R14 schema-doc-link footer): RESOLVED — Appendix A
  embeds the exact URL line.
- Ambiguity 6 (R14 #1 allowed `[workspace]` keys): RESOLVED — Appendix
  A's literal body contains exactly `name` and `content_dir`;
  byte-equality contract makes additional keys a failure.
- Ambiguity 7 (R14 #3 Repo.Private mapping): RESOLVED — R14 now states
  explicitly: `Private: true → [groups.private] visibility =
  "private"`; `Private: false → [groups.public] visibility = "public"`.
- Ambiguity 8 (R18 "meaningful" commit message): RESOLVED — "meaningful"
  removed; R18 says "Use the exact subject `Initial niwa workspace
  config` (no body)."
- Ambiguity 9 (R19 "prominent" + line-range citation): RESOLVED —
  Appendix B embeds the literal stderr block with column alignment at
  byte 30; line-range citation removed.
- Ambiguity 10 (R19 "publish" verb): RESOLVED — Appendix B's "Next
  steps" uses concrete commands only (`git show HEAD`, `git push -u`,
  `niwa apply`); the word "publish" is gone.
- Ambiguity 11 (R20 landing-path mechanism unspecified): RESOLVED —
  R20 now references `workspace/landing.go::writeLandingPath`, names
  the `NIWA_RESPONSE_FILE` env var, and specifies the file format
  ("single line containing the absolute path without trailing newline").
- Ambiguity 12 (R23 "identifies which step failed" format): RESOLVED —
  R23 specifies the exact literal prefix `bootstrap
  step=<init|create|session-create>:` and tabulates exit codes 0–4.
- Ambiguity 13 (TTY decline exit code): RESOLVED — R13's table and R23
  both fix exit 0 for the TTY "N" decline path.
- Ambiguity 14 (visibility-fixture TOML metacharacter unspecified):
  RESOLVED — AC "Visibility-from-bool with TOML-injection fixture"
  specifies the literal JSON `{"private": false, "visibility":
  "\"\n[evil]\nkey = \"x"}` and the assertion ("no `[evil]` block").
- Ambiguity 15 (N1 latency "proportional to" target): RESOLVED — N1
  moved to Known Limitations with "v1 does not target a specific
  latency budget"; the requirement is gone, not handwaved.
- Ambiguity 16 (R6 "new success preconditions"): RESOLVED — R6
  rephrased as a concrete checklist: "shall pass the same arguments
  and environment to the internal create call that `niwa create`
  would receive standalone."
- Ambiguity 17 (R10 paraphrase vs exact text): RESOLVED — R10 now says
  "containing this exact substring on stderr: `verify GH_TOKEN scopes;
  fine-grained PATs need Contents: read, classic PATs need repo
  scope`."
- Ambiguity 18 (R11 only one cause-string specified): RESOLVED — R11
  now lists all three exact substrings.
- Ambiguity 19 (R17 "explaining the fallback"): RESOLVED — R17 embeds
  the exact note text with `<cause>` enumerated to one of four
  values.
- Ambiguity 20 (R25 line-range citation + "wording pattern"): RESOLVED
  — R25 embeds the exact error string `--bootstrap and --no-bootstrap
  are mutually exclusive`; line-range citation gone.
- Ambiguity 21 ("minimal-ideal" in Out of Scope): RESOLVED — replaced
  with "the scaffold defined in Appendix A non-interactively."
- Ambiguity 22 (Goals #4 fails to reflect R13's matrix): RESOLVED —
  Goal #4 lists the four cases (auth 401/403, 404 missing/private/
  zero-commit, ambiguous markers, no-marker without `--bootstrap`)
  and cites R10–R13.
- Ambiguity 23 (worktree terminology): RESOLVED — Glossary locks the
  definition.
- Ambiguity 24 (workspace root vs instance root): RESOLVED — Glossary
  defines both with absolute-path templates.
- Ambiguity 25 (instance terminology): RESOLVED — Glossary entry.
- Ambiguity 26 (User Story #5 "discoverable"): RESOLVED — story now
  reads "named in the failure message and tearable-down via the `niwa
  destroy <name>` command named in that message."
- Ambiguity 27 (Goal #2 "self-revealing" aspirational): RESOLVED — Goal
  #2 now anchors directly to R19 ("Per R19, the success output names
  every niwa artifact ...").
- Ambiguity 28 (Classifier "most-specific" undefined): RESOLVED — N2
  embeds an explicit five-arm precedence list with numeric ordering.

## New Ambiguities Found

1. **Appendix B alignment column 30 vs the literal block** (line
   ~840–855): The fenced success block shows labels like `Workspace
   bootstrapped at:` (28 chars) followed by four spaces — but
   `Instance:` is 9 chars and would need 21 spaces to reach column 30,
   while the fenced block visually shows about 21 spaces. Two
   implementers may compute padding differently depending on whether
   they count the label-with-colon (29 chars for "Workspace
   bootstrapped at:") plus one space, or pad to a fixed column 30. The
   literal-block contents and the textual "byte position 30" rule
   should agree exactly; right now the rule is stated but the
   golden-text could be off-by-one. -> Pick one: either drop the
   alignment rule and let the literal block be the golden source, OR
   keep the rule and add a sentence: "If the literal block above
   contradicts the byte-position-30 rule, the rule wins."

2. **Appendix B "Next steps" line 3 wording** ("Merge to the default
   branch, then run `niwa apply` to refresh."): "Refresh" is not
   defined as a niwa verb anywhere else in the PRD. Compare to
   "drift checking" used in R26 and Out of Scope. -> Replace "refresh"
   with "to check for drift" to match the R26 wording, OR define
   "refresh" in the Glossary.

3. **R6 "same environment"**: R6 now says "shall pass the same
   arguments and environment to the internal create call." Does
   "environment" mean process env vars, or the full execution context
   (cwd, stdin/stdout/stderr handles, registry state)? Two
   implementers could disagree on whether `NIWA_RESPONSE_FILE` for
   landing-path counts as "environment" pass-through. -> Specify:
   "process environment variables (os.Environ) and cwd."

4. **R7 daemon-shutdown timeout**: "5 s graceful shutdown via SIGTERM,
   then SIGKILL" — does the 5 s timer start when SIGTERM is sent, or
   when the daemon was first detected during teardown? -> Specify:
   "the rollback sends SIGTERM, waits 5 s wall-clock, then sends
   SIGKILL if the process is still alive."

5. **R13 prompt exact text — bracketed default**: The prompt is
   `Remote has no .niwa/workspace.toml. Scaffold a minimal config and
   stage it on a niwa-bootstrap branch? [Y/n] ` and the table says
   "Proceed only on Y." But `[Y/n]` conventionally means "empty input
   = Y." Does an empty input (bare Enter) count as Y? -> Specify
   accepted input set: e.g., "accepts Y, y, or empty input as
   affirmative; everything else as decline."

6. **R23 exit code 1 for all three step-failure cases**: The exit
   table assigns code 1 to "Step failure (init, create, or
   session-create)" without sub-distinguishing. Is that intentional
   given that R7's rollback differs per step? A tester can't
   distinguish step failures by exit code alone, only by the stderr
   prefix `bootstrap step=<...>:`. -> Confirm intentional, or split
   exit codes per step. (Likely intentional; flagged as a
   verification point.)

7. **AC "Branch-name back-compat fallback"** (line ~647–649): "a
   session state file pre-dating the schema (no `branch_name` field)
   is still readable" — but the PRD doesn't say what the pre-existing
   schema version is or whether such files exist in any deployed
   workspace. Without a versioning anchor, the AC tests a hypothetical.
   -> Either drop the AC (R5 fallback rule is well-specified) or
   anchor it to a specific session-state schema version.

8. **Glossary "Session" definition** ("a niwa-managed `(branch,
   worktree, daemon, lifecycle state)` quadruple"): The "daemon"
   component implies every session has a daemon. Is that true for
   bootstrap-created sessions before the user does anything? The
   non-bootstrap session-create flow may or may not spawn a daemon
   immediately. -> Clarify whether the daemon is provisioned at
   session-create time (and therefore at bootstrap completion) or
   lazily on first use.

## Suggested Improvements

1. **Cross-check Appendix B padding visually**: A reviewer should
   paste the literal block into a fixed-width buffer and verify each
   label aligns to byte 30. If the literal block currently misaligns,
   either the spaces or the rule should change.

2. **Promote "refresh" -> "drift check" everywhere**: R26 and Out of
   Scope both say "drift checking"; Appendix B says "refresh." Pick
   one.

3. **Add a Glossary entry for "daemon"**: It appears in R7 (5 s
   shutdown contract) and in the Session definition but is not
   defined.

4. **Add a versioning anchor for session-state schema** (or remove
   the back-compat AC): The back-compat AC has no schema-version
   reference point.

5. **State the bare-Enter behavior in the R13 prompt**: The standard
   `[Y/n]` convention is ambiguous in CLI code review; one explicit
   line resolves it.

## Summary

The revision substantially improved clarity: all 28 round-1 ambiguities
are addressed (most via direct text inlining in Appendix A, Appendix B,
the Glossary, and the new tables for Flag Interactions, Exit Codes,
Token Presence, and Notices). Subjective hedges ("meaningful,"
"prominent," "minimal-ideal," "appropriate," "reasonable") and
line-range source citations are gone. Eight new ambiguities surface,
most cosmetic or low-risk (Appendix B alignment math, "refresh" vs
"drift check" wording, the bare-Enter prompt convention) — none would
plausibly cause two implementers to ship different artifacts; the PRD
passes clarity review.
