# Decision 5 — Flag deprecation, strict-flag registration, and precedence

Serves R16, R17, R12 (per-invocation half), R11.
Upstream: `docs/prds/PRD-oss-no-infisical.md`.

## Context

### What `--allow-missing-secrets` is today

Registered on exactly two commands, with byte-identical help text:

- `internal/cli/apply.go:24-26` → `applyAllowMissingSecrets` (`apply.go:47`), copied
  onto the applier at `apply.go:166`.
- `internal/cli/create.go:22-24` → `createAllowMissingSecrets` (`create.go:40`),
  copied at `create.go:218`.

Verbatim help text on both:

> `downgrade unresolved vault:// references to empty strings with stderr warnings. Does NOT override *.required misses. One-shot -- re-evaluated each invocation.`

It is absent from `niwa init` (flag set at `internal/cli/init.go:43-56`), from
`niwa reset` (which builds an applier at `reset.go:97` and never sets the field),
and from the unattended provisioner `realProvisionInstance`
(`internal/cli/instance_from_hook.go:365-417`, serving the SessionStart hook,
`niwa dispatch`, and `niwa watch`) which also never sets it.

The field threads: `Applier.AllowMissingSecrets` (`internal/workspace/apply.go:55-60`)
→ the overlay resolve at `apply.go:1075` and `EffectiveConfigOptions.AllowMissingSecrets`
at `apply.go:1311` → `internal/workspace/effective_config.go:29,75,93` →
`resolve.ResolveOptions.AllowMissing` (`internal/vault/resolve/resolve.go:37-40`).

`AllowMissing` is consulted in exactly one place: `resolve.go:531-536`, inside the
`errors.Is(resolveErr, vault.ErrKeyNotFound)` branch. The provider-unreachable
branch at `resolve.go:550-555` never consults it and hard-fails unconditionally.
So the flag covers one of the two first-run walls and neither of the two the PRD's
personas actually hit.

Three statements in the tree claim behaviour the code does not have (R17 targets):

- `internal/vault/errors.go:14-15`: "`--allow-missing-secrets` consults this
  sentinel to decide whether to downgrade" — on `ErrProviderUnreachable`. It does not.
- `internal/vault/infisical/subprocess.go:249` and
  `internal/vault/infisical/infisical_test.go:648`: both describe a misclassified
  transient error as something "`--allow-missing-secrets` silently downgrades",
  which is only true via the `ErrKeyNotFound` path they are not on.
