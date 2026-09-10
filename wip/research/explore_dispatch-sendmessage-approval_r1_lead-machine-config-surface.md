# Lead: niwa's machine-level configuration surface and precedence rules

Investigated in the worktree `public/niwa/.claude/worktrees/dispatch-sendmessage-approval`.
Everything below marked VERIFIED was read in the source at the cited line; INFERRED
marks a conclusion drawn from what was read rather than stated by it.

## Findings

### 1. The three configuration levels, crisply

VERIFIED. There are exactly three, and only one of them is machine-level.

**Level 1 — machine (host) config.** `~/.config/niwa/config.toml`, or
`$XDG_CONFIG_HOME/niwa/config.toml` when that variable is set. Format: TOML.
Path computed in `internal/config/registry.go:242-252` (`GlobalConfigPath`).
Read by `LoadGlobalConfig` / `LoadGlobalConfigFrom` / `ParseGlobalConfig`
(`registry.go:256-284`); a missing file is *not* an error, it returns an empty
`&GlobalConfig{}` (`registry.go:268-271`). Written by `SaveGlobalConfigTo`
(`registry.go:314-318`) atomically through `writeGlobalConfigFile`
(`registry.go:342-381`): temp file in the same directory, chmod 0600, fsync,
rename. This file is the developer's own and is never part of a workspace
snapshot.

Its schema is `config.GlobalConfig` (`registry.go:15-19`), three tables:

| Table | Go type | Contents |
|---|---|---|
| `[global]` | `GlobalSettings` | the machine-level settings (below) |
| `[global_config]` | `GlobalConfigSource` | `repo` — the registered personal overlay repo |
| `[registry.<name>]` | `map[string]RegistryEntry` | every workspace registered on this machine |

**Level 2 — workspace config.** `.niwa/workspace.toml` inside a workspace,
parsed into `config.WorkspaceConfig` (`internal/config/config.go:268-280`).
`[workspace]` is `WorkspaceMeta`, which is where `default_agent` lives
(`config.go:329-338`). Crucially this file is usually a *snapshot*
materialized from a source repo and replaced wholesale on the next refresh —
the `niwa config set default-dispatch-harness` help text says exactly that
(`internal/cli/config_default_harness.go:29-33`), and there is a test named
`TestConfigSetDefaultHarness_WritesNothingInsideAWorkspaceSnapshot`
(`config_default_harness_test.go:69`). So an edit at this level is a *team*
statement committed upstream, not a personal one.

**Level 3 — instance settings.** `<instance>/.claude/settings.json`, the JSON
document niwa materializes into each instance directory. Read back by
`readInstanceSettings` (`internal/cli/dispatch_plugins.go:222-236`) through the
narrow `instanceSettings` projection (`dispatch_plugins.go:186-205`). It is
*derived*, not authored: its contents come from `[claude.settings]` in the
workspace config (and `[instance.claude.settings]`, `config.go:306-310`) via
`buildSettingsDoc` in `internal/workspace/materialize.go`.

There is a fourth thing confusingly also called "global" that is **not** a
level in this sense: the personal *overlay repo* registered by
`niwa config set global <slug>` (`internal/cli/config_set.go:23-92`). It is a
cloned repo holding a `niwa.toml` with its own `[global]` / `[workspaces.<name>]`
tables (`internal/config/overlay.go`), merged into *workspace* config at apply
time. Documented as distinct at `docs/guides/workspace-config-sources.md:645-653`.
Do not confuse `[global]` in `~/.config/niwa/config.toml` with `[global]` in an
overlay `niwa.toml`; they are different schemas.

### 2. Every key that exists in `[global]` today

VERIFIED, `internal/config/registry.go:28-107`:

| Key | Go type | Zero/unset meaning | Has a `niwa config set`? |
|---|---|---|---|
| `clone_protocol` | `string` | falls back to `"ssh"` (`registry.go:155-160`) | no |
| `auto_install_plugins` | `*bool` | nil = install (`registry.go:126-131`) | no |
| `clone_workers` | `int` | 0 = built-in default | no |
| `remote_control_on_dispatch` | `*bool` | nil = off | no |
| `keep_alive_on_dispatch` | `*bool` | nil = off | no |
| `dispatch_model` | `string` | "" = forward no model | no |
| `watch_sandbox` | `string` (enum "required"/"off") | "" = "required" | no |
| `watch_max_staged` | `int` | 0 = default, negative = hard error | no |
| `default_dispatch_harness` | `string` (agent enum) | "" = no preference | **yes** |

