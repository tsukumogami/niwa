# Decision 1: Where the resolver records *why* a key is unresolved, and how that reason travels

Serves R2, R2a, R5, R10 of `docs/prds/PRD-oss-no-infisical.md`.

## Context

### The collapse today

`internal/vault/resolve/resolve.go:470` (`resolveOne`) has exactly three
outcomes for a `vault://` reference:

- success -> `config.MaybeSecret{Secret: val, Token: token}` (line 522)
- `?required=false` miss -> `config.MaybeSecret{}` (line 529)
- `--allow-missing-secrets` miss -> `config.MaybeSecret{}` + stderr warning (line 535)
- everything else -> a bare `error` that aborts the whole walk

Lines 529 and 535 return **byte-identical zero values**. `MaybeSecret` is
`{Plain string; Secret secret.Value; Token vault.VersionToken}`
(`internal/config/maybesecret.go:24-37`); both returns leave all three fields
zero. Downstream, `internal/workspace/required.go:135` (`isEmptyMaybeSecret`)
and `internal/workspace/materialize.go:38` (`maybeSecretString`) see the same
thing. That is the forcing constraint in the brief, and it is real.

### The four shapes the resolver actually produces, versus the three reasons R5 names

Mapping the PRD's enforcement points onto the code:

| PRD reason | Where it arises today | Does the resolver visit it? |
|---|---|---|
| Unsatisfiable declaration, shape (a): key listed in `[env.secrets.required]`, no entry in `Values` at all | `internal/workspace/required.go:118` — the `!ok` branch of `ms, ok := t.Values[key]` | **No.** The walker iterates `values` (`resolve.go:389`); a key with no `Values` entry is never visited. |
| Unsatisfiable declaration, shape (b): `Values` holds `vault://name/KEY` but no provider `name` is in the bundle | `resolve.go:502-517` | Yes |
| Provider unreachable | `resolve.go:550-555` | Yes |
| Required-key shortfall (reachable provider does not hold the key) | `resolve.go:526-545` | Yes |

Two findings fall straight out of this table and constrain every option:

1. **Shape (a) can never be carried out of the resolver by any mechanism**,
   because the resolver never sees it. It must be *derived* post-merge, at the
   point where the `Required`/`Recommended`/`Optional` sub-tables are in scope.
   `required.go:118` already computes exactly this predicate. So the question
   is not "carry three reasons" but "carry the two-or-three the resolver
   visits, and derive the rest".

2. **Shape (b) is currently misclassified.** `resolve.go:513-516` wraps
   `vault.ErrKeyNotFound` for "references provider %q but it is not declared in
   the active bundle", with an explicit comment saying it is "actionable as a
   key-not-found problem". Under R10 that classification is now
   outcome-changing in the wrong direction: key-not-found-on-a-reachable-provider
   is the one fatal case, and an undeclared provider is precisely *not* that —
   no provider was contacted. Whatever mechanism is chosen must reclassify this
   as unsatisfiable-declaration. This is a behaviour fix, not a refactor.

### The join the report requires

R6 requires the report to carry each key's **declared requirement level** and
**declared description**. Those live in `EnvVarsTable.Required` /
`.Recommended` / `.Optional` (`internal/config/env_tables.go:34-74`). The
resolver **never reads them** — `walkTable` (`resolve.go:388`) takes only
`values map[string]config.MaybeSecret`. So the report cannot be assembled
inside the resolver under any option. It must be assembled post-merge in
`internal/workspace`, where `checkRequiredKeys` already walks all eleven
scopes (`required.go:55-79`).

This is the single most load-bearing fact in the decision: **the resolver's
only job is to contribute a reason per visited reference; the report is
assembled at the `checkRequiredKeys` altitude regardless.** The options differ
only in the transport between those two points.

### The path a value takes between the two points

