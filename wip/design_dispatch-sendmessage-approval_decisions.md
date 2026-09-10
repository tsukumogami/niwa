# /design Decisions: dispatch-sendmessage-approval

| id | artifact | tier | status | question |
|---|---|---|---|---|
| inline-resolution | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | confirmed | Spawn decider agents, or resolve decisions inline under the parent? |
| d1-settings-builder | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | How does the setting share the one `--settings` slot with remote control? |
| d2-capability-row | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | How is eligibility decided without naming an agent? |
| d3-marker-location | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | Where is the one-time explanation remembered? |
| d4-record-fields | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | How does `niwa list` learn the outcome? |
| cross-validation | wip/design_dispatch-sendmessage-approval_coordination.json | 2 | confirmed | Do the four decisions' assumptions conflict? |
| security-review-deny | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | assumed (high) | Deny cross-session messaging in `niwa watch` review sessions in this PR? |
| security-review-env | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | confirmed | Fix or document the `XDG_CONFIG_HOME`/`HOME` relocation route? |
| arch-attach-timing | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | When does the explanation print when `claude attach` follows? |
| arch-capability-phase | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | confirmed | Where does the capability row land, and at which row number? |
| arch-watch-coverage | docs/prds/PRD-dispatch-sendmessage-approval.md | 2 | assumed (high) | Functional harness for the watch exclusion, or unit coverage with an R18 amendment? |
| arch-small-fixes | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 1 | confirmed | Map vs builder type, directory mode, config-path error, resolver shape, hostGlobal hoist |
| sec6-prompt-separator | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | confirmed | Fix the prompt-as-flag route into the `--settings` slot in this PR? |
| sec6-tool-list | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | confirmed | Which tools does the review-session deny cover, and how is it verified? |
| sec6-out-of-scope | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 1 | confirmed | Inbound refusal into review sessions, `disableAllHooks`, and existing matcher bugs |
| sec6-broader-holds | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | assumed (high) | Accept that `accept` lifts four holds, not only the class mismatch? |
| sec6-unmeasured | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | assumed (high) | Proceed with three Claude Code behaviors unmeasured until the manual check? |
| design-approval | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | assumed (high) | Approve the design (Proposed -> Accepted) and route to /plan? |

<!-- decision:start id="design-approval" status="assumed" priority="high" -->
**Decision:** Approved the design and transitioned it Proposed -> Accepted;
routed to `/plan` (complexity: Complex -- 4+ files, new test infrastructure for
the marker and watch-site tests, a new CLI flag and config key, and changes
across `internal/cli`, `internal/config`, `internal/agentplan`,
`internal/workspace`, and `internal/watch`). **Why assumed:** approval gates are
always assumed under `--auto`; the parent `/scope` owns the approval
(parent-delegated-approval). Basis: architecture, structural-format, and both
security reviews were applied; `shirabe validate` is clean. Residual concerns
for the author are the high-priority assumptions above: the review-session deny
beyond the PRD, unit coverage for the watch exclusion with R18 amended, the four
holds `accept` lifts, and three behaviors left to the manual check.
<!-- decision:end -->

<!-- decision:start id="sec6-prompt-separator" status="confirmed" -->
**Decision:** Set `PromptSeparator: true` on Claude's launch spec in this PR.
Evidence, measured on 2.1.267 with a nonexistent model so no API call ran:
`claude -p --model <bogus> "--version"` printed `2.1.267 (Claude Code)`, so the
prompt element was parsed as a flag; `claude -p --model <bogus> -- "--version"`
reached model resolution with the text as the prompt. `buildLaunchArgs` already
inserts `--` when the spec asks (`dispatch_launcher.go:372`); only Codex asks.
Alternative, refusing prompts that start with `-`, would reject Markdown-list
briefs.
<!-- decision:end -->

<!-- decision:start id="sec6-tool-list" status="confirmed" -->
**Decision:** Deny `SendMessage|SendFile|RemoteTrigger|ListAgents`, verified by
matcher and command. Evidence: all four names (and the `ListPeers` alias) are in
the 2.1.267 binary; `SendFile` delivers through the same peer path;
`RemoteTrigger` deliveries are cross-session inbound and the tool also escapes
review egress containment today. `SendUserFile`, `SendUserMessage`, and
`PushNotification` address the user, not peers, and are left alone.
<!-- decision:end -->

