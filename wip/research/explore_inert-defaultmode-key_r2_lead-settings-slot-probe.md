# Lead: The --settings slot -- can niwa vacate it, and does it merge?

Measured against **Claude Code 2.1.267** (`claude --version` -> `2.1.267 (Claude Code)`),
native binary at `/home/dangazineu/.local/share/claude/versions/2.1.267`, linked from
`/home/dangazineu/.local/bin/claude`. Linux x86-64.

Scratch space: `/home/dangazineu/.claude/jobs/276fb88b/tmp`.
Nothing in the niwa repo was touched. The user's real `~/.claude/settings.json` was never
written (mtime before the probes: `2026-09-09 22:37:09`; unchanged after).

## Findings

### A. --remote-control and --bg

**It composes. `--remote-control` works alongside `--bg`, and the backgrounded session
actually connects to Remote Control.**

The probe (run from a throwaway project directory, deliberately trivial prompt, cheap model):

```
env -C /home/dangazineu/.claude/jobs/276fb88b/tmp/proj \
  claude --bg --remote-control=rcprobe-276fb88b --model haiku 'reply with exactly: RCPROBE'
```

Output:

```
backgrounded · 38f8b3ec
  claude agents             list sessions
  claude attach 38f8b3ec    open in this terminal
  claude logs 38f8b3ec      show recent output
  claude stop 38f8b3ec      stop this session
```

No validation error, no warning. Then:

```
claude logs 38f8b3ec
```

The terminal capture of the background session shows, in order, the status line going to
`/rc connecting…` (amber), and then the connected banner:

```
/remote-control is active · Continue here, on your phone, or at
https://claude.ai/code/session_01VCoz7nFEsYuSq6HVKE2KU7
```

with the `/rc` indicator turning green. The session also answered the prompt (`RCPROBE`).
So this is not "flag accepted and ignored" -- the bridge came up inside a background
session with no terminal attached.

**Cleanup performed.** The probe session was stopped and deleted immediately:

```
claude stop 38f8b3ec     ->  stopped 38f8b3ec
claude rm 38f8b3ec       ->  removed 38f8b3ec
```

`claude agents --json` afterwards lists only pre-existing sessions; `38f8b3ec` is gone.
**No stray background or remote-control session was left running.**

#### Does it require an interactive terminal?

No terminal, but yes a REPL. Two data points:

1. The `--bg` probe above had no TTY attached at any point and Remote Control still
   engaged. `--bg` runs a real (headless, pty-backed) REPL in the daemon, and that is
   enough.
2. In `--print` mode the flag is **silently accepted and does nothing**:

```
env -C /home/dangazineu/.claude/jobs/276fb88b/tmp/proj \
  claude -p 'reply with exactly: OK' --model haiku --remote-control=rcprobe2-276fb88b
```

Output was exactly `OK`. No error, no warning, no session URL, no remote session created.
There is no argv validation rejecting `--remote-control` with `--print` (the binary has
explicit `Error: --X requires --print`-style checks for a dozen other flags; none for this
one). The bridge lives in the REPL app state (`replBridgeEnabled`), and `-p` has no REPL,
so the flag is inert there. **A builder that emits `--remote-control` on a `-p` invocation
gets silence, not a diagnostic.**

#### It is the same switch that `remoteControlAtStartup` throws

Decompiling the bundled JS out of the binary (`strings -n 8` over the ELF; the bun bundle
is embedded as readable JS) shows both inputs feeding one resolution:

```js
function Mi({remoteControlFlag: C, isRemoteThinClient: E}) {
  let O = env.CLAUDE_CODE_REMOTE,
      P = g7t(),                                   // resolveExplicitRemoteControlAtStartup
      R = !E && !O && !C && P.value === void 0 ? CYt() : void 0,
      T = !E && !O && (C || (P.value ?? R?.value ?? false)),
      ...
```

`C` is the `--remote-control` flag; `P` is the `remoteControlAtStartup` setting. `T` (the
"turn Remote Control on" verdict) is `C || P.value`. The flag and the setting are
interchangeable for enabling, and the flag short-circuits ahead of the setting.

Also worth having on record, because it constrains where the *setting* may live:

