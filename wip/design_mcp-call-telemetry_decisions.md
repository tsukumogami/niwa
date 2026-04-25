# --auto decisions: mcp-call-telemetry

| # | Decision point | Choice | Reason |
|---|----------------|--------|--------|
| 1 | Storage location | single per-instance file `.niwa/mcp-audit.log` | tests want to count per-tool/per-role across whole instance; one path simplifies discovery and reading; volume is bounded (one line per tool call) |
| 2 | Schema | NDJSON v=1 with fields `{v, at, role, task_id, tool, arg_keys, ok, error_code}` | mirrors transitions.log shape and parsing; extensible by adding fields without breaking old readers |
| 3 | Concurrency | O_APPEND only, no flock | audit entries are independent (no read-modify-write); writes < PIPE_BUF (~4096 bytes) are atomic on Linux O_APPEND; flock would serialize unrelated calls |
| 4 | Crash durability | no fsync per call | telemetry is best-effort observability, not state; the tool call's own fsync (when present) provides ordering; fsync per emit doubles syscalls for no observable benefit in tests |
| 5 | Hook point | central `dispatch` "tools/call" branch | one call site emits for all 11 tools; per-handler emission would be 11 boilerplate copies and would miss handlers added later |
| 6 | Argument capture | sorted top-level key names only | values are LLM-generated text and may contain secrets, prompt injections, or PII; key names alone prove which tool was invoked with which shape |
| 7 | Error capture | extract leading `^[A-Z_]+:` code from result text (e.g., `NOT_TASK_PARTY:`); fall back to literal `ERROR` | niwa error codes are stable identifiers that tests assert on; full text could leak content |
| 8 | Failure isolation | swallow audit errors silently | a failing audit must never break a tool call; observability degrading silently is preferable to user-visible breakage |
| 9 | Future extensibility | emit through a `AuditSink` interface (single method `Emit(entry)`); default impl is the file appender | swapping for a sidecar streamer / remote shipper later is one line of wiring; no call-site changes |
| 10 | Test consumer | helper `ReadAuditLog(instanceRoot) []Entry` returning parsed NDJSON | one helper reused across scenarios; filter by tool/role at the call site |
