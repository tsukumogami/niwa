# Lead: Layering and scoping semantics

## Findings

### Current niwa machinery

The relevant types and functions already in the tree (before any vault work):

- `internal/config/config.go:63-74` — `WorkspaceConfig` top-level TOML types.
  Sources live at `cfg.Sources []SourceConfig` (line 65), where each
  `SourceConfig` has `Org`, `Repos`, `MaxRepos` (lines 101-105). **A workspace
  can list multiple sources** — the schema is a TOML array of tables, so
  "one workspace, one org" is a convention not a constraint.
- `internal/config/config.go:311-319` — `GlobalOverride` captures only the
  fields a user-owned config may set: `Claude *ClaudeOverride`, `Env EnvConfig`,
  `Files map[string]string`. Explicitly omits `URL`, `Branch`, `Group`, `Scope`,
  `SetupDir`, and `Claude.Enabled`.
- `internal/config/config.go:324-327` — `GlobalConfigOverride` wraps a flat
  `[global]` table and a `[workspaces.<name>]` table map. Per-workspace
  scoping already exists — but keyed by **workspace name**, not by source org.
- `internal/workspace/override.go:219-310` — `ResolveGlobalOverride` collapses
  `[global]` + `[workspaces.<workspaceName>]` with workspace-specific-wins
  semantics per field.
- `internal/workspace/override.go:327-436` — `MergeGlobalOverride` applies the
  resolved override on top of `WorkspaceConfig`. Merge order (line 92 of the
  global-config design) is **workspace defaults → global override →
  per-repo override**. Key semantics:
  - Env files: global appended after workspace (line 412).
  - Env vars: global wins per key (line 416-420).
  - Files: global wins per key; empty value suppresses workspace mapping
    (line 424-432). This "empty-string-to-suppress" idiom is the existing
    override vocabulary for saying "remove what the team declared."
- `internal/workspace/override.go:28-123` — `MergeOverrides` applies per-repo
  overrides last. Env vars: repo wins per key (line 103).
- `internal/workspace/override.go:311-436` — the three-layer chain is computed
  once per apply run; `intermediate := MergeGlobalOverride(ws, ...)` then
  `effective := MergeOverrides(intermediate, repoName)`.

The current machinery already has the right shape for vault layering: a team
config and a user-owned overlay, computed in a fixed order at apply time, with
per-workspace keys on the overlay side. **What it lacks** is any notion of
"per-source-org scoping" — the overlay keys are workspace names, not orgs. So
a user with two workspaces both named `platform` against different orgs would
collide on the workspace-name key.

### Options evaluated

Scores use 1 (poor) to 5 (excellent).

#### Option A — Implicit scoping by workspace source org

User's personal config declares `[vaults.tsukumogami]` and `[vaults.codespar]`
tables. niwa inspects `cfg.Sources[0].Org` and looks up `vaults.<org>`
automatically. No new field on the team config.

- **Correctness: 3.** Works for the common case (one source, one org), but
  breaks down when `Sources` has length >1 or length 0 (explicit-repos-only
  workspace). The code in `internal/config/config.go:65` allows both.
- **Ergonomics: 5.** User writes `[vaults.tsukumogami] token = "op://..."`
  and it just works when they clone a `tsukumogami` workspace on any machine.
- **Complexity: 2.** Small: one lookup keyed by `ws.Sources[0].Org`.
- **Config-repo-portability: 5.** The team config needs zero new fields; only
  the personal config changes. Team config can be copied or forked without
  carrying vault-specific plumbing.

#### Option B — Explicit `vault_scope` string in workspace.toml

Team config declares e.g. `[workspace] vault_scope = "tsukumogami"`. Personal
config's `[vaults.tsukumogami]` is matched by that string. Decouples resolution
from `[[sources]].org`.

- **Correctness: 5.** Unambiguous; handles multi-source and source-less
  workspaces; handles cases where a workspace wants to borrow another project's
  scope (e.g., a fork workspace under `dangazineu` still wants the
  `tsukumogami` PAT).
- **Ergonomics: 3.** Team has to remember to set `vault_scope`. Forgetting it
  silently disables secret resolution.
- **Complexity: 3.** New field on `WorkspaceMeta`, new fallback rules if it's
  absent.