```js
function g7t() {
  let e = require("chunk-yjq8982j.js"),
      n = e.projectSettingsAliasesUserSettings() ? undefined
            : e.getSettingsForSource("projectSettings")?.remoteControlAtStartup,
      r = e.getSettingsForSource("localSettings")?.remoteControlAtStartup;
  if (n === false || r === false) return {value: false, source: "project_or_local_false"};
  let o = e.getSecuritySensitiveSettingWithSources("remoteControlAtStartup")[0], ...
  if (p.value !== true && (n === true || r === true))
    t(`remoteControlAtStartup: true in ${...} settings ignored — repo-scoped settings ` +
      `cannot enable Remote Control; set it at user scope (/config)`);
```

with

```js
var Nge = {policySettings: "policy", flagSettings: "flag", userSettings: "user"};
```

So `remoteControlAtStartup: true` is honored from exactly three sources: managed policy,
**`--settings` (the "flag" source)**, and user settings. A `true` in project or local
settings is discarded with a warning; a `false` there force-disables. That is precisely
why niwa cannot push this key down into a repo-scoped settings file and has been holding
the `--settings` slot for it.

#### Naming

`--remote-control [name]` is strictly *more* expressive than the setting, not less:

- `remoteControlAtStartup` is a plain boolean. It carries no name at all.
- `--remote-control=<name>` names the **Remote Control session** (the thing you see at
  claude.ai/code and in the mobile app). It does **not** rename the local background
  session: after the probe, `claude agents --json` showed
  `"name": "reply with exactly: RCPROBE"` -- the bg display name still came from the
  prompt, not from `rcprobe-276fb88b`. `-n/--name` continues to own the local name, so the
  two do not collide.
- There is also `--remote-control-session-name-prefix <prefix>` (default: hostname) and the
  env var `CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX` for auto-generated names.
- `--rc` is a documented alias for `--remote-control` (hidden from `--help`, present in the
  option table).

#### One trap that is *not* the root flag

