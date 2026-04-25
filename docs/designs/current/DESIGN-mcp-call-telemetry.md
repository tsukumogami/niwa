---
status: Accepted
problem: |
  Functional tests can't prove that an LLM-driven coordinator actually used niwa MCP
  tools to delegate work. Today's graph-e2e scenario asserts marker files exist and
  tasks reach `state=completed`, which proves *some* `niwa_delegate` happened, but
  a non-obedient coordinator could write the markers itself and delegate two trivial
  empty tasks — both assertions still pass. With no per-call observability, the test
  cannot distinguish "the coordinator coordinated" from "the coordinator cheated."
decision: |
  Add a single chokepoint emit at `dispatch`'s "tools/call" branch that writes one
  NDJSON line per invocation to `<instance-root>/.niwa/mcp-audit.log`. Each line
  captures the timestamp, caller role, caller task_id, tool name, sorted
  top-level argument key names (never values), success/error flag, and the leading
  niwa error code if any. The emitter is a small `AuditSink` interface so the file
  appender can be swapped for a streaming or networked sink later without touching
  the 11 tool handlers. Tests parse the file post-hoc to assert who called what.
rationale: |
  The cheapest thing that closes the test gap is also the thing with the smallest
  blast radius: one emit point, one file, one schema. Logging only argument *keys*
  (not values) avoids LLM-text leakage. The dispatch chokepoint guarantees coverage
  of every tool, including any added later. Extracting the emitter behind an
  interface keeps the door open for real telemetry shipping without locking us into
  the file format we're picking today.
---

# DESIGN: MCP-call telemetry

## Status

Accepted

## Context and Problem Statement

The cross-session communication feature delegates work to LLM-driven worker
sessions using niwa MCP tools (`niwa_delegate`, `niwa_finish_task`,
`niwa_check_messages`, etc.). Functional tests need to prove that a given
session actually invoked a given tool — not just that some downstream
filesystem state happens to look correct.

The headline `@channels-e2e-graph` scenario currently asserts:

- both worker tasks reach `state=completed`
- per-repo `marker.txt` files exist with the expected content
- coordinator stdout contains `GRAPH_DONE`

Those assertions are necessary but not sufficient. A misaligned coordinator
LLM could:

- write the marker files itself with absolute paths
- delegate two trivial empty tasks (workers call `niwa_finish_task` immediately)
- output `GRAPH_DONE`

All four assertions pass. The test silently accepts the cheat.

The MCP server and its 11 tool handlers leave no per-call audit trail today.
`transitions.log` records task state changes (a small subset of activity), and
the daemon log records spawn events — neither captures `tools/call` itself.
Adding per-call observability lets tests anchor their assertions on the actual
tool-use record rather than on indirect side effects.

## Decision Drivers

- **Test-driven need.** The motivating consumer is functional tests; the
  schema must be deterministic and easy to parse from Go test code.
- **Privacy.** MCP tool arguments routinely contain LLM-generated text that
  may include prompt injections, secrets, or PII. Logging values is unsafe;
  logging top-level argument keys preserves enough signal for tests without
  the leakage risk.
- **Crash safety.** A worker can be SIGKILLed mid-call (defensive reap path,
  Issue 6). A torn audit line breaks log parsing. The append must be atomic.
- **Low overhead.** The MCP path is on the hot loop of every tool call;
  audit emission must not add noticeable latency or block real work.
- **Centralisation.** Eleven tool handlers exist today; more will follow.
  Per-handler audit emission would be 11+ identical insertions to maintain.
- **Future-proof.** A future iteration may stream telemetry to a sidecar
  rather than a file. Today's call sites should not need rewriting then.
- **Discoverability.** Tests must locate the audit log from the instance
  root without a config lookup; the path is a stable convention.

## Considered Options

### Decision 1: Storage layout

**Option A — Single per-instance file `.niwa/mcp-audit.log` (CHOSEN).** All
tool calls across all roles and tasks land in one append-only NDJSON file at
the instance root. Discovery is `<instance>/.niwa/mcp-audit.log`. Tests
parse once, filter in memory.

