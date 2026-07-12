---
schema: brief/v1
status: Accepted
problem: |
  niwa validates the team-vault credential-sync contract only lazily,
  mid-apply, deep in the read path. The only way to learn a vault
  credential entry is missing or malformed is to run an apply and watch
  it fail. There's no way to ask "is my team vault set up correctly?"
outcome: |
  A team lead or developer can confirm, before an apply runs, that every
  configured credential entry will resolve -- and when one won't, see which
  pair is wrong and why -- from one read-only check that never prints a
  secret value, instead of diagnosing it as a failed apply.
motivating_context: |
  The machine-identity vault-sync work shipped the credential contract
  (vault path /niwa/provider-auth/<kind>, key p-<project-uuid>, TOML body
  with version/client_id/client_secret) but left validation embedded in
  the apply read path, with no standalone diagnostic surface.
---

# Brief: Vault doctor

## Status

Accepted

Framing for a live diagnostic over the credential-sync contract
established by the machine-identity vault-sync PRD. The capability is
settled here; the exact command surface is a downstream design decision.

## Problem Statement

niwa resolves a workspace's vault secrets through a machine-identity
credential. The contract is precise: a TOML body at vault path
`/niwa/provider-auth/<kind>` under key `p-<project-uuid>`, carrying
`version = "1"`, `client_id`, and `client_secret`. A local file layer
mirrors it: `~/.config/niwa/provider-auth.toml`, mode 0600, entries
keyed by kind and project.

niwa checks that contract in exactly one place: a parse function deep in
the apply read path. Validation is lazy, so every failure mode -- entry
missing, body not TOML, a required field absent, an unrecognized version,
the local file world-readable -- surfaces the same way: an apply blows up
partway through, on an error raised far from the thing that's wrong.

Nothing else covers it. `niwa status --audit-auth` is offline; it
reports what the last apply resolved, from state.json. `--check-vault`
watches secret-material rotation, not the auth contract. `--audit-secrets`
classifies secret table values. None of them talks to the vault to ask
whether the credential entries a future apply will need are actually
there and well-formed.

So a team lead who just populated the vault has no way to confirm it
before teammates depend on it, and a developer whose apply failed has no
tool that names which pair is broken and how.

## User Outcome

A team lead or developer knows, before an apply runs, whether every
configured credential entry will resolve -- and when one won't, they see
exactly which pair is wrong and why, instead of discovering it as a failed
apply. The uncertainty of "did I set the vault up right?" is answered on
demand, up front, by a read-only check that reuses the same validators the
apply path trusts, so a clean report is a real guarantee the next apply
won't fail on credentials. The answer never exposes a secret value -- it
speaks to presence and shape, not contents.

## User Journeys

### Team lead confirms the vault before the team onboards

A team lead has just created the machine identity and written credential
entries into the vault for each provider kind the workspace uses. Before
telling teammates to run their first apply, she runs the doctor. It
reads each expected (kind, project) entry live from the vault and
reports one line per pair. One entry shows `missing field: client_secret`
-- she'd pasted a truncated body. She fixes the entry, reruns, sees all
OK, and onboards the team knowing their applies will resolve.

### Developer turns an apply failure into a named cause

A developer's `niwa apply` dies with a credential parse error somewhere
in secret resolution. Instead of re-running the apply with guesswork
edits, he runs the doctor. It reports the exact pair at fault --
`unsupported version` on the entry for one kind -- while every other
pair is OK. He now knows it's a stale entry written by an older setup
doc, not his local config, and asks the team lead to rewrite that one
entry.

### Agent runs a pre-flight before driving onboarding

An automated agent is walking a new machine through workspace setup. As
a pre-flight, it runs the doctor before the first apply. The vault-side
pairs come back OK, but the local layer reports `bad mode` on
`~/.config/niwa/provider-auth.toml` -- 0644 instead of 0600. The agent
tightens the mode, reruns to confirm all clear, and only then proceeds
to apply. The onboarding flow never hits a mid-apply credential failure.

## Scope Boundary

### In

- Live, read-only validation of the credential-sync contract against
  the vault, across every (kind, project) pair the workspace config
  expects, reusing niwa's existing parse/validation code so the doctor
  and the apply path can't disagree.
- Per-pair status report: OK, missing entry, malformed body, missing
  field, unsupported version.
- The local file layer: presence, mode 0600, and per-entry shape checks
  on `~/.config/niwa/provider-auth.toml`, reported the same way (adding
  bad-mode as a file-layer status).
- Secret hygiene throughout: the command asserts existence and shape,
  never prints or logs a credential value.

### Out

- A config-authoring or scaffold command (for example `niwa vault init`)
  that would write the `[vault.provider]`/`[env.secrets]` contract into
  the workspace config. That's a separate follow-on feature: niwa has no
  config-authoring commands today and treats team config as an immutable
  read-only snapshot.
- Provider-side creation -- niwa creating the shared machine identity,
  vault folders, or ACLs. Rejected, not deferred: niwa's stance is that
  it only reads from the vault; provisioning team-shared identity is
  delegated to the provider's own admin CLI or dashboard.
- Deciding the exact command surface (a `niwa vault check` subcommand
  versus a `niwa status --check-*` flag). This brief frames the
  capability; the downstream design owns the surface.

## References

- `docs/prds/PRD-machine-identity-vault-sync.md` -- establishes the
  credential-sync contract this feature validates.
- `docs/prds/PRD-vault-integration.md` -- the vault provider abstraction
  the doctor reads through.
