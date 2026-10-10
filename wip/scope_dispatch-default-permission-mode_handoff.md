# /scope Handoff: dispatch-default-permission-mode

## Provenance
Written by `/explore` on 2026-10-10 from `wip/explore_dispatch-default-permission-mode_crystallize.md`.
Research files: `wip/explore_dispatch-default-permission-mode_findings.md`,
`wip/explore_dispatch-default-permission-mode_decisions.md`, and
`wip/research/explore_dispatch-default-permission-mode_r1_lead-*.md`.
One discover-converge round with four leads (posture flow, host-default
precedent, Codex vocabulary, Claude mode behavior). The author narrowed reach
to Claude and Codex dispatch and reframed the motivating constraint as a
personal host preference rather than a detected capability.

## Problem Statement
The permission mode a `niwa dispatch` worker launches with is derived from the
workspace's `[claude.settings] permissions` posture, so every host running the
same workspace gets the same mode. A developer who uses one workspace on
several machines can't say "no bypass on this corporate laptop, bypass
everywhere else": the only per-dispatch control is the `--permission-mode` flag,
which has to be typed every time. Permission posture is a property of the
machine and its owner, not of the workspace.

## Scope Boundary
### In scope
- A host-level key in `~/.config/niwa/config.toml` `[global]` setting the default dispatch permission mode
- Precedence: `--permission-mode` flag > host key > workspace posture > nothing, with stderr naming the source
- Claude dispatch and Codex dispatch, including what the host value means for Codex
- Deprecating the workspace `[claude.settings] permissions` key (warn, keep honoring it below the host key)
- Exposure through `niwa config set/unset`, plus validation

### Out of scope
- `niwa watch` review sessions: the author excluded them; watch passes no permission mode today
- Ephemeral sessions provisioned by the SessionStart hook: author excluded
- Per-workspace overrides inside the host config: the point is host-wide
- Detecting whether a host allows a mode: the author knows their host's policy

## Decisions Already Settled
- The host default is a developer preference; niwa takes it as given and does not read managed policy or probe Claude Code.
- Host default outranks the workspace posture; the flag outranks both. This deliberately reverses the polarity `default_dispatch_harness` documents.
- The workspace `permissions` posture will be deprecated; both are supported during the transition.
- Reach is Claude and Codex dispatch only.
- Raw passthrough of the host value to every agent is ruled out (a Claude value forwarded as Codex `--sandbox` breaks the launch).
- Mapping a bypass default to Codex `danger-full-access` is ruled out (it disables the sandbox and writes trust, which the Codex posture design forbids).
- Post-launch mode-mismatch detection is not required for this feature.

## Coverage Notes
- The value vocabulary isn't chosen: Claude's raw mode names, or a niwa-owned set mapped per agent through LaunchSpec. The research favored niwa-owned with Codex mapping to "forward nothing".
- Whether the explicit `--permission-mode` flag adopts the same vocabulary, and whether to fix the existing bug where an explicit Claude value reaches Codex raw as `--sandbox`.
- What replaces the workspace `ask` posture's `permissions.defaultMode: "default"` write into instance-root, per-repo, and workspace-root settings (it constrains interactive sessions, not just dispatch) before the key can be removed.
- `niwa config set/unset` exposure: only `default-dispatch-harness` has a setter today, and `SaveGlobalConfigTo` drops comments and unknown keys.
- `--bg` launches of bypass need `skipDangerousModePermissionPrompt` accepted; Claude Code silently falls back to Manual when a mode is locked or unsupported (auto on some models). Docs may want to say so.

## Upstream Observations
`docs/designs/current/DESIGN-dispatch-permission-mode.md` and the PRD for the
inert defaultMode key describe today's derivation from the recorded
`claude_permissions` instance state. `DESIGN-global-config.md` and the
`[global]` key comments in `internal/config/registry.go` (`dispatch_model`,
`accept_session_messages_on_dispatch`, `default_dispatch_harness`) are the
precedents. `DESIGN-codex-dispatch-posture-persistence.md` and
`internal/agentplan/posture.go` constrain the Codex mapping. No ROADMAP covers
this.

## Framing-Shift Answer
**Pre-supplied answer:** yes, the framing shifted
**Evidence:** Round 1 convergence. The author first framed it as "this host
does not support bypassPermissions", but the research found no local policy
disabling bypass and saw Claude Code fall back silently rather than fail. The
author reframed it as a preference: "I do not want to use bypass on this host,
but I want to use bypass on other hosts." The feature is a per-host preference,
not capability handling.

## Shape Signals
### Architectural alternatives left open
- Vocabulary: raw Claude mode names (simple, matches the flag today, but meaningless to Codex) vs. a niwa-owned set mapped per agent (needs a mapping table in LaunchSpec, fixes the Codex bug)
- Replacement for the `ask` interactive defaultMode: a host-level equivalent, dropping it, or keeping a narrower workspace key
- Setter: hand-edited key only (like `dispatch_model`) vs. a `niwa config set` subcommand (like `default-dispatch-harness`)

### Complexity signals
- Small code footprint in the dispatch path (one rung in `derivePermissionMode`), but it touches three agent/config surfaces (Claude, Codex, workspace deprecation) and reverses a documented precedence convention.
- The deprecation is a staged change spanning parse-time warnings, docs, and the dot-niwa config that declares `permissions = "bypass"`.