- **Config-repo-portability: 4.** Team configs are portable but carry an
  explicit scope string. A forked team config must be edited when the
  fork lives under a different scope.

#### Option C — Explicit per-provider default + namespaced keys

Personal config declares one default vault provider (`[vault] url = "op://..."`).
Team config just says `env.MY_PAT = "vault:tsukumogami/github-pat"`. Keys are
namespaced inside the vault.

- **Correctness: 4.** Works if every secret reference self-namespaces. Fails
  when a team config references a secret without knowing the user's namespace
  convention.
- **Ergonomics: 2.** User must namespace every secret key in their vault, and
  the team must agree on a namespace convention. Any convention drift breaks
  resolution silently.
- **Complexity: 3.** Single provider, but complex key-rewriting rules inside
  `env.Vars` values.
- **Config-repo-portability: 3.** Team config embeds assumptions about the
  user's vault layout, which isn't portable across users.

#### Option D — Layered lookup with `kind` tag and fallback chain

Each secret key is tagged `kind = "team"` or `kind = "personal"`. Resolution
tries personal vault, then team vault, then env, then fails. Conflicts are
resolved by `kind`.

- **Correctness: 4.** Handles conflicts explicitly. But the `kind` tag doubles
  the schema surface and doesn't by itself solve per-project scoping of
  personal secrets — you still need to know which personal vault entry to pick.
- **Ergonomics: 2.** Every secret reference needs a `kind`. High ceremony.
- **Complexity: 5 (meaning: highest).** Adds a tag system on top of an
  existing namespacing problem; still needs Option A or B for the per-project
  question.
- **Config-repo-portability: 4.** Team config is explicit about what's
  personal vs team, which travels well, but the tag overhead is pure tax.

#### Option E — URI-reference model

Each secret is referenced by a full URI carrying its own locator:
`vault://team/gh-pat`, `vault://personal/tsukumogami/gh-pat`. niwa parses the
prefix and dispatches to the appropriate resolver.

- **Correctness: 5.** Fully explicit. No ambiguity ever. Handles multi-source,
  handles borrowing scopes across projects (`vault://personal/codespar/pat`
  from inside a `tsukumogami` workspace), handles multiple vaults of same
  kind (`vault://personal-work/...` vs `vault://personal-home/...`).
- **Ergonomics: 3.** Users write longer strings, but they're readable.
  Comparable to the `@import ./path` pattern niwa already uses for CLAUDE.md.
- **Complexity: 4.** URI parser, dispatcher, validator. But the mental model
  is simple and self-documenting.
- **Config-repo-portability: 5.** Team config's `vault://team/...` means the
  same thing under any user's setup. Personal config's `vault://personal/X/...`
  means the same thing regardless of which workspace references it.

### Sub-question answers

**1. Same secret key in team and personal config — who wins?**

- **A:** Personal wins for that org's `[vaults.<org>]` table; team's
  `[vault]` is the fallback. User can't override team secrets without a
  companion mechanism.
- **B:** Same as A but keyed by `vault_scope`.
- **C:** Team's reference points at one vault, so there's no collision —
  unless the user also points at that same vault, in which case whoever
  rewrites the URL last wins (no clear rule).
- **D:** The `kind` tag makes intent explicit; personal overrides team only
  when the reference says so.
- **E:** No collision possible — each reference resolves to exactly one
  locator. If the user wants to shadow a team secret, they write a `files`
  or `env.vars` override pointing at `vault://personal/...` instead.

**2. Override story for debugging (replace a team secret locally)?**

- **A:** User adds `[vaults.<org>] token = "op://personal-debug/..."` in
  personal config. Personal wins, so team's value is shadowed. Works.
- **B:** Same as A but user also needs `vault_scope` to match.
- **C:** User points their local `[env.vars]` at a different vault key;
  works but requires knowing the namespacing convention.
- **D:** User adds a `kind = "personal"` override; the fallback chain picks
  it up first.
- **E:** User adds `env.vars.MY_PAT = "vault://personal/debug/..."` in their
  personal overlay; trivially overrides because personal layer wins in the
  existing merge order.

**3. Interaction with multi-source workspaces?**

The `[[sources]]` array allows multiple orgs. This is where the options
diverge most sharply:

