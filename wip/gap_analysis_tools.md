# Gap Analysis: tools/install.sh vs niwa

Comparison of what the tools bash installer does versus what niwa can replace today.

## 1. Repo Cloning and Organization

**tools/install.sh**: Hardcodes a list of 6 repos (tsuku, koto, niwa, shirabe, tools, vision) with
fixed paths under `$WORKSPACE/public/` and `$WORKSPACE/private/`. Does not clone repos -- assumes
they already exist. Skips repos whose directories are missing.

**niwa**: Discovers repos from GitHub org APIs or explicit lists in `workspace.toml`. Clones repos
into group directories (e.g., `public/`, `private/`). Groups are defined via visibility-based
classification. Supports per-repo branch overrides, clone URL overrides, and SSH/HTTPS protocol
selection. Multiple instances of the same workspace can coexist (instance numbering).

**Verdict: niwa fully replaces this.** niwa is strictly more capable -- it actually clones repos
rather than assuming they exist, supports auto-discovery, and handles the group directory structure
declaratively.

---

## 2. CLAUDE.md Hierarchy Generation

**tools/install.sh**: Installs three layers of CLAUDE.md files:
- Workspace-level: `$WORKSPACE/CLAUDE.md` (from `claude/workspace/CLAUDE_.md`)
- Visibility-level: `$WORKSPACE/private/CLAUDE.md` and `$WORKSPACE/public/CLAUDE.md`
- Repo-level: `CLAUDE.local.md` files (from `CLAUDE_local.md` sources), recursively into subdirectories

All files go through `sed` for `$WORKSPACE` path substitution. Ensures `*.local*` is in each repo's
`.gitignore`.

**niwa**: Installs three identical layers:
- Workspace-level: `{instanceRoot}/CLAUDE.md` (from `content.workspace.source`)
- Group-level: `{instanceRoot}/{group}/CLAUDE.md` (from `content.groups.{group}.source`)
- Repo-level: `{instanceRoot}/{group}/{repo}/CLAUDE.local.md` (from `content.repos.{repo}.source`)

Supports subdirectory content via `content.repos.{repo}.subdirs`. Template variable expansion
includes `{workspace}`, `{workspace_name}`, `{group_name}`, `{repo_name}`. Auto-discovers repo
content files by convention (`repos/{repoName}.md`). Checks `.gitignore` for `*.local*` and warns
if missing (but does not auto-add the pattern). Tracks managed files with SHA-256 hashes and
detects drift on re-apply.

**Verdict: niwa fully replaces this.** niwa's content system is more capable (auto-discovery,
drift detection, more template variables, subdirectory support). The only minor difference: niwa
warns about missing `.gitignore` patterns but doesn't auto-add them, while install.sh does. This
is arguably better behavior -- niwa should not modify repo-owned files without explicit intent.

---

## 3. Hooks Distribution (.claude/hooks/)

**tools/install.sh**: Creates `.claude/hooks/` in each target repo and copies shell scripts
(e.g., `gate-online.sh`, `workflow-continue.sh`) from the tools source directory. Makes them
executable with `chmod +x`.

**niwa**: Has a `hooks` field in `WorkspaceConfig` and `RepoOverride` structs, but they are typed
as `map[string]any` with a comment "placeholder". `MergeOverrides` implements merge semantics
(repo hooks extend workspace hooks via list concatenation), but there is no code that actually
writes hook files to `.claude/hooks/` directories. The apply pipeline does not process hooks.

**Verdict: not replaceable today.** The config schema reserves space for hooks and the merge logic
exists, but the apply pipeline has no hook installation step. This is a clear gap.

---

## 4. Settings Distribution (settings.local.json)

**tools/install.sh**: Generates `.claude/settings.local.json` in each repo with:
- `permissions.defaultMode = "bypassPermissions"`
- Hook references (PreToolUse -> gate-online.sh, Stop -> workflow-continue.sh)
- Optional `env.GH_TOKEN` from `workspace.env`

**niwa**: Has a `settings` field in `WorkspaceConfig` and `RepoOverride` (both `map[string]any`
placeholders). `MergeOverrides` handles per-key override semantics. But no code in the apply
pipeline generates or writes `settings.local.json` files.

**Verdict: not replaceable today.** Same situation as hooks -- schema and merge logic exist, but
no writer in the apply pipeline.

---

## 5. Environment File Merging (.env files)

**tools/install.sh**: Merges environment files from two layers:
- `env/workspace.env` (workspace defaults)
- `env/repos/{repoName}.env` (per-repo overrides)

Produces `.local.env` in each repo root. Merge is key-level: repo values override workspace
values for the same key. Also ensures `*.local*` is gitignored.

**niwa**: Has an `env` field in `WorkspaceConfig` and `RepoOverride` (both `map[string]any`
placeholders). `MergeOverrides` implements merge logic where the `"files"` key appends (list
concatenation) and other keys use repo-wins semantics. No code writes `.local.env` files.

**Verdict: not replaceable today.** Merge semantics are designed but not materialized. The tools
installer also reads secrets (GH_TOKEN, TELEGRAM_BOT_TOKEN) from env files and distributes them
to specific locations, which is a separate concern niwa hasn't addressed.

---

## 6. Plugin/Channel Configuration (Telegram bots, access control)

**tools/install.sh** performs extensive Telegram channel setup:
- Deploys `TELEGRAM_BOT_TOKEN` to `~/.claude/channels/telegram/.env`
- Copies `channels.json` to `~/.tsuku/channels.json` (host-to-bot mapping)
- Creates per-bot state directories under `~/.claude/channels/telegram/bots/`
- Merges shared access config (allowFrom, groups) into each bot's `access.json`
- Manages root-level access.json for the default bot
- Installs plugins via `claude plugin marketplace add` and `claude plugin install`:
  - tsukumogami marketplace (local path)
  - shirabe marketplace (GitHub)
  - telegram channel plugin (user scope)
