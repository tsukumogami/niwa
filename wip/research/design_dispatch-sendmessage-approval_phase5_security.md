# Security Review: dispatch-sendmessage-approval

Reviewed: `docs/designs/DESIGN-dispatch-sendmessage-approval.md` against
`docs/prds/PRD-dispatch-sendmessage-approval.md`, grounded in
`internal/cli/dispatch.go` (steps 2a, 9a-derive, 9b, 9c, 9d, 11),
`internal/cli/dispatch_remotecontrol.go`, `internal/cli/prompt.go`,
`internal/config/registry.go`, `internal/config/overlay.go`,
`internal/workspace/session_map.go`, `internal/workspace/materialize.go`,
`internal/workspace/sessionenv.go`, and `internal/watch/containment.go`.

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes

niwa itself downloads and executes nothing new. The external input this
feature matters for is the text of peer-session messages. niwa never sees that
text, but the feature's whole purpose is to let it reach a worker without a
person looking at it first. Three inputs are worth separating.

**1. Peer-session message text (prompt-injection exposure). Severity: high in
effect, accepted by design.**

Before this feature, the class-mismatch hold was an accidental but real
checkpoint. A worker running `bypassPermissions` got messages from
prompting-class senders only after a human approved them. With the behavior on,
text from any session that can address the worker by name goes straight into
the worker's context, and the worker acts on it with no permission prompts.
The PRD's Known Limitations records the reachable sender set: every session on
the developer's account that can address the worker by name, including
sessions on other machines and in the cloud. One account was observed with 116
addressable sessions.

The concrete attack is prompt-injection laundering. A session that reads
untrusted content (a web page, an issue body, a third-party repo, a PR under
review) gets injected and tells it to SendMessage a named worker. Worker names
are predictable because niwa passes the dispatch `--name` slug as the display
name (`buildDispatchPassthrough`, `flags.DisplayName`). The injected
instruction then runs with the worker's full authority: shell access, write
access to the instance and beyond it (dispatched workers have no containment),
the instance's resolved credentials (vault-resolved values land in the
materialized settings `env` block), and git push rights. Keep-alive makes it
worse, because a message can wake an idle worker that finished its task hours
ago.

Some pieces soften this, and they're real:
- It only changes delivery across classes. Same-class delivery, which is
  bypass worker to bypass worker, was already unattended before this feature.
  The feature doesn't open that path, and switching the behavior off doesn't
  close it (the PRD says so).
- The sender usually has its own checkpoint. A prompting-class sender has to
  get its SendMessage tool call approved in that session unless its own
  settings auto-allow it.
- It's off by default and needs an explicit machine key or flag.

**2. `niwa watch` review sessions as a sender. Severity: medium.**

This path is specific to this repository. Review sessions review untrusted
changes, which makes them the likeliest sessions in the workspace to be
prompt-injected. They run in a non-bypass posture (`watch.go:827`,
`ApplyReviewSettings` sets `defaultMode: "default"` in the ask posture). Today
that puts them in a different class from bypass workers, so a message from a
review session to a bypass worker is held. With the behavior on, it isn't.

The review containment doesn't cover this channel. The egress-deny hook
matches `WebFetch|WebSearch|mcp__` (`containment.go:18`), so the OS no-egress
sandbox and the egress hook don't cover cross-session messaging, which goes
through the local Claude daemon rather than the network. The auto-allow
matcher is `Bash|Read|Glob|Grep`, so in the ask posture a SendMessage from a
review session still needs operator approval at the sender. That's the one
remaining checkpoint, and it's a single approval click from an operator who
may be approving many tool calls. Past that approval, a sandboxed review
session can hand instructions to an unsandboxed bypass worker. That turns this
feature into a way around watch containment, which the PRD excludes review
sessions from receiving but doesn't consider as senders.

Mitigation: add the cross-session messaging tool to the review sessions' deny
list, either the egress-deny matcher or `permissions.deny`, so review sessions
can't send messages at all. It's a small change to `internal/watch`, and
review sessions have no legitimate reason to message dispatched workers. If it
stays out of this pull request, the design should name it as a follow-up and
the guide should state the gap.

**3. JSON document construction. Severity: none.**