Two shapes recur and they are deliberate:

- **`*bool` for a tri-state.** `remote_control_on_dispatch` and
  `keep_alive_on_dispatch` are pointers precisely so "unset" is distinguishable
  from "set to false" (`registry.go:37-51`). That is the mechanism that answers
  the sub-question about unset-vs-false: a pointer, nil-checked at the use site.
- **Plain scalar with a `<= 0 -> default` / `"" -> default` convention** for
  ints and strings where false-vs-unset does not arise.

### 3. The `default_dispatch_harness` precedence chain, traced

VERIFIED end to end. The chain is four rungs plus a built-in floor:

```
--harness  >  NIWA_DISPATCH_HARNESS  >  [workspace].default_agent  >  [global].default_dispatch_harness  >  claude
```

- The merge happens in exactly one function,
  `agent.ResolveAgent(flag, env, workspaceDefault, hostDefault)`,
  `internal/agent/agent.go:122-135`. It is a plain `switch` on
  `!= ""`, first non-empty wins, `default: return AgentClaude`.
- Its only caller is `resolveSessionAgent`, `internal/cli/agent.go:120-132`,
  which gathers the four raw strings: flag value passed in,
  `os.Getenv("NIWA_DISPATCH_HARNESS")` (line 125), `cfg.Workspace.DefaultAgent`
  (line 123), and `gc.DefaultDispatchHarness()` (line 126).
- **Zero-value/unset is `""` for every rung.** There is no tri-state here
  because there is no "explicitly off" — an enum has no negative.
- **Validation is a single boundary.** Every rung's raw string goes through
  `agent.ParseAgent` (`agent.go:60-69`), which accepts `""`, `"claude"`,
  `"codex"` and errors on anything else naming the accepted set. The value is
  stored raw in the config struct on purpose; the comment at
  `registry.go:101-105` says a value decoded from a file must never *look*
  validated by its type.
- **Error attribution** is `agentSourceLabel` (`cli/agent.go:142-153`): it
  re-walks the same precedence order to name which rung held the bad value, and
  for the host rung prints the resolved file path via `globalConfigDisplayPath`
  (`agent.go:220-226`), which honors `XDG_CONFIG_HOME`.
- A nil `*config.GlobalConfig` reads as unset — `DefaultDispatchHarness()` has
  an explicit nil-receiver guard (`registry.go:114-119`), so an unreadable host
  config degrades rather than failing the command.

**Polarity note that matters.** The host rung sits *below* the workspace rung
(`agent.go:108-121` explains why: a workspace stating an agent is stating it for
everyone in it). The `registry.go:80-90` comment makes the general rule
explicit: in this file, a downstream value outranks the personal one, and a key
with the opposite polarity "would make the file's precedence something a reader
looks up per key rather than learns once."

### 4. The chain that actually matches what the user asked for

**This is the most important finding of this lead.** The brief names the harness
chain as the precedent to mirror. It is the wrong precedent for a boolean that
is off by default and overridable by a new dispatch flag. The right precedent is
**keep-alive**, which is the exact shape requested and shipped:

VERIFIED, `internal/cli/dispatch_keepalive.go:76-105`:

```
--keep-alive  >  [claude.settings] keepAliveOnDispatch (downstream, instance)  >  [global].keep_alive_on_dispatch  >  off
```

```go
func resolveDispatchKeepAlive(flag *bool, global config.GlobalSettings, inst *instanceSettings) bool {
	if flag != nil {
		return *flag
	}
	if inst != nil && inst.KeepAliveOnDispatch != nil {
		return *inst.KeepAliveOnDispatch
	}
	if global.KeepAliveOnDispatch != nil {
		return *global.KeepAliveOnDispatch
	}
	return false
}
```

