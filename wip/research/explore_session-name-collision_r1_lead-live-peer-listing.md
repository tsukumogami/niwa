# Lead: What does a real peer listing look like, and are name collisions actually occurring?

> **Redaction note:** this lead was run by the exploration orchestrator directly
> rather than by a research agent, because it required calling `ListAgents` from a
> live dispatched session — something a subagent cannot do on the orchestrator's
> behalf. The listing contains session names from non-public projects. Only counts,
> structure, and redacted shapes are recorded below; no private session name is
> reproduced.

## Method

This exploration is itself running inside a session that `niwa dispatch` launched.
That makes the orchestrator a first-hand witness to the very mechanism under
investigation: it can read its own display name and enumerate its peers. A single
`ListAgents` call was made from this session on 2026-09-09.

This is direct observation of the deployed system, and it outranks both the
documentation search in `lead-peer-addressing` and the static code reading in
`lead-slug-path` on the questions it can answer.

## Findings

### 1. The dispatched session's display name is the bare sanitized slug — confirmed from the receiving end

`ListAgents` opens by naming the calling session. This session was dispatched with
`--name session-name-collision`, and it reports its own name as
`session_name_collision`.

That is exactly what `sanitizeInstanceSlug` (`internal/cli/dispatch.go:910-929`)
produces from that input: lowercased, the dash collapsed to a single `_`, no
suffix. The instance directory this session runs in is
`tsuku+session_name_collision-21dbe5c8`, carrying the 8-hex token.

So the fork that `lead-slug-path` traced statically — hex to the directory, bare
slug to the session — is confirmed from the other end of the exec. The session
name and the directory name were derived from the same input, in the same
dispatch, and only one of them got the uniqueness.

### 2. Every listed peer carries a short reference id, and it is 6 hex characters

Each row in the listing has the shape:

```
<name> [<6 hex>]  ·  <kind>  ·  <state>  ·  started <age>
```

The bracketed token is present on **every** row unconditionally, not only on
ambiguous ones. This session's own is `[07483a]`.

Two consequences:

- A sender always has a disambiguator available before attempting a send. The
  `ListAgents` contract describes appending a row's ` [ref]` "only when the bare
  name is not enough — two rows share it, or an error asks you to disambiguate."
  So the affordance `lead-peer-addressing` could not find documented specifics for
  does exist and is always in hand.
- **Claude Code's own disambiguator is 6 hex, not 8.** That is a directly relevant
  precedent for the suffix-length question, which
  `lead-name-readability` flagged as open (4 vs 6 vs 8). The harness that owns the
  namespace picked 6.

### 3. Name collisions are not hypothetical — they are present right now, at scale

The listing returned **115 peers** plus this session. Grouping the rows by name:

| Colliding name (redacted) | Sessions sharing it |
|---|---|
| A | 3 |
| B | 2 |
| C | 2 |
| D | 2 |
| E | 2 |
| F | 2 |
| G | 2 |

**Seven distinct names are shared by 15 sessions** in one developer's fleet. That
is roughly 13% of the listed peers sitting in a name group with at least one
sibling, and one name held by three sessions at once.

Crucially, one collision involves a **live** session: a background session in
`idle` state, started two hours before the probe, shares its name with an offline
Remote Control session. This is not an artifact of dead history piling up — a
currently-addressable peer is currently ambiguous.

The colliding names are all role-shaped or topic-shaped — the kind of short,
reusable label a person naturally types twice (`coordinator`, a recurring
question, a repeated workflow step). That matches the prediction in
`lead-collision-detection`: collisions arise from reusing a role name, not from
adversarial input.

### 4. The namespace is genuinely cross-machine and cross-workspace

The listing mixes `bg` sessions (niwa dispatches on this machine), one
`interactive` session, and a large majority of `Remote Control` sessions —
this account's sessions on other machines and in the cloud. Several colliding
pairs straddle that boundary: a local background session on one side, a Remote
Control session on the other.

This settles the scope question empirically. `lead-collision-detection` established
that niwa can read a machine-wide job store but cannot see off-machine peers; the
listing shows that off-machine peers are not an edge case here, they are the bulk
of the namespace (roughly 100 of 115 rows). Any dispatch-time collision check
would be blind to most of the address space it is checking against.

