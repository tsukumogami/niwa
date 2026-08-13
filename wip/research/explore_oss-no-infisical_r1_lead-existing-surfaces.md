# Lead: What escape hatches and warning surfaces for missing secrets already exist in niwa, and are they discoverable and reachable from where the failure happens?

All paths below are relative to the niwa worktree
`public/niwa/.claude/worktrees/oss-no-infisical/`.

## Findings

### 1. `--allow-missing-secrets`: registered on two commands, absent from four

Registered on exactly two commands:

- `internal/cli/apply.go:24-26`
- `internal/cli/create.go:22-24`

Both use identical help text (verified by running `niwa create --help` on a
locally built binary):

```
      --allow-missing-secrets     downgrade unresolved vault:// references to empty strings with stderr warnings. Does NOT override *.required misses. One-shot -- re-evaluated each invocation.
```

Not registered on:

- `niwa init` — flag list at `internal/cli/init.go:45-54` has ten flags, none of them this one.
- `niwa dispatch` — `internal/cli/dispatch.go:23-36`.
- `niwa reset` — builds an Applier at `internal/cli/reset.go:97` and calls
  `applier.Create` at `:151` with `AllowMissingSecrets` never set.
- `niwa instance from-hook` — the SessionStart ephemeral-instance provisioner.
  `internal/cli/instance_from_hook.go:365` builds the Applier, `:417` calls
  `applier.Create`. No flag, no env var, no config key sets
  `AllowMissingSecrets` on this path. This matters more than the others:
  it is the path that runs unattended on every dispatched session.

There is no environment variable or config-file equivalent anywhere. A search
for `NIWA_ALLOW*` / any `Getenv` feeding `AllowMissingSecrets` returns nothing.
The flag is one-shot by design and deliberately not persisted
(`docs/guides/vault-integration.md:565` documents the parallel
`--allow-plaintext-secrets` non-persistence rule).

### 2. What the flag actually downgrades — much narrower than the name suggests

`internal/vault/resolve/resolve.go:524-560`, `resolveOne`:

- `AllowMissing` is consulted **only** inside the `errors.Is(resolveErr,
  vault.ErrKeyNotFound)` branch (line 531).
- `ErrProviderUnreachable` falls to line 549 and returns a hard error
  regardless of the flag:
  ```go
  if errors.Is(resolveErr, vault.ErrProviderUnreachable) {
      return config.MaybeSecret{}, secret.Errorf(
          "vault: %s: provider %q unreachable while resolving key %q: %w", ...)
  }
  ```
- `?required=false` (`ref.Optional`) is likewise checked only in the
  key-not-found branch, so a per-ref opt-out **also** does not survive an
  unreachable provider.

And "Infisical CLI not installed" maps to exactly that unreachable class.
`internal/vault/infisical/subprocess.go:138-146`:

```go
// Process failed to start (e.g., CLI not installed). ...
return nil, vault.VersionToken{}, secret.Errorf(
    "infisical: running export: %w: %w",
    vault.ErrProviderUnreachable, err,
)
```

An auth failure detected in stderr maps to the same sentinel
(`subprocess.go:149-154`).

So: **neither `--allow-missing-secrets` nor `?required=false` helps a host with
no Infisical CLI, even for keys the config itself declares optional.** This is a
distinct failure mode from the R33/R34 required-key one and is not documented
anywhere.

Provider construction is lazy (`resolve.BuildBundle` at
`internal/vault/resolve/resolve.go:85-98` never shells out; the Infisical
provider only execs in `ensureLoaded`, `infisical.go:346`), so the failure lands
at first *resolution*, not at bundle build.

### 3. The actual tsukumogami failure is neither of those — it is a bare `*.required` miss with no vault involved

The public base config `tsukumogami/dot-niwa`
(`public/dot-niwa/.niwa/workspace.toml`) declares five required keys and no
`[vault.provider]` and no `vault://` values at all:

