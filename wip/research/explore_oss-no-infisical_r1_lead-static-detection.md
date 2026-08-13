# Lead: Is an *unsatisfiable* required secret statically detectable at config-merge time, before any network call?

Short answer: **partially, and the interesting part is not where the lead assumed.**
For the concrete tsukumogami case there is *no vault round-trip at all* — the
public base config declares no `[vault]` block and no `vault://` refs, so the
resolver is a no-op and the failure is already a pure config-level failure. It is
just emitted *late* (after overlay clone attempt and GitHub repo discovery) and
worded as a value-absence error rather than a mechanism-absence error. A precise
"you declared X required and this file gives no way to satisfy it" diagnostic is
computable at `config.Parse` time on a single file — but only as an *advisory*,
because at parse time niwa cannot know whether an overlay exists.

All paths below are relative to the niwa repo root unless noted.

---

## Findings

### 1. Where the required-key check actually lives, and what it sees

`checkRequiredKeys` is the entire enforcement of `*.required`
(`internal/workspace/required.go:47`). It is called from exactly one place:
`internal/workspace/apply.go:1325`, immediately after
`ResolveAndMergeEffectiveConfig`. There are no other call sites (verified by
grep across the repo).

What it inspects: for each `*.required` sub-table, whether the *parent table's
`Values` map* holds a non-empty `MaybeSecret` for that key
(`internal/workspace/required.go:105-134`). It walks `env.vars`, `env.secrets`,
`claude.env.vars`, `claude.env.secrets`, all `repos.<name>.*` variants, and
`instance.*` variants (`required.go:55-79`). `warnRecommended`
(`required.go:136-179`) does the same for `Recommended` and emits one stderr
warning per miss. `Optional` is entirely unimplemented — the code comments say
"silent in v1 (no verbose flag yet)".

The error text is:

```
required env keys not supplied:
  [env.secrets] ANTHROPIC_API_KEY: Anthropic API key for Claude — resolved by overlay vault
  ...
declare each key under the matching table with a value, supply it via the personal overlay, or remove it from the required sub-table
```

(`required.go:96-101`). Note the remedy line already names two of the three
mechanisms — it does *not* mention the workspace overlay, which is the mechanism
the tsukumogami OSS contributor is actually missing.

**`checkRequiredKeys` has zero unit tests.** No `required_test.go` exists; the
only coverage is indirect, via a functional scenario that asserts a *different*
regression (`test/functional/features/critical-path.feature:158-188`, which
mentions the error string only in a comment).

### 2. The pipeline order: when are required keys and resolution mechanisms both known?

`runPipeline` (`internal/workspace/apply.go:732`) in order:

| Step | Line | What | Network? |
|---|---|---|---|
| 0.3a/b | 838, 866 | sync personal overlay snapshot (`~/.config/niwa/global`), parse `niwa.toml` | yes (git) |
| 0.4 | 880 | credential-sync vault bootstrap check | possibly |
| 0.5 | 936 | sync/clone the **workspace overlay** (convention-derived `<repo>-overlay`) | yes (git) |
| 0.6 | 1016-1089 | parse overlay, build its own vault bundle, resolve its env, `MergeWorkspaceOverlay` | yes (vault) |
| 1 | 1100 | `discoverAllRepos` — GitHub org scan | yes (GitHub API) |
| 3a-3c | 1219, 1232 | `BuildBundle` for team + personal layers | yes (vault auth) |
| — | 1308 | `ResolveAndMergeEffectiveConfig` (team resolve → personal resolve → merge) | yes (vault) |
| — | **1325** | **`checkRequiredKeys`** | no |
| 3 | 1329+ | clone repos | yes |
| — | 1545-1560 | `DiscoverEnvFiles`, then per-repo materializers | no |