The `launchSettings` builder marshals a map with `encoding/json`. Every key
and value is a compile-time constant (`crossSessionInbound`/`accept`,
`remoteControlAtStartup`/`true`). The document is appended as one discrete argv
element after `spec.Flags.Settings`, with no shell involved. Nothing author- or
workspace-supplied reaches it. `set(key string, value any)` is general enough
that a later contributor could pass a non-constant value, so a short comment
on the builder that contributors must pass constants, plus the byte-exact
tests the design already plans, is enough. `niwa dispatch` accepts no
arbitrary passthrough arguments (`cobra.MaximumNArgs(1)`, which is the prompt),
so a user-supplied `--settings` can't collide with the merged document.

**4. Config file parsing. Severity: low. There's one real gap against the
PRD's "no other source" claim.**

The new key is decoded by the existing `ParseGlobalConfig` into a `*bool`, and
a type error makes the whole file unreadable. The feature then counts the
setting as absent, which fails safe. The global overlay repo (`[global_config]`
`repo`, cloned under `.../niwa/global`) decodes into `WorkspaceOverlay`, which
has no `[global]` table, so a remote overlay can't set the key directly. The
design correctly avoids `GlobalConfigDir()`.

The gap is that the machine configuration path comes from the environment.
`GlobalConfigPath()` reads `XDG_CONFIG_HOME`, then `HOME`. A workspace's
`[claude.env]` or `[session.env]` tables are materialized into the instance's
`.claude/settings.json` `env` block (`materialize.go:905-916`), Claude Code
exports that block to the tools a session runs, and I found no deny list for
`XDG_CONFIG_HOME` or `HOME` in `internal/config` or `internal/workspace`. So a
workspace config, which can come from a remote repo, can set
`XDG_CONFIG_HOME` to a directory inside a cloned repo that carries
`niwa/config.toml` with `accept_session_messages_on_dispatch = true`. Every
`niwa dispatch` launched from inside that workspace's sessions, which is the
coordinator-dispatches-workers pattern in the PRD's first user story, would
then turn the behavior on. The marker would also be read from and written into
that directory. The PRD's acceptance fixtures test the key under
`workspace.toml` tables and in settings files, but not this indirection.

This isn't a new privilege. The same indirection already affects
`remote_control_on_dispatch`, `keep_alive_on_dispatch`,
`default_dispatch_harness` and `dispatch_model`, and a workspace config that
can install hooks into instance settings can already run code in the session.
Still, the design says no other source can turn the behavior on, and as
written that's true only of declarative config keys. Mitigation, in order of
preference:
- Document it: the guarantee covers config keys and settings files. A
  workspace that sets the session environment, or an agent that runs
  `niwa dispatch` itself, is covered separately below.
- As a follow-up that covers every machine-level key: reject `XDG_CONFIG_HOME`
  and `HOME` as keys in `[claude.env]` and `[session.env]`, the way
  `NIWA_WORKER_SPAWN_COMMAND` is already refused (`config.go:813`).
- Optionally, add an acceptance fixture that sets `XDG_CONFIG_HOME` through
  `[claude.env]` and records the result, so the behavior is pinned rather than
  assumed.

### Permission Scope

**Applies:** Yes

**1. Removing a human checkpoint for bypass-mode workers. Severity: high,
accepted and bounded by opt-in.**

The feature grants no filesystem, network or process permission. What it does
is remove the one gate between cross-class peer text and a worker that has
all of those permissions. The design's Negative consequences state this
directly, and the PRD's reason for keeping it off by default is the right one.
Two amplifiers aren't written down, and should be.

- **Self-propagation through nested dispatch.** A worker that receives an
  injected message can run `niwa dispatch --accept-session-messages` itself.
  With the machine key on, it doesn't even need the flag, because the key
  applies to every dispatch on the machine, including ones agents start. An
  injected message can therefore spawn more accepting, bypass-mode workers. The
  audit line for those dispatches goes to the dispatching agent's stderr,
  where no person reads it. `niwa list` is the only record a human will see.
