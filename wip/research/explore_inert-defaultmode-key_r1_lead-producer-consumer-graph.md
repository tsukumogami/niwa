# Lead: The producer/consumer graph for permissions.defaultMode inside niwa

All line numbers re-derived from the worktree at
`public/niwa/.claude/worktrees/docs+inert-defaultmode-key` (HEAD includes
`d25ad4d fix: dispatch-permission-mode (#277)`).

## Findings

### 0. The graph in one picture

```
                         workspace.toml
                         [claude.settings] permissions = "bypass" | "ask"
                         [instance.claude.settings] / [repos.<n>.claude.settings]
                                   |
                        config.SettingsConfig (map[string]MaybeSecret)
                                   |
                        MergeInstanceOverrides / ctx.Effective.Claude.Settings
                                   |
                 workspace.buildSettingsDoc  (materialize.go:669)
                 permissionsMapping: bypass->bypassPermissions
                                     ask   ->askPermissions
                 (unknown value => hard error; absent => NO permissions key at all)
                                   |
        +--------------------------+---------------------------+
        |                          |                           |
   PRODUCER A                 PRODUCER B                  PRODUCER C
   RootSettingsMaterializer   SettingsMaterializer        writeRootSettings
   workspace_context.go:337   materialize.go:1214         root_materializer.go:238
        |                          |                           |
  <instanceRoot>/.claude/    <repoDir>/.claude/          <workspaceRoot>/.claude/
     settings.json            settings.local.json           settings.json
   (SettingsAtInstanceRoot)   (SettingsInRepo)            (SettingsAtWorkspaceRoot)
        |                          |                           |
        |                     [NO niwa reader]            [NO niwa reader]
        |                     Claude Code project          Claude Code reads it
        |                     scope only                   for root sessions
        |
        +--> PRODUCER D (mutating overwrite, watch only):
        |    watch.ApplyReviewSettings  internal/watch/containment.go:201
        |    sets permissions.defaultMode = "default" when (sandbox && ask)
        |    verified back by watch.VerifyReviewSettings  containment.go:290
        |
        +--> CONSUMER 1 (LIVE): cli.readInstanceSettings  dispatch_plugins.go:228
        |                       -> runDispatch derivation  dispatch.go:550-557
        |                       -> dispatchPermissionMode -> `--permission-mode`
        |
        +--> CONSUMER 2 (DEAD): workspace.WorkerPermissionMode  permissions.go:25
```

### 1. `buildSettingsDoc` — the single shared producer of the key

`internal/workspace/materialize.go:669` is the only function in the tree that
emits `permissions.defaultMode` from workspace config. The mapping is a
two-entry table at `internal/workspace/materialize.go:310-315`:

```go
// permissionsMapping translates niwa permission values to Claude Code
// settings.local.json permission mode strings.
var permissionsMapping = map[string]string{
	"bypass": "bypassPermissions",
	"ask":    "askPermissions",
}
```

The emission site, `internal/workspace/materialize.go:681-691`:

```go
	var permissions map[string]any
	if perm, ok := cfg.Settings["permissions"]; ok {
		permStr := maybeSecretString(perm)
		mapped, known := permissionsMapping[permStr]
		if !known {
			return nil, fmt.Errorf("unknown permissions value %q", permStr)
		}
		permissions = map[string]any{
			"defaultMode": mapped,
		}
	}
```

Answers to the lead's three questions:

- **Accepted workspace-declared values:** exactly `"bypass"` and `"ask"`. Any
  other string is a hard error that fails the whole apply
  (`unknown permissions value %q`). The value passes through `maybeSecretString`
  first, so a `vault://` reference could technically back it.
- **What they map to:** `bypassPermissions` and `askPermissions`.
- **When a workspace declares nothing:** the `permissions` map is never created
  from settings, so the document carries **no** `permissions.defaultMode` key at
  all. The only other way a `permissions` block appears is the worktree-delegation
  deny fallback at `internal/workspace/materialize.go:698-703`, which adds
  `permissions.deny` into the same map without touching `defaultMode` (the
  comment at :677-680 spells out that the two keys are independent, and
  `internal/workspace/materialize_worktree_test.go:190-194` pins the coexistence).