```
apply.go:1074   resolve.ResolveWorkspace(tmpCfg{overlay.Env, overlay.Repos})   <- workspace-overlay layer
apply.go:1084   MergeWorkspaceOverlay(cfg, overlay, overlayDir)                (override.go:708)
apply.go:1308   ResolveAndMergeEffectiveConfig(...)                            (effective_config.go:65)
  -> effective_config.go:74   resolve.ResolveWorkspace(cfg, teamBundle)        <- team layer
  -> effective_config.go:92   resolve.ResolveGlobalOverride(gco, personalBundle) <- personal layer
  -> effective_config.go:100  ResolveGlobalOverride(resolvedOverride, name)     (override.go:334)
  -> effective_config.go:101  MergeGlobalOverride(resolvedCfg, flattened, dir)  (override.go:494)
apply.go:1325   checkRequiredKeys(effectiveCfg, a.Reporter.Writer())           (required.go:47)
apply.go:1564   runRepoMaterializers(repoMaterializeInputs{Cfg: effectiveCfg, ...})
  -> worktree_content.go:74   MergeOverrides(in.Cfg, in.RepoName)              (override.go:46)
  -> worktree_content.go:140  &MaterializeContext{Effective: effective, ...}
  -> materialize.go:1140      ResolveEnvVars(ctx)  reads ctx.Effective.Env.{Vars,Secrets}.Values
  -> materialize.go:925       resolveClaudeEnvVars(ctx)  R11 promotion
```

Three separate resolve calls; **four** merge functions between the resolver and
the materializer (`MergeWorkspaceOverlay`, `MergeGlobalOverride`,
`MergeOverrides`, and `MergeInstanceOverrides` for the instance-root path at
`workspace_context.go:243` / `root_materializer.go:236`). Every one of those
merges implements last-layer-wins per key by copying whole `MaybeSecret` values
(`override.go:117,128,147,156` for `MergeOverrides`; `override.go:398-432`,
`569-642`, `767-886` for the two global paths).

Note also `apply.go:1074` -> `apply.go:1308`: the workspace-overlay layer is
resolved, merged into `cfg`, and then **resolved a second time**. The second
pass is a no-op for already-resolved values only because `resolveOne` returns
early on `ms.IsSecret()` (line 473) and on `ms.Plain == ""` (line 477). Any new
state must be given an equivalent early return or the second pass will
overwrite it.

Two paths are out of scope by construction and should not be plumbed:

- **The worktree path** no longer resolves secrets at all. Since the fork
  removal recorded at `internal/cli/session_lifecycle_cmd.go:348-354`, it runs
  the structural overlay merge only and inherits the clone's materialized env
  by byte-copy (`worktree_content.go:561-570`, `inheritEnvOutputs`). This is
  why R21 is already structurally satisfied, and why the R3 file record — not
  any in-memory carrier — is how an omission reaches a worktree.
- **The pre-resolve consumers** — `internal/guardrail/githubpublic.go:176`
  (`isPlaintextSecret`) and `internal/cli/status_audit.go:186`
  (`classifyMaybeSecret`) — read the *unresolved* config on purpose
  (`apply.go:1268-1277` explains why for the guardrail). Neither can see a
  post-resolve marker, and neither needs to.

### What R1 forces regardless of option

Today a required key missing from a reachable provider aborts `ResolveWorkspace`
with an error (`resolve.go:539-544`) and never reaches `checkRequiredKeys`. R6
requires the report to name *every* affected key, and R1 requires the pipeline
to keep going. So under every option the resolver must stop failing fast and
start accumulating, and the fatality decision must move entirely to
`checkRequiredKeys` plus strict mode. `TestResolveWorkspaceMissingErrorsByDefault`
(`internal/vault/resolve/resolve_test.go:265`) asserts today's behaviour and
must change under all three options. It is not an option differentiator.

There is no `required_test.go`; `checkRequiredKeys` is exercised only indirectly
through `internal/workspace/apply_vault_test.go`. Adding direct tests for it is
cheap and should be part of the work whichever option wins.

---

## Options

