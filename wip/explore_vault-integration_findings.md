# Exploration Findings: vault-integration

## Core Question

What vault provider(s) should niwa integrate with first, and what
architecture supports layering a team-shared vault (from a team config repo)
with a per-project-scoped personal vault (from a personal config repo)?

## Round 1

### Key Insights

- **sops + age is the strongest first integration.** $0 cost, no hosted
  service, git-native, vendor-neutral, and the key property: it lets
  `tsukumogami/dot-niwa` go public because secrets inside the repo are
  encrypted. Team membership is expressed as "the set of age public keys
  listed in `.sops.yaml`"; key rotation on member-leave is manual but
  tractable at 1-5-dev scale. [Leads 1, 3]

- **Infisical is the natural hosted upgrade.** MIT-licensed core with a
  self-host option (Docker), generous free tier (5 identities, 3 projects,
  3 envs), CLI matches the shape niwa needs. Teams that outgrow sops (want
  audit logs, managed rotation, UI) can switch without rearchitecting.
  [Leads 1, 3]

- **`[content]` was 100% Claude-coupled; `[vault]` is NOT.** Vault refs
  appear in `[env.vars]`, `[claude.env.vars]`, `[repos.*.env.vars]`,
  `[instance.env.vars]`, `[files]`, and `[claude.settings]`. Cross-cutting.
  Therefore vault config belongs at **top level**, not under `[claude]`.
  [Lead 5]

- **The existing `GlobalOverride` per-workspace scoping solves the
  per-org PAT problem for free.** Lead 5 discovered that
  `GlobalConfigOverride.Workspaces` is already keyed by workspace name,
  and `ResolveGlobalOverride` already picks the right entry. Personal PAT
  scoping needs no new mechanism — it rides the existing `[workspaces.<name>]`
  block. The `<name>` key defaults to the workspace's source-org name
  (Option A), with an explicit `[workspace].vault_scope` escape hatch
  (Option B) for multi-source or borrowed-scope workspaces. [Lead 4]

- **Reference syntax is a URI in any existing string slot, NOT a new
  binding table.** Option 3 from Lead 5: every string slot can carry
  `vault://<provider>/<key>`. Resolution happens at materialize time via
  a single interpolation pass. No new per-field merge logic needed —
  `MergeOverrides`, `MergeGlobalOverride`, and `MergeInstanceOverrides`
  already do last-writer-wins per key on `Env.Vars`, `Files`, etc. A
  personal overlay that rewrites `GITHUB_TOKEN = "vault://..."` Just Works.
  [Lead 5]

- **No surveyed tool solves niwa's exact per-org-scoped-personal-overlay
  requirement out of the box, but three patterns compose cleanly:**
  mise's two-file implicit-precedence convention (for the layering),
  1Password's URI references (for the in-config references),
  Pulumi-ESC-style published resolution tables (for user-facing
  explainability). [Lead 2]

- **Current materialization pipeline has a pre-existing `0o644` bug.**
  `SettingsMaterializer` and `EnvMaterializer` both write plaintext files
  world-readable. Vault integration must fix this to `0o600` as a
  prerequisite (INV-FILE-MODE). Separate from vault, but flagged by Lead
  6's audit; the PRD treats it as in-scope. [Lead 6]

- **12 "never leaks" invariants form the security-requirement floor** —
  no argv secrets, redacting `secret.Value` type, no `os.Setenv`, file
  mode 0o600, `.local` infix, instance-root `.gitignore`, no CLAUDE.md
  interpolation, `niwa status` path+status only, etc. These map directly
  to PRD acceptance criteria. [Lead 6]

- **Drift vs rotation distinction via `ManagedFile.SourceFingerprint`.**
  Content-hash change with stable source fingerprint = user drift.
  Content-hash change paired with source-fingerprint change = upstream
  rotation. `niwa apply` re-resolves on every run so rotated secrets
  propagate. No daemon, no watchers. [Lead 6]

### Tensions

- **OSS-fit vs bootstrap ergonomics.** sops wins on OSS-fit but requires
  the user to manage an age key pair (generate, publish, rotate). Doppler
  has the smoothest bootstrap (`doppler login` → browser → done) but is
  closed-source SaaS. Leads 1, 2, and 3 all resolved this tension the same
  way: sops first because niwa is an OSS CLI positioning as vendor-neutral,
  Doppler as a future stretch integration if demand appears. Infisical
  splits the difference.

