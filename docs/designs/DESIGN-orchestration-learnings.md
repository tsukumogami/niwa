---
status: Proposed
problem: |
  niwa can launch a background worker but teaches nothing about running several. The
  decisions that determine whether a fleet finishes unattended -- how much framing the
  work needs before someone starts implementing, and whether to hand it off autonomously
  or settle the framing interactively first -- are made on intuition and never recorded.
  When a worker stalls or goes quiet there is no documented way to reach it, and the
  obvious recipe silently fails. Work can sit finished-but-uncommitted beside a green pull
  request, invisible to every query a coordinator would think to run.
decision: |
  Ship the coordinator's operating knowledge in the paths that are actually loaded. Extend
  the existing `/dispatch` root skill with the two before-launch decisions, add a `/fleet`
  root skill for the after-launch loop, ship a canonical standing agreement into
  `.niwa/dispatch-briefs/_common.md` as a niwa-owned sentinel block that a workspace can
  extend, and publish the harness mechanics as a contributor guide where every claim
  carries the command that verifies it. Extend the root materializer to copy whole skill
  directories, and add `niwa create` as a materialization call site so a dispatch always
  refreshes what the worker will read.
rationale: |
  A `docs/guides/` file is unreachable from a workspace that uses niwa -- niwa is a binary
  and most workspaces never clone its source -- so anything meant to change behavior has to
  be embedded in the binary and written out. That splits the work by audience rather than
  by kind: re-verifiable evidence belongs in the guide where it can be re-checked and is
  expected to age, while the operational recipe has to ship. The sentinel merge is chosen
  over write-if-absent because niwa must be able to ship a correction; the previously
  circulated resume recipe was wrong, and a write-once file would have frozen it forever.
---

# DESIGN: Coordinating a fleet of background workers

## Status

Proposed

## Context and Problem Statement

`niwa dispatch` launches one background worker in its own instance, and the shipped
`/dispatch` skill explains how to write a brief for it. That is the whole of what niwa
teaches about delegated work. It says nothing about running ten workers at once, which is
what people actually do once dispatch works.

The gap is not documentation for its own sake. Four things go wrong in practice, and all
four are decisions or checks that niwa is positioned to carry:

**The framing decision is invisible.** Before dispatching, someone decides how much
investigation the work needs first. Get it wrong toward too little and the worker produces
a change that implements the first plausible mechanism, which someone has to catch in
review. Get it wrong toward too much and a two-file fix burns a full investigation cycle
while the author waits. This gets decided over and over and the reasoning is never written
down, so it can't improve.

**The launch-mode decision is invisible too, and it's the expensive one.** Some work should
be handed off fully autonomous, because what "done" looks like is unambiguous and the
author's absence costs nothing. Some should settle its framing interactively first, because
the framing is exactly what's uncertain — and an autonomous agent will confidently resolve
a question the author would have answered differently, which surfaces only when the pull
request arrives.

**A worker that goes quiet is hard to reach, and the obvious approach fails silently.**
Resuming a background session has several traps that each look like success. The recipe
that seems right returns "No conversation found." Wrapping the resume in a timeout kills a
turn partway through, after some of its work has already landed. A session killed that way
keeps a stale registration, so the retry is refused in about three seconds — and a caller
that checks only whether the process launched reports the session as restarted.

**Finished work can be invisible.** A worker can have the right change on disk,
uncommitted, beside a pull request whose checks are green. Pull request state, check runs,
review status and commit history all show a complete, mergeable change. Nothing that
queries the forge catches it. Only looking at the filesystem does.

These are not hypothetical. They come from a documented batch of 16 dispatches across three
days in one repository, plus a probe run against the CLI. The evidence base is small and
its limits are stated where it matters; see Decision Drivers.

## Decision Drivers

**Reachability decides where content can live.** niwa is a binary. A workspace clones
whatever repos its configuration names, and most workspaces that dispatch workers will
never have niwa's source tree on disk. Only content niwa embeds and writes out reaches
every workspace. Anything meant to change behavior at the moment it matters has to ship;
anything meant to be re-checked and corrected over time belongs in the repository.

**Every mechanical claim must carry the command that proves it.** The claims here are about
a CLI that changes. A previously circulated set of resume findings was careful, well
evidenced, and recommended a recipe that did not work — the failure was not sloppiness, it
was that a reader had no way to re-check. Findings that have gone stale get corrected, not
preserved.

