# Crystallize Decision: cross-session-communication

## Chosen Type

Design Doc

## Rationale

The "what" is settled: niwa provisions a workspace-aware inter-session communication layer so independently-opened Claude sessions can exchange messages without user intermediation. What's not settled is the "how" — and the exploration surfaced six distinct architectural questions that are interrelated and can't be resolved by picking one option in isolation. A design doc is the right artifact to compare the paths, record the decisions, and give future contributors the reasoning.

Critically, architectural decisions made during exploration — session identity encoding (instance + role, not PID), IPC mechanisms eliminated (named pipes, Redis, nanomsg), and Phase 1 scope boundaries (same-machine, no session spawning) — will be lost when `wip/` is cleaned at PR merge unless captured in a permanent document. The design doc is the permanent home.

## Signal Evidence

### Signals Present

- **What to build is clear, how to build it is not**: The feature is clearly scoped (inter-session messaging, provisioned by niwa, same-machine first). But the IPC mechanism (file-based vs. Unix socket vs. NATS), broker model (daemon vs. daemonless), delivery primitive (MCP channels vs. independent transport), and `[channels]` config namespace are all open.
- **Technical decisions between approaches**: SQLite WAL vs. file-based queues vs. Unix domain sockets as transport; MCP channels (maturing, constrained today) vs. an independent Unix socket broker; daemon-backed NATS vs. zero-dependency file-based approach. These are legitimate competing paths, not obvious choices.
- **Architecture, integration, and system design questions remain**: Where does the broker live? How does session discovery work? What is the `ChannelMaterializer` design? How does the `[channels]` key distinguish built-in broker from plugin-backed channels? How does `workspace-context.md` injection work for the channel address?
- **Multiple viable implementation paths**: File queues (zero dependency, proven by community tools), Unix sockets (low latency, clean TCP migration path), NATS JetStream (best network optionality if a daemon exists), MCP HTTP transport (most "native" to Claude Code, constrained today).
- **Architectural decisions made during exploration that must survive**: Session identity = workspace instance + role (PID as tiebreaker only); named pipes, Redis, and nanomsg eliminated; Phase 1 scopes out session spawning; file-based queue as default starting transport. These are record-worthy decisions.
- **Core question is "how should we build this?"**: Confirmed. The exploration established that it CAN be built (demand validated, `[channels]` placeholder exists, provisioning pipeline is ready) and narrowed what should be built. The remaining question is architecture.

### Anti-Signals Checked

- **What to build is still unclear**: Not present. Feature scope is well-defined.
- **No meaningful technical risk or trade-offs**: Not present. Multiple real trade-offs surfaced (daemon vs. no daemon, MCP maturity risk, `[channels]` naming collision).
- **Problem is operational, not architectural**: Not present. This is clearly an architectural design problem.

## Alternatives Considered

- **PRD**: Demoted. Requirements were provided as input before exploration started — the user described the feature need before research began. Exploration confirmed feasibility and landscape, not requirements. PRD tiebreaker: "requirements given as input → Design Doc."
- **Plan**: Demoted. Technical approach is actively debated (IPC mechanism, broker model, MCP vs. independent). A plan can't sequence work where the architecture isn't decided yet.
- **No Artifact**: Demoted. Architectural decisions were made during exploration that need to survive `wip/` cleanup. Multiple people will need to build from this design. "Urgency doesn't override the need to capture decisions."
- **Decision Record**: Demoted. The decisions are multiple and interrelated — IPC transport, broker model, session registry, MCP integration, `[channels]` naming all influence each other. An ADR handles a single decision; a design doc handles an interconnected design space.
- **Spike Report**: Low score. The feasibility question is largely answered (it can be built; community tools prove it). The remaining questions are architectural trade-offs, not feasibility unknowns.
