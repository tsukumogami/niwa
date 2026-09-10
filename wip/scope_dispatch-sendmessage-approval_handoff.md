# /scope Handoff: dispatch-sendmessage-approval

## Provenance

Written by `/explore` on 2026-09-10 from
`wip/explore_dispatch-sendmessage-approval_crystallize.md`. Research files:
`wip/explore_dispatch-sendmessage-approval_findings.md`,
`wip/explore_dispatch-sendmessage-approval_decisions.md`, and
`wip/research/explore_dispatch-sendmessage-approval_r*_lead-*.md`.
The exploration ran two discover-converge rounds. Round 1 overturned the problem
framing and settled the mechanism. Round 2 measured the `--settings` behavior,
found the dated root cause of the symptom, and settled how the user-settings half
is handled. Along the way the author widened scope to both traffic directions and
moved two adjacent defects out into separate explorations.

## Problem Statement

niwa-dispatched Claude Code sessions have their peer messages held for human
approval. The hold is on the receiver's side: Claude Code holds a message
whenever the sending and receiving sessions fall into different permission-mode
classes. The immediate cause is a niwa defect. PR #277 restored dispatched
workers' `bypassPermissions` posture through the `--permission-mode` launch flag
after Claude Code 2.1.258 stopped honoring the project-settings channel, but
`dispatch_reentry.go` never carries that flag, so a resumed session drops out of
the bypassing class while a freshly launched peer stays in it. The underlying
cause is that delivery depends on two sessions agreeing on a permission class at
all. That agreement has broken twice in eight days, from two different
directions. The setting that removes the dependency is
`crossSessionInbound: "accept"`, and niwa's normal configuration channel cannot
set it.

## Scope Boundary

### In scope

- Carrying the derived permission mode through `dispatch_reentry.go` so a
  resumed session keeps the posture it was launched with, and reconciling the
  orphaned `WorkerPermissionMode` helper with the inline read #277 added
- A merged settings-document builder that replaces the single remote-control
  `--settings` injection, so more than one contributor can add keys to the one
  slot
- A dispatch-side grant of `crossSessionInbound: "accept"` contributed to that
  document, off by default, controlled by a per-machine `[global]` key in
  `~/.config/niwa/config.toml` and a tri-state `niwa dispatch` flag
- Carrying the merged document through the re-entry path as well as the launch
  path
- An audit line on dispatch output whenever the grant is active
- A one-time statement of the user-side remedy, shown when the dispatch-side
  grant is enabled, plus a guide section with the JSON and the real blast radius
- Tests for all of the above, including `@critical` functional scenarios

### Out of scope

- The inert `permissions.defaultMode` key niwa still materializes. A separate
  exploration owns it.
- The non-unique session name niwa forwards as the peer address. A separate
  exploration owns it too, although pre-approval makes that misdelivery silent,
  which is worth stating in the design.
- Detecting the user-settings gap, or writing `~/.claude/settings.json` for the
  user. Both were rejected; see below.
- Moving workers from `bypassPermissions` to `dontAsk`. Worth its own thread.
- Dispatch containment hooks equivalent to `niwa watch`'s. They're the right
  follow-up, and this feature raises their value, but they aren't this feature.
- Any change to Claude Code itself, and revisiting the mesh-removal decision.

## Decisions Already Settled

- **Deliverable shape.** The re-entry fix and the `crossSessionInbound` feature
  ship together, with the fix first. The author wants the result as a
  **single-pr PLAN**.
- **Both traffic directions are in scope.** The author kept the
  worker-into-own-terminal direction even after the evidence showed that path
  had never failed. It's a requirement about capability, not a diagnosis.
- **Mechanism.** `crossSessionInbound: "accept"` on the receiving session,
  delivered through `--settings`. Tool-permission constructs don't touch this
  prompt: `permissions.allow`, `--allowedTools`, and
  `--dangerously-skip-permissions` are all ruled out, because `SendMessage`
  needs no permission and the hold isn't a tool-permission prompt.
- **Rejected: a `PreToolUse` allow-decision hook.** It runs on the sender side,
  no hook event covers inbound delivery, and an `allow` decision has no effect
  on the "actions no mode auto-approves" class. It looks attractive because niwa
  already ships `watch.autoAllowHook()`, so record the rejection.
- **Rejected: materializing the key into instance settings.** Project scope can
  only ratchet `crossSessionInbound` stricter, so an `accept` written there is
  silently ignored. The idea came up again in round 2, so the rejection belongs
  on record.
- **Rejected: managed settings.** They're an administrator artifact that applies
  machine-wide.
- **The merged builder is mandatory.** Measured on 2.1.267: a repeated
  `--settings` is last-wins and silent, so a second flag would throw away the
  remote-control document. Also measured: `--settings` deep-merges onto the
  project settings file, and `hooks` arrays union across scopes, so an injected
  document breaks nothing niwa materializes. Malformed JSON refuses to start.
- **Re-entry must carry the grant.** `--permission-mode` and `--settings` are
  both command-line scope and die with the process, whichever channel carries
  the grant.
- **The control surface copies `--keep-alive`.** A `*bool` in `[global]`, a
  tri-state flag built on `triBoolValue`, and a resolver shaped like
  `resolveDispatchKeepAlive`. Off by default.
- **Off by default, and here's why.** An unattended worker, a derived
  `bypassPermissions`, no dispatch containment, hook `ask` decisions failing open
  under bypass, and a send that resumes a finished session in an instance still
  holding materialized secrets. "Messaging is risky" alone wouldn't survive
  review.
- **Naming.** The key must not be named after a boundary niwa can't enforce. The
  live peer set is 116 sessions across the author's account, reached by
  unauthenticated names that already collide, so avoid `workspace`, `instance`,
  `peers`, and `local`.
