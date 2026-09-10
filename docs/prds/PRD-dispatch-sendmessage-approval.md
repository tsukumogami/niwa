---
schema: prd/v1
status: In Progress
problem: |
  Developers who dispatch background Claude Code sessions with niwa can't count
  on those sessions' messages to each other arriving unattended. Claude Code
  holds a message whenever sender and receiver run in different permission
  postures, a developer's sessions routinely do, and niwa offers no way to say
  that the sessions it dispatches should take peer messages without asking.
goals: |
  A developer can opt in, once per machine and overridably per dispatch, to
  dispatched sessions accepting peer messages with no approval prompt, whatever
  posture the sender runs in and across restarts. They can see in dispatch
  output and in `niwa list` when that applies, and are told once what extending
  it to their own interactive session takes and costs.
absorbed: docs/briefs/BRIEF-dispatch-sendmessage-approval.md
motivating_context: |
  Peer-message holds between niwa-dispatched sessions first appeared on
  2026-09-02, when a Claude Code release stopped honoring a permission value niwa
  wrote into instance settings. niwa's fix forwarded the permission mode on the
  launch command instead, and the holds came back on 2026-09-10 (UTC) between a
  session dispatched before that fix and one dispatched after it. A live
  experiment on Claude Code 2.1.267 reproduced the hold and confirmed the
  setting that removes it.
---

# PRD: Unattended peer messages for dispatched sessions

## Status

In Progress

Absorbed [BRIEF: Unattended peer messages for dispatched sessions](docs/briefs/BRIEF-dispatch-sendmessage-approval.md); carried in Absorbed Brief.

## Absorbed Brief

Carried from the brief this PRD was written from, which framed the feature
before its requirements existed.

**Problem.** A developer who dispatches background Claude Code sessions with
niwa and has them message each other can't count on those messages arriving
unattended. Whether one is delivered or held for a human depends on whether the
two sessions run in the same permission posture, which they often don't, so
unattended fan-outs stall. The developer also has no way to tell niwa that the
sessions it dispatches should take messages from their peers, or to switch that
off for one dispatch.

**Outcome.** A developer who opts in on their machine gets every peer message to
the sessions niwa dispatched delivered without a prompt, whatever posture the
sender runs in and across restarts, and never has to think about permission
modes. One who doesn't opt in sees no change. A developer who also wants
workers' messages to reach their own interactive session learns once what that
takes and what it costs.

**Journeys.** Four journeys framed the feature, and User Stories 1 to 4 carry
them: a coordinator collecting reports while it sits idle overnight; opting in
once per machine and seeing each dispatch confirm it; switching the behavior off
for one dispatch and seeing the override confirmed, knowing that Claude Code's
default doesn't isolate a session from peers in the same posture; and new
workers alongside a coordinator dispatched before an upgrade, where the
coordinator's hand-offs arrive but the workers' reports wait inside it until
it's dispatched again.

**Scope boundary.** Requirements R1 to R16 are the in-list, and Out of Scope is
the out-list, unchanged from the brief. The brief left three framing questions
open: what the setting and the flag are called, whether a workspace can set a
default, and what happens for agents that can't receive the behavior.
Decisions and Trade-offs closes all three.

**History.** The brief was first written blaming the way niwa brings sessions
back after a resume. A live experiment refuted that before this PRD was
accepted: Claude Code restores a session's launch settings itself, and the holds
come from sessions in different permission postures messaging each other.

**References.** [DESIGN: dispatch permission mode](../designs/current/DESIGN-dispatch-permission-mode.md)
records the earlier regression and the fix that restored the permission posture
for freshly dispatched sessions. [Remote control on dispatch](../guides/remote-control-on-dispatch.md)
and [session keep-alive](../guides/session-keep-alive.md) are the existing
guides for a host-level, off-by-default, per-dispatch-overridable behavior of
this kind. [DESIGN: niwa mesh removal](../designs/current/DESIGN-niwa-mesh-removal.md)
explains why niwa removed its own agent-messaging layer, which this feature
doesn't reintroduce.

## Problem Statement

A developer who runs background Claude Code sessions through `niwa dispatch`
often has them message each other: workers report to a coordinator, a
coordinator hands follow-up work back to a worker. Claude Code either delivers
such a message straight away or holds it inside the receiving session for a
person to approve, and what decides it is whether the two sessions are in the
same permission-mode class, running with permission prompts turned off or with
them on. When they differ, the message waits for someone to approve it.

