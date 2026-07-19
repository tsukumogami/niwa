# Verdict: PASS

## Findings

### 1. All four Phase 5 REQUIRED changes are reflected in the architecture, not just Security Considerations

- **R-SEC-1 (refuse watch-sandboxed instances).** Present in the architecture text, not merely restated. The `retask.go` component bullet (Solution Architecture) states it "Refuses instances carrying a watch staged record or a sandbox stanza in their settings (R-SEC-1, `sandboxed` sentinel error) — only watch's own continuation path can safely re-assert review containment." It is also carried in the frontmatter `decision` ("Generic retask refuses watch-sandboxed instances"), Decision Outcome, and Q1's sentinel taxonomy. The behavior is described where the responsible component is defined.
- **R-SEC-2 (default-deny classifier).** Present in the Considered Options / Q1 text: "Worker classification is default-deny (see Security Considerations, R-SEC-2): gone = `!sessionLive`; a worker is retaskable only when the decoded job state positively proves it idle (terminal state, no active tempo, no in-flight tasks, no pending need); busy, blocked, absent, or undecodable signals all refuse." It also correctly notes the decoder does not yet read these fields and that adding/validating them is part of the live-gate step — matching the report's core concern.
- **R-SEC-3 (id revalidation at point of use).** Present in the `retask.go` bullet: "every session id read from a mapping body is re-validated with `ValidSessionID` immediately before entering argv or a path (R-SEC-3)."
- **R-SEC-4 (lock filename from validated resolved name).** Present in the `retask.go` bullet: "The lock filename is built from the resolved `mapping.InstanceName` after a path-component assertion (R-SEC-4), and ... never the raw user-controlled target string" (Security Considerations restatement).

### 2. No section contradicts or waters down a security requirement

- The classifier is described as default-deny consistently in Q1, the Decision Outcome, and R-SEC-2. No section elsewhere describes permissive/zero-value defaults or an "anything else is retaskable" fallthrough (the exact fail-open the report flagged).
- The data flow diagram folds the sandboxed refusal into the generic `classify -> [refuse: sentinel error]` branch rather than drawing it as its own arrow. This is an abstraction, not an omission — the sandboxed refusal is explicitly described in the `retask.go` component prose one paragraph above, and the diagram's refuse branch subsumes it. Not a contradiction or a watering-down.
- Fail-closed ordering (write-new → delete-old → `claude rm`; refuse-before-mutation) is consistent across Q2, Q3, Decision Outcome, and the data-flow note "Failure at any arrow before RebindMapping aborts with prior state intact."

### 3. Security Considerations fairly represents the Phase 5 report

The design's Security Considerations and Consequences carry the report's clean findings (same-user boundary / no new trust boundary, single-argv prompt guard, flock self-release on crash, N4 no-new-privileges) and its accepted residual risks (fork-under-the-hood id rotation; crash-between-resume-and-rebind self-healing via the reap sweep). Nothing material from the report is dropped.

Two non-blocking gaps, both below FAIL threshold:
- The report's correctness note that a *failed* `claude rm` re-introduces #211 residual ambiguity (report calls it "worth a reap-backed sweep or a logged warning, but not a security defect") is not surfaced in the design. The report itself classifies it as correctness, not security, so its absence does not affect this verdict — flagging for the architecture reviewer.
- The report's "three independent layers" observation names `instanceHasLiveJob` (the cwd guard in `job_state.go`) as a third defense that must be *kept* alongside the new trylock and live-mapping-wins collision preference. The design describes adding the trylock and the collision preference but does not affirm that the existing `instanceHasLiveJob` guard is retained. The design does not remove or alter it either, so no new hole is created; worth an explicit "keep this guard" line but not required.

### 4. Sanity check for NEW holes in the assembled design — none found

- **`--json` leakage.** The result record is instance name, old/new session ids, rotated flag, and state. Session ids are same-user UUIDs, not secrets; the prompt text is never echoed into output. No leak.
- **PreLaunch hook as an injection point.** The hook is a package-internal `func` seam (watch passes `ApplyReviewSettings(...)`); it is not reachable from user flags or argv on the generic `retask` path. The generic path has *no* PreLaunch hook, which is precisely why R-SEC-1 refuses sandboxed instances — the coupling is closed, not opened.
- **Reap collision-preference change.** The change from last-write-wins to live-mapping-wins is a strengthening the report endorsed; combined with the trylock it closes the stop-window reap race. It does not remove the `instanceHasLiveJob` spare-live-instance guard, so reap's core "never reap an instance with a live job" guarantee is preserved. No weakening.
- **`--` terminator (R-SEC-3 sub-point).** The report recommended passing ids after a `--` terminator "where the subcommand supports one" in addition to `ValidSessionID`. The design captures the `ValidSessionID` point-of-use check (which itself rejects the leading-dash / flag-shaped strings the `--` guards against) but omits the `--` detail. The primary defense is present; the `--` is defense-in-depth and an implementation detail below design altitude. Not a hole.

### 5. Public-repo cleanliness

Clean. No `wip/` references in frontmatter, prose, or code comments (grep confirmed). No secrets, no private/competitive content. The doc references only public artifacts (the sibling PRD, GitHub issue #211, public repo paths). Frontmatter `upstream:` points at a durable `docs/prds/` path, not a `wip/` staging path.
