# /design Decisions: dispatch-sendmessage-approval

| id | artifact | tier | status | question |
|---|---|---|---|---|
| inline-resolution | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 2 | confirmed | Spawn decider agents, or resolve decisions inline under the parent? |
| d1-settings-builder | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | How does the setting share the one `--settings` slot with remote control? |
| d2-capability-row | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | How is eligibility decided without naming an agent? |
| d3-marker-location | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | Where is the one-time explanation remembered? |
| d4-record-fields | docs/designs/DESIGN-dispatch-sendmessage-approval.md | 3 | confirmed | How does `niwa list` learn the outcome? |
| cross-validation | wip/design_dispatch-sendmessage-approval_coordination.json | 2 | confirmed | Do the four decisions' assumptions conflict? |

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
