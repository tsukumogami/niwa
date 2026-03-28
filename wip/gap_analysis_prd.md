# Gap Analysis: niwa PRD vs Current Implementation

Generated: 2026-03-27

## Functional Requirements (R1-R13)

| Requirement | Status | Evidence / Gap Description |
|---|---|---|
| **R1: Global config and registry** | Implemented | `config/registry.go` stores global config at `~/.config/niwa/config.toml` following XDG conventions. Registry maps workspace names to source/root entries. Clone protocol setting with "https" default is present. |
| R1: NIWA_CONFIG env var override | Not implemented | PRD says the config path is "overridable via `NIWA_CONFIG` env var." Code only checks `XDG_CONFIG_HOME`, not `NIWA_CONFIG`. |
| R1: Default clone protocol "ssh" | Partially implemented | PRD specifies default is `ssh`. Code defaults to `"https"` in `CloneProtocol()`. The setting is present and functional, just the wrong default value. |
| **R2: Workspace config format** | Implemented | `config/config.go` parses TOML `workspace.toml` with workspace metadata, sources, groups, repos, content, and placeholder hooks/settings/env sections. Schema is additive. |
| R2: Version field | Partially implemented | `WorkspaceMeta.Version` field exists but is not validated or used for compatibility checking. PRD requires erroring on breaking schema changes. |
| **R3: Workspace roots and instances** | Implemented | `init` creates workspace roots. `create` creates instances with auto-numbering (`tsuku`, `tsuku-2`) and `--name` suffix support. `apply`, `reset`, `destroy` all work on instances. `.niwa/instance.json` marker directory with metadata (name, config path, creation time) is present. |
| R3: `niwa apply [instance]` positional arg | Partially implemented | Apply takes a workspace name positional arg (registry lookup) but instance targeting uses `--instance` flag, not a positional arg. PRD says `niwa apply tsuku-2` (positional). |
| **R4: Detached workspaces** | Partially implemented | `niwa init` (no args) scaffolds a local config. State supports `Detached: true`. However, running `niwa apply` in a directory with a `workspace.toml` directly (not under `.niwa/`) is not supported -- the code always looks for `.niwa/workspace.toml`. The PRD envisions `--config /path/to/workspace.toml` flag which doesn't exist. |
| **R5: CLAUDE.md hierarchy generation** | Implemented | Workspace-level `CLAUDE.md` in niwa-owned directories, group-level `CLAUDE.md` in group directories, and `CLAUDE.local.md` in repo directories. User content is appended via content source files. niwa never touches a repo's committed `CLAUDE.md`. |
| R5: Auto-generated boilerplate | Not implemented | PRD says auto-generate repo table, directory structure, visibility rules, scope defaults, and metadata headers. Current code only copies user-authored content files with variable substitution -- it does not generate any boilerplate content itself. The entire CLAUDE.md content comes from user-provided source files. |
| **R6: Template variable substitution** | Partially implemented | Variables are expanded at apply time. However, the variable syntax uses `{workspace}` (curly-brace) instead of `$WORKSPACE` (dollar-sign) as specified in the PRD. Additional variables `{workspace_name}`, `{group_name}`, `{repo_name}` are available beyond what the PRD specifies. Undefined variables are left unexpanded (matches PRD). |
| **R7: Init command** | Implemented | Three modes work: no-args scaffold, named local, named with `--from`. Registry lookup for previously registered names works. Conflict detection (existing workspace, orphaned `.niwa/`, inside instance) is present. |
| R7: Config file location | Partially implemented | PRD says `workspace.toml` at the workspace root directory. Implementation puts it at `.niwa/workspace.toml` (inside the state directory). This is a design deviation. |
| R7a: Remote config update (future) | Not implemented | Explicitly deferred in PRD. No `niwa update` command. Config format doesn't preclude it. |
| R7b: Per-host overrides (future) | Not implemented | Explicitly deferred in PRD. No hostname-keyed override support. |
| **R8: Adopt command (future)** | Not implemented | Explicitly deferred in PRD. No `niwa adopt` command. |
| **R9: Idempotent apply** | Implemented | Repos already cloned are skipped. Generated files are regenerated from current config. Removed repos' managed files are cleaned up. Removed empty group directories are cleaned up. Repo directories are never deleted. |
| R9: Continue on clone failure | Not implemented | Current code returns immediately on the first clone error (`return nil, fmt.Errorf("cloning repo %s: %w", ...)`). PRD requires continuing with remaining repos and reporting failures at the end. |
| **R10: State tracking** | Implemented | `.niwa/instance.json` records managed files with SHA-256 hashes, repo states, timestamps, instance metadata. Drift detection via `CheckDrift()` compares current hashes against recorded hashes. |
| **R11: Repo non-interference** | Partially implemented | `CheckGitignore()` checks for `*.local*` pattern. However, the check only emits a warning -- it does not fail/skip writing `CLAUDE.local.md`. PRD says niwa should **fail for that repo** by default (skip writing, report error). The file is written regardless of the gitignore check result. |
| R11: Never modify .gitignore | Implemented | No code writes to `.gitignore`. |
| **R12: Flat layout support** | Partially implemented | When no groups match, repos get "skipping" warnings and are not cloned. A config with repos but no groups would skip all repos. The PRD says `[[repos]]` with no group should place repos at the workspace root, but the current model requires groups for classification. |
| **R13: Status command** | Implemented | Detail view inside an instance shows repos, managed files, drift. Summary view at workspace root shows all instances. Positional instance name argument works. |
| R13: Drift reporting detail | Implemented | Status reports "cloned"/"missing" for repos and "ok"/"drifted"/"removed" for managed files. |
| R13a: List command (future) | Not implemented | Explicitly deferred in PRD. |
| R13b: Which command (future) | Not implemented | Explicitly deferred in PRD. |

