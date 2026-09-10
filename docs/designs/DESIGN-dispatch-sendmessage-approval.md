---
schema: design/v1
status: Proposed
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
  A small launch-settings builder replaces the fixed remote-control document and
  emits one merged `--settings` value from whichever contributors apply: remote
  control, as today, and a new inbound-acceptance contributor resolved from a
  tri-state `--accept-session-messages` flag over a `[global]`
  `accept_session_messages_on_dispatch` key. A new `DispatchInboundAcceptance`
  capability row decides which agents can receive it. The outcome is recorded on
  the session mapping and shown by `niwa list`, and a one-time explanation is
  suppressed by a marker file beside `config.toml` that is created only when a
  terminal saw it. `niwa watch` review sessions gain a hook denying
  cross-session messaging, so a contained reviewer can't hand instructions to a
  worker that now accepts them.
rationale: |
  The merged document is the only channel Claude Code honors for this setting
  that niwa can reach, and merging is forced by the measured last-wins behavior
  of a repeated `--settings`. A builder keeps remote control's output
  byte-identical while letting each contributor keep its own conditions. A
  capability row, not a flag-spelling check, keeps the dispatch path agent-neutral
  under its AST scan and gives Codex's gap list a reason. A marker file beside
  `config.toml` avoids rewriting that file and survives fresh dispatch
  instances, and recording on the mapping reuses the durable per-session record
  keep-alive already relies on.
upstream: docs/prds/PRD-dispatch-sendmessage-approval.md
user_visible_surface: true
decision_provenance: inline-resolved
---

# DESIGN: Unattended peer messages for dispatched sessions

## Status

Proposed

## Context and Problem Statement

`niwa dispatch` launches a Claude worker as `claude --bg` with an argv assembled
in `runDispatch` (`internal/cli/dispatch.go`). Two pieces of that argv matter
here. For a workspace that declares a bypass posture, step 9a derives
`--permission-mode bypassPermissions`. When the machine preference
`remote_control_on_dispatch` is on and the instance settings leave
`remoteControlAtStartup` unset, step 9c appends `--settings` followed by the
fixed document `{"remoteControlAtStartup":true}` (`dispatch.go:585-601`,
`internal/cli/dispatch_remotecontrol.go`).

Claude Code holds an inbound cross-session message whenever the sending and
receiving sessions are in different permission-mode classes, unless the
receiving session's `crossSessionInbound` setting is `accept`. The PRD asks niwa
to launch workers with that setting when the developer opts in: from a
`[global]` key in `config.toml` (R1), overridable in both directions per dispatch
(R2), with no other source able to turn it on or off (R3). A worker launched
with it must accept inbound messages whatever the sender's class (R4), keep
accepting after Claude Code restarts or reopens it (R5), and keep every other
launch setting niwa already applies (R6).

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

The rest of the PRD adds surface around the launch. Each dispatch where the
behavior takes effect writes one stderr audit line naming its source (R7); a
dispatch whose flag turns off a machine setting of `true` writes one override
line that says the worker keeps Claude Code's default (R8). The session record
stores the outcome and `niwa list` reports it per instance, always present (R9).
The first dispatch where the behavior takes effect prints an explanation of what
it doesn't cover and what the developer's own sessions would need (R10), and a
marker file beside `config.toml`, created only when a terminal saw the
explanation, suppresses it afterwards (R11). An agent that can't receive the
behavior gets a warning only when the flag asked for it (R13). `niwa watch`
review sessions never receive it (R14). An unreadable `config.toml` counts as an
absent machine setting while the flag still applies (R15), and `niwa dispatch`
stdout doesn't change (R16).

The technical problem is to add a machine-level, per-dispatch-overridable launch
contributor that shares the single `--settings` slot with remote control without
disturbing remote control's own conditions; to decide which agents can receive
it without naming an agent in the dispatch path; to record the outcome where
`niwa list` can read it; and to remember, per configuration directory, that the
explanation was seen.

## Decision Drivers

- **D1. One legal channel.** The setting must arrive in the launch `--settings`
  document. The materializer can't grant it, and niwa may not touch personal or
  managed settings (R3, R12).
- **D2. One `--settings` slot.** A second `--settings` element silently replaces
  the first, so remote control's document has to share the one element (R6).
- **D3. Remote control's conditions are untouched.** Remote control injects only
  when the machine preference is on, the instance left `remoteControlAtStartup`
  unset, and `ANTHROPIC_API_KEY` isn't forcing API-key auth. Its tests,
  including the settings round-trip test, must pass unchanged.
