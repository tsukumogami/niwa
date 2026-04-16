# Security Review: workspace-visibility-overlay

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes — HIGH severity

The overlay is a git repository cloned from a user-specified or convention-derived URL. Its `workspace-overlay.toml` specifies file paths, env var names, hook scripts, and content file paths. Several controls exist or are proposed:

**Controls in place or specified:**
- `ParseOverlay()` rejects absolute paths and `..` components in all path fields (files, env.files, content overlay paths). This mirrors the existing `validateGlobalOverridePaths()` and `validateContentSource()` logic, which are already exercised in production.
- `MergeWorkspaceOverlay()` resolves hook script paths to absolute paths within the overlay clone directory before the merged config is used by downstream materializers. The `HooksMaterializer` treats absolute paths as pre-validated (line 77-79 of `materialize.go`), so anchoring to the clone directory is the correct containment approach.
- The existing `checkContainment()` function (with symlink-safe `resolveExistingPrefix`) is reused for source file validation at content installation time.

**Residual risks:**

1. **Hook script execution**: A malicious overlay can declare `[hooks]` entries that execute arbitrary shell commands. This is the same residual risk as GlobalOverride. The design correctly notes this is user-initiated — the user ran `niwa init --from <url>`, making the overlay URL their explicit input. However, convention discovery (see Supply Chain below) can introduce overlay repos the user did not explicitly request, which does not carry the same user-intent justification.

2. **Files mappings write scope**: The `[files]` field in the overlay maps source files from the overlay clone to destination paths within the workspace instance root. `MergeWorkspaceOverlay()` calls `ParseOverlay()` path validation before merge, but the write destination is constrained only by the per-file path validation — it is not constrained to a specific subdirectory of the instance root. A `files` entry could write to `{instanceRoot}/.claude/settings.json`, overwriting the generated settings. The base-wins merge semantics mitigate this for fields that exist in the base config, but the `files` map uses per-key semantics where the base wins per key only if that key already exists in the base. An overlay-only key (new file mapping) is additive and executes unconditionally.

   **Mitigation:** Add a containment check on all write destinations for files declared in overlay `[files]` blocks, anchoring destinations to non-sensitive subdirectories (e.g., reject targets that start with `.claude/` or `.niwa/`). Document this constraint in `ParseOverlay()` validation.

3. **CLAUDE.overlay.md content injection**: `InstallOverlayClaudeContent` copies `CLAUDE.overlay.md` from the overlay clone into the instance root. This file is then processed by Claude Code as instruction content. A malicious overlay could inject instructions that alter Claude's behavior for all repos in the workspace. This is a prompt injection vector at the workspace level, not just file system access. The existing `InstallGlobalClaudeContent` carries the same risk from the global config layer — but that layer requires explicit `niwa global register` (user intent). Convention-discovered overlays do not require the same explicit step.

   **Mitigation:** Document that `CLAUDE.overlay.md` content is treated as instructions and is therefore subject to the same trust requirements as hook scripts. Add a note in the design that this content runs at the same trust level as GlobalOverride content, not at a lower level.

4. **Path validation completeness**: The design specifies path validation on `files`, `env.files`, and content `overlay` paths. It does not explicitly mention validating the `hooks.scripts` paths in `ParseOverlay()`. The `MergeWorkspaceOverlay()` resolves hook script paths to absolute paths within `overlayDir`, which provides containment — but only if the absolute path join is not bypassed. Confirm that `filepath.Join(overlayDir, scriptPath)` followed by `checkContainment` is applied consistently, and that `ParseOverlay()` validates that script paths are relative (not absolute) before `MergeWorkspaceOverlay()` computes the join.

**Severity of residual risk after mitigations:** LOW for explicitly-specified overlay URLs; MEDIUM for convention-discovered overlays (see Supply Chain).

---

### Permission Scope

**Applies:** Yes — LOW severity

**Filesystem writes:**
- `$XDG_CONFIG_HOME/niwa/overlays/<derived-name>/` — new directory, same scope as the existing global config clone at `$XDG_CONFIG_HOME/niwa/global/` (not yet in codebase but same pattern).
- Workspace instance root (`.niwa/`, CLAUDE files, hook scripts, env files) — same as current apply pipeline.

**Network access:**
- `git clone <overlayURL>` and `git pull --ff-only origin` — same network scope as cloning workspace repos and syncing global config. No new network permissions.

**No privilege escalation:** All writes are within user home directory scope. No setuid, no sudo, no privileged paths. The design does not propose any mechanism that would exceed the permissions the niwa process already holds.

