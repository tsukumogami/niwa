# Lead: How does niwa dispatch launch the session, and where to inject a host default?

## Findings

### The dispatch launch path, end to end

`niwa dispatch` is one cobra command, `dispatchCmd`, defined in
`internal/cli/dispatch.go:97-120`. `Args: cobra.ExactArgs(1)` -- exactly one
positional arg (the prompt). Its `RunE` is `runDispatch` (`dispatch.go:122-277`).
The sequence:

1. Validate prompt (`dispatch.go:126-131`).
2. Resolve the enclosing workspace root from cwd via
   `workspace.ClassifyCwd` (`dispatch.go:137-148`).
3. Preflight `claude` on PATH via `lookClaude()` (`dispatch.go:152`,
   defined `dispatch.go:72-74`).
4. Build the ephemeral instance name suffix (`dispatch.go:164-174`).
5. Opportunistic reap (`dispatch.go:178`).
6. **Provision the instance** through the shared path:
   `provisionInstanceFunc(cmd.Context(), workspaceRoot, cwd, namePrefix, sep)`
   (`dispatch.go:181`). This is the SAME provisioner the SessionStart hook uses
   (`instance_from_hook.go:165`); production impl is `realProvisionInstance`
   (`instance_from_hook.go:344-388`).
7. Arm deferred self-rollback; drop pending marker (`dispatch.go:193-207`).
8. **Build passthrough and launch the worker**:
   `passthrough := buildDispatchPassthrough(slug)` (`dispatch.go:212`) then
   `dispatchLaunch(cmd.Context(), instancePath, prompt, passthrough)`
   (`dispatch.go:213`).
9. Capture session UUID by jobs-dir cwd correlation
   (`dispatch.go:228`, impl `dispatch_capture.go:41`).
10. Write durable `SessionMapping` with `Origin: "dispatch"`
    (`dispatch.go:235-246`).
11. Optionally `claude attach <shortID>` unless `--detach` (`dispatch.go:269-274`).

### Where claude argv is built (the actual exec)

`dispatchLaunch` is a package var wired to `realDispatchLaunch`
(`dispatch_launcher.go:14`). `realDispatchLaunch` (`dispatch_launcher.go:25-46`):

- `bin, _ := exec.LookPath("claude")`
- `args := buildClaudeBgArgs(prompt, passthrough)`
- `cmd := exec.CommandContext(ctx, bin, args...)`
- `cmd.Dir = instanceDir`
- `cmd.Env = os.Environ()` (`dispatch_launcher.go:40`)
- `cmd.Run()`

`buildClaudeBgArgs(prompt, passthrough)` (`dispatch_launcher.go:56-62`) returns
`["--bg", ...passthrough, prompt]`. Each value is a discrete argv element by
design (D8 anti-injection). This helper and `cmd.Env` are the two physical exec
seams, and both are reached ONLY from `realDispatchLaunch` -- i.e. dispatch only.
Neither the interactive/ephemeral hook path nor `niwa apply` touches them; those
paths launch `claude` outside niwa entirely (niwa only materializes their
instance, the user/agent runs `claude`).

### Can a caller already pass arbitrary claude flags through dispatch? No.

Passthrough is a CLOSED whitelist. `buildDispatchPassthrough(slug)`
(`dispatch.go:385-400`) forwards only:
- `--model <dispatchModel>` (flag `--model`)
- `--permission-mode <dispatchPermissionMode>`
- `--agent <dispatchAgent>`
- `--name <slug>` (the sanitized `--name`)

The only registered dispatch flags are `--label`, `--name/-n`, `--model`,
`--permission-mode`, `--agent`, `--detach/-d` (`dispatch.go:18-26`). There is NO
generic `--` / variadic passthrough, and `cobra.ExactArgs(1)` forbids extra
positionals. So today a caller cannot inject an arbitrary `claude` flag through
`niwa dispatch`; any new flag must be added explicitly to
`buildDispatchPassthrough`.

### Two different "global" layers -- do not conflate them

1. **Host-level niwa config** -- `~/.config/niwa/config.toml`, parsed to
   `config.GlobalConfig` (`internal/config/registry.go:13-30`). Its `[global]`
   table is `GlobalSettings` (`registry.go:27-30`), today holding only
   `clone_protocol` and `auto_install_plugins` (a `*bool`). Loaded via
   `config.LoadGlobalConfig()` (`registry.go:158`). This is the machine-scoped
   "host toggle" surface -- adding a field here is a one-line struct change.