### 5. Names in the wild are not all niwa-produced

Several rows carry names containing spaces and capital letters — shapes
`sanitizeInstanceSlug` cannot emit, since it lowercases and collapses everything
outside `[a-z0-9]` to `_`. Those are auto-derived or human-set names from
interactive and Remote Control sessions.

So niwa is one writer into a shared namespace it does not control, alongside the
harness's own auto-naming. niwa cannot enforce global uniqueness even in
principle; it can only avoid being the source of a collision it created itself.

## Implications

1. **The premise the whole exploration rests on is confirmed.** Names are the
   address, the namespace is shared and global, and niwa's dispatched sessions sit
   in it under non-unique names. Three research leads flagged "is the display name
   really the peer address?" as their biggest open question; the answer is yes.

2. **The bug has already happened, repeatedly, in this fleet.** Combined with the
   hand-applied `legacy-` rename that `lead-collision-detection` found in the job
   store, there are now two independent lines of evidence that a human has already
   run into this and worked around it manually. The demand question is settled by
   observation rather than argument.

3. **Detection-then-warn is weaker than it looked one lead ago.** A machine-local
   check would have seen only about 15 of 115 peers at the moment of this probe.
   A "no collision detected" result would be close to meaningless, and printing it
   would be worse than printing nothing, because it reads as an all-clear.

4. **Unique-by-construction is the only option that survives the observed
   namespace.** niwa cannot see the namespace, cannot lock it, and does not own it.
   The one thing it can do is guarantee that the name it generates is not the
   source of the ambiguity — which is exactly the property the instance directory
   already has and the session name does not.

5. **6 hex is the length to match.** The harness's own row disambiguator is 6 hex.
   A niwa suffix of the same width sits naturally next to it and is 2 characters
   cheaper than reusing the instance's 8-hex token wholesale. Against that,
   reusing the instance token makes the session name and the instance directory
   legible as a pair, which is a real operational benefit. This is now a genuine
   trade-off with evidence on both sides rather than an open question.

## Surprises

- **13% of the fleet is already in a name collision.** The exploration was framed
  around a hypothetical two dispatches sharing `--name`. The reality is seven
  collisions standing in one listing, one of them three-deep.
- **The harness disambiguates with 6 hex, while niwa's instance names use 8.**
  Nobody in the niwa repo has noticed the neighbouring convention.
- **Off-machine peers are the majority, not the tail.** Roughly 100 of 115 rows are
  Remote Control. Any reasoning that treats them as an edge case has the
  proportions backwards.
- **The listing's own contract offers the remedy.** The ` [ref]` affordance means a
  careful sender can already address unambiguously today. The bug is not that
  disambiguation is impossible; it is that the bare name is the ergonomic default
  and niwa makes that default unsafe.

## Open Questions

1. **What does a bare-name send to an ambiguous name actually do?** Still
   unresolved, and still the one thing neither documentation nor observation has
   settled. It was not tested here: probing it means sending a real message to a
   real colliding session belonging to unrelated work, which is not a side effect
   this exploration is entitled to cause.
2. **Does the harness ever rename a background session on collision?** The
   documentation says background and `-p` sessions are not uniqueness-checked at
   startup. The observed duplicates corroborate that, but whether a later rename
   ever happens automatically is unknown — the one `legacy-` prefixed name looks
   hand-applied, not generated.
3. **Does Agent View truncate the row?** Unmeasured. The `ListAgents` text listing
   does not truncate, but that is a different surface from the visual one.

## Summary

Running `ListAgents` from inside this dispatched session confirms the exploration's
load-bearing premise first-hand: the session's own display name is exactly the bare
sanitized slug with no uniqueness token, names are the address, and the namespace is
global across background, interactive, local and Remote Control sessions. The listing
returned 115 peers in which seven distinct names are shared by fifteen sessions — one
name three ways, and one collision involving a live idle background session — so the
bug is not hypothetical but already present in this fleet at roughly a 13% rate.
Roughly 100 of those 115 peers are off-machine, which guts the detect-and-warn option
that looked viable one lead earlier and leaves unique-by-construction as the only
remedy niwa can actually deliver; the harness's own row disambiguator is 6 hex, which
is the neighbouring convention a niwa suffix should probably match.