- **Persistence through the machine config.** A bypass worker can edit
  `~/.config/niwa/config.toml` to set the key, or create the marker to suppress
  the explanation. Both files are in the developer's home directory, which the
  worker can write. This isn't a niwa privilege issue, since a bypass worker
  can already write anything the user can. It does mean the opt-in is only as
  strong as the least-contained session that can write the user's home
  directory, and the guide should say so.

Neither needs a design change. The first is a consequence of letting agents
dispatch, which is intended, and the second of bypass mode with no
containment, which the PRD names as the follow-up. Both belong in the Security
Considerations and the guide.

**2. Marker file creation. Severity: low. The design is sound.**

- `os.MkdirAll(dir, 0o700)` only affects directories it creates. An existing
  config directory keeps its mode, which is correct.
- `O_CREATE|O_EXCL|O_WRONLY` with mode `0o600` doesn't follow a symlink at the
  final component. POSIX requires `O_EXCL` with `O_CREAT` to fail with
  `EEXIST` when the path exists, symlink or not, dangling included. So a
  planted symlink can't redirect the create to write or truncate another file.
  The file is empty and never read for content, so there's nothing to inject.
- The existence check that runs before printing should use `os.Lstat` rather
  than `os.Stat`. With `Stat`, a dangling symlink reads as absent: the
  explanation prints, then the create fails with `EEXIST` and is ignored. That
  only means the explanation keeps printing, which isn't a security problem,
  but `Lstat` makes the check and the create agree. It's a nit.
- There's a TOCTOU window between the check and the create. The worst outcome
  is printing the explanation twice, which the PRD accepts.
- The marker only suppresses an explanation. It gates no authority, and the
  audit line prints every time whether or not the marker exists, so tampering
  with it can only hide the one-time text. That's the right design: the
  security-relevant signals, the audit line and the `niwa list` record, don't
  depend on it.
- If the marker directory is moved through `XDG_CONFIG_HOME` (see above), the
  marker lands wherever the environment points. It's still created with
  `O_EXCL`, so the only effect is suppressing the explanation.

**3. The recorded grant can differ from the effective grant. Severity: low,
and it fails safe.**

niwa records `accepts_session_messages: true` when it passed the setting. A
managed-settings policy or a stricter project or local `crossSessionInbound`
value can make Claude Code ignore that. The record would then overstate
acceptance, which is the safe direction for an audit field. The guide should
say the field means "launched with the setting", not "verified accepting".
There's no case where niwa records `false` for a worker that accepts because
of this feature. A worker whose acceptance comes from the developer's own user
settings shows `false`, and the guide should also say the field only reports
niwa's contribution.

**4. Respawn persistence. Severity: low, documented.**

The setting sits in `respawnFlags` for the job's lifetime, so turning the
machine key off doesn't reach live or stopped sessions. The PRD documents
this. For incident response, the guide should give the procedure: find the
workers with `niwa list --json` where `accepts_session_messages` is `true`, and
stop them.

### Supply Chain or Dependency Trust

**Applies:** No

The feature adds no module dependency. It uses `encoding/json`, `os` and the
existing `golang.org/x/term` terminal check already in `prompt.go`. It
downloads nothing, clones nothing new and runs no new binary. The `claude`
binary it launches is the one dispatch already resolves, with the same
provenance. The only trust it takes on is in Claude Code's semantics for
`crossSessionInbound`, deep-merged `--settings`, and saved `respawnFlags`. The
design lists that as a behavioral dependency and covers it with the manual
delivery check and the version recorded in the guide. It's a correctness
dependency, not a supply-chain one, and a semantics change there would err
toward holding messages rather than accepting more.

### Data Exposure

**Applies:** Yes, minimally for niwa's own outputs, with the main exposure
indirect.

**1. niwa's new outputs. Severity: none to low.**

- The audit, override and warning lines, and the explanation, are fixed
  strings. The only variables are the source name, the agent name and a public
  guide URL, with no paths, session ids or secrets, and they all go to stderr.
  Stdout doesn't change.
- `SessionMapping.accepts_session_messages` is one boolean in a file written
  `0o600` inside a `0o700` directory under the workspace's `.niwa/`
  (`session_map.go:135-148`), which is the protection mappings already have.
- `niwa list --json` gains an always-present boolean. It tells anyone who can
  run `niwa list` in that workspace which instances take unattended messages,
  but that's the same local user who can already read the session mappings. It
  gives such a user nothing new, and doesn't reach anyone else.
