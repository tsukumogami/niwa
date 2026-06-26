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
workspace. It has the workspace's committed state and tooling, but **none of this chat** --
no decisions, no constraints, no "we agreed not to touch X." The task brief you write is
the worker's ONLY context. So your real job here is synthesis: turn the conversation into a
brief a competent stranger could execute cold.

## Procedure

### 1. Synthesize a complete task brief

Read back over the conversation and write a brief that stands on its own. Include:

- **Goal** -- one or two sentences: what done looks like.
- **Context / decisions** -- the conclusions reached in the chat that the worker can't see:
  chosen approach, rejected alternatives, constraints, assumptions.
- **Pointers to durable artifacts** -- prefer referencing committed files the worker's clone
  already has (e.g. "implement `docs/designs/DESIGN-foo.md`", "the issue is tsukumogami/niwa#123")
  over re-explaining them. The clone has committed state, NOT this session's uncommitted edits.
- **Acceptance criteria** -- how the worker (and you) will know it's done.
- **Out of scope** -- what NOT to touch.

If the work is already fully captured in a committed doc or issue, the brief can be short and
point at it. If it lives only in this chat, the brief must carry it.

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
- Pass the brief's absolute path in the prompt; keep the inline summary short (the prompt is a
  single shell argument).

### 4. Report back

Tell the user: the brief path, the dispatched session id and how to reach it
(`claude attach <id>` / `claude logs <id>` / `claude stop <id>`), and that the worker is
running in its own instance. If they want to fan out more, repeat from step 1.

## Cautions

- **Don't paste giant context into the prompt.** Put it in the brief file; the prompt just
  points at it. Long prompts also risk the argument-length limit.
- **The worker can't see your uncommitted work.** If the task depends on edits that only exist
  in this session's tree, either commit them first or spell them out in the brief.
- **Don't do the work yourself here.** This skill's job is to hand off, not to implement. If
  the user actually wants the work done in this session, that's a normal task, not a dispatch.
- **One brief, one worker.** For parallel work, write a separate brief and dispatch per unit so
  each gets its own isolated instance.