- **D4. No agent names in the dispatch path.** `internal/cli/dispatch_layout_test.go`
  scans the dispatch-path files and fails on any agent constant or literal. An
  agent-specific decision goes through an `agentplan` declaration or a flag
  spelling, and any new `dispatch_*.go` file joins the scanned list.
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
- **D8. `niwa list` is per instance.** `niwa list` emits one record per instance
  and annotates it from session mappings. The new field must be present on every
  record and not gated on liveness, unlike `keep_alive` (R9).
- **D9. Stdout is unchanged.** All new output goes to stderr (R16).
- **D10. `niwa watch` is unaffected.** Watch builds its own passthrough and calls
  `dispatchLaunch` directly (`internal/cli/watch.go:581`, `:844`), so the
  new resolution must live in `runDispatch` only (R14).
- **D11. One pull request.** All changes are in this repository, and no piece
  needs another merged first.
- **D12. Review containment holds.** `niwa watch` review sessions read
  untrusted changes. Today, when they run in a prompting mode, the class
  mismatch holds their messages to bypass workers. A worker that accepts
  would remove that hold, so the feature must not give a contained reviewer a
  channel to an uncontained worker. Review containment's egress hook matches
  only `WebFetch|WebSearch|mcp__` (`internal/watch/containment.go:18`), so it
  doesn't cover messaging today.

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

#### Chosen: A launch settings builder with independent contributors

A new `launchSettings` type in a new file, `internal/cli/dispatch_settings.go`,
collects key-value pairs from contributors and renders them once. Each
contributor decides for itself whether it contributes: remote control adds
`remoteControlAtStartup: true` exactly when `resolveDispatchRemoteControl`
returns inject, and inbound acceptance adds `crossSessionInbound: "accept"`
exactly when the new behavior takes effect. The builder marshals the collected
map with `encoding/json`, which sorts keys, and step 9c appends
`spec.Flags.Settings` and the document as two discrete argv elements, only when
at least one key is present. With remote control alone the output is
`{"remoteControlAtStartup":true}`, byte-identical to the current fixed
document, so the existing remote-control tests hold unchanged. With both it is
`{"crossSessionInbound":"accept","remoteControlAtStartup":true}`. Every key and
value is a constant, so nothing author-supplied reaches the document, and the
no-shell-interpolation rule of discrete argv elements holds.

#### Alternatives Considered

**A second `--settings` element**: append another `--settings` carrying only the
new key. Rejected because a repeated `--settings` is last-wins and silent, so it
would drop remote control's document whenever both apply (D2).

**Widen the remote-control helper**: turn `remoteControlSettingsJSON` into a
function that takes extra keys. It has fewer moving parts, but it ties an
unrelated feature to the remote-control file, and remote control's default-fill
condition would start deciding whether the inbound key is emitted, even though
the inbound behavior has its own precedence and eligibility (D3). The next
contributor would need the same rework.

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

#### Chosen: A `DispatchInboundAcceptance` capability row

`internal/agentplan/capability.go` gains a constant `DispatchInboundAcceptance`
with the catalog row `{DispatchInboundAcceptance, "dispatch-inbound-acceptance",
RouteLaunch}`, placed after `DispatchLaunch`. `internal/agentplan/declaration.go`
declares it `StateImplemented` for Claude, requiring `DispatchLaunch`, and
`StateUnavailable` for Codex with `ReasonNoSuchConcept` and the reason "Codex has
no setting for accepting messages from other sessions." `gaplist.go` gains the
label "Accepting messages from other sessions without an approval prompt", so
Codex's generated gap list names the gap. The dispatch path treats the behavior
as deliverable when `agentplan.Lookup(agentplan.DispatchInboundAcceptance,
dispatchedAgent)` reports `StateImplemented` and `spec.Flags.Settings` is
non-empty, the same shape remote control and keep-alive use. When the flag asked
and the behavior isn't deliverable, step 9c prints
`niwa dispatch: --accept-session-messages does not apply to the %q agent and was ignored. %s`
with the declaration's reason; when only the machine setting asked, it prints
nothing.

#### Alternatives Considered

**Gate on the settings flag spelling alone**: treat the behavior as deliverable
whenever `spec.Flags.Settings` is non-empty. It works for today's two agents,
but it makes "Codex has no `--settings`" the reason, which is incidental. A
future agent with a settings flag but no inbound setting would receive a
meaningless key silently, and the gap list would say nothing.