**The audience is any niwa user.** Not one workspace's practice. That rules out naming
another tool's skills as doctrine, and it rules out in-house issue numbers as load-bearing
content. Provenance can be cited once; a rule has to stand on its own.

**Confidence gets stated, not implied.** The framing-level criteria come from 16 dispatches
in one repository over three days, and the shape chosen is almost perfectly confounded with
the date — every full-investigation dispatch is from the first two days, every third-day
dispatch is the light shape. Three criteria survive that scrutiny. Two intuitive ones are
contradicted by the data and are recorded as contradicted rather than quietly dropped.

**Two readers, two load points.** The coordinator reads the skills. The worker reads the
standing agreement, as the first tool call of its task, because every brief points at it.
Collapsing them into one artifact loses a load point: the coordinator never reads the
agreement and the worker never reads the dispatch skill.

**Don't over-produce.** The material invites a sprawl of skills and documents that nobody
loads. Every artifact here has to answer: who reads this, and when.

## Considered Options

### How the standing agreement reaches a workspace

The agreement is the part a worker reads. Today it's hand-written, per machine, and
nothing seeds or updates it.

- **Sentinel-section merge (chosen).** niwa owns a delimited block and rewrites it on every
  root materialization; content outside the sentinel survives. Copies
  `installWorktreeContextLayer` / `stripWorktreeContextSection` in
  `internal/workspace/worktree_content.go`, which already do strip-and-reappend against a
  stable heading and are tested.
- **Write if absent.** Simplest, and rejected: it freezes each workspace at whatever
  version it first saw. niwa could never ship a correction — which is precisely the failure
  this design exists to prevent.
- **Overwrite plus a `_common.local.md` companion.** Less code and it matches the `.local`
  convention elsewhere. Rejected as primary because it pushes composition onto the reader,
  and a workspace that edits the base file loses the edit with no warning.
- **Distribute through `[root.files]`.** Zero new Go. Rejected because the file would then
  be workspace-authored rather than niwa-shipped, which doesn't satisfy the requirement,
  and it targets a directory the file-distribution guide excludes.

The snapshot-preservation guarantee for `.niwa/dispatch-briefs/` is not an obstacle.
`preserveDispatchBriefs` defends the config-snapshot swap, and `niwa apply` runs that swap
before root materialization, so a file niwa writes there survives every later swap.

### How a root skill carries more than one file

`writeRootSkills` picks up only `rootskills/<name>/SKILL.md`.

- **Copy the whole skill directory (chosen).** The walk stops filtering on the basename and
  preserves relative paths, so a skill can ship `SKILL.md` plus references it loads on
  demand.
- **Keep one file per skill and write tighter.** Rejected: compressing the harness recipe
  and the review standard is how a rule loses the specific, checkable form that makes it
  usable.
- **Push detail into `docs/guides/` and keep skills thin.** Rejected on reachability.

### Skill boundaries

- **Two skills (chosen).** `/dispatch` extended with the before-launch decision; a new
  `/fleet` for the after-launch loop. The deciding property is trigger moment. Skill
  descriptions gate loading, and a skill called `dispatch` doesn't load when someone asks
  what their workers are doing — a question that came up four separate times in the batch
  under study.
- **One skill.** Smaller shipped surface, and the strongest argument against sprawl.
  Rejected because it puts the stranded-work sweep and the wake-or-fix decision behind a
  trigger that fires before either is relevant.
- **Three skills, with the review standard separate.** Rejected: reviewing what came back
  is the same loop as everything else in `/fleet`, so it adds a trigger without adding a
  moment. With whole-directory skills available it can be a reference file instead.

### How chain shape is expressed

- **Tool-neutral framing levels (chosen).** niwa owns the decision — how much framing the
  work needs before implementation — with a short note mapping the levels onto a workflow
  plugin's skills for workspaces that use one.
- **Name a specific plugin's skills as doctrine.** Sharper and closer to what happened.
  Rejected: it makes niwa's documentation depend on a tool most niwa users don't have.

### The materialization coverage gap

`MaterializeWorkspaceRoot` runs at root-scope `niwa apply` and on `niwa init` in named and
clone modes. `niwa create` and `niwa dispatch` don't refresh the root.

- **Add `niwa create` as a call site (chosen).** `niwa dispatch` goes through `Create`, so
  this is the moment the content is about to matter. Without it, the ship-a-correction
  property doesn't hold for the workspaces most likely to need it.
- **Document the constraint.** Rejected: it leaves the central benefit conditional on a
  command the author may never run.
