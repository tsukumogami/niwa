# Explore Scope: session-name-collision

## Visibility

Public

## Core Question

`niwa dispatch --name <slug>` sanitizes the user-supplied name and forwards it to
the launched agent as the session's display name. Nothing makes that slug unique,
yet the name is also the address other Claude Code sessions use to reach the
worker. Two dispatches with the same `--name` should therefore produce two live
sessions sharing one address. We need to know whether that is true of the current
code, what actually happens on a send to a colliding name, and whether it is worth
fixing given the readability cost of any uniqueness scheme.

## Context

Reported from a research pass in a sibling session; every claim in the report is
to be re-verified against current `main` rather than trusted, line numbers in
particular.

Claims to verify:

- `niwa dispatch` accepts `--name`, sanitizes it to `[a-z0-9_]` capped at 40 runes,
  and forwards the slug as the session display name.
- niwa generates 8 hex digits of uniqueness per dispatch, but spends it on the
  instance directory name (`<config>+<slug>-<8hex>`), not on the session name.
- Claude Code cross-session messaging addresses peers by name; a bare name matching
  more than one live session resolves "latest wins", silently.

Constraint on any implementation shape: `internal/cli/dispatch_layout_test.go` runs
AST scans over the dispatch-path files that forbid naming an agent, as a constant
or as a string literal. A fix must gate on flag spellings or capability lookups,
never on an agent name.

Stakes: today a misdelivered peer message still surfaces to a human at the approval
prompt. A sibling session is exploring a dispatch flag that would pre-approve
inbound peer delivery for dispatched workers; with that in place the same
misdelivery becomes silent. That sibling exploration treats this as arguably a
prerequisite, which bears on urgency.

## In Scope

- The `niwa dispatch` naming path: `--name` parsing, sanitization, the instance-name
  builder, the 8-hex suffix, and the per-agent flag table that carries the display
  name.
- What the session name is used for elsewhere in niwa (capture correlation,
  keep-alive, remote control, reap, mapping store).
- Whether niwa can detect a collision at dispatch time at all.
- The readability trade-off of any uniqueness scheme, since the name is what a
  human reads in Agent View.
- Whether this is worth fixing, argued either way.

## Out of Scope

- The peer-message pre-approval flag itself. A sibling session owns that.
- `crossSessionInbound`, permission modes, and approval prompts.
- Redesigning niwa's messaging or dispatch model more broadly.

## Research Leads

1. **Is the collision real in the current code, and exactly where does the slug
   travel from `--name` to the agent's display-name flag?**
   The whole exploration rests on this. Trace the path end to end with file and
   line citations, confirm or correct the sanitization rule and the 40-rune cap,
   and find where the 8-hex suffix is generated and what it is spent on.

2. **What actually happens when a message is sent to a name that matches two live
   sessions?**
   The report asserts "latest wins" and cites Claude Code's cross-session
   messaging. Confirm the disambiguation rule from the tool contract rather than
   assuming it, and establish whether the sender gets any error, warning, or
   disambiguation affordance.

3. **What else in niwa depends on the session name being exactly the user-supplied
   slug?**
   Any uniqueness change breaks whatever correlates on the name. The report says
   session capture correlates on the instance directory rather than the name;
   verify that, and sweep keep-alive, remote control, reap, the mapping store, and
   anything that greps or matches on the name.

4. **Can niwa detect a name collision at dispatch time, and against what set of
   peers?**
   Determines whether "disambiguate only on collision" is even implementable.
   Establish what niwa knows about live sessions -- its own mapping store, other
   workspaces on this machine, and sessions on other machines or in the cloud.

5. **What do the dispatch layout and spelling tests actually forbid, and which
   implementation shapes stay legal?**
   `internal/cli/dispatch_layout_test.go` and `internal/agentplan/layout_scan_test.go`
   constrain the fix before it is designed. Enumerate the rules the scans enforce
   and name concrete shapes that pass and fail them.

6. **Where does the session name surface to a human, and what is the readability
   cost of a suffix?**
   `review-3f9a2b1c` is uglier than `review`. Find every surface that displays the
   name (Agent View, niwa's own output, docs, logs) and look for precedent in the
   repo for readable-name-plus-suffix schemes and how they were justified.
