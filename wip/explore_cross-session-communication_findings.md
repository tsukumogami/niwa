# Exploration Findings: cross-session-communication

## Core Question

Can niwa provision a communication layer that lets Claude sessions within a workspace exchange messages without user intermediation? The goal is a workspace-aware mesh where sessions can ask questions, receive answers, delegate work, and review each other's output — starting with same-machine, designed to support network transport later.

## Round 1

### Key Insights

- **Demand is validated at high confidence** (adversarial lead): 8+ distinct GitHub issue threads with multiple independent reporters, maintainer triage labels (`area:core`, `area:cowork`), all duplicates pointing to open canonical issue #24798 — never closed as "won't fix." Community has built serious infrastructure: bash file-polling scripts, production Ed25519-signed FastAPI servers, tmux injection runtimes. The pattern the user described (human as copy-paste relay) is explicitly named as a daily workflow across many reporters.

- **Agent Teams exist but don't cover the niwa topology** (Claude Code primitives + adversarial leads): Anthropic shipped Agent Teams (Feb 2026, experimental) but requires sessions to be spawned by a single lead. Independently-opened sessions per repo — the niwa workspace pattern — are not supported. This is the exact gap where niwa has differentiated value.

- **Niwa already has a `[channels]` placeholder** (niwa workspace model): `WorkspaceConfig.Channels` is typed as `map[string]any` with the comment "placeholder" in `internal/config/config.go`. The workspace.toml scaffold template includes a commented `[channels]` section. This was clearly anticipated. A `ChannelMaterializer` would fit alongside `HooksMaterializer`, `SettingsMaterializer`, etc. in the existing provisioning pipeline (`Applier.Create`/`Apply`).

- **`workspace-context.md` is already the session injection mechanism** (niwa workspace model + message schema): Niwa already generates `workspace-context.md` and imports it into each session's CLAUDE.md. The channel broker's socket path can be injected here without any new Claude Code primitives — sessions learn their channel address the same way they learn the repo layout.

- **File-based queues + Unix sockets are the right starting transport** (IPC mechanisms): File-based queues (atomic rename into inbox dirs, `fsnotify` for push notification) provide zero-dependency durable delivery with no daemon. Unix domain sockets are the right low-latency complement when durability isn't needed. NATS JetStream is the best long-term transport if niwa ever gains a persistent daemon; SQLite WAL is the right middle ground if durability + zero-dependency are both required. Named pipes, Redis, and nanomsg were eliminated.

- **Four transport-agnostic patterns from multi-agent frameworks** (multi-agent patterns): typed message envelope with correlation IDs, role-based addressing over UUID, back-channel questions as tool calls, and persistent serializable state. All four hold at 2–8 agents and work with any IPC mechanism. These should be design constraints, not implementation choices.

- **Message schema is well-defined** (message schema): An 8-field envelope (`v`, `id`, `type`, `from`, `to`, `reply_to`, `task_id`, `sent_at`) covers all 5 use cases. Session identity = workspace instance name + user-assigned role (with PID as tiebreaker). The `type` field is a dotted routing key (`question.ask`, `task.delegate`, `review.feedback`, `status.update`, `session.hello`).

- **Gas Town is the closest existing analog** (adversarial lead): gastownhall/gastown (December 2025) is a Go + Dolt + tmux workspace manager that provisions inter-agent mail and nudge primitives at the workspace level — exactly the provisioner angle niwa is considering. Its architecture is a candidate design template.

### Tensions

- **`[channels]` namespace collision**: The existing `Channels` config key appears to be reserved for plugin-backed external channels (Telegram, etc.). Using it for the built-in broker risks semantic confusion. Resolution options: `[channels.mesh]` for built-in broker vs. `[channels.telegram]` for plugins (distinguishing by well-known key name), or a separate top-level `[mesh]` config section. This is a naming decision that must be made before the design doc.

