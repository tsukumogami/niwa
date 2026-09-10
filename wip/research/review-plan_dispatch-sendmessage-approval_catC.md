# Category C (AC Discriminability) review: dispatch-sendmessage-approval, round 1

Mode: fast-path. Input type: design (pattern 6 checked against
docs/designs/DESIGN-dispatch-sendmessage-approval.md). Execution mode: single-pr.

## Pass 1: pattern pass

- Pattern 1 (fixture-anchored): no AC uses the trigger phrases ("fixture data",
  "test data", "sample data", "seed data", "pre-populated", "all fixture"). Issue 1's
  "include `sessionReachDenyHook()` in their fixtures" and issue 5's "legacy-mapping
  fixture" are hand-built documents whose tests also assert the failure condition,
  not pre-populated state that masks the operation. No finding.
- Pattern 3 (happy-path only): every issue has at least one failure or negative AC.
  Issue 1 has the impostor-hook and verify-error ACs. Issue 2 has the mistyped-value
  parse errors. Issue 3 has the empty-map case and the `--settings=` prompt. Issue 4
  has the launch-error, Codex, and unreadable-config ACs. Issue 5 has the
  empty/unreadable store. Issue 6 has the unwritable, unsearchable, dangling-symlink,
  and failed-launch cases. Issue 7 has launch failure and the unreadable config.
  Issue 8 has "a case with any other outcome blocks the merge". No finding.
- Pattern 7 (existence-without-correctness): issue 8 AC1 ("the guide exists") sits
  next to content ACs for the same file. Issue 6 and issue 7 marker-exists ACs come
  with regular-file, empty, and `0o600` assertions, and the marker's content is empty
  by design. No finding.

## Pass 2: adversarial pass

Issues 2, 3, 5, and 6 held up. Their ACs pin byte-level output, wire shape
(omitempty on one side, always present on the other), the no-liveness rule (a
mapping with no live job still reports true), the Codex-with-flag case, which
catches a record recomputed from the flag, and the attach timing (stderr and the
marker are inspected at the moment `dispatchAttach` is called). Pattern 6 found no
drift. `renderLaunchSettings`, `resolveDispatchInboundAcceptance`,
`inboundResolution`, `showInboundExplanation`, `sessionReachDenyMatcher`,
`sessionReachDenyHook`, `DispatchInboundAcceptance`, and `CrossSessionInboundKey`
match the design everywhere. Issue 1 AC4's claim that both watch call sites fail on
a dropped hook holds: `ApplyReviewSettings` ends with
`return VerifyReviewSettings(merged, sandbox, ask)` (containment.go:278), and both
watch sites return that error before calling `dispatchLaunch`.

There are three gaps.

