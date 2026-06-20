# Crystallize Decision: niwa-default-worktree

## Chosen Type

Spike Report (followed by a delegation-first Design Doc)

## Rationale

Exploration flipped the issue's premise: live-doc verification confirmed that
Claude Code's `WorktreeCreate`/`WorktreeRemove` hooks fire in git repos and replace
default git worktree logic, so true delegation (`EnterWorktree` → `niwa worktree
create`) is viable today. With feasibility of the *mechanism* established, exactly
one technical unknown gates the design: at what settings scope must niwa install the
hook so Claude Code actually fires it for an agent running inside a repo
(workspace-root `.claude/settings.json` vs per-repo settings vs user/enterprise)?
That is a focused, time-boxable "can we, and where?" question whose answer
determines the design's install strategy — the definition of a spike. The user
explicitly chose "quick spike first."

## Signal Evidence

### Signals Present
- Core question is feasibility ("can we do this, and at what scope?"): the
  settings-cascade reach of niwa's installed hook is unconfirmed.
- Technical uncertainty blocks a decision: the design's install strategy can't be
  finalized until the scope is known.
- A specific technical risk was identified and is testable: configure a real
  `WorktreeCreate` hook via niwa's existing `InstallWorkspaceRootSettings()` surface
  and observe whether Claude Code invokes it from where niwa writes it.

### Anti-Signals Checked
- "Should we do this?" (strategy) / "What should we build?" (requirements): not
  present — the what and why are settled; only the how-feasible remains.
- Exploration was broad, not focused on a specific risk: not present — the spike
  targets one concrete risk.
- Approach known, only sequencing remains: not present — there is a genuine unknown.

## Alternatives Considered

- **Design Doc**: scored equally well on raw signals (how-to-build is the open
  question; delegation hooks, branch-name reconciliation, non-blocking
  `WorktreeRemove` vs niwa's dirty/attached guards, and the deny+steer fallback are
  real architectural decisions). Ranked second only because it is *gated* by the
  spike — finalizing the hook install strategy requires the spike's answer. It is
  the committed immediate follow-on, not a rejected alternative.
- **No Artifact**: rejected — architectural decisions were made during exploration
  (delegation-first + deny+steer fallback) that must persist beyond `wip/`, and
  multiple people/agents will build from this.
- **Decision Record**: rejected — this is not a single isolated choice; it's a
  feasibility gate plus a multi-decision design to follow.

## Carry-Forward Into the Spike and Design

- Committed approach (for the design after the spike): delegation-first via
  niwa-installed `WorktreeCreate` → `niwa worktree create` and `WorktreeRemove` →
  detach/cleanup, with `permissions.deny: ["EnterWorktree","ExitWorktree"]` +
  CLAUDE.md guidance as a documented fallback. Both installed by `niwa apply`.
- Spike must resolve: settings scope where the hook fires; the stdin→`niwa worktree
  create` adapter and repo resolution from `cwd`/`git_dir`; branch-name
  reconciliation (`worktree-<name>` vs `session/<sid>`); and `WorktreeRemove`
  non-blocking behavior vs niwa's dirty/attached destroy guards.
