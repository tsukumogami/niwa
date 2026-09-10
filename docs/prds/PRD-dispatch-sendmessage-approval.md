---
schema: prd/v1
status: Draft
problem: |
  Developers who dispatch background Claude Code sessions with niwa can't count
  on those sessions' messages to each other arriving unattended. Delivery depends
  on whether sender and receiver agree about running with permission prompts off,
  and niwa breaks that agreement whenever it brings a session back, so fan-outs
  stall on approval prompts nobody is there to answer.
goals: |
  Sessions niwa brings back keep the posture they were launched with. A developer
  can opt in, once per machine and overridably per dispatch, to dispatched
  sessions accepting peer messages with no approval prompt, can see in dispatch
  output and in `niwa list` when that applies, and is told once what extending it
  to their own interactive session takes and costs.
upstream: docs/briefs/BRIEF-dispatch-sendmessage-approval.md
motivating_context: |
  Peer-message holds between niwa-dispatched sessions first appeared on
  2026-09-02, when a Claude Code release stopped honoring a permission value niwa
  wrote into instance settings. niwa's fix forwarded the permission mode on the
  launch command instead, which covered first launches only, and the holds
  returned on 2026-09-09 for sessions niwa had re-entered.
---

# PRD: Unattended peer messages for dispatched sessions

## Status

Draft

## Problem Statement

A developer who runs background Claude Code sessions through `niwa dispatch`
often has them message each other: workers report to a coordinator, a
coordinator hands follow-up work back to a worker. Claude Code either delivers
such a message straight away or holds it inside the receiving session for a
person to approve, and what decides it is whether the two sessions agree about
running with permission prompts turned off. When they disagree, the message
waits.

niwa creates that disagreement itself. When a workspace asks for dispatched
sessions to run with prompts off, niwa passes a permission-mode flag on the
launch command, but the ways niwa offers back into the same session don't carry
it. A session brought back that way returns with prompts on while its freshly
launched peers still have them off, and messages between them are held. The
first holds, on 2026-09-02, followed a Claude Code release that stopped honoring
the permission value niwa writes into instance settings. niwa's fix moved the
value onto the launch command, which reached first launches only, and the holds
came back on 2026-09-09. Nothing in dispatch output says a message is waiting,
so the first sign is a fan-out that stopped making progress.

Fixing re-entry alone would leave delivery hanging on two sessions happening to
agree about prompts, and that has broken twice in one week from two different
directions. A developer also has no way to say that the sessions they dispatch
should take messages from their peers without asking, or to make an exception
for one dispatch that will read untrusted text. The only workaround today is to
watch every session and approve prompts by hand, which defeats the reason for
dispatching work into the background.

## Goals

- A session niwa brings back is treated, for message delivery, exactly like a
  freshly launched peer.
- A developer who opts in never has a peer message to a niwa-dispatched session
  held for approval, across launch and the ways back niwa provides, and never
  needs to know which permission mode any session runs in.
- The choice is explicit, off by default, made once per machine, and reversible
  for a single dispatch in either direction.
- The developer can tell, both at dispatch time and afterwards, which sessions
  accept peer messages unattended.
- The developer learns once, when the behavior first takes effect on their
  machine, what extending unattended delivery to their own interactive session
  takes and what it costs, and niwa never makes that change for them.

## User Stories

1. As a developer running a coordinator that dispatched several workers, I want
   the workers' reports to reach the coordinator after I resume it the next
   morning, so that I spend the morning reading results rather than approving
   prompts.
2. As a developer who dispatches background work every day, I want to turn
   unattended peer delivery on once for my machine and see each dispatch confirm
   it, so that I stop approving messages by hand and know which sessions it
   covers.
3. As a developer with the machine default on, I want to switch the behavior off
   for one dispatch that triages issues filed by strangers, so that messages into
   that one session still need my approval while every other session keeps the
   default.
4. As a developer who has never changed a setting, I want a resumed dispatched
   session to receive messages from a sibling it was launched alongside, so that
   resuming a session doesn't strand the work sent to it.
5. As a developer who wants workers' messages to reach my own interactive
   session unattended, I want to be told exactly what to change and what it
   costs, so that I decide knowingly and niwa never alters my personal settings.
6. As a developer checking on a fan-out I dispatched earlier with its output
   discarded, I want `niwa list` to show which sessions were accepting messages
   unattended, so that I can audit the grant after the fact.

## Requirements

### Functional

- **R1. Re-entry keeps launch posture.** Every way niwa provides back into a
  session it dispatched resumes that session with the permission mode it was
  launched with. The ways back are the attach `niwa dispatch` runs when it
  finishes, the resume command it prints after a dispatch, the resume command it
  prints when that attach fails, and the resume command `niwa list` prints for a
  session.
