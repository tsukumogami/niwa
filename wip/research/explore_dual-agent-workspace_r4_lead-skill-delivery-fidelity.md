# Lead: How much fidelity do loose skills lose versus the plugin cache, and is it recoverable?

All work against codex-cli 0.147.0 (`/home/dgazineu/.tsuku/tools/current/codex`),
upstream source read at `openai/codex` tag `rust-v0.147.0`. Every experiment ran
under an isolated `CODEX_HOME` beneath `/home/dgazineu/.claude/jobs/7838923c/tmp/r4/`.
The host's real `~/.codex` was never written to. Scripts are at
`/home/dgazineu/.claude/jobs/7838923c/tmp/r4/{setup,cache_route,exp2,exp3,exp4,exp5,exp6}.sh`.

**Headline: the tension dissolves.** `${CLAUDE_PLUGIN_ROOT}` is never textually
expanded by Codex — not in skill bodies, not in frontmatter, not in `.mcp.json`,
not on either route. And namespacing is *not* a plugin-cache privilege: it comes
from the nearest `plugin.json` above the skill on disk, which a symlink from a
loose `.codex/skills/` directory reaches for free. Both named advantages of the
plugin-cache route are gone.

## Findings

### 1. `${CLAUDE_PLUGIN_ROOT}` is never expanded as text — anywhere, on any route

**Read from the implementation.** The entire codex binary contains the string
`CLAUDE_PLUGIN_ROOT` exactly once:

```
$ grep -c "CLAUDE_PLUGIN_ROOT" codex.strings
1
$ grep -o "[A-Z_]*PLUGIN_ROOT[A-Z_]*" codex.strings | sort | uniq -c
      1 CLAUDE_PLUGIN_ROOTCLAUDE_PLUGIN_DATA
      2 PLUGIN_ROOT
      1 PLUGIN_ROOTPLUGIN_DATAA
```

That single occurrence is an **environment variable name**, injected into the
process environment of plugin hook commands, at
`codex-rs/hooks/src/engine/discovery.rs:226-236`:

```rust
let plugin_root_value = plugin_root.display().to_string();
env.insert("PLUGIN_ROOT".to_string(), plugin_root_value.clone());
// For OOTB compat with existing plugins that use this env var.
env.insert("CLAUDE_PLUGIN_ROOT".to_string(), plugin_root_value);
env.insert("PLUGIN_DATA".to_string(), plugin_data_root_value.clone());
// For OOTB compat with existing plugins that use this env var.
env.insert("CLAUDE_PLUGIN_DATA".to_string(), plugin_data_root_value);
```

The only *textual* placeholder substitution in the codebase is
`expand_agent_plugin_placeholders` at
`codex-rs/codex-mcp/src/agent_plugin_config.rs:374-395`, and it handles a
different pair of tokens:

```rust
const ROOT: &str = "${PLUGIN_ROOT}";
const DATA: &str = "${PLUGIN_DATA}";
```

— with no `CLAUDE_` prefix, and only inside an **Agent Plugins v1** `mcp.json`
identified by `"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"`
(`agent_plugin_config.rs:14-15`), not a Claude-style `.mcp.json`. So even the one
substitution engine that exists would not touch `${CLAUDE_PLUGIN_ROOT}`.

**Verified by experiment — model-visible surface.** I put the variable in the
`description` frontmatter field of two skills, one loose in `<repo>/.codex/skills/`
and one installed in `$CODEX_HOME/plugins/cache/`, then rendered the prompt
(`r4/exp2.sh`):

```
$ codex debug prompt-input
literal CLAUDE_PLUGIN_ROOT in prompt: 2
  'plainskill: PLAINSKILL_SENTINEL run ${CLAUDE_PLUGIN_ROOT}/scripts/run.sh to start.'
  'cacheplug:cacheskill: CACHESKILL_SENTINEL run ${CLAUDE_PLUGIN_ROOT}/scripts/run.sh to start.'
```