Differences from the harness chain that all favor keep-alive as the model:

- It is a **boolean, off by default**, which is the requested shape.
- It has a **per-dispatch flag that overrides in both directions**. The
  mechanism is `triBoolValue` (`dispatch_keepalive.go:54-74`), a pflag `Value`
  over a `**bool`, registered with `NoOptDefVal = "true"`
  (`internal/cli/dispatch.go:38-39`) so bare `--keep-alive` is explicit true and
  `--keep-alive=false` is explicit off. The design doc calls this out as the one
  genuinely net-new piece rather than a copy
  (`docs/designs/current/DESIGN-niwa-session-keep-alive.md:151-160`).
  **That mechanism now exists and a second boolean flag reuses it for free.**
- It has **no env rung.** Neither keep-alive nor remote-control reads a
  `NIWA_*` variable. `NIWA_DISPATCH_HARNESS` is the only dispatch-resolution env
  var in the codebase (verified by enumerating `NIWA_[A-Z_]*` across
  `internal/` and `cmd/`; the other high-count hits — `NIWA_RESPONSE_FILE`,
  `NIWA_INSTANCE_ROOT`, `NIWA_SHELL_INIT`, `NIWA_WORKTREE_*` — are runtime
  plumbing, not user-facing precedence rungs, and `NIWA_AGENT` is the retired
  spelling kept only to print a rename notice, `cli/agent.go:172-181`).
- It has a **downstream (instance settings) rung between flag and host
  default**, which the harness chain does not.

`remote_control_on_dispatch` is the same family minus the flag
(`internal/cli/dispatch_remotecontrol.go:37-50`), and its host value is a
*default-fill*: it never overrides a downstream decision, which is what keeps a
downstream "off" winning even though `claude --settings` outranks
`settings.json`.

### 5. How `niwa config set` / `unset` work — the key space is CLOSED

VERIFIED. There is **no generic `niwa config set <key> <value>`**. `configCmd`
(`internal/cli/config.go:9-17`) has two children, `set` and `unset`
(`config_set.go:18-21`, `config_unset.go:16-19`), and each of those has exactly
two hand-written cobra subcommands:

- `niwa config set global <repo>` / `niwa config unset global` — the overlay
  repo (`config_set.go:15`, `config_unset.go:13`)
- `niwa config set default-dispatch-harness <agent>` /
  `niwa config unset default-dispatch-harness` (`config_default_harness.go:13-14`)

That is the complete list (`grep configSetCmd.AddCommand` returns two hits).
There is no `niwa config get` and no `niwa config list`. **So a new key is not a
registry entry — it is a new cobra subcommand file.** This is stated as a known
gap in the shipped design docs:
`docs/designs/current/DESIGN-instance-dispatch.md:146` — "no `niwa config set`
path exists for `[global]` scalar keys, so `dispatch_model` is [hand-edited]".
`docs/guides/session-keep-alive.md:26-27` and
`docs/guides/remote-control-on-dispatch.md:11-18` both tell the user to
hand-edit `~/.config/niwa/config.toml`.

The `default-dispatch-harness` subcommand is worth copying as a template, and it
does five things a new one should also do (`config_default_harness.go:52-90`):

1. Reject an empty argument explicitly, because `cobra.ExactArgs(1)` counts `""`
   as an argument and a scripted `set "$VAR"` with `VAR` unset would otherwise
   silently write a default (lines 52-63).
2. Validate *before* touching the file, at the same boundary every other source
   uses (lines 65-71).
3. Load, mutate one field, save — never rewrite the whole file from scratch.
   `TestConfigSetDefaultHarness_PreservesOtherSettings`
   (`config_default_harness_test.go:229`) pins this.
4. Print the resolved path it wrote to (line 87).
5. Print a one-line reminder of what still outranks it (line 88).

Plus an alias so the TOML spelling works as a subcommand name
(`config_default_harness.go:17-20`: `default_dispatch_harness`).