The full set of declared-required keys is known after Step 0.6 (workspace-overlay
merge adds tier maps, `internal/workspace/override.go:875-893`). The full set of
*resolution mechanisms* is known at the same point — every provider registry
(`cfg.Vault`, `overlay.Vault`, `globalOverride.Global.Vault`) is in scope by line
1232. So at line 1325 the call site holds `teamBundle`, `personalBundle`, and the
overlay bundle, plus the merged config. **It simply does not pass any of them to
`checkRequiredKeys`, which takes `(cfg, stderr)` only.** The mechanism-vs-value
distinction is therefore available at the existing call site with no
re-plumbing of the pipeline — only a signature change.

For the tsukumogami OSS-contributor case the failure needs *no* network
information at all: the base config has no `[vault]` block, so
`resolve.BuildBundle` returns empty bundles and `ResolveWorkspace` passes
through unchanged. The three `INFISICAL_*` keys and `ANTHROPIC_API_KEY`/`GH_TOKEN`
are simply never populated. The apply still burned an overlay clone attempt
(404, silently skipped at `apply.go:991-995`) and a full GitHub org scan before
reporting.

### 3. Parse-time detectability on a single file

`config.Parse` (`internal/config/config.go:449`) already runs four static passes:
`validateReservedEnvKeys` (460), `validate` (494), `VaultRegistry.Validate` (499),
and `validateNoVaultRefs` (506).

`validateNoVaultRefs` (`internal/config/validate_vault_refs.go:38`) is *the exact
mirror image* of the check the lead proposes. Its
`walkVaultRefsForUnknownProvider` (line 228) already errors with:

```
%s references %q but the config declares no [vault] providers
```

(`validate_vault_refs.go:234-239`) — i.e. "you have a resolution mechanism
pointing at a provider that doesn't exist". The proposed check is "you have a
requirement with no resolution mechanism at all". Both are single-file, pure,
zero-I/O. The helpers are in place: `VaultRegistry.IsEmpty()`
(`internal/config/vault.go:136`) and `KnownProviderNames()` (line 146).

So the computation is trivially available at parse time. The obstruction is
semantic, not technical: **at parse time niwa does not know whether an overlay
exists.** Overlay discovery is by convention (`config.DeriveOverlayURL`,
`internal/config/overlay.go:202`) and is attempted on every apply. A parse-time
hard error would break every overlay-based workspace, which is the *designed*
usage per `docs/prds/PRD-workspace-visibility-overlay.md:462`. A parse-time
*advisory* is safe.

There is already a natural home for such an advisory: `emitVaultBootstrapPointer`
(`internal/cli/init.go:939`, called at line 720) fires on `niwa init` in clone
mode with the parsed base config in hand, and prints "this workspace declares a
vault (kind: X). Bootstrap with: …". Its exact inverse — "this workspace declares
N required secrets and no vault provider; they must come from an overlay or a
personal config at `~/.config/niwa/global/niwa.toml`" — would land in the same
place, at the same moment, with the same informational (never-fails) contract.

### 4. Existing validation / lint stages

There is **no `niwa validate`, no `niwa doctor`, no `niwa lint`**. The full
command set is: `apply, config, create, destroy, go, init, instance, dispatch,
setup-sandbox, source inspect, list, status, reap, plugins, reset, worktree,
shell-init, version, watch` (from `Use:` fields across `internal/cli/`).

The closest existing lint surfaces, all under `niwa status`:

- **`niwa status --audit-secrets`** (`internal/cli/status_audit.go:40`). Fully
  offline. Loads the team config *only* (no overlay merge, no personal overlay,
  no resolve) and classifies each `*.secrets` entry as `vault-ref` / `plaintext`
  / `empty` / `resolved` (`status_audit.go:184-198`). Exits non-zero when
  plaintext is present *and* a vault is configured.
  **Critically: it enumerates `.Values` only** (`status_audit.go:156-176`).
  A key declared solely under `[env.secrets.required]` with no `Values` entry —
  which is the entire public tsukumogami config — produces **zero rows**. Running
  `niwa status --audit-secrets` against the public base config prints
  "No *.secrets entries found." That is a live gap: the one offline audit command
  is blind to exactly the declaration form the visibility-overlay feature
  introduced.
