# Decision: fallback detection & disclosure

## Findings

### 1. Can niwa detect the Claude Code version at apply time?

**No existing version-probing mechanism found in niwa codebase.**

- Search of `internal/` reveals no `claude` subprocess calls, no version-check routines, and no harness version detection.
- Codebase uses `exec.CommandContext` for git operations (`fallback.go:148`, `fallback.go:154`) but only for git, not for external tool version probing.
- niwa's apply pipeline (`internal/workspace/apply.go`) orchestrates materializers and configs but has no version-detection seam.

**`claude --version` is feasible but adds friction:**

- Claude Code CLI supports `claude --version` (standard practice for CLI tools).
- A version-probe subprocess at apply time would require:
  - Parsing version output (format: `claude X.Y.Z` or similar)
  - Comparing against minimum version that supports per-repo WorktreeCreate/WorktreeRemove hooks (spike used v2.1.183; this is the known-good baseline)
  - Handling parse/execution errors gracefully (fallback to assume-supported vs assume-unsupported)
- This adds latency to every apply, even when hooks are not used in the workspace config.

**Minimum version requirement:** Claude Code v2.1.183 confirmed in spike (SPIKE-niwa-default-worktree.md:73) as supporting per-repo `.claude/settings.local.json` WorktreeCreate/WorktreeRemove hooks. Exact minimum version for hook support not established; spike tested only v2.1.183.

---

### 2. niwa's one-time-notice mechanism

**Pattern is fully documented and in active use.**

- File: `docs/guides/one-time-notices.md`
- Key data structure: `InstanceState.DisclosedNotices []string` (JSON: `disclosed_notices`, omitted when empty)
- Helpers in `internal/workspace/state.go`:
  - `noticeDisclosed(s *InstanceState, notice string) bool` — check if notice key is recorded
  - `mergeDisclosedNotices(existing, added []string) []string` — deduplicating union for state save
- Existing precedent notices:
  - `provider-shadow`: personal overlay declares a provider that shadows team config (apply.go:176)
  - `rank2-deprecation:team-config`, `rank2-deprecation:overlay`: deprecated rank-2 layout (disclosure.go:8, :12)
  - `plugin-installed:niwa`, `plugin-install-skipped:niwa`: plugin auto-install status (disclosure.go:17, :24)

**Adding a new notice (per docs/guides/one-time-notices.md:39-66):**

1. Define key constant in `internal/workspace/apply.go` (e.g., `const noticeWorktreeFallback = "worktree-fallback"`)
2. Guard emission in `runPipeline` with `if condition && !noticeDisclosed(opts.existingState, noticeWorktreeFallback)`
3. Append to `newDisclosures` slice (already wired to `pipelineResult` and merged into saved state by both Create and Apply)

The notice surface is `Reporter.Log()` or `Reporter.Defer()` in apply.go's runPipeline (applies print to stderr, both blocking and deferred paths available).

---

### 3. Hook vs deny mutual exclusivity & interaction

**Hook and deny are mutually exclusive; they must be chosen, not combined.**

Reasoning from codebase analysis:

- **Permission mode enforcement:** `internal/workspace/permissions.go:25-39` shows `WorkerPermissionMode()` reads the materialized `settings.json` and returns the permission mode (bypass or acceptEdits). The Claude Code harness **checks permissions before dispatching tool calls** — this is standard for all tools, not special to worktree.
- **Tool allow/deny in settings:** Niwa writes `permissions.allow` and `permissions.deny` arrays (not yet observed in code, but the permission mode is materialized via `buildSettingsDoc` in `materialize.go:298-307`). When a tool is in the deny list, the harness refuses the tool call entirely; the hook system is not consulted.
- **Hook dispatch is post-authorization:** Per Claude Code's hook architecture, hooks fire **after the tool is authorized** and **as part of the tool's execution**. If the tool is denied, the hook never runs.

**Implication for fallback strategy:**

- If `EnterWorktree` is denied (via `permissions.deny: ["EnterWorktree"]`), the hook infrastructure does not even run; the tool call fails at authorization.
- If the hook is installed but Claude Code version does not support it, the tool call proceeds (authorized) but the hook does not fire; Claude falls back to default git worktree behavior.
- **Therefore:** hook and deny are a choice, not a belt-and-suspenders pair. Fallback detection must choose one or the other based on harness capability.