A developer's sessions differ all the time. On 2026-09-02 a Claude Code release
stopped honoring the permission value niwa writes into instance settings, so
dispatched workers fell into a different class from the sessions they talked to.
niwa's fix forwarded the permission mode on the launch command, which Claude Code
honors and saves with the session, so it survives restarts. The holds still came
back on 2026-09-10 (UTC), this time between a coordinator dispatched before that
fix, which runs with prompts on, and workers dispatched after it, which run with
them off. The same mismatch arises whenever a worker talks to the developer's own
session, to a session another tool started, or to a worker from a workspace that
asks for a different posture. Nothing in dispatch output says a message is
waiting, so the first sign is a fan-out that stopped making progress.

Keeping every session in the same class isn't something niwa can do: the classes
are fixed when each session is launched, and some of those sessions aren't
niwa's. Claude Code does let a receiving session accept messages from other
sessions whatever class they're in, but niwa gives a developer no way to ask for
that for the sessions it dispatches, or to switch it off for one dispatch. The
only workaround today is to watch every session and approve prompts by hand,
which defeats the reason for dispatching work into the background.

## Goals

- A peer message sent to a session niwa dispatched with the behavior in effect is
  never held for approval, whatever class the sender is in, including after that
  session is restarted or reopened, and the developer never needs to know which
  permission mode any session runs in.
- The choice is explicit, off by default, made once per machine, and reversible
  for a single dispatch in either direction.
- The developer can tell, both at dispatch time and afterwards, which sessions
  accept peer messages unattended.
- The developer learns once, when the behavior first takes effect on their
  machine, what the behavior doesn't cover and what extending unattended
  delivery to their own interactive session takes and costs, and niwa never
  makes that change for them.

## User Stories

1. As a developer running a coordinator I dispatched with niwa, which in turn
   dispatched several workers, I want the workers' reports to reach the
   coordinator while it sits idle overnight, so that I spend the morning reading
   results rather than approving prompts.
2. As a developer who dispatches background work every day, I want to turn
   unattended peer delivery on once for my machine and see each dispatch confirm
   it, so that I stop approving messages by hand and know which sessions it
   covers.
3. As a developer with the machine default on, I want to switch the behavior off
   for one dispatch I intend to steer myself, and see the dispatch confirm it, so
   that the session keeps Claude Code's default handling of messages from other
   sessions while every other session keeps the machine default.
4. As a developer mixing sessions launched at different times or with different
   postures, I want workers dispatched with the behavior to receive messages
   from any of them, so that a coordinator from before an upgrade can still hand
   work to new workers.
5. As a developer who wants workers' messages to reach my own interactive
   session unattended, I want to be told exactly what to change and what it
   costs, so that I decide knowingly and niwa never alters my personal settings.
6. As a developer checking on a fan-out I dispatched earlier with its output
   discarded, I want `niwa list` to show which sessions were accepting messages
   unattended, so that I can audit the grant after the fact.
7. As a developer whose coordinator session dispatches workers itself, so that
   niwa's output goes to an agent rather than to me, I want the one-time
   explanation to still reach me the first time I dispatch from a terminal, so
   that an agent can't consume it unseen.
8. As a developer whose new workers report to a coordinator dispatched before I
   turned the behavior on, I want to know why those reports wait inside the
   coordinator and how to clear it, so that I don't mistake a working feature for
   a broken one.

## Requirements

### Definitions

- **Permission-mode class.** Claude Code sorts sessions into those running with
  permission prompts off (`bypassPermissions`) and those running with them on,
  and holds a message between sessions of different classes unless the
  receiving session accepts inbound messages. Messages between sessions of the
  same class are delivered either way.
- **Inbound-acceptance setting.** Claude Code's `crossSessionInbound` setting
  with the value `accept`, carried in the `--settings` document niwa passes when
  it launches a worker.
- **Successful launch.** The worker's process started and niwa recorded the
  dispatched session.
- **The behavior takes effect** for a dispatch when it resolves on under R3, the
  dispatched agent can receive it (R13), and niwa successfully launches the
  worker with the inbound-acceptance setting. niwa doesn't check the Claude Code
  version. The audit line, the session record, and the one-time explanation
  follow a successful launch and never precede it.

### Functional

- **R1. Machine setting.** niwa's machine configuration (`config.toml` in
  niwa's configuration directory, `~/.config/niwa/` unless `XDG_CONFIG_HOME` is
  set) accepts a boolean `accept_session_messages_on_dispatch` in its `[global]`
  table. When it is `true`, every `niwa dispatch` on that machine launches its
  worker with the inbound-acceptance setting, subject to R2, R13 and R14. When
  the key is absent or `false`, the behavior is off.