- The marker is empty.
- niwa doesn't read or write `~/.claude/` or managed settings. The PRD has a
  code-review acceptance criterion for that, which is worth keeping.

**2. Indirect exposure through the accepting worker. Severity: high in
combination with bypass mode, accepted.**

The data at risk isn't what niwa emits but what an accepting bypass worker can
reach and send out when a peer message tells it to: the instance's resolved
environment, including vault-sourced secrets materialized into the settings
`env` block; source in the cloned repos; git credentials; and anything in the
user's home directory. Dispatched workers have no network restrictions, so
exfiltration is one `curl` away. This is the same exposure as the permission
scope item seen from the data side, and the same mitigations apply: default
off, the durable record, and dispatch containment as the named follow-up. It
belongs in the Security Considerations so a reader deciding whether to turn
the key on sees the cost in terms of data.

### Sufficiency of the stated mitigations

Default off, the audit line and the `niwa list` record are detective controls,
and they're right for an opt-in that the developer chooses knowingly. They
don't prevent anything once the key is on, and they aren't claimed to. Two
weaknesses are worth stating plainly.

- In the agent-driven dispatch pattern the feature targets, the audit line goes
  to an agent, not a person. That makes `niwa list` the real audit surface, and
  the design's always-present, not-gated-on-liveness field is the right call
  for that reason.
- The explanation covers only the inbound-only limit and the developer's
  personal settings. It doesn't say that an accepting worker in bypass mode
  will act on peer text without any prompt. That's the most important thing a
  developer opting in should know. The explanation's text is fixed by the PRD,
  so this belongs in the guide's first section, and ideally in the flag's help
  text and the audit line's guide link.

## Recommended Outcome

**OPTION 2 - Document considerations.**

The architecture is sound from a security standpoint. The settings document is
built only from constants, the marker creation is symlink-safe and gates
nothing, the new records expose nothing beyond one boolean behind existing
file modes, and no declarative workspace config key can turn the behavior on.
The findings are risks the PRD already accepts that need to be written down
with their amplifiers, plus two hardening items that don't change the design's
structure:

- Deny cross-session messaging in `niwa watch` review sessions, so a sandboxed
  reviewer of untrusted changes can't hand instructions to an unsandboxed
  worker that accepts them. Recommended in this pull request because it's a
  one-line matcher or deny entry in `internal/watch/containment.go`, or else
  named as an immediate follow-up.
- Qualify the design's "no other source" statement for environment-relocated
  configuration (`XDG_CONFIG_HOME` or `HOME` set through `[claude.env]` or
  `[session.env]`), and track rejecting those keys as follow-up work covering
  every machine-level dispatch key.
- Nit: check for the marker with `os.Lstat`.

Draft Security Considerations section, ready to paste:

---

## Security Considerations

This feature doesn't give a dispatched worker any new permission. What it
removes is a checkpoint. Claude Code normally holds a message from a session in
a different permission-mode class until a person approves it, and dispatched
workers often run with `bypassPermissions` and no containment. With the
behavior on, text from another session reaches such a worker and is acted on
with no prompt at either end of the receiving side. Everything below follows
from that.

**Who can send.** A worker that accepts takes messages from any session able
to address it by name. That set covers the developer's whole account,
including sessions on other machines and in the cloud, and niwa sits nowhere in
the delivery path, so it can't narrow it. Worker names are predictable, since
niwa uses the dispatch name as the session's display name. The realistic
threat is prompt-injection laundering: a session that reads untrusted content
(a web page, an issue, a third-party repository) is told to message a worker,
and the worker carries out the instruction with its full authority. That
authority includes shell access, write access outside its instance, the
credentials resolved into its environment, git push rights, and unrestricted
network access. Keep-alive can wake an idle worker to act on such a message. The
same exposure already exists between two bypass-mode sessions, which Claude
Code delivers between without a hold whether or not this feature is on;
switching the behavior off doesn't isolate a worker from them.

