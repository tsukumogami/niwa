# Lead: How niwa dispatch launches sessions, and what it writes into instance settings

Research date: 2026-09-09. All paths relative to the niwa repo root. Line numbers
are from the `dispatch-sendmessage-approval` worktree at the time of writing.

Everything below marked VERIFIED was read in source. INFERRED items are flagged.

## Findings

### 1. The full path from `niwa dispatch <task>` to a running Claude process

`runDispatch` in `internal/cli/dispatch.go:269-862` is one long, explicitly
numbered sequence. The numbering in the comments is the actual execution order.
VERIFIED:

| Step | Line | What happens |
|---|---|---|
| (1) | `dispatch.go:280` | Validate the positional prompt (non-empty; no size limit — oversize spills to a file later). |
| (2) | `dispatch.go:289-300` | `workspace.ClassifyCwd` resolves the enclosing workspace root. Outside a workspace: hard refusal, nothing created. |
| (2a) | `dispatch.go:308` | `config.LoadGlobalConfig()` — the host `~/.config/niwa/config.toml`, loaded **once** and reused for every host-level default below. |
| (2b) | `dispatch.go:319-341` | Resolve which agent this dispatch launches: `--harness` > `NIWA_DISPATCH_HARNESS` > workspace `[workspace].default_agent` > host `[global].default_dispatch_harness`. |
| (2d) | `dispatch.go:365-373` | `agentplan.Lookup(agentplan.DispatchLaunch, agent)` gate + `dispatchLaunchSpec(agent)` → `agentplan.LaunchSpec`. A non-launchable agent refuses here, quoting the declaration's own reason. |
| (3) | `dispatch.go:381` | Preflight `exec.LookPath(spec.Binary)` **before** any instance exists. |
| (3b) | `dispatch.go:420-424` | Repo-scoped-config-doc warning (Codex today). |
| (3c) | `dispatch.go:430-448` | Interactive prompt capture if no positional arg. |
| (4) | `dispatch.go:458-468` | Name the instance: `<config>+<slug>-<8hex>`. |
| (5) | `dispatch.go:472` | Opportunistic reaper sweep. |
| **(6)** | `dispatch.go:476` | **`provisionInstanceFunc(...)` — the whole provisioning + materialization pipeline runs here, synchronously.** |
| (7) | `dispatch.go:494-499` | Arm the deferred self-rollback (`destroyInstanceFunc`). |
| (8) | `dispatch.go:506` | Write `.niwa/dispatch-pending`. |
| (9a) | `dispatch.go:520-527` | Resolve the model (`--model` > host `dispatch_model`), map through `spec.ModelCategories`. |
| **(9a-derive)** | `dispatch.go:550-557` | **`inst, _ := readInstanceSettings(instancePath)`** — reads the just-materialized `<instance>/.claude/settings.json`; derives `--permission-mode bypassPermissions` when the settings declare it and the agent's flag spelling is `--permission-mode`. |
| (9b) | `dispatch.go:564` | `buildDispatchPassthrough(spec.Flags, slug, resolvedModel)` builds the flag argv. |
| (9c) | `dispatch.go:585-601` | Remote control: may append `spec.Flags.Settings` (`--settings`) + `remoteControlSettingsJSON` as **two discrete argv elements**. |
| (9d) | `dispatch.go:620-658` | Keep-alive: may set `promptPrefix` (prompt text, no settings flag). |
| (9b'/mode) | `dispatch.go:667` | `spec.Runner.ModeFor(dispatchDetach)` → `LaunchForeground` / `LaunchDetached` / `LaunchBackgrounded`. |
| **(9e)** | `dispatch.go:669-680` | **`dispatchLaunch(ctx, launchRequest{...})` — the exec.** |
| (10) | `dispatch.go:727` | `dispatchCapture` polls the agent's session-record store, correlating on cwd == instance dir. |
| (11) | `dispatch.go:761-792` | Durable `workspace.SessionMapping` write. |
| (12)-(14) | `dispatch.go:796-859` | Disarm rollback, print hints, optionally attach. |

**Ordering conclusion (the load-bearing one for this exploration):** every file
niwa writes into the instance exists on disk *before* the worker starts.
`provisionInstanceFunc` → `realProvisionInstance`
(`internal/cli/instance_from_hook.go:428-508`) calls `applier.Create(...)` at
`instance_from_hook.go:501`, and that is what runs the whole materialization
pipeline (settings, hooks, context docs, `[instance.files]`, plugin pre-warm). It
returns before step (7). The launch at step (9e) is ~200 lines later in the same
synchronous function. So a fix that writes or amends a settings file anywhere
between line 486 and line 669 lands before the process exists. VERIFIED.

**argv construction** — `buildLaunchArgs` in
`internal/cli/dispatch_launcher.go:364-377`, in order:

1. `spec.LeadingArgs` (Claude: `["--bg"]`; Codex: `["exec", "--skip-git-repo-check"]`)
2. `spec.DetachedArgs` (only when `LaunchDetached`; Codex: `["--json"]`, Claude: none)
3. `formatWorkdirGrant(spec, workdir)` (Codex only: `-c projects={"<dir>"={trust_level="trusted"}}`)
4. the passthrough (already split into discrete elements)
5. a bare `--` if `spec.PromptSeparator` (Codex only)
6. the prompt as the final single argv element

Every value is its own slice element, never string-concatenated — this is
DESIGN Decision 8, restated at `dispatch_launcher.go:360-363`. VERIFIED.

**env** — `realDispatchLaunch` at `dispatch_launcher.go:138-141`:
`worker := os.Environ()` unless `req.Env != nil`. **No caller sets `Env` today**
(`dispatch_launcher.go:47-53` says so explicitly, calling it "an extension point
the launcher honors rather than a seam anything currently goes through"). So the
worker inherits the dispatching shell's full environment. VERIFIED.

**process model** — `startDetachedWorker` (`dispatch_launcher.go:258-291`):
`Setsid`, stdin `/dev/null`, stdout/stderr to `<instance>/.niwa/dispatch-claude.{out,err}`,
`cmd.Process.Release()`. `runForegroundWorker` (`:197-232`): inherits caller's
stdout/stderr, stdin still `/dev/null`, shares the caller's process group.
Claude's `RunnerSelfBackgrounding` means it always takes the `LaunchBackgrounded`
branch (`dispatch_launcher.go:148-158`): `cmd.Run()` and wait for the hand-off.
VERIFIED.

### 2. The harness abstraction and how unsupported flags get dropped

`internal/agentplan/dispatch.go` is the whole per-agent table.

- `LaunchFlags` (`agentplan/dispatch.go:235-252`) holds five intents, each as the
  agent's own spelling: `Model`, `PermissionMode`, `SubagentType`, `DisplayName`,
  `Settings`. **An empty value means the agent has no such flag and the intent is
  dropped rather than guessed at.**
- Claude's row (`agentplan/dispatch.go:348-385`): `Binary: "claude"`,
  `LeadingArgs: ["--bg"]`, `Flags{Model: "--model", PermissionMode:
  "--permission-mode", SubagentType: "--agent", DisplayName: "--name", Settings:
  "--settings"}`.
- Codex's row (`agentplan/dispatch.go:387-483`): `PermissionMode: "--sandbox"`,
  and **`Settings` is empty** — the comment at `:421-424` says an inline settings
  document is "a niwa-side intent this agent has no flag for."
- The drop happens in `buildDispatchPassthrough` (`dispatch.go:974-992`): the
  loop appends a pair only `if pair.flag != "" && pair.value != ""`.

**What a new knob must do to behave correctly for a non-Claude harness.** Two
patterns already exist and they answer this differently:

- **Flag-spelling gate** (what the permission-mode derivation uses,
  `dispatch.go:552`): `spec.Flags.PermissionMode == "--permission-mode"`. It
  compares a *flag spelling*, not an agent name. This is what keeps the
  `dispatch_layout_test.go` scan green (see §6).
- **Capability-declaration gate** (what remote control and keep-alive use,
  `dispatch.go:586-587` and `:638-639`): `agentplan.Lookup(agentplan.RemoteControl,
  dispatchedAgent)` and check `State == StateImplemented`, plus (for RC)
  `spec.Flags.Settings != ""`. When the capability is undeliverable, keep-alive
  *warns and proceeds* (`dispatch.go:646-652`), quoting the declaration's own
  `Reason`. RC silently injects nothing.

A pre-approval knob should follow the second pattern if it is conceptually a new
capability, or ride `ApprovalPosture` (row 12,
`internal/agentplan/capability.go:91-92, 184`) if it is an extension of the
existing posture row. VERIFIED that both mechanisms exist; which is right is a
design call.

### 3. Every place niwa writes or merges Claude settings

There is exactly one producer of the settings *file* — `agentplan.SettingsPlan`
in `internal/agentplan/settings.go:113-136` — and exactly one producer of the
settings *document* — `buildSettingsDoc` in
`internal/workspace/materialize.go:669-955`. Three callers, three scopes
(`agentplan/settings.go:41-75`):

| Scope | File | Caller | Managed? |
|---|---|---|---|
| `SettingsAtWorkspaceRoot` | `<workspaceRoot>/.claude/settings.json` | `writeRootSettings`, `internal/workspace/root_materializer.go:238-292` | no (overwrite-idempotent, no state store) |
| `SettingsAtInstanceRoot` | `<instanceRoot>/.claude/settings.json` | `internal/workspace/workspace_context.go:429-462` (the `RootSettingsMaterializer`, wired at `internal/workspace/apply.go:1587`) | yes |
| `SettingsInRepo` | `<repoDir>/.claude/settings.local.json` | `internal/workspace/materialize.go:1249-1283` | yes |

**Every one of these is a whole-file overwrite, not a merge with user content.**
`SettingsPlan` marshals the map and writes `Content` at `Mode 0o600` with
`Op: OpWriteFile` (`agentplan/settings.go:119-135`). There is no read-modify-write
of an existing user document anywhere in the materializer path. VERIFIED.

**What `buildSettingsDoc` will actually emit** (this constrains what can ride the
existing machinery). Reading `materialize.go:669-955` end to end, the document's
keys come from a *fixed* set of producers:

- `permissions.defaultMode` — from `cfg.Settings["permissions"]` mapped through
  `permissionsMapping` (`materialize.go:312-315`), which accepts **only `"bypass"`
  → `bypassPermissions` and `"ask"` → `askPermissions`**; anything else is a hard
  error at `materialize.go:685-687`.
- `permissions.deny` — only the worktree-delegation fallback
  (`materialize.go:698-703`), a fixed two-element list.
- `remoteControlAtStartup`, `keepAliveOnDispatch` — boolean passthroughs, each
  read by an explicit named key (`materialize.go:716-739`).
- `hooks` — from installed hook scripts, plus the worktree hooks, plus the
  shirabe work-summary hooks, plus the pr-body `PreToolUse` hook, plus
  `SessionHooks`.
- `env`, `includeGitInstructions`, `enabledPlugins`, `extraKnownMarketplaces`.

**There is no generic passthrough loop over `cfg.Settings`.** A key in
`[claude.settings]` that `buildSettingsDoc` does not name is silently dropped, and
**`permissions.allow` has no emitter at all today**. VERIFIED — I read the whole
function looking for a `for k, v := range cfg.Settings` and there is none.

`workspace.WorkerPermissionMode` (`internal/workspace/permissions.go:25-39`) is a
*reader*, not a writer: it reads `<instanceRoot>/.claude/settings.json` and
returns `"bypassPermissions"` or `"acceptEdits"`. Grep shows no caller on the
dispatch path — it is a separate narrow accessor whose doc comment
(`permissions.go:18-24`) documents the settings.json-vs-settings.local.json split.
The dispatch path uses `readInstanceSettings` (`dispatch_plugins.go:222-238`)
instead, which reads the same file into a wider projection (`instanceSettings`,
`dispatch_plugins.go:186-205`).

**One place does merge rather than overwrite**, and it is the most useful
precedent in the repo: `watch.ApplyReviewSettings`
(`internal/watch/containment.go:201-278`). It reads the provisioned instance's
`.claude/settings.json`, merges a `sandbox` stanza and appends `PreToolUse` hook
entries (deduped by matcher), optionally sets `permissions.defaultMode = "default"`
preserving other permissions keys, writes the file back, then **re-reads and
re-verifies** via `VerifyReviewSettings` before launch. Callers:
`internal/cli/watch.go:557` and `:831`, both after provisioning and before the
Claude launch. VERIFIED.

### 4. Can a pre-approval ride `[files]` / `[instance.files]` / `[root.files]`?

The mechanics permit it. `materializeVerbatimFile`
(`internal/workspace/materialize.go:1998-2022`) computes
`targetPath = filepath.Join(ctx.RepoDir, dest)` with **no restriction on `dest`
containing a subdirectory** — `checkContainment` is applied to the *source* only
(`materialize.go:2000`). So `[instance.files]` with
`"preapprove.json" = ".claude/settings.local.json"` would write
`<instanceRoot>/.claude/settings.local.json`, which niwa does not otherwise manage
at the instance root (`dispatch_plugins.go:117-119` explicitly notes that the
plugin pre-warm writes there *because* niwa leaves that file alone), and which a
Claude session rooted at the instance would load as project-local settings.
VERIFIED mechanically.

**But I would call it a stretch, for four reasons:**

1. `docs/guides/file-distribution.md:96-98` states as a documented limitation
   that "destinations stay at the project root of their level… not for
   niwa-internal directories." The code does not enforce it, but writing into
   `.claude/` from a file table contradicts the guide.
2. The user asked for a **per-machine** knob that is **off by default**. The file
   tables live in `workspace.toml` (per-workspace, checked into the config repo),
   not in the host `~/.config/niwa/config.toml`. Wrong axis.
3. There is no flag layer. `niwa dispatch --<flag>` cannot override a distributed
   file; the file is written at provision time by config alone.
4. It would collide with the plugin pre-warm's `--scope local` writes to that
   same file (`dispatch_plugins.go:121`), whose ordering relative to
   `[instance.files]` materialization I did not trace. INFERRED risk.

Verdict: real but wrong-shaped. Worth one paragraph in a design doc as a
considered-and-rejected option, not a candidate.

### 5. Are hooks already generated for instances? Would a `PreToolUse` hook fit?

Yes on both counts, and this is stronger than the exploration brief assumed.

- The workspace-root `SessionStart` hook invoking `niwa instance from-hook` is
  built at `root_materializer.go:73-75` and `:264-267`, emitted by
  `buildSettingsDoc` at `materialize.go:875-899`. It **merges** rather than
  overwrites the `SessionStart` block, appending to any installed entries
  (`materialize.go:894-898`).
- **niwa already generates a `PreToolUse` hook.** The pr-body hook at
  `materialize.go:847-863`: matcher `"Bash"`, an inline
  `command -v shirabe`-guarded command (no script file), appended to the
  `PreToolUse` array rather than replacing it. Its command builder is
  `prBodyHookCommand()` at `materialize.go:619`. So an inline, no-script-file,
  appended-to-`PreToolUse` entry is an established pattern in the *materializer*.
- **niwa already emits a `PreToolUse` hook whose entire job is to print an allow
  decision.** `watch.autoAllowHook()` at `internal/watch/containment.go:143-158`:

  ```
  {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow",
   "permissionDecisionReason":"niwa watch review: in-instance tool auto-approved"}}
  ```

  delivered as `printf '%s' '<json>'` against a matcher. This is exactly the
  mechanism a `SendMessage` pre-approval would need, already written, already
  shipped, already tested in this repo. VERIFIED.
- `hookEventMapping` (`materialize.go:319-324`) already maps `pre_tool_use` →
  `PreToolUse`, so a workspace can also declare its own via `[claude.hooks]`.

There is one caveat worth naming: `containment.go:150-152` and
`watch.go`'s `ask` posture exist precisely *because* a hook's `ask` decision is
inert under `bypassPermissions`. Whether a hook's **`allow`** decision is honored
under `bypassPermissions` is not something this repo documents. INFERRED open
question — flagged in §Open Questions and belongs to the sibling agent
investigating the Claude Code mechanism.

### 6. Tests

**Unit tests** (`internal/cli/`):

- `dispatch_permissionmode_test.go` (280 lines) — the closest analogue; tests the
  9a-derive behavior.
- `dispatch_remotecontrol_test.go`, `dispatch_remotecontrol_roundtrip_test.go` —
  the `--settings` injection and the settings→materializer→read-back roundtrip.
  The roundtrip test (`:14`) exists to catch a one-sided rename between the key
  the materializer writes and the key `readInstanceSettings` reads. A new
  settings-carried knob needs its equivalent.
- `dispatch_keepalive_test.go`, `dispatch_keepalive_roundtrip_test.go`,
  `dispatch_wiring_keepalive_test.go` — the flag > downstream > host-default
  resolver plus its wiring into `runDispatch`.
- `dispatch_launcher_test.go` — asserts the constructed argv and `cmd.Dir`
  through the `dispatchLaunch` seam without spawning a process.
- `dispatch_contract_test.go` — ranges over every agent and asserts the command
  matches the capability table.

**Structural constraint on where a new knob may live.**
`internal/cli/dispatch_layout_test.go:40-50` lists `dispatchPathFiles`:
`dispatch.go`, `dispatch_reentry.go`, `dispatch_capture.go`,
`dispatch_keepalive.go`, `dispatch_launcher.go`, `dispatch_model.go`,
`dispatch_remotecontrol.go`, `dispatch_spill.go`, `session_records.go`. Two AST
scans run over them: `TestDispatchPathNamesNoAgentConstant` (`:309`) forbids
`AgentClaude`/`AgentCodex`, and `TestDispatchPathNamesNoAgentLiteral` (`:325`)
forbids the string literals `"claude"`, `"codex"`, `".claude"`, `".codex"` etc.
`TestDispatchPathFilesAreAllPresent` (`:579`) keeps the list honest.
**A new `dispatch_*.go` file will be added to this list and must not name an
agent** — gate on `spec.Flags.*` spellings or on `agentplan.Lookup`, never on the
agent. `dispatch_plugins.go` and `job_state.go` are the only excused files
(`dispatch_layout_test.go:52-73`), each because its capability is declared
`AgentCannotReceive` for Codex.

**Functional tests** (`test/functional/features/`). The repo's own `CLAUDE.md:29-30`
requires a `@critical` Gherkin scenario for any user-facing CLI command change.
`dispatch.feature` already carries 16 `@critical` scenarios including
"dispatch derives --permission-mode from a bypass-declared workspace"
(`dispatch.feature:51`), which is the direct template: a fake `claude` on PATH
that records its argv, plus an assertion on the recorded flags.
`keep-alive.feature` is the template for a three-layer (flag/downstream/host)
resolver. A new knob needs at minimum: off-by-default (argv unchanged), flag-on,
and the per-machine config-on scenario.

**Docs.** `docs/guides/remote-control-on-dispatch.md` and
`docs/guides/session-keep-alive.md` are the two existing guides for exactly this
shape of knob (host-level default, off by default, overridable). A new one should
match them.

## Implications

Candidate insertion points, ranked.

### 1. A `--settings` JSON injection at step (9c), beside remote control — but it collides

`dispatch.go:598` already does `passthrough = append(passthrough, spec.Flags.Settings, remoteControlSettingsJSON)`.
The same two lines with a `{"permissions":{"allow":[...]}}` document would be a
~15-line change in a new `dispatch_preapproval.go`, gated on
`spec.Flags.Settings != ""` (so Codex, whose `Settings` is empty, drops it
automatically and correctly).

**The blocker is documented and explicit.** `DESIGN-niwa-session-keep-alive.md:141-146`
("B-note — no second `--settings`"): niwa never merges settings JSON, a second
`--settings` would be passed verbatim alongside RC's, and the CLI's
repeated-`--settings` behavior is undocumented (likely last-wins), so it could
clobber `remoteControlAtStartup`. `DESIGN-niwa-watch-once-pr-review.md:167-169`
rejected the same option for the same reason. This is a known landmine that has
already been stepped around twice.

The fix is available but is a real change: make the `--settings` injection a
single **merged** document built from all contributors (RC + pre-approval +
whatever comes next), emitted once. That is the right long-term shape and turns
a "no second `--settings`" prohibition into "there is one settings-document
builder." Also note the RC injection is *default-fill only* — it is skipped
entirely when the instance settings decided `remoteControlAtStartup`
(`dispatch_remotecontrol.go:42-45`) — so a pre-approval that rode a *shared*
document would need its own emit path for the case where RC injects nothing.

### 2. A `PreToolUse` allow-decision hook merged into instance settings after provision

Model it directly on `watch.ApplyReviewSettings`
(`internal/watch/containment.go:201-278`) + `watch.autoAllowHook()`
(`containment.go:143-158`): read `<instance>/.claude/settings.json`, append one
`PreToolUse` entry with a `SendMessage` matcher whose command prints the allow
decision, write back, re-verify. Insert the call in `runDispatch` between step
(8) (`dispatch.go:508`) and step (9e) (`dispatch.go:669`) — the natural slot is
right after 9a-derive at `dispatch.go:557`, where `inst` has just been read.

Advantages: no `--settings` collision; the mechanism already exists in this repo
and is proven; it composes with everything else in the document rather than
replacing it. Disadvantages: it mutates a file niwa has fingerprinted as a
managed file, so the next `niwa apply` will report it "modified outside niwa" —
this is the exact failure mode `dispatch_plugins.go:112-119` describes for
`--scope project` (issue #179) and routes around by writing
`settings.local.json` instead. **That precedent suggests writing the hook into
`<instance>/.claude/settings.local.json`, which niwa does not manage at the
instance root, rather than into the managed `settings.json`.** INFERRED: I did
not verify that `watch`'s own merge into the managed `settings.json` avoids the
drift report; it may simply not care because a watch instance is short-lived.

### 3. Emit `permissions.allow` from `buildSettingsDoc`

The materializer already owns the `permissions` map and already merges two
independent keys into it (`defaultMode` from settings, `deny` from worktree
delegation — `materialize.go:681-707`, with a comment explaining that they are
emitted into the same map so neither clobbers the other). Adding an `allow` key
there is a small, well-precedented change.

**But this is probably dead on arrival**, for the reason in §Surprises: Claude
Code 2.1.258 stopped honoring the project `.claude/settings.json`'s
`permissions.defaultMode`. Whether `permissions.allow` from the same file is
still honored is the single most important open question in this exploration and
belongs to the sibling agent. If it is not honored, this option is worthless and
options 1 and 2 are the only live ones. Also note this is the wrong axis for
"per-machine": `buildSettingsDoc` is driven by `workspace.toml`, not by the host
config.

### 4. The host-config + flag surface (needed by whichever of 1-3 wins)

Independent of the delivery mechanism, the *control* surface has an exact
template. Add a field to `config.GlobalSettings`
(`internal/config/registry.go:28-107`) alongside `RemoteControlOnDispatch` and
`KeepAliveOnDispatch` — a `*bool` with a `toml:"...,omitempty"` tag, nil meaning
unset. Add the flag in `dispatch.go`'s `init()` (`:24-41`); if it needs to
override in both directions, use `triBoolValue` + `NoOptDefVal = "true"`
(`dispatch_keepalive.go:54-74`) exactly as `--keep-alive` does. Write a resolver
in the shape of `resolveDispatchKeepAlive` (`dispatch_keepalive.go:94-105`):
flag > downstream (`instanceSettings`) > host default > off.

Note `internal/config/registry.go:298-306`: `remote_control_on_dispatch`,
`keep_alive_on_dispatch` and `dispatch_model` have **no `niwa config set`
setter** — the documented way to use them is to hand-edit
`~/.config/niwa/config.toml`. A new host key would match that precedent (and
inherit the same known comment-loss defect on any `niwa config set` write).

## Surprises

**1. `--permission-mode` already does the thing the brief assumed it should, and
the settings channel is already known to be broken.**
`docs/designs/current/DESIGN-dispatch-permission-mode.md` (frontmatter, and
`dispatch.feature:41-48`) records that **Claude Code 2.1.258 stopped honoring
`permissions.defaultMode` from a project's materialized `.claude/settings.json`**,
and that `--permission-mode` is "one of the two channels still honored." The
9a-derive block at `dispatch.go:539-557` is the regression fix: a workspace that
declares `permissions = "bypass"` gets `--permission-mode bypassPermissions`
forwarded on the argv, printing a notice to stderr.

This directly contradicts the exploration brief's framing that the prompt appears
"even though niwa materializes `permissions.defaultMode: bypassPermissions` into
`.claude/settings.json`." That materialization has been inert since 2.1.258; the
posture reaches the worker via the CLI flag only. It also means: if the
`SendMessage` prompt survives `--permission-mode bypassPermissions`, then
bypassPermissions genuinely does not cover this tool, and no amount of settings
plumbing on niwa's side will change that. That is a Claude Code behavior fact,
not a niwa gap.

**2. The derivation is conditional and silent when it does not fire.** It only
runs when `dispatchPermissionMode == ""` AND `inst.Permissions.DefaultMode ==
"bypassPermissions"`. A workspace that never declared `permissions = "bypass"` in
`[claude.settings]` gets nothing forwarded (`dispatch.go:551-554`). Worth
confirming the affected workspace actually declares it — if it does not, the
worker is running under the default posture and the prompt is expected.

**3. `[claude.settings]` is not a passthrough.** I expected a generic map that
would let a workspace declare arbitrary Claude settings keys. It is not:
`buildSettingsDoc` names each key it emits, `permissions` accepts only `"bypass"`
or `"ask"` (`materialize.go:312-315`), and an unrecognized value is a hard error
(`:685-687`). Anything new needs a new emitter.

**4. niwa already ships a `PreToolUse` hook that prints an `allow` decision.**
`watch.autoAllowHook()` (`containment.go:143-158`). The hook shape this
exploration is contemplating is not new work in this codebase — it is a second
customer for an existing pattern.

**5. `launchRequest.Env` is an unused, deliberately preserved seam.**
`dispatch_launcher.go:47-53`. If the Claude Code mechanism turns out to be an
environment variable rather than a settings key or a flag, the plumbing already
exists and no caller has claimed it.

**6. The dispatch path is under an AST scan that forbids naming an agent.**
`dispatch_layout_test.go:304-331`. Any implementation must route the
Claude-vs-Codex decision through `spec.Flags.*` or `agentplan.Lookup`, never a
literal. The permission-mode derivation's `spec.Flags.PermissionMode ==
"--permission-mode"` comparison at `dispatch.go:552` is the sanctioned idiom.

## Open Questions

1. **Does a `PreToolUse` hook's `allow` decision take effect under
   `bypassPermissions`?** `containment.go:150-152` states that a hook's *`ask`*
   is inert under bypass, which is why `watch`'s ask posture forces
   `defaultMode = "default"`. It says nothing about `allow`. If `allow` is also
   inert under bypass — or if the prompt in question is raised by a path that
   runs before `PreToolUse` — candidate 2 collapses. Sibling-agent territory.

2. **Is `permissions.allow` in a project `.claude/settings.json` still honored
   post-2.1.258, given that `permissions.defaultMode` from the same file is not?**
   Decides whether candidate 3 exists at all.

3. **Does a `--settings` document merge with, or replace, the project
   `settings.json`?** `SPIKE-remote-control-by-default.md:23` says `--settings`
   *outranks* the project settings.json. If it replaces rather than layers, an
   injected pre-approval document could knock out plugins/env/hooks the instance
   settings carry — a much bigger blast radius than candidate 1 implies. Needs
   measurement before candidate 1 is chosen.

4. **What is Claude Code's behavior with repeated `--settings` flags?**
   `DESIGN-niwa-session-keep-alive.md:143-144` calls it "undocumented (likely
   last-wins)" and routes around it. If the answer is "last wins," the
   single-merged-document refactor in candidate 1 is mandatory rather than
   merely tidy.

5. **Does merging into the managed `<instance>/.claude/settings.json` trigger the
   "modified outside niwa" drift report on the next apply?**
   `dispatch_plugins.go:112-119` says yes for the analogous plugin-install case
   (issue #179) and routes to `settings.local.json` to avoid it. Whether
   `watch.ApplyReviewSettings` suffers the same thing, or is exempt because its
   instances are ephemeral, I did not determine.

6. **Does a `claude --bg` worker rooted at the instance load
   `<instance>/.claude/settings.local.json`?** niwa's own comments assert
   `settings.local.json` is "for per-repo dirs, never the root"
   (`dispatch_plugins.go:224-227`), yet the plugin pre-warm writes exactly there
   with `--scope local` and expects it to take effect
   (`dispatch_plugins.go:117-119`). These two statements are in tension. If the
   worker does load it, it is the cleanest delivery target: unmanaged by niwa, so
   no drift report, and loaded before the process starts.

7. **Per-machine, off-by-default, flag-overridable — which of the three layers
   does the user actually want?** The `--keep-alive` shape (tri-state flag >
   instance settings > host `[global]`) is the closest match to the stated
   requirement, but "per-machine" could equally mean host-config-only with a
   simple `bool` flag. The `[global]` keys have no `niwa config set` setter today
   (`registry.go:298-306`), so "configure it once per machine" means hand-editing
   `~/.config/niwa/config.toml` unless a setter is also added.

## Summary

Every file niwa writes into a dispatched instance exists on disk before the
worker process starts — `provisionInstanceFunc` at `dispatch.go:476` runs the
whole materialization pipeline synchronously, and the exec is ~200 lines later at
`dispatch.go:669` — so both a settings amendment and an argv injection are viable
insertion points, with `dispatch.go:557` (just after the existing single
`readInstanceSettings` read) the natural slot. The biggest implication is that
niwa already ships every piece a pre-approval would need: a `--settings` inline
JSON injection seam beside remote control (`dispatch.go:598`), a settings-merge
routine that appends `PreToolUse` entries after provisioning
(`watch.ApplyReviewSettings`), and a `PreToolUse` hook that literally prints
`permissionDecision: "allow"` (`watch.autoAllowHook()`), plus a three-layer
flag/downstream/host-default resolver template in `--keep-alive` — so the work is
choosing among existing seams, not building new plumbing. The biggest open
question is a Claude Code behavior fact, not a niwa one: the repo already records
that 2.1.258 stopped honoring `permissions.defaultMode` from the project
settings.json (which is why `--permission-mode` is derived and forwarded at
`dispatch.go:550-557`), so whether a settings-file `permissions.allow` rule or a
hook `allow` decision is honored at all — and whether either survives
`bypassPermissions` — decides which candidate is even buildable.
