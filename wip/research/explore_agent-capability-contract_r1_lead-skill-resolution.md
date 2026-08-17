# Lead: How are marketplace plugins and their skills resolved and installed today, and where does that resolution depend on Claude Code being installed on the machine?

## A. Plugin/Skill Mechanism on main

**Config surface.** `workspace.toml` declares marketplaces under `[claude.marketplaces]`
via `MarketplaceConfigs` (`internal/config/config.go:77-93`), which accepts either the
legacy bare-string list form or a table form:

```toml
marketplaces = ["org/repo", "repo:tools/.claude-plugin/marketplace.json"]

[[claude.marketplaces]]
source = "org/repo"        # required
auto_update = true          # optional bool
track = "latest"            # optional string
```
(`internal/config/config.go:84-189`, comment block at 95-113). Plugins to enable are a
separate `plugins` list of `"<plugin>"` or `"<plugin>@<marketplace>"` strings, consumed
in `internal/workspace/materialize.go:909-916`.

**What niwa actually does.** Two independent things, both on main:

1. **niwa's own plugin** (`internal/plugin/embed.go`, `installer.go`) is embedded in the
   binary at build time (`//go:embed files/niwa`, `embed.go:25`) and written directly to
   disk by `stageAndRename` (`installer.go:133-175`) — no `claude` invocation, no network.
   This is unrelated to marketplace-declared plugins; it's niwa shipping its own
   `/niwa:migrate-config` skill.

2. **Marketplace-declared plugins** are *not* fetched or installed by niwa itself. niwa
   only writes a **manifest Claude Code reads later**: `materialize.go:909-937` builds
   `enabledPlugins` (map of plugin name → `true`) and `extraKnownMarketplaces` (map of
   marketplace name → source entry, via `mapMarketplaceSourceWithIndex`) into the
   instance's `.claude/settings.json`. Claude Code itself is the thing that reads that
   file at its own startup and performs the real install (clone marketplace, populate
   plugin cache).

   There is one best-effort acceleration of that: `prewarmDeclaredPlugins`
   (`internal/cli/dispatch_plugins.go:48-97`) reads the just-written
   `.claude/settings.json` back (`readInstanceSettings`, line 172) and **shells out to
   the `claude` CLI** — `claude plugin marketplace add <repo>` for each github-sourced
   marketplace (line 72) and `claude plugin install <plugin> --scope local` for each
   enabled plugin (line 93) — via `runClaudePluginCmd` (line 112), which resolves the
   binary with `lookClaude()` (`internal/cli/dispatch.go:113`). This exists solely to
   close a *race* (comment, lines 33-36): a github marketplace clone that Claude Code
   would otherwise do lazily during its own first skill enumeration might not finish
   before that enumeration runs. It is explicitly "best-effort" and falls back silently
   to "Claude still installs from settings.json at startup" (lines 42-47) — i.e. it
   assumes a Claude Code session is the thing that ultimately does the real install.

**Where plugin files end up:**
- niwa's own plugin: `~/.claude/plugins/marketplaces/niwa/` (`internal/plugin/embed.go:78`,
  disclosure text at `internal/workspace/disclosure.go:41`).
- Marketplace-declared plugins: wherever **Claude Code** puts them when it does the
  real install — `~/.claude/plugins/marketplaces/<name>/` for github sources (this is
  Claude Code's own convention, not something niwa's mainline code computes/reads).
  niwa's `prewarmDeclaredPlugins` never inspects that path; it only invokes `claude`
  and discards success/failure into a warning.

**Does main read a Claude-Code-owned global directory?** Only for **hygiene
bookkeeping**, never for skill/plugin *content*:
- `internal/pluginrecord/registry.go:32` — `~/.claude/plugins/installed_plugins.json`,
  read/written by `Prune` (`prune.go`) to remove dangling records when a niwa instance
  is destroyed. It parses `scope`/`projectPath`/`installPath` fields only to decide
  removal; it never reads plugin *content*.