1. **Issue 7, watch-site unit test: mock-swallowed (pattern 2).** The test replaces
   `dispatchLaunch` with a stub and inspects the captured `launchRequest`. The argv
   Claude actually receives is built inside the real `dispatchLaunch`
   (`realDispatchLaunch` calls `buildLaunchArgs(spec, req.Mode, instanceDir,
   prompt, req.Passthrough)`, dispatch_launcher.go:134). The design names this exact
   wrong implementation ("nothing moves into `dispatchLaunch`, which `niwa watch`
   also calls"). An implementation that adds `crossSessionInbound` inside
   `realDispatchLaunch` or `buildLaunchArgs`, for example by reading the
   package-level flag variable the test deliberately sets to on, puts the key into
   every watch launch. The stubbed test still passes, because the stub never runs
   that code. Issue 4's matching AC ("the new code is confined to `runDispatch` and
   `dispatch_inbound.go`") describes code structure and has no test behind it. The
   one guard for R14 is blind to the most plausible way of breaking R14.

2. **Issue 4, stderr lines: missing negative cases between launch and step 12.**
   The AC places the audit and override lines after step 12. Its only negative test
   is a `dispatchLaunch` error. `runDispatch` can also fail after a successful
   launch, at capture (step 10) or `WriteSessionMapping` (step 11), and both return
   before step 12. An implementation that prints the audit line right after
   `dispatchLaunch` returns passes every issue 4 AC. It also passes issue 6's
   "directly after the audit line" ordering, since nothing else writes to stderr on
   that path, and issue 7's launch-failure scenario. It then reports "this worker
   accepts messages" for a dispatch that rolled back with no mapping and no record.
   That breaks D6 ("nothing before a successful launch ... whose session niwa
   recorded") and makes the audit line disagree with `niwa list`. Issue 6 already
   tests the capture and mapping failures for the explanation, but not for the
   audit or override line.

3. **Issues 1 and 8, review-session deny hook: integration scope gap (pattern 5).**
   Every issue 1 AC checks strings in a settings document, and the sh -c test only
   proves the command exits 2. None of them can show that Claude Code applies the
   matcher the way the design assumes: an exact list of tool names, aliases included
   (the design says this was "read from the 2.1.267 bundle"), and a hook that exits
   2 and blocks these tools. A matcher Claude Code treated differently, or tool
   names that don't match, would pass every unit AC. The only real-Claude check is
   issue 8's manual session. Its measurements cover the subagent case and a
   tool-list comparison, and none of the PRD's delivery cases 0 to 4 involves a
   review session. No AC requires observing that a real `niwa watch` review
   session's own `SendMessage` (or `ListAgents`) call is refused with niwa's
   message, and no outcome blocks the merge if it isn't. This is the feature's
   one preventive control for the channel it opens, and its core behavior has no
   discriminating AC.

## Findings

```yaml
critical_findings:
  - category: "C"
    description: "Issue 7, watch-site unit test AC: mock-swallowed (pattern 2). The test stubs dispatchLaunch and inspects only the captured launchRequest (Passthrough and Body), but the final Claude argv is built inside the real dispatchLaunch (realDispatchLaunch -> buildLaunchArgs, dispatch_launcher.go:134). A wrong implementation that adds crossSessionInbound inside realDispatchLaunch or buildLaunchArgs (for example, reading the package-level flag variable the test sets to on) gives every niwa watch launch the key, while the stubbed test still passes. Issue 4's AC that the new code is 'confined to runDispatch and dispatch_inbound.go' is a code-structure claim with no test, so R14 has no AC that would catch its most plausible violation."
    affected_issue_ids: [7, 4]
    correction_hint: "Observe the argv Claude would actually receive at both watch launch sites, not the request handed to a stub. Leave the real dispatchLaunch in place and put a fake claude binary on PATH that records its argv, as the functional fake does; or stub below buildLaunchArgs, at the exec/start seam. Then assert that no argv element at either site contains crossSessionInbound while the machine key is true and the flag variable is on. Issue 4 should state the confinement as this tested outcome rather than as a file-location claim."
  - category: "C"
    description: "Issue 4, stderr-line ACs: missing negative cases (state-without-transition, pattern 4). The audit and override lines must come after step 12 (mapping written, rollback disarmed), but the only negative AC is a dispatchLaunch error. runDispatch can also fail after a successful launch, at capture (step 10) or WriteSessionMapping (step 11), and both return before step 12. An implementation that prints the audit line right after dispatchLaunch returns passes every issue 4 AC, issue 6's 'directly after the audit line' ordering, and issue 7's launch-failure scenario. Yet it claims 'this worker accepts messages' for a dispatch that rolled back with no mapping, which breaks the design's D6 and contradicts niwa list."
    affected_issue_ids: [4]
    correction_hint: "Extend the failure AC to the post-launch failure points. With the behavior on (and separately with the machine key true and --accept-session-messages=false for the override line), make the capture stub fail, and separately make WriteSessionMapping fail. Assert that runDispatch returns an error, stderr has no audit line and no override line, and no mapping records acceptance. Mirror the capture and mapping-failure cases issue 6 already specifies for the explanation."
  - category: "C"
    description: "Issues 1 and 8, review-session deny hook: integration scope gap (pattern 5). Issue 1's ACs check matcher and command strings in a settings document and run the command with sh -c. None of them can show that Claude Code applies the matcher as the design assumes (an exact list of tool names with aliases, read from the 2.1.267 bundle) or that the hook actually blocks these tools in a real review session. A matcher Claude Code treated differently would pass every unit AC. Issue 8's manual session measures the subagent case and compares tool lists, and none of the PRD's delivery cases 0 to 4 involves a review session. No AC requires observing a real niwa watch review session's own SendMessage or ListAgents call being refused with niwa's message, and no outcome blocks the merge if it isn't. This is the feature's only preventive control for the channel it opens, and nothing tests it end to end."
    affected_issue_ids: [8, 1]
    correction_hint: "Add a merge-blocking manual-check measurement to issue 8. In a real niwa watch review session, in the sandbox posture and with watch_sandbox = off, have the session attempt SendMessage to an accepting dispatched worker and attempt ListAgents. Confirm that each call is refused with 'niwa watch: review sessions don't reach other sessions' and that nothing reaches the worker. Record the result in the PR description, and if either call gets through, fix the matcher before merge."
```
