# Exploration Decisions: embedded-niwa-config

## Round 1 (Phase 1 -- scoping)

- **Topic slug**: `embedded-niwa-config`. Captures both the single-repo goal and
  the broader pattern (config embedded in a general-purpose repo).
- **Scope**: Tactical. Default for this repo; the user did not flag the topic
  as strategic.
- **Agent bias for research**: Neutral. The user asked agents to survey the
  option space rather than pre-commit to "`--from` and overlay don't change."
- **Migration framing**: Consolidation is a serious branch, not just a
  coexistence option. The user explicitly opened the door to migrating
  existing `dot-niwa` setups onto the same convention. Lead 7 reflects this.
- **Execution mode**: Switched to `--auto` mid-Phase-1 at user request.
  Decisions from here on follow the research-first protocol (gather, form,
  follow, document).
- **Target artifact**: PRD. The user stated explicitly that they expect a PRD
  out of this exploration. Phase 4 will still score the alternatives, but the
  user's stated preference is the strong default.

## Round 1 (Phase 3 -- converging findings)

- **Reframing**: the exploration shifts from "design a large UX change" to
  "close the R5 implementation gap in an already-shipped PRD." The
  existing `docs/prds/PRD-workspace-config-sources.md` covers the user's
  feature in detail and is marked `Done`, but R5 (auto-discovery of
  `.niwa/workspace.toml` when no explicit subpath is given) is not
  actually implemented in the materializer code path.
- **Verified gap**: `internal/workspace/snapshotwriter.go:440` calls
  `github.ExtractSubpath(body, src.Subpath, staging)` with an empty
  subpath when none is given, which extracts the whole repo. No probing
  step inspects the source for marker files first.
- **Scope of the new PRD**: closure of R5 + R6 + R7 + R8 + R33 from the
  existing PRD, plus a Decision section for the open questions (discovery
  probe mechanism, migration tooling, rank-3 `niwa.toml` keep/drop).
- **Convergence verdict**: Ready to decide after round 1 (per --auto
  research-first protocol). No second discover round needed.
- **Artifact form**: new PRD that references the existing one as the
  umbrella spec, rather than an amendment. Reasoning: the policy choices
  ahead (consolidation tooling, discovery-probe mechanism) and the user
  stories worth restating make a freestanding PRD easier to read than a
  long amendment block.
