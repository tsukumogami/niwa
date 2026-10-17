# Instance trust

Claude Code won't read a project's `.claude/settings.json` until that exact
directory is trusted. In a fresh niwa instance, that means two interruptions at
the start of every session that isn't in bypass mode: the folder-trust dialog,
then an approval prompt for each MCP server in the instance's `.mcp.json`. The
approved-server list niwa writes into the instance root's settings does nothing
until the folder is trusted, and a trusted parent directory doesn't count.

`[claude] trust_instances` has niwa mark each instance root, and each
repository directory niwa clones into it, trusted for you. With it on, every
instance niwa creates or applies is trusted in your Claude Code user config, so
a session started at the instance root or anywhere inside one of its cloned
repositories gets neither prompt. It's off by default.

## Turning it on

```toml
[claude]
trust_instances = true
```

The key takes a TOML boolean; any other value is a config error. It can go in
`workspace.toml` or in the workspace overlay's `workspace-overlay.toml`:

- When only one of them sets it, that value applies.
- When both set it, the workspace config wins.
- An explicit `false` in the workspace config refuses an overlay's `true`.
  Leaving the key out of the workspace config hands the choice to the overlay,
  so a workspace config that wants to say no has to say `false`.

Every run of the create/apply pipeline then trusts the instance root and its
repository directories before the command exits. That covers `niwa create`,
`niwa apply`, `niwa reset`, `niwa init` when it creates an instance, `niwa
dispatch`, and session-start provisioning for ephemeral instances. Instances
that existed before you turned the option on catch up on their next `niwa
apply`. Review instances are the exception: niwa never trusts their
repository directories (see [Review instances](#review-instances)).

## What niwa writes

niwa sets one flag, `projects[<key>].hasTrustDialogAccepted = true`, in Claude
Code's user config file, for the instance root and for each repository
directory it cloned there. No other value in the file changes.

- **The file** is `$CLAUDE_CONFIG_DIR/.claude.json` when `CLAUDE_CONFIG_DIR` is
  set and non-empty, and `$HOME/.claude.json` otherwise. niwa reads
  `CLAUDE_CONFIG_DIR` from its own environment when the command runs, and
  refuses a relative value with a warning. If the file is a symlink, niwa
  writes the file it points to and leaves the link in place. If the legacy
  `.config.json` exists in Claude Code's config home (`$CLAUDE_CONFIG_DIR`, or
  `$HOME/.claude` when that's unset), Claude Code reads it instead of
  `.claude.json`, so niwa writes nothing and prints a warning naming it.
- **The key** is the directory's absolute path with every symlink resolved,
  in the directory itself and in every parent. That's the key Claude Code
  computes for a session started there, and for a session started anywhere
  inside a repository it's the repository's root. On macOS, for example, a
  root under `/var/folders/...` is keyed as `/private/var/folders/...`.
  Outside a git repository, Claude Code skips its trust dialog in any
  directory beneath a trusted one, so niwa writes nothing and warns when the
  key is `/`, your home directory, Claude Code's config home, or an ancestor
  of either, the key a single-instance `niwa init` run in your home directory
  would give. The repositories inside such an instance are still trusted:
  each is a directory of its own.
- **The repositories** are the ones niwa cloned into the instance, at
  `<instance>/<group>/<repo>`, except a repository with Claude turned off
  (`[repos.<name>.claude] enabled = false`, or the workspace-wide
  `[claude] enabled = false`, which excludes every repository that doesn't
  turn Claude back on for itself, while the instance root is still trusted),
  which gets no Claude settings from niwa
  either. Only Claude's own setting counts: a repository with
  Claude off and another agent on isn't trusted. A repository directory, or
  the group directory above it, that resolves outside the instance (a symlink
  to somewhere else, say) isn't trusted either, since niwa could never check
  that entry against the instance to take it back. niwa skips it with a
  warning naming the repository.
- **The lock.** niwa holds Claude Code's own config lock, the
  `<config file>.lock` directory, while it reads, changes and replaces the
  file, so neither side loses the other's write. It waits up to 5 seconds and
  treats a lock older than 10 seconds as stale, Claude Code's own threshold.
  Once a wait on one config file's lock has timed out, the rest of that niwa
  command skips writing trust to that file without waiting, so an apply over
  many instances doesn't wait 5 seconds for each. Removal doesn't take that
  shortcut: a destroy or reap waits for the lock again for each instance, so
  one busy moment doesn't leave every later instance's entry behind.
- **The write** goes to a temporary file beside the original, is flushed to
  disk, and is renamed over it, keeping the original's permission mode. A new
  file is created with mode 0600. A file that already says what niwa would
  write isn't rewritten.

What niwa does depends on the entry it finds:

| Entry at the key | What niwa does | What it records |
|---|---|---|
| None | creates it with the flag set | `created` |
| Flag absent or `false` | sets the flag | `flag-only` |
| Flag `true`, set by someone else | nothing | nothing |
| Flag `true`, already in niwa's record | nothing | keeps the record |

The record lives in the instance's `.niwa/instance.json`: the root's under
`claude_trust`, and each repository's under `claude_trust_repos`, keyed by the
repository's path inside the instance (`tools/app`, say). A record holds the
config file path, the key, the mode, and which feature wrote it (`workspace`,
or `review` for a review instance). It's how niwa knows what to take back
later.

### The approval list in each repository

A session started inside a repository finds the instance root's `.mcp.json` by
walking up from its directory, and asks you to approve each server in it unless
the repository's own project settings list that server. So with the option on,
niwa also writes `enabledMcpjsonServers` into each repository's
`.claude/settings.local.json`, naming the servers it generated into the
instance root's `.mcp.json` from `[mcp.servers]`, the same list the instance
root's `.claude/settings.json` carries. Claude Code reads that list only in a
trusted directory, which is why it's written only while the option is on. With
the option off, the repository's settings document is exactly what it was
before this option existed.

When niwa can't parse the config file (it isn't JSON, the top level,
`projects` or the entry isn't an object, or the file can't be read), or can't
get the lock within 5 seconds, it leaves the file exactly as it was and prints
a warning naming the file. The command still succeeds. Nothing is recorded, so
the next apply tries again.

## Taking trust back

niwa removes only what its records name. The instance root and each repository
directory are taken back the same way, each by its own record.

- **Destroying an instance** removes the whole entry when the record says
  `created` and only the flag when it says `flag-only`, for the root and for
  every repository. That holds for every route: `niwa destroy` in any form,
  `niwa reap`, `niwa reset`, dispatch's cleanup of a failed launch, and
  review-instance cleanup. Removal uses the recorded file and key, so a
  destroy run under a different `CLAUDE_CONFIG_DIR` still cleans the right
  file. An entry or flag that's already gone is fine. A repository directory
  you deleted before destroying the instance can't be checked against its
  record any more, so its entry is left and the warning names it.
- **Turning the option off** withdraws once, on the next `niwa apply` in each
  instance. niwa deletes the flag, never the whole entry, since the instance
  and whatever Claude Code stored for it still exist, and then clears the
  record, whatever its mode. A flag you set by hand afterwards is yours, and
  later applies leave it alone. An entry niwa created stays behind without the
  flag (see [Known limits](#known-limits)). Turning the option back on writes a
  fresh grant, recorded `flag-only` since the entry is still there.
- **Changing `CLAUDE_CONFIG_DIR`** between applies withdraws the flag from the
  old file before the new one is written. When both paths name the same file
  (`CLAUDE_CONFIG_DIR` is a symlink to your home directory, say), nothing
  moved: niwa withdraws nothing and keeps the record's mode.
- **A moved instance** gets fresh grants at its new paths. niwa can't prove
  the old entries still belong to this instance, so it leaves them and warns,
  naming each key and file.
- **A repository that stops getting trust**, because you removed it from the
  config or turned Claude off for it, is treated like the off switch on the
  next apply: niwa deletes its flag once and drops its record. If its
  directory is already gone, niwa drops the record, leaves the entry and
  warns.

An entry you made yourself, by answering Claude Code's trust prompt, is never
in a record, so niwa never removes it. Every record is checked before niwa
acts on it: the key has to be the resolved path of the instance, or of the
repository, being destroyed or applied, and the file has to be named
`.claude.json`. A repository record's path has to be a plain relative path
inside the instance, with no `..`, that still resolves inside it; one that
isn't names no directory of this instance, so niwa refuses it rather than
computing a key from it. niwa never acts on a record that fails, and warns. An
apply also drops a malformed record (unknown mode or source, a bad file path)
from the instance's state, so you see that warning once rather than on every
apply.

A skipped removal (unparseable file, lock held) doesn't stop a destroy. The
warning names the file and key that were left, so you can delete the entry by
hand. A skipped withdrawal on apply keeps the record, and the next apply tries
again.

## What trust lets Claude Code do

Trusting an instance makes Claude Code apply that instance's project
permissions, hooks and MCP servers without asking. That's the point of the
option, and it's also the cost.

The same goes for every repository niwa clones into it. Each repository's own
committed `.claude/settings.json`, with whatever permissions and hooks it
declares, and its own `.mcp.json`, now apply in a session started there
without Claude Code asking you first. So turning the option on means trusting
the contents of every repository the workspace clones, not only the workspace
config, and a commit to any of them can change what a session does there with
no prompt.

Whoever can change the workspace config or the overlay can put hooks and
pre-approved MCP servers in front of every developer who opted in, with no
prompt. A stdio server runs a command, so a pre-approved stdio server runs a
command the config's author picked. If trust comes from an overlay, treat the
overlay's repository like code that runs on your machine. It is.

When trust is on only because the overlay set it, niwa prints a one-time
notice saying so, naming the key and file it wrote, how many repository
directories it trusted, and what trust allows. The notice is recorded in the
workspace root's state, so it appears once per workspace. A workspace that saw
the notice before niwa trusted repository directories gets a second one-time
notice the first time an instance in it holds trust for a repository
directory, saying those directories are trusted now too. A later overlay
commit that adds a server or changes a command reaches trusted sessions with
no new notice, and a dispatched worker never sees the notice at all. In a
single-instance layout there's no workspace root state to record it in, so the
notice appears on any apply that writes trust.

Code that already runs in the instance as you (a build script in a cloned
repository, an agent session) can rewrite the instance root's
`.claude/settings.json` or `.mcp.json`, or a repository's `.claude` settings,
and with those directories trusted, Claude Code honors the rewrite. That code
could already do anything you can, so trust gives it another route rather than
more reach.

## Which sessions it covers

Trust covers a session started at the instance root, and a session started
anywhere inside a repository niwa cloned into the instance. Claude Code keys a
session inside a repository by that repository's root, not by the instance
root, which is why each repository gets an entry of its own and an approval
list of its own.

Whether the servers declared in `[mcp.servers]` load without a dialog depends
on how the session was launched:

| Launch path | Declared servers load without a dialog? |
|---|---|
| `niwa dispatch` worker | Yes, under every permission mode, whether or not `trust_instances` is on |
| Ephemeral session started at the workspace root | No: the servers don't load at all |
| Foreground `claude` or `claude -p` at an instance root or in a cloned repository | Yes, when the instance is trusted |
| `claude --bg` started by hand at an instance root or in a cloned repository | Yes, when the instance is trusted, the same as foreground |

**Dispatched workers** get their approval from the launch, not from the
instance's settings files. Without it, a dispatched worker stopped at startup
on the per-server approval dialog even with the server listed under
`enabledMcpjsonServers` in those files (see [Verified with](#verified-with)), so `niwa dispatch` puts the same list in the
`--settings` document it passes to the worker. The names come from what niwa
recorded in the instance's `.niwa/instance.json` when it generated the
instance root's `.mcp.json`, never from a `.mcp.json` or a settings file. Only
names made of letters, digits, `_` and `-`, at most 64 characters, are sent,
and at most 64 of them; each name left out gets a warning. The approval is
sent whether or not `trust_instances` is on. niwa records no servers for a
review instance, so a review launch carries no approval.

**Ephemeral sessions** started at the workspace root don't get the servers.
The session's project is the workspace root, not the instance niwa provisions
for it, and Claude Code reads MCP config once, at startup, so the instance's
`.mcp.json` never reaches the session. The SessionStart hook's context names the
servers the session lacks and the two ways to get them: run the work as a
`niwa dispatch` worker, or start a new session inside the instance. When the
hook can't read the instance's state, it adds no such notice.

**Foreground and hand-started background sessions** at an instance root or
inside a cloned repository load the servers with no dialog when the instance
is trusted, as described above. `claude --bg` started by hand there behaves
like foreground `claude`.

Two places stay uncovered:

- **Worktrees.** A worktree niwa creates gets the same `settings.local.json`
  as its repository, approval list included, but niwa doesn't trust the
  worktree's directory. A session usually enters a worktree from one that's
  already running, so there's no fresh start for the dialog to interrupt; a
  session started directly in a worktree still asks.
- **The workspace root**, which holds no instance of its own.

## Still seeing prompts?

Work through these in order:

1. **Start the session at the instance root or inside a cloned repository**,
   not in a worktree and not at the workspace root.
2. **Run `claude doctor`** in that directory and look for settings errors. One
   invalid value in a settings file makes Claude Code ignore the whole file,
   approval list included, and trust can't help until the file loads. In a
   repository, that includes the repository's own committed
   `.claude/settings.json`.
3. **Run `claude mcp list`** in that directory and look for "Pending
   approval". A server shown that way isn't on an approved list Claude Code
   read. In a repository, check that its `.claude/settings.local.json` lists
   the server under `enabledMcpjsonServers`; a repository with Claude turned
   off gets no settings from niwa.
4. **Check `CLAUDE_CONFIG_DIR`.** niwa wrote to the file its own environment
   named. A session started under a different `CLAUDE_CONFIG_DIR` reads a
   different file and won't find the entry. `claude_trust.config_file` in the
   instance's `.niwa/instance.json` shows the file niwa used.
5. **For a dispatched worker, read the dispatch output.** The worker's
   approval comes from the launch, not from the instance's settings files, so
   editing `.claude/settings.json` or `.claude/settings.local.json` changes
   nothing for it. A warning that an MCP server was left out of the launch
   approval means that server's name didn't fit the allowed characters or
   length, or the list already held 64 names; that server will prompt.
   Renaming it in `[mcp.servers]` fixes the first case. A warning that niwa
   couldn't read the instance's state means no server was approved at launch.

Servers in a `.mcp.json` distributed verbatim, through `[instance.files]` for
example, are never pre-approved, because niwa doesn't read the files it copies.
Declare those servers in `[mcp.servers]` instead, in the workspace config or in
the overlay.

Run the same check after upgrading Claude Code. niwa matches the key, file
location and lock of the version listed under [Verified with](#verified-with),
and a release that changes any of them breaks this feature without an error.

## Declaring an MCP server in an overlay

An overlay can declare `[mcp.servers.<name>]` exactly as the workspace config
does. That's how a server whose name or credentials can't appear in a public
workspace config still gets generated and approved:

```toml
# workspace-overlay.toml
[vault.providers.example]
# kind and provider settings, as described in vault-integration.md

[claude]
trust_instances = true

[mcp.servers.example]
url = "https://mcp.example.com/mcp"

[mcp.servers.example.headers]
X-API-Key = "vault://example/API_KEY"
```

`vault://` values in an overlay server's `headers` and `env` resolve against
the overlay's own `[vault]` providers, the same way the overlay's `[env]` does.
A reference to a provider only the workspace config declares fails with the
usual provider-not-declared error. Every other check is the one a workspace
config entry gets, with the same error text.

When the workspace config declares a server with the same name, the workspace
config's declaration wins. niwa warns, naming the server, and never looks up
the dropped overlay entry's secrets, so a stale overlay entry can't fail an
apply.

After the merge, overlay servers are ordinary `[mcp.servers]` entries. They
appear in the instance root's `.mcp.json`, in every other agent's generated MCP
config (Codex's `[mcp_servers.<name>]` table, for example), and in the
approved-server lists, the instance root's and each repository's, sorted by
name together with the workspace config's servers. A worktree created with
`niwa worktree` gets them too; a vault value that path can't resolve is left
out with a warning.

## Review instances

`niwa watch` review instances use the same writer and the same record, with
source `review`. Destroying a review instance withdraws its grant through the
record like any other. A review instance created in a workspace that has
`trust_instances` on starts with a workspace grant, and the review relabels it
as its own, so an apply in that instance with the option off can't pull trust
out from under a running review. An apply never withdraws a review grant.

A review instance never gets repository-directory trust, whatever
`trust_instances` says. It exists so an agent can read a pull request's code
before anyone has accepted it. `niwa watch` fetches the pull request into its
own clone directory, not into the repository directories, but the agent can
check the pull request out in a repository directory of the same repository,
and in a trusted directory Claude Code applies the `.claude` settings and
hooks committed there without asking. A pull request's committed hooks could
then run in a session there with no prompt. So before the pull request is fetched,
`niwa watch` withdraws every repository grant the instance was created with,
as destroying the instance would, and records `review_instance` in the
instance's `.niwa/instance.json`. Every later apply in that instance (or one
whose root grant has source `review`) trusts no repository directory and
retracts any repository grant it finds. If a withdrawal is skipped (Claude
Code's config lock held too long, or a config file niwa can't parse), the
review doesn't start: the stage fails, and destroying the instance retries
the withdrawal. Only the instance root is trusted, since that's where the
review session runs.

The repositories in a review instance can still carry the approval list in
their `.claude/settings.local.json`: it's written when the instance is
created, before the review marks it. Claude Code reads that list only in a
trusted directory, so without trust it approves nothing.

## Known limits

- **Environment skew.** niwa can only write the file its own environment
  names. A session started later under a different `CLAUDE_CONFIG_DIR` still
  prompts.
- **Entries left by older niwa versions.** Review-instance entries written
  before review trust moved onto the record have no record, so reap no longer
  removes them. Their paths are already gone, so they grant nothing, but they
  stay in the file until you delete them.
- **Entries for paths that no longer exist.** A withdrawal skipped during
  destroy, an instance deleted by hand instead of through niwa, and a
  repository directory deleted by hand before the instance was destroyed or
  applied, leave a trusted entry behind. If niwa later creates an instance or
  clones a repository at the same path, that entry already trusts it, and
  since the entry is already trusted, niwa records nothing for it. The
  warning names the file and key to delete.
- **Entries left by the off switch.** Turning the option off clears the
  records, so an entry niwa created stays in the config without the flag, and
  destroying the instance doesn't remove it. It trusts nothing; delete it by
  hand if you want the file tidy.
- **Worktrees aren't trusted.** Their settings carry the approval list, but a
  session started directly in a worktree still sees the trust dialog.
- **A session still running in a destroyed instance** can write its project
  entry back when it saves state. That entry has no trust flag, so it's
  leftover data, not trust.

## Verified with

The live check runs at a fresh instance root created with the option on and an
overlay-declared server (`claude -p "list your mcp tools"` in default mode,
`claude mcp list`, `claude doctor`), and again inside one of that instance's
cloned repositories, where the server should be connected with no prompt too.

The instance-root check last passed on Claude Code 2.1.296, macOS, on
2026-10-13, with a niwa built from the change that added the option, before
repository directories were trusted:

- `niwa create` with `trust_instances = true` set in the overlay wrote
  `projects["/private/tmp/.../tc"].hasTrustDialogAccepted = true` (the
  symlink-resolved path of an instance under `/tmp`), recorded it as
  `created`, and printed the one-time overlay notice.
- At the instance root, `claude mcp list` showed the overlay-declared HTTP
  server as connected, `claude -p` in default permission mode listed its tools
  with no prompt, and `claude doctor` reported no settings errors.
- Inside a cloned repository of the same instance, `claude mcp list` showed the
  server as pending approval: that session is keyed by the repository, which
  that build didn't trust.
- `niwa destroy` removed the entry niwa had created and left no other entry
  under the instance path.
- The Claude Code binary contains no `hasTrustDialogHooksAccepted` string, so
  there's no separate hooks-trust prompt for niwa to pre-answer.

What the repository half writes comes from a measurement on Claude Code
2.1.296 with an isolated `CLAUDE_CONFIG_DIR`, one stdio server in the instance
root's `.mcp.json`, and a git repository at `<instance>/<group>/<repo>`, the
trust entries and settings written by hand:

- A session in the repository saw the instance root's server through the
  ancestor `.mcp.json`, pending approval, whether or not the instance root was
  trusted and approved it.
- Trusting the repository's root alone left the server pending.
- Trusting the repository's root and listing the server in
  `enabledMcpjsonServers` in its `.claude/settings.local.json` (or its
  `.claude/settings.json`) approved it, with the instance root untrusted.

The repository check last passed on Claude Code 2.1.296, macOS, on 2026-10-13,
with a niwa built from the change that trusts repository directories, a fresh
instance of ten cloned repositories, and an overlay-declared HTTP server with
two vault-backed headers:

- `niwa create` recorded a grant for the instance root and for each of the ten
  repositories, and each repository's `.claude/settings.local.json` listed the
  server in `enabledMcpjsonServers`.
- `claude mcp list` showed the server connected at the instance root and inside
  two of the repositories; inside one of them, `claude -p` in default
  permission mode listed its tools with no prompt, and `claude doctor` reported
  no settings errors.
- `niwa destroy` took the trusted entries under the instance path from eleven
  to none.

The launch-path coverage above was checked on Claude Code 2.1.301 and
2.1.302, macOS, on 2026-10-16, with a declared HTTP server:

- Foreground `claude`, `claude -p`, and hand-started `claude --bg` at a
  freshly created trusted instance root loaded the declared server with no
  dialog, in default and `auto` permission modes.
- A `niwa dispatch` worker on the released niwa stopped at startup on the "New
  MCP server found in this project" dialog, on both 2.1.301 and 2.1.302.
  Replaying its command line, environment and prompt by hand in the same
  instance didn't reproduce it, so the cause inside Claude Code isn't known.
  Adding the approval to the instance's `.claude/settings.local.json` didn't
  help.
- A dispatched worker whose launch `--settings` document carried
  `enabledMcpjsonServers` started with the server connecting and no dialog,
  under `auto`, on Claude Code 2.1.301, with a niwa build that sends the
  approval from the launch.
- An ephemeral session started at the workspace root, on 2.1.302, had none of
  the workspace's declared MCP servers, consistent with its project being the
  workspace root.

One check is still to run with the final build: a dispatched worker under
`default` permission mode. To run it, dispatch with `--permission-mode
default` in a workspace that declares a server, and confirm the worker reaches
its first turn with no startup dialog.