**One hazard to design around.** `SaveGlobalConfigTo` round-trips the parsed
struct through the TOML encoder, so every write **drops the file's comments and
any key this build does not know about**. This is documented as a deliberate
known loss at `registry.go:299-313`, and it explicitly names the hand-edited
keys (`dispatch_model`, `remote_control_on_dispatch`, `keep_alive_on_dispatch`,
`watch_sandbox`, `watch_max_staged`) as the casualties. A developer who
hand-edits with an explanatory comment loses it to any unrelated
`niwa config set`.

### 6. What one more setting costs

INFERRED from the keep-alive and default-harness change sets, which are the two
most recent precedents. Files that must change, for a boolean with a flag:

**Schema and accessor** (1 file + 1 test)
- `internal/config/registry.go` — one `*bool` field on `GlobalSettings` with a
  `toml:"...,omitempty"` tag and a doc comment stating the polarity.
- `internal/config/registry_<name>_test.go` — the parse and round-trip pair,
  mirroring `registry_keepalive_test.go:10,41`. The round-trip test asserts an
  unset value is *omitted* from the encoded output (see
  `registry_default_harness_test.go:73-74`).

**Resolution** (1 file + 1 test)
- a new `internal/cli/dispatch_<name>.go` holding the resolver, mirroring
  `resolveDispatchKeepAlive`. `triBoolValue` already exists and is reusable.
- `internal/cli/dispatch_<name>_test.go` — the resolver matrix.

**Dispatch wiring** (1 file + 1 test)
- `internal/cli/dispatch.go` — the flag var in the `var` block near line 52, the
  registration near lines 37-39 (`Flags().Var(triBoolValue{...})` plus the
  `NoOptDefVal = "true"` line), and the resolution call in the step-(9) region
  around lines 580-660. `gc`/`gcErr` and `inst` are already loaded there
  (lines 308, 550) and reused, so no new I/O.
- `internal/cli/dispatch_wiring_<name>_test.go` — the full matrix
  (`dispatch_wiring_keepalive_test.go` has 11 cases covering flag-on, flag-off
  over host-on, downstream-off over host-on, host-default-on, unset-unchanged).

**Downstream rung, only if you want one** (2 files + tests)
- `internal/config/config.go` — a `<Name>Key` const beside
  `RemoteControlAtStartupKey` (line 449) and `KeepAliveOnDispatchKey` (line 459).
- `internal/workspace/materialize.go` — a `cfg.Settings[key]` passthrough block
  in `buildSettingsDoc` (the two existing ones are at roughly lines 709-740).
- `internal/cli/dispatch_plugins.go` — a `*bool` field on `instanceSettings`
  (lines 186-205). Note the struct tag cannot reference the const, so a test
  pins the two together (`dispatch_keepalive_test.go:63`).

**Config subcommand, only if you want one** (1 file + 1 test)
- a new `internal/cli/config_<name>.go` on the `config_default_harness.go`
  template, plus its test file. This is optional — five of the nine `[global]`
  keys ship without one.

**Capability declaration, if the behavior is agent-specific** (1 file)
- `internal/agentplan/capability.go` (the `Capability` enum, lines 118-124, and
  the `catalog` rows at 192-193) and `internal/agentplan/declaration.go`
  (a `StateImplemented` row for Claude and a `StateUnavailable` +
  `ReasonNoSuchConcept` row for Codex, lines 316-332). Dispatch then gates on
  `agentplan.Lookup(...)` and prints the declaration's `Reason` when a developer
  asks for it under an agent that cannot deliver it
  (`dispatch.go:638-639, 646-651`). **A SendMessage pre-approval is Claude-only,
  so this row is required** unless the setting is scoped so narrowly that
  `--settings` deliverability alone gates it.

**Docs** (3 files)
- a new `docs/guides/<name>.md` on the `docs/guides/session-keep-alive.md`
  template (100 lines: what it is, "Opting in" listing the rungs in precedence
  order, how it works, what it does not do).
- a row in `CLAUDE.md`'s Contributor Guides list (lines 36-49).
- a section in `docs/guides/workspace-config-sources.md`, which is where the
  machine-level keys are collected for users (the remote-control section is at
  lines 704-729; the CLI-surface table at lines 303-311 gets a row only if a
  `niwa config set` subcommand ships).