- **The user-settings half is stated once, never detected, never written.**
  There's no diagnostic surface to host detection, no Claude Code command
  exposes a key's effective value, a false positive would push the user to
  loosen a security-relevant setting machine-wide, and niwa's own rule for the
  one personal config it edits is "no global keys, only whole project blocks".
  The recommended surface is a closing line on the command that enables the
  grant, modeled on `config_default_harness.go:87-88`, plus a guide section
  modeled on the eligibility section of `remote-control-on-dispatch.md`.

## Coverage Notes

- **Does `claude --resume` restore the launch permission mode on its own?** The
  code shows niwa never passes it on re-entry. Whether Claude Code remembers it
  is untested. If it does, the re-entry gap is latent rather than live, but
  carrying the grant explicitly is still correct. The fix's acceptance test
  should settle this: dispatch, resume, and compare the class against a fresh
  peer.
- **The key and flag names.** Constrained, not chosen.
- **What the audit line says** when the grant is active.
- **Which harnesses are covered.** Codex has no `--settings` flag, so the grant
  can't be delivered to it. Warn and proceed, as keep-alive does, or drop it
  silently, as remote control does? The design has to pick one.
- **Where the guide section goes:** a new guide, or an extension of
  `remote-control-on-dispatch.md` or `session-keep-alive.md`.
- **Whether the new `[global]` key gets a `niwa config set` setter.** None of
  the analogous dispatch keys have one.
- **How the builder handles remote control's default-fill-only rule.** Remote
  control injects nothing when instance settings already decided
  `remoteControlAtStartup`. The builder has to preserve that while still
  emitting other contributors' keys.

## Upstream Observations

No ROADMAP covers this work; `docs/roadmaps/` is empty. The exploration read
these designs and guides:

- `docs/designs/current/DESIGN-dispatch-permission-mode.md` records the 2.1.258
  regression, the #277 derivation that fixed the launch path, an audit-line
  precedent for derived posture, and an open gap: no dispatch containment. It
  doesn't cover re-entry.
- `DESIGN-niwa-session-keep-alive.md` and `DESIGN-niwa-watch-once-pr-review.md`
  each declined a second `--settings` flag, assuming "undocumented, likely
  last-wins". The exploration has now measured that assumption and it holds.
- `docs/designs/current/DESIGN-niwa-mesh-removal.md` removed niwa's own
  agent-messaging layer because it was non-functional and off-identity, not
  because it was unsafe. It also reserved "downstream replacement coordination"
  as gated, so the design should say how this feature relates to that
  reservation.
- `docs/guides/remote-control-on-dispatch.md` and
  `docs/guides/session-keep-alive.md` are the two existing guides for this exact
  kind of knob: host-level default, off by default, overridable. Match them.

## Framing-Shift Answer

**Pre-supplied answer:** yes, the framing shifted.

**Evidence:** The request was framed as "SendMessage now needs approval even
under bypassPermissions; bake the approval into dispatched sessions". Round 1
found there's no permission prompt on `SendMessage`. There's a receiver-side
delivery hold, and `bypassPermissions` is what triggers it, not the thing that
failed to cure it. Round 2 found the symptom isn't new: a transcript scan shows
seven holds ever, all received by background sessions, the first on
2026-09-02. Its proximate cause is a niwa re-entry defect. The success
criterion moved from "pre-approve a tool" to "make delivery independent of
permission-class agreement, and stop the re-entry path from dropping launch
posture".

## Shape Signals

### Architectural alternatives left open

- **Whether there's an instance-settings rung between flag and host config.**
  Including it matches `--keep-alive` exactly and lets a workspace set a
  default. Leaving it out keeps the grant off every file a repo or branch could
  carry, which is what the blast-radius analysis argued for. The two leads
  disagreed.
- **The builder's shape.** One option is a small collector in the dispatch path
  that every contributor appends keys to and that emits one `--settings`
  element. That's clean, but remote control's default-fill logic has to be
  rewritten as a contributor. The other is widening
  `dispatch_remotecontrol.go`'s helper to take extra keys. That's fewer moving
  parts, but it ties unrelated features to the remote-control file and has to
  be redone at the next contributor.
- **How re-entry gets the grant.** Re-running the resolvers at re-entry is
  stateless, but it picks up any host-config change made between launch and
  resume, which may surprise people. Persisting the resolved launch decision in
  the instance and replaying it is faithful to the launch, but it needs a new
  state record and a migration story for instances created before it existed.
- **Whether the re-entry permission-mode fix and the grant share one carrier.**
  Carrying the derived mode as its own argv flag is the minimal fix. Putting the
  mode inside the merged settings document too collapses the two into one
  carrier, but `permissions.defaultMode` from `--settings` versus
  `--permission-mode` precedence would need confirming first.

### Complexity signals

- The author wants a single-pr PLAN spanning a defect fix, a refactor of an
  existing injection (the merged builder), a new host-config key, a new
  tri-state flag, re-entry wiring, docs, and functional tests.
- The dispatch path sits under AST layout scans
  (`internal/cli/dispatch_layout_test.go`) that forbid naming an agent in
  dispatch-path files. New code has to gate on flag spellings or capability
  lookups, and any new `dispatch_*.go` file has to be registered with the scan.
- The default is security-relevant, and two prior designs refused the shape this
  one needs (a second `--settings`). The design has to write down why the
  refusal no longer applies: the builder replaces it rather than violating it.
- The re-entry file's header comment says a missing grant fails silently, and
  that's how the #277 defect reached a release. A regression test that fails
  when a future grant is added to launch but not to re-entry would be
  proportionate.
