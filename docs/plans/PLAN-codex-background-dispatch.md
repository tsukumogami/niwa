---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-codex-background-dispatch.md
milestone: "Codex background dispatch: --detach decides the process model"
issue_count: 4
---

# PLAN: --detach decides the process model

## Status

Active

## Scope Summary

`niwa dispatch` currently decides how to start a worker from the agent's
declaration alone. `realDispatchLaunch` switches on `spec.Mode` and
nothing else, and `--detach` is wired to a separate question -- whether
an attach step runs afterwards. For Claude that is invisible, because
`claude --bg` backgrounds its own session and there is no foreground
alternative. For Codex it produces a command that ignores its own flag:
the worker is detached whether or not the developer asked, and dispatch
then explains that Codex will not hand over a session whose turn is
still running.

This plan makes `--detach` select the process model. Without it, an
agent whose runner executes the turn in the foreground runs it in the
developer's terminal and they watch the work. With it, that agent's
worker is detached exactly as today. Agents that background their own
session are unaffected.

The scope is deliberately narrow: it changes when and how a worker is
started, and nothing about capture, resume, liveness, reclamation, or
agent selection. It lands inside #261, on the branch that already
carries the feature.

## Decomposition Strategy

**By the seam, then by what the seam changes.** Issue 1 moves the
decision so it can see the invocation. Issue 2 is the foreground runner
itself and the three measured properties it must not drop. Issue 3 is
the argv difference the foreground path implies. Issue 4 is the
documentation and the messaging that stops being true the moment this
lands.

## Issue Outlines

### Issue 1: The launch mode is resolved from the agent and the invocation

**Goal**: `realDispatchLaunch` currently reads `spec.Mode` and cannot see
`--detach`. Give the decision both inputs, so an agent that offers two
process models can be started either way and an agent that offers one is
unaffected.

**Acceptance Criteria**:
- The mode reaching the launcher is a function of the agent's declared
  capability and the invocation's `--detach`, not of the declaration
  alone
- An agent whose runner backgrounds its own session starts identically
  with and without `--detach`; only the attach step differs, as today
- A test fails if the launch mode is derived from the declaration alone
  -- this is the defect that shipped, and the test is what stops it
  coming back
- The declaration still says what the agent's runner *is*; the
  invocation says which of its available modes to use. A declaration
  that offers one mode cannot be overridden into the other

**Dependencies**: None
**Complexity**: testable

### Issue 2: The foreground runner, with the three measured properties intact

**Goal**: Run the worker in the developer's terminal when they did not
ask to detach, without losing what Decision 8 measured.

**Acceptance Criteria**:
- Without `--detach`, a foreground-runner agent's turn runs to completion
  in the caller's terminal, its output visible as it happens, and
  dispatch does not return until the turn ends
- **Stdin is `/dev/null` even here.** The measured hang is on stdin
  specifically; a test fails if the foreground path inherits it
- The prompt remains one argv element
- Exit status is still not read as task success; what the run reports at
  the end is that the turn ended
- Ctrl-C reaches a foreground worker and does not reach a detached one.
  Both directions are asserted, because the second is what `Setsid`
  buys and the first is what a developer expects of a command running in
  front of them
- The session mapping is still written -- capture reads the record on
  disk, which appears about 0.7 seconds in, so it does not wait for the
  turn

**Dependencies**: Issue 1
**Complexity**: complex

### Issue 3: The machine-readable stream flag belongs to the detached path

**Goal**: `--json` exists so niwa can parse a log nobody is watching. In
the foreground the developer is the reader.

**Acceptance Criteria**:
- The detached argv is unchanged, `--json` included
- The foreground argv omits it, so the developer sees the human output
  rather than an event stream
- Capture works in both, because the session id comes from the rollout
  record rather than from stdout -- a test covers the foreground case
  explicitly rather than assuming it follows
- The rest of the argv is identical between the two paths, including
  `--skip-git-repo-check`, the workdir trust grant, the `--` separator,
  and the continued absence of `--ephemeral`

**Dependencies**: Issue 1
**Complexity**: testable

### Issue 4: The help, the guide, and the message that stops being true

**Goal**: Three surfaces currently describe the behavior this plan
changes, and one of them is an apology that should disappear rather than
be reworded.

**Acceptance Criteria**:
- The "will not open a session while its turn is still running" message
  no longer fires on a plain `niwa dispatch`, because there is nothing to
  apologize for -- the developer is watching the turn. It remains correct
  and reachable for an agent that cannot hand over a running session
  *and* was asked to detach
- `niwa dispatch --help` describes `--detach` as choosing how the worker
  runs, not only whether a step follows
- The Codex guide's background-dispatch section says what a plain
  dispatch does now, and keeps the resume path for the detached case
- No agent name appears in any of the three: the dispatch-path scan
  still passes

**Dependencies**: Issue 1, Issue 2, Issue 3
**Complexity**: simple

## Implementation Sequence

Issue 1 first, because 2 and 3 both need the decision to see the flag.
Issues 2 and 3 are independent of each other once it does. Issue 4 last,
because documentation written against an unlanded behavior is
documentation that is wrong on the day it merges -- the same rule the
previous round arrived at.

Everything lands on `feat/codex-background-dispatch`, inside #261.

## Open Questions

**Whether a foreground dispatch should still write the instance log
files.** Today `.niwa/dispatch-<binary>.out` and `.err` exist because the
detached worker's output has nowhere else to go. In the foreground the
terminal has it, and a developer who scrolls away has lost it. Writing
both -- terminal and file -- means teeing, which is more machinery than
this plan otherwise needs. Left to the implementer with a bias toward not
teeing, since scrollback is the normal expectation for a foreground
command and the detached path exists for when it is not.

## Before Ready

This plan is deleted and the chain re-finalized -- BRIEF and PRD to Done,
DESIGN staying Current -- before #261 leaves draft again. Creating it
re-roots the chain, so the lifecycle gate returns to mid-PR posture and
#261 goes back to draft until the work lands. That is the honest posture:
the feature is being amended, and a PR that presents as mergeable while
its own plan is open would be claiming otherwise.
