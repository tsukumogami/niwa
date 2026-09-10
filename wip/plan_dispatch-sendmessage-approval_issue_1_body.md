---
complexity: critical
complexity_rationale: It changes the containment that `niwa watch` applies to review sessions reading untrusted pull requests, and a wrong matcher, a hook that can be impersonated, or a verification gap would leave a contained reviewer able to instruct an uncontained, bypass-mode worker.
---

## Goal

Add a PreToolUse hook to every `niwa watch` review session, in every containment mode, that denies the Claude Code tools able to reach other sessions (`SendMessage`, `SendFile`, `RemoteTrigger`, `ListAgents`). Verify the hook by matcher and by command before any review launches.

## Context

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md`

This feature lets `niwa dispatch` launch workers with `crossSessionInbound: "accept"`. Today, a message from a review session to a bypass-mode worker is held by Claude Code, because watch passes no `--permission-mode` and review sessions most likely run in a prompting permission-mode class. An accepting worker removes that hold, so a contained reviewer could hand instructions to an uncontained worker. Review containment doesn't cover this channel. The egress-deny hook in `internal/watch/containment.go` matches only `WebFetch|WebSearch|mcp__` and is applied only in sandbox mode. The session-reaching tools run on the Claude Code process's own connection or its local unix-socket inbox, which the OS sandbox doesn't cage.

Decision 5 of the design closes the gap with a separate deny hook applied in every mode, like the Bash posting guard (`postGuardHook`). The design rejected widening `egressDenyMatcher`: that hook is sandbox-only, and changing its matcher would change its dedupe identity. Verification checks the command as well as the matcher, so a workspace or overlay hook that reuses the matcher with a no-op command can't stand in for niwa's. Denying `RemoteTrigger` also closes a gap that predates this feature: a sandboxed review could create and run remote routines.

Two routes stay outside the hook. First, in sandbox mode Bash is kept off the local inbox only because niwa's no-egress stanza leaves unix sockets disallowed, so the stanza must keep doing that. Second, with `watch_sandbox = off` Bash has full access, and the hook is accident prevention only.

This issue stands alone and is Phase 7 of the design's implementation approach. Both watch launch paths already call `watch.ApplyReviewSettings` and return its error before launching: the fresh stage in `internal/cli/watch.go` (around line 831) and the resume re-assert (around line 557). So no change is needed outside `internal/watch`.

## Acceptance Criteria

- [ ] `internal/watch/containment.go` defines `sessionReachDenyMatcher = "SendMessage|SendFile|RemoteTrigger|ListAgents"`. Its doc comment names the four tools, says the list comes from Claude Code 2.1.267's tool list, and says the matcher uses only letters and `|`, so Claude Code compares it as an exact list of tool names (aliases included, so `ListPeers` resolves to `ListAgents`), not as a substring.
- [ ] `sessionReachDenyHook()` returns a PreToolUse entry with matcher `sessionReachDenyMatcher` and one `"type": "command"` hook. The command writes exactly `niwa watch: review sessions don't reach other sessions` to stderr and exits 2. The command must survive the apostrophe in "don't". A unit test runs the command the applied settings contain with `sh -c`, feeds it a PreToolUse payload for each of the four tools, and asserts exit code 2 and that stderr contains the message.
- [ ] `ApplyReviewSettings` appends `sessionReachDenyHook()` in every combination of `sandbox` and `ask`: (false, false), (true, false), and (true, true). It skips the append only when an entry with the same matcher and the same command is already present. Checking the matcher alone isn't enough. `preToolUseHasMatcher` keeps its current matcher-only behavior for the existing hooks; the matcher-and-command check is a new helper.
- [ ] `VerifyReviewSettings(merged, sandbox, ask)` returns an error in every mode when no PreToolUse entry has both matcher `sessionReachDenyMatcher` and niwa's exact deny command. The error names the matcher, in the same `review settings check: ... PreToolUse hook (matcher %q) missing` form the other checks use. Because both watch call sites return this error before launch, a dropped hook stops the review launch.
- [ ] New unit test: for each of the three `(sandbox, ask)` combinations, a fresh `ApplyReviewSettings` produces settings where `countPreToolUseMatcher(t, got, sessionReachDenyMatcher)` is 1 and `VerifyReviewSettings` passes.
- [ ] New unit test: a settings file that already holds a PreToolUse entry with matcher `SendMessage|SendFile|RemoteTrigger|ListAgents` and a different command (for example `exit 0`) keeps that entry, gains niwa's deny hook as a second entry with the same matcher, and verifies. A hand-built document holding only the impostor entry fails `VerifyReviewSettings` in every mode.
- [ ] New unit test: applying twice leaves exactly one deny-hook entry (extend `TestApplyReviewSettings_DedupesHooks` or add a sibling). Applying to a settings file written in the pre-feature shape (post-guard, egress-deny, and filesystem-guard hooks, no deny hook) adds the hook. This covers the resume re-assert for reviews staged before the upgrade.
- [ ] The existing tests that build settings documents by hand and expect them to verify (`TestVerifyReviewSettings_RequiresAskPostureBits`, `TestVerifyReviewSettings_RejectsRelaxations`, the non-sandbox halves of `TestVerifyReviewSettings_RequiresEgressDeny` and `TestVerifyReviewSettings_RequiresFSGuard`) include `sessionReachDenyHook()` in their fixtures. Each test still fails only for the condition its name describes. The existing assertions in `TestApplyReviewSettings_NoSandbox`, `TestApplyReviewSettings_Sandbox`, `TestApplyReviewSettings_AskPosture`, and `TestApplyReviewSettings_HardDenyPostureUnchanged` still hold, and each also asserts the deny hook is present exactly once.
- [ ] `egressDenyMatcher` stays `"WebFetch|WebSearch|mcp__"`, and the egress-deny hook stays sandbox-only. `postGuardMatcher`, `fsGuardMatcher`, and `autoAllowMatcher` and their hooks are unchanged.
- [ ] `noEgressSandboxStanza` gains a comment saying the stanza must keep unix sockets disallowed, because that's what keeps Bash in a sandboxed review off Claude Code's local cross-session inbox. A unit test asserts the stanza's `network` map holds only `allowedDomains` (empty) and no unix-socket allowance.
- [ ] The doc comments on `ApplyReviewSettings` and `VerifyReviewSettings` describe the new hook as applied and required in every mode, keyed on matcher and command.
- [ ] `go test ./...` and `go vet ./...` pass, and `gofmt -l internal/watch` prints nothing.
- [ ] Must deliver: the deny hook applied by both watch launch paths, together with `sessionReachDenyMatcher` and the refusal message as stable strings in `internal/watch/containment.go`. The end-to-end coverage can then rely on review-session containment already being in place, and doesn't need to restate it (required by <<ISSUE:7>>).
- [ ] Must deliver: the denied tool list (`SendMessage`, `SendFile`, `RemoteTrigger`, `ListAgents`) and the Claude Code version it was taken from (2.1.267), recorded in the `sessionReachDenyMatcher` doc comment, plus the hook's stated limits: with `watch_sandbox = off` it's accident prevention only, it relies on the stanza keeping unix sockets disallowed, it applies only after watch re-stages or resumes a review, and `disableAllHooks` turns it off. The guide copies these (required by <<ISSUE:8>>).

## Dependencies

None

## Downstream Dependencies

<<ISSUE:7>> covers dispatch session-message acceptance end to end. It needs review sessions already denied the session-reaching tools, so accepting workers open no channel from a contained reviewer, and it needs the matcher constant and refusal message to be stable. Its watch-side coverage is the unit test at both watch launch sites, not a functional scenario.

<<ISSUE:8>> writes the user guide and records the manual check. The guide names the denied tool list and the Claude Code version the list came from. It explains the hook's limits (no-sandbox mode, the unix-socket dependency, reviews staged before the upgrade, `disableAllHooks`). The manual check compares Claude Code's current tool list against `sessionReachDenyMatcher` and confirms whether the hook also blocks a review session's subagents.
