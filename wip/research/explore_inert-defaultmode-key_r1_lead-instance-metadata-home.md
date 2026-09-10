# Lead: niwa's own instance metadata as a home for the signal

## Findings

### 1. The metadata store

niwa owns exactly one structured, versioned state artifact, plus a small set of
unstructured markers and side stores.

**`.niwa/instance.json` — `workspace.InstanceState`** (`internal/workspace/state.go:93-147`).
Path helper `statePath` joins `StateDir` (`.niwa`) + `StateFile` (`instance.json`)
(`state.go:27,35,240-242`). Written by `SaveState` (`state.go:281-297`), read by
`LoadState` (`state.go:260-278`).

Current fields, and the shape they establish:

```go
type InstanceState struct {
	SchemaVersion  int     `json:"schema_version"`
	ConfigName     *string `json:"config_name"`
	InstanceName   string  `json:"instance_name"`
	InstanceNumber int     `json:"instance_number"`
	Root           string  `json:"root"`
	Detached       bool    `json:"detached,omitempty"`
	SkipGlobal     bool    `json:"skip_global,omitempty"`
	OverlayURL     string  `json:"overlay_url,omitempty"`
	NoOverlay      bool    `json:"no_overlay,omitempty"`
	OverlayCommit  string  `json:"overlay_commit,omitempty"`
	NoWorktreeDelegation bool `json:"no_worktree_delegation,omitempty"`
	EphemeralSessionMode bool `json:"ephemeral_session_mode,omitempty"`
	ConfigNameOverride   string `json:"config_name_override,omitempty"`
	Created            time.Time
	LastApplied        time.Time
	ManagedFiles       []ManagedFile
	Repos              map[string]RepoState
	Shadows            []Shadow                    `json:"shadows,omitempty"`
	DisclosedNotices   []string                    `json:"disclosed_notices,omitempty"`
	ConfigSource       *ConfigSource               `json:"config_source,omitempty"`
	AuthSources        map[string]AuthSourceRecord `json:"auth_sources,omitempty"`
	TrustKeys          []string                    `json:"trust_keys,omitempty"`
}
```

Write sites: `Applier.Create` (`internal/workspace/apply.go:569-588`),
`Applier.Apply` (`apply.go:763-785`), `saveWorkspaceRootDisclosures`
(`apply.go:2717-2738`), and `niwa init`'s pre-apply `buildInitState`
(`internal/cli/init.go:1029-1036`). Read sites are numerous — 18 `LoadState`
call sites in `internal/cli` alone (reset, status, destroy, create,
completion, session lifecycle, audit).

The same file also lives at the **workspace root**: `init` writes one there to
carry init-time state (`state.go:299-308` explains that `workspace.toml`, not
`instance.json`, is what distinguishes a root from an instance).

**Other niwa-owned artifacts:**

| Artifact | Path | Shape | Written / read |
|---|---|---|---|
| Session mapping store | `<root>/.niwa/sessions/<uuid>.json` | `workspace.SessionMapping` (`session_map.go:47-96`) | dispatch + SessionStart hook write; reaper, `niwa list`, session lifecycle read |
| Snapshot provenance marker | `<root>/.niwa/.niwa-snapshot.toml` | `workspace.Provenance` (TOML, `provenance.go:29-38`) | snapshot writer; read by status, drift, reset, guardrail |
| Dispatch pending marker | `<instance>/.niwa/dispatch-pending` | RFC3339 timestamp, one line | `writeDispatchMarker` (`dispatch.go:1059-1066`); read by reaper backstop |
| Dispatch retain marker | `<instance>/.niwa/dispatch-retain` | free-text reason | `writeDispatchRetainMarker` (`dispatch.go:1081-1087`) |
| Prompt spill dir | `<instance>/.niwa/dispatch-prompts/` | prompt bodies | `dispatch_spill.go:96` |

There is **no lockfile** and no separate mapping DB. `instance.json` is the
only schema'd, versioned store.

### 2. Is the workspace config reachable at dispatch time? (the important one)

**Short answer: the raw config is already a live variable in the same function;
the *effective* config the materializer actually used is computed a few frames
down and thrown away. Neither is free, but neither needs a re-resolve.**

**(a) The raw workspace.toml is already loaded, in scope, ~230 lines earlier.**
`runDispatch` step 2b loads it to resolve the dispatch harness:

```go
wsConfigPath := filepath.Join(workspaceRoot, workspace.StateDir, workspace.WorkspaceConfigFile)
var wsConfig *config.WorkspaceConfig
wsCfg, cfgErr := config.Load(wsConfigPath)
if cfgErr == nil {
	wsConfig = wsCfg.Config
}
```
(`internal/cli/dispatch.go:319-325`)

`wsConfig` is still in scope at the derivation site (`dispatch.go:550-557`).
Reading `wsConfig.Claude.Settings["permissions"]` there costs **zero new I/O**.

**(b) But the raw config is not the input the settings document was built from.**
The materialized `permissions.defaultMode` is
`permissionsMapping[MergeInstanceOverrides(cfg).Claude.Settings["permissions"]]`
(`materialize.go:312-315`, `682-691`; called from
`RootSettingsMaterializer.Materialize`, `workspace_context.go:337-341,425-437`),
where `cfg` has been through four transformations that `config.Load` alone has not:

1. `ReconcileAndReloadConfig` — source reconciliation / possible refetch
   (`instance_from_hook.go:454`).
2. `MergeWorkspaceOverlay` (`apply.go:1191`, `override.go:710`). For settings the
   documented rule is **"Claude.Settings: base wins per key"** (`override.go:697`),
   so an overlay can only *add* a `permissions` key the base lacks. Low risk.
3. `MergeGlobalOverride` via `ResolveAndMergeEffectiveConfig`
   (`effective_config.go:102`). Here the rule is the opposite —
   **"Settings: global wins per key"** (`override.go:534-551`). A machine-level
   personal overlay can *override* `permissions`, subject only to
   `[vault].team_only` locking. **This is the case where reading raw
   `workspace.toml` at dispatch would give the wrong answer.**
4. `MergeInstanceOverrides` — the `[instance]` block, which overwrites
   `Claude.Settings` per key (`override.go:210-216`).

Plus vault resolution: `SettingsConfig` values are `MaybeSecret`
(`config.go:437-441`), so `permissions` may be a `vault://` ref. An unresolved
ref fails closed in `buildSettingsDoc` ("unknown permissions value",
`materialize.go:684-687`; pinned by
`internal/workspace/worktree_secret_ref_test.go:19-38`).

**(c) The effective config *is* computed at dispatch time — in the same call.**
`runDispatch` step 6 calls `provisionInstanceFunc(...)`
(`dispatch.go:476`) → `realProvisionInstance`
(`internal/cli/instance_from_hook.go:428-510`), which does the full
load + reconcile + apply, ending with `applier.Create(ctx, cfg, ...)`
(`instance_from_hook.go:501`). Inside `Create`, the pipeline produces the
fully merged, vault-resolved `cfg` and hands it to the materializer that writes
the very `defaultMode` value dispatch then reads back off disk, milliseconds later.

So the round trip is: **effective config → settings.json → parse → derive**, when
**effective config → derive** was available in-process the whole time.

The plumbing needed is small and mechanical:

- `provisionResult` (`instance_from_hook.go:92-102`) carries only `Name`, `Path`,
  `Keys`. Add one field, e.g. `PermissionPosture string` (or the whole effective
  `SettingsConfig`).
- `Applier.Create` returns `(string, error)` (`apply.go:501`); the effective
  posture would need to surface from `pipelineResult`
  (`apply.go:344-380`), which is precisely the established mechanism — that struct
  already carries `shadows`, `authSources`, `trustKeys`, `overlayURL`,
  `disclosedNotices` out of the pipeline for exactly this "the pipeline knew it,
  the caller needs it" reason.
- `provisionInstanceFunc` is a package var overridden by tests
  (`instance_from_hook.go:118`), and has three production callers: dispatch
  (`dispatch.go:476`), the SessionStart hook (`instance_from_hook.go:183`), and
  `niwa watch` (`watch.go:790`). A new result field is additive for all three.

**(d) What re-resolving instead would cost.** Calling
`ResolveAndMergeEffectiveConfig` a second time in the CLI means re-opening vault
bundles and re-hitting the provider — expensive, and it can produce a *different*
answer if a secret rotated between the apply and the read. That is the fragile
option and should not be confused with option (c).

**(e) What the current output-read buys that an input-read would lose.** Two
things, both thin:

- **Drift.** The file read reflects a hand-edit made after apply. For a dispatch
  instance the file is seconds old, so this is near-vacuous — but it is not
  vacuous everywhere: `internal/watch/containment.go:201-228`
  (`ApplyReviewSettings`) *rewrites* `permissions.defaultMode` to `"default"` in
  an already-materialized instance's `settings.json` after the apply. The
  settings document is a shared mutable surface with more than one writer;
  `instance.json` is niwa's alone.