- **Single backend vs pluggable.** Committing to sops alone is simpler
  to implement but leaves Infisical as a "later refactor" moment where
  the abstraction is exposed late and risks being wrong. Decision
  (recorded in decisions file): pluggable interface from v1. The
  interface is small (Resolve(key) -> secret + metadata) and the cost is
  one extra indirection — well worth the optionality.

- **Scoping mechanism.** Option A (implicit by source org) is the cleanest
  for the 80% case but breaks on multi-source workspaces. Option E (full
  URI with embedded scope like `vault://personal/tsukumogami/pat`) is
  the most explicit but adds keystroke cost to every reference. Lead 4
  resolved this: Option A with an Option B escape hatch
  (`[workspace].vault_scope` string) covers every case with minimum
  surface. Option E stays in the Deferred pile if niwa ever needs more
  than two vault layers.

### Gaps

- **Deprecation of current plaintext `[env].vars` secrets.** Not a
  blocker — users keep working with plaintext during migration — but the
  PRD should specify the `niwa status --audit-secrets` subcommand that
  identifies plaintext values and recommends migration. Deferred from
  research to requirements.

- **Public-repo guardrail tuning.** If a config repo has a public GitHub
  remote and plaintext env values are still present, niwa should refuse
  apply. Lead 6 proposed this; PRD needs to define exact detection
  (remote URL inspection vs explicit `public: true` flag) and override.

- **Escape for literal `vault://` strings.** Edge case flagged by Lead 5.
  Probably `\vault://...` or `raw:vault://...`. PRD picks one before
  shipping.

### Decisions

See `wip/explore_vault-integration_decisions.md` for the full list:
- Pluggable backend; sops+age first, Infisical second.
- Option A scoping + Option B escape hatch (`vault_scope`).
- Option 3 schema (`[vault.providers.*]` + `vault://provider/key` URIs).
- Personal-wins conflict, with `team_only` opt-in.
- Fail hard by default; `--allow-missing-secrets` + `?required=false` opt-outs.
- 12 security invariants become PRD acceptance criteria.
- `SourceFingerprint` on `ManagedFile` for rotation detection.
- No niwa-internal secret caching.
- Artifact type: PRD.

### User Focus

User set all context explicitly in their initial message: private `dot-niwa`
repo is the pain point, layering must work per-org, PRD is the target
artifact. Running in `--auto`, so convergence was self-directed per the
research-first protocol.

## Accumulated Understanding

Niwa's vault integration is **architecturally smaller than its strategic
weight suggests**:

1. A single new top-level `[vault.providers.*]` registry.
2. A `vault://<provider>/<key>` URI scheme accepted wherever env values
   can live today.
3. One new optional field `Vault *VaultRegistry` on `GlobalOverride`; no
   changes to the existing merge machinery.
4. A pluggable provider interface with two shipping backends in v1 (sops
   and Infisical) plus a "plaintext passthrough" dev-mode backend.
5. Twelve security invariants that partly fix pre-existing bugs (file
   mode 0o644 → 0o600) and partly establish new contracts (no argv
   secrets, redacting secret.Value type, no CLAUDE.md interpolation).
6. One resolution algorithm with a clear chain (personal-scoped →
   personal-default → team).
7. A `SourceFingerprint` addition to `ManagedFile` for distinguishing
   user drift from upstream rotation.

Bootstrap UX for a new developer joining `tsukumogami`:
1. `niwa init tsukumogami --from tsukumogami/dot-niwa` (existing).
2. `niwa config set global dangazineu/dot-niwa` (existing, v0.5.0).
3. Generate an age key pair, publish public key via PR to
   `tsukumogami/dot-niwa/.sops.yaml`, team lead re-encrypts secrets
   (sops backend), OR run `infisical login` (Infisical backend).
4. `niwa apply` — secrets resolve, materialize at 0o600, instance
   ready.

Estimated implementation effort: **one to two design-doc-sized PRs**.
The first wires the schema + resolver + sops backend; the second adds
Infisical. Both are tractable in the niwa v0.7 → v0.8 window.

Open questions the PRD must resolve:
- Escape syntax for literal `vault://` strings.
- Exact `team_only` enforcement mechanism (config-only vs runtime check).
- Public-repo guardrail detection heuristic.
- Per-reference `?required=false` vs global `--allow-missing-secrets`
  interaction.
- Whether `niwa vault import` (plaintext → vault migration) ships in v1
  or is deferred.

## Decision: Crystallize
