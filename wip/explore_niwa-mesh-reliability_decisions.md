# Exploration Decisions: niwa-mesh-reliability

## Round 1

- **Treat the cluster as one design, not nine independent bugfixes.**
  Reason: shared root causes (file/API state asymmetry), shared modules
  (`internal/mcp`, `internal/workspace`, `internal/cli/mesh_watch.go`),
  and shared user-facing surfaces (niwa-mesh skill + `docs/guides/sessions.md`).
  Splitting would force the skill text to drift across nine PRs.

- **One round of research is sufficient.** Reason: the six leads
  returned tight, file:line-cited findings with no contradictions.
  Surviving open questions are design choices answerable inside the
  doc, not feasibility questions that need investigation.

- **Crystallize to a Design Doc.** Reason: the WHAT (issue acceptance
  criteria) is settled, the HOW has multiple valid options that need a
  unified resolution. Roadmap is wrong because this is one coupled
  feature, not multiple sequenced features. PRD is wrong because the
  requirements aren't contested.
