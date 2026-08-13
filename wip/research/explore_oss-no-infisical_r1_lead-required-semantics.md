# Lead: What did `required` / `recommended` / `optional` mean by design, and why was `--allow-missing-secrets` blocked from downgrading a `required` miss?

Scope note: all paths below are relative to
`/Users/danielgazineu/dev/niwaw/tsuku/tsuku+tsuku_oss_no_infisical-26c0f110/public/niwa/.claude/worktrees/oss-no-infisical`.

## Findings

### 1. The three levels are a PRD contract requirement, not a design decision

The whole ladder enters the codebase in exactly one requirement,
`docs/prds/PRD-vault-integration.md:834` (R33), and one follow-up,
`:877` (R34). Verbatim, R33:

> - `[env.vars.required]` / `[env.secrets.required]` — keys that MUST
>   resolve to a non-empty value by apply time. Missing >=1 required
>   key is a hard error; `niwa apply` fails with a message listing
>   every missing key and its description.
> - `[env.vars.recommended]` / `[env.secrets.recommended]` — keys the
>   workspace expects but can operate without. Missing -> loud stderr
>   warning; `niwa apply` proceeds.
> - `[env.vars.optional]` / `[env.secrets.optional]` — keys that are
>   genuinely nice-to-have. Missing -> info log (visible only with
>   `--verbose`); `niwa apply` proceeds.

And R34 in full (`:877-890`):

> **R34. `*.required` tables have precedence over
> `--allow-missing-secrets`.** The `--allow-missing-secrets` flag (R10)
> downgrades unresolved `vault://` references to empty strings. It does
> NOT downgrade unresolved `[env.vars.required]` or
> `[env.secrets.required]` keys. A missing required key is always a hard
> error regardless of flags, because the team config explicitly marked
> the key as load-bearing. This separation lets users use
> `--allow-missing-secrets` for exploratory runs without accidentally
> bypassing team-declared requirements.

