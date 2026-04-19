# Crystallize Decision: clone-output-ux

## Chosen Type

Design Doc

## Rationale

What to build is settled: cargo-style two-layer output display (scrolling log for
completed events + single `\r`-rewritten status line at bottom) with goroutine-based
git stderr capture filtering progress frames from error lines, backed by a thin
Reporter abstraction, with TTY-gated inline display falling back to append-only.

The open questions are architectural: how to design the Reporter interface and where
it lives, exactly how the subprocess stderr goroutine reads and classifies lines, 
whether to add a library dependency or implement the ANSI mechanism directly, how the
TTY detection layering works (`isatty` + `NO_COLOR` + `CI` + `--no-progress`), and
how the injectable `io.Writer` fields already in the codebase get wired to the
Reporter. These decisions need to be on record so they're not re-litigated during
implementation.

## Signal Evidence

### Signals Present

- **What to build is clear, how to build it is not**: target UX (cargo two-layer
  model) is decided; Reporter interface shape, subprocess capture mechanism, and
  library choice are not.
- **Technical decisions need to be made between approaches**: suppress vs. capture
  for git subprocess output; mpb vs. schollz/progressbar vs. no-dependency manual
  ANSI; where the Reporter abstraction boundary lives.
- **Architecture questions remain**: Reporter interface (methods, placement in
  Applier struct), goroutine pipe implementation for subprocess stderr, TTY
  detection layering across the CLI and workspace packages.
- **Multiple viable implementation paths surfaced**: three approaches to git
  subprocess output (suppress, capture, wrap-only); multiple library candidates
  with different integration models.
- **Architectural decisions made during exploration that should be on record**:
  goroutine capture chosen over suppression; bubbletea ruled out; cargo model
  preferred over Docker per-row; `--no-progress` preferred over `--json`;
  overlay suppression must not change.
- **Core question is "how should we build this?"**: yes.

### Anti-Signals Checked

- "What to build is still unclear" — not present; target UX is settled.
- "No meaningful technical risk or trade-offs" — not present; subprocess capture
  is a real risk surface.
- "Problem is operational, not architectural" — not present; this is clearly
  architectural.

## Alternatives Considered

- **PRD**: Ranked lower. Requirements were given as input to the exploration (user
  described the problem clearly before research started). Tiebreaker PRD vs. Design
  Doc: given → Design Doc wins.
- **No artifact**: Ranked lower because architectural decisions were made during
  exploration (goroutine capture approach, no bubbletea, cargo model, Reporter
  abstraction). These need a permanent record; wip/ is cleaned before merge.
- **Plan**: Demoted. Open architectural decisions (Reporter interface, subprocess
  capture design) must be made before implementation can be sequenced.
