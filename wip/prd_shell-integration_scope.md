# /prd Scope: shell-integration

## Problem Statement

After `niwa create`, users must manually cd into the workspace directory because
compiled binaries can't change the parent shell's working directory. There's also
no way to jump to a specific repo within a workspace. The predecessor tools
(newtsuku/resettsuku) solved this with shell functions, establishing UX patterns
that niwa users expect. Shell integration must be optional with explicit
lifecycle management.

## Initial Scope

### In Scope
- Post-create navigation (landing in workspace or specific repo)
- Workspace/repo navigation for existing workspaces (go command)
- Shell completions (bundled with shell integration)
- Shell integration lifecycle (install, uninstall, status)
- Installer opt-out (--no-shell-init flag)
- Runtime detection and hinting when shell integration is missing
- Bash and zsh support

### Out of Scope
- Fish shell support (deferred)
- Tsuku post-install shell mechanism (tsuku platform decision)
- Changes to niwa's core commands (create, apply, etc.) beyond stdout protocol
- Interactive/TUI repo selection

## Research Leads

1. **User stories from design decisions**: Extract testable requirements from the
   6 design decisions — each decision implies specific user-facing behaviors.
2. **Predecessor UX contract**: Document which newtsuku/resettsuku behaviors are
   preserved, changed, or dropped — and why.
3. **Non-functional requirements**: Shell startup impact, error handling, concurrent
   shell behavior, upgrade path safety.

## Coverage Notes

This is a retrofit PRD. The design doc (DESIGN-shell-integration.md) and its 6
decision reports contain all source material. Research agents should extract
requirements from these artifacts rather than doing fresh investigation.
