---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-codex-background-dispatch.md
milestone: "Codex background dispatch: the reachable surface"
issue_count: 10
---

# PLAN: Codex background dispatch -- the reachable surface

## Status

Active

## Scope Summary

The first round of this chain shipped the mechanism: `niwa dispatch` can
launch a background Codex worker, capture the session it became, and
resume it. It did not ship a way to reach that mechanism. Selecting the
launched agent had no flag, could not be set by `niwa config set`, and
appeared in no committed documentation; the command's own help text said
it launched Claude Code; and the handle the feature exists to produce has
no durable home.

This plan covers what three reviewers -- on UX, on documentation, and on
this repository's delivery conventions -- judged necessary before the
feature counts as delivered. Their verdicts are at
`wip/research/review_agent-selection_{ux,docs,process}.md` and are the
source of every item below. Items nobody named are not here; the
reviewers were each asked to be adversarial about scope inflation and
each returned a shorter list than the one I would have written.

## Decomposition Strategy

**By failure, not by layer.** Each item below is a thing that is wrong
for a developer today, ordered by what it costs them. Items 1 through 3
are the selection surface and are already in flight on
`feat/agent-selection-surface`; items 4 through 6 are defects the review
surfaced that nothing was tracking; items 7 through 10 are the
documentation and messaging the reviewers judged necessary.

Two items are deliberately *not* here and are named in Open Questions
rather than dropped silently.

## Issue Outlines

### Issue 1: `--launch-agent` on `niwa dispatch`

**Goal**: Fill the flag slot in `resolveSessionAgent` that has existed
since the resolver was written and whose one caller passes `""`.

**Acceptance Criteria**:
- `niwa dispatch --launch-agent codex` launches a Codex worker in a
  workspace whose stated agent is Claude
- `--agent` keeps its current meaning as the subagent-type passthrough,
  unchanged and unaliased
- `niwa create` and `niwa apply` gain no such flag; the scenario pinning
  `apply --agent codex` as an unknown flag still passes
- A `@critical` scenario covers the flag, offline, with no real `codex`
  binary

**Dependencies**: None
**Complexity**: simple

### Issue 2: `niwa config set default-agent` and its `unset`

**Goal**: Give the setting a durable home that a snapshot refresh cannot
take away.

**Acceptance Criteria**:
- Writes `[global].default_agent` to `~/.config/niwa/config.toml`,
  alongside `dispatch_model` and the other dispatch-scoped host defaults
- Never writes inside `<workspace>/.niwa/`; a test fails if the write
  target is under that directory
- `niwa config unset default-agent` removes it
- Rejects a value `agent.ParseAgent` does not accept, naming the accepted
  set

**Dependencies**: None
**Complexity**: testable

### Issue 3: The precedence, stated once and enforced

**Goal**: flag > `NIWA_AGENT` > `[workspace].default_agent` >
`[global].default_agent` > claude, with the host default the weakest rung
above the built-in -- matching what `remote_control_on_dispatch` and
`keep_alive_on_dispatch` already document.

**Acceptance Criteria**:
- A test exercises all five rungs and fails if any is unreachable or the
  order differs
- The `GlobalSettings.DefaultAgent` doc comment states the full chain and
  says it never changes what `niwa apply` prepares, matching its
  neighbours' style
- Whether the workspace-outranks-host case warrants a stderr line is
  decided explicitly, with silence a defensible answer

**Dependencies**: Issue 1, Issue 2
**Complexity**: simple

### Issue 4: `niwa dispatch --agent codex` must not silently launch Claude

**Goal**: Close the first command a developer types. Today the flag is
ignored for selection, forwarded to Claude as a nonexistent subagent
type, and the failure carries no diagnosis -- the backgrounded launch
path wires no stdout or stderr, and `readWorkerLogTail` reads only the
detached path's log.

**Acceptance Criteria**:
- When `--agent`'s value parses as a known agent and differs from the
  resolved launch agent, one line on stderr before the launch says the
  flag names a subagent type, names the agent actually being launched,
  and names `--launch-agent`
- A warning, never a refusal: `claude` is a legitimate subagent-type name
  and rejecting it would break real use
- A test fails if the warning is absent or if it fires when the two agree

**Dependencies**: Issue 1
**Complexity**: simple

### Issue 5: Session mappings survive a config-snapshot refresh

**Goal**: Stop a snapshot swap from destroying the handle the feature
exists to produce. Dispatch writes `<workspace>/.niwa/sessions/<id>.json`
inside the directory the snapshot writer rotates wholesale, and only
`instance.json` and `dispatch-briefs/` are carried across.

**Acceptance Criteria**:
- A third preserver carries `sessions/` across the swap, following
  `preserveInstanceState` and `preserveDispatchBriefs`
- A test plants a mapping, forces a refresh, and fails if the mapping is
  gone
- The reaper's mapped sweep still finds its join after a refresh

**Dependencies**: None
**Complexity**: testable

**Note:** for a GitHub-sourced snapshot the swap fires only on upstream
drift, so the trigger is a teammate pushing any commit to the shared
config repo rather than every dispatch. For a non-GitHub source it fires
on every reconcile.

### Issue 6: A dispatched session's handle is readable after the terminal closes

**Goal**: For Codex the terminal never attaches, so resume-later is the
only way the feature is used -- and the handle is printed once and stored
nowhere a developer is told to look.

**Acceptance Criteria**:
- `niwa list` prints the resume command for an instance backed by a
  dispatch mapping, built from the mapping's agent and that agent's
  declared binary and resume verb
- It reads the mapping store it already opens for keep-alive annotation
- A test fails if a dispatched instance lists with no way to reach its
  session