2. **Global override repo layer** -- the user's `[global_config]` overlay repo's
   `niwa.toml` (`GlobalConfigOverrideFile`, `apply.go:183`), parsed to
   `config.GlobalConfigOverride` whose `[global]` is a `GlobalOverride`
   (`config.go:583-597`). Its `[global.claude.settings]` map is merged into every
   workspace's effective `claude.settings` by `MergeGlobalOverride`
   (`override.go:483`, settings "global wins per key" at `override.go:520-533`).
   Those effective settings are written to each instance's
   `.claude/settings.json` by the `SettingsMaterializer`
   (`materialize.go:646-650`, `settings := ctx.Effective.Claude.Settings`).

The exploration's key tension is exactly the boundary between these two. Putting
`remoteControl: on` in layer 2's `[global.claude.settings]` materializes it into
EVERY instance's settings.json -- hook-provisioned interactive sessions, `niwa
apply` instances, AND dispatch -- so it is NOT scopable to dispatch. The host
toggle therefore belongs in layer 1 (`GlobalSettings`) and must be consumed by
the dispatch launch path directly, not routed through `claude.settings`
materialization.

### Is the host config reachable from the dispatch command?

Yes, trivially. `realProvisionInstance` already calls
`config.LoadGlobalConfig()` (`instance_from_hook.go:373`) and
`config.GlobalConfigDir()` (`instance_from_hook.go:374`). `runDispatch` itself
does not currently import `config` (`dispatch.go:3-16` imports only `workspace`),
but adding `config.LoadGlobalConfig()` there is a trivial, well-precedented call
(it never errors on a missing file -- returns an empty config, `registry.go:171`).

### The three injection seams, evaluated

**(a) Inject an extra claude flag via passthrough / buildClaudeBgArgs.**
Cleanest mechanically. `runDispatch` reads the host toggle, and if on, appends
the remote-control flag to `passthrough` before `dispatchLaunch` (or
`buildDispatchPassthrough` reads it). Code changed: a struct field on
`GlobalSettings` (`registry.go:27`), a `config.LoadGlobalConfig()` call + append
in `runDispatch`/`buildDispatchPassthrough` (`dispatch.go:212`/`385`). Scope:
dispatch-only by construction -- `buildDispatchPassthrough` and
`buildClaudeBgArgs` are reached nowhere else. PRECONDITION: requires that
`claude --bg` exposes a CLI flag that enables Remote. (I could not confirm such a
flag exists in this repo; niwa never enumerates claude's flag surface. This is an
open question for the claude CLI side.)

**(b) Set an env var in cmd.Env before launch.**
`realDispatchLaunch` already sets `cmd.Env = os.Environ()` (`dispatch_launcher.go:40`);
appending one entry (e.g. `append(env, "CLAUDE_..._REMOTE=1")`) is a two-line
change. But the toggle must be threaded into `realDispatchLaunch`, which today
takes `(ctx, instanceDir, prompt, passthrough)` and does not receive config --
so either the signature gains a param, or a package-level var is set by
`runDispatch` before calling `dispatchLaunch` (mirroring the existing
`lookClaude`/`dispatchAttach`/`runClaudePluginCmd` seam-var pattern). Scope:
dispatch-only -- `realDispatchLaunch` is the dispatch launcher and nothing else
sets that worker's env. PRECONDITION: requires claude to honor an env var for
Remote. Env is also strictly inherited by the worker's child processes, which may
or may not be desirable.

**(c) Write/merge a key into the instance's settings.json before launch.**
Heaviest and most fragile. The instance's `.claude/settings.json` is a niwa-
MANAGED, fingerprinted file (the `dispatch_plugins.go:80-91` comment documents
that `--scope project` re-serializing it makes the next `niwa apply` report
"modified outside niwa", #179). Post-materialize hand-editing it from
`runDispatch` reintroduces exactly that hazard and duplicates the merge logic.
It is also not naturally dispatch-scoped: settings.json is written for every
instance by the shared provisioner, so a dispatch-only write would have to be a
special extra step after `provisionInstanceFunc` returns. Not recommended.

### How a downstream "off" override would be honored

The dispatch instance's settings.json already reflects the FULL effective merge
(workspace `[claude.settings]` + per-repo overrides via `MergeOverrides`
`override.go:46` + global overlay via `MergeGlobalOverride` `override.go:483`),
because dispatch provisions through the same `applier.Create` path
(`instance_from_hook.go:382`) and the materializer reads `ctx.Effective.Claude.Settings`
(`materialize.go:650`). So whatever the user puts in their workspace/overlay
`claude.settings` for the remote-control key IS present in the dispatched
instance's settings.json. Therefore, if the host toggle is injected as a flag (a)
or env var (b) that claude treats as a DEFAULT below settings.json precedence, a
downstream `settings.json` value cleanly overrides it. If claude treats CLI flags
as highest precedence (typical), seam (a) would be a hard override, not a
defaultable one -- in which case (b) env-var-as-default is more likely to satisfy
"downstream config can override," PENDING confirmation of claude's precedence
order. niwa does not control that precedence; it is a claude-CLI fact to verify.

`runDispatch` does NOT read the merged effective config itself -- `provisionResult`
returns only `{Name, Path}` (`instance_from_hook.go:90-93`); the effective config
is computed inside `applier.Create` and not surfaced back. So "have dispatch read
the effective remote-control value and decide" would require either re-reading the
just-written instance settings.json (the `readInstanceSettings` helper already
exists, `dispatch_plugins.go:163`) or plumbing the effective config out of
provision.

## Implications

The cleanest seam is **(a) append a claude flag to `passthrough` in `runDispatch`,
gated on a new `*bool` field in `config.GlobalSettings`** -- IF `claude --bg`
exposes a remote-enable flag. It is dispatch-scoped for free (passthrough is a
closed dispatch-only whitelist), reuses the established `config.LoadGlobalConfig()`
call already living one function away in `realProvisionInstance`, and is a few
lines: add the field (`registry.go:27`), load + conditionally append in
`runDispatch` before `dispatch.go:213`. If the override must be DOWNSTREAM-defeatable
and claude ranks flags above settings.json, fall back to **(b) env var**, set via a
package-level seam var (matching `lookClaude`/`dispatchAttach`) so
`realDispatchLaunch`'s `cmd.Env` (`dispatch_launcher.go:40`) carries it.
Option (c) (settings.json write) should be avoided -- it collides with niwa's
managed-file fingerprint and is not naturally dispatch-scoped. Crucially, the
toggle must live in host config layer 1 (`GlobalSettings`), NOT in the overlay's
`[global.claude.settings]` (layer 2), because layer 2 materializes into every
instance and cannot be scoped to dispatch.

## Surprises

- Dispatch and the interactive/ephemeral SessionStart hook share ONE provisioner
  (`provisionInstanceFunc` -> `realProvisionInstance`), so anything injected at
  the provision/settings-materialize layer is unavoidably shared with interactive
  sessions. The ONLY dispatch-exclusive code after provisioning is the
  `buildDispatchPassthrough` + `realDispatchLaunch` argv/env construction -- that
  narrow window is the sole place a dispatch-only effect can be applied.
- There is no `--` passthrough today; the dispatch flag set is a deliberately
  closed whitelist with strong anti-injection rationale (D8). A remote-control
  flag is a net-new whitelist entry, consistent with the existing design rather
  than a generic escape hatch.
- `cmd.Env = os.Environ()` (no augmentation) already exists, making env injection
  a near-trivial diff -- but the toggle is not currently threaded into
  `realDispatchLaunch`.

## Open Questions

- Does `claude --bg` actually expose a CLI flag to enable Claude Code Remote, and
  what is it? (Determines whether seam (a) is viable at all.) Not answerable from
  the niwa repo.
- Is there a claude ENV VAR that enables Remote, and what is claude's precedence
  between CLI flag, env var, and settings.json? (Determines whether the host
  default is downstream-overridable, and which of (a)/(b) satisfies that.)
- Should the host toggle live as a simple `*bool` on `GlobalSettings` (machine
  default), or is per-workspace granularity wanted (which would push it toward the
  overlay `[workspaces.<name>]` layer and complicate dispatch-only scoping)?
- Config rejects a `NIWA_WORKER_SPAWN_COMMAND` key (per exploration context) --
  worth confirming there is no adjacent reserved-key validation that a new
  `GlobalSettings` field would trip. (Not yet traced.)

## Summary

The only dispatch-exclusive seam is the argv/env built in
`buildDispatchPassthrough`/`buildClaudeBgArgs` and `realDispatchLaunch`'s
`cmd.Env` (`dispatch_launcher.go`), since provisioning/settings-materialization is
shared with interactive sessions -- so the host toggle must be a new `*bool` on
`config.GlobalSettings` read in `runDispatch` (via the already-nearby
`config.LoadGlobalConfig()`) and translated into a claude flag appended to
`passthrough`, or, if downstream-overridability requires it, an env var. The main
implication is that the toggle must NOT go in the overlay's
`[global.claude.settings]`, which materializes into every instance and cannot be
scoped to dispatch. The biggest open question is purely claude-side: whether
`claude --bg` exposes a Remote-enable flag or env var, and its precedence versus
settings.json, which decides between seam (a) and (b).
