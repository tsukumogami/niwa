# Lead

What does Claude Code do when launched with a permission mode the host forbids,
and what does `auto` mode need to work? Covers the valid `--permission-mode`
values, how a host disables `bypassPermissions` and `auto`, what a worker
launched by `niwa dispatch` sees in that case, how the CLI flag ranks against
`permissions.defaultMode`, and whether niwa can (and should) detect host policy
itself.

Claude Code version on the test host: 2.1.296.

## Findings

### 1. Valid permission modes today

`claude --help` (2.1.296) lists `--permission-mode <mode>` with choices
`"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"`.
`default` is still accepted: the docs say Manual mode's config value is
`default` and `manual` is an alias "wherever you type the value"
(https://code.claude.com/docs/en/permission-modes, "Available modes"). The
settings reference lists the same seven `permissions.defaultMode` values
(`default`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`,
`manual` as alias) (https://code.claude.com/docs/en/settings-reference,
`permissions.defaultMode`). `askPermissions` is not and never was a value
(already recorded in `docs/designs/current/DESIGN-inert-defaultmode-key.md:57-60`).

`--dangerously-skip-permissions` is the equivalent of
`--permission-mode bypassPermissions`; `--allow-dangerously-skip-permissions`
puts bypass in the Shift+Tab cycle without starting in it.

### 2. Disabling bypass, and what a launch with bypass then does

Policy key: `permissions.disableBypassPermissionsMode: "disable"`. Scope is
"Any file", typically managed settings. The settings reference says Claude Code
"then rejects the `--dangerously-skip-permissions` flag, and ignores an agent
definition's `permissionMode: bypassPermissions`" and that the key "takes
precedence over `--dangerously-skip-permissions`"
(https://code.claude.com/docs/en/settings-reference#permissions-disablebypasspermissionsmode).
Under `managedSourcesBehavior: "merge"` it's a lock that takes the strictest
value across sources (https://code.claude.com/docs/en/managed-settings,
"Compose every managed source"). An invalid value inside a managed source
"reads as its restrictive value" (same page, "Invalid values").

The docs never say what "rejects" looks like for `--permission-mode
bypassPermissions`. I measured it on 2.1.296 with `claude -p ... --tools ""
--no-session-persistence --output-format stream-json --verbose`:

| Launch | Extra settings (`--settings` file) | `permissionMode` in init event | Result |
|---|---|---|---|
| `--permission-mode bypassPermissions` | none | `bypassPermissions` | success, exit 0 |
| `--permission-mode bypassPermissions` | `{"permissions":{"disableBypassPermissionsMode":"disable"}}` | `default` | success, exit 0, no warning in the stream |
| `--permission-mode auto` | none | `auto` | success |
| `--permission-mode auto` | `{"disableAutoMode":"disable"}` | `default` | success, no warning |

So in a headless run the forbidden mode is a **silent downgrade to Manual
(`default`)**, not an error and not an exit. Caveat: I set the lock through
`--settings` (precedence level 2), not a real managed source; since the key's
scope is "Any file" the code path should be the same, but a managed-source run
wasn't available on this host.

There are other ways bypass gets refused that are separate from the policy key
(https://code.claude.com/docs/en/permission-modes, "Skip all checks with
bypassPermissions mode"):

- `claude --bg --permission-mode bypassPermissions` "is refused until you
  accept the dialog in an interactive session" when no
  `skipDangerousModePermissionPrompt: true` is recorded in user or managed
  settings. If the key is only in `.claude/settings.local.json`, the background
  session starts "with the bypass request ignored" and pins a notice
  (`Bypass permissions was requested at launch and ignored ...`). This matters
  directly: niwa launches Claude workers with `--bg`
  (`internal/agentplan/dispatch.go:378`). The key's scope is "User, local, or
  managed" (https://code.claude.com/docs/en/settings-reference#skipdangerousmodepermissionprompt),
  so niwa's own `--settings` document can't supply it.
- `--restricted` refuses bypass (v2.1.248+).
- Running as root/sudo on Linux/macOS refuses to start in bypass with
  `--dangerously-skip-permissions cannot be used with root/sudo privileges for
  security reasons`, unless inside a recognized sandbox.

I didn't run the `--bg` + disabled-bypass combination (it creates a real
background session), so whether `--bg` downgrades silently, pins a notice, or
refuses under the policy key is unverified.

### 3. What auto mode needs, and disabling it

From https://code.claude.com/docs/en/permission-modes, "Eliminate prompts with
auto mode":

- Plan: all plans. Team/Enterprise have it on by default; admins can turn it off.
- Model: on the Anthropic API, Opus 4.6+, Sonnet 4.6+, Haiku 5.5, or a Fable
  model; on Bedrock / Vertex / Foundry / the apps gateway, only Sonnet 5+,
  Opus 4.7+, Haiku 5.5, Fable. "On any other model, the session starts in
  Manual instead."
- Not turned off server-side by Anthropic, and not rejected by the server for
  the account; either answer keeps auto off for the rest of the session.
- Policy: `disableAutoMode: "disable"` (top-level; also accepted as
  `permissions.disableAutoMode`). "Any session that would otherwise start in
  auto mode, whether from `--permission-mode auto`, a settings file, or the
  built-in default, starts in `default` instead"
  (https://code.claude.com/docs/en/settings-reference#disableautomode).
- `CLAUDE_CODE_ENABLE_AUTO_MODE=1` was needed on third-party providers in
  v2.1.158-v2.1.206 and has no effect from v2.1.207.

General rule: "When the flag, a settings file, or the built-in default selects
`auto` but auto mode isn't available to the session, Claude Code starts the
session in Manual instead" (permission-modes, "Which mode a session starts in").

Auto in a headless run: a `-p` run with no `--permission-prompt-tool` has no
prompt to fall back to; "When repeated blocks reach a threshold, the action
doesn't run and Claude keeps working. Claude Code doesn't stop the run"
(permission-modes, "When auto mode falls back"). So auto is workable for an
unattended worker, but a blocked action quietly doesn't run.

The built-in default for `claude -p` / Agent SDK sessions is `default` in
sessions that fetch feature flags (auto only in some non-flag-fetching sessions
on 2.1.285+). So a worker launched with no flag usually starts in Manual, not
auto, even though interactive terminals now default to auto (2.1.283+).

### 4. CLI flag vs `permissions.defaultMode`, and precedence

Starting-mode order (permission-modes, "Which mode a session starts in"):

1. `--permission-mode` or `--dangerously-skip-permissions`
2. `permissions.defaultMode` in a settings file (settings precedence decides
   among files: managed > `--settings` > local > project > user,
   https://code.claude.com/docs/en/settings)
3. the built-in default

The settings reference states the flag "take[s] precedence over this key for
one session". The flag does not beat the *locks*: `disableBypassPermissionsMode`
and `disableAutoMode` override the flag, and the result is Manual (measured
above). A managed `defaultMode` does not lock anything; people "can still
switch to auto mode".

Project-scope caveat (already in niwa's designs): `auto` and
`bypassPermissions` "don't take effect from project or local settings ...
Before v2.1.257, `bypassPermissions` took effect from any file". A project-file
`auto` falls through to the built-in default (not to the user file's
`defaultMode`); a project-file `bypassPermissions` starts Manual. See
`docs/designs/current/DESIGN-inert-defaultmode-key.md:52-60` and
`docs/designs/current/DESIGN-dispatch-permission-mode.md:58-59,310-319`.

Open point: whether `auto`/`bypassPermissions` in a `--settings` *document*
take effect. The VS Code note says it reads "user, managed, and `--settings`
values", and the bypass-cycle note says `defaultMode: "bypassPermissions"` in
"user, `--settings`, or managed settings" enables it. That suggests a
`--settings` defaultMode would work, but niwa already uses the CLI flag, which
outranks it anyway.

### 5. niwa today, and whether it can detect host capability

`derivePermissionMode` (`internal/cli/dispatch.go:1237-1245`) returns the
explicit `--permission-mode` unchanged, otherwise `bypassPermissions` when the
agent's flag is `--permission-mode` and the recorded posture is `bypass`,
otherwise nothing. `buildDispatchPassthrough` (`internal/cli/dispatch.go:1267-1287`)
adds `--permission-mode <mode>` as discrete argv elements. The Claude launch
spec is `claude --bg ... --permission-mode ... --settings <doc>`
(`internal/agentplan/dispatch.go:375-390`). Nothing in niwa's Go code reads
managed settings or knows about `disableBypassPermissionsMode`/`disableAutoMode`
(grep finds them only in a PRD).

Where managed policy can live (https://code.claude.com/docs/en/managed-settings,
"Where each mechanism stores the policy" and "How Claude Code combines managed
sources"), ranked:

1. Server-managed (claude.ai console or gateway), cached locally
2. MDM: macOS `com.anthropic.claudecode` managed-preferences domain; Windows
   `HKLM\SOFTWARE\Policies\ClaudeCode` `Settings`
3. Files: `managed-settings.json` plus `managed-settings.d/*.json` in
   `/Library/Application Support/ClaudeCode/` (macOS), `/etc/claude-code/`
   (Linux/WSL), `C:\Program Files\ClaudeCode\` (Windows)
4. Windows HKCU (user-writable, only when no admin document is present)

Default is "first-wins": the highest-ranked source with any policy key is used
and the rest are skipped, except a few cross-source keys. There are also
host-supplied "parent settings" from an embedding host. So reading
`managed-settings.json` alone can give the wrong answer both ways: a remote or
MDM policy can disable bypass with no file, and a file that disables bypass can
be skipped because a higher source exists (under first-wins).

Local host check (read-only, permission keys only): the file-based
`managed-settings.json` exists at the macOS path but sets neither
`permissions.disableBypassPermissionsMode` nor `disableAutoMode`, nor any
`permissions.defaultMode`. No `com.anthropic.claudecode` managed-preferences
plist is present. The local server-managed settings cache is empty. User
settings set `permissions.defaultMode: "default"`. The control run confirmed
`--permission-mode bypassPermissions` *is* honored on this host in `-p`. So on
this host, Claude Code's own managed policy does not block bypass; whatever
makes this a "host that refuses bypass" is coming from somewhere other than the
documented Claude Code policy keys (an organizational rule, hooks, or a
`--bg`/acceptance issue), not from `disableBypassPermissionsMode`.

Cheaper, more accurate detection: Claude Code reports the mode it actually
chose. The `system`/`init` event of `--output-format stream-json --verbose`
carries `"permissionMode"` (observed values `bypassPermissions`, `auto`,
`default`), and `/status` / `claude doctor` name the managed source in force.
A probe run costs a model call, though (about $0.20 on Opus with a large
system prompt in my runs), so it isn't free either.

## Implications

- A forbidden mode does not fail the launch. It downgrades to Manual silently.
  For a `--bg` dispatch worker that means a worker the operator thinks is
  unattended actually sits waiting on approvals with nobody watching. This is
  the strongest argument for a host-level default: an operator on a
  bypass-blocked host should configure `auto` once, rather than discover the
  downgrade worker by worker.
- `auto` is the right fallback on a bypass-blocked host, but it has its own
  failure: it also silently becomes Manual when the model isn't supported (for
  example a dispatched worker pinned to an older model or a third-party
  provider model), when `disableAutoMode` is set, or when the server turns it
  off. A host default of `auto` is only as good as the worker's model choice;
  niwa resolves `--model` (`buildDispatchPassthrough`) and the two interact.
- Precedence works in niwa's favor: the CLI flag beats every `defaultMode`, so
  a host-level niwa default turned into `--permission-mode <mode>` behaves
  identically regardless of workspace or project settings, apart from the locks.
  The `--permission-mode` flag on `niwa dispatch` overriding the host default
  mirrors Claude's own flag-over-settings rule.
- niwa should not try to read managed policy to pick a mode. The policy can be
  in four ranked sources plus parent settings, with first-wins/merge rules and
  version-dependent behavior; a file-only check is wrong in both directions, and
  reimplementing the full resolution chases Claude Code releases. The
  operator-declared host default is the better source of truth. If niwa wants a
  safety net, a post-launch check is more accurate than a pre-launch guess:
  surface a warning when the worker's reported mode differs from the requested
  one (if the `--bg` path exposes it), or point to `/status`.
- `--bg` adds a bypass-specific trap: without `skipDangerousModePermissionPrompt:
  true` in user or managed settings, bypass is refused or ignored for background
  sessions regardless of policy. niwa can't fix that through its `--settings`
  document (that key's scope excludes `--settings`), so docs for a
  `bypassPermissions` host default should say the operator must have accepted
  the dialog once interactively.

## Surprises

- The lock produces no error and no warning in a `-p` stream: exit 0,
  `permissionMode: "default"`, normal result. Nothing in the output says the
  requested mode was dropped unless you compare the init event's
  `permissionMode` to what you asked for.
- On this host, Claude Code's managed policy does not disable bypass at all,
  and bypass works headless. The premise "this host refuses bypassPermissions"
  doesn't come from `disableBypassPermissionsMode` here.
- `--settings` is a settings source above project and local, and per the docs
  it can carry a working `defaultMode: "bypassPermissions"` where project files
  can't. That's a channel niwa doesn't use (it uses the flag) and probably
  shouldn't start using.
- The built-in default for `-p`/Agent SDK sessions is still Manual in most
  configurations even though interactive sessions now default to auto, so
  "launch with no flag" doesn't give a worker auto.

## Open Questions

- What exactly does `claude --bg --permission-mode bypassPermissions` do when
  `disableBypassPermissionsMode` is set: silent Manual, a pinned notice like the
  settings.local.json case, or a refusal? Same for `--bg --permission-mode auto`
  with `disableAutoMode`. Worth one manual test in a throwaway instance.
- Can niwa see the worker's effective mode after a `--bg` launch (session
  metadata, Agent View, a `claude` subcommand) cheaply enough to warn on
  mismatch?
- What is the real source of "refuses bypass" on the host that motivated this
  work, if it isn't the managed policy key? Possibly the `--bg` acceptance
  requirement, an org rule outside Claude Code settings, or a policy this host
  hasn't received yet.
- Does a `defaultMode` in a `--settings` document honor `auto` and
  `bypassPermissions` (the docs imply yes)? Only relevant if niwa ever stops
  using the flag.
- Should a host default of `auto` be paired with a check that the resolved
  worker model supports auto mode, given the silent Manual fallback?

## Summary

When a host forbids a mode (`permissions.disableBypassPermissionsMode` or `disableAutoMode`, both valid in any settings file and usually in managed policy), a headless launch with `--permission-mode bypassPermissions` or `auto` doesn't error: measured on Claude Code 2.1.296, it silently starts in Manual (`default`) with exit 0. Auto also falls back to Manual on unsupported models or a server-side off switch, and `--bg` bypass launches separately need `skipDangerousModePermissionPrompt` accepted in user or managed settings. The CLI flag outranks every `defaultMode` but not those locks, and managed policy can come from four ranked sources (server, MDM, files, HKCU) with first-wins rules. So niwa should take an operator-declared host default rather than read `managed-settings.json` (on this host that file doesn't block bypass at all), and if it wants a safety net, compare the worker's reported `permissionMode` to the requested one after launch.
