# Lead: IPC mechanisms for same-machine inter-process messaging

## Findings

### Unix Domain Sockets

Unix domain sockets (UDS) are bidirectional, stream-oriented or datagram-oriented IPC endpoints backed by a filesystem path (e.g., `/tmp/niwa-workspace.sock`). The kernel buffers data in-flight; processes connect, send, and receive like TCP but without network overhead.

**Process restarts**: The server process owns the socket file. If it dies, clients receive a connection error. The socket file may persist as a stale artifact and must be cleaned up before the server can rebind. Clients must implement reconnect logic. Messages in the kernel buffer at the time of crash are lost.

**Message durability**: None by default. UDS is a transport, not a queue. Any message in flight when a process dies is dropped. An application-level queue (in memory or on disk) is required for durability.

**Path to network transport**: Direct. The socket API is nearly identical to TCP sockets. Replacing `AF_UNIX` with `AF_INET`/`AF_INET6` and a host:port pair converts the transport with minimal code change. Framing, serialization, and application protocol transfer unchanged.

**Provisioning complexity**: Low. niwa writes a single socket path to a config file; processes discover it from there. No daemon required beyond the broker process itself.

**Go support**: First-class. `net.Listen("unix", path)` and `net.Dial("unix", path)`. No external dependencies.

**Verdict**: Best raw transport primitive. Fast, low-latency, zero external dependencies. Requires application-level framing and reconnect handling.

---

### Named Pipes (FIFOs)

Named pipes are half-duplex IPC backed by a filesystem path created with `mkfifo`. One process writes, another reads. For bidirectional messaging, two FIFOs are needed (one per direction).

**Process restarts**: Opening a FIFO blocks until both a reader and a writer are present. If either side dies, the other receives EOF or SIGPIPE. Re-establishing requires both sides to reopen, which can race. Managing this for N processes is complex.

**Message durability**: None. The kernel buffer holds a small amount of data (typically 64 KB) while both ends are open. If the reader dies, data in the buffer is lost.

**Path to network transport**: None natural. Named pipes are strictly local. Bridging to a network requires a separate relay process.

**Provisioning complexity**: Moderate for 2-process, high for N-process. Each pair needs dedicated FIFOs; a hub-and-spoke topology requires a broker process with one FIFO pair per client, which negates the simplicity advantage.

**Go support**: Standard `os.OpenFile` with `os.ModeNamedPipe`. Workable but awkward.

**Verdict**: Suitable for simple parent-child or 2-process scenarios. Not a fit for the async multi-session mesh described here. The blocking open semantics and half-duplex nature create coordination problems when processes start and stop independently.

---

### File-Based Queues (Polling)

Each process owns an inbox directory. Senders atomically write message files (e.g., via `rename`) into the recipient's inbox. The recipient polls the directory on a timer or uses `inotify`/`kqueue` for event-driven notification.

**Process restarts**: Excellent. Messages are durable on disk. A process that restarts reads all unconsumed messages from its inbox. The sender does not need to know whether the recipient is running.

**Message durability**: High. Messages survive process crashes and machine reboots (assuming fsync). Delivery is at-least-once if the reader acknowledges by moving or deleting the file after processing.

**Path to network transport**: None direct. To support remote peers, a relay process would need to forward files over a network protocol. This is architecturally awkward — the file-based model doesn't map cleanly to a network primitive.

**Provisioning complexity**: Very low. niwa creates inbox directories per session as part of workspace setup. No additional processes required.

**Go support**: `os.Rename` for atomic delivery, `fsnotify` (cross-platform inotify wrapper) for event-driven polling. Both well-supported.

**Verdict**: Best durability story for same-machine, zero-dependency operation. The lack of a natural network path is the main limitation. Works well as a fallback or as the durable layer sitting beneath a socket-based transport.

---

### SQLite (WAL Mode)

SQLite in WAL (Write-Ahead Logging) mode allows one writer and multiple readers to operate concurrently without blocking each other. A shared database file can act as a message queue: producers INSERT rows, consumers SELECT and DELETE or mark as consumed.

**Process restarts**: Strong. Messages persist until explicitly consumed. A restarting process reconnects to the file and picks up where it left off. Multiple producers and consumers can coexist safely with proper transaction design.

**Message durability**: High. WAL mode with `PRAGMA synchronous = NORMAL` gives good durability with acceptable performance. Full `PRAGMA synchronous = FULL` gives crash-safe guarantees at higher write latency.

**Path to network transport**: Partial. SQLite itself is file-local, but libSQL (a SQLite fork from Turso) adds a network server mode. Alternatively, the same schema could be lifted to PostgreSQL or a hosted SQLite-compatible service. The SQL schema is portable.

**Provisioning complexity**: Low. niwa initializes a single `.db` file in the workspace directory. No additional daemon. The schema (a `messages` table with sender, recipient, payload, timestamp, consumed columns) is simple to set up programmatically.

**Go support**: `mattn/go-sqlite3` (CGo) or `modernc.org/sqlite` (pure Go, no CGo). The pure-Go driver is particularly attractive since it eliminates build complexity.

