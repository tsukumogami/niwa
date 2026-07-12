# /brief Discovery: niwa-onboard

## Problem Candidate

niwa vault onboarding is a long, cross-context, multi-login choreography where
every step is mechanical but the whole thing must land on an exact shape or it
silently fails at a later `niwa apply`. The sequence spans two phases with an
org-context switch in the middle: a team phase (create the machine identity,
attach Universal Auth, grant the ACL, create the folder structure) run once by
an admin, and an individual phase (set up the personal overlay, mint a fresh
client secret in the TEAM org, switch login to the PERSONAL org, store the
credential at the exact credential-sync contract shape, verify it resolves)
run by every developer. Today it lives as hand-run shell in onboarding
runbooks: deterministic enough that a machine should do it, fiddly enough that
humans get it wrong, and failing silently and far from the mistake. Nobody
should have to hold this sequence in their head.

## Outcome Candidate

A developer or team admin runs one command — `niwa onboard` — and is walked
through onboarding as a wizard: it detects (or asks) which mode they're in,
automates every mechanical and exact-shape step, and pauses only for the
irreducible human logins (the interactive `infisical login` org picks and any
SSO round-trip). The credential-sync contract shape (path
`/niwa/provider-auth/<kind>`, key `p-<project-uuid>`, TOML body with
`version = "1"`, `client_id`, `client_secret`) is produced by construction so
it cannot come out malformed, and the wizard verifies the result resolves
before declaring success. The operator never has to know, sequence, or
hand-execute the steps, and never produces a silently-broken vault.

## Grounding Anchor

conversation only (dispatch brief at the workspace root:
`.niwa/dispatch-briefs/niwa-onboard.md` — settled problem, boundaries, and
scope; supersedes closed PRs tsukumogami/niwa#194 and tsukumogami/niwa#199,
whose code-verified DESIGN/PRD docs are mined into `wip/research/mined_*.md`)

## Journey Sketch

- Team admin (once per workspace/vault org): runs the wizard in team mode; it
  drives their own `infisical` CLI session to create the machine identity,
  attach Universal Auth, grant the environment ACL, and create the secret-path
  folder structure. Plan-gated steps (e.g. creating a new org machine identity
  on some Infisical plans) degrade gracefully: the wizard says "do this one
  step in the dashboard, here's exactly what to create," then continues.
- Individual developer (once per person): runs the wizard in individual mode;
  it sets up the personal overlay, mints a fresh client secret on the team
  identity while logged into the TEAM org, walks them through the org switch
  to the PERSONAL org, stores the credential at the exact contract shape, and
  verifies resolution (`niwa status --audit-auth` shows the team row resolving
  from the personal-overlay vault source).
- Either operator, on failure or doubt: the wizard's verify step (folding in
  the vault-doctor logic from #199) tells them whether onboarding actually
  landed, instead of a later `niwa apply` failing silently.

## Author Amendment (2026-07-12, at the BRIEF approval gate)

The two-login / org-switch flow is NOT intrinsic to onboarding — it's a
property of one vault topology. Two shapes are both common and must be
first-class:

1. **Same-login shape**: the workspace vault project and the personal overlay
   project live in the same Infisical account/org (example: the author's
   commuter workspace). No login switch during individual setup.
2. **Split-login shape**: the workspace vault lives in a dedicated org
   (example: a tsukumogami org with a project for the workspace vault) while
   the personal overlay vault lives in the operator's personal account. The
   individual setup needs the login switch.

The invariant: mint the client secret against the org hosting the workspace
vault; store the credential into the vault backing the personal overlay. The
wizard must make the topology an explicit, easy-to-reason-about choice at
onboarding time, insert only the login pauses the chosen shape requires, and
make switching shapes later easy (re-running the wizard against the new
topology). The BRIEF must not frame the org switch as universal.

## Open Questions for Drafting

- Wizard shape specifics (mode auto-detection vs explicit `--team` flag,
  resume-after-login mechanics) are design-altitude decisions; the BRIEF
  should state the requirement (one command, two modes, interactive branches)
  without settling the mechanism.
- Boundary is settled and must be stated: niwa never holds admin tokens or
  reimplements the provider's admin REST API; privileged team-phase steps are
  delegated to the operator's own `infisical` CLI session. Non-Infisical
  backends for admin/provisioning steps are out for v1.