### Option A — a field on the `MaybeSecret` value itself

**Mechanism.** Add one nilable field to `config.MaybeSecret`:

```go
// internal/config/maybesecret.go
type MaybeSecret struct {
    Plain  string
    Secret secret.Value
    Token  vault.VersionToken

    // Unresolved, when non-nil, records why this declared slot holds no
    // value. Nil on every value the parser produces and on every value
    // the resolver resolves — including a deliberate ?required=false
    // empty, which is resolved-to-empty, not unresolved (R2a).
    Unresolved *Unresolved
}

type Unresolved struct {
    Reason       Reason // unsatisfiable declaration | provider unreachable | provider lacks key
    ProviderKind string // R9: kind only, never vendor vocabulary (R20)
    Detail       Detail // R9's binary-absent vs otherwise-unreachable split
}
```

A pointer, not a value, so `MaybeSecret{}` remains byte-identical to today's
zero and the documented zero-value semantics at `maybesecret.go:19-23` stay
true for every existing producer.

`resolveOne` changes from returning errors to returning marked values at
`resolve.go:513` (reclassified as unsatisfiable-declaration, per finding 2
above), `:539` (provider-lacks-key), and `:550` (unreachable). Line 529
(`?required=false`) is **left exactly as it is** — bare `MaybeSecret{}`, nil
marker — which is how R2a is satisfied: not by a new rule, but by the absence
of one. Line 477's early return gains an `ms.Unresolved != nil` guard so the
double-resolve at `apply.go:1074`->`:1308` does not clobber the marker.

**Code impact.**

- `internal/config/maybesecret.go`: +1 field, +2 small types. `String()` and
  `MarshalText()` need a decision — leaving them returning `m.Plain` (i.e. `""`)
  is correct, since the marker must never be mistaken for a value.
- `internal/vault/resolve/resolve.go`: four branches in `resolveOne`; one guard
  at line 477. No signature changes. `walkTable` / `walkEnv` / `walkSettings` /
  `walkGlobalOverride` / `ResolveWorkspace` / `ResolveGlobalOverride` all keep
  their signatures, because the accumulation happens in the values themselves.
- `internal/vault/resolve/deepcopy.go`: **no change required.**
  `cloneEnvVarsTable` (line 175-194) uses `maps.Copy` over
  `map[string]MaybeSecret`, which copies the struct by value including the new
  pointer. Caveat to document: the pointed-to `Unresolved` is shared between
  copies, so it must be treated as immutable after construction.
- `internal/workspace/override.go`: **no change required** in any of the four
  merges. They assign whole `MaybeSecret` values, so last-layer-wins works
  correctly for free: if the personal overlay supplies a real value for a key
  the team layer could not resolve, the overlay's unmarked value overwrites the
  marked one at `override.go:640-642`, and the marker is gone. This is the
  semantics you want and it costs nothing.
- `internal/workspace/required.go`: `collectMissing` reads `ms.Unresolved` and
  keeps only `ReasonProviderLacksKey` in the fatal list; everything else
  (including the `!ok` shape-(a) case it already detects at line 118) goes to
  the R6 report. `isEmptyMaybeSecret` gains a marker branch.
- `internal/workspace/materialize.go`: `ResolveEnvVars` skips marked keys in
  both loops (lines 1199-1208) and does not emit a `SourceEntry` for them;
  `resolveClaudeEnvVars` skips them in the inline loop (line 980-984) and the
  promotion loop at line 958-962 stops erroring on `!found` (R11).
  `maybeSecretString` (line 38) should assert-or-guard rather than silently
  return `""`.
- New file for report assembly, taking `*config.WorkspaceConfig` and returning
  an ordered `[]Shortfall`. No plumbing.

**Trade-offs.**

*For.* It is the only option that survives all four merges and both deep-copy
paths without a line of new merge code, because the carrier *is* the thing
being merged. It reaches `checkRequiredKeys` with zero new parameters and no
vault bundle — `checkRequiredKeys(cfg, w)` keeps its exact signature. R2a is
satisfied by leaving one line untouched, which is a much stronger guarantee
than "we wrote a test for it". The blast radius is confined to files that
already deal with resolution.

