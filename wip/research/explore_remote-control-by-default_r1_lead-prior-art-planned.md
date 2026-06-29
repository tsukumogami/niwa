# Lead: Prior art and planned work for remote-control on dispatched sessions

## Findings

### 1. There is NO prior art for "remote control" / Claude Code Remote / claude.ai connectivity

A repo-wide grep for `remote-control`, `claude code remote`, `teleport`, `claude.ai`,
`claudeRemote`, `enableRemote`, `connect` found **nothing** about remotely controlling
or connecting a dispatched session from claude.ai or the Agent View web surface. The
only `remote-controlled` hits are unrelated (an untrusted-input warning in
`docs/designs/current/DESIGN-init-bootstrap-empty-source.md:643,1269`). The word
"remote" elsewhere means git remotes, not session remote-control.

So the feature is greenfield in niwa. "Agent View" in this codebase always means the
LOCAL `claude agents` TUI (list/attach/stop background sessions on the same host), never
a cloud/remote-control surface.

### 2. Dispatch and ephemeral-session docs explicitly scope OUT remote/cross-machine

- `docs/briefs/BRIEF-instance-dispatch.md:154-155` (Scope Boundary -> Out):
  > "Cross-machine or remote dispatch. The command launches a local background session
  > on the same host."
- `docs/briefs/BRIEF-ephemeral-session-instances.md:133` (Out):
  > "Sharing or resuming an instance across more than one session, and cross-machine
  > session/instance resume."
- `test/live/dispatch_live_test.go:134`: the worker "runs headless and is managed via
  claude" (local management assumption).

Important nuance: the stated non-goal is **cross-machine dispatch / launching a worker
on another host**. Enabling Claude Code Remote ("remote-control") as a connectivity
attribute on a *locally* launched `claude --bg` session is NOT the same thing as
cross-machine dispatch, and is not directly addressed by these non-goals. But the docs'
framing ("launches a LOCAL background session on the same host") is the closest stated
boundary and should be respected / explicitly distinguished in any new design.

### 3. `niwa dispatch` is BUILT and shipped; it launches `claude --bg` with a fixed,
   small pass-through flag set

The dispatch command is fully implemented, not planned:
- Command: `internal/cli/dispatch.go` (`niwa dispatch <prompt>`, top-level verb).
- Launcher: `internal/cli/dispatch_launcher.go` -- `realDispatchLaunch` runs
  `claude --bg <prompt>` with `cmd.Dir = instanceDir` and `cmd.Env = os.Environ()`
  (inherits the parent environment).
- DESIGN: `docs/designs/current/DESIGN-instance-dispatch.md`; PRD:
  `docs/prds/PRD-instance-dispatch.md`; BRIEF: `docs/briefs/BRIEF-instance-dispatch.md`.

The pass-through flag surface is deliberately bounded. From `dispatch.go:18-35` the only
flags are `--label`, `--name/-n`, `--model`, `--permission-mode`, `--agent`, `--detach/-d`.
`buildDispatchPassthrough` (`dispatch.go:385-400`) forwards only `--model`,
`--permission-mode`, `--agent`, and `--name <slug>` to `claude --bg`. The argv is built
as discrete elements specifically so a value can't smuggle in an extra claude flag
(`dispatch_launcher.go:56-62`, "Decision 8 / security note 1").

**There is currently NO host-level / settings-driven way to inject additional `claude`
flags or env into the dispatch launch.** Passthrough is hardcoded per-flag; the launcher
only inherits ambient `os.Environ()`. Any new "remote-control on by default" knob would
be a net-new addition to this passthrough/launch path. DESIGN D1
(`DESIGN-instance-dispatch.md:82-102`) is where the flag surface is justified and is the
natural extension point.

### 4. The "existing global override layer" exists and already carries Claude config

The core question's "host-level setting in the existing global override layer" maps to
a real, shipped layer:
- `DESIGN-global-config.md` -- a user-owned global config repo synced independently;
  merge chain is **workspace defaults -> global overrides -> per-repo overrides**
  (`DESIGN-global-config.md:52,60`). Inert when not configured
  (`DESIGN-global-config.md:51`).
