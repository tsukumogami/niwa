# Lead: What actually happens when a Claude Code session sends a cross-session message to a peer name that matches two live sessions?
## Findings

### How Claude Code's cross-session messaging addresses peers

**Contract language (cross-session-messaging.md):**
- "Claude uses two tools for this: `ListAgents` to discover which agents it can reach, and `SendMessage` to deliver a message to one of them by name."
- ListAgents "identifies Claude Code sessions" and returns available agents.
- SendMessage parameters: `message` (required), `summary` (optional). No documented `to` parameter in the official tools reference.

**Finding:** Contracts confirm addressing by name, but SendMessage's `to` parameter format is not documented.

### Disambiguation affordance and when [ref] is shown

**Contract language:**
- "Claude adds a short identifier to each row of its listing and uses the identifier in the address."
- "Sessions can still share a name, for example when one of them runs an earlier version of Claude Code."

**Finding:** Documentation states identifiers exist but does not show format, when they appear, or whether they are stable.

### When a bare name matches multiple live sessions

**Contract language:**
- "Claude addresses the message in one of two ways...Claude adds a short identifier to each row and uses the identifier in the address."

**Critical gap:** Does not specify which session receives the message, whether send succeeds, which session is selected (oldest/newest/first), whether sender is informed, or if behavior is deterministic.

### What the sender receives when a name is ambiguous

**Contract language:**
- For @-mention: "When more than one live session answers to the mentioned name, Claude asks you which one you mean."
- For programmatic SendMessage: **No documentation found.**

**Finding:** User-facing @-mention prompts for disambiguation. Programmatic calls are unspecified.

**Related bug reports:** GitHub Issue #42999: "[BUG] SendMessage silently fails when using agent name — only agent ID works". Name-based sends may fail silently.

### Whether the receiving side knows it may not be the intended recipient

**Contract language:** No documentation found.

**Finding:** Receiving session sees sender's name but not disambiguation information.

### Scope of the name namespace

**Finding:** Name namespace spans subagents, teammates, local sessions, cloud sessions, and Remote Control sessions. Collisions are machine-global and cross-cloud when Remote Control enabled.

### Where session names come from and where they are displayed

**Critical for niwa:** "It doesn't check the `--name` of a background or `-p` session at startup."

**Finding:** Background sessions (niwa's use case) don't get uniqueness-checked, so two sessions can share the same name.

## Implications

1. **niwa dispatch with duplicate names creates unmitigated collision risk.** Background sessions aren't checked for name uniqueness at startup. Two `niwa dispatch --name worker` calls create two global-namespace sessions with the same name.

2. **SendMessage contract is silent on the critical case.** Documentation does not specify which session receives messages to ambiguous names, whether sends succeed, or whether senders are informed.

3. **"Latest wins" claim is unverified.** No documentation confirms the most recently started session deterministically receives the message.

4. **Identifier format and stability are undocumented.** Contract states identifiers exist but doesn't specify format, stability, or sender access.

## Surprises

- **SendMessage with names may already be broken.** GitHub Issue #42999 reports name-based sends "silently fail" — API returns success but message never arrives.
- **Background sessions skip name uniqueness checks.** Unlike interactive sessions (renamed on collision), background sessions are not checked at startup.
- **No explicit "latest wins" rule documented.** The exploration context claims this but official docs don't state it.

## Open Questions

1. **What is the exact SendMessage `to` parameter contract?**
2. **Which session receives a message when multiple share a name?**
3. **What does the sender see when targeting an ambiguous name?**
4. **What is the identifier format shown in ListAgents for ambiguous names?**
5. **Can a sender obtain the identifier before attempting a send?**
6. **Is the name-based SendMessage bug still present in the latest version?**
7. **How does niwa dispatch set the session name?**

## Summary

Official Claude Code documentation confirms background sessions (niwa's use case) are not uniqueness-checked at startup, so two dispatches with the same `--name` create colliding sessions in global namespace. SendMessage contract does not specify which session receives messages to ambiguous names, whether sends succeed, or what signal senders receive. GitHub issues suggest name-based SendMessage calls may fail silently. The "latest wins" claim is not verified in official documentation.
