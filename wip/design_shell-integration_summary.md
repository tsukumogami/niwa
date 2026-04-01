# Design Summary: shell-integration

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** Niwa's create command can't navigate the user into the new workspace because binaries can't change the parent shell's directory. Needs shell integration using the eval-init pattern.
**Constraints:** Must support bash and zsh. Must work standalone (without tsuku). Must be transparent UX (no special syntax for users). Communication protocol must handle concurrent shells and failed commands.

## Security Review (Phase 5)
**Outcome:** Option 2 - Document considerations
**Summary:** Design is sound. Two implementation considerations documented: stdout protocol safety invariant (double-quoting) and path containment validation for the go subcommand's repo argument.

## Current Status
**Phase:** 5 - Security
**Last Updated:** 2026-04-01
