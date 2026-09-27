<!-- decision:start id="prewarm-marketplace-pin" status="assumed" -->
### Decision: how the plugin pre-install applies the resolved marketplace ref

**Context**

niwa resolves a ref for each github marketplace: the latest stable release by default, nothing for `track = "main"`, or an explicit ref. It writes that ref into the instance's `.claude/settings.json`. The pre-install in `prewarmDeclaredPlugins` then registers the marketplace with `claude plugin marketplace add` and installs the declared plugins. That registration decides which commit gets installed. Until now the ref was dropped, so every instance installed the default branch (#327).

Passing the ref as `<repo>#<ref>` works in a fresh HOME. But `marketplace add` defaults to `--scope user`, and Claude Code refuses any network source that differs from a user-scope declaration of the same name. Measured with Claude Code 2.1.283 in throwaway HOMEs, and holding at every scope:

- After one pinned user-scope add, later adds of a newer tag are refused, and so are bare adds.
- On a HOME with a bare user-scope declaration, which is what every earlier pre-warm left behind and is the state of the host this was measured from, every pinned add is refused.
- The conflict check reads only the user-scope declaration. It ignores other projects' local declarations and the instance's own settings.json.
- With no user-scope declaration, local-scope adds with different refs from different projects all succeed. The installed plugins keep per-project versions in the version-split plugin cache: one HOME ended with project A on koto-skills 0.13.0 (`b4db4518`) and project B on 0.12.2 (`1ca8c980`).
- The single marketplace clone and `known_marketplaces.json` follow whichever project added last.
- `marketplace remove` uninstalls the marketplace's plugins from every project in the HOME, so replacing a registration is out.
- A directory-source add with the same name bypasses the check and takes over the HOME-wide registration, so a niwa-managed checkout is out too.

**Assumptions**

- A Claude Code session starting in an instance doesn't re-clone or rewrite the HOME-wide marketplace registration to match that instance's declared ref. Not tested: it needs an authenticated interactive session. If wrong, instances with different pins would move the shared clone back and forth at each session start, the per-instance pin would last only until another instance's session starts, and not pinning (option c) becomes the stable choice. The check is one authenticated run: two instances pinned to different refs, a session in each, then read the clone HEAD and `installed_plugins.json`.
- Claude's settings precedence is local over project over user. That's documented behaviour, not measured here. If wrong, the local-scope declaration the add writes and niwa's managed settings.json declaration could disagree with unclear effect. They name the same ref whenever the add succeeds, so this only matters if they diverge later.
- niwa doesn't manage `.claude/settings.local.json` at the instance root. The existing `install --scope local` already writes it without tripping the drift check (#179), which is consistent.

**Chosen: pin at local scope, fall back visibly (d)**

For each github marketplace the pre-install runs `claude plugin marketplace add <target> --scope local` from the instance root. `<target>` is `<repo>#<ref>` when the instance settings carry a ref and the bare repo otherwise. niwa no longer writes the user's `~/.claude/settings.json`.

- **When the add succeeds:** install each declared plugin with `--scope local` as today. For a plugin already installed for this instance, also run `claude plugin update <plugin> --scope local`. `install` is a no-op for an installed plugin, and without the update a re-apply would never move an instance to a new pin.
- **When the add is refused:** warn, naming the marketplace, the pin that wasn't applied, and the reason: a same-named marketplace is already declared for the whole HOME with a different source. Point at the fix, which is to remove that HOME-wide declaration. Then continue with the plugin installs against the registration already in place.

That path is never silent, and niwa never removes or rewrites a registration it didn't just make. Bare (`track = "main"`) entries use the same local-scope add, which a bare HOME-wide declaration accepts.

The comment in `prewarmDeclaredPlugins` and the `track` section of `docs/guides/workspace-config-sources.md` change to match. The "known limitation" note that the switch to releases is blocked upstream is wrong for this path. The docs should say instead that the pin applies unless the HOME already declares the marketplace at user scope.

**Rationale**

It's the only option that applies the pin where the pin can apply without acting beyond the instance, and that never makes a HOME worse than it found it.

- (a) as coded writes the first pin at user scope, which freezes the whole HOME: every later release and every `track = "main"` instance is refused until someone edits their settings by hand. That's strictly worse than today.
- (b) would need niwa to parse Claude's own settings file for an exact pre-check, because `list --json` reports the registration, not the declaration the refusal is based on. As specified it keeps the user-scope add, so it inherits (a)'s freeze.
- (c) is uniform and honest, but it gives up a pin that measurably works per instance on any HOME without a user-scope declaration: CI runners, new machines, and this host once its stale declaration is removed.

Attempting the add and treating a non-zero exit as the refusal needs no knowledge of Claude's file layout. Warning on the refusal satisfies the requirement that the unapplied pin always be visible.

**Alternatives Considered**

- **Pass `#ref` at the default user scope (a)**: the current branch as first written. Rejected: the first pin freezes the HOME for every later release and every bare add, and on HOMEs with a bare declaration it fails on every provision.
- **Pre-detect an existing registration and skip with a note (b)**: rejected. An exact check means parsing Claude's user settings, since `list --json` reports the HOME-wide registration and not the declaration the check uses, and the two were seen to diverge. As specified it still declares at user scope, so it freezes fresh HOMEs like (a).
- **Stop pinning in the pre-install (c)**: bare registration, corrected docs, and a visible note when the settings name an unapplied ref. Rejected on the evidence available: it throws away a working per-instance pin on every HOME without a user-scope declaration. It stays the fallback if the startup assumption turns out wrong.

**Consequences**

- On a HOME with no user-scope declaration, instances install the release their settings name, and each instance keeps its own plugin version.
- A `niwa apply` after a new release moves that instance, and only that instance.
- The shared marketplace clone follows the last instance that ran the pre-install. That affects only new installs and the untested startup path.
- On a HOME that already declares the marketplace at user scope, nothing changes in what gets installed, but every create and apply now prints which pin wasn't applied and why.
- To adopt pins there, the user removes the HOME-wide declaration once. That also removes that marketplace's installed plugins HOME-wide until each instance is re-applied, so it's a deliberate user step, not something niwa does.
- Instances that do get pinned stop seeing fixes merged to shirabe or koto after their latest release until a release is cut. At decision time that was shirabe's `coordinate-skill` (#397), 2 commits past v0.20.0, and three koto commits (release chores and a docs change) past v0.13.0.
- The pre-install makes one extra `plugin update` call per already-installed plugin.
<!-- decision:end -->
