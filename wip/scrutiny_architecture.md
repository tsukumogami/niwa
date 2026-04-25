# Architecture Review — `docs/cross-session-communication`

Scope: MCP-call audit (Issue 12), daemon inbox-ownership fix, graph-e2e tightening.
Greenfield branch — no prior `main` baseline for "established convention."

## 1. AuditSink interface lives at the right level — OK as-is

`internal/mcp/audit.go` defines `AuditSink` and `fileAuditSink` inside the same
package as the dispatch loop that emits to it. The seam is at the right place:
the producer (server.go dispatch) and the consumer schema (`AuditEntry`) are
co-located, and `Server.SetAuditSink` is the single test injection point.
`ReadAuditLog` / `FilterAudit` ship as a sibling reader API rather than living
in tests, so the functional-test mirror in `mesh_steps_test.go` is a comment
choice (shallow dep graph), not a missing seam.

## 2. Functional-test audit mirror duplicates the schema — Advisory

`test/functional/mesh_steps_test.go` (lines ~1357-1412) re-declares
`auditEntry` / `filterAudit` / `mcpReadAuditLog` to avoid importing
`internal/mcp`. The comment justifies it ("keeps the test dependency graph
shallow"), but the schema is now duplicated three places (the Go struct, the
NDJSON tag set, and this mirror). If `AuditEntry` gains a field, the mirror
silently keeps the old shape. Recommend exporting a `mcptest` (or
`mcp/audittest`) sub-package with the read-only helpers; the functional test
imports that, dependency stays one-way. Not blocking — three places is
still tractable, and the JSON-tag drift would surface in a feature test.

## 3. Audit emission coupling at server.go dispatch — OK as-is

`server.go:170` calls `s.audit.Emit(buildAuditEntry(...))` exactly once, in
the `tools/call` arm, after `callTool` returns. Adding tool 12 means adding
one case to `callTool`'s switch; the audit path requires no change. The
emission is at the dispatch boundary, not inside individual handlers, so
the "doesn't compound" criterion is met.

## 4. Two watchers on one inbox dir — Advisory, watch for drift

The daemon (`mesh_watch.go::handleInboxEvent`) and the MCP server
(`internal/mcp/watcher.go::watchRoleInbox`) both fsnotify the same
`.niwa/roles/<role>/inbox/` directory. `daemonOwnsInboxFile` partitions by
peeking at body `type`. This works, but the "ownership predicate" is
implicit cross-package coupling: the daemon's filter and the MCP server's
`taskTerminalKind` plus reply-waiter logic must remain disjoint. Today they
are (delegate vs terminal-events/asks), but a new message type added to
either side without checking the other will race.

Recommendation: factor the type-classification into a single function in
`internal/mcp` (e.g., `MessageOwner(type) Owner`) that both consumers call.
Today's `daemonOwnsInboxFile` reads the file from disk in `cli` and
re-derives ownership; that decision belongs next to the message-type
constants in `mcp`. Not blocking because the test in
`mesh_watch_test.go::TestDaemonOwnsInboxFile_DelegatesAndPeerMessages`
enumerates the full known set, but the test itself is the seam — adding
type 8 means remembering to extend that table.

## 5. `cli` package depends on `internal/mcp` — OK as-is

`mesh_watch.go` imports `internal/mcp` for `UpdateState`, `ReadState`,
`IsPIDAlive`, `TaskState*` constants. Direction is correct: cli is the
higher-level orchestrator and mcp is the lower-level state/protocol layer.
No reverse imports observed.

## 6. Filename-as-task-id legacy branch in `daemonOwnsInboxFile` — Advisory

The function accepts three legacy shapes (lines 710-720). On a
greenfield branch with no main precedent, "legacy" is aspirational — it
exists only because pre-Issue-3 envelopes in test fixtures didn't set
`type`. Recommend deleting the legacy branches before this branch lands;
they widen the daemon's claim surface and the fallback semantics
(filename-task_id match) is exactly the racy behavior the fix is closing.
If kept, document which on-disk artifacts produce them — otherwise this is
forward-looking ambiguity, not backward compatibility.

## 7. Audit error-code regex couples to error-text format — Advisory

`extractErrorCode` parses the leading ALL_CAPS token from
`toolResult.Content[0].Text`. The actual error string format
(`"error_code: X\ndetail: Y"` from `errResultCode`) is parsed by both this
regex and the `errorCodeFromText` string-search helper in `server.go:808`.
Two parsers for one wire format. Recommend: `errResultCode` should set a
structured field on `toolResult` that both consumers read, and the regex
becomes a fallback for free-form errors. Not blocking — the regex
correctly handles both shapes today — but it's a parallel-pattern smell.

## Summary of severities

- Blocking: none.
- Advisory: items 2, 4, 6, 7. Items 4 and 6 are the ones most likely to
  bite a future contributor; the others are containment.
