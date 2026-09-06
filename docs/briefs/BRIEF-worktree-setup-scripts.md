---
schema: brief/v1
status: Accepted
problem: |
  A niwa worktree never runs its repo's setup scripts, so it arrives with the
  repo's tracked source and none of its dependency tree while receiving every
  other thing a clone gets. Alongside it, a worktree hook registered under any
  event name but "apply" is discovered, indexed, and silently never run.
outcome: |
  Someone landing in a fresh worktree finds it provisioned the way the repo
  says it should be, and where the repo says a step must not run per-tree, it
  visibly does not. A lifecycle hook either runs or says why it will not.
motivating_context: |
  A session could not run a single test file in a fresh worktree and pushed the
  question to CI -- a twelve-minute round trip for something that takes eighteen
  seconds locally. A repo that uses niwa now documents the workaround in its
  README. Separately, a hook written under worktree-hooks/create/ was discovered,
  indexed, and never ran, caught only by a positive control that finished in
  zero seconds.
---

# BRIEF: Worktree Setup Scripts

## Status

Accepted

The framing is settled. Both Phase 4 jury reviewers returned PASS and two
reviewers standing in for the author approved the transition.

Two questions were deliberately left open and handed downstream rather than
answered here. The PRD owns which surfaces run setup and what a
half-provisioned worktree leaves behind. The DESIGN owns the insertion point,
the shape of a repo's opt-in -- whether it is one mechanism or genuinely two --
and where an unknown hook event name surfaces, given that a discovery error
today reaches the delegated create path's teardown.

## Problem Statement

`niwa apply` runs a repo's own `scripts/setup/` against the clone it
materializes. Nothing runs them against a worktree. So a worktree gets the
repo's tracked source, its CLAUDE content, its materialized environment, its
settings and hooks and rules import -- and none of its dependency tree. No
`node_modules`, no `.venv`, no generated client.

The cost lands on whoever is standing in the worktree. A session that wanted to
run one test file could not, and pushed the question to CI instead: a
twelve-minute round trip to answer something that takes eighteen seconds
locally. Worse, the failure does not name its cause. The symptom reported from
the field was a bundler error about a package's `exports` field, which reads as
a broken dependency and points nowhere near provisioning. A repo that uses niwa
has since written the workaround into its README, telling readers that in "a git
worktree (which niwa does not provision)" they have to run the setup scripts
themselves. A third-party README documenting a workaround is the clearest
statement that the gap is real.

The gap is undeclared rather than deliberate, and niwa's own design record is
what settles that. `DESIGN-worktree-command-parity.md` built `ApplyToWorktree`
specifically so a worktree would match a repo checkout. It enumerates the
instance pipeline it set out to mirror -- "repo materializers -> setup scripts
-> state" -- and concludes that a repo checkout "therefore emerges fully
formed." Its decision drivers say gaps "should be deliberate," and its verb
table marks exactly one gap as deliberate: `attach`/`detach`. Setup scripts
appear twice in that whole document: in the enumeration, and in a consequence
promising "no manual setup for agents launched there." That promise is the one
the README above falsifies. `DESIGN-niwa-default-worktree.md` Decision 9 is the
precedent for closing this kind of divergence -- it closed a strictly weaker,
*latent* clone/worktree difference on principle alone.

Running the scripts naively would be wrong, though, and that is the second half
of the problem rather than an objection to the first. Setup scripts have only
ever had one working directory, and scripts do path arithmetic upward against
it. The only first-party setup script in this workspace computes its target by
walking up two directories from the repo root: from a clone that lands on the
instance root, from a worktree it lands on the instance's internal directory,
so the script would write several megabytes where nothing reads it and exit 0.
The worked example in niwa's own setup-script design is a git-hooks installer,
and git hooks live in the shared common directory, so that one must not run
per-tree either. Meanwhile a JavaScript monorepo genuinely must, because
`node_modules` is structurally per-tree. One repo's `scripts/setup/` can hold
both kinds. And the contract that would let a script tell the difference is
unwritten: `scripts/setup` appears in exactly two files under `docs/`, both of
them design documents, so the convention is recorded where it was decided and
nowhere a user would look for it. The idempotency requirement exists only as a
decision-driver bullet in one of those two.

The same failure class shows up one layer over, in the mechanism the field
workaround actually used. Worktree hooks are discovered from the config repo by
reading event names straight off the filesystem, with no validation against any
known set, and only the `apply` event is ever consumed. So a hook registered
under `worktree-hooks/create/` is discovered, indexed, and never run, and so is
a hook in a file with a typo in its name. Nothing reports either. The worktree
guide states that these scripts "run on every `create` and `apply`", which reads
as though a `create/` directory works -- and a real workspace wrote one there
first for exactly that reason, then found it only through a positive control
that completed in zero seconds.