`claude remote-control` also exists as a hidden **subcommand** ("Control local sessions
from claude.ai/code or the Claude mobile app"), and it refuses most root options placed
before the verb -- `--settings` among them:

```
env -C .../proj timeout 25 claude --settings .../pm.json remote-control
```

```
Error: `--settings` before `remote-control` is not carried over to the sessions Remote
Control starts, so Remote Control refuses to start rather than drop it — remove it, and
give Remote Control's own options after the verb (see `claude remote-control --help`).
```

This is a different code path from the `--remote-control` root flag and does not apply to
the `--bg` invocation shape. Mentioned only so nobody conflates the two while reading
`--help`.

### B. --settings merge vs replace

**It merges.** A `--settings` document is a real settings *source* that composes with user,
project and local settings; it does not replace them. And the merge is deep, not top-level
key replacement.

Setup. Throwaway project at `/home/dangazineu/.claude/jobs/276fb88b/tmp/proj` with
`.claude/settings.json`:

```json
{
  "env": {
    "PROBE_PROJECT_ENV": "project-env-value",
    "PROBE_SHARED_ENV": "from-project"
  },
  "includeCoAuthoredBy": false,
  "permissions": { "deny": ["Bash(rm:*)"] },
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command",
      "command": "echo \"PROJECT_HOOK_FIRED PROJ=[$PROBE_PROJECT_ENV] INLINE=[$PROBE_INLINE_ENV] SHARED=[$PROBE_SHARED_ENV]\" >> .../out/marker.txt" }] }]
  }
}
```

The `SessionStart` hook is the observation instrument: it fires before any model turn, and
because settings `env` vars are injected into the process the hook inherits, one hook line
reports both *which sources contributed hooks* and *which env keys survived*.

The `--settings` document (`.../tmp/inline.json`) supplies a *different* env key, a
*colliding* env key, and its own `SessionStart` hook:

```json
{
  "env": {
    "PROBE_INLINE_ENV": "inline-env-value",
    "PROBE_SHARED_ENV": "from-inline"
  },
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command",
      "command": "echo \"INLINE_HOOK_FIRED PROJ=[$PROBE_PROJECT_ENV] INLINE=[$PROBE_INLINE_ENV] SHARED=[$PROBE_SHARED_ENV]\" >> .../out/marker.txt" }] }]
  }
}
```

**Run 1 (control, no `--settings`):**

```
env -C /home/dangazineu/.claude/jobs/276fb88b/tmp/proj \
  claude -p 'reply with exactly: OK' --model haiku
```

```
PROJECT_HOOK_FIRED PROJ=[project-env-value] INLINE=[] SHARED=[from-project]
```

**Run 2 (`--settings` as a file path):**

```
env -C /home/dangazineu/.claude/jobs/276fb88b/tmp/proj \
  claude -p 'reply with exactly: OK' --model haiku \
  --settings /home/dangazineu/.claude/jobs/276fb88b/tmp/inline.json
```

```
PROJECT_HOOK_FIRED PROJ=[project-env-value] INLINE=[inline-env-value] SHARED=[from-inline]
INLINE_HOOK_FIRED  PROJ=[project-env-value] INLINE=[inline-env-value] SHARED=[from-inline]
```

Three things fall out of that single line pair:

- **Both hooks fired.** The project file's `hooks.SessionStart` array was *not* discarded;
  the inline document's entry was appended to it. Arrays concatenate across sources.
- **`PROBE_PROJECT_ENV` survived** next to `PROBE_INLINE_ENV`. The `env` object was merged
  key by key -- this is not even top-level-key replacement, where the inline `env` object
  would have shadowed the project one wholesale.
- **`PROBE_SHARED_ENV` resolved to `from-inline`.** On a genuine key collision, `--settings`
  outranks the project file.

**Run 3 (add a user-scope layer, isolated):** to prove `--settings` also merges with user
settings without touching the real `~/.claude/settings.json`, the run was repeated with
`CLAUDE_CONFIG_DIR` pointed at a scratch config home holding its own `settings.json`
(`PROBE_USER_ENV`, `PROBE_SHARED_ENV: "from-user"`, plus a `USER_HOOK_FIRED` SessionStart
hook):

```
CLAUDE_CONFIG_DIR=/home/dangazineu/.claude/jobs/276fb88b/tmp/cfg \
env -C /home/dangazineu/.claude/jobs/276fb88b/tmp/proj \
  claude -p 'reply with exactly: OK' --model haiku \
  --settings /home/dangazineu/.claude/jobs/276fb88b/tmp/inline.json
```

```
USER_HOOK_FIRED    PROJ=[project-env-value] INLINE=[inline-env-value] USER=[user-env-value] SHARED=[from-inline]
PROJECT_HOOK_FIRED PROJ=[project-env-value] INLINE=[inline-env-value] SHARED=[from-inline]
INLINE_HOOK_FIRED  PROJ=[project-env-value] INLINE=[inline-env-value] SHARED=[from-inline]
```

All three layers contributed hooks; all three env keys coexist; `--settings` won the
collision over both user and project.

**Run 4 (`--settings` as an inline JSON string, the form niwa uses):** same result. Driven
from `.../tmp/run_inline_string.sh` so the JSON survives quoting:

```
CLAUDE_CONFIG_DIR="$D/cfg" claude -p 'reply with exactly: OK' --model haiku \
  --settings '{"env":{"PROBE_INLINE_ENV":"inline-string-value","PROBE_SHARED_ENV":"from-inline-string"},"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo \"INLINE_STRING_HOOK_FIRED ...\" >> .../out/marker.txt"}]}]}}'
```

```
USER_HOOK_FIRED           PROJ=[project-env-value] INLINE=[inline-string-value] USER=[user-env-value] SHARED=[from-inline-string]
INLINE_STRING_HOOK_FIRED  PROJ=[project-env-value] INLINE=[inline-string-value] USER=[user-env-value] SHARED=[from-inline-string]
PROJECT_HOOK_FIRED        PROJ=[project-env-value] INLINE=[inline-string-value] SHARED=[from-inline-string]
```

The file form and the inline-string form take the identical path.

#### Isolation held

The isolated `CLAUDE_CONFIG_DIR` genuinely took: the scratch dir was populated with a fresh
`.claude.json`, `projects/`, `sessions/` and `session-env/`, and the user-scope hook that
fired was the scratch one. The real `~/.claude/settings.json` mtime was `22:37:09` before
the probes and unchanged after. A copy of `~/.claude/.credentials.json` was placed in the
scratch config home so the isolated runs could authenticate, and **deleted afterwards**.

#### Merge is per-key, not uniform

Important caveat for anyone writing a builder. "Merge" is the default, but the binary's own
settings schema documents per-key composition rules that differ, e.g.:

- `modelPicker`: "Honored from managed, `--settings`/SDK, and user settings only (not from a
  project checkout); the highest-precedence of those that defines modelPicker wins outright
  (**no merging across sources**)."
