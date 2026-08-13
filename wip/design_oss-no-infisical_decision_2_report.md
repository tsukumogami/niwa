# Decision 2 — Where strict mode lives, how it merges, how the overlay prohibition is enforced

Serves R12 (setting honoured by unattended paths + per-invocation flag on
create/apply/init), R13 (not settable by a visibility overlay; an attempt is
reported and does not take effect), R21 (does not apply to worktree
re-materialization).

## Context

### The layers that exist, and which of them the unattended paths actually load

Four configuration rungs could plausibly host the setting. Three of them reach
`niwa dispatch` and the SessionStart hook; one is the layer R13 forbids.

| Rung | Type | File | Reached by the unattended path? |
|---|---|---|---|
| Workspace | `config.WorkspaceMeta` (`internal/config/config.go:276-307`) | `.niwa/workspace.toml` `[workspace]` | Yes — `config.Load` at `internal/cli/instance_from_hook.go:357`, reconciled at `:371` |
| Host | `config.GlobalSettings` (`internal/config/registry.go:27-80`) | `~/.config/niwa/config.toml` `[global]` | Yes — `config.LoadGlobalConfig()` at `internal/cli/instance_from_hook.go:394` |
| Personal overlay | `config.GlobalOverride` (`internal/config/config.go:627-640`) | global-config repo `niwa.toml` `[global]` / `[workspaces.<name>]` | Yes, indirectly — the hook sets `applier.GlobalConfigDir` (`instance_from_hook.go:394-397`) and the pipeline parses it at `internal/workspace/apply.go:867-874` |
| Visibility overlay | `config.WorkspaceOverlay` (`internal/config/overlay.go:18-26`) | overlay clone `workspace-overlay.toml` | Yes (`apply.go:1027`) — and this is the layer R13 forbids |

`realProvisionInstance` (`internal/cli/instance_from_hook.go:351-423`) is the
single function behind `niwa dispatch` (`internal/cli/dispatch.go:300`), the
SessionStart hook (`instance_from_hook.go:172`), `niwa watch --once`
(`internal/cli/watch.go:777`) and reap. It already has the workspace config
(line 357/377) and the host global config (line 394) in hand before it calls
`applier.Create` at line 417. The PRD's claim that the unattended path already
loads the rungs a setting would live on is confirmed: threading a resolved
boolean onto the applier there is a handful of lines, not a plumbing project.

### R13 is structurally free on `[workspace]`, and needs new code only to be *reported*

Two independent facts make a `[workspace]`-hosted setting unreachable from the
visibility overlay:

1. `WorkspaceOverlay` (`internal/config/overlay.go:18-26`) has **no**
   `Workspace` field. Its fields are Sources, Groups, Repos, Claude, Env,
   Files, Vault. There is no workspace-metadata stanza to decode into.
2. `MergeWorkspaceOverlay` (`internal/workspace/override.go:708-950`) starts
   with a shallow `merged := *ws` at line 710 and **never assigns**
   `merged.Workspace` anywhere in the function (verified by grep: no
   `merged.Workspace`, `merged.Vault`, `merged.Instance`, or `merged.Root`
   assignment exists in the file). Whatever the base config declared survives
   the merge byte-for-byte.

So "does not take effect" is free. "Is reported" is not: `ParseOverlay` uses
`toml.Unmarshal` (`overlay.go:89`), which discards the `MetaData`
(BurntSushi/toml v1.6.0 — `Unmarshal` is a two-line wrapper over
`NewDecoder(...).Decode(v)` that drops the metadata return). An unknown key in
`workspace-overlay.toml` is therefore silently dropped today — no warning, no
diagnostic. Contrast `config.Parse`, which keeps the `MetaData` and turns
`md.Undecoded()` into `ParseResult.Warnings` (`config.go:449-513`). The overlay
parser has no equivalent. R13's reporting clause is the part that costs code.

### Two other rungs are reachable from the overlay — this rules options out

- `RepoOverride` **is** an overlay-settable type. `MergeWorkspaceOverlay` takes
  the overlay's whole `RepoOverride` verbatim for any repo absent from the base
  (`override.go:753-760`, `merged.Repos[k] = v`), including its
  `EnvExamplePolicy`, `ReadEnvExample`, and `EnvOutput` fields. Any design that
  mirrors `env_example_policy`'s per-repo position hands the overlay a legal
  place to set strictness.
