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
- **Multi-instance** -- run multiple workspace instances from the same config

## Quick start

### 1. Install

```bash
curl -fsSL https://raw.githubusercontent.com/tsukumogami/niwa/main/install.sh | sh
```

Or via tsuku: `tsuku install niwa`

Or from source: `go install github.com/tsukumogami/niwa/cmd/niwa@latest`

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
[content.workspace]
source = "workspace.md"

[content.groups.public]
source = "public.md"
```

### 5. Apply

```bash
niwa apply
```

This discovers repos from your GitHub org, classifies them into groups, clones
them, and installs CLAUDE.md files at each level of the hierarchy.

## Commands

| Command | Description |
|---------|-------------|
| `niwa init [name]` | Create a new workspace with a scaffolded config |
| `niwa init <name> --from <org/repo>` | Clone a shared workspace config from GitHub |
| `niwa apply` | Apply the workspace config: clone repos, install content |
| `niwa version` | Print version information |

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