- `allowedHttpHookUrls` / `allowedHttpHookEnvVars`: "Arrays merge across settings sources
  (same semantics as allowedMcpServers)."
- `remoteControlAtStartup`, `processWrapper`, `disableRemoteControl` and friends are
  "security-sensitive" and read only from a subset of sources (policy / `--settings` /
  user), with project and local ignored or restricted to the disabling direction.
- `disableRemoteControl`'s own doc: "Disable Remote Control (claude.ai/code,
  `claude remote-control`, `--remote-control`/`--rc`, auto-start, and the in-session
  toggle). Typically set in managed settings."

So the safe statement is: **`--settings` participates in the normal layered merge as a
distinct, high-precedence source named `flagSettings`** -- not that every key deep-merges.

#### `--settings` vs `--permission-mode`

Cheap, and measured. Observation was the `permissionMode` field on the `init` event of
`--output-format stream-json`.

`--settings` alone (`.../tmp/pm.json` = `{"permissions":{"defaultMode":"plan"}}`):

```
CLAUDE_CONFIG_DIR=/home/dangazineu/.claude/jobs/276fb88b/tmp/cfg \
env -C /home/dangazineu/.claude/jobs/276fb88b/tmp/proj \
  claude -p 'reply with exactly: OK' --model haiku \
  --output-format stream-json --verbose \
  --settings /home/dangazineu/.claude/jobs/276fb88b/tmp/pm.json
```

-> `"permissionMode":"plan"`

Both together:

```
CLAUDE_CONFIG_DIR=/home/dangazineu/.claude/jobs/276fb88b/tmp/cfg \
env -C /home/dangazineu/.claude/jobs/276fb88b/tmp/proj \
  claude -p 'reply with exactly: OK' --model haiku \
  --output-format stream-json --verbose \
  --permission-mode acceptEdits \
  --settings /home/dangazineu/.claude/jobs/276fb88b/tmp/pm.json
```

-> `"permissionMode":"acceptEdits"`

**`--permission-mode` wins over `permissions.defaultMode` in a `--settings` document.** The
dedicated flag outranks the settings source. Silently -- no warning that the settings value
was overridden.

## Implications

**The single-`--settings`-slot constraint is real, but niwa's occupancy of the slot is not.**
`--settings` really is one slot and round 1 established that a second occurrence silently
discards the first. That part is durable and any builder still has to produce exactly one
document. But the *reason* niwa is holding the slot -- remote control -- dissolves.
`--remote-control` (or `--rc`) composes with `--bg`, connects for real in a background
session with no terminal, is the same internal switch (`C || P.value`), and adds a session
name the boolean setting cannot express. niwa can drop
`--settings '{"remoteControlAtStartup":true}'` and pass `--remote-control` (optionally
`--remote-control=<instance-slug>`) instead, vacating the slot entirely.

That reframes the sibling session's problem. The slot is not "already taken and therefore
contested" -- it is currently taken by something that has a first-class flag. Whatever
niwa wants to put in the slot next gets a clean slot, and the exploration should say so
rather than designing around a phantom occupant.

**A merged settings-document builder is on solid ground, with one correction to its mental
model.** The worry that motivated the lead -- "a builder that assumes merge and gets
replacement is a new class of bug" -- does not materialize. `--settings` is a settings
*source* (`flagSettings`), sitting between managed policy and user settings, and it composes
with user/project/local exactly the way the layered settings system composes anything else:
arrays concatenate, objects merge key-wise, scalar collisions go to the higher-precedence
source. A builder that emits a partial document containing only the keys it cares about is
correct and will not silently nuke a repo's `.claude/settings.json`.

The correction: the builder must not assume *uniform* deep merge. Several keys have
documented per-key rules -- some do not merge across sources at all (`modelPicker`), and
several security-sensitive keys are only read from a subset of sources. A builder that
needs to guarantee a value should check that the key it is setting is actually honored from
the `flagSettings` source. And it should know that a dedicated CLI flag beats the settings
document for the same concept: `--permission-mode` overrode `permissions.defaultMode`
silently. If both a flag and a settings key can express something, the flag wins, and the
builder should either own both or own neither -- writing `permissions.defaultMode` into the
document while something else passes `--permission-mode` produces a value that never takes
effect and never complains.

