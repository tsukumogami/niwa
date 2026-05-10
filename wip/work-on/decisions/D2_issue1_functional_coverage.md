# Decision D2: Issue 1 functional test coverage

## Question

Issue 1's AC asks for an `@critical` Gherkin scenario covering the
worker → live-coordinator routing path end-to-end. The functional
test infrastructure does not currently support session worktrees
with `NIWA_MAIN_INSTANCE_ROOT` set (no test step references either,
verified via grep). Building that infrastructure would take
~2-3 hours.

## Options

- **A**: Build the worktree-aware functional test infrastructure
  and add the new scenario. Closes the AC literally.
- **B**: Rely on the new unit tests
  (`TestHandleAsk_SessionWorktreeRoutesToMainInstance`,
  `TestSendMessage_SessionWorktreeRoutesToMainInstance`) plus the
  three existing `live-coordinator-ask.feature` scenarios that
  exercise the routing path through the same `roleRoot` helper.
  File a follow-on issue for the worktree-aware functional
  infrastructure.
- **C**: Skip the AC entirely.

## Chosen: B

The Issue 1 fix is a small routing redirect. The unit tests cover
both call sites (`isKnownRole` via handleAsk, inbox path via
sendMessage) with the exact scenario the design names: a session
worker reaching the coordinator from a worktree where no
coordinator dir exists. The existing `live-coordinator-ask.feature`
scenarios exercise the routing path's downstream behavior (notify,
question waiter, finish-task). The combination covers the fix
end-to-end at unit level + downstream behavior at functional level;
the only thing not covered functionally is the multi-instance setup
itself.

A follow-on issue for worktree-aware functional infrastructure
will be filed at PR finalization. That infrastructure benefits
multiple future tests (Issues 4, 7, 12 also touch worktree
behavior), so building it once for this PR vs. building it for
each issue is the right tradeoff.

## Status

Confirmed. Issue 1 acceptance: unit tests + existing functional
coverage. Worktree-aware functional infra is a follow-on issue.
