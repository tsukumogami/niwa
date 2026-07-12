---
schema: brief/v1
status: Draft
problem: |
  niwa vault onboarding is a long, cross-context choreography spanning a team
  phase and an individual phase. Every step is mechanical, but the whole
  sequence must land on an exact credential shape or it fails silently at a
  later `niwa apply`, far from the mistake. Depending on the vault topology the
  individual phase may also require a login switch, and the operator gets no
  help working out which. Today it lives as hand-run shell in runbooks.
outcome: |
  A team admin or a developer runs one command, `niwa onboard`, and is walked
  through setup as a wizard. It handles the mode they're in, makes the vault
  topology an explicit choice, automates every mechanical and exact-shape step,
  and pauses only for the logins that topology actually needs. The credential
  comes out in the exact contract shape by construction, the wizard confirms it
  resolves before declaring success, and nobody ships a silently broken vault.
motivating_context: |
  Two prior efforts productized the individual pieces of this flow in isolation:
  tsukumogami/niwa#194 (mint-and-store a credential on an existing identity) and
  tsukumogami/niwa#199 (a doctor that validates the credential contract live).
  Neither owned the whole choreography, the topology choice, or the team-phase
  setup. This brief frames the wizard those pieces become building blocks of.
---

# BRIEF: niwa onboard

## Status

Draft