**Reuse the `RemoteControl` row**: treat the behavior as deliverable wherever
remote control is. It couples two independent capabilities; an agent could
support one and not the other.

**Name the agent in the dispatch path**: rejected outright by the AST scan
(D4).

### Decision 3: Remembering that the explanation was shown

The explanation prints on the first dispatch where the behavior takes effect and
is suppressed afterwards, per configuration directory, only once a terminal has
seen it (R10, R11). niwa has no once-per-machine notice mechanism today.

Key assumptions:
- `os.OpenFile` with `O_CREATE|O_EXCL` is atomic on the local filesystems niwa
  supports.
- `IsStderrTTY` in `internal/cli/prompt.go` is the right terminal check; it's
  already a stubbable variable used for the interactive prompt capture.

#### Chosen: A marker file beside `config.toml`, created only for a terminal

The marker is `accept-session-messages-notice` in the directory that holds
`config.toml`, computed as `filepath.Dir(config.GlobalConfigPath())`, so it is
`$XDG_CONFIG_HOME/niwa/` or `~/.config/niwa/`. `config.GlobalConfigDir()` isn't
used, because it returns the overlay clone directory `.../niwa/global`. When the
behavior takes effect, niwa checks whether the marker exists with `os.Lstat`, so
a dangling symlink counts as present, matching what the exclusive create will
find. If it doesn't,
niwa prints the explanation; then, only if `IsStderrTTY()` reports a terminal,
it runs `os.MkdirAll(dir, 0o700)` and opens the marker with
`O_CREATE|O_EXCL|O_WRONLY` and mode `0o600`. An "already exists" error means a
concurrent dispatch won the race and is ignored; any other error is ignored too,
because the dispatch already succeeded, and the explanation simply prints again
next time. The file's contents are empty.

#### Alternatives Considered

**A "seen" key in `config.toml`**: store the flag in `[global]`. Rejected because
`SaveGlobalConfigTo` re-encodes the parsed struct, dropping the developer's
comments and unknown keys, and parallel dispatches would race on the
whole-file rewrite (D7).

**The per-instance one-time notices**: record it in the instance's
`DisclosedNotices`. Every dispatch runs in a fresh instance, so the notice would
fire on every dispatch.

**A state or cache directory**: put the marker under `XDG_STATE_HOME` or the
user cache directory. niwa has no convention for either, and keeping the marker
beside `config.toml` lets `XDG_CONFIG_HOME` move both together, which the PRD's
acceptance criteria rely on.

**Mark it shown whenever it's printed**: create the marker regardless of the
terminal check. Rejected because the first qualifying dispatch is often run by
an agent whose stderr no person reads, which would consume the explanation
unseen.

### Decision 4: Recording the outcome for `niwa list`

`niwa list` emits one `workspace.InstanceRecord` per instance and annotates it
from session mappings in `annotateFromSessionMappings`
(`internal/cli/list.go:117`), which today sets `KeepAlive` only for a live
session. The new field must be on every record, `true` when the instance's
dispatched session had the behavior take effect, for as long as the record
exists (D8).

Key assumptions:
- Dispatch writes one mapping per instance; if more than one mapping points at an
  instance, any one recording the behavior makes the instance report `true`.

#### Chosen: A mapping field and an always-present instance field

`workspace.SessionMapping` gains `AcceptsSessionMessages bool
\`json:"accepts_session_messages,omitempty"\``, set at step 11 from whether the
behavior took effect, beside `KeepAlive`. The omitempty tag keeps mappings written
by other paths, and older mappings, byte-identical; they read as `false`.
`workspace.InstanceRecord` gains `AcceptsSessionMessages bool
\`json:"accepts_session_messages"\`` without omitempty, so it's present on every
record. `annotateFromSessionMappings` sets it for any instance with a mapping that
recorded it, without the liveness check keep-alive applies. The human output
appends ` (accepts session messages)` after the name, after ` (keep-alive)` when
both apply.

#### Alternatives Considered

**Gate it on liveness like keep-alive**: report it only while the session runs.
Rejected because the grant was true for the session's whole life, and the audit
need is after the fact, including for finished sessions (R9).

**A separate audit log**: append each grant to a log file. It adds a file to
rotate and clean up, while the mapping is already the durable per-session record
the reaper removes with the instance.

**An optional list field**: add the field with omitempty, like `keep_alive`.
Rejected because scripts would have to treat a missing field as `false`, and R9
asks for the field on every record.

## Decision Outcome

**Chosen: 1 (settings builder) + 2 (capability row) + 3 (marker beside
`config.toml`) + 4 (mapping and instance fields)**