- **R2. Re-entry carries every launch grant.** The same ways back resume a
  session with every per-launch grant niwa gave it at launch, including the
  behavior R3 defines, and without any grant it wasn't launched with.
- **R3. Machine setting.** niwa's machine configuration (`config.toml` in
  niwa's configuration directory, `~/.config/niwa/` unless `XDG_CONFIG_HOME` is
  set) accepts a boolean `accept_session_messages_on_dispatch` in its `[global]`
  table. When it is `true`, every `niwa dispatch` on that machine launches its
  worker so that the worker accepts messages from other Claude Code sessions
  without an approval prompt, unless R4 overrides it for that dispatch. When the
  key is absent or `false`, the behavior is off.
- **R4. Per-dispatch override.** `niwa dispatch` accepts
  `--accept-session-messages`, which turns the behavior on for that dispatch,
  and `--accept-session-messages=false`, which turns it off for that dispatch.
  Without the flag, R3 decides.
- **R5. Precedence and sources.** The behavior resolves as: flag, then machine
  setting, then off. No other source can turn it on: not a workspace's
  configuration, not an instance's settings, and not any file a repository or
  branch carries.
- **R6. Effect on delivery.** A worker launched with the behavior in effect
  delivers inbound messages from other Claude Code sessions without an approval
  prompt, whatever permission mode the sending and receiving sessions run in.
- **R7. Coexistence with other launch configuration.** Turning the behavior on
  doesn't remove or change any other configuration niwa applies at launch.
  In particular, remote control on dispatch and this behavior are both in effect
  when both are on.
- **R8. Audit line.** For every dispatch where the behavior actually takes
  effect, `niwa dispatch` writes exactly one line to stderr, prefixed
  `niwa dispatch: `, stating that the worker accepts messages from other
  sessions without asking and naming the source (the machine setting or the
  flag). It writes no such line for a dispatch where the behavior doesn't take
  effect, including one where the flag turned it off.
- **R9. Durable record.** niwa's session record for a dispatched session stores
  the permission mode the session was launched with and whether the behavior
  took effect for it, so that the ways back in R1 and R2 can reproduce both.
  `niwa list` marks sessions for which the behavior took effect in its human
  output, and reports a boolean field for every session in its `--json` output,
  for as long as the session record exists.
- **R10. One-time explanation.** On the first dispatch on a machine where the
  behavior takes effect, whatever turned it on, `niwa dispatch` writes an
  explanation to stderr stating: that the developer's own interactive Claude Code
  sessions still ask before delivering a message a worker sends them; that this
  is governed by the developer's Claude Code user settings, which niwa doesn't
  change; how to change it (the "Messages from your other sessions" row in Claude
  Code's `/config`, or `"crossSessionInbound": "accept"` in
  `~/.claude/settings.json`); that the change applies to every Claude Code
  session the developer runs and to messages from any session able to reach
  theirs, on this machine or elsewhere; and that niwa won't show it again and
  where to find it.
- **R11. One-time suppression.** R10's explanation is suppressed on later
  dispatches by a marker file niwa creates in its configuration directory. The
  marker isn't `config.toml`, and printing the explanation never rewrites
  `config.toml`. Failing to create the marker doesn't fail the dispatch and
  leaves the explanation to print again on the next dispatch where the behavior
  takes effect.
- **R12. Personal settings are out of reach.** niwa doesn't read or write the
  developer's Claude Code user or managed settings to implement any of R1-R11.
- **R13. Agents that can't receive it.** When the flag asks for the behavior on
  a dispatch whose agent has no way to receive it, `niwa dispatch` writes a
  warning to stderr naming the agent and stating that the flag doesn't apply to
  it and was ignored, and launches the worker without it. When only the machine
  setting turned it on for such an agent, niwa launches the worker without it
  and prints nothing. In both cases the behavior counts as not in effect for R8,
  R9, R10 and R11.
- **R14. Watch review sessions are excluded.** Sessions `niwa watch` launches or
  resumes never receive the behavior, whatever the machine setting says.
- **R15. Unreadable machine configuration.** When niwa's machine configuration
  can't be read, the behavior resolves off, as niwa's other dispatch
  preferences do in that case.
- **R16. Standard output is unchanged.** Everything this feature prints goes to
  stderr. What `niwa dispatch` writes to stdout is the same whether or not the
  behavior is in effect, apart from the resume command it prints, which carries
  what R1 and R2 require.

### Non-functional

- **R17. Documentation.** A user guide documents the machine setting, the flag,
  their precedence, the audit line, the `niwa list` marker, the exclusion of
  watch review sessions, the behavior for agents that can't receive it, the
  routes back into a session that niwa doesn't provide, and the step for the
  developer's own interactive session together with its full cost, in the same
  form as the existing guide for keep-alive, including an entry in the
  repository's contributor-guide index.