- **MCP channels vs. independent transport**: MCP channels (`claude/channel` capability) are the most "native" delivery mechanism (push unsolicited messages into a running session's context), but currently require `claude.ai` OAuth auth and `--dangerously-load-development-channels`. An independent Unix socket broker works today with no constraints but diverges from Claude Code's own direction. If channels graduate from research preview, the independent approach may become redundant infrastructure.

- **Build vs. wait for Anthropic's `area:cowork`**: The `area:cowork` label on multiple open issues and the Claude Managed Agents launch (April 8, 2026) signal active investment. But the independently-opened-session topology is specifically not covered by Agent Teams, and no timeline for addressing it was found. Building niwa's own layer carries some redundancy risk; waiting carries opportunity cost.

- **Session lifecycle management is separable but intertwined**: The messaging layer (transport, schema, registry) can be built without session spawning. But the value of the mesh is much lower if the user must manually start each session. Session spawning is a harder problem (requires `claude` CLI subprocess management, PID tracking, restart handling) and may need to be a separate follow-on.

### Gaps

- **Gas Town's technical architecture hasn't been analyzed**: It's the closest existing analog for workspace-level session provisioning + inter-agent mail. A design spike on its architecture could provide a tested reference design and avoid re-solving known problems.

- **MCP channels graduation timeline is unknown**: Whether to build on MCP channels vs. an independent transport is the biggest architectural choice. It depends on a timeline that is not publicly committed.

- **Concurrency model for `sessions.json`**: No daemon means concurrent session writes to the registry require file locking or an atomic-swap protocol. The details matter for correctness but weren't fully designed in this round.

### Decisions

- **Named pipes, Redis, and nanomsg eliminated**: Lose on critical dimensions (named pipes: half-duplex, blocking open; Redis: heavier than NATS for no additional benefit; nanomsg: no durability).
- **Session identity = workspace instance + role**: PID as tiebreaker only. Routing logic must not depend on PID. This matches the existing `InstanceState` model.
- **File-based queue as the default starting transport**: Zero dependencies, process-restart safe, proven by community tools. Unix socket as optional low-latency complement.
- **Scoping session spawning as out of scope for Phase 1**: The communication layer (provisioning, registry, transport, schema) is the Phase 1 problem. Automated session spawning is a follow-on.

### User Focus

Not applicable (auto mode — decisions captured above based on evidence).

## Accumulated Understanding

Niwa can provision a workspace-aware inter-session communication layer with moderate implementation effort, building primarily on existing patterns in its own codebase. The key facts:

**What's already there**: A `[channels]` config placeholder, a `ChannelMaterializer`-ready provisioning pipeline, `workspace-context.md` as the injection mechanism, and `InstanceState` as the identity anchor. The plumbing has been partially thought through already.

**What's needed**: A session registry (`<instance-root>/.niwa/sessions/` with a `sessions.json` file), per-session inbox directories or a broker socket, a `niwa session register` subcommand for self-registration, and injection of the channel address into `workspace-context.md`. The design decisions are: transport (file-based vs. Unix socket vs. hybrid), broker model (daemon vs. daemonless), and how `[channels]` config is structured.

**The biggest uncertainty**: Whether to build on MCP channels (Claude Code's own push primitive) or an independent transport. MCP channels are maturing but constrained today. An independent transport works now but may become redundant. The safe bet is an independent transport with a documented migration path to MCP channels when they graduate.

**Community validation**: Multiple production-quality workarounds exist (session-bridge, Gas Town, a2a). All solve the same problem niwa would solve, at the cost of being standalone tools rather than workspace-native. Niwa's provisioner angle — setting up the channel as part of workspace create/apply — is genuinely differentiated.

**Recommendation heuristic**: Enough is known to define the design. The remaining open questions (MCP timeline, Gas Town architecture, `[channels]` naming) are design-doc-level decisions, not exploration-level unknowns. Ready to crystallize.

## Decision: Crystallize
