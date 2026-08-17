# Lead: What must niwa write for a Codex session to load the workspace's marketplaces, plugins, skills, and MCP servers?

All experiments ran against `codex-cli 0.147.0`
(`/home/dgazineu/.tsuku/tools/codex-0.147.0/bin/codex`, a stripped static-pie ELF,
247 MB) with an isolated `CODEX_HOME` under
`/home/dgazineu/.claude/jobs/7838923c/tmp/lab/`. Every lab `CODEX_HOME` started
from an **empty directory** — nothing was copied from, symlinked to, or written
into the real `~/.codex`. The real home was read only (`cat config.toml`,
`find plugins`), never modified. No `forced_login_method` was written anywhere,
no `codex login`/`logout` was run, and the lab homes carried no `auth.json` at
all, so no credential was ever in reach.

The test subject is a purpose-built two-file plugin I wrote from scratch
(`niwaprobe`, one skill whose description contains the unique marker
`ZZPROBEALPHA`), plus, for the end-to-end check, a plugin sourced from a public
local directory checkout in this workspace. Verification is
`codex debug prompt-input`, which renders the model-visible prompt as JSON; a
skill "loads" iff its description appears in that JSON.

---

## Findings

### 1. The minimal sufficient file set (established by subtraction)

**Answer: two things. A `[plugins."<name>@<marketplace>"] enabled = true` entry
in `config.toml`, and a real directory tree under
`$CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/` containing
`.claude-plugin/plugin.json` with a matching `name`. Nothing else.**

The `[marketplaces.*]` block is **not** required for skills to load.

Baseline (works). `CODEX_HOME` contains exactly:

```
config.toml
plugins/cache/nlab/niwaprobe/9.9.9/.claude-plugin/plugin.json
plugins/cache/nlab/niwaprobe/9.9.9/skills/probe-alpha/SKILL.md
```

`config.toml`:

```toml
[marketplaces.nlab]
source_type = "local"
source = "/home/dgazineu/.claude/jobs/7838923c/tmp/lab/src"

[plugins."niwaprobe@nlab"]
enabled = true
```

Command and result:

```
$ cd /…/lab/proj
$ CODEX_HOME=/…/lab/H_B codex debug prompt-input "hi"
```

The prompt JSON contains:

```
- niwaprobe:probe-alpha: ZZPROBEALPHA marker skill used to verify plugin skill
  loading. Use when the user says zzprobealpha.
  (file: /…/lab/H_B/plugins/cache/nlab/niwaprobe/9.9.9/skills/probe-alpha/SKILL.md)
```

Subtraction table. Each row is a separate `CODEX_HOME` built from scratch; the
result is whether `niwaprobe:probe-alpha` appears in the prompt JSON. Every run
exited 0 with empty stderr.

| # | Change from baseline | Skill in prompt |
|---|---|---|
| H_A | Delete the whole `plugins/cache` tree, keep both config blocks | **absent** |
| H_B1 | Delete the entire `[marketplaces.nlab]` block | **present** |
| H_B2 | Delete the `[plugins."niwaprobe@nlab"]` block | **absent** |
| H_B3 | Point `source` at `/nonexistent/path/does/not/exist` | **present** |
| H_B4 | Delete `.claude-plugin/plugin.json` from the cache | **absent** |
| H_B5 | Rename version dir `9.9.9` → `whatever` | **present** |
| H_B6 | `enabled = false` | **absent** |
| H_B7 | Empty `config.toml`, cache intact | **absent** |
| C1 | Cache under `cache/othermkt/…`, config still says `@nlab` | **absent** |
| C2 | Cache dir `cache/nlab/otherplug/…`, config says `niwaprobe` | **absent** |
| C4 | `plugin.json` = `{"name":"totallydifferent","version":"9.9.9"}` | **absent** |
| C5 | `plugin.json` = `{}` | **absent** |
| D1 | `plugin.json` = `{"name":"niwaprobe"}` (no `version`) | **present** |
| C6 | Add `this_key_does_not_exist = "x"` and `[bogus_table]` to config | **present** |
| D2 | Plugin files placed directly under `cache/nlab/niwaprobe/` (no version dir) | **absent** |

So: the marketplace name and plugin name in the config key are used as literal
path segments (`cache/<marketplace>/<plugin>/`), the version directory must
exist but its **name is arbitrary**, and `plugin.json` must exist and its `name`
must equal the plugin name from the config key. `version` in `plugin.json` is
optional.