**Verdict**: Strong fit. Durable, process-restart-safe, concurrent-safe with correct transaction isolation, zero external services. The schema doubles as an audit log. Network optionality requires a separate project (libSQL) but is achievable. Main costs: polling latency (mitigated by SQLite's built-in `NOTIFY`-like mechanism via hooks, or by pairing with a lightweight socket ping), and row growth requiring periodic cleanup.

---

### Embedded NATS (nats-server)

NATS is a lightweight, high-performance message broker. The `nats-server` binary is ~20 MB and can run embedded in a Go process via the `nats-server` Go package, or as a standalone daemon. It supports publish-subscribe, request-reply, and queue groups. JetStream (built-in) adds persistent streams.

**Process restarts**: Without JetStream, NATS is in-memory only — messages in flight at crash time are lost. With JetStream enabled and a file store, messages persist. The server (broker) must survive for clients to communicate; if the broker dies, all sessions lose connectivity until it restarts.

**Message durability**: With JetStream file store: high. Without: none. The broker itself becomes the durability layer.

**Path to network transport**: Native. NATS speaks TCP by default. Enabling remote access is a config flag change (`listen: 0.0.0.0:4222`). NATS clustering is also built-in. This is the strongest network optionality story of any option here.

**Provisioning complexity**: Moderate. niwa would need to start and manage a `nats-server` process as part of workspace setup, write a config file for it, and ensure it restarts if it dies (e.g., via a PID file and health check). The broker is a process dependency that must be supervised.

**Go support**: Excellent. `github.com/nats-io/nats.go` client is idiomatic Go. Embedding the server: `github.com/nats-io/nats-server/v2/server`. Both well-maintained.

**Verdict**: Best network optionality and strongest pub-sub semantics. JetStream gives durable streams with consumer groups. Main cost is operational: niwa must manage a broker process lifecycle. For a same-machine-first design that must scale to network, this is the most future-proof choice but adds a runtime dependency.

---

### Embedded Redis

Redis is an in-memory data structure server with pub-sub, streams (XADD/XREAD), and persistence options (RDB snapshots, AOF). A workspace could run a Redis instance on a local port or Unix socket.

**Process restarts**: Redis itself is durable with AOF enabled. Streams (XADD/XREAD with consumer groups) provide at-least-once delivery across restarts. If the Redis process dies, clients reconnect and resume from their last acknowledged position.

**Message durability**: With AOF persistence: high. Default (no persistence): none. Consumer groups + XREAD with explicit ACK give at-least-once guarantees.

**Path to network transport**: Native. Redis binds TCP by default. Remote access is a config change. Redis Sentinel or Cluster available for HA.

**Provisioning complexity**: Higher than NATS. Redis is a larger dependency (~7 MB binary but also requires its own config, port management, and persistence path setup). niwa would need to download and manage it. If using tsuku to install Redis, provisioning complexity reduces significantly.

**Go support**: `github.com/redis/go-redis/v9` is mature and idiomatic.

**Verdict**: Viable but heavier than NATS for this use case. Redis Streams are well-designed for this pattern. However, the operational burden (managing a Redis process, port conflicts, AOF log growth) exceeds the benefit when NATS JetStream or SQLite cover the same ground more cleanly.

---

### nanomsg / ZeroMQ

nanomsg (and its successor nng, or ZeroMQ) are messaging library frameworks rather than brokers. They provide socket patterns (PUSH/PULL, PUB/SUB, REQ/REP, PAIR) as library calls — no separate broker process.

**Process restarts**: Depends on pattern. PUSH/PULL is lossy if the receiver is down; messages queued in memory on the sender are lost if the sender restarts. No built-in persistence. nanomsg does not store messages — it's a transport abstraction.

**Message durability**: None. These are transport libraries. Durability requires an application-level layer on top.

**Path to network transport**: Native. nanomsg supports `tcp://`, `ipc://`, `inproc://`, and `ws://` (WebSocket) transports. Switching from `ipc://` to `tcp://` is a URL change.

**Provisioning complexity**: Low — no daemon. Linked as a library. However, Go bindings for nanomsg (go-nanomsg) are less actively maintained than alternatives. nng has Go bindings (`go.nanomsg.org/mangos/v3`), which are actively maintained.

**Go support**: `go.nanomsg.org/mangos/v3` (nng-based). Pure Go implementation available. No CGo required.

**Verdict**: Interesting as a transport abstraction layer that handles reconnect logic and socket patterns. No durability. For the workspace mesh, the lack of persistence makes this unsuitable as a standalone solution but it could layer under a durability mechanism.

---

### HTTP / SSE (local server)

A process could expose an HTTP server on a local port or Unix socket. Other processes POST messages and GET or SSE-stream responses. This is essentially a hand-rolled broker.

**Process restarts**: Same as UDS — if the server dies, clients lose connection. No durability unless the server persists a queue to disk.

**Message durability**: None by default. Requires explicit persistence layer.

**Path to network transport**: Native. HTTP already speaks TCP. Adding TLS and auth converts it to a production network endpoint.

**Provisioning complexity**: Low to moderate. Each session runs its own HTTP server; a root coordinator process aggregates. Port allocation for N sessions requires coordination.

**Go support**: Excellent. `net/http` standard library.

**Verdict**: Familiar and debuggable (curl, browser), but reinvents broker logic. Worth considering if the team wants minimal new dependencies and is comfortable building the coordination layer.

---

## Implications

For the workspace-aware session mesh described in the exploration context, three mechanisms stand out:

**SQLite (WAL) as the durable backbone**: Zero external dependencies, process-restart safe, naturally auditable (messages persist as records), trivial for niwa to provision (create a file), and works well for async back-and-forth where latency is not critical. The polling latency (10–100ms) is acceptable for inter-session coordination. Pairing SQLite with a lightweight Unix socket "ping" (to wake sleeping readers without polling) gives low latency without a full broker.

**NATS with JetStream as the network-capable option**: If the design must reach network transport without an architectural rewrite, NATS is the cleanest path. niwa provisions a `nats-server` process per workspace, and sessions connect as clients. JetStream streams provide durability. Upgrading to remote peers is a config flag. The cost is a managed process lifecycle.

**Unix Domain Sockets as the raw transport**: If building a custom broker or peer-to-peer protocol, UDS is the right primitive. It's fast, zero-dependency, and ports to TCP with minimal change. But it requires building the queue, framing, and reconnect logic on top.

The file-based queue deserves consideration as a fallback that requires nothing: no SQLite dependency, no daemon, no socket lifecycle. For a first implementation, inbox directories with `fsnotify` are operationally trivial and compose well with other layers later.

Named pipes, Redis, nanomsg, and HTTP-as-broker all lose to the above three options on at least one critical dimension for this use case.

## Surprises

**SQLite WAL handles concurrent writers better than its reputation suggests.** The common perception is that SQLite is single-writer only. In WAL mode, multiple writers can coexist with short serialization at commit time. For a workspace with 2–8 sessions posting messages at human interaction speeds (not thousands/sec), WAL mode is effectively concurrent. This makes SQLite more viable than expected.

**NATS server is embeddable in-process as a Go library.** This means niwa could start the broker as a goroutine within its own process (if niwa runs a persistent daemon) rather than launching a separate binary. This significantly reduces provisioning complexity — no process supervision required if niwa itself is the supervisor.

**File-based queues using `rename` are genuinely crash-safe.** The POSIX guarantee that `rename` is atomic means a message is either fully written (the renamed file exists) or not delivered (the tmp file exists). Combining this with a dedicated `inbox/` directory per session gives a remarkably solid foundation with no dependencies beyond the OS.

**None of these mechanisms solve session identity.** All of them require a stable identifier for each session to address messages correctly. If Claude Code sessions don't have stable identities across restarts, the addressing layer must be built regardless of which IPC mechanism is chosen. This is an application-level concern that sits above the transport.

**Unix socket files left by crashed processes block rebind.** On Linux, `SO_REUSEADDR` does not apply to Unix domain sockets the same way it does to TCP. The stale socket file must be explicitly removed before the server can rebind. niwa's provisioning code must handle this cleanup case.

## Open Questions

1. **Does niwa run a persistent daemon, or is it purely a CLI tool?** If niwa can run a long-lived process per workspace, embedding a NATS server (or a lightweight custom broker) becomes much simpler. If niwa is command-only, the broker must either be a separate managed process or use a broker-less approach (SQLite or file queues).

2. **What is the latency requirement for inter-session messages?** If sessions are waiting on answers before proceeding, even 100ms polling latency may be acceptable. If near-real-time responsiveness matters, a socket-based approach is necessary.

3. **How should session identity be established?** A stable session ID must be provisioned and associated with a repo/task. Does this come from niwa's workspace config, from the Claude session itself, or from something else? This determines how inbox addresses are assigned.

4. **Should messages be retained after consumption?** For debugging and audit purposes, keeping a history of all inter-session messages is valuable. SQLite naturally supports this (mark as consumed but retain rows). Other mechanisms discard on delivery.

5. **Is the SQLite + fsnotify combination sufficient, or does the design need the full pub-sub semantics of NATS?** The workspace mesh use case (N sessions, request-reply, delegation) maps to pub-sub patterns (broadcast to all sessions, subscribe to topics by session ID). SQLite can emulate this with schema design, but NATS provides it natively.

6. **What happens when two sessions write simultaneously to SQLite?** At 2–8 sessions at human interaction speed, contention is negligible. But a stress test confirming WAL-mode write throughput at the expected message rates would close this question definitively.

7. **How does niwa clean up IPC artifacts on workspace teardown?** Socket files, NATS data directories, SQLite files — each requires a cleanup path. This should be part of the provisioning design from the start.

## Summary

SQLite in WAL mode and NATS with JetStream are the two strongest candidates: SQLite requires zero external dependencies and is trivially provisioned by niwa as a single file, while NATS provides native pub-sub semantics and a clean path to network transport by changing a config flag. The biggest open question is whether niwa runs a persistent daemon — if it does, embedding NATS in-process eliminates the operational gap and makes NATS the clear winner; if niwa is a CLI-only tool, SQLite is the safer starting point because it needs no supervised process.
