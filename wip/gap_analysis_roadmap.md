# Roadmap Gap Analysis: niwa

Date: 2026-03-27

## Summary

The roadmap lists F1 as Done and F2-F13 as Not started. The codebase tells a
different story: F1 through F6 are substantially implemented, F7 has partial
groundwork, and F8-F13 remain unstarted. The roadmap's progress table is stale
and needs updating.

---

## Feature-by-Feature Analysis

### F1: Project scaffolding and CI
**Roadmap status:** Done
**Actual status:** Done

Evidence: Go module with cobra CLI, `niwa version` command, GoReleaser config
(implied by `internal/buildinfo` ldflags pattern), CI workflows at
`.github/workflows/test.yml` and `.github/workflows/release.yml`, tsuku recipe
at `.tsuku-recipes/niwa.toml`, install script. Cross-platform binary release
pipeline is operational.

**Gaps:** None.

---

### F2: Config format and parser
**Roadmap status:** Not started (needs-design)
**Actual status:** Done

Evidence in `internal/config/config.go`:
- Full TOML schema implemented: `[workspace]` metadata (name, version,
  default_branch, content_dir), `[[sources]]` with org/repos/max_repos,
  `[groups]` with visibility and explicit repo lists, `[repos]` per-repo
  overrides (url, branch, scope, claude toggle, hooks, settings, env),
  `[content]` hierarchy (workspace, groups, repos with subdirs).
- Forward-compatibility: warns on unknown fields via `md.Undecoded()`.
- Validation: name format, source org required, content source path traversal
  prevention, subdirectory containment checks.
- Placeholder fields for future sections: hooks, settings, env, channels
  (parsed as `map[string]any`).

**Gaps:** None for v0.1 scope. The `needs-design` tag can be removed. Schema
versioning (R19) uses a simple `version` string field but has no migration
logic -- acceptable for v0.1.

---

### F3: Init command and global registry
**Roadmap status:** Not started (needs-design)
**Actual status:** Done

Evidence in `internal/cli/init.go` and `internal/config/registry.go`:
- Three init modes: `niwa init` (scaffold), `niwa init <name>` (named),
  `niwa init <name> --from <org/repo>` (clone from remote).
- Global config at `~/.config/niwa/config.toml` (XDG-aware) with
  `[global]` settings (clone_protocol) and `[registry]` (name -> source/root).
- Registry lookup, save, and update operations.
- Init conflict detection (`CheckInitConflicts`): existing workspace, orphaned
  `.niwa/` directory, nested instance.
- Scaffold template generation with commented examples.

**Gaps:** None for v0.1 scope. The `needs-design` tag can be removed.

---

### F4: Workspace creation and multi-instance lifecycle
**Roadmap status:** Not started (needs-design)
**Actual status:** Done

Evidence in `internal/cli/create.go`, `internal/workspace/apply.go`,
`internal/workspace/state.go`, `internal/workspace/destroy.go`,
`internal/cli/destroy.go`, `internal/cli/reset.go`:
- `niwa create` with `--name` flag for custom instance naming.
- Instance numbering: first instance uses config name, subsequent get numeric
  suffix, custom names produce `<config>-<name>`.
- `.niwa/instance.json` state file with schema_version, instance metadata,
  managed files (path + SHA-256 hash), repo states.
- `niwa destroy [instance]` with `--force` flag, uncommitted changes check.
- `niwa reset [instance]` with `--force` flag, validates remote config source.
- Instance discovery: walk-up from cwd, enumeration of workspace root children.
- State persistence: load, save, hash, drift detection.

**Gaps:** None for v0.1 scope. The `needs-design` tag can be removed.

---

### F5: CLAUDE.md hierarchy generation
**Roadmap status:** Not started (needs-design)
**Actual status:** Done

Evidence in `internal/workspace/content.go`:
- Workspace-level: reads content source, expands template variables, writes
  `{instanceRoot}/CLAUDE.md`.
- Group-level: writes `{instanceRoot}/{groupName}/CLAUDE.md` (non-git
  directories get CLAUDE.md, not .local).
- Repo-level: writes `{instanceRoot}/{groupName}/{repoName}/CLAUDE.local.md`
  (git directories get .local variant).
- Subdirectory content: per-repo subdirs with containment validation.
- Template variables: `{workspace}`, `{workspace_name}`, `{group_name}`,
  `{repo_name}` with `strings.NewReplacer` for safe expansion.
- Auto-discovery: checks `{content_dir}/repos/{repoName}.md` when no explicit
  entry exists.
- Gitignore verification (R11): warns if `.gitignore` lacks `*.local*` pattern.
- Content source containment: prevents path traversal via symlink resolution.

**Gaps:** None for v0.1 scope. The `needs-design` tag can be removed.

---

### F6: Idempotent apply and status
**Roadmap status:** Not started (needs-design)
**Actual status:** Done