Byte-identical treatment. **The plugin cache does not expand it either.** This is
the fact the lead asked me to establish first, and it holds: the comparison
collapses.

**Verified by experiment — hook surface.** This is the one place the variable is
real. A marketplace-registered, installed, trusted plugin shipping
`hooks/hooks.json` (`r4/exp5.sh` plus the follow-up runs) produced:

```
CPR=[/…/r4/home3/plugins/cache/r4mkt/hookplug/1.0.0]
PR=[/…/r4/home3/plugins/cache/r4mkt/hookplug/1.0.0]
CPD=[/…/r4/home3/plugins/data/hookplug-r4mkt]
PD=[/…/r4/home3/plugins/data/hookplug-r4mkt]
```

A **project-layer** hook (`<repo>/.codex/hooks.json`) firing in the *same session*
saw it empty:

```
PROJ_CPR=[]
```

So the variable is scoped strictly to plugin hook command processes; expansion is
done by the shell (`SHELL -lc`, `hooks/src/engine/command_runner.rs`), not by
Codex. It is not in the session environment, so a script the *model* is told to
run does not get it on either route.

**Two mechanics worth recording.** Codex reads plugin hooks from
`hooks/hooks.json` by default, not `./hooks.json` at the plugin root
(`core-plugins/src/loader.rs:69`, `DEFAULT_HOOKS_CONFIG_FILE`); a root-level
`hooks.json` is only picked up if `plugin.json` declares `"hooks": "./hooks.json"`
(`core-plugins/src/manifest.rs:413-436`). And plugin hooks still need a trust
entry — `[hooks.state."<plugin>@<marketplace>:hooks/hooks.json:session_start:0:0"]`
with a `trusted_hash`, key format from `hooks/src/declarations.rs:35-37`.

**Net for question 1: `${CLAUDE_PLUGIN_ROOT}` is never expanded as text on any
route. It exists solely as an env var for plugin hook commands.** Since neither
real plugin in this workspace ships hooks at all, the loose route loses nothing
here that the cache route would have provided.

### 2. Namespacing is not a cache privilege — it comes from `plugin.json` on disk

**Read from the implementation.** `codex-rs/ext/skills/src/loader/namespace.rs:11-24`:

> A plugin namespace is the plugin name from the nearest valid plugin manifest
> above a skill path. […] Namespace precedence is: 1. the deepest matching
> canonical symlink root or nested plugin root; 2. the namespace inherited from
> the scanned skills root.

`qualify()` at `namespace.rs:168-173` produces `format!("{namespace}:{base_name}")`.
The manifest is found by `plugin_namespace_for_root_uri`
(`utils/plugins/src/plugin_namespace.rs:75-100`), which reads the `name` field of
whichever of these exists (`exec-server-protocol/src/protocol.rs:46-50`):

```rust
pub const DISCOVERABLE_PLUGIN_MANIFEST_PATHS: &[&str] = &[
    ".codex-plugin/plugin.json",
    ".claude-plugin/plugin.json",
    ".cursor-plugin/plugin.json",
];
```

The load-critical detail is `ext/skills/src/loader/host.rs:137-141`: for every
discovered skill whose **canonicalized** path differs from its walk path — i.e.
every skill reached through a symlink — the canonical parent directory is added
as a namespace root, and that root's ancestors are then probed for a manifest.
**A symlink carries the namespace of its target's real plugin tree.**

**Verified by experiment** (`r4/setup.sh`, `r4/cache_route.sh`). Four delivery
shapes in one isolated home, one prompt render:

| Shape | Name in the model-visible prompt |
|---|---|
| Bare loose dir, no manifest above | `plainskill` |
| Loose dir, frontmatter `name: fakens:claimedskill` | `fakens:claimedskill` |
| Loose **symlink** into a payload plugin tree | `demoplug:pkgskill` |
| Nested `.claude-plugin/plugin.json` inside the loose root | `nestedplug:nestskill` |
| Installed via the **plugin cache** | `cacheplug:cacheskill` |

