# Decision 3: niwa_ask Blocking Mechanism

## Context

`niwa_ask` is a blocking MCP tool: it sends a `question.ask` message to a target
session's inbox, then holds the tool call open until the target replies with a
`question.answer` bearing a matching `reply_to`. The MCP server (`niwa mcp-serve`)
today processes tool calls synchronously — `dispatch` blocks until the response is
written to stdout. `niwa_ask` must block inside `dispatch` without blocking the whole
server loop, while guaranteeing timeout enforcement and goroutine cleanup.

The existing server already has a fsnotify watcher goroutine (`watchInbox`) that fires
when files appear in the calling session's inbox. The key design question is: how do we
wire the dispatch goroutine and the watcher goroutine together so the dispatch goroutine
sleeps on the reply without racing or leaking?

## Key assumptions

- Claude's session is blocked waiting for the tool result while `niwa_ask` is in flight.
  It will not send additional requests to the server during this window. Therefore,
  blocking the scan loop (no new requests processed while waiting) is safe and acceptable.
- The inbox watcher goroutine (`watchInbox`) is already running. The reply-watch path
  must reuse this watcher to avoid double-watching the same directory.
- Go channels are the natural mechanism for wiring the watcher goroutine to the blocked
  dispatch goroutine. A `chan toolResult` registered in a map keyed by expected
  `reply_to` message ID is the minimal shared state needed.
- Goroutine cleanup on timeout is mandatory. A leaked watcher continuation that fires
  after timeout sends to a closed or abandoned channel, causing a panic or silently
  interfering with the next `niwa_ask` call.
- `niwa_wait` (which collects N messages of specified types) uses the same map/channel
  mechanism with a different matching predicate.

## Chosen: Option A — Block in dispatch goroutine; background watcher signals via channel

### Full description

`niwa_ask` blocks inside `dispatch`. Before blocking, it registers a reply channel in a
server-level map keyed by the expected `reply_to` message ID. The existing `watchInbox`
goroutine is extended to check this map on every new inbox file it observes. When the
file's `reply_to` field matches a registered waiter, it sends the parsed message on the
channel. The dispatch goroutine selects on the channel and a timeout timer, then removes
its entry from the map before returning.

#### Shared state additions to `Server`

```go
// pending waiters for niwa_ask and niwa_wait
waitersMu sync.Mutex
waiters   map[string]chan toolResult // keyed by expected reply_to message ID
```

`waiters` is initialized in `New()` alongside `seenFiles`.

#### Registration and cleanup helper

```go
func (s *Server) registerWaiter(msgID string) (ch chan toolResult, cancel func()) {
    ch = make(chan toolResult, 1) // buffered: watcher can send without blocking
    s.waitersMu.Lock()
    s.waiters[msgID] = ch
    s.waitersMu.Unlock()
    cancel = func() {
        s.waitersMu.Lock()
        delete(s.waiters, msgID)
        s.waitersMu.Unlock()
    }
    return ch, cancel
}
```

The channel is buffered with capacity 1. This lets the watcher goroutine send without
blocking even if the dispatch goroutine has already timed out and the cancel function has
run — the send succeeds into the buffer and nobody reads it, which is safe. Without the
buffer, a watcher trying to send after timeout would block forever.

#### `handleAsk` (new tool handler)

```go
func (s *Server) handleAsk(args askArgs) toolResult {
    timeout := 10 * time.Minute
    if args.Timeout != "" {
        d, err := parseDuration(args.Timeout)
        if err != nil {
            return errResult("invalid timeout: " + err.Error())
        }
        timeout = d
    }

    // 1. Write question.ask to target's inbox (reuse existing send path).
    sendArgs := sendMessageArgs{
        To:     args.To,
        Type:   "question.ask",
        Body:   args.Body,
        TaskID: args.TaskID,
    }
    sendResult := s.handleSendMessage(sendArgs)
    if sendResult.IsError {
        return sendResult
    }
    msgID := extractSentMessageID(sendResult) // parse "ID: <uuid>" from the result text

    // 2. Register waiter before blocking.
    replyCh, cancel := s.registerWaiter(msgID)
    defer cancel()

    // 3. Block until reply arrives or timeout fires.
    select {
    case result := <-replyCh:
        return result
    case <-time.After(timeout):
        return errResultCode("ASK_TIMEOUT",
            fmt.Sprintf("no reply received within %s (sent message ID: %s)", timeout, msgID))
    }
}
```

