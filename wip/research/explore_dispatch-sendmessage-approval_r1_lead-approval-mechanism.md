# Lead: Why SendMessage requires approval under bypassPermissions, and which side is prompted

Round 1. Claude Code 2.1.267. Investigated 2026-09-09.

## Findings

### Summary of the mechanism (both sub-questions at once)

The premise in the exploration scope is half right and half inverted, and the
inverted half is the important one.

There are two independent approval mechanisms on the cross-session messaging
path. Only one of them is firing here, and it is a RECEIVER-side gate whose
trigger is bypassPermissions itself. bypassPermissions is not a mode that
fails to suppress the prompt. Under the default rule it is the thing that
CAUSES the prompt.

The two mechanisms:

1. Inbound message hold. Prompts the RECEIVING session. Triggered by a
   permission-mode class mismatch between sender and receiver. Governed by the
   `crossSessionInbound` setting, whose default is derived per message from
   both sessions' permission modes.

2. Cross-machine send gate. Prompts the SENDING session. Triggered when the
   target session is on another machine. Governed by the `isolatePeerMachines`
   setting, which defaults to off and is opt-in.

Mechanism 1 is almost certainly what niwa is hitting. Mechanism 2 is off by
default and only applies to messages that leave the machine.

---

### Sub-question 1: Why does SendMessage require approval under bypassPermissions, and what governs that decision?

VERIFIED (official docs). The controlling documentation is
https://code.claude.com/docs/en/cross-session-messaging , section "Control
inbound messages". It states that when no `crossSessionInbound` value applies,
Claude Code "decides per message from the two sessions' permission modes". It
sorts every session into one of two classes:

- Bypassing class: `bypassPermissions`; also `plan` mode when bypass
  permissions are available in that session.
- Prompting class: everything else, namely `default`, `auto`, `acceptEdits`,
  `dontAsk`, and `plan` when bypass is not available.

The default rule, quoted from that page:

    * The receiving session prompts for permissions: Claude Code delivers
      each message. It holds one for your approval only when the sending
      session identifies itself as bypassing permission prompts.
    * The receiving session bypasses permission prompts: Claude Code holds
      each message for your approval. It delivers one only when the sending
      session identifies itself as also bypassing.

So the default holds a message in exactly two situations, and both are
mismatches:

- receiver prompting, sender bypassing, so held
- receiver bypassing, sender prompting or unknown, so held

And it delivers with no prompt when the two sides MATCH: both bypassing, or
both prompting.

This is why `permissions.defaultMode: bypassPermissions` does not fix it, and
it is NOT because the carve-out ignores permission mode. The default rule
reads the permission mode of BOTH sessions and holds on disagreement. A
dispatched worker running bypassPermissions that receives a message from a
session that is not in the bypassing class gets the message held. Setting
bypassPermissions on one side alone moves that side into the bypassing class
and can CREATE the mismatch rather than remove it.