The cache route and the loose-symlink route produce the *same form*,
`<plugin>:<skill>`, from the same manifest `name` field. The namespace comes from
the `plugin.json`, not from the marketplace name and not from the cache location.

Two independent recovery mechanisms therefore exist, and neither requires
rewriting skill bodies:

- **Structural (zero content change):** symlink or copy the plugin tree so a
  `plugin.json` sits above the skills. Namespacing is automatic.
- **Declarative (one frontmatter line):** `name: shirabe:brief` in a loose
  `SKILL.md` is honored verbatim. Confirmed above — the prompt shows
  `fakens:claimedskill` exactly as written. (Qualified names are capped at 128
  chars, `ext/skills/src/loader/mod.rs`, `MAX_QUALIFIED_NAME_LEN`.)

The structural route is strictly better: it costs nothing and keeps niwa out of
the content-transformer business entirely.

### 3. The real plugins' skills work when delivered loose

`r4/exp4.sh` copied a genuine workspace plugin (sourced from a local directory,
20 skills, 298 `${CLAUDE_PLUGIN_ROOT}` occurrences in its markdown, a
plugin-root `references/` tree and a plugin-root `scripts/` tree) out to scratch,
symlinked each skill directory into `<repo>/.codex/skills/`, and rendered the
prompt:

```
skills listed: 28
names: ['Pipes', 'imagegen', 'openai-docs', 'plugin-creator',
        'shirabe-preflight-liveness-fixture:preflight-liveness-sat',
        'shirabe-preflight-liveness-fixture:preflight-liveness-unsat',
        'shirabe:brief', 'shirabe:charter', 'shirabe:comp', 'shirabe:decision',
        'shirabe:design', 'shirabe:execute', 'shirabe:explore', 'shirabe:inflight',
        'shirabe:plan', 'shirabe:prd', 'shirabe:private-content',
        'shirabe:public-content', 'shirabe:release', 'shirabe:review-plan',
        'shirabe:roadmap', 'shirabe:scope', 'shirabe:strategy', 'shirabe:vision',
        'shirabe:work-on', 'shirabe:writing-style', 'skill-creator', 'skill-installer']
```

All 20 skills load, all fully namespaced, descriptions intact, with **zero
content modification**. A nested fixture plugin inside the tree even picked up its
own namespace, which is the symlink-ancestor rule working exactly as documented.

The **control** matters as much. A single skill copied *flat* — detached from its
plugin — loads as bare `decision`, and its plugin-root references cannot resolve:

```
########## flat
names: ['Pipes', 'decision', 'imagegen', 'openai-docs', 'plugin-creator',
        'skill-creator', 'skill-installer']
--- MISSING (plugin-root refs cannot resolve from a detached skill)
```

This is the real constraint, and it has nothing to do with the cache: **the unit
of delivery must be the plugin directory, not the skill directory.** Every one of
the 295 path references points at `${CLAUDE_PLUGIN_ROOT}/references/…` or
`${CLAUDE_PLUGIN_ROOT}/scripts/…` — files at the *plugin* root, one or more
levels above any individual skill. Ship the whole tree and they all exist at a
real absolute path; ship a skill alone and they cannot exist at any path.

**Full chosen architecture, end to end** (`r4/exp6.sh`): payload at
`<instance>/.codex/payload/<plugin>/`, one symlink
`<instance>/.codex/skills/<plugin> -> ../payload/<plugin>`, and
`<repo>/.codex -> ../../.codex` per repo:

```
cwd=<instance>/public/tsuku                    shirabe-namespaced: 20
cwd=<instance>/public/tsuku/deep/nested/dir    shirabe-namespaced: 20
```

Twenty namespaced skills, from the repo root and from four levels down, with
**one symlink per plugin**, no `CODEX_HOME`, no content rewriting.

