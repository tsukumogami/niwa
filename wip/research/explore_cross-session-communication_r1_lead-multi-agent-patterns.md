# Lead: Multi-agent framework patterns

## Findings

### Microsoft AutoGen

AutoGen's core abstraction is the `ConversableAgent` — every participant, whether an LLM, a human proxy, or a tool executor, speaks the same message interface. Coordination happens through conversation patterns, not a dedicated message broker.

**Coordinator/worker**: AutoGen uses a `GroupChat` + `GroupChatManager` for multi-agent scenarios. The manager agent selects the next speaker based on either a round-robin rule or an LLM-based selection policy. Workers don't subscribe to topics; the manager routes explicitly. In two-agent scenarios, agents just take turns in a loop.

**Clarifying questions**: Workers ask questions by replying to the manager in the same conversation thread. There's no separate back-channel — questions surface in the primary dialogue. The human proxy intercepts when `human_input_mode` is set, otherwise the LLM decides autonomously.

**Peer review**: Implemented as a multi-turn conversation where one agent produces output and another critiques it. AutoGen has a `SocietyOfMind` pattern where an inner group produces a result and an outer group reviews it.

**Discovery**: No built-in registry. Agents are instantiated in code and wired together explicitly. The `GroupChat` object holds a list of agents — static membership.

**Transport**: Everything passes through Python function calls in-process. In the newer AutoGen 0.4 (agentchat rewrite), a distributed runtime uses gRPC or Azure Service Bus, but the message schema is the same. The API is transport-agnostic by design; switching the runtime doesn't change agent code.

**Key design decision**: Message type is always `dict` with a `role` and `content` field, modeled after the OpenAI chat format. This means any agent can read any message — there's no capability negotiation.

### CrewAI

CrewAI organizes agents into a `Crew` with explicit roles (`Agent` objects have a `role`, `goal`, and `backstory`). Tasks are `Task` objects assigned to agents, and the crew has a `Process` — either `sequential` or `hierarchical`.

**Coordinator/worker**: In hierarchical mode, a manager LLM (auto-created or user-provided) breaks down a goal and delegates `Task` objects to worker agents by name. Workers don't pick tasks; the manager assigns them. This is closer to a dispatcher pattern than a conversation.

**Clarifying questions**: Agents can be configured with `allow_delegation=True`, which lets them ask other agents for help. Delegation is tool-based — the agent calls a `DelegateWork` or `AskQuestion` tool, which triggers a sub-task. This is a key insight: back-channel communication is modeled as tool use, not a separate message type.

**Peer review**: Not a first-class primitive. Typically implemented by making review a sequential task — agent A produces, agent B reviews. Some users add a dedicated `QAAgent` as the last step.

**Discovery**: Agents are registered in the `Crew` at construction time. Delegation uses agent role names as addresses (e.g., "Ask the Senior Developer"). Role strings are the routing key.

**Transport**: In-process Python calls. CrewAI added an async execution mode and a "kickoff for each" batch mode, but there's no network transport option in the core library.

**Key design decision**: Role-based addressing. Agents have human-readable role strings rather than IDs. Routing logic matches by role, not by UUID. This is ergonomic but fragile if two agents share a role.

### OpenAI Swarm

Swarm (now superseded by the Agents SDK) was a minimalist reference implementation. Its core insight was that agent handoffs and context are the only two primitives you need.

**Coordinator/worker**: Agents can call a "transfer to agent" function, which returns an `Agent` object instead of a string. The orchestration loop detects this and re-routes the conversation to the returned agent. Coordination is done by returning agent references from tool functions, not by a central dispatcher.

**Clarifying questions**: Swarm has no back-channel. An agent that needs clarification either asks in the main conversation (which the human sees) or is expected to have enough context to proceed. The design assumes a human is in the loop.

**Peer review**: Not a pattern Swarm addresses — it's oriented toward linear handoff chains, not review loops.

**Discovery**: Agents are Python objects. The initial agent is passed to `run()`, and subsequent agents are discovered by following handoff tool returns. This is lazy discovery — you only know about the next agent, not the whole fleet.

**Transport**: Purely in-process. No network support.

**Key design decision**: Handoff-as-return-value. The routing decision is embedded in the tool result, not in a separate routing layer. Simple but limits parallel dispatch.

### LangGraph

LangGraph models agent systems as directed graphs where nodes are functions or LLM calls and edges define control flow. State is a typed dict that flows through the graph.

