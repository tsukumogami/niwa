# Distributing files

niwa can copy files from your workspace config directory into the things it
manages. There are three file tables, one per level, and they differ in two
ways that matter: **where** the file lands and **whether the name is rewritten**.

| Table | Lands at | Name |
|-------|----------|------|
| `[files]` | each managed repo | rewritten with a `.local` infix |
| `[instance.files]` | each instance root | verbatim (exact name) |
| `[root.files]` | the workspace root | verbatim (exact name) |

Every entry is a `source = destination` mapping. The source is a path relative
to the config directory (`.niwa/`); the destination is relative to the target
level's root. A trailing `/` on the source means "copy the whole directory."

## Why repos get `.local` and the non-repo levels do not

A managed repo is a git working tree, and niwa keeps its output invisible to
that tree by giving every file a `.local` infix (`config.json` ->
`config.local.json`) so it matches the `*.local*` pattern the repo gitignores.
That is what `[files]` does.

The workspace root and the instance root are **not** git repositories — they are
container directories that hold repos (and instances) as subdirectories. There
is no repo gitignore at those levels for a `.local` infix to satisfy, and some
files have to keep their exact name to work at all. So `[instance.files]` and
`[root.files]` copy **verbatim**: the destination name you write is the name on
disk, with no infix inserted.

The per-repo `[files]` behavior is unchanged by the non-repo tables; the three
levels are independent.

## Example: a project MCP config at every level

A Claude Code project MCP config must be named exactly `.mcp.json` to be loaded,
and Claude Code reads it from the directory a session starts in. To make the
same MCP server available to sessions started at the workspace root and at every
instance root, distribute the file verbatim from both non-repo tables:

```toml
# .niwa/workspace.toml

# Workspace root (the parent dir holding the instance subdirectories).
[root.files]
"mcp.json" = ".mcp.json"

# Every instance root.
[instance.files]
"mcp.json" = ".mcp.json"
```

Given a source file `mcp.json` in `.niwa/`, `niwa apply` writes
`<workspaceRoot>/.mcp.json` and `<instanceRoot>/.mcp.json` for each instance, and
`niwa create` writes it into a freshly provisioned instance — all under the exact
name, no `.local`.

The source in the config repo is named `mcp.json` rather than `.mcp.json` only to
keep it a visible template; mapping `".mcp.json" = ".mcp.json"` works the same.

### Loading the server without a trust prompt

Claude Code may show a per-session trust prompt the first time a project MCP
server loads. A workspace that runs sessions in bypass mode suppresses it:

```toml
[claude.settings]
permissions = "bypass"
```

This is the existing settings surface (it maps to Claude Code's
`bypassPermissions` mode); it is independent of the file tables above.

## Tracking and cleanup

- **Instance-root files** (`[instance.files]`) are recorded as niwa-managed, so
  drift detection and cleanup apply: remove an entry and re-run `niwa apply`, and
  the previously materialized file is deleted from each instance root.
- **Workspace-root files** (`[root.files]`) are re-written on every apply
  (overwrite-idempotent), matching the other workspace-root managed files
  (`.claude/settings.json`, `CLAUDE.md`, root skills). The workspace root has no
  managed-file state store, so removing a `[root.files]` entry leaves the
  previously written file in place until you delete it by hand.

## Limitations

- **Repo-subdirectory sessions are not covered.** Claude Code loads `.mcp.json`
  from the directory a session starts in and does not walk up to an ancestor. A
  file at the workspace root or instance root reaches sessions started *at* those
  levels, not sessions started *inside* a managed repo. Per-repo coverage is the
  separate `[files]` mechanism.
- **Destinations stay at the project root** of their level. Distributed files are
  meant for the workspace-root or instance-root project directory, not for
  niwa-internal directories.