**Multiple version directories: the highest wins, compared as a version, not a
string.** With `1.0.0` and `2.0.0` both present (markers `ZZV1`/`ZZV2`), the
prompt carried `ZZV2`. With `9.9.9` and `10.0.0` (markers `ZZNINE`/`ZZTEN`), the
prompt carried `ZZTEN` — lexicographic ordering would have picked `9.9.9`.

**Symlinks: allowed everywhere except the version directory itself.** This is a
sharp, reproducible edge and it matters for a Go materializer that would rather
link than copy.

| Layout | Result |
|---|---|
| `cache/nlab/niwaprobe` is a symlink to a dir holding `9.9.9/` | **present** |
| `cache/nlab/niwaprobe/9.9.9` is a symlink to the plugin checkout | **absent** (verified twice, D3 and F1) |
| `…/9.9.9/skills` is a symlink to the checkout's `skills/` | **present** |
| `…/9.9.9/skills/probe-alpha` is a symlink to one skill dir | **present** |

The version level is discovered by directory enumeration; the marketplace and
plugin levels are path-joined from config keys, which is consistent with the
version level being the only one that rejects a symlink. (That mechanism is
inferred; the observed behavior is verified.)

**End-to-end on a real plugin.** I copied a plugin from a local directory
checkout in this workspace into
`$CODEX_HOME/plugins/cache/<mkt>/<plugin>/0.18.1-dev/` (dropping `.git`) and
wrote only:

```toml
[plugins."<plugin>@<mkt>"]
enabled = true
```

`codex debug prompt-input` exited 0 and the prompt carried **22 namespaced
skills** from that plugin (`<plugin>:brief`, `<plugin>:design`,
`<plugin>:plan`, …). No marketplace block, no `codex plugin add`, no prompt.

**The verbatim minimal `config.toml`** niwa should template — plugin loading
plus the project-trust line from §7, which is the other thing you cannot skip:

```toml
[projects."/abs/path/to/instance"]
trust_level = "trusted"

[plugins."<plugin>@<marketplace>"]
enabled = true
```

and beside it, per plugin:

```
$CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/.claude-plugin/plugin.json
$CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/…rest of the plugin tree…
```

where `plugin.json` need only be `{"name":"<plugin>"}` and `<version>` may be
any string as long as it is a real directory.

I recommend niwa **also** emit the `[marketplaces.*]` block even though loading
does not need it — see §3 for what it buys.

### 2. The critical question: config alone, or must the cache be populated?

**The cache must be populated. `config.toml` alone does nothing, and Codex never
self-populates.**

H_A is the decisive run: both config blocks present, `source` pointing at a real,
readable marketplace directory, no `plugins/` tree. `codex debug prompt-input`
exited 0, printed **nothing on stderr**, and the skill was absent. Afterwards
`find $CODEX_HOME` showed Codex had created `installation_id`, `.sandbox_migration`,
`skills/.system/…`, `tmp/`, and `shell_snapshots/` — but **no `plugins/`
directory at all**. It did not try to install and fail; it simply skipped the
plugin without a word.

So niwa is in the "must place files" world. The good news is that the layout is
simple and the acceptance rules are lenient (arbitrary version-dir name, minimal
`plugin.json`, symlinks fine below the version dir), and — see §4 — placing them
requires no network.

There is no manifest, lockfile, content hash, or integrity check anywhere in the
cache. A tree niwa writes by hand is accepted identically to one `codex plugin
add` wrote; H_B and the real-plugin run both prove that. The only cache-adjacent
metadata I found is `.codex-remote-plugin-install.json`
(`{"schema_version":1,"remote_plugin_id":"plugin_connector_1p_…"}`), present only
for plugins installed from OpenAI's *remote* connector marketplace, not for local
or git ones. `.codex-plugin/` directories inside cached plugins are shipped by
the plugin authors (the Claude-plugins repo ships `.codex-plugin`,
`.cursor-plugin`, `.devin-plugin`, `.hermes-plugin` side by side), not written by
Codex.

**But `codex plugin add` is itself non-interactive, which reopens the design
space.** With stdin closed (`</dev/null`) and a fresh empty `CODEX_HOME`:

```
$ CODEX_HOME=…/J1 codex plugin marketplace add /…/lab/src --json </dev/null
{
  "marketplaceName": "nlab",
  "installedRoot": "/…/lab/src",
  "alreadyAdded": false
}
$ CODEX_HOME=…/J1 codex plugin add niwaprobe@nlab </dev/null
Added plugin `niwaprobe` from marketplace `nlab`.
Installed plugin root: /…/J1/plugins/cache/nlab/niwaprobe/9.9.9
```

Both exited 0, no prompt, no trust question, and `marketplace add` has a `--json`
mode built for exactly this kind of scripting. It wrote the `[marketplaces]`
block (adding `last_updated = "2026-08-17T02:37:58Z"`) and the `[plugins]` block,
and copied the tree into the cache. A Go program shelling out to these two
commands is not "a human completing an interactive flow" — so niwa may choose
between writing the cache itself and driving the CLI. Writing it directly keeps
niwa independent of Codex's CLI surface; driving the CLI keeps niwa correct if
that surface changes. Both work today.

### 3. Marketplace source types

**There are exactly two: `local` and `git`.** Read directly from the binary — the
serde enum's variant names are laid out contiguously in the string table:

```
$ strings -n 4 …/bin/codex | grep -o '…MarketplaceSourceType…'
…PluginMcpServerConfig…MarketplaceSourceTypegitlocalNotificationCondition…
```

`MarketplaceSourceType` is immediately followed by `git` and `local` and then the
next type name, which is how `serde`'s derived `Deserialize` stores a variant
list. No `github`, `directory`, `path`, `url`, or `remote` variant exists. This
is corroborated by the CLI help — `codex plugin marketplace add --help` says
"Add a **local or Git** marketplace" and accepts "a local path, owner/repo[@ref],
HTTPS Git URL, or SSH Git URL", i.e. four *input syntaxes* collapsing onto two
stored types — and by the error strings
`` ` source does not match source_type `git` `` and `` ` is missing source_type ``.

The real (host) `config.toml` shows both shapes in production use:

```toml
[marketplaces.<local-name>]
last_updated = "2026-08-16T20:16:41Z"
source_type = "local"
source = "/abs/path/to/dir/containing/.claude-plugin"

[marketplaces.<git-name>]
last_updated = "2026-08-17T00:59:06Z"
source_type = "git"
source = "https://github.com/<org>/<repo>.git"
ref = "v0.17.0"
```

`ref` is git-only and optional. `last_updated` is written by `marketplace add`;
nothing required it in any of my runs.

Both were exercised. `local`: verified end-to-end (§2, J1). `git`: verified with
a local git repository as the remote (§4, J3/J4) — `codex plugin list` and
`codex plugin add` both worked against a `source_type = "git"` marketplace.

`file://…` is **rejected by the CLI** (`codex plugin marketplace add
"file:///…" → Error: invalid marketplace source format; expected owner/repo, a
git URL, or a local marketplace path`) but is accepted when written directly into
`config.toml`, which is how I tested the git path offline.

**What the `[marketplaces]` block buys you, given loading doesn't need it:**
`codex plugin list` and `codex plugin marketplace upgrade`. With only the plugins
block and a populated cache, `codex plugin list` prints `No marketplace plugins
found.` even though the skills load. With the marketplace block present it prints
the real inventory, resolving `PATH` and `VERSION` live from the marketplace
source:

```
Marketplace `nlab`
/…/lab/src/.claude-plugin/marketplace.json

PLUGIN          STATUS              VERSION  PATH
niwaprobe@nlab  installed, enabled  9.9.9    /…/lab/src/niwaprobe
```

That is a user-facing affordance ("what do I have, and can I upgrade it"), not a
loading requirement. Emit it.

### 4. Does a git/remote marketplace need a network fetch at materialization time?

**No, if niwa places the files. And no network at session start under any
configuration.**

Two separate results.

**Session start never touches the network or the marketplace source.** H_B3:
`source = "/nonexistent/path/does/not/exist"` with the cache present → skill
loads. K1: `source_type = "git"`, `source = "https://github.invalid/nope/nope.git"`,
`ref = "v0.0.0"`, no marketplace snapshot on disk, cache present → exit 0, empty
stderr, skill loads. Once the cache exists, the marketplace entry is inert at
runtime. A niwa-materialized `CODEX_HOME` starts fully offline.

**`codex plugin add` against a git marketplace also runs offline, if the
marketplace *snapshot* is pre-seeded.** This is a second on-disk location I had
not expected. Writing a git marketplace into `config.toml` and running
`codex plugin list` with nothing else on disk gives:

```
Error: failed to load configured marketplace snapshot(s):
- `nlab` at /…/J3/.tmp/marketplaces/nlab: marketplace root does not contain a supported manifest
```

So for `source_type = "git"`, Codex expects a checkout at
**`$CODEX_HOME/.tmp/marketplaces/<name>/`** — and notably it did **not** clone to
fill it; it reported the empty path as an error. The host's real home confirms
the layout: `~/.codex/.tmp/marketplaces/<git-marketplace-name>/` is a full git
working tree, and `~/.codex/.tmp/` also holds `plugins.sync.lock` and
`plugins.sha`. For `source_type = "local"` there is no snapshot — the source path
is read in place.

Pre-seeding that directory (I copied the git repo there, `.git` and all) makes
the whole flow offline:

```
$ CODEX_HOME=…/J4 codex plugin list
Marketplace `nlab`
/…/J4/.tmp/marketplaces/nlab/.claude-plugin/marketplace.json
PLUGIN          STATUS         VERSION  PATH
niwaprobe@nlab  not installed           /…/J4/.tmp/marketplaces/nlab/niwaprobe

$ CODEX_HOME=…/J4 codex plugin add niwaprobe@nlab
Added plugin `niwaprobe` from marketplace `nlab`.
Installed plugin root: /…/J4/plugins/cache/nlab/niwaprobe/9.9.9
```

No fetch, no prompt, exit 0.

The remaining network dependence is niwa's own: it must obtain the plugin's files
from somewhere. For a `repo:`-style local source that is already a cloned repo in
the instance — zero network. For a GitHub source niwa must clone or fetch, but
that is niwa's own `git`, on niwa's own terms, and it is the same fetch niwa
already performs for the workspace's repos. I did **not** test a real
`https://github.com/...` marketplace end-to-end; the file-URL substitute exercises
the same `source_type = "git"` code path, and the host's real config shows two
GitHub marketplaces working in production.

### 5. Loose skills as an alternative

**They work, and they are cheap, but you lose namespacing by default and you
break every `${CLAUDE_PLUGIN_ROOT}` reference.**

Verified:

- `$CODEX_HOME/skills/<name>/SKILL.md` with an empty `config.toml` loads. It
  appears **unnamespaced**: `- probe-alpha: ZZPROBEALPHA …` versus
  `- niwaprobe:probe-alpha: …` for the same file inside a plugin cache.
- **The displayed name comes from the YAML frontmatter `name:`, not the
  directory.** A skill in `skills/dirname-differs/` whose frontmatter says
  `name: probe-alpha` loads as `probe-alpha`. Same for plugin skills.
- **A colon in the frontmatter name is accepted verbatim.** Setting
  `name: shirabe:probe-alpha` produced `- shirabe:probe-alpha: …` in the prompt.
  So namespacing *can* be reproduced — at the cost of rewriting the frontmatter
  of every copied SKILL.md, which makes niwa a content transformer rather than a
  file copier, and desynchronizes the copy from upstream.
- **Subdirectories are walked but do not namespace.** A skill at
  `skills/mygroup/probe-alpha/SKILL.md` loads as plain `probe-alpha`; `mygroup`
  is not part of the name.
- **A loose skill directory may be a symlink.** `skills/probe-alpha` symlinked to
  a checkout's skill directory loads. This is the opposite of the plugin cache's
  version-dir rule, and it means the loose-skill route can be pure symlinking
  with no copying at all.
- Copying a real plugin's whole `skills/` directory into `$CODEX_HOME/skills/`
  loaded all of its skills (27 entries total including Codex's own bundled
  `.system` skills), all unnamespaced: `brief`, `charter`, `design`, `plan`, ….

**What breaks.** Anything inside the skill directory — `references/`, `scripts/`,
`assets/`, relative links — survives, because those resolve relative to the
SKILL.md and the directory is copied whole. What does not survive is anything
pointing *above* the skill directory. In the real plugin I tested, `${CLAUDE_PLUGIN_ROOT}`
appears **331 times across the skills**, and the targets are plugin-root paths
that live outside any individual skill:

```
${CLAUDE_PLUGIN_ROOT}/references/coordination-strategy.md
${CLAUDE_PLUGIN_ROOT}/references/cross-repo-references.md
${CLAUDE_PLUGIN_ROOT}/references/decision-protocol.md
${CLAUDE_PLUGIN_ROOT}/references/dependency-diagram.md
…
```

