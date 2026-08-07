---
name: dispatch
description: Hand the work you just discussed to an isolated background agent via `niwa dispatch`. Use when, after a planning chat at the workspace root, you are ready to launch the actual work in its own ephemeral niwa instance rather than doing it in this session. Triggers on "dispatch this", "hand this off", "launch a worker for this", "let's kick this off in its own instance".
---

# /dispatch

Hand the work the user just decided on to a fresh background agent running in its own
ephemeral niwa instance, by constructing a self-contained task brief and launching it with
`niwa dispatch`.

Use this skill from a **coordinator session at the workspace root**: the user has been
chatting about what to do, and now wants the work itself to run in isolation (its own
clone, its own branch, its own Agent View session) instead of in this conversation.

## The one thing that matters: the worker starts blind

`niwa dispatch` launches a brand-new `claude --bg` session in a fresh clone of the
workspace. It has the workspace's pushed (remote) state and tooling, but **none of this chat** --
no decisions, no constraints, no "we agreed not to touch X." The task brief you write is
the worker's ONLY context. So your real job here is synthesis: turn the conversation into a
brief a competent stranger could execute cold.

## Two decisions before you write anything

These determine whether the worker finishes without the author, which is the whole point
of dispatching. Both get made every time; neither is usually written down. Say your answer
and your reason in the brief, so the next person can tell a good call from a lucky one.

### Launch mode: autonomous, or settle the framing first?

**Autonomous** when what "done" looks like is unambiguous. The goal is stated, the
acceptance criteria are checkable, and the author's absence costs nothing. This is the
common case and the default.

**Interactive first** when the framing itself is the uncertain part -- when a reasonable
agent could answer "what is this actually for?" several ways and pick differently from the
author. Say so explicitly in the brief: tell the worker to ask its framing questions, wait
for answers, and only then continue autonomously.

The failure this prevents is specific and expensive. An autonomous agent handed an
uncertain framing does not stall -- it resolves the question confidently, builds
everything downstream on that answer, and the author discovers the divergence when the
pull request arrives. By then the work is done and the rework is total. A brief that
inverts the default costs one round trip; getting it wrong costs the whole task.

Signal that you are in the second case: you cannot write the acceptance criteria without
first deciding something you would rather the author decided.

### Framing level: how much investigation before implementation?

| Level | Take it when | Signal the property holds |
|-------|--------------|---------------------------|
| **None** | The mechanism already exists in the codebase and this work is applying it | You can name the file and the existing pattern being copied |
| **One question** | Everything is settled except one choice, and that choice has a real cost either way | You can state the question in a sentence and both answers are defensible |
| **Full investigation** | The option set is genuinely larger than the problem statement suggests, or the change alters something persisted with no version field to gate reads on | You cannot enumerate the options without reading code first |

At the **one question** level, name the question in the brief. A worker told "decide the
marker's cost before writing code" produces a recorded decision; a worker told "use your
judgment" produces an unexamined default.

Two things that look like criteria and are not:

- **A full investigation is not what catches a wrong direction.** Workers at the lightest
  level rejected their issue's proposed approach on evidence just as often. What the full
  chain uniquely produces is a durable design document -- which matters because
  squash-merge deletes everything else, so reasoning recorded only in a pull request body
  is gone the moment it lands. Choose the level by whether the reasoning needs to outlive
  the pull request, not by whether you expect the direction to change.
- **Size does not predict the level.** Diffs from the light and heavy levels overlap in
  both lines and files. Do not back-fit a threshold.

**Where this comes from, and how far to trust it.** These criteria are drawn from a batch
of 16 dispatches in one repository over three days. The level chosen is almost perfectly
confounded with the date -- every full-investigation dispatch is from the first two days,
every third-day dispatch is the lightest level -- so a "the coordinator got more
confident" explanation fits the data as well as the criteria do. Treat them as a starting
heuristic worth recording your disagreement with, not as a settled rule.

If the workspace uses a workflow plugin, the levels map onto its skills: none is a direct
implementation run, one question is an implementation run with a mandated decision step,
and full investigation is that plugin's explore-then-plan chain. The levels are the
decision; the skill names are one tool's spelling of it.

## Procedure

### 1. Synthesize a complete task brief

Read back over the conversation and write a brief that stands on its own. Include:

- **Goal** -- one or two sentences: what done looks like.
- **Context / decisions** -- the conclusions reached in the chat that the worker can't see:
  chosen approach, rejected alternatives, constraints, assumptions.
- **Pointers to durable artifacts** -- prefer referencing pushed files the worker's clone
  already has (e.g. "implement `docs/designs/DESIGN-foo.md`", "the issue is tsukumogami/niwa#123")
  over re-explaining them. The clone is made from each repo's remote, so it has the PUSHED
  state, NOT this session's uncommitted edits or unpushed local commits.
- **Acceptance criteria** -- how the worker (and you) will know it's done.
- **Out of scope** -- what NOT to touch.
- **Final-message work-in-flight block** -- the instruction described in step 1a below.

If the work is already fully captured in a pushed doc or issue, the brief can be short and
point at it. If it lives only in this chat, the brief must carry it.

### 1a. Require the work-in-flight block in the worker's final message

