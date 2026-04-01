# Crystallize Decision: navigate-workspace-after-create

## Chosen Type

Design Doc

## Rationale

The exploration answered "what to build" (shell integration for post-create workspace
navigation) but left "how to build it" open on several fronts. The eval-init pattern
was identified as the right approach, but the specific design needs work: binary-to-shell
communication protocol, init subcommand structure, relationship to the existing env file,
which subcommands the wrapper intercepts, and whether completions bundle into the init
output.

Key architectural decisions were made during exploration (niwa owns this, not tsuku;
eval-init over env file wrapper; tsuku generalization rejected) that need permanent
documentation. These decisions and their rationale will be lost when wip/ is cleaned
unless captured in a design doc.

## Signal Evidence

### Signals Present

- **What to build is clear, how is not**: Shell navigation is the goal. The eval-init
  pattern is the right family of approaches, but protocol details (directive file vs.
  output parsing vs. structured protocol) and subcommand design (intercept `create` only
  vs. add `go` command) are open.
- **Technical decisions between approaches**: Three communication protocols identified
  (output parsing, directive file, exit code). Two init patterns (env file function vs.
  `niwa init` subcommand). Each has different fragility and race-condition profiles.
- **Architecture/integration questions remain**: How does `niwa init` relate to the
  existing `~/.niwa/env`? Does it replace it or complement it? How does the install
  script change?
- **Multiple viable implementation paths**: At minimum three niwa-only approaches plus
  the tsuku-generalized approach (ruled out but worth documenting the rationale).
- **Architectural decisions made during exploration**: Niwa-only vs. tsuku generalization
  was resolved with clear evidence. Eval-init over pure env file was recommended.
  These need permanent record.
- **Core question is "how should we build this?"**: Requirements are clear from issue #31.
  The exploration was about approach, not scope.

### Anti-Signals Checked

- **What to build is still unclear**: Not present. Issue #31 and exploration converge
  on clear requirements.
- **No meaningful technical risk or trade-offs**: Not present. Protocol choice has
  real fragility/race-condition implications.
- **Problem is operational, not architectural**: Not present. This is a shell integration
  architecture question.

## Alternatives Considered

- **No Artifact**: Ranked lower because architectural decisions were made during
  exploration (niwa-only, eval-init pattern, tsuku rejection) that need permanent
  documentation. These would be lost in wip/ cleanup without an artifact.
- **PRD**: Ranked lower because requirements were provided as input (issue #31),
  not discovered during exploration. The "what" is clear; the "how" is the gap.
- **Plan**: Ranked lower because open architectural decisions remain (protocol,
  init design). Can't sequence work before the approach is decided.

## Deferred Types

- **Decision Record**: The "niwa vs. tsuku" choice could fit a standalone ADR, but the
  broader design questions (protocol, init subcommand, completions bundling) need more
  than a single-choice record. A design doc subsumes the decision record.
