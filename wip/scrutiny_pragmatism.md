# Pragmatism / YAGNI review — `docs/cross-session-communication`

Brief: "simplest thing that works, no flake, room to grow later." The audit
slice is largely sound but ships several speculative knobs not exercised by
any current caller. The daemon fix is fine. The functional test layer
duplicates code that the package already exports.

## Blocking

**1. `AuditSink` interface has one production impl + a nop**
`internal/mcp/audit.go:44` — `AuditSink` interface, `nopAuditSink`,
`fileAuditSink`, `SetAuditSink`. Tests only ever swap to `nopAuditSink` (via
`SetAuditSink(nil)`) or read the on-disk log. No recording sink, no second
backend. Today this is one concrete writer behind an interface that no
caller benefits from.
**Fix:** delete `AuditSink` and `SetAuditSink`; keep `*fileAuditSink` as a
concrete type on `Server.audit`. If a test ever needs to disable it, set
the path to `os.DevNull` or skip emission when `path == ""`.

**2. Functional test re-implements `audit_reader.go`**
`test/functional/mesh_steps_test.go:1357-1412` — `auditEntry`,
`auditFilter`, `mcpReadAuditLog`, `filterAudit` are a verbatim re-do of
exported symbols in `internal/mcp/audit_reader.go`. The comment claims
"keeps the test dependency graph shallow," but the test already imports
plenty of `internal/*` indirectly via the binary it spawns, and a test
package importing an `internal/` package in the same module is allowed.
**Fix:** `import "github.com/tsukumogami/niwa/internal/mcp"` in the
functional test and call `mcp.ReadAuditLog` / `mcp.FilterAudit` directly.
Delete the four duplicates.

## Advisory

**3. `AuditFilter.OK *bool` and `HasKey`**
`internal/mcp/audit_reader.go:65-71` — five fields, one `*bool`. Live
callers pass only `{Role, Tool, OK: &true}`. `TaskID` and `HasKey` are
unused outside the filter's own unit test.
**Fix:** drop `TaskID` and `HasKey` until a real consumer needs them; the
unit-test cases that exercise them go too. Keep `OK` as `*bool` only if
you keep the field at all — otherwise replace with a plain `OkOnly bool`
matching the test helper's actual usage.

**4. `extractErrorCode` regex vs. boolean OK**
`internal/mcp/audit.go:152-168` — the regex + "ERROR" fallback is fine,
but the brief says v1 has one consumer (the e2e step that filters
`ok=true` delegates and finishes). That consumer ignores `ErrorCode`
entirely. The regex is cheap and well-tested, so it isn't load-bearing
weight, but the `error_code` field is currently dead weight in the JSON
schema.
**Fix (defer):** keep, but stop emitting `error_code` until a reader uses
it; saves a regex compile + branch per failed call.

**5. `daemonOwnsInboxFile` legacy branches**
`internal/cli/mesh_watch.go:710-721` — three accept paths: type ==
`"task.delegate"`, type empty + matching task_id, type empty + empty
task_id. The package has never been in `main`; "legacy delegates pre-
dating the explicit type" do not exist on disk anywhere a real daemon
would meet them.
**Fix:** drop the two empty-type branches. Accept only `type ==
"task.delegate"`. Anything else is a peer message or a bug to surface,
not a compatibility surface to preserve.

**6. `extractArgKeys` never-nil contract**
`internal/mcp/audit.go:93-94` — `if e.ArgKeys == nil { e.ArgKeys = []string{} }`
in `Emit`, but `extractArgKeys` already returns `[]string{}` on every
non-key path. The only way `ArgKeys` is nil at `Emit` time is a hand-
constructed `AuditEntry` from a test. The autofill is harmless but
restates an invariant the producer already enforces.
**Fix:** remove the nil-check in `Emit`; trust `buildAuditEntry`. If a
test wants to emit `ArgKeys: nil`, that's the test's choice.

## Out of scope (not flagged)

- Naming of `mcpReadAuditLog` etc. — defer to maintainer-reviewer.
- Whether the design doc itself is over-considered — could not locate
  `wip/DESIGN-mcp-call-telemetry.md` in the working tree to assess.

## Net suggestion

Cut the `AuditSink` indirection, the functional-test duplicate readers,
and two legacy branches in `daemonOwnsInboxFile`. The audit package then
becomes one concrete writer, one reader, one filter, with all five
exported symbols genuinely used. That's the v1 brief.