The `defer cancel()` removes the waiter map entry whether the select exits via reply or
timeout. After cancel runs, the buffered channel may still receive a late reply, but no
code reads it and it is garbage-collected normally.

#### Watcher extension in `notifyNewFile`

The existing `notifyNewFile` function is called by `watchInbox` for every new
`.json` file in the inbox. It is extended with a waiter check before the notification
path:

```go
func (s *Server) notifyNewFile(path, name string) {
    data, err := os.ReadFile(path)
    if err != nil {
        return
    }
    var m Message
    if err := json.Unmarshal(data, &m); err != nil {
        return
    }
    // Check expiry (unchanged).
    if m.ExpiresAt != "" {
        exp, err := time.Parse(time.RFC3339, m.ExpiresAt)
        if err == nil && time.Now().After(exp) {
            return
        }
    }
    s.markSeen(name)

    // NEW: deliver to a waiting niwa_ask or niwa_wait goroutine if one exists.
    if m.ReplyTo != "" {
        s.waitersMu.Lock()
        ch, ok := s.waiters[m.ReplyTo]
        s.waitersMu.Unlock()
        if ok {
            // Move file to read/ before signaling so the dispatch goroutine
            // does not double-read it via niwa_check_messages.
            readDir := filepath.Join(s.inboxDir, "read")
            _ = os.MkdirAll(readDir, 0o755)
            dest := filepath.Join(readDir, filepath.Base(path))
            _ = os.Rename(path, dest)
            // Build result and send. Channel is buffered: this never blocks.
            ch <- buildReplyResult(m)
            return // do not send a channel notification for this message
        }
    }

    // Existing notification path (unchanged).
    content := fmt.Sprintf(...)
    s.notify("notifications/claude/channel", channelNotificationParams{...})
}
```

The file is moved to `read/` before signaling. This prevents `niwa_check_messages` from
also returning the same reply if the user calls it concurrently — the file is gone from
the inbox before the dispatch goroutine wakes up.

#### `niwa_wait` using the same mechanism

`niwa_wait` needs to collect N messages of given types. The same `waiters` map is used,
but with a different key strategy: the waiter registers a synthetic key (e.g., a
`wait:<uuid>` key), and the watcher's extended loop checks a second map for type-based
waiters. The simplest approach: give `niwa_wait` its own `typeWaiters` map keyed by a
wait ID, each holding the filter criteria and a channel. The watcher checks `typeWaiters`
for non-reply-to messages. This keeps the two maps separate and avoids coupling.

```go
type typeWaiter struct {
    types  map[string]bool
    from   map[string]bool // empty = accept all senders
    count  int
    ch     chan toolResult
    buf    []Message
}
// typeWaiters map[string]*typeWaiter  (protected by waitersMu)
```

The `notifyNewFile` watcher checks `typeWaiters` after the `reply_to` check: if the
message type and sender match any registered type waiter, it appends to `buf` and fires
the channel when `len(buf) >= count`.

#### Polling fallback path

The `watchInboxPolling` fallback (used when fsnotify is unavailable) calls `pollInbox`,
which also needs the waiter check. The same `notifyNewFile` call path is reused: polling
discovers new files and calls `notifyNewFile`, so no separate polling-path code is needed
for the waiter check.

### Timeout parsing

ISO 8601 duration parsing (`PT10M`, `PT30S`, `PT2H`) is not in the Go standard library.
A small hand-written parser handles the common cases: `PTnS`, `PTnM`, `PTnH`, `PTnHnM`,
`PTnHnMnS`. Durations exceeding 24 hours are capped or rejected with a clear error.

## Rationale

**Why blocking in the dispatch goroutine is correct**: Claude's MCP client is also
blocked waiting for the tool result. It does not send new requests during this window.
The server's scan loop is blocked, but nothing is trying to write to it. This is not a
bottleneck — it is the correct model for a blocking tool in a single-session MCP server.
Making the dispatch non-blocking (Option B) introduces out-of-order response risk and
complicates cleanup without any actual benefit, because there is no concurrent traffic.

