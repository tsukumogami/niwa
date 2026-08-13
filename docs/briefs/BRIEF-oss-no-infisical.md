---
schema: brief/v1
status: Accepted
problem: |
  niwa treats resolving a workspace's declared secrets as a precondition for
  materializing an instance, so a host with no vault backend cannot create one
  at all. The workspace is still created and registered and reports healthy,
  leaving the user holding a well-formed workspace that can never be used.
outcome: |
  A contributor on a host with no vault backend runs the create path and gets a
  working instance, plus a legible inventory of which declared secrets went
  unresolved and what that costs them. Strictness stays available for anyone
  who wants the command to fail instead.
motivating_context: |
  Reported by a maintainer setting up a new host without the vault CLI
  installed. Investigation found the same wall reached from a second direction
  by anyone cloning a public workspace config without access to the overlay
  that supplies its values.
---

## Status

Accepted

Framing is settled through the four required sections. The downstream PRD owns
the requirements articulation.

Three framing details were deferred rather than settled here, and the PRD's
Decisions and Trade-offs section is where they close: whether the strictness
surface is a CLI flag, a declaration in workspace configuration, or both;
whether one strictness switch governs all four failure gates or granularity is
per-gate; and what a total provisioning failure should say, as distinct from a
partial one.

## Problem Statement

niwa resolves a workspace's declared secrets before it will materialize an
instance, and treats any shortfall as fatal. That ordering makes an absent or
unauthenticated vault backend equivalent to a broken workspace, even when
nothing niwa itself does with those values is load-bearing for cloning repos or
writing configuration.

The consequence is worse than a plain failure. `niwa init` succeeds: it clones
the configuration, registers the workspace, and reports success. Every command
that materializes an instance then fails — `niwa create`, `niwa dispatch`, and
the SessionStart hook that provisions an ephemeral instance for a background
session. `niwa status` reports the workspace healthy throughout. The user is
left holding a well-formed, permanently unusable workspace, with no signal
connecting the cheerful init to the wall they hit next.

Two different users reach that wall through two different gates. Someone
cloning a public workspace configuration without access to an overlay never
contacts a vault at all — their configuration declares required keys and
contains no mechanism that could ever supply them, which is a static
contradiction reported late and in terms that name neither the missing layer
nor the vault. Someone who has the overlay but has not installed or
authenticated the vault CLI fails earlier, inside the resolver, and never
reaches the required-key check. A fix aimed at one of those does nothing for
the other.

The escape hatch that appears to cover this does not. `--allow-missing-secrets`
downgrades a key the backend does not hold; it does not downgrade a backend
that cannot be reached, which is precisely the shape of "the CLI is not
installed" and "the session expired." Its own help text reads as though it
covers both. It is also absent from `niwa init`, and structurally unreachable
from `niwa dispatch` and the session hook, which take no flags because no human
is present to pass one.

Underneath sits an unsettled question the project has argued both ways in
writing: whether a configuration that declares required keys it cannot itself
satisfy is an authoring error the author must fix, or a situation niwa owes a
degraded mode. Requirement tables are described in one place as documentation
for manual secret setup and in another as a gate that aborts apply.

## User Outcome

A contributor who has never heard of the workspace's vault backend can clone a
public workspace configuration, create an instance, and start working. The
instance materializes with every value niwa could resolve. The values it could
not resolve are absent rather than blank, listed once on the terminal in terms
that make sense to someone with no knowledge of where they were supposed to
come from, and recorded in the generated environment files themselves so the
gap is visible at the moment someone wonders why a variable is missing.

A maintainer setting up a new machine gets the same instance and a message that
names the backend, says it could not be reached, and says what to install. When
they install and authenticate, re-running the apply fills in the gaps without
re-creating anything.

An operator who wants a shortfall to stop the command keeps that behavior by
asking for it, and gets it on the unattended paths too, not only where a flag
can be typed.

Nobody is told about configuration layers they cannot see. A message niwa
cannot justify — including any claim about whether a private overlay exists —
is not printed.

## User Journeys