**`"askPermissions"` is not a Claude Code permission mode.** Claude Code's
`permissions.defaultMode` vocabulary is `default | acceptEdits | plan |
bypassPermissions`. `askPermissions` has never been a member. So the `"ask"`
branch of the mapping has, since it was written, emitted a value the harness
cannot interpret. It is pinned by `internal/workspace/materialize_test.go:434`
and treated as a real fixture in `internal/workspace/permissions_test.go:26-27`.
This is documented as intended behavior in `docs/prds/PRD-config-distribution.md:197`
and `docs/designs/archive/DESIGN-worker-permissions.md:78` — nobody caught it.

### 2. The three settings documents and their scopes

Scope-to-filename is owned by `internal/agentplan/settings.go:29-67`:

```go
func (s SettingsScope) fileName() (string, bool) {
	switch s {
	case SettingsAtWorkspaceRoot, SettingsAtInstanceRoot:
		return "settings.json", true
	case SettingsInRepo:
		return "settings.local.json", true
	...
```

The brief's claim holds and is centrally enforced: **root gets `settings.json`,
repos get `settings.local.json`**. The doc comment at `settings.go:29-36`
gives the reason — instance and workspace roots are non-git directories, a
repository checkout takes the `.local` form so it is not committed. All three
producers route through `agentplan.SettingsPlan` (`internal/agentplan/settings.go:113`),
which marshals and declares one `OpWriteFile` entry tagged
`Capability: ApprovalPosture`.

**Producer A — instance root.** `RootSettingsMaterializer.Materialize`,
`internal/workspace/workspace_context.go:337`. Builds via `buildSettingsDoc` at
`workspace_context.go:431` with `Settings: effective.Claude.Settings`
(`MergeInstanceOverrides(cfg)` at :340), declares
`Scope: agentplan.SettingsAtInstanceRoot` at `workspace_context.go:451-455`.
Path: `<instanceRoot>/.claude/settings.json`. Registered in the delivery table at
`internal/workspace/delivery_binding.go:37` under `agentplan.DeliveryRootSettings`;
also instantiated directly at `internal/workspace/apply.go:1587`.

**Producer B — per-repo.** `SettingsMaterializer.Materialize`,
`internal/workspace/materialize.go:1214`. `buildSettingsDoc` at :1248 with
`Settings: ctx.Effective.Claude.Settings` (so a `[repos.<name>.claude.settings]`
override lands here), declares `Scope: agentplan.SettingsInRepo` at :1271-1275.
Path: `<repoDir>/.claude/settings.local.json`. Note the early return at
`materialize.go:1243-1246`: with no settings, hooks, env, plugins, marketplaces,
worktree-delegation decision, work-summary hooks, or pr-body hook, **no file is
written at all**.

**Producer C — workspace root.** `writeRootSettings`,
`internal/workspace/root_materializer.go:238`. `buildSettingsDoc` at :249, again
fed `effective.Claude.Settings` from `MergeInstanceOverrides(cfg)` at :239, so
the root's `defaultMode` is byte-identical to what an instance gets — the comment
at `root_materializer.go:224-232` says so explicitly. Declares
`Scope: agentplan.SettingsAtWorkspaceRoot` at :278-282. Path:
`<workspaceRoot>/.claude/settings.json`. Unlike the other two this document is
*not* managed-file-tracked (`SettingsScope.managed()`, `settings.go:70-75`).

**Producer D — `niwa watch`, a mutating overwrite of Producer A's file.**
`watch.ApplyReviewSettings`, `internal/watch/containment.go:201`, read-modify-writes
`<instancePath>/.claude/settings.json` (`containment.go:202`). At
`containment.go:218-227`:

```go
	if sandbox && ask {
		perms, _ := settings["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		perms["defaultMode"] = "default"
		settings["permissions"] = perms
	}
```

So the same on-disk key has a **second, unrelated producer** that writes a value
(`"default"`) outside `permissionsMapping`'s range entirely. It is live: called
from `internal/cli/watch.go:557` (continue path) and `:831` (fresh stage path),
with `ask` from `resolveAskPosture` (`internal/cli/watch.go:921-944`).

### 3. Consumer 1 (LIVE): `runDispatch`'s `--permission-mode` derivation

`internal/cli/dispatch.go:550-557`, step (9a-derive):

```go
	inst, _ := readInstanceSettings(instancePath)
	if dispatchPermissionMode == "" &&
		spec.Flags.PermissionMode == "--permission-mode" &&
		inst != nil && inst.Permissions != nil &&
		inst.Permissions.DefaultMode == "bypassPermissions" {
		dispatchPermissionMode = "bypassPermissions"
		fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: derived --permission-mode bypassPermissions from the workspace's declared permissions posture\n")
	}
```