One measured caveat: the one-symlink-per-plugin shape consumed one extra path
level and dropped the two deeply-nested test-fixture skills that the
one-symlink-per-skill shape retained. `MAX_SCAN_DEPTH = 6`
(`ext/skills/src/loader/mod.rs`), alongside `MAX_SKILLS_DIRS_PER_ROOT = 2000` and
`MAX_DESCRIPTION_LEN = 1024`. Deeply-buried skills can be silently dropped; if
that matters, symlink per skill instead of per plugin.

### 4. The rewrite is unnecessary — and a blind one would corrupt 11 sites

Since nothing expands the variable on either route, substitution buys nothing
Codex-side. But the honest costing, because it was asked:

| Scope | Occurrences | `${CLAUDE_PLUGIN_ROOT}/…` path use | Self-referential prose |
|---|---|---|---|
| Plugin A, `skills/**/*.md` | 298 | 295 | 3 |
| Plugin B (local dir), `**/*.md` | 62 | 54 | 8 |

The 11 non-path sites are prose *about* the variable, and a literal substitution
turns each into nonsense. Real examples:

```
skills/scope/SKILL.md:216:   a missing script or an unexpanded `${CLAUDE_PLUGIN_ROOT}` exits 127, and an
skills/execute/SKILL.md:79:  script or an unexpanded `${CLAUDE_PLUGIN_ROOT}` exits 127, and an unguarded 127
skills/roadmap/SKILL.md:499: unexpanded `${CLAUDE_PLUGIN_ROOT}` exits 127, and an ungua
```

Substituting gives "an unexpanded `/abs/path/to/plugin` exits 127" — a sentence
that no longer means anything. A blind `sed` is therefore **not** safe; it would
need a "path use only" rule (`${CLAUDE_PLUGIN_ROOT}` immediately followed by `/`),
which is one more regex but also one more thing to keep right forever.

Two further traps a blind substitution walks into. Both shipped shell scripts use
the **fallback form**, which a `${CLAUDE_PLUGIN_ROOT}` literal search does not match:

```
scripts/skill-preflight.sh:142:
  PREFLIGHT_ROOT="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd)}"
skills/execute/scripts/assert-child-template.sh:22:
  ROOT="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)}"
```

That is the happy ending: these scripts **already self-resolve** from their own
`BASH_SOURCE` when the variable is unset. Under the loose route they sit at a
real path on disk and work unmodified. And the 22 `allowed-tools:` frontmatter
entries containing the variable are a Claude Code permission-matching construct
that Codex does not read at all, so they are inert either way.

**Verdict on question 4: do not substitute.** It is unnecessary (nothing expands
it), it corrupts 11 documentation sites, it misses the `:-` fallback form, and it
would make niwa a content transformer for no gain. Keep the literal strings; they
are cosmetic prose under Codex, and the scripts they name resolve themselves.

### 5. No third route exists — and none is needed

**No config key relocates the plugin cache.** Searched the binary and the
config schema:

```
$ grep -o "CODEX_PLUGINS[A-Z_]*\|plugins_dir\|plugin_cache_dir" codex.strings
(no output)
```

Nothing in `config/src/config_toml.rs` or `docs/config.md` either. The cache path
is derived from `$CODEX_HOME` with no override. Confirmed empirically —
`codex plugin add` reported:

```
Installed plugin root: …/home3/plugins/cache/r4mkt/hookplug/1.0.0
```

**The project layer cannot declare a plugin root by path.** Skill roots come from
`resolve_skill_roots` (`ext/skills/src/host_roots.rs:29-85`). It accepts an
`extra_skill_roots` parameter, but that is an internal API argument with no
config-file feeder; the only user-facing skills config
(`config/src/skills_config.rs`) is `skills.bundled`, `skills.include_instructions`,
and a `skills.config` list that only enables/disables by path or name. It cannot
add a root.

**`.agents/skills` supports nothing that `.codex/skills/` does not.** Both are
built as `HostSkillRoot` with `SkillScope::Repo`, `plugin_root: None`, and
recursive discovery (`host_roots.rs:100-106` and `168-212`). Repo scope means
`DirectorySymlinkPolicy::Follow` in both cases (`loader/host.rs:88-92`), so the
symlink-namespacing trick works identically in either. The only differences are
mechanical: `.agents/skills` is probed at every directory from the project root
down to the cwd and needs no `.codex` folder; `.codex/skills` comes from the
discovered project config folder.

