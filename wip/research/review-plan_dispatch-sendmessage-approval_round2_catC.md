# Category C (AC Discriminability) review, round 2: dispatch-sendmessage-approval

Input type: design. Execution mode: single-pr. Issues 1, 4, 7 and 8 were regenerated
after round 1; issues 2, 3, 5 and 6 were kept and re-read for context.

## Round-1 findings

### (a) Watch-site test observes the real argv, and issue 4 states confinement as a tested outcome: resolved

Issue 7's watch-site unit test now leaves `dispatchLaunch` set to `realDispatchLaunch`
and doesn't replace `realDispatchLaunch` or `buildLaunchArgs`. A fake `claude` placed
first on `PATH` records every argv element NUL-separated. I checked this against the
code. Both watch sites (`continueReview` near watch.go:581 and `stageReview` near
watch.go:844) launch with `claudeLaunchSpec().Runner.ModeFor(true)`, the backgrounded
mode. In that mode `realDispatchLaunch` resolves the binary with `exec.LookPath`
(dispatch_launcher.go:129), builds the argv with `buildLaunchArgs` (:134), and runs it
with `exec.CommandContext(...).Run()` (:152). A fake binary on `PATH` therefore
receives exactly the final argv.

The setup uses both triggers a wrong implementation could read: the machine key is
`true` through `XDG_CONFIG_HOME`, and the package-level flag variable is set on. So
either way of adding the key below `runDispatch` gets caught. The test can't pass
vacuously either. It asserts one `--bg` invocation per site, `--resume <id>` at the
continue site, and the site's prompt as the last element. The fallback, a seam at the
exec/start point below `buildLaunchArgs`, keeps the same observation point.

Issue 4's "Output and scope" AC now describes the confinement as the outcome that test
checks: the real `dispatchLaunch`, both watch sites, argv below `buildLaunchArgs`, no
`crossSessionInbound`. It also requires issue 4's implementation to pass that test
without changes to it.

### (b) Post-launch failure ACs for the audit and override lines: resolved

Issue 4 now has three failure cases: launch, capture, and mapping write. Each one runs
with the behavior on (machine key `true` with no flag, and separately
`--accept-session-messages`). Each also runs with the machine key `true` and
`--accept-session-messages=false`, which is the only setup where a stray override line
could show. Each case asserts that `runDispatch` returns an error, that stderr carries
neither line, that the destroy fake is called, and that no mapping file exists.

I checked the triggers against the code:

- The capture error wraps `capturing dispatch session id` (dispatch.go:753), and
  Claude's backgrounded mode takes the error-return branch, not the foreground
  keep-instance branch.
- A non-UUID session id is rejected in `sessionMappingPath` before any `MkdirAll` or
  write (session_map.go:114-129). Nothing in `runDispatch` validates the id before
  step 11, so the rejection really happens inside `WriteSessionMapping`.
- A `.niwa/sessions` that's a regular file makes `MkdirAll` fail.
- The mapping-write error wraps `writing dispatch session mapping` (dispatch.go:791).

An implementation that prints the audit line right after `dispatchLaunch` returns now
fails both the capture case and the mapping-write case.

### (c) Real review-session refusal check that blocks the merge: resolved

Issue 8 has a "Review-session refusal check, which blocks the merge". It runs a fresh
`niwa watch` stage in the sandbox posture and again with `watch_sandbox = off`, against
a running accepting worker. Before the attempt it confirms that a plain `claude --bg`
sender can deliver to that worker, which rules out a false pass where nothing could
have arrived anyway. It then requires three things: the `SendMessage` tool result
carries the refusal text, `ListAgents` is refused with no session list returned, and
`claude logs <worker>` shows nothing within 60 seconds. Results go in the PR
description. If a call gets through, the matcher is fixed in this PR and the check is
repeated. Issue 1 names this as a "Must deliver" item that issue 8 verifies, and says
the same in its complexity rationale, Context, and Downstream Dependencies.

### (d) Four parallel dispatches from a terminal: resolved

Issue 7 adds a `@critical` scenario, run 4 times in parallel under a pty against one
configuration directory with no marker. The scenario asserts the marker is absent
before the runs.

The fake claude mints a distinct session id per launch. That's needed because the
existing fake's single `FAKE_CLAUDE_SESSION_ID` (dispatch_steps_test.go:51) would make
three of the four captures miss.

After the runs, the scenario asserts:

- all four exit 0;
- the marker exists;
- at least one transcript has the explanation;
- each transcript has exactly one audit line;
- there are four accepting mappings with distinct ids;
- `config.toml` is byte-for-byte unchanged.

Each plausible race fails one of these. An `O_EXCL` loser that errors fails "exit 0",
a lock that hangs fails the step timeout, and a write back to `config.toml` fails the
byte comparison. A 5-consecutive-run stability requirement is added under suite health.
This matches the PRD's automated acceptance criterion at PRD line 391.

## New problems introduced by the regenerated bodies

I ran the pattern pass (patterns 1, 3, 7) and the adversarial pass (patterns 2, 4, 5,
6) over issues 1, 4, 7 and 8. Neither turned up a new critical problem.

- Pattern 3: every issue has failure-path ACs.
- Pattern 7: the "exists" ACs in issue 8 (the guide file) and issue 7 (the marker)
  come with content assertions, so they aren't existence-only.
- Pattern 2: issue 4's precedence matrix still inspects the request handed to a
  stubbed `dispatchLaunch`. That's acceptable now, because issue 7 checks the recorded
  argv of the real binary for the same matrix.
- Pattern 6: issue 7 deliberately differs from the design's component note ("stub
  `dispatchLaunch`") and says so. That's stricter than the design, not name drift.

One non-critical note, which isn't a Category C finding. Issue 7's step
`there are (\d+) dispatch mappings that record session-message acceptance` is worded
as counting `.niwa/sessions/*.json` "across the workspace's instances". Dispatch
mappings live at the workspace root's `.niwa/sessions/` (`sessionsDir`,
session_map.go:107). A step written per instance would make a correct implementation
fail, not let a wrong one pass, so it doesn't affect discriminability. Whoever
implements the step should read the workspace-root directory.

## Findings

```yaml
critical_findings: []
```