- **Fail-closed on a malformed document.** R7's "degrade to nothing derived"
  currently rides `readInstanceSettings` returning an error. An input-read would
  need its own equivalent (a `nil` `wsConfig`, an unknown value).

**(f) The design record never considered this.**
`docs/designs/current/DESIGN-dispatch-permission-mode.md` weighs exactly three
options (lines 150-205): derive inline from one moved `readInstanceSettings`
call (A, chosen), a second independent read of the same file (B), and pushing
the read into `buildDispatchPassthrough` (C). All three read the *output*.
DD1 (line 126-128) asserts the constraint — "must come from the instance's
already-materialized `.claude/settings.json`, never from re-parsing
`workspace.toml` in the CLI layer" — and the only reason offered anywhere is
"re-parsing in the CLI layer", i.e. the cost of a second parse. That objection
dissolves entirely under (a) and (c). AC6 and
`TestDispatch_PermissionMode_MaterializedSettingsWinOverWorkspaceToml`
(`internal/cli/dispatch_permissionmode_test.go:140-180`) pin the constraint with
a two-direction fixture, so changing it is a deliberate test rewrite, not a
silent drift.

### 3. Precedent for a niwa-owned signal

**Precedent for storing decisions in `instance.json` and reading them back: strong
and repeated.** `SkipGlobal`, `NoOverlay`, `NoWorktreeDelegation`,
`OverlayURL`, `EphemeralSessionMode`, `ConfigNameOverride`, `TrustKeys`,
`AuthSources` are all "niwa decided/discovered something once, wrote it to its
own state, and reads it back later." `DESIGN-global-config.md:54` states the
principle outright:

> **Testable opt-out.** The `SkipGlobal` flag in instance state must be
> inspectable without running apply -- i.e., stored in `.niwa/instance.json`,
> not inferred from the absence of global config registration.

That is the same argument the current exploration is making, already won once.

**Precedent for the opposite shape — a niwa-only key riding the settings
document — also exists, and is explicit.**
`config.KeepAliveOnDispatchKey` (`internal/config/config.go:451-459`):

> Unlike `RemoteControlAtStartupKey` it is a niwa-defined key, not a Claude Code
> one -- Claude Code ignores it in settings.json; the materializer emits it there
> so the dispatch keep-alive resolver can read the downstream decision back,
> exactly the same read-back seam the remote-control resolver rides.

Emitted at `materialize.go:725-739`, read at
`dispatch_keepalive.go:94-105` via the same `instanceSettings` projection
(`dispatch_plugins.go:186-204`). So "settings.json is also a niwa↔niwa channel"
is established policy, not an accident.

**The single best precedent is `EphemeralSessionMode`, because it is both at
once.** It is stored authoritatively in `instance.json`
(`state.go:111-119`) and read back from there by the only consumer that acts on
it (`session_map.go:37-42`, called at `instance_from_hook.go:269`). It is
*also* emitted into the root settings document as `ephemeralSessionMode`
(`root_materializer.go:274-276`) — and **nothing reads that copy**. The settings
key is write-only decoration; the load-bearing copy lives in niwa's own metadata.
That is precisely the target shape for the permission signal, already built.

**Counter-precedent worth naming:**
`docs/designs/current/DESIGN-codex-dispatch-posture-persistence.md` frontmatter
rejects persisting a posture grant:

> Persisting a trust stanza would fix re-entry too ... and is rejected on
> re-entry-specific grounds: retraction that depends on a reap that may never
> run, the developer's configuration as shared mutable state, and a grant that
> outlives the session it vouched for.

Its objections are about writing into the *developer's own* Codex config, not
niwa's instance state (which dies with the instance), so it does not transfer
cleanly — but "posture is a per-invocation decision, not persisted state" is on
the record as a niwa principle and any proposal to store a permission grant will
have to answer it.

### 4. Cost of a new field

**Versioning exists but is thin, and works in favour of a new field.**
`SchemaVersion = 4` (`state.go:53`). The comment block at `state.go:41-53`
documents the whole contract: every version bump is additive, every new field
carries `omitempty`, and there are **no migration functions** — v1/v2/v3 files
unmarshal cleanly with new fields zero-valued and are rewritten at the current
version on the next `SaveState` (`state.go:244-254`). Forward versions are
rejected outright (`state.go:271-275`).

