# Documentation Plan: Cross-Session Communication

Generated from: docs/plans/PLAN-cross-session-communication.md
Issues analyzed: 5
Total entries: 6

---

## doc-1: docs/designs/DESIGN-cross-session-communication.md
**Section**: Status
**Prerequisite issues**: #1, #2, #3, #4, #5
**Update type**: modify
**Status**: updated
**Details**: Change `status: Planned` frontmatter field and the `## Status` body heading to `Implemented` once all five issues are complete.

---

## doc-2: README.md
**Section**: Commands
**Prerequisite issues**: #4
**Update type**: modify
**Status**: updated
**Details**: Add `niwa mesh watch` and `niwa destroy` (with daemon-stop behavior) to the commands table. The mesh watch entry should describe it as the background daemon started automatically by `niwa apply` when `[channels.mesh]` is configured; `niwa destroy` already exists but needs a note that it stops the daemon before removing the instance.

---

## doc-3: docs/guides/cross-session-communication.md
**Section**: (new file)
**Prerequisite issues**: #1, #2, #3, #4, #5
**Update type**: new
**Status**: updated
**Details**: New guide covering: what the session mesh is and when to use it; how to enable it (adding `[channels.mesh]` to workspace.toml); what `niwa apply` provisions (sessions dir, sessions.json, .mcp.json, ## Channels section in workspace-context.md, daemon); the four MCP tools (niwa_check_messages, niwa_send_message, niwa_ask, niwa_wait) with usage examples; daemon lifecycle (started by apply, stopped by destroy, restarted by re-running apply); the `niwa destroy` requirement for instances with mesh (do not use `rm -rf`); graceful degradation when Claude session ID discovery fails; and the busy-session tradeoff (niwa_ask timeout behavior).

---

## doc-4: docs/guides/functional-testing.md
**Section**: (multiple sections)
**Prerequisite issues**: #1, #2, #4, #5
**Update type**: modify
**Status**: updated
**Details**: Add a section documenting the test patterns introduced by the mesh scenarios: how to fake the sessions directory layout, how to test MCP tool behavior in functional scenarios, and how to set up multi-role inbox scenarios in Gherkin. The guide's step table and sandbox description may also need updates if new step definitions or sandbox setup is added for mesh tests (e.g., steps that create sessions.json fixtures or check daemon.pid).

---

## doc-5: docs/guides/one-time-notices.md
**Section**: Existing notice keys
**Prerequisite issues**: #2
**Update type**: modify
**Status**: skipped — no new notice keys added in Issue 2; only noticeProviderShadow exists in apply.go
**Details**: If `InstallChannelInfrastructure` at step 4.75 introduces any new one-time notice keys (e.g., a notice that fires the first time mesh infrastructure is provisioned), add them to the "Existing notice keys" table with their condition and source file. Skip this entry if no new notice keys are added in Issue 2.

---

## doc-6: docs/guides/cross-session-communication.md
**Section**: Configuration reference
**Prerequisite issues**: #1, #2
**Update type**: new
**Status**: updated — merged into doc-3 (cross-session-communication.md)
**Details**: Within the new guide (doc-3), a config reference subsection documenting the `[channels.mesh]` TOML block: the `roles` map (role name → session UUID or "auto"), the `message_ttl` field (default 24h), and a minimal working example. This section can be written as soon as Issues 1 and 2 are complete — before the daemon and blocking tools land — so it doesn't block on doc-3's full prerequisite set. Split from doc-3 so it can be written earlier and merged into doc-3 when the full guide is ready.
