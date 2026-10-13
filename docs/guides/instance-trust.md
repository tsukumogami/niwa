# Instance trust

Claude Code won't read a project's `.claude/settings.json` until that exact
directory is trusted. In a fresh niwa instance, that means two interruptions at
the start of every session that isn't in bypass mode: the folder-trust dialog,
then an approval prompt for each MCP server in the instance's `.mcp.json`. The
approved-server list niwa writes into the instance root's settings does nothing
until the folder is trusted, and a trusted parent directory doesn't count.

`[claude] trust_instances` has niwa mark each instance root trusted for you.
With it on, every instance niwa creates or applies is trusted in your Claude
Code user config, so a session started at the instance root gets neither
prompt. It's off by default.

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

Every run of the create/apply pipeline then trusts the instance root before the
command exits. That covers `niwa create`, `niwa apply`, `niwa reset`, `niwa
init` when it creates an instance, `niwa dispatch`, and session-start
provisioning for ephemeral instances. Instances that existed before you turned
the option on catch up on their next `niwa apply`.

## What niwa writes

niwa sets one flag, `projects[<key>].hasTrustDialogAccepted = true`, in Claude
Code's user config file. No other value in the file changes.

- **The file** is `$CLAUDE_CONFIG_DIR/.claude.json` when `CLAUDE_CONFIG_DIR` is
  set and non-empty, and `$HOME/.claude.json` otherwise. niwa reads
  `CLAUDE_CONFIG_DIR` from its own environment when the command runs, and
  refuses a relative value with a warning. If the file is a symlink, niwa
  writes the file it points to and leaves the link in place. If the legacy
  `.config.json` exists in Claude Code's config home (`$CLAUDE_CONFIG_DIR`, or
  `$HOME/.claude` when that's unset), Claude Code reads it instead of
  `.claude.json`, so niwa writes nothing and prints a warning naming it.
- **The key** is the instance root's absolute path with every symlink
  resolved, in the root itself and in every parent. That's the key Claude Code
  computes for a session started there. On macOS, for example, a root under
  `/var/folders/...` is keyed as `/private/var/folders/...`. Outside a git
  repository, Claude Code skips its trust dialog in any directory beneath a
  trusted one, so niwa writes nothing and warns when the key is `/`, your
  home directory, Claude Code's config home, or an ancestor of either, the
  key a single-instance `niwa init` run in your home directory would give.
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

The record lives in the instance's `.niwa/instance.json` under `claude_trust`:
the config file path, the key, the mode, and which feature wrote it
(`workspace`, or `review` for a review instance). It's how niwa knows what to
take back later.

When niwa can't parse the config file (it isn't JSON, the top level,
`projects` or the entry isn't an object, or the file can't be read), or can't
get the lock within 5 seconds, it leaves the file exactly as it was and prints
a warning naming the file. The command still succeeds. Nothing is recorded, so
the next apply tries again.

## Taking trust back

niwa removes only what its record names.

- **Destroying an instance** removes the whole entry when the record says
  `created` and only the flag when it says `flag-only`. That holds for every
  route: `niwa destroy` in any form, `niwa reap`, `niwa reset`, dispatch's
  cleanup of a failed launch, and review-instance cleanup. Removal uses the
  recorded file and key, so a destroy run under a different
  `CLAUDE_CONFIG_DIR` still cleans the right file. An entry or flag that's
  already gone is fine.
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
- **A moved instance** gets a fresh grant at its new path. niwa can't prove
  the old entry still belongs to this instance, so it leaves it and warns,
  naming its key and file.

An entry you made yourself, by answering Claude Code's trust prompt, is never
in the record, so niwa never removes it. Every record is checked before niwa
acts on it: the key has to be the resolved path of the instance being
destroyed or applied, and the file has to be named `.claude.json`. niwa never
acts on a record that fails, and warns. An apply also drops a malformed record
(unknown mode or source, a bad file path) from the instance's state, so you
see that warning once rather than on every apply.

A skipped removal (unparseable file, lock held) doesn't stop a destroy. The
warning names the file and key that were left, so you can delete the entry by
hand. A skipped withdrawal on apply keeps the record, and the next apply tries
again.

## What trust lets Claude Code do

Trusting an instance makes Claude Code apply that instance's project
permissions, hooks and MCP servers without asking. That's the point of the
option, and it's also the cost.

Whoever can change the workspace config or the overlay can put hooks and
pre-approved MCP servers in front of every developer who opted in, with no
prompt. A stdio server runs a command, so a pre-approved stdio server runs a
command the config's author picked. If trust comes from an overlay, treat the
overlay's repository like code that runs on your machine. It is.

