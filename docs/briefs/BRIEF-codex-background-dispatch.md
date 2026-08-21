---
schema: brief/v1
status: Done
problem: |
  niwa dispatch refuses any workspace whose resolved agent is not Claude,
  and row 22 of the capability table declares that refusal as niwa's own
  debt. An instance is prepared for both agents today; the door into a
  background session is still Claude-only, and the reclamation sweep would
  destroy a non-Claude worker's instance even if one could be launched.
outcome: |
  A developer in a codex-default workspace runs niwa dispatch, a Codex
  worker starts in a fresh instance, does something small and real, and the
  developer steps back into that session later. Same command, same session
  mapping, same management surface as a Claude dispatch, and every gap that
  remains is declared where the existing gap list already lives.
motivating_context: |
  The capability contract made workspace preparation serve both agents and
  put the dispatch refusal on the generated gap list as not-built, out of
  its own scope. This feature closes that row. The prior dual-agent attempt
  failed by shipping agent-specific behavior as a parallel hardcoded pass;
  dispatch is where that failure mode is most likely to recur, because
  Claude's session bookkeeping is sitting right next to every seam.
---

# BRIEF: codex background dispatch

## Status

Done

This brief frames the delivery of `niwa dispatch` for Codex: the framing,
the scope boundary, and the sequencing. The downstream PRD owns the
numbered requirements; the design owns the per-agent launch description,
how a launch-routed capability binds, and the mechanics of each enforcing
test.

It describes the tree as it stood when the work was framed, and it is not
maintained as the branches land -- the present tense here is the state
the framing argues against, not the state after it. The design and the
code are the current record.

## Problem Statement

The code states the problem in two places. `internal/cli/dispatch.go`
refuses, at its step (2b), any workspace whose resolved agent is not
Claude: "niwa dispatch launches a Claude worker; this workspace's agent is
%q, which background dispatch does not support yet." And row 22 of the
capability table in `internal/agentplan/declaration.go` declares that
refusal as niwa's own debt -- `DispatchLaunch` for Codex is unavailable
with reason not-built: "Nothing in niwa knows how to start a Codex
worker, recover which session it became, or step back into one." The
generated gap list in `docs/guides/codex-agent.md` publishes the same
sentence under "What niwa hasn't built yet." An earlier form of the
reason cited the per-agent model table's Codex entries as half-built
groundwork; the launch route's binding check identified those entries as
a delivery no declaration stood behind -- nothing read them, because the
refusal fired first -- and they are gone.

So a workspace prepared for Codex -- context composed, skills delivered,
MCP servers and environment in place, the directory trusted -- still can't
send work to a background Codex session. The developer's whole loop is cut
at the last step: they can open an interactive Codex session in a prepared
instance, but `niwa dispatch`, the command that hands a task to an isolated
worker and lets you step away, turns them away with an instruction to
switch agents.

There's a second, quieter half. Even if the refusal were lifted naively,
the dispatched worker would not survive its siblings. The reclamation sweep
runs at the top of every dispatch and create, and it decides whether a
dispatched session is still alive by looking in Claude's jobs directory. A
Codex dispatch would write a valid session mapping and never write a Claude
job entry, so the very next `niwa dispatch` would judge the first worker
dead and destroy its instance while it was still working in it. Launch,
capture, and resume were the surfaces the work arrived describing; liveness
is the one the code added.

## User Outcome

A developer whose workspace defaults to Codex dispatches a background
worker the way a Claude-default workspace always could: `niwa dispatch
"<task>"`, a fresh instance, a worker that starts and does the work, a
printed session id, and a way back into the session afterward. `niwa list`
shows the session; finishing or resuming it behaves the same regardless of
which agent is behind it. The agent is whichever one the workspace already
resolves, so a developer who has set one names nothing on the command
line.

*Amended in the second round.* This paragraph originally ended "No new
flag appears", and that was the position the work started from: the agent
resolves from the environment variable and `default_agent`, so no new
surface was needed. It shipped the opposite -- `--harness`, and `niwa
config set default-dispatch-harness` behind it -- because resolution
existing turned out not to be the same as selection being reachable. The
sentence is corrected here rather than left standing under the
not-maintained note, which covers descriptions of the tree at authoring
time and not an outcome that is now the reverse of what was delivered. PRD
R1 carries the full reversal and preserves the original requirement
underneath it.