**Concurrency note:** The design acknowledges shared clone sync contention (two concurrent `niwa apply` on the same overlay URL, no file locking). Without locking, a concurrent `git pull` into the same directory could produce a partially-updated overlay config that is then parsed mid-write. The impact is limited to corrupt TOML that fails loudly during `ParseOverlay()`. This is a reliability issue more than a security issue, but worth noting because a race window where one process pulls a malicious update while another reads the previous state could cause inconsistent behavior.

**Severity:** LOW. No privilege escalation; same filesystem scope as existing features.

---

### Supply Chain or Dependency Trust

**Applies:** Yes — MEDIUM to HIGH severity

**Convention-based discovery is the primary risk:**

When a user runs `niwa init --from acmecorp/dot-niwa`, niwa derives `acmecorp/dot-niwa-overlay` and attempts a clone. If `acmecorp` has not created that repo, the name is available for squatting by anyone with a GitHub account. A squatted overlay from an unrelated party would be silently cloned and applied.

The design's mitigation — "an empty repo with no `workspace-overlay.toml` fails loudly" — provides partial protection. A squatted repo with an empty or absent `workspace-overlay.toml` does fail loudly. However:

1. A squatter who wants to cause harm would create a valid `workspace-overlay.toml`, not an empty repo. The loud-failure mitigation does not protect against active squatting.

2. The silent skip on first-time failure (firstTime=true + 404/403 → skip) means the absence of the overlay repo is not visible to the user. If a team has never created `acmecorp/dot-niwa-overlay` and a squatter creates it after the team has been using niwa for months, the next `niwa apply` will silently adopt the squatted overlay without any user-visible indication.

3. Derivation is deterministic and public — the naming convention is documented, so any attacker who knows a team uses niwa can preemptively squat the overlay namespace.

**No cryptographic authenticity verification:** Unlike package managers with signed artifacts, git clone over HTTPS verifies TLS certificate chains but not repository content authenticity. There is no mechanism to pin a specific commit hash or verify a GPG-signed tag. Any commits pushed to the overlay repo after init will be applied on the next `niwa apply`.

**Mitigations to consider:**

1. **Commit pinning in state**: Store the HEAD commit SHA of the overlay at init time in `instance.json` (`OverlayCommit string`). On subsequent applies, warn (or hard-error) if the overlay has advanced past a verified commit. This converts the trust model from "latest HEAD is trusted" to "a specific commit was trusted at init time; subsequent advances require explicit re-trust." This is a significant improvement without requiring signed artifacts.

2. **Explicit opt-in for convention discovery**: Change convention discovery from automatic-with-silent-skip to explicit. If `OverlayURL` is not set in `instance.json`, don't attempt discovery on every `niwa apply` — only attempt it during `niwa init`. This reduces the attack window: squatting after init has no effect until the user explicitly re-inits or runs a future `niwa overlay set` command.

3. **URL display at init**: When convention discovery succeeds at init time, print the discovered overlay URL to stdout so the user sees what was adopted. Currently the design does not include this output (overlay URL is not in standard output). Silent adoption is the highest-risk aspect of convention discovery.

4. **Document proactive overlay repo creation**: As the design already notes, teams should create the overlay repo (even empty) before publishing `dot-niwa`. Add this to the `niwa init` output when convention discovery fails silently.

**Severity:** HIGH for convention-discovered overlays on public org/repo combinations where the overlay repo doesn't yet exist. MEDIUM for explicitly-specified overlay URLs (user intent established at init time, but no commit pinning).

---

### Data Exposure

**Applies:** Yes — LOW severity (with one notable exception)

**What is accessed:**
- The overlay `workspace-overlay.toml` and content files are read from the overlay clone directory and processed in memory.
- Env vars declared in overlay `[env]` blocks are resolved and written to `.local.env` files within the instance — same as existing env materialization.
- `CLAUDE.overlay.md` content is written to the instance root and read by Claude Code at runtime.

**What is transmitted:**
- `git clone` and `git pull` expose the local machine's IP address and git client fingerprint to the overlay repo's hosting service (typically GitHub). This is identical to existing repo clone behavior and not new.
- The overlay URL stored in `instance.json` is a local file. It is not transmitted anywhere by niwa itself.

**Notable exception — env var promotion:**
If the overlay declares `[claude.env] promote = ["SECRET_KEY"]`, and that key exists in the workspace's resolved env pipeline, the overlay causes a secret to be written into `settings.local.json` inside every cloned repo. This is the same risk as the existing `claude.env.promote` feature in GlobalOverride — but GlobalOverride requires explicit registration. A convention-discovered overlay that promotes sensitive env vars from the workspace env pipeline would write those vars into repo-level settings files, potentially committing them to version control if the user doesn't have `*.local*` in `.gitignore`.

The design's `CLAUDE.local.md` gitignore check (`CheckGitignore`) only warns about content files, not `settings.local.json`. The existing `localRename()` convention ensures hook scripts and env files get `.local` in their name, but `settings.local.json` already has `.local` in its name. The gitignore warning for sensitive env promotion should cover `settings.local.json` as well.