- `VaultRegistry` **is** an overlay-settable type (`overlay.go:25`), parsed and
  consumed at `apply.go:1042`, `:1051`, `:1059`. `MergeWorkspaceOverlay` never
  folds `overlay.Vault` into `merged.Vault`, so a `[vault].strict` key authored
  in the overlay would be parsed, validated, and then silently ignored — the
  worst of both worlds (it looks settable, it isn't, and nothing says so).

### The existing precedents

- **`EffectiveEnvExamplePolicy`** (`internal/config/env_example_policy.go:109-156`):
  a four-rung ladder, most-specific first — per-variable (repo, then workspace),
  inline annotation, per-category (repo, workspace, global), default `warn`.
  Its `Action` type is a string enum with `UnmarshalText`
  (`env_example_policy.go:9-34`). Note that its "global" rung is the *personal
  overlay* (`GlobalOverride.EnvExamplePolicy`, `config.go:632-635`), and it sits
  at the **bottom** of the ladder — the broadest, weakest rung.
- **`resolveDispatchKeepAlive`** (`internal/cli/dispatch_keepalive.go:94-105`):
  a three-rung pure function, flag > downstream instance setting > host
  `[global]` default-fill > default off, with the tri-state `triBoolValue` pflag
  adapter (`dispatch_keepalive.go:44-74`, registered with `NoOptDefVal = "true"`
  at `dispatch.go:35-36`) so `--keep-alive=false` can force off against a host
  default of on.
- **`*bool` = unset/inherit** is the house idiom: `ReadEnvExample`
  (`config.go:298`), `AutoInstallPlugins` (`registry.go:28`),
  `RemoteControlOnDispatch` (`registry.go:42`), `KeepAliveOnDispatch`
  (`registry.go:50`), `WorkSummaryHooks` (`config.go:45`).
- **`team_only` / `ErrTeamOnlyLocked`** (`config/vault.go:28-30`,
  `vault/errors.go:24-27`, enforced at `override.go:498` and six call sites at
  `:537 :565 :580 :622 :638 :653`): a per-key lock that makes a personal-overlay
  override a hard error naming the key and the rule.
- **`Shadow`** (`internal/workspace/shadows.go:23-54`): a value-free record
  (Kind, Name, TeamSource, PersonalSource, Layer) that flows to stderr via
  `Reporter.Defer` (`apply.go:39-44`) and into state.json, deterministically
  sorted (`shadows.go:166-171`), with a compile-time invariant forbidding any
  `secret.Value` field.
- **`isProtectedDestination`** (`overlay.go:132-135`, `:191-195`): the one
  existing case of the visibility overlay being told "you may not set that" —
  a hard parse error naming the field and the prohibited destination.

### R21: the tolerance is no longer where the brief expects it

`EffectiveConfigOptions.AllowMissingSecrets`
(`internal/workspace/effective_config.go:28-32`) has a doc comment at lines
17-20 claiming "the worktree apply path sets this true so a transient vault
outage degrades a worktree re-materialization to a warning". **That comment is
stale.** `ResolveAndMergeEffectiveConfig` has exactly one caller today —
`apply.go:1308`. The worktree path stopped resolving secrets entirely: see
`internal/cli/session_lifecycle_cmd.go:347-353` ("the worktree path no longer
resolves secrets at all… so there is no need to rebuild vault bundles or re-run
ResolveAndMergeEffectiveConfig here") and `worktree_content.go:479-501` (the
EnvMaterializer is dropped; env is inherited by byte-copy via
`inheritEnvOutputs`, `worktree_content.go:206-229`).

R21 is therefore satisfied by *omission*: there is no strict decision to make on
a path that performs no resolution. The only shared failure site between the two
paths is the promote check at `internal/workspace/materialize.go:958-962`
("promoted key %q not found in resolved env vars"), which the worktree path
reaches through `ctx.InheritedEnv` (`materialize.go:939-946`). That line is the
R11 fix site and it is shared — which matters, see Open risks.

## Options

### Option A — `*bool` on `[workspace]`, tri-state flag, pure resolver; overlay attempt reported via a tombstone field

**Mechanism.** Add `StrictSecrets *bool \`toml:"strict_secrets,omitempty"\`` to
`config.WorkspaceMeta` (`config.go:276-307`). Add a `--strict-secrets` flag to
`create`, `apply`, and `init`, registered through the existing `triBoolValue`
adapter with `NoOptDefVal = "true"` so it overrides in both directions. Resolve
with a pure function mirroring `resolveDispatchKeepAlive`:

```go
// precedence: flag > [workspace].strict_secrets > host [global] > default off
func resolveStrictSecrets(flag *bool, ws config.WorkspaceMeta, global config.GlobalSettings) bool
```

The host rung parameter is accepted from day one but left unpopulated in v1
(see the sub-decision below). Thread the result as `Applier.StrictSecrets`,
declared next to `AllowMissingSecrets` (`apply.go:55-71`), set from the flag at
`create.go:218` / `apply.go:166` / init's applier construction
(`init.go:176-197`), and set from config in `realProvisionInstance` after the
config load at `instance_from_hook.go:377` and before `applier.Create` at
`:417`.

For R13, add a decode-only stanza to `WorkspaceOverlay`:

```go
Workspace *OverlayWorkspaceStanza `toml:"workspace,omitempty"`  // rejected, never read
Warnings  []string                `toml:"-"`
```

`validateOverlay` (`overlay.go:101`) checks for a non-nil `Workspace` carrying
`strict_secrets` and appends a warning to `o.Warnings`; nothing ever reads the
value. `apply.go` drains `overlay.Warnings` into `a.Reporter.DeferWarn` right
after `ParseOverlay` at `:1027`, so it lands in the same deferred-diagnostic
block as the shadow records (`apply.go:39-44`) and reaches stderr on the
unattended path too (`realProvisionInstance` wires the reporter to os.Stderr at
`instance_from_hook.go:366`). No function signature changes; the worktree
caller at `session_lifecycle_cmd.go:433` can drain the same slice or ignore it.

**Code impact.** One field on `WorkspaceMeta`; one field pair on
`WorkspaceOverlay` plus ~10 lines in `validateOverlay`; one ~15-line resolver
with the `triBoolValue` adapter reused as-is; one field on `Applier`; four call
sites setting it (create, apply, init, `realProvisionInstance`). Nothing in
`MergeWorkspaceOverlay`, `MergeGlobalOverride`, or `ResolveAndMergeEffectiveConfig`
changes. The R21 work is a test asserting `WorktreeApplyOptions` carries no
strict field, plus deleting the stale comment at `effective_config.go:17-20`.

**Trade-offs.** Cheapest option that satisfies every requirement and every
acceptance criterion. R13's "does not take effect" is guaranteed by two
structural facts rather than by a runtime check that could regress. The
tombstone field is mildly unusual — a struct field that exists to be refused —
but it has direct precedent in `isProtectedDestination` and in the deliberate
omission of `Enabled` from `OverlayClaudeConfig` (`overlay.go:36-45`, which
notes "It does not carry Enabled"); the tombstone's only addition is turning
that silence into a message. The `*bool` forecloses a third value, mitigated
below.

### Option B — `config.Action`-style enum mirroring the `.env.example` policy ladder

**Mechanism.** Define `StrictPolicy` with per-enforcement-point actions
(`unsatisfiable`, `unreachable`, `required_shortfall`, `promotion`) plus a
`vars` map for per-key severity, hosted at the same four positions
`EnvExamplePolicy` occupies: `WorkspaceMeta` (`config.go:299-302`),
`RepoOverride` (`config.go:390-392`), `GlobalOverride` (`config.go:632-635`),
plus an inline annotation analogue. Resolve with an `EffectiveStrictPolicy`
function shaped like `EffectiveEnvExamplePolicy`
(`env_example_policy.go:120-156`).

**Code impact.** Largest of the three. New enum type with `UnmarshalText`/
`MarshalText`, four struct positions, a four-rung resolver, per-key merge in
both `MergeWorkspaceOverlay` and `MergeGlobalOverride`, and a per-key lookup at
each of the four enforcement points instead of one boolean read.

**Trade-offs.** It contradicts a decision the PRD already made and defended
("One strict mode, not per-enforcement-point granularity" — the enforcement
points are an artifact of niwa's internal structure and an operator holds a
single intent one altitude above them). Worse, it **breaks R13 structurally**:
`RepoOverride` is copied verbatim from the overlay for any repo absent from the
base config (`override.go:753-760`), so `[repos.<private-repo>.strict_policy]`
in `workspace-overlay.toml` would take effect. Enforcing R13 would then require
an active scrub of overlay-supplied repo overrides rather than a structural
guarantee — a check that a future merge-semantics change can silently defeat.
The one thing it buys (per-key severity) is exactly what the PRD's Known
Limitations records as a deliberate, reversible-in-the-cheap-direction
omission.

### Option C — boolean on the existing `[vault]` table, beside `team_only`

**Mechanism.** `[vault].strict = true` on `config.VaultRegistry`
(`config/vault.go:18-31`), read at the enforcement points; R13 enforced by the
same `ErrTeamOnlyLocked`-style hard error the personal overlay gets
(`override.go:537`).

**Code impact.** Smallest struct diff — one field on an existing type — but the
precedence story is the messiest: `VaultRegistry` exists at three positions
(workspace `config.go:253`, personal overlay `config.go:631`, visibility
overlay `overlay.go:25`) and none of them merge into a single effective
registry today.

**Trade-offs.** Fails R13 in the "reported" direction and is conceptually
misplaced in the "unsatisfiable declaration" direction. `WorkspaceOverlay.Vault`
is a real, parsed, consumed field (`apply.go:1027-1059`), so `[vault].strict` in
the overlay is legal TOML that decodes successfully and is then ignored, because
`MergeWorkspaceOverlay` never folds `overlay.Vault` into the merged config —
silence, not a report. Fixing that means adding a targeted rejection anyway, at
which point Option A's tombstone is strictly simpler. And the deeper problem:
the unsatisfiable-declaration enforcement point fires precisely when **no vault
is configured at all** (`VaultRegistry.IsEmpty()`, `vault.go:136-141`, returns
true), so hanging the control off `[vault]` makes it undiscoverable in the one
first-run scenario the PRD is built around. A contributor who has never heard of
a vault would have to author a `[vault]` block to say "fail if secrets are
missing".

### Sub-decision — should the host `[global]` rung ship in v1?

`GlobalSettings` (`registry.go:27-80`) is attractive: it is a plain local file
with a `SaveGlobalConfigTo` writer, it is already loaded on the unattended path
(`instance_from_hook.go:394`), it is per-host so an operator declares strictness
once for every workspace, and it is structurally unreachable by any overlay.
The personal-overlay rung (`GlobalOverride`, the rung `env_example_policy`
chose) is the weaker of the two candidates for this feature: it is a *repo
snapshot* synced by apply, converted from working tree to snapshot with an
explicit "manual edits inside this directory will no longer persist" notice
(`apply.go:860-866`), and re-registering it re-clones from scratch
(`config_set.go:60-77`). An operator cannot just edit it.

Recommendation: **defer the host rung.** The PRD never asks for it; the
acceptance criterion says "strict mode set as a workspace setting". Shipping one
rung plus a flag means one precedence rule to document and test. Write
`resolveStrictSecrets` with the host parameter in its signature from day one
(nil-safe, consulted last) so adding it later is a one-line change and a config
field, matching the PRD's own "start coarse, keep the cheaper correction
available" logic.

## Recommendation

**Option A: `strict_secrets` as a `*bool` on `[workspace]` in `WorkspaceMeta`,
a tri-state `--strict-secrets` flag on create/apply/init resolved by a pure
`resolveStrictSecrets(flag, workspace, host)` function with precedence
flag > `[workspace]` > (reserved host `[global]`) > default off, and R13
enforced structurally with a decode-only tombstone stanza on `WorkspaceOverlay`
whose only effect is a deferred warning.**

Rationale:

1. **R13 becomes a structural property, not a runtime check.** Two independent
   facts — `WorkspaceOverlay` has no workspace stanza, and
   `MergeWorkspaceOverlay` never writes `merged.Workspace` (`override.go:710`,
   no assignment anywhere in the function) — mean an overlay cannot alter the
   setting even if the reporting code is later deleted or a merge rule changes.
   Both competing options put the field on a type the overlay *can* supply
   (`RepoOverride` in B, `VaultRegistry` in C) and would rely on an active
   scrub. This is the strongest argument and it is verified, not assumed.
2. **R12 is a handful of lines because the rungs are already loaded.**
   `realProvisionInstance` holds the reconciled workspace config at
   `instance_from_hook.go:377` and calls `applier.Create` at `:417`; one
   assignment between them makes dispatch, SessionStart, watch, and reap all
   honour the setting with no flag. The interactive commands set the same field
   next to the `AllowMissingSecrets` line they already have
   (`create.go:218`, `apply.go:166`).
3. **Every mechanism has a working precedent.** `*bool`-means-inherit is the
   house idiom at five existing sites; `triBoolValue` +
   `NoOptDefVal="true"` already exists for exactly the "must override a config
   default in both directions" problem (`dispatch_keepalive.go:44-74`); the pure
   three-rung resolver has a line-for-line model in `resolveDispatchKeepAlive`;
   the overlay refusal has a model in `isProtectedDestination`.
4. **R21 needs nothing.** The worktree path performs no secret resolution
   (`session_lifecycle_cmd.go:347-353`), so not threading the field into
   `WorktreeApplyOptions` is the whole implementation. Add a regression test and
   delete the stale comment at `effective_config.go:14-21`.

Two details the design must pin down:

- **The R16 conflict rule needs a precise trigger.** With a tri-state flag,
  "passing the strict-mode flag" means passing it *true*. `--allow-missing-secrets
  --strict-secrets=false` is agreement, not contradiction, and must not be
  rejected. The check is `allowMissingSet && strictFlag != nil && *strictFlag`.
- **`--strict-secrets=false` is the contributor's escape hatch**, not
  `--allow-missing-secrets` (which R16 makes a no-op). Document it as such.

On enum vs boolean: the migration path stays open. BurntSushi/toml's
`UnmarshalTOML(data any)` hook can accept both a bool and a string on the same
key — `VaultProviderConfig` already uses that hook shape
(`config/vault.go:51-81`) — so `strict_secrets = true` and
`strict_secrets = "fail"` can coexist if a third value ever earns its place.
That removes the usual "we picked a bool and now we're stuck" objection.

On reusing the `Shadow` record for R13's report: **reuse the shape, not the
type.** `Shadow` is the right model for a value-free, layer-labelled,
deterministically-sorted record that flows through `Reporter.Defer` into both
stderr and state.json, and R13's report should look like that and ride the same
sink. But `DetectShadows` walks only `GlobalConfigOverride`
(`shadows.go:96-160`), its `Layer` vocabulary is `"personal-overlay"`
(`shadows.go:54`), and its `TeamSource`/`PersonalSource` pair encodes "two files
declared the same key" — which does not describe "one file declared a key it may
not declare". A Shadow reports a *successful* override; R13 reports a *refused*
one. Widening the type to carry both would blur a record whose whole value is
that it means one specific thing. Likewise reuse `ErrTeamOnlyLocked`'s *message
shape* (name the key, name the rule, say what to do) but not its severity:
team_only returns a hard error (`override.go:537`), and R13 says "reported and
does not take effect", not "aborts the command". Failing an apply because a
private overlay contains a stray key would break the very users the overlay
serves, for a key that was already inert.

## Rejected alternatives

- **Option B (per-enforcement-point / per-key policy ladder).** Rejected on
  three counts: it re-opens a decision the PRD closed with reasons, it hands the
  visibility overlay a legal position via `RepoOverride` (`override.go:753-760`)
  and so converts R13 from structural to enforced, and it is the largest diff of
  the three for a capability recorded as a deliberate limitation.
- **Option C (`[vault].strict`).** Rejected because `[vault]` is absent
  precisely in the first-run case the feature exists for
  (`VaultRegistry.IsEmpty()`, `vault.go:136-141`), and because
  `WorkspaceOverlay.Vault` (`overlay.go:25`) makes the key authorable in the
  forbidden layer with no report — the silent-drop failure R13 explicitly rules
  out.
- **Silent structural prohibition only (no tombstone).** The cheapest possible
  R13 implementation is to do nothing at all: `toml.Unmarshal` at `overlay.go:89`
  already discards the key. Rejected because R13's second sentence requires the
  attempt to be *reported*, and because silence here is indistinguishable from
  a typo — the overlay author gets no signal that their intent was dropped.
- **A general `md.Undecoded()` warning scan in `ParseOverlay`** (mirroring
  `config.Parse` at `config.go:509-512`). Tempting, and it would fix a real
  hygiene gap — every typo'd key in `workspace-overlay.toml` is silently ignored
  today. Rejected as scope creep for this PRD: it would surface a backlog of
  pre-existing warnings in every workspace with an overlay, on a change whose R19
  requirement is "a workspace whose secrets all resolve behaves exactly as it
  does today… no new output". Worth filing separately.
- **Hard-failing the apply on an overlay strict-mode key** (the
  `isProtectedDestination` / `ErrTeamOnlyLocked` severity). Rejected: R13 asks
  for report-and-ignore, and a hard failure would take down instance creation
  for every consumer of an overlay whose author added one inert key.
- **Personal-overlay (`GlobalOverride`) as the second rung**, the position
  `env_example_policy` chose. Rejected in favour of deferring the second rung
  entirely; if one is later added, the host `[global]` table is the better
  target because it is locally editable, while the personal overlay is a synced
  snapshot that warns manual edits will not persist (`apply.go:860-866`).
- **"Any layer that says strict wins" (OR/max-severity merge)** instead of
  most-specific-wins. Rejected: it makes strict impossible to turn off
  per-invocation, which R12's "a per-invocation setting SHALL take effect" reads
  against, and it has no precedent in either existing ladder.

## Open risks

1. **A strict workspace config is unescapable on the unattended paths.** Under
   flag > workspace precedence, a workspace author who sets
   `[workspace].strict_secrets = true` restores the exact first-run wall this
   PRD removes, for every contributor — and `niwa dispatch` / SessionStart take
   no flags, so those contributors have no override at all. The setting is at
   least readable (unlike the overlay, which is why R13 exists), and
   `--strict-secrets=false` covers create/apply/init. Mitigations if this bites:
   document the key as a CI/fleet knob rather than a workspace default, or add
   the host `[global]` rung *above* the workspace rung. The second is a
   precedence inversion relative to both existing ladders, so it should not be
   done pre-emptively.
2. **R11's fix site is shared with the worktree path, and R2 makes it newly
   hot.** `materialize.go:958-962` fails on a promoted key absent from the
   resolved env, and the worktree path reaches it through `ctx.InheritedEnv`
   (`materialize.go:939-946`). R2 replaces today's empty-string write with
   omission, so a promoted-but-unresolved key that currently slips through as
   `""` will start firing that error — on the worktree path too, which R21 says
   must stay tolerant. If R11 is implemented as an instance-path-only tolerance,
   R21 breaks. The fix must land in `resolveClaudeEnvVars` itself.
3. **The stale comment at `effective_config.go:14-21` will mislead the
   implementer.** It states the worktree path sets `AllowMissingSecrets` true;
   that path no longer calls the function at all
   (`session_lifecycle_cmd.go:347-353`; the helper has one caller,
   `apply.go:1308`). Someone reading it may "preserve" a tolerance that is not
   there, or thread strict into the worktree options to switch it off. Delete
   the comment as part of this work.
4. **`niwa watch` and `niwa reap` inherit strict silently.** Both route through
   `provisionInstanceFunc` (`watch.go:777`, `reap.go` eligibility path), so a
   workspace-level strict setting changes their behaviour too. Probably correct
   and certainly consistent, but the PRD names only dispatch and SessionStart;
   the design should state it deliberately rather than letting it be discovered.
5. **The tombstone stanza is load-bearing but invisible.** Nothing prevents a
   future contributor from "cleaning up" an unused struct field on
   `WorkspaceOverlay`, or from adding a `Workspace WorkspaceMeta` field to it
   for an unrelated reason and silently re-opening the R13 hole. Pin it with a
   test in the spirit of `TestShadowHasNoSecretValueField`
   (`shadows.go:16-18`): assert that `MergeWorkspaceOverlay` leaves
   `merged.Workspace` equal to the base, and that an overlay declaring
   `strict_secrets` produces a warning and a non-strict resolution.
6. **Two contradictory precedence conventions already exist in the tree.**
   `EffectiveEnvExamplePolicy` puts the personal/global rung at the *bottom*
   (`env_example_policy.go:143-152`), while `MergeGlobalOverride` lets the
   personal overlay *win* per key over the team config (`override.go:474-485`).
   Any documentation of the new ladder should state its precedence explicitly
   rather than saying "like the other ones", because there is no single "other
   one".