**Why reusing the existing watcher goroutine is correct**: Adding a second fsnotify
watcher on the same directory produces redundant inotify kernel registrations and
introduces race conditions between two independent watchers reading the same files.
The existing watcher already handles all inbox file events; extending it with a map
lookup is the minimal change.

**Why a buffered channel is required**: The cancel function (which deletes the waiter map
entry) runs as a `defer` and fires even if the goroutine exited via timeout. If the
watcher fires immediately after the timeout select branch is taken but before cancel runs,
it would attempt to send on the channel. Without the buffer, this send blocks the watcher
goroutine indefinitely. With capacity-1 buffer, the send succeeds instantly and the value
is discarded when the channel is GC'd.

**Why the reply file is moved before signaling**: If the dispatch goroutine wakes and
immediately calls `niwa_check_messages` (in a future combined `niwa_ask` flow), the reply
message would appear twice. Moving it to `read/` atomically before signaling prevents
this. The signal on the channel is the authority; the file is already consumed.

## Rejected alternatives

### Option B: New goroutine per niwa_ask; non-blocking dispatch; response written out-of-band

The dispatch function returns immediately, and a spawned goroutine later writes the
delayed JSON-RPC response via the shared mutex-protected encoder.

Rejected for two reasons. First, the JSON-RPC response `id` must match the original
request `id`, and writing it out-of-order relative to other responses may confuse MCP
clients that assume responses arrive in request order. The MCP spec permits out-of-order
responses, but Claude Code's implementation has not been verified to handle them, and
testing this assumption is difficult. Second, if stdin closes before the goroutine
finishes (e.g., the user kills the session), the goroutine blocks on the encoder write
indefinitely. Cleanup requires a context passed to the goroutine and checked before every
write. This complexity is unnecessary: there is no concurrent traffic, so blocking the
scan loop is harmless.

### Option C: Poll inbox directory with ticker

`niwa_ask` blocks using a ticker (e.g., 500ms) to poll for the reply file. No watcher
integration needed.

Rejected because 500ms latency per tick adds up in multi-exchange conversations. A
coordinator asking three clarifying questions before delegating a task adds 1.5 seconds
of unnecessary wait at minimum. The existing watcher fires within ~10ms of file creation
(one fsnotify inotify event plus the 10ms settle delay already in `watchInbox`). Polling
is the anti-pattern the watcher was built to replace. Re-introducing it for the reply-
watch path is inconsistent and slower.

### Option D: Named pipe or Unix socket for the reply signal

A per-session named pipe signals reply arrival; `niwa_ask` selects on the pipe read. The
daemon writes to the pipe when it delivers a message.

Rejected because it adds infrastructure that isn't already there (named pipes per
session, daemon coordination) and solves a problem that's already solved by the in-process
channel approach. The entire signal path (watcher goroutine → map lookup → Go channel →
blocked dispatch goroutine) is in one process with no IPC overhead. A named pipe between
the daemon and the MCP server also re-introduces the "daemon as single point of failure"
concern that the file-based inbox design specifically avoids.

## Consequences

- Positive: no goroutine leak possible — `defer cancel()` always runs, buffered channel
  allows late watcher sends without blocking
- Positive: reply latency matches fsnotify latency (~10ms), not polling latency (500ms)
- Positive: reuses the existing watcher goroutine and its polling fallback; no duplicate
  directory watching
- Positive: the reply file is consumed (moved to `read/`) atomically before the waiter is
  signaled, preventing double-delivery via `niwa_check_messages`
- Positive: `niwa_wait` uses the same map/channel structure with a separate type-waiter
  map, keeping the two blocking tools consistent
- Negative: the scan loop is blocked while `niwa_ask` is in flight — no new requests can
  be processed during a `niwa_ask` call
- Mitigation: this is not actually a problem in practice. Claude's MCP session is also
  blocked waiting for the tool result; it does not send new requests during this window.
  The server is a single-session server (one Claude Code session per `niwa mcp-serve`
  process), so there is no concurrent traffic to serve.
- Negative: `extractSentMessageID` requires parsing the text output of `handleSendMessage`
  to recover the assigned message ID — a fragile string dependency between two handlers
- Mitigation: refactor `handleSendMessage` to return a struct (sent message ID + status)
  that `handleAsk` can use directly, without parsing text output. The text rendering
  stays in a separate formatting step.
