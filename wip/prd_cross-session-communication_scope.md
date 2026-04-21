# /prd Scope: cross-session-communication

## Problem Statement

Developers using niwa to manage multi-repo workspaces often run multiple Claude sessions simultaneously — one per repo, plus a coordinator at the workspace root. When sessions need to exchange information (clarifying questions, task delegation, code review feedback), the user must manually copy-paste between terminals. This "human as relay" pattern breaks flow and prevents Claude from operating autonomously across a workspace. Niwa can solve this by provisioning a messaging layer at workspace setup time, so sessions can communicate directly.

## Initial Scope

### In Scope

- Workspace.toml configuration to opt into the messaging layer (`[channels]`)
- Niwa provisioning the channel infrastructure during `niwa create` and `niwa apply`
- Session self-registration via a `niwa session register` subcommand (called at session startup)
- A pull-model message delivery mechanism: Claude polls for messages via an MCP tool niwa provisions
- Message types: question/answer, task delegation, code review feedback, status updates, session hello/bye
- Role assignment: auto-derived from repo name, overridable via `NIWA_SESSION_ROLE`
- Message TTL with sender-side failure notification on expiry
- Same-machine operation only
- UX: what the user configures, what Claude sees, what happens in error cases

### Out of Scope

- Automated session spawning (user still opens Claude sessions manually)
- Network/cross-machine transport
- MCP channels integration (deferred until channels graduate from research preview)
- NATS or Redis as transport (requires daemon niwa doesn't have yet)
- Integration with Anthropic's Agent Teams

## Research Leads

1. **UX patterns for pull-based message delivery in CLI tools**: How do other tools (Gas Town, session-bridge) surface messages to an interactive agent? What does a good "check your inbox" UX look like for a Claude session vs. a human-facing CLI?
2. **Error and failure modes**: What are the realistic failure scenarios (session offline, message TTL expiry, duplicate roles, broker unavailable)? What do users and sending sessions need to see in each case?
3. **Role/identity UX**: How should role assignment feel for the user? Auto-derive from repo name vs. explicit assignment — tradeoffs in a multi-repo workspace where repo names may be ambiguous.
4. **Observable mesh state**: What information does the user need to monitor the mesh? What does `niwa session list` need to show? What does a session log of inter-session traffic look like?

## Coverage Notes

- Success criteria need to be defined in terms of latency (how quickly does a sent message appear?), reliability (what delivery guarantees does v1 provide?), and developer experience (how many steps from "I have a workspace" to "sessions can talk").
- The coordinator/worker topology is the primary use case, but peer-to-peer (worker-to-worker) should not be architecturally excluded.
