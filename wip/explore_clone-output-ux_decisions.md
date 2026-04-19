# Exploration Decisions: clone-output-ux

## Round 1

- **Docker-style per-row multi-line display eliminated**: apply loop is sequential
  (no goroutines), making per-row cursor repositioning unnecessary complexity.
- **Cargo two-layer model selected as target pattern**: single status line at bottom
  with scrolling log for completed events. Maps cleanly to niwa's sequential
  per-repo workflow.
- **Full TUI approach (bubbletea) eliminated**: requires full architectural rewrite
  due to incompatibility with scattered fmt.Fprintf calls and subprocess pipes.
- **Git subprocess approach: capture via goroutine**: user chose to pipe git stderr
  through a goroutine reader, filtering \r-terminated progress frames while
  forwarding \n-terminated error lines. Enables inline error surfacing.
- **Machine-readable flag: --no-progress preferred over --json**: no concrete
  machine-readable consumer identified that needs structured per-repo data;
  progress suppression is sufficient.
- **Overlay sync output suppression must not change**: privacy requirement (R22)
  is a hard constraint.
