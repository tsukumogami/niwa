# Design Summary: niwa-config-skill

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** Single-repo (rank-1) niwa workspaces have no in-session agent
guidance for editing `.niwa/workspace.toml` in place. The existing embedded
Claude Code plugin's install is gated on rank-2 detection, which never fires
for the rank-1 workspaces that need this guidance.
**Constraints:**
- `public/niwa` repo only for this dispatch (companion changes elsewhere: note, don't implement)
- Bootstrap-scaffold-only delivery is ruled out (never reaches already-adopted workspaces)
- A bare static schema copy baked into the skill is ruled out (concrete drift-risk evidence: `docs/designs/current/DESIGN-workspace-config.md` is stale and actively wrong on the exact blocks -- `[claude.hooks]`, file-distribution -- this skill needs to teach)
- Two mechanically-proven delivery mechanisms are in play, with real trade-offs: extending/generalizing the embedded-plugin install gate, vs. using `[instance.files]` to materialize the skill from workspace.toml itself
- wip-hygiene applies: no committed references to wip/... paths
- niwa conventions apply: gofmt, go vet only, conventional commits, functional-test coverage for user-facing CLI changes

## Security Review (Phase 5)
**Outcome:** Option 2 -- document considerations
**Summary:** No design changes needed. One actionable finding: niwa's
public-repo plaintext-secret guardrail doesn't walk `claude.settings` or
`vault.provider` config, both blocks the new skill teaches editing, so
`SKILL.md` needs its own explicit anti-plaintext-secret guardrail language.

## Current Status
**Phase:** 6 - Final Review
**Last Updated:** 2026-08-06