```toml
[env.secrets.required]
ANTHROPIC_API_KEY  = "Anthropic API key for Claude — resolved by overlay vault"
GH_TOKEN           = "GitHub PAT with repo scope — supplied via personal overlay"

[repos.niwa.env.secrets.required]
INFISICAL_TEST_PROJECT_ID = "Infisical test project ID — resolved by overlay vault"
INFISICAL_CLIENT_ID       = "Infisical machine identity client ID — resolved by overlay vault"
INFISICAL_CLIENT_SECRET   = "Infisical machine identity client secret — resolved by overlay vault"
```

Because there are no values, the resolver never runs on these keys. The
resolver's flag is irrelevant. The failure is purely
`checkRequiredKeys` in `internal/workspace/required.go:47`, invoked at
`internal/workspace/apply.go:1325`. This is the **only** call site
(`ResolveAndMergeEffectiveConfig` is also called from
`internal/cli/session_lifecycle_cmd.go:352`, a fork that skips the required
check entirely — noted in that file's own comment as drift to be fixed).

### 4. `niwa init` never reaches the required check — the reported symptom is at `create` / `from-hook`

I traced `runInit` (`internal/cli/init.go:441-836`) end to end. The
non-bootstrap `--from` clone path materializes `.niwa/`, runs a post-flight
`config.Load`, emits the vault bootstrap pointer, registers in the global
registry, writes instance state, and calls `MaterializeWorkspaceRoot`. It never
builds an Applier and never calls `runPipeline`. Only the `--bootstrap` path
does (`defaultRunBootstrap`, `init.go:175-197` → `applier.Create`).

So `niwa init tsukumogami --from tsukumogami/dot-niwa` should succeed on the
required-key axis; the abort the maintainer reported almost certainly comes from
the immediately-following `niwa create`, or from the SessionStart hook
(`niwa instance from-hook`) that this workspace's ephemeral-session mode fires.
Worth confirming with the maintainer — it changes which command needs the fix.
Either way, adding the flag to `init` alone would fix nothing.

### 5. Verbatim failure output (reproduced locally)

Built `cmd/niwa` at this worktree's HEAD, made a throwaway workspace declaring
the same shape, ran `niwa create --no-progress`. Full stderr:

```
warning: recommended env key "TAVILY_API_KEY" not supplied: Tavily search API key - resolved by overlay vault (scope env.secrets)
required env keys not supplied:
  [env.secrets] ANTHROPIC_API_KEY: Anthropic API key for Claude - resolved by overlay vault
  [repos.niwa.env.secrets] INFISICAL_CLIENT_ID: Infisical machine identity client ID - resolved by overlay vault
declare each key under the matching table with a value, supply it via the personal overlay, or remove it from the required sub-table
```

Exit code 1. Re-running the identical command **with**
`--allow-missing-secrets` produced byte-identical output and exit 1, confirming
R34 empirically.

Quality assessment of that message: it does name each key, its scope, and the
team-authored description — that half is genuinely good and satisfies PRD
R33's "self-documenting" requirement. What it does not say:

- That a personal overlay is a **GitHub repo** registered via
  `niwa config set global <org/repo>` and snapshotted to
  `~/.config/niwa/global/niwa.toml` (`internal/workspace/apply.go:204,867`;
  `internal/config/registry.go:274`; `internal/cli/config_set.go:24-87`).
  "Supply it via the personal overlay" reads like editing a local file; it is
  not.
- That "remove it from the required sub-table" means editing an upstream repo
  the contributor does not own. The local `.niwa/` is a snapshot — README:2:
  "Manual edits inside `.niwa/` don't survive a refresh, so make changes
  upstream and re-apply." So two of the three suggested remediations are
  unavailable to an OSS contributor, and the message does not say which one to
  reach for.
- That the workspace overlay which was *supposed* to supply these keys was
  silently skipped (see next finding).
- Any flag, any escape hatch, any doc pointer.

The error is not wrapped by callers: `runPipeline` returns it, `Applier.Create`
passes it through unchanged (`apply.go:457-460`), and `cli.Execute` prints it
raw (`internal/cli/root.go:42,95` — `SilenceErrors: true`, then
`fmt.Fprintln(os.Stderr, err)`). So no "Error:" prefix, no added context.

### 6. The overlay skip is silent — cause and effect are disconnected

`internal/workspace/apply.go:989-994`:

```go
wasCloneAttempt, overlayRank, cloneErr := a.cloneOrSync(ctx, conventionURL, dir)
if cloneErr != nil {
    if wasCloneAttempt {
        // Fresh clone failed: overlay repo likely doesn't exist — skip silently.
        break
    }
```

For an OSS contributor, `tsukumogami/dot-niwa-overlay` is private and
inaccessible, so this fires and produces **zero output**. Moments later the same
apply fails on five required keys whose descriptions all say "resolved by
overlay vault". Nothing connects the two. One `Reporter.Warn` line here would
close most of the diagnostic gap on its own.

Contrast: when the overlay URL is already recorded in state, the same failure is
a hard error with an escape hatch named inline (`apply.go:956`):
`"workspace overlay sync failed. Use --no-overlay to skip."`

### 7. `niwa status --audit-auth`: real, but unreachable at the moment of failure

`internal/cli/status_audit_auth.go:34-38` opens with:

```go
instanceRoot, err := workspace.DiscoverInstance(cwd)
if err != nil {
    return fmt.Errorf("--audit-auth must run inside a workspace instance: %w", err)
}
```

It renders a four-column KIND / PROJECT-UUID / SOURCE / FALLBACK table read
purely from `state.AuthSources` in `state.json` — offline, no network. It exits
non-zero only when some row shows `source=none`, with:

```
at least one credential resolved to source=none in the last apply (no entry in ~/.config/niwa/provider-auth.toml, no entry in the personal vault, and no usable CLI session). Populate the missing credential then re-run `niwa apply`.
```

Two reasons it cannot diagnose the failure under investigation:

1. **The instance is deleted on the failure path.** `Applier.Create` at
   `internal/workspace/apply.go:457-459` does `_ = os.RemoveAll(instanceRoot)`
   before returning the error. After a failed create there is no instance, so
   `DiscoverInstance` fails and `--audit-auth` refuses to run.
2. Even with an instance, it reports **credential-pool** decisions
   (which machine identity authenticated which vault provider), not which
   declared env keys went unsupplied. It is the wrong axis for this failure.

`niwa status --audit-secrets` is reachable without an instance
(`loadShadowsForAudit` degrades to nil, `status_audit.go:92-99`) but is equally
useless here: `collectAuditEntries` (`status_audit.go:144-157`) walks only
`.Values` maps, never `.Required`. On the tsukumogami config it prints
`No *.secrets entries found.` and exits 0 — verified by running it.

### 8. Credential-pool audit log and the R12 stderr surface

`AuditLog()` at `internal/workspace/credentialpool.go:554`;
`EmitR12Lines` at `:585`, called from `internal/workspace/apply.go:1214`.
Emission rules are deliberately quiet: only `SourceVault` rows and
`SourceLocalFile`-with-fallback rows print. `cli-session` and plain
`local-file` rows print nothing; `SourceNone` prints nothing "because apply will
fail at the backend auth call, which already produces its own diagnostic"
(`credentialpool.go:571-573`).

Emission happens at `apply.go:1214`, which is **before** `checkRequiredKeys` at
`:1325`, so on a required-key failure the user does see whatever R12 lines fire.
In the no-Infisical OSS case that is nothing at all: no credential-sync
provider, no vault, empty audit trail, and `EmitR12Lines` returns early on
`len(a) == 0`.

### 9. The R13.1 aggregated vault-unreachable warning

Emitted at `internal/workspace/apply.go:1191-1204`, immediately after the three
`injectProviderTokens` calls:

```go
a.Reporter.Warn(
    "personal vault provider %s unreachable; falling back to local-file and cli-session credentials.",
    nameForWarning,
)
```

Renders as `warning: personal vault provider (anonymous) unreachable; falling
back to local-file and cli-session credentials.` for the anonymous provider
shape. Deduplicated per provider name.

Ordering: this is at line 1191, the required check at 1325 — so **the warning
does precede the hard failure**, and it uses `Reporter.Warn` (immediate write,
`reporter.go:146`) rather than `Defer`, so it survives an error return. Good.

But its trigger is narrow. It fires only from
`CredentialPool.VaultUnreachableObservations()`, populated inside
`lookupVault` when a **credential-sync** vault (the anonymous
`[global.vault.provider]` in the personal overlay) is unreachable. It says
nothing about a team vault, an unreachable Infisical for ordinary
`vault://` resolution, or a skipped workspace overlay. For an OSS contributor
with no personal overlay at all, it never fires.

Worth noting the softening asymmetry: `providerauth.go:206-215` `isSoftenable`
treats vault-unreachable as recoverable *for credential injection* (continue
iterating, fall through to CLI session), but `resolveOne` treats the same
sentinel as fatal *for URI resolution*. The machinery to soften already exists
and is already deemed acceptable on one path.

### 10. Deferred warnings are lost on the failure path

`Reporter.Defer` / `DeferWarn` (`reporter.go:153-163`) buffer until
`FlushDeferred`, which is called only at `apply.go:531` and `apply.go:724` —
both **after** `runPipeline` returns successfully. Shadow-detection lines
(`apply.go:1290`) and overlay-advance warnings (`apply.go:962`) use `Defer`, so
on a failed create they are silently dropped. Anything intended to be visible
at the moment of a secrets failure must use `Warn`, not `DeferWarn`.

### 11. `.env.example` policy machinery: a complete per-key severity ladder already exists

`internal/config/env_example_policy.go` implements exactly the model a
missing-secret severity ladder would want:

- `Action` type with `ActionWarn` / `ActionFail`, TOML-unmarshalled with
  validation (`"invalid env_example_policy action %q (want \"warn\" or
  \"fail\")"`).
- `EnvExamplePolicy{VendorToken, Entropy *Action; Vars map[string]Action}` —
  per-category *and* per-key.
- `EffectiveEnvExamplePolicy` (line 120) resolves a four-level precedence
  chain: per-repo vars → workspace vars → inline per-key annotation → per-repo
  category → workspace category → **global/personal category** → default warn.
- Nil pointers mean inherit, so operators can set policy at any rung.
- A one-shot bypass flag downgrades every remaining `fail` to `warn` with a
  per-key audit line — `internal/workspace/env_example_prepass.go:118-120`:
  ```go
  if ctx.AllowPlaintextSecrets && action == config.ActionFail {
      fmt.Fprintf(e.stderr(), "audit: .env.example in %s: --allow-plaintext-secrets downgraded fail to warn for key %s (category %s)\n", ctx.RepoName, key, category)
      action = config.ActionWarn
  }
  ```

Critically, the **global/personal rung** already exists in this ladder
(`globalPolicy.categoryAction`, threaded from `apply.go:1308` through to the
materialize context at `:1576`). That is precisely the missing piece on the
required-keys side: `*.required` / `*.recommended` / `*.optional` **is** a
three-level severity ladder, but it is written once by the team config with no
operator-side rung and no downgrade flag. The reuse story is strong; the
existing precedence resolver and the one-shot-with-audit-line bypass are both
directly transferable.

### 12. Documentation: an OSS contributor will not find any of this

`README.md` (243 lines) contains **zero** occurrences of "secret", "vault",
"Infisical", or "required". The Quick start (§19-113) walks init → configure →
content → create → apply with no mention that a shared config can declare
secrets. `--allow-missing-secrets` appears nowhere in the README, including in
the Commands table (§114-140).

The "Shared workspace configs" section (§141-183) is the exact OSS-contributor
entry point, and §183 makes a promise the current behavior breaks:

> Users without overlay access see none of these additions. Their workspace is
> identical to one set up from the base config alone.

That is true for content files and false for env: without overlay access,
create fails outright.

`docs/guides/vault-integration.md` documents everything correctly but is not
linked from the README and is framed as a vault-feature guide, not
troubleshooting. Line 174:

> | `*.required` | Hard error; `niwa apply` fails. Error names the key, the scope (e.g. `env.secrets`), and the description string. |

Line 178-180:

> `--allow-missing-secrets` downgrades vault misses to empty strings
> but does NOT downgrade `*.required` misses. A required key remains
> a hard error even with the flag set.

Correct and clear — but a contributor hitting the error has no breadcrumb
pointing here, and the error text names no doc. There is no troubleshooting
guide, no onboarding/quickstart mention of secrets, and no
`CONTRIBUTING.md` at the repo root.

### 13. Test coverage

`test/functional/features/critical-path.feature:157-190` exercises the
required-key path only in its **satisfied** form (personal overlay supplies
`MY_KEY`); the comment at :163 references the failure string as historical
context for a regression. No functional scenario covers no-overlay / no-vault /
no-Infisical, and none covers the interaction with `--allow-missing-secrets`.
Unit coverage of R34 is `TestApplyAllowMissingSecretsDoesNotDowngradeRequired`
(`internal/workspace/apply_vault_test.go:745`), which locks in the current
behavior — any change here has to amend that test and the acceptance-coverage
table at `docs/guides/vault-integration-acceptance-coverage.md:41`.

## Implications

**The fix is roughly 70% plumbing/discoverability and 30% new mechanism, and
the new-mechanism part is small and has a template.**

Plumbing and discoverability, no design work needed:

1. Warn instead of silently skipping the inaccessible overlay
   (`apply.go:992`). Single line, closes the biggest cause/effect gap.
2. Extend the required-key error with the two facts it is missing: what the
   personal overlay actually is (a registered repo at
   `~/.config/niwa/global/niwa.toml`, set with `niwa config set global`), and
   the escape hatch once one exists.
3. Register the escape hatch on `reset`, `dispatch`, and — most importantly —
   give `instance from-hook` a way to inherit it, since that path provisions
   unattended and currently has no knob at all.
4. README: mention secrets in the Shared-workspace-configs section, link
   `docs/guides/vault-integration.md`, and either qualify or fix the
   "identical to one set up from the base config alone" promise.
5. A functional scenario for the no-overlay contributor path.

Genuinely new mechanism, but narrow:

6. **Something must downgrade a required miss**, because by construction
   nothing does today and PRD R34 says nothing should. That is a deliberate
   product decision to revisit, not a bug to patch. The cleanest shape given
   what already exists: an operator-side rung in the requirement ladder,
   mirroring `EffectiveEnvExamplePolicy`'s global rung, plus a one-shot flag
   that downgrades `required` → `recommended` for the run and prints a per-key
   audit line (verbatim the `--allow-plaintext-secrets` pattern at
   `env_example_prepass.go:118`). Reusing that vocabulary keeps the two
   "I know what I'm doing" flags consistent.
7. **`ErrProviderUnreachable` needs a softening path in `resolveOne`.** This is
   separate from R34 and currently undocumented. At minimum `?required=false`
   should survive an unreachable provider — a ref the config itself marks
   optional failing hard because a CLI is missing is hard to defend. The
   softening precedent already exists on the credential-injection side
   (`isSoftenable`, `providerauth.go:212`).

The maintainer's stated goal ("creating and operating the workspace must
succeed with no Infisical installed and no valid credential; a vault login
failure should be loud, but not fatal") is achievable, but it requires
overturning PRD R34 as written, not just wiring an existing flag to more
commands. That is a PRD amendment plus a `docs/guides/vault-integration.md`
update plus amending
`TestApplyAllowMissingSecretsDoesNotDowngradeRequired` — worth naming
explicitly so it does not get discovered mid-implementation.

## Surprises

1. **`niwa init` does not run the required-key check at all.** The scope brief
   assumes `niwa init ... --from` aborts on `checkRequiredKeys` and that the
   missing flag registration on `init` is the gap. Neither holds: init never
   builds an Applier on the non-bootstrap clone path. The abort is at `create`
   or at the SessionStart hook. Adding the flag to `init` would be a no-op.

2. **`--allow-missing-secrets` does not cover the no-Infisical case even for
   optional refs.** It only handles `ErrKeyNotFound`. A missing CLI binary
   produces `ErrProviderUnreachable`, which bypasses both the flag and
   `?required=false`. The flag's name badly oversells its reach, and no doc
   states this limit.

3. **The overlay skip is completely silent** while every required-key
   description in the config says "resolved by overlay vault". The two halves
   of the diagnosis are one `Reporter.Warn` apart and neither references the
   other.

4. **A failed create deletes the instance**, which disables `--audit-auth`
   exactly when a user would reach for it. The audit surfaces are built for
   post-mortem on a *successful* apply, not for triaging a failed one.

5. **README already promises the behavior the maintainer wants** ("Users
   without overlay access ... identical to one set up from the base config
   alone", README:183). This is a documented-contract violation, not just a UX
   preference — which strengthens the case for changing R34 rather than
   documenting around it.

6. **`--audit-secrets` cannot see requirement tables at all.** It walks
   `.Values` only. On the tsukumogami public config it reports
   `No *.secrets entries found.` and exits 0 while `create` fails on five
   required keys from that same config.

7. **`session_lifecycle_cmd.go:352` calls `ResolveAndMergeEffectiveConfig`
   without `checkRequiredKeys`** — an existing acknowledged drift. Whatever
   shape the fix takes should not deepen that fork.

## Open Questions

1. Which command does the maintainer actually see fail? `init` cannot fail this
   way. Confirming `create` vs. `instance from-hook` determines whether the fix
   needs a flag, a config key, or both — a hook has no CLI surface to pass a
   flag through.
2. Should the downgrade be operator-side (personal overlay / global config,
   persistent, per-key) or invocation-side (one-shot flag), or both? The
   `.env.example` ladder does both; the required ladder does neither.
3. Should `?required=false` survive `ErrProviderUnreachable`? Answering yes is
   a small, defensible change independent of the R34 question and could ship
   separately.
4. Is R34's rationale ("lets users use `--allow-missing-secrets` for
   exploratory runs without accidentally bypassing team-declared requirements",
   PRD:882-885) still the right trade when the team is an OSS project and the
   "requirement" is a developer convenience? An explicit second flag preserves
   the no-accidental-bypass property while unblocking the contributor.
5. Should `tsukumogami/dot-niwa`'s own declarations change — e.g. the three
   `[repos.niwa.env.secrets.required]` INFISICAL_* keys are integration-test
   credentials and look like `recommended` at most? A config fix would unblock
   contributors immediately without any code change, and is worth costing
   alongside the code fix. (Note also: PRD:868-870 says per-repo requirement
   tables are "NOT supported in v1", yet `checkRequiredKeys` at
   `required.go:63-70` implements them and dot-niwa uses them. The PRD is stale
   here.)
6. Does `MaterializeWorkspaceRoot` (init's last step) touch env at all? I found
   no env resolution in `root_materializer.go`, but did not exhaustively trace
   the settings/hooks materializers it drives.

## Summary

The existing surfaces are real but all aimed at the wrong axis: `--allow-missing-secrets` only downgrades key-not-found (so it helps neither a missing Infisical CLI, which raises `ErrProviderUnreachable` past both the flag and `?required=false`, nor a bare `*.required` miss, which the resolver never sees), `--audit-auth` needs an instance that `Create` deletes on the failure path, `--audit-secrets` reads only `.Values` and never requirement tables, and the R13.1 warning fires only for an unreachable *credential-sync* vault an OSS contributor doesn't have — meanwhile the inaccessible overlay that was supposed to supply every one of those keys is skipped with zero output at `apply.go:992`. About 70% of the fix is plumbing and wording (warn on overlay skip, name the personal overlay concretely in the error, wire the flag into `reset`/`dispatch`/`from-hook`, put secrets in the README where it already promises overlay-less parity), but the remaining 30% is a real product decision: nothing today can downgrade a required miss, by PRD R34's explicit design, so the maintainer's goal requires amending R34, `docs/guides/vault-integration.md:178`, and `TestApplyAllowMissingSecretsDoesNotDowngradeRequired` — with the `.env.example` failure-policy ladder (`internal/config/env_example_policy.go`, four-level precedence including a global rung, plus a one-shot downgrade-with-audit-line flag) sitting right there as the template. The biggest open question is which command actually aborts: `niwa init` never runs `checkRequiredKeys` on the non-bootstrap clone path, so the reported symptom must come from `create` or the SessionStart hook, and that distinction decides whether a flag is even a usable delivery mechanism.
