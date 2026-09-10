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