## Non-functional Requirements (R14-R19)

| Requirement | Status | Evidence / Gap Description |
|---|---|---|
| **R14: Single binary, zero dependencies** | Implemented | Go binary with cobra. Only runtime dependency is git (used for clone and status checks). |
| **R15: Cross-platform** | Implemented | Pure Go code, no platform-specific dependencies. Build targets not verified but nothing prevents macOS/Linux arm64/amd64. |
| **R16: Bootstrap via GitHub Releases** | Not verified | No install script found in the repo. No `get.niwa.dev` domain setup visible. CI/release workflows not checked. |
| **R17: Self-update** | Not implemented | No `self-update` command exists. No code for downloading/replacing the binary. |
| **R18: Safe defaults** | Implemented | No secrets embedded. No permission bypassing. Environment references use variable syntax. |
| **R19: Config schema versioning** | Partially implemented | `version` field exists in config. Unknown fields produce warnings (forward compatibility). However, no version validation logic exists -- niwa doesn't error on breaking schema changes or warn about version mismatches. |

## Acceptance Criteria

| Criterion | Status | Notes |
|---|---|---|
| `niwa init my-project` scaffolds workspace.toml and registers | Implemented | Works via modeNamed path |
| `niwa init tsuku --from org/repo` pulls config and registers | Implemented | Works via modeClone path |
| `niwa init tsuku` (after prior registration) pulls from registry | Implemented | Registry lookup in resolveInitMode |
| `niwa create` creates first instance (no suffix) | Implemented | computeInstanceName logic |
| `niwa create` again creates numbered instance | Implemented | NextInstanceNumber + suffix |
| `niwa create --name hotfix` creates named instance | Implemented | --name flag support |
| `niwa apply tsuku-2` re-applies to specific instance | Partially | Uses `--instance` flag, not positional arg for instance name |
| `niwa apply` inside instance targets current instance | Implemented | DiscoverInstance walk-up |
| `niwa reset` destroys and recreates | Implemented | Full destroy + create cycle |
| `niwa destroy` removes instance | Implemented | With uncommitted changes check |
| `niwa status` at root shows summary | Implemented | showSummaryView |
| `niwa status` inside instance shows health | Implemented | showDetailView |
| `.niwa/` marker with instance metadata | Implemented | instance.json with schema |
| `niwa init` no args scaffolds detached workspace | Implemented | modeScaffold path |
| `NIWA_CONFIG` env var override | Not implemented | Not referenced in code |
| `niwa destroy` then `niwa status` no longer lists | Implemented | Directory removal means enumeration skips it |
| `niwa reset` doesn't affect other instances | Implemented | Only targets resolved instance |
| Detached mode works without registry | Partially | Scaffolding works but apply path requires `.niwa/workspace.toml` |
| CLAUDE.md at workspace root with auto-generated + user content | Partially | User content only; no auto-generated repo table or structure |
| CLAUDE.md in group directories | Implemented | InstallGroupContent |
| CLAUDE.local.md in repo directories | Implemented | InstallRepoContent |
| Never modifies repo's committed CLAUDE.md | Implemented | Only writes CLAUDE.local.md in repos |
| Warns if .gitignore missing `*.local*` | Implemented | CheckGitignore emits warning |
| Fails/skips if .gitignore missing (default) | Not implemented | Writes file regardless, only warns |
| Never modifies .gitignore | Implemented | No gitignore write code |
| `git status` clean after apply | Partially | Depends on gitignore coverage; niwa doesn't verify post-apply |
| Double apply produces no changes | Implemented | Files regenerated identically, repos skipped |
| Adding repo and re-applying clones only new repo | Implemented | Existing repos skipped by Cloner |
| Removing repo cleans managed files, leaves directory | Implemented | cleanRemovedFiles + no directory deletion |
| Status identifies missing repos | Implemented | findRepoDir check |
| Status identifies drifted CLAUDE.md | Implemented | CheckDrift comparison |
| Flat layout (no groups) | Not implemented | Repos without group matches are skipped with warning |
| `$WORKSPACE` template variable | Partially | Uses `{workspace}` syntax instead of `$WORKSPACE` |
| `niwa self-update` | Not implemented | Command doesn't exist |
| Apply continues on unreachable URL | Not implemented | Returns on first clone error |
| Malformed workspace.toml gives clear error | Implemented | TOML parse errors propagated |
| Skips CLAUDE.local.md when gitignore fails | Not implemented | Writes regardless, only warns |
| Reset with uncommitted changes prompts | Implemented | --force flag for override |
| Init in existing workspace refuses | Implemented | CheckInitConflicts |
| Warns on unrecognized config fields | Implemented | md.Undecoded() check |
| Version forward compatibility | Partially | Unknown fields warn, but no version-based schema validation |