**Functional test** (2 files)
- `test/functional/features/<name>.feature` plus a `<name>_steps_test.go`, on
  the `keep-alive.feature` (68 lines, 2 scenarios) model. `CLAUDE.md:28-32`
  requires a `@critical` Gherkin scenario for a user-facing CLI change.

**Shell completion: no change.** `internal/cli/completion.go` holds hand-written
closures for workspace/instance *names* only; cobra generates the rest from the
command tree, so a new subcommand and flag are completed automatically.

Rough total: **6 files for the minimum viable version** (schema, resolver,
dispatch wiring, one guide, one feature file, plus tests), **12-14** with a
downstream rung, a `niwa config set` subcommand, and a capability declaration.

### 7. Boolean, enum, or list?

- **A boolean fits the existing `[global]` table best** and has two working
  precedents (`remote_control_on_dispatch`, `keep_alive_on_dispatch`) plus a
  ready-made tri-state flag adapter.
- **An enum also fits.** `watch_sandbox` (`registry.go:59-66`) is a string with
  a closed set and a `"" -> "required"` default;
  `default_dispatch_harness` is a string validated through a single parse
  boundary. So a three-value setting (`off` / `send-message` / `all`) is
  precedented and costs nothing extra except a parse function.
- **A list is NOT precedented in `[global]`.** VERIFIED: every field of
  `GlobalSettings` is a scalar or a `*bool`; the only non-scalar in the whole
  `GlobalConfig` is `Registry map[string]RegistryEntry`. TOML handles
  `key = ["A", "B"]` fine and `BurntSushi/toml` decodes `[]string` without
  ceremony, so a list is *possible*; it would just be the first one at this
  level, and it needs its own `niwa config set` shape (append? replace?
  comma-split?) which the existing single-value subcommand template does not
  answer.
- **The downstream rung cannot carry a list as-is.** `SettingsConfig` is
  `map[string]MaybeSecret` (`config.go:440`), i.e. string values only — which
  is why `keepAliveOnDispatch` is written as the quoted string `"true"` and
  parsed with `strconv.ParseBool` in `materialize.go`. A list would either need
  a comma-split string in `[claude.settings]`, or a new typed block in
  `WorkspaceConfig`.

INFERRED recommendation for the sibling agent's open question: if the Claude
Code mechanism turns out to need a tool *list*, the cheapest shape that supports
both is a **`*bool` host key that switches on a niwa-owned, fixed tool list**
rather than a user-editable list. The user asked for one machine-level on/off
switch; the list is then an implementation detail of what niwa injects, not a
config surface, and can grow without a schema change or a migration.

### 8. The `--settings` injection channel is single-slot — a real constraint

VERIFIED, and this is the finding most likely to change the sibling agent's
implementation. There is exactly **one** site that appends a `--settings`
argument to a dispatched worker:

```go
// internal/cli/dispatch.go:598
passthrough = append(passthrough, spec.Flags.Settings, remoteControlSettingsJSON)
```

`spec.Flags.Settings` is the literal `"--settings"`
(`internal/agentplan/dispatch.go:357`), and `remoteControlSettingsJSON` is
`{"remoteControlAtStartup":true}` built from the shared const
(`dispatch_remotecontrol.go:16`). The keep-alive design records why it did not
use this channel: "niwa never merges settings JSON. A second `--settings` for
keep-alive would be passed verbatim alongside RC's, and the CLI's
repeated-`--settings` behavior is undocumented (likely last-wins), so it could
clobber `remoteControlAtStartup`"
(`DESIGN-niwa-session-keep-alive.md:140-145`). Keep-alive dodged it entirely by
riding the prompt prefix instead.

**So a SendMessage pre-approval delivered via `--settings` must be merged into
that one JSON document, not appended as a second `--settings` pair** — which
means changing that line from a constant to a built document, and handling the
case where remote control is *not* injected but the new setting is (today the
`--settings` pair only appears when RC injects).

### 9. On the "bypassPermissions is already materialized" premise

