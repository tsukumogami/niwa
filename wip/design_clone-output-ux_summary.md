# Design Summary: clone-output-ux

## Input Context (Phase 0)

**Source:** /explore handoff
**Problem:** Niwa's create and apply commands dump a linear log of git subprocess output and status messages to stderr with no TTY awareness. The target is a cargo two-layer model (scrolling log + single updating status line) with goroutine-based git stderr capture.
**Constraints:**
- Overlay sync output must remain suppressed (privacy requirement R22)
- Non-TTY behavior must be identical to today (append-only)
- bubbletea and full TUI ruled out
- Apply loop is sequential (no multi-bar concurrent display needed)
- Machine-readable mode via --no-progress flag (not --json)

## Decisions From Exploration

- Cargo two-layer model as target pattern
- Goroutine pipe for git subprocess stderr capture
- Thin Reporter abstraction for niwa's 29 output sites
- TTY detection: isatty() + NO_COLOR + CI env var + --no-progress flag

## Key Open Questions For Design

- Reporter interface shape: methods, naming, placement in Applier struct
- Goroutine pipe implementation: line classification, error vs. progress frame detection
- Library choice: mpb vs. schollz/progressbar vs. no-dependency manual ANSI
- TTY detection: where does it live in the call stack? CLI layer or workspace package?
- How does the status line interact with per-repo timing (start vs. completion events)?
- What is the non-TTY output format? (current behavior kept, or structured prefix like `[clone] repo: done`)

## Current Status

**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-04-19