**Coordinator/worker**: A supervisor node reads the shared state and decides which worker subgraph to invoke next. Workers write results back to shared state. The supervisor then reads those results and decides the next step. This is essentially a state machine.

**Clarifying questions**: Workers write a "needs clarification" flag or a question field to shared state. The supervisor reads it and routes back to the requester or to a human-in-the-loop node. Questions are state mutations, not messages.

**Peer review**: Implemented as graph edges — after worker A completes, an edge can route to a reviewer node before returning to the supervisor. The reviewer reads worker A's output from state and writes feedback back.

**Discovery**: Nodes are registered in the graph at compile time. Dynamic dispatch is possible via conditional edges, but the set of possible targets is static.

**Transport**: By default, in-process Python. LangGraph Platform (cloud product) adds a persistence layer (Postgres) and enables resumable, distributed graphs. The state schema is the stable interface; the execution model can change.

**Key design decision**: Shared mutable state as the communication medium. Agents don't send messages to each other — they read and write a common state object. This eliminates the need for a message bus but makes concurrency harder.

### Lighter Approaches

**Agency Swarm** (open source, inspired by OpenAI Assistants): Defines communication as "send message" tools that agents call explicitly. An agent's "inbox" is just the message history of a sub-thread. No separate transport — messages are LLM context injections.

**bare-metal approaches using files or named pipes**: Some practitioners skip frameworks entirely and use a shared directory where agents write task files (JSON or markdown) and a coordinator polls for results. This is essentially a file-based message queue. It's transport-agnostic and resumable after crashes.

**Redis Pub/Sub with agent subscribers**: Common in production systems. Each agent subscribes to a topic. The coordinator publishes task messages. Workers publish results to a reply topic. The coordinator correlates by message ID. This pattern is fully decoupled and transport-swappable (replace Redis with NATS, a Unix socket, etc.).

### Convergent Design Patterns

After reviewing these frameworks, four patterns appear consistently:

**Pattern 1: Typed message envelope**
Every framework wraps payloads in an envelope with at minimum: sender, recipient (or topic), message type, and correlation ID. Even Swarm, which avoids explicit messages, encodes this in the tool call schema. The envelope is what makes routing, filtering, and reply correlation possible without a broker that understands payload content.

**Pattern 2: Role/capability addressing over UUID addressing**
Systems that use human-readable role strings (CrewAI's "Senior Developer", AutoGen's agent name) are easier to configure and debug. UUID-only addressing works but requires a side registry. The practical convergence is: agents have a stable ID (UUID or name) plus a set of capability tags. Routing can use either. This also enables substitution — if "worker-1" is busy, route to any agent with the "code-review" capability.

**Pattern 3: Back-channel questions as tool calls**
CrewAI models delegation and questions as tool calls. This is the right pattern because it keeps the LLM's primary conversation stream clean while allowing structured side communication. The tool call is the question; the tool result is the answer. This maps cleanly to any IPC mechanism — the tool implementation just needs to know how to reach the target agent.

**Pattern 4: State persistence as a first-class concern**
LangGraph's insistence on typed, serializable state is the most operationally sound pattern. When a session crashes or is interrupted, the graph can resume from the last checkpoint. For same-machine use, this can be a file. For network use, it's a database. The state schema is the stable contract between execution environments.

### Transport-agnostic vs. transport-coupled patterns

**Transport-agnostic** (work with any IPC):
- Typed message envelope with correlation ID
- Role/capability addressing
- Back-channel as tool call
- State serialization checkpoints

**Transport-coupled**:
- AutoGen's in-process `ConversableAgent` reply loop (assumes shared memory)
- Swarm's agent-as-return-value handoff (assumes in-process object references)
- LangGraph's conditional edge routing (assumes single executor with access to graph definition)

The transport-agnostic patterns all share one property: the communication primitive is a serializable data structure, not a function call or object reference. Any system that serializes its messages to JSON (or similar) can swap the transport layer without changing agent logic.

## Implications

For niwa's use case — same-machine Claude sessions coordinated through a workspace — the relevant patterns are:

**Adopt the typed envelope immediately.** Even for file-based IPC, define a JSON schema for messages with: `id`, `from`, `to`, `type`, `payload`, `correlation_id`, `timestamp`. This costs nothing and makes the system debuggable and transport-upgradeable.