- **R2. Per-dispatch override.** `niwa dispatch` accepts
  `--accept-session-messages` (or `--accept-session-messages=true`), which turns
  the behavior on for that dispatch, and `--accept-session-messages=false`,
  which turns it off for that dispatch. Without the flag, R1 decides.
- **R3. Precedence and sources.** The behavior resolves as: flag, then machine
  setting, then off. No other source can turn it on or off: not a workspace's
  configuration, not an instance's settings, and not any file a repository or
  branch carries.
- **R4. Effect on delivery.** A worker for which the behavior took effect
  delivers inbound messages from other Claude Code sessions without an approval
  prompt, whatever permission-mode class the sender is in.
- **R5. Restarts and reopening keep it.** A worker for which the behavior took
  effect still accepts inbound messages without a prompt after Claude Code
  restarts or reopens it, whether through `claude respawn` or through
  `claude attach` after its process has stopped, and a worker for which it didn't
  take effect doesn't gain it. niwa's resume commands and its handling of the
  permission mode are unchanged by this feature.
- **R6. Coexistence with other launch configuration.** Turning the behavior on
  doesn't remove or change any other configuration niwa applies at launch.
  Remote control on dispatch, keep-alive, and the forwarded permission mode all
  remain in effect alongside it.
- **R7. Audit line.** For every dispatch where the behavior takes effect,
  `niwa dispatch` writes exactly one line to stderr, prefixed `niwa dispatch: `,
  containing the text `accepts messages from other sessions without asking`, the
  source (`machine setting` or `--accept-session-messages`), and the URL of the
  user guide R17 requires. It writes no such line for a dispatch where the
  behavior doesn't take effect.
- **R8. Override line.** When `--accept-session-messages=false` turns the
  behavior off while the machine setting is `true`, and the dispatched agent
  could have received the behavior, `niwa dispatch` writes exactly one line to
  stderr, prefixed `niwa dispatch: `, containing the text
  `keeps Claude Code's default for messages from other sessions` and
  `--accept-session-messages=false`. It writes no such line otherwise. The line
  doesn't claim that messages into the worker will all ask: Claude Code's
  default delivers messages between sessions of the same class.
- **R9. Durable record.** niwa's session record for a dispatched session stores
  whether the behavior took effect for it. `niwa list` emits one record per
  instance; its `--json` output includes an `accepts_session_messages` boolean on
  every instance record, always present, `true` when a dispatched session
  recorded for that instance had the behavior take effect and `false` otherwise,
  including instances with no dispatched session record (such as `niwa watch`
  review instances) and records written before this feature. Its human output
  marks the `true` instances with `(accepts session messages)`. Both hold for as
  long as the record exists, whether or not the session is still running.
- **R10. One-time explanation.** On the first dispatch where the behavior takes
  effect, whatever turned it on, `niwa dispatch` writes an explanation to stderr
  stating:
  - that the behavior is inbound only: a message a worker sends into a session
    launched without it, such as a coordinator dispatched earlier, one
    dispatched with the behavior off, or one another tool started, still waits
    for approval there when the two are in different classes, and dispatching
    that session again with the behavior on clears it;
  - that the developer's own interactive Claude Code sessions are one such case,
    governed by the developer's Claude Code user settings, which niwa doesn't
    change;
  - how to change them: the "Messages from your other sessions" row in Claude
    Code's `/config`, or `"crossSessionInbound": "accept"` in
    `~/.claude/settings.json`;
  - that the change applies to every Claude Code session the developer runs and
    to messages from any session able to reach theirs, on this machine or
    elsewhere;
  - when it's written to a terminal, that niwa won't show it again; otherwise,
    that niwa will show it again until it's been shown at a terminal;
  - and the URL of the user guide, where it can be read again.
- **R11. One-time suppression.** R10's explanation is suppressed on later
  dispatches by a marker file named `accept-session-messages-notice` in niwa's
  configuration directory. The marker is created only when the explanation was
  written to an interactive terminal; when stderr isn't a terminal, the
  explanation is written and no marker is created. niwa creates its
  configuration directory if it doesn't exist. The marker isn't `config.toml`,
  printing the explanation never rewrites `config.toml`, and the marker is
  separate from the per-instance one-time notices niwa records during `create`
  and `apply`. Concurrent first dispatches may each print the explanation.
  Failing to create the marker never fails the dispatch.
