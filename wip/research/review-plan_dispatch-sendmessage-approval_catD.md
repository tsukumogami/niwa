# Category D Review: Sequencing / Priority Integrity

Topic: dispatch-sendmessage-approval. Round 1, fast-path, input_type design,
execution_mode single-pr, decomposition horizontal, 8 issues. Full check applies.

## Dependency edges

The graph is 1, 2, 3 as roots; 4 blocked by 2 and 3; 5 and 6 blocked by 4; 7 and
8 blocked by 1, 4, 5, 6. This matches the design's commit order (Phase 7 anywhere,
then 1 and 2, then 3, then 4 and 5, then 6).

Checked each place where an issue edits or reads what another issue creates:

- Issue 4 reads `GlobalSettings.AcceptSessionMessagesOnDispatch` and
  `config.CrossSessionInboundKey` (issue 2) and adds a key to the step 9c map and
  `renderLaunchSettings` (issue 3). It also edits `dispatch_layout_test.go`, which
  issue 3 edits first, and it hoists `hostGlobal` above the step 9c block that
  issue 3 reshapes. Both edges are declared.
- Issue 5 writes `inboundApplied` into the step 11 mapping literal, and its
  Codex-with-flag wiring case needs issue 4's capability row. The dependency on
  4 is declared.
- Issue 6 adds `showInboundExplanation` and constants to `dispatch_inbound.go`
  (created by 4), uses `inboundGuideURL` (4), and hangs off the audit-line call
  site (4). The dependency on 4 is declared.
- Issue 7 drives the flag and key (4), asserts the list field and marker (5), the
  explanation and marker file (6), and its watch-site unit test sets issue 4's
  flag variable. Edges to 4, 5, and 6 are declared. Its edge to 1 is broader
  than it needs to be: nothing in issue 7 exercises the deny hook, since the
  watch-site test checks `dispatchLaunch` passthrough, not hooks. That edge is
  harmless and only removes a little parallelism.
- Issue 8 quotes strings from 1, 4, 5, and 6, and `inboundGuideURL` from 4.
  All four edges are declared. Its manual check also validates issue 3's
  `claude --bg ... -- <prompt>` argv shape, which it reaches transitively
  through 4.

No issue's acceptance criteria reference behavior from an issue it doesn't
depend on, directly or transitively.

## Issues 5 and 6 running in parallel on runDispatch

Both edit `internal/cli/dispatch.go`, in separate regions. Issue 5 edits the
step 11 `workspace.SessionMapping` literal (around line 770, next to
`KeepAlive: keepAliveArmed`). Issue 6 adds a call right after issue 4's audit
line (after step 12) and in the two `dispatchAttach` branches at step 14 (around
line 853). Neither reads or changes state the other sets; both only read
`inboundApplied`, which issue 4 fixes before the mapping write. In single-pr mode
these land as sequential commits on one branch, so at worst they cause an
ordinary textual rebase. They share no critical state.

## Issue 3's argv change and tests owned elsewhere

Setting `PromptSeparator` on Claude's spec changes every Claude argv. I checked
every test that uses `claudeLaunchSpec()` or reads the launch argv:

- `dispatch_launcher_test.go` (`TestBuildLaunchArgs_Order`, `_NoPassthrough`,
  `_PromptRemainsSingleElement`) uses exact `reflect.DeepEqual` pins. Issue 3's
  acceptance criteria name all three.
- `dispatch_promptsplit_test.go` reads the prompt as the final element, which
  still holds. Issue 3 names it.
- `dispatch_wiring_keepalive_test.go` and `dispatch_wiring_remotecontrol_test.go`
  are named in issue 3.
- `dispatch_reachability_test.go`, `dispatch_promptsize_test.go`, and
  `dispatch_capture_test.go` use the spec but don't pin argv shape.
- No `watch*_test.go` pins the argv.
- Functional: the fake `claude` records `$*`; `the launched claude was invoked
  with` is a substring check; the spill scenario parses `file: ` pointers. Issue
  3's acceptance criteria cover the fake and the spill scenario. The Codex
  features are unaffected.

Issue 7 explicitly says it doesn't rework argv expectations and that issue 3
owns them. No test owned by a later issue is left broken by issue 3.

## Must-run QA ownership and priority

- `@critical` scenarios: issue 7 (testable) owns all of them. It's a leaf that
  depends on every behavior issue it validates (1, 4, 5, and 6 directly, and 2
  and 3 through 4). That isn't structural deferral; it validates after building.
- Watch exclusion (R14): the design calls it a unit test at both watch launch
  sites. Issue 7 owns it and depends on issue 4's flag variable. It's owned.
- Manual delivery check: issue 8 owns it, alongside the five extra measurements.
  Issue 8 is classified `simple`, but it isn't the only integration validation,
  because issue 7 is testable. It's a leaf that depends on everything it
  measures, and its acceptance criteria say a failing case blocks the merge.
  Under the procedure's key signal, this isn't structural deferral.

## Non-critical observations (not findings)

- Issue 8's `simple` label understates the manual check. If the manual check
  finds the tool list or the flag registration wrong, issue 8's acceptance
  criteria have it change code from issues 1 and 4 "in this PR". A matcher change
  would also invalidate issue 1's tests that hard-code
  `SendMessage|SendFile|RemoteTrigger|ListAgents`. That's a backward edit from a
  docs issue into Go code, but in single-pr mode it lands before merge.
- The only real-Claude check of `claude --bg ... -- <prompt>` (a design
  assumption behind issue 3, which touches every Claude launch including watch)
  runs last, in issue 8. That's the design's intended order, and the merge is
  gated on it, so no unvalidated argv reaches the default branch.
- The edge from issue 7 to issue 1 is conservative, since no functional scenario
  exercises the hook.

## Result

```yaml
critical_findings: []
```

Confidence: high. All eight issue bodies, the four plan artifacts, and the
design's Implementation Approach were read. The dependency claims were checked
against the actual `runDispatch` layout and the existing argv-pinning tests.