**Option B — Per-task file under `.niwa/tasks/<T>/mcp-audit.log`.**
Co-located with task state. Reads only the calls relevant to one task. Loses
non-task calls (coordinator-level peer messaging, list_outbound_tasks).
Forces the dispatcher to know the task dir even when the call has no task
context. Rejected: the simpler single file already supports per-task
filtering by reading `task_id` from each entry.

**Option C — Per-role file under `.niwa/roles/<role>/mcp-audit.log`.**
Mirrors inbox layout. Reads only one role's calls. Forces a directory write
per emit (creation if missing). Tests would need to read multiple files to
get a graph view. Rejected: the single file is one syscall for any query.

**Why A wins:** smallest emit path (no role/task plumbing into the writer),
one open file descriptor, one read for any test query. NDJSON volume is
bounded (one line per tool call, kilobytes per typical scenario).

### Decision 2: Schema

**Option A — Fixed v=1 NDJSON line `{v, at, role, task_id, tool, arg_keys, ok, error_code}` (CHOSEN).** Mirrors the existing `transitions.log` shape;
parsers and unit tests already work this way. Forward-compatible: adding
fields is safe; removing them is a breaking change tracked by `v`.

**Option B — Free-form JSON event with a `kind` field.** Heterogeneous
shapes per tool. Maximally flexible, painful to parse. Rejected.

**Option C — Protobuf or another binary format.** Faster to parse at scale,
overkill for tens-to-hundreds of lines per scenario. Rejected.

**Why A wins:** matches an in-repo convention the team already maintains,
keeping cognitive cost near zero. The fixed shape means a single Go struct
handles all reads.

### Decision 3: Concurrency and crash safety

**Option A — O_APPEND, no flock, no per-emit fsync (CHOSEN).** Linux
guarantees atomic appends for writes smaller than PIPE_BUF (~4096 bytes);
audit entries fit comfortably. Concurrent emits never interleave or tear.
fsync omitted because audit is best-effort observability, not state — a
crashed worker might lose its last line, but the consumer (tests) reads
post-hoc when the writers have exited normally.

**Option B — flock around append.** Serializes unrelated tool calls across
roles. Adds latency without correctness benefit (atomic append already
prevents tearing). Rejected.

**Option C — fsync per emit.** Doubles syscall cost on the tool path. Tests
read after the writer process exits; the OS page cache is durable across the
process boundary even without fsync. Rejected for the test-driven use case;
revisit if a forensic audit consumer ever needs durability.

**Why A wins:** matches Linux's atomic-append guarantee, drops one syscall,
and stays correct for the consumer pattern (read after writers exit).

### Decision 4: Argument capture

**Option A — Sorted top-level key names only (CHOSEN).** Tool calls are
`{"tool":"niwa_delegate","arguments":{"to":"web","body":{...}}}`. The audit
records `arg_keys: ["body","to"]`. Proves shape (which tool was called with
which named parameters present) without exposing values.

**Option B — Full arguments JSON.** Maximum signal, maximum risk: LLM input
can carry secrets, prompt injections, or PII. Rejected.

**Option C — A whitelist per tool of safe-to-log fields.** Lower risk than
B, requires per-tool maintenance forever. Rejected for v1; the keys-only
form is enough for tests today.

**Why A wins:** zero per-tool maintenance, no leakage risk, sufficient for
the test-driven use case (assertion on call shape, not values).

### Decision 5: Error capture

**Option A — Boolean `ok`; on error, `error_code` set to the leading
identifier from the result text matching `^[A-Z_]+(?::|$)` or `ERROR` if no
match (CHOSEN).** niwa errors prefix their text with stable codes
(`NOT_TASK_PARTY`, `UNKNOWN_ROLE`, `BAD_TYPE`, `TASK_ALREADY_TERMINAL`,
`TASK_NOT_FOUND`, `INVALID_ARGS`, etc.). Capturing the code preserves the
machine-readable signal tests assert on; falling back to `ERROR` keeps the
schema closed.

**Option B — Full result text.** Same leakage concern as full arguments.
Rejected.

**Option C — `ok` only, no code.** Tests can't distinguish authorization
denials from validation failures. Rejected.

**Why A wins:** preserves enough signal for tests to assert specific failure
modes; the codes themselves are public/intentional API.

### Decision 6: Hook point

**Option A — `dispatch`'s "tools/call" branch (CHOSEN).** One call site
wraps `s.callTool(p)`. Captures every tool, including future ones, with no
per-handler boilerplate.

