---
status: In Progress
problem: |
  niwa validates the team-vault credential-sync contract in exactly one
  place: a parse function deep in the apply read path. Validation is
  lazy, so every failure mode -- entry missing, body not TOML, a required
  field absent, an unrecognized version, the local file world-readable --
  surfaces the same way: an apply blows up partway through, on an error
  raised far from the thing that's wrong. No existing status flag covers
  it live: `--audit-auth` is offline (last-apply snapshot), `--check-vault`
  watches secret-material rotation, `--audit-secrets` classifies values.
  There's no way to ask "is my team vault set up correctly?" before an
  apply depends on the answer.
goals: |
  Give team leads and developers a read-only, on-demand check that
  confirms every configured (kind, project) credential entry will resolve
  before an apply runs -- and when one won't, names exactly which pair is
  wrong and why, from a fixed status vocabulary. The check reuses the
  same validators the apply path trusts, so a clean report is a real
  guarantee the next apply won't fail on credentials. It also validates
  the local provider-auth file layer (presence, mode, entry shape) and
  never prints or logs a secret value.
upstream: docs/briefs/BRIEF-vault-doctor.md
motivating_context: |
  The machine-identity vault-sync work shipped the credential contract
  (vault path /niwa/provider-auth/<kind>, key p-<project-uuid>, TOML body
  with version/client_id/client_secret, ~8 KiB body cap) but left
  validation embedded in the apply read path, with no standalone
  diagnostic surface.
---

# PRD: Vault doctor

## Status

In Progress

## Problem Statement

niwa resolves a workspace's vault secrets through a machine-identity
credential. The contract is precise: a TOML body at vault path
`/niwa/provider-auth/<kind>` under key `p-<project-uuid>`, carrying
`version = "1"` (empty version defaults to "1"), `client_id`, and
`client_secret`, with the body capped at roughly 8 KiB. A local file
layer mirrors it: `~/.config/niwa/provider-auth.toml`, mode 0600,
entries keyed by (kind, project).

niwa checks that contract in exactly one place -- a parse function deep
in the apply read path. Validation is lazy. Every failure mode surfaces
the same way: an apply dies partway through, on an error raised far
from the misconfigured entry. A team lead who just populated the vault
can't confirm it before teammates depend on it. A developer whose apply
failed has no tool that names which (kind, project) pair is broken and
how.

Nothing else covers this. `niwa status --audit-auth` is offline; it
reports what the last apply resolved, from state.json. `--check-vault`
watches secret-material rotation, not the auth contract.
`--audit-secrets` classifies secret table values. None of them talks to
the vault to ask whether the credential entries a future apply will
need are actually there and well-formed.

## Goals

Credential misconfiguration stops being something a team discovers as a
failed apply and becomes something they catch and fix up front. A team
lead can trust the vault is apply-ready before teammates depend on it; a
developer whose apply failed gets the exact pair and cause instead of
guesswork; an onboarding agent can gate the first apply on a clean result.
The check earns that trust by speaking with the same authority as apply
itself -- a clean report is a real guarantee, not a second opinion -- and
it does so without ever exposing a credential value.

## User Stories

- As a team lead who just wrote credential entries into the vault, I
  want to validate every expected (kind, project) entry live before my
  teammates run their first apply, so that I can fix a truncated or
  malformed entry up front instead of fielding failed-apply reports.

- As a developer whose `niwa apply` died with a credential parse error,
  I want a check that names the exact pair at fault and the failure
  class (missing entry, malformed body, missing field, unsupported
  version), so that I know whether the problem is a stale vault entry
  or my local config without guesswork edits and re-runs.

- As an automated agent driving new-machine onboarding, I want a
  pre-flight check with machine-readable output and a meaningful exit
  code -- including local-file findings like a too-permissive mode on
  `~/.config/niwa/provider-auth.toml` -- so that I can remediate and
  re-check before the first apply, and the flow never hits a mid-apply
  credential failure.

- As anyone running the check in a shared terminal or CI log, I want
  the output to assert existence and shape only, so that no credential
  value ever lands in scrollback or logs.

## Requirements

### Functional

- **R1**: The command enumerates the expected (kind, project) pairs from
  the workspace config's vault providers, covering both the anonymous
  `[vault.provider]` form and named `[vault.providers.<name>]` entries.
  Testable: given a config with one anonymous and two named providers,
  the report lists exactly the pairs those three providers imply.