A dispatched worker runs in the background: its user-visible `systemMessage` channel
does NOT reach the Agent View dashboard row, so the only durable surface for the
session's real pull-request set is the worker's own final message. Every brief you
write MUST therefore instruct the worker to **end its final message with the
standardized work-in-flight block** (the block under the literal marker
`=== WORK IN FLIGHT ===`).

Do not spell out the block's layout in the brief. There is exactly ONE source of
truth for its format -- the shirabe `work-summary` component (the same renderer the
`/inflight` skill and the ambient hooks use); reference it, don't restate it, so the
two layers cannot drift. Tell the worker to produce the block from its real captured
PR state (never invented references), and to apply the **same terminal-safety
sanitization** that governs every other emission of the block: strip control and ANSI
bytes, keep one line per PR with the bare URL last, never a newline or `|` inside a
cell, and never a second `=== WORK IN FLIGHT ===` marker inside any field. If the
worker opened no PRs, it says so in one line rather than emitting an empty marker.

Concretely, add a line to the brief such as: "When you finish, end your final message
with the standardized `=== WORK IN FLIGHT ===` block (the shirabe work-summary format,
same as `/inflight`) listing every PR you opened, one per line with its bare URL last;
apply the block's terminal-safety sanitization and never invent a PR reference."

### 2. Write the brief to a stable file

Resolve the workspace root (the cwd of this session, or walk up to the `.niwa/` directory).
Write the brief to an absolute path under it, creating the directory if needed:

```
<workspace-root>/.niwa/dispatch-briefs/<slug>.md
```

where `<slug>` is a short kebab/underscore topic name. This file is the durable handoff and
the audit trail. The worker reads it by absolute path (same machine, same filesystem), so it
does not need to be committed.

### 3. Launch the worker

Run `niwa dispatch` from the workspace root, pointing the worker at the brief:

```bash
niwa dispatch "Read <abs-path-to-brief> for your complete task brief, then implement it. <one-line summary>" \
  --name "<short topic>" --detach
```

- **`--detach`** is important here: without it the command attaches THIS terminal to the new
  session, pulling the coordinator into the worker. With `--detach`, this session stays put so
  the user can keep planning or dispatch more workers; they can `claude attach <id>` later to
  look in. Only omit `--detach` if the user explicitly wants to jump straight into the worker.
- **`--name`** gives the session a readable name in Agent View (sanitized into a slug; it also
  names the instance, e.g. `<config>+<slug>-<id>`).
- **`--model`** (optional) picks the model that runs the worker's main chat loop. Pass it when
  the user asked for a specific model, or when the work clearly warrants a heavier or lighter
  one. It accepts either a capability **category** -- `fast`, `balanced`, or `powerful` -- or a
  versionless vendor name -- `fable`, `sonnet`, `opus`, `haiku`. Prefer a category unless the
  user named a specific model, since categories stay correct as concrete models change. Omit the
  flag to use the workspace default (the `[global] dispatch_model` host setting, if any). Example:
  `niwa dispatch "..." --name "<topic>" --model powerful --detach`.
- Pass the brief's absolute path in the prompt and keep the inline summary short. The prompt
  rides a single argv element and is never passed through a shell, so quoting and
  metacharacters are not a hazard.

### 4. Report back

Tell the user: the brief path, the dispatched session id and how to reach it
(`claude attach <id>` / `claude logs <id>` / `claude stop <id>`), and that the worker is
running in its own instance. If they want to fan out more, repeat from step 1.

## Cautions

- **Put giant context in the brief file, not the prompt.** The prompt just points at it. This
  is about legibility, not size: a prompt that is a wall of pasted text is harder for you and
  the user to read back later than one that names a durable artifact. niwa handles size on its
  own -- a prompt too large to pass as a command argument is written to a file automatically
  and the worker gets a pointer -- so there is no limit here to work around.
- **The worker can't see your unpushed work.** The clone comes from the remotes, so if the
  task depends on edits that only exist in this session's tree (uncommitted, or committed but
  not pushed), either commit AND push them first, or spell them out in the brief.
- **Don't do the work yourself here.** This skill's job is to hand off, not to implement. If
  the user actually wants the work done in this session, that's a normal task, not a dispatch.
- **One brief, one worker.** For parallel work, write a separate brief and dispatch per unit so
  each gets its own isolated instance.

## Dispatching several at once

Two things make parallel dispatch work, and both go in each brief.

**Name the siblings and their file surfaces.** Tell each worker which other work is
in flight right now and which files those workers are touching -- not just "don't fix
issue X", but "issue X is running in its own session against these three files; if your
change reaches them, stop and say so rather than editing them." Workers respect this and
will decline to widen scope when they hit a neighbour's file, which is what keeps ten
concurrent sessions from colliding.

**Point every brief at the shared agreement.** `<workspace-root>/.niwa/dispatch-briefs/_common.md`
carries what does not change between tasks -- autonomy, verification discipline, scope
rules, how to report. niwa ships it and updates its own block on every workspace-root
materialization; anything the workspace adds outside the sentinel markers is preserved.
Every brief should open by naming it as required reading. Factoring it out cuts each brief
to roughly a third and stops the boilerplate drifting between workers.

## After they are running

Dispatching is half the job. Once workers are in flight, use `/fleet`: what is in flight,
what is stranded, what needs review, and whether to wake a session or fix the thing
yourself. That skill also carries the recipe for reaching a session that has gone quiet,
which has several traps that each look like success.
