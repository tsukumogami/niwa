---
status: Accepted
problem: |
  niwa consumes a machine-identity credential to resolve a workspace's vault
  secrets, but gives a developer or agent no help obtaining that credential.
  Onboarding a machine-identity workspace today means hand-running fragile,
  non-portable REST and base64 shell to mint and store the credential, with no
  secret-hygiene guarantee, duplicated across every onboarding document.
goals: |
  A single niwa command mints a credential on an existing machine identity,
  verifies it authenticates and can read the target environment, and stores it
  where niwa's own resolution reads it -- portably, non-interactively, and without
  ever printing the secret. Onboarding docs shrink to "run this command" plus the
  human login steps.
upstream: docs/briefs/BRIEF-provider-auth-provision.md
motivating_context: |
  A downstream workspace's machine-identity onboarding runbook productizes this
  flow as hand-rolled shell and names a native niwa command as its follow-up. niwa
  already reads machine-identity credentials; this PRD specifies the command that
  helps a user obtain one.
---

## Status

Accepted

Requirements for the native `niwa provider-auth provision` command, downstream of
`docs/briefs/BRIEF-provider-auth-provision.md`. This feature is Complex: it adds a
credential-write path to a secrets API and touches the boundary of niwa's recorded
"reads only" stance, so it warrants a technical DESIGN before implementation. The
Decisions and Trade-offs section records the three framing questions the BRIEF
deferred here; the ones that turn on architecture are handed forward to that DESIGN.

## Problem Statement

niwa resolves `vault://` secrets for a machine-identity workspace by authenticating
with a `client_id` / `client_secret` pair it reads from `provider-auth.toml` or a
vault entry. The read side is built; the acquisition side is not. A developer or
agent onboarding such a workspace has to obtain the credential by hand: confirm the
right provider organization is selected, call the provider's REST API to read an
identity's `client_id` and mint a fresh `client_secret`, check the new credential
authenticates and can read the target environment, then write it to the exact on-disk
schema niwa expects.

Every step is a liability. The organization check is hand-decoded base64 that yields
the wrong answer under BSD `base64` on macOS. The mint and verify calls are raw `curl`
against endpoints the developer must look up, with the secret at risk of landing in
shell history or terminal scrollback. The final write must match a precise schema and
file mode. Because the sequence is transcribed into each onboarding document, the
fragility is copied rather than fixed, and nothing guarantees the secret is never
echoed. The people affected are workspace maintainers and the agents that drive
onboarding; it matters now because a downstream workspace already depends on this flow
and carries it as fault-prone prose.

## Goals

- Reduce credential acquisition for a machine-identity workspace to one niwa command
  that an agent can drive non-interactively.
- Move the portability and secret-hygiene guarantees out of copy-pasted prose and into
  niwa, so no onboarding reader re-implements them.
- Keep the command generic: it productizes the machine-identity flow niwa already
  targets, with no workspace-, org-, or provider-vocabulary-specific constants baked
  in.
- Bound the credential-write surface deliberately, so the feature does not become an
  open-ended "niwa manages your identities" mandate.

## User Stories

- As a developer joining a team whose workspace already has a shared machine identity,
  I want to run one command that mints and stores my own credential, so that my next
  `niwa apply` resolves the workspace's secrets with no further setup and I never touch
  the provider's REST API.
- As an agent driving an onboarding procedure, I want to invoke the provision step
  non-interactively and detect success from an exit code and machine-readable output,
  so that I can proceed without scripting raw REST calls and without a secret entering
  my transcript.
- As a maintainer whose credential is expiring or may be exposed, I want to re-run the
  command against the same identity to mint a fresh secret and overwrite the stored
  one, so that I restore a known-good state without reconstructing the flow from
  memory.

## Requirements

### Functional

- **R1.** The CLI SHALL expose a `provider-auth` parent command with a `provision`
  subcommand.
- **R2.** Given a reference to an **existing** machine identity, `provision` SHALL mint
  a fresh credential (`client_secret`) on that identity via the provider's credential
  API. It SHALL NOT create a new identity.
- **R3.** `provision` SHALL verify, before storing, that the minted credential
  authenticates and can read the specified target environment. Storage SHALL NOT occur
  if verification fails (verify-before-store ordering).
- **R4.** `provision` SHALL write the verified credential to exactly one caller-selected
  storage target, in the exact schema niwa's existing resolution reads. The supported
  targets are the `provider-auth.toml` file and a vault-sourced entry; a single run
  writes one of them, not both.
- **R5.** `provision` SHALL support a fully non-interactive invocation: when the
  identity, the target environment, and the storage target are all supplied via flags
  and/or environment, the command SHALL run to a terminal outcome without emitting any
  interactive prompt.
- **R6.** `provision` SHALL emit machine-readable output under a `--json` flag. On
  success the JSON object SHALL contain, at minimum, the identity acted on, the storage
  target written, and the minted credential's identifier (for later revocation). The
  secret value SHALL NOT appear anywhere in that output.
- **R7.** `provision` SHALL use a distinct, documented exit code for each terminal
  outcome: success, authentication failure, target-environment-not-readable, and
  storage-write failure.
- **R8.** Re-running `provision` against the same identity and target SHALL mint a new
  credential and overwrite the stored credential, leaving a known-good state (rotation
  is a supported, repeatable operation).
- **R9.** When `provision` writes the `provider-auth.toml` target, the file SHALL be
  written with mode `0600`.
