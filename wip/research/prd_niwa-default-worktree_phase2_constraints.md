# Findings: niwa Worktree Default Mechanism Constraints

## 1. When does `niwa apply` run, and what does it touch per repo?

**Apply lifecycle:** `niwa apply` (internal/workspace/apply.go:384–555) runs the full pipeline orchestrating repo discovery, classification, cloning, and content installation. It is invoked by the CLI and always runs against an existing instance.

**Per-repo materializer invocation:** The apply pipeline passes through Step 6.5 (apply.go:1299–1348), which calls `runRepoMaterializers` for every repo in the classified set. This function (worktree_content.go:64–132) runs the canonical four-materializer set:
- `HooksMaterializer` — installs hooks scripts from config directory
- `SettingsMaterializer` — installs settings.json (.local)  
- `EnvMaterializer` — installs .env files
- `FilesMaterializer` — installs arbitrary files from content templates

**Materialization is idempotent:** Each materializer is stateless and re-run on every apply (apply.go:1319: `runRepoMaterializers` receives `effectiveConfig` and repo context). The hooks materializer (materialize.go) copies scripts idempotently from source to destination; re-running produces the same result.

**Key constraint for hooks default installation:** Hooks are installed by the HooksMaterializer for EVERY repo on every apply run, provided claude=true for that repo (worktree_content.go:118). When niwa apply becomes default for agents, the hooks installed on apply will need to be compatible with agent execution — no assumption of interactive shell, TTY, or local git state.

**Citation:** internal/workspace/apply.go:1299–1348 (Step 6.5 loop), worktree_content.go:64–132 (runRepoMaterializers), worktree_content.go:118–122 (claude-disabled skip).

---

## 2. What does `niwa worktree create` output today on success?

**Success output:** The command prints a machine-readable line to stdout: `session: created <sessionID> at <worktreePath>` (session_lifecycle_cmd.go:144). This is paired with per-file content lines: `session: content <path>` for each written file (session_lifecycle_cmd.go:145, printWorktreeContentFiles:326–330).

**Machine-readable path:** The worktree path is returned from `worktree.CreateSession` (called at line 117) and output verbatim at line 144. The path is absolute and fully qualified — no guessing needed downstream. The format is: `session: created <sessionID> at <worktreePath>`.

**Additional outputs:** After the path, the command may print:
- `session: content <path>` lines (one per written file from ApplyToWorktree)
- Warning lines (if vault sync warned)
- Shell init hints (hintShellInit, line 153)

**NOT machine-parseable:** The shell init hint and any warnings are free-form text. Only the `session: created` line provides a stable machine-readable API.

**Constraint for delegation:** The worktree path MUST be extracted from the `session: created <sessionID> at <worktreePath>` line by any caller that needs to delegate work. The command does not offer a `--json` flag or structured output alternative. If agents need the path for onward delegation, they must parse this line or the command must be extended with a `--json` output mode.

**Citation:** internal/cli/session_lifecycle_cmd.go:102–155 (runSessionCreate), line 144 (success output), line 326–330 (printWorktreeContentFiles), line 231 (options for default behavior).

---

## 3. Secret-resolution policy for worktrees

**Current policy:** `niwa worktree create/apply` uses `AllowMissingSecrets=true` (session_lifecycle_cmd.go:285). This is deliberate: worktree content installation calls `ResolveAndMergeEffectiveConfig` with this flag set.

**Rationale from code comment:** "A worktree apply is a localized re-materialization; the instance create/apply already enforced strict secret resolution at bootstrap. A transient vault outage during a worktree apply should warn-and-continue rather than hard-fail the worktree." (session_lifecycle_cmd.go:259–264)

**Trade-off:** 
- **Pro:** Tolerant to transient vault/credential provider outages; a worktree create doesn't fail if the machine can't reach the credential provider.
- **Con:** Missing secrets silently downgrade to empty MaybeSecret values, which may result in .env or other content files being incomplete. No guarantee that the workspace-level strict resolution is honored in the worktree.

**Implication for agent worktrees:** If agents become the primary worktree mechanism, the policy should be re-evaluated:
- **Strict (current for instance apply):** Hard-fail on missing secrets. Agent workflows cannot proceed without complete context.
- **Warn-and-continue (current for worktree apply):** Accept degraded context. Agents may operate on incomplete state but won't block.

The PRD must explicitly state which policy is acceptable when agents use worktrees. The current AllowMissingSecrets=true is appropriate for interactive users (allow recovery); agents may require strict=true to catch configuration errors early.