So the mechanical cost of a new `omitempty` field is: one struct field, and
adding it to both `InstanceState` literals — `Create` (`apply.go:569-584`) and
`Apply` (`apply.go:763-781`). Whether the version constant even moves is a
judgement call; the v3→v4 precedent bumped it for a purely additive `omitempty`
map, so bumping to 5 would match practice.

**The real cost is the carry-forward trap, not the schema.** `Apply` rebuilds
`InstanceState` from scratch and must *explicitly* copy each init-time field
forward from `existingState` (`apply.go:771-773` carries `SkipGlobal`,
`NoOverlay`, `NoWorktreeDelegation`). Any field not listed is silently dropped
on the next apply. A new field that is recomputed from config every apply is
immune; a new field that is not would need a line in that literal, and the
failure mode of forgetting it is silent data loss.

**Backward compatibility for an instance materialized by an older niwa is
essentially a non-issue for *this* signal.** The dispatch derivation reads the
instance it just created seconds earlier (`instancePath := res.Path`,
`dispatch.go:487`; read at `dispatch.go:550`). A dispatch instance is always
materialized by the running binary, so an older-niwa state file can never reach
this reader. Any long-lived instance would converge on its next `niwa apply`.

### 5. The ephemeral-session angle

Ephemeral instances go through **the same** `provisionInstanceFunc` →
`realProvisionInstance` → `Applier.Create` path as dispatch instances
(`instance_from_hook.go:183` vs `dispatch.go:476`), so they get an identical
instance-root `settings.json` with the same `permissions.defaultMode`.

**But they never reach the derivation, and structurally cannot.** The derivation
lives in `runDispatch` only (`dispatch.go:550-557`), and it works by mutating
`dispatchPermissionMode` before argv is built. A hook-provisioned ephemeral
instance is created by a `SessionStart` hook — the Claude session is *already
running*; there is no argv left to add `--permission-mode` to. The guide states
the consequence for the root document plainly
(`docs/guides/ephemeral-session-instances.md:327-333`):

> settings resolve at launch and cannot be scoped per session, so a root-level
> bypass-permissions posture applies to **every** session launched at the root

Two lifecycle facts change the calculus:

- **The workspace root's own `settings.json` carries `permissions.defaultMode`
  too** (`root_materializer.go:97-107,249-256`), and that is the document a
  root-launched session actually reads at startup. It is inert for the same
  2.1.258 reason, and no derivation compensates for it anywhere — the root has
  no launch seam niwa controls.
- **Instances outlive the task.** Ephemeral instances are kept while the session
  exists, including idle-and-resumable, and are reclaimed only on session
  deletion (`DESIGN-ephemeral-session-instances.md:270-276`). A resumed session
  re-launches through `claude --resume`, which does not re-run the derivation
  either. Amusingly, the design already knows `SessionEnd` can fire with
  `reason: bypass_permissions_disabled` (`DESIGN-ephemeral-session-instances.md:266`).

`niwa watch` is the third provisioning caller and a third gap: it calls
`buildDispatchPassthrough(claudeLaunchSpec().Flags, slug, "")`
(`watch.go:836`, `watch.go:576`) with `dispatchPermissionMode` at its zero value
and never runs the derivation, so a watch review session gets no
`--permission-mode` at all.

## Implications

1. **"Read the input instead of the output" is available and cheap, and the
   design record never evaluated it.** The correct input is not raw
   `workspace.toml` (which misses the personal-overlay layer that explicitly
   wins per key) but the effective config the provisioning call already
   computed. Surfacing it through `provisionResult` follows the exact pattern
   `pipelineResult` already uses for five other values. This is very likely
   smaller than adding a persisted field, and it removes a file read rather than
   adding a store.

2. **If a persisted signal is wanted anyway, `instance.json` is the
   uncontroversial home** — but note that niwa has *two* live precedents pointing
   in opposite directions, and `ephemeralSessionMode` demonstrates both
   simultaneously, with the authoritative copy in `instance.json` and the decorative
   copy in the settings document. A third option, symmetric with
   `keepAliveOnDispatch`, is a niwa-defined key inside the same settings document
   (e.g. `dispatchPermissionMode`), which keeps the read path byte-identical and
   only changes what the document claims.