**Dependencies**: Issue 5
**Complexity**: testable

### Issue 7: `niwa dispatch`'s help text stops describing a Claude-only command

**Goal**: Five statements in `Short` and `Long` are factually wrong for a
Codex dispatch, and a reader who has just read the guide runs `--help` to
confirm and believes the binary.

**Acceptance Criteria**:
- `Short` says "background worker", not "background Claude Code worker"
- `Long` names the resolution ladder in one sentence
- The attach sentence is conditional on the agent handing over a mid-turn
  session, which Codex does not
- The hint sentence stops promising attach/logs/stop, which is Claude's
  verb set
- The orphan sentence says "the agent's own stop verb" rather than naming
  `claude stop` and `claude list`
- Replace rather than append; net line count roughly unchanged

**Dependencies**: Issue 1
**Complexity**: simple

### Issue 8: The guide's selection section is rewritten around the four rungs

**Goal**: The section is built on the premise that editing
`workspace.toml` is the only durable route, which is why it spends
twenty lines on the snapshot trap. Item 2 makes that a footnote.

**Acceptance Criteria**:
- The ladder appears once, reusing the help text's own wording rather
  than inventing a second phrasing
- `niwa config set default-agent` is the durable per-developer answer;
  `[workspace].default_agent` is kept as the workspace author's rung
- The snapshot trap shrinks to a sentence pointing at the command, with
  `workspace-config-sources.md` linked for the model
- The two sentences that become false when item 1 lands are corrected in
  the same pass
- The section is shorter than it was

**Dependencies**: Issue 1, Issue 2
**Complexity**: simple

### Issue 9: `README.md` and the guide index stop contradicting the binary

**Goal**: Two clauses.

**Acceptance Criteria**:
- The README command row names both agents and `--launch-agent` instead
  of "background Claude Code worker"
- The repo `CLAUDE.md` line for the Codex guide mentions agent selection
  and background dispatch
- No `niwa config` rows are added to the README command table; that table
  omits `config set global` today and the gap predates this work

**Dependencies**: Issue 1
**Complexity**: simple

### Issue 10: The unoriented-worker warning fires before the prompt, not after

**Goal**: The warning that a root-launched worker receives none of the
workspace's orientation, skills, MCP servers, or posture is correct and
arrives too late -- it prints at step 13, after the launch and after the
capture, when the worker has been running for half a minute.

**Acceptance Criteria**:
- The existing `Fprintf` moves to just after the agent resolves, ahead of
  the interactive prompt capture, so a developer reads it before writing
  the prompt rather than after spending it
- A test fails if the warning is emitted after the launch
- No new surface: this is a move of code that already exists

**Dependencies**: None
**Complexity**: simple

**Note**: the cost of getting this wrong is a wasted first dispatch that
is not cheaply undone. The developer writes a prompt assuming workspace
orientation the worker does not have, and stopping the result means
finding the process by hand -- Codex declares only a resume verb, no
stop, and `niwa destroy` removes the directory while a detached worker
keeps running.

## Open Questions

**Two defects the documentation work surfaced. Ownership is now settled;
neither is fixed.**

The first is the resolution lag: `niwa dispatch` resolves the agent
before it refreshes the config snapshot, so a `default_agent` pushed
upstream takes effect one dispatch late. Issue 2 routes around it rather
than fixing it -- the host config is not a snapshot, so there is nothing
to refresh and no lag -- and the guide's trap paragraph now ends by
pointing at that way out. The underlying ordering is untouched, because
changing when the refresh happens relative to resolution is a real
behavior change that deserves its own increment and its own reasoning.
It affects every workspace-level setting a command reads at startup, not
only this one.

The second is not this feature's at all. `[workspace]` in a config
overlay is inert, so a `default_agent` set there is dropped with no
warning -- but that is overlay-merge behavior, and `default_agent` is
just one field that happens to land in it. Fixing it for this field
alone would leave the next `[workspace]` field with the identical silent
drop. It belongs to whoever owns overlay merging, and the silence is the
defect rather than the inertness.

**Whether this plan should exist.** The process reviewer recommended
against writing it: `docs/plans/` held eight PLANs and every one was
deleted by its implementing commit, nothing since 2026-06 has used one,
and creating one converts the chain's status conventions into
gate-enforced obligations that currently bind to nothing, adding a
delete-before-ready step. It exists because it was asked for, and the
reviewer's objection is recorded here rather than argued away. It is
deleted before merge, which is the lifecycle the schema intends.

## Implementation Sequence

Items 1, 2, 5 have no dependencies and can run in parallel. Item 3
follows 1 and 2; item 4 follows 1; item 6 follows 5. The documentation
items 7, 8, 9 follow the surface they describe, because docs written
against an unlanded flag are docs that are wrong on the day they merge.

Everything folds into #261 rather than opening a fourth pull request.
There is no precedent in this repository for amended scoping artifacts
and their implementing code landing separately, and the amendments to the
BRIEF, PRD, and DESIGN are already on that branch.

## Before Ready

`wip/research/` is deleted and the tree grepped for `wip/` references
before the pull request leaves draft, per the workspace wip-hygiene rule.
No CI check enforces this here, so it is a manual step and it is written
down for that reason. This plan is deleted in the same pass, and the
BRIEF and PRD move to Done as they land -- the statuses they carry now
are the mid-PR ones the lifecycle gate requires while a PLAN roots the
chain.

The pull request's title and body have to satisfy the `pr-body` gate: a
Conventional Commits title from the fixed type list, exactly one bare
`---` separator, a non-empty Part 1 above it, and no ATX heading in that
part. `shirabe validate --pr-body <file> --pr-title <string>` checks it
offline before the body is posted.