### Summary

A new tri-state flag, `--accept-session-messages`, is registered on
`niwa dispatch` with `triBoolValue` and `NoOptDefVal = "true"`, so a bare flag
and `=true` mean on and `=false` means off. `config.GlobalSettings` gains
`AcceptSessionMessagesOnDispatch *bool
\`toml:"accept_session_messages_on_dispatch,omitempty"\``, with no `niwa config
set` setter, like its siblings. A new file, `internal/cli/dispatch_inbound.go`,
holds the resolver `resolveDispatchInboundAcceptance(flag *bool, global
config.GlobalSettings) (on bool, source string)`: the flag wins when given;
otherwise the machine setting decides when non-nil; otherwise the behavior is
off. The source is `--accept-session-messages` or `machine setting`. An
unreadable or malformed `config.toml` already yields a zero `GlobalSettings` in
`runDispatch`, so the machine setting counts as absent while the flag still
applies.

At step 9c, `runDispatch` resolves remote control exactly as today and resolves
the inbound behavior, then checks deliverability through the new capability row
and the settings flag. A non-deliverable resolution prints the warning only when
the flag asked. The builder collects the keys that apply and appends one
`--settings` document. Nothing else in the argv changes, and the resume commands
niwa prints stay `claude attach <id>`, because Claude Code carries the setting
across restarts itself.

After the launch returns and the session mapping is written, which is what
counts as a successful launch, `runDispatch` records `AcceptsSessionMessages` on
the mapping and writes the stderr lines. When the behavior took effect, the audit
line comes first, followed by the explanation if the marker is absent. When the
flag turned off a machine setting of `true` for an agent that could have received
the behavior, the override line is written instead. A launch that fails returns
before any of this, so no audit line, marker, or `true` record can precede a
failed launch.

### Rationale

The four decisions reinforce each other around one seam, step 9c, and one record,
the session mapping. The builder is what lets inbound acceptance join the one
`--settings` element without inheriting remote control's conditions. The
capability row is what keeps that seam agent-neutral, so the same line of code
serves Claude and gives Codex a reason in its gap list. Writing the stderr lines
and the mapping only after the mapping write succeeds makes "takes effect" a
single fact that the audit line, `niwa list`, and the explanation all report
consistently. The marker's terminal rule trades a repeated explanation for agents
that only ever dispatch through agents, which is documented, against losing the
explanation unseen, which isn't recoverable.

## Solution Architecture

### Overview

The feature adds one launch contributor, one capability row, one machine-config
key, one flag, two record fields, one marker file, and a guide. Resolution,
delivery, and stderr output all live in `runDispatch`; nothing moves into
`dispatchLaunch`, which `niwa watch` also calls.

### Components

- `internal/config/registry.go`: `GlobalSettings.AcceptSessionMessagesOnDispatch`,
  a `*bool` with the TOML key `accept_session_messages_on_dispatch`, listed with
  the other setter-less keys in the file's comment.
- `internal/agentplan/capability.go`, `declaration.go`, `gaplist.go`: the
  `DispatchInboundAcceptance` constant, catalog row, per-agent declarations, and
  gap-list label.
- `internal/cli/dispatch_settings.go` (new): the `launchSettings` builder, with
  `set(key string, value any)` and `render() (string, bool)`, where the boolean
  reports whether any key was set.
- `internal/cli/dispatch_inbound.go` (new): the flag variable, the resolver, the
  four fixed message strings, and `showInboundExplanation(w io.Writer, dir
  string, isTTY func() bool)`, which prints the explanation when the marker is
  absent and creates the marker when `isTTY()` is true.
- `internal/cli/dispatch.go`: flag registration in `init()`; step 9c rewritten to
  use the builder; the post-mapping stderr lines.
- `internal/cli/dispatch_layout_test.go`: both new files added to
  `dispatchPathFiles`, so the AST scan covers them.
- `internal/workspace/session_map.go`: `SessionMapping.AcceptsSessionMessages`.
- `internal/workspace` instance records and `internal/cli/list.go`:
  `InstanceRecord.AcceptsSessionMessages`, the annotation, the human marker, and
  the `--json` help text.
- `internal/watch/containment.go`: a `messagingDenyMatcher = "SendMessage"`
  constant and a `messagingDenyHook()` PreToolUse hook that exits 2 with
  `niwa watch: review sessions don't message other sessions`, appended by
  `ApplyReviewSettings` in every mode (deduped by matcher, like the posting
  guard) and required by `VerifyReviewSettings` in every mode, so a dropped
  hook stops the launch.
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
- **Guide URL:**
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
  When stderr isn't a terminal, the last sentence is instead
  `niwa will show this again until it's been shown at a terminal; it's also at <guide URL>`.
