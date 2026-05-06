# Lead: Human main-clone UX

## Findings

### Does anything currently prevent or warn about launching Claude in a main clone?

Nothing in the codebase prevents or warns a human from running `claude` directly inside a repo's main clone at `<workspace>/<instance>/repos/<repo>/`. The only active detection mechanism is the `session_start` hook (`mesh-session-start.sh`), which runs `niwa session register` when Claude starts. That command reads `NIWA_INSTANCE_ROOT` from the environment and derives a role from the current working directory. If a human launches Claude in the main clone without `NIWA_INSTANCE_ROOT` set, the command returns an error (`NIWA_INSTANCE_ROOT is not set`) but this is a hook error, not a user-facing warning. When `NIWA_INSTANCE_ROOT` is set (because the workspace was applied and env was configured), `niwa session register` succeeds, registers the human as "coordinator" or as the repo-role depending on CWD, and silently proceeds.

The `user_prompt_submit` hook runs `niwa session register --check-only`, which skips registration when the role is already registered. Neither hook inspects whether the CWD is a main clone vs. a session worktree, nor emits any guidance to the human.

The shell integration (`internal/cli/shell_init.go`) wraps only `create`, `go`, and `session create`. It has no logic to intercept a bare `claude` invocation.

In short: **no guardrails exist today**. A human can run `claude` in a main clone and niwa treats them as an ordinary coordinator-level or repo-level session.

### What does the PRD say about the non-mesh developer use case (R16)?

PRD R16 explicitly provides for the non-mesh (human) use case: "`niwa session create <repo> [--purpose <text>]` creates a session worktree... and navigates the shell to the session worktree root." The PRD's goals section states: "Non-mesh developers can use the same session model via a CLI command to get an always-clean main clone without a coordinator." User story: "As a developer using niwa without a coordinator, I want to create a session for a repo from the CLI, so that my main clone stays on main while I work on a feature in a clean, isolated worktree."

The PRD's framing is opt-in: `niwa session create` is the recommended path, but there is no requirement that humans must use it. The backward-compatibility decision ("No implicit sessions on untagged niwa_delegate") ensures existing workflows are unaffected. That same logic applies implicitly to human-launched Claude: requiring session use would be a breaking behavioral change. The PRD made sessions strictly opt-in for exactly this reason.

Crucially, the PRD never classifies running Claude in a main clone as an error state. It acknowledges the gap (main clones accumulate stale branches) without mandating a UX response to it.

### What would a "warn and redirect" implementation look like?

The natural insertion point is the `session_start` hook (`mesh-session-start.sh`). This hook already runs at every Claude startup, has access to CWD via the shell, and can be customized per-repo or per-instance. A warn-and-redirect implementation would:

1. Check whether the current CWD is `<instance>/repos/<repo>` (a main clone) vs. `<instance>/.niwa/worktrees/<repo>-<id>/` (a session worktree).
2. Detect that this is a human-initiated session, not a worker spawned by the daemon (workers have `NIWA_TASK_ID` set; a bare human launch does not).
3. Print a warning: "You are working in the main clone. Consider running `niwa session create <repo>` to isolate your work in a worktree."

A redirect (instead of warn) would require the hook to exit non-zero to abort the launch, but Claude Code's hook exit semantics are not designed for blocking Claude startup — the `session_start` hook is informational. Redirection via a shell wrapper wrapping `claude` is theoretically possible but would require changes to the niwa shell function template to intercept bare `claude` calls, which the current design explicitly avoids (`niwa() { case "$1" in create|go|session) ... }` — it only wraps `niwa` subcommands, not `claude`).

A "block entirely" approach would require either:
- A Claude Code permission mode that refuses interactive launch (not a current feature of Claude Code), or
- A wrapper function in the niwa shell integration that intercepts `claude` itself — a significant scope expansion that invades the user's shell environment.

### Trade-offs of blocking vs. warning vs. documenting

**Blocking:** High friction. Prevents an action that is not inherently harmful (a human doing ad-hoc work in a main clone is a legitimate pattern for read-only investigations, issue triage, etc.). Requires intercepting the `claude` binary, which is outside niwa's current shell integration scope. Would generate user hostility for a tool with no prior users, establishing a combative first impression. No upside proportional to the cost.

