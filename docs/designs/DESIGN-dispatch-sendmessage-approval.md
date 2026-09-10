---
schema: design/v1
status: Planned
problem: |
  `niwa dispatch` has no way to launch a Claude worker that accepts messages from
  other Claude Code sessions without an approval prompt. The one setting that
  does it, Claude Code's `crossSessionInbound: "accept"`, can only reach a worker
  through the launch `--settings` document, and that single slot already carries
  remote control's document; a second `--settings` silently replaces the first.
  The behavior also needs an agent-neutral eligibility check, a durable record,
  and a one-time explanation, without niwa writing `config.toml` or the
  developer's personal settings.
decision: |
  One rendering helper replaces the fixed remote-control document and emits a
  single merged `--settings` value from whichever contributors apply: remote
  control, as today, and a new inbound-acceptance contributor resolved from a
  tri-state `--accept-session-messages` flag over a `[global]`
  `accept_session_messages_on_dispatch` key. A new `DispatchInboundAcceptance`
  capability row decides which agents can receive it. The outcome is recorded on
  the session mapping and shown by `niwa list`, and a one-time explanation is
  suppressed by a marker file beside `config.toml` that is created only once a
  terminal has shown it and the developer has the terminal back. `niwa watch`
  review sessions gain a hook denying the tools that reach other sessions, and
  Claude dispatches put `--` before the prompt so a prompt can't replace niwa's
  settings document.
rationale: |
  The merged document is the only channel Claude Code honors for this setting
  that niwa can reach, and merging is forced by the measured last-wins behavior
  of a repeated `--settings`. A map rendered once keeps remote control's output
  byte-identical while letting each contributor keep its own conditions. A
  capability row, not a flag-spelling check, keeps the dispatch path agent-neutral
  under the dispatch-path AST scan and gives Codex's gap list a reason. Denying
  the session-reaching tools in review sessions closes the path the feature
  opens out of review containment, and the prompt separator keeps niwa the only
  writer of the launch settings the audit line and the record describe. A marker file beside
  `config.toml` avoids rewriting that file and survives fresh dispatch
  instances, and recording on the mapping reuses the durable per-session record
  keep-alive already relies on.
upstream: docs/prds/PRD-dispatch-sendmessage-approval.md
user_visible_surface: true
decision_provenance: inline-resolved
---

# DESIGN: Unattended peer messages for dispatched sessions

## Status

Planned

## Context and Problem Statement

`niwa dispatch` launches a Claude worker as `claude --bg` with an argv assembled
in `runDispatch` (`internal/cli/dispatch.go`). Two pieces of that argv matter
here. For a workspace that declares a bypass posture, the step the code labels
9a-derive adds `--permission-mode bypassPermissions`. When the machine
preference `remote_control_on_dispatch` is on and the instance settings leave
`remoteControlAtStartup` unset, step 9c appends `--settings` followed by the
fixed document `{"remoteControlAtStartup":true}` (`dispatch.go:585-601`,
`internal/cli/dispatch_remotecontrol.go`).

Claude Code holds an inbound cross-session message whenever the sending and
receiving sessions are in different permission-mode classes, unless the
receiving session's `crossSessionInbound` setting is `accept`. The upstream PRD
asks niwa to launch workers with that setting when the developer opts in,
through a machine-level key and a per-dispatch flag (R1-R6), and to surround
the launch with an audit line, a `niwa list` record, and a one-time explanation
(R7-R16).

Three facts about Claude Code constrain how that can be built, all measured on
2.1.267 for this design's PRD:

- `crossSessionInbound: "accept"` only takes effect from managed settings, the
  `--settings` flag, or user settings. A project or local settings file can only
  make it stricter, so writing it into the instance's materialized
  `.claude/settings.json` is silently ignored. The launch flag is the only
  channel niwa may use, since R12 keeps it out of the developer's personal
  settings.
- A repeated `--settings` flag is last-wins and prints no warning. Adding a
  second `--settings` for this setting would drop remote control's document.
- Claude Code saves a background session's launch flags as `respawnFlags` in the
  session's job record and reapplies them when it restarts the session with
  `claude respawn` or reopens a stopped one with `claude attach`. A setting
  carried in the launch `--settings` document therefore survives both, and
  niwa's re-entry path needs no change.

The technical problem is to add a machine-level, per-dispatch-overridable launch
contributor that shares the single `--settings` slot with remote control without
disturbing remote control's own conditions; to decide which agents can receive
it without naming an agent in the dispatch path; to record the outcome where
`niwa list` can read it; and to remember, per configuration directory, that the
explanation was seen.

## Decision Drivers

- **D1. One legal channel.** The setting must arrive in the launch `--settings`
  document. The materializer can't grant it, and niwa may not touch personal or
  managed settings (R3, R12). R3's "no other source" also rests on the
  materializer: `buildSettingsDoc` (`internal/workspace/materialize.go:668-760`)
  emits only the settings keys it knows, so a `crossSessionInbound` entry under
  `[claude.settings]` is dropped, and an unknown top-level `workspace.toml` field
  only warns (`internal/config/config.go:624`).
- **D2. One `--settings` slot.** A second `--settings` element silently replaces
  the first, so remote control's document has to share the one element (R6).
- **D3. Remote control's conditions are untouched.** Remote control injects only
  when the machine preference is on, the instance left `remoteControlAtStartup`
  unset, and `ANTHROPIC_API_KEY` isn't forcing API-key auth. Its tests,
  including the wiring test that compares the argv element to
  `remoteControlSettingsJSON` byte for byte, must pass unchanged, and keep-alive,
  which arms only when remote control is on (`dispatch.go:653`), must keep
  reading remote control's own decision.
- **D4. No agent names in the dispatch path.** `internal/cli/dispatch_layout_test.go`
  scans the dispatch-path files and fails on any agent constant or whole-literal
  agent name. An agent-specific decision goes through an `agentplan` declaration
  or a flag spelling, and any new `dispatch_*.go` file joins the scanned list in
  the commit that creates it.
- **D5. Precedence without a downstream rung.** Flag, then machine setting, then
  off. The flag must override in both directions, which needs the tri-state
  `triBoolValue` keep-alive already uses (R1-R3).
- **D6. Nothing before a successful launch.** The audit line, the session
  record, and the explanation follow a launch whose session niwa recorded, never
  precede it.
- **D7. The marker never rewrites `config.toml`.** `SaveGlobalConfigTo`
  re-encodes the whole struct, dropping comments, and parallel dispatches would
  race on it. The marker must also tolerate concurrent first dispatches and never
  fail a dispatch (R11).
