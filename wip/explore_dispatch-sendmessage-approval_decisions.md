# Exploration Decisions: dispatch-sendmessage-approval

## Round 1

- **Both traffic directions are in scope**: worker-to-worker and worker-into-the
  user's own interactive session. Rationale: the user needs unattended delivery
  in both directions. Consequence: niwa can only fix the half it launches. The
  other half requires `crossSessionInbound: "accept"` in the user's
  `~/.claude/settings.json`, which niwa must detect and advise rather than
  silently edit.

- **The two incidental defects do not travel with this work.** Each was
  dispatched as its own `/shirabe:explore` session rather than folded into this
  exploration or filed as a bare issue. Rationale: keeps this exploration's
  output focused on the pre-approval knob, while neither defect is lost.
  - Non-unique session name forwarded as the peer address
  - Inert `permissions.defaultMode` in materialized instance settings

- **`crossSessionInbound` is the mechanism; tool-permission constructs are
  ruled out.** `SendMessage` requires no permission and its own check allows
  same-machine sends unconditionally, so `permissions.allow` rules,
  `--allowedTools`, and `--dangerously-skip-permissions` are the wrong layer and
  suppress nothing that is currently prompting.

- **The `PreToolUse` allow-decision hook is ruled out**, despite niwa already
  shipping exactly that pattern in `watch.autoAllowHook()`. Three independent
  reasons: the hook is sender-side, no hook event covers inbound message
  delivery, and an `allow` decision is explicitly powerless against the
  documented "actions no mode auto-approves" class. Keep it in any design doc as
  considered-and-rejected so the next reader does not re-propose it.

- **The materializer channel is ruled out for this key.** `crossSessionInbound`
  has inverted precedence: a project-scope `.claude/settings.json` can only
  ratchet it stricter, and `accept` is the loosest rung, so writing it there is
  a silent no-op. This is niwa's normal configuration lever and it structurally
  cannot grant the value.

- **Managed settings are ruled out** as niwa's channel: an administrator
  artifact, machine-wide across every session and tool, not something a
  user-level CLI should write on a developer machine.

- **The precedent to mirror is keep-alive, not the harness chain.**
  `resolveDispatchKeepAlive` (flag > instance settings > `[global]` > off, with
  `*bool` tri-state) is the exact shape requested. The four-rung harness chain
  was named in the scope file but is the wrong template for an off-by-default
  boolean.

- **Off by default is confirmed, on a sharper rationale than the scope file
  assumed.** Not "messaging is risky" but the specific combination: an
  unattended worker, derived `bypassPermissions`, no dispatch containment hooks,
  hook `ask` decisions failing open under bypass, and a send that resumes a
  finished session in an instance still holding materialized secrets.

- **Do not name the config key after a boundary niwa cannot hold.** Delivery is
  addressed by unauthenticated, non-unique names and crosses machines and the
  cloud. Anything containing `workspace`, `instance`, `peers`, or `local` would
  assert a scope that does not exist at the delivery layer.

## Round 2

- **The deliverable is both the bug fix and the feature, sequenced.** The
  re-entry gap is fixed first because it resolves the observed symptom and is
  small; the `crossSessionInbound` grant follows because it removes the
  underlying coupling. Rationale: delivery currently depends on two peers
  agreeing on permission class, and that coupling has broken twice from two
  different directions in eight days. Fixing only the symptom leaves the third
  break to a schedule set by session lifetime.

- **Both traffic directions stay in scope**, confirmed after the evidence showed
  the user's interactive terminal was never actually held. The user's need is a
  requirement about intended capability, not a diagnosis. Consequence unchanged:
  the worker-into-terminal direction needs a one-time user-settings change that
  niwa surfaces once and never applies.

- **niwa must not detect the user-settings gap.** There is no `niwa doctor` to
  host the check, no Claude Code command exposes a settings key's effective
  value, and niwa would have to reimplement an inverted precedence ladder
  against managed settings it may not be able to read. A false positive would
  push a developer to loosen a security-relevant setting machine-wide on niwa's
  authority, and the dispatch path it would print on runs in fan-out loops and
  on `niwa watch`'s cron timer. Say it once when the dispatch-side grant is
  enabled; document the rest in a guide.

- **niwa must not write the key on the user's behalf either.** niwa's own
  documented rule for the one personal config it does edit is "no global keys,
  only whole project blocks", which forbids a `niwa config` command that would
  set `crossSessionInbound` in `~/.claude/settings.json`.

- **A merged settings-document builder is mandatory, not optional.** Measured:
  a repeated `--settings` is last-wins and silent, so a second flag would
  discard the Remote Control document niwa already passes. Measured also that
  `--settings` deep-merges onto the project settings file and that `hooks`
  arrays union across scopes, so an injected document breaks nothing niwa
  materializes. Malformed JSON fails loudly at startup, which is the desirable
  failure mode for a programmatically assembled document.

- **Whatever channel carries the grant, `dispatch_reentry.go` must carry it
  too.** Both `--permission-mode` and `--settings` are command-line scope and
  die with the process. This is the generalized form of the defect that caused
  today's recurrence, and shipping the grant without it would reproduce the same
  bug in a new channel.

- **Rejected: materializing `crossSessionInbound: "accept"` into instance
  settings.** Proposed in round 2 by an agent that had not seen round 1's
  precedence finding. Project scope can only ratchet this key stricter, so
  `accept` written there is silently ignored. Recorded here because the proposal
  is intuitive and will recur.
