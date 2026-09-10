# Exploration Findings: dispatch-sendmessage-approval

## Core Question

As of today, delivering a message via the `SendMessage` tool between niwa
sessions prompts for human approval, even when the session was launched in
`bypassPermissions` mode. We want to find out what governs that prompt and
whether a dispatched session can be launched with the approval already in
place. The knob should be a per-machine configuration that is off by default,
overridable by a new flag on `niwa dispatch`.

## Round 1

### Key Insights

- **The premise inverts. This is not a tool-permission prompt.** `SendMessage`
  is documented as requiring no permission, and its own check allows
  same-machine sends unconditionally. What fires is a *receiver-side inbound
  hold*. (leads: approval-mechanism, preapproval-mechanisms)

- **`bypassPermissions` is the trigger, not the failed cure.** When no
  `crossSessionInbound` value applies, Claude Code sorts each session into a
  bypassing class (`bypassPermissions`, plus `plan` where bypass is available)
  or a prompting class (everything else) and holds the message whenever the two
  sides *disagree*. Matching pairs deliver silently. Setting bypass on one side
  alone creates the mismatch. (lead: approval-mechanism)

- **The dialog opens in the receiving session.** The receiver checks each
  arriving message against its own inbound controls, ending in Delivered,
  Held, or Refused. The sender only gets notices. Configuring only the
  dispatched sender cannot fix it. (lead: approval-mechanism)

- **`crossSessionInbound: "accept"` is the fix, and it has inverted
  precedence.** A project or local settings file can only ratchet this key
  *stricter* on the accept-hold-refuse ladder. `accept` is the loosest rung, so
  writing it into the materialized instance `.claude/settings.json` — niwa's
  normal lever — is silently ignored. Only managed settings, the `--settings`
  flag, or user settings can grant it. The docs name the `--settings` route
  explicitly for unattended `-p` workers.
  (leads: approval-mechanism, preapproval-mechanisms)

- **A malformed value fails closed and beats a good one.** A bad
  `crossSessionInbound` in any scope forces `hold` and warns, even when a
  higher-precedence source sets `accept`. Whatever niwa emits must be validated.
  (lead: preapproval-mechanisms)

- **niwa already owns the `--settings` lever, but the slot is occupied.**
  `internal/cli/dispatch_remotecontrol.go` builds an inline settings document
  and appends `--settings` plus the JSON as two discrete argv elements. The
  repo has declined a second `--settings` twice on the record
  (`DESIGN-niwa-session-keep-alive.md`, `DESIGN-niwa-watch-once-pr-review.md`).
  (leads: dispatch-launch-path, approval-mechanism)

- **Timing is not a constraint.** Every file niwa writes into an instance exists
  before the worker starts: provisioning runs synchronously at `dispatch.go:476`
  and the exec is ~200 lines later at `dispatch.go:669`.
  (lead: dispatch-launch-path)

- **The requested control shape already exists, twice.** `--keep-alive` resolves
  flag (tri-state `*bool`) > instance settings > `[global].keep_alive_on_dispatch`
  in `~/.config/niwa/config.toml` > off. That is the template, not the four-rung
  harness chain. `[global]` keys other than `default_dispatch_harness` have no
  `niwa config set` setter, so configuring it per machine means hand-editing the
  TOML unless a setter is added. (lead: machine-config-surface)

- **niwa can fix its own half only.** Injecting `accept` into every dispatched
  worker covers worker-to-worker traffic *and* messages from the user's terminal
  into a worker, because the worker is the receiver in both. A message from a
  worker into the user's interactive session is gated by
  `~/.claude/settings.json`, outside the dispatch argv.
  (leads: approval-mechanism, preapproval-mechanisms)

- **Guardrails that sound good are illusory.** Delivery is addressed by name,
  names are unauthenticated and non-unique, and delivery explicitly crosses
  machines and reaches the cloud. niwa is not in the routing path. A key
  implying a workspace, instance, or host boundary would be a false statement.
  (lead: blast-radius-guardrails)

- **Off by default is right, for a sharper reason than "messaging is risky".**
  niwa's dispatched workers are the specific population where every other gate
  is already open: derived `bypassPermissions`, no dispatch containment hooks (a
  gap `DESIGN-dispatch-permission-mode.md` already records), hook `ask`
  decisions failing open under bypass, and a send that *resumes a finished
  session* in an instance still holding vault-materialized secrets.
  (lead: blast-radius-guardrails)

- **The mesh removal is not a precedent against this.** niwa removed its
  agent-to-agent messaging layer because it was non-functional and off-identity,
  not because such messaging was judged dangerous. But the removal reserved
  "downstream replacement coordination" as gated, so a knob that makes peer
  messaging frictionless is functionally a position on the replacement and
  should say so. (lead: blast-radius-guardrails)

### Tensions

