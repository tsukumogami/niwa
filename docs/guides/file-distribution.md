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

### From a workspace overlay

A workspace overlay (`workspace-overlay.toml`) can declare its own
`[instance.files]` table. Entries are copied verbatim to each instance root,
exactly like the base table, with two differences:

- **Sources resolve in the overlay clone**, not the base config directory, so
  the distributed file's content lives in the overlay repo and never has to
  appear in the base config.
- **The base table wins per destination.** An overlay entry that targets a
  destination the base `[instance.files]` already fills is skipped, silently,
  rather than overwriting a base-managed file. The comparison runs on the
  cleaned path, so `./.mcp.json` collides with `.mcp.json`.

Destinations follow the same rules as the overlay's per-repo `[files]` table:
relative, no `..`, and nothing under `.claude/` or `.niwa/`. This is how a
private overlay distributes a file that must keep its exact name to work — a
project `.mcp.json`, say — without declaring it in a public base config. The
overlay has no `[root.files]` counterpart: the workspace root is shared by
every instance, overlaid or not.

### Loading the server without a trust prompt

Claude Code asks a person to approve each server in a project `.mcp.json` the
first time a session that isn't in bypass mode starts in that directory. How
much of that niwa can take off your hands depends on how the servers arrive.

If you declare the servers under `[mcp.servers.<name>]` instead of shipping a
file, niwa generates the instance root's `.mcp.json` itself and approves the
servers it wrote. The same apply puts Claude Code's `enabledMcpjsonServers`
list into the instance root's `.claude/settings.json`, holding exactly the
names of the generated servers, sorted:

```toml
[mcp.servers.example]
command = "example-mcp-server"
```

```json
{
  "enabledMcpjsonServers": ["example"]
}
```

The list sits beside everything else niwa writes to that file, and it follows
the declaration: drop a server and the next `niwa apply` drops its name, drop
them all and the key goes away. A server scoped to another agent with
`agents = [...]` isn't in Claude's `.mcp.json`, so it isn't in the list either.
niwa only ever names servers one by one. It never writes
`enableAllProjectMcpServers`, which would approve whatever any `.mcp.json` in
that directory happens to contain.

Claude Code reads that list from a project `settings.json` only once you've
trusted the directory, so unless [instance trust](instance-trust.md) is on, the
first session in a new instance still asks the folder trust question. After
you accept it, the declared servers start without a per-server prompt. Checked against Claude Code 2.1.296 with `claude mcp list`,
which reports a project server as pending until it's approved: the declared
servers showed as pending in an untrusted instance and were started once the
instance root itself was trusted. A trust entry on a parent directory alone
wasn't enough.

A `.mcp.json` distributed verbatim through `[instance.files]`, `[root.files]`
or `[files]` gets no approval. niwa copies that file without reading it, so it
has no server names to approve, and those servers keep the per-server prompt.
Declaring them under `[mcp.servers]` is the way to have them approved.

Declared servers are generated at the instance root only, so this covers
sessions started there. A session at the workspace root still gets its servers
from a `[root.files]` copy, with the prompt.

Bypass mode is the other route: a session running in bypass mode doesn't show
the prompt. To get that for dispatched workers, declare the posture in
`workspace.toml`:

```toml
[claude.settings]
permissions = "bypass"
```

`bypass` reaches dispatched workers through `niwa dispatch`: niwa records the
declared posture for each instance, and dispatch passes
`--permission-mode bypassPermissions` to the workers it launches. niwa doesn't
write a bypass mode into any `.claude/settings.json`, because Claude Code
2.1.257 stopped honoring one set in a project's settings file. A session you
start yourself at the workspace root or an instance root gets your own Claude
Code settings; launch it with `claude --permission-mode bypassPermissions` if
you want bypass there. This setting is independent of the file tables above.

To get past the trust dialog and the MCP approval prompt without bypass mode,
see [instance trust](instance-trust.md).

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

  **Correction (2026-10-13):** the walk-up claim above is wrong for current
  Claude Code. Measured on Claude Code 2.1.296, a session started inside a
  repository does find the instance root's `.mcp.json` by walking up, and asks
  to approve each server in it. With `[claude] trust_instances` on, niwa covers
  those repository sessions: it trusts each cloned repository directory and
  approves the instance root's generated servers in each repository's
  settings. See [instance trust](instance-trust.md). A file distributed
  verbatim through `[instance.files]` or `[root.files]` is still never
  pre-approved, since niwa doesn't read the files it copies.
- **Destinations stay at the project root** of their level. Distributed files are
  meant for the workspace-root or instance-root project directory, not for
  niwa-internal directories.