Copy `skills/<name>/` into `$CODEX_HOME/skills/` and every one of those becomes
a dangling path. You would have to copy the plugin root's `references/` and
`scripts/` too and rewrite the variable — at which point you have reimplemented
the plugin cache, worse.

Separately, `${CLAUDE_PLUGIN_ROOT}` is **not expanded even inside plugins**, at
least not for MCP server commands — see §6. And a plugin's `hooks.json` and
`.mcp.json` are plugin-level files with no loose-skill equivalent; dropping to
loose skills silently drops those capabilities.

**Slash commands: untested.** The binary references `commands/` (8 occurrences)
and the TUI advertises "Type / to open the command popup", but slash commands are
a TUI surface and `codex debug prompt-input` does not render them, so I could not
exercise plugin-provided commands non-interactively. `agents/` is confirmed
absent from the prompt regardless of route (pre-established; unchanged here).

**Verdict:** loose skills are a legitimate fallback for skills that are
self-contained, and a bad fit for a real plugin. The plugin cache route costs
niwa a recursive directory copy and gets namespacing, plugin-root layout,
`.mcp.json`, and hooks for free. Prefer it.

### 6. MCP servers

**Correction to the brief's premise: niwa does not emit MCP servers into
`.claude/settings.json` at all.** `buildSettingsDoc` in
`/home/dgazineu/dev/niwaw/tsuku/tsuku+codex_dual_agent-4ff0633a/public/niwa/internal/workspace/materialize.go`
(lines 655–939) emits exactly `permissions`, `hooks`, `env`,
`includeGitInstructions`, `enabledPlugins`, and `extraKnownMarketplaces` — there
is no MCP branch, and `grep -rn '"mcpServers"' internal/` finds only test
fixtures. niwa's MCP path is a **verbatim file copy**: `[instance.files]` /
`[root.files]` map `"mcp.json" = ".mcp.json"` and copy the file unchanged into
the instance and workspace roots (`internal/workspace/scaffold.go:75-84`,
"copy VERBATIM (no .local), so a tool config that loads by an exact filename
keeps its name. Example: a Claude Code project .mcp.json").

That changes the translation problem. niwa has a Claude `.mcp.json` document
(`{"mcpServers": {"<name>": {...}}}`) that it copies; for Codex it would have to
*translate* that JSON into TOML tables, because Codex has no project-level
`.mcp.json` pickup — only `$CODEX_HOME/config.toml`.

**The Codex shape, verified accepted:**

```toml
[mcp_servers.probe_stdio]
command = "/bin/echo"
args = ["hello"]
env = { FOO = "bar" }

[mcp_servers.probe_http]
url = "https://example.invalid/mcp"
```

```
$ CODEX_HOME=…/I2 codex mcp list
Name         Command    Args   Env        Cwd  Status   Auth
probe_stdio  /bin/echo  hello  FOO=*****  -    enabled  Unsupported

Name        Url                          Bearer Token Env Var  Status   Auth
probe_http  https://example.invalid/mcp  -                     enabled  Unknown
```

Field inventory, from `codex mcp add --help` and `codex mcp get`: stdio servers
take `command`, `args`, `env`, `cwd`; streamable-HTTP servers take `url`,
`bearer_token_env_var`, plus optional `oauth_client_id` / `oauth_resource`. The
binary additionally carries a `PluginMcpServerConfig` with
`default_tools_approval_mode`, `enabled_tools`, `disabled_tools`, `tools` —
Codex-only extensions with no Claude counterpart.

Field-by-field against Claude's `.mcp.json`: `command` → `command`, `args` →
`args`, `env` → `env` (JSON object → TOML inline table), `cwd` → `cwd`,
`url` → `url`. Those are one-to-one and a mechanical translation is
straightforward. What does not map: Claude's `type`/`transport` discriminator has
no Codex key — Codex infers stdio vs HTTP from whether `command` or `url` is
present; and Claude's `headers` map for HTTP servers has no Codex equivalent
(Codex offers only `bearer_token_env_var`), so a header-authenticated remote
server cannot be expressed. Everything else translates.

**Verified they actually spawn, not merely parse.** I pointed an MCP entry at a
script that appends to a log and sleeps, then ran `codex exec`. The log gained a
line, so the process was really launched:

```
SPAWNED pwd=/…/lab/proj PLUGIN_ROOT=unset PLUGIN_DATA=unset
```

**A plugin's own `.mcp.json` is honored with no config.toml entry.** Dropping
`{"mcpServers":{"probefromplugin":{"command":"/bin/echo","args":["x"]}}}` into
the cached plugin root and enabling only the plugin made it appear in
`codex mcp list` and spawn under `codex exec`. So plugin-supplied MCP servers
come along for free with the cache route — another point in its favor over loose
skills.

**But `${CLAUDE_PLUGIN_ROOT}` is not substituted in a plugin's `.mcp.json`.**
With `"command": "${CLAUDE_PLUGIN_ROOT}/probe.sh"` (script present in the cached
plugin root), `codex mcp list` shows the command **literally unexpanded**:

```
Name        Command                         Args  Env  Cwd  Status   Auth
probe_root  ${CLAUDE_PLUGIN_ROOT}/probe.sh  -     -    -    enabled  Unsupported
```

and the spawn log stayed empty across two runs — the server never started, with
no error surfaced. The strings `CLAUDE_PLUGIN_ROOT` and `CLAUDE_PLUGIN_DATA` do
exist in the binary (adjacent to config-path strings), so some code path knows
them, but it is not this one. Any Claude plugin that launches its MCP server via
`${CLAUDE_PLUGIN_ROOT}/...` — a very common shape — will silently have no MCP
server under Codex. Flag this loudly; it is a functional gap, not a cosmetic one.

### 7. Project trust

**`trust_level` takes exactly `"trusted"` or `"untrusted"`, and niwa must
pre-write an entry for every directory a session may start in — but the
surprising part is that the *value* does not matter, only the entry's presence.**

Value domain read from the binary (serde variant list, same layout as §3):

```
TrustLeveltrusteduntrustedPersonalitypragmaticSandboxModeread-onlyworkspace-write…
```

and confirmed by the error a bad value produces:

```
$ printf '[projects."/…/proj"]\ntrust_level = "banana"\n' > config.toml
Error: /…/M4/config.toml:2:15: unknown variant `banana`, expected `trusted` or `untrusted`
  in `projects./…/proj.trust_level`
```

**What it controls: the sandbox mode of the session.** Reading the
`<permissions instructions>` block out of `codex debug prompt-input`, same cwd,
three `CODEX_HOME`s differing only in the projects block:

| `config.toml` | resulting `sandbox_mode` |
|---|---|
| `[projects."/…/proj"] trust_level = "trusted"` | `workspace-write` |
| `[projects."/…/proj"] trust_level = "untrusted"` | `workspace-write` |
| no `[projects]` entry | `read-only` |

Reproduced in a clean second batch. Untrusted and trusted behave identically; the
distinction that matters is *recorded* versus *unrecorded*. My reading is that
Codex treats the presence of a key as "the trust question has been answered for
this folder", and the `read-only` default is the not-yet-asked state — but the
value being inert is verified behavior, not inference, and it is odd enough that
niwa should write `"trusted"` explicitly rather than rely on it.

**Matching is exact-path, no prefix inheritance, no trailing-slash tolerance:**

| config key | cwd | sandbox |
|---|---|---|
| `/…/proj` | `/…/proj` | `workspace-write` |
| `/…/proj` | `/…/proj/sub/deeper` | `read-only` |
| `/…/proj/` (trailing slash) | `/…/proj` | `read-only` |
| `/some/other/path` | `/…/proj` | `read-only` |

So niwa must enumerate every directory a session might start in — the instance
root and each cloned repo root at minimum — and emit one exact-path entry each.
A parent entry does not cover children.

**Does an untrusted project prompt at session start?** In `codex exec`, **no** —
it runs straight through with `sandbox: read-only`, `approval: never`, no
question asked. In the TUI the binary carries the prompt text:

> Do you trust the contents of this directory? Working with untrusted contents
> comes with higher risk of prompt injection. Trusting the directory allows
> project-local config, hooks, and exec policies to load.

and a companion notice:

> Project-local config, hooks, and exec policies are disabled in the following
> folders until the project is trusted, **but skills still load.**

I could not drive the TUI to confirm when the modal fires (see Open Questions).
The last clause is independently verified: with no `[projects]` entry at all, the
plugin's skills were still present in the prompt. So an untrusted project does
**not** block skills — it blocks writes, project-local config, hooks, and exec
policies, and it very likely costs the user a modal on first launch. Pre-writing
`trust_level = "trusted"` is the difference between "run `codex` and it just
works" and "run `codex`, answer a question, and work read-only until you do".

### 8. Config validity and failure modes

Four distinct behaviors, all measured with `codex debug prompt-input`:

| Situation | Behavior |
|---|---|
| Plugin enabled but not in cache (stale/partial state) | **Silent skip.** exit 0, empty stderr, skill simply absent (H_A) |
| Marketplace `source` path does not exist | **Ignored at runtime.** exit 0, empty stderr, skills load from cache (H_B3, K1) |
| Unknown key / unknown table (`totally_unknown_key = 5`, `[unknown_table]`) | **Silently accepted.** exit 0, empty stderr (M3, C6) |
| Plugin key with no `@marketplace` (`[plugins."justaname"]`) | **Silently ignored.** exit 0, empty stderr (M2) |
| Malformed TOML | **Hard error with line:col.** `Error: /…/config.toml:1:6: key with no value, expected '='` |
| Invalid enum value (`trust_level = "banana"`) | **Hard error, exit 1**, names the offending key path |

The pattern: syntax and enum-domain violations are fatal and precisely located;
missing referents and unknown keys are silently tolerated. For a mechanically
generated file that is the friendly combination — niwa cannot brick a session by
referring to a plugin it failed to place, but it also gets no warning that it
did. Note the asymmetry for the git path: a *runtime* session ignores a broken
marketplace, while `codex plugin list` / `plugin add` hard-error on a missing
`.tmp/marketplaces/<name>` snapshot.

---

## Implications

**Materialization can be pure file-writing, and it is not network-dependent at
session start.** niwa writes one `config.toml` and copies each plugin's tree into
`$CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/`. No CLI invocation,
no trust prompt, no interactive step, and — verified — no network access at
session start even when the marketplace is a GitHub source whose host does not
resolve. The Go work is `os.MkdirAll` plus a recursive copy plus a small TOML
template. The one hard rule to encode: **the version directory must be a real
directory, not a symlink**; everything below it may be symlinked, so niwa can
copy a shallow skeleton and symlink the bulk if it wants to save space.

**niwa's existing Claude marketplace model maps onto Codex cleanly.** niwa's
`mapMarketplaceSourceWithIndex` (`internal/workspace/workspace_context.go:463`)
already produces `{"source":"directory","path":<abs dir>}` for `repo:` sources and
`{"source":"github","repo":"org/repo","ref":<tag>}` for GitHub ones. Those
translate to `source_type = "local"` + `source = <same abs dir>` and
`source_type = "git"` + `source = "https://github.com/org/repo.git"` + `ref =
<same tag>`. The registration-name logic (`marketplaceRegistrationName`, reading
the manifest's declared name for local sources, the repo name for GitHub) carries
over unchanged, since Codex keys marketplaces by name the same way. Same two-type
shape, same names, different serialization — a genuinely mechanical port.

**The plugin cache route dominates the loose-skills route.** Loose skills lose
namespacing (recoverable only by rewriting frontmatter, which makes niwa a
content transformer), lose every `${CLAUDE_PLUGIN_ROOT}` reference (331 of them
in one real plugin), and lose plugin-level `.mcp.json` and `hooks.json`
entirely. The cache route costs one recursive copy and keeps all of it. Keep
loose skills in the toolbox for genuinely self-contained, niwa-authored skills
that have no plugin to belong to.

**Trust must be pre-written per directory, exactly.** One `[projects."<abs>"]
trust_level = "trusted"` per instance root and per cloned repo root. Omit it and
the session runs `read-only` — the agent cannot write files — and the TUI very
likely asks a question niwa was supposed to have answered. This is as
session-blocking as untrusted hooks, and it needs the same treatment.

**One capability does not survive the port.** Plugin-supplied MCP servers whose
command is `${CLAUDE_PLUGIN_ROOT}/…` do not start under Codex and say nothing
about it. If any workspace plugin ships such a server, niwa should either rewrite
that path to the absolute cache path when materializing the plugin, or lift the
server into `config.toml`'s `[mcp_servers]` with the path already resolved. The
second is cleaner and niwa knows the absolute cache path by construction.

**If niwa prefers to drive the CLI instead of writing the cache, it can.**
`codex plugin marketplace add --json` and `codex plugin add` are non-interactive,
exit 0 with stdin closed, and — with `$CODEX_HOME/.tmp/marketplaces/<name>/`
pre-seeded — run entirely offline even for a git marketplace. That is a real
fallback if the cache layout ever changes under us. It costs two subprocess
invocations per plugin and a dependency on the CLI's argument surface.

## Surprises

- **The `[marketplaces.*]` block is not needed for anything to load.** Deleting it
  entirely left every skill in the prompt. It buys `codex plugin list` and
  `upgrade`, nothing more. I expected it to be load-bearing.
- **`trust_level = "untrusted"` behaves identically to `"trusted"`.** Both yield
  `workspace-write`; only a *missing* entry yields `read-only`. Recorded-ness
  matters, the recorded value does not. Reproduced twice.
- **Symlinks are accepted at every level of the plugin cache except the version
  directory.** The plugin directory may be a symlink, `skills/` may be a symlink,
  an individual skill directory may be a symlink — but a symlinked version
  directory is silently skipped.
- **Version directories are compared as versions, not strings**: `10.0.0` beat
  `9.9.9`. Yet the directory name is otherwise arbitrary — a single directory
  named `whatever` works fine.
- **`${CLAUDE_PLUGIN_ROOT}` is not expanded in a plugin's `.mcp.json`**, and the
  resulting spawn failure is completely silent. `codex mcp list` cheerfully
  reports the server as `enabled` while displaying the unexpanded literal.
- **Git marketplaces need a second on-disk location**,
  `$CODEX_HOME/.tmp/marketplaces/<name>/`, and Codex does *not* populate it on
  demand — it errors on the empty path. Irrelevant to session start (which never
  reads it), essential to `codex plugin list`/`add`.
- **`file://` git URLs are rejected by the CLI but accepted in `config.toml`.**
  Inconsistent validation, and useful for offline testing.

## Open Questions

- **Does the TUI trust modal actually block session start for an unrecorded
  directory?** I have the prompt string from the binary and I have the verified
  `read-only` sandbox consequence, but `codex debug prompt-input` and `codex exec`
  are both non-interactive and neither prompts. Confirming would take driving the
  TUI under a pty (`script`/`expect`) with an isolated `CODEX_HOME` and observing
  the first frame. Worth doing before the design commits to "just works".
- **Plugin-provided slash commands (`commands/*.md`).** The binary references
  `commands/` and the TUI advertises a command popup, but nothing renders commands
  non-interactively, so I could not test whether a cached plugin's commands
  register or how they are namespaced. Same pty approach would settle it.
- **A real `https://github.com/...` marketplace end-to-end.** I exercised the
  `source_type = "git"` code path with a local repository as the remote and relied
  on the host's production config for evidence that GitHub sources work. A clean
  test against a real public repo in an isolated home would confirm that a
  first-time git marketplace clones on `plugin add` and where it stages
  (`.tmp/marketplaces/.staging` exists on the host, suggesting it does).
- **`CLAUDE_PLUGIN_DATA` and where `CLAUDE_PLUGIN_ROOT` *is* honored.** Both
  strings are in the binary; neither reached an MCP server's environment. Likely
  candidates are hook execution and the Claude-config import flow. Hooks are
  another lead's scope, so I stopped here.
- **Whether Codex ever prunes or rewrites a niwa-written cache.** `~/.codex/.tmp/`
  holds `plugins.sync.lock` and `plugins.sha`, which suggests a sync/reconcile
  pass exists somewhere. Nothing in my runs touched the cache, but I only ran
  short-lived commands; a long TUI session or an `upgrade` might.

## Summary

A hand-written `config.toml` alone does nothing — Codex never populates its own plugin cache and skips an uncached plugin in total silence — but the cache it requires is just a recursive copy of the plugin tree under `$CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/` with a one-key `plugin.json`, so niwa's materialization can be pure file-writing, needs no `[marketplaces]` block at all, and reads nothing from the network or the marketplace source at session start. The two things niwa must not forget are an exact-path `[projects."<abs>"] trust_level = "trusted"` entry for every directory a session may start in (absence, not the value, drops the session to a read-only sandbox) and the fact that plugin-supplied MCP servers invoked via `${CLAUDE_PLUGIN_ROOT}/…` never start under Codex and fail silently. The biggest open question is whether the TUI's trust modal actually blocks session start for an unrecorded directory — the binary carries the prompt text, but neither non-interactive entry point will surface it, so it needs a pty-driven test before the design claims "run `codex` and it just works".
