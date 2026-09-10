# Phase 6 security review: dispatch-sendmessage-approval

Reviewed: the Security Considerations and Solution Architecture of
`docs/designs/DESIGN-dispatch-sendmessage-approval.md`, against
`internal/cli/dispatch.go`, `dispatch_remotecontrol.go`, `dispatch_launcher.go`,
`watch.go`, `internal/watch/containment.go`, `internal/agentplan/dispatch.go`,
`internal/config/config.go`, `overlay.go`, `registry.go`, and
`internal/workspace/materialize.go`.

Claude Code evidence comes from the installed 2.1.267 binary
(`~/.local/share/claude/versions/2.1.267`), read by grepping its embedded,
minified JavaScript. Quotes below are literal strings from that bundle. Where
the finding depends on control flow I inferred from minified code, it says so
and names the measurement that would confirm it.

## Verdict

The mechanics the design already covers hold up: constant-only settings
document, symlink-safe marker, one boolean behind existing file modes. The
review-session deny is the right idea, but as specified it is incomplete in
four ways:

- it names one of at least three tools that reach other sessions;
- it leaves the reverse direction open;
- its verification checks only the matcher string;
- it rests on a claim about which review posture is which permission class that
  current Claude Code probably no longer satisfies.

Separately, the design's statement that no caller can add a second `--settings`
is false for Claude dispatches, because the prompt is the last argv element with
no `--` separator. Finally, `crossSessionInbound: "accept"` lifts more holds than
the class-mismatch hold the design describes.

None of this changes the architecture. It changes the containment bullet, adds
a one-line launch-spec fix, and needs several Security Considerations sentences
rewritten. Five items are worth putting to the user before implementation (see
the escalation section).

## 1. Attack vectors not considered

### 1.1 The deny matcher misses other tools that reach sessions

The 2.1.267 bundle's built-in tool list includes `SendMessage`, `SendFile`,
`RemoteTrigger`, `ListAgents` (with `ListPeers` kept as an alias:
`ListPeers:"ListAgents"`), `SendUserMessage`, `PushNotification`, `CronCreate`,
`ScheduleWakeup`, `TeamCreate`, `Agent`, and `Task`.

