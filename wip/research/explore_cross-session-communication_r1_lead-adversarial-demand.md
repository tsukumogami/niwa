# Lead: Demand validation for cross-session communication

## Findings

### Q1: Is demand real?

**Confidence: High**

Multiple independent reporters have filed distinct issues on the anthropics/claude-code public GitHub repository, with different use cases and professional backgrounds. The issues carry maintainer-applied labels (`enhancement`, `area:core`, `area:agents`, `area:cowork`) that signal triage, not just auto-labeling. Several issues have 8–15 comments with substantive technical detail from different users.

Sampled issues with distinct reporters and substantive content:

- **#24798** (hmcg001, open, 15 comments, labels: area:core, area:tui, enhancement): User running 5 concurrent sessions on a large project — server migration, database, search, dashboards, and a terminal monitor — with no way for sessions to notify each other of changes. Multiple commenters described identical friction with different use cases: a physician in Chile running 4 sessions for clinical workflow (betoescobar46), an MLOps team running a 3-agent swarm (mlops-kelvin, nexus-marbell), and others.
- **#27441** (shanemericle01, closed as duplicate, 3 comments): Multi-machine setup (VPS + Mac + orchestrator); proposed a local socket or file-watch hook event for message injection. Closed as duplicate, meaning the issue is recognized — not rejected.
- **#21277** (benshawuk, closed as duplicate, 3 comments): Two independent sessions for architecture exploration and feature implementation with no shared context.
- **#5703** (miteshashar, closed as duplicate with autoclose): Sub-agents running in parallel with no way to redirect specific agents.
- **#4993** (coygeek, closed, 9 comments, labels: area:core, area:tools, enhancement): Detailed three-level feature proposal — messaging bus, shared whiteboard, and team abstraction — with concrete scenarios.
- **#48965** (ThatDragonOverThere, open, 2 comments, labels: area:agents, area:cowork, enhancement): PM-orchestrator pattern with N worker sessions; detailed 6 friction points from production use over weeks.
- **#30140** (lukaemon, closed as duplicate, 4 comments): Shared channel request for agent teams; currently workarounding with a shared `channel.md` file.
- **#28300** (MarioK1975, open, 8 comments, labels: area:agents, area:mcp, enhancement): Cross-machine multi-repo coordination for microservices.

The volume (8+ distinct issue threads found in this search, with multiple commenters in several), the label triage, and the technical depth across reporters all point to real, documented demand.

### Q2: What do people do today instead?

**Confidence: High**

Workarounds are well-documented in issue comments, blog posts, and open-source tooling. They cluster into four categories:

**File-based polling (most common)**
Sessions write to a shared markdown or JSON file and poll for changes. Reported in #24798 (shared markdown), #30140 (shared `channel.md`), #48965 (shared markdown with 15-minute poll cycles producing 28-minute ACK gaps). The session-bridge tool (PatilShreyas/claude-code-session-bridge) formalizes this pattern: each session gets `~/.claude/session-bridge/sessions/<id>/inbox/` and `outbox/` directories with JSON message files. Published March 20, 2026.

**Human as message bus (universal fallback)**
Explicitly named in #24798 ("copy-pasting terminal output as a poor man's message bus"), #27441 ("human relay the message manually — not scalable"), and #4993. Multiple reporters describe this as a daily workflow.

**tmux send-keys injection**
#27441 proposed this as a model; nexus-marbell in #24798 described actually implementing it (cron + tmux + two-step `send-keys` calls with a sleep, because tmux drops Enter if sent in the same call). Also used by Gas Town (gastownhall/gastown), a multi-agent workspace manager built on tmux with nudges, mail, and shared state primitives.

**Custom HTTP/WebSocket servers**
mlops-kelvin and nexus-marbell in #24798 built a full Ed25519-signed FastAPI messaging layer with SQLite-backed message store, cron-based wake polling, and a CLI client — running in production for weeks, 180+ PRs merged.

The a2a project (dopatraman/a2a) uses an MCP-client-per-session architecture connected via WebSocket to a central hub on port 7800, routing events between agents.

**`claude --print` headless subprocess**
Spawning `claude -p` (non-interactive) to generate responses — works but loses session context and incurs API cost per invocation. Described in #27441 and the session-bridge blog post as an attempted but abandoned workaround.

### Q3: Who specifically asked?

**Confidence: High**

Cited directly from GitHub issues (public, durable):

- **#24798** — opened by hmcg001; comments from betoescobar46, mlops-kelvin, finml-sage, nexus-marbell, valllabh, and others (15 comments total)
- **#27441** — opened by shanemericle01; closed as duplicate of #24798
- **#21277** — opened by benshawuk (Ben Shaw); closed as duplicate
- **#5703** — opened by miteshashar (Mitesh Ashar); closed as duplicate
- **#4993** — opened by coygeek (Coy Geek); 9 comments
- **#48965** — opened by ThatDragonOverThere; 2 comments
- **#30140** — opened by lukaemon (lucas); closed as duplicate, 4 comments
- **#28300** — opened by MarioK1975; 8 comments
- **#51256** — opened by lockezhou18; filed April 20, 2026, labeled duplicate on same day; label applied same day issue was filed, suggesting the issue is recognized in active triage