VERIFIED with a correction. niwa does not unconditionally materialize
`permissions.defaultMode: bypassPermissions`. The settings doc gets a
`permissions` block only when the *workspace config* sets
`[claude.settings] permissions = "bypass"`, which `permissionsMapping`
(`internal/workspace/materialize.go:310-314`) translates to
`"bypassPermissions"`; `buildSettingsDoc` emits it at
`materialize.go:681-691, 705-707`. The read-back side is
`internal/workspace/permissions.go:21-37` and the `Permissions.DefaultMode`
field on `instanceSettings` (`dispatch_plugins.go:198-204`).

The only other thing niwa ever writes under `permissions` is a `deny` list, for
worktree delegation (`materialize.go:693-703`). **There is no writer for
`permissions.allow` anywhere in the tree.** If the SendMessage fix needs an
allow entry, that writer is net-new.

## Implications

**Where the setting belongs.** `[global]` in `~/.config/niwa/config.toml`, as a
`*bool` field on `config.GlobalSettings`. That is the only machine-level surface
niwa has, it already holds two dispatch-scoped booleans with exactly the
requested semantics, and its nil-means-unset convention is what "off by default,
and I can tell 'off' from 'never set'" needs.

**Its name.** Follow the dispatch-scoped boolean convention
`<capability>_on_dispatch`, which `remote_control_on_dispatch` and
`keep_alive_on_dispatch` both use, rather than the `default_dispatch_*` or
`dispatch_*` prefixes used for the string-valued keys. So:

```toml
# ~/.config/niwa/config.toml
[global]
session_messaging_on_dispatch = true
```

with the flag spelled `--session-messaging` (kebab-case of the key minus the
`_on_dispatch` suffix, which is what `--keep-alive` is to
`keep_alive_on_dispatch`). If the team prefers naming the tool rather than the
capability, `send_message_on_dispatch` / `--send-message` is the same shape.
Either way the `_on_dispatch` suffix is load-bearing: it is what tells a reader
the key does not touch interactive sessions, ephemeral SessionStart sessions, or
`niwa apply`.

**Its override chain.** Mirror keep-alive exactly:

```
--session-messaging  >  [claude.settings] sessionMessagingOnDispatch  >  [global].session_messaging_on_dispatch  >  off
```

with the flag registered as `triBoolValue` + `NoOptDefVal = "true"` so
`--session-messaging=false` forces off over a host-level on. The downstream rung
is optional for a first cut; if it ships, it needs a `SessionMessagingOnDispatchKey`
const, a `buildSettingsDoc` passthrough, and an `instanceSettings` field, and it
must be a *default-fill* (host never overrides a decided downstream value), the
polarity both existing keys use.

**The env rung.** Do not add one. `NIWA_DISPATCH_HARNESS` is the only env rung
in the dispatch resolution, and it exists because the harness is the thing a
developer flips per-shell across many dispatches. If one is wanted anyway, the
consistent spelling is `NIWA_DISPATCH_SESSION_MESSAGING` — `NIWA_` +
`DISPATCH` + the capability, matching `NIWA_DISPATCH_HARNESS`. Note that adding
it makes this a *five*-rung chain and creates a precedence question the two
existing booleans never had to answer (does env beat the downstream instance
value, the way it beats the workspace value for harness?). That is a real design
cost for a setting a developer sets once.

**A `niwa config set` subcommand is optional.** Five of the nine `[global]` keys
have none and are documented as hand-edits. If one ships, copy
`internal/cli/config_default_harness.go` wholesale, including the empty-argument
guard, the alias for the TOML spelling, and the "here is what still outranks
this" closing line. Setting it would read:

```
niwa config set session-messaging-on-dispatch true
niwa config unset session-messaging-on-dispatch
```

though note the existing template takes an *enum value*, not a boolean, so a
boolean subcommand has to decide whether `true`/`false` are the accepted words
(`strconv.ParseBool` accepts `1`, `t`, `T`, `TRUE`, …, which is probably too
loose for a help text that says "accepted values are").

## Surprises

1. **The brief points at the wrong precedent.** The harness chain is a string
   enum with an env rung, no flag-off direction, and the host rung *below* the
   workspace rung. Keep-alive is a boolean, off by default, with a two-direction
   dispatch flag and a downstream rung — precisely the requested shape, and it
   already ships the tri-state flag mechanism the new flag needs. Mirroring the
   harness chain would produce a fourth pattern; mirroring keep-alive produces a
   third instance of an existing one.