**Review sessions.** Sessions that `niwa watch` launches review untrusted
changes and never receive this behavior. They can still send, though. Their
network sandbox doesn't cover cross-session messaging, which goes through the
local Claude daemon, and they run in a prompting posture, so the class
mismatch is what used to hold their messages to bypass workers. To keep this
feature from becoming a way around review containment, review sessions deny
the cross-session messaging tool outright. They have no reason to message
dispatched workers.

**Who can turn it on.** Only the `[global] accept_session_messages_on_dispatch`
key in niwa's machine configuration and the `--accept-session-messages` flag
decide the behavior. No workspace config key, instance setting, or settings
file a repository carries is read for it. The overlay repository registered
under `[global_config]` can't set it either, because its schema has no
`[global]` table. That guarantee is about configuration sources. It doesn't
cover two indirect routes, and both are documented. First, niwa finds its
machine configuration through `XDG_CONFIG_HOME` and `HOME`. A workspace that
sets either through its session environment tables changes which
`config.toml` a `niwa dispatch` run from inside its sessions reads. That
affects every machine-level dispatch preference, not just this one, and
rejecting those variable names in the session environment is tracked
separately. A workspace config that can do this can already install hooks that
run in the session, so this isn't a new capability. Second, any agent can pass
the flag. A worker that has been steered by an injected message can dispatch
more workers with the behavior on. With the machine key on, it doesn't even
need the flag. A bypass worker can also edit the machine configuration itself,
since it can write anything the user can.

**The settings document.** niwa builds the launch settings document from
constant keys and values only, marshals it with `encoding/json`, and passes it
as a single argv element. No shell is involved, and nothing from a workspace,
repository or prompt reaches it. `niwa dispatch` accepts no extra agent
arguments, so a caller can't add a second `--settings` that would replace this
one.

**The marker file.** The explanation marker is an empty file created with
exclusive-create semantics and mode `0600`, in a directory niwa creates with
mode `0700` only if it's missing. Exclusive create doesn't follow a symlink at
that path, so a planted link can't redirect the write. The marker gates no
authority. It only suppresses the one-time explanation, and the audit line
prints on every dispatch where the behavior takes effect whether or not the
marker exists. Deleting or planting it affects only whether the explanation
shows.

**What's recorded and what isn't.** The session mapping gains one boolean,
stored with the same `0600` file and `0700` directory protection mappings
already have. `niwa list` reports it for every instance, including finished
sessions. The stderr lines carry no paths, identifiers or secrets. niwa
doesn't read or write the developer's Claude Code user or managed settings.
The recorded value means niwa launched the worker with the setting. It doesn't
mean Claude Code confirmed it: a managed policy or a stricter project setting
can still hold messages, which makes the record err on the side of reporting
more acceptance than there is. The record also doesn't reflect acceptance a
developer turned on in their own user settings.

**Audit surfaces.** Default off, the per-dispatch audit line and the
`niwa list` field are detective controls, not preventive ones. When an agent
dispatches workers, the audit line lands in that agent's output rather than in
front of a person, so `niwa list --json` is the reliable way to see which
instances accept unattended messages. The field is present on every record for
that reason. Turning the machine key off doesn't reach sessions already
launched, because Claude Code reapplies their launch settings when it restarts
or reopens them. To withdraw the grant, list the instances reporting
`accepts_session_messages: true` and stop their sessions.

**Recommended posture.** Turn the behavior on only on machines where every
session that shares the Claude Code account is one the developer would let
direct a bypass-mode worker. Prefer the per-dispatch flag to the machine key
when only some fan-outs need it. Treat dispatch containment, meaning a sandbox
and outbound network limits comparable to review sessions, as the control that
makes unattended delivery safe for untrusted inputs. It's planned as separate
work.

---

## Summary

The design is sound on its own mechanics: the settings document is built only
from constants, the marker is symlink-safe and gates nothing, and the new
records expose one boolean behind existing file protections. The real risk is
the accepted one, that bypass-mode workers now act on account-wide peer text
with no human checkpoint, and it has two unstated amplifiers: `niwa watch`
review sessions can now hand instructions to unsandboxed workers, and
`XDG_CONFIG_HOME` or `HOME` set through the workspace session environment can
move the machine config a nested dispatch reads. The recommendation is to
document these considerations (draft above), deny cross-session messaging in
review sessions, and qualify the "no other source" statement.