The comment above it (`dispatch.go:536-549`) states the premise the exploration
is built on: "Since Claude Code 2.1.258 that posture no longer reaches a worker
through the settings-file channel; the only channel still honored is this CLI
flag."

The derived value is consumed by `buildDispatchPassthrough`
(`internal/cli/dispatch.go:983`), which pairs `flags.PermissionMode` with
`dispatchPermissionMode` and appends both as discrete argv elements. The
Claude row's spelling is `"--permission-mode"`
(`internal/agentplan/dispatch.go:354`); Codex's is `"--sandbox"`
(`internal/agentplan/dispatch.go:420`), which is why the gate compares the
literal flag string rather than the agent.

The reader itself, `internal/cli/dispatch_plugins.go:228-238`:

```go
func readInstanceSettings(instancePath string) (*instanceSettings, error) {
	data, err := os.ReadFile(filepath.Join(instancePath, ".claude", "settings.json"))
	...
```

and the projection at `internal/cli/dispatch_plugins.go:186-206`, whose
`Permissions` field is an anonymous struct pointer so "absent" and
"present but not bypass" are distinguishable:

```go
	Permissions *struct {
		DefaultMode string `json:"defaultMode"`
	} `json:"permissions"`
```

`readInstanceSettings` is shared with three other dispatch concerns —
plugin/marketplace pre-warming (`dispatch_plugins.go:65-115`),
`remoteControlAtStartup` (`dispatch.go:594`) and `keepAliveOnDispatch`
(step 9d) — which is why the read was hoisted to (9a-derive). Its doc comment at
`dispatch_plugins.go:222-227` cross-references `internal/workspace/permissions.go`
for the settings.json-vs-settings.local.json rule, i.e. the live consumer's
documentation points at the dead one.

Covered by `@critical` scenario "dispatch derives --permission-mode from a
bypass-declared workspace" at `test/functional/features/dispatch.feature:51-68`,
whose leading comment names 2.1.258 explicitly, and by unit tests in
`internal/cli/dispatch_permissionmode_test.go`.

### 4. Consumer 2 (DEAD): `workspace.WorkerPermissionMode`

`internal/workspace/permissions.go:25`. Its only callers in the entire tree are
its own test (`internal/workspace/permissions_test.go:59`) and design prose. I
ran `grep -rn "WorkerPermissionMode" --include=*.go .` — the only `.go` hits are
`permissions.go:18,25` (definition) and `permissions_test.go:9,59,61`.

**The orphaning commit is identifiable.** `git log -S "WorkerPermissionMode"`
returns three commits:

- `2039f1b feat(mesh): resolve worker permission mode from coordinator settings (#87)` — introduced it.
- `69b735f refactor!: remove pre-pivot mesh and add symmetric niwa worktree commands (#151)` — removed the caller. The diff for that commit deletes the line
  `workerPermMode: workspace.WorkerPermissionMode(instanceRoot),` (the mesh
  daemon's `spawnContext`), leaving the function itself behind.
- `5232825 docs(explore): capture scope for inert-defaultmode-key` — this exploration's own scope file.

Design record: `docs/designs/archive/DESIGN-worker-permissions.md` (archived by
the same PR #151) is where it came from; `docs/designs/current/DESIGN-niwa-mesh-removal.md`
is the removal's account. Its doc comment still says "reads the **coordinator's**
materialized permission mode" — coordinator being pre-pivot mesh vocabulary,
exactly as the lead suspected.

`DESIGN-dispatch-permission-mode.md:369-377` already diagnosed this in prose and
deliberately declined to reuse it:

> **Considered and rejected: reusing `internal/workspace/permissions.go`'s
> `WorkerPermissionMode`.** It reads the identical `permissions.defaultMode`
> path and looks, at a glance, like it does this derivation already. It is
> dead code (its only caller, the removed mesh daemon, is gone), and its
> fallback semantics — mapping every non-bypass case to `"acceptEdits"` —
> would grant a stronger-than-today posture to every workspace with no
> declared `bypass` posture, which R3 explicitly forbids.

So the design *knew* it was dead and left it in place rather than deleting it.
Deleting `internal/workspace/permissions.go` and `permissions_test.go` is a
zero-risk, zero-behavior-change cleanup available to this exploration.

### 5. `internal/agentplan/` — a genuinely parallel posture pipeline, but Codex-only

`internal/agentplan/posture.go` handles a **different** input and a **different**
output. Input is `[session.posture]` (`internal/config/session.go:63-66`):

```go
type SessionPostureConfig struct {
	Approvals string `toml:"approvals,omitempty"`
	Sandbox   string `toml:"sandbox,omitempty"`
}
```

Output is Codex's two top-level keys, `internal/agentplan/posture.go:46-49`:
`approval_policy` and `sandbox_mode`, with their own closed vocabularies at
`posture.go:76-81` (`on-untrusted|on-failure|on-request|never`) and
`posture.go:89-93` (`read-only|workspace-write|full-access`), validated by
`validateSessionPosture` (`posture.go:104`).

**agentplan never writes or reads a Claude-Code `permissions.defaultMode`.**
`agentplan/settings.go` is scope/filename/marshalling plumbing only — it takes
`Doc map[string]any` already built by `internal/workspace` and is deliberately
agent-agnostic (`settings.go:98-108`). `internal/agentplan/settings_test.go`
tests paths, marshalling, managed-ness and scope validation; it does not touch
permissions.

So yes, there is a second posture pipeline, but it is disjoint: neutral
`[session.posture]` -> Codex TOML, versus Claude-named `[claude.settings]
permissions` -> `permissions.defaultMode`. `internal/config/session.go:57-61`
states the split as a deliberate decision:

> Claude Code's approval posture keeps coming from `[claude.settings]`
> permissions, which is unchanged by this table. Pointing that agent at the
> neutral declaration too is a separate change; what this table must not do,
> and does not, is let one agent's key decide another agent's posture.

That sentence is the pre-authorized migration path for anyone who wants to move
the Claude posture onto the neutral declaration.

### 6. `internal/watch/` — a third producer *and* a third reader, on the same file

Not merely a mention. Producer side is section 2 above. Reader side is
`watch.VerifyReviewSettings` (`internal/watch/containment.go:290`), called by
`ApplyReviewSettings` itself after re-reading the merged file
(`containment.go:270-278`). At `containment.go:333-339`:

```go
			perms, ok := merged["permissions"].(map[string]any)
			if !ok {
				return fmt.Errorf("review settings check: permissions block missing under the operator-approval posture")
			}
			if mode, _ := perms["defaultMode"].(string); mode != "default" {
				return fmt.Errorf("review settings check: permissions.defaultMode must be \"default\" under the operator-approval posture, got %q", mode)
			}
```

This is a launch gate: a failed check aborts the stage rather than launching.
`internal/watch/guardfs.go` does not read the key; it emits PreToolUse
`permissionDecision` objects (`guardfs.go:107-120`) whose effect *depends* on the
session running under a non-bypass mode — the doc at `guardfs.go:50-51` says
"the harness honors it under a non-bypass permission mode".

**This is where the inert key bites hardest.** `containment.go:188-192` documents
the hard-deny posture as:

> ask == false (hard-deny posture, the shipped floor): the emitted settings are
> the PR #198 shape. permissions.defaultMode is NOT set (the session inherits the
> bypassPermissions the dispatch applies) ...

But watch does not launch through `runDispatch`. It builds its own passthrough at
`internal/cli/watch.go:836` — `buildDispatchPassthrough(claudeLaunchSpec().Flags, slug, "")`
— and then calls `dispatchLaunch` directly (`watch.go:845-853`). Since
`buildDispatchPassthrough` reads the package-level `dispatchPermissionMode`
(`dispatch.go:983`), which in a `niwa watch` process is always `""`, **watch
forwards no `--permission-mode` at all**. Its hard-deny posture therefore relies
entirely on the settings-file inheritance that 2.1.258 killed. And in the ask
posture its whole mechanism (`defaultMode = "default"` so a hook `ask` is
honored) depends on a key that, per the core premise, project scope no longer
honors for permissive values — though `"default"` is the *restrictive* direction,
so whether 2.1.258's change touches it is exactly the question the
settings-scope lead needs to answer.

### 7. `internal/config/session.go` — what it holds

The agent-neutral `[session]` declaration: `SessionConfig{Env SessionEnvConfig,
Posture SessionPostureConfig}` (`session.go:26-29`). It holds **no** Claude
permission state. Its file-level comment (`session.go:3-17`) explains why the
table is not spelled under any agent's namespace, and `session.go:57-61` (quoted
above) explicitly carves Claude's permission posture out of it.

### 8. Where the workspace author declares the input

**Config surface.** `[claude.settings] permissions = "bypass" | "ask"`, at three
merge positions:

- workspace level: `[claude.settings]` -> `config.ClaudeConfig.Settings`
  (`internal/config/config.go:36`)
