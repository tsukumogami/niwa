# Crystallize Decision: coordinator-loop

## Chosen Type

Design Doc

## Rationale

What to build is established: four targeted changes to niwa's coordinator-delegation and stall-recovery paths. The remaining uncertainty is entirely architectural — how to implement each change. Three of the four require non-trivial design decisions between competing approaches, and two architectural choices made during exploration (rejecting skill-level progress reporting, choosing resume over fresh spawn) must survive past branch close. A design doc is the right place to record both the "how" decisions and the rationale behind the "what" choices that led here.

## Signal Evidence

### Signals Present

- **What to build is clear, how to build it is not**: The exploration established four changes (delegation contract injection, stop hook, resume-with-reminder, niwa_ask typed error) but left open how each is implemented — specifically session ID capture mechanism, stop hook automation level, watchdog code path changes, and error response format.
- **Technical decisions between approaches remain**: Session ID acquisition has three candidate approaches (new MCP tool, stdout parsing, env-var injection), each with different trade-offs. Stop hook has two automation levels (fully automated CLI call vs. reminder output). These are real architectural choices.
- **Architecture, integration, and system design questions remain**: Resume-with-reminder requires extending TaskState.Worker, adding a new MCP tool, modifying runWatchdog(), and adding a resume-attempt counter. The niwa_ask error change requires modifying handleAsk routing logic.
- **Multiple viable implementation paths surfaced**: Session ID capture has three distinct paths; stop hook automation has two. Both need evaluation and a documented choice.
- **Architectural decisions made during exploration that should be on record**: Skill-level progress reporting fix rejected (breaks abstraction). Fresh-spawn restart replaced by resume-with-reminder. These decisions need permanent homes.
- **Core question is "how should we build this?"**: All remaining open questions are implementation-level, not requirement-level.

### Anti-Signals Checked

- **What to build is still unclear**: Not present. Four specific changes are well-defined.
- **No meaningful technical risk or trade-offs**: Not present. Session ID plumbing and resume path both carry risk.
- **Problem is operational, not architectural**: Not present. This is a daemon behavior change with schema impact.

## Alternatives Considered

- **PRD**: Scored +2 but Design Doc wins cleanly (+6). Requirements emerged during exploration, but they're clear enough now that a PRD would be redundant — the design doc can capture them inline as constraints/requirements sections. Tiebreaker (requirements identified vs. given as input) favors PRD slightly, but the score gap and the depth of open implementation questions override it.
- **Plan**: Demoted. No upstream design doc exists, and architectural decisions (session ID mechanism, stop hook automation) are still open. Can't sequence work that hasn't been designed.
- **No Artifact**: Demoted. Multiple people would need to understand these changes; key architectural decisions made during exploration would be lost at branch close.
- **Decision Record**: Demoted. Multiple interrelated decisions (not a single choice), requiring a design doc rather than a narrow decision record.
