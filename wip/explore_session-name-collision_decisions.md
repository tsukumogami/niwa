# Exploration Decisions: session-name-collision

Made under `--auto` mode via the research-first protocol: evidence gathered,
recommendation formed, recommendation followed, decision recorded here.

## Round 1

- **"Latest wins" is dropped as the severity mechanism**: no Claude Code
  documentation states any resolution rule for a programmatic bare-name send to an
  ambiguous name. The behavior is unspecified. Downstream artifacts must describe
  the failure as "unspecified resolution, possibly silent non-delivery" rather than
  "deterministic misdelivery to the newer session".

- **The bug is confirmed worth fixing, on observed evidence rather than argument**:
  seven names shared by fifteen sessions in a live 115-peer listing, plus a
  hand-applied `legacy-` rename in the machine's job store. The task brief asked for
  the case to be argued either way; the "acceptable as-is" side loses to two
  independent observations of it having already happened.

- **Detect-and-warn is eliminated**: technically implementable at ~0.4 ms from
  `~/.claude/jobs/<id>/state.json`, but roughly 100 of 115 peers are off-machine, so
  a clean check proves nothing and reads as a false all-clear. Rejected on the
  grounds that a misleading signal is worse than no signal.

- **Detect-and-auto-disambiguate is eliminated**: races against concurrently
  launching workers (the check precedes the new job entry), and makes an unsuffixed
  name look like a uniqueness guarantee it is not. Same TOCTOU that pushed instance
  naming to unique-by-construction in `DESIGN-instance-dispatch.md:150-158`.

- **Unique-by-construction is the surviving approach**: it is the only remedy niwa
  can actually deliver against a namespace it can neither see nor lock, and it
  reapplies a decision the repo already made and documented for the instance
  directory.

- **The suffix belongs in `runDispatch`, not in `buildDispatchPassthrough`**:
  the builder is shared with two `niwa watch` call sites where the slug doubles as a
  staged-record filename and is deliberately stable across resumes, and one test
  compares argv byte-for-byte against a second builder call. Four leads reached this
  independently.

- **No capability row is warranted**: the per-agent difference is already expressed
  as `LaunchFlags.DisplayName` being empty for Codex; adding a row would trip the
  declaration-coverage test into demanding declarations for a fact the flag table
  already carries.

- **Suffix width left open, not defaulted**: reusing the instance's 8 hex buys
  session/directory pair-legibility; matching the harness's own 6-hex row id buys
  convention fit and two characters. Genuine trade-off with evidence both ways, so
  it is handed downstream rather than settled here.

- **Three adjacent collisions recorded but not absorbed**: `niwa watch`'s
  by-construction slug collision, the `/dispatch` skill's brief-file overwrite, and
  the unnamed-dispatch case pinned by existing tests. Each is a separate decision
  with its own trade-offs; folding them in silently would widen the change beyond
  what the evidence covers.

- **The live ambiguous-send probe was deliberately not run**: it would mean sending a
  real message into a real colliding session belonging to unrelated work. The gap is
  accepted because all plausible answers point to the same remedy.

- **Private identifiers redacted from all committed artifacts**: the job-store dump
  and the peer listing both contain session and workspace names from non-public
  projects. This is a public repo, so workspace and role names were replaced with
  neutral placeholders and the redaction noted in-file. Structure, counts, and
  reasoning are unchanged.