That last sentence is the *entire* recorded rationale. There is no
design-level treatment: `docs/designs/current/DESIGN-vault-integration.md`
mentions the sub-tables only in passing (`:57`, `:881`, `:1013`, `:1194`)
as a schema/classification item, and carries no decision, no options
table, and no trade-off analysis for the hard-fail. No ADR exists.
Everything shipped in a single commit, `5f2cdea` (PR #52,
"feat: vault-backed secrets with pluggable providers and publishable
team configs", merged 2026-04-17) — the only commit that has ever
touched `internal/workspace/required.go`. `internal/workspace/effective_config.go`
was created later by `5157c57` (PR #163) and does not touch the rule.

So the problem the hard fail was solving is narrow and explicitly
stated: keep `--allow-missing-secrets`, which exists for *exploratory
and CI* runs (R10, `:585`: "Intended for debug and CI fallback cases"),
from being a blanket "ignore everything the team said matters" switch.
The concern behind that is stated one requirement earlier, in R10:
silent identity pivot — "an empty `AWS_ACCESS_KEY_ID` falling through
to ambient credentials". R34 is the backstop that says: for the subset
of keys the team named load-bearing, degrading to empty is never
acceptable, because empty is not a safe value.

### 2. The implementation matches the PRD, and enforces it post-merge on purpose

`internal/workspace/required.go:47` `checkRequiredKeys` walks every
`*.required` map in the effective (post-merge, post-resolve) config,
collects keys whose `Values` entry is absent or an empty `MaybeSecret`
(`:111` `collectMissing`, `:135` `isEmptyMaybeSecret`), sorts, and
returns one aggregated error naming scope, key, and the team-authored
description, plus a remediation line (`:100`):

> declare each key under the matching table with a value, supply it via
> the personal overlay, or remove it from the required sub-table

It runs at exactly one call site, `internal/workspace/apply.go:1325`,
after `ResolveAndMergeEffectiveConfig` and *before* repo cloning
(Step 3 begins at `:1329`). A required miss therefore aborts the whole
pipeline before a single repo is cloned.

The post-merge placement is deliberate and documented at
`required.go:34-37`: the R33 example is "team lists GITHUB_TOKEN in
`[env.secrets.required]`, personal overlay supplies the value under
`[env.secrets]`". Which answers part of the lead question directly —
see §4.

Two implementation notes that diverge from the PRD, both benign:

- R33 says "Per-repo, per-instance, and `[files]`-scoped requirement
  tables are NOT supported in v1". `checkRequiredKeys` in fact scans
  `repos.<name>.env.*`, `repos.<name>.claude.env.*`,
  `instance.env.*`, and `instance.claude.env.*`
  (`required.go:62-79`). The implementation went wider than the
  contract. Additive, so nothing breaks, but any change to the rule has
  a bigger blast radius than the PRD implies.
- `optional` is entirely silent, not "info log under `--verbose`"
  (`required.go:142-145`: "Optional sub-tables are silent in v1 (no
  verbose flag yet)"). `docs/guides/vault-integration.md:176` documents
  the shipped behavior honestly.

### 3. The preliminary read is wrong about the failure path that actually bites

Preliminary framing was: "the resolver softens the unreachable vault
lookup to an empty value and the required check then fails on that
emptiness". That is **not** what happens for an unreachable provider.

`--allow-missing-secrets` only ever consults `vault.ErrKeyNotFound`.
In `internal/vault/resolve/resolve.go:470` `resolveOne`, the
`AllowMissing` branch sits at `:531`, inside the
`errors.Is(resolveErr, vault.ErrKeyNotFound)` block. The
`ErrProviderUnreachable` branch at `:550` returns a hard error with no
flag consultation at all. The test is explicit —
`internal/vault/resolve/resolve_test.go:648-680`,
`TestResolveWorkspaceProviderUnreachable`, whose comment reads
"regardless of AllowMissing -- AllowMissing targets ErrKeyNotFound
only" and whose fixture sets `AllowMissing: true, // should not help
-- unreachable != missing`.

A missing `infisical` binary maps to exactly that sentinel:
`internal/vault/infisical/subprocess.go:139` — "Process failed to start
(e.g., CLI not installed)" — wraps `vault.ErrProviderUnreachable`. And
it surfaces at *resolve* time, not bundle-build time, because
`infisical.Factory.Open` is non-blocking by design
(`internal/vault/infisical/infisical.go:87`: "Open is non-blocking: it
does NOT invoke `infisical`").

Consequence: for the maintainer-on-a-fresh-host persona, the wall is
**R9 (fail-hard resolution), not R34**. `checkRequiredKeys` is never
reached; apply dies inside the resolver. Every remediation the R9 error
prints (`resolve.go:542`, and R9 itself at PRD `:564-581`, which
mandates pointing at "US-9's three paths") is misleading here, because
one of those three paths — `--allow-missing-secrets` — provably does
not fix an unreachable provider. Note the doc comment at
`internal/vault/errors.go:14` ("`--allow-missing-secrets` consults this
sentinel to decide whether to downgrade") sits on
`ErrProviderUnreachable` and is simply false as written.

R34 *is* the real wall in one narrower case: the base config declares a
required key, the vault reference resolves to `ErrKeyNotFound` (or names
a provider not in the active bundle, `resolve.go:514`, which is
deliberately wrapped as `ErrKeyNotFound`), the user passes
`--allow-missing-secrets`, the resolver downgrades to empty, and
`collectMissing` then fires on the emptiness. That is the exact scenario
covered by `internal/workspace/apply_vault_test.go:740-810`
(`TestApplyAllowMissingSecretsDoesNotDowngradeRequired`).

There is a third wall, earlier than either, that matters for the
no-overlay contributor: if a config contains a `vault://` URI but
declares no `[vault]` providers, `config.Load` itself refuses the file
at parse time — `internal/config/validate_vault_refs.go:236`:
"`%s references %q but the config declares no [vault] providers`".
No flag exists that gets past that; it is not even reached by the
resolver. If the private overlay is what supplies `[vault.provider]`
and the public base carries the `vault://` refs, the OSS contributor
fails at *parse*, with an error that names neither the overlay nor the
fact that the config is only half-visible to them.

### 4. "Base declares required, only an overlay can satisfy it" — contemplated for the *personal* overlay, never for the *workspace* overlay

The PRD contemplates exactly one overlay: the personal one
(`niwa.toml` / `GlobalOverride`). R33's worked example is a team
`[env.secrets.required]` satisfied by a personal overlay value, and
US-9 (`:367-393`) is the whole external-contributor story, which offers
three escape hatches: override the provider in the personal overlay,
override individual keys in the personal overlay, or run
`niwa apply --allow-missing-secrets`.

The *workspace visibility overlay* (`workspace-overlay.toml`, the
`-overlay` repo) is a different feature (`PRD-workspace-visibility-overlay`)
and was never reconciled with R33/R34. Mechanically it does work: the
overlay merge at `internal/workspace/apply.go:1084` happens well before
the required check at `:1325`, and `MergeWorkspaceOverlay` merges the
tier maps additively with base-wins-on-collision
(`internal/workspace/override.go:875-893`, helper
`mergeStringMapBaseWins` at `:1165`; contract documented at `:699-702`).
So an overlay-supplied value does satisfy a base-declared required key,
and an overlay can *add* required declarations but can never *remove* a
base one.

That asymmetry is the crux of the scoping question. A public base config
that declares `required` keys is making a promise on behalf of every
consumer, including consumers who cannot see (let alone clone) the
overlay that would satisfy it. Nothing in the config schema lets the
base say "required *if* the overlay is present", and nothing in the
error message tells a contributor that the missing piece is an overlay
they have no access to. The required check does not know overlays exist.

### 5. Do `recommended` and `optional` have teeth today? Yes — one non-obvious set

Beyond diagnostics:

- `recommended` — one stderr warning line per miss
  (`required.go:146-179` `warnRecommended`), unconditional, not gated
  on any flag. Covered by
  `internal/workspace/apply_vault_test.go` `TestApplyMissingRecommendedEmitsStderrWarning`.
- `optional` — no output at all.
- **All three levels** (and the `Values` table) feed
  `buildSecretsExclusionSet` in
  `internal/workspace/env_example_prepass.go:145-172`. Any key named in
  `env.secrets.{required,recommended,optional}` or
  `claude.env.secrets.*` is excluded from the `.env.example` pre-pass,
  which otherwise classifies undeclared probable-secret keys and can
  **fail the apply** (`ActionFail` at `:124`). So declaring a key at
  *any* level in a secrets table is a real behavioral act: it
  suppresses a separate hard-fail path. This is undocumented in
  `docs/guides/vault-integration.md`.
- Tier maps also participate in deep-copy and merge plumbing
  (`internal/vault/resolve/deepcopy.go:181-192`,
  `internal/config/env_tables.go:36-57`) but carry no other semantics.
- The tier maps hold **descriptions only**, never values. Open issue
  tsukumogami/niwa#62 proposes allowing `vault://` URIs directly in
  `[env.secrets.recommended]` / `[.optional]` so the sub-table level
  selects miss-behavior for a vault ref. Its "Backward compat" note is
  the clearest existing statement of intent on this whole question:
  "`[env.secrets] KEY = "vault://..."` keeps working — no level ->
  **defaults to required**." Today's model is: an undeclared vault ref
  is implicitly required, and `?required=false` (R11) is the only way
  to soften it — silently, with no warning, which #62 correctly calls
  out as a missing rung ("There's no way today to express 'vault-backed
  AND warn-on-miss'").

### 6. What would break if required became a loud warning

Concretely, in shipped code, very little — the check has exactly one
call site and returns an error nobody inspects. The breakage is
contractual, and it splits by scenario:

- **Legitimate loss.** The R10 identity-pivot hazard returns for
  precisely the keys teams flagged as load-bearing. An empty
  `AWS_ACCESS_KEY_ID` or `GITHUB_TOKEN` materialized into `.local.env`
  can silently fall through to ambient credentials on the host. A hard
  fail at apply is cheap; a wrong-identity operation two hours into an
  agent session is not. Any softening should preserve "empty is never
  written for a required key" even if it stops preserving "apply
  fails" — those are separable, and today they are conflated.
- **Test surface.** `TestApplyAllowMissingSecretsDoesNotDowngradeRequired`
  and `TestApplyFailsOnMissingRequiredEnvSecret` encode the rule, and
  `docs/guides/vault-integration-acceptance-coverage.md:38,41` bind
  them to PRD acceptance criteria. A change needs the PRD amended, not
  just the tests edited.
- **What does not break.** The worktree path never ran the check
  (`checkRequiredKeys` has one caller; `applyContentToWorktree` in
  `internal/cli/session_lifecycle_cmd.go:352-370` deliberately does not
  even resolve secrets any more — it byte-copies the instance's
  already-materialized env). So niwa already treats required as a
  *bootstrap-time gate*, not a per-materialization invariant. Softening
  it at bootstrap makes the two paths more consistent, not less.

### 7. Precedent inside niwa for degrading instead of failing

`PRD-machine-identity-vault-sync.md:533` R13 row 1 covers almost exactly
the observed situation, for the credential-pool layer:

> Personal vault unreachable (network down, **CLI not installed**, not
> logged in) AND every needed `(kind, project)` has an entry in the
> local file or a working CLI session -> Single stderr warning naming
> the unreachable provider. Apply continues. | 0

Implemented at `internal/workspace/apply.go:1192-1206`. So niwa already
says "vault unreachable, warn once, fall back, exit 0" — but only for
credentials it uses to *reach* vaults, not for the env values it
resolves *from* them. Row 2 ("no fallback -> hard error, non-zero") is
the shape a softened env path could copy: the distinction is not
warn-vs-fail, it is *is there a fallback*, and env resolution today has
no fallback concept at all.

### 8. The flag is not reachable from the command that was actually run

`--allow-missing-secrets` exists on exactly two commands:
`internal/cli/apply.go:24` and `internal/cli/create.go:22`. It does
**not** exist on `niwa init` (`internal/cli/init.go:45-54` lists every
init flag), even though init runs the full pipeline via
`applier.Create` at `init.go:197`. Same for `niwa reset`
(`internal/cli/reset.go:151`) and the ephemeral-session/dispatch
provisioning path (`internal/cli/instance_from_hook.go:417`), neither of
which sets `AllowMissingSecrets`.

This is a repeat of a bug already fixed once: issue #141 ("niwa create
suggests `--allow-plaintext-secrets` in error, but the flag only exists
on niwa apply"), fixed by PR #142, which added both flags to `create`.
`init` was missed. So the exact command in the observed report,
`niwa init tsukumogami --from tsukumogami/dot-niwa`, has no opt-out
available even for the failure modes the opt-out does cover.

## Implications

1. **The scope assumption needs correcting before design starts.** The
   problem is not primarily "required is too strict". It is that three
   independent hard-fail gates sit in front of a first-run user
   (parse-time no-providers-declared, resolve-time provider-unreachable,
   post-merge required-miss), only the third is governed by R33/R34,
   and only the second and third are even flag-adjacent. Any "loud but
   non-fatal" story has to name which of the three it changes.
2. **R9, not R34, is the highest-value target.** Making
   `--allow-missing-secrets` also cover `ErrProviderUnreachable` (with a
   distinct, per-provider aggregated warning, mirroring R13 row 1) would
   unblock the maintainer-on-a-fresh-host persona without touching the
   required contract at all. That is a smaller, better-precedented
   change than softening R34.
3. **R34 can stay hard and still not be the wall,** provided the
   workspace author gets a way to express conditionality. Today a base
   config's only options are `required` (fail), `recommended` (warn but
   also: the value must be declared separately), or nothing. Issue #62's
   proposal — level-bearing vault refs — is the missing expressiveness,
   and it lands the responsibility where this exploration suspects it
   belongs: on the config author, not on niwa's enforcement.
4. **`niwa init` needs flag parity regardless of the semantic outcome.**
   Even the fully-correct current behavior is unusable from init because
   the documented escape hatch is not wired there. This is a
   self-contained fix with a merged precedent (#141/#142).
5. **The "declared required, only the overlay can satisfy it" case is
   an unwritten contract.** The base-wins additive merge means a public
   base config's `required` declarations are binding on people who
   cannot see the overlay. Either the base must not declare required
   keys that only an overlay satisfies (a documentation/authoring rule),
   or niwa needs to know an overlay is expected-but-absent and say so.
   Nothing in the code today can distinguish those cases.

## Surprises

- `--allow-missing-secrets` does not help with an unreachable provider
  at all — the single most likely first-run failure. The flag's name,
  its R9 remediation text, and the doc comment on
  `vault.ErrProviderUnreachable` all imply otherwise.
- A missing `infisical` binary is indistinguishable, at the error-sentinel
  level, from an expired login or a network outage. Yet the remediation
  differs completely (install a tool vs. re-auth vs. wait).
- `checkRequiredKeys` enforces per-repo and per-instance required
  tables that R33 explicitly deferred out of v1.
- All three requirement levels silently suppress a *different* hard
  fail (the `.env.example` pre-pass), so `optional` is not inert even
  though it prints nothing.
- The worktree path was deliberately stripped of secret resolution
  (PR #163 lineage, issue #170) — niwa already retreated from
  "enforce strictness everywhere" once, for the same class of reason.
  The doc comment at `internal/workspace/effective_config.go:14-20`
  still describes the old worktree behavior and is now stale.
- Zero design-doc or ADR coverage for a rule whose comment cites a
  requirement ID in three separate places. The requirement ID lends the
  rule an authority its provenance does not support.

## Open Questions

1. Should "required" mean *fail the command* or *never silently
   materialize an empty value*? These are separable and currently
   conflated. A design that keeps the second while relaxing the first
   (omit the key from `.local.env`, warn loudly, exit 0) may satisfy
   both R34's stated intent and the degradation need. Needs a human
   call on whether omission is acceptable to downstream tooling.
2. Is a public base config allowed to declare `required` keys that only
   a private overlay can satisfy? If yes, niwa needs an
   overlay-absent-but-expected signal. If no, this is an authoring rule
   for `tsukumogami/dot-niwa` plus a lint, and the code does not change.
3. Should `--allow-missing-secrets` be extended to cover
   `ErrProviderUnreachable`, or should that be a separate flag/condition
   (e.g. a `vault_optional` workspace setting, or auto-degradation when
   the provider CLI is absent as opposed to auth-failed)? Auto-degrading
   on "binary not installed" is attractive and requires a new sentinel
   distinct from `ErrProviderUnreachable`.
4. Does `niwa init` get flag parity, or does the whole init path get a
   different default (e.g. init degrades by default and `apply`
   enforces)? The two-personas framing suggests first-run should be
   permissive and steady-state strict, but that is a product call.
5. Is issue #62 (level-bearing vault refs) the right vehicle for the
   expressiveness gap, and should it be folded into this exploration's
   artifact rather than left as a separate feature?
6. What is the intended behavior when the overlay is inaccessible
   (private, no auth) versus genuinely absent? `niwa apply` silently
   skips a failed overlay clone today; the required check then fires
   with an error that mentions neither.

## Summary
`required`/`recommended`/`optional` come from a single PRD contract (PRD-vault-integration R33/R34, shipped whole in PR #52 with no design doc or ADR), whose only recorded rationale for the hard fail is that `--allow-missing-secrets` is an exploratory/CI switch that must not silently blank a key the team called load-bearing — and the check is deliberately post-merge so a *personal* overlay can satisfy a base-declared required key, a case the visibility overlay was never reconciled with. The preliminary read is wrong on the decisive detail: `--allow-missing-secrets` only downgrades `ErrKeyNotFound`, never `ErrProviderUnreachable`, so a host without the Infisical CLI dies inside the resolver at R9 and never reaches `checkRequiredKeys` at all — and `niwa init` does not even expose the flag, repeating the #141/#142 gap. The biggest open question is whether "required" should mean "fail the command" or merely "never materialize an empty value", since those are separable today only in principle.