**Citation:** internal/cli/session_lifecycle_cmd.go:259–288 (applyContentToWorktree vault policy), line 285 (AllowMissingSecrets: true), workspace/apply.go:384–555 (instance apply uses AllowMissingSecrets from Applier field, default false per line 44).

---

## 4. Idempotency / re-apply: Constraints on hook installation

**Idempotency property:** `workspace.ApplyToWorktree` (worktree_content.go:212–300) is fully idempotent by design:
- Rules import (installWorktreeRulesImport, line 323–351): rewrites `.claude/rules/worktree-imports.md` with absolute @import paths. Re-running re-points the same paths — no duplicate imports.
- Worktree-context layer (installWorktreeContextLayer, line 368–402): replaces the `## Worktree Context (niwa worktree)` section (delimited by stable heading). Re-running replaces in-place, not append.
- Repo content (InstallRepoContentTo, line 223): same handlers as instance apply; idempotent by construction.
- Materializers (runRepoMaterializers, line 244–261): each materializer (hooks, settings, env, files) is idempotent. HooksMaterializer copies scripts; re-running produces the same files.

**Hook script re-run behavior:** Hooks are executed at the end of worktree apply (runWorktreeHooks, line 295–297). On re-apply, the same hook scripts are re-installed (unchanged by idempotency) and re-executed. Hook scripts must be designed to tolerate re-execution — a script that increments counters, appends to logs, or has side effects will produce different results on the second run.

**Constraint for default hook installation on apply:** If hooks become the default-installation mechanism (run on every `niwa apply` and every worktree apply), hook scripts MUST be idempotent:
1. Hooks that modify state (write counters, append logs) will re-run and duplicate work.
2. Hooks that run external commands with side effects (git commits, API calls) will repeat those effects.
3. The current worktree-hook model warns non-executable scripts (worktree_content.go:505–509) but does not enforce idempotency.

The PRD must define hook idempotency expectations. Either:
- Hooks are run only on worktree create, not re-apply (skip on idempotent re-run).
- Hooks are designed to be idempotent (the operator's responsibility, documented).
- The hook framework detects re-runs and skips previously-executed scripts (complex, requires state tracking).

**Citation:** worktree_content.go:212–300 (ApplyToWorktree structure), line 323–351 (idempotent rules import), line 368–402 (idempotent context layer), line 295–297 (hook execution), workspace/materialize.go (HooksMaterializer idempotent copy), worktree_content.go:478–530 (runWorktreeHooks: non-executable warning, re-run on every apply).

---

## Implications for PRD Requirements

1. **Hook installation timing:** `niwa apply` runs hooks materializer on every apply, on every repo (when claude=true). Hooks are discovered and installed idempotently. **Requirement:** Document that hooks installed on worktrees are the same class of scripts as instance hooks; they are re-run on every `niwa worktree apply`, and operators must design them to be idempotent (or the feature must distinguish create-only vs. re-apply behavior).

2. **Worktree path delivery:** `niwa worktree create` outputs the path as plain text in the `session: created <id> at <path>` line. **Requirement:** When agents delegate worktree creation, they must parse this line OR the CLI must offer a `--json` / structured output mode for machine consumption. The current text output is suitable for shell integration (the shell wrapper reads the path) but not for programmatic delegation without regex/text parsing.

3. **Secret resolution trade-off:** Worktree apply uses `AllowMissingSecrets=true` (warn-and-continue) vs. instance apply's default strict resolution. **Requirement:** The PRD must explicitly choose the policy for agent worktrees. If agents require complete context, this policy must change to strict=true (hard-fail on missing secrets). If agents can tolerate degraded context, the current warn-and-continue is acceptable but should be documented as intentional degradation.

4. **Idempotency guarantee:** Content installation is idempotent; hooks are idempotent if operators design them that way. **Requirement:** The PRD must document the idempotency contract for hooks (re-run on every apply, must tolerate re-execution) and clarify whether hook scripts should be run only on create or re-run on every apply when niwa apply becomes the agent default.

---

## Summary

Exploration confirms: (1) `niwa apply` runs materializers on every repo on every apply cycle via the instance pipeline; hooks are installed by HooksMaterializer idempotently (Step 6.5). (2) `niwa worktree create` outputs the path as plain text (`session: created <id> at <path>`); no structured alternative exists. (3) Worktree secret resolution uses `AllowMissingSecrets=true` (warn-and-continue), a deliberate trade-off vs. instance apply's strict resolution; PRD must choose policy for agents. (4) Content and hooks are idempotent but hook scripts must be designed to tolerate re-execution; PRD must clarify whether hooks run only on create or re-apply.
