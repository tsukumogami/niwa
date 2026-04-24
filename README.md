# niwa

Declarative workspace manager for AI-assisted development. niwa manages
multi-repo workspaces with layered [Claude Code](https://docs.anthropic.com/en/docs/claude-code)
configuration.

## What it does

niwa reads a TOML config file and sets up a multi-repo workspace where Claude Code
has properly scoped context in every repo from the first session. It handles:

- **Repo discovery** -- auto-discover repos from GitHub orgs, classify into groups
- **CLAUDE.md hierarchy** -- generate context files at workspace, group, and repo levels
- **Template expansion** -- variables like `{workspace_name}` and `{repo_name}` in content files
- **Per-repo overrides** -- custom settings, hooks, and env per repo
- **Overlay layer** -- companion repos that layer additional repos, groups, and Claude context onto the base config; auto-synced on every apply
- **Multi-instance** -- run multiple workspace instances from the same config

## Quick start

### 1. Install

```bash
curl -fsSL https://raw.githubusercontent.com/tsukumogami/niwa/main/install.sh | sh
```

Or via tsuku: `tsuku install tsukumogami/niwa`

Or from source: `go install github.com/tsukumogami/niwa/cmd/niwa@latest`

The installer and the tsuku recipe both wire up shell integration, including
dynamic tab-completion for workspace, instance, and repo names. Open a new
shell after installing and `niwa go <tab>` will list matches from your
current workspace and registered workspaces.

### 2. Create a workspace

```bash
mkdir my-workspace && cd my-workspace
niwa init my-project
```

This creates `.niwa/workspace.toml` with a commented template you can edit.

### 3. Configure

Edit `.niwa/workspace.toml`:

```toml
[workspace]
name = "my-project"
content_dir = "claude"

[[sources]]
org = "my-github-org"

[groups.public]
visibility = "public"

[groups.private]
visibility = "private"
```

### 4. Add content files

Create content files in `.niwa/claude/` that become CLAUDE.md files in your workspace:

```
.niwa/claude/
  workspace.md      ->  CLAUDE.md at workspace root
  public.md         ->  public/CLAUDE.md
  private.md        ->  private/CLAUDE.md
  repos/my-repo.md  ->  public/my-repo/CLAUDE.local.md
```

Reference them in the config:

```toml
[claude.content.workspace]
source = "workspace.md"

[claude.content.groups.public]
source = "public.md"
```

The top-level `[content]` key is a deprecated alias for `[claude.content]`
and still parses cleanly (with a warning) until niwa v1.0.

### 5. Create an instance

```bash
niwa create
```

This creates a workspace instance as a subdirectory, clones all repos, and
installs CLAUDE.md files. Run `niwa create` again to create parallel instances
(numbered automatically), or `niwa create --name hotfix` for a named one.

### 6. Update after config changes

```bash
niwa apply
```

From the workspace root, this applies config to all instances. From within an
instance, it targets just that one. Apply is idempotent -- it clones missing
repos, regenerates content files, and cleans up repos removed from the config.

## Commands

| Command | Description |
|---------|-------------|
| `niwa init [name]` | Create a new workspace with a scaffolded config |
| `niwa init <name> --from <org/repo>` | Clone a shared workspace config from GitHub |
| `niwa init <name> --from <org/repo> --overlay <repo>` | Use `<repo>` as the overlay instead of auto-discovering one (`--overlay` and `--no-overlay` are mutually exclusive) |
| `niwa init <name> --from <org/repo> --no-overlay` | Skip overlay discovery entirely |
| `niwa create [--name <name>]` | Create a new workspace instance; on a TTY, shows a live status line ("cloning <repo>...") while each repo is processed |
| `niwa apply [--instance <name>]` | Apply config to all instances (from root) or one (from instance); on a TTY, shows a live status line ("cloning <repo>...", "syncing <repo>...") while each repo is processed |
| `niwa status [instance]` | Show workspace health: repos, drift, last applied |
| `niwa reset [instance] [--force]` | Tear down and recreate an instance |
| `niwa destroy [instance] [--force]` | Permanently remove an instance; when `[channels.mesh]` is configured, SIGKILLs running workers first, then stops the mesh watch daemon with a grace window |
| `niwa mesh watch --instance-root <path>` | Run the mesh watch daemon (started automatically by `niwa apply` when `[channels.mesh]` is configured; not normally invoked directly) |
| `niwa task list` | List tasks (filter by `--role`, `--state`, `--delegator`, `--since`) |
| `niwa task show <task-id>` | Show envelope, state, and transitions for one task |
| `niwa version` | Print version information |
| `niwa --no-progress <command>` | Suppress the animated status line regardless of TTY state |

`--no-progress` is a persistent flag -- it applies to all subcommands. Use it in CI pipelines and scripts where the animated status line is unwanted.

On a TTY, `niwa create` and `niwa apply` show a single in-place status line for the current operation; completed-repo lines scroll normally above it. On non-TTY output (piped, redirected, or CI), output is append-only, identical to previous behavior.

## Shared workspace configs

Teams can share workspace configs via a GitHub repo:

```bash
# Clone config from GitHub and set up the workspace
niwa init my-team --from my-org/workspace-config
niwa apply
```

The config repo is cloned as `.niwa/` (a git checkout), so it can be updated later.

Once registered, the name can be reused on the same machine to create additional
workspace instances in different directories:

```bash
cd ~/other-dir
niwa init my-team    # uses the registered source from the first --from
niwa apply
```

### Overlay discovery

When you run `niwa init --from <org/repo>`, niwa looks for a companion repo named
`<repo>-overlay` in the same org. If it exists and you have access, it's cloned
alongside the base config and merged in before workspace setup runs. Subsequent
`niwa apply` calls pull the latest overlay automatically.

You don't have to do anything for this to work -- it's automatic. But you have two
escape hatches:

```bash
# Point to a specific overlay repo instead of the auto-discovered one
niwa init my-team --from my-org/workspace-config --overlay my-org/my-custom-overlay

# Skip overlay discovery entirely
niwa init my-team --from my-org/workspace-config --no-overlay
```

`--overlay` and `--no-overlay` are mutually exclusive.

### Overlay content

When the overlay repo provides content files, `niwa apply` integrates them into the
workspace without touching base config files:

- **Repo CLAUDE.local.md** -- if the overlay config maps `OverlaySource` to a repo, its
  content is appended to that repo's `CLAUDE.local.md` after a blank line. Base content
  comes first; overlay content is added below it.
- **CLAUDE.overlay.md** -- if the overlay provides a `CLAUDE.overlay.md` file at its root,
  it's copied to the workspace instance root. niwa injects `@CLAUDE.overlay.md` into the
  workspace `CLAUDE.md` automatically.

The import order in `CLAUDE.md` is always:

```
@workspace-context.md
@CLAUDE.overlay.md
@CLAUDE.global.md
```

Users without overlay access see none of these additions. Their workspace is identical to
one set up from the base config alone.

## Config reference

See the [workspace config design](docs/designs/current/DESIGN-workspace-config.md)
for the full schema reference, including sources, groups, content hierarchy,
per-repo overrides, hooks, settings, and environment configuration.

## macOS Gatekeeper

macOS may block unsigned binaries. If you see a warning:

```bash
xattr -d com.apple.quarantine ~/.niwa/bin/niwa
```

## License

[MIT](LICENSE)