Evidence in `internal/workspace/apply.go`, `internal/workspace/scope.go`,
`internal/workspace/status.go`, `internal/cli/apply.go`,
`internal/cli/status.go`:
- `niwa apply [workspace-name]` with `--instance` flag.
- Scope resolution: single instance (inside one), all instances (at root),
  named instance (by flag), registry lookup (by positional arg).
- Idempotent pipeline: discover repos, classify into groups, skip already-
  cloned repos, install/regenerate content files, clean up removed managed
  files and empty group directories.
- Drift detection: SHA-256 hash comparison for managed files, warns on
  external modifications before overwriting.
- `niwa status [instance]`: detail view (repos, managed files, drift) from
  inside an instance; summary view (repo count, drift count, relative time)
  from workspace root.
- Multi-source repo discovery with duplicate detection and max_repos threshold.
- Per-repo clone URL and branch override resolution.
- `claude = false` opt-out for content installation.
- Error collection: apply continues across instances, reports all failures.

**Gaps:**
- Apply does not print a summary before acting (the roadmap calls for a
  pre-action summary). It just runs. This is minor UX polish.
- No `niwa diff` command yet (that's listed under F12, not F6).

---

### F7: Detached workspaces
**Roadmap status:** Not started
**Actual status:** Partially started

Evidence: The `InstanceState` struct has a `Detached bool` field
(`json:"detached,omitempty"`). The init scaffold mode (`niwa init` with no
args) skips registry registration, which is the expected behavior for
detached workspaces.

**Gaps:**
- `niwa apply` does not have a detached mode that works from a directory
  containing workspace.toml without registry involvement. The current apply
  command requires either being inside an instance or at a workspace root
  with `.niwa/workspace.toml`. A detached workspace would need to treat the
  current directory as the single instance, which is not wired up.
- No test coverage or explicit code path for the detached flow end-to-end.
- The `Detached` field is declared but never set to true anywhere in the code.

---

### F8: Per-repo hooks and settings
**Roadmap status:** Not started (needs-design)
**Actual status:** Not started (groundwork only)

Evidence: The config schema has placeholder fields (`Hooks`, `Settings`, `Env`
as `map[string]any` at workspace level; per-repo overrides with the same).
`internal/workspace/override.go` implements `MergeOverrides` with merge
semantics (settings: repo wins, env files: append, hooks: concatenate). But
no code generates `settings.local.json` or installs hook scripts.

**Gaps:**
- No `settings.local.json` generation.
- No hook script installation to `.claude/hooks/`.
- No permission model implementation.
- The merge logic exists but is never called from the apply pipeline.

---

### F9: Environment file distribution
**Roadmap status:** Not started (needs-design)
**Actual status:** Not started (groundwork only)

Same as F8: env fields exist in the config schema and `MergeOverrides` handles
env merge semantics (files key appends, other keys override). But no code
generates `.local.env` files.

**Gaps:** Everything beyond schema and merge logic.

---

### F10: Remote config update and per-host overrides
**Roadmap status:** Not started (needs-design)
**Actual status:** Not started

No `niwa update` command. No hostname-keyed override sections. The config
schema has a `channels` placeholder field but no implementation.

**Gaps:** Everything.

---

### F11: Plugin orchestration
**Roadmap status:** Not started (needs-design)
**Actual status:** Not started

No plugin-related code exists.

**Gaps:** Everything.

---

### F12: Convenience commands and shell integration
**Roadmap status:** Not started (needs-design)
**Actual status:** Not started

No `niwa list`, `niwa which`, or `niwa diff` commands. No shell integration.
`niwa status` provides some of the listing functionality but doesn't match the
cross-root listing described in F12.

**Gaps:** Everything.

---

### F13: Adopt command
**Roadmap status:** Not started (needs-design)
**Actual status:** Not started

No `niwa adopt` command or repo detection heuristics.

**Gaps:** Everything.

---

## v0.1 Release Readiness

| Feature | Roadmap Says | Actual | v0.1 Blocking? |
|---------|-------------|--------|----------------|
| F1: Scaffolding/CI | Done | Done | No |
| F2: Config parser | Not started | Done | No |
| F3: Init + registry | Not started | Done | No |
| F4: Create/lifecycle | Not started | Done | No |
| F5: CLAUDE.md hierarchy | Not started | Done | No |
| F6: Apply + status | Not started | Done | No |
| F7: Detached workspaces | Not started | Partial | Yes - needs apply support |

**Assessment:** F1-F6 are complete. F7 is the only v0.1 blocker and has minimal
remaining work: wire a detached apply path that treats cwd as the single
instance when a workspace.toml is present but no instance.json exists. The
`Detached` state field is already declared.

## Roadmap Staleness

The roadmap progress table is significantly out of date. F2 through F6 are
listed as "Not started" but all have full implementations with tests. The
`needs-design` tags on F2-F6 should be removed. The status column should be
updated to "Done" for F1-F6 and "In progress" for F7.
