# Security Review: global-config

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes

The design introduces a second external artifact source: a user-owned GitHub-backed TOML file cloned to `$XDG_CONFIG_HOME/niwa/global/`. This is parallel to the existing workspace config, but with distinct risks:

**Git pull via shell exec.** `SyncConfigDir()` runs `git pull --ff-only origin` via `exec.Command`. The global config repo is controlled by the user, so the pull target is trusted to the same degree as the workspace config. No new command injection surface exists — `configDir` is a fixed local path derived from `XDG_CONFIG_HOME`, not from TOML field values.

**CLAUDE.global.md verbatim copy.** `InstallGlobalClaudeContent` copies `CLAUDE.global.md` verbatim from the cloned directory to the instance root, then injects an `@import` directive. The file is treated as plain text; no template expansion or execution occurs. The risk is content injection (malicious Claude instructions) rather than code execution. This risk exists identically for the workspace CLAUDE.md, so no new attack class is opened.

**`GlobalConfigOverride` TOML parsing.** The design calls for a new `ParseGlobalConfigOverride` function. The design document explicitly states that path-traversal checks must be applied to the `files` map at parse time, consistent with how `validate()` does it for workspace config. However, the design does not yet exist as code, so the check is a documented requirement rather than an implemented control.

**Gap identified — parse-time path validation is required but unimplemented.** The existing workspace config enforces path safety inside `validate()` via `validateContentSource()`. The `GlobalConfigOverride` parser must apply equivalent checks on `files` destination values. If omitted, a compromised or misconfigured global config repo could write files to arbitrary paths on the filesystem through the `FilesMaterializer` (which calls `checkContainment` at materialize time, but that is a second line of defense, not a substitute for parse-time rejection).

**Severity:** Medium. The `FilesMaterializer` already calls `checkContainment` on destination paths at materialize time (lines 604, 666 in `materialize.go`), so the runtime guard exists. The risk is that a missing parse-time check delays error detection and may confuse users with opaque materializer errors instead of a clear config validation failure.

**Mitigation:** `ParseGlobalConfigOverride` must call a path validation function on each value in the `files` map before returning, using the same logic as `validateContentSource`. This should be a unit-test-covered requirement in Block 1.

**Hook script source containment.** Hook scripts referenced in global config are resolved relative to `globalConfigDir` by `HooksMaterializer`. The existing `checkContainment(src, ctx.ConfigDir)` guard (line 78 of `materialize.go`) prevents hook scripts from reading files outside the config directory. When global hooks are merged into the intermediate `WorkspaceConfig`, `ctx.ConfigDir` must point to `globalConfigDir` for those entries, not the workspace config dir, or the containment check will either reject legitimate global hook scripts or be skipped. The design does not address how `ctx.ConfigDir` is set when hooks from two different config directories are merged into a single `WorkspaceConfig`.

**Gap identified — hook script source directory ambiguity.** After `MergeGlobalOverride`, the intermediate `WorkspaceConfig` contains hooks from both the workspace config repo and the global config repo. When `HooksMaterializer` resolves hook script paths using `ctx.ConfigDir`, it uses a single directory. If `ctx.ConfigDir` is the workspace config dir, global hook scripts cannot be found. If the design resolves this by rewriting global hook paths to absolute paths at merge time, that would bypass `checkContainment`. The design document does not specify the resolution.

**Severity:** Medium. Without a clear resolution, implementations may either silently drop global hooks, use absolute paths (bypassing containment checks), or fail at apply time with confusing errors.

**Mitigation:** The design should specify explicitly how hook script paths are resolved when two config directories are in play. The cleanest option is to resolve global hook script paths to absolute paths at merge time and remove the `checkContainment` src-side check only for pre-resolved absolute paths, while preserving destination containment checks. Alternatively, keep global hooks separate from workspace hooks in the intermediate struct and run two separate materialize passes with different `ctx.ConfigDir` values. Either approach must be specified and tested.

---

### Permission Scope

**Applies:** Yes

**Filesystem permissions.** The feature writes to:
- `$XDG_CONFIG_HOME/niwa/global/` (clone destination, created during `niwa config set global`)
- `$XDG_CONFIG_HOME/niwa/config.toml` (registration storage, updated by `SaveGlobalConfig`)
- Instance root and per-repo directories (file materialization, CLAUDE.global.md copy)

All writes are to user-owned paths. No escalation risk exists; no new directories are created outside `$XDG_CONFIG_HOME` or the workspace instance root.

**`os.RemoveAll` on unregistration.** `runConfigUnsetGlobal` calls `os.RemoveAll(cloneDir)`. If `LocalPath` in the stored `GlobalConfigSource` is tampered with (e.g., via direct edit of `config.toml`), `RemoveAll` could delete an arbitrary directory. However, `config.toml` is a user-owned local file, and a user who edits it can already cause arbitrary harm to their own filesystem. This is not an escalation.

**Network permissions.** The design performs network operations (git clone and git pull) to a user-supplied GitHub repo URL. The URL is stored in `config.toml`, not interpreted as a shell command, so there is no injection risk beyond what git itself enforces. No credentials are stored by niwa; git's credential helper handles auth.

**Severity:** Low. No new permission classes are required; all operations are bounded to user-owned filesystem paths and user-controlled network endpoints.

---

### Supply Chain or Dependency Trust

