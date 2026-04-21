# Exploration Decisions: cross-session-communication

## Round 1

- **Eliminated named pipes, Redis, nanomsg as IPC candidates**: Named pipes fail on half-duplex and blocking-open semantics for N-process meshes; Redis is heavier than NATS for no additional benefit; nanomsg has no durability. File-based queues and Unix domain sockets are the viable same-machine transports; NATS is reserved for if/when niwa gains a daemon.
- **Session identity = workspace instance name + user-assigned role**: PID used only as a tiebreaker. This matches the existing InstanceState model and avoids PID recycling hazards. Routing must not depend on PID.
- **File-based queue as default starting transport**: Zero dependencies, crash-safe (atomic rename), proven by community workarounds (session-bridge, Gas Town). Unix socket as optional low-latency complement for same-machine operation.
- **Session spawning scoped out of Phase 1**: The communication layer (provisioning, registry, transport, schema) is Phase 1. Automated Claude session spawning is a separate, harder problem and a follow-on.
- **Auto-mode decision — ready to crystallize**: Findings are sufficient. Remaining open questions (MCP timeline, Gas Town architecture, [channels] naming) are design-doc decisions, not exploration-level unknowns.