This brief frames a net-new niwa command that owns the entire vault-onboarding
choreography. The downstream PRD owns the requirements (the wizard's interface,
mode detection, resume mechanics, the full corner-case set); this brief frames
the problem, outcome, journeys, and boundary. Two closed draft PRs
(tsukumogami/niwa#194 and tsukumogami/niwa#199) are folded in as internal
building blocks rather than shipped as standalone commands.

## Problem Statement

niwa resolves a workspace's `vault://` secrets by authenticating with a machine
identity's `client_id` / `client_secret` pair. Getting a workspace to the point
where that resolution works is a long setup sequence, and the read side gives no
help performing it. Someone onboarding a machine-identity workspace has to run
the whole thing by hand.

The sequence spans two phases. A team admin, once per workspace, creates the
machine identity in the vault org, attaches Universal Auth to it, grants that
identity read access on the target environment, and creates the folder structure
the workspace expects. Then every developer, once each, sets up their personal
overlay, mints a fresh client secret against the org that hosts the workspace
vault, stores the credential into the vault that backs their personal overlay,
and confirms it resolves.

That crossing from the workspace-vault org to the personal-overlay vault is where
a hidden assumption lives. When the workspace vault and the personal overlay
share one account, the developer stays in a single session throughout. When the
workspace vault lives in a dedicated org and the overlay vault lives in the
developer's personal account, the same crossing requires a login switch in the
middle. Both shapes are common, and today the developer has to work out on their
own which one they're in, with no help, before they can even follow the runbook.

Every step is mechanical and deterministic, which is exactly what makes the
current state frustrating: a machine could do all of it, yet a human does it by
hand from a runbook. And the individual phase must land on an exact shape or
nothing works. The credential lives at a specific vault path, under a key with a
mandatory `p-` prefix (the vault rejects keys that start with a digit), carrying
a TOML body with a required version and the two credential fields. Get any part
of that wrong and there's no error at store time. The failure surfaces later, as
a `niwa apply` that dies partway through on a credential it can't parse, far from
the typo that caused it.

So the sequence is deterministic enough that a machine should own it, fiddly
enough that humans get it wrong, and unforgiving in a way that hides the mistake
until much later. Today it's transcribed as hand-run shell into onboarding
documents, so the fragility is copied from workspace to workspace rather than
fixed once. Nobody should have to hold this sequence in their head, and nobody
should be able to finish it believing they succeeded when they've actually
produced a vault that will fail silently.

## User Outcome

A team admin or a developer runs a single command, `niwa onboard`, and is guided
through setup as a wizard. The wizard works out which of the two setups they're
doing and takes them down the matching path. It performs every mechanical step
itself and stops only at the points where a human genuinely has to act, the
interactive vault logins where the operator picks an organization or completes an
SSO round-trip. It walks them to each login, waits, and resumes on the other side.

How many of those login pauses there are is a property the wizard makes explicit
rather than one the operator has to discover. During the individual setup it
presents the vault topology as a named choice, whether the workspace vault and
the personal overlay share one account or live in separate ones, and inserts only
the login pauses the chosen shape actually requires: none when they share an
account, one switch when they're split. If the operator's topology changes later,
they re-run the wizard against the new shape and it re-mints and re-stores the
credential where it now belongs.

For the individual setup, the credential the wizard writes is correct by
construction. Because niwa assembles the vault path, the prefixed key, and the
TOML body itself rather than asking a human to type them, the exact-shape contract
can't come out malformed. Before it reports success, the wizard checks that the
credential actually resolves, so an operator learns onboarding worked from the
wizard rather than from a failed apply days later.

For the team setup, the privileged steps still run against the operator's own
authenticated vault session, so niwa never has to hold administrative power of its
own. When one of those steps isn't available on the operator's plan, the wizard
doesn't dead-end: it tells them exactly what to create in the dashboard, then
picks the sequence back up.

The operator never has to know the order of the steps, remember the exact shape,
or hand-execute the fiddly parts. They answer the wizard's prompts, complete the
logins it pauses for, and end with a working, verified vault setup, or a clear
statement of what still needs a human, instead of a silent failure waiting to
happen.

## User Journeys

### Team admin stands up a workspace's vault

A team admin is bringing a new machine-identity workspace online for their team.
Nobody can onboard until the shared identity, its authentication, its read access,
and the secret-path structure exist in the vault org. The admin runs the wizard in
its team setup. Driving their own authenticated vault session, it creates the
machine identity, attaches Universal Auth, grants read on the target environment,
and lays down the folder structure, prompting only where a choice is needed. The
admin finishes with a vault org ready for their teammates to onboard against,
without having assembled the sequence from a runbook or touched the vault's admin
API directly.

### Developer joins a team that already has a shared identity

A developer has cloned a workspace whose team-phase setup is already done, and
their `niwa apply` can't resolve the team's secrets because they have no
credential yet. They run the wizard in its individual setup. It sets up their
personal overlay and asks which vault topology they're in: whether the workspace
vault and their personal overlay share one account, or the workspace vault lives
in a separate org from their personal one. It mints a fresh client secret against
the org that hosts the workspace vault, and if the topology is the split shape it
pauses here and walks them through the one login switch to their personal account;
in the shared shape it just keeps going. Either way it stores the credential in
the overlay's vault at the exact contract shape and confirms it resolves. Their
next apply works, and they never learned the vault path, the key prefix, the body
format, or had to figure out on their own whether a login switch was even needed.

### Team admin hits a step their plan won't allow

A team admin running the team setup reaches a step the vault provider gates behind
a plan they're not on, such as creating a new org machine identity. Instead of
failing with a raw provider error, the wizard recognizes the gated step, tells the
admin precisely what to create in the provider's dashboard and with what settings,
and waits. Once they've done that one step by hand, the wizard continues with the
rest of the sequence automatically. The plan limit costs a single manual detour,
not the whole automated flow.

### Developer confirms onboarding actually landed

A developer has finished their individual setup and wants to know it's real before
an apply depends on it, or an apply already failed and they can't tell whether the
fault is in the vault entry or their local config. They re-run `niwa onboard`,
which recognizes their setup is already complete and goes straight to verification.
It confirms whether the credential resolves from the expected source and, when it
doesn't, points at what's wrong. The developer gets a straight answer up front
instead of discovering a broken setup through a later apply that dies partway
through. A team admin can run the same check to confirm the team-phase setup before
teammates depend on it.

## Scope Boundary

### In

- One command with two setups, a team setup run once by an admin and an individual
  setup run by each developer, delivered as an interactive wizard that works out
  which setup applies and branches accordingly.
- Automating every mechanical and exact-shape step of both setups: the team-phase
  identity, authentication, access grant, and folder creation; and the individual
  phase's mint, store, verify, and (when the topology calls for it) the login
  switch between them.
- Making the vault topology an explicit choice during the individual setup, between
  the same-login shape (workspace vault and personal overlay in one account) and
  the split-login shape (workspace vault in a dedicated org, overlay vault in the
  operator's personal account), and letting an operator switch shapes later by
  re-running the wizard, which re-mints and re-stores the credential where it now
  belongs.
- Producing the individual-phase credential in the exact credential-sync contract
  shape by construction (the vault path, the prefixed key, and the required TOML
  body), so it can't be stored malformed.
- Pausing only for the human logins the chosen topology actually requires, the
  interactive organization picks and any SSO round-trip, and resuming automatically
  afterward.
- Delegating the privileged team-phase steps to the operator's own authenticated
  `infisical` CLI session, the same delegation niwa already uses for vault reads.
- Degrading gracefully when a step is gated by the operator's provider plan:
  telling them exactly what to do in the dashboard, then continuing.
- Verifying that the result resolves before declaring success, folding in the live
  credential-contract validation from tsukumogami/niwa#199 as an internal
  post-condition rather than a separate pass.
- Keeping the command surface generic: no org-, workspace-, or project-specific
  identifiers baked into the command; those live in workspace config and the
  personal overlay.

### Out

- niwa holding administrative vault credentials of its own, or reimplementing the
  provider's admin REST API to create identities, grants, or folders directly. This
  is the hard line: the wizard drives the operator's own `infisical` session for
  every privileged step and never becomes a vault-administration tool with its own
  admin-token custody. Crossing it would take on org-wide admin blast radius and
  duplicate a maintained provider surface niwa has no reason to own.
- Non-Infisical vault backends for the admin and provisioning steps in v1. The
  credential-resolution layer is already provider-abstracted; this onboarding
  choreography targets the machine-identity flow niwa already supports and can
  generalize to other backends later.
- Preserving tsukumogami/niwa#194 (`provider-auth provision`) and
  tsukumogami/niwa#199 (`niwa vault check`) as standalone shipped commands. Their
  mechanics are folded into the wizard as internal building blocks; this feature
  supersedes them rather than shipping alongside them.

## Open Questions

These framing details are deferred to the downstream PRD and DESIGN; none blocks
the framing.

- How the wizard determines which setup an operator is in: automatic detection from
  the workspace and session state, an explicit flag, an early prompt, or a mix. The
  requirement is one command with two setups and interactive branching; the
  detection mechanism is a design choice.
- How the wizard pauses for a human login and resumes on the other side (a single
  guided run that blocks on the interactive login, a resumable multi-step flow, or
  another shape). The requirement is that the operator is walked to each login and
  the automation continues afterward; the resume mechanism is design territory.
- How the wizard names the vault topology and how it settles which shape applies:
  inferring it from the personal overlay and workspace config, asking the operator
  outright, or a mix. The requirement is that topology is an explicit, reason-able
  choice that drives how many login pauses appear; the naming and detection
  mechanism is a PRD and design detail.

## References

- `docs/guides/machine-identity-vault-sync.md` -- the machine-identity credential
  sync this onboarding sets up, including the credential-sync contract the
  individual phase must produce.
- `docs/guides/vault-integration.md` -- how niwa resolves `vault://` secrets by
  delegating to the operator's own `infisical` session, the delegation pattern the
  team-phase steps reuse.
- `docs/guides/init-bootstrap.md` -- the existing workspace-scaffolding path this
  onboarding flow complements.
- `docs/briefs/BRIEF-instance-dispatch.md` -- a prior brief framing a net-new,
  additive niwa command; precedent for this brief's shape.
