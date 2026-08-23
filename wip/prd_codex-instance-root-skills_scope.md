# /prd Scope: codex-instance-root-skills

Chain-driven run: the upstream BRIEF (docs/briefs/BRIEF-codex-instance-root-skills.md,
now Accepted) supplies the framing, and the parent /scope chain supplies two
rounds of completed research plus measured design inputs. This scope file
records the requirements-altitude cut rather than re-deriving the framing.

## Problem Statement

`niwa dispatch` launches every background worker at a workspace instance root.
A Codex worker there resolves zero workspace skills, while the identical
session one directory down inside a cloned repository resolves all of them
namespaced. The root's own orientation document tells the worker to invoke a
skill it doesn't have. Rows 18 (RootProjectSkills) and 19 (NiwaPlugin) of the
capability contract record this as niwa's own unbuilt work (ReasonNotBuilt),
and both rows sit outside the bound-capability set, so nothing mechanical ties
declaration to delivery.

## Initial Scope

### In Scope

- Workspace plugin skills delivered to the instance root for Codex (row 18).
- niwa's own plugin (migrate-config skill) delivered to root Codex sessions (row 19).
- Both rows end implemented and bound: named deliveries, drift-failing in both
  directions, Claude's existing deliveries named as part of joining the set.
- Guide gap list shrinks by regeneration; authored prose corrected (guide
  instance-root section, dispatch warning, feature preamble, matrix amendment).
- Dispatch warning re-gated so it stays truthful rather than going silent.
- Functional acceptance with a negative control at zero model cost.
- A written position on root symlink targets under marketplace content replacement.

### Out of Scope

- Any trust entry for the instance root in the developer's own Codex config.
- Widening the Codex payload layout to instance-root scope.
- A where-from axis on the capability declaration schema.
- Trust-bypass flags.
- Changing per-repository or worktree delivery.
- Making Claude's plugin registration work (adjacent defect, observed on one machine).

## Research Leads

All leads were investigated by the parent chain before this skill ran:

1. Delivery path mechanics and structural-scan constraints — settled
   (exploration round 1, leads: skills-path, layout-scope, scans).
2. Row 19's embedded tree, manifest shape, and namespacing — measured
   (round 2, lead: niwa-plugin-delivery; codex-cli 0.147.0 measurements).
3. What remains missing at the root and the warning's re-gate — settled
   (round 2, lead: remaining-gap).
4. Acceptance mechanics with negative control — measured
   (round 1, lead: acceptance; live measurements via codex debug prompt-input).

## Coverage Notes

The four open questions the brief defers are design-level (binding mechanics,
manifest addition, warning gate mechanism, materialization site); the PRD pins
the requirement-level property behind each and leaves the mechanism to /design.
No uncovered dimension remains that another research round would settle — the
exploration's own closing judgment, accepted here.
