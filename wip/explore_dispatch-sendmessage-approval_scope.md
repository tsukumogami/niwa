# Explore Scope: dispatch-sendmessage-approval

## Visibility

Public

## Core Question

As of today, delivering a message via the `SendMessage` tool between niwa
sessions prompts for human approval, even when the session was launched in
`bypassPermissions` mode. We want to find out what governs that prompt and
whether a dispatched session can be launched with the approval already in
place. The knob should be a per-machine configuration that is off by default,
overridable by a new flag on `niwa dispatch`.

## Context

- The prompt started appearing today, so something changed on the Claude Code
  side rather than in niwa. Whether that is version-gated, policy-driven, or a
  deliberate carve-out that ignores permission mode is unknown.
- niwa already materializes `permissions.defaultMode: bypassPermissions` into a
  dispatched instance's Claude settings (`internal/workspace/materialize.go`,
  `internal/workspace/permissions.go`), and that is evidently not sufficient.
- `niwa dispatch` already carries a `--permission-mode` flag
  (`internal/cli/dispatch.go`) forwarded to the background worker, plus a
  harness abstraction that drops flags an agent does not support.
- niwa already has a three-layer configuration chain for a similar concern:
  `--harness` flag, `NIWA_DISPATCH_HARNESS` env var, and
  `[global].default_dispatch_harness` in machine-level config. That chain is a
  precedent worth mirroring for precedence and naming.
- The user has no fixed preference on where the machine-level configuration
  lives or on the flag's shape; both are outputs of this exploration.

## In Scope

- Why the `SendMessage` approval prompt appears and what suppresses it
- Whether the prompt fires on the sending side, the receiving side, or both
- Claude Code launch-time surfaces for pre-approving a tool: CLI flags,
  `settings.json` permission rules, and hooks
- Where the knob belongs in niwa: machine-level config, materialized instance
  settings, or dispatch launch arguments
- Flag, environment variable, and config precedence, following existing niwa
  precedent
- Guardrails that justify keeping the behavior off by default

## Out of Scope

- Redesigning niwa's inter-session messaging model
- Revisiting the mesh-removal decision
- Any approach requiring changes to Claude Code itself
- Approval behavior for tools other than `SendMessage`, except where a general
  mechanism is the natural way to cover `SendMessage`

## Research Leads

1. **Why does `SendMessage` require approval even under `bypassPermissions`, and what governs that decision?**
   This is the premise of the whole task. If the approval is a deliberate
   carve-out that ignores permission mode, most of the obvious fixes are dead on
   arrival and the exploration has to look elsewhere.

2. **Which side of the exchange gets prompted -- the sender or the recipient?**
   The problem was reported as approval "to deliver" a message. If the prompt
   fires in the receiving session, configuring only the dispatched sending
   session cannot fix it, and the solution has to reach both ends.

3. **What launch-time mechanisms does Claude Code offer to pre-approve a specific tool?**
   Candidates include `--allowedTools`, `--permission-mode`,
   `--dangerously-skip-permissions`, `permissions.allow` rules in
   `settings.json`, and a `PreToolUse` hook returning an allow decision. We need
   to know which of these actually suppresses this particular prompt, and the
   exact rule syntax for a tool like `SendMessage`.

4. **How does `niwa dispatch` construct and launch the Claude session today, and where would a pre-approval knob slot in?**
   The existing `--permission-mode` flag and harness abstraction suggest there is
   already a path for forwarding session configuration. We want to extend that
   path rather than build a parallel one.

5. **What does niwa already write into a dispatched instance's Claude settings, and can pre-approval ride that machinery?**
   `materialize.go` already writes a permissions block, and the file
   distribution tables already place files into instances. If an existing seam
   fits, the change is configuration rather than new plumbing.

6. **What machine-level configuration surface does niwa have, and what are its precedence rules?**
   The `[global]` table, the `niwa config` commands, and the
   `NIWA_DISPATCH_HARNESS` override form a working precedent. Knowing where that
   file lives and how precedence resolves tells us the natural home for the new
   setting and the shape its override should take.

7. **What is the blast radius of pre-approving `SendMessage`, and what guardrails should scope it?**
   This justifies the off-by-default requirement. A session that can message any
   peer with no human in the loop widens the prompt-injection surface, and the
   guardrails available (scoping to peers in the same workspace, to dispatched
   sessions only, or to a named allowlist) shape both the flag and the config.

## Round 2 Leads

Round 1 settled the mechanism (`crossSessionInbound: "accept"`, receiver-side,
deliverable only via `--settings`, user settings, or managed settings) and the
control shape (the `--keep-alive` tri-state precedent). These leads close the
gaps that gate the implementation.

A direct `ListAgents` call during convergence already answered the peer-set
question empirically, so it is not a lead below. Findings: 116 peers, spanning
projects unrelated to this workspace, almost all reached over Remote Control
rather than the local socket. The set is account-scoped, not machine-scoped or
workspace-scoped, confirming that no workspace or host boundary exists at the
delivery layer. It also shows **live name collisions already present**:
one session name appears three times and six others appear twice each. The
non-unique-name defect is therefore observed fact, not inference.

8. **Does `--settings` layer onto the project `settings.json` or replace it, and what does a repeated `--settings` do?**
   This is the primary gate on the implementation shape. If `--settings`
   replaces rather than layers, an injected document would silently strip the
   plugins, env, and hooks niwa materializes into the instance. If repeated
   `--settings` is last-wins, the merged-document refactor is mandatory rather
   than tidy. Both are called undocumented in-repo and neither has been
   measured.

9. **What changed today, given the mechanism is 43 releases old?**
   The inbound hold shipped in 2.1.224 and the installed version is 2.1.267,
   with no changelog entry near current versions touching this path. If a
   niwa-side change flipped a previously-matched pair into a mismatch, the same
   change could flip it back, which would change both the urgency and the
   framing of the whole feature.

10. **What should the user-settings half of the fix look like?**
    The user needs messages flowing into their own interactive session too, and
    that is gated by `~/.claude/settings.json`, which niwa must not silently
    edit. What is the right surface for detecting the gap and prescribing the
    remedy, and what precedent does niwa have for advising rather than applying?