- Structs in `internal/config/config.go`:
  - `GlobalConfigOverride` (line 601): `[global]` flat section +
    `[workspaces.<name>]` per-workspace sections.
  - `GlobalOverride` (line 583): carries `Claude *ClaudeOverride`, `Env`, `Files`,
    `Vault`, `EnvExamplePolicy`, `EnvOutput`.
  - `ClaudeOverride` (line 52): `Enabled`, `Plugins`, `Hooks`, `Settings SettingsConfig`,
    `Env ClaudeEnvConfig`.
- `internal/config/registry.go:14` `GlobalConfig` holds the global config repo URL and
  local clone path (registered via `niwa config set global`).

So a per-host/per-user override rung already exists and already reaches Claude settings
and env that get **materialized into an instance's `settings.json`** at apply/create
time (`internal/workspace/materialize.go`, `root_materializer.go`). What it does NOT
currently do is influence the `claude --bg` *launch argv* in `dispatch.go` -- the global
override feeds instance materialization, not the dispatch launcher. Connecting the two
(a global-override-sourced default that adds a launch flag/env) is the gap.

### 5. Precedent: dispatch/ephemeral config is gated by an opt-in/opt-out default already

The ephemeral-session feature establishes the exact "overridable default" pattern the
new feature wants to mirror:
- `docs/guides/ephemeral-session-instances.md:348-358`: "The feature installs by
  default at `niwa init`. To skip it: `niwa init <name> --no-ephemeral-sessions`."
- Guard #1 (`ephemeral-session-instances.md:91-95`): an "opt-in master switch -- a
  workspace-root state flag, default off". (Note: the guide describes ephemeral *mode*
  as default-off at the state-flag level but installed-by-default at init; the
  `--no-ephemeral-sessions` flag is the opt-out.)
- `PRD-ephemeral-session-instances.md:147`: opt-out keeps "plain background sessions at
  the root".

Also relevant: the root `settings.json` already carries a `permissions.defaultMode`
posture sourced the same way instance materialization sources it
(`ephemeral-session-instances.md:300-316`), and the guide warns settings "resolve at
launch and cannot be scoped per session" -- a precedent both for *how* a default config
value flows to launched sessions and for the caveat that root-level settings hit every
root session, not just workers.

### 6. No GitHub issue or roadmap/decision doc covers this

- `gh issue list` (origin `tsukumogami/niwa`): no open issue mentions remote-control,
  Agent View remote, teleport, or claude.ai. Issue #53 ("niwa config set global is too
  coarse-grained: can only point at an entire remote repo", enhancement/needs-design) is
  the only globally-relevant one -- it's about granularity of the global config layer,
  not remote control, but it touches the same layer this feature would extend.
- `docs/roadmaps/` and `docs/decisions/` directories do not exist (no roadmap or ADR
  artifacts in this repo). Doc types present: briefs, designs, guides, prds, spikes.
- Relevant existing docs (paths + one-liners):
  - `docs/designs/current/DESIGN-instance-dispatch.md` -- dispatch command design;
    flag surface (D1) is the extension point.
  - `docs/prds/PRD-instance-dispatch.md` -- dispatch requirements (R14 launch via
    `claude --bg`, R15 prompt as single arg).
  - `docs/briefs/BRIEF-instance-dispatch.md` -- dispatch framing; "remote dispatch" non-goal.
  - `docs/designs/current/DESIGN-ephemeral-session-instances.md` -- hook-based
    ephemeral-instance mechanism (sibling feature).
  - `docs/prds/PRD-ephemeral-session-instances.md` / `BRIEF-ephemeral-session-instances.md`
    -- ephemeral-session framing/requirements; opt-out default pattern.
  - `docs/spikes/SPIKE-ephemeral-session-instances.md` -- feasibility spike for
    SessionStart/SessionEnd hooks + `claude --bg`.
  - `docs/designs/current/DESIGN-global-config.md` -- the global override layer
    (workspace -> global -> per-repo merge).
  - `docs/spikes/SPIKE-dispatched-worker-pr-template-gap.md` -- notes dispatch launches
    `claude --bg ...` "with no readiness gate" (relevant to launch-time behavior).

### 7. Is passing extra `claude` flags through dispatch a supported convention?

Partially. `niwa dispatch` supports a **fixed, enumerated** set of pass-through flags
(`--model`, `--permission-mode`, `--agent`) wired one-by-one in `buildDispatchPassthrough`
(`dispatch.go:385-400`). There is **no generic flag-forwarding mechanism** (no `--`
escape hatch, no settings-sourced extra args) and **no host/global-config-sourced
default** that augments the launch. Adding remote-control-by-default means adding a new
flag/env to this enumerated passthrough whose default is sourced from the global override
rung -- a small, additive change consistent with how `--model` etc. were added, but new.