**Warn and redirect:** Moderate friction, good discoverability. The session_start hook is already the natural channel for this message. A one-time warning (using the one-time notices pattern from `docs/guides/one-time-notices.md`) would surface the guidance without becoming noise. Requires no binary interception. The main downside: a warning on startup that users cannot act on immediately (they'd need to restart in a session) may be confusing if they're just doing a quick lookup.

**Document only:** Lowest friction, zero discoverability. The sessions guide (`docs/guides/sessions.md`) already describes `niwa session create` as the recommended path. But a human who navigates into a repo directory and types `claude` has no reason to have read the sessions guide first. Documentation-only fails on the discoverability dimension — the user most likely to need guidance is the one who hasn't sought it.

**One-time notice via hook:** A middle path. Use the one-time notices infrastructure to emit the guidance exactly once per workspace instance (or once per Claude version). After that, the user is informed and the message stops. This is the best-fit option: discoverable, not persistent noise, and requires only a hook script change plus a notice key.

### Is there a natural UX flow guiding a human to `niwa session create`?

Not today. The flow that would exist naturally is:

1. Human runs `niwa go <repo>` — navigates to main clone.
2. Human runs `claude` — launches in main clone with no guidance.
3. Human accidentally leaves main clone on a feature branch.
4. `niwa apply` skips the repo on next update.

The intended flow (post-R16) is:

1. Human runs `niwa session create <repo> "my work"` — creates worktree, navigates to it.
2. Human runs `claude` — launches in session worktree, correctly isolated.

The gap is step 1: there is no prompt, hint, or default behavior that steers the human from step 1 of the actual flow to the intended flow. `niwa go <repo>` navigates to the main clone by design (R26: "without `<session-id>`, behavior is unchanged").

A natural integration point would be `niwa go <repo>` emitting a one-time hint: "To isolate work in a worktree, run: niwa session create <repo>". This fires at the navigation step, before Claude launches, when the human has agency to act on it.

### What is the session model's answer for humans wanting a persistent interactive context?

R16 and R17 establish that `niwa session create` is precisely the answer for humans. It provisions the same git worktree, daemon, and session state that coordinator-created sessions use. The human launches Claude inside the session worktree and gets identical isolation benefits: main clone stays on main, work is on a dedicated `session/<id>` branch, `niwa apply` does not touch the worktree.

However, the session model as implemented is task-centric: it assumes a coordinator will send tasks via `niwa_delegate`. A human launching Claude interactively in a session worktree gets the isolation benefit but none of the task-dispatch machinery — the session daemon is running, but no tasks will arrive via inbox. This is fine for human use: the human just talks to Claude directly. The `niwa session register` hook on session_start registers the human as the coordinator role, which is semantically reasonable.

One gap: when the human finishes and runs `niwa session destroy`, it removes the worktree. But `niwa_destroy_session` and `niwa session destroy` have no mechanism to interactively confirm what work has been pushed vs. not. The `blocked_by_unpushed_work` guard addresses data loss, but the human must explicitly run `--force` to override. This matches coordinator semantics, which is appropriate.

## Implications

The session model's opt-in design is intentional and correct for the current stage. Forcing sessions would break any use case where a human runs Claude in a main clone for read-only or investigative work. The practical problem — main clones drifting onto feature branches — is a consequence of writing work in the main clone, not of launching Claude there.

The right intervention is at the point where the human is about to write code: a one-time hint at `niwa go <repo>` or on Claude startup (via session_start hook) pointing toward `niwa session create`. This is discoverable, non-blocking, and consistent with the sessions guide.

The `niwa go <repo>` command is the cleanest hook. It is the canonical path to a repo (it already emits a trace to stderr: `go: repo "niwa" in tsukumogami-4`). Adding a one-time notice there — emitted only when the user is navigating to a main clone while active sessions exist (or unconditionally the first time) — surfaces the guidance precisely when the user is making the navigation decision.

## Surprises

**The session_start hook already runs in main clones.** It registers the human as a coordinator/role-name session via `niwa session register`. This means there IS a hook insertion point for a main-clone warning — it just isn't used for that purpose yet. The infrastructure to detect and warn is partially in place.

**`niwa go <repo>` already distinguishes main-clone navigation.** The two-arg form (`niwa go <repo> <session-id>`) navigates to a session worktree; the one-arg form navigates to the main clone. This distinction in the existing code is a natural place to emit a first-run hint, since the binary can check at go-time whether any active sessions exist for that repo.

**The workspace-root Claude session (instance root) has a design doc.** `DESIGN-workspace-root-claude.md` covers running Claude at the instance root (above all repos). That case has explicit support via workspace-context.md and settings.json. The human-in-a-main-clone case (below the instance root, inside a single repo) has no equivalent design document and no design decision recorded.

## Open Questions

1. **Should `niwa go <repo>` emit a one-time hint suggesting `niwa session create`?** The one-time notices guide exists; the infrastructure is already used for other hints. Is this the right trigger point, or is the session_start hook preferable?

2. **What is the right scope for the one-time notice?** Per workspace instance? Per repo? Per human? A global "you've been warned" flag in `~/.niwa/` would fire once ever; per-instance is more targeted.

3. **Should human-launched Claude in a main clone register differently in `sessions.json`?** Currently it registers with role derived from CWD (e.g., "niwa" if CWD is the niwa repo). There's no semantic distinction between a human-launched session and a coordinator-spawned coordinator session at the same CWD. This matters if the warning logic wants to detect "human in main clone" specifically.

4. **Is `niwa session create` discoverable enough from `niwa session list` output?** If no sessions exist, `niwa session list --status active` returns an empty table. Does that empty table surface a helpful hint? Currently, no.

5. **Does the human need `niwa session create` at all for read-only work?** For investigation and triage in a main clone, there's no branch-contamination risk. A warning that fires even for read-only Claude sessions would be noise. Is there a way to distinguish intent (write work vs. read-only exploration)?

6. **Is the coordinator's session at the workspace instance root the right mental model for human coordinators?** The exploration context notes that there is no session equivalent at the workspace instance root level. The coordinator always runs from the main instance root without a worktree. This asymmetry — coordinators are safe at the instance root because they don't write to repos; humans are unsafe in a main clone because they do — should be made explicit in documentation.

## Summary

No guardrail currently exists for humans launching Claude directly in a main clone; the session_start hook fires but does not inspect or warn about the context. The PRD explicitly provides `niwa session create` as the human path to isolation (R16), but treats it as opt-in — the gap is discoverability, not capability. The most proportionate response is a one-time notice at `niwa go <repo>` or via the session_start hook, steering humans toward `niwa session create` before they begin writing work, without blocking legitimate read-only use cases.
