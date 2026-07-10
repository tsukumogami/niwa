---
schema: brief/v1
status: Accepted
problem: |
  Onboarding a niwa workspace that authenticates to a vault provider with a
  machine identity forces developers and agents to hand-run fragile, non-portable
  shell to mint and store the credential. The steps are error-prone, duplicated in
  every onboarding doc, and offer no secret-hygiene guarantees.
outcome: |
  A maintainer or agent onboarding a machine-identity workspace runs one niwa
  command that mints, verifies, and stores the provider credential -- portably and
  without ever printing a secret -- instead of copy-pasting REST and base64 shell.
motivating_context: |
  A downstream workspace's machine-identity onboarding runbook documents a validated
  two-login provisioning flow built entirely from hand-rolled curl, jq, and base64,
  and its own follow-up names productizing that flow as a native niwa command. niwa
  already consumes machine-identity credentials; it does not yet help a user obtain
  one.
---

## Status

Accepted

Framing for the native `niwa provider-auth provision` command. The brief stops at
the feature's problem, outcome, journeys, and boundary; requirements are the
downstream PRD's to capture. One boundary question -- whether minting a credential
extends or carves out an exception to niwa's existing "only reads" stance -- is
carried forward to the PRD and design to settle, not decided here.

## Problem Statement

niwa resolves `vault://` secrets by shelling out to a provider CLI, and for the
machine-identity path it authenticates with a `client_id` / `client_secret` pair it
reads from `provider-auth.toml` or a vault entry. But niwa gives a user no help
*obtaining* that credential in the first place. Today, onboarding a workspace that
uses machine-identity auth means hand-running a sequence of low-level steps: decode a
session token's org claim to confirm the right organization is selected, call the
provider's REST API to read an identity's `client_id` and mint a fresh
`client_secret`, check that the new credential authenticates and can read the target
environment, then write the credential body to the conventional storage location.

Each step is a liability. The token-claim decode is hand-rolled base64 that silently
yields the wrong answer under BSD `base64` on macOS. The mint and verify calls are raw
`curl` against endpoints a developer has to look up, with the secret at risk of
landing in shell history or terminal scrollback. The final write has to match an exact
on-disk schema and file mode. The whole sequence is transcribed into every onboarding
document that needs it, so the fragility is copied rather than fixed, and there is no
single place that guarantees the secret is never echoed. What should be one
deterministic operation is instead a fault-prone ritual re-derived per workspace.

## User Outcome

A developer or an agent onboarding a machine-identity workspace obtains a working,
verified provider credential by running a single niwa command. They point it at an
existing machine identity, and niwa mints a credential, confirms it authenticates and
can read the target environment, and stores it where niwa's own secret resolution will
find it -- reporting success or a precise failure without ever printing the secret
value. The developer never assembles a REST call, never decodes a token by hand, and
never pastes a secret. Onboarding docs shrink to "run this command" plus the
irreducibly human login steps, and the portability and secret-hygiene guarantees live
in niwa rather than in prose each reader has to execute correctly.

## User Journeys

### Developer onboards against a shared team identity

A developer joining a team whose workspace already has a shared machine identity runs
the provision command, naming that identity. niwa mints a client secret on the
existing identity, verifies it can read the workspace's environment, and writes the
credential to the developer's chosen storage target. The developer's next `niwa apply`
resolves the workspace's secrets with no further setup. The developer never sees the
secret and never touches the provider's REST API.

### Agent provisions non-interactively while driving onboarding

An agent following an onboarding procedure reaches the credential step. It invokes the
provision command in a non-interactive form -- identity and target supplied as
arguments or environment, structured success signalled by exit code and machine-
readable output, no interactive prompt. The command performs the mint / verify / store
mechanics the agent would otherwise have to script from raw REST calls, and the agent
proceeds to the next onboarding step on a clean exit. Secrets stay out of the agent's
transcript because the command never emits them.

### Maintainer rotates a compromised or expiring credential

A maintainer needs to replace a credential that is expiring or may be exposed. They
re-run the provision command against the same identity to mint a fresh secret and
overwrite the stored credential, restoring a known-good state without reconstructing
the flow from memory or re-reading a runbook.

## Scope Boundary

### In

- Minting a fresh `client_secret` on an **existing** machine identity via the
  provider's credential API.
- Verifying the newly minted credential authenticates and can read the target
  environment before it is stored.
- Writing the verified credential to a niwa-recognized storage target (the
  `provider-auth.toml` file and/or a vault-sourced entry) in the exact schema niwa's
  resolution already reads.
- Secret hygiene as a property of the command: the secret value is never printed to
  stdout, logs, or error output; verification asserts existence and access, not value.
- A non-interactive invocation shape suitable for an agent to drive.

### Out

- **Creating a new machine identity.** Reading and minting on an existing identity is
  the boundary; provisioning the identity itself is a distinct, provider-plan-gated
  operation the command does not attempt.
- **Interactive provider and GitHub logins.** The `infisical login` organization
  picks, `gh auth login`, and the Path A vs Path B choice are irreducibly human and
  stay with the human, orchestrated by the downstream onboarding skill that calls this
  command.
- **Workspace-specific onboarding orchestration and its constants.** Sequencing the
  human logins around this command, and any org/project/identity identifiers, belong
  to a downstream onboarding skill and the private workspace overlay that carries those
  constants, not to this generic command.
- **Provider backends beyond the one niwa's machine-identity auth already targets.**
  The command productizes the existing machine-identity flow; generalizing across other
  vault providers is separate future work.

## References

- `docs/prds/PRD-machine-identity-vault-sync.md` -- establishes the machine-identity
  credential schema niwa reads and the "reads only" stance this feature's boundary
  question turns on.
- `docs/prds/PRD-vault-integration.md` -- the vault provider abstraction and
  `vault://` resolution this feature builds on.