- **D8. "Shown at a terminal" means the developer could read it.** For Claude,
  a dispatch from a terminal without `--detach` hands the terminal to
  `claude attach` right after the launch (`dispatch.go:852-858`), so anything
  printed at the post-mapping point scrolls away under the attached session.
- **D9. `niwa list` is per instance.** `niwa list` emits one record per instance
  and annotates it from session mappings. The new field must be present on every
  record and not gated on liveness, unlike `keep_alive` (R9).
- **D10. Stdout is unchanged.** All new output goes to stderr (R16).
- **D11. `niwa watch` is unaffected.** Watch builds its own passthrough and calls
  `dispatchLaunch` directly (`internal/cli/watch.go:581`, `:844`) without
  writing a session mapping, so the new resolution must live in `runDispatch`
  only (R14).
- **D12. Review containment holds.** `niwa watch` review sessions read
  untrusted changes. Watch passes no `--permission-mode`, so on current Claude
  Code they most likely run in a prompting mode in both containment postures,
  and the class mismatch holds their messages to bypass workers. A worker that
  accepts removes that hold, so the feature must not give a contained reviewer
  a channel to an uncontained worker. Four Claude Code tools reach other
  sessions: `SendMessage`, `SendFile` (files onto another session's
  filesystem), `RemoteTrigger` (remote routines, whose deliveries count as
  cross-session inbound), and `ListAgents` (session discovery). Review
  containment's egress hook matches only `WebFetch|WebSearch|mcp__`
  (`internal/watch/containment.go:18`), so it covers none of them.
- **D13. One pull request.** All changes are in this repository, and nothing
  has to reach the default branch before another piece can work.
- **D14. niwa alone writes the launch settings.** The audit line and the
  `niwa list` record describe the `--settings` document niwa rendered. Claude
  Code reads a prompt element that begins with a dash as a flag. Measured on
  2.1.267, `claude -p "--version"` prints the version, while
  `claude -p -- "--version"` treats the same text as the prompt. So a prompt
  beginning with `--settings=` would replace niwa's document by the last-wins
  rule, and both surfaces would misreport. Only the Codex launch spec sets
  `PromptSeparator` today (`internal/agentplan/dispatch.go:408`).

## Considered Options

### Decision 1: Carrying the setting in the launch settings document

The worker has to receive `crossSessionInbound: "accept"` through `--settings`
(D1), and remote control already occupies that slot with a fixed document built
in `dispatch_remotecontrol.go` (D2). Remote control's decision to inject has its
own conditions (D3), and the new behavior has different ones. The question is
how two contributors with independent conditions share one document.

Key assumptions:
- Claude Code deep-merges a `--settings` document onto the project settings, so
  adding a key doesn't disturb the materialized instance settings. This was
  measured for the PRD.
- Claude Code keeps honoring an inline JSON `--settings` value and saving it in
  `respawnFlags`.

#### Chosen: One map, rendered once, with independent contributors

Step 9c builds a `map[string]any`. Each contributor decides for itself whether
it adds a key: remote control adds `config.RemoteControlAtStartupKey: true`
exactly when `resolveDispatchRemoteControl` returns inject, and inbound
acceptance adds `config.CrossSessionInboundKey: "accept"` exactly when the new
behavior applies. `config.CrossSessionInboundKey` is a new constant beside the
remote-control one in `internal/config/config.go`, so the spelling lives in one
place. A helper in a new file, `renderLaunchSettings(map[string]any) (string,
bool)` in `internal/cli/dispatch_settings.go`, marshals the map with
`encoding/json`, which sorts keys, and reports whether any key was present.
Step 9c appends `spec.Flags.Settings` and the document as two discrete argv
elements only when it was.

With remote control alone the output is `{"remoteControlAtStartup":true}`,
byte-identical to today's document; this was checked by running it. With both
it is `{"crossSessionInbound":"accept","remoteControlAtStartup":true}`.
`remoteControlSettingsJSON` stays as the pinned remote-control-alone rendering,
and a test asserts the helper reproduces it. `rcInjected`, which keep-alive
reads, stays set from remote control's own inject decision and never from
whether the helper rendered a document, so an inbound-only document can't arm
keep-alive on a worker without remote control. Every key and value is a
constant, and a comment on the helper says contributors must pass constants.

#### Alternatives Considered

**A second `--settings` element**: append another `--settings` carrying only the
new key. Rejected because a repeated `--settings` is last-wins and silent, so it
would drop remote control's document whenever both apply (D2).

**Widen the remote-control helper**: turn `remoteControlSettingsJSON` into a
function that takes extra keys. It has the fewest moving parts, but it ties an
unrelated feature to the remote-control file, and remote control's default-fill
condition would start deciding whether the inbound key is emitted, even though
the inbound behavior has its own precedence and eligibility (D3). The next
contributor would need the same rework.

**A builder type**: a `launchSettings` type with `set` and `render` methods. It
produces the same bytes as the chosen map but adds a type whose only state is the
map, and nothing about the two contributors needs more than a map.

**A per-instance settings file passed by path**: write the merged document to a
file in the instance and pass `--settings <path>`. It's easier to inspect after
the fact, but it still uses the one slot, adds a file niwa must place, exclude
from drift checks, and clean up. It also puts a path into `respawnFlags` that
stops resolving once the instance is reaped, so a later `claude respawn` would
find nothing.

**Write the key into the instance settings**: materialize
`crossSessionInbound: "accept"` into `.claude/settings.json` or
`settings.local.json`. Rejected because a project or local value can only make
this setting stricter, so `accept` there is silently ignored (D1).

### Decision 2: Deciding which agents can receive it

niwa dispatches Claude and Codex. Codex has no `--settings` flag and no
cross-session inbound setting, so the behavior can't reach a Codex worker. The
dispatch path may not name either agent (D4). The PRD wants a warning only when
the flag explicitly asked (R13).

Key assumptions:
- Every agent niwa adds later declares its capability rows, as the existing
  table requires.
- Capability numeric values aren't persisted anywhere, so appending a row
  changes nothing stored.

#### Chosen: A `DispatchInboundAcceptance` capability row