Third-party artifacts:

- PatilShreyas (Shreyas Patil), blog post March 20, 2026, and open-source session-bridge tool (GitHub: PatilShreyas/claude-code-session-bridge)
- dopatraman, GitHub: dopatraman/a2a (WebSocket-based hub)
- gastownhall (Steve Yegge), Gas Town — workspace manager built specifically for this problem, started late December 2025
- bfly123, GitHub: bfly123/claude_code_bridge (tmux split-pane multi-agent runtime)
- DEV Community post by non4me: "How I Made Two Claude Code Instances Talk to Each Other (With JSON Files, Obviously)"
- Medium post by brentwpeterson: "When Your AI Assistants Need to Talk to Each Other"

### Q4: What behavior change counts as success?

**Confidence: Medium**

Reporters describe outcomes but no single issue contains formal acceptance criteria authored by a maintainer. The stated desired behaviors, aggregated across issues:

From #24798: "Inter-session messaging — one session sends a message to another by session ID or name"; "shared project scratchpad — key-value store all sessions in the same project can read/write"; "event/notification bus — sessions subscribe to events"; "dependency sequencing — sessions coordinate automatically."

From #27441: "external process can send a message to a running Claude Code interactive session" without requiring the human to relay it, and without losing the interactive session's context.

From #48965: "Cross-session messaging alone would eliminate an estimated 70% of the coordination friction we've experienced." Specific: PM session posts a directive and worker session receives it event-driven within seconds, not polled over 15-minute cycles.

From #30140: A shared, persistent, ordered channel that survives context compression and agent idle/wake cycles, readable by all agents and the human.

From #4993: A `sendMessage(targetAgent, message)` API as the core primitive; a shared dynamic state store for file-in-progress coordination.

Common pattern: the minimum viable success is event-driven message delivery from one running session to another without the human as intermediary, surviving session restart. Shared state and discovery are secondary.

### Q5: Is it already built?

**Confidence: High (community tools exist; official capability is partial and constrained)**

**Official capability (experimental, constrained):**
Anthropic shipped Agent Teams in Claude Code v2.1.32 (February 5, 2026) as an experimental research preview, enabled via `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`. Agent Teams provides: a SendMessage tool for directed and broadcast messaging between teammates, a shared task list at `~/.claude/tasks/{team-name}/`, automatic idle notifications, and TeammateIdle/TaskCreated/TaskCompleted hooks.

