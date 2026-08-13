# Lead: Can niwa distinguish "no vault provider configured at all" from "provider configured but unreachable" from "provider reachable but the key is missing" at runtime -- and does it act differently on each?

Short answer: niwa distinguishes **two** of the three at the vault layer
(`ErrKeyNotFound` vs `ErrProviderUnreachable`), and acts differently on
exactly one of them. The third case -- "no provider configured at all" --
never reaches the vault layer, so it is not a vault condition at all: it is
a `*.required` bookkeeping miss handled by a function
(`checkRequiredKeys`) that has zero knowledge of vaults. All three
converge on a hard, non-overridable apply failure, and two of the three
produce a message that never mentions a vault.

All paths below verified against the worktree at
`/Users/danielgazineu/dev/niwaw/tsuku/tsuku+tsuku_oss_no_infisical-26c0f110/public/niwa/.claude/worktrees/oss-no-infisical`.

---

## Findings

### 1. The Infisical backend's three failure shapes, and where they land

The backend is subprocess-only (R20: no Go SDK). `Factory.Open` is
deliberately lazy -- it validates config keys and returns, **without ever
invoking `infisical`**
(`internal/vault/infisical/infisical.go:89-160`, doc comment lines 6-10).
So a missing CLI binary is *never* detected at `Factory.Open`/`Registry.Build`
time; it is detected on the first `Resolve`, inside `ensureLoaded`
(`infisical.go:346-383`) -> `runInfisicalExport`
(`internal/vault/infisical/subprocess.go:119-172`).

`defaultCommander.Run` (`subprocess.go:56-78`) separates two outcomes:
process-never-started returns `(stderr, stdout, exitCode=-1, err != nil)`;
started-but-nonzero returns `(…, exitCode=N, err=nil)`. `runInfisicalExport`
then classifies:

| Host condition | Code path | Sentinel |
|---|---|---|
| CLI binary absent from `$PATH` | `subprocess.go:138-147` (start failure) | `ErrProviderUnreachable` |
| CLI present, not logged in / 401 / 403 / session expired | `subprocess.go:148-155` gated on `looksLikeAuthFailure` | `ErrProviderUnreachable` |
| CLI present, authenticated, but **no access to the project** | Infisical returns 401/403 -> same marker match | `ErrProviderUnreachable` |
| CLI present, nonzero exit with **unrecognised** stderr | `subprocess.go:156-159` | *no sentinel* -- bare `secret.Errorf` |
| CLI present, authenticated, project readable, key absent from payload | `infisical.go:249-256` | `ErrKeyNotFound` |
| Provider `Close()`d, then resolved | `infisical.go:243-247`, `346-353` | `ErrProviderUnreachable` |

So: **"CLI not installed", "CLI not logged in", and "no project access"
are all collapsed into one sentinel with no discriminator field.** The only
thing that distinguishes them downstream is the free-text error string
(`exec: "infisical": executable file not found in $PATH` vs a scrubbed
stderr excerpt). Tests lock this in:
`internal/vault/infisical/infisical_test.go:354-366`
(`TestStartFailureMapsToUnreachable`) and `:298-327`
(`TestAuthFailureMapsToUnreachable`).

The auth-marker list is a **substring allowlist** of nine phrases
(`subprocess.go:258-281`): `401`, `403`, `unauthorized`, `unauthorised`,
`forbidden`, `not logged in`, `login expired`, `invalid credentials`,
`authentication failed`, `session expired`. It was deliberately *tightened*
-- `auth` and `token` were removed because they misclassified transient
network faults. The consequence is real and asserted in tests:
`internal/vault/infisical/infisical_test.go:614-643` locks in that
`"please run infisical login"` -> **false** and `"invalid token"` ->
**false**. If a future Infisical CLI release phrases its
not-logged-in error as anything outside those nine strings, the
unauthenticated maintainer's failure silently reclassifies from
`ErrProviderUnreachable` into the *unclassified* bucket. Nothing pins the
CLI's actual wording; there is no integration test against a real binary
covering this (`integration_test.go` is build-tagged).