- `internal/pluginrecord/marketplaces.go:26` — `~/.claude/plugins/known_marketplaces.json`,
  read/written by `ReconcileAutoUpdate` to fix a stale `autoUpdate` flag Claude Code
  won't overwrite from project settings on its own. Same story: metadata only, and it
  explicitly "never adds a marketplace" (comment, line 61) — "Claude Code owns
  registration."

So **on main, no code path resolves skill content out of `~/.claude/plugins`.** The only
thing consulted there is Claude Code's *own* bookkeeping files, for pruning/reconciling
flags — not for sourcing a plugin's skills. The dependency the lead worries about does
not exist on main today.

## B. The Prior Attempt's Symlink Source

On `origin/docs/dual-agent-workspace` (commit `26c69e35b1` "feat(worktree): materialize
the Codex payload and composed override", merged into `9bedc38` at HEAD of that branch),
a new file `internal/workspace/codex_payload.go` adds the second-agent (Codex) skill
delivery the lead question is about. It writes, per instance, one symlink per configured
plugin pointing at that plugin's whole installed tree
(`git show origin/docs/dual-agent-workspace:internal/workspace/codex_payload.go:28-29`).

The symlink SOURCE is resolved by `resolveCodexPluginRoots` →
`codexMarketplaceRoots` (same file, ~lines 288-378). It branches on marketplace kind:

- **repo-sourced** marketplace (`repo:` prefix): resolves through
  `ResolveMarketplaceSource` into the **workspace's own clone** (`cfg.RepoIndex`) —
  niwa-owned, no Claude Code dependency (lines ~347-353).
- **github-sourced** marketplace: 
  ```go
  // A github-sourced marketplace is cloned into Claude Code's user-global
  // plugin directory, keyed by the same registration name.
  pluginsRoot, rootErr := claudePluginsRoot()
  ...
  roots[name] = filepath.Join(pluginsRoot, "marketplaces", name)
  ```
  (`codex_payload.go:366-375`), where `claudePluginsRoot()` is
  `filepath.Join(home, ".claude", "plugins")` (`codex_payload.go:60-65`, comment at
  56-59: "resolves Claude Code's user-global plugin directory, where a github-sourced
  marketplace's clone lives").

**Confirmed:** for the common case (a GitHub-hosted marketplace, e.g. `shirabe`), the
Codex symlink's source directory is literally `~/.claude/plugins/marketplaces/<name>/`
— populated only by `claude plugin marketplace add`, i.e. only by Claude Code itself
(either via the best-effort prewarm in part A, or via a live Claude Code session's own
startup). If neither ever ran — e.g. no Claude Code binary on the machine at all — that
directory never exists, and `readPluginSourceDir` (`codex_payload.go:451+`) fails to
find the plugin's source subdirectory.

To the branch's credit this is **not silently swallowed**: `resolvePluginRoot`
(lines 379-433) records a `MissingPluginRoot` whose `Reason` is exactly "the plugin is
declared but not installed (the pre-warm that installs it is best-effort, and can be
skipped, absent, or timed out)" (line 429), surfaced via
`MissingPluginRoot.String()` (lines 90-91): *"Codex sessions in this instance get none
of its skills until it is installed and `niwa apply` re-runs."* That message is honest
about the *symptom* but wrong about the *remedy* on a Claude-Code-less machine: re-
running `niwa apply` cannot fix it, because nothing in that rerun can populate
`~/.claude/plugins/marketplaces/<name>/` without the `claude` binary — the "self-heal"
loop the message implies doesn't close in that environment. That's the defect: the
warning is loud (good), but the suggested recovery path is unavailable, and the
underlying resolution genuinely has no non-Claude-Code path for github marketplaces.

## C. A Claude-Code-Independent Route

niwa already has the low-level machinery to fetch a GitHub repo's tree itself, without
`git` or Claude Code:
- `internal/github/fetch.go:99` — `APIClient.FetchTarball(ctx, owner, repo, ref, etag)`
  downloads a repo tarball via the GitHub API.
- `internal/github/tar.go:68` / `:248` — `ExtractSubpath` / `ProbeAndExtractSubpath`
  extract a validated subtree from that tarball to a destination directory (symlink/
  device/FIFO rejection, path traversal checks, size caps — see comments at
  `internal/workspace/fallback.go:36-46`).

These are used today for **repository cloning/snapshotting** — e.g.
`internal/workspace/snapshotwriter.go:544-597` calls `FetchTarball` then
`ExtractSubpath`/`ProbeAndExtractSubpath` to materialize a niwa-managed repo's content
into an instance — and for `niwa source inspect` (`internal/cli/source_inspect.go:136`).
**They are not used anywhere for marketplace/plugin resolution on main or on the prior
branch.** The prior branch's `codex_payload.go` only *reads* a directory Claude Code
already populated; it never calls into `internal/github`.

**The gap, concretely:** what a skills symlink needs is a marketplace's
`.claude-plugin/marketplace.json` manifest plus each declared plugin's source
subdirectory, sitting somewhere on local disk niwa controls. Today niwa has:
- the *parsing* logic for that manifest already, in the prior branch's
  `readPluginSourceDir` (`codex_payload.go:451+`) — it just points at the wrong root
  for github sources.
- the *fetch* primitive already, in `internal/github` (`FetchTarball` +
  `ExtractSubpath`), proven safe and already wired into the apply pipeline via
  `snapshotwriter.go`.

What's missing is the connective tissue: a marketplace-fetch step that, for a
github-sourced marketplace, calls `FetchTarball`/`ExtractSubpath` into a niwa-owned
cache directory (something like `<instance>/.niwa/plugins/marketplaces/<name>/` or a
per-workspace cache — not `~/.claude/...`), the same way `snapshotwriter.go` already
does for ordinary repos, and then points both `resolveCodexPluginRoots` (for Codex) and
(optionally) the Claude settings' `extraKnownMarketplaces` at that niwa-owned root. That
would let niwa host the content itself instead of relying on Claude Code's process to
populate it — which is the "either implemented or explicitly declared unavailable"
contract the lead question asks for, since a fetch failure there is a normal, loud
niwa-side error rather than a silent dependency on a third-party CLI having run first.

## D. Failure Mode Without Claude Code Installed

**On main**, `niwa apply` with a marketplace declared, on a machine with no `claude`
binary:
- `materializeSettings` (`internal/workspace/materialize.go:909-937`) still writes
  `enabledPlugins`/`extraKnownMarketplaces` into `.claude/settings.json` unconditionally
  — this doesn't touch the filesystem beyond that file, so it always succeeds.
- `InstallNiwaPlugin` (niwa's own embedded plugin, `apply.go:494-495` /
  `651-652` / `994-995` / `1023-1024`) succeeds regardless of Claude Code, since it's a
  pure filesystem write from embedded bytes.
- `PrewarmDeclaredPlugins` → `prewarmDeclaredPlugins` (`dispatch_plugins.go:48-97`) calls
  `lookClaude()` inside `runClaudePluginCmd` (line 113) for every marketplace/plugin;
  each call fails immediately (`claude` not found), and the failure is turned into
  `warnPrewarm(reporter, "pre-warming marketplace %q (...): %v; it will install on
  startup instead", ...)` (lines 73, 94) — a **non-fatal warning**, printed via
  `reporter.Warn` if a reporter was supplied, otherwise silently dropped (`warnPrewarm`,
  lines 101-105: "tolerating a nil reporter"). `niwa apply` completes successfully
  either way — this is a warn-and-continue posture by design (doc comment,
  `dispatch_plugins.go:46-47`: "a provision must never be less robust than before when
  the plugin CLI is unavailable").

  So today, on a Claude-Code-less machine, `niwa apply` **does not fail loudly** for a
  declared marketplace — it warns (if a reporter is present) that pre-warming was
  skipped and says installation will happen "on startup instead." That statement is
  true for a *Claude Code* session (which does its own settings.json-driven install at
  launch) and is the reason main has no real defect here: main never promises anything
  to a second agent, so "install happens at Claude's own startup" is a complete,
  accurate story for the only consumer that exists today.

**On the prior-attempt branch**, the same machine additionally runs
`InstallCodexPayload` (`apply.go` step 6c, prior branch), which calls
`resolveCodexPluginRoots` → for each github-sourced marketplace/plugin, finds
`~/.claude/plugins/marketplaces/<name>/` missing, and appends a `MissingPluginRoot` to
`CodexPayloadResult.MissingRoots`, surfaced as a warning line in `apply.go`
(`allWarnings = append(allWarnings, m.String())`, per the earlier stat/grep). This *is*
loud (a per-plugin warning naming the plugin and the expected path), but as covered in
B, the remedy it names ("niwa apply re-runs") does not actually work in a Claude-Code-
less environment — there's no code path on that branch that can ever populate the
directory it's waiting on.

## Implications

- The Claude-Code-global-directory coupling the lead worried about **does not exist on
  main**. It was introduced entirely by the prior attempt's `codex_payload.go`, and only
  for the github-marketplace branch of `codexMarketplaceRoots`. PR 2 (Codex as second
  implementation) needs to either avoid reintroducing that branch as-is, or pair it with
  a niwa-owned fetch step (see C) before landing.
- Directory/repo-sourced marketplaces (`repo:` prefix) were already Claude-Code-
  independent on the prior branch — they resolve through `cfg.RepoIndex`, the
  workspace's own clone. Only github-sourced marketplaces have the gap.
- niwa already owns a safe, tested tarball-fetch primitive (`internal/github`) that is
  structurally suited to closing this gap without shelling out to `claude` at all; it
  is currently used only for ordinary repo materialization, not plugins.
- The prior branch's `MissingPluginRoot` warning mechanism is a good model for "loud,
  declared limitation" in general — the test-worthy property the lead question wants
  is closer to "does the reported reason correctly describe an available remedy," not
  merely "is a warning emitted at all." The existing message fails that stricter bar in
  the no-Claude-Code case.

## Surprises

- niwa's *own* embedded plugin install (`internal/plugin`) already fully sidesteps this
  problem — it embeds its content in the binary and writes it directly, no network, no
  external CLI. That's the existing precedent for "niwa owns its content" that the
  marketplace path could be brought in line with.
- `prewarmDeclaredPlugins`'s doc comment already anticipates and accepts "claude
  absent" as a normal, non-fatal case (`dispatch_plugins.go:46`) — the warn-and-continue
  posture for *that* mechanism is intentional and fine; it's only the prior branch's
  *symlink resolution*, which has no fallback at all, where the gap actually bites.

## Open Questions

- Does the prior branch's design intend for repo-sourced (`repo:`) marketplaces to
  become the *recommended* pattern for anything meant to work without Claude Code, or
  is github-sourced expected to stay primary (e.g. because `shirabe` itself is
  github-hosted)? If github-sourced stays primary, closing the gap in C is not optional
  — it's required for any second-agent skill delivery to work at all on a plain
  checkout.
- Is there an existing niwa-owned cache location convention (something under
  `~/.niwa/` or per-workspace `.niwa/`) that a marketplace-fetch step should reuse,
  or would this be the first such cache?

## Summary
On main, niwa never resolves plugin/skill *content* out of a Claude-Code-owned
directory — it only writes `enabledPlugins`/`extraKnownMarketplaces` into
`.claude/settings.json` for Claude Code's own startup to consume, and its best-effort
`claude plugin marketplace add`/`install` pre-warm (`internal/cli/dispatch_plugins.go`)
warns and continues if `claude` is absent, which is a complete story since main has no
second agent to serve. The defect is entirely in the prior attempt's
`internal/workspace/codex_payload.go`: for github-sourced marketplaces (not
repo-sourced ones), `codexMarketplaceRoots` points the Codex skills symlink at
`~/.claude/plugins/marketplaces/<name>/`, a directory only `claude plugin marketplace
add` populates, so a Claude-Code-less machine gets a loud `MissingPluginRoot` warning
whose suggested fix ("niwa apply re-runs") cannot actually succeed. niwa already owns
the tarball-fetch primitives (`internal/github.FetchTarball` + `ExtractSubpath`,
already used for ordinary repo cloning in `snapshotwriter.go`) needed to fetch a
github-sourced marketplace into a niwa-owned directory instead, closing the gap without
depending on Claude Code being installed.