**The one thing that genuinely cannot move.** `remoteControlAtStartup` is honored only from
managed policy, `--settings`, and user settings. If for some reason niwa keeps using the
setting rather than the flag, that key can never be relocated to a repo-scoped
`.claude/settings.json` -- project and local `true` values are discarded with a warning.
The flag is the escape hatch, not a project settings file.

## Surprises

**`--remote-control` is silently inert under `-p`.** It parses, it is accepted, nothing
happens, and there is no message. The binary carries explicit
`Error: --X requires --print`-style validation for roughly a dozen other flags but nothing
for this one. Anything generating invocation lines programmatically can emit
`--remote-control` on a print-mode command and get no feedback that it did nothing.

**`--remote-control=<name>` does not name the local background session.** After the probe,
`claude agents --json` showed the bg session named `reply with exactly: RCPROBE` -- derived
from the prompt -- while `rcprobe-276fb88b` went to the Remote Control session on
claude.ai. Easy to assume one name covers both. `-n/--name` still owns the local name.

**Project/local settings are second-class for a whole family of keys, in an asymmetric way.**
`remoteControlAtStartup: true` in a repo checkout is ignored *with a warning*, but
`false` in the same place is honored and force-disables. A repo can veto Remote Control but
cannot request it. Same shape appears on `processWrapper`, `modelPicker`, `enableArtifacts`
and others in the schema. Anyone reasoning about "which layer wins" from a single
precedence ordering will get these wrong.

**There is a hidden `claude remote-control` subcommand distinct from the flag**, and it
actively refuses `--settings` placed before the verb with a long explanatory error. Two
things named almost identically with opposite compatibility behaviour toward `--settings`
is a good way to draw the wrong conclusion from a quick test.

## Open Questions

- **The one-time Remote Control confirmation on a fresh account.** The binary contains
  "Remote Control asks for a one-time confirmation before it's first enabled, and this
  session can't show it. Run /remote-control from an interactive Claude Code session." That
  string sits in the `/remote-control` *slash command* component, and the startup path
  (`Mi`) does not route through it -- but this machine's account has already accepted the
  callout, so the probe could not distinguish "startup path never asks" from "already
  answered". On a machine that has never enabled Remote Control, `--bg --remote-control`
  might hit an unshowable dialog. Not measurable here without an unauthenticated fresh
  account.
- **Whether every settings key merges the way `env` and `hooks` do.** Measured for `env`
  (deep, key-wise) and `hooks` (array concat), and inferred for the rest from the schema
  docstrings extracted from the binary. `permissions.allow`/`deny` array merging across
  sources was not directly measured.
- **The exact precedence of `--settings` against *local* settings** (`settings.local.json`)
  was not measured -- only against user and project. The source map
  (`policy` > `flag` > `user`) plus the observed win over project makes `--settings` the
  highest non-managed source, but local was not exercised.
- **Whether `niwa dispatch`'s actual argv shape tolerates `--remote-control`** was not
  tested; the probe used a bare `claude --bg` invocation. Anything niwa does to the argv
  (quoting, prompt placement, `--` handling) would need its own check, though the binary's
  bg argv-rewriting code explicitly knows `--remote-control [name]` takes an optional value
  and peels it correctly.

## Summary

`--remote-control` composes with `--bg` on 2.1.267: a backgrounded session with no terminal
attached connected for real (`/remote-control is active … https://claude.ai/code/session_…`),
it is the same internal switch `remoteControlAtStartup` throws (`C || P.value`), and its
optional name is expressiveness the boolean setting does not have -- so niwa can vacate the
`--settings` slot, and the slot's occupancy is an artifact rather than a durable constraint.
A `--settings` document merges rather than replaces: it is a distinct settings source
(`flagSettings`) whose hooks append to the project's and whose `env` object merges key-wise
with user and project, winning only the keys it actually collides on -- so a merged-document
builder is safe, though merge is per-key and several keys have their own composition rules.
Two silent overrides to design around: `--permission-mode` beats a `permissions.defaultMode`
in the document with no warning, and `--remote-control` under `-p` is accepted and does
nothing at all.
