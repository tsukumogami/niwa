# Decision 2: niwa config subcommand structure

## Chosen: Option A — `niwa config` subcommand tree

## Rationale

Option A fits the existing codebase cleanly: every cobra command is a single
top-level verb (`apply`, `init`, `create`, `destroy`, `status`). A `config`
command continues that pattern while grouping machine-level settings under a
single, predictable namespace. The `set global` / `unset global` hierarchy
mirrors the shape of the operation — you're setting or unsetting a named slot
(`global`) under a category (`config`) — and leaves room for future slots
without adding top-level noise to `niwa --help`.

## How It Works

### Command surface

```
niwa config set global <repo>   # register global config repo
niwa config unset global         # unregister global config repo
```

### Cobra registration

Three new files mirror the existing one-command-per-file convention:

- `internal/cli/config.go` — defines `configCmd` as a cobra group command with
  no `RunE` (it only prints help). Registers under `rootCmd` via `init()`.
- `internal/cli/config_set.go` — defines `configSetCmd` (group) and
  `configSetGlobalCmd` (leaf). `configSetCmd` is added to `configCmd`;
  `configSetGlobalCmd` is added to `configSetCmd`.
- `internal/cli/config_unset.go` — defines `configUnsetCmd` (group) and
  `configUnsetGlobalCmd` (leaf). Same nesting pattern.

`config.go`:

```go
var configCmd = &cobra.Command{
    Use:   "config",
    Short: "Read and write machine-level niwa configuration",
}

func init() {
    rootCmd.AddCommand(configCmd)
}
```

`config_set.go`:

```go
var configSetCmd = &cobra.Command{
    Use:   "set",
    Short: "Set a configuration value",
}

var configSetGlobalCmd = &cobra.Command{
    Use:   "global <repo>",
    Short: "Register a global config repo",
    Long: `Register a GitHub repo as the machine-wide global config source.

The repo is cloned to $XDG_CONFIG_HOME/niwa/global/ and its URL is stored in
~/.config/niwa/config.toml under [global_config].`,
    Args: cobra.ExactArgs(1),
    RunE: runConfigSetGlobal,
}

func init() {
    configCmd.AddCommand(configSetCmd)
    configSetCmd.AddCommand(configSetGlobalCmd)
}
```

`config_unset.go` follows the same structure with `runConfigUnsetGlobal`.

### `runConfigSetGlobal` behaviour

1. Call `config.LoadGlobalConfig()` to get the current config.
2. Resolve the clone URL from the `<repo>` argument using
   `workspace.ResolveCloneURL` (already used in `init.go`).
3. Determine the clone destination:
   `filepath.Join(configHome, "niwa", "global")` where `configHome` comes from
   `config.GlobalConfigPath()` logic (XDG-aware).
4. Clone with `workspace.Cloner{}` (same as `init.go`).
5. Add a `[global_config]` section to `GlobalConfig` (requires a new
   `GlobalConfigRepo` field on `GlobalSettings` or a sibling struct — see
   Assumptions).
6. Call `config.SaveGlobalConfig(cfg)`.

### `runConfigUnsetGlobal` behaviour

1. Load config; if no global config is registered, exit cleanly with a message.
2. Delete `$XDG_CONFIG_HOME/niwa/global/` with `os.RemoveAll`.
3. Clear the `[global_config]` section from `GlobalConfig`.
4. Save config.

### Discoverability

`niwa --help` shows one new top-level entry (`config`). `niwa config --help`
lists `set` and `unset`. Users can reach the exact command in two help steps,
which is the same depth as any leaf command with flags in this codebase.

## Alternatives Rejected

**Option B — `niwa global` top-level command**: Adds a second top-level verb
whose name (`global`) describes the *subject* rather than the *action*. Every
other niwa top-level command is an action verb. This would be the only
noun-based top-level command, which is inconsistent and harder to discover by
users scanning `niwa --help`. It also conflates the global-config feature with
the `global` namespace in a way that could confuse future contributors (e.g.,
is `niwa global list` listing global workspaces or global config entries?).

**Option C — `niwa init --global-config <repo>` flag**: Registration and
workspace initialisation are independent operations. Coupling them means users
must `niwa init` a workspace just to register a global config, and there's no
clean way to *unregister* without adding a separate command anyway. This option
solves only half the problem and obscures the operation behind an unrelated
command's flags.

## Assumptions

1. `GlobalConfig` (or `GlobalSettings`) will grow a new field to hold the
   global config repo URL, something like:

   ```go
   type GlobalConfigSource struct {
       Repo string `toml:"repo"`
   }
   ```

   stored under `[global_config]` in TOML. The exact field name and struct
   placement are implementation details to confirm when writing the config
   layer.

2. The clone destination (`$XDG_CONFIG_HOME/niwa/global/`) is fixed and not
   user-configurable in this iteration.

3. `workspace.ResolveCloneURL` handles both `org/repo` shorthand and full HTTPS
   or SSH URLs, as it does in `init.go`. No new URL resolution logic is needed.

4. `workspace.Cloner` is reused without modification — the same shallow-clone
   path used by `init.go` is sufficient.

## Open Questions

1. **Field naming**: Should the TOML key be `[global_config]` with a `repo`
   field, or should it live inside the existing `[global]` section as
   `global_config_repo`? This affects backward compatibility if the section
   already exists on any machines.

2. **Overwrite behaviour on `set`**: If a global config is already registered,
   should `niwa config set global <new-repo>` silently replace it, prompt for
   confirmation, or fail with a hint to unset first?

3. **Clone depth**: Should the global config repo use a shallow clone (depth 1)
   like workspace configs, or a full clone? If users need to contribute back to
   the global config repo, a full clone is better.

4. **Pull on apply**: Should `niwa apply` also pull the global config clone, or
   is that a separate concern handled by a future `niwa config pull` or similar?