- **`niwa status --check-vault`** (`internal/cli/status_check_vault.go:32`).
  Re-resolves recorded vault sources; returns "no vault providers declared;
  nothing to check." when `cfg.Vault.IsEmpty()` (line 54). Requires an existing
  instance and state file.
- **`niwa status --audit-auth`** — offline credential-source table from
  `state.json`.

None of these read `Required`/`Recommended`/`Optional` at all. Grep for uses of
the `Required` field outside `required.go` returns only: the type definition
(`config.go:217`), the TOML decoder (`env_tables.go:36-53`), merge/deep-copy
plumbing (`override.go:875-893, 1273`; `vault/resolve/deepcopy.go:181`), and
`buildSecretsExclusionSet` (`env_example_prepass.go:154`).

### 5. Every mechanism that can supply a value for a declared-required key

This is the false-positive enumeration the lead asked for. Split into two groups,
because the split is the surprising part.

**Group A — mechanisms `checkRequiredKeys` actually sees** (they land in
`cfg.<table>.Values` before line 1325):

1. **Inline value in the same table**, plaintext or `vault://`, in the base
   `workspace.toml` — resolved by `resolve.ResolveWorkspace` against `teamBundle`.
2. **Workspace overlay** `[env.secrets]` / `[env.vars]` /
   `[repos.<n>.env.secrets]` in `workspace-overlay.toml` — resolved against the
   overlay's *own* bundle at `apply.go:1074`, then merged base-wins-per-key at
   `override.go:865-893` (top level) and `override.go:762-782` (per repo).
3. **Personal/global overlay** at `~/.config/niwa/global/niwa.toml`
   (`config.GlobalConfigDir`, `internal/config/registry.go:274`), either
   `[global.env.secrets]` or `[workspaces.<scope>.env.secrets]` — resolved
   against `personalBundle`, merged personal-wins at `override.go:616-644`,
   subject to `[vault].team_only` rejection.
4. **Machine-identity credential sync** — injects provider tokens into the
   registries (`injectProviderTokens`, `apply.go:1051, 1168, 1172`) so the above
   vault refs resolve; it does not itself add keys.

**Group B — mechanisms that supply the value in the *materialized output* but
are invisible to the required check.** Every one of these is a way the *current*
check is already wrong, and a way a stricter static check would also be wrong:

5. **`[env.files]`** — parsed in `ResolveEnvVars`
   (`internal/workspace/materialize.go:1163-1185`), which runs per repo at
   `apply.go:1560+`, i.e. **220 lines after** `checkRequiredKeys`. A key supplied
   by `env/workspace.env` satisfies `.local.env` but **fails the required check**.
   The public tsukumogami config declares `files = ["env/workspace.env"]`, so this
   is not hypothetical.
6. **Auto-discovered env files** — `DiscoverEnvFiles(configDir)`
   (`apply.go:1546`) picks up a workspace-level env file and per-repo
   `repos/<name>.env` without any config declaration. Same blindness.
7. **`.env.example` pre-pass** — `runEnvExamplePrePass`
   (`internal/workspace/env_example_prepass.go:32`) seeds `vars` as the
   lowest-priority layer (`materialize.go:1159-1161`). Note it *excludes* keys
   named in any `*.required`/`*.recommended`/`*.optional` table
   (`buildSecretsExclusionSet`, `env_example_prepass.go:148-172`) — so this one
   is deliberately not a supplier for declared keys.
8. **`.local.env` / custom `env_output` targets on disk** — written by niwa,
   never read back for value resolution. Not a supplier.
9. **Ambient process environment** — **not a mechanism at all.** Grep for
   `os.Getenv` / `os.Environ` across `internal/workspace/` and `internal/config/`
   returns only `XDG_CONFIG_HOME` lookups, a `GIT_*` sanitizer in
   `bootstrap.go:258-260`, and a subprocess env in `worktree_content.go:852`.
   Nothing reads the parent environment to satisfy an env key. This kills a whole
   false-positive category — but it also contradicts the PRD (see Surprises).
