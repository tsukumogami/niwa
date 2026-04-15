# Exploration Decisions: vault-integration

Running in `--auto` mode per user instruction. Decisions here follow the
research-first protocol: recommendation grounded in lead output, picked,
documented, move on.

## Round 1

- **Backend strategy: pluggable interface, ship sops+age first, Infisical
  second.** Leads 1 and 2 converge on sops+age as the strongest first
  integration (zero cost, zero vendor lock-in, makes `dot-niwa` publishable,
  git-native) with Infisical as the natural hosted upgrade path (MIT core,
  5-identity free tier, self-hostable). A pluggable interface from v1 means
  Infisical is an equal peer, not "the upgrade." Rationale: if we hard-
  code sops, the Infisical integration becomes a later refactor and the
  abstraction is exposed late. Better to design the boundary now.

- **Scoping: Option A implicit by source org + Option B `vault_scope`
  escape hatch.** Lead 4's recommendation. Personal config declares
  `[workspaces.<org-or-scope>]` tables; resolution key defaults to
  `ws.Sources[0].Org`; falls back to explicit `[workspace].vault_scope`
  when the team config sets it. Multi-source workspaces (len(Sources) > 1)
  require explicit `vault_scope` or error. Zero-source workspaces
  (explicit-repos-only) have no automatic scope and must set
  `vault_scope` explicitly if they want a personal overlay.

- **Schema: Option 3 — top-level `[vault.providers.*]` registry +
  `vault://<provider>/<key>` URI refs in existing string slots.** Lead 5's
  recommendation. Provider config is at the top level; every existing
  string slot (`env.vars`, `claude.env.vars`, `repos.*.env.vars`,
  `instance.env.vars`, `files` source keys, `claude.settings` values) can
  carry a `vault://` URI. `GlobalOverride` grows one optional
  `Vault *VaultRegistry` field; personal PAT scoping rides existing
  `[workspaces.<name>]` layering for free.

- **Conflict resolution: personal wins, with team `team_only = [...]`
  opt-in for keys the team must control.** Lead 4's recommendation.
  Mirrors the existing `MergeGlobalOverride` precedence rule (global
  wins per key for `Env.Vars`). `team_only` is an explicit list in
  `[vault]` on the team config; users cannot shadow listed keys via their
  personal overlay without first editing the team config.

- **Graceful degradation: fail hard by default, with
  `--allow-missing-secrets` flag + per-reference `?required=false` query
  parameter.** Lead 4's recommendation. `niwa apply` refuses to proceed
  when a vault reference can't be resolved. Users opt into leniency
  explicitly, either globally (the flag) or per-reference (the query
  param on the URI).

- **Runtime secret handling: 12 "never leaks" invariants from Lead 6 are
  the PRD's security requirements.** Key items: no secrets on argv, all
  resolved values wrapped in a `secret.Value` opaque type with redacting
  formatter, no `os.Setenv`, materialized secret-bearing files written at
  `0o600` (fixes a pre-existing `0o644` bug), `.local` infix + instance-
  root `.gitignore`, no secret interpolation in CLAUDE.md, `niwa status`
  stays path+status only.

- **Drift vs rotation distinction: `ManagedFile.SourceFingerprint`.** Lead
  6's recommendation. A content-hash change with stable source fingerprint
  is user drift (reports `drifted`); a content-hash change paired with a
  source-fingerprint change is upstream rotation (reports `stale`). `niwa
  apply` re-resolves on every run so rotated secrets propagate
  automatically.

- **No niwa-internal secret caching.** Lead 6's recommendation. Delegate
  caching to the vault CLI's own session machinery (1Password session
  tokens, Infisical universal-auth tokens, sops's age-key-from-disk). If
  perf becomes a problem, add a process-lifetime in-memory cache only.
  Never a disk cache.

- **Artifact type: PRD.** User explicitly requested a PRD. Crystallize
  scores also support it (requirements are emerging — vendor choice,
  scoping semantics, security invariants — and stakeholder alignment is
  needed across team users + personal users + OSS community).