- **R12. Personal settings are out of reach.** niwa doesn't read or write the
  developer's Claude Code user or managed settings to implement any of R1-R11.
- **R13. Agents that can't receive it.** When the flag asks for the behavior on
  a dispatch whose agent has no way to receive it, `niwa dispatch` writes a
  warning to stderr naming the agent and stating that the flag doesn't apply to
  it and was ignored, and launches the worker without it. When only the machine
  setting turned it on for such an agent, niwa launches the worker without it
  and prints nothing. In both cases the behavior doesn't take effect.
- **R14. Watch review sessions are excluded.** Sessions `niwa watch` launches or
  resumes never receive the behavior, whatever the machine setting says.
- **R15. Unreadable machine configuration.** When niwa's machine configuration
  can't be read, the machine setting counts as absent and the flag still
  applies. That covers a file niwa can't open, a file that isn't valid TOML, and
  a `accept_session_messages_on_dispatch` value that isn't a boolean, which makes
  the whole file unreadable as any type error in it does.
- **R16. Dispatch standard output is unchanged.** Everything this feature adds to
  `niwa dispatch` output goes to stderr, and what `niwa dispatch` writes to
  stdout, including the resume commands it prints, is the same whether or not
  the behavior takes effect, apart from values that differ between any two
  dispatches, such as instance names and session identifiers.

### Non-functional

- **R17. Documentation.** A user guide, reachable at a stable URL, documents: the
  machine setting and the flag and their precedence; the audit and override
  lines; the `niwa list` marker and field; that the behavior is inbound only;
  that switching it off keeps Claude Code's default, which delivers messages
  between sessions of the same class and so doesn't isolate a worker; the
  exclusion of watch review sessions; the behavior for agents that can't receive
  it; that turning the machine setting off doesn't reach sessions already
  dispatched; that a stopped worker receives nothing until it's reopened; that a
  foreground `claude --resume` of a dispatched conversation doesn't carry the
  behavior; the marker file's path, that deleting it shows the explanation again
  and creating it suppresses it; the step for the developer's own interactive
  session with every cost R10 names; and the Claude Code version the manual
  delivery check last passed on. It uses the same top-level section headings as
  the existing keep-alive guide and is listed in the repository's
  contributor-guide index. `niwa dispatch --help` describes the flag, and shell
  completion offers it.
- **R18. Functional coverage.** `@critical` functional scenarios cover the
  behavior off by default, on by flag, on by machine setting, on by machine
  setting and off by flag with its override line, the one-time explanation and
  its marker, the warning for an agent that can't receive it, and the exclusion
  of watch review sessions.

## Acceptance Criteria

### Automated

- [ ] With no `accept_session_messages_on_dispatch` key, and again with the key
  set to `false`, and no flag, the `--settings` document on the launch command
  niwa builds for a Claude worker has no `crossSessionInbound` key, and stderr
  contains no line with `accepts messages from other sessions without asking`.
- [ ] With `accept_session_messages_on_dispatch = true` and no flag, the launch
  command's `--settings` document has `crossSessionInbound` set to `accept`;
  stderr contains exactly one line starting `niwa dispatch: ` that contains
  `accepts messages from other sessions without asking`, `machine setting`, and
  the guide URL; and stderr contains no override line.
- [ ] With the key absent and `--accept-session-messages` given, and again with
  `--accept-session-messages=true`, the launch command carries the setting, and
  stderr contains exactly one line starting `niwa dispatch: ` that contains
  `accepts messages from other sessions without asking`,
  `--accept-session-messages`, and the guide URL.
- [ ] With the key `true` and `--accept-session-messages=true`, stderr contains
  one audit line and no override line.
- [ ] With the key `true` and `--accept-session-messages=false`, the launch
  command doesn't carry the setting, stderr contains no audit line, and stderr
  contains exactly one line starting `niwa dispatch: ` that contains
  `keeps Claude Code's default for messages from other sessions` and
  `--accept-session-messages=false`.
- [ ] With the key absent and `--accept-session-messages=false`, stderr contains
  neither an audit line nor an override line.
- [ ] With remote control on dispatch, keep-alive, a declared bypass posture, and
  the behavior all on, the launch command carries one `--settings` document
  holding both the remote-control setting and `crossSessionInbound`, carries
  `--permission-mode bypassPermissions`, and keep-alive is armed exactly as it is
  without the behavior.