Where a Codex dispatch genuinely differs -- keep-alive doesn't exist for
it, and a Codex-dispatched instance isn't reclaimed automatically the way a
Claude one is -- the developer learns it from the same generated gap list
and guide that already carry Codex's other gaps, not by experiment.

Two judgments govern what counts as done, and they're worth stating as
bluntly as the requirements they'll shape. First: a feature that lands in
reduced mode with its gap declared is a success, and one that lands with an
undeclared gap is a failure regardless of how much it delivers. The
declaration table exists so that "what does a Codex session get" has one
honest answer; a quiet regression in that answer costs more than a missing
capability. Second: this feature is cheap only if the agent-specific
knowledge concentrates at seams. The moment the implementation starts
spreading `if agent == ...` branches across dispatch, capture, resume, and
reap, the cost structure has inverted, and that's a reason to stop and
escalate rather than absorb -- a spread-out version of this feature is
worse than no version, because it's the exact shape the capability contract
was built to prevent.

## User Journeys

### Deciding that this workspace's workers are Codex

A developer has a workspace prepared, as every workspace is, for both
agents. They want its background workers to be Codex. They find out that
this is a choice they get to make, and where to make it, without reading
source: the command that launches workers says so, the guide says so, and
the setting has a documented home that survives the next `niwa apply`.
Having made it, they can tell which agent a dispatch will launch before
they run one. Nothing they do here changes what a workspace is prepared
with -- both agents stay ready, and the choice is only about who gets
launched.

This journey is the one the first pass through this brief left out, and
leaving it out is what made the rest of the feature unreachable. A
capability whose entry point is an undocumented environment variable is
not delivered; it is present.

### Dispatching from a workspace whose workers are Codex

A developer's workspace sets `default_agent = "codex"`. They run
`niwa dispatch "fix the flaky retry test" --detach` from the workspace
root. niwa checks that the `codex` binary is on PATH before creating
anything, provisions a fresh instance, launches a Codex worker in it,
captures the worker's session id, writes the durable session mapping, and
prints the id with resume hints. The developer comes back an hour later and
steps into the session to see what happened. At no point did they name an
agent on the command line.

### Watching the work, when that is what you asked for

A developer runs `niwa dispatch "<task>"` without `--detach`, which is the
way you say "I want to be in this". The worker's turn runs in their
terminal and they watch it happen -- the same thing `--detach` exists to
opt out of. When it finishes they are back at the shell with the session
id and the command to continue the conversation.

The first round did not deliver this for Codex and delivered something
that reads like a bug instead: the worker was detached whatever the
developer asked for, and dispatch then explained that it could not put
them into the session. Both halves were true. Together they describe a
command that ignores its own flag and then apologizes. `--detach` should
decide how the worker is run, not merely whether a step happens after it
is already too late.

### The second dispatch doesn't eat the first

The same developer dispatches a second task while the first Codex worker is
still running. The reclamation sweep at the top of the second dispatch asks
whether the first session is still alive using the rule that fits its
agent, finds that it is, and leaves the instance alone. When the session is
genuinely gone, the mapping path reclaims a Claude instance as it always
has; for Codex, the sweep declines to guess, and the guide says so.

### A gap is discovered by reading, not by surprise

A developer wonders why a finished Codex dispatch's instance is still on
disk days later. The guide's Codex section answers it: Codex session
records are never aged out, so niwa doesn't infer death from their absence,
and a Codex-dispatched instance is reclaimed when the session is deleted or
the instance destroyed, not automatically. The answer was generated from
the declarations, and it names the backstop that closes the gap as future
work rather than pretending the gap isn't there.

## Scope Boundary

**In:**