- **R18. Functional coverage.** `@critical` functional scenarios cover the
  behavior off by default, on by flag, on by machine setting, on by machine
  setting and off by flag, and carried into the resume commands niwa prints.

## Acceptance Criteria

- [ ] With no `accept_session_messages_on_dispatch` key and no flag, the launch
  command niwa builds for a Claude worker contains no inbound-acceptance
  setting, and dispatch stderr contains no audit line.
- [ ] With `accept_session_messages_on_dispatch = true` and no flag, the launch
  command carries the inbound-acceptance setting, and stderr contains exactly one
  audit line, which names the machine setting as its source.
- [ ] With the key absent and `--accept-session-messages` given, the launch
  command carries the setting, and the audit line names the flag as its source.
- [ ] With the key `true` and `--accept-session-messages=false` given, the launch
  command doesn't carry the setting, and stderr contains no audit line.
- [ ] With remote control on dispatch and this behavior both on, the launch
  command carries both the remote-control setting and the inbound-acceptance
  setting, and neither is lost when niwa builds the command.
- [ ] A workspace configuration, an instance settings file, or any file inside
  the workspace's repositories that tries to enable the behavior has no effect:
  with the machine key absent and no flag, the launch command carries no
  inbound-acceptance setting.
- [ ] For a session launched with a permission mode and with the behavior on,
  the resume command niwa prints after the dispatch, the one it prints when
  attaching fails, and the one `niwa list` prints for that session each carry
  that permission mode and the inbound-acceptance setting.
- [ ] For a session launched with the behavior off, none of those three resume
  commands carries the inbound-acceptance setting.
- [ ] In a manual check on Claude Code 2.1.267 or later, a worker dispatched with
  the behavior on receives a message from a session running in a different
  permission-mode class, and the message is delivered with no approval prompt.
- [ ] In the same manual check, a session launched with a permission mode and the
  behavior on, then resumed with the command `niwa list` prints for it, receives
  a message from a freshly dispatched peer with no approval prompt.
- [ ] In the same manual check, a session reached through the attach
  `niwa dispatch` runs receives a message from a freshly dispatched peer with no
  approval prompt.
- [ ] In the same manual check, with the behavior off, a session launched with a
  permission mode and resumed with the command `niwa list` prints receives a
  message from a sibling launched with the same permission mode, with no approval
  prompt.
- [ ] A session dispatched with the behavior in effect is marked in `niwa list`
  and reports the field as `true` in `niwa list --json`; a session dispatched
  without it carries no marker and reports `false`.
- [ ] On a machine with no marker file, the first dispatch where the behavior
  takes effect prints the one-time explanation and creates the marker; a second
  such dispatch doesn't print it.
- [ ] The one-time explanation names the `/config` row, names the
  `crossSessionInbound` key, states that the change applies to every Claude Code
  session the developer runs, states that it applies to messages from any
  session able to reach theirs, and states that niwa won't show it again.
- [ ] When niwa's configuration directory isn't writable, a dispatch with the
  behavior in effect still succeeds and prints the explanation, and the next
  such dispatch prints it again.
- [ ] `config.toml` is byte-for-byte unchanged by a dispatch that prints the
  one-time explanation.
- [ ] A dispatch with the behavior on, run with `HOME` pointed at a directory
  holding a `.claude/settings.json`, leaves that file's contents and modification
  time unchanged.
- [ ] Dispatching a Codex worker with `--accept-session-messages` prints a
  warning that the flag doesn't apply to the agent and was ignored, launches
  without the setting, prints no audit line and no one-time explanation, creates
  no marker, and records the behavior as not in effect.
- [ ] Dispatching a Codex worker with only
  `accept_session_messages_on_dispatch = true` prints no warning, no audit line
  and no one-time explanation, and launches without the setting.
- [ ] A review session `niwa watch` launches or resumes on a machine where
  `accept_session_messages_on_dispatch = true` doesn't carry the
  inbound-acceptance setting.
- [ ] With an unreadable `config.toml`, a dispatch proceeds with the behavior
  off and prints no audit line.
- [ ] Apart from the resume command, the stdout of a dispatch with the behavior
  on contains the same lines as the stdout of the same dispatch with it off.
- [ ] A user guide covers every item R17 lists, and the repository's
  contributor-guide index links to it.
- [ ] `@critical` functional scenarios exist for each case R18 lists.

## Out of Scope

- **Changing or checking the developer's personal Claude Code settings.** niwa
  states the step for the developer's own interactive session; the developer
  takes it. The setting applies to every Claude Code session they run and isn't
  niwa's to own.