- **R10.** The command's flags, output, and diagnostics SHALL name the identity,
  environment, and target generically, without hardcoding any one provider's vocabulary
  or any workspace-/org-specific identifier.

### Non-functional

- **R11.** `provision` SHALL rely only on portable, in-process logic for the mint,
  verify, and store steps -- no dependence on `curl`, `jq`, `base64`, or other external
  shell utilities -- and SHALL behave identically on Linux and macOS.
- **R12.** The secret value SHALL NOT be printed to stdout, stderr, logs, or error
  messages at any point; all verification SHALL assert existence and access, never the
  value.
- **R13.** Failure diagnostics SHALL distinguish authentication failure,
  target-environment-not-readable, and storage-write failure. Each failure message SHALL
  name the stage that failed and state a remediation, and SHALL NOT contain the secret
  value.

## Acceptance Criteria

- [ ] `niwa provider-auth provision` exists as a subcommand and its `--help` documents
  the identity, environment, and storage-target inputs.
- [ ] Given an existing identity and a readable target environment, a provision run
  exits 0, and afterward niwa resolves the target's secrets using the stored credential
  (verified by a subsequent resolution/audit succeeding).
- [ ] A provision run whose minted credential cannot read the target environment exits
  with the documented target-not-readable code and writes nothing to any storage
  target (verify-before-store held).
- [ ] `--json` output on success is valid JSON carrying the identity and storage target
  and omitting the secret value; grepping the full stdout+stderr of any run for the
  secret value finds nothing.
- [ ] Re-running provision against the same identity/target leaves niwa able to resolve
  secrets with the newly stored credential (rotation works and is idempotent in effect).
- [ ] A `provider-auth.toml` written by provision has mode `0600`.
- [ ] Authentication failure, target-not-readable, and write failure each produce a
  distinct exit code; each failure message names the failed stage and states a
  remediation.
- [ ] A run with the identity, target environment, and storage target all supplied via
  flags/environment completes to a terminal exit with no interactive prompt (verifiable
  by running with stdin closed and observing a terminal exit code).
- [ ] A provision run whose provider session is scoped to the wrong organization exits
  with the documented authentication-failure code and writes nothing to any storage
  target.
- [ ] Grepping the command's `--help`, flag names, defaults, and emitted messages against
  a banned-term set (provider product names and workspace/org identifiers) finds no
  match.
- [ ] Provision runs with identical behavior on Linux and macOS with no external shell
  utility invoked (no `curl`/`jq`/`base64` shell-out).

## Decisions and Trade-offs

- **Scope is mint-on-existing-identity, not create-identity (closes BRIEF open
  question 1, partially deferred to DESIGN).** The command mints a credential on an
  identity that already exists and never creates a new one. Rationale: minting a
  credential on an existing identity is not gated by the provider's plan, whereas
  creating a new identity is -- so the create path would fail for exactly the users who
  need onboarding most. This choice also bounds the write surface. Whether this bounded
  write formally *reverses* or *carves an exception into* the "reads only, does not
  auto-mint" stance recorded in `docs/prds/PRD-machine-identity-vault-sync.md` is an
  architectural reconciliation deferred to the DESIGN; the PRD's requirement-level
  position is that the write is limited to minting a secret on an already-existing
  identity plus writing niwa's own credential config, not a general vault-write mandate.
- **Storage target must be selectable with a deterministic default (closes BRIEF open
  question 2, default choice deferred to DESIGN).** R4 requires supporting both the
  `provider-auth.toml` file and a vault-sourced entry, one per run. The alternatives for
  the no-flag default are: default to the `provider-auth.toml` file (simplest, always
  writable, but leaves a secret-bearing file on disk), default to the vault-sourced entry
  (no local secret file, but depends on a writable vault session), or require the caller
  to name a target explicitly (no surprise, but more friction for the common path). The
  PRD decides only that a default MUST exist and MUST be deterministic; which of the
  three wins turns on the storage architecture and is handed to the DESIGN, because
  choosing now would presuppose the write-path design the DESIGN owns.
- **Generic vocabulary is a hard requirement, not a preference (closes BRIEF open
  question 3).** R10 forbids provider- or workspace-specific vocabulary in the command
  surface. The rejected alternative is to let the first consumer's identifiers (its
  provider product name, org, or project) appear as defaults or in help text for
  convenience; that loses because it would bind a generic niwa command to one workspace
  and force a breaking change the moment a second workspace adopts it. The first
  consumer's identifiers live in that workspace's private overlay and its downstream
  onboarding skill instead, never in this command.

## Known Limitations

- `provision` runs *after* the human has authenticated a provider session scoped to the
  identity's organization; the command does not perform or manage that interactive
  login. A session scoped to the wrong organization is surfaced as an authentication
  failure, not silently worked around.
- The command productizes the single machine-identity provider flow niwa already
  targets. Generalizing across other vault providers is out of scope and left to future
  work.

## Out of Scope

- **Creating a new machine identity.** Distinct, provider-plan-gated, and deliberately
  excluded; the command acts only on an identity that already exists.
- **Interactive provider and GitHub logins.** The organization-pick logins and the Path
  A / Path B choice are human steps owned by the downstream onboarding skill that calls
  this command.
- **Workspace-specific onboarding orchestration and its constants.** Sequencing the
  human steps around this command, and any org/project/identity identifiers, belong to a
  downstream onboarding skill and the private workspace overlay.
- **Other vault-provider backends.** Only the machine-identity flow niwa already targets
  is in scope.