<!-- decision:start id="sec6-out-of-scope" status="confirmed" -->
**Decision:** Leave out of this PR, as review-containment follow-up: refusing
inbound messages into review sessions (not a direction this feature opens),
rejecting `disableAllHooks` in review settings, matcher-and-command verification
for the existing egress and filesystem hooks, the `mcp__` entry in
`egressDenyMatcher` that matches no MCP tool on 2.1.267, and the incorrect
substring-matching comment in `containment.go`. All predate this feature.
<!-- decision:end -->

<!-- decision:start id="sec6-broader-holds" status="assumed" priority="high" -->
**Decision:** Proceed with `accept` as the mechanism, documenting that for a
bypass receiver it lifts the class-mismatch, unattested-sender, bypass-default,
and routine-delivery holds. **Why assumed:** the PRD's acceptance of the risk was
framed around the class mismatch alone; there is no finer-grained receive-side
setting, so the alternative is not shipping the feature. The author should
confirm the broader removal is acceptable.
<!-- decision:end -->

<!-- decision:start id="sec6-unmeasured" status="assumed" priority="high" -->
**Decision:** Proceed with three behaviors unmeasured, each assigned to the
manual delivery check: the permission class of a hard-deny review session on
2.1.258+, whether a non-Claude process can send over the local inbox socket, and
whether review-session subagents are blocked by the hook. **Why assumed:** each
needs a live Claude Code session; the design is correct under either outcome for
the first and third, and the second changes only how the guide describes the
sender set.
<!-- decision:end -->

<!-- decision:start id="arch-attach-timing" status="confirmed" -->
**Decision:** Print the explanation, and create the marker, after
`dispatchAttach` returns when an attach follows; otherwise right after the audit
line. Evidence: Claude's `ResumeDuringTurn` is true, and step 14 attaches unless
`--detach` (`dispatch.go:852-858`), so output at the post-mapping point is
covered by the attached TUI. The audit and override lines stay before step 13,
since they're short and `niwa list` also carries the grant.
<!-- decision:end -->

<!-- decision:start id="arch-capability-phase" status="confirmed" -->
**Decision:** Land the capability row in Phase 3 with the delivery, appended as
row 25, together with the count-test updates, gap-list regeneration, and the
capability-contract PRD amendment. Evidence: `declaration.go:73-75` forbids
declaring an implemented row before it's delivered; `TestAllIsTheClosedSet` and
`TestCodexColumnTotals` pin counts; inserting mid-block renumbers cited rows.
<!-- decision:end -->

<!-- decision:start id="arch-watch-coverage" status="assumed" priority="high" -->
**Decision:** Cover the watch exclusion with a unit test that stubs
`dispatchLaunch` and drives both watch launch sites with the machine key on, and
amend R18 to accept unit coverage for that one case. **Why assumed:** it changes
an accepted PRD requirement. Evidence: no functional feature or step file runs
`niwa watch`; building a `watch --once` harness (PR fake, fake claude, sandbox
off) is a separate project; watch never goes through `runDispatch` and writes no
mapping, so the exclusion also holds by construction.
<!-- decision:end -->

<!-- decision:start id="arch-small-fixes" status="confirmed" -->
**Decision:** Adopted the review's smaller corrections: a plain map plus
`renderLaunchSettings` instead of a builder type; directory mode `0o755` to match
`writeGlobalConfigFile` (`registry.go:344`); on a `GlobalConfigPath()` error,
print the explanation's non-terminal form and skip the marker; the resolver
returns `inboundResolution{on, source, overrodeMachineOn}`; hoist `hostGlobal`
above step 9c; one `inboundApplied` boolean feeds the mapping, audit line, and
explanation; `rcInjected` stays tied to remote control's own decision; set the
mapping field in the literal before `WriteSessionMapping`.
<!-- decision:end -->

