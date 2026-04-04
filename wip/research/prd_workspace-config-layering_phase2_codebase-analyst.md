# Phase 2 Research: Codebase Analyst

## Lead 1: Registration Clone Timing

### Findings

`SyncConfigDir()` in `internal/workspace/configsync.go` (lines 15-49) assumes the target directory already exists as a git repo. At line 18, it checks `git -C <dir> rev-parse --git-dir` -- if the directory doesn't exist or isn't a git repo, it returns nil (silent pass, not an error). It has no `git clone` step; it only runs `git pull --ff-only`.

By contrast, `niwa init --from <repo>` in `internal/cli/init.go` (around line 122) calls `cloner.CloneWith()` to perform an eager initial clone before the workspace is usable. The pattern is: clone at registration time, pull on subsequent syncs.

For personal config, this means:
- `niwa config set personal <repo>` must eagerly clone to a local path (e.g., `~/.config/niwa/personal/`)
- Subsequent `niwa apply` calls invoke `SyncConfigDir()` on the already-cloned directory

If registration is lazy (no clone until first apply), `SyncConfigDir()` would silently skip the sync (non-git dir → nil return), meaning personal config would never be loaded on first apply. This would be a silent failure.

### Implications for Requirements

- The PRD must specify that `niwa config set personal <repo>` performs an eager clone to a derived local path
- The local path should be deterministic and well-known (e.g., `$XDG_CONFIG_HOME/niwa/personal/`)
- Registration and cloning are a single atomic operation; partial failure (registered but not cloned) must be handled
- A `niwa config unset personal` command should remove both the registration and the local clone

### Open Questions

1. Should the clone path be configurable or always derived from XDG_CONFIG_HOME?
2. What happens if `niwa config set personal` is called when personal config is already registered -- update the repo URL and re-clone, or error?
3. Should registration validate that the repo is accessible (auth check) before writing to global config?

---

## Lead 2: Error Behavior on Sync Failure

### Findings

In `internal/cli/apply.go` (lines 82-84), workspace config sync failure is fatal:

```go
if err := ws.SyncConfigDir(configPath, applyAllowDirty); err != nil {
    return err  // aborts entire apply
}
```

There is no degraded-mode fallback for workspace config sync. If the team workspace repo can't be pulled, the apply stops entirely. There is no "apply with stale config" mode.

Within the apply pipeline itself (`internal/workspace/apply.go`), individual repo clone failures produce warnings but don't abort the pipeline -- the error is collected and the apply continues for other repos. So there's a distinction: *config* sync failures are fatal; *repo* sync failures are warnings.

### Implications for Requirements

The PRD must choose one of two behaviors for personal config sync failure:

**Option A: Abort (consistent with workspace config)** -- if personal config can't be synced, apply fails. Forces user to fix connectivity before applying. Safe: no stale personal secrets are applied silently.

**Option B: Continue with workspace-only (resilient)** -- if personal config sync fails, apply proceeds with workspace config only. Warning is printed. Useful for offline scenarios but risks applying outdated personal secrets.

The current architecture strongly favors Option A (abort): workspace config sync already aborts, and config-level failures are treated differently from repo-level failures. Treating personal config sync failure as a warning would create an inconsistency: workspace config sync is fatal but personal config sync is not.

### Open Questions

1. Should personal config sync failure behavior be configurable (e.g., `niwa config set personal on-sync-error continue`)?
2. If the user has never applied personal config before, should a first-time sync failure be fatal or a warning (since there's no stale config to fall back to)?

---

## Lead 3: Opt-out Persistence Storage

### Findings

`InstanceState` in `internal/workspace/state.go` (lines 27-38) contains: schema version, config name, instance name, instance number, root, creation time, last applied time, managed files, and repos. There is no field for preferences or feature flags. The one nullable field precedent is `ConfigName *string` (pointer), suggesting nullable fields for optional state exist.

`RegistryEntry` in `internal/config/registry.go` (lines 23-26) has only `Source` and `Root`. Adding opt-out here would affect all instances of a workspace (workspace-level).

Current pattern: per-invocation flags (like `--no-pull`) are not persisted anywhere. The only persistent preferences are in `~/.config/niwa/config.toml` (global) and `.niwa/workspace.toml` (workspace).

### Implications for Requirements

Two distinct opt-out granularities are available:

- **Per-invocation (flag only):** `--skip-personal` at init or apply time; not stored anywhere; consistent with `--no-pull`
- **Per-workspace persistent:** Stored in RegistryEntry or global config; affects all future applies for that workspace until changed
- **Per-instance persistent:** Stored in InstanceState; affects that specific instance's future applies

Evidence from the codebase strongly favors per-invocation: no preference flags exist in InstanceState today; the distinction between "runtime state" (instance.json) and "preferences" (config files) is clear and consistent.

### Open Questions

1. If persistent per-workspace opt-out is needed (for CI/CD), should it be in `~/.config/niwa/config.toml` under `[personal_config]` or in `.niwa/workspace.toml`?
2. Should `niwa status` report whether personal config is registered and whether it's being skipped?

---

## Summary

Registration must be eager: `SyncConfigDir()` assumes git repo exists locally, so `niwa config set personal <repo>` must clone immediately, mirroring `niwa init --from`. Error behavior should abort apply on sync failure, consistent with workspace config sync (which is also fatal) and to avoid silently applying stale personal secrets. Opt-out is best kept per-invocation (not persistent state), consistent with `--no-pull` and Unix CLI conventions; persistent opt-out for CI/CD belongs in config files, not command-line flags or instance state.