## Implications

- Greenfield: no remote-control prior art to reuse or contradict; nothing is partially
  built for this specific feature. The mechanics (dispatch launch path, global override
  layer, opt-out-default pattern) all exist and are the building blocks.
- The relevant non-goal ("cross-machine or remote dispatch... local session on the same
  host") is about WHERE the process runs, not about whether a locally-run session is
  remote-controllable. A new design must explicitly distinguish "remote-control
  connectivity on a local `claude --bg` session" from "cross-machine dispatch" to avoid
  appearing to violate the stated boundary -- and ideally update the BRIEF/PRD boundary
  wording so the distinction is on record.
- The global override layer (`GlobalOverride.Claude` / `GlobalConfigOverride`) is the
  correct home for a host-level overridable default, and `niwa config set global` /
  issue #53's granularity discussion is the surrounding context.
- The dispatch launcher (`dispatch.go` `buildDispatchPassthrough` + `dispatch_launcher.go`)
  is the single, well-isolated insertion point. The argv-as-discrete-elements security
  invariant (Decision 8) must be preserved for any new flag/env.
- The `--no-ephemeral-sessions` / default-off-but-installed pattern is the precedent for
  "overridable default" wording and ergonomics.

## Surprises

- `niwa dispatch` already attaches the terminal by default (Docker-style;
  `--detach`/`-d` to skip) -- DESIGN D1 explicitly weighed and rejected `--headless`
  naming because it "connotes no UI" (`DESIGN-instance-dispatch.md:97-101`). So "remote"
  vs "headless" framing has already been litigated once in this repo for the attach flag.
- The dispatch launcher inherits the full parent environment (`cmd.Env = os.Environ()`),
  so an env-var-based remote-control toggle would already propagate to the worker without
  touching argv -- a possible lighter-weight mechanism than a new flag.
- No `docs/roadmaps/` or `docs/decisions/` directories exist at all; sequencing/ADR
  artifacts simply aren't a convention in this repo, so "planned/sequenced work" lives in
  GitHub issues + the brief/PRD/design chain, none of which mention this feature.

## Open Questions

- Does Claude Code expose a `claude --bg` flag OR an env var that enables Claude Code
  Remote / claude.ai remote-control? (Needs a claude-code-capability check; the niwa repo
  has no record of one.) The chosen mechanism -- new passthrough flag vs inherited env var
  -- depends on this.
- Should the default live as a workspace-root state flag (like ephemeral mode), in the
  `GlobalOverride.Claude` rung, or both (global default, workspace override)? The merge
  semantics (`workspace -> global -> per-repo`) need a defined precedence for a launch-time
  toggle, which is a new kind of value for that layer (it currently feeds materialization,
  not launch argv).
- Does enabling remote-control by default conflict with the security posture of
  bypass-permissions workers, or with the "local same-host" framing in the brief? Needs a
  decision + a boundary-wording update.
- Per-session scoping: root `settings.json` "cannot be scoped per session"
  (`ephemeral-session-instances.md:300-316`). If remote-control is set via settings rather
  than a dispatch-launch flag, it would hit all root sessions, not just dispatched
  workers -- so a dispatch-launch-flag mechanism is likely required to scope it to workers.

## Summary
No prior art exists in niwa for remote-control/claude.ai connectivity on dispatched
sessions; `niwa dispatch` is fully built but launches `claude --bg` with only a fixed,
enumerated pass-through flag set (`--model`/`--permission-mode`/`--agent`) and no
host/global-config-sourced launch default, while the relevant non-goal scopes out
cross-machine dispatch (a distinct concern from remote-controlling a local session). The
main implication: the global override layer (`GlobalOverride.Claude`) plus the
`--no-ephemeral-sessions` overridable-default pattern are the right building blocks, and
the dispatch launcher's `buildDispatchPassthrough` is the single clean insertion point,
but no design connects the override layer to the launch argv today. The biggest open
question is whether Claude Code actually exposes a `--bg` flag or env var to enable
remote-control, and whether to wire the default via the global-override layer or a
workspace-root state flag while scoping it to workers rather than all root sessions.