## Decisions and Trade-offs (D1-D15)

| Decision | Status | Notes |
|---|---|---|
| **D1**: Claude Code first | Followed | Only CLAUDE.md files generated |
| **D2**: Clean separation from tsuku | Followed | No tsuku references in code |
| **D3**: Outside-in bootstrap | Followed | niwa is installed first, creates workspaces |
| **D4**: Hybrid CLAUDE.md (generate + merge) | Partially contradicted | Only merge (user content files) is implemented. No auto-generation of boilerplate (repo tables, visibility rules, metadata headers). The "generate" half is missing. |
| **D5**: TOML config format | Followed | workspace.toml uses TOML |
| **D6**: Flat layout first-class | Contradicted | Repos without group assignment are skipped with a warning. The PRD says flat layout should work without groups. |
| **D7**: Init wizard in v0.1, adopt deferred | Followed | Init works, adopt not present |
| **D8**: Lightweight niwa home as registry | Followed | `~/.config/niwa/config.toml` is a small registry file, not a workspace container |
| **D9**: Six commands for v0.1 | Partially followed | init, create, apply, status, reset, destroy all present. version is extra but harmless. |
| **D10**: Append-only merge | Followed | Content files are written as-is with variable substitution, no insertion points |
| **D11**: Apply prints summary before acting | Not implemented | Apply outputs per-repo clone/skip messages but does not print a pre-execution summary of what it will do |
| **D12**: NIWA_HOME is just a config file | Followed | Single config.toml, no dedicated directory structure |
| **D13**: CLAUDE.md in parent dirs, CLAUDE.local.md in repos | Followed | Correct separation implemented |
| **D14**: Repo non-interference | Partially contradicted | gitignore check only warns, doesn't block writes. PRD says default behavior is to fail (skip writing). |
| **D15**: Name: niwa | Followed | Binary and package named "niwa" |

## Summary of Key Gaps

### Blocking for v0.1 completeness

1. **Flat layout (R12/D6)**: Repos with no group are skipped. Configs without groups should work.
2. **Clone failure resilience (R9)**: Apply aborts on first clone error instead of continuing.
3. **Gitignore enforcement (R11/D14)**: CLAUDE.local.md is written even when gitignore check fails. Should skip by default.
4. **Self-update command (R17)**: Not implemented at all.
5. **Auto-generated boilerplate (R5/D4)**: No repo tables, visibility rules, or metadata headers are generated. Only user content is installed.

### Minor / Low Priority

6. **Variable syntax**: `{workspace}` vs PRD's `$WORKSPACE`. Functional but different convention.
7. **Default clone protocol**: Code defaults to "https", PRD says "ssh".
8. **NIWA_CONFIG env var**: Not supported for overriding config path.
9. **Config version validation (R19)**: Version field exists but isn't checked.
10. **Apply pre-execution summary (D11)**: No "will do X, Y, Z" summary before acting.
11. **Config file location**: PRD says `workspace.toml` at root; implementation uses `.niwa/workspace.toml`.