**Option B — Per-handler emit.** Each of the 11 handlers calls a helper.
Eleven duplications today; new handlers must remember to add it. Rejected.

**Why A wins:** one chokepoint, exhaustive coverage by construction.

### Decision 7: Future extensibility

**Option A — `AuditSink` interface; default impl is a file appender
(CHOSEN).** `Server` holds an `auditSink AuditSink`; `New` wires the
default; tests can substitute a recording sink directly.

**Option B — Hard-coded `os.OpenFile` in `dispatch`.** Less code today,
forces a refactor later when telemetry graduates. Rejected.

**Why A wins:** the indirection costs nothing today and saves a sweeping
refactor later when (not if) we ship to a sidecar or a worker.

## Decision Outcome

The MCP server gains a one-method `AuditSink` interface and a default
file-backed implementation that writes NDJSON to
`<instance-root>/.niwa/mcp-audit.log`. The `dispatch` "tools/call" branch
calls the sink before returning; on emit failure the call still returns
normally. Tests read the file post-hoc through a small helper.

The chosen design keeps the implementation footprint small (~150 lines plus
unit tests), uses conventions already in the codebase (NDJSON + `v=1`
versioning, mirror of `transitions.log`), and leaves the door open for a
streaming/networked sink without touching the 11 tool handlers.

## Solution Architecture

### File layout

```
<instance-root>/
  .niwa/
    mcp-audit.log    # NDJSON, append-only, 0o600
    tasks/<T>/
      transitions.log  # existing — unchanged
```

### Schema (v=1)

One line per `tools/call`. Field order is canonical for readability but
parsers must not depend on it.

```json
{
  "v": 1,
  "at": "2026-04-24T03:14:15.926Z",
  "role": "coordinator",
  "task_id": "26230581-45f4-440a-a491-c6eb38d8fba7",
  "tool": "niwa_delegate",
  "arg_keys": ["body", "mode", "to"],
  "ok": true,
  "error_code": ""
}
```

| Field | Type | Notes |
|-------|------|-------|
| `v` | int | Schema version. Always `1` for this design. |
| `at` | string | RFC3339Nano UTC timestamp at emit. |
| `role` | string | Caller's `NIWA_SESSION_ROLE` (empty for coordinators with no role env, e.g. niwa CLI invocations). |
| `task_id` | string | Caller's `NIWA_TASK_ID`. Empty for non-worker callers. |
| `tool` | string | Tool name from `params.name`. |
| `arg_keys` | string[] | Sorted top-level keys present in `params.arguments`. Empty array if arguments was null/missing/non-object. Never includes nested keys or values. |
| `ok` | bool | `false` when the result's `IsError` field is true. |
| `error_code` | string | Leading `^[A-Z_]+(?::|$)` token from the result content. `"ERROR"` if `IsError` is true but no code prefix is found. Empty when `ok=true`. |

### Components

```
+----------------------+       +---------------------+
|     dispatch         | call  |      callTool       |
|  case "tools/call":  |------>|   (per-tool branch) |
|  pre = now()         |       +---------------------+
|  res = callTool(p)   |
|  sink.Emit(entry)    |       +---------------------+
|  send(res)           |------>|     AuditSink       |
+----------------------+       |    interface{       |
                               |    Emit(Entry) error|
                               |    }                |
                               +---------------------+
                                         |
                                         v
                               +---------------------+
                               | fileAuditSink       |
                               | <inst>/.niwa/       |
                               |   mcp-audit.log     |
                               +---------------------+
```

### Interface

```go
// AuditEntry is the public schema (v=1).
type AuditEntry struct {
    V         int      `json:"v"`
    At        string   `json:"at"`
    Role      string   `json:"role,omitempty"`
    TaskID    string   `json:"task_id,omitempty"`
    Tool      string   `json:"tool"`
    ArgKeys   []string `json:"arg_keys"`
    OK        bool     `json:"ok"`
    ErrorCode string   `json:"error_code,omitempty"`
}

// AuditSink writes one entry per tool call. Implementations must be
// concurrency-safe — multiple goroutines may emit simultaneously.
type AuditSink interface {
    Emit(entry AuditEntry) error
}

// NewFileAuditSink returns a sink that appends NDJSON lines to
// <instanceRoot>/.niwa/mcp-audit.log. Returns a no-op sink when
// instanceRoot is empty (defensive: unit-test Servers without a
// workspace must not write outside their tempdir).
func NewFileAuditSink(instanceRoot string) AuditSink
```

