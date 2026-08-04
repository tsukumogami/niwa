# Design Decisions: orchestration-learnings (--auto)

## Protocol note

Phase 2 normally spawns one decision-researcher per architectural question. Four of the
five questions below were already researched to citation depth during `/explore`, by
agents dispatched for exactly that purpose — `lead-niwa-distribution` enumerated the
seeding mechanisms with file:line evidence and a recommendation, `lead-extension-layering`
tested the `@import` route experimentally and refuted it, `lead-chain-shape` produced the
criteria and their counter-evidence, and a direct probe run settled the harness mechanics.

Re-dispatching researchers against questions that already have cited answers would be the
same wasteful delegation this design exists to warn about: the session under study lost
three of four status agents to silent stalls while direct work answered the same questions
in minutes. Phase 2 therefore consumes the existing research files as its decision reports
and records the reasoning here. D3 and D5 had no prior research and are decided inline
below with their alternatives.

## D1 — How the standing agreement reaches a workspace

**Chosen: sentinel-section merge.** niwa owns a delimited block in
`.niwa/dispatch-briefs/_common.md` and rewrites it on every root materialization;
anything outside the sentinel survives.

Alternatives, from `lead-niwa-distribution` §4:

- *Write-if-absent.* Rejected. It freezes every workspace at whatever version of the
  agreement it first saw. niwa could never ship a correction — and the whole reason this
  design exists is that the previously published resume recipe was wrong.
- *Plain overwrite plus a `_common.local.md`.* Rejected as the primary. Less Go, and it
  matches the `.local` convention, but it pushes composition onto the reader and a
  workspace that edits the base file loses the edit silently.
- *`[root.files]` distribution.* Rejected. The file would be workspace-authored rather
  than niwa-shipped, which does not satisfy "ships with niwa", and it targets a directory
  the file-distribution guide rules out.

Precedent: `installWorktreeContextLayer` / `stripWorktreeContextSection` in
`internal/workspace/worktree_content.go` already implement strip-and-reappend against a
stable heading sentinel, and are tested. This copies a pattern rather than inventing one.

The preservation guarantee is not an obstacle: `preserveDispatchBriefs` defends the
config-snapshot swap, which `niwa apply` runs at `cli/apply.go:177`, before
`MaterializeWorkspaceRoot` at `:238`.

## D2 — How a root skill carries more than one file

**Chosen: extend the materializer to copy a root skill's whole directory.**

`writeRootSkills` currently walks the embedded tree and picks up only
`rootskills/<name>/SKILL.md`. The content here does not fit in single files without
either padding out the guides — which most workspaces cannot reach — or cutting material
the acceptance criteria require.

Alternatives:

- *Write tighter, one file per skill.* Rejected. The harness recipe and the review
  standard both need room, and compressing them is how a rule loses the specific,
  checkable form that makes it usable.
- *Detail in `docs/guides/`, skills stay thin.* Rejected on the reachability finding: a
  workspace that uses niwa does not have niwa's source tree.

The change is small and mechanically identical to the existing walk — it stops filtering
on the `SKILL.md` basename and preserves the relative path under the skill directory.

## D3 — Skill boundaries

**Chosen: two shipped root skills.** `/dispatch` extended with the before-launch
decision; a new `/fleet` skill for the after-launch loop.

The deciding property is trigger moment, not subject matter. Skill descriptions gate
loading, and a skill named `dispatch` does not load when someone asks what their workers
are doing. The transcript shows that question asked four separate times, and the
after-launch material is worthless if it is only in context at the moment work is handed
off.

Alternatives:

- *One skill.* Genuinely tempting — the coordinator that produced this material
  specifically predicted over-production as this task's failure mode, and one skill is the
  smaller shipped surface. Rejected because it puts the stranded-work sweep and the
  wake-or-fix decision behind a trigger that fires before either is relevant.
- *Three skills, reviews separate.* Rejected. The review standard is a thing the
  coordinator does to work that came back, which is the same loop as everything else in
  `/fleet`; splitting it adds a trigger without adding a distinct moment. With D2 resolved,
  it can be a reference file inside `/fleet` rather than its own skill.

Naming: `/fleet` rather than `/inflight`, which is taken by shirabe and deliberately
scoped to *the current session's own* PRs with an explicit contract never to issue a
non-session listing. `/fleet` is about other sessions' work. The two are complementary and
the descriptions must say so, or they will collide in the trigger space.

## D4 — Chain-shape vocabulary

**Chosen: tool-neutral framing levels, with a mapping note.**

Settled during exploration and author-confirmed. `explore → scope → execute`, `work-on`
and `decision` are one plugin's skill names; niwa cannot assume that plugin is installed,
and shipping them as doctrine would make niwa's documentation depend on another tool's API.
The decision underneath is tool-neutral — how much framing the work needs before someone
starts implementing — and it has three levels selected by properties of the work.

Alternative considered: naming the shirabe skills outright, framed as an example. Sharper
and closer to what actually happened, but it fails the audience constraint the author set.

## D5 — The materialization coverage gap

**Chosen: add `niwa create` as a second call site.**

`MaterializeWorkspaceRoot` runs only at root-scope `niwa apply` (`cli/apply.go:237`) and
on `niwa init` in named/clone mode (`init.go:771`, `:1004`). `niwa create` and
`niwa dispatch` do not refresh the workspace root, so a workspace whose owner only runs
instance-scoped applies never receives shipped content — or a correction to it.

`niwa dispatch` goes through `Create`, which makes this the exact moment the content is
about to matter: dispatching a worker should guarantee the current agreement is on disk
for that worker to read. Without it, the ship-a-correction property D1 was chosen for does
not actually hold for the workspaces most likely to need it.

Alternatives:

- *Document the constraint, do not fix it.* Rejected. It leaves D1's central benefit
  conditional on a command the author may never run.
- *File as a follow-up.* Rejected for the same reason; it is load-bearing for this design
  rather than adjacent to it.

The write is idempotent by construction (unconditional overwrite of niwa's own block,
merge-preserving for everything else), so adding a call site cannot corrupt state — it can
only cost a few file writes per create.
