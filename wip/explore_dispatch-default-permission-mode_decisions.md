# Exploration Decisions: dispatch-default-permission-mode

## Round 1
- The host default is a developer preference, not a detected capability: the user does not want bypass on one host (a corporate laptop) and does want it elsewhere. niwa takes the value as given and does not read managed policy or probe Claude Code.
- Precedence is flag > host default > workspace posture > nothing: the developer's machine choice beats the shared workspace config, deliberately reversing the default_dispatch_harness polarity.
- The workspace `[claude.settings] permissions` posture is to be deprecated: configuring permission posture per workspace makes no sense when the same workspace runs on hosts with different policies. Both stay supported during the transition.
- Reach is Claude and Codex dispatch only; niwa watch and ephemeral sessions are out.
- Raw passthrough of the host value to every agent is ruled out: a Claude value forwarded as Codex `--sandbox` breaks the launch, which matters on hosts that run both agents.
- Mapping a bypass default to Codex `danger-full-access` is ruled out: it disables the sandbox and writes trust, which the Codex posture design forbids.
- Post-launch mode-mismatch detection is not required for this feature (the user knows their host); it may be noted as a follow-up.