*Against.* It widens a type used in 134 test literals and ~15 production files
to carry a diagnostic that three of them read. It converts a documented
two-state invariant ("exactly one of Plain or Secret is populated",
`maybesecret.go:8-23`) into a three-state one, and every existing `ms.Plain ==
""` test becomes a place where a future reader can silently mis-handle the new
state. It puts a must-be-printed field (the reason, which R18 requires be
printable) inside a type whose entire contract is never-print-me — `String()`
redacts, `MarshalText()` redacts, and the new field must do neither. That is an
ergonomic hazard, though a documentable one.

*Non-issue, verified.* Adding a field breaks no compiles: every
`config.MaybeSecret{...}` literal in the tree uses field names (checked across
all `_test.go` and production files; the only unusual form is the redundant
`config.MaybeSecret{Plain: "..."}` inside map literals at
`internal/workspace/override_test.go:196` etc., which is still keyed).
`MaybeSecret` is already non-comparable — `secret.Value` holds `b []byte`
(`internal/secret/value.go:67-70`) — so no `==` comparison exists to break, and
there is no `reflect.DeepEqual` over configs in `internal/config`,
`internal/workspace`, or `internal/vault/resolve`. Nothing serializes
`MaybeSecret` into `state.json` (`internal/workspace/state.go:92-132` carries
no config values).

### Option B — a parallel structure alongside the resolved map

**Mechanism.** `ResolveWorkspace` and `ResolveGlobalOverride` gain a second
return value: an ordered slice (or map) of shortfall records keyed by the
dotted location the walker already builds — `"env.secrets.GH_TOKEN"`, from
`resolveOne`'s `location` parameter (`resolve.go:390`, `:470`). That key space
already aligns with `checkRequiredKeys`'s `Scope` strings (`required.go:55-79`
uses `"env.secrets"`, `"repos.%s.env.vars"`, etc.), so the join is a string
split, not a new index.

Note that keying by *env var name alone*, as the brief phrases it, is not
viable: the same name can appear in `env.vars`, `env.secrets`,
`claude.env.secrets`, `repos.<n>.env.secrets` and `instance.env.secrets`, and
`MergeOverrides` deliberately lets a repo-level entry win over a
workspace-level one of the same name (`override.go:143-157`). The key must be
`(scope, name)`.

**Code impact.** The transport is the cost. Counting the sites:

1. `resolve.ResolveWorkspace` / `ResolveGlobalOverride` signatures (2), plus
   walker accumulation state.
2. Three call sites: `apply.go:1074`, `effective_config.go:74`,
   `effective_config.go:92`.
3. `ResolveAndMergeEffectiveConfig` goes from four returns to five
   (`effective_config.go:65-72`) — it is already at four, which is a smell but
   not a blocker. One production call site (`apply.go:1308`).
4. `checkRequiredKeys` gains a parameter (`required.go:47`), one call site.
5. `repoMaterializeInputs` gains a field (`worktree_content.go:39-64`), two
   construction sites (`apply.go:1564`, `worktree_content.go:530`).
6. `MaterializeContext` gains a field (`materialize.go:69-142`), three
   non-test construction sites (`worktree_content.go:140`,
   `workspace_context.go:318`, `root_materializer.go:163`).
7. `runRepoMaterializers` copies it through (`worktree_content.go:140`).
8. `ResolveEnvVars` and `resolveClaudeEnvVars` consult it
   (`materialize.go:1140`, `:925`).

Roughly a dozen sites, several of them signature changes to functions with
production and test callers. `deepcopy.go` needs no change (the structure lives
outside the config), but the *caller* must clone it if it is ever mutated after
return.

**The three merge hazards.** These are the substance of the objection, not the
site count.

