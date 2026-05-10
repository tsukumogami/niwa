---
complexity: testable
complexity_rationale: New queue-time gate added between two existing handler steps; introduces a new error code `MISSING_SKILLS` with a `{missing, available}` response shape. Body convention preserves the wire schema. Defense-in-depth on top of <<ISSUE:4>>'s inheritance contract.
---

## Goal

Add a queue-time `required_skills` precondition gate to `niwa_delegate` (and the to-be-added `niwa_redelegate`) that reads `body.required_skills: string[]`, intersects with the workspace skill manifest, and returns `errResultCode("MISSING_SKILLS", {missing, available})` synchronously when any required skill is absent — catching typos and intent drift before a task ID is allocated.

## Context

Design: `docs/designs/current/DESIGN-niwa-mesh-reliability.md`

After <<ISSUE:4>> lands, workers inherit the workspace's plugin set as their baseline (Decision 1's contract). The `required_skills` gate becomes defense-in-depth — its primary load-bearing utility is **catching typos at queue time** (`/shirabe:prd` vs. `/shirabe:rpd`), so the failure surfaces synchronously at delegation rather than the worker discovering it mid-task and abandoning.

The body-convention placement (`body.required_skills` rather than a top-level `delegateArgs` field) preserves niwa's small wire schema and matches the existing "body is opaque to niwa" convention for workspace-specific fields. It also propagates through `niwa_redelegate` (<<ISSUE:7>>) for free — the redelegate handler reads the source body and the gate runs against the merged result without special propagation logic.

The gate runs uniformly on `read_only=true` and session-routed delegations: `read_only=true` routes to the main clone, where the manifest is the same workspace `.claude/` tree the gate consults.

The manifest source-of-truth is the workspace's `.claude/` tree under `s.mainInstanceRoot` (or `s.instanceRoot` if mainInstanceRoot is empty). Specifically: enumerate plain skills under `<workspaceRoot>/.claude/skills/<skill-name>/SKILL.md`, plus resolve plugin skills via `enabledPlugins` from the workspace settings (which the user's `~/.claude.json` plugin store knows how to expand).

Closes #113.

## Acceptance Criteria

- [ ] `handleDelegate` (`internal/mcp/handlers_task.go:111-165`) is updated to insert a body peek between the existing `UNKNOWN_ROLE` check (line 130-133) and `createTaskEnvelope` (line 141):
  - `var peek struct { RequiredSkills []string \`json:"required_skills"\` }`
  - `_ = json.Unmarshal(args.Body, &peek)` (already validated as a non-null object).
  - If `len(peek.RequiredSkills) == 0`, behavior is unchanged from today.
- [ ] When `body.required_skills` contains entries, the handler resolves the workspace skill manifest:
  - Plain skills: enumerate `<workspaceRoot>/.claude/skills/<name>/` directories that contain a `SKILL.md`. The skill name is the directory name (e.g., `niwa-mesh`).
  - Plugin skills: parse `<workspaceRoot>/.claude/settings.json`'s `enabledPlugins` keys (e.g., `shirabe@shirabe`) and expose namespaced skill IDs derivable from each plugin's manifest (`shirabe:plan`, etc.).
  - Both are unioned into the available set used for the intersection check.
- [ ] On miss, the handler returns `errResultCode("MISSING_SKILLS", body)` where the body is JSON of shape `{"missing": [<missing>...], "available": [<sorted available>...]}`. No task ID is allocated; no envelope is written.
- [ ] On match (or empty `required_skills`), behavior continues to `createTaskEnvelope` unchanged.
- [ ] The same gate runs inside `handleRedelegate` (delivered by <<ISSUE:7>>) before `createTaskEnvelope`, against the merged body.
- [ ] The gate fires uniformly on `read_only=true` and session-routed delegations; no per-routing-path branching.
- [ ] New error code `MISSING_SKILLS` added to the audit log error vocabulary in `handlers_task.go:13-15` (and corresponding tests).
- [ ] No top-level field added to `delegateArgs` (`internal/mcp/handlers_task.go:46-55`); no schema change to the MCP wire-level descriptor at `internal/mcp/server.go:264-279`.
- [ ] Functional test (typo catch): `niwa_delegate(body={..., required_skills: ["shirabe:rpd"]})` returns `MISSING_SKILLS` with `missing=["shirabe:rpd"]` and `available` containing `shirabe:plan`.
- [ ] Functional test (match): `niwa_delegate(body={..., required_skills: ["shirabe:plan", "niwa-mesh"]})` succeeds and the task is created normally.
- [ ] Functional test (omitted): `niwa_delegate(body={...})` (no `required_skills` key) succeeds — behavior unchanged from today.
- [ ] Functional test (read_only path): `niwa_delegate(body={..., required_skills: [...]}, read_only=true)` runs the gate against the workspace manifest the same way as the session-routed path.
- [ ] Must deliver: `MISSING_SKILLS` error code and `{missing, available}` response shape (required by <<ISSUE:7>> and <<ISSUE:8>>).

## Dependencies

- <<ISSUE:4>> (worker config inheritance) — the gate reads from the workspace `.claude/` tree that <<ISSUE:4>> makes authoritative for workers. Without <<ISSUE:4>>, the gate could fail on skills the worker would actually have, or accept skills the worker won't see.

## Downstream Dependencies

- <<ISSUE:7>> (niwa_redelegate) runs the same gate against the merged body; relies on the `MISSING_SKILLS` error code and the manifest-resolution helper.
- <<ISSUE:8>> documents the gate, the body convention, and the `MISSING_SKILLS` error in the niwa-mesh skill text.