- **File as a follow-up.** Rejected for the same reason — it's load-bearing here, not
  adjacent.

## Decision Outcome

Four artifacts, split by who reads them and when.

| Artifact | Reader | Read when | Ships how |
|---|---|---|---|
| `/dispatch` skill, extended | coordinator | the author asks to hand work off | embedded, written to `<workspaceRoot>/.claude/skills/dispatch/` |
| `/fleet` skill, new | coordinator | the author asks what workers are doing, what's next, or to review what came back | embedded, written to `<workspaceRoot>/.claude/skills/fleet/` |
| `_common.md` standing agreement | **worker** | first tool call of every dispatched task | embedded, merged into `<workspaceRoot>/.niwa/dispatch-briefs/_common.md` |
| `docs/guides/background-session-control.md` | niwa contributor, or anyone re-checking a claim | working on niwa, or following a pointer from `/fleet` | in the repository |

`/dispatch` gains the two before-launch decisions. **Launch mode**: hand off autonomously
when what "done" looks like is unambiguous; settle the framing interactively first when the
framing itself is the uncertain part. **Framing level**: three levels, selected by
properties of the work rather than by intuition, with the criteria and their confidence
stated.

`/fleet` owns everything after launch: the work-in-flight table, the stranded-work sweep,
the decision to wake a session versus fix the thing directly versus file it, the review
standard, monitor seeding, and the reporting rule that keeps status claims falsifiable. It
carries the operational half of the harness mechanics — the recipe for reaching a session
and the three traps — and points at the guide for the evidence behind each claim.

The guide carries the full probe: each finding, the command that produced it, the observed
output, and the CLI version it was observed against. It also records that it supersedes an
earlier recipe and why, so a reader who encounters the old one knows which is current.

### The framing levels

| Level | Take it when | Signal |
|---|---|---|
| None | The mechanism already exists in the codebase and the work is applying it | You can name the file and the existing pattern being copied |
| One question | Everything is settled except one choice, and that choice has a real cost either way | You can state the question in a sentence, and both answers are defensible |
| Full investigation | The option set is genuinely larger than the problem statement suggests, or the change alters something persisted with no version field to gate reads on | You cannot enumerate the options without reading code first |

Two things the data contradicts, recorded because they're the intuitive answers:

- **A full investigation is not what catches a wrong direction.** Light-shape dispatches
  rejected their issue's proposed direction on evidence just as often. What the full chain
  uniquely produces is a durable design document — which matters because squash-merge
  deletes everything else.
- **Size doesn't predict the level.** Diffs from the two shapes overlap in both lines and
  files. Don't back-fit a size threshold.

## Solution Architecture

### Content, embedded

```
internal/workspace/
  rootskills/
    dispatch/SKILL.md              (extended)
    fleet/SKILL.md                 (new)
    fleet/references/
      review-standard.md           (new)
      session-control.md           (new: the operational recipe)
  dispatchbriefs/
    _common.md                     (new: the niwa-owned block)
  root_materializer.go             (whole-directory walk; agreement writer)
```

Two `go:embed` roots: the existing `rootskills` tree and a new `dispatchbriefs` tree.

### The whole-directory walk

`writeRootSkills` currently derives the skill name from the parent directory of each
`SKILL.md` and ignores everything else. The change: treat `rootskills/<name>/` as the unit,
copy every regular file under it preserving relative paths, and keep the existing
unconditional-overwrite semantics. A skill directory without a `SKILL.md` is a
programming error and fails loudly rather than installing a skill the harness won't load.

### The agreement writer

`writeDispatchBriefCommon(workspaceRoot)` runs alongside `writeRootSkills` inside
`MaterializeWorkspaceRoot`:

1. Read `<workspaceRoot>/.niwa/dispatch-briefs/_common.md` if it exists.
2. Strip from the sentinel heading to the end of the niwa-owned block.
3. Append the freshly embedded block.
4. Write back, creating the directory if needed.

The sentinel is a stable heading plus an explicit end marker, so the block has bounds and
workspace content can sit on either side of it. This differs from the worktree-context
precedent, which truncates at its heading and therefore has to own the file's tail; here a
workspace may well want its own sections after niwa's.

A workspace that has never had the file gets one containing only niwa's block. A workspace
that already has a hand-written `_common.md` gets niwa's block appended and keeps
everything it wrote.

### Call sites

`MaterializeWorkspaceRoot` gains a third call site in `Create`. The two existing ones —
root-scope apply and named/clone init — are unchanged. All three are idempotent: niwa's
block is replaced, everything else is preserved.

