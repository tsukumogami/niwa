# Maintainability scrutiny — cross-session-communication

Reviewed through the next-developer lens: would someone six months from now form
the right mental model from the code alone?

## Blocking

**1. `DESIGN-mcp-call-telemetry.md` does not exist in the tree.**
Four comments cite it as authoritative: `internal/mcp/audit.go:4`, `audit.go:68`,
`internal/mcp/server.go:68-72`, `server.go:168-169` ("see DESIGN-mcp-call-telemetry.md,
'Failure isolation'"), and `mesh.feature:299`. A reader chasing the rationale for the
v=1 schema, "Failure isolation", or the privacy contract finds nothing.
*Either commit the doc, or replace the references with the rationale inline (the
"never log values" reason already lives in `audit.go:4-7` — extend that pattern).*

**2. `bootstrapPromptTemplate` lies about the retrieval path.**
`mesh_watch.go:85` instructs the worker to "Call niwa_check_messages to retrieve your
task envelope" — but by the time the worker boots, the daemon has already renamed
the envelope out of `inbox/` into `inbox/in-progress/<task-id>.json`. The retrieval
only works because of the special-case branch in `handleCheckMessages`
(`server.go:435-450`). A reader of either site cannot reconstruct the round-trip
without reading the other.
*Add a one-line cross-reference on `bootstrapPromptTemplate` pointing to the
in-progress-lookup branch in `handleCheckMessages`.*

**3. `daemonOwnsInboxFile` predicate logic is non-obvious without three-way history.**
`mesh_watch.go:697-722` has three accept-paths — "type==task.delegate", "type empty
AND filename==task_id", "type empty AND task_id missing" — each born from a different
era. The doc-comment lists them but doesn't say which one a v2 envelope should use,
nor which paths exist purely for backwards compatibility. The next person extending
this (e.g. for a new envelope type) cannot tell which branches are load-bearing
versus deprecated.
*Mark the legacy branches as "legacy v0 / pre-type-field — do not extend; new
envelope kinds get a fresh type string".*

**4. Audit schema versioning has no v=2 path.**
`AuditEntry.V` is hard-coded to 1 in `Emit`, `buildAuditEntry`, and the test fixtures.
There is no decoder that switches on `v`, no doc on how a reader should treat
unknown versions, and `ReadAuditLog` will silently parse a v=2 line into the v=1
struct (extra fields dropped, missing required fields zero-valued). The field
*name* implies versioning is planned; the *behavior* is "we'll figure it out then".
*Either drop the field (single-version forever, document that) or add a one-line
contract: "readers of an unrecognized v MUST skip the entry."*

**5. Functional-test `auditEntry` is a divergent twin of `mcp.AuditEntry`.**
`mesh_steps_test.go:1357-1366` redeclares the schema "to keep the test dependency
graph shallow." There is no compile-time link; a future field added to
`mcp.AuditEntry` will silently parse-drop in functional tests. The comment
acknowledges the duplication but doesn't mention the drift risk.
*Either import `mcp.AuditEntry` directly (the dependency is already shallow —
`internal/mcp` is consumed elsewhere in the package) or add a `// KEEP IN SYNC
WITH internal/mcp/audit.go AuditEntry` notice.*

## Advisory

**6. "PIPE_BUF atomicity" is a load-bearing claim with no in-code reference.**
`audit.go:56-58` and `audit_test.go:106-110` both assert atomicity for writes
< PIPE_BUF, but neither cites `man 7 pipe` or quantifies the entry size against
the limit. A reader cannot verify the claim without an external lookup, and the
"audit entries are well under that limit" line offers no upper bound — a future
field addition could push past 4 KiB without anyone noticing.
*Add an assertion (test or runtime) that an emitted line is < 4096 bytes, or
state the worst-case size derivation in the comment.*

**7. `errCodeRE` regex is correct but the boundary is non-obvious.**
`audit.go:152` requires at least two ALL_CAPS chars (`[A-Z][A-Z_]*[A-Z]`). The
`single-letter not a code` test at `audit_test.go:54` proves the intent, but the
pattern itself looks like it would match `A_B`. It does — and the test asserts
that. A reader changing this to allow shorter codes will silently change every
existing fallback path.
*Inline the "min-2-chars" rationale on the regex declaration; the existing
prose lists examples but doesn't explain the lower bound.*

**8. Stale `nopAuditSink` rationale.**
`audit.go:48-51` says nop is for "unit-test setups that exercise individual
handlers without provisioning a workspace." But `NewFileAuditSink("")` returns
nop for any empty `instanceRoot`, and `SetAuditSink(nil)` also installs it. The
doc-comment names one of three call sites.
*Rephrase as "any caller without a writable .niwa/ directory" so future
non-test callers fit the contract.*