- **Marker:** `<config dir>/accept-session-messages-notice`, empty, mode `0o600`.
- **Records:** `SessionMapping.accepts_session_messages` (omitempty) and
  `InstanceRecord.accepts_session_messages` (always present).

### Data Flow

1. `runDispatch` loads `config.toml` once. A load failure leaves a zero
   `GlobalSettings`.
2. The resolver combines the flag and `GlobalSettings.AcceptSessionMessagesOnDispatch`
   into `on` and a `source`.
3. Step 9c checks deliverability through `agentplan.Lookup` and
   `spec.Flags.Settings`, prints the warning when the flag asked for an
   undeliverable behavior, and feeds the builder: remote control's key when its
   resolver says inject, the inbound key when the behavior is on and deliverable.
4. The builder renders one document, appended as `--settings <document>`.
   Claude Code saves it in the job's `respawnFlags` and reapplies it on
   `claude respawn` and `claude attach`.
5. After the launch and the mapping write succeed, step 11 records
   `AcceptsSessionMessages`, and the stderr lines follow: the audit line and the
   explanation, or the override line.
6. `niwa list` reads the mappings and sets `accepts_session_messages` on each
   instance record.

## Implementation Approach

All phases land in one pull request; each is a separately reviewable commit, and
none has to merge before another.

### Phase 1: Configuration key and capability row

Add `AcceptSessionMessagesOnDispatch` to `GlobalSettings`, and the
`DispatchInboundAcceptance` constant, catalog row, declarations, and gap-list
label. Update the capability tests that enumerate the table.

Deliverables:
- `internal/config/registry.go` and its tests
- `internal/agentplan/capability.go`, `declaration.go`, `gaplist.go` and their tests

### Phase 2: Launch settings builder

Introduce `launchSettings` and route remote control's injection through it,
producing byte-identical output. The existing remote-control tests and the
settings round-trip test are the regression guard.

Deliverables:
- `internal/cli/dispatch_settings.go` and tests
- `internal/cli/dispatch.go` step 9c
- `internal/cli/dispatch_layout_test.go`

### Phase 3: Flag, resolver, delivery, and the audit, override, and warning lines

Register the flag, add the resolver and message strings, feed the inbound key
into the builder, and write the three kinds of stderr line at the post-mapping
point. Depends on Phases 1 and 2.

Deliverables:
- `internal/cli/dispatch_inbound.go` and tests
- `internal/cli/dispatch.go` flag registration and post-mapping output

### Phase 4: Durable record and `niwa list`

Add the two record fields, set the mapping field at step 11, and annotate and
print in `niwa list`. Depends on Phase 3 for the value.

Deliverables:
- `internal/workspace/session_map.go`, the instance record type, and tests
- `internal/cli/list.go` and tests

### Phase 5: One-time explanation and marker

Add `showInboundExplanation` and call it after the audit line. Tests stub
`IsStderrTTY` and cover the terminal and non-terminal paths, concurrent first
dispatches, an unwritable directory, a missing directory, and `XDG_CONFIG_HOME`.
Depends on Phase 3.

Deliverables:
- `internal/cli/dispatch_inbound.go` and tests

### Phase 6: Guide, help, and functional scenarios

Write the guide with the same top-level headings as the keep-alive guide, add
the contributor-guide index entry, check the flag's help and completion, and add
the `@critical` scenarios, including the watch exclusion and the Codex warning.

Deliverables:
- `docs/guides/session-message-acceptance.md`, `CLAUDE.md`
- `test/functional/features/` scenarios and step definitions

### Phase 7: Review-session messaging deny

Add the messaging-deny hook to review-session settings and to their
verification. It depends on nothing else here and can land first; it's in this
pull request because the feature is what makes the channel matter.

Deliverables:
- `internal/watch/containment.go` and its tests

## Security Considerations

This feature gives a dispatched worker no new permission. What it removes is a
checkpoint. Claude Code normally holds a message from a session in a different
permission-mode class until a person approves it, and dispatched workers often
run with `bypassPermissions` and no containment. With the behavior on, text
from another session reaches such a worker and is acted on without a prompt.
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
The same exposure already exists between two bypass-mode sessions, which Claude
Code delivers between without a hold whether or not this feature is on, so
switching the behavior off doesn't isolate a worker from them.