`internal/agentplan/capability.go` gains a constant `DispatchInboundAcceptance`,
appended as row 25 at the end of the `iota` block and of the catalog as
`{DispatchInboundAcceptance, "dispatch-inbound-acceptance", RouteLaunch}`.
Appending avoids renumbering `DirectoryTrust` and `GitExcludeBookkeeping`, whose
row numbers are cited in comments and in `docs/prds/PRD-agent-capability-contract.md`.
`internal/agentplan/declaration.go` declares it `StateImplemented` for Claude,
requiring `DispatchLaunch`, and `StateUnavailable` for Codex with
`ReasonNoSuchConcept` and the reason "Codex has no setting for accepting
messages from other sessions." `gaplist.go` gains the subject "Accepting
messages from other sessions without an approval prompt", so Codex's generated
gap list names the gap. The row lands in the same commit as the delivery, as
`declaration.go:73-75` requires ("never before").

The dispatch path treats the behavior as deliverable when
`agentplan.Lookup(agentplan.DispatchInboundAcceptance, dispatchedAgent)` reports
`StateImplemented` and `spec.Flags.Settings` is non-empty, the shape remote
control's gate uses. When the flag asked and the behavior isn't deliverable,
step 9c prints
`niwa dispatch: --accept-session-messages does not apply to the %q agent and was ignored. %s`
with the declaration's reason; when only the machine setting asked, it prints
nothing.

Adding a capability moves several fixed counts, and all of them change in that
commit:
- `TestAllIsTheClosedSet` (24 to 25).
- `TestCodexColumnTotals` (15 implemented and 9 unavailable, to 15 and 10), with
  `DispatchInboundAcceptance: ReasonNoSuchConcept` added to `codexFinalGaps`.
- `TestEveryCapabilityHasAGuideSubject`, through the new subject.
- The prose counts in `capability.go` and `declaration.go`.
- The generated gap list in `docs/guides/codex-agent.md`, regenerated with the
  drift test's `-update` flag.
- An amendment under the capability-contract PRD's matrix, as earlier row
  changes got.

#### Alternatives Considered

**Gate on the settings flag spelling alone**: treat the behavior as deliverable
whenever `spec.Flags.Settings` is non-empty. It works for today's two agents and
touches no capability tests, but it makes "Codex has no `--settings`" the reason,
which is incidental. A future agent with a settings flag but no inbound setting
would receive a meaningless key silently, and the gap list would say nothing.

**Reuse the `RemoteControl` row**: treat the behavior as deliverable wherever
remote control is. It needs no new row or count changes, but it couples two
independent capabilities; an agent could support one and not the other.

### Decision 3: Remembering that the explanation was shown

The explanation prints on the first dispatch where the behavior takes effect and
is suppressed afterwards, per configuration directory, only once a terminal has
shown it (R10, R11). niwa has no once-per-machine notice mechanism today.

Key assumptions:
- `os.OpenFile` with `O_CREATE|O_EXCL` is atomic on the local filesystems niwa
  supports.
- `IsStderrTTY` in `internal/cli/prompt.go` is the right terminal check; it's
  already a stubbable variable used for the interactive prompt capture.

#### Chosen: A marker file beside `config.toml`, created once the developer has the terminal back

The marker is `accept-session-messages-notice` in the directory that holds
`config.toml`: the directory part of the path `config.GlobalConfigPath()`
returns, so `$XDG_CONFIG_HOME/niwa/` or `~/.config/niwa/`.
`config.GlobalConfigDir()` isn't used, because it returns the overlay clone
directory `.../niwa/global`. If `GlobalConfigPath()` returns an error, niwa
prints the explanation with its non-terminal closing sentence and creates no
marker.

The marker counts as present when `os.Lstat` on its path succeeds; any error,
including a permission error on an unsearchable directory, counts as absent.
`Lstat` makes a dangling symlink count as present, which matches what the
exclusive create will find. When the marker is absent, niwa prints the
explanation. Then, only if `IsStderrTTY()` reports a terminal, it runs
`os.MkdirAll(dir, 0o755)`, the mode the config writer already uses for the same
directory (`registry.go:344`), and opens the marker with
`O_CREATE|O_EXCL|O_WRONLY` and mode `0o600`. An "already exists" error means a
concurrent dispatch won the race and is ignored. Any other error is ignored too,
because the dispatch already succeeded, and the explanation simply prints again
next time. The file's contents are empty.

