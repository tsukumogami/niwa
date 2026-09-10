---
review_result:
  verdict: "loop-back"
  loop_target: 4
  round: 1
  confidence: "high"
  critical_findings:
    - category: "C"
      description: "Issue 7, watch-site unit test: mock-swallowed. The test stubs dispatchLaunch and inspects only the captured launchRequest, but the final Claude argv is built inside the real dispatchLaunch (realDispatchLaunch -> buildLaunchArgs, dispatch_launcher.go:134). An implementation that adds crossSessionInbound inside realDispatchLaunch or buildLaunchArgs, for example by reading the package-level flag variable, gives every niwa watch launch the key while the stubbed test passes. Issue 4's claim that the new code is confined to runDispatch and dispatch_inbound.go is a code-structure claim with no test, so R14 has no AC that would catch its most plausible violation."
      affected_issue_ids: [7, 4]
      correction_hint: "Observe the argv Claude would actually receive at both watch launch sites, not the request handed to a stub: keep the real dispatchLaunch and put a fake claude binary on PATH that records its argv, or stub below buildLaunchArgs at the exec/start seam. Assert no argv element at either site contains crossSessionInbound while the machine key is true and the flag variable is on. Issue 4 should state the confinement as this tested outcome rather than as a file-location claim."
    - category: "C"
      description: "Issue 4, stderr-line ACs: missing negative cases. The audit and override lines must follow step 12, but the only negative AC is a dispatchLaunch error. runDispatch can also fail after a successful launch, at capture (step 10) or WriteSessionMapping (step 11), both before step 12. An implementation that prints the audit line right after dispatchLaunch returns passes every issue 4 AC, issue 6's ordering, and issue 7's launch-failure scenario, while claiming acceptance for a dispatch that rolled back with no mapping (design D6)."
      affected_issue_ids: [4]
      correction_hint: "Extend the failure ACs to the post-launch failure points. With the behavior on, and separately with the machine key true and --accept-session-messages=false for the override line, make the capture stub fail, and separately make WriteSessionMapping fail. Assert runDispatch returns an error, stderr has no audit line and no override line, and no mapping records acceptance."
    - category: "C"
      description: "Issues 1 and 8, review-session deny hook: integration scope gap. Issue 1's ACs check matcher and command strings and run the command with sh -c; none shows Claude Code applies the matcher as the design assumes or that the hook blocks the tools in a real review session. Issue 8's manual session measures the subagent case and compares tool lists, and none of the PRD's delivery cases involves a review session. No AC requires observing a real niwa watch review session's SendMessage or ListAgents call being refused, and no outcome blocks the merge if it isn't."
      affected_issue_ids: [8, 1]
      correction_hint: "Add a merge-blocking manual-check measurement to issue 8: in a real niwa watch review session, in the sandbox posture and with watch_sandbox = off, have the session attempt SendMessage to an accepting dispatched worker and attempt ListAgents; confirm each call is refused with 'niwa watch: review sessions don't reach other sessions' and nothing reaches the worker; record the result in the PR description; if either call gets through, fix the matcher before merge. Issue 1 should name this as a downstream deliverable issue 8 verifies."
    - category: "C"
      description: "Issue 7: missing coverage of a PRD automated acceptance criterion. The PRD requires four dispatches run in parallel from a terminal, with the behavior in effect and no marker, all to succeed, with the marker existing afterwards and the explanation printed at least once. Issue 6 tests concurrency only inside the helper with goroutines, and issue 7 has no parallel-dispatch scenario, so a marker race or lock in the real binary would pass every AC. Surfaced as a note by the design-fidelity reviewer."
      affected_issue_ids: [7]
      correction_hint: "Add a scenario that runs four niwa dispatch --accept-session-messages --detach processes in parallel under ptys against one configuration directory with no marker, and asserts all four exit 0, the marker exists afterwards, and at least one transcript contains the explanation."
  summary: "Loop-back required at Phase 4. Issues 1, 4, 7 and 8 have acceptance criteria that would pass for a wrong implementation (stubbed watch-site test, missing post-launch failure cases, no real review-session deny check, missing parallel-dispatch scenario); correction hints provided. Categories A, B and D found nothing critical."
---

# Plan Review: dispatch-sendmessage-approval

Round 1 review result: loop-back at Phase 4.

Issues 1, 4, 7 and 8 have acceptance criteria that would pass for a wrong
implementation; correction hints are provided. Scope, design fidelity and
sequencing found nothing critical.