**Review sessions.** Sessions that `niwa watch` launches review untrusted changes
and never receive this behavior, but they could still send. Their network
sandbox doesn't cover cross-session messaging, which goes through the local
Claude Code process rather than the network, and in the operator-approval
posture they run in a prompting mode, so the class mismatch was what held their
messages to bypass workers. To keep this feature from becoming a way around
review containment, review sessions get a PreToolUse hook that denies the
cross-session messaging tool in every containment mode, the same way the
posting guard applies in every mode. Nothing in a review session's job needs to
message another session.

**Who can turn it on.** Only the `[global] accept_session_messages_on_dispatch`
key in niwa's machine configuration and the `--accept-session-messages` flag
decide the behavior. No workspace config key, instance setting, or settings file
a repository carries is read for it, and the overlay repository registered under
`[global_config]` can't set it because its schema has no `[global]` table. That
guarantee covers configuration sources. It doesn't cover two indirect routes:

- niwa finds its machine configuration through `XDG_CONFIG_HOME` and `HOME`. A
  workspace that sets either in its `[claude.env]` or `[session.env]` tables
  changes which `config.toml` a `niwa dispatch` run from inside its sessions
  reads. That affects every machine-level dispatch preference, not only this
  one, and a workspace config able to do it can already install hooks that run
  in the session, so it isn't a new capability. Rejecting those two variable
  names in the session environment tables is follow-up work covering all
  machine-level keys.
- Any agent can pass the flag. A worker steered by an injected message can
  dispatch more workers with the behavior on, and with the machine key on it
  doesn't need the flag. A bypass worker can also edit the machine
  configuration, since it can write anything the user can.

**The settings document.** niwa builds the launch settings document from
constant keys and values only, marshals it with `encoding/json`, and passes it
as one argv element with no shell involved. Nothing from a workspace,
repository, or prompt reaches it. `niwa dispatch` accepts no extra agent
arguments, so a caller can't add a second `--settings` that would replace it. A
comment on the builder records that contributors pass constants.

**The marker file.** The explanation marker is an empty file created with
exclusive-create semantics and mode `0600`, in a directory niwa creates with
mode `0700` only when it's missing. Exclusive create doesn't follow a symlink at
that path, so a planted link can't redirect the write, and the existence check
uses `os.Lstat` so it agrees with the create. The marker gates no authority: it
only suppresses the one-time explanation, and the audit line prints on every
dispatch where the behavior takes effect whether or not the marker exists.

**What's recorded.** The session mapping gains one boolean, stored with the same
`0600` file and `0700` directory protection mappings already have, and
`niwa list` reports it for every instance, including finished sessions. The
stderr lines carry no paths, identifiers, or secrets, and niwa doesn't read or
write the developer's Claude Code user or managed settings. The recorded value
means niwa launched the worker with the setting, not that Claude Code confirmed
it: a managed policy or a stricter project setting can still hold messages,
which makes the record err toward reporting more acceptance than there is. It
also doesn't reflect acceptance a developer turned on in their own user
settings.

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

- Dispatched workers stop depending on permission-mode agreement for everything
  they receive, which removes the cause of both rounds of observed holds for the
  receiving side.
- Remote control's injection gains a builder any later launch setting can join
  without adding a second `--settings`.
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
- It depends on Claude Code behavior niwa doesn't control: the meaning of
  `crossSessionInbound`, deep-merged `--settings`, and saved `respawnFlags`. niwa
  doesn't gate on the Claude Code version.
- A developer who only dispatches through agents sees the explanation on every
  qualifying dispatch.
- The capability table grows a row that every future agent has to declare.
- A worker steered by an injected message can dispatch more accepting workers,
  and a workspace that relocates `XDG_CONFIG_HOME` or `HOME` in its session
  environment changes which machine configuration a nested dispatch reads.
- Review sessions lose the ability to message other sessions, which nothing
  uses today.

### Mitigations

- Off by default, with an audit line on every dispatch where it takes effect and a
  marker in `niwa list`.
- Review sessions deny cross-session messaging in every containment mode, so a
  contained reviewer can't hand instructions to an uncontained worker.
- The guide states what the behavior doesn't cover, how to extend it to the
  developer's own sessions and at what cost, and the Claude Code version the
  manual delivery check last passed on, which catches a change in Claude Code's
  behavior.
- The developer can create the marker by hand to stop the explanation repeating.
- The capability row gives Codex's gap list an explicit reason, and the
  declaration test fails for any agent that omits the row.