10. **`[claude.env] promote`** — *not* a supplier; it is a second, independent
    **consumer** that hard-fails on the same absence. `resolveClaudeEnvVars`
    returns `claude.env: promoted key %q not found in resolved env vars`
    (`materialize.go:961`) and the settings materializer propagates it fatally
    (`materialize.go:1011-1014`). The public tsukumogami config has
    `promote = ["GH_TOKEN"]`, so even if the `required` check were relaxed to a
    warning, `GH_TOKEN` absence would still abort apply from a different, less
    legible site. **Any "loud but non-fatal" design must address promote too.**
    (Promote *does* read from `ResolveEnvVars`, so it is satisfied by
    `[env.files]` — the two checks disagree about what counts as supplied.)
11. **Worktree apply path** (`internal/cli/session_lifecycle_cmd.go:443`,
    `ResolveAndMergeEffectiveConfig` with `AllowMissingSecrets: true`) — calls
    the merge helper but **never calls `checkRequiredKeys`**. Required enforcement
    is instance-apply-only; worktrees inherit already-materialized env
    (`InheritedEnv`, `materialize.go:132-141`).

### 6. Schema affordances for "needed to materialize" vs "needed for full workflow"

Not expressible today. The dimensions that exist:

- **Three severity levels** — `required` / `recommended` / `optional`
  (`config.EnvVarsTable`, `internal/config/config.go:215-220`; spec at
  `docs/prds/PRD-vault-integration.md:845-870`). `optional` is parsed and merged
  but never acted on. These encode *how loudly to complain*, not *when the value
  is needed*.
- **Per-URI opt-out** — `vault://KEY?required=false` sets `Ref.Optional`
  (`internal/vault/ref.go:43, 150-160`); the only accepted query parameter. This
  is per-reference, so it only helps a layer that *has* the ref — i.e. the
  overlay, not the public base config.
- **`[vault].team_only`** (`internal/config/vault.go:30`) — a lock, the inverse
  of an escape hatch: it *forbids* the personal overlay from supplying a key
  (`override.go:616-660`).
- **Per-repo tables** — `[repos.<name>.env.secrets.required]` is enforced
  (`required.go:66-72`) even though `PRD-vault-integration.md:866-870` says
  per-repo requirement tables are "NOT supported in v1 (deferred)". The public
  tsukumogami config relies on this undocumented capability for the three
  `INFISICAL_*` keys. Per-repo scoping is the *closest* thing to a "needed for
  full development workflow" signal that exists — those three keys are needed
  only to run niwa's own live vault tests — but nothing in the schema or the code
  treats a per-repo required key as weaker than a workspace-level one.
- **Run-time flags** — `--allow-missing-secrets` explicitly does *not* downgrade
  required misses (R34, `PRD-vault-integration.md:877-885`; enforced by the
  post-resolve empty-value check in `collectMissing`). `--allow-plaintext-secrets`
  is unrelated. `--no-overlay` disables overlay discovery, which makes the problem
  strictly worse.

There is no `phase`, `stage`, `when`, or `for` key anywhere in the env schema.
Adding one is additive and non-breaking (the TOML decoder in
`env_tables.go:36-53` routes reserved sub-table names explicitly), but it is a
genuinely new axis, not a repurposing of an existing one.

---

## Implications

**The precise error is cheap; the hard part is knowing whether to be loud or
fatal.** `checkRequiredKeys` already has the merged config; the call site at
`apply.go:1325` already has all three vault bundles. Passing them in turns
"required env keys not supplied" into a two-class diagnosis:
*unsatisfiable* (no provider declared in any merged layer, no overlay synced, no
personal overlay registered → the config as assembled has no route to this value)
versus *unsatisfied* (a provider exists and either the key is absent from the
backend or auth failed). Those two deserve different words and arguably different
exit codes. The information for the split exists today at the exact line where
the error is produced.