So there is no cache-equivalent reachable without `$CODEX_HOME`. That would have
mattered if the cache conferred an advantage. It does not.

### 6. Bonus: the two remaining named losses are also not cache-recoverable here

`.mcp.json` and `hooks.json` — neither real plugin ships either, so both are
hypothetical for the content in play. Worth noting anyway that the cache route
would not fully rescue a plugin `.mcp.json`: the only placeholder expansion that
exists is `${PLUGIN_ROOT}`/`${PLUGIN_DATA}` inside an Agent Plugins v1 `mcp.json`
(`agent_plugin_config.rs:374`), so a Claude-style `.mcp.json` using
`${CLAUDE_PLUGIN_ROOT}` gets no expansion on any route. This confirms the
config-materialization spike's observation and answers its open question: the
variable is honored **only** as a hook process env var.

## Implications

**Verdict on question 6: the chosen architecture is complete. It does not need a
per-instance `CODEX_HOME`.**

The argument for the plugin cache rested on two claimed advantages, and neither
survives contact with the implementation:

- **`${CLAUDE_PLUGIN_ROOT}` expansion** — the cache does not provide it. Codex
  never expands the variable textually, on any route. The 370-and-62 occurrence
  counts are not a fidelity gap between routes; they are literal strings both
  routes deliver identically. Under Claude Code they expand; under Codex they are
  prose either way.
- **Namespacing** — the cache does not own it. Namespacing derives from the
  nearest `plugin.json` above the skill on disk, and Codex canonicalizes
  symlinked skills specifically so that symlinked plugin content keeps its
  namespace. A loose `.codex/skills/<plugin> -> payload/<plugin>` symlink yields
  `shirabe:brief` exactly as the cache does. Verified on the real plugin: 20 of
  20 skills, correctly namespaced, from a project-layer payload, with no
  `CODEX_HOME` and no content edits.

**The one real design constraint is the delivery unit.** Ship whole plugin
directories, not individual skill directories. Every `${CLAUDE_PLUGIN_ROOT}/…`
reference points at plugin-root `references/` and `scripts/` that live above the
skill; a detached skill copy both loses its namespace and orphans its references.
This is the concrete requirement to write into the design, and it is cheap: one
symlink per plugin into `<instance>/.codex/skills/`.

**Concretely, niwa should materialize:**

```
<instance>/.codex/payload/<plugin>/          # whole plugin tree, verbatim copy or symlink
<instance>/.codex/skills/<plugin> -> ../payload/<plugin>
<repo>/.codex -> <instance>/.codex           # per repo, as already chosen
```

and perform **no content transformation at all** — no frontmatter rewriting, no
`${CLAUDE_PLUGIN_ROOT}` substitution. Substitution is the wrong move on its
merits: unnecessary, and it corrupts 11 self-referential documentation sites
while missing the `${CLAUDE_PLUGIN_ROOT:-…}` fallback form that the plugin's own
scripts already use to self-resolve.

**What genuinely degrades without a `CODEX_HOME`** — state it plainly in the
design so nobody rediscovers it:

- **Plugin hooks do not run.** Hooks require a marketplace-registered, installed,
  enabled plugin in the cache plus a `hooks.state` trust entry. Neither workspace
  plugin ships hooks, so today the cost is zero. If one ever does, its hook is
  the only thing that ever sees a populated `CLAUDE_PLUGIN_ROOT`. A project-layer
  `<repo>/.codex/hooks.json` still works and can be hand-written to do the same
  job with an absolute path — verified firing in this lab.
- **Plugin-level `.mcp.json` is not delivered.** Also zero cost today, and as
  noted the cache would not expand its variable anyway.
- **`codex plugin list` shows nothing.** Loose skills load and are namespaced,
  but they are not "installed plugins," so plugin-management commands do not see
  them. Cosmetic; skills work regardless.