- **Hook candidate: proposed by one lead, killed by another.** The niwa-side
  lead ranked a `PreToolUse` allow-decision hook as a candidate because niwa
  already ships `watch.autoAllowHook()`. The Claude Code lead killed it on three
  independent grounds: sender-side only, no hook event covers inbound delivery,
  and `allow` is explicitly powerless against the "actions no mode
  auto-approves" class. Resolved against the hook.

- **Should the downstream (instance-settings) rung exist?** The blast-radius
  lead argues for the launch-argument channel only, so the grant cannot be
  carried by a repo or branch. The keep-alive precedent includes an
  instance-settings rung. A design decision, not a research gap.

- **`permissions.allow` is honored but useless here.** Allow rules merge across
  scopes and genuinely apply, but do not touch this prompt. They become
  load-bearing only if workers move to `dontAsk`, which one lead argues is a
  better worker mode than bypass. Out of scope.

### Gaps (closed in round 2 unless noted)

- Why this started today — **closed**: see round 2.
- Whether `--settings` layers or replaces — **closed**: deep layering, measured.
- What repeated `--settings` does — **closed**: last-wins and silent, measured.
- What is in the peer set — **closed** by a direct `ListAgents` call.
- Whether `isolatePeerMachines` is set anywhere — **closed**: nowhere, and there
  is no managed settings file on the machine.
- Which peer actually prompted — **closed**: every receiver was a background
  session.

### Decisions

Recorded in `explore_dispatch-sendmessage-approval_decisions.md`, Round 1.

### User Focus

The user needs both directions to flow unattended, including messages from a
worker into their own interactive session, and dispatched the two incidental
defects (non-unique session name, inert `defaultMode` key) as separate
explorations rather than folding them in.

## Round 2

### Key Insights

- **The peer set is account-scoped, and name collisions are already live.** A
  direct `ListAgents` call returned 116 peers, spanning projects unrelated to
  this workspace, almost all over Remote Control. Seven names appear more than
  once, one of them three times. The non-unique-name defect is observed fact. (direct observation during convergence)

- **The symptom is not new, and it has a dated root cause.** A machine-wide
  transcript scan found exactly seven hold events ever. Five fell on
  2026-09-02/03, the first 38 seconds after Claude Code 2.1.258 first ran on
  this host — the release that made `permissions.defaultMode` inert in project
  settings. niwa PR #277 repaired that via the `--permission-mode` flag, shipped
  in niwa v0.24.0, installed here at 2026-09-03T03:01Z. All five events predate
  the install. Then zero holds for a week. (lead: what-changed-today)

- **Today's recurrence is #277's fix not surviving re-entry.** The derivation
  sets `dispatchPermissionMode` in `runDispatch` only. `dispatch_reentry.go`,
  whose own header comment says every way back into a session must re-carry the
  launch grant, carries the Codex workdir grant and contains no reference to
  permission mode. A resumed peer falls out of the bypassing class while a
  freshly dispatched one stays in it. Both of today's receivers were long-lived
  coordinator sessions, one demonstrably resumed across the version boundary.
  The code gap is verified; that it is what fired today is inferred, since
  background sessions are claimed from a pre-warmed spare pool whose argv `ps`
  cannot show. (lead: what-changed-today)

- **Every receiver was a background session; the user's terminal was never
  held.** This corrects round 1's inference that the prompting peer was
  probably the user's interactive terminal. The hold's own message confirms the
  mechanism verbatim: "The sending session's permission mode class doesn't match
  this session's. Review it below, or set `crossSessionInbound` to `accept`."
  (lead: what-changed-today)

- **Four candidate causes eliminated with dated evidence.** Remote control: its
  config key is two weeks old, it has been on by default since June, and every
  hold names a local `uds:` socket rather than a bridge address.
  `isolatePeerMachines`: set nowhere, and no managed settings file exists. A new
  bypass declaration: present since the workspace's initial commit in April. A
  2.1.224-boundary upgrade: crossed long before. Not fully ruled out: something
  in 2.1.260–2.1.267, since the host upgraded 83 minutes before today's first
  hold, though the hold's wording is byte-identical to the 2.1.258 one.
  (lead: what-changed-today)

- **`--settings` deep-layers; repeated `--settings` is last-wins and silent.**
  Measured with a hook that logs its own environment. A command-line `env` or
  `permissions` object overrides only the leaf keys it names; project hooks
  still fire; command-line hooks union with project hooks. A second flag
  discarded the first document entirely, with nothing on stderr, and the same
  held when mixing the file-path and inline forms. Malformed JSON refuses to
  start. (lead: settings-flag-layering)

