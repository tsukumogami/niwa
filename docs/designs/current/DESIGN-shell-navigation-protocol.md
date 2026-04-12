---
status: Proposed
problem: |
  The shell wrapper's stdout-as-cd protocol assumes niwa's entire stdout is
  the landing path. Any other stdout content — from subprocesses, third-party
  libraries, verbose flags, or CI output modes — silently breaks navigation.
  The current implementation already has this bug, and future output modes
  make it worse.
decision: |
  TBD — under investigation.
rationale: |
  TBD — under investigation.
---

# DESIGN: Shell Navigation Protocol

## Status

Proposed

## Context and Problem Statement

`niwa create` and `niwa go` are "cd-eligible" commands: after they run, the
shell should change directory to the workspace or repo they operated on. Because
a subprocess (the niwa binary) cannot change its parent shell's working
directory directly, this requires a protocol between the binary and the shell
wrapper.

The current protocol: the shell wrapper captures all of niwa's stdout via `$()`
and treats the entire output as a directory path to `cd` into. This works when
stdout contains only the path but fails silently whenever anything else is
written to stdout — git clone progress, `fmt.Printf` calls in the workspace
package, or output from third-party libraries.

The fundamental problem is that we can't control what all code paths spawned
from the CLI entry point will print to stdout. Any future log level flag,
verbose mode, CI output mode, or upstream library could add stdout output and
silently break navigation again. The contract must survive this environment: it
needs to let niwa communicate the landing directory reliably even when stdout
contains other output.

## Decision Drivers

- **Shell compatibility**: The mechanism must be parseable by a POSIX shell
  function (bash/zsh) without external tools beyond what ships with the OS.
- **Output transparency**: Progress output, error messages, and diagnostic
  lines should still reach the terminal — the protocol must not suppress them.
- **Future output modes**: The contract must survive `--verbose`, `--debug`,
  `--json`, `NIWA_LOG_LEVEL`, and CI/quiet modes that may be added later.
- **Subprocess independence**: We cannot require that every subprocess niwa
  spawns routes its output to a specific FD. Third-party code will write where
  it writes.
- **Minimal shell complexity**: The shell wrapper function must remain
  maintainable by contributors who aren't shell experts.
