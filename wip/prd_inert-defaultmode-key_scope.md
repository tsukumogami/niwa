# /prd Scope: inert-defaultmode-key

## Problem Statement

Workspace maintainers who declare a permission posture, and developers who
open sessions inside niwa-managed instances, are affected. Since Claude Code
2.1.257, the `bypassPermissions` value niwa writes into generated project
settings for a `permissions = "bypass"` workspace doesn't take effect. Instead
it wins the settings merge and is downgraded to `default`, which overrides the
developer's own user-level posture. The `"ask"` declaration has always
produced `askPermissions`, which isn't a valid mode. The fix is needed now
because every niwa instance of a bypass workspace currently makes sessions
*more* restrictive than doing nothing, while the generated file claims
otherwise.

## Initial Scope

### In Scope

- What niwa writes about permission posture into the three generated settings
  documents: the instance-root `settings.json`, each repo's
  `settings.local.json`, and the workspace-root `settings.json`, for each
  declared value (`bypass`, `ask`, undeclared).
- Where the dispatch derivation of `--permission-mode` reads the declared
  posture from.
- What the `ask` declaration produces.
- The niwa docs that describe the generated setting as the posture mechanism.
- Removal of the dead `WorkerPermissionMode` reader (implementation-level; the
  requirement is that no reader of the retired value remains).

### Out of Scope

- The `[claude.settings] permissions` TOML surface: its key and accepted names.
- Interactive sessions a developer opens themselves (they get their own settings).
- `niwa watch`'s supervised-review posture (`defaultMode: "default"`), which is
  honored and must not regress.
- Moving remote control off `--settings`; a merged inline-settings builder.
- Containment for bypass workers.
- Codex posture keys.

## Research Leads

1. **Resume and re-entry**: when a dispatched worker is resumed, does the
   re-entry command carry `--permission-mode`? Read
   `internal/cli/dispatch_reentry.go` and the printed re-entry forms. This
   closes the BRIEF's third open question: whether a resume counts as a session
   niwa starts.
2. **Workspace-root and hook-launched sessions**: which paths launch a Claude
   Code session at the workspace root or through the SessionStart hook, is there
   any niwa seam that could carry a flag, and what exactly do
   `docs/guides/ephemeral-session-instances.md` and
   `DESIGN-mcp-root-instance-distribution.md` promise? This closes the second
   open question.
3. **What `ask` should mean**: the history of the `ask` mapping in
   `PRD-config-distribution.md` and its original intent, and the measured
   consequence of each candidate: explicit `default`, which is honored from
   project scope and forces prompting even over a permissive user setting,
   versus writing nothing, where user settings apply. This closes the first
   open question.
4. **Testability of the requirements**: what an acceptance check can observe
   for each requirement. That covers the derived argv (the `@critical`
   `dispatch.feature` scenarios), the bytes of the generated documents (the
   golden manifests), and the absence of the dead reader. It also covers where
   the seven fixture-based unit tests would give false confidence.

## Coverage Notes

The `/explore` research already covers the scope rule (measured on 2.1.267),
the producer/consumer graph, the drift and test blast radius, the `--settings`
channel, instance metadata, prior art, and external readers. Phase 2 agents
should read `wip/research/explore_inert-defaultmode-key_*` before investigating
and must not re-derive those findings. The requirements must stay at the WHAT
level. Whether the derivation reads the effective config or a renamed key is a
DESIGN question, and the PRD should state the requirement it has to satisfy
without choosing between the two.