- instance override: `[instance.claude.settings]` -> merged by
  `MergeInstanceOverrides` (`internal/workspace/override.go:179`, settings copied
  at :189)
- per repo: `[repos.<name>.claude.settings]` -> `internal/config/config.go:102`

**Schema.** `internal/config/config.go:436-440`:

```go
// SettingsConfig maps setting keys to their values. The primary key today is
// "permissions" (values: "bypass", "ask"). Values are MaybeSecret so a vault
// reference can back a setting value per PRD R3; the plaintext path still
// accepts raw strings through MaybeSecret's TextUnmarshaler.
type SettingsConfig map[string]MaybeSecret
```

**Validation.** There is **none at config-parse time**. `SettingsConfig` is an
open `map[string]MaybeSecret`, so `permissions = "byapss"` parses cleanly and
fails only much later, inside `buildSettingsDoc`, as
`unknown permissions value "byapss"` — an error surfaced from the middle of a
materializer during `niwa apply`. Compare `[session.posture]`, which validates up
front with a message that enumerates the accepted set
(`internal/agentplan/posture.go:104-127`). The two sibling keys
`remoteControlAtStartup` and `keepAliveOnDispatch` also live in this same open
map and are parsed with `strconv.ParseBool` at emit time
(`internal/workspace/materialize.go:719-724`, `:735-740`), with named constants
in `internal/config/config.go:442-459`. `permissions` has no such constant.

**Documentation.** Thin and scattered, with no dedicated guide:

