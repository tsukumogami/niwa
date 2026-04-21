# Explore Scope: cross-session-communication

## Visibility

Public

## Core Question

Can niwa provision a communication layer that lets Claude sessions within a workspace exchange messages without user intermediation? The goal is a workspace-aware mesh where sessions can ask questions, receive answers, delegate work, and review each other's output — starting with same-machine, designed to support network transport later.

## Context

The user currently operates multi-repo workflows by manually copy-pasting messages between Claude sessions in different niwa workspaces: one session files an issue and reviews the PR, another implements it, and the user acts as the relay. The long-term vision is a workspace with N repos and N+1 Claude sessions (one per repo, one at the workspace root) where the root session can delegate, coordinate disputes, and follow up as work progresses — without user intermediation. Niwa is the provisioner that sets up the communication layer, not the relay itself (unless nothing suitable exists). Same-machine first, with future optionality for network transport.

## In Scope

- Same-machine IPC between Claude sessions in a niwa workspace
- Niwa as the provisioner that sets up and registers the communication layer per workspace
- Coordinator-to-worker and peer-to-peer session topologies
- Message types: questions, answers, work delegation, code review feedback, and others TBD

## Out of Scope

- Network/cross-machine communication (future optionality, not designed now)
- Replacing niwa's existing workspace provisioning for repos and config
- Agent frameworks that require replacing Claude Code with something else

## Research Leads

1. **What IPC mechanisms are viable for same-machine inter-process messaging at this scale?**
   Unix sockets, named pipes, file-based polling, SQLite, embedded brokers — understand which ones handle async back-and-forth between processes that may start and stop independently, with a design path to network transport.

2. **Does Claude Code already expose any inter-session or subprocess communication primitives?**
   The CLI has `--resume`, MCP servers, hooks, and the SDK has session management. Before building anything new, understand what the existing surface area can do.

3. **How do existing multi-agent frameworks handle coordinator-to-worker and peer-to-peer patterns, and what can niwa adapt?**
   AutoGen, CrewAI, and others have tackled this. The goal isn't to adopt a framework, but to understand which design patterns hold up at small scale (2–6 agents, same machine).

4. **What would the message schema need to look like to support the known use cases?**
   Questions mid-implementation, code review feedback, work delegation, status updates — what's the minimal message envelope that covers these without over-engineering?

5. **What changes does niwa's workspace model need to provision and register sessions?**
   Niwa knows the repos in a workspace, but not which Claude sessions are running against them. What session registry or discovery mechanism would niwa need to add?

6. **Is there evidence of real demand for this, and what do users do today instead?** (lead-adversarial-demand)
   You are a demand-validation researcher. Investigate whether evidence supports pursuing this topic. Report what you found. Cite only what you found in durable artifacts. The verdict belongs to convergence and the user.

   ## Visibility

   Public

   Respect this visibility level. Do not include private-repo content in output that will appear in public-repo artifacts.

   ## Six Demand-Validation Questions

   Investigate each question. For each, report what you found and assign a confidence level.

   Confidence vocabulary:
   - **High**: multiple independent sources confirm
   - **Medium**: one source type confirms without corroboration
   - **Low**: evidence exists but is weak
   - **Absent**: searched relevant sources; found nothing

   Questions:
   1. Is demand real? Look for distinct issue reporters, explicit requests, maintainer acknowledgment.
   2. What do people do today instead? Look for workarounds in issues, docs, or code comments.
   3. Who specifically asked? Cite issue numbers, comment authors, PR references.
   4. What behavior change counts as success? Look for acceptance criteria, stated outcomes.
   5. Is it already built? Search the codebase and existing docs for prior implementations.
   6. Is it already planned? Check open issues, linked design docs, roadmap items.