- [ ] With the machine key absent and no flag, each of these fixtures leaves the
  launch command without the setting and leaves every settings file niwa writes
  into the instance without a `crossSessionInbound` key: the key
  `accept_session_messages_on_dispatch` in the workspace's `workspace.toml`, in
  its global and per-repository tables; `crossSessionInbound = "accept"` under
  `[claude.settings]` in `workspace.toml`; `"crossSessionInbound": "accept"` in
  the `.claude/settings.json` at the workspace root; and the same in a cloned
  repository's `.claude/settings.json` and `.claude/settings.local.json`.
- [ ] When the fake `claude` exits non-zero at launch with the behavior on,
  stderr contains no audit line and no explanation, no marker is created, and no
  record reports `accepts_session_messages: true`.
- [ ] The resume commands `niwa dispatch` prints after a dispatch and when an
  attach fails, and the one `niwa list` prints for a session, are identical for
  a session dispatched with the behavior on and one dispatched with it off,
  apart from instance names and session identifiers.
- [ ] An instance whose dispatched session had the behavior take effect shows
  `(accepts session messages)` in `niwa list` and
  `"accepts_session_messages": true` in `niwa list --json`, including after the
  session has finished; an instance dispatched without it shows no marker and
  reports `false`.
- [ ] `niwa list --json` reports `"accepts_session_messages": false`, and doesn't
  fail, for an instance whose session record was written before this feature and
  for an instance with no dispatched session record; with the machine key
  `true`, a `niwa watch` review instance that appears in the list reports
  `false`.
- [ ] With stderr attached to a terminal and no marker file, the first dispatch
  where the behavior takes effect prints the one-time explanation, including the
  sentence saying niwa won't show it again, and creates
  `accept-session-messages-notice`; a second such dispatch, which runs in a
  fresh instance, doesn't print it.
- [ ] With stderr not attached to a terminal, a dispatch where the behavior takes
  effect prints the explanation, including the sentence saying niwa will show it
  again until it's been shown at a terminal, and creates no marker; a later
  dispatch with stderr on a terminal prints it and creates the marker.
- [ ] The one-time explanation's text, which the design fixes, contains a
  sentence for each item R10 lists, and the scenario asserts each of those
  sentences verbatim: inbound only, with re-dispatching as the fix; the
  developer's interactive sessions as a governed case; the `/config` row name and
  the `crossSessionInbound` key; every Claude Code session they run and any
  session able to reach theirs; the terminal-dependent repeat sentence; and the
  guide URL.
- [ ] When niwa's configuration directory isn't writable, a dispatch where the
  behavior takes effect still succeeds and prints the explanation, and the next
  such dispatch prints it again. The scenario is skipped when tests run as root.
- [ ] When niwa's configuration directory doesn't exist, a dispatch with
  `--accept-session-messages` from a terminal creates it and the marker.
- [ ] With `XDG_CONFIG_HOME` set, the key is read from, and the marker written
  under, `$XDG_CONFIG_HOME/niwa/`.
- [ ] Four dispatches run in parallel from a terminal, with the behavior in effect
  and no marker, all succeed, the marker exists afterwards, and the explanation
  is printed at least once.
- [ ] `config.toml` is byte-for-byte unchanged by a dispatch that prints the
  one-time explanation.
- [ ] A dispatch with the behavior on, run with `HOME` pointed at a directory
  whose `.claude/settings.json` is unreadable (mode 000), succeeds and prints
  nothing about that file; with the file readable, its contents and modification
  time are unchanged afterwards. The unreadable case is skipped when tests run as
  root.
- [ ] A code review confirms that no path the feature adds opens a file under
  `~/.claude/` or a Claude Code managed-settings location.
- [ ] Dispatching a Codex worker with `--accept-session-messages` prints a
  warning naming the `codex` agent and stating the flag doesn't apply and was
  ignored, launches without the setting, prints no audit line and no
  explanation, creates no marker, and records `accepts_session_messages: false`.
- [ ] Dispatching a Codex worker with only
  `accept_session_messages_on_dispatch = true` prints no warning, no audit line
  and no explanation, creates no marker, launches without the setting, and
  records `false`; with `--accept-session-messages=false` added, it also prints
  no override line.
- [ ] A review session `niwa watch` launches or resumes on a machine where
  `accept_session_messages_on_dispatch = true` doesn't carry
  `crossSessionInbound` in its launch settings.
- [ ] Each of these fixtures, run without the flag, leaves the behavior off with
  no audit line, and run with `--accept-session-messages`, makes it take effect
  with the audit line naming the flag: a `config.toml` niwa can't open (mode 000,
  skipped as root), a `config.toml` that isn't valid TOML, and
  `accept_session_messages_on_dispatch = "yes"`.
