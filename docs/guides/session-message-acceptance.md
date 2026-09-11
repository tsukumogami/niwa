# Session-message acceptance

Claude Code normally holds a message sent from one session to another when the
two run in different permission-mode classes, and waits for a person to approve
it. That checkpoint is useful at a keyboard and useless in a fan-out: a
coordinator dispatches five workers with `niwa dispatch`, sends each one a
follow-up, and every follow-up sits in a session nobody is watching.

Session-message acceptance removes that checkpoint for workers niwa launches.
With it on, `niwa dispatch` starts the worker with
`"crossSessionInbound": "accept"` in its launch settings, and messages from
other sessions reach it without a prompt.

It is off by default. A dispatch that doesn't opt in is byte-identical to
before. Turning it on is a real grant, not a convenience toggle: read
[Who can send](#who-can-send) before you turn it on for a machine.

## Opting in

### The flag and the machine setting

Two surfaces, resolved as flag, then machine setting, then off:

1. **Per dispatch**: `niwa dispatch --accept-session-messages <prompt>`. The
   flag is tri-state — bare `--accept-session-messages` forces on and
   `--accept-session-messages=false` forces off, overriding the machine setting
   in either direction. Its help text reads "accept messages from other Claude
   Code sessions without an approval prompt; overrides the [global]
   accept_session_messages_on_dispatch machine setting in either direction".
2. **Per machine**: `accept_session_messages_on_dispatch = true` under
   `[global]` in `~/.config/niwa/config.toml` (or
   `$XDG_CONFIG_HOME/niwa/config.toml` when `XDG_CONFIG_HOME` is set).

There is no `niwa config set` for the key. niwa's other machine-level dispatch
preferences are set by editing `config.toml`, and a setter that rewrote the file
would strip whatever comment you left beside a security-relevant key. Edit it by
hand, inside the `[global]` table that's already there — a second `[global]`
header makes the file invalid.

A value that isn't a boolean (`"yes"`, `1`) makes the whole `[global]` table
unreadable, and niwa treats an unreadable machine configuration as an absent
setting. The behavior stays off, silently, and so do `remote_control_on_dispatch`,
`keep_alive_on_dispatch`, and every other `[global]` key. Nothing warns you.

### What can't turn it on

No workspace config key, no instance setting, no `[claude.settings]` entry, and
no settings file a cloned repository carries is read for this. Whether a worker
takes unattended messages from other sessions is your call, not a repository's.

Two indirect routes sit outside that guarantee:

- **A relocated configuration root.** niwa finds its machine configuration
  through `XDG_CONFIG_HOME` and `HOME`. A workspace can set either in its
  `[claude.env]` or `[session.env]` tables, and an overlay can through its
  `[env]`. Either changes which `config.toml` a `niwa dispatch` run from inside
  those sessions reads. Relocating `HOME` also moves the Claude Code user
  settings a nested session reads, which can grant acceptance with no niwa
  involvement at all — so no audit line and no record. This affects every
  machine-level dispatch preference, not just this one, and a workspace config
  able to do it can already install hooks that run in the session.
- **Any agent can pass the flag.** A worker steered by a message it received can
  dispatch more workers with the behavior on, and with the machine key on it
  doesn't even need the flag.

### Recommended posture

Turn the machine setting on only where every session sharing your Claude Code
account is one you'd let direct a worker running with permission prompts off.
When only some fan-outs need it, prefer the per-dispatch flag and leave the
machine key alone.

### Agents that can't receive it

`--accept-session-messages` applies to Claude. Codex has no setting for
accepting messages from other sessions, so a Codex dispatch that passes the flag
prints

```
niwa dispatch: --accept-session-messages does not apply to the "codex" agent and was ignored. Codex has no setting for accepting messages from other sessions.
```

and continues without it. The machine setting alone prints nothing for such an
agent. Either way the behavior doesn't take effect, and the instance's record
stays `false`.

### Your own interactive sessions

This setting governs what a *dispatched worker* accepts. Your own interactive
Claude Code sessions are governed by your Claude Code user settings, which niwa
never reads or writes. To accept there too, set "Messages from your other
sessions" to accept in Claude Code's `/config`, or add
`"crossSessionInbound": "accept"` to `~/.claude/settings.json`.

Know what that costs before you do it. The change applies to **every** Claude
Code session you run, and to messages from **any** session able to reach yours —
on this machine or elsewhere. niwa never makes it for you.

## How it works

### What niwa puts in the launch

When the behavior resolves on and the dispatched agent can receive it,
`niwa dispatch` puts `crossSessionInbound: "accept"` into the single launch
settings document it passes as `--settings`, alongside `remoteControlAtStartup`
when remote control also injects:

```
claude --bg --permission-mode bypassPermissions --name worker-1 \
  --settings '{"crossSessionInbound":"accept","remoteControlAtStartup":true}' -- <prompt>
```

niwa builds that document from constant keys and values, marshals it with
`encoding/json`, and passes it as one argv element with no shell involved.
Nothing from a workspace, repository, or prompt reaches it, and every Claude
launch puts `--` before the prompt — so a prompt that begins with `--settings=`
is read as prompt text, not as a second flag that would replace niwa's document.

For a worker running with permission prompts off, `accept` lifts four holds, not
one: the hold on a sender in a different permission-mode class, the hold on a
sender that doesn't attest its permission mode, the default hold such a receiver
applies, and the hold on remote-routine deliveries.

### Who can send

A worker that accepts takes messages from any session able to address it by
name. That set covers your whole Claude Code account, including sessions on
other machines and in the cloud. niwa sits nowhere in the delivery path, so it
can't narrow it.

On the local machine the set is wider still. Delivery between two sessions on one
host runs over a per-session unix-domain socket, and that socket takes a
connection from **any local process running as you**, with no token. This was
measured, not assumed: a plain Python script opened an accepting worker's socket
and wrote one message, and the text landed in that worker's conversation
unprompted. So the sender set is every local process running as you, not only
sessions on your account.

Worker names are predictable, because niwa uses the dispatch name as the
session's display name.

The realistic threat is prompt-injection laundering: a session reads untrusted
content — a web page, an issue, a third-party repository — is told to message a
worker, and the worker carries the instruction out with its full authority.
That authority includes shell access, write access outside its instance, the
credentials in its environment, git push rights, and unrestricted network
access. Keep-alive can wake an idle worker to act on such a message.

### It's inbound only

This settles what the worker **accepts**. It says nothing about what happens
when the worker speaks first. A message the worker sends into a session launched
without the same setting — a coordinator dispatched earlier, one dispatched with
the behavior off, one another tool started, or your own interactive session —
still waits for approval there when the two run in different permission modes.
Dispatching that session again with the behavior on clears it.

### Off doesn't isolate a worker

`--accept-session-messages=false` keeps Claude Code's default, which still
delivers messages between sessions of the same class without a hold. Two workers
dispatched from the same bypass-mode workspace reach each other whether or not
this behavior is on. A mode that holds everything would be a different feature.

### The audit line and the override line

When the behavior takes effect, `niwa dispatch` writes one line to stderr once
the session record is durable:

```
niwa dispatch: this worker accepts messages from other sessions without asking (source: machine setting accept_session_messages_on_dispatch); see https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md
```

When the flag was what turned it on, the parenthetical reads
`(source: --accept-session-messages)` instead.

When `--accept-session-messages=false` turned off a machine setting that would
have applied, the line is

```
niwa dispatch: this worker keeps Claude Code's default for messages from other sessions (--accept-session-messages=false overrides the machine setting)
```

Neither line is a preventive control. When an agent does the dispatching, the
audit line lands in that agent's output rather than in front of a person, which
is why the record in `niwa list` exists.

### The one-time explanation and its marker

The first time the behavior takes effect, niwa also writes an explanation to
stderr, because the inbound-only asymmetry is the thing you'd otherwise discover
the hard way:

```
niwa dispatch: note: accepting messages without asking is inbound only. A message this worker sends into a session launched without it, such as a coordinator dispatched earlier, one dispatched with the behavior off, or one another tool started, still waits for approval there when the two run in different permission modes; dispatching that session again with the behavior on clears it. Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change. To accept there too, set "Messages from your other sessions" to accept in Claude Code's /config, or add "crossSessionInbound": "accept" to ~/.claude/settings.json. That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere. niwa won't show this again; it's also at https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md
```

The paragraph prints wherever the behavior takes effect. What a terminal decides
is only whether niwa remembers having shown it. When stderr isn't a terminal, or
there's no configuration directory to remember it in, the closing sentence is
this one instead, with the same URL:

```
niwa will show this again until it's been shown at a terminal; it's also at https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md
```

niwa remembers with an empty file named `accept-session-messages-notice` in the
directory holding `config.toml` — so it follows `XDG_CONFIG_HOME` the same way
the key does. The name is the whole record; niwa never rewrites `config.toml` to
note something it printed. Delete the file and the explanation shows again;
create it by hand and it never does.

Two consequences worth knowing. The marker is written only when stderr is a
terminal, so if you only ever dispatch through agents you'll keep seeing the
paragraph until you run one dispatch yourself at a terminal. And the terminal
closing sentence is printed *before* the write is attempted — if the
configuration directory can't be written, niwa says it won't show this again and
then does. That's deliberate: a repeated paragraph is the safe direction for
that promise to be wrong in.

### Restarts, stops, and resumes

Claude Code saves a session's launch settings and reapplies them when it
restarts or reopens the session. That has four consequences:

- `claude respawn <worker>` and `claude attach <worker>` keep the behavior. A
  message delivered before the restart is still delivered after it.
- Turning the machine setting off doesn't reach sessions already dispatched.
  Their launch settings are already saved. See
  [Withdrawing the grant](#withdrawing-the-grant).
- A worker stopped with `claude stop` receives nothing: the send itself fails,
  because a stopped background session isn't reachable by name. The message is
  neither held nor delivered. Reopen the worker and delivery resumes.
- A foreground `claude --resume` of a dispatched conversation does **not** carry
  the behavior. It's a new foreground session with your own settings, not the
  dispatched one.

### Review sessions never receive it, and can't send either

Sessions that `niwa watch` launches or resumes review untrusted changes, and
never get this behavior. They could still *send*, though, and their network
sandbox doesn't cover the tools that reach other sessions — those run on the
Claude Code process's own connection or its local inbox.

So every review session carries a PreToolUse hook, in every containment mode,
denying the Claude Code tools that reach another session:

```
SendMessage|SendFile|RemoteTrigger|ListAgents
```

That list was taken from Claude Code 2.1.267's tool list and re-checked against
2.1.268, whose advertised tools add nothing else that reaches another session. A
denied call is refused with

```
niwa watch: review sessions don't reach other sessions
```

The hook is verified by matcher *and* command before a review launches, so a
same-matcher hook from a workspace or overlay can't stand in for it, and a
dropped hook stops the launch rather than quietly widening the session. It fires
at the subagent's own tool boundary too, so a review session can't route a send
through a subagent it launches.

Its limits:

- With `watch_sandbox = off`, Bash has full access to the local inbox, so the
  hook is accident prevention only.
- In sandbox mode, Bash is kept off the local inbox by niwa's no-egress stanza,
  which grants no unix-socket allowance of any kind.
- It covers the built-in tools named above. It does not cover MCP tools.
- Hooks load when a session starts, so a review staged before this shipped runs
  without the hook until watch re-stages or resumes it. Let in-flight reviews
  re-stage before you turn the behavior on.
- A `disableAllHooks` setting in any settings source turns it off, along with
  every other review hook.

Messages *into* review sessions aren't refused; this feature doesn't change that
direction.

## Seeing which instances accept messages

### The marker and the field

`niwa list` marks the instances whose dispatched session was launched with the
behavior in force:

```
myws+worker-1a2b3c4d (accepts session messages)
myws+other-5e6f7a8b
```

With `--json`, every record carries `accepts_session_messages`, always present:

```json
[{"name":"myws+worker-1a2b3c4d","path":"...","ephemeral":true,"accepts_session_messages":true}]
```

It stays `true` after the session finishes, for as long as the instance exists,
and turning the machine setting off later doesn't change it. When keep-alive
also applies, `(keep-alive)` comes first: `<name> (keep-alive) (accepts session
messages)`.

### What the value actually means

Read it as "niwa launched this worker with the setting", not "Claude Code
confirmed it". A managed policy or a stricter project setting can still hold
messages, so the field errs toward reporting more acceptance than there is. It
also says nothing about acceptance you turned on in your own user settings, or
acceptance a relocated `HOME` brought in. Two further gaps: on the foreground
path, a capture or record-write failure leaves a kept instance reporting `false`
with no audit line even though Claude Code saved the setting for a later resume;
and an unreadable session store makes every record report `false` silently.

## Withdrawing the grant

### Why turning the key off isn't enough

Turning `accept_session_messages_on_dispatch` off stops *new* dispatches. It
does not reach sessions already launched, because Claude Code reapplies their
saved launch settings on every restart and reopen.

### Finding and closing the sessions

To actually withdraw it, find the sessions and stop them:

```
niwa list --json | jq -r '.[] | select(.accepts_session_messages) | .name'
```

Then close each one — `claude rm <id>`, or close it in the agents view. A
`claude stop` halts the process but keeps the conversation, so reopening it
brings the behavior back.

## Validation

### The manual delivery check

The manual delivery check last passed on **Claude Code 2.1.268**. It covers what
no offline test can: real delivery between two live sessions. It confirmed that
a worker dispatched with the behavior off holds a message from a prompts-on
sender and keeps holding it across `claude respawn` and across a stop-and-reopen;
that a worker dispatched with it on is delivered to, and stays that way across
both; that a send to a stopped worker fails outright; that the worker's own
message into a session launched without the behavior is held there; and that two
workers dispatched with the behavior off still reach each other, because they
share a permission-mode class.

The same session confirmed that a real `niwa watch` review session, staged with
`watch_sandbox = off`, is refused when it calls `SendMessage` or `ListAgents` —
with the message above, with no session list returned, and with nothing reaching
the target worker — and that a subagent it launches is refused the same way.

### What the automated suite covers

The functional suite
(`test/functional/features/session-message-acceptance.feature`) covers
everything up to the launch seam: resolution across the flag and the machine
setting, the rendered settings document, the audit and override lines, the
explanation and its marker, the `niwa list` record, and the agents that can't
receive the behavior. Live delivery itself isn't automatable offline.