- `docs/guides/file-distribution.md:62-72` — the only user-facing guide snippet:
  "A workspace that runs sessions in bypass mode suppresses it: `[claude.settings]
  permissions = "bypass"` ... This is the existing settings surface (it maps to
  Claude Code's `bypassPermissions` mode)."
- `docs/guides/ephemeral-session-instances.md:326-333` — the root-scope caveat.
- `docs/prds/PRD-config-distribution.md:46,106,121,246,258,286-288` — the
  original requirement, including R10 per-repo override.
- `docs/designs/current/DESIGN-dispatch-permission-mode.md` — the live channel.
- `docs/designs/archive/DESIGN-worker-permissions.md` — the dead consumer.

No guide states that `"ask"` produces a value Claude Code does not recognize, and
no guide states that since 2.1.258 the `"bypass"` value only takes effect on a
*dispatched* worker (via the derived flag) and not on an interactive session
started in the instance.

## Implications

1. **The graph is asymmetric and that is the whole problem.** Three producers
   write the key to three paths, but only one path (`<instanceRoot>/.claude/settings.json`)
   has a niwa reader, and that reader looks for exactly one value
   (`bypassPermissions`). The repo-level `settings.local.json` and the
   workspace-root `settings.json` copies have no niwa consumer at all — they exist
   purely to speak to Claude Code, which is precisely the audience that stopped
   listening. They are the part of the document making a claim it cannot keep.

2. **The smallest honest change has a natural shape.** The instance-root document
   needs to carry a niwa-internal signal; the repo-level and workspace-root
   documents do not need to carry anything. niwa already has the pattern for a
   niwa-defined key that Claude Code ignores: `keepAliveOnDispatch`
   (`internal/config/config.go:449-459`, emitted at `materialize.go:733-742`, read
   back through the same `instanceSettings` projection). Renaming the signal into
   that family — a niwa-namespaced key emitted only where a reader exists — would
   stop the document from asserting a Claude Code setting while preserving the
   dispatch derivation verbatim. It also makes the `"ask"` -> `askPermissions`
   nonsense disappear rather than needing a separate fix.

3. **Any change must not disturb `niwa watch`.** Watch is a second, independent
   writer and reader of the *same* on-disk key at the *same* path, with its own
   value vocabulary (`"default"`) and its own launch gate. A change that stops
   emitting `permissions.defaultMode` from `buildSettingsDoc` is safe for watch's
   ask posture (watch writes its own), but it invalidates the hard-deny posture's
   documented assumption that bypass is inherited — which, per section 6, is
   *already* invalid today and nobody noticed.

4. **`internal/workspace/permissions.go` is free to delete.** Zero non-test
   callers, a design doc that already declared it dead, and a named orphaning
   commit. Its removal also removes the misleading cross-reference that
   `dispatch_plugins.go:222-227` currently points at.

5. **Validation belongs at parse time.** Whatever shape the signal takes, the
   `[session.posture]` validator is the in-repo precedent for rejecting a typo
   with an enumerated error message before a materializer is halfway through
   writing files.

## Surprises

- **`"ask"` maps to `askPermissions`, which is not a Claude Code permission
  mode.** Valid modes are `default`, `acceptEdits`, `plan`, `bypassPermissions`.
  Half the accepted config vocabulary has always produced an uninterpretable
  value, pinned by tests (`materialize_test.go:434`) and enshrined in a PRD
  (`PRD-config-distribution.md:197`). The key was inert for `ask` long before
  2.1.258 made it inert for `bypass`.

- **`niwa watch` is a fourth producer of the same key with a fifth value.**
  The lead framed watch as a possible third *reader*; it is both, and it writes
  `"default"`, a value outside `permissionsMapping` entirely, into the same file
  Producer A wrote.

- **Watch's hard-deny posture is already broken by the same 2.1.258 change.**
  `containment.go:189-191` says the session "inherits the bypassPermissions the
  dispatch applies", but watch launches via `dispatchLaunch` directly
  (`watch.go:845`) with a passthrough built from a package-level
  `dispatchPermissionMode` that is always empty in a watch process. No flag is
  forwarded. This is a live consequence of the inert key that the exploration's
  scope did not anticipate.

- **`runDispatch` mutates a package-level flag variable** (`dispatchPermissionMode`,
  declared at `dispatch.go:47`, assigned at `:555`). It works because each
  dispatch is its own process, but it is a shared global that
  `buildDispatchPassthrough` reads implicitly rather than as a parameter — and
  `internal/cli/watch.go:836` calls that same builder.

- **The workspace root and the instance root get byte-identical permission
  postures from the same source** (`root_materializer.go:224-232`), so a
  `bypass` declaration writes `bypassPermissions` at workspace altitude too,
  where it governs every root-launched session. `docs/guides/ephemeral-session-instances.md:326-333`
  flags this as wider than per-instance bypass. If the settings-file channel is
  dead, that widened blast radius is now purely notional — worth confirming.

- **No config-time validation whatsoever** for a key whose entire purpose is a
  security posture; the error arrives mid-apply from inside a materializer.

## Open Questions

1. Does 2.1.258's change affect *restrictive* `defaultMode` values (`"default"`)
   from project scope, or only permissive ones (`bypassPermissions`,
   `acceptEdits`)? Watch's operator-approval posture depends entirely on the
   answer. This belongs to the settings-scope lead.

2. Is `<instanceRoot>/.claude/settings.json` "project scope" or "local scope"
   for a session whose cwd is the instance root? The instance root is a non-git
   directory holding `settings.json` (not `.local`), which is an unusual pairing
   — and the honored-scope question may turn on it.

3. Does anything outside this repo (shirabe, koto, a hook script, a CI job) read
   `permissions.defaultMode` out of a niwa-materialized settings file? Only the
   niwa tree was searched.

4. If the signal moves to a niwa-namespaced key, does the workspace-root
   `settings.json` still need one? Nothing reads the root document's permissions
   today, but the ephemeral-session guide describes it as governing root sessions
   — a claim now in the same category as the one this exploration is retiring.

5. Should `niwa watch` forward `--permission-mode bypassPermissions` explicitly
   in its hard-deny posture to restore what `containment.go:189-191` claims, or
   is the loss of bypass there actually harmless (the auto-allow hook exists only
   in the ask posture, so the hard-deny session may now prompt with no operator
   attached)? This may be a live bug independent of the key's shape.

## Summary

`permissions.defaultMode` has one builder (`buildSettingsDoc`, accepting only
`"bypass"`/`"ask"` and emitting nothing when undeclared) fanning out to three
documents — instance root `settings.json`, per-repo `settings.local.json`, and
workspace-root `settings.json` — of which only the instance-root copy has a niwa
reader: the live `--permission-mode` derivation at `internal/cli/dispatch.go:550-557`.
`workspace.WorkerPermissionMode` is confirmed dead (no non-test callers; its
mesh-daemon caller was deleted in `69b735f`, PR #151, and
`DESIGN-dispatch-permission-mode.md:369-377` already says so), agentplan's
posture pipeline is a disjoint Codex-only path that never touches this key, and
`internal/watch/containment.go` turns out to be both a fourth producer (writing
`"default"`) and a genuine reader (a launch gate). Two things surprised me: the
`"ask"` value has always mapped to `askPermissions`, which is not a Claude Code
mode at all, and `niwa watch`'s hard-deny posture already depends on the same
dead settings-file inheritance while forwarding no flag of its own.