Key constraints that leave gaps relevant to niwa's use case:
- Teammates are spawned by and subordinate to a single lead session; they cannot exist independently across separately-opened terminal sessions
- No session resumption with in-process teammates (`/resume` and `/rewind` don't restore them)
- One team per lead session; no cross-team communication
- Messages are ephemeral (land in context windows); no durable shared log
- Teammates load context from their working directory, not from a workspace-root CLAUDE.md hierarchy the way niwa structures it
- The team config is runtime state (`~/.claude/teams/{team-name}/config.json`) and not a provisioned workspace artifact

The agent teams model assumes sessions are spawned by the lead. The niwa use case involves separately-opened sessions (one per repo) that exist independently and need to communicate — a topology Agent Teams doesn't address.

**Community tools:**
Multiple third-party solutions exist, all working around the same gap:

- **session-bridge** (PatilShreyas/claude-code-session-bridge): file-based inbox/outbox JSON system, 9 bash scripts + jq, published March 20, 2026
- **a2a** (dopatraman/a2a): MCP client per session + WebSocket hub on port 7800
- **Gas Town** (gastownhall/gastown): full workspace manager with tmux orchestration, nudge/mail/shared-state primitives, git-backed work tracking, started December 2025
- **cc2cc**: mentioned in community discussions as MIT-licensed file-based agent-to-agent messaging for Claude Code (referenced in search summaries; exact repository not confirmed in this search)
- **claude_code_bridge** (bfly123/claude_code_bridge): tmux split-pane runtime with built-in communication layer
- **agents-council** (MrLesk/agents-council): MCP tool for feedback requests across Claude Code, Codex, Gemini, Cursor sessions

All community tools use either file polling, WebSocket hubs, or tmux injection — no production-ready, durable, event-driven primitive exists.

### Q6: Is it already planned?

**Confidence: Medium**

No public roadmap document found. Evidence that Anthropic is tracking and responding to this demand:

- Multiple issues closed as "duplicate" (not "won't fix"): #27441, #21277, #5703, #30140, #51256. Duplicates point to #24798, which remains open and labeled `area:core`. This is consistent with active tracking, not rejection.
- Agent Teams (v2.1.32, February 2026) directly addresses the simpler intra-team case. The feature's architecture (SendMessage, task list, mailbox) is consistent with eventually extending to cross-session scenarios.
- Issue #48965 was labeled `area:cowork` — a label that also appears on issue #41184 and is present on the #51256 duplicate, suggesting Anthropic has an internal "cowork" work area that may be the canonical tracking location.
- Claude Managed Agents launched April 8, 2026 (hosted infrastructure service) includes agent coordination as a core feature, signaling continued investment in multi-agent primitives.

No maintainer has publicly committed to a timeline or spec for cross-session messaging between independently-opened sessions.

---

## Calibration

**Demand not validated** does not apply here.

**Demand is validated as real.** The evidence meets the High bar for Q1 and Q3: multiple independent reporters across distinct use cases, maintainer triage labels applied, issues tracked as duplicates of an open canonical issue (#24798 labeled area:core), and a community ecosystem of workaround tools that would not exist without genuine friction.

**Demand validated as absent** does not apply. There is no evidence of explicit rejection: no closed PRs with maintainer rejection reasoning, no design doc that de-scoped this feature, no maintainer comment declining it. Every closed issue was closed as "duplicate," not as "won't fix" or "by design."

The open question is whether the specific niwa framing — provisioner-controlled workspace communication layer for independently-opened sessions, one per repo, with a workspace-root coordinator — is addressed by what Anthropic has built (Agent Teams) or is planning. Agent Teams requires sessions to be spawned by a lead; it does not support independently-opened sessions joining a communication mesh. That gap is where the niwa provisioner angle is differentiated and where no existing solution — official or community — currently lands.

---

## Surprises

**The community built serious infrastructure.** The mlops-kelvin/nexus-marbell swarm in #24798 — Ed25519-signed messages, FastAPI server, SQLite store, cron wake polling, 180+ PRs merged — was not a hobby project. Finding production-quality infrastructure built to fill this gap is stronger evidence of demand than issue volume alone.

**Issue #51256 was filed and labeled duplicate on the same day (April 20, 2026).** The same date as this research. The label was applied rapidly, suggesting active triage. The canonical destination (#24798) remains open and labeled area:core.

**Gas Town is a direct analog to the niwa provisioner idea.** gastownhall/gastown is a workspace manager (Go + Dolt + tmux) that provisions multi-agent coordination from the workspace level, with inter-agent mail and nudge primitives. It started December 2025 — before Agent Teams shipped. It is the closest existing project to what the niwa cross-session communication feature would be doing, but built as a standalone tool rather than as a workspace manager extension.

**Agent Teams' persistent channel gap is explicitly documented by a power user.** Issue #30140's description of the shared `channel.md` workaround — "you can, we've been doing it" — and the request for a first-class ordered log shows that even users of the official Agent Teams feature hit the same walls. The official feature solves ephemeral directed messaging but not durable shared state.

**The cross-repo dimension is underserved.** The niwa use case involves sessions in different repos within a workspace. Agent Teams sessions share a working directory. Issue #24798 explicitly describes the cross-repo pattern (5 sessions on different concerns of the same project). This is not solved by Agent Teams today.

---

## Open Questions

1. **What is the `area:cowork` label tracking?** It appears on multiple open and recently-triaged issues. If Anthropic has an internal cowork work area, knowing its scope would clarify whether the cross-independently-opened-session case is in or out of scope.

2. **Does issue #24798 have a linked internal tracking issue, project, or milestone?** The GitHub issue is the public face; the actual planning artifact may not be public.

3. **What are Agent Teams' actual message delivery semantics?** The docs say "automatic message delivery" but don't specify whether this survives context compaction, session restart, or teammate respawn. The #30140 report suggests it does not survive compaction.

4. **Can a niwa-provisioned communication layer interoperate with Agent Teams?** If Agent Teams eventually supports independently-opened sessions joining a team, a niwa-provisioned layer could complement rather than replace it. If not, niwa needs its own transport.

5. **Does Gas Town's mail/nudge system offer a design template?** Gas Town appears to have solved the provisioner-side problem (workspace-aware session registration, inter-agent mail). A technical spike on its architecture could accelerate niwa's design without duplicating the problem-solving effort.

6. **What is the failure mode when a session dies?** Multiple reporters mention sessions dying from context bloat. A cross-session communication layer must handle ungraceful session termination. None of the community tools found describe a robust handling of this case.

---

## Summary

Demand for cross-session communication in Claude Code is validated at high confidence: at least 8 distinct issue threads with multiple independent reporters, maintainer triage labels, all duplicates pointing to an open canonical issue (#24798, labeled area:core), and a community ecosystem of workaround tools ranging from 9-shell-script file polling to production Ed25519-signed HTTP servers. Anthropic shipped Agent Teams (February 2026) as a partial answer, but its architecture requires sessions to be spawned by a single lead — it does not address independently-opened sessions per repo, which is the niwa workspace topology. The largest open question is whether Anthropic's internal `area:cowork` work area already targets the independently-opened-session case, which would determine whether niwa is building infrastructure that Anthropic will eventually make redundant or infrastructure with durable value as a workspace-layer provisioner.