**Severity:** LOW overall, MEDIUM for the env var promotion case in convention-discovered overlays.

---

## Recommended Outcome

**OPTION 2 - Document considerations:**

The design's security analysis is largely correct in identifying and characterizing the risks, but it underweights the convention discovery supply chain risk. The following Security Considerations section replaces the draft section in the design:

---

**Security Considerations**

**External artifact handling — HIGH relevance**

The overlay clone is a git repository cloned from a user-specified or convention-derived URL. Its `workspace-overlay.toml` specifies file paths, env var names, hook scripts, and content file paths. `ParseOverlay()` rejects absolute paths and `..` components in all path fields, mirroring the existing `validateGlobalOverridePaths()` and `validateContentSource()` implementations. Hook script paths in `[hooks]` entries must be validated as relative before `MergeWorkspaceOverlay()` resolves them to absolute paths within the overlay clone directory; this should be explicit in `ParseOverlay()` rather than relied upon implicitly.

Files declared in overlay `[files]` blocks with destination paths that target sensitive directories (`.claude/`, `.niwa/`) would overwrite generated artifacts. The base-wins merge only protects keys already present in the base config; additive overlay keys execute unconditionally. A destination containment check anchored away from `.claude/` and `.niwa/` should be added to `ParseOverlay()`.

`CLAUDE.overlay.md` is injected into the instance root as instruction content for Claude Code. A malicious overlay can use this file to alter Claude's behavior across all repos in the workspace. This is the same risk as `CLAUDE.global.md` from the global config layer, but that layer requires explicit `niwa global register`. Convention-discovered overlays do not require the same explicit user step, making prompt injection via `CLAUDE.overlay.md` a higher residual risk than the design currently acknowledges.

Residual risk after all mitigations: hook scripts and `CLAUDE.overlay.md` can execute or inject arbitrary instructions. Same residual as GlobalOverride for explicitly-specified overlay URLs. Higher residual for convention-discovered overlays.

**Supply chain or dependency trust — HIGH relevance**

Convention-based URL derivation (`<base>-overlay`) introduces a squatting vector. A team that publishes `acmecorp/dot-niwa` without creating `acmecorp/dot-niwa-overlay` leaves the overlay namespace open to any GitHub user. A squatter who creates a valid `workspace-overlay.toml` will have it silently cloned and applied on the next `niwa apply` after squatting occurs. The design's "empty repo fails loudly" mitigation only addresses accidental name collisions — it does not protect against an attacker who intentionally creates a valid overlay.

The discovery behavior compounds this: overlay URL adoption is not printed to stdout, so users have no visible signal that a convention-discovered overlay was adopted or changed.

Three mitigations are recommended before this feature reaches stable:

1. Print the convention-discovered overlay URL to stdout at init time, so users see what was adopted.
2. Restrict convention discovery to `niwa init` only. Subsequent `niwa apply` calls should only use the `OverlayURL` stored in `instance.json`, never re-attempt discovery. This prevents post-init squatting from taking effect without user action.
3. Store the overlay HEAD commit SHA in `instance.json` at init time (`OverlayCommit`). On subsequent applies, warn when the overlay has advanced. This converts the trust model from implicit HEAD-is-trusted to explicit re-trust of advances.

**Permission scope — MEDIUM relevance**

Requires filesystem write to `$XDG_CONFIG_HOME/niwa/overlays/` and workspace instance root. Same scope as GlobalOverride. Concurrent `niwa apply` calls on the same overlay URL have no file locking, creating a narrow race window where one process reads a partially-updated config mid-pull. The impact is a loud parse error rather than silent corruption. No privilege escalation.

**Data exposure — LOW relevance**

Overlay file contents are processed in memory and written only to the workspace instance root. The overlay URL is stored locally in `instance.json` and not transmitted. One notable case: overlay `[claude.env] promote` can cause env vars from the workspace pipeline to be written into `settings.local.json` inside cloned repos. If a repo lacks a `*.local*` gitignore entry, these vars could be committed to version control. The existing `CheckGitignore` warning covers `CLAUDE.local.md` but not `settings.local.json` — extend the check or document this risk.

---

## Summary

The design is architecturally sound and consistent with existing patterns. The primary concern is convention-based overlay discovery: the silent adoption model (no stdout confirmation, discovery on every apply, no commit pinning) creates a supply chain risk that is meaningfully higher than the analogous global config layer, which requires explicit user registration. Three targeted mitigations — visible URL adoption at init, restricting discovery to init-time only, and commit SHA pinning in instance state — would bring the convention discovery risk to parity with GlobalOverride. The files-destination and hook-script path validation gaps are straightforward to close within `ParseOverlay()`.