A separate auth surface exists for multi-org machine identities:
`infisical.Authenticate` (`internal/vault/infisical/auth.go:33-107`) does an
HTTP POST to universal-auth. **None of its error paths wrap
`ErrProviderUnreachable`** -- a network failure, an HTTP 401 from a rotated
`client_secret`, or a missing `accessToken` field all return plain
`secret.Errorf` values (`auth.go:68,73,79,85,91,101,104`). That means a
credential that authenticates over HTTP is on a *different* classification
track than a credential that authenticates via the CLI session, and the
former can never be softened by any `errors.Is(…, ErrProviderUnreachable)`
check.

### 2. `ErrProviderUnreachable` is classified but **not acted on differently** by the resolver

This is the load-bearing finding. `resolveOne`
(`internal/vault/resolve/resolve.go:470-562`) has three terminal branches:

- `ErrKeyNotFound` (`:526-545`): honours `ref.Optional` (`?required=false`,
  silent empty) **and** `opts.AllowMissing` (`--allow-missing-secrets`,
  warn + empty). Only here.
- `ErrProviderUnreachable` (`:550-555`): **always** returns an error. No
  `Optional` check, no `AllowMissing` check.
- everything else (`:558-561`): always an error.

So `vault://ANTHROPIC_API_KEY?required=false` -- an explicitly optional
secret -- **still hard-fails the whole apply** when the provider is
unreachable. And `--allow-missing-secrets` does nothing for the
no-Infisical-CLI case. This is intentional and asserted:
`internal/vault/resolve/resolve_test.go:648-680`
(`TestResolveWorkspaceProviderUnreachable`) sets `AllowMissing: true` and
requires the error anyway, with the comment "AllowMissing targets
ErrKeyNotFound".

Two doc comments in the tree state the opposite and are stale:

- `internal/vault/errors.go:11-16`: "`--allow-missing-secrets` consults
  this sentinel to decide whether to downgrade." It does not.
- `internal/vault/infisical/infisical_test.go:645-650`: "Under
  `--allow-missing-secrets` that classification would silently downgrade
  the result to empty, masking a retriable fault." It would not -- which
  removes the stated justification for the marker-list tightening.