- **Limiting acceptance to peers in one workspace or on one machine.** Messages
  are addressed by session name across a developer's whole account, niwa sits
  nowhere in that delivery path, and so it can't enforce such a boundary.
  Offering one would promise protection that doesn't exist.
- **Granting a session anything beyond receipt of a message.** Whatever a
  message asks a session to do still goes through that session's own
  permissions.
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

- **Routes back that niwa doesn't provide are out of its reach.** A developer
  typing their own resume command, Agent View reopening a session, and Claude
  Code restarting a worker on its own all bring a session back without niwa.
  Whether Claude Code keeps a session's launch settings on those routes hasn't
  been established. Where it doesn't, a session brought back that way loses both
  its permission mode and this behavior, and only the developer's personal
  settings reach it.
- **Acceptance isn't scoped by sender.** A session that accepts peer messages
  accepts them from any session able to address it by name, and that set spans
  the developer's whole account: sessions on other machines and in the cloud,
  including ones unrelated to the workspace. A single account was observed with
  116 addressable sessions across unrelated projects.
- **It removes the one human checkpoint on incoming peer text.** Pre-approving
  delivery grants no authority by itself, but for workspaces that run dispatched
  sessions with permission prompts off, nothing downstream asks again before the
  receiving session acts on what the message says. That's why the behavior is
  off by default, and why dispatch containment is the natural follow-up.
- **The developer's own interactive session stays governed by their personal
  settings.** niwa can describe the step for that direction; it can't take it or
  confirm it was taken.
- **"Once" means once per machine, not once per opt-in.** A developer who turns
  the behavior off and back on months later won't see the explanation again. The
  audit line, which prints on every dispatch where the behavior takes effect,
  points to where the explanation lives.

## Decisions and Trade-offs

- **Every way back niwa provides carries what the launch carried.**
  Alternatives: fix only the permission-mode gap, or carry only the new
  behavior. Both were rejected because each launch grant niwa passes lives only
  as long as the process it was passed to, and a grant carried on launch but not
  on the way back is exactly how the 2026-09-09 recurrence reached users. The
  rule covers the existing permission mode and the new behavior alike, and it's
  why the session record has to store both.
- **Re-entry requirements are stated as outcomes, and limited to the ways back
  niwa provides.** Alternative: state them purely in terms of what the resume
  commands contain. Rejected as insufficient on its own, because the one way back
  niwa runs itself appears to connect to the running worker rather than start a
  new process, so a command-contents check could pass while delivery still fails;
  the acceptance criteria therefore check delivery as well. Routes niwa has no
  code on are recorded under Known Limitations rather than promised.
- **The behavior is off by default.** Alternative: on by default, on the ground
  that a single developer dispatching their own sessions on their own machine
  gains little from the prompt. Rejected because the population this applies to
  is the one where every other gate is already open: unattended sessions, often
  running with prompts off, with no dispatch containment, where a message can
  also wake a finished session in an instance still holding its credentials. The
  prompt is friction for an attended interactive session and a real checkpoint
  for these.
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
  the BRIEF's naming question.
- **No workspace-level default.** Alternative: a workspace-level rung between
  the flag and the machine setting, as keep-alive has. Rejected because a file a
  repository carries can be carried onto machines and into sessions the developer
  never chose to grant, and because Claude Code ignores this particular setting
  when it comes from a project's own settings file anyway. Only the developer's
  machine configuration and the dispatch command can turn it on. This closes the
  BRIEF's question about who can turn the behavior on.
- **Agents that can't receive it: warn only when the flag asked.** Alternatives:
  always warn, as keep-alive does, or never warn, as remote control does.
  Keep-alive's warning names its flag even when the machine setting turned it
  on, so a machine-wide preference produces a warning on every dispatch of an
  agent it can't reach, which trains developers to ignore the line. Never warning
  would leave a developer who explicitly asked for the behavior believing it
  applied. Warning on an explicit request, and staying silent when a machine-wide
  preference simply doesn't apply, keeps the line meaningful. This closes the
  BRIEF's question about such agents.
- **"Once" is per machine, on the first dispatch where the behavior takes
  effect.** Alternatives: print on every such dispatch, rejected because a
  multi-sentence explanation repeated through a fan-out stops being read, and no
  dispatch notice today repeats advice about something outside niwa; print when a
  setter command runs, rejected because the sibling keys have no setter and
  developers who edit the file or use the flag would never see it; record "seen"
  inside `config.toml`, rejected because rewriting that file strips the
  developer's comments and parallel dispatches would race on it.
- **The audit record lives in the session record and `niwa list`, not only on
  stderr.** Alternative: the stderr line alone. Rejected because detached and
  scripted dispatches routinely discard stderr, and the stated need is to tell
  afterwards which sessions accepted messages unattended. niwa already records
  keep-alive this way.
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