- [ ] In a scenario whose stderr isn't a terminal, the stdout of a dispatch with
  the behavior on is identical to the stdout of the same dispatch with it off,
  apart from instance names and session identifiers.
- [ ] The user guide covers every item R17 lists and uses the same top-level
  section headings as the keep-alive guide, the contributor-guide index links to
  it, `niwa dispatch --help` describes the flag, and shell completion offers it.
- [ ] `@critical` functional scenarios exist for each case R18 lists.

### Manual delivery check

Real delivery between two live Claude Code sessions can't be reproduced by the
fake `claude` binary the functional tests use, so R4 and R5 are verified by this
procedure, which a person other than the author can repeat.

Preconditions:
- Record the Claude Code version.
- The tester's `~/.claude/settings.json` sets no `crossSessionInbound` and no
  bypass default permission mode, and no managed settings file exists.
- The workspace declares `permissions = "bypass"` under `[claude.settings]` in
  `workspace.toml`, so dispatched workers run with `bypassPermissions`.
- Workers are addressed by the `--name` given to `niwa dispatch`.
- The sender S is a session started with `claude --bg` and no permission-mode
  flag, so it runs with prompts on. The tester makes S send by telling it, in its
  own conversation, which session to message and what to say; if S's own tool
  asks for permission to send, the tester approves it, because that prompt isn't
  the hold under test.

A message is **delivered** when its text appears in the receiver's output
(`claude logs <id>`) as an incoming message within 60 seconds with no approval
dialog. It is **held** when the receiver shows an approval dialog stating that
the sending session's permission mode class doesn't match its own. A case in
which neither appears within 60 seconds fails, except case 2c, whose expected
outcome is that the send itself fails.

- [ ] Case 0, control: dispatch worker W0 with the behavior off. A message from
  S to W0 is held.
- [ ] Case 0b, control after a restart or reopen: run `claude respawn <W0>`, and
  a message from S to W0 is still held. Then run `claude stop <W0>`, reopen W0
  with the `claude attach` line `niwa list` prints for it, and detach; a message
  from S to W0 is still held.
- [ ] Case 1: dispatch worker W with the behavior on. A message from S to W is
  delivered.
- [ ] Case 2: run `claude respawn <W>` and confirm it reports the session
  respawned. A message from S to W is delivered.
- [ ] Case 2b: run `claude stop <W>`, then reopen W with the `claude attach` line
  `niwa list` prints for it and detach. A message from S to W is delivered.
- [ ] Case 2c, documenting a limitation: run `claude stop <W>` again and, without
  reopening W, have S message it. The send fails because a stopped background
  session isn't reachable by name; the message is neither held nor delivered.
- [ ] Case 3: reopen W with its `claude attach` line, since case 2c left it
  stopped, then have W message S. The message is held in S, which documents
  that the behavior is inbound only.
- [ ] Case 4, control: dispatch two workers with the behavior off. A message
  from one to the other is delivered, because both run in the same class.

## Out of Scope

- **Changing or checking the developer's personal Claude Code settings.** niwa
  states the step for the developer's own interactive session; the developer
  takes it. The setting applies to every Claude Code session they run and isn't
  niwa's to own.
- **Limiting acceptance to peers in one workspace or on one machine.** Messages
  are addressed by session name across a developer's whole account, niwa sits
  nowhere in that delivery path, and so it can't enforce such a boundary.
  Offering one would promise protection that doesn't exist.
- **Isolating a dispatched session so every inbound message waits for
  approval.** Switching the behavior off keeps Claude Code's default, which only
  holds messages from sessions in a different class. A mode that holds
  everything would be a separate feature.
- **Granting a session anything beyond receipt of a message.** Whatever a
  message asks a session to do still goes through that session's own
  permissions.
- **Making sessions niwa didn't launch accept messages**, including sessions
  niwa dispatched before this feature existed. A session's inbound behavior is
  fixed when it's launched.
- **Changing how niwa resumes or reopens sessions.** Claude Code restores a
  session's launch settings when it restarts or reopens it.
- **Containment for dispatched sessions** comparable to what `niwa watch`
  applies to review sessions, such as a sandbox and outbound network limits.
  Closely related, and more valuable once this lands, but separate work.
- **Extending the behavior to `niwa watch` review sessions.** They review
  untrusted changes, where an unattended inbound channel is the wrong default.
- **A `niwa config set` command for the new key.** niwa's other machine-level
  dispatch preferences are set by editing `config.toml`, and a setter that
  rewrites the file would strip the developer's comments beside a
  security-relevant key.