### Boundary with existing skills

`/fleet` reports on work done by *other* sessions this coordinator dispatched. That's the
complement of a per-session in-flight report, which covers the current session's own pull
requests and deliberately never issues a broader listing. Both skill descriptions state the
boundary so they don't compete in the trigger space.

## Implementation Approach

**Phase 1 — materializer.** Whole-directory walk for root skills, plus the agreement writer
and its embed. Unit tests for both, including the merge case where a workspace's own
content sits before and after niwa's block, and the re-run case that must not duplicate the
block. This phase changes no shipped content, so it can land and be verified on its own.

**Phase 2 — the `Create` call site.** One call plus a functional scenario proving that
creating an instance refreshes the workspace root.

**Phase 3 — content.** The extended `/dispatch`, the new `/fleet` and its references, and
the embedded agreement. Content only; the machinery from phases 1 and 2 already carries it.

**Phase 4 — the guide.** `docs/guides/background-session-control.md` with the full probe
evidence, plus its entry in the contributor-guides list in `CLAUDE.md`.

Phases 1 and 2 are independent of 3 and 4 and can be reviewed separately. Phase 3 depends
on phase 1 for the multi-file skill layout.

## Security Considerations

**Writes into the workspace root.** The agreement writer adds `.niwa/dispatch-briefs/` to
the set of paths niwa writes at the workspace root. The path is a constant joined to the
resolved workspace root — no component comes from configuration, from a repository, or from
any other input a third party controls — so there's no traversal surface. The merge reads
and rewrites one file at a fixed path.

**Destroying content on merge.** The realistic risk isn't injection, it's data loss: a
merge bug silently eats a workspace's own prose, and workspace-root writes are untracked by
design, so there's no drift warning to catch it. This is the strongest argument for the
sentinel merge over a plain overwrite, and it's why the tests cover content before the
block, after the block, and a re-run that must be a no-op. A malformed or truncated
sentinel is treated as "no niwa block present" and results in an append, which is
recoverable; the alternative — guessing at bounds — is not.

**Content that instructs an agent.** Both skills and the agreement are read by agents that
act on what they read. They're embedded in the binary and not fetched at runtime, so the
trust boundary is the same as the rest of niwa's shipped content: whoever installed the
binary. The `/fleet` skill's session-control content emits commands built from a session
identifier the coordinator captured itself; the guide's recipe reads a working directory
out of a session transcript rather than reconstructing it from an encoded path, which also
avoids interpolating a decoded string into a shell command.

**Nothing new is fetched, and no credentials are named.** No new network access, no new
external dependency, no secrets or tokens in any shipped file. The review standard tells a
reviewer to run things — building a branch, applying a mutation — inside the repository
being reviewed, which is the same trust posture as reviewing that code at all.

**Visibility.** niwa is public. The shipped content and the guide carry no repository-
specific issue numbers, paths, or organization detail as load-bearing material; provenance
is cited once, generically.

## Consequences

**Positive.** The two decisions that determine whether a fleet runs unattended are made
against stated criteria instead of intuition, at the moment the decision is made, in a path
that's actually loaded. The standing agreement stops living on one machine and becomes
something niwa can correct in the field. The harness claims become re-checkable rather than
folklore, and the recipe that ships is one that works. Whole-directory root skills unblock
any future shipped skill that needs more than one file.

**Negative.** niwa's shipped agent surface doubles, from one root skill to two, and every
workspace gets a file in `.niwa/dispatch-briefs/` it didn't ask for. The framing-level
criteria rest on a small, confounded sample and will need revision as more evidence
accumulates — they're published with that stated, which is honest but is also an admission
that a reader can't fully trust them yet. The harness findings will go stale; that's
inherent to documenting a moving CLI and is the reason each one carries its command.

**Mitigations.** The merge is bounded and additive, so a workspace that dislikes niwa's
block can write around it, and content outside the sentinel is never touched. Both skills
state their boundary with each other and with per-session reporting, so the trigger space
stays legible. The guide names the CLI version every claim was observed against and the
commands to re-run, so a future reader can tell staleness from error in one pass.

**What this doesn't do.** It doesn't automate the fleet loop — `/fleet` describes checks a
coordinator runs, it doesn't run them. It doesn't add a way to message a running session
that the CLI doesn't already provide. And it doesn't settle whether the framing-level
criteria generalize beyond the repository they came from; that needs more dispatches from
more places.