- **R2**: For each expected pair, the command fetches the credential
  body live from the vault (path `/niwa/provider-auth/<kind>`, key
  `p-<project-uuid>`) and validates it against the credential-sync
  contract. Testable: seeding the vault with a valid body yields OK for
  that pair; deleting the entry yields a failure for that pair only.

- **R3**: Each vault-side pair reports exactly one status from a fixed
  vocabulary: `OK`, `missing-entry`, `malformed-body`, `missing-field`,
  `unsupported-version`. Testable: constructing one fixture per failure
  class (absent key, non-TOML body, body without `client_secret`, body
  with `version = "2"`) produces the corresponding status and no other.

- **R4**: The command also checks the local file layer
  (`~/.config/niwa/provider-auth.toml`) when it is present: mode 0600 and
  per-entry shape keyed by (kind, project). The file layer reports its own
  status vocabulary: `bad-mode` (permissions other than 0600),
  `malformed-file` (present but not parseable / an entry missing a required
  field), and `absent` (no file at all -- an informational status, not a
  failure, since the file layer is optional when the vault layer resolves).
  Testable: chmod the file to 0644 flags `bad-mode`; a present file with a
  kind-less entry flags `malformed-file`; no file yields `absent`; a valid
  0600 file yields no file-layer finding.

- **R5**: For any given credential body, the doctor's verdict matches the
  apply path's accept/reject decision -- the two never disagree. Testable:
  any body the apply path rejects, the doctor reports as invalid with the
  matching failure class; any body the apply path accepts, the doctor
  reports OK (including edge cases like an empty version string defaulting
  to "1" and bodies near the ~8 KiB cap). (How the doctor achieves this --
  reusing the apply-path validators rather than reimplementing them -- is
  recorded in Decisions and Trade-offs.)

- **R6**: The command is strictly read-only: it performs no writes to
  the vault, the workspace config, the local provider-auth file, or
  niwa state. Testable: hashing state.json, the workspace config, and
  the provider-auth file before and after a run shows no change, and a
  vault access log shows only read operations.

- **R7**: The command never prints or logs a credential value, in any
  output mode, on any code path -- including error paths. It asserts
  existence and shape only. Testable: run the command against fixtures
  containing known sentinel secret values across all statuses and
  output modes; the sentinel never appears in stdout, stderr, or logs.

- **R8**: The command offers a `--json` output mode emitting one
  machine-readable record per checked pair (and per file-layer
  finding), each carrying the pair identity and its status. Testable:
  the output parses as JSON and a consumer can recover every (kind,
  project, status) triple without scraping the human table.

- **R9**: The default output mode is a human-readable table with one
  line per pair, showing the pair identity and its status, plus the
  file-layer findings. Testable: a run over three pairs produces three
  pair rows a reader can match to R3's vocabulary.