---

### 4. Existing disclosure pattern for integration-level fallbacks

**Plugin install fallback is the closest precedent** (disclosure.go:26-45):

- Niwa attempts plugin auto-install during apply.
- If skipped (user opt-out or filesystem error), `EmitPluginNotice(NoticeIDPluginSkipped, manualCmd, reporter)` is called.
- The notice includes a copy-paste command for manual install.
- The emit is not gated by `noticeDisclosed`; plugin install status is checked on every apply.

**Difference:** Plugin skipping is a state-dependent outcome (checked each run), not a one-time setup fact. Fallback detection (harness version) is also state-dependent (the Claude Code version can change between applies), so skipping the one-time-notice pattern may be appropriate.

---

## Options

### Option A: Assume-supported (no probe, no fallback)

**Action:** Skip version detection entirely. Install hooks on every apply and assume Claude Code honors them. If a user's harness doesn't support hooks, niwa's hooks simply don't fire; the user must manually use `niwa worktree create` when `EnterWorktree` creates a bare git worktree instead.

**Pros:**
- Zero apply-time latency cost
- Simplest implementation (no version parsing, no fallback machinery)
- Hooks are "best-effort" — graceful degradation is acceptable

**Cons:**
- Silent degradation (the problem the fallback is meant to solve per PRD R7/R8)
- Users don't know why `--worktree` suddenly produces a bare worktree instead of a niwa worktree
- No disclosure that the feature is unavailable

**Viability:** Does not meet PRD R7 (fallback must be detectable and disclosed).

---

### Option B: Apply-time version probe

**Action:** At apply time (before materializers run), shell out `claude --version`, parse the version string, and compare against minimum version (v2.1.183 or TBD). If the version is too old or the probe fails, set a flag (`HarnessSupportsFallbackHooks` or similar) on the Applier. Conditionally:
- If supported: install hooks normally (status quo).
- If not supported: write deny entries for `["EnterWorktree","ExitWorktree"]` instead and emit a one-time notice directing users to `niwa worktree create`.

**Pros:**
- Exact knowledge of harness capability
- Can make a choice at apply time (hook vs deny) based on real capability
- Transparent to the user: notice tells them why native `--worktree` is blocked

**Cons:**
- Adds latency to every apply (subprocess overhead for version parse)
- Brittle version parsing (format changes, pre-release variants, etc.)
- Minimum version is empirically-determined by spike but not formally specified
- If `claude` is not in PATH, probe fails; fallback strategy must handle missing-tool case
- Couples niwa to Claude Code release schedule (must update min version if hooks behavior changes)

**Implementation sketch:**
```go
// In apply.go Applier.runPipeline or Applier.Apply, before materializers:
if !a.SkipVersionProbe {
    supported, err := probeClaudeVersionSupportsHooks(ctx)
    if err != nil {
        // Probe failed: log warning, assume supported (optimistic fallback)
        a.Reporter.Warn("could not detect Claude Code version; assuming hook support")
        supported = true
    }
    a.HarnessSupportsFallbackHooks = supported
}
```

Then in `SettingsMaterializer.buildSettingsDoc`:
```go
if !cfg.HarnessSupportsFallbackHooks {
    // Write deny instead of hooks
    doc["permissions"]["deny"] = []string{"EnterWorktree", "ExitWorktree"}
}
```

---

### Option C: Assume-supported with opt-out flag

**Action:** Install hooks by default. Add an init-time flag `--no-worktree-delegation` (or similar) that opts out of the entire feature, persisting as a boolean in `InstanceState` (mirroring `SkipGlobal`, `NoOverlay`). If the flag is set, skip hook install and don't attempt the fallback. When unset, install hooks and do not probe.

**Pros:**
- No apply-time latency or version probing
- Explicit user control (users who know their harness is old can opt out at init)
- Reversible via re-init
- Simple implementation (add flag to InstanceState, gate install in runPipeline)

**Cons:**
- Does not detect harness incapability; requires the user to know their version is too old
- If user upgrades Claude Code mid-workspace-lifetime, they don't automatically get the feature (must re-init with flag removed)
- No disclosure for users who don't opt out but have an old harness — they get silent degradation (the problem PRD R7/R8 is meant to solve)