**`[env.files]` is an existing correctness bug in the required check, independent
of this exploration.** `PRD-workspace-visibility-overlay.md:316` specifies
"absent after all vault resolution **and env file sourcing**". The implementation
checks before env file sourcing. Any redesign of this check should fix the
ordering — either move `checkRequiredKeys` after materialization, or hoist
env-file parsing above line 1325. The second is cheap: `DiscoverEnvFiles` and
`parseEnvFile` are pure and configDir-relative.

**A parse-time advisory is the right shape for the OSS-contributor complaint.**
The contributor's real problem is not that apply fails — it is that it fails
*after* an overlay clone attempt and a full GitHub org scan, with a message that
reads like "you forgot to set a value" rather than "this config is the public
half of a two-part config". `emitVaultBootstrapPointer` at `init.go:720` is the
established precedent for a non-fatal, parse-time, config-shape advisory, and its
inverse fits there exactly.

**`niwa status --audit-secrets` is the obvious host for a real lint.** It is
already offline, already exits non-zero on a policy violation, and already claims
to be the secrets-audit surface — but it currently prints nothing for a
declaration-only config. Teaching it to enumerate `Required`/`Recommended`/
`Optional` alongside `Values`, with a classification like `declared-unbacked`,
would give both the OSS contributor and CI a way to ask "is this config
self-sufficient?" without running apply.

**"Loud but non-fatal" is not a one-line change**, because `[claude.env] promote`
is a second fatal path over the same key (`materialize.go:961`) and it lives in a
different subsystem with different supply semantics (it *does* see env files).
Relaxing `required` alone would move the tsukumogami failure from a legible
message to `claude.env: promoted key "GH_TOKEN" not found in resolved env vars`.

---

## Surprises

