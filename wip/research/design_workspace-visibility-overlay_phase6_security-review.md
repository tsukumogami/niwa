# Security Review: workspace-visibility-overlay (Phase 6)

Reviewer analysis of the Security Considerations section from the design document.

---

## Attack Vectors Not Considered

### 1. Symlink injection via overlay clone contents

The design validates path fields in `ParseOverlay()` against absolute paths and `..` components, and `checkContainment()` in `content.go` uses `resolveExistingPrefix()` to handle symlinks in the target tree. However, the overlay clone directory itself is under the attacker's control. A malicious overlay can place a symlink inside the clone directory (e.g., `hooks/evil.sh -> /etc/passwd`) and the hook resolution path `filepath.Join(overlayDir, scriptPath)` would resolve to the symlink target before any containment check runs. The design specifies that `ParseOverlay()` validates script paths are relative but says nothing about whether `MergeWorkspaceOverlay()` then calls `checkContainment` on the resolved absolute path. The existing `HooksMaterializer` skips containment checks for absolute paths (lines 77-79 of `materialize.go`), treating them as pre-validated by `MergeGlobalOverride`. If `MergeWorkspaceOverlay()` resolves overlay hook paths to absolute in the same way, the symlink bypass lands in the pre-validated bucket and containment is never re-checked. This needs explicit `checkContainment(resolvedAbsPath, overlayDir)` after the join, not assumed.

### 2. Content template variable injection

`InstallWorkspaceContent` and `InstallRepoContent` expand template variables (`{workspace}`, `{workspace_name}`, `{repo_name}`, `{group_name}`) in files before writing. These variables are derived from workspace state and config, not from the overlay. However, if `CLAUDE.overlay.md` is processed through the same `expandVars` path, a workspace whose `workspace_name` or `repo_name` contains characters meaningful in Claude instruction syntax (markdown headers, tool-call markers) could be used to produce different effective content than what a static read of the overlay file suggests. The design doesn't clarify whether `CLAUDE.overlay.md` is installed via `installContentFile` (which expands vars) or written verbatim. If it uses the same pipeline, an org with a crafted workspace name could amplify prompt injection.

### 3. TOML injection via overlay keys into merged WorkspaceConfig

`MergeWorkspaceOverlay()` is described as additive for sources/groups/repos. The design states "base wins on collision" for groups and repos, but for sources the merge is "append, with duplicate-org check." A malicious overlay that adds a new `[sources]` entry pointing to an attacker-controlled GitHub org would cause niwa to discover and clone repos from that org on the next `niwa apply`. This is not the same as executing code, but it widens the trust boundary by pulling in arbitrary repos into the workspace. The design does not identify this as a distinct risk. It is different from hook execution but qualifies as a new attack surface: org squatting on the discovery side rather than the overlay side.

---

## Mitigations That Are Insufficient

### 4. Supply chain: "empty repo fails loudly" does not address intentional squatting

The design correctly notes this in the body but the mitigation list only partially addresses it. Mitigation 2 (commit SHA pinning) is listed as the "primary compensating control" for the ongoing discovery-on-apply window, but it only helps after init. Before the first `niwa init` completes against a workspace whose overlay namespace has been squatted, there is no pinned SHA to compare against. The window between the org publishing `dot-niwa` and executing `niwa init` (including any time between when the squatter creates the repo and when the user runs init) is unprotected. The design acknowledges this trade-off but does not recommend any user-facing signal during init that would allow the user to notice the squatted overlay URL. Mitigation 1 (print discovered URL at init) is necessary but not sufficient: users cannot verify a URL is legitimate from a name alone. A recommendation to verify the overlay repo's owner and commit history before proceeding would add meaningful friction against squatting.

### 5. Data exposure: CheckGitignore scope is too narrow

The design identifies that `settings.local.json` can receive promoted env vars and that `CheckGitignore` only covers `CLAUDE.local.md`. The mitigation proposed ("extend the check or document the risk") is underspecified. Looking at the actual `CheckGitignore` implementation in `content.go` (lines 152-174), it checks for the literal string `*.local*` as a complete line. `settings.local.json` matches `*.local*` by glob semantics, so if the user has the correct gitignore pattern, they are protected. The real gap is that `CheckGitignore` is called only in `InstallRepoContent` (line 127), which runs only when content files are installed. If a repo has no CLAUDE content configured but does have `claude.env.promote` (written to `settings.local.json`), `CheckGitignore` is never called for that repo. The mitigation should be: call `CheckGitignore` whenever `settings.local.json` is written, not only when CLAUDE content is installed.

---

## "Not Applicable" Justifications That Are Actually Applicable

### 6. Permission scope rated MEDIUM in the design, assessed as LOW in the earlier phase5 analysis

The design rates permission scope as MEDIUM. The phase5 security analysis rates it as LOW. The discrepancy centers on the concurrent apply race. The phase5 analysis characterizes the race impact as "loud parse error rather than silent corruption," which is correct for the config read path. However, a concurrent `git pull` during `git clone` into the same `overlayDir` can leave the directory in a partially-initialized state (`.git/` exists but HEAD is missing or corrupt). If a second process sees `.git/` exists (via the `os.Stat(filepath.Join(targetDir, ".git"))` check in `clone.go` line 41), it skips cloning and proceeds directly to sync via `git pull --ff-only`. A partially-initialized clone directory could cause `git pull` to fail in ways that are not parse errors — they are git errors that may or may not surface clearly to the user. The phase5 analysis's LOW assessment may be correct for reliability, but it understates the user-experience impact. The design's MEDIUM is the more defensible rating.

---

## Residual Risk That Should Be Escalated

### 7. Convention discovery combined with no user confirmation creates a novel consent model

All existing niwa features that can execute code or inject Claude instructions require explicit user action tied to the specific source: `niwa init --from <url>` establishes consent to that URL's workspace config, and `niwa global register` establishes consent to the global config. Convention discovery creates a third consent pathway where executing code (via hooks in `workspace-overlay.toml`) and injecting Claude instructions (via `CLAUDE.overlay.md`) can result from a URL the user never explicitly specified. The user consented to `acmecorp/dot-niwa`; they did not consent to `acmecorp/dot-niwa-overlay`. The design's framing that "init with --from establishes user intent" does not extend cleanly to the convention-derived URL.

This is worth escalating because the three listed mitigations (print URL, pin SHA, proactive creation recommendation) reduce the risk but do not restore the explicit-consent model. A more conservative alternative — require explicit `niwa overlay set <url>` with no convention discovery — would match the consent model of `niwa global register`. The design documents that this trade-off is accepted for v1, but the escalation path if post-launch squatting is observed should be specified: what is the rollback plan? Disabling convention discovery in a patch release would break all users relying on it. The design should state this consequence explicitly so the decision to accept the risk is made with full knowledge of the recovery cost.