When the explanation prints depends on the launch (D8). If `claude attach`
follows, niwa prints the explanation and creates the marker only after the
attach returns, on both its success and failure branches. Before that the
developer can't read the terminal, and after it they can. If no attach follows
(`--detach`, an agent that doesn't hand a session over, or a foreground launch),
niwa prints the explanation right after the audit line. A developer who closes
the window instead of returning from the attach gets no marker, so the
explanation shows again, which errs in the safe direction.

The PRD already settles that the record is a marker file, not a key in
`config.toml` (D7) or a per-instance notice, and that it's created only for a
terminal (R11). What stays open is which directory holds it, how it's created
without a race, and when in the dispatch it's created.

#### Alternatives Considered

**`config.GlobalConfigDir()`**: the existing directory helper. Rejected because
it returns the overlay clone directory `.../niwa/global`, which a
`[global_config]` clone owns and can replace, rather than the directory holding
`config.toml`.

**A state or cache directory**: put the marker under `XDG_STATE_HOME` or the
user cache directory. A seen-flag is arguably state rather than configuration,
but niwa has no convention for either directory, and keeping the marker beside
`config.toml` lets `XDG_CONFIG_HOME` move both together, which the PRD's
acceptance criteria rely on.

**Check-then-create without exclusive create**: stat the path, then write it
with `os.WriteFile`. It's one call shorter, but concurrent first dispatches
would both write, and a planted symlink at the path would redirect the write to
another file.

**Print it with the audit line in every flow**: simpler, but in the default
terminal flow the attach takes the terminal a moment later, so the marker would
record an explanation nobody could read (D8).

### Decision 4: Recording the outcome for `niwa list`

`niwa list` emits one `workspace.InstanceRecord` (`internal/workspace/state.go:365`)
per instance and annotates it from session mappings in
`annotateFromSessionMappings` (`internal/cli/list.go:117`), which today sets
`KeepAlive` only for a live session. The new field must be on every record,
`true` when the instance's dispatched session had the behavior take effect, for
as long as the record exists (D9).

Key assumptions:
- Dispatch writes one mapping per instance; if more than one mapping points at an
  instance, any one recording the behavior makes the instance report `true`.

#### Chosen: A mapping field and an always-present instance field

`workspace.SessionMapping` gains `AcceptsSessionMessages bool
\`json:"accepts_session_messages,omitempty"\``. It's set in the mapping literal
at step 11 (`dispatch.go:761-772`), beside `KeepAlive` and before
`WriteSessionMapping` runs, from the same boolean that gates the audit line. The
omitempty tag keeps mappings written by other paths, and older mappings,
byte-identical; they read as `false`. `workspace.InstanceRecord` gains
`AcceptsSessionMessages bool \`json:"accepts_session_messages"\`` without
omitempty, so it's present on every record. `annotateFromSessionMappings` sets it
for any instance with a mapping that recorded it, without the liveness check
keep-alive applies. The human output appends ` (accepts session messages)` after
the name, after ` (keep-alive)` when both apply, and the command's `--json` help
and `Long` text describe the new field.

R9 already fixes the field's shape: present on every record and not gated on
liveness. The open question is where the value comes from.

#### Alternatives Considered

**Derive it at list time from Claude Code's job record**: read the session's
saved `respawnFlags` and look for `crossSessionInbound` in its `--settings`
document. It needs no new mapping field, but it reads another tool's internal
files, it's Claude-specific, and the job record disappears when the session is
deleted, while the instance and its list record can outlive it.

**A separate audit log**: append each grant to a log file. It adds a file to
rotate and clean up, while the mapping is already the durable per-session record
the reaper removes with the instance.

### Decision 5: Keeping review sessions from reaching accepting workers

A review session launched by `niwa watch` could reach a worker that accepts, and
its containment covers none of the tools that reach other sessions (D12). The
PRD excludes review sessions as receivers but doesn't consider them as senders.
Nothing in the repo has a review session message, send files to, list, or
schedule work for another session.

Key assumptions:
- A PreToolUse hook that exits 2 blocks the tool call under every permission
  mode, including `bypassPermissions`, as the existing containment hooks rely on.
- A matcher made only of letters, digits, underscores, and `|` is compared as an
  exact list of tool names, aliases included (so `ListPeers` resolves to
  `ListAgents`), not as a substring or a regex. This was read from the 2.1.267
  bundle.
- Hooks apply to tool calls from subagents a session starts. The manual check
  confirms it.

#### Chosen: A session-reach deny hook in every containment mode

`internal/watch/containment.go` gains
`sessionReachDenyMatcher = "SendMessage|SendFile|RemoteTrigger|ListAgents"` and a
`sessionReachDenyHook()` that exits 2 with
`niwa watch: review sessions don't reach other sessions`. `ApplyReviewSettings`
appends it in every mode unless an entry with the same matcher and the same
command is already present, and `VerifyReviewSettings` requires an entry with
that matcher and that command in every mode. Checking the command as well as the
matcher means a workspace or overlay hook that reuses the matcher with a no-op
command can't stand in for niwa's. A dropped hook stops the review launch.

Denying `RemoteTrigger` also closes a gap that predates this feature: a
sandboxed review could create and run remote routines, because that call uses
the Claude Code process's own connection, which neither the OS sandbox nor the
egress hook cages.

Two routes stay outside the hook:
- Local delivery runs over a unix-socket inbox. In sandbox mode, the no-egress
  stanza niwa owns leaves unix sockets disallowed, which is what closes this
  route for Bash, and the stanza must keep doing so.
- With `watch_sandbox = off`, Bash has full access, and the hook is accident
  prevention only, like the posting guard.

Whether a process that isn't Claude Code can send over the inbox is unverified,
and the manual delivery check tests it.

#### Alternatives Considered

**Deny `SendMessage` alone**: the tool the feature is about. Rejected because
`SendFile` delivers files through the same peer path and `RemoteTrigger`
deliveries are cross-session inbound too, so an accepting worker takes all
three.

**Extend the egress-deny matcher**: add the tools to
`WebFetch|WebSearch|mcp__`. It's a one-line change, but that hook is applied
only in sandbox mode, so a review session run without the sandbox would keep the
channel, and the dedupe identity of an existing hook would change.

**Deny them only in the operator-approval posture**: a rule scoped to one
posture is one more condition to keep in step with the posture list, and on
current Claude Code the hard-deny posture most likely prompts too.

**Also refuse inbound messages into review sessions**: write
`crossSessionInbound: "refuse"` into review settings, which a project file may
do because it tightens the setting. It would stop a steered session from
messaging a reviewer, but this feature doesn't open that direction, so it's left
to review-containment follow-up work.

**Leave it to the dispatch-containment follow-up**: document the gap and close
it later. Rejected because this feature is what removes the hold, so the fix
belongs with it.

### Decision 6: Keeping the prompt out of the settings slot

The prompt is the last argv element, after niwa's `--settings`. A Claude prompt
that begins with a dash is read as a flag (D14), so a prompt beginning with
`--settings=` would silently replace niwa's document. The worker could then
accept messages with no audit line and a `false` record, or lose remote control.

Key assumptions:
- `claude --bg` parses the separator the same way `claude -p` does. The manual
  check confirms a detached launch.

#### Chosen: Set `PromptSeparator` on the Claude launch spec

`internal/agentplan/dispatch.go` sets `PromptSeparator: true` on Claude's
launch spec, so `buildLaunchArgs` inserts a bare `--` before the prompt, as it
already does for Codex. The measurement in D14 shows Claude Code then reads the
next element as the prompt. The change applies to every Claude launch niwa
builds, detached and foreground, and it also stops a prompt from smuggling in
any other flag, such as a permission mode. Tests pin the separator in the argv
and show that a prompt beginning with `--settings=` leaves niwa's document in
force.

#### Alternatives Considered

**Refuse prompts that begin with a dash**: no dependency on Claude Code's
parser, but it rejects ordinary prompts, such as a brief that opens with a
Markdown list item (`- fix the build`), which the separator handles.

**Leave it and document it**: the direct caller already has the flag's
authority, but agents compose prompts, and the audit line and the `niwa list`
record are the feature's only durable controls. A gap that makes both
misreport can't stay open.

## Decision Outcome

**Chosen: 1 (one rendered map) + 2 (capability row) + 3 (marker beside
`config.toml`, after the terminal is back) + 4 (mapping and instance fields) +
5 (review-session deny of the session-reaching tools) + 6 (prompt separator for
Claude)**

### Summary

A new tri-state flag, `--accept-session-messages`, is registered on
`niwa dispatch` with `triBoolValue` and `NoOptDefVal = "true"`, so a bare flag
and `=true` mean on and `=false` means off. `config.GlobalSettings` gains
`AcceptSessionMessagesOnDispatch *bool
\`toml:"accept_session_messages_on_dispatch,omitempty"\``, with no `niwa config
set` setter, like its siblings. A new file, `internal/cli/dispatch_inbound.go`,
holds the flag variable and the resolver
`resolveDispatchInboundAcceptance(flag *bool, global config.GlobalSettings)
inboundResolution`, whose result carries `on`, `source`
(`--accept-session-messages` or `machine setting`), and `overrodeMachineOn`,
which is true when the flag is `false` and the machine setting is `true`. The flag
wins when given; otherwise the machine setting decides when non-nil; otherwise
the behavior is off.

The `hostGlobal` block that builds the zero-on-failure `GlobalSettings` for
keep-alive moves up from step 9d to above step 9c, and both resolvers read it.
An unreadable or malformed `config.toml` therefore counts as an absent machine
setting while the flag still applies. Remote control still injects nothing
without a readable config, because a zero `RemoteControlOnDispatch` means no
inject.

At step 9c, `runDispatch` resolves remote control exactly as today and resolves
the inbound behavior, then checks deliverability through the new capability row
and the settings flag. A non-deliverable resolution prints the warning only when
the flag asked. One boolean, `inboundApplied`, records that the behavior is on,
deliverable, and in the map. It feeds the mapping literal, the audit line, and
the explanation, so the three can't disagree. The map is rendered and appended
as one `--settings` document. Nothing else in the argv changes, and the resume
commands niwa prints stay `claude attach <id>`, because Claude Code carries the
setting across restarts itself.

Step 11 writes `AcceptsSessionMessages: inboundApplied` in the mapping literal.
Once the write succeeds and step 12 has disarmed the rollback, and before step
13 prints the stdout hints, niwa writes one stderr line. If `inboundApplied` is
true, that's the audit line. If the resolution's `overrodeMachineOn` is true and
the behavior was deliverable, it's the override line. The explanation follows
as Decision 3 places it: right after the audit line when no attach follows, or
after `dispatchAttach` returns at step 14. A launch that fails returns before
step 11, so no audit line, marker, or `true` record can precede a failed launch.

Independently of dispatch, every `niwa watch` review session gets the
session-reach deny hook, so turning the behavior on never opens a channel from a
contained reviewer to an uncontained worker. Every Claude launch niwa builds puts
`--` before the prompt, so the `--settings` document niwa rendered is the one
Claude Code reads.

### Rationale

The first four decisions meet at one seam, step 9c, and one record, the session
mapping. The fifth stands apart, in review-session settings, and exists because
the first four remove a hold review sessions used to be subject to. The sixth is
what makes the first and fourth hold: the audit line and the record describe a
document that, with the separator, only niwa can write. Rendering one map is what lets inbound acceptance join the one
`--settings` element without inheriting remote control's conditions. The
capability row is what keeps that seam agent-neutral, so the same line of code
serves Claude and gives Codex a reason in its gap list. Deriving the audit line,
the record, and the explanation from one boolean set before the mapping write
makes "takes effect" a single fact they all report consistently. The marker's
timing rule trades a repeated explanation, for developers who only dispatch
through agents or close the window, against losing the explanation unseen, which
isn't recoverable.

## Solution Architecture

### Overview

The feature adds one launch contributor, one capability row, one machine-config
key, one flag, two record fields, one marker file, a review-session hook, and a
guide. Resolution, delivery, and stderr output all live in `runDispatch`;
nothing moves into `dispatchLaunch`, which `niwa watch` also calls.

### Components

- `internal/config/registry.go`: `GlobalSettings.AcceptSessionMessagesOnDispatch`,
  a `*bool` with the TOML key `accept_session_messages_on_dispatch`, added to the
  setter-less key list in the `SaveGlobalConfigTo` comment. A new
  `registry_inbound_test.go` covers decoding and round-tripping, like the
  keep-alive and remote-control key tests.
- `internal/config/config.go`: `CrossSessionInboundKey = "crossSessionInbound"`.
- `internal/agentplan/capability.go`, `declaration.go`, `gaplist.go` and their
  tests: the `DispatchInboundAcceptance` constant and catalog row, the per-agent
  declarations, the gap-list subject, and the count updates from Decision 2.
- `docs/guides/codex-agent.md`: the regenerated gap list.
- `docs/prds/PRD-agent-capability-contract.md`: an amendment under the matrix
  adding row 25.
- `internal/cli/dispatch_settings.go` (new): `renderLaunchSettings`.
- `internal/cli/dispatch_inbound.go` (new):
  - the flag variable, the resolver, and `inboundResolution`;
  - one `inboundGuideURL` constant;
  - the four fixed message strings;
  - `showInboundExplanation(w io.Writer, dir string, dirErr error, isTTY func() bool)`.
    It prints the explanation when the marker is absent and creates the marker
    when `isTTY()` is true.
- `internal/cli/dispatch.go`:
  - flag registration in `init()`;
  - the `hostGlobal` hoist;
  - step 9c rewritten to build and render the map;
  - `AcceptsSessionMessages` in the step 11 literal;
  - the stderr line between steps 12 and 13;
  - the explanation call, either after that line or after `dispatchAttach` at step 14.
- `internal/cli/dispatch_layout_test.go`: each new file added to
  `dispatchPathFiles` in the commit that creates it.
- `internal/cli/dispatch_test.go`: the shared flag-reset helper saves and
  restores the new flag variable, as it does `dispatchKeepAlive`.
- `internal/cli/watch_test.go` (or the existing watch test file): a unit test
  that stubs `dispatchLaunch`, sets the machine key `true`, runs both watch
  launch sites, and asserts that no `crossSessionInbound` reaches the
  passthrough.
- `internal/workspace/session_map.go`: `SessionMapping.AcceptsSessionMessages`.
- `internal/workspace/state.go`: `InstanceRecord.AcceptsSessionMessages`.
- `internal/cli/list.go`: the annotation, the human marker, the `--json` help,
  and the `Long` text.
- `internal/watch/containment.go`: the `sessionReachDenyMatcher` constant and
  `sessionReachDenyHook()` from Decision 5. `ApplyReviewSettings` appends the hook
  and `VerifyReviewSettings` requires it, in every mode, both keyed on matcher
  and command. A comment on the no-egress stanza says it must keep unix sockets
  disallowed.
- `internal/agentplan/dispatch.go`: `PromptSeparator: true` on Claude's launch
  spec. The `buildLaunchArgs` tests in `internal/cli` pin the separator, and the
  existing tests that assert Claude's argv are updated.
- `test/functional/features/` and step definitions: the scenarios in Phase 6.
- `docs/guides/session-message-acceptance.md` (new) and the contributor-guide
  index in `CLAUDE.md`.

### Key Interfaces

- **Flag:** `--accept-session-messages[=true|false]`, help text "accept messages
  from other Claude Code sessions without an approval prompt; overrides the
  [global] accept_session_messages_on_dispatch machine setting in either
  direction".
- **Machine setting:** `[global] accept_session_messages_on_dispatch = true`.
- **Launch argv:** `--settings '{"crossSessionInbound":"accept"}'`, or merged with
  remote control as `--settings '{"crossSessionInbound":"accept","remoteControlAtStartup":true}'`.
- **Guide URL** (`inboundGuideURL`):
  `https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md`,
  following the form niwa already prints in `internal/workspace/scaffold.go`.
- **Audit line** (machine setting source):
  `niwa dispatch: this worker accepts messages from other sessions without asking (source: machine setting accept_session_messages_on_dispatch); see <guide URL>`.
  With the flag as the source, the parenthetical reads
  `(source: --accept-session-messages)`.
- **Override line:**
  `niwa dispatch: this worker keeps Claude Code's default for messages from other sessions (--accept-session-messages=false overrides the machine setting)`.
- **Warning:**
  `niwa dispatch: --accept-session-messages does not apply to the %q agent and was ignored. <declaration reason>`.
- **Explanation**, one stderr line, with the final sentence depending on the
  terminal check:
  `niwa dispatch: note: accepting messages without asking is inbound only. A message this worker sends into a session launched without it, such as a coordinator dispatched earlier, one dispatched with the behavior off, or one another tool started, still waits for approval there when the two run in different permission modes; dispatching that session again with the behavior on clears it. Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change. To accept there too, set "Messages from your other sessions" to accept in Claude Code's /config, or add "crossSessionInbound": "accept" to ~/.claude/settings.json. That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere. niwa won't show this again; it's also at <guide URL>`.
  When stderr isn't a terminal, or the configuration directory can't be
  resolved, the last sentence is instead
  `niwa will show this again until it's been shown at a terminal; it's also at <guide URL>`.
- **Explanation call:** production passes `cmd.ErrOrStderr()`, the directory
  part of `config.GlobalConfigPath()` with its error, and `IsStderrTTY`.
- **Marker:** `<config dir>/accept-session-messages-notice`, empty, mode `0o600`,
  directory mode `0o755` when niwa creates it.
- **Records:** `SessionMapping.accepts_session_messages` (omitempty) and
  `InstanceRecord.accepts_session_messages` (always present).
- **Review-session deny:** the matcher `SendMessage|SendFile|RemoteTrigger|ListAgents`,
  taken from Claude Code 2.1.267's tool list. The guide records the list and
  the version it came from.
- **Claude launch argv tail:** `... --settings <document> -- <prompt>`.

### Data Flow

1. `runDispatch` loads `config.toml` once and, above step 9c, builds
   `hostGlobal`: the parsed `[global]` table, or a zero value when the load
   failed.
2. The resolver combines the flag and `hostGlobal.AcceptSessionMessagesOnDispatch`
   into an `inboundResolution`.
3. Step 9c checks deliverability through `agentplan.Lookup` and
   `spec.Flags.Settings` and prints the warning when the flag asked for an
   undeliverable behavior. It then fills the map: remote control's key when its
   resolver says inject (which also sets `rcInjected`), and the inbound key when
   the behavior is on and deliverable (which sets `inboundApplied`).
4. `renderLaunchSettings` produces one document, appended as
   `--settings <document>`. Claude Code saves it in the job's `respawnFlags` and
   reapplies it on `claude respawn` and `claude attach`.
5. Step 11 writes the mapping with `AcceptsSessionMessages: inboundApplied`.
6. After step 12, niwa writes the audit line or the override line to stderr.
   When no attach follows, the explanation comes next.
7. Step 13 prints the stdout hints, unchanged.
8. When an attach follows, the explanation prints after `dispatchAttach`
   returns.
9. `niwa list` reads the mappings and sets `accepts_session_messages` on each
   instance record.

## Implementation Approach

All phases land in one pull request as separately reviewable commits, in the
order below: Phase 7 first or anywhere, since it stands alone; then 1 and 2;
then 3; then 4 and 5; then 6.

### Phase 1: Configuration key

Add `AcceptSessionMessagesOnDispatch` to `GlobalSettings` and
`CrossSessionInboundKey` to `config.go`. The key decodes and round-trips but
nothing reads it yet.

Deliverables:
- `internal/config/registry.go`, `internal/config/config.go`
- `internal/config/registry_inbound_test.go`

### Phase 2: Launch settings rendering and the prompt separator

Introduce `renderLaunchSettings` and route remote control's injection through
the map, producing byte-identical output. The existing remote-control tests and
a new test pinning the helper's remote-control-alone output to
`remoteControlSettingsJSON` are the regression guard. Set `PromptSeparator` on
Claude's launch spec. Update the tests that assert Claude's argv, and add a test
that a prompt beginning with `--settings=` leaves the rendered document in
force. The functional fake `claude` must accept the separator.

Deliverables:
- `internal/cli/dispatch_settings.go` and tests
- `internal/cli/dispatch.go` step 9c
- `internal/cli/dispatch_layout_test.go`
- `internal/agentplan/dispatch.go`, and the launcher and wiring tests in
  `internal/cli`

### Phase 3: Capability row, flag, resolver, delivery, and the audit, override, and warning lines

Add the capability row with its count updates, regenerated gap list, and
capability-contract amendment. Register the flag, hoist `hostGlobal`, add the
resolver and message strings, add the inbound key to the map, introduce
`inboundApplied`, and write the audit, override, and warning lines. Tests that
build both keys parse the document rather than comparing strings. Depends on
Phases 1 and 2.

Deliverables:
- `internal/agentplan/capability.go`, `declaration.go`, `gaplist.go` and their
  tests
- `docs/guides/codex-agent.md`, `docs/prds/PRD-agent-capability-contract.md`
- `internal/cli/dispatch_inbound.go` and tests
- `internal/cli/dispatch.go`, `internal/cli/dispatch_test.go`,
  `internal/cli/dispatch_layout_test.go`

### Phase 4: Durable record and `niwa list`

Add the two record fields, put `inboundApplied` into the step 11 literal, and
annotate and print in `niwa list`. Depends on Phase 3 for the boolean.

Deliverables:
- `internal/workspace/session_map.go`, `internal/workspace/state.go`, and tests
- `internal/cli/list.go` and tests

### Phase 5: One-time explanation and marker

Add `showInboundExplanation` and its two call sites: after the audit line when
no attach follows, and after `dispatchAttach` returns at step 14. Tests stub
`IsStderrTTY` and cover:
- the terminal and non-terminal paths;
- both call sites;
- concurrent first dispatches;
- an unwritable directory, a missing directory, a dangling symlink, and an
  unresolvable configuration path;
- `XDG_CONFIG_HOME`.

Depends on Phase 3.

Deliverables:
- `internal/cli/dispatch_inbound.go` and tests
- `internal/cli/dispatch.go` step 14

### Phase 6: Guide, help, functional scenarios, and the manual check

Write the guide with the same top-level headings as the keep-alive guide and add
the contributor-guide index entry. Check the flag's help and its completion with
`niwa __complete dispatch --acc`.

Add the `@critical` scenarios with new step definitions:
- the recorded launch argv, read from `$HOME/dispatch-launch-argv` as the
  keep-alive steps do, carries or lacks `crossSessionInbound`;
- the marker exists or doesn't under `$XDG_CONFIG_HOME/niwa/`;
- `config.toml` is byte-unchanged.

Terminal scenarios run dispatch with `--detach` under the existing `script`
pty helper, which merges stderr into stdout, so they assert on the combined
transcript.

Separate scenarios cover:
- the source fixtures R3 lists (`workspace.toml` keys, `[claude.settings]
  crossSessionInbound`, repository settings files), showing none of them turns
  the behavior on;
- the Codex warning.

The watch exclusion is covered by the Phase 3 unit test at both watch launch
sites rather than a functional scenario, because no functional harness runs
`niwa watch`; the PRD's R18 is amended to match.

A scenario also dispatches a prompt beginning with `--settings=` and shows the
recorded document is niwa's.

Run the PRD's manual delivery check and record the Claude Code version it
passed on as the guide's version line. The same session also checks the
following:
- A detached `claude --bg ... -- <prompt>` launch starts normally.
- The permission class a hard-deny review session actually runs in.
- Whether a review session's subagents are blocked by the deny hook.
- Whether a process that isn't Claude Code can send over the local inbox. The
  guide's description of the sender set follows that result.
- Claude Code's tool list, taken from the init event of
  `claude -p --output-format stream-json`, compared against the denied list.

Deliverables:
- `docs/guides/session-message-acceptance.md`, `CLAUDE.md`
- `test/functional/features/` scenarios and step definitions
- `internal/cli/watch_test.go` (or the existing watch test file), if not
  already in Phase 3

### Phase 7: Review-session messaging deny

Add the session-reach deny hook to review-session settings and to their
verification, keyed on matcher and command. Tests cover both postures, sandbox
on and off, and a pre-existing hook with the same matcher and a different
command. It depends on nothing else here; it's in this pull request because the
feature is what makes the channel matter.

Deliverables:
- `internal/watch/containment.go` and its tests

## Security Considerations

This feature gives a dispatched worker no new permission. What it removes is a
checkpoint. Claude Code normally holds a message from a session in a different
permission-mode class until a person approves it, and dispatched workers often
run with `bypassPermissions` and no containment. With the behavior on, text and
files from another session reach such a worker and are acted on without a
prompt. For a bypass-mode worker, `accept` lifts four holds, not one:
- the hold on a sender in a different permission-mode class;
- the hold on a sender that doesn't attest its permission mode;
- the default hold a bypass receiver applies;
- the hold on remote-routine deliveries.

Everything below follows from that.

**Who can send.** A worker that accepts takes messages from any session able to
address it by name. That set covers the developer's whole Claude Code account,
including sessions on other machines and in the cloud, and niwa sits nowhere in
the delivery path, so it can't narrow it. Worker names are predictable, because
niwa uses the dispatch name as the session's display name. The realistic threat
is prompt-injection laundering: a session that reads untrusted content, such as
a web page, an issue, or a third-party repository, is told to message a worker,
and the worker carries out the instruction with its full authority. That
authority includes shell access, write access outside its instance, the
credentials resolved into its environment, git push rights, and unrestricted
network access. Keep-alive can wake an idle worker to act on such a message.
The same exposure already exists between two bypass-mode sessions when the
sender attests its mode, which Claude Code delivers without a hold whether or
not this feature is on, so switching the behavior off doesn't isolate a worker
from them. Local delivery runs over a unix-socket inbox. If a process that
isn't Claude Code can send over it, the sender set is wider than sessions on
the account. That's unverified, and the manual delivery check tests it before
the guide describes the sender set.

**Review sessions.** Sessions that `niwa watch` launches review untrusted changes
and never receive this behavior, but they could still send. Their network
sandbox doesn't cover the tools that reach other sessions, which run on the
Claude Code process's own connection or its local inbox. Watch passes no
permission mode, so they most likely prompt in both containment postures, and
the class mismatch was what held their messages to bypass workers.

Review sessions therefore get a PreToolUse hook denying `SendMessage`,
`SendFile`, `RemoteTrigger`, and `ListAgents` in every containment mode. It's
verified by matcher and command, so a same-matcher hook from a workspace or
overlay can't stand in for it. The hook has limits:
- In sandbox mode, Bash can't reach the local inbox, because the no-egress
  stanza niwa owns leaves unix sockets disallowed.
- With `watch_sandbox = off`, Bash has full access, and the hook is accident
  prevention only.
- Hooks load when a session starts, so a review staged before the upgrade runs
  without the hook until watch re-stages it. The guide says to let in-flight
  reviews re-stage before turning the behavior on.
- A `disableAllHooks` setting in any source turns this hook off along with every
  other review hook. That's true of review containment generally, and the guide
  notes it.

Messages into review sessions aren't refused, because this feature doesn't
change that direction.

**Who can turn it on.** Only the `[global] accept_session_messages_on_dispatch`
key in niwa's machine configuration and the `--accept-session-messages` flag
decide the behavior. No workspace config key, instance setting, or settings file
a repository carries is read for it, and the overlay repository registered under
`[global_config]` has no `[global]` table to set it in. That guarantee covers
configuration sources. It doesn't cover two indirect routes:

- niwa finds its machine configuration through `XDG_CONFIG_HOME` and `HOME`. A
  workspace can set either in its `[claude.env]` or `[session.env]` tables, and
  an overlay can if its `[env]` reaches the session. Either one changes which
  `config.toml` a `niwa dispatch` run from inside those sessions reads.
  - That affects every machine-level dispatch preference, not only this one, and
    a workspace config able to do it can already install hooks that run in the
    session, so it isn't a new capability.
  - Relocating `HOME` also moves the Claude Code user settings a nested session
    reads. That can grant acceptance with no niwa involvement, so no audit line
    or record shows it.

  Rejecting those two variable names in the session environment tables is
  follow-up work covering all machine-level keys.
- Any agent can pass the flag. A worker steered by an injected message can
  dispatch more workers with the behavior on, and with the machine key on it
  doesn't need the flag. A bypass worker can also edit the machine
  configuration, since it can write anything the user can.

**The settings document.** niwa builds the launch settings document from
constant keys and values only, marshals it with `encoding/json`, and passes it
as one argv element with no shell involved. Nothing from a workspace,
repository, or prompt reaches it. `niwa dispatch` accepts no extra agent
arguments, and every Claude launch puts `--` before the prompt. A prompt that
begins with `--settings=` is therefore read as prompt text, not as a second
`--settings` that would replace niwa's. A comment on the rendering helper
records that contributors pass constants.

**The marker file.** The explanation marker is an empty file created with
exclusive-create semantics and mode `0600`, in a directory niwa creates only
when it's missing, with the `0755` mode its config writer already uses.
Exclusive create doesn't follow a symlink at that path, so a planted link can't
redirect the write, and the existence check uses `os.Lstat` so it agrees with
the create. The marker gates no authority: it only suppresses the one-time
explanation, and the audit line prints on every dispatch where the behavior
takes effect whether or not the marker exists.

**What's recorded.** The session mapping gains one boolean, stored with the same
`0600` file and `0700` directory protection mappings already have, and
`niwa list` reports it for every instance, including finished sessions. The
stderr lines carry no paths, identifiers, or secrets, and niwa doesn't read or
write the developer's Claude Code user or managed settings. The recorded value
means niwa launched the worker with the setting, not that Claude Code confirmed
it: a managed policy or a stricter project setting can still hold messages,
which makes the record err toward reporting more acceptance than there is, now
that the prompt can't replace niwa's document. It doesn't reflect acceptance a
developer turned on in their own user settings, or acceptance a relocated
`HOME` brings in.

**Audit surfaces.** Default off, the per-dispatch audit line, and the
`niwa list` field are detective controls, not preventive ones. When an agent
dispatches workers, the audit line lands in that agent's output rather than in
front of a person, so `niwa list --json` is the reliable way to see which
instances accept unattended messages; the field is on every record for that
reason. Turning the machine key off doesn't reach sessions already launched,
because Claude Code reapplies their launch settings when it restarts or reopens
them. To withdraw the grant, list the instances reporting
`accepts_session_messages: true` and stop their sessions.

**Recommended posture.** The guide recommends turning the behavior on only on
machines where every session sharing the Claude Code account is one the
developer would let direct a bypass-mode worker, and preferring the
per-dispatch flag to the machine key when only some fan-outs need it. Dispatch
containment, a sandbox and outbound network limits comparable to review
sessions, is the control that would make unattended delivery safe for
untrusted inputs, and it's separate work.

## Consequences

### Positive

- Dispatched workers stop depending on permission-mode agreement for what they
  receive, so a coordinator and a worker launched in different permission modes
  no longer need a person to approve the coordinator's messages to the worker.
- Remote control's injection now goes through a map any later launch setting can
  join without adding a second `--settings`.
- The grant is auditable after the fact in `niwa list`, including for finished
  sessions and detached dispatches whose stderr nobody kept.
- niwa writes neither `config.toml` nor the developer's personal settings.

### Negative

- The behavior is inbound only. Replies into sessions launched without it,
  including the developer's own, still wait there.
- Switching it off keeps Claude Code's default, which doesn't isolate a worker
  from same-class peers.
- It removes a human checkpoint on text arriving from sessions in a different
  class, for workers that often run with prompts off and without containment.
- It depends on Claude Code behavior niwa doesn't control, and niwa doesn't gate
  on the Claude Code version. The dependencies are:
  - the meaning of `crossSessionInbound`;
  - deep-merged `--settings`;
  - saved `respawnFlags`;
  - the `--` prompt separator;
  - the tool list;
  - hook matcher semantics.
- A developer who only dispatches through agents, or who closes the window
  instead of returning from the attach, sees the explanation again.
- The capability table grows a row that every future agent has to declare, and
  adding it moves fixed counts in several tests and documents.
- A worker steered by an injected message can dispatch more accepting workers,
  and a workspace that relocates `XDG_CONFIG_HOME` or `HOME` in its session
  environment changes which machine configuration a nested dispatch reads.
- Review sessions lose `SendMessage`, `SendFile`, `RemoteTrigger`, and
  `ListAgents`, which nothing uses today.
- Every Claude launch gains a `--` before the prompt, which changes the argv
  shape that existing tests expect.
- The watch exclusion is covered by a unit test rather than a functional
  scenario.

### Mitigations

In the order of the negatives above:

- **Inbound only:** the one-time explanation and the guide say so, name
  re-dispatching as the fix for a dispatched peer, and give the step and its
  cost for the developer's own sessions.
- **Off doesn't isolate:** accepted. It's Claude Code's default, and the guide
  and the override line both say the worker keeps that default rather than
  becoming isolated.
- **Removed checkpoint:** off by default, an audit line on every dispatch where
  it takes effect, a `niwa list` record, and the guide's recommended posture.
  Review sessions deny cross-session messaging in every containment mode, so a
  contained reviewer can't hand instructions to an uncontained worker.
- **Claude Code dependency:** the guide records the Claude Code version the
  manual delivery check last passed on, and a semantics change there would err
  toward holding messages rather than accepting more. The manual check also
  compares Claude Code's tool list against the denied list.
- **Repeated explanation:** the developer can create the marker by hand, and the
  guide says so.
- **Capability counts:** the count changes land in one commit with the row, and
  the declaration test fails for any agent that omits it.
- **Nested dispatch and relocated configuration:** accepted as properties of
  letting agents dispatch and of workspace-controlled session environments.
  Every nested grant still prints its audit line and lands in `niwa list`, and
  rejecting `XDG_CONFIG_HOME` and `HOME` in session environment tables is
  follow-up work covering all machine-level keys.
- **Review sessions lose the session-reaching tools:** accepted. Nothing uses
  them, and the hook's message says why the call was refused.
- **Argv shape:** the separator is the form Codex launches already use, and the
  tests that pin Claude's argv change in the same commit.
- **Unit coverage for the watch exclusion:** the test drives both real launch
  sites, and watch writes no session mapping, so the exclusion holds by
  construction as well as by test.