1. **No vault round-trip happens in the failing case.** The lead's framing
   ("rather than failing later on an empty value after an attempted vault
   round-trip") does not describe the tsukumogami OSS path. With no `[vault]`
   block in the base config, `BuildBundle` produces an empty bundle and the
   resolver is a pass-through. The failure is already 100% static — it is only
   *positioned* late.

2. **`niwa status --audit-secrets` reports "No *.secrets entries found." for the
   public tsukumogami config.** The only offline secrets-audit command is blind
   to declaration-only tables (`status_audit.go:156-176` reads `.Values` only).

3. **`[env.files]`-supplied values do not satisfy a required key.** The check runs
   at `apply.go:1325`; env files are parsed at `materialize.go:1163`, reached from
   `apply.go:1560`. This directly contradicts
   `PRD-workspace-visibility-overlay.md:316` ("after all vault resolution and env
   file sourcing").

4. **The ambient process environment is not a supply mechanism.**
   `PRD-workspace-visibility-overlay.md:402` writes an acceptance criterion as
   "`FOO` absent from environment: `niwa apply` aborts" — implying the environment
   is consulted. It is not; no code path reads `os.Environ()` for env values. The
   AC passes for the wrong reason.

5. **`checkRequiredKeys` has no unit tests at all**, despite being the enforcement
   point for two numbered PRD requirements (R33/R34) and the sole gate that makes
   the visibility-overlay split fail loudly.

6. **Per-repo requirement tables work despite the PRD deferring them**
   (`PRD-vault-integration.md:866-870` vs `required.go:66-72`). The public
   tsukumogami config depends on this. Docs and code disagree.

7. **`MergeWorkspaceOverlay` drops the overlay's per-repo tier maps** when a repo
   exists in both layers: the loop at `override.go:762-782` merges only
   `Env.Vars.Values` and `Env.Secrets.Values` into the base `RepoOverride`, while
   the top-level tables *do* get `Required`/`Recommended`/`Optional` merged
   (`override.go:875-893`). An overlay can therefore declare a new required key
   for a repo the base doesn't mention, but cannot add one to a repo the base
   already overrides. Asymmetric and almost certainly unintentional.

8. **`optional` is dead schema.** Parsed, merged, deep-copied, never read.
   `warnRecommended`'s comment says it awaits a `--verbose` flag; `niwa status`
   has a `--verbose` flag but apply does not.

9. **The worktree apply path skips required enforcement entirely** while sharing
   the same merge helper (`effective_config.go:65`). Whatever contract lands must
   state whether that asymmetry is intentional.

---

## Open Questions

1. **Should the parse-time signal be an error, a warning, or silent?** A hard
   error is wrong (breaks every overlay workspace by design). A warning on every
   `config.Parse` would fire inside `niwa status`, completion, and every internal
   re-parse. Does it belong on `init` only (next to `emitVaultBootstrapPointer`),
   or gated behind an explicit `niwa status --audit-secrets`?

2. **Is "no overlay was found" a distinguishable state worth surfacing?**
   Convention discovery failure is silently swallowed at `apply.go:991-995`
   ("Fresh clone failed: overlay repo likely doesn't exist — skip silently").
   Making required-key failure precise arguably requires making *that* visible
   first: "no overlay repo at tsukumogami/dot-niwa-overlay (404); N required keys
   have no other source."

3. **Should `[env.files]` count as satisfying a required key?** The PRD says yes,
   the code says no. Fixing it means either reordering the pipeline or accepting
   that the check is per-repo (env files can differ per repo, so "required" would
   become a per-repo predicate). This is a product decision, not a refactor.

4. **What is the intended severity of a per-repo required key?** The three
   `INFISICAL_*` keys are required only to run niwa's live vault tests, yet they
   block the entire workspace materialization for every contributor. Is per-repo
   scoping meant to imply weaker enforcement, or does the schema need a genuinely
   new axis?

5. **What happens to `[claude.env] promote` under a non-fatal required policy?**
   Options: promote-miss becomes a warning and the key is omitted from
   `settings.local.json`; or promote implies required and the two checks are
   unified. Today they disagree about what counts as supplied.

6. **Does the public base config want to keep declaring `required` at all?**
   Nothing forces it — `recommended` already produces a loud stderr warning per
   miss and lets apply proceed. The cheapest fix for the tsukumogami instance is a
   config edit, not a niwa change. Whether niwa *should* additionally make the
   failure legible is the separable product question, and the answer determines
   whether this exploration produces a niwa artifact, a config change, or both.

7. **Should the check be able to see auth state?** "Provider declared but
   unauthenticated" is a third class distinct from unsatisfiable/unsatisfied.
   `credentialPool.VaultUnreachableObservations()` (`apply.go:1192`) already
   collects it and warns separately; correlating it with required-key misses would
   turn "ANTHROPIC_API_KEY not supplied" into "vault provider unreachable; the 4
   keys it backs are unresolved."

---

## Summary

The unsatisfiable-required-secret contradiction is statically computable — `config.Parse` already runs the mirror-image check (`vault://` ref with no declared provider, `validate_vault_refs.go:234-239`), and the apply-time call site at `apply.go:1325` already holds every vault bundle it would need to split "no mechanism exists" from "mechanism exists but the value is missing", yet passes `checkRequiredKeys` only the config; for the tsukumogami OSS case no vault call happens at all, so the failure is already purely static and merely late and badly worded. The bigger finding is that the existing check is already wrong in the other direction: `[env.files]`, auto-discovered env files, and the whole materialization layer supply values *after* the check runs, contradicting `PRD-workspace-visibility-overlay.md:316`, while `niwa status --audit-secrets` — the only offline audit command — reads `.Values` only and prints "No *.secrets entries found." for a declaration-only config like the public base. The biggest open question is whether a static signal should fire at parse time at all given that overlay presence is unknowable there, or whether the real fix is to make silent overlay-discovery failure (`apply.go:991-995`) visible and to decide what `[claude.env] promote` — a second, independent fatal path over the same key — should do under any "loud but non-fatal" policy.
