---
schema: brief/v1
status: Accepted
problem: |
  Workers launched by `niwa dispatch` can end up sharing a name, and the name is
  how other Claude Code sessions reach a worker. When two live workers share one,
  a message meant for either has no documented destination, and nobody is told.
  Developers already rename sessions by hand to tell colliding workers apart.
outcome: |
  A developer reuses a role name across as many dispatches as their work needs,
  and niwa never hands a worker a name another niwa worker already holds. They can
  find the one name that reaches a specific worker without renaming anything by
  hand, and a message sent to that name reaches that worker and no other.
---

# BRIEF: Dispatched session names that identify one worker

## Status

Accepted

The Phase 4 jury passed on content quality and structural format. Two framing
questions the draft left open are handed to the downstream PRD's Decisions and
Trade-offs: how short the distinguishing part of a name can be while staying
readable, and what form the name takes when a developer learns it at dispatch.

## Problem Statement

A developer running parallel work fans it out with `niwa dispatch`, one background
Claude Code worker per task. They name each worker for its role: `review`,
`coordinator`, `ci`. Role names are short and they recur, so a developer who
reviews two pull requests at once dispatches `--name review` twice.

Those two workers now answer to the same name. That would be cosmetic if the name
were only a label, but it isn't: in Claude Code the session name is the address.
Another session finds a peer by listing sessions and sends to it by name. The
namespace isn't scoped to one workspace or one machine either. It spans every
background, interactive, local and remote session on the account. A worker niwa
launches under a reused name is ambiguous against everything that answers to that
name, anywhere.

When a message goes to a name two live sessions share, nothing documents which one
receives it. Claude Code's cross-session messaging documentation describes
addressing a peer by name and states no rule for a name that matches two live
sessions. The message may reach the worker the sender didn't mean, or neither, and
the sender gets no error either way. The developer who dispatched the workers gets
no warning at dispatch time either. niwa does make each worker's instance directory
unique, so from niwa's side the two workers look distinct. The collision lives in
the session name, the one identifier niwa hands a worker and never shows back to
the developer.

This is already happening. A peer listing taken in September 2026 from inside one
dispatched session showed 115 peers, seven names shared by fifteen of them, and one
name held by three sessions at once. On the same machine, a developer had renamed
one of two same-named dispatched sessions by hand to tell them apart, a rename
niwa's own naming rules can't produce.

## User Outcome

A developer dispatches workers under whatever role names their work calls for, and
reuses them freely. Two reviews running side by side are two workers a developer
or a peer session can reach separately, not one address with two occupants.

When the developer needs a particular worker, they can find the name that reaches
exactly that one. They don't need to open each candidate to check which task it
holds, and they never rename a session by hand to break a tie. A coordinating
session that hands work to a peer niwa launched gets the peer it looked up.

The developer stops carrying a question they couldn't answer before: did that
message go to the worker I launched? niwa no longer produces the ambiguity, so
choosing a name goes back to being about readability.

## User Journeys

### Journey 1: Developer telling two live look-alikes apart

**User:** a developer who reviews several pull requests in parallel.
**Trigger:** an hour after dispatching two workers with `--name review`, one per
pull request, they want to send the first one a follow-up about its PR, and both
workers are already running.
**Outcome shape:** the two workers are separately reachable. The developer can tell
which name belongs to which worker and sends the follow-up to the right one on the
first try, without opening either session to check what it's working on.

### Journey 2: Coordinating session handing work to a peer

**User:** a coordinating session that is itself a dispatched worker, splitting a
larger job across peers it or the developer launched earlier. The developer who
set it up is the one relying on its handoffs landing where it says they did.
**Trigger:** it lists the peers available to it, picks one by name, and sends it a
unit of work.
**Outcome shape:** the name the coordinator picked resolves to exactly one niwa
worker, so the work lands on the peer the coordinator chose. That holds even when
other niwa workers on the account were dispatched under the same role name, from
other workspaces or other machines.

### Journey 3: Developer capturing the name at launch

**User:** a developer who has just run `niwa dispatch` and is about to move on to
other work.
**Trigger:** before switching away, they want to note the new worker's name so they
can find it in Agent View later or give it to another session.
**Outcome shape:** the developer knows the name the worker answers to from the
dispatch itself, without piecing it together from an instance directory path or
scanning a list of look-alike names afterward.

## Scope Boundary

### In scope

- The name each worker gets when `niwa dispatch` launches it under a
  developer-supplied `--name`, and whether that name identifies one worker.
- How a developer learns what name a new worker answers to, without reconstructing
  it from other output.
- Keeping the dispatched name readable enough that a developer still recognizes the
  role they chose.
- Correcting niwa's own documentation where it currently claims dispatch is
  collision-safe even when two dispatches share a `--name`. That's true of instance
  directories and not of session names.

### Out of scope

- **Global uniqueness across the whole session namespace.** niwa is one of several
  writers into a namespace it neither owns nor can fully see. Sessions started
  outside niwa carry names niwa never chose. This feature makes sure niwa isn't the
  source of a collision it created, and stops there.
- **`niwa watch` review sessions.** They collide by construction, because their
  name is derived from the pull request being reviewed. But that name also serves
  as a staged-record filename and a handle developers type, so fixing it carries
  costs dispatch doesn't. It needs its own framing.
- **Dispatches with no `--name`.** An unnamed dispatch forwards no name today, and
  niwa's tests pin that behavior. Whether anonymous workers should become
  addressable is a separate product decision.
- **The dispatch brief files the `/dispatch` skill writes.** Two same-named
  dispatches overwrite each other's brief one layer above niwa. It's a real
  collision of the same family, and a different fix.
- **How Claude Code resolves a message sent to an ambiguous name.** That behavior
  belongs to Claude Code, not niwa. This feature stops niwa from producing the
  ambiguity; it doesn't change how the harness handles one.
- **Pre-approving inbound peer messages for dispatched workers.** A separate effort
  is exploring a dispatch flag for it. That work raises this feature's urgency,
  since a misdelivered message it pre-approves would go unnoticed, but the flag
  itself isn't part of this feature.
- **Codex workers.** niwa gives Codex dispatches no session name at all, so there's
  nothing for them to collide on today.

## References

- `docs/prds/PRD-instance-dispatch.md`: the dispatch requirements, including the
  requirement that makes instance names unique and the one on reporting how to
  reach a launched session.
- `docs/designs/current/DESIGN-instance-dispatch.md`: the dispatch design, whose
  collision-safety claim this feature corrects for session names.