- **niwa should not detect the user-settings gap, and should not write it.** No
  `niwa doctor` exists, `niwa status` is instance-scoped, and no Claude Code
  command exposes a key's effective value. A false positive would push a
  machine-wide loosening on niwa's authority. niwa's documented rule for the
  one personal config it edits is "no global keys, only whole project blocks".
  The recommendation is to say it once, as a closing line on the command that
  enables the dispatch-side grant, following
  `config_default_harness.go:87-88`, with the JSON and blast radius in a guide
  section modeled on `remote-control-on-dispatch.md:52-67`. The user remedy is
  the `/config` row "Messages from your other sessions", or
  `"crossSessionInbound": "accept"` in `~/.claude/settings.json`.
  (lead: user-settings-advisory)

- **An orphaned helper duplicates #277's read.** `WorkerPermissionMode` in
  `internal/workspace/permissions.go` reads exactly what #277 reimplemented
  inline in `dispatch.go`, and nothing calls it but its own test. A re-entry
  fix should reconcile the two rather than add a third copy.
  (lead: what-changed-today)

### Tensions

- **Round 2 proposed materializing `accept` into instance settings; round 1
  proved that cannot work.** The forensics lead, not having seen round 1's
  precedence finding, proposed writing `crossSessionInbound: "accept"` into the
  instance settings on the grounds that a scope-resolved setting survives
  re-entry. Round 1 established from docs, across two independent leads, that
  project scope can only ratchet this key stricter. Resolved in favor of round
  1. But the lead's underlying point survives and generalizes: a command-line
  grant is process-scoped, and `--settings` dies on re-entry exactly as
  `--permission-mode` does. Whichever channel carries the grant, the re-entry
  path must carry it too.

- **The user wants the terminal direction; the evidence says it never broke.**
  Not a contradiction — the user's requirement is about intended capability —
  but it means the terminal half is precautionary rather than remedial, which
  matters for how its guide section is written.

### Gaps

- Whether Claude Code's `--resume` restores the launch permission mode from
  session state, which would make the re-entry gap latent rather than live. The
  code says niwa never passes it; whether Claude Code remembers it is untested.
  Cheap experiment: dispatch, resume, compare class against a fresh peer. This
  belongs to the bug fix's own acceptance test rather than to another
  exploration round.
- Whether anything in 2.1.260–2.1.267 touches the inbound-hold path. Low
  probability given the identical wording; noted, not pursued.

### Decisions

Recorded in `explore_dispatch-sendmessage-approval_decisions.md`, Round 2.

### User Focus

The user chose to ship both the re-entry bug fix and the `crossSessionInbound`
feature, sequenced with the fix first, and to keep the worker-into-terminal
direction in scope even though that path was never observed failing.

## Accumulated Understanding

The reported problem is real, is not new, and has two layers.

The surface layer is a niwa bug. Claude Code holds a peer message whenever the
sender and receiver fall into different permission-mode classes. When 2.1.258
made the project-settings copy of `defaultMode` inert, niwa's workers and their
peers fell out of step, and niwa PR #277 put them back in step by forwarding
`--permission-mode` on the launch argv. That repair lives only in the launch
path. `dispatch_reentry.go` exists precisely to re-carry launch grants into
resumed sessions and does not carry this one, so any pair where one side has
been resumed is a mismatch again. That is today's symptom. It is a small,
well-located fix, and it should reconcile the orphaned `WorkerPermissionMode`
helper rather than add another copy of the same read.

The deeper layer is the coupling itself. Delivery currently depends on two
sessions agreeing on a permission class, and that agreement has broken twice in
eight days from two different directions. The setting that removes the coupling
is `crossSessionInbound: "accept"`, read by the receiver. Its precedence is
inverted, so niwa's usual materializer channel cannot grant it; it has to arrive
through `--settings`. Measurement shows `--settings` deep-layers onto the
project file and breaks nothing niwa materializes, but that a second
`--settings` silently discards the first, so niwa must replace its single
remote-control injection with one merged settings-document builder that any
contributor can add to. And because `--settings` is command-line scope, the
re-entry path must carry that merged document too, or the feature reproduces
the surface-layer bug in a new channel.

The control surface is settled by precedent: a `*bool` in `[global]` of
`~/.config/niwa/config.toml` and a tri-state `niwa dispatch` flag, resolved the
way `--keep-alive` is, off by default, with an audit line on the dispatch output
when it engages. Whether an instance-settings rung sits between them is still an
open design choice. The key must not be named after a boundary niwa cannot
enforce, because the live peer set is 116 sessions across the user's whole
account, addressed by names that already collide.

Because the user wants both directions, one part of the deliverable is not code.
A message from a worker into the user's own interactive session is gated by
`~/.claude/settings.json`, which niwa must neither detect nor write. It gets
stated once, when the user enables the dispatch-side grant, with the honest
price attached — an `accept` there applies to every Claude Code session the user
runs and every sender the fabric can reach — and documented in a guide. The
evidence shows this path has never actually failed, so that guidance is
precautionary.

What remains open is small and belongs to implementation rather than
exploration: whether Claude Code's `--resume` restores the launch permission
mode on its own, which the bug fix's acceptance test should settle directly.

## Decision: Crystallize