- **`SendFile`** is described in the bundle as: "Send files to another Claude
  Code session -- a peer session on this machine, or a Remote Control / cloud
  session on another machine. The receiving Claude gets the files on its own
  filesystem with @path references". That is a message channel under another
  name: attacker-chosen files land on the worker's filesystem and in its
  context. Its permission text ("Send N files to '<to>'? ...
  (isolatePeerMachines is enabled.)") shows it goes through the same peer path
  as `SendMessage`. A `"SendMessage"` matcher does not cover it.
- **`RemoteTrigger`**: "Manage scheduled remote Claude Code agents (routines)
  via the claude.ai CCR API ... Auth is handled in-process -- the token never
  reaches the shell." The bundle also says a scheduled-trigger delivery is
  "distinguishable so the receive-side crossSessionInbound setting can apply to
  it". So routine deliveries into a session are cross-session inbound, and a
  worker set to `accept` takes them. Two problems follow for a review session:
  - It can schedule a routine that delivers into a named worker.
  - Independently of this feature, it can create and run cloud agents with
    content it read from an untrusted PR. The call goes out on the Claude
    process's own connection, not through Bash, so the OS no-egress sandbox
    doesn't cage it, and the egress matcher `WebFetch|WebSearch|mcp__` doesn't
    name it. That is a pre-existing hole in review containment, not only a
    messaging one.
- **`ListAgents`** is read-only discovery. It lists sessions across the account,
  cloud included, with names and descriptions. It gives a reviewer target names,
  which are predictable anyway, and it exposes the developer's session titles to
  a session that reads untrusted content. Low severity, but nothing in a
  review needs it.
- **`SendUserMessage` and `PushNotification`** address the user, not peers.
  **`CronCreate` and `ScheduleWakeup`** schedule the session's own turns, as far
  as the strings show. **`Agent`, `Task` and `TeamCreate`** spawn helpers inside
  the same instance. Those helpers load the same project hooks, but that should
  be tested (see 1.9).

Recommendation: a matcher of `SendMessage|SendFile|RemoteTrigger|ListAgents`,
plus a test that fails when the Claude Code tool list gains a new session-reaching
tool. The init event of `claude -p --output-format stream-json` lists the loaded
tools, which gives the manual delivery check something to diff against. The
guide should record the list and the Claude Code version it was taken from.

### 1.2 Matcher semantics: exact list, no substring over-match

The bundle's hook matcher does the following:

- An empty matcher or `*` matches every tool.
- A matcher made only of `[a-zA-Z0-9_|]` is split on `|` and compared by exact
  name. The comparison also accepts the tool's "hook matcher family" names and
  alias-normalized names, so `ListPeers` resolves to `ListAgents`.
- Anything else compiles as an unanchored `RegExp`.

So `"SendMessage"` matches only `SendMessage` and its family. It can't catch
`mcp__x__SendMessageFoo` by substring. Over-matching would have been fail-safe
anyway; the real risk is under-matching, which is 1.1.

Two side findings, both pre-existing and outside this design:

- The `fsGuardMatcher` comment in `containment.go` says "the harness matches by
  substring (so "Edit" already covers "MultiEdit")". That's wrong on 2.1.267. It
  does no harm there, because `MultiEdit` is listed explicitly.
- `egressDenyMatcher = "WebFetch|WebSearch|mcp__"` is a simple list, so `mcp__`
  matches only a tool literally named `mcp__`, not the MCP tools the comment says
  it denies. Sandboxed reviews pass `--strict-mcp-config` with no `--mcp-config`,
  so no MCP server loads and the gap is latent today. The regex form
  `^(WebFetch|WebSearch|mcp__.*)$` would do what the comment claims. Changing
  that string changes the dedupe and verify identity, so tests need to move with
  it.

For the new hook, keep the simple-list form and don't introduce regex
characters.

### 1.3 Does the hook fire in both postures?

Yes. PreToolUse command hooks run whatever the permission mode, and exit 2
blocks the call; the egress and filesystem hooks already rely on this under
`bypassPermissions`. In the operator-approval posture, the auto-allow matcher
(`Bash|Read|Glob|Grep`) doesn't name any of the tools above, and a blocking hook
beats an allow decision in any case. The hook runs before `SendMessage`'s own
permission check, which for cloud and Remote Control destinations under
`isolatePeerMachines` returns an "ask" safety check.

The premise the design states, though, is doubtful. It says that "in the
operator-approval posture they run in a prompting mode, so the class mismatch
was what held their messages to bypass workers", which implies the hard-deny
posture runs as bypass. But:

- Watch builds its argv with `buildDispatchPassthrough`, which reads the package
  variable `dispatchPermissionMode`. That variable stays empty in watch.
- The bypass derivation step (9a-derive) runs only in `runDispatch`.
- `dispatch.go`'s own comment says that since Claude Code 2.1.258 a
  `bypassPermissions` default mode in `settings.json` "no longer reaches a worker
  through the settings-file channel".

So on current Claude Code the hard-deny review session is most likely
prompting-class too. That means the class-mismatch hold protected both postures,
and this feature removes it for both. The hook is still correct in both. The
Security Considerations text should stop describing the hold as specific to the
ask posture, and someone should measure the actual mode. The containment.go
comments also assume hard-deny review sessions run as bypass, which may no longer
be true; that is out of scope here but worth a check.

With `watch_sandbox = off`, the hook is accident prevention only, like the
posting guard. Bash has full network and filesystem access there and can reach
the local inbox directly (see 1.4). The guide should say that.

### 1.4 Bash can bypass the tool deny through the local inbox socket

The bundle shows local cross-session delivery runs over a unix-domain-socket
inbox with a published key. The strings are the startup phases `setup_uds_dir`,
`uds_inbox_dir_ready`, `uds_inbox_listening`, `uds_inbox_key_published`, and the
event `cross_session_inbox_auth`. A tool-name hook doesn't touch a process that
speaks that protocol directly.

In sandbox mode, the no-egress stanza niwa owns sets neither
`network.allowUnixSockets` nor `network.allowAllUnixSockets`, both of which the
bundle recognizes. So sandboxed Bash can't open the socket, and that is the real
closure for this route. The design should name it so nobody later adds a unix
socket allowance to the stanza. Two caveats:

- `VerifyReviewSettings` reads only the project `settings.json`. An allowance in
  the developer's user or managed settings could merge in. I didn't verify how
  sandbox keys merge across sources. The same gap already exists for the egress
  posture.
- Under `accept`, a message whose sender "did not attest its permission mode"
  stops being held at a bypass receiver (see 1.5). Whether a process that isn't
  Claude Code can read the inbox key and send over the socket is unverified. If
  it can, the sender set under `accept` stops being "sessions on the account"
  and becomes "any process running as the user that can reach the socket",
  including an unsandboxed review session's Bash. The manual delivery check
  should test this before the guide describes the sender set.

### 1.5 `accept` lifts more holds than the class mismatch

Paraphrasing the bundle's receive-side policy:

- A kill switch refuses everything.
- An explicit `crossSessionInbound` value wins (`accept`, `hold`, or `refuse`).
- A self-sent message is accepted.
- If the receiver's mode is unknown, the message is held (`mode-unknown`).
- If the sender attested its mode:
  - same class: accept;
  - different class: hold (`mode-mismatch`).
- If the sender didn't attest its mode:
  - bypass receiver: hold (`no-mode-asserted`, "The sender did not attest its
    permission mode, and this session bypasses permission prompts.");
  - prompting receiver: accept.
- A separate default path returns hold with cause `bypass-default` for a bypass
  receiver ("This session is not prompting for permissions." / "It is being
  reviewed before delivery.").

For the bypass-mode workers niwa launches, `accept` therefore lifts four holds:
class-mismatch holds, holds on unattested senders, the `bypass-default` hold,
and holds on routine deliveries (1.1). The design says "The same exposure already
exists between two bypass-mode sessions, which Claude Code delivers between
without a hold". That is true only when the sending bypass session attests its
mode. The opening paragraph and "Who can send" should list all four, and say
that files arrive through the same path as text (`SendFile`).

### 1.6 The reverse direction: messages into review sessions

Nothing stops a steered worker, or any other session, from messaging a review
session. That session reads untrusted changes and drafts a review a person then
posts, so an inbound message can steer the verdict. This isn't new with the
feature, but it's cheap to close, and D12 is really about both directions.

The bundle lists `crossSessionInbound` as restrictive with values
`["refuse","hold"]`, and has a `repo-setting` hold cause ("This repository's
settings set crossSessionInbound..."). So a project settings file can tighten it.
`ApplyReviewSettings` can write `"crossSessionInbound": "refuse"` into the review
instance's `settings.json` in every mode, and `VerifyReviewSettings` can require
it.

`isolatePeerMachines` is also restrictive and project-settable. It turns any
cross-machine send into a safety check that the classifier can't approve
(`classifierApprovable:!1`). Setting it gives a second layer if a send ever gets
past the hook.

### 1.7 Dedupe by matcher and matcher-only verification

`ApplyReviewSettings` appends a hook only when no existing entry shares its
matcher, and `VerifyReviewSettings` only checks that some entry has the matcher.
A pre-existing `PreToolUse` entry with matcher `SendMessage` and a no-op command
would suppress niwa's deny and still pass verification. Such an entry could come
from the workspace's `[claude.hooks]`, or from an overlay's hooks, which
`OverlayClaudeConfig` appends.

That's tolerable for the posting guard, which is accident prevention. It isn't
for a hook the design presents as a containment control. Two fixes work:

- Always append niwa's entry. A duplicate matcher is harmless, since any hook's
  exit 2 blocks.
- Or have verification compare the command as well as the matcher.

The egress and filesystem hooks have the same weakness, and all of them can be
fixed together.

Related: `disableAllHooks: true` in any settings source turns off every review
hook. `SettingsConfig` is a free map (`map[string]MaybeSecret`). I didn't check
whether the materializer passes unknown keys into `settings.json`; if it does, a
workspace or overlay can set `disableAllHooks`. `VerifyReviewSettings` should
reject it in the merged project file, and the guide should say that a user-level
`disableAllHooks` defeats review containment.

### 1.8 Hooks are loaded at session start

A review session staged before niwa is upgraded runs without the hook until
`continueReview` re-applies settings and relaunches it. Once the developer turns
the key on, workers dispatched after the upgrade are reachable from those older
reviews. This is transitional. The guide or release note should say to let watch
re-stage, or dismiss, in-flight reviews before enabling the behavior.

### 1.9 Subagents and teammates inside review sessions

`Agent`, `Task` and `TeamCreate` can start helpers that call the denied tools.
Project hooks should apply to their tool calls. The functional scenario should
include a subagent attempt so a regression there is caught.

### 1.10 The prompt can reach the `--settings` slot

The design says: "`niwa dispatch` accepts no extra agent arguments, so a caller
can't add a second `--settings` that would replace it." For Claude that isn't
true:

- `buildLaunchArgs` appends the prompt as the last element, after the
  passthrough, which includes niwa's `--settings`.
- It inserts `--` first only when `spec.PromptSeparator` is set, and only the
  Codex spec sets it (`internal/agentplan/dispatch.go:408`).
- `validateDispatchPrompt` rejects only blank prompts.

Claude Code parses options with commander, so a prompt element beginning with
`--settings=` is read as a flag, and by the measured last-wins rule it replaces
niwa's merged document. There are three ways to supply one: typed as
`niwa dispatch -- '--settings={"crossSessionInbound":"accept"}'`, pasted into the
interactive capture, or composed by an agent.

The effect is that a worker can accept with no audit line and with
`accepts_session_messages: false` in `niwa list`, or can silently lose remote
control. The same route admits `--permission-mode=bypassPermissions` and
`--dangerously-skip-permissions`. It's closed only when keep-alive's prefix
occupies the start of the element, or when the prompt spills to a file.

The direct caller already has the same authority as the flag, so the practical
harm is a false record and a broken guarantee the design leans on. The design
says the record "errs toward reporting more acceptance than there is"; this path
makes it err the other way. Fix: set `PromptSeparator: true` for Claude, after
confirming that `claude --bg ... -- "<prompt>"` parses, or refuse prompts that
begin with `-`.

Other niwa paths are clean:

- Watch builds its passthrough without `--settings` and uses fixed prompt
  templates.
- Re-entry is `claude attach <id>`.
- Codex has no settings flag.
- The `--model`, `--agent` and `--name` values are safe. Commander's
  required-value options consume the next element even when it starts with a
  dash, and `--name` is sanitized anyway.

## 2. Are the mitigations sufficient?

- **Review-session deny.** Necessary but not sufficient as specified. It becomes
  sufficient for sandboxed reviews with the broader matcher (1.1), inbound
  refusal (1.6), command-level verification (1.7), and the unix-socket
  dependency written down (1.4). For `watch_sandbox = off` it is accident
  prevention only, and the design should say so.
- **Default off, the audit line, and the `niwa list` field.** Appropriate
  detective controls. Their accuracy depends on niwa owning the `--settings`
  slot, which needs the 1.10 fix. Env relocation (section 3) produces acceptance
  that neither surface records.
- **Marker file.** Sound; nothing to add.
- **Narrowing the sender set.** Nothing in the design does it. The bundle shows
  no receive-side narrowing key, so "niwa can't narrow it" is fair for the
  receiving worker. `isolatePeerMachines` narrows cross-machine sends at the
  sender. The guide can recommend it for the developer's own sessions that read
  untrusted content, without niwa writing user settings.

## 3. Are the "not applicable" or "no new capability" claims right?

- **"A workspace that sets XDG_CONFIG_HOME or HOME ... isn't a new
  capability."** The conclusion holds, but the reasoning is incomplete in two
  places.
  - The design says the overlay can't set the key because it has no `[global]`
    table. But `WorkspaceOverlay` carries `Env EnvConfig` and `Claude.Hooks`. If
    overlay env reaches the materialized settings `env` block (not verified
    here), the overlay can relocate `XDG_CONFIG_HOME` just like a workspace. The
    overlay sentence needs the same qualification.
  - Relocating `HOME` also moves `~/.claude/settings.json` for a nested `claude`,
    and user settings is a source that honors `accept`. So a workspace can get
    acceptance with no niwa involvement at all, bypassing the audit line and the
    record. That supports "not new", but the design should say this route is
    invisible to `niwa list`.
- **"This feature gives a dispatched worker no new permission."** Accurate for
  tool permissions. It should say the checkpoint it removes covers files as well
  as text, via `SendFile`, and covers routine deliveries.
- **"Nothing in a review session's job needs to message another session."**
  True, and it extends to sending files, scheduling remote routines, and listing
  sessions.
- **"Nothing from a workspace, repository, or prompt reaches it" / "a caller
  can't add a second `--settings`."** False for the prompt (1.10).
- **Supply chain "not applicable."** Still accurate. The bundle findings make the
  dependency on Claude Code's tool list and hook matcher semantics explicit; the
  design should list those next to `crossSessionInbound`, deep-merge, and
  `respawnFlags` as behavior it depends on.
- **Data exposure.** Accurate. Add `ListAgents` session-title disclosure to
  review sessions, which the broader matcher closes.

## 4. Residual risk to escalate before implementation

1. **The prompt-as-flag path (1.10).** The `niwa list` record and the audit line
   are the feature's only durable controls, and both assume niwa alone writes the
   `--settings` slot. Recommend fixing it in this PR, since it's a one-line spec
   change plus a test. It also closes a general flag-smuggling gap in D8's
   guarantee.
2. **`RemoteTrigger` from sandboxed reviews (1.1).** This is a pre-existing
   containment gap wider than messaging: a no-egress review can create and run
   cloud agents. The user should know it exists and decide whether denying it
   lands here (cheap, recommended) or separately.
3. **Which permission class review sessions run in on 2.1.258+ (1.3).** This
   affects the design's wording and possibly watch's hard-deny posture overall.
   It needs one measurement.
4. **Raw-socket senders under `accept` (1.4).** Until measured, the design and
   guide shouldn't claim the sender set is "sessions on the account".
5. **The broader hold removal (1.5).** The user is opting into lifting the
   unattested-sender, `bypass-default` and routine-delivery holds, not only the
   class mismatch. The PRD's acceptance of the risk was framed around the class
   mismatch alone.

## 5. Recommended edits, by section

- **Frontmatter `decision` and Context.** "a hook denying cross-session
  messaging" becomes "a hook denying the tools that reach other sessions
  (messages, files, remote routines, session listing) and a setting refusing
  inbound messages".
- **Decision Drivers, D12.** State both directions and name the tools. Say the
  hold covered review sessions in both postures, subject to the 1.3 measurement.
- **Solution Architecture > Components, `internal/watch/containment.go`
  bullet.**
  - Replace `messagingDenyMatcher = "SendMessage"` with a matcher of
    `SendMessage|SendFile|RemoteTrigger|ListAgents`, kept in the simple-list form.
  - Append niwa's entry unconditionally, or verify the command as well as the
    matcher.
  - Write `"crossSessionInbound": "refuse"`, and optionally
    `"isolatePeerMachines": true`, into review settings in every mode, and
    require both in `VerifyReviewSettings`.
  - Reject `disableAllHooks: true` in the merged project file.
  - Note that the unix-socket route is closed only by the owned sandbox stanza
    leaving unix sockets off.
- **Solution Architecture > Components, new bullet for
  `internal/agentplan/dispatch.go`.** Set `PromptSeparator: true` on the Claude
  launch spec, or have `validateDispatchPrompt` refuse a leading `-`.
- **Key Interfaces.** Record the denied tool list, the review settings keys, and
  the Claude Code version the tool list was taken from.
- **Implementation Approach.**
  - Phase 2 gains the prompt-separator change and a test that a prompt beginning
    with `--settings=` leaves the rendered document in force.
  - Phase 6 gains a functional scenario for that case.
  - Phase 7's deliverables gain a subagent attempt, the inbound-refuse
    assertion, and a tool-list drift check.
- **Security Considerations, opening paragraph.** Name the four holds `accept`
  lifts, and say files arrive through the same path as text.
- **Security Considerations, "Who can send."** Qualify the "same exposure
  between bypass sessions" sentence to senders that attest bypass mode. Add the
  unverified raw-socket question.
- **Security Considerations, "Review sessions."** Rewrite to cover:
  - both postures, and the likely prompting class on current Claude Code;
  - the full tool list and the inbound refusal;
  - the sandbox's unix-socket closure;
  - `watch_sandbox = off` being accident prevention only;
  - hooks being loaded at session start.
- **Security Considerations, "Who can turn it on."**
  - Qualify the overlay sentence for overlay `[env]`.
  - Add that relocating `HOME` can grant acceptance through user settings, which
    niwa neither causes nor records.
- **Security Considerations, "The settings document."** Replace the "caller can't
  add a second `--settings`" claim with the separator fix and what it closes.
- **Security Considerations, "What's recorded."** Make "errs toward reporting
  more acceptance" conditional on the separator fix. Add that env-relocated
  acceptance isn't recorded.
- **Consequences > Negative.** Review sessions also lose `SendFile`,
  `RemoteTrigger` and `ListAgents`, and refuse inbound messages. Add Claude Code's
  tool list and hook matcher semantics to the list of behavior niwa depends on.
- **Consequences > Mitigations.** Update the review-session bullet to match.
- **Outside this design (pre-existing, file separately).**
  - The `egressDenyMatcher` `mcp__` entry matches no MCP tool on 2.1.267.
  - The substring-matching comment in `containment.go` is wrong.
  - `RemoteTrigger` escapes review egress containment, if it isn't denied in this
    PR.