- *Cross-layer key collision.* The overlay pass at `apply.go:1074` wraps the
  overlay in a `tmpCfg` whose locations are `"env.secrets.X"` — identical to
  the team pass's locations at `effective_config.go:74`. Two shortfall sets
  with colliding keys, produced 230 lines apart, must be unioned by hand with
  layer disambiguation the resolver does not currently supply.
- *Stale entries after merge.* If the team layer fails to resolve `GH_TOKEN`
  and the personal overlay supplies it, `MergeGlobalOverride` (`override.go:494`)
  produces a config with a real value while the parallel structure still says
  "unresolved". The report would name a key that is present in the generated
  file. The fix is a post-merge reconciliation pass that drops any entry whose
  merged `Values` slot is now populated — which is a re-implementation, by hand,
  of the last-layer-wins semantics that Option A gets from the assignment
  operator. This is not hypothetical: it is the normal case for a maintainer
  whose overlay supplies what the public config declares.
- *Per-repo merge.* `MergeOverrides` (`override.go:46`) rewrites the key space
  from `repos.<n>.env.secrets.X` to `env.secrets.X` for the repo being
  materialized. The materializer's lookup must apply the same precedence rule,
  or it will omit a key the repo override actually supplied.

**Trade-offs.**

*For.* `MaybeSecret` stays a two-state sum type with its documented invariant
intact. The reason lives in a structure designed to be printed, next to nothing
secret, which makes R18 and R20 easy to keep. The report assembly reads one
purpose-built input rather than walking eleven config scopes. It is the option
a reviewer would call "cleanest" on a whiteboard.

*Against.* It re-implements config merge semantics in a second, parallel
namespace, and gets one shot at getting three merge interactions right. The
stale-entry hazard produces a wrong report (naming a key that resolved) rather
than a crash, which is the failure mode least likely to be caught. And the
plumbing terminates at `MaterializeContext`, a struct that already carries
twelve fields of threaded state and whose growth is itself a maintenance signal.

### Option C — a typed error carried up and aggregated by the caller

**Mechanism.** `resolveOne` keeps returning `error`, but returns a typed
`*resolve.Shortfall` implementing `error`. The walker stops bailing on first
error and accumulates: `walkTable` (`resolve.go:388-397`), `walkEnv` (`:366`),
`walkClaudeEnv` (`:376`), `walkSettings` (`:402`), `walkGlobalOverride`
(`:434`), and the twelve `if err := ...; err != nil { return nil, err }`
blocks in `ResolveWorkspace` (`:251-293`) and `ResolveGlobalOverride`
(`:342-350`) all change from propagate to collect. `ResolveWorkspace` then
returns `(cfg, *ShortfallSet)` where the second is a non-nil `error` **and**
the first is still usable.

**Code impact.** ~8 functions inside `resolve`, plus every call site must learn
the convention:

- `effective_config.go:79-81` currently does `if err != nil { return nil, nil,
  nil, err }` — it nils the config. Under Option C it must become "if
  `errors.As(&shortfallSet)`, keep the config and carry the set; otherwise
  bail", in two places (`:79` and `:97`).
- `apply.go:1078` wraps with `%w` (`fmt.Errorf("resolving overlay vault
  references: %w", resolveErr)`), so `errors.As` survives — but only by luck,
  and only because that one wrap used `%w`.
- `apply.go:1316` must do the same discrimination.

**Trade-offs.**

*For.* It preserves the resolver's existing error-shaped contract, so a future
strict mode (R12) is a one-line "return the set as a real error". It requires
no change to `MaybeSecret` and no new struct in `MaterializeContext`.

*Against.* Two objections, and the second is decisive.

First, it inverts Go's central error convention: a non-nil `error` that means
"use the result anyway". Every current and future call site must know the
convention or it will silently turn a partial success into a hard failure —
which is *precisely the bug class this PRD exists to eliminate*. There are
three call sites today and the pipeline has grown a new resolve call twice
already (the workspace-overlay pass at `apply.go:1074` is newer than the team
pass). A fourth added by someone who does not know the convention reintroduces
the wall.

