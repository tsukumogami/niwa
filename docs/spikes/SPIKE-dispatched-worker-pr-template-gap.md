---
status: Complete
question: |
  Why do niwa-dispatched background workers produce pull requests that ignore the
  org's pr-creation two-part template, even when the brief tells them to run the
  shirabe /scope and /execute workflows (which author a template-conformant PR), and
  what is the narrowest durable fix that makes the next dispatched worker conformant
  by construction?
timebox: "1 session (diagnosis only; no fix implemented)"
---

# SPIKE: dispatched workers ignore the pr-creation two-part PR template

## Status

Complete

The cause is identified and reproduced from inside a live dispatched instance. This
spike diagnoses and recommends only. No fix is implemented here; landing the fix is
deferred for human direction (see Recommendation).

## Question

A coordinator dispatched a background worker into a fresh ephemeral niwa instance
with a brief that said "run shirabe:scope, then shirabe:execute." The worker did the
work but authored PR tsukumogami/niwa#177 with a freeform body: no `---` separator,
no two-part (Part 1 commit message / Part 2 reviewer context) structure, and it
leaked internal-workflow phrasing into a public-repo PR. It only fixed this afterward
by running `/fix-pr`.

Why does the template step get skipped for a dispatched worker, and what's the
narrowest durable fix?

A gate question had to be settled first, because an existing brief
(`.niwa/dispatch-briefs/fix-root-plugin-provisioning.md`) asserts the opposite of
what the worker reported. That brief claims ephemeral **instances** load the full
shirabe plugin set and only the workspace **root** is missing them. The worker
reported shirabe was not invocable inside its instance. The whole diagnosis branches
on which is true, so it was established empirically before anything else.

## Context

The shirabe `/execute` workflow has an explicit step that authors a
template-conformant PR. If a dispatched worker can run `/execute` the normal way,
the PR is conformant by construction and no agent memory is required. The symptom PR
shows that didn't happen. Three things needed to be distinguished:

- whether the shirabe skills were even loadable in the dispatched instance (a
  provisioning question, owned by niwa);
- whether the concrete PR-template spec is reachable when the workflow is hand-run
  from its `SKILL.md` instead of driven by the koto runtime (a content question,
  owned by shirabe);
- whether anything downstream enforces conformance (a CI question).

The candidate hypotheses going in:

- **H1 — plugin-load gap.** Dispatched instances don't load the shirabe plugin, so
  the worker can't invoke `/scope` / `/execute` and hand-runs them.
- **H2 — template step unreachable when hand-run.** The concrete spec lives only in
  the koto template, which a human or agent reading `SKILL.md` never opens.
- **H3 — enforcement gap.** Nothing checks PR-body conformance, so a malformed PR
  merges silently.
- **H4 — dispatch front door.** The `/dispatch` brief convention should hard-wire the
  template requirement, since a dispatched worker predictably hand-runs workflows.

## Approach

The investigation ran inside a live dispatched ephemeral instance (the one created
for this spike), so the gate question could be answered with direct evidence rather
than inference.

1. Enumerated the skills actually invocable in this session and compared them against
   the plugins declared enabled.
2. Inspected this instance's `.claude/settings.json`, `.niwa/instance.json`, and the
   shared `~/.claude/plugins/` store (`known_marketplaces.json`,
   `installed_plugins.json`, and the on-disk plugin cache).
3. Compared install timestamps against instance-creation time to test for a
   startup race.
4. Traced niwa's dispatch and provisioning code
   (`internal/cli/dispatch.go`, `internal/cli/dispatch_launcher.go`,
   `internal/cli/instance_from_hook.go`, `internal/workspace/materialize.go`,
   `internal/workspace/apply.go`, `internal/plugin/`) to find whether plugins are
   installed before the worker launches.
5. Read the shirabe `/execute` skill surface
   (`skills/execute/SKILL.md` vs `skills/execute/koto-templates/execute.md`) and the
   `tsukumogami:pr-creation` skill to locate where the two-part spec actually lives
   and whether its references resolve.

## Findings

### Gate: shirabe-namespace skills are NOT invocable in a dispatched instance

Confirmed, with evidence from this instance. The shirabe skills (`scope`, `execute`,
`design`, `plan`, `brief`, `prd`, ...) are not in this session's invocable skill set.
Only the `tsukumogami:*` skills, plus the stock plugins (superpowers, vercel,
telegram, skill-creator) and `dispatch`, are present. An independent tell: the
`tsukumogami:legacy-design` skill advertises "[DEPRECATED: use /shirabe:design]" —
pointing at a skill that isn't loaded.

