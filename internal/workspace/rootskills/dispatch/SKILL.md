---
name: dispatch
description: >-
  Run the work just discussed as a background agent in its own fresh niwa
  instance -- its own clone, its own branch, its own Agent View session --
  instead of doing it in this conversation. Reach for it at the workspace root
  when a planning chat turns into doing ("alright, go build it"), when several
  independent units should be worked at once ("can you do these three
  refactors in parallel?", "knock all of these out"), when the user wants to
  stop waiting ("this'll take a while, don't block on me"), when isolation is
  the constraint ("don't touch my working tree", "start clean on this one"),
  and on "farm this out", "put it in the background", "queue this up", "fire
  and forget" -- usually nobody says the word dispatch. The worker starts
  blind, with the remotes' pushed state and none of this chat, so the
  self-contained brief this skill writes is its only context; skip it and the
  work either quietly happens here or lands on a worker that has no idea what
  was decided. What separates this from an in-session Agent-tool fan-out, or
  from `shirabe:work-on` and `shirabe:execute`, is where the work runs --
  another session, another clone -- not what the work is. Do NOT use when the
  user wants the work done in this session, or when the task depends on
  uncommitted or unpushed local state the worker's clone cannot see.
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
