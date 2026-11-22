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

## GitHub marketplaces: one held copy per workspace

A marketplace with a GitHub source (`source = "owner/repo"`) isn't kept in the
workspace root. niwa resolves its track to a commit and holds that release
once per workspace, under your home:

```
~/.niwa/w-<key>/plugins/github.com/<owner>/<repo>/<commit>/
  tree/        the marketplace at that commit, read-only
  held.json    the release's mark: source, ref, commit and a seal of tree/
```

`<key>` comes from the workspace root's real path, so every path that reaches
one root shares one store. An instance holds only an absolute symlink to the
release, at `<instance>/.niwa/marketplaces/<name>`, so a second instance of
the workspace downloads nothing. An instance stays on the release it was
created with; a newer tag reaches the instances created after it.

Don't edit files under a held `tree/`. A changed tree no longer matches its
seal, and the next provisioning command fetches the release again.

### Turning sharing off

```toml
[workspace]
share_plugin_releases = false
```

With sharing off, a new instance fetches its own copy of each GitHub
marketplace into `.niwa/marketplaces/<name>`, at the unresolved ref, as niwa
did before the store. It turns off nothing else: an instance that already
links into the store keeps its link, the restore below still runs for every
linked release, and so does the sweep. Only the workspace's own config sets
it.

### When the store can't be used

If the store can't be used, the instance gets its own copy, fetched at the
same commit the store would have held, and the command says which store and
why. The line looks like this:

```
warning: the host plugin store at /home/you/.niwa/w-<key>/plugins can't be used (unwritable); this instance fetched its own copy of marketplaces: skills, tools.
```

`niwa create` and `niwa apply` print it on the terminal, `niwa dispatch`
prints it on its stderr, and the SessionStart hook puts it in the session's
context. A command that falls back for several marketplaces on one store
gives one line naming each of them. The condition is one of these:

- `unreadable`: `plugins/` or a held release in it can't be read or walked.
- `unwritable`: nothing can be written in `plugins/` itself, whatever the
  modes above it.
- `can't be created`: `plugins/` doesn't exist and can't be made, because the
  directory above it can't be written.
- `release not placed`: the store is there but couldn't take this release,
  because its staging area couldn't be written, say, or the download was
  refused.

niwa never changes the mode of a directory it didn't just create, so a store
whose mode was narrowed by hand stays that way until you put it back. Once
the store can be used again, the next new instance links into it.

### Restoring a release

Every `niwa create`, `niwa apply` and `niwa dispatch`, and the SessionStart
hook, checks the releases that instances on disk link to. A release that's
missing, has lost its `held.json`, or whose tree no longer matches its seal
is fetched again at its commit and marked, and the command says so. There's
nothing to run: the next provisioning command restores it with nobody
acting. A release whose commit isn't shown to be in its source's own
history, or whose source the workspace no longer declares, is reported and
left as it is.

### Unused releases

The same commands sweep the store. A held release that no instance on disk
links to is deleted, except the newest held release of each source, which
stays for the next instance. An instance kept under `~/.niwa/w-<key>/kept/`
counts as on disk. When the sweep can't read every instance, or the store
itself, it deletes nothing and says why.