No hybrid is needed. If a plugin later ships hooks or MCP servers and those turn
out to matter, that is the moment to revisit — and the fallback is a
project-layer `hooks.json` written by niwa with absolute paths, not a
`CODEX_HOME`.

## Surprises

**The symlink-canonicalization rule is deliberate, not accidental.** Codex
explicitly canonicalizes each discovered skill path and registers the canonical
parent as a namespace root (`loader/host.rs:137-141`), with a comment in
`namespace.rs:23` naming "canonical symlink root" as first-precedence. Someone
designed for exactly the delivery shape niwa wants. This is the single fact that
makes the whole architecture work, and it was invisible from the outside.

**The cache-route skill also arrives with the variable unexpanded.** I expected
at least the description or body to be rewritten at install time. It is not — the
copy is verbatim. The cache's advantage was assumed rather than measured.

**Codex's plugin hook path is `hooks/hooks.json`, not `hooks.json`.** A
root-level `hooks.json` is silently ignored unless `plugin.json` declares it. My
first three hook attempts failed for this reason (compounded by an invalid `\$`
JSON escape in my own fixture, and then a duplicate TOML key that made the whole
config unparseable — all three were my bugs, not Codex behaviour).

**`codex plugin list` says "No marketplace plugins found" while the plugin's
skills are loading fine.** Skill loading from the cache needs only
`[plugins."<name>@<marketplace>"] enabled = true`; the listing command needs a
`[marketplaces.*]` block. The two are decoupled, which is confusing to debug.

**The shipped scripts already survive an unset variable.** Both use
`${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/..")}`. The plugin
author had already solved the problem this spike was convened to worry about.

## Open Questions

- **Skill body text at read time.** `codex debug prompt-input` renders only
  names, descriptions and file paths; bodies load when the model calls the skill
  tool, and a `$skill` mention in the prompt does not inline them. I could not
  observe body delivery without a live model. The claim that bodies arrive
  literal rests on (a) the single-occurrence binary string scan, (b) no expansion
  code in `ext/skills/src/tools/read.rs`, and (c) the frontmatter description
  arriving literal on both routes. Exercising it would need a stub model server
  that returns a skill tool call — perhaps an hour.
- **Whether `$CODEX_HOME/plugins/data/<plugin>-<marketplace>/` has any loose
  equivalent.** `CLAUDE_PLUGIN_DATA` pointed at it in the hook run. Nothing in
  either real plugin uses it, so I did not chase it.
- **Non-markdown payload fidelity.** I verified markdown skills and confirmed
  bundled `references/` and `scripts/` files land at real paths. I did not
  exercise a skill actually *executing* one of those scripts under Codex, since
  the `!` bash-injection syntax the plugin uses at load time is a Claude Code
  feature Codex does not implement — meaning the preflight checks are inert under
  Codex regardless of delivery route. Worth confirming separately if preflight
  behaviour matters to the design.
- **Interaction with `MAX_SKILLS_DIRS_PER_ROOT = 2000`** across many plugins in
  one payload. Two plugins is well under; a workspace with dozens might not be.

## Summary

`${CLAUDE_PLUGIN_ROOT}` is never expanded textually by Codex on any route — the
binary contains the string exactly once, as an env var handed only to plugin hook
processes — and namespacing comes from the nearest `plugin.json` above a skill on
disk, which Codex deliberately follows through symlinks, so a loose
`.codex/skills/<plugin> -> payload/<plugin>` symlink yields `shirabe:brief`
exactly as the plugin cache does. The chosen architecture is therefore complete
as designed: all 20 real skills loaded fully namespaced from a project-layer
payload with no `CODEX_HOME` and no content rewriting, and niwa should ship whole
plugin directories verbatim rather than substituting anything, since substitution
buys nothing and corrupts 11 self-referential documentation sites. The biggest
open question is skill *body* delivery at tool-call time, which `codex debug
prompt-input` cannot show and which would need a stub model server to exercise.