- The four surfaces of a background session's lifecycle, for Codex:
  **launch** (starting the worker in a prepared instance), **capture**
  (learning the session's identity), **resume** (stepping back into it),
  and **liveness** (answering whether it still exists, so reclamation
  never destroys a live worker's instance).
- The dispatch gate becoming a declaration lookup on row 22, so the
  refusal, the table, and the guide can't drift apart -- and the row
  flipping to implemented in the change that delivers the launch.
- One session-identity representation for both agents, with the mapping
  recording which agent it belongs to.
- The structural property that the dispatch path names no agent constants
  at call sites, held by a test that is demonstrably capable of failing.
- The regenerated gap list plus the hand-written guide prose around it,
  edited together.
- Declaring the reclamation gap: a Codex-dispatched instance is safe from
  the reaper and consequently not auto-reclaimed. Declared, published, and
  its closure named as the next feature's work.
- **Selecting which agent a workspace's background workers are, as a
  surface a developer can find, set, and verify.** Added in the second
  round of this chain. The first round put this out of scope on the
  argument that the environment variable and `default_agent` already
  resolved the agent, so no new user-facing surface was needed. That was
  wrong in a way that only shows up from outside: resolution existing is
  not the same as selection being reachable. What lands here is the entry
  point rather than a new resolution rule -- the precedence order is unchanged, and
  nothing here alters what `niwa apply` prepares.
- **`--detach` deciding the process model rather than a later step.**
  Third round. Without it, an agent whose runner is a foreground process
  runs in the developer's terminal and they watch the turn; with it, the
  worker is detached as today. What made this necessary is that the
  process model was a property of the *agent* and the flag was a property
  of the *invocation*, so the two could not meet: Codex was detached
  unconditionally and then found to be un-attachable, which is a problem
  this feature created rather than one it inherited from Codex.
- **The documentation of that surface, and of the costs this feature
  declares.** Also second-round. A cost declared only in a design document
  is not declared to the person who pays it, and a setting whose only
  written home is a commented-out line in a generated file is not
  documented. What the reviewers judge necessary is what this covers; the
  PRD carries the list.

**Out (each item named as deferred, not absorbed):**

- `niwa session attach` / `niwa worktree attach`, `internal/cli/sessionattach/`,
  and `internal/worktree/`. Dispatch resume lives on the dispatch path's
  own attach step; touching the worktree-session surfaces is the signal a
  boundary has been crossed. The inert state of that command is a real
  defect belonging to whoever owns it.
- The review-continuation path in `internal/cli/watch.go`. It belongs to
  watch.
- Ephemeral-session provisioning (row 17) and dispatch keep-alive
  (row 21). Both stay declared unavailable for Codex; no partial bridge.
- Automatically switching agents on quota exhaustion. Detecting the
  condition is in scope only so errors aren't misclassified; acting on it
  is a policy decision nobody has made.
- Any route that installs a niwa-owned hook for Codex. Whether niwa would
  ever ship a hook-trust bypass is a policy call, not this feature's.
- Slash-command and extension-tree distribution for Codex. Whether Codex
  surfaces plugin command trees at all is unmeasured, so it's an open
  question upstream of this work.
- Building the Codex liveness backstop (a name-plus-TTL-plus-mtime rule
  for aging out unreclaimed instances). This feature declares the gap and
  avoids data loss; closing the gap is the next feature.

## Sequencing

Two pull requests, in order, and the split is not optional.

**PR 1** puts the agent seam through the dispatch path against Claude
only: the gate becomes a declaration lookup, the session-identity
representation lands, capture is generalized to a pluggable candidate
source, and the structural scan over the dispatch path goes from red to
green. Its behavior claim is exact rather than flat: four user-visible
changes, three incidental to routing the gate through the declaration and
one a fix, and nothing else. The refusal now renders the declaration's
own reason and enumerates the launchable agents from the table, since a
hardcoded hint would go stale silently the day a row flips. The gate now
runs even when the workspace config cannot be loaded -- previously an
unreadable config skipped the check and launched Claude anyway, so this
one is a fix riding along, named rather than found. The `--model` help
names the portable categories instead of one agent's concrete model
names, which would start lying the day a second agent shipped. And the
preflight error names the missing binary rather than a product.
Everything else is provably unchanged -- and a claim with a named
exception list is provable where a flat claim is simply false. That
matters beyond this PR: the documents behind a squash-merged PR become
what someone bisecting a behavior change reads, and an overstated claim
there is the prior attempt's failure in a smaller frame.

**PR 2** delivers Codex as the second implementation of the seam: launch,
capture, resume, and liveness, with row 22 flipped and the guide
regenerated. Its correctness argument is "the new thing works," which is
only reviewable when the refactor underneath it is already trusted.

Folding them together produces a diff where neither argument can be made,
which is how the prior attempt became unreviewable. If PR 2 grows beyond
one reviewable change, it splits by surface -- launch, then capture and
liveness, then resume -- never into "the plumbing" and "the behavior."

**This commitment was not kept, and saying so here is the point of having
made it.** PR 2 grew to 27 commits across five increments -- the Codex
delivery, the selection surface, three defects review found, the
process-model amendment, and a folded-in spike -- and did not split. It
was reviewed at ten commits and roughly 1,400 added lines; it is several
times that now. The direct cause was an instruction to collapse the stack
so one merge would finish the work, which is a legitimate call and not
one this brief anticipated. The cost is the one this paragraph predicted:
a reviewer who read the first version has to re-enter a much larger diff,
and the argument for each increment is harder to make in isolation than
it would have been in its own pull request.

What limited the damage was not the split but the review that replaced
it: the delta is grouped by concern rather than chronologically, and each
group names what it is weakest at. That is a worse instrument than a
split and better than nothing, and it is worth writing down which one
actually happened.

## Open Questions

- **Where the instance-root delivery gap lands.** A dispatched Codex
  worker starts at the instance root, and a session there receives none
  of what rows 5, 8, 9, and 12 declare implemented -- skills, MCP
  servers, environment, and posture are all delivered inside
  repositories -- and no orientation either. niwa never launched a Codex
  session at an instance root before, so this feature introduces the gap
  rather than inheriting it, and it is measured to be worse than
  "unoriented": Codex fixes discovery at session construction, keyed to
  the launch directory; it follows neither the working directory nor an
  instruction, so a worker told in its prompt to work in a particular
  repository still gets nothing from that repository. The files stay
  readable on request, which is a categorically weaker delivery than the
  instruction being in the worker's context. The gap is not yet declared
  anywhere -- by this brief's own first judgment, the failure mode -- and
  the contract cannot currently express it: declarations are per
  capability and agent, two states, scoped by who receives and never by
  where from, and this gap runs along the second axis. That is a limit
  the work hit, not a caveat to bury in a reason string, and no new
  capability row is invented for it. Where the landing goes is being
  decided above this feature: closing the gap means correcting row 2's
  stated reason -- which is factually wrong, since a session's working
  directory always contributes its context file (measured with and
  without a marker-bearing ancestor), so an `AGENTS.md` at an instance
  root would be read -- and writing Codex-shaped content at the instance
  root, both of which touch already-shipped declarations.
- **The shape of the per-agent launch description.** What it carries --
  command construction, launch mode, capture source, liveness probe, resume
  command -- and where it lives relative to the declaration table. The
  design owns this.
- **How a launch-routed capability binds in both drift directions.** The
  existing binding mechanisms serve plan- and procedure-routed
  capabilities; a launch has nothing registered in the places they check.
  The bar is that deleting the delivery must fail the declaration. The
  design owns the mechanism.
- **The resume handle's representation.** Claude's management commands key
  on a short id distinct from the session UUID; Codex sessions have only
  the UUID. The identity representation has to carry a handle whose meaning
  is the agent's business, and the PRD owns stating that requirement
  precisely.

None of these block the brief; each defers a requirements- or design-level
determination downstream.

## References

- docs/prds/PRD-agent-capability-contract.md -- the upstream contract
  work: row 22 of its capability matrix is this feature's subject, and its
  out-of-scope list names building Codex dispatch as future work the
  declarations make visible.
- docs/designs/current/DESIGN-agent-capability-contract.md -- Decision 3's
  `RouteLaunch` bullet states the shape the gate takes: a declaration
  lookup, so the gap list and the refusal can't drift apart.
- docs/guides/codex-agent.md -- the published guide whose generated gap
  list currently carries the dispatch refusal and changes when row 22
  flips.
- internal/agentplan/declaration.go and internal/agentplan/capability.go
  -- the declaration table and the closed capability set.
- internal/cli/dispatch.go -- the refusal at step (2b) and the
  launch-capture-mapping-attach flow this feature generalizes.
- test/functional/features/codex-agent.feature -- the scenario pinning the
  refusal as the declared gap, which this feature extends to assert the
  declared delivery.