Second, and fatally: **it does not solve the downstream half of the question.**
An aggregate error tells the caller *that* keys failed, but the materializer at
`materialize.go:1199` needs a per-key answer to "do I write this one". So the
aggregate has to be indexed by `(scope, key)` and threaded to
`MaterializeContext` — which is Option B's plumbing, plus Option C's convention
hazard. Option C is not a third mechanism; it is Option B with a worse
transport out of the resolver.

---

## Recommendation

**Option A: a nilable `Unresolved *Unresolved` field on `config.MaybeSecret`,
with report assembly and the R10 fatality decision living post-merge in
`internal/workspace` beside `checkRequiredKeys`, and the
no-binding-at-all case derived there rather than carried.**

Concretely:

1. `internal/config/maybesecret.go` gains `Unresolved *Unresolved` plus a
   `Reason` enum wide enough for R9's binary-absent / otherwise-unreachable
   split. Pointer so `MaybeSecret{}` is unchanged.
2. `resolveOne` marks instead of erroring at `resolve.go:513` (reclassified to
   unsatisfiable-declaration), `:539`, and `:550`; line 529 is untouched, which
   is R2a. Line 477 gains an `Unresolved != nil` guard for the double-resolve.
3. `internal/workspace/required.go` grows a report-assembly function that walks
   the same eleven scopes `checkRequiredKeys` already walks, joins each marked
   `Values` entry against the `Required`/`Recommended`/`Optional` description
   maps for its level and description (R6), adds the `!ok` shape-(a) keys as
   unsatisfiable-declaration, and sorts by `(scope, key)` for R7.
   `checkRequiredKeys` keeps its `(cfg, io.Writer)` signature and fails only on
   `ReasonProviderLacksKey` (R10).
4. `ResolveEnvVars` and `resolveClaudeEnvVars` skip marked entries (R2) and
   stop erroring on a missing promoted key (R11).

**Reason one: it is the only option whose carrier is merged by the code that
already merges values.** Four merge functions and two deep-copy paths sit
between the resolver and the materializer, and all six move `MaybeSecret`
values wholesale — `maps.Copy` in `deepcopy.go:175-194`, plain assignment in
`override.go:117/128/147/156/398-432/569-642/767-886`. Last-layer-wins is the
core semantic of this config system, and a marker riding inside the value gets
it exactly right for free, including the common case where a personal overlay
supplies what a public config could not. Options B and C must re-implement that
semantic by hand in a parallel namespace, and the failure mode when they get it
wrong is a report that names a key which is present in the file — a wrong
answer, not a crash.

**Reason two: it answers the `checkRequiredKeys` question with no plumbing at
all.** The brief asks whether the reason can reach `checkRequiredKeys` without
threading vault bundles into a function that takes only the config. Under
Option A the answer is that the signature does not change: the reason is
already inside `cfg`, because the resolver put it there and every merge carried
it. R10's "reachable provider does not hold this key" becomes a field read at
`required.go:118` rather than a new parameter, and `internal/vault` stays out
of `required.go`'s import list entirely.

A supporting point worth stating because it removes the apparent asymmetry:
Option A does **not** lose the unsatisfiable-declaration case. Shape (b) is
carried on the value like any other visited reference. Shape (a) — a key
declared required with no `Values` entry — is invisible to the resolver under
*all three* options, and is already detected at `required.go:118`. It is
derived post-merge in every design; that is not a cost Option A uniquely pays.

## Rejected alternatives

**Option B (parallel structure keyed alongside the resolved map)** — rejected
on merge fidelity, not on plumbing volume. The dozen threading sites are
tolerable; the three merge interactions are not. Cross-layer key collision
between the workspace-overlay pass (`apply.go:1074`) and the team pass
(`effective_config.go:74`), stale entries surviving `MergeGlobalOverride`, and
the key-space rewrite in `MergeOverrides` each require hand-written logic that
duplicates semantics the assignment operator already implements. B is the
better-looking design and the more fragile one. It becomes the right answer if
the reason ever needs to be per-*declaration* rather than per-*value* — see
open risks.