This is not a misconfiguration. The plugin is enabled and fully present on disk:

- `.claude/settings.json` declares
  `enabledPlugins: { "shirabe@shirabe": true, "tsukumogami@tsukumogami": true }`.
- `installed_plugins.json` registers `shirabe@shirabe` for this exact project path,
  installPath `~/.claude/plugins/cache/shirabe/shirabe/0.13.1-dev`.
- That cache directory contains the full `skills/` tree (scope, execute, design,
  plan, brief, prd, ...) and a `plugin.json` declaring `"skills": "./skills/"`.

So the plugin is on disk and enabled, yet its skills were never loaded into the
running session.

### Mechanism: a startup race between marketplace install and skill enumeration

The two declared marketplaces resolve very differently:

- `tsukumogami` is **directory**-sourced (local `private/tools`). It's already on
  disk, so its skills load immediately. These are the skills that did appear.
- `shirabe` is **github**-sourced (`tsukumogami/shirabe`). It requires a network
  clone/update at Claude Code startup. These are the skills that didn't appear.

Timestamps from this instance show the race directly:

- instance created (`.niwa/instance.json`): `2026-06-28T03:08:33Z`
- shirabe plugin cache install finished (`installed_plugins.json` `installedAt`):
  `2026-06-28T03:08:52Z` (+19s)
- shirabe marketplace `lastUpdated` (`known_marketplaces.json`):
  `2026-06-28T03:08:54Z` (+21s)

niwa launches the worker without waiting for any of this. In
`internal/cli/dispatch.go`, `runDispatch` provisions the instance and then calls
`dispatchLaunch` immediately; `internal/cli/dispatch_launcher.go` runs
`claude --bg ...` with no readiness gate. Provisioning
(`internal/workspace/materialize.go`, the `buildSettingsDoc` path that emits
`enabledPlugins` / `extraKnownMarketplaces`) only **writes settings** — there is no
`claude plugin install` / `marketplace add` shell-out and no poll that blocks until
the github marketplace is resolved. Claude Code therefore enumerates its skills at
startup before the github clone finishes, and the shirabe skills are absent for the
entire session.

Worth noting: niwa already knows how to materialize a plugin to disk before launch —
`internal/plugin/` embeds niwa's own plugin and installs it under
`~/.claude/plugins/marketplaces/niwa/` from the binary, with no network. That
machinery just isn't applied to the workspace-declared marketplaces.

An aggravating factor: a version/config mismatch widens the race window. This
instance's settings pin shirabe to `ref: v0.13.0`, but the shared
`known_marketplaces.json` has shirabe with `autoUpdate: true` and no pin, and the
installed version is `0.13.1-dev`. The autoUpdate-plus-pin mismatch invites a fresh
fetch at startup every time, where a stable, already-resolved local marketplace would
never race.

**This refutes the premise of `fix-root-plugin-provisioning.md`.** That brief assumes
instances load shirabe and only the root is short. In fact an instance gets shirabe
*written into settings and eventually installed to disk*, but *not loaded into the
running session*. The earlier observation of "instances are fine" most likely came
from inspecting settings/disk state (where shirabe looks enabled) rather than the
session's actual invocable-skill list. The root-provisioning work in that brief may
still be valid on its own terms, but it does not address — and is partly contradicted
by — what breaks dispatched workers.

### Why the template is then skipped: H2 is real and compounds H1

Because `/execute` wasn't invocable, the worker hand-ran it by reading
`skills/execute/SKILL.md`. The concrete two-part PR spec is not there. `SKILL.md`
mentions it only in passing: "`pr_finalization` — assemble the template-conformant PR
(title + two-part body)" (around line 187). The actual spec — conventional-commit
title, Part 1 becomes the squash commit body, everything from `---` down is deleted
at merge, the exact `gh pr edit --title ... --body-file ...` form — lives only in
`skills/execute/koto-templates/execute.md`, in the `pr_finalization` state
(lines 386-417). That koto template is consumed by the koto runtime, not by a human
or agent reading `SKILL.md`. Hand-running drops the spec.

Two details make the hand-run path worse:

