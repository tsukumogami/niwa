# Brief Discovery: oss-no-infisical

## Scoping (auto mode)

Seeded by a completed two-round exploration in this same worktree. The
discovery conversation is collapsed against those findings rather than re-run.

## Problem / Outcome pair

**Problem.** niwa treats resolving a workspace's declared secrets as a
precondition for materializing an instance. When the vault backend is absent or
unauthenticated, instance creation fails outright instead of producing a
workspace with the values it could resolve. The workspace itself is created and
registered, and `niwa status` reports it healthy, so the user holds a
well-formed workspace that can never produce an instance.

**Outcome.** A contributor on a host with no vault backend runs the create path
and gets a working instance, plus a legible inventory of which declared secrets
were not resolved and what that costs them.

## Grounding established during exploration

- `niwa init` succeeds; the failure is at every instance-materializing command:
  `niwa create`, `niwa dispatch`, and the SessionStart ephemeral-instance hook.
- Four independent hard-fail gates sit in front of a first-run user: a
  parse-time check for a `vault://` ref with no declared provider; the
  resolver's `ErrProviderUnreachable`; the post-merge required-key check; and
  materialize-time promotion of an absent key into the Claude settings env
  block.
- Two distinct users hit two different gates. Someone with no access to a
  configuration overlay never touches the vault at all — their failure is a
  static declaration-without-supply contradiction. Someone with the overlay but
  no vault CLI dies inside the resolver and never reaches the required check.
- The documented escape hatch, `--allow-missing-secrets`, downgrades only a
  missing key, never an unreachable provider, so it is inert against both. Its
  help text implies otherwise.
- Peer tools hard-fail on missing required secrets routinely, but none makes
  secret resolution a precondition for *constructing* the environment.

## Framing-shift answer

No signal. This is a cold start with no prior BRIEF, PRD, DESIGN, or PLAN on
disk for the topic.

## Journey entry points identified

1. First-time contributor cloning a public workspace config, no vault access.
2. Maintainer on a fresh machine, before installing or authenticating the CLI.
3. Background worker provisioned by a session hook — no human, no flags.
4. Operator who wants today's hard gate kept as a provisioning guard.

## Deferred to the downstream PRD

- Whether strictness is expressed as a CLI flag, a config action, or both.
- Whether one strictness switch governs all four gates or granularity is
  per-gate.
- The exact annotation syntax for unresolved keys in generated env files.