**Option C (typed error aggregated by the caller)** — rejected because it is
not a distinct mechanism downstream. It answers only "how does the reason leave
the resolver", and its answer requires a non-nil `error` that callers must
ignore. Downstream it still needs B's indexed structure and B's plumbing to
reach the materializer. Net: B's costs plus a convention that invites exactly
the fail-fast regression this PRD exists to remove.

**A sentinel string in `MaybeSecret.Plain`** (e.g. `Plain: "\x00unresolved"`) —
not evaluated at length because it fails immediately: `maybeSecretString`
(`materialize.go:38`) would write the sentinel into `.local.env`, and
`isEmptyMaybeSecret` (`required.go:135`) would report the key as *present*.
Recorded only so the design shows it was considered.

## Open risks

1. **The three-state `MaybeSecret` invariant is now load-bearing and
   unenforced.** `maybesecret.go:8-23` documents a two-state sum type. Adding a
   third state means every `ms.Plain == ""` and every `isEmptyMaybeSecret` call
   is a place a future change can mishandle. Mitigation: update the type's doc
   comment as part of the change (not after), guard `maybeSecretString`
   explicitly rather than letting it return `""`, and add the direct
   `required.go` unit tests that do not exist today. Residual risk: real.

2. **`String()` / `MarshalText()` on a marked value return the empty string.**
   Nothing in the tree currently `MarshalText`s a config (checked:
   `state.json` carries no config values), so this is latent rather than live.
   But a marked value formatted with `%s` renders as empty, which reads as "set
   to empty" in any log line. Decide explicitly what these two methods do and
   test it.

3. **`Unresolved` is shared by pointer across deep copies.** `maps.Copy` in
   `cloneEnvVarsTable` (`deepcopy.go:175-194`) copies the pointer, not the
   pointee. Safe only while `Unresolved` is treated as immutable after
   construction. Enforce by having no setters and constructing it in exactly
   one place inside `resolveOne`.

4. **R9's two-cause split does not exist yet below the resolver.**
   `internal/vault/infisical/subprocess.go:139-146` collapses "binary failed to
   start" and "auth failure / non-zero exit" into the same
   `vault.ErrProviderUnreachable`. The `Reason`/`Detail` enum this decision
   defines must be wide enough to carry the distinction, but a sibling decision
   has to add the sentinel (or an `errors.As`-able type) that lets `resolveOne`
   populate it. If that sibling lands narrower than assumed here, `Detail`
   arrives permanently unpopulated and an acceptance criterion fails.

5. **`SourceEntry` behaviour for an omitted key is unspecified by this
   decision.** `sourceForMaybeSecret` (`materialize.go:1263`) currently emits a
   plaintext-kind entry hashing `""` for any non-secret value; feeding it a
   marked value would put a constant hash into `ManagedFile.SourceFingerprint`.
   The recommendation says "emit nothing", but the interaction with R4
   (record removed when a later apply resolves the key) and with the
   fingerprint-driven cleanup in `refreshWorktreeEnvs` (`apply.go:1600-1620`)
   belongs to the R3/R4 record-format decision and should be checked there, not
   assumed here.

6. **The biggest one: the reason is attached to a *value*, and the
   unsatisfiable-declaration case has no value.** Today the split is clean —
   shape (a) is derived at `required.go:118`, everything else is carried. If a
   future requirement needs a reason attached to a declaration that was never
   bound (say, "this required key has no provider *and* here is which layer
   declared it"), Option A has nowhere to put it and the derivation at the
   check point has to grow its own parallel record — at which point B's
   structure reappears, half-built, alongside A's field. Watch for it; the
   trigger is any requirement that needs per-declaration provenance rather than
   per-value provenance.