- **Dangling cross-plugin pointer.** `execute.md:390` says the canonical spec is
  `skills/pr-creation/SKILL.md` and to "apply that spec inline." But shirabe has no
  `pr-creation` skill; that path doesn't resolve inside the shirabe repo. The real
  skill is `tsukumogami:pr-creation`, which lives in a different plugin (sourced from
  the `tools` repo). An agent that did reach `execute.md` still couldn't follow the
  pointer without already knowing where the skill lives.
- **An instruction that backfires when hand-run.** `execute.md:390` also says "do
  NOT invoke the cross-plugin `/fix-pr` or `pr-creation` skill at runtime." That's
  correct when koto drives, because koto inlines the spec. When the workflow is
  hand-run, it actively tells the agent not to use the one skill that was loaded the
  whole time and would have produced a conformant PR — `tsukumogami:pr-creation`.

So the symptom is a chain: **H1 (the load race) forces the hand-run, and H2 (spec
only in the koto template, plus an unresolvable pointer) is what actually drops the
template.** A third factor sits behind both: "create a PR" is itself a task with a
dedicated, loaded skill (`tsukumogami:pr-creation`), and the worker treated it as
freeform writing.

H3 (no PR-body conformance check) and H4 (dispatch brief doesn't hard-wire it) are
genuine gaps but secondary — defense-in-depth, not the root cause.

### Note on single-repo /scope

`skills/scope/SKILL.md` confirms a single-repo `/scope` opens no regular PR (only a
coordination PR when coordination intent is present; line 121). So for a single-repo
scope, the "open a PR" step is supplied by the brief or operator and has no template
anchor of its own — another reason the worker was authoring a PR body without a spec
in front of it.

## Recommendation

Go on a two-part fix, in priority order. Prefer making the right thing happen by
construction (the niwa fix) over relying on the agent to remember (brief/CI nudges).

### Primary — close the plugin-load race in niwa (owner: niwa)

Make instance provisioning install/resolve the declared github-sourced marketplaces
and plugins to disk **before** launching the worker, so the skills are present when
Claude Code enumerates them at startup. niwa already has the shape for this in
`internal/plugin/` (it materializes its own plugin pre-launch); extend the dispatch
path (`internal/cli/dispatch.go` -> `dispatch_launcher.go`) to either shell out to
`claude plugin marketplace add` / `claude plugin install` for the declared set, or
block on a readiness check until the marketplace cache is resolved, before
`dispatchLaunch`. Also reconcile the `autoUpdate: true` vs pinned-`ref` mismatch so
the marketplace resolves deterministically instead of re-fetching at every startup.

This is the durable root fix: it makes `/scope`, `/execute`, and every other shirabe
skill invocable for dispatched workers. Once `/execute` is koto-driven, the
`pr_finalization` state inlines the correct spec and authors a conformant PR with no
agent memory required — which dissolves H2 in the normal path.

Trade-off: it adds latency to dispatch (a network clone before launch) and couples
niwa to Claude Code's plugin CLI surface. Both are acceptable for correctness; the
latency is one-time per instance and the CLI surface is already a dependency.

### Companion — harden the hand-run fallback in shirabe (owner: shirabe)

Even with the niwa fix, the hand-run path persists during any residual race window or
if a pre-install fails, so make it safe:

- Fix the dangling reference at `skills/execute/koto-templates/execute.md:390`.
  Either make it resolvable ("if running outside koto, invoke the `pr-creation`
  skill") or stop telling a hand-running agent not to invoke `pr-creation`. The
  "do not invoke at runtime" instruction should be scoped to koto-driven runs.
- Surface the two-part requirement in `skills/execute/SKILL.md` itself — at minimum a
  pointer that resolves without the koto runtime — so a worker reading only `SKILL.md`
  sees it.

This is low-cost and high-value; it's the difference between a hand-run that degrades
gracefully and one that silently drops the template.

### Optional — backstops (owners: dot-niwa-overlay / CI)

- Have the `/dispatch` brief convention state that PRs must follow the pr-creation
  two-part template and that `tsukumogami:pr-creation` should be invoked. Cheap, but
  relies on agent memory — weaker than the niwa fix.
- Consider a CI PR-body conformance check (analogous to the existing per-file
  validate-docs workflow). Likely overkill for now; flagged for completeness.

### Scope discipline for the follow-up

The niwa fix and the shirabe fix are separate PRs in separate repos. Don't fold the
shirabe content fix into the niwa change. Don't re-litigate the #177 idle-reap fix —
only its PR format is in scope here. The `fix-root-plugin-provisioning.md` task should
be re-scoped in light of this spike's gate finding before it proceeds.