3. **The narrowest possible fix for "the document makes a claim it cannot keep"
   is to stop writing `permissions.defaultMode` and write niwa's own key
   instead** — one line in `buildSettingsDoc` (`materialize.go:688-690`) plus the
   reader's JSON tag. That fixes the falsehood without touching state schema,
   dispatch ordering, or the provision result. It does *not* fix the workspace
   root document (which has no reader to compensate) or the watch/ephemeral gaps.

4. **Any change here should reckon with three unfixed consumers of the same
   posture**: the workspace-root settings document, `niwa watch`, and resumed
   ephemeral sessions. All three still rely on a channel Claude Code no longer
   honors, and only the dispatch path was patched.

## Surprises

- **`workspace.WorkerPermissionMode` (`internal/workspace/permissions.go:25-39`)
  is dead code with zero production callers** — only its own test calls it. The
  design doc says so explicitly ("its only caller, the removed mesh daemon, is
  gone", `DESIGN-dispatch-permission-mode.md` Security section) and rejects
  reusing it because its `acceptEdits` fallback would over-grant. It is a
  ready-made second reader of the key, sitting unused, and would go stale
  silently under any change to the signal's shape.

- **`ephemeralSessionMode` is already written into a settings document and read
  by nobody** (`root_materializer.go:276`); the real read is against
  `instance.json` (`session_map.go:37-42`). The exploration's proposed shape
  already exists in the codebase for a different flag.

- **The personal global override wins per key on `Claude.Settings`**
  (`override.go:534-551`) while the *workspace* overlay loses per key
  (`override.go:697`). Anyone reading `workspace.toml` directly at dispatch time
  would silently get the wrong posture for exactly the machine-level override
  case.

- **`internal/watch/containment.go:189-192` documents the hard-deny posture as
  relying on "the `bypassPermissions` the dispatch applies"** — but `niwa watch`
  never runs the derivation and passes no `--permission-mode`. The comment
  describes a channel that no longer works on a path that never had the
  replacement. The resulting session is *less* permissive than the comment
  assumes, so the security posture is not weakened, but the stated reasoning is
  wrong.

- **`dispatchPermissionMode` is a package-level global that the derivation
  mutates** (`dispatch.go:555`), and the codebase elsewhere is careful about
  exactly this — `CloneWorkers` was made a parameter rather than package state
  "because concurrent dispatches share the process and would otherwise overwrite
  each other's setting" (`instance_from_hook.go:114-117`). The permission mode
  has the identical hazard and did not get the identical treatment.

- **`instance.json` lives at the workspace root as well as in instances**, and
  `workspace.toml` — not the presence of `instance.json` — is what distinguishes
  them (`state.go:299-308`). Any root-scoped variant of a new field inherits
  that ambiguity.

## Open Questions

- Should the derivation move earlier still, into the provisioning path itself, so
  the SessionStart-hook and `niwa watch` callers can act on the same resolved
  posture — or is a launch-seam-only fix the deliberate scope?
- Does anything need to compensate for the **workspace-root** settings document's
  inert `permissions.defaultMode`, given the root has no niwa-controlled launch seam?
- Is the `[claude.settings] permissions` key still the right *authoring* surface
  at all, or should the workspace declare a dispatch posture in its own vocabulary
  now that the key no longer maps onto a Claude Code setting that works?
- Does `WorkerPermissionMode` get deleted as part of this, and does its deletion
  belong in the same change?
- Does a resumed dispatch/ephemeral session need the posture re-applied, and if so
  through which of `dispatch_reentry.go`'s printed forms?

## Summary

niwa's one versioned, self-owned store is `.niwa/instance.json`
(`InstanceState`, schema v4, additive `omitempty` fields with no migration
functions), and it already holds several "niwa decided this once, reads it
back later" flags, so a new field there is cheap and precedented. But the
stronger finding is on sub-question 2: `runDispatch` already holds the raw
workspace config in a live variable 230 lines above the derivation
(`dispatch.go:319-325`), and the fully merged, vault-resolved effective config —
the actual input the materializer turned into `permissions.defaultMode` — is
computed inside the same `provisionInstanceFunc` call moments earlier and simply
discarded, so surfacing it through `provisionResult` needs no re-resolve and
follows the pattern `pipelineResult` already uses for five other pipeline
outputs; the governing design doc weighed three options and all three read the
output, with the only stated reason being an objection to re-parsing that this
plumbing removes. The most surprising precedent is `ephemeralSessionMode`, which
niwa already writes into a settings document that nobody reads while keeping the
authoritative copy in `instance.json` — the exact shape under discussion, already
shipped for a different flag.