- **R10**: The exit code distinguishes outcomes: 0 when every pair and
  the file layer are valid; non-zero when any pair or file-layer check
  fails; and the "couldn't reach the vault at all" case is
  distinguishable from "reached the vault, found an invalid pair" (in
  the exit code, the output, or both -- exact encoding is the design's).
  Testable: an all-OK run exits 0; a run with one `missing-entry` exits
  non-zero; a run against an unreachable vault is distinguishable from
  the invalid-pair run by a scripted consumer.

### Non-functional

- **R11**: The command runs standalone, outside any apply, and does not
  require or trigger an apply. Testable: it succeeds in a workspace
  where no apply has ever run.

- **R12**: A failure on one pair doesn't stop the check: the command
  evaluates every expected pair and reports all findings in a single
  run. Testable: with two broken pairs in different failure classes,
  one run reports both.

## Acceptance Criteria

- [ ] Running the doctor in a workspace whose config declares an
  anonymous `[vault.provider]` and at least one named
  `[vault.providers.<name>]` produces a report covering every implied
  (kind, project) pair, with no pair missing and none invented.
- [ ] With all vault entries valid and the local file present at mode
  0600 with well-formed entries, the command reports every pair `OK`
  and exits 0.
- [ ] Deleting one pair's vault entry yields `missing-entry` for that
  pair, `OK` for the rest, and a non-zero exit code.
- [ ] Replacing one pair's body with non-TOML content yields
  `malformed-body` for that pair.
- [ ] Removing `client_secret` (or `client_id`) from one pair's body
  yields `missing-field` for that pair.
- [ ] Setting one pair's body to `version = "2"` yields
  `unsupported-version`; a body with an empty version string is
  accepted as version "1" and reports `OK`.
- [ ] Setting `~/.config/niwa/provider-auth.toml` to mode 0644 yields a
  `bad-mode` file-layer finding and a non-zero exit; restoring 0600
  clears it; with no file present the file layer reports `absent` without
  causing a non-zero exit on its own.
- [ ] The doctor runs to a full report in a workspace where `niwa apply`
  has never run, without requiring or triggering an apply.
- [ ] With two pairs broken in different failure classes, a single run
  reports both findings.
- [ ] `--json` output parses as valid JSON and contains one record per
  checked pair and per file-layer finding, each with the pair identity
  and status; the default mode prints a human-readable table.
- [ ] Across every status and output mode, no credential value (seeded
  as a known sentinel) appears anywhere in stdout, stderr, or logs.
- [ ] Byte-for-byte hashes of state.json, the workspace config, and the
  local provider-auth file are identical before and after a run, and
  the vault sees only read operations.
- [ ] A scripted consumer can distinguish three outcomes without
  parsing prose: all valid (exit 0), at least one invalid pair
  (non-zero), and vault unreachable (distinguishable from the
  invalid-pair case).
- [ ] For a set of credential-body fixtures spanning valid and invalid
  cases, the doctor's verdict matches the apply path's accept/reject
  behavior on every fixture.

## Out of Scope

- **Config authoring or scaffolding** (for example a `niwa vault init`
  that writes the `[vault.provider]`/`[env.secrets]` contract into the
  workspace config). That's a separate follow-on feature: niwa has no
  config-authoring commands today and treats team config as an
  immutable read-only snapshot.
- **Provider-side creation** -- niwa creating the shared machine
  identity, vault folders, or ACLs. Rejected, not deferred: niwa only
  reads from the vault; provisioning team-shared identity is delegated
  to the provider's own admin CLI or dashboard.
- **Fixing what it finds.** The doctor diagnoses; it doesn't rewrite
  vault entries, chmod files, or edit config. Remediation stays with
  the operator (or an agent acting on the report).
- **The exact command surface and internal architecture.** This PRD
  fixes the capability and its contract; the downstream DESIGN owns the
  surface and structure (see Decisions and Trade-offs).

## Decisions and Trade-offs

- **Command surface: new `niwa vault check` subcommand vs a `niwa
  status` flag.** Deferred to DESIGN. Alternatives: (a) `niwa vault
  check`, which introduces a `vault` command namespace -- none exists
  today -- and gives future vault tooling a natural home; (b) a flag
  like `niwa status --check-credentials` or `--audit-provider-auth`,
  consistent with the existing audit-flag family (`--audit-auth`,
  `--check-vault`, `--audit-secrets`) but stretching `status`, whose
  existing flags are offline or rotation-focused, to cover a live
  network check. Reasoning for deferring: the brief settles the
  capability and explicitly leaves the surface to the design; both
  options satisfy every requirement here, so the choice turns on CLI
  taxonomy questions the design is better placed to weigh.

- **Live-fetch mechanism: reuse the existing infisical read/export path
  vs a lighter existence probe.** Deferred to DESIGN as an
  implementation choice. Alternatives: reusing the full read path
  maximizes fidelity with what an apply actually does (R5 leans this
  way); a lighter probe could be cheaper but risks the doctor and apply
  seeing different things. The requirement is fixed (R5: doctor and
  apply can't disagree); how the fetch achieves it is the design's.

- **Exit-code granularity: single non-zero vs distinct codes per
  failure class or for unreachable-vault.** Partially decided,
  remainder deferred. Decided here: failures must be distinguishable --
  in particular "vault unreachable" from "pair invalid" (R10), because
  an automated consumer remediates them differently. Deferred to
  DESIGN: whether that distinction lives in the exit code, the JSON
  output, or both, and whether individual failure classes get their own
  codes. Reasoning: the observable contract belongs in the PRD; the
  exact numeric encoding is a design detail with CLI-convention
  trade-offs.

## Known Limitations

- A clean report guarantees the credential contract will resolve at
  check time; it can't guarantee an entry isn't rotated or deleted
  between the check and the next apply. The doctor is a point-in-time
  diagnostic, not a lock.
- The doctor validates the machine-identity credential contract and the
  local file layer. It doesn't validate that the secrets those
  credentials unlock are themselves present or well-formed -- that
  remains the territory of `--check-vault` and `--audit-secrets`.