When trust is on only because the overlay set it, niwa prints a one-time
notice saying so, naming the key and file it wrote and what trust allows. The
notice is recorded in the workspace root's state, so it appears once per
workspace. A later overlay commit that adds a server or changes a command
reaches trusted sessions with no new notice, and a dispatched worker never sees
the notice at all. In a single-instance layout there's no workspace root state
to record it in, so the notice appears on any apply that writes trust.

Code that already runs in the instance as you (a build script in a cloned
repository, an agent session) can rewrite the instance root's
`.claude/settings.json` or `.mcp.json`, and with the root trusted, Claude Code
honors the rewrite. That code could already do anything you can, so trust
gives it another route rather than more reach.

## Which sessions it covers

Trust covers a session started at the instance root. A session started inside
a cloned repository is keyed by that repository, not by the instance root, and
still sees Claude Code's prompts. niwa doesn't trust repository directories or
the workspace root.

## Still seeing prompts?

Work through these in order:

1. **Start the session at the instance root**, not inside a repository and not
   at the workspace root.
2. **Run `claude doctor`** and look for settings errors. One invalid value in
   the instance's `.claude/settings.json` makes Claude Code ignore the whole
   file, approval list included, and trust can't help until the file loads.
3. **Run `claude mcp list` at the instance root** and look for "Pending
   approval". A server shown that way isn't on an approved list Claude Code
   read.
4. **Check `CLAUDE_CONFIG_DIR`.** niwa wrote to the file its own environment
   named. A session started under a different `CLAUDE_CONFIG_DIR` reads a
   different file and won't find the entry. `claude_trust.config_file` in the
   instance's `.niwa/instance.json` shows the file niwa used.

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
approved-server list, sorted by name together with the workspace config's
servers. A worktree created with `niwa worktree` gets them too; a vault value
that path can't resolve is left out with a warning.

## Review instances

`niwa watch` review instances use the same writer and the same record, with
source `review`. Destroying a review instance withdraws its grant through the
record like any other. A review instance created in a workspace that has
`trust_instances` on starts with a workspace grant, and the review relabels it
as its own, so an apply in that instance with the option off can't pull trust
out from under a running review. An apply never withdraws a review grant.

## Known limits

- **Environment skew.** niwa can only write the file its own environment
  names. A session started later under a different `CLAUDE_CONFIG_DIR` still
  prompts.
- **Entries left by older niwa versions.** Review-instance entries written
  before review trust moved onto the record have no record, so reap no longer
  removes them. Their paths are already gone, so they grant nothing, but they
  stay in the file until you delete them.
- **Entries for paths that no longer exist.** A withdrawal skipped during
  destroy, and an instance deleted by hand instead of through niwa, leave a
  trusted entry behind. If niwa later creates an instance at the same path,
  that entry already trusts it, and since the entry is already trusted, niwa
  records nothing for the new instance. Destroy's warning names the file and
  key to delete.
- **Entries left by the off switch.** Turning the option off clears the
  record, so an entry niwa created stays in the config without the flag, and
  destroying the instance doesn't remove it. It trusts nothing; delete it by
  hand if you want the file tidy.
- **A session still running in a destroyed instance** can write its project
  entry back when it saves state. That entry has no trust flag, so it's
  leftover data, not trust.

## Verified with

The live check runs at a fresh instance root created with the option on and an
overlay-declared server (`claude -p "list your mcp tools"` in default mode,
`claude mcp list`, `claude doctor`), and again inside one of that instance's
cloned repositories, where the trust dialog should still appear.

Last passed on Claude Code 2.1.296, macOS, on 2026-10-13, with a niwa built
from this change plus the declared-server approval list:

- `niwa create` with `trust_instances = true` set in the overlay wrote
  `projects["/private/tmp/.../tc"].hasTrustDialogAccepted = true` (the
  symlink-resolved path of an instance under `/tmp`), recorded it as
  `created`, and printed the one-time overlay notice.
- At the instance root, `claude mcp list` showed the overlay-declared HTTP
  server as connected, `claude -p` in default permission mode listed its tools
  with no prompt, and `claude doctor` reported no settings errors.
- Inside a cloned repository of the same instance, `claude mcp list` showed the
  server as pending approval, as expected: that session is keyed by the
  repository.
- `niwa destroy` removed the entry niwa had created and left no other entry
  under the instance path.
- The Claude Code binary contains no `hasTrustDialogHooksAccepted` string, so
  there's no separate hooks-trust prompt for niwa to pre-answer.
