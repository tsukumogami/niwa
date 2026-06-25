# Design summary: instance-dispatch

Upstream PRD: docs/prds/PRD-instance-dispatch.md (Accepted, R1-R46). Visibility Public.
Single-pr scope. Auto-mode under /scope.

## Decision questions (Phase 1 decomposition)

- D1 Command surface (verb/name + flags). Indicated: `niwa dispatch` (positional prompt,
  --label, pass-through --model/--permission-mode/--agent, returns-with-hints not attach).
- D2 Instance naming token (concurrency-safe --name). Indicated: random short token
  (e.g. `disp-<8 hex>`) via the customName branch, sidestepping the racy numbered scan.
- D3 Session-id capture mechanism. CONTESTED: (a) scrape `backgrounded · <short-id>` then
  read jobs/<short-id>/state.json; (b) skip scraping — watch the jobs dir for a new
  state.json whose cwd == the instance dir; (c) hybrid. -> decision-researcher.
- D4 Partial-failure rollback. CONTESTED: (a) command self-rollback (destroy own instance
  on any pre-mapping failure); (b) teach the reaper to reclaim mapping-less ephemeral
  dirs by a marker; (c) write a provisional sentinel. -> decision-researcher.
- D5 In-flight protection (R38). Indicated: no new lock — the reaper only targets
  instances WITH an ephemeral mapping, so an unmapped in-flight instance is invisible.
- D6 Mapping provenance. Indicated: additive `origin` field (absent-decodes-to-zero),
  not Label; informational, does not change reaper eligibility.
- D7 Reuse & code structure. Indicated: generalize sessionattach supervisor into a
  capture-capable launcher; reuse realProvisionInstance + destroyInstanceFunc; new
  internal/cli/dispatch.go.
- D8 Prompt handling. Indicated: single argv element, reject empty, clear ARG_MAX error.
- D9 Test seams. Indicated: injectable launcher func var, injectable jobs-dir root,
  destroyInstanceFunc var, localGitServer harness.

Contested decisions (D3, D4) go to decision-researchers; the rest are settled from the
phase-2 grounding (wip/research/prd_instance-dispatch_phase2_code-facts.md) and recorded
as Considered Options in the design.