- **Making dispatched session names unique.** A duplicate name can route a
  message to the wrong session; that's separate work.
- **Removing the permission value niwa writes into instance settings** that
  current Claude Code versions no longer honor. Separate work.
- **Changing which permission mode dispatched sessions run in.**
- **Anything that needs a change to Claude Code itself.**

## Known Limitations

- **It's inbound only.** The behavior governs what a dispatched worker receives.
  A worker's replies into a session that doesn't accept inbound messages, such as
  the developer's own session or a coordinator dispatched before this feature,
  still wait for approval there when the two are in different classes. The
  experiment behind this PRD observed exactly that.
- **Switching it off isn't isolation.** A worker dispatched with
  `--accept-session-messages=false` keeps Claude Code's default, which delivers
  messages between sessions of the same class. In a workspace whose workers run
  with prompts off, other such workers still reach it without a prompt.
- **A stopped worker receives nothing.** A background session stopped with
  `claude stop` isn't reachable by name, so a message sent to it fails to send
  until something reopens it, such as `claude attach`. That isn't a hold, and the
  behavior doesn't change it.
- **It relies on Claude Code keeping launch settings across restarts.** Claude
  Code 2.1.267 saves a background session's launch flags and reapplies them when
  it restarts or reopens the session, which is what R5 depends on. A release that
  stopped would make restarted workers ask again; the manual check's cases 2 and
  2b are what would catch it.
- **A foreground `claude --resume` doesn't carry it.** Resuming a dispatched
  worker's conversation in a new foreground process, by hand or through a tool
  that runs `claude --resume`, starts a session without the worker's launch
  settings. The ways back niwa itself offers into a dispatched session all reopen
  the saved background session instead.
- **Turning the machine setting off doesn't reach sessions already dispatched.**
  A session launched with the behavior keeps it across restarts. To drop it,
  stop the session and dispatch a new one.
- **Acceptance isn't scoped by sender.** A session that accepts peer messages
  accepts them from any session able to address it by name, and that set spans
  the developer's whole account: sessions on other machines and in the cloud,
  including ones unrelated to the workspace. A single account was observed with
  116 addressable sessions across unrelated projects.
- **It removes the human checkpoint on incoming peer text from other classes.**
  Pre-approving delivery grants no authority by itself, but for workspaces that
  run dispatched sessions with permission prompts off, nothing downstream asks
  again before the receiving session acts on what a message from a different
  class says. That's why the behavior is off by default, and why dispatch
  containment is the natural follow-up.
- **The developer's own interactive session stays governed by their personal
  settings.** niwa can describe the step for that direction; it can't take it or
  confirm it was taken.
- **"Once" means once per niwa configuration directory, and only at a
  terminal.** A developer who turns the behavior off and back on later won't see
  the explanation again, and a configuration directory shared across machines
  shares the marker. A developer who only ever dispatches through an agent, with
  no terminal on stderr, sees the explanation in the agent's output on every
  qualifying dispatch until they dispatch once from a terminal or create the
  marker by hand. The audit line on every dispatch where the behavior takes
  effect carries the guide URL, and deleting the marker shows the explanation
  again.

## Decisions and Trade-offs

- **No change to how niwa brings sessions back.** An earlier diagnosis blamed
  the 2026-09-10 holds on niwa's resume commands not carrying the permission
  mode, and proposed adding launch flags to them. A live experiment on Claude
  Code 2.1.267 refuted it. Every way niwa builds or prints back into a dispatched
  Claude session is `claude attach <id>`, which takes no launch flags, and Claude
  Code saves a background session's launch flags with the session and reapplies
  them when it brings the session back. The experiment confirmed that for both
  the permission mode and the inbound-acceptance setting, after `claude respawn`
  of a running session and after `claude attach` reopened a stopped one. The
  holds paired a session dispatched before niwa's permission-mode fix with one
  dispatched after it. The re-entry change would have altered nothing, so R5
  states the outcome and the manual check verifies it across each way a session
  comes back.
- **The fix is the receiving session's inbound setting, not class agreement.**
  Alternative: keep every session in the same permission-mode class. Rejected
  because classes are fixed at launch, some of the sessions involved aren't
  niwa's, and class agreement has already broken twice from two different
  directions. The inbound setting removes the dependency for everything a
  dispatched worker receives.
