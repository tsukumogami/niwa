# Marketplaces from a workspace repo

A workspace can declare a Claude Code plugin marketplace that lives in one of
its own repos:

```toml
[claude]
marketplaces = ["repo:team-plugins/.claude-plugin/marketplace.json"]
plugins = ["review@team-plugins"]
```

Claude Code keeps one entry per marketplace name in
`~/.claude/plugins/known_marketplaces.json`, shared by every project on the
machine. If each instance registered the marketplace from its own clone of
`team-plugins`, that one entry would follow whichever instance registered last:
every other instance's sessions would load plugins and hooks from someone
else's clone, and destroying that instance would leave the entry naming a
directory that's gone.

So niwa registers a `repo:` marketplace from its own copy of the repo, kept at
the workspace root, outside every instance.

## The managed copy

```
<workspace root>/.niwa/marketplaces/<repo>/
  repo.git/          bare clone of the repo
  checkouts/<sha>/   a checkout of one commit
  current            symlink to the checkout in use
```

Every `niwa create` and `niwa apply` fetches the repo, checks out the
configured commit, and points `current` at it. The instance settings and
Claude Code's global entry both name `.../<repo>/current`, so the registered
path never changes:

- `create` and `apply` rewrite the global entry's `source.path` and
  `installLocation` to the managed copy whenever they find it pointing anywhere
  else, such as an instance clone registered by an older niwa. The file is
  replaced atomically, other marketplaces' entries are left as they were, and a
  backup is kept beside it.
- niwa doesn't create a missing entry. Claude Code adds it itself the first time
  it installs one of the marketplace's plugins (niwa's plugin pre-install after
  `create`, or a session's startup), reading the instance settings, which already
  name the managed copy.
- `destroy` doesn't touch the entry. Nothing it removes is registered.
- A worktree of the marketplace's repo registers the managed copy too, not the
  worktree.
- The managed copy needs a workspace root to live in. In a single-instance
  layout, where the instance has no workspace root above it, the instance keeps
  registering its own clone.

A new commit is checked out next to the old one and `current` is then swapped
in a single rename, so a session reading the marketplace sees either the old
tree or the new one, never a mix. The checkout it replaced stays until the next
swap, then goes.

Don't edit files under `.niwa/marketplaces/`. They're replaced on the next
apply.

## Which commit

The `track` of a `repo:` marketplace picks the commit:

```toml
[[claude.marketplaces]]
source = "repo:team-plugins/.claude-plugin/marketplace.json"
track = "v2.4.0"
```

- Unset: the remote's default branch, asked of the remote on every apply.
- Anything else: that branch, tag or commit. `main` means the branch named
  `main`, not whatever the default branch is.
- `release` is a config error for a `repo:` source.

Every marketplace in the same repo shares one copy, so they share one track; a
second, different track is reported and ignored.

## When an update fails

If the fetch fails, the ref doesn't exist, or the commit has no marketplace
manifest where the source says, the apply warns and carries on. The managed
copy keeps the commit it already had, and the entry keeps naming it.

When there's no managed copy yet (the first apply on a machine failed to
fetch), the instance's settings fall back to its own clone and niwa leaves the
global entry alone. The next apply that fetches successfully moves both to the
managed copy.

## Trying unmerged plugin changes in one session

The registered marketplace is the reviewed code at the configured commit, for
every session on the machine. To try a plugin change from your own clone,
start one session with Claude Code's `--plugin-dir`, pointing at the plugin
directory inside your clone:

```bash
claude --plugin-dir <instance>/<group>/team-plugins/<path to the plugin>
```

Claude Code's plugin loading reference (the name conflicts section) says a
plugin loaded this way replaces an installed marketplace plugin with the same
manifest name, for that session only. Nothing is written to
`known_marketplaces.json`, and other sessions keep loading the managed copy.

niwa has no override of its own on purpose: anything niwa could change is the
global entry, and pointing that at your clone is exactly the move that hands
every other session on the machine your unmerged code.

Two workspaces on one machine that declare a marketplace with the same name
still share Claude Code's one entry for it, so each apply points it at that
workspace's managed copy. Give them different marketplace names if they need
different code.
