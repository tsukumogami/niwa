---
status: Proposed
problem: |
  After niwa create, users must manually cd into the workspace directory because
  a compiled binary cannot change the parent shell's working directory. The tool
  needs shell integration that wraps certain commands with a shell function to
  enable transparent navigation. The eval-init pattern (used by zoxide, direnv,
  mise) is the right approach, but the specific protocol, subcommand design, and
  relationship to niwa's existing env file need architectural decisions.
---

# DESIGN: Shell Integration

## Status

Proposed

## Context and Problem Statement

Issue #31 identifies a UX gap: after `niwa create`, users land in the same
directory they started in and must manually cd to the new workspace. This is a
fundamental constraint of compiled binaries -- child processes cannot modify the
parent shell's working directory.

Exploration confirmed that the eval-init pattern (`eval "$(tool init shell)"`) is
the dominant modern approach for compiled CLIs needing shell integration. Zoxide
is the closest analog: binary resolves a path, shell function does cd. The
pattern also handles completions, which cobra already generates for free.

The exploration also resolved a broader question: whether tsuku should provide a
general post-install shell integration mechanism. Research found tsuku has no
such capability today (no action for sourceable files, no auto-sourcing), and
generalizing would cost 200+ lines across two repos with no second consumer.
Cobra's built-in completion commands remove completions as a validating use case.
Niwa should own its shell integration.

The remaining design questions are:
- Binary-to-shell communication protocol (how the binary tells the wrapper "cd here")
- Init subcommand structure and output format
- Relationship to the existing ~/.niwa/env file and install.sh
- Which subcommands the shell function intercepts
- Whether completions bundle into the init output

## Decision Drivers

- **Proven patterns over novel design**: the eval-init pattern is battle-tested
  across zoxide, direnv, mise, starship, and others
- **Minimal shell code**: the binary should generate shell glue, not maintain
  hand-written bash/zsh scripts
- **Both bash and zsh required**: fish support can be deferred
- **Transparent UX**: users should type `niwa create` and land in the workspace
  without remembering special syntax
- **Fragility and race conditions**: the communication protocol between binary and
  shell function must handle concurrent shells, failed commands, and output format
  changes
- **Install simplicity**: adding shell integration should be a one-line rc file change
- **Independence from tsuku**: niwa must work when installed standalone

## Decisions Already Made

These choices were settled during exploration and should be treated as constraints:

- **Niwa owns shell integration, not tsuku**: tsuku has no post-install shell mechanism,
  and adding one costs 200+ LOC across two repos with only one consumer (niwa).
  Completions are handled per-tool by cobra, removing the second use case.
- **Eval-init pattern is the right approach**: proven by zoxide, direnv, mise. Lets the
  binary version its shell output, handles multiple shells via a single subcommand,
  and is a known convention.
- **Tsuku generalization is deferred**: not rejected, but not warranted by current
  evidence. If more tools need post-install shell functions, revisit.