**Applies:** Yes, with the same trust model as workspace config

The global config repo is a user-owned GitHub repository. The user explicitly registers it via `niwa config set global <repo>`, and subsequent applies sync it with `git pull --ff-only`. The trust model is identical to the workspace config repo: niwa trusts the registered repo's HEAD.

**No new binary or plugin dependencies.** The global config TOML supports hooks (shell scripts), env vars, files, and Claude config. It does not introduce a mechanism to specify new Go dependencies, compiled binaries, or plugin URLs. The `plugins` field in `GlobalOverride` names Claude Code plugins by identifier; it does not download them.

**Hook script provenance.** Global config hooks are shell scripts committed to the user's repo. A compromised global config repo (e.g., via a hijacked GitHub account or supply chain attack on the repo) would allow arbitrary shell execution at apply time. This risk is identical to the workspace config repo and is documented in the design's Security Considerations section. The threat model explicitly requires user ownership of the registered repo.

**Sync failure abort.** The design correctly specifies that global config sync failure aborts apply rather than falling back to a cached state. This prevents a "remove the repo and fall back silently" attack where an attacker deletes the repo to cause silent fallback to a less-secure config state.

**Severity:** Low (same as existing workspace config). The feature does not expand the supply chain trust surface relative to what already exists.

**Mitigation:** No new mitigations are required. Existing guidance that users should review their global config repo's commit history applies.

---

### Data Exposure

**Applies:** Yes

**Env vars in TOML.** The `GlobalOverride.Env.Vars` field allows inline env var values in the global config TOML. Since this file lives in a GitHub repo, committing secrets to it would expose them publicly if the repo is public. This is a user responsibility issue, not a niwa vulnerability, but documentation should warn against storing secrets inline.

**Env var logging.** The design notes that apply must not print env var values. This constraint already applies to workspace config. The global config layer uses the same `EnvMaterializer` and `resolveClaudeEnvVars` paths, which do not log values. The constraint must be verified for any new error paths added in the global sync or merge steps.

**Machine-level config.** `~/.config/niwa/config.toml` will store the registered repo URL under `[global_config]`. The URL itself is not sensitive in most contexts (a GitHub repo URL is typically not a secret), but it does reveal what personal config repo a user has registered. On shared machines, other local users with read access to the home directory could discover this URL.

**`config.toml` file permissions.** The existing `SaveGlobalConfigTo` function creates the config file with default OS permissions (no explicit mode argument to `os.Create`). On most Unix systems, files created without explicit mode inherit `0o644` minus umask, making them world-readable. The registered repo URL and any other future `GlobalConfig` fields would be readable by other local users.

**Gap identified — config file permissions.** The design does not specify that `config.toml` should have restricted permissions. Given that future versions of `GlobalConfig` might store more sensitive data (e.g., auth tokens, if niwa ever adds them), establishing `0o600` permissions now is a prudent baseline. The current implementation uses `os.Create`, which does not set restricted permissions.

**Severity:** Low. The repo URL is not sensitive. However, the file permission pattern should be established correctly now to avoid a harder migration later.

**Mitigation:** `SaveGlobalConfigTo` should use `os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)` instead of `os.Create(path)`. This is a low-effort change with long-term defensive value.

---

## Recommended Outcome

**OPTION 2 - Document considerations:**

The design's existing Security Considerations section is accurate and covers the main risks. Two gaps require clarification in the design before implementation, and one code-level change is recommended.

**Additions to the Security Considerations section:**

---

**Hook script source directory resolution with two config repos.**
After `MergeGlobalOverride`, the intermediate `WorkspaceConfig` contains hooks sourced from both the workspace config repo and the global config repo. The `HooksMaterializer` resolves hook script paths relative to a single `ctx.ConfigDir`. The implementation must resolve how two different source directories are handled. The recommended approach is to resolve global hook script paths to absolute paths at merge time (during `MergeGlobalOverride`) so that materialize-time resolution requires no knowledge of which config directory a hook came from. The destination containment check (`checkContainment(targetPath, ctx.RepoDir)`) remains in place regardless. This choice must be documented in the `MergeGlobalOverride` function comment and covered by integration tests that verify global hook scripts are installed correctly.

**Parse-time path validation on `files` map.**
`ParseGlobalConfigOverride` must validate all destination values in the `files` map using the same path-traversal rejection logic as `validateContentSource` in `internal/config/config.go`. The `FilesMaterializer` performs runtime `checkContainment` checks, but parse-time rejection produces clearer error messages and catches issues before any disk writes occur. This validation must be implemented in Block 1 and covered by unit tests for both absolute paths and `..`-traversal attempts.

**`config.toml` file permissions.**
`SaveGlobalConfigTo` should create `config.toml` with mode `0o600` rather than default permissions. This restricts the registered repo URL (and any future fields) to the owning user on multi-user systems.

---

## Summary

The global-config design is architecturally sound and reuses existing patterns correctly. Two gaps need resolution before implementation: (1) how hook script paths from two different config directories are resolved after merging, since the materializer assumes a single `ctx.ConfigDir`; and (2) explicit parse-time path-traversal validation on the global `files` map, which the design describes as required but leaves to implementors. A minor improvement — restricting `config.toml` to `0o600` permissions — is also recommended. None of these issues require design changes; they are implementation-level constraints that should be documented as explicit requirements before Block 1 work begins.