### Dispatch changes

```go
case "tools/call":
    var p toolCallParams
    if err := json.Unmarshal(req.Params, &p); err != nil {
        s.sendError(req.ID, -32600, "invalid params")
        return
    }
    res := s.callTool(p)
    _ = s.audit.Emit(buildAuditEntry(s.role, s.taskID, p, res))
    s.send(response{JSONRPC: "2.0", ID: req.ID, Result: res})
```

`buildAuditEntry` is a pure function: parse `p.Arguments` once for keys,
inspect `res.IsError` plus `res.Content[0].Text` for the error code, fill
the struct.

### Argument-key extraction

```go
func extractArgKeys(raw json.RawMessage) []string {
    if len(raw) == 0 || string(raw) == "null" {
        return []string{}
    }
    var m map[string]json.RawMessage
    if err := json.Unmarshal(raw, &m); err != nil {
        return []string{}
    }
    keys := make([]string, 0, len(m))
    for k := range m {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    return keys
}
```

Unmarshal failure (e.g., arguments was a string or array, not an object)
yields an empty slice. The audit still records the call.

### Error-code extraction

```go
var errCodeRE = regexp.MustCompile(`^([A-Z][A-Z_]*[A-Z])(?::|$)`)

func extractErrorCode(res toolResult) string {
    if !res.IsError {
        return ""
    }
    if len(res.Content) == 0 {
        return "ERROR"
    }
    if m := errCodeRE.FindStringSubmatch(res.Content[0].Text); m != nil {
        return m[1]
    }
    return "ERROR"
}
```

The regex matches a leading `ALL_CAPS_IDENTIFIER` followed by either a colon
or end-of-string. Single-letter tokens are excluded by requiring at least
two characters (start with `A-Z`, end with `A-Z`).

### File sink

```go
type fileAuditSink struct {
    path string
}

func (s *fileAuditSink) Emit(e AuditEntry) error {
    if e.V == 0 {
        e.V = 1
    }
    if e.At == "" {
        e.At = time.Now().UTC().Format(time.RFC3339Nano)
    }
    if e.ArgKeys == nil {
        e.ArgKeys = []string{}
    }
    data, err := json.Marshal(e)
    if err != nil {
        return err
    }
    data = append(data, '\n')
    f, err := os.OpenFile(s.path,
        os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
    if err != nil {
        return err
    }
    _, werr := f.Write(data)
    cerr := f.Close()
    if werr != nil {
        return werr
    }
    return cerr
}
```

Atomic append (single `Write` call, payload < PIPE_BUF) plus
`O_NOFOLLOW` matches the `transitions.log` writer's threat model. No fsync
per emit (Decision 4).

### Test-side reader

```go
// AuditEntries reads the per-instance audit log and returns parsed entries
// in file order (which matches emission order). Returns an empty slice when
// the file does not exist (no calls made).
func ReadAuditLog(instanceRoot string) ([]AuditEntry, error)
```

Tests filter the slice in memory:

```go
calls := mcp.FilterAudit(entries, mcp.AuditFilter{
    Role: "coordinator",
    Tool: "niwa_delegate",
})
require.Len(t, calls, 2)
```

### Sequence

```
Coordinator claude -p
     │
     │ tools/call {"name":"niwa_delegate","arguments":{"to":"web",...}}
     ▼
MCP Server.dispatch
     │
     │ pre = now()
     │ res = callTool(p)        ── handles authorization, state, inbox write
     │ entry = buildAuditEntry(s.role, s.taskID, p, res)
     │ s.audit.Emit(entry)      ── appends one NDJSON line
     │
     ▼
.niwa/mcp-audit.log (single line appended, atomic)
     │
     │ ... later ...
     ▼
Test reads file post-hoc; asserts coordinator emitted niwa_delegate × 2 with
target roles web and backend; asserts each worker emitted niwa_finish_task × 1.
```

## Implementation Approach