2. **The key space is closed, not open.** `niwa config set` is two hand-written
   cobra subcommands, not a key registry. Adding a setting to the *file* costs
   one struct field; adding it to the *CLI* costs a new command file. Those are
   separable decisions, and the project has shipped five keys with only the
   former.

3. **Writing the machine config destroys unknown keys and all comments.**
   `SaveGlobalConfigTo` round-trips a struct
   (`registry.go:299-313`). Any `niwa config set` silently drops the developer's
   hand-written `keep_alive_on_dispatch` comment. If the new setting ships a
   `config set` subcommand *and* the docs tell users to hand-edit neighbouring
   keys, those two instructions actively fight.

4. **`--settings` has exactly one injection slot and it is already occupied by
   remote control**, with a design note explaining that a second one may
   clobber the first. Any settings-document delivery for SendMessage approval
   must merge, not append.

5. **niwa never writes `permissions.allow`**, and it writes
   `permissions.defaultMode` only when a workspace config asks for it. The
   exploration context's premise that niwa "materializes
   `permissions.defaultMode: bypassPermissions` into instance settings" is true
   for this workspace but is a *workspace config* choice, not niwa behavior.

6. **A capability declaration row is probably mandatory.** `agentplan` gates
   dispatch-time behavior per agent and prints the declaration's `Reason` when a
   developer asks for something the launched agent cannot deliver
   (`dispatch.go:638-651`). A Claude-only pre-approval that silently no-ops
   under Codex is the exact failure that table exists to prevent — the
   keep-alive rows at `declaration.go:325-332` are the template.

## Open Questions

1. **Does the fix even reach through `--settings`?** If SendMessage approval is
   granted by a settings document key, finding #8 says the injection line must
   become a merge. If it is granted some other way (a CLI flag, an env var, a
   prompt-level instruction), the cost drops sharply — keep-alive's
   prompt-prefix channel exists precisely because the settings channel was
   unavailable. The sibling agent's answer decides this, and it is the single
   biggest fork in the implementation.

2. **Boolean or list, settled by the same sibling.** The config surface supports
   a scalar cheaply, an enum cheaply, and a list at the cost of being the first
   one at this level plus a `[claude.settings]` representation problem
   (string-only map). My recommendation is a boolean gate over a niwa-owned tool
   list, but that only holds if the tool set is stable.

3. **Does the downstream (instance-settings) rung ship in v1?** It is half the
   file count. Both existing dispatch booleans have one; whether a
   *machine-level, deliberately-not-default* setting benefits from a workspace
   override is a product call, not a code one.

4. **Env rung: yes or no?** Not adding one is consistent with both existing
   booleans. Adding one is consistent with the harness chain the brief names.
   Needs a human decision, and if yes, a stated answer to whether env outranks
   the downstream instance value.

5. **What does the setting do when the launched agent is Codex?** Warn and
   proceed (keep-alive's behavior, `dispatch.go:646-651`), or stay silent? Warn
   is the house style, but it requires the agentplan row.

## Summary

niwa's machine-level surface is the `[global]` table of
`~/.config/niwa/config.toml` (TOML, XDG-aware, atomically rewritten from a
struct so unknown keys and comments are lost), holding nine keys of which only
`default_dispatch_harness` has a `niwa config set` subcommand — the key space is
a pair of hand-written cobra commands, not an open registry. The requested
shape already exists twice: `keep_alive_on_dispatch` is a `*bool` resolved
`--keep-alive` (tri-state pflag) > instance `[claude.settings]` > `[global]` >
off, and it, not the four-rung harness chain the brief names, is the precedent
to copy — cost is roughly six files minimum, twelve with a downstream rung, a
config subcommand, and the per-agent capability declaration. The biggest open
question is delivery rather than configuration: there is exactly one
`--settings` injection slot on the dispatch argv, already holding remote
control's document, and niwa has no writer for `permissions.allow` at all, so
whether the SendMessage grant travels through that document decides whether this
is a small change or a merge-the-settings-document change.
