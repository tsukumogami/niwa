# Verdict: PASS

## Findings

### 1. Requirement unambiguity (vague qualifiers)
Pass. The vague-sounding qualifiers are each anchored by a concrete mechanism nearby, so they don't leave interpretation gaps that matter:
- R5 "reliably recovers" is immediately defined: "Ambiguity is resolved deterministically (newest-registration wins, validated before use); an unresolvable capture fails closed." A second developer would implement newest-registration disambiguation with pre-use validation.
- N3 "immediately after a retask" is bounded by the fail-closed/synchronous model (rebind happens in-band before the command returns), and the acceptance criterion pins it to `niwa list --json` reflecting the surviving session post-command.
- R4 "distinct, actionable error" and N2 "clear error" are made concrete by N3 ("Errors name the target, the detected worker state, and the reason") and the acceptance criteria ("distinct error per cause"). Not left to interpretation.
- R2 context continuity is unambiguous: "full prior transcript followed by the new instruction," with a byte-identical delivery acceptance test.

Each of R1–R9 and N1–N4 resolves to a single implementation. R4's worker-state matrix (live-idle / stopped-with-entry succeed; actively-running / attached / gone-entry fail closed) is explicit and testable.

### 2. Term consistency and reader-understandability
Mostly pass, with one minor finding.
- handle, target, session, job entry, mapping, instance, worker, superseded/surviving session are used consistently throughout. R1 defines `<target>` (instance name or session short id) and D1 restates it identically. The short-id (targeting) vs raw/rotated session id (rotates under the hood) distinction is drawn deliberately and held consistently (R1, R5, R6, KL1).
- Minor finding (non-blocking): `ED1` (R7) and `ED2` (D4) are used without definition. They read as exploration-internal decision labels and are not expanded anywhere in the PRD, so a reader who knows niwa but not this exploration cannot resolve the labels themselves. This does not rise to a FAIL because the substantive requirement is fully recoverable without them: R7 spells out "re-asserted through the same settings-applying launch path on every continuation," and the acceptance criterion pins it to egress-denial settings verified by the existing live gate. The `ED1`/`ED2` tokens are decorative citations, not load-bearing terms. Recommend expanding on first use (e.g., "the sandbox posture established during exploration (egress-denial via the settings-applying launch path)") or dropping the labels.
- `#211` is a GitHub issue reference, understandable to a niwa reader; niwa is a public repo so the reference is appropriate.

### 3. Frontmatter vs body substance
Pass. Frontmatter `problem` (handle holder cannot hand a follow-up task; the fork maneuver orphans the original, desyncs the mapping, and lets two sessions contend; every consumer rebuilds a partial workaround) matches the body Problem Statement in substance (resume forks a new session id, superseded session lingers as orphan, mapping points at a dead session, victims are the coordinator and watch's continuation, niwa should own it once). Frontmatter `goals` (one command delivers follow-up through the handle; context continuity; exactly one live owner; mapping/list truthful; chains indefinitely; supported surfaces, no root; replaceable delivery) maps one-to-one onto the body Goals bullets. No divergence.

### 4. Structural
Pass.
- Frontmatter has schema (prd/v1), status (Draft), problem, goals. Extra keys (upstream, motivating_context) are present and permissible.
- Frontmatter status "Draft" matches the body `## Status` first line, which is the bare word "Draft".
- Required sections present and in order: Status, Problem Statement, Goals, User Stories, Requirements, Acceptance Criteria, Out of Scope. Additional sections (Known Limitations, Decisions and Trade-offs, Downstream Artifacts) follow, which is allowed.
- Writing style: no AI-pattern words. Scanned for robust / leverage / comprehensive / holistic / facilitate / tier(ed) — none present. Prose is direct and varied.

### 5. Public-repo cleanliness
Pass. No private repo names (coding-tools, dot-niwa-overlay, tools, vision) and no private paths appear. No `wip/` references in the document. The `upstream:` frontmatter points at `docs/briefs/BRIEF-dispatch-handle-retask.md`, a durable path, not a wip artifact.