### A first-time contributor clones a public workspace configuration

A developer with no affiliation to the project reads the workspace's README and
runs the documented init and create commands. They have no vault CLI, no
credentials, and no access to any overlay. Today the create fails on keys they
have never heard of. After this feature, the instance is created, the repos are
cloned, and a short block tells them which declared values were not resolved and
that the workspace is usable without them. They start reading code within the
minute. Nothing in the output references a configuration layer they cannot see,
because niwa cannot distinguish a private overlay it may not read from one that
does not exist.

### A maintainer sets up a fresh machine

Someone who normally works with a fully provisioned environment gets a new
laptop and runs create before installing the vault CLI. Today the resolver
aborts with an error naming one arbitrary key out of several. After this
feature, the message names the backend, states that it could not be reached,
gives the install pointer, and lists every affected key rather than the first
one iteration happened to reach. The instance materializes. Once they install
and log in, re-applying fills the values in.

### A background worker is provisioned by a session hook

A background session starts and the SessionStart hook provisions an ephemeral
instance. No human is watching and no flag can be passed. Today a shortfall
exits non-zero with no output the agent can read, so the worker boots with no
instance and no explanation. After this feature the worker gets its instance,
and the inventory of unresolved keys reaches it through the channel the hook
protocol actually delivers, so the agent knows which capabilities are
unavailable before it tries to use them.

### An operator keeps the strict behavior

Someone using niwa to provision environments where a missing credential must
stop the run declares that intent once, in a place the unattended paths read.
Provisioning continues to fail loudly on a shortfall, on the session hook and
dispatch paths as well as the interactive ones. The declaration lives where a
public contributor's experience cannot be changed by a layer they cannot see.

## Scope Boundary

### In

- Where secret resolution sits relative to instance materialization, across
  `niwa create`, `niwa apply`, `niwa dispatch`, and the SessionStart
  ephemeral-instance path.
- The four independent failure gates a first-run user meets: an unsatisfiable
  declaration with no resolution mechanism, an unreachable provider, a
  post-merge required-key shortfall, and promotion of an absent key into the
  generated Claude settings.
- The distinction between "no provider is configured" and "a provider is
  configured but unreachable," including whether the two produce different
  messages and different severities.
- How an unresolved key is represented in generated environment files, given
  that the representation has to be legible to a reader opening the file and
  parseable by the code that reads those files back.
- The strictness surface that restores fail-on-shortfall behavior, and its
  reach onto paths that cannot pass a flag.
- What the SessionStart hook emits when an instance materializes with
  unresolved keys.
- The wording constraint that no message may assert or imply the existence of a
  configuration layer niwa cannot verify.

### Out

- Which vault backend niwa integrates with, and adding or swapping backends.
  The question is what happens when a backend is absent, not which one it is.
- The content of any particular workspace's secret declarations. Correcting a
  specific workspace configuration is separate downstream work in that
  configuration's own repository, even though this feature is what makes the
  correction expressible.
- Secret redaction, scrubbing, and the credential audit log. Those are settled
  elsewhere and this feature does not revisit them.
- Non-interactive CI behavior as a distinct persona with its own requirements.
  CI is served by the same strictness surface an operator uses.
- The auto-discovery repository-count threshold and the quality of the API
  error surfaced when repository listing is rate-limited. Both were found while
  investigating first-contact experience and are tracked separately.
- Whether an API key genuinely precludes remote control on dispatch. That gate
  is deliberate, documented, and orthogonal to secret resolution.

## References

- `docs/prds/PRD-vault-integration.md` — the requirement contract that makes a
  required-key shortfall fatal, and the flag that does not downgrade it.
- `docs/prds/PRD-workspace-visibility-overlay.md` — the overlay merge model,
  and the two passages that describe requirement tables as documentation and as
  a gate.
- `docs/prds/PRD-env-example-failure-policy.md` — the existing four-level
  severity ladder with a global rung, the closest in-repo precedent for a
  per-key severity model.
- `docs/guides/ephemeral-session-instances.md` — the SessionStart provisioning
  path that cannot pass flags.