Also stale: `internal/workspace/effective_config.go:12-20` claims "the
worktree apply path sets this true so a transient vault outage degrades a
worktree re-materialization to a warning rather than a hard failure." The
worktree path no longer calls `ResolveAndMergeEffectiveConfig` at all
(`internal/cli/session_lifecycle_cmd.go:348-355` -- it byte-copies the
instance's already-materialized env), and the only production caller is
`internal/workspace/apply.go:1308`.

One more misclassification worth noting: an **unknown provider name** at
resolve time (a `vault://team/KEY` ref where `team` is not in the bundle)
is deliberately returned as a wrapped `ErrKeyNotFound`
(`resolve.go:502-517`), which means it *is* softenable by
`--allow-missing-secrets` even though the actual cause is a config
topology error, not a missing key.

### 3. "Required keys declared, no `[vault.provider]`, no `vault://` anywhere" -- the OSS-contributor path

This case never touches `internal/vault` at all. Trace:

1. Parse-time: `validateNoVaultRefs` -> `walkVaultRefsForUnknownProvider`
   (`internal/config/validate_vault_refs.go:228-296`) would emit
   `"<location> references <uri> but the config declares no [vault]
   providers"` -- **but only if a `vault://` string exists**. With no refs,
   `check()` short-circuits at `:231`. Parse succeeds cleanly.
2. `BuildBundle` with a nil `cfg.Vault` produces an **empty bundle**, which
   is explicitly valid (`internal/vault/resolve/resolve.go:77-98`,
   `specsFromRegistry` returns nil for nil `vr`). No error.
3. `resolveOne` never fires -- there are no values to resolve; the
   `Required` sub-table holds *description strings*, not values, and lives
   in `EnvVarsTable.Required`, a map the resolver's `walkTable` never
   visits (it iterates `.Values`).
4. `checkRequiredKeys` (`internal/workspace/required.go:47-103`), called at
   `internal/workspace/apply.go:1325-1327`, walks `t.Required` and finds no
   matching entry in `t.Values` (`collectMissing`, `required.go:111-128`).
   Hard error.

The message is (`required.go:96-102`):

```
required env keys not supplied:
  [env.secrets] ANTHROPIC_API_KEY: Anthropic API key for Claude — resolved by overlay vault
  [env.secrets] GH_TOKEN: GitHub PAT with repo scope — supplied via personal overlay
declare each key under the matching table with a value, supply it via the
personal overlay, or remove it from the required sub-table
```

This is exactly the scenario asserted by
`internal/workspace/apply_vault_test.go:677-744`
(`TestApplyFailsOnMissingRequiredEnvSecret`) -- a config with
`[env.secrets.required]` and no `[vault.*]` block whatsoever.

The remediation line is the only vault-adjacent signal, and it is
misleading for this persona in two ways: (a) "personal overlay" means
`~/.config/niwa/niwa.toml`, not the workspace overlay repo the contributor
couldn't clone; (b) the only actually-reachable remedy for an OSS
contributor -- "these are developer conveniences, proceed without them" --
is not offered and does not exist.

`checkRequiredKeys` is not downgradeable. `--allow-missing-secrets` is
explicitly excluded (PRD R34, `docs/prds/PRD-vault-integration.md:877-886`;
asserted by `apply_vault_test.go:745-…`). And `niwa init` does not even
expose the flag -- only `apply` (`internal/cli/apply.go`) and `create`
(`internal/cli/create.go:22`) do. `niwa init <org>/<repo>` runs
`Applier.Create` inside the bootstrap orchestrator
(`internal/cli/init.go:184-196, 210-222`), so the contributor's first
command fails with no escape hatch on that command at all.

### 4. Overlay auto-discovery and what the merged config looks like without it

`DeriveOverlayURL` (`internal/config/overlay.go:197-215`) is pure string
manipulation: `org/repo` -> `org/repo-overlay`, with HTTPS/SSH/`file://`
variants. For tsukumogami it yields `tsukumogami/dot-niwa-overlay`.

The discovery branch is `internal/workspace/apply.go:983-1012`, reached
only when `!opts.noOverlay` and `opts.overlayURL == ""` (i.e. nothing
recorded in `InstanceState.OverlayURL` yet). It calls `a.cloneOrSync` ->
`EnsureOverlaySnapshot` (`internal/workspace/overlaysync.go:29-53`), whose
`wasFreshClone` return exists precisely to separate "overlay repo doesn't
exist -- silent skip" from "previously-cloned overlay failed to refresh --
hard error". The skip is at `apply.go:990-994`:

```go
if cloneErr != nil {
    if wasCloneAttempt {
        // Fresh clone failed: overlay repo likely doesn't exist — skip silently.
        break
    }
    return nil, fmt.Errorf("workspace overlay sync failed. Use --no-overlay to skip.")
}
```

**`break` with no `Reporter` call of any kind.** A 404 (repo doesn't exist)
and a 403 (repo exists, you can't read it) are indistinguishable here --
the error value is discarded entirely. The OSS contributor gets zero
signal that a whole config layer was skipped.

Consequently, for a contributor without overlay access, the merged config
is **exactly the public base**
(`public/dot-niwa/.niwa/workspace.toml`): five `required` keys
(`[env.secrets.required]` ANTHROPIC_API_KEY, GH_TOKEN;
`[repos.niwa.env.secrets.required]` INFISICAL_TEST_PROJECT_ID,
INFISICAL_CLIENT_ID, INFISICAL_CLIENT_SECRET), no `[vault]` block, no
`vault://` string anywhere. The base file's own comment says it plainly:
"Declare what secrets this workspace needs without specifying vault
addresses. The overlay provides `[vault.provider]` and resolves these via
`vault://`."

Two knock-on effects:

- `emitVaultBootstrapPointer` (`internal/cli/init.go:928-953`) reads
  `result.Config.Vault` from the **base** `workspace.toml` only. The base
  declares no providers, so the "this workspace declares a vault (kind:
  infisical). Bootstrap with `infisical login`" hint **never fires for
  tsukumogami**, for either persona. It also fires before apply, so it
  could not have doubled as the diagnostic.
- `niwa status --check-vault` (`internal/cli/status_check_vault.go:54-57`)
  likewise reads only `cfg.Vault` and prints "no vault providers declared;
  nothing to check." for both personas -- correct for the contributor,
  actively wrong for the maintainer whose overlay *does* declare one.

### 5. The maintainer-on-a-fresh-host path fails somewhere else entirely

With overlay access, the overlay clones, `overlayDir` is set, and
`apply.go:1016-1089` (Step 0.6) runs: parse overlay ->
`injectProviderTokens(ctx, credentialPool, overlay.Vault)` ->
`resolve.BuildBundle(overlay.Vault)` (succeeds; Open is lazy) ->
`resolve.ResolveWorkspace` over the overlay's own `[env.secrets]`.

That last call is where the first `infisical export` subprocess fires.
With no CLI on `$PATH` it returns `ErrProviderUnreachable`, `resolveOne`
refuses to soften it, and `apply.go:1078-1080` wraps it:

```
resolving overlay vault references: vault: env.secrets.ANTHROPIC_API_KEY:
provider "(anonymous)" unreachable while resolving key "ANTHROPIC_API_KEY":
infisical: running export: vault: provider unreachable: exec: "infisical":
executable file not found in $PATH
```

Note this aborts **before** `checkRequiredKeys` at `:1325` -- so the two
personas do *not* in fact converge on the same code path, contrary to the
scope's framing. They converge on the same *outcome* (hard failure, exit
non-zero, no instance) via two different failure sites roughly 250 lines
apart, with two error messages that share no vocabulary. The maintainer's
message is diagnostically decent (it names the binary and the key); the
contributor's message never mentions vault, Infisical, or the overlay.

Reaching for `--no-overlay` on the maintainer path just relocates the
failure: overlay skipped -> no `[vault.provider]`, no values -> the
contributor's `checkRequiredKeys` error. Both escape hatches lead to the
same wall.

### 6. `vaultUnreachableError` / `isSoftenable` / the R13.1 warning cover a *different* vault

This machinery is the closest thing niwa already has to "loud but
non-fatal" -- and it is scoped to **credential-sync bootstrap only**, not
to secret resolution.

`vaultUnreachableError` (`internal/workspace/credentialpool.go:198-213`) is
produced in exactly one place: `lookupVault`
(`credentialpool.go:463-472`), which is reachable only when
`p.vaultLoader != nil`. That loader is constructed only when the user's
*personal global* config (`~/.config/niwa/niwa.toml`) declares an anonymous
`[global.vault.provider]` (`apply.go:899-927` via `pickCredentialSyncSpec`,
`internal/workspace/credentialsync.go:31-41`). Neither persona has one.
`credentialPool.vaultLoader` is nil, `lookupVault` never runs,
`recordVaultUnreachable` never fires,
`VaultUnreachableObservations()` returns empty, and the R13.1 emitter at
`apply.go:1191-1205` iterates zero times.

Where it *is* rendered: `apply.go:1201-1204`, via `Reporter.Warn`, which is
`Reporter.Log("warning: "+format, …)` writing immediately to the reporter's
writer -- inline stderr, not deferred to the post-summary block
(`internal/workspace/reporter.go:137-148`). Text:
`warning: personal vault provider <name> unreachable; falling back to
local-file and cli-session credentials.` Deduplicated per provider name
across all three `injectProviderTokens` call sites (`apply.go:1051`,
`:1168`, `:1172`), emitted before `BuildBundle` so the R13.2 hard-fail
path still shows it (AC-17).

`isSoftenable` (`internal/workspace/providerauth.go:211-214`) is an
`errors.As` on the *concrete type*, not `errors.Is` on the sentinel. That
matters: a raw `ErrProviderUnreachable` arriving through any path other
than `lookupVault` -- e.g. from `Registry.Build` or from `resolveOne` --
is **not** softenable, because nothing else in the codebase constructs a
`*vaultUnreachableError`. So the softening precedent exists, works, is
tested (`credentialpool_r13_test.go:14-208`,
`credentialpool_lazyvault_test.go:193-208`), and structurally cannot reach
the case this exploration is about.

The R13 failure-mode table
(`docs/prds/PRD-machine-identity-vault-sync.md:533-560`) is worth reading
as prior art: it is a seven-row matrix that assigns each distinguishable
failure an explicit behavior and exit code, including row 1 = "warn,
continue, exit 0". That is precisely the shape the secret-resolution path
lacks.

---

## Implications

**The taxonomy exists but is one level too shallow.** Two sentinels
(`ErrKeyNotFound`, `ErrProviderUnreachable`) plus one unclassified bucket
cover a space that actually has at least five distinguishable states:
no provider declared, provider declared but backend tooling absent,
tooling present but unauthenticated, authenticated but unauthorized for
this project, and authorized but key absent. Only the last one gets
distinct behavior. Any design that wants per-case severity needs either a
richer sentinel set or a typed error carrying a reason code -- and the
`vaultUnreachableError` type is the existing pattern to copy.

**The `required` contract and the vault contract don't know about each
other.** `checkRequiredKeys` (`required.go:47-103`) takes only
`*config.WorkspaceConfig` and an `io.Writer`. It cannot tell "you declared
this required and forgot to fill it in" from "you declared this required,
the layer that fills it in exists but you can't see it" from "the layer
exists, you can see it, but its backend is down". Whatever "loud but
non-fatal" means, it has to be decided at a layer that holds both facts --
and today no single function does. The natural join point is `apply.go`
around `:1325`, which already has `overlayDir`, `cfg.Vault`, and the
credential pool's observations in scope.

**Severity is currently a property of the table, not of the reason for the
miss.** `required` -> fatal, `recommended` -> `warnRecommended`
(`required.go:146-179`, one stderr line each), `optional` -> silent. The
tsukumogami base config puts `ANTHROPIC_API_KEY` and `GH_TOKEN` under
`required` -- correct for a maintainer, wrong for a read-only contributor,
and the level is not conditional on who is running. Either the level must
become conditional (on visibility/overlay presence/a flag), or the config
must move keys down a level and accept that maintainers lose the hard gate.

**The silent overlay skip is arguably the single highest-leverage fix.**
One `Reporter.Warn` at `apply.go:992` -- "overlay `tsukumogami/dot-niwa-overlay`
could not be cloned; continuing with the public config only" -- would turn
the contributor's confusing message into a comprehensible one at near-zero
cost, independent of whatever is decided about severity.

**Optional refs (`?required=false`) do not survive an unreachable
provider.** Anyone reaching for the existing per-ref opt-out as the
"degrade gracefully" mechanism will find it only covers missing keys. If
"loud but non-fatal" is to be expressed per-ref rather than per-workspace,
`resolve.go:550-555` has to change.

---

## Surprises

1. **Three stale doc comments all assert that `--allow-missing-secrets`
   softens `ErrProviderUnreachable`.** `internal/vault/errors.go:11-16`,
   `internal/vault/infisical/infisical_test.go:645-650`, and
   `internal/workspace/effective_config.go:12-20`. The code
   (`resolve.go:550-555`) and its dedicated test
   (`resolve_test.go:648-680`) say otherwise. Two of the three comments are
   the *rationale* for other decisions (the marker-list tightening; the
   worktree path's flag setting), so the reasoning behind those decisions
   is now unsupported.

2. **The two personas do not share a code path.** The scope assumed both
   "funnel into an empty resolved value that trips the same hard
   `checkRequiredKeys` failure". The maintainer never reaches
   `checkRequiredKeys` -- `resolveOne` aborts the apply ~250 lines earlier,
   at `apply.go:1078`. Only the contributor hits `required.go`.

3. **The overlay clone failure is completely silent** -- `break` with the
   error value discarded (`apply.go:990-994`). No warning, no deferred
   notice, no distinction between "repo doesn't exist" and "you lack
   access".

4. **`emitVaultBootstrapPointer` cannot fire for tsukumogami.** It reads
   the base config's `cfg.Vault` (`init.go:939-942`), which is nil because
   the provider lives in the overlay. Same blind spot in `niwa status
   --check-vault` (`status_check_vault.go:54-57`), which will tell an
   overlay-holding maintainer "no vault providers declared".

5. **`Factory.Open` never probes the backend.** So `Registry.Build` /
   `BuildBundle` succeed on a host with no `infisical` binary at all. There
   is no cheap pre-flight reachability check anywhere; the first signal is
   a full `infisical export` on the resolve path.

6. **An unknown provider name is reported as `ErrKeyNotFound`**
   (`resolve.go:502-517`), making a config-topology error softenable by
   `--allow-missing-secrets` while a genuine backend outage is not. The
   softening axis is inverted relative to intuition.

7. **`isSoftenable` matches a concrete type, not the sentinel**
   (`providerauth.go:211-214`), so the existing soften mechanism is
   structurally unreachable from the resolver even if someone wired it up
   naively.

8. **`niwa init` has no `--allow-missing-secrets`.** Only `apply` and
   `create` do. The contributor's very first command has no override.

---

## Open Questions

1. **What does the real Infisical CLI print when not logged in?** The
   nine-marker allowlist (`subprocess.go:263-274`) is the only thing
   keeping the unauthenticated maintainer in the `ErrProviderUnreachable`
   bucket, and `infisical_test.go:635-636` proves `"please run infisical
   login"` and `"invalid token"` fall *outside* it. Needs a human with the
   CLI installed to capture actual stderr for: not-logged-in, expired
   session, valid session but no project access. Without that, any
   severity policy keyed on the sentinel is built on an unverified string
   match.

2. **Is the `required` level a property of the key or of the reader?**
   `ANTHROPIC_API_KEY` genuinely is required to run Claude in this
   workspace and genuinely is not required to read the code. Does the fix
   live in niwa (conditional severity, a new level, a `--no-secrets` mode)
   or in the tsukumogami config (demote to `recommended` and accept losing
   the maintainer gate)? This is the responsibility question the
   exploration set out to answer and the code does not settle it.

3. **Should a silent overlay skip stay silent, warn, or become a distinct
   config state?** Today it is invisible. If the merged config is
   materially different from what the config author intended, arguably
   `InstanceState` should record "overlay expected but unavailable" so
   later commands (`status`, `apply`) can reason about it -- not just emit
   a one-shot warning.

4. **Where should the join between "required is unmet" and "and here is
   why" be implemented?** `checkRequiredKeys` currently takes only a config
   and a writer. Threading vault/overlay context into it, versus computing
   the explanation at the `apply.go:1325` call site and passing it in,
   versus a separate diagnostic pass -- all three are viable and the choice
   shapes how testable the result is.

5. **Does the answer differ for `[repos.niwa.env.secrets.required]`?** The
   three `INFISICAL_*` keys there are test fixtures for niwa's own
   integration suite, arguably a third severity class ("required to run
   *these particular tests*"). PRD R33
   (`docs/prds/PRD-vault-integration.md:869-872`) says per-repo requirement
   tables are explicitly *not supported in v1* -- yet
   `collectMissing` is called for `repos.<name>.env.secrets`
   (`required.go:62-71`) and the tsukumogami base config uses them. Worth
   confirming whether that is intentional scope creep or an
   unreviewed gap.

6. **Should `?required=false` survive an unreachable provider?** Currently
   no (`resolve.go:526-536` gates `ref.Optional` inside the
   `ErrKeyNotFound` branch only). Making it survive is a small change with
   a large blast radius -- it would let a workspace author express
   per-secret degradation without any new syntax.

---

## Summary

niwa's vault layer distinguishes only missing-key from provider-unreachable, collapsing "CLI not installed", "not logged in", and "no project access" into one sentinel matched by a nine-phrase stderr allowlist -- and it acts differently on exactly one of them, since `resolveOne` softens `ErrKeyNotFound` under `--allow-missing-secrets`/`?required=false` but always hard-fails `ErrProviderUnreachable` (`internal/vault/resolve/resolve.go:526-555`), contradicting three stale doc comments that claim otherwise. The two personas do not share a code path as the scope assumed: the maintainer aborts at the overlay resolve (`apply.go:1078`) with a vault-literate message, while the OSS contributor's overlay clone fails *completely silently* (`apply.go:990-994`) and he dies ~250 lines later in `checkRequiredKeys`, a function with zero vault awareness whose remediation text never mentions Infisical, the overlay, or the fact that a config layer went missing. The existing "loud but non-fatal" precedent -- `vaultUnreachableError` / `isSoftenable` / the aggregated R13.1 `Reporter.Warn` at `apply.go:1191-1205` -- is real, tested, and structurally unreachable here, because it fires only for *credential-sync* lookups gated on a personal `[global.vault.provider]` neither persona has; the biggest open question is whether `required` severity should become conditional on the reader or whether the tsukumogami config should demote these keys and give up the maintainer's hard gate.