- **The behavior is off by default.** Alternative: on by default, on the ground
  that a single developer dispatching their own sessions on their own machine
  gains little from the prompt. Rejected because the population this applies to
  is the one where every other gate is already open: unattended sessions, often
  running with prompts off, with no dispatch containment, where a message can
  also resume an idle session that finished its task, in an instance still
  holding its credentials. The prompt is friction for an attended interactive
  session and a real checkpoint for these.
- **Names: `accept_session_messages_on_dispatch` and
  `--accept-session-messages`.** They follow the shape of niwa's sibling
  machine-level dispatch keys, `keep_alive_on_dispatch` and
  `remote_control_on_dispatch`, and use the words of Claude Code's own setting
  label, "Messages from your other sessions". Alternatives: an
  "auto-approve messages" name, rejected because nothing is approved, the hold is
  skipped; a name built on Claude Code's `crossSessionInbound` key, rejected as
  jargon a reader has to look up; any name containing `workspace`, `instance`,
  `peer`, or `local`, rejected because each suggests a boundary that doesn't
  exist where messages are delivered, and people plan around names. This closes
  the naming question the brief left open.
- **No workspace-level default.** Alternative: a workspace-level rung between
  the flag and the machine setting, as keep-alive has. Rejected because a file a
  repository carries can be carried onto machines and into sessions the developer
  never chose to grant, and because Claude Code ignores this particular setting
  when it comes from a project's own settings file anyway. Only the developer's
  machine configuration and the dispatch command can turn it on or off. This
  closes the brief's question about who can turn the behavior on.
- **Agents that can't receive it: warn only when the flag asked.** Alternatives:
  always warn, as keep-alive does, or never warn, as remote control does.
  Keep-alive's warning names its flag even when the machine setting turned it
  on, so a machine-wide preference produces a warning on every dispatch of an
  agent it can't reach, which trains developers to ignore the line. Never warning
  would leave a developer who explicitly asked for the behavior believing it
  applied. This closes the brief's question about such agents.
- **Switching it off keeps Claude Code's default, and the override line says
  exactly that.** Alternatives: make `--accept-session-messages=false` hold every
  inbound message, whatever the sender's class; or say nothing when the flag
  turns the machine setting off. Holding everything was rejected as a different
  feature with its own design questions, recorded under Out of Scope. Silence was
  rejected because the third journey promises the developer sees which choice
  applied. The line names Claude Code's default rather than promising that every
  message will ask, because messages between sessions of the same class don't.
- **"Once" is per niwa configuration directory, marked only when a terminal saw
  it.** Alternatives: print on every dispatch where the behavior takes effect,
  rejected because a multi-sentence explanation repeated through a fan-out stops
  being read; mark it shown whenever it's printed, rejected because the first
  qualifying dispatch is often run by an agent, which would consume it unseen;
  print when a setter command runs, rejected because the sibling keys have no
  setter; record "seen" inside `config.toml`, rejected because rewriting that
  file strips the developer's comments and parallel dispatches would race on it.
  The cost, accepted and documented, is that a developer who only dispatches
  through agents sees it repeatedly until they dispatch once from a terminal or
  create the marker.
- **The audit record lives in the session record and `niwa list`, not only on
  stderr.** Alternative: the stderr line alone. Rejected because detached and
  scripted dispatches routinely discard stderr, and the stated need is to tell
  afterwards which sessions accepted messages unattended. niwa already records
  keep-alive this way. Because `niwa list` has one record per instance, the field
  is keyed to the instance, and it's always present, unlike `keep_alive`, so
  scripts can rely on it.
- **niwa doesn't gate on the Claude Code version.** Alternative: check the
  version and treat an older Claude Code as unable to receive the behavior.
  Rejected because it would make niwa a second source of truth for Claude Code's
  capabilities; the guide states the version the behavior was verified against.
- **niwa neither checks nor edits the developer's personal Claude Code
  settings.** Alternatives: detect whether the developer has enabled the
  interactive-session direction and warn when they haven't, or offer a command
  that writes the setting for them. Detection was rejected because Claude Code
  exposes no way to read a setting's effective value, so niwa would have to
  reimplement Claude Code's precedence rules against sources it may not be able
  to read, and a false warning would push the developer to loosen a
  security-relevant setting machine-wide on niwa's word. Writing was rejected
  because niwa's rule for the one personal configuration it edits is to add whole
  project blocks and never global keys.
- **Watch review sessions are excluded.** They launch and resume through their
  own path, review untrusted changes inside a sandbox, and don't use niwa's other
  machine-level dispatch preferences either. The exclusion is written down and
  tested so that a later change to how launches are built can't extend it by
  accident.