The governing setting is `crossSessionInbound`, with values `accept`, `hold`,
and `refuse` (docs:
https://code.claude.com/docs/en/settings-reference#crosssessioninbound ).
When any value applies, it wins over the mode-derived default entirely.
`accept` delivers every inbound peer message with no dialog.

VERIFIED (local config). Neither the user's `~/.claude/settings.json`, nor
`~/.claude/settings.local.json`, nor the instance's materialized
`.claude/settings.json` sets `crossSessionInbound`. So the mode-derived
default is what is in force today. There is no explicit setting to blame.

Also verified locally and directly relevant: the user's USER-scope
`~/.claude/settings.json` sets `permissions.defaultMode` to `plan`, while the
instance-scope `.claude/settings.json` sets `permissions.defaultMode` to
`bypassPermissions`. Those two scopes put differently-rooted sessions into
DIFFERENT permission classes, which is precisely the input the default rule
keys on. A session that resolves to the user-scope `plan` default without
bypass availability lands in the prompting class; a dispatched worker under
the instance file lands in the bypassing class. That is a mismatch, and a
mismatch is a hold.

#### The bypass-immunity claim, checked properly

The exploration scope asked whether this is "a deliberate carve-out that
intentionally ignores permission mode". For mechanism 1 the answer is NO. It
reads permission mode, it does not ignore it.

For mechanism 2 the answer is YES, and it is genuinely bypass-immune.
Verified against a local agent source reference: the send path classifies a
refusal to auto-approve a cross-machine peer message as a safety check rather
than a mode decision, and the permission pipeline returns safety-check "ask"
results BEFORE it reaches the step that grants bypassPermissions. The same
pipeline also excludes non-classifier-approvable safety checks from auto
mode's allowlist and from its classifier. So a safety-check ask cannot be
cleared by permission mode, by an allow rule, or by a PreToolUse hook
returning allow.

The public docs corroborate this for the shipped build. `isolatePeerMachines`
"asks for your approval before Claude's message to a session beyond this
machine leaves, even in bypassPermissions mode", and "a true from any settings
scope applies, so a checked-in project file can turn the requirement on but
not off."

But mechanism 2 is opt-in and defaults to off, and it only fires for targets
beyond this machine. Unless something in the environment sets
`isolatePeerMachines` to true, it is not the cause here. This should be
checked before the exploration proceeds (see Open Questions). It is cheap to
rule out and expensive to have wrong, because if it WERE the cause, no
settings change from niwa could clear it.

#### Precedence, the constraint that matters most for a fix

VERIFIED (docs). `crossSessionInbound` is one of a small set of keys with
INVERTED precedence. From https://code.claude.com/docs/en/settings , the
exceptions table says of this key: "A stricter value from
`.claude/settings.json` or `.claude/settings.local.json`, on the accept then
hold then refuse ladder" is "Honored over managed, `--settings`, and user
values; a project or local value that isn't stricter is ignored".

Read that carefully. A project-scope `.claude/settings.json` can only ever
TIGHTEN this key. `accept` is the loosest rung on the ladder, so
`crossSessionInbound: "accept"` written into a project `.claude/settings.json`
is IGNORED.

This is directly load-bearing for niwa, because niwa's standard configuration
mechanism materializes exactly that file. niwa's usual config-materialization
path structurally cannot grant this. The fix has to come from a scope that can
set the loose value: `--settings` on the worker launch, user settings, or
managed settings.

The docs name the intended pattern for precisely this case:

    To let a -p worker take messages unattended, start it with
    crossSessionInbound set to accept in its --settings value. An accept in
    your user settings also works but applies to every session you run.

VERIFIED (niwa source, public repo). niwa already owns this lever.
`internal/cli/dispatch_remotecontrol.go` builds an inline settings document
and injects it via `claude --settings` to start a dispatched worker with the
Remote Control bridge on, appended to the dispatch argv as a single discrete
element. The comments in that file already record that `claude --settings`
outranks the project `settings.json`. So the injection point exists and its
precedence behavior is already understood in-tree.

---

### Sub-question 2: Which side of the exchange gets prompted?

VERIFIED, and this is the answer the exploration most needed: the RECEIVING
session is prompted.

The docs are unambiguous. From "Control inbound messages":

    When the default holds a message, Claude Code opens an approval dialog in
    the receiving session. The dialog shows the sender and a preview.

Supporting detail from the same page, all pointing the same way:

- "The receiving session checks each arriving message against its own inbound
  controls", ending in Delivered, Held, or Refused.
- "Held: Claude Code sets the message aside undelivered. A held message
  reaches Claude only when you approve it or a later mode or settings change
  allows it."
- The sender only gets NOTICES, not a prompt: "When the sender is an
  interactive session on the same machine, Claude Code shows a notice there
  when the receiver holds the message, and a follow-up when the receiver later
  delivers, denies, or expires it."

So the user-visible report of "approval to deliver a message" maps onto the
receiver's hold dialog, not a sender-side permission prompt. The phrasing "to
deliver" is literally accurate. It is a delivery gate, not a send gate.

The scope's stated worry is CONFIRMED: configuring only the dispatched sending
session cannot fix this. The setting that matters, `crossSessionInbound`, is
read by the RECEIVER. Any fix must reach the receiving end of the exchange.

Since niwa-dispatched sessions message each other, every dispatched worker is
both a sender and a receiver, so injecting `crossSessionInbound: "accept"`
into every dispatched worker covers worker-to-worker traffic. It does NOT
cover a message sent into a session niwa did not launch, notably the user's
own interactive root session, whose inbound controls come from
`~/.claude/settings.json`.

#### Dialog behavior in dispatched (background) sessions

Relevant because niwa's workers are background sessions surfaced in Agent View:

- A `-p` session cannot show the dialog at all. A default-held message there is
  kept for the `dialogExpiry` deadline (default five minutes) and then dropped
  and reported to the sender as expired.
- For a background session: "While no terminal is attached to a background
  session, Claude Code leaves the dialog open past the deadline. After you
  attach, if the dialog stays unanswered for a full deadline period, Claude
  Code closes it and drops the message."

That matches the reported symptom of a human approval prompt appearing rather
than messages silently vanishing, and it is consistent with these being
attachable background sessions rather than pure `-p` workers.

`dialogExpiry` accepts the value `never` to keep default-held messages until
the session ends. Note the asymmetry: a message held by an EXPLICIT `hold`
setting never expires, whereas a DEFAULT-held message does.

---

### Other verified facts worth carrying forward

- Feature age. Cross-session messaging, `crossSessionInbound`, and
  `dialogExpiry` all landed in 2.1.224 (changelog). The 2.1.224 entry states
  the behavior plainly: "Added crossSessionInbound and dialogExpiry settings:
  cross-session messages sent to a session running with bypassed permissions
  are held for your approval, and messages to other sessions auto-deliver."
  So the receiver-side hold is NOT new in 2.1.267. It is over forty releases
  old.

- Nothing in 2.1.267, 2.1.266, or 2.1.265 touches this path. I read the full
  2.1.267 entry and grepped the whole changelog for SendMessage, ListAgents,
  cross-session, crossSessionInbound, isolatePeerMachines, and permission
  class. The nearest recent entries are 2.1.261 ("Fixed Remote Control showing
  stale permission modes across attached sessions") and 2.1.267 ("Fixed Remote
  Control clients that join a Claude Desktop or VS Code session showing a
  stale permission mode until it was changed again"). Both are about stale
  permission-mode reporting, which is suggestive given that the rule keys on
  permission mode, but neither names messaging. I am NOT claiming either is
  the cause.

- Own-child messages are a separate carve-out. When no `crossSessionInbound`
  applies, Claude Code delivers a message it can verify came from the
  session's own child processes, such as a hook or Bash command posting to its
  own socket. When it can verify neither by process evidence nor by the
  `CLAUDE_CODE_MESSAGING_TOKEN` auth line, "it treats the message like any
  other that asserts no permission class, so a session that bypasses
  permission prompts holds it for your approval." A sender that asserts no
  class is treated as NOT bypassing, so a bypassing receiver holds it. This is
  a second, non-obvious route to the same hold.

- Transport. Same-machine messages travel over a per-session Unix domain
  socket and never touch Anthropic servers. The socket path is exported as
  `CLAUDE_CODE_MESSAGING_SOCKET` before any hook runs, including SessionStart,
  and shows in `/status` as the "Peer address" row prefixed with `uds:`. Only
  cross-machine and cloud targets route through Anthropic servers.

- Diagnostics available. `/list-agents` (alias `/peers`) lists reachable
  sessions; `/status` shows the session's own peer address. The docs' branch
  for "list-agents works but a send didn't arrive" names "the receiving
  session's inbound controls can hold or drop what you send it" as a cause, so
  there is a ready-made confirmation path.

---

## Implications

1. The exploration's dead-on-arrival worry is real, but it points at a
   different target than expected. Most obvious fixes ARE dead, but not
   because a carve-out ignores permission mode. They are dead because the gate
   is on the RECEIVER, and because the one setting that opens it cannot be set
   from a project `.claude/settings.json`, which is niwa's normal lever.

2. `--permission-mode` forwarding cannot fix this, and may be making it worse.
   `niwa dispatch` deriving `--permission-mode bypassPermissions` moves the
   worker into the bypassing class. Under the default rule a bypassing
   receiver holds every message from a non-bypassing sender. Tuning the
   permission mode is tuning the TRIGGER, not the GATE.

3. The concrete fix shape is to inject `crossSessionInbound: "accept"` via the
   existing `claude --settings` path in
   `internal/cli/dispatch_remotecontrol.go`. That file already builds an
   inline settings JSON document and appends it to the dispatch argv, and its
   comments already reason about `--settings` outranking the project
   `settings.json`. This is a small extension of an existing, already-designed
   mechanism rather than a new one. The design question is whether to widen
   that helper or add a sibling that composes several injected keys into one
   document, since `--settings` is a single argv element and both keys would
   have to share it.

4. Do not put `crossSessionInbound` in the materializer. Anything niwa writes
   into an instance's `.claude/settings.json` can only tighten this key.
   Writing `accept` there would be silently ignored, which is the worst
   failure mode: it looks configured and does nothing. This deserves a comment
   in the materializer so a future change does not helpfully move it there.

5. Worker-to-worker is fixable by niwa; worker-to-root is not, fully.
   Injecting into every dispatched worker covers messages between dispatched
   sessions, and also covers a message from the user's own root session into a
   worker, because the worker is the receiver. But a message from a worker
   INTO the root session is gated by the root session's own inbound controls,
   which come from `~/.claude/settings.json`, outside niwa's dispatch argv. If
   the exploration wants that direction to work unattended, it needs a
   documented user-settings step, not a code change.

6. `dialogExpiry` is a secondary lever, not a fix. It changes how long a held
   message waits, not whether it is held. Setting it to `never` only helps
   default-held messages and would leave workers blocked indefinitely rather
   than proceeding, which is probably worse for a dispatch workflow than a
   five-minute drop.

## Surprises

- bypassPermissions is the TRIGGER, not the failed cure. This is the single
  most counterintuitive fact here. The scope framed it as "the approval fires
  even though we set bypassPermissions". The accurate framing is "the approval
  fires BECAUSE one side is bypassing and the other is not." Symmetry matters,
  not permissiveness.

- Inverted precedence. `crossSessionInbound` is one of a handful of keys where
  project-scope settings can only tighten, and a looser project value is
  silently ignored. This inverts the normal mental model of settings
  precedence and defeats niwa's primary configuration mechanism for this key
  specifically.

- The behavior is roughly 43 releases old, not new. It shipped in 2.1.224. The
  report that it "did not happen before today" is therefore NOT explained by a
  Claude Code behavior change, and I could not find one. Something else
  changed. See Open Questions. This gap is unresolved and I am flagging it
  rather than papering over it.

- A sender that asserts no permission class is treated as not-bypassing, which
  trips the hold on a bypassing receiver. Unverifiable senders fail closed.

- The sender-side gate is genuinely bypass-immune and cannot be cleared by
  permission mode, allow rules, or a PreToolUse hook returning allow. It is
  just not on by default. If it ever gets switched on, no niwa-side settings
  change will clear it.

## Open Questions

1. What actually changed today? The mechanism is old; the symptom is new.
   Candidate explanations, none verified:
   - `remote_control_on_dispatch` was recently enabled for this host. niwa has
     a PRD and a guide for remote-control-by-default. If workers are now
     started with the Remote Control bridge on, peer discovery and addressing
     change, and a target may resolve to a cross-machine address rather than a
     local socket, which is mechanism 2's territory.
   - A niwa change altered the sender's or receiver's resolved permission mode,
     for example which settings file a worktree-rooted session resolves
     against, flipping a previously-matched pair into a mismatch.
   - A Claude Code upgrade crossed the 2.1.224 boundary on this machine. Worth
     confirming which version was installed yesterday.

   This is the highest-value next question, because the fix differs by cause.
   Round 2 should establish it before committing to a design.

2. Is `isolatePeerMachines` set anywhere? It is not present in the two user
   settings files or the instance settings file I read, and it defaults to
   off. But managed settings were not inspected. If it is on, mechanism 2
   applies, it is bypass-immune, and it can be turned on but not off from a
   project file, which would genuinely be dead-on-arrival for a niwa-side fix.
   Cheap to check, high cost if wrong.

3. Which permission mode does each side actually resolve to at runtime? I
   established that user scope says `plan` and instance scope says
   `bypassPermissions`, and that the rule keys on the resulting class. I did
   NOT verify which file a given dispatched worker or worktree-rooted session
   resolves against, nor whether `plan` in these sessions has bypass
   available, which would place it in the bypassing class and change the
   analysis. Confirm empirically with `/status` and `/list-agents` in both a
   dispatched worker and the root session.

4. Are dispatched workers `-p` sessions or attachable background sessions? The
   dialog behavior, and whether the message is dropped after five minutes or
   waits for attach, differ materially. The reported symptom suggests
   attachable, but this was not verified against the dispatch launch path.

5. Does the injected `--settings` document compose or replace? niwa currently
   injects a single-key document for remote control. Whether a second key can
   be added to that same document without disturbing the remote-control
   default-fill logic, which deliberately injects nothing when downstream
   decided the value, needs a design decision. The two keys have different
   injection conditions but must share one argv element.

6. Is there a hook-based alternative? Not investigated. Given that the gate is
   a delivery decision inside the receiver rather than a tool-permission
   decision, a PreToolUse hook is very unlikely to reach it, and for the
   safety-check path a hook returning allow is explicitly overridden. Low
   priority, but worth one line of confirmation before anyone proposes it.

## Summary

The approval is a receiver-side delivery hold, not a sender-side permission prompt: when no `crossSessionInbound` value applies, Claude Code compares the sender's and receiver's permission-mode classes and holds the message whenever they disagree, so bypassPermissions on a dispatched worker is the trigger for the hold rather than a failed cure, and the dialog opens in the receiving session.
The fix must therefore reach the receiving end, and must arrive via `claude --settings` (which niwa already uses for remote control) rather than the materialized instance `.claude/settings.json`, because `crossSessionInbound` has inverted precedence in which a project-scope file can only tighten the accept-hold-refuse ladder and a looser project value is silently ignored.
The biggest unresolved gap is why this started today, since the mechanism shipped in 2.1.224 and no changelog entry near 2.1.267 touches it, so the cause is most likely a niwa-side change such as remote-control-on-dispatch rather than a Claude Code behavior change.