**Use role/capability addressing, not session PIDs.** Claude sessions should register with a role ("coordinator", "reviewer", "api-implementer") when they start. The coordinator routes by role. Niwa already has the concept of a workspace with named repos — repo name or component name is a natural role anchor.

**Model clarifying questions as tool calls.** The Claude session acting as a worker should have a `ask_coordinator` tool (or `send_message` tool) that writes to a shared inbox. The coordinator polls or is notified. This keeps the worker's primary conversation intact and produces a structured audit trail.

**Start with files, design for sockets.** A directory-based message queue (one JSON file per message, named by ID) is the simplest possible transport for same-machine use. It survives session crashes, supports polling and `inotify`-based push, and can be replaced with a Unix domain socket or HTTP later without changing the message schema.

**Niwa's role**: Niwa provisions the communication infrastructure the same way it provisions `.env` files and CLAUDE.md overlays — by writing the inbox directories, the shared state file, and the agent registry file at workspace init time. Agents find their inbox path from a well-known location (e.g., `.niwa/mesh/` in the workspace root).

## Surprises

**Questions as tool calls is underrated.** Most write-ups about multi-agent systems focus on the coordinator-to-worker direction. The worker-to-coordinator back-channel (asking for clarification) is treated as an afterthought. CrewAI's tool-call model for this is cleaner than it sounds — it means a worker never has to break its primary conversation to ask a question, and the question is structured enough to route and log.

**Discovery is almost always static in practice.** Every framework reviewed has a static agent registry. Dynamic discovery (agents announcing themselves, coordinator finding available workers) exists in theory but frameworks don't implement it because it adds complexity with little payoff at small scale. For niwa's 2–6 agent scenario, static registration at workspace init is the right call.

**State persistence is where frameworks diverge most.** LangGraph treats it as load-bearing; AutoGen and CrewAI treat it as optional. In production, teams always add persistence to AutoGen/CrewAI because session crashes lose everything. For niwa, building persistence in from the start (as files in the workspace) avoids a painful retrofit.

**The "human in the loop" question is load-bearing.** Every framework has a different stance on where and how a human can intercept. AutoGen makes it explicit via `human_input_mode`. Swarm assumes a human is always present. LangGraph has an `interrupt` primitive. For niwa's use case, the root Claude session acts as both coordinator and "human proxy" — it's the session the user is actually talking to. The implication is that the communication layer should be able to surface messages to the user-facing session without requiring the user to manually copy-paste them.

## Open Questions

1. **Inbox polling vs. push notification**: File-based IPC works with polling, but polling frequency is a tradeoff between latency and CPU. `inotify` (Linux) provides push semantics for file changes. Does niwa want to build this in, or leave it to agent implementations?

2. **Message ordering guarantees**: For same-machine file-based IPC, rename-into-place gives atomicity but not ordering across writers. Does the niwa mesh need ordering, or is correlation-ID-based matching sufficient?

3. **Session lifecycle management**: Who starts and stops Claude sessions? Currently the user does this manually. For automated delegation, niwa would need to spawn sessions (via `claude --dangerously-skip-permissions` or similar) and track their PIDs. Is this in scope?

4. **Auth / trust model**: On the same machine, all agents have equal filesystem access. A malicious or buggy agent could write to another agent's inbox or overwrite the registry. Is a trust model needed, or is the threat model out of scope for v1?

5. **Message size limits**: Claude context windows are large but not infinite. If agents pass large artifacts (file contents, diffs) through the mesh, messages can get big. Should the mesh support references (pass a file path, not the file) rather than inlining payloads?

6. **How does the user observe the mesh?** The user talks to one session. If two workers are exchanging review feedback, the user doesn't see it. Does niwa need a "mesh observer" mode that surfaces inter-agent traffic to the user session?

## Summary

The frameworks surveyed converge on four durable patterns: a typed message envelope with correlation IDs, role-based addressing, back-channel questions modeled as tool calls, and persistent serializable state — and all four are transport-agnostic, meaning they work equally well over files, sockets, or HTTP. For niwa, the practical starting point is a file-based mesh provisioned at workspace init, with agents registering by role and communicating through JSON message files in a `.niwa/mesh/` directory. The biggest open question is session lifecycle management: file-based messaging is straightforward to implement, but spawning and supervising Claude sessions programmatically is a separate, harder problem that determines whether the mesh is fully automated or still requires the user to start each session manually.