- `docs/guides/vault-integration.md:178-180` and the flag table at `:576`, plus the
  in-error remediation string at `resolve.go:542` ("or re-run with
  `--allow-missing-secrets` to downgrade") and the warning at `resolve.go:533`.

Further descriptive sites that must stop claiming the removed behaviour:
`resolve.go:37-39`, `resolve.go:203-205`, `resolve.go:460`,
`internal/workspace/apply.go:55-60` and `apply.go:1320-1324`,
`internal/workspace/effective_config.go:14-20`,
`internal/workspace/required.go:39-46, 108-110, 130-134`,
`docs/guides/vault-integration-acceptance-coverage.md:41,47`.
`README.md` mentions the flag nowhere — verified by grep.

### The promotion failure (R11)

`internal/workspace/materialize.go:958-964`:

```go
for _, key := range claudeEnv.Promote {
        val, found := resolvedEnv[key]
        if !found {
                return nil, nil, fmt.Errorf("claude.env: promoted key %q not found in resolved env vars", key)
        }
        envResult[key] = val
}
```

`resolvedEnv` comes from `ResolveEnvVars` (`materialize.go:1140-1235`), which copies
**every key** of `envCfg.Vars.Values` / `envCfg.Secrets.Values` into the map via
`maybeSecretString` (`materialize.go:1199-1208`). The resolver's downgrade writes the
zero `MaybeSecret` back into the same map slot — `walkTable` at `resolve.go:388-397`
does `values[key] = resolved` and never deletes — so a downgraded key is *present with
an empty string* and `found` is true. That is precisely why the check never fires today,
and precisely why R2's shift from blank-to-omitted will start firing it: the key
disappears from `Values`, disappears from `vars`, and `found` goes false.

The error aborts `SettingsMaterializer.Materialize` (`materialize.go:1011-1014`) and
therefore the whole apply. `TestApplyAllowMissingSecretsDoesNotDowngradeRequired`
never reaches it because `checkRequiredKeys` fires first, but a *recommended* or
*optional* promoted key reaches it directly.

There is a second, less obvious instance of the same check. On the worktree path
`ctx.InheritedEnv` is non-nil (`materialize.go:132-141`) and the promoted key is looked
up in the map returned by `readCloneEnvOutput`
(`internal/workspace/worktree_content.go:334-367`), which `parseEnvFile`s the clone's
already-materialized `.local.env`. Under R2 that file no longer contains the key, and
`parseEnvFile` (`materialize.go:1438-1457`) skips `#`-prefixed lines, so an R3 record
written as a comment is invisible to it. Left alone, worktree re-materialization of an
instance with an unresolved secret would fail — directly contradicting the acceptance
criterion "Worktree re-materialization of an instance whose secret is unresolvable
succeeds with strict mode set" and R21's tolerance. `TestApplyToWorktreePromoteMissingFromCloneErrors`
(`internal/workspace/worktree_promote_inherit_test.go:59-79`) pins today's failure.

### Precedent already in the tree

- **Mutually exclusive flags**: `internal/cli/init.go:442-459`. Two hand-rolled checks
  in `runInit`, one plain (`--overlay` / `--no-overlay`, `fmt.Errorf`, exit 1), one
  typed (`--bootstrap` / `--no-bootstrap` → `workspace.InitConflictError{ExitCode: 2}`,
  rendered by `Execute` at `internal/cli/root.go:90-94`).
- **Deprecation with a warning**: `internal/cli/apply.go:160-165` prints
  `warning: --allow-dirty is no longer meaningful under the snapshot model and will be
  removed in v1.1` from inside `runApply` whenever the flag is set. A no-op flag that
  warns is already niwa's house style.
- **Flag-over-config precedence**: `--agent` (`apply.go:189-197` →
  `resolveSessionAgent`, flag > `NIWA_AGENT` > `default_agent` > claude) and
  `--parallel` (`apply.go:158`, `create.go:239-241`: flag when > 0, else
  `clone_workers`, else built-in). Both resolve "flag wins when the user supplied one".

### Verified library behaviour (cobra v1.10.2 / pflag v1.0.9)

Empirically confirmed with a scratch binary, not asserted from memory:

- `Flags().MarkDeprecated(name, msg)` sets `flag.Deprecated = msg` **and**
  `flag.Hidden = true` (`pflag@v1.0.9/flag.go:432-445`). The flag then vanishes from
  `--help` entirely.
- On use, pflag prints `Flag --old has been deprecated, <msg>` **to stderr**, once,
  only when the flag is passed (`flag.go:510-512`). Confirmed reaching the terminal
  under cobra 1.10.2 despite the `flagErrorBuf` wiring at `command.go:1691-1694`.
- Setting `Flags().Lookup(name).Hidden = false` after `MarkDeprecated` restores it to
  `--help` rendered as `--old   <usage> (DEPRECATED: <msg>)` (`flag.go:759-761`), while
  keeping the runtime notice. Confirmed.
- `MarkFlagsMutuallyExclusive` (`cobra/flag_groups.go:65`) fires on *presence*, not on
  value, and its message is generic: `if any flags in the group [a b] are set none of
  the others can be; [a b] were all set`. Validation runs before `RunE` via the
  exported `ValidateFlagGroups` (`flag_groups.go:81`), so a test can drive it directly
  after `ParseFlags`. With niwa's `SilenceUsage: true` / `SilenceErrors: true`
  (`root.go:42-43`) it surfaces as one stderr line, exit 1, no usage banner.

## Options for retiring `--allow-missing-secrets`

### Option A — accept and ignore silently

Delete `applier.AllowMissingSecrets = ...` at `apply.go:166` and `create.go:218`,
reword both usage strings, keep the flag bound to a package var nothing reads.

*Existing scripts*: unchanged behaviour on the happy path, and on the shortfall path
they now succeed where they used to fail — which is the whole point of R1. Nothing
breaks, nothing prints.

*Cost*: the flag becomes undetectable rubbish. A CI job passing it in a workspace that
later turns on strict mode gets a failure whose obvious suspect (the flag that says
"allow missing") is silent about being inert. Nobody reads usage strings for a flag
they already "know". There is also no forcing function to ever delete it: the tree keeps
a dead flag with no migration signal, contradicting the `--allow-dirty` precedent that
niwa deprecates loudly and names a removal release.

*Verdict*: satisfies the letter of R16 and is the cheapest, but it is the only option
that leaves the user with no way to learn the flag stopped meaning anything.

### Option B — accept with a deprecation notice on use (recommended)

Same removal of the two assignments, plus `MarkDeprecated` + `Hidden = false` on each
registration, so:

- `niwa apply --help` lists it as
  `--allow-missing-secrets   deprecated: accepted and ignored; unresolved secrets are tolerated by default (DEPRECATED: this flag no longer changes any outcome; see --strict-secrets)`
- passing it prints one stderr line and changes nothing else.

*Existing scripts*: exit codes and generated files identical; one extra stderr line for
invocations that pass the flag. This is compatible with R19 as the PRD operationalizes
it — both governing acceptance criteria compare **generated files and exit codes**,
not stderr ("`--allow-missing-secrets` changes no outcome: for both an absent-key
shortfall and an unreachable-provider shortfall, the exit code and generated files are
identical with and without the flag"; "A workspace whose secrets all resolve produces
byte-identical generated files and identical exit codes"). A CI job that diffs stderr
byte-for-byte and passes the flag would see the new line; that is the intended signal.

*Cost*: one line of noise per invocation, and a small amount of care needed because
pflag's `MarkDeprecated` hides the flag by default (the `Hidden = false` line is
load-bearing for the "help text identifies the flag as deprecated" criterion).

*Verdict*: recommended. It is the only option that reaches the person who wrote the
script, and it costs one line of registration per command.

### Option C — remove entirely with a helpful error

Unregister the flag; cobra then rejects `--allow-missing-secrets` with `unknown flag`,
exit 1. To make that message helpful you must keep something registered anyway (a hidden
flag whose `RunE` errors, or a pre-parse scan of `os.Args`), at which point you have
re-implemented the flag in order to reject it.

*Existing scripts*: every invocation that passes it now fails outright — including the
maintainer-with-a-fresh-host and CI cases this work exists to unblock, and including
invocations against fully-resolving workspaces where the flag was pure superstition.
The failure lands *before* any work, so the remedy is always "edit the script", never
"ignore it".

*Cost/verdict*: rejected, as the PRD already decided ("accepting it as a documented
no-op costs one line and breaks nothing"). Worth recording that C is also the only
option where the deprecation window is zero, which is out of step with `--allow-dirty`
warning for a full minor release before removal.

## Recommendation

### 1. Flag surface

Add a shared registrar in `internal/cli` (one helper, three call sites) so the three
commands cannot drift:

- `niwa apply` and `niwa create`: register `--strict-secrets` **and** keep
  `--allow-missing-secrets` as a deprecated, visible no-op; declare the pair mutually
  exclusive via `cmd.MarkFlagsMutuallyExclusive("allow-missing-secrets", "strict-secrets")`.
- `niwa init`: register `--strict-secrets` only. `--allow-missing-secrets` is not
  registered there today and R16 only requires it to *continue* being accepted where it
  already is; adding a deprecated flag to a new surface would be perverse, and with only
  one flag present there is no contradiction to reject.

Name: `--strict-secrets`, mirroring decision 2's configuration key (`strict_secrets`)
with the dash spelling niwa uses everywhere else. Not bare `--strict` (niwa has other
strictness axes and the PRD's "one strict mode" is specifically about secrets), and not
`--fail-on-missing-secrets` (long, and reads as covering only one of the four
enforcement points).

Use cobra's built-in group check rather than a hand-rolled `runX` guard: it is one line
per command versus three duplicated blocks, it runs before `RunE` on all three
surfaces uniformly, and it is directly testable through the exported
`ValidateFlagGroups`. Its generic message does name both flags, which is what the
acceptance criterion requires ("exits non-zero with a message naming the contradiction").
If the reviewers want the `init.go:453-459` bespoke-message treatment, that is a
localized upgrade, not a different design.

Rejection is on **co-occurrence**, not on resolved value — so
`--allow-missing-secrets --strict-secrets=false` is also rejected. That is R16 read
literally ("passing it together with the strict-mode flag"), it is what cobra gives for
free, and it is trivial to explain. The semantic alternative (reject only when strict
resolves true) requires a hand-rolled check and asks the user to reason about the
resolved value before they can predict whether their command is legal.

### 2. Internal plumbing

Replace the tolerance field with a strictness field, rather than inverting a bool in
place:

- Delete `Applier.AllowMissingSecrets` (`apply.go:55-60`), its two reads
  (`apply.go:1075`, `apply.go:1311`), `EffectiveConfigOptions.AllowMissingSecrets`
  (`effective_config.go:29,75,93`), and `resolve.ResolveOptions.AllowMissing`
  (`resolve.go:37-40`). Under R1 the resolver is unconditionally tolerant, so the field
  has no remaining reader — decision 1 owns what the resolver returns instead.
- Add `Applier.StrictSecrets bool`, consulted **once**, after resolution, at the
  enforcement gate that replaces `checkRequiredKeys` (`apply.go:1325-1327`). Strictness
  becomes a property of the enforcement step, not of the resolver walk, which is what
  lets one setting cover all four enforcement points (R12) without four knobs.
- The worktree path then satisfies R21 structurally: it simply never sets
  `StrictSecrets`, replacing today's deliberate `AllowMissingSecrets: true` hack
  (`effective_config.go:17-20`) with the absence of an opt-in.

### 3. Precedence

The arbiter is `cmd.Flags().Changed("strict-secrets")`, not the bool's value. This makes
the flag genuinely tri-state (`unset` / `=true` / `=false`) and matches how `--agent`
and `--parallel` already resolve against config.

| configuration setting | `--strict-secrets` | `--allow-missing-secrets` | outcome |
|---|---|---|---|
| absent | not passed | not passed | tolerant (R1 default) |
| absent | not passed | passed | tolerant — flag inert, one deprecation line |
| `true` | not passed | not passed | **strict** |
| `true` | not passed | passed | **strict** — the deprecated flag cannot soften it |
| `false` | not passed | not passed | tolerant |
| absent | `--strict-secrets` | not passed | **strict** |
| absent | `--strict-secrets=false` | not passed | tolerant |
| `true` | `--strict-secrets` | not passed | **strict** |
| `true` | `--strict-secrets=false` | not passed | tolerant — flag wins |
| `false` | `--strict-secrets` | not passed | **strict** — flag wins |
| any | `--strict-secrets` (any value) | passed | **rejected**, exit non-zero, neither applies |
| any (worktree re-materialization) | any | any | tolerant — R21, path never consults either |

**The rule**: strictness is the configuration setting's value, overridden by the
per-invocation flag whenever the flag was explicitly present on the command line, and
tolerant when neither speaks; `--allow-missing-secrets` never participates, and its
presence alongside the strict flag rejects the invocation instead of resolving it.

Why flag-wins rather than OR-semantics ("either input can turn strictness on, nothing
can turn it off"): OR is defensible only if a lower rung needs protection from a higher
one, and R13 already supplies that protection at the layer where it actually matters —
a visibility overlay cannot set strict mode at all, and decision 2 rejects and reports
the attempt before this resolution runs. Everything that survives to here is a setting
the operator can read in their own workspace or global config. Weakening it for one
invocation on their own machine produces a materialized instance plus an R6 report, not
a security event. Against that, OR would make `--strict-secrets=false` a flag that
parses, is documented, and silently does nothing whenever the config says `true`, which
is the exact class of lie R17 exists to eliminate. Flag-wins also keeps one precedence
story across `--agent`, `--parallel`, and this.

One user-visible consequence to document: a script currently spelled
`niwa apply --allow-missing-secrets`, run in a workspace whose config sets strict mode,
now fails. The remedy is `--strict-secrets=false`, not the deprecated flag — and the
deprecation notice is what points there. This is the one migration case Option A would
leave undiagnosable.

### 4. Where each command resolves it

- `apply`: alongside the other applier wiring at `internal/cli/apply.go:151-167`, after
  `ReconcileAndReloadConfig` (`apply.go:177-181`) so the setting read is the
  post-reconcile config, matching how `resolveSessionAgent` is sequenced at
  `apply.go:193-197`.
- `create`: same position, after the reconcile at `create.go:172-180` and next to the
  existing `applier.AllowPlaintextSecrets` assignment at `create.go:219`.
- `init`: the flag cannot be resolved against config at parse time — init clones the
  config it would read. Resolution therefore belongs inside `createWrapper`
  (`internal/cli/init.go:185-198`), which already does `config.Load` on the freshly
  scaffolded `workspace.toml` before calling `applier.Create`; read the package-level
  `initStrictSecrets` there, exactly as `handleNoMarkerR13` reads `initBootstrap`
  directly. On init's non-bootstrap clone path no instance is materialized, so the flag
  has nothing to enforce; say so in the help text rather than pretending otherwise.
- Unattended paths (`realProvisionInstance` at `instance_from_hook.go:351-422`, and
  `reset.go:97-115`) take no flag and read the setting only. `reset` is not in R12's
  list but builds an applier through the same `runPipeline`; wiring it to the same
  config read costs one line and avoids a surface where strict mode silently does not
  apply.

### 5. Promotion (R11)

Rewrite `materialize.go:958-964` into a three-way branch:

1. key present in `resolvedEnv` → promote it (unchanged);
2. key absent **and** carried in the unresolved-key set → omit it from the generated
   settings and add it to the R6 report; continue;
3. key absent **and** not in that set → keep today's hard error verbatim.

Case 3 is load-bearing and the PRD names it: "The test uses a declared-but-unresolved
key, not an undeclared one." A promoted key that is simply a typo must still fail, which
is why the discriminator has to be the unresolved set and cannot be "is it in
`ctx.Effective`'s tables" — under R2 the key is gone from `Values`, and requirement
sub-tables would only cover keys someone bothered to declare a level for.

Plumbing: one new field on `MaterializeContext` (alongside `EnvExampleVars` at
`materialize.go:111-118`) carrying decision 1's unresolved-key records, populated by the
applier from what `ResolveAndMergeEffectiveConfig` returns. `resolveClaudeEnvVars`
(`materialize.go:925`) reads it. No other materializer changes.

Worktree half: `readCloneEnvOutput` (`worktree_content.go:334-367`) must return the
omitted-key records recovered from the clone's generated env file in addition to the
key/value map, so the worktree path can populate the same set and take branch 2. This is
a hard dependency on decision 3 delivering a dotenv record that niwa's own reader can
recover (R3, and the acceptance criterion "niwa's reader of generated environment files
recovers the key name and declared description of every omitted key"). `parseEnvFile`
(`materialize.go:1438-1457`) drops `#` lines and `=`-less lines, so recovery needs a
deliberate second return value, not an accident of the existing parse. If decision 3 lands
a format the reader cannot recover, the fallback is to tolerate *any* absent promoted key
on the worktree path — which loses typo detection there and should be recorded as a
regression rather than chosen silently.

## Rejected alternatives

- **Widen `--allow-missing-secrets` to also downgrade `ErrProviderUnreachable`.** The
  obvious "fix" for the false doc comment at `errors.go:14`. Rejected upstream by R16
  and correctly: it would give two spellings for the default, and to stay meaningful it
  would have to override strict-when-reachable (R10), the one guarantee worth keeping.
- **Remove the flag outright** (Option C above) — breaks the scripts of the exact
  persona this work serves, with no deprecation window.
- **Invert the existing field in place** (`AllowMissingSecrets` → `!StrictSecrets`,
  same plumbing). Cheaper diff, but it keeps strictness threaded into the *resolver*,
  where it can only ever reach the one enforcement point `AllowMissing` sits at
  (`resolve.go:531`). R12 requires all four. Inverting also silently flips the meaning
  of the worktree path's deliberate `true` at `effective_config.go:17-20`.
- **`--no-strict-secrets` as a separate flag** (the `--bootstrap` / `--no-bootstrap`
  shape at `init.go:53-54`). Rejected: pflag bools already support `--flag=false`, and a
  second flag would need its own mutual-exclusion rule against the first, plus a third
  rule against `--allow-missing-secrets`. Three pairwise rules to express one tri-state.
- **OR-semantics precedence** (strictness only ever accumulates) — see rationale above.
- **Hand-rolled contradiction check per command** instead of
  `MarkFlagsMutuallyExclusive` — three duplicated blocks for a better message; keep it
  in reserve if reviewers dislike cobra's wording.
- **Deriving "was this key declared?" from `ctx.Effective`** instead of an explicit
  unresolved set — cannot distinguish an unresolved declared key from a typo once R2
  removes the entry, and carries no reason code for R6.

## Test inventory that must change

**Must be rewritten (behaviour they pin is being removed):**

| Test | Location | Why |
|---|---|---|
| `TestApplyCmd_HasAllowMissingSecretsFlag` | `internal/cli/apply_test.go:103-117` | Comment claims the flag plumbs into `Applier.AllowMissingSecrets`; that field is gone. Becomes: registered, default false, `Deprecated != ""`, `Hidden == false`, usage text carries "deprecated". |
| `TestCreateCmd_HasAllowMissingSecretsFlag` | `internal/cli/create_test.go:358-370` | Same, create side. |
| `TestApplyCmd_AllowFlagsThreadToApplier` | `internal/cli/apply_test.go:136-165` | Asserts the parse populates `applyAllowMissingSecrets`; the var survives but no longer threads anywhere. Split: keep the plaintext half, replace the missing-secrets half with a strict-flag threading assertion. |
| `TestCreateCmd_AllowFlagsThreadToApplier` | `internal/cli/create_test.go:386-412` | Same, create side. |
| `TestResolveWorkspaceAllowMissingDowngradesWithWarning` | `internal/vault/resolve/resolve_test.go:222-260` | Sets `AllowMissing: true` and asserts the stderr warning contains `--allow-missing-secrets` (`:257`). Both the option and the warning string disappear. |
| (missing-key error-text test) | `internal/vault/resolve/resolve_test.go:263-292` | Asserts the error names `--allow-missing-secrets` and `?required=false` (`:292`). The remediation string at `resolve.go:542` must stop naming the flag (R17). |
| `TestResolveWorkspaceProviderUnreachable` | `internal/vault/resolve/resolve_test.go:650-673` | Sets `AllowMissing: true, // should not help` against a removed field; the underlying assertion (unreachable ≠ missing) is decision 1's to restate. |
| `TestResolveAndMergeEffectiveConfigAllowMissingDowngrades` | `internal/workspace/effective_config_test.go:138-180` | Exists solely to prove `AllowMissingSecrets` threads to both resolver walks. Delete or repoint at the unconditional-tolerance contract. |
| `TestApplyVaultAllowMissingSecretsDowngrades` | `internal/workspace/apply_vault_test.go:415-470` | Sets `applier.AllowMissingSecrets = true` (`:466`) and expects success. Success is now the default; the assignment must go. |
| `TestApplyAllowMissingSecretsDoesNotDowngradeRequired` | `internal/workspace/apply_vault_test.go:740-805` | Fixture is a *reachable* fake provider that does not hold `GITHUB_TOKEN`, so the expected outcome (fail, naming the key) survives verbatim under R10. Only `applier.AllowMissingSecrets = true` (`:797`) and the R34-era name/comment change. Worth keeping as the R10 regression test. |
| `TestApplyToWorktreePromoteMissingFromCloneErrors` | `internal/workspace/worktree_promote_inherit_test.go:59-79` | Must keep failing only when the clone env carries no omitted-key record; a new sibling must assert tolerance when the record is present. |
| (promote-missing unit test) | `internal/workspace/materialize_test.go:810-818` | Asserts `promoted key "MISSING_KEY" not found`. Stays as the undeclared-key case; needs a sibling for the declared-but-unresolved case. |
| `?required=false` silence test | `internal/workspace/apply_vault_test.go:477-547` | Asserts stderr does **not** contain `--allow-missing-secrets` (`:546`). Once nothing emits that token the assertion is vacuous; repoint it at R2a — the key must be absent from the R6 report and still written empty. |
| `TestTransientErrorDoesNotMapToUnreachable` | `internal/vault/infisical/infisical_test.go:643-655` | Test body is fine; its doc comment asserts removed behaviour (R17). |

**New tests required by the acceptance criteria:**

- `--allow-missing-secrets` changes no outcome: same exit code and same generated files
  with and without it, for both an absent-key and an unreachable-provider shortfall.
- The flag's registration is deprecated and visible: `Deprecated != ""`, `Hidden == false`.
- Contradiction rejection on `apply` and `create`: `ParseFlags` then
  `ValidateFlagGroups()` returns an error naming both flags. (Not on `init` — the
  deprecated flag is not registered there.)
- `--strict-secrets` registered on all three of `apply`, `create`, `init`, default false.
- Precedence matrix: at minimum the four contested rows (config true + no flag; config
  true + `=false`; config false + flag; neither).
- R17 scan test: walk the tree, collect every line mentioning `allow-missing-secrets`,
  and require each to match an approved-phrasing allowlist. Scope it to `**/*.go` plus
  `docs/guides/**` and `README.md`; exclude `docs/prds/**`, `docs/designs/**`, and
  `wip/**`, which are dated records of what was true when they were written — a scan
  that included them would force rewriting shipped PRDs to satisfy a code requirement.
  This test must fail against the tree as it stands (it does: `errors.go:14`,
  `resolve.go:37-39,203,533,542`, `required.go:39`, `apply.go:55-60`,
  `effective_config.go:14`, `subprocess.go:249`, `vault-integration.md:178,576`).
- R11: a declared-but-unresolved promoted key is omitted from `settings.local.json`,
  named in the report, exit 0; an undeclared promoted key still fails.

**Docs that must be edited alongside:** `docs/guides/vault-integration.md:178-180` and
the flag table at `:576`; `docs/guides/vault-integration-acceptance-coverage.md:41,47`
(both rows describe tests that are changing).

## Open risks

1. **`Flags().Changed` leaks across tests.** The existing CLI tests call `ParseFlags`
   on the shared package-level `applyCmd` / `createCmd` and restore only the bool vars
   (`apply_test.go:141-153`). Precedence keyed on `Changed` makes that insufficient —
   `Changed` stays true for the rest of the process and can flip a later test's
   precedence assertion. Every new test must reset
   `cmd.Flags().Lookup("strict-secrets").Changed = false` in `t.Cleanup`, or build a
   fresh command. This is a live footgun, not a hypothetical.
2. **"Help text identifies the flag as deprecated" has two readings.** If the reviewers
   read it as "the usage string contains the word", `MarkDeprecated` alone suffices and
   the flag is hidden from `--help`. If they read it as "`niwa apply --help` shows it as
   deprecated", the `Hidden = false` line is mandatory. The recommendation takes the
   second reading because a hidden flag cannot be "documented as deprecated" to anyone
   who did not already know it exists.
3. **Stderr is not covered by the no-outcome-change criteria.** The deprecation notice
   is permitted only because both governing criteria compare generated files and exit
   codes. A reviewer who reads R19's "no new output" literally would reject Option B and
   force Option A. Flag it for explicit sign-off rather than discovering it at jury.
4. **R11's worktree half depends on decision 3.** If the omitted-key record is not
   recoverable by niwa's dotenv reader, worktree re-materialization either breaks or
   must tolerate every absent promoted key, losing typo detection on that path.
5. **The unresolved-key set is a hard dependency on decision 1.** If decision 1 does not
   carry declared-key identity all the way to `MaterializeContext`, the promotion branch
   cannot distinguish an unresolved key from a typo and R11 collapses into "tolerate
   everything", which the PRD explicitly declined.
6. **`niwa reset` is unspecified.** It materializes through the same `runPipeline` but
   appears in neither R1's list nor R12's. The recommendation wires it to the config
   setting for consistency; if the design deliberately leaves it out, strict mode will
   have a surface where it silently does not apply.
7. **Flag-name churn risk.** `--strict-secrets` must match decision 2's setting key. If
   decision 2 names the setting something else, this flag renames with it — cheap now,
   expensive after release.