**Viability:** Partially meets PRD R7/R8 if users are well-informed at init, but doesn't solve the silent-degradation case for mid-lifetime upgrades or users who don't know their version.

---

### Option D: Lazy one-time detection + disclosure

**Action:** Install hooks on every apply. On the first apply where a hook is used (i.e., a user attempts `--worktree` or `isolation: "worktree"`), observe whether the hook actually fired (e.g., by checking worktree path or branch name against what niwa would have produced). If the hook did not fire (fallback-to-bare-worktree detected), emit a one-time notice, write deny entries on the next apply, and record the detection in InstanceState.

**Pros:**
- No apply-time version probe (zero latency when hooks are not used)
- Detects real-world incapability (not just version number)
- Disclosure is tied to actual failure, not speculated capability

**Cons:**
- Requires active use of `--worktree` to trigger detection (users who don't use worktrees never see the notice, even if feature is unsupported)
- Detection happens after the fact (user's first `--worktree` produces a bare worktree, then on the next apply it's blocked)
- Complex state machine (need to track "detection-pending", "fallback-observed", "disclosed")
- Observing hook success/failure requires examining the resulting worktree (brittle; could fail due to timing or other reasons)

**Viability:** Meets PRD R7/R8 for users who actually use worktrees, but leaves a gap for users who don't yet.

---

## Recommendation

**Option B (apply-time version probe)** is the best balance for niwa's apply-time context.

**Rationale:**

1. **Transparent and user-friendly**: Disclosure happens at apply time, before the user tries to use the feature. They see a notice immediately if fallback mode is active.
2. **Zero runtime cost**: Version probe is one-time per apply; it's not in the critical path of every tool call or workspace operation.
3. **Matches existing patterns**: niwa already probes for things at apply time (vault connectivity, GitHub access, config source reachability). One more probe fits the model.
4. **Meets PRD R7/R8**: Fallback is both detectable (via probe) and disclosed (via one-time notice).
5. **Reversible**: If the user upgrades Claude Code, the next apply re-probes and automatically enables hooks again.
6. **Defer min version refinement**: The spike confirmed v2.1.183 works; ship with that as the baseline and gather feedback. If a later version breaks hooks, the probe can be updated in a patch.

**Implementation summary:**

- Add `probeClaudeVersionSupportsHooks(ctx context.Context) (bool, error)` in a new file (e.g., `internal/workspace/harness_compat.go`).
  - Runs `claude --version`, parses version, compares against v2.1.183.
  - Returns `(true, nil)` if version >= 2.1.183; `(false, nil)` if version < 2.1.183; `(false, err)` if probe fails (treat error as "assume supported" per graceful fallback).
- In `Applier.runPipeline`, call the probe before materializers.
- Store result in a new `Applier` field (`HarnessSupportsFallbackHooks bool`).
- In `SettingsMaterializer.buildSettingsDoc`, conditionally:
  - If supported: install hooks as designed (no change).
  - If not supported: write `permissions.deny: ["EnterWorktree", "ExitWorktree"]` instead.
- Add a one-time notice (`const noticeWorktreeFallbackActive = "worktree-fallback"`) that fires when fallback mode is active and has not been disclosed before.
- The notice text should explain: "niwa could not detect support for WorktreeCreate hooks in your Claude Code version. EnterWorktree is disabled. Use `niwa worktree create` to create worktrees. For details, see docs/guides/worktree.md or run `niwa help worktree`."

**Disclosure surface:** `Reporter.Log()` in `runPipeline` after probe completes (or deferred with `Reporter.Defer()` if less obtrusive).

**Alternative**: If the version probe adds unacceptable latency or complexity, fall back to Option C (opt-out flag at init) and document that users with Claude Code < v2.1.183 should use `--no-worktree-delegation` at `niwa init`.

---

## Summary

niwa has no existing harness version-detection code and would need to add `claude --version` probe. Hook and deny are mutually exclusive; fallback detection must choose one based on harness capability. Version probe at apply time (Option B) is recommended: it adds minimal latency, enables transparent disclosure via one-time notice, and meets PRD requirements for detectable & disclosed fallback. Minimum version is v2.1.183 per spike; exact minimum for general hook support should be confirmed with Claude Code team if not already specified.