<!-- decision:start id="security-review-deny" status="assumed" priority="high" -->
**Decision:** Add a PreToolUse hook denying `SendMessage` to `niwa watch` review
sessions in every containment mode, in this PR. **Why assumed:** it extends the
PRD, which excluded review sessions as receivers but didn't consider them as
senders. Evidence: the egress-deny matcher is `WebFetch|WebSearch|mcp__`
(`internal/watch/containment.go:18`), so messaging isn't contained; the
operator-approval posture runs review sessions in `default` mode, so today the
class mismatch holds their messages to bypass workers, and this feature would
remove that hold. No code or prompt in the repo uses cross-session messaging from
a review session. The change is one matcher constant, one hook, and one
verification line, following the existing always-on post-guard pattern.
Alternative: name it as a follow-up and document the gap; rejected because the
feature itself opens the path.
<!-- decision:end -->

<!-- decision:start id="security-review-env" status="confirmed" -->
**Decision:** Document the `XDG_CONFIG_HOME`/`HOME` relocation route and qualify
the "no other source" guarantee as covering configuration sources, with
rejecting those names in `[claude.env]`/`[session.env]` as follow-up. Evidence:
the route already affects every existing machine-level dispatch key, and a
workspace config able to set the session environment can already install hooks
that run in the session, so it grants nothing new; fixing it belongs to all
machine keys at once.
<!-- decision:end -->

<!-- decision:start id="inline-resolution" status="confirmed" -->
**Decision:** Resolved the four decisions inline rather than spawning decider
agents, per the parent-orchestration fallback "decision-bypass-with-inline-
resolution" for `/design` Phase 2 under `/scope`. Recorded in the design's
frontmatter as `decision_provenance: inline-resolved`.
<!-- decision:end -->

<!-- decision:start id="d1-settings-builder" status="confirmed" -->
**Decision:** A launch settings builder with independent contributors. Evidence:
a repeated `--settings` is last-wins and silent (measured on 2.1.267); project
scope can't grant `accept`; `encoding/json` sorts keys, so remote control's
output stays `{"remoteControlAtStartup":true}`. Rejected: second `--settings`,
widening the remote-control helper, a settings file passed by path (the path
lands in `respawnFlags` and dies with the instance), materializing the key.
<!-- decision:end -->

<!-- decision:start id="d2-capability-row" status="confirmed" -->
**Decision:** A `DispatchInboundAcceptance` capability row, Implemented for
Claude and Unavailable for Codex with a reason, checked together with
`spec.Flags.Settings`. Evidence: remote control and keep-alive are gated the same
way (`dispatch.go:586-587`, `:638-639`), and the dispatch-path AST scan forbids
naming an agent. Rejected: flag-spelling gate alone, reusing `RemoteControl`.
<!-- decision:end -->

<!-- decision:start id="d3-marker-location" status="confirmed" -->
**Decision:** `accept-session-messages-notice` in `filepath.Dir(GlobalConfigPath())`,
created with `O_CREATE|O_EXCL` only when `IsStderrTTY()` is true. Evidence:
`GlobalConfigDir()` returns the overlay clone directory `.../niwa/global`, not
the `config.toml` directory; `SaveGlobalConfigTo` strips comments; dispatch
instances are fresh, so `DisclosedNotices` would re-fire.
<!-- decision:end -->

<!-- decision:start id="d4-record-fields" status="confirmed" -->
**Decision:** `SessionMapping.AcceptsSessionMessages` (omitempty) set at step 11,
and an always-present `InstanceRecord.AcceptsSessionMessages` set in
`annotateFromSessionMappings` without a liveness check. Evidence: `niwa list`
is per instance (`list.go:117`); keep-alive's liveness gate exists because
keep-alive stops with the session, which doesn't apply to an audit of a grant.
<!-- decision:end -->

<!-- decision:start id="cross-validation" status="confirmed" -->
**Decision:** No conflicts. D1 assumes the inbound key is emitted only when the
behavior is deliverable, which D2 provides; D3 and D4 both key off the same
post-mapping "takes effect" point that D1's delivery and step 11 define.
<!-- decision:end -->