- **A:** Ambiguous. Must pick one — first source, or require single source.
  Breaks for legitimate multi-org workspaces (e.g., a workspace that spans
  `tsukumogami` + a downstream consumer's org).
- **B:** Handles it — one `vault_scope` per workspace, chosen by the team.
- **C:** Orthogonal — keys are namespaced globally, not per-source.
- **D:** Doesn't address scoping directly; falls back to A or B.
- **E:** Handles it — each secret reference carries its own scope.

For the common case (one source per workspace), A is fine. For the edge case
(two sources), only B and E give a clean answer.

**4. Schema location — under `[claude]` or top-level?**

Vault config is NOT Claude-specific. The just-shipped `[claude.content]`
consolidation (see `DESIGN-claude-key-consolidation.md` lines 37-53) put
`content` under `[claude]` precisely because every consumer of `content`
writes to `CLAUDE.md`/`CLAUDE.local.md` — it's 100% Claude-coupled.

Vault resolution feeds `env.vars`, `claude.env.vars`, and potentially `files`.
The first and third are top-level; only the second is under `[claude]`.
Therefore:

- Vault declarations belong at **top level**: `[vault]` (team config) and
  `[vaults.<scope>]` or `[vault]` (personal config, depending on option).
- Vault **references** appear inline in existing fields: `env.vars`,
  `claude.env.vars`, `files` destinations.

This matches the precedent set by `[env]` itself, which sits at top level
and is referenced from `[claude.env.promote]`.

**5. Interaction with `niwa init` re-clone on a second machine?**

`niwa init <name> --from tsukumogami/dot-niwa` clones the team config repo
into `.niwa/`. On a second machine the user re-runs the same command.

- The team config is reproducible across machines (it's a git repo).
- The personal config is reproducible via `niwa config set global
  <personal-dot-niwa-repo>` on the new machine — this exists today
  (`DESIGN-global-config.md` lines 119-150).
- Vault credentials themselves (e.g., 1Password session, GitHub token for
  the vault provider's own auth) are **not** reproducible via niwa and
  must come from the provider's own CLI/auth mechanism.

Option A, B, and E all work identically across machines because the scope
key (org name, `vault_scope` string, or URI) travels in the config files.
Option C and D work only if the user re-namespaces consistently.

**6. Graceful degradation on vault unreachable / auth expired?**

This is a requirements question, not option-dependent. Three stances:

- **Fail hard.** Safest for correctness (a Claude instance without its PAT
  may silently skip auth-required operations and produce garbage).
- **Fall back to empty.** Matches how niwa treats missing `env.files`
  today (non-fatal). Dangerous for secrets because downstream tools may
  hit rate-limited unauthenticated endpoints.
- **Prompt interactively.** niwa is a CLI run before an agent session, so
  prompting is acceptable; but `niwa apply` is also called from
  `niwa init` non-interactively, so a prompt requires a `--no-prompt`
  fallback that fails hard.

Recommended: **fail hard by default, with a `--allow-missing-secrets` flag
for exceptional runs and a `vault.required = false` per-secret tag for
known-optional secrets.**

## Recommendation

**Recommend Option A (implicit scoping by workspace source org), with an
Option B escape hatch (`vault_scope` field) for multi-source or borrowed-scope
workspaces.** This is the minimum-surface path that hits the 80% case
(single-source workspaces matching the dot-niwa convention) while leaving
room for the 20% edge cases without rearchitecting.

Rationale against the alternatives:

- **C** silently couples team configs to user conventions — dealbreaker.
- **D** adds ceremony without solving the scoping question it claims to.
- **E** is the purest model and would be my second choice, but the URI
  overhead for every secret reference is steeper than the problem requires
  today. E is worth revisiting if niwa ever needs three+ vault scopes
  (personal-work, personal-home, team, shared-contractor, ...).
- **B** alone is correct but requires every team config to opt into a new
  field; folding it into A as an escape hatch is strictly better.

### Resolution algorithm (pseudocode)

Inputs:
- `ws *WorkspaceConfig` — the team workspace config (already parsed).
- `global *GlobalConfigOverride` — the personal overlay (already parsed).
- `secretRef string` — a reference value appearing in `env.vars`,
  `claude.env.vars`, or `files`. Shape: `"vault:<key>"` (no scheme means not
  a vault reference; pass through unchanged).

Computed once per `niwa apply` run:

```
1.  scope := ResolveVaultScope(ws)
    1a. if ws.Workspace.VaultScope != "" return ws.Workspace.VaultScope
    1b. if len(ws.Sources) == 1 return ws.Sources[0].Org
    1c. if len(ws.Sources) == 0 return ""   // no-source workspace
    1d. if len(ws.Sources) > 1 return error
        "workspace has multiple sources; set workspace.vault_scope explicitly"

2.  teamVault := ws.Vault                  // may be zero-value
    personalScoped := global.Vaults[scope] // may be zero-value (if scope != "")
    personalDefault := global.Vault        // may be zero-value

3.  chain := []VaultRef{}
    if !zero(personalScoped) append chain personalScoped
    if !zero(personalDefault) append chain personalDefault
    if !zero(teamVault)       append chain teamVault
```

Per-reference resolution (called once per `secretRef` during materialization):

```
4.  if !strings.HasPrefix(secretRef, "vault:")
        return secretRef, nil            // literal, pass through

5.  key := strings.TrimPrefix(secretRef, "vault:")

6.  for vault in chain:
       value, err := vault.Lookup(key)
       if err == ErrKeyNotFound: continue
       if err != nil:
          return "", wrap(err, "vault lookup failed for key %q", key)
       return value, nil

7.  if allowMissing: return "", nil      // --allow-missing-secrets flag
    return "", fmt.Errorf("secret %q not found in any configured vault", key)
```

`personalScoped` before `personalDefault` before `teamVault` encodes the
policy that **the most-specific source wins**: a per-project personal override
beats a personal default beats a team value. This mirrors niwa's existing
"more-specific layer wins" merge order (repo > global > workspace).

### Conflict-resolution policy

When the same key `K` exists in both team and personal vaults:

- **Default:** personal wins. Rationale: personal overlay is more specific
  to the user, and the v0.5.0 `MergeGlobalOverride` already establishes that
  the personal layer wins for `env.vars` and `files`. Vault resolution just
  extends the same principle.
- **Exception:** keys the team explicitly marks as `team_only = ["KEY1",
  "KEY2"]` in `[vault]` — these refuse personal shadowing. Use case: a team
  telemetry endpoint secret that must not be overridden per-user. This list
  is small and explicit; default is still personal-wins.

The user can always shadow a team vault reference by redeclaring the env
var in their personal overlay with a literal value or a different
`vault:<key>` reference — this is just the existing `env.vars` override
mechanism (which already has personal-wins semantics) and requires no new
machinery.

### Override story

A user debugging in local needs to replace `GITHUB_PAT` (team vault) with
a personal debug token. Three escalating options:

1. **Shadow via env var:** personal overlay adds `[env.vars] GITHUB_PAT =
   "ghp_abc..."` — literal value, no vault. Simple and always works.
2. **Shadow via personal vault:** personal overlay adds `[env.vars]
   GITHUB_PAT = "vault:debug-gh-pat"` and `[vaults.tsukumogami] debug-gh-pat
   = "op://Personal/debug-gh-pat/credential"`. Keeps the secret out of
   plaintext.
3. **Shadow the team key directly:** if the team uses `vault:github-pat`,
   the personal overlay adds `[vaults.tsukumogami] github-pat = "op://..."`
   — personal scope wins during lookup.

None of these requires editing the team config repo. All three are
reproducible on a second machine via `niwa config set global`.

### Edge cases the PRD must address

1. **Zero-source workspace.** A workspace with no `[[sources]]` (e.g.,
   explicit-repos-only). Implicit scoping yields empty string; the PRD must
   say whether this (a) disables personal-scoped lookup entirely, falling
   back to `[vault]` (personal default) + team, (b) fails fast, or
   (c) requires an explicit `vault_scope`.

2. **Multi-source workspace.** `[[sources]]` with two orgs. Algorithm step 1d
   errors; the PRD must specify that `workspace.vault_scope` is required in
   this case, and document the error message clearly. A workspace that
   legitimately needs secrets from both orgs (a federation case) needs a
   PRD-level answer — perhaps `vault_scope = ["a", "b"]` with a defined
   first-match order, or a recommendation to split into two workspaces.

3. **Workspace name collision across orgs.** User has a `platform` workspace
   under `tsukumogami` and another `platform` workspace under `codespar`.
   The existing `GlobalConfigOverride.Workspaces` map is keyed by workspace
   name, so a single personal overlay serves both and they collide on the
   key. The PRD must decide: (a) rename one, (b) key by
   `<org>/<workspace-name>`, or (c) accept that the `[workspaces.<name>]`
   overlay section is org-agnostic (collision is user's problem) while
   `[vaults.<scope>]` is org-scoped (no collision). I recommend (c).

4. **Source org mismatch between team config and workspace clone location.**
   User clones `tsukumogami/dot-niwa` but the team config's `[[sources]]`
   lists a different org (e.g., team config is in `tsukumogami/dot-niwa` but
   uses `[[sources]] org = "my-company"` internally). Implicit scope is
   `"my-company"`, not `"tsukumogami"`. This is actually the correct
   behavior — the vault scope should match where code lives, not where the
   dot-niwa repo lives — but the PRD needs to state it explicitly because
   users will expect otherwise.

5. **Secret reference in a field that's merged, not replaced.** `env.files`
   uses append semantics in both `MergeGlobalOverride` and `MergeOverrides`.
   If a `vault:` reference appears inside an env file's *contents* (not as
   an `env.vars` value), niwa's current design doesn't resolve it — env
   files are read at materialization time as plaintext. PRD must decide
   whether vault refs are supported only in `env.vars` (recommended for v1)
   or also as `${vault:KEY}` interpolations inside env file contents.

6. **Stale vault cache on fast switches.** If a user switches between
   `tsukumogami` and `codespar` workspaces rapidly, the vault provider may
   cache an auth session scoped to one. The PRD should state whether niwa
   invalidates any cache on workspace switch or relies on the provider.

## Surprises

- **`GlobalConfigOverride.Workspaces` is keyed by workspace name, not org.**
  This works for the "one user, many workspaces" case but not for the
  per-source-org scoping the exploration brief assumes. The vault feature
  introduces a second, orthogonal scoping dimension (scope = org) that the
  existing key (scope = workspace name) doesn't cover. The PRD should
  surface this as a deliberate design choice, not a bug.
- **`ClaudeOverride` vs `ClaudeConfig` split (just shipped in v0.7)** means
  vault refs inside `claude.env.vars` must be resolvable in the override
  path too — but since `ClaudeEnvConfig` is the same type in both places,
  resolution logic doesn't need to branch.
- **The "empty string suppresses a workspace mapping" idiom** in
  `MergeGlobalOverride` (line 424-432) already gives us a way to say "remove
  a team-declared `env.vars` key" — the vault-resolution layer doesn't need
  a new idiom for opting out.
- **No existing workspace config declares `[vault]` today**, so the rollout
  is backwards-compatible by construction. The addition is purely additive.

## Open Questions

1. Do we enforce a single vault provider per scope, or allow a list with
   fail-over semantics? (Fail-over complicates the algorithm considerably.)
2. Is the `team_only` list in `[vault]` worth the complexity for v1, or
   defer until a concrete use case emerges?
3. Should `vault_scope` accept globs (`vault_scope = "tsukumogami/*"`) so
   one personal table serves multiple related orgs? Probably not for v1.
4. Does the resolved-secret value get cached in-process for the apply run,
   or is each reference resolved independently? Caching matters for cost
   (provider rate limits) but has a subtle TTL/staleness story.
5. How does this interact with the planned `vault.required = false` per-key
   tag — is it a list on the `[vault]` table (`optional = ["KEY"]`) or a
   per-reference suffix (`vault:KEY?`)?

## Summary

Recommend **Option A (implicit scoping by `[[sources]].org`)** with an explicit
`workspace.vault_scope` escape hatch for multi-source or borrowed-scope
workspaces, `[vault]` at top level (not under `[claude]`), and personal-wins
conflict resolution that mirrors the existing `MergeGlobalOverride` semantics.
The resolution chain is `personal[scope] → personal[default] → team`,
computed once per apply run and reused per-reference; secrets are referenced
inline as `vault:<key>` in `env.vars`, `claude.env.vars`, and `files` values.
The PRD must explicitly address zero-source workspaces, multi-source
workspaces, cross-org workspace-name collisions, source-org vs config-repo-org
mismatches, and graceful degradation policy (recommended: fail hard with a
`--allow-missing-secrets` opt-out).