- All secrets written with `chmod 600`

**niwa**: Has a `channels` field (`map[string]any` placeholder) in `WorkspaceConfig`. No
implementation exists for any channel-related operations. No plugin installation support.

**Verdict: not replaceable today.** This is the largest gap. Channel/plugin configuration is
complex, security-sensitive, and host-specific. It involves operations outside the workspace
directory tree (writing to `~/.claude/`, `~/.tsuku/`, running `claude plugin` commands).

---

## 7. Per-Instance Configuration (Multiple Workspace Copies)

**tools/install.sh**: Supports a single workspace copy. Uses `$TSUKU_WORKSPACE` env var to
override the default workspace path (derived from script location). No concept of multiple
instances.

**niwa**: First-class multi-instance support. `niwa create` assigns instance numbers, `niwa apply`
can target single/all/named instances via `ResolveApplyScope`. `niwa status` reports per-instance
drift. `niwa destroy` with uncommitted-changes safety checks. Global registry at
`~/.config/niwa/config.toml` tracks workspace configs.

**Verdict: niwa is strictly more capable.** The tools installer has no equivalent.

---

## 8. Other Operations

### Workspace Script Installation
**tools/install.sh**: Copies scripts from `scripts/` subdirectories to `$WORKSPACE/.local/bin/`
and makes them executable. Advises adding the bin dir to PATH.

**niwa**: No equivalent. No concept of workspace-level scripts or a bin directory.

**Verdict: not replaceable today.**

### Bun Runtime Provisioning
**tools/install.sh**: Installs bun via `tsuku install bun --force` (required for channel plugins).

**niwa**: No dependency/runtime provisioning. Out of scope for a workspace manager.

**Verdict: not replaceable (and likely should not be).** This is a tsuku concern, not a niwa one.

### workflow-tool Binary Build & Distribution
**tools/install.sh**: Builds a Go binary from `command_assets/tools/` and copies it to
`.claude/bin/workflow-tool` in each repo.

**niwa**: No binary build or distribution support.

**Verdict: not replaceable today.** Could be modeled as a hook or post-apply action.

### Shirabe Extension Distribution
**tools/install.sh**: Copies `.md` files from `shirabe-extensions/` to each repo's
`.claude/shirabe-extensions/` directory with `.local.md` suffix.

**niwa**: No equivalent concept.

**Verdict: not replaceable today.**

### Git Hook Installation
**tools/install.sh**: Runs `scripts/install-hooks.sh` in each repo that has one.

**niwa**: No post-clone hook execution.

**Verdict: not replaceable today.**

### Old Directory Cleanup
**tools/install.sh**: Removes obsolete `$WORKSPACE/.claude/` directory (from pre-v2.1.11
Claude Code).

**niwa**: Has managed file cleanup on re-apply (removes files no longer produced) and empty
group directory cleanup. But this is limited to niwa-managed files.

**Verdict: partially replaceable.** niwa's cleanup is more principled (hash-based drift tracking)
but narrower in scope.

### .gitignore Management
**tools/install.sh**: Auto-adds `*.local*` and `.claude/` patterns to repo `.gitignore` files.

**niwa**: Checks for `*.local*` in `.gitignore` and warns if missing, but does not modify the file.

**Verdict: partially replaceable.** niwa detects the issue but doesn't fix it. Whether auto-fixing
is desirable is a design question.

---

## Summary Table

| Capability | tools/install.sh | niwa | Coverage |
|---|---|---|---|
| Repo cloning & organization | Hardcoded list, no clone | Declarative, auto-discover, clone | Full |
| CLAUDE.md hierarchy | 3 layers + subdirs + sed | 3 layers + subdirs + vars + drift | Full |
| Hooks distribution | Copies .sh to .claude/hooks/ | Schema + merge logic, no writer | None |
| Settings distribution | Generates settings.local.json | Schema + merge logic, no writer | None |
| Env file merging | 2-layer merge to .local.env | Schema + merge logic, no writer | None |
| Plugin/channel config | Telegram bots, access, plugins | Placeholder schema only | None |
| Multi-instance support | Single workspace only | First-class instances | Full (niwa ahead) |
| Workspace scripts | Copies to .local/bin/ | No equivalent | None |
| workflow-tool build | Go build + distribute | No equivalent | None |
| Shirabe extensions | Copy with .local.md suffix | No equivalent | None |
| Git hook installation | Runs repo install scripts | No equivalent | None |
| .gitignore management | Auto-adds patterns | Warns only | Partial |
| Bun provisioning | tsuku install | Not in scope | N/A |

---

## Priority Gaps for Replacement

To replace install.sh with niwa, these capabilities need implementation (ordered by impact):

1. **Hooks distribution** -- write hook files to `.claude/hooks/` during apply
2. **Settings distribution** -- generate `settings.local.json` during apply
3. **Env file merging** -- write `.local.env` files during apply
4. **Plugin installation** -- run `claude plugin` commands during apply
5. **Channel configuration** -- deploy Telegram bot tokens and access configs

Items 1-3 share a pattern: the config schema and merge logic exist, but the apply pipeline lacks
a "materialize" step that writes the merged result to disk. Adding these three would cover the
core Claude Code configuration loop.

Items 4-5 involve operations outside the workspace tree and interaction with external CLIs. These
may be better handled as post-apply hooks or a separate "provision" command rather than being
baked into the apply pipeline.
