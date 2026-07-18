# Exploration Scope: dispatch-handle-retask

## Visibility

Public

## Topic

Can (and should) a niwa dispatch handle be used to push new instructions into
the same already-running dispatched session in-place — without forking a new
Claude Code process or minting a new session id? Determine whether this is a
real capability gap or a usage/documentation gap given #209 (opt-in keep-alive
for remote-control sessions).

## Triggering symptom

A coordinator agent in a sibling workspace dispatched niwa worker sessions,
then re-tasked one via `claude --resume <session-id> --bg "<task>"`. This
preserved conversation context but minted a NEW session handle, leaving the
original session idle/orphaned — session sprawl, stale orphan, and risk of two
sessions owning the same branch.