Both halves are the same problem stated at two altitudes: niwa reports a
successful provisioning that did not happen. They are also mechanically
coupled. What counts as a valid worktree lifecycle event decides whether setup
can run at a different moment than the existing `apply` event, and settling
either one alone produces an answer the other has to undo.

## User Outcome

Someone who lands in a fresh worktree -- a developer who just ran `niwa
worktree create`, or an agent that was handed one -- finds it provisioned the
way the repo says it should be. The test suite runs. The dev server starts. The
question that sent them to CI gets answered where they are standing.

Where a repo says a step must not run per-tree, that step visibly does not run,
rather than running and quietly writing to the wrong place. A maintainer can
tell which of their setup steps are per-tree work and which are shared-state
work, because the working directory a script gets and what it may assume about
it are written down somewhere a script author can read.

And a lifecycle hook either runs or says why it will not. A hook registered
under a name niwa does not consume stops being a silent no-op: the operator
learns about it from niwa rather than from a positive control they thought to
build. That diagnostic does not come at the cost of the worktree -- a
misconfigured config repo never destroys work an agent has in flight.

## User Journeys

### An agent runs one test in a worktree it was handed

An agent is launched into a delegated worktree of a JavaScript repo -- the
worktree was created by the hook path, so the agent never ran a command to make
it. It edits a component and runs the single test file covering it. Today that
fails with a bundler error naming a package's `exports` field, the agent cannot
tell provisioning from a broken dependency, and it either pushes to CI or
guesses. After this feature, the dependency tree is there and the test runs in
seconds, or -- if provisioning genuinely failed -- the agent is told that
plainly, in the worktree it is still standing in.

### An operator adds a setup step to a repo with live worktrees

A maintainer adds a code-generation step to a repo's `scripts/setup/`, commits
it, and runs `niwa apply` from the instance root. Three worktrees of that repo
are checked out and in use. The operator's expectation is the one niwa already
sets everywhere else: apply is the idempotent verb that brings everything to
the configured state. They should not have to know which of their checkouts
were born before the script existed, and they should not have to visit each
worktree by hand.

### A config-repo author registers a hook under an event name

An author writing workspace configuration adds
`worktree-hooks/create/01-bootstrap.sh`, because the guide says these scripts
run on every create and apply, and because `create` is when they want it to
run. Today the script is discovered, recorded, and never executed, and nothing
anywhere says so. After this feature, the author learns the event name is not
one niwa consumes -- at a moment and in a form that does not take a live
agent's worktree down with it.

### A maintainer opts in a repo whose setup does two different jobs

A maintainer has a `scripts/setup/` holding a git-hooks installer and a
dependency install. One writes to state every worktree already shares; the
other must run per-tree. They need to turn this on for their repo without the
first script silently doing the wrong thing in every worktree, and they need to
be able to read what a script is allowed to assume about where it is running.
The outcome they reach is a repo where the per-tree step runs per-tree, the
shared step does not, and both facts are legible from the repo rather than
inferred from behavior.

## Scope Boundary

### In

- Repo-provided setup scripts reaching worktrees, on some set of the surfaces
  that install worktree content.
- The switch that turns this on for a repo, and where it lives in
  configuration, given that the existing setting that governs the clone run
  cannot be reused to mean two things.
- What a setup script may assume about its working directory and about being
  re-run, published as a contract rather than left implicit in a design
  document.
- What a worktree hook's event name is checked against, and what happens when
  it is not a name niwa consumes.
- The failure posture of a half-provisioned worktree, including the delegated
  create path that tears a worktree down when content installation fails.
- Narrowing the worktree guide's sentence promising that create and apply need
  "no network access", which a dependency install breaks in letter. The
  promise is really about niwa not resolving secrets; the same nominal breach
  already exists on the clone path.

### Out

- Any change to how setup scripts run against a clone. That path needs none:
  the runner never touches git and is already parameterized on the directory
  it runs in.
- The shell wrapper's directory handoff after `niwa worktree create`, which is
  broken for an unrelated reason and separately owned.
- Codex directory trust, which is the other cell missing from the
  clone/worktree parity table. Separate owner.
- A general per-event hook vocabulary for niwa as a whole. This feature settles
  what a *worktree* lifecycle event is; the instance-level hook surface is not
  in question.
- Making setup failures fail the command. The exit code stays what it is, for
  reasons the setup-script design already settled on the clone path.

## References

- `docs/designs/current/DESIGN-worktree-command-parity.md` -- created
  `ApplyToWorktree` and is the design this gap belongs to.
- `docs/designs/current/DESIGN-post-clone-scripts.md` -- the setup-script
  design, its idempotency driver, and its settled exit-code argument.
- `docs/designs/current/DESIGN-niwa-default-worktree.md` -- Decision 9, the
  precedent for closing a clone/worktree divergence.
- `docs/guides/worktree.md` -- documents worktree hooks and carries the
  network-access sentence that needs narrowing.