Four small slices, each independently mergeable but landed together as part
of the cross-session-communication branch:

1. **Audit primitive.** New file `internal/mcp/audit.go` with `AuditEntry`,
   `AuditSink`, `NewFileAuditSink`, `extractArgKeys`, `extractErrorCode`,
   `buildAuditEntry`. Unit tests cover key extraction (object, null, array,
   malformed), error code extraction (each known niwa code, no-prefix
   error, no-error case), file appender atomicity (parallel goroutines
   emitting concurrently — read-back parses cleanly).

2. **Server wiring.** Add `audit AuditSink` field to `Server`; `New(role,
   instanceRoot)` constructs `NewFileAuditSink(instanceRoot)`. Dispatch
   `tools/call` branch calls `s.audit.Emit(buildAuditEntry(...))` before
   `send`. A unit-test option (`SetAuditSink`) lets tests substitute a
   recording sink.

3. **Test reader.** `internal/mcp/audit_reader.go` with `ReadAuditLog`
   and a small `FilterAudit` helper. Round-trip test (write N entries,
   read back, assert equality).

4. **Tighten graph-e2e.** Add step helpers
   `theCoordinatorEmittedNDelegateCallsToRoles` and
   `roleEmittedFinishTaskCalls`. Modify the `@channels-e2e-graph` scenario
   to assert audit-grounded coordinator behaviour after the existing
   filesystem assertions. The marker assertions stay (defence in depth);
   the audit assertions add the missing "coordinator definitely used niwa"
   check.

Build order: 1 → 2 → 3 → 4. Slices 1-3 are pure additions and do not change
existing behaviour; slice 4 strengthens an existing scenario.

## Security Considerations

| Threat | Mitigation |
|--------|------------|
| LLM-supplied secrets/PII in tool arguments | Capture top-level key names only; never values. `extractArgKeys` runs `json.Unmarshal` into `map[string]json.RawMessage` and discards values immediately. |
| Symlink substitution at the audit log path | `O_NOFOLLOW` on the open call rejects pre-planted symlinks; matches `transitions.log` pattern. |
| Disk filling via runaway emission | One line per tool call; LLM workers are bounded by retry cap and stall watchdog; the audit log grows roughly with task volume, not adversarial input. Mitigation deferred (rotation when needed). |
| Audit emit failure breaking real work | `dispatch` ignores the emit error (`_ = s.audit.Emit(...)`). Observability degrades silently; tool calls always complete. |
| Cross-instance contamination | File path is anchored at `instanceRoot`; instances have separate roots. Multi-tenant concerns are out of scope (single-user local CLI). |
| Log readers reading partial lines on a crashed writer | NDJSON parse errors are skipped, not fatal. The audit reader logs and continues. Atomic append makes torn lines unreachable in practice (Linux PIPE_BUF), but the reader is defensive anyway. |

No new attack surface introduced. The audit log is local-only,
instance-scoped, 0o600. No network egress, no external dependencies.

## Consequences

### Positive

- **Closes the test gap:** graph-e2e and similar scenarios can prove the
  coordinator actually invoked niwa MCP tools, not just that filesystem
  state happens to look correct.
- **General-purpose:** every tool-call audit lands in one place, enabling
  future debugging tools (`niwa mesh audit` could become a thing) and
  forensic investigation without needing to add per-handler hooks.
- **Cheap to ship:** ~150 lines of production code plus tests. No new
  external dependencies. Mirrors an existing pattern.

### Negative / accepted

- **No durability guarantee:** a SIGKILL'ed worker may lose its final
  audit line. Acceptable: audit is observability, not state; tests run
  post-hoc when writers have exited cleanly.
- **No rotation:** the log grows forever. Acceptable for now (one line per
  tool call, dwarfed by `transitions.log` and the daemon log). Rotation can
  be added when a real-world instance starts to feel it.
- **Schema drift risk:** v=1 is the only schema today. Adding a v=2 reader
  requires a migration plan. Standard versioned-NDJSON discipline applies.

### Mitigations

- The `AuditSink` interface and `v=1` field together create the seam for
  future change without rewriting call sites.
- The audit emit is wrapped in `_ = ...` so a degraded sink never breaks
  the MCP path; failures must be discovered by reading the file (or its
  absence), not by user-visible breakage.
