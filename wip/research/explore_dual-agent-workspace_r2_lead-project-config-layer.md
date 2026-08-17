# Lead: What can Codex's project-level config layer do, and does it replace a per-instance CODEX_HOME?

All experiments ran against codex-cli 0.147.0 (`/home/dgazineu/.tsuku/tools/current/codex`,
`codex --version` -> `codex-cli 0.147.0`) with an isolated `CODEX_HOME` at
`/home/dgazineu/.claude/jobs/7838923c/tmp/lab2/home`. The host's real `~/.codex` was read
only (key *shape* of `auth.json`, marketplace stanza shape) and never written. No
`codex login`/`logout` ran; the isolated home used a synthetic API-key `auth.json`
containing the literal string `sk-DUMMY-not-a-real-key-000`.

Upstream source was read from tag `rust-v0.147.0` of `openai/codex` via
`https://raw.githubusercontent.com/openai/codex/rust-v0.147.0/...` (curl needed
`--cacert /etc/ssl/certs/ca-certificates.crt`). Fetched copies are under
`/home/dgazineu/.claude/jobs/7838923c/tmp/src147/`.

**The round-1 finding is confirmed and is much bigger than reported.** The project layer
is real, it walks upward, and it carries almost everything niwa needs. It also cannot
bootstrap itself, which is the whole story.

## Findings

### The test lab

```
lab2/instance/                       <- "niwa instance root": .niwa/ marker, .codex/, AGENTS.md
lab2/instance/public/                <- intermediate dir, no marker
lab2/instance/public/repo/           <- a real `git init` repo: .git/, .codex/, AGENTS.md
lab2/instance/public/repo/sub/deep/  <- where the developer actually works
```

`lab2/home` is the isolated `CODEX_HOME`. Probe: `codex doctor` prints the effective
`model`, so `model = "gpt-5-instance-LEVEL"` vs `"gpt-5-REPO-LEVEL"` vs `"gpt-5-USER-LEVEL"`
reads out which layer won. Skills and instruction docs were probed with
`codex debug prompt-input` grepped for unique markers.

---

### 1. The exact discovery rule for the project layer

**Verified by experiment and read directly from the implementation.** It is a bounded
upward walk, and the bound is configurable.

`codex-rs/config/src/loader/mod.rs:1161-1183`:

```rust
async fn find_project_root(
    fs: &dyn ExecutorFileSystem,
    cwd: &AbsolutePathBuf,
    project_root_markers: &[String],
) -> io::Result<AbsolutePathBuf> {
    if project_root_markers.is_empty() {
        return Ok(cwd.clone());
    }
    for ancestor in cwd.ancestors() {
        for marker in project_root_markers {
            let marker_path = ancestor.join(marker);
            ...
            if fs.get_metadata(&marker_path_uri, None).await.is_ok() {
                return Ok(ancestor);
            }
        }
    }
    Ok(cwd.clone())
}
```

`load_project_layers` (same file, lines 1220-1260) then collects `cwd.ancestors()`,
stopping *after* it passes `project_root`, reverses the list, and for each directory in
that chain loads `<dir>/.codex/config.toml` if `<dir>/.codex` is a directory. The doc
comment at lines 1204-1206 states the order in words:

```
/// - cwd       `${PWD}/config.toml` (loaded but disabled when the directory is untrusted)
/// - tree      parent directories up to root looking for `./.codex/config.toml` ...
/// - repo      `$(git rev-parse --show-toplevel)/.codex/config.toml` ...
```

The precise rule:

1. `project_root` = the **nearest** ancestor of `cwd` (starting at `cwd` itself) that
   contains any entry named in `project_root_markers`. If none matches, `project_root = cwd`.
2. Every directory from `project_root` down to `cwd` inclusive is scanned for `.codex/config.toml`.
3. Precedence increases toward `cwd` (project root lowest, cwd highest). All project
   layers sit at precedence 25, above `User` (20/21) and below `SessionFlags` (30) —
   `codex-rs/config/src/config_layer_source.rs:37-46`.

`project_root_markers` defaults to `[".git"]` —
`codex-rs/config/src/project_root_markers.rs:5`:

```rust
const DEFAULT_PROJECT_ROOT_MARKERS: &[&str] = &[".git"];
```

There is **no** home boundary, no filesystem-root boundary, and no separate git
boundary. The marker *is* the boundary, and by default the marker is `.git`.

Experiments (`codex doctor` model readout; instance `.codex/config.toml` sets
`gpt-5-instance-LEVEL`):

| # | markers | cwd | trust | result |
|---|---------|-----|-------|--------|
| A | default | `instance` | none | `gpt-5-USER-LEVEL` |
| C | default | `instance` | `[projects."<instance>"] trusted` | **`gpt-5-instance-LEVEL`** |
| D | default | `instance/public` | instance trusted | `gpt-5-USER-LEVEL` |
| E | default | `repo/sub/deep` | instance trusted | `gpt-5-USER-LEVEL` |
| F | `[".niwa"]` | `repo/sub/deep` | instance trusted | **`gpt-5-instance-LEVEL`** |
| G | `[".niwa"]` | `instance/public` | instance trusted | **`gpt-5-instance-LEVEL`** |
| H | `-c 'project_root_markers=[".niwa"]'` | `repo/sub/deep` | instance trusted | **`gpt-5-instance-LEVEL`** |

**Case D is the crux and it is brutal.** `instance/public` has no `.git` and no marker
anywhere above it, so `project_root = cwd = instance/public`, the chain is exactly one
directory, and `<instance>/.codex` is never even looked at. One directory below the
instance root, with no git repo involved at all, the instance config is already invisible.
Case E adds the realistic nested `.git` and fails the same way.

**Case F/G/H is the fix.** Setting `project_root_markers = [".niwa"]` makes the walk
run all the way to the instance root from arbitrary depth, straight through a nested git
repository. `-c` on the command line works identically (case H).

Marker precedence is nearest-ancestor-wins, not list-order-wins. Verified:

- Case I: `project_root_markers = [".niwa", ".git"]`, cwd `repo/sub/deep` -> `project_root`
  becomes `repo` (its `.git` is nearer than the instance's `.niwa`), the chain never
  reaches the instance, and the result was `gpt-5-USER-LEVEL`. **So you cannot keep `.git`
  in the marker list and still reach an instance root above a clone.**
- Case J: `project_root_markers = [".niwa"]` with `.codex/config.toml` at *both* the
  instance root and the repo root, cwd `repo/sub/deep` -> `gpt-5-REPO-LEVEL`. Both layers
  loaded; the one nearer `cwd` won, confirming the increasing-precedence stacking.

Critically, `project_root_markers` is read from `merged_so_far` — the config merged from
system + cloud + user + profile + CLI overrides — *before* any project layer is loaded
(`loader/mod.rs:302-313`). **A project-local config cannot change the marker list.**
This is inference-free: the read happens at line 312 and `load_project_layers` is not
called until line 351.

The skills subsystem reimplements the identical walk against the same
`project_root_markers` (`codex-rs/ext/skills/src/host_roots.rs:234-292`), so everything
below is bounded the same way.

---

### 2. The complete denylist

**Read directly from the implementation**, `codex-rs/config/src/loader/mod.rs:60-76`:

```rust
// Project-local config comes from repository contents, so it should not get to
// choose where a user's credentials are sent or which local commands are run.
// These settings are still supported from user, system, managed, and runtime
// config layers.
const PROJECT_LOCAL_CONFIG_DENYLIST: &[&str] = &[
    "openai_base_url",
    "chatgpt_base_url",
    "apps_mcp_product_sku",
    "model_provider",
    "model_providers",
    "notify",
    "profile",
    "profiles",
    "experimental_realtime_webrtc_call_base_url",
    "experimental_realtime_ws_base_url",
    "otel",
];
```

Plus one nested key, stripped separately in `sanitize_project_config`
(`loader/mod.rs:958-975`): `features.respect_system_proxy`.

That is the complete list — twelve entries, eleven top-level keys and one nested key.
Stripping is top-level-key granularity: the whole table is removed, and a startup warning
is emitted naming the removed keys (`project_ignored_config_keys_warning`,
`loader/mod.rs:977-993`).

**Verified by experiment.** A project config containing `openai_base_url`, `notify`,
`profile`, `[otel]`, `[model_providers.evil]`, `model`, and `[mcp_servers.demo]`, run from
a trusted project root, produced:

```
model                    gpt-5-instance-LEVEL · openai
MCP servers              1
startup warning          Ignored unsupported project-local config keys in
  /home/dgazineu/.claude/jobs/7838923c/tmp/lab2/instance/.codex/config.toml:
  openai_base_url, model_providers, notify, profile, otel.
  If you want these settings to apply, manually set them in your user-level config.toml.
```

Exactly the five denied keys present were stripped; `model` and `mcp_servers` survived.

**Answering the specific question:** `[mcp_servers.*]`, `[hooks.*]`, `skills`,
`[marketplaces.*]` and `[plugins.*]` are **not** in the denylist. `mcp_servers` and
`hooks` genuinely work from a project layer. **`marketplaces` and `plugins` do not**,
despite their absence from the denylist — they are blocked by a separate mechanism
(next section). Note also that `profiles`/`profile` being denied means the `-p/--profile`
mechanism cannot be driven from a project config at all.

---

### 3. Project layer vs `$CODEX_HOME`: the matrix

| What niwa must deliver | From project `.codex/`? | Trust needed? | Evidence |
|---|---|---|---|
| Composed instruction context (`AGENTS.md`) | **Yes** | **No** | experiment below |
| Skills (`.codex/skills/`, `.agents/skills/`) | **Yes** | **No** | experiment below |
| General config (`model`, sandbox, etc.) | **Yes** | **Yes** | cases C/F/G/H |
| MCP servers | **Yes** | **Yes** | `MCP servers 1` above |
| Hooks (`.codex/hooks.json`) | **Yes** | **Yes**, plus `trusted_hash` | section 5 |
| Marketplaces + plugins | **No** | n/a | experiment + source below |

**Skills load even when the project is untrusted.** This confirms the binary string
round-1 quoted. With *no* `[projects.*]` entry at all, default markers, cwd
`repo/sub/deep`:

```
$ codex debug prompt-input | grep -o "MARKER-[a-z-]*" | sort -u
MARKER-home-skill
MARKER-repo-skill
```

`repo-skill` lives at `<repo>/.codex/skills/repo-skill/SKILL.md` and loaded with zero
trust configuration. `inst-skill` (at `<instance>/.codex/skills/`) did not, because the
default `.git` marker stopped the walk at the repo. Switching to
`project_root_markers = [".niwa"]`, still with no trust entry:

```
MARKER-home-skill
MARKER-inst-skill
MARKER-repo-skill
```

The mechanism, read from `codex-rs/ext/skills/src/host_roots.rs:87-108`: skill roots are
built by iterating `config_layer_stack.all_layers_high_to_low()` — **all** layers,
including the ones disabled for lack of trust — and for every `ConfigLayerSource::Project`
appending `config_folder.join("skills")`. Trust gates the config *values*; it does not gate
the skills directory attached to the layer. Separately,
`repo_agents_skill_roots` (lines 168-213) adds `<dir>/.agents/skills` for every directory
in the same project-root-to-cwd chain (`AGENTS_DIR_NAME = ".agents"`,
`SKILLS_DIR_NAME = "skills"`, lines 25-26).

**`AGENTS.md` follows the same walk.** With default markers from `repo/sub/deep`, only the
repo's doc appeared (`YYBOT`), not the instance's (`ZZTOP`). With
`project_root_markers = [".niwa"]`, both appeared:

```
$ codex debug prompt-input | grep -o "ZZTOP\|YYBOT" | sort -u
YYBOT
ZZTOP
```

So project-level instruction context works with no `CODEX_HOME` and no trust, but is
bounded by the marker walk exactly like everything else.

**Marketplaces and plugins are user-layer-only.** Verified by a three-step experiment
with a local marketplace at `lab2/mkt` (`.claude-plugin/marketplace.json` declaring
`pluginA`, which ships `skills/plug-skill/SKILL.md`):

1. Marketplace + `[plugins."pluginA@niwamkt"] enabled = true` declared in the **project**
   config, project trusted: `codex plugin list` -> `No marketplace plugins found.`, and
   `MARKER-plugskill` absent from the prompt.
2. Same stanzas moved to the **user** config: `codex plugin list` -> `Marketplace 'niwamkt' ...
   pluginA@niwamkt  not installed`. After `codex plugin add pluginA@niwamkt` (installs to
   `$CODEX_HOME/plugins/cache/niwamkt/pluginA/0.1.0`), `MARKER-plugskill` **appears**.
3. Stanzas moved **back** to the project config, cache left in place, project trusted:
   `MARKER-plugskill` **absent** again.

The mechanism, read from source. `configured_plugins_from_stack`
(`codex-rs/core-plugins/src/marketplace_policy.rs:284-301`) calls
`project_effective_user_config`, which calls `config_layer_stack.effective_user_config()`.
That accessor (`codex-rs/config/src/state.rs:327-339`) is:

```rust
pub fn effective_user_config(&self) -> Option<TomlValue> {
    let mut user_layers = self
        .layers_low_to_high()
        .filter(|layer| matches!(layer.name, ConfigLayerSource::User { .. }))
        .peekable();
    ...
}
```

Project layers are filtered out by construction. Plugin payloads also always materialize
under `$CODEX_HOME/plugins/cache`, so even the install target is home-scoped.

---

### 4. The trust precondition

**Verified by experiment and read from source.** There are two *different* trust checks
with two *different* key-resolution rules, and conflating them is easy.

**(a) Whether a project config layer is enabled** — `ProjectTrustContext::decision_for_dir`
(`loader/mod.rs:868-911`). For each directory in the chain it tries, in order: the
directory's own key, then the `project_root` keys, then the **git repo root** keys. First
hit wins. Anything other than `TrustLevel::Trusted` produces a `disabled_reason` and the
layer is loaded-but-ignored (`disabled_reason_for_decision`, lines 913-931):

```
To load project-local config, hooks, and exec policies, add {trust_key} as a trusted
project in {user_config_file}.
```

Because `project_root` is in the fallback chain, **a single trust entry on the instance
root transitively trusts every `.codex` in the chain below it** — including one inside a
third-party cloned repo. Case J demonstrated this: with trust only on `<instance>`, the
repo's own `.codex/config.toml` loaded *and won on precedence*.

**(b) Whether the interactive TUI shows a blocking trust screen** —
`should_show_trust_screen` (`codex-rs/tui/src/lib.rs:1963-1965`):

```rust
fn should_show_trust_screen(config: &Config) -> bool {
    config.active_project.trust_level.is_none()
}
```

`active_project` comes from `get_active_project(resolved_cwd, repo_root)`
(`codex-rs/config/src/config_toml.rs:830-854`, called at
`codex-rs/core/src/config/mod.rs:3412-3416` with
`repo_root = resolve_root_git_project_for_trust(fs, &resolved_cwd)`). It tries **only two**
keys: exact `cwd`, then the git repo root. **`project_root` is not consulted.**

That asymmetry is real and I verified it:

| Case | trust entries | cwd | TUI result |
|---|---|---|---|
| 1 | none | `repo/sub/deep` | **prompts** |
| 2 | `<instance>` only | `repo/sub/deep` | **still prompts** (though config *did* load) |
| 3 | `<instance>` + `<repo>` | `repo/sub/deep` | clean start, header shows `gpt-5-instance-LEVEL` |
| 4 | `<instance>` only | `instance` (not a git repo) | clean start |
| 5 | `<instance>` only | `instance/public` (not a git repo) | **prompts** |

So: **niwa must write a trust entry keyed on every cloned repo's git root**, not just the
instance root, or the TUI prompts. Case 2 is the trap — the config works, the prompt still
appears.

The prompt text (case 1), captured from a pty-driven TUI:

```
> You are in /home/dgazineu/.claude/jobs/7838923c/tmp/lab2/instance/public/repo/sub/deep
  Note: You're in a subdirectory of a Git project. Trusting will apply to the repository root:
  /home/dgazineu/.claude/jobs/7838923c/tmp/lab2/instance/public/repo
  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of
  prompt injection. Trusting the directory allows project-local config, hooks, and exec policies to load.
> 1. Yes, continue
  2. No, quit
```

**Untrusted degrades silently under `exec` but prompts in the TUI.** `codex exec` and
`codex doctor` never prompted in any case above; they just ignored the layer (a startup
warning is recorded but `doctor` only surfaces it under its warnings section).

**`trust_level = "untrusted"` suppresses the prompt without granting trust** — because
the gate is `is_none()`, not `is_trusted()`. Case 6: an explicit
`[projects."<repo>"] trust_level = "untrusted"` gave a clean TUI start showing
`model: gpt-5-USER-LEVEL` and a visible startup warning. This is a legitimate
"quiet but disabled" state.

**A project cannot vouch for itself.** `project_trust_context` deserializes its
`projects` table from `merged_so_far` (`loader/mod.rs:1002-1014`), which is built before
project layers exist. Verified: a `.codex/config.toml` containing
`[projects."<its own path>"] trust_level = "trusted"` alongside `model =
"gpt-5-instance-LEVEL"` yielded `gpt-5-USER-LEVEL` — self-trust ignored. **niwa can
pre-write trust, but only into the user (or system/managed) config, or via `-c`.**

Being inside a git repo changes trust only through the key that gets used, per the two
rules above. `--skip-git-repo-check` was **not** used for any of cases 1-6 or A-J; all the
nested-repo cases ran inside a real `git init` repository.

---

### 5. Interactive TUI behavior, and hooks

Driven with a Python `pty.fork()` harness rendering into a `pyte` screen buffer
(`/home/dgazineu/.claude/jobs/7838923c/tmp/tui2.py`, `tui3.py`), 40x110, answering the
`OSC 10/11` and kitty-keyboard capability queries the TUI blocks on.

Beyond the trust screen, **an unreviewed project hook produces a second blocking screen**:

```
  Hooks need review
  1 hook is new or changed.
  Hooks can run outside the sandbox after you trust them.
> 1. Review hooks
  2. Trust all and continue
  3. Continue without trusting (hooks won't run)
```

I drove the TUI to "Trust all and continue" and read back what Codex wrote into the user
config, which gives the exact state-key format:

```toml
[hooks.state."/home/.../lab2/instance/.codex/hooks.json:session_start:0:0"]
trusted_hash = "sha256:7a573f29f82220d65bc5a3ff13e77896969d7275dad20e184fe115fbd2c98680"
```

The key is `<absolute hooks.json path>:<snake_case_event>:<group_index>:<handler_index>`.
Round-1's hash algorithm was correct — the hash Codex wrote matched what round-1's
`codexhash.py` computed for the same handler, byte for byte. My first attempt failed
purely because I guessed the key as the bare file path.

**With trust entries and `hooks.state` fully pre-written, the TUI starts clean** — no
trust screen, no hooks review — with the header reading
`model: gpt-5-instance-LEVEL` while cwd is `instance/public/repo/sub/deep`. The hook
itself fires:

```
$ cd lab2/instance/public/repo/sub/deep
$ codex exec --sandbox danger-full-access "hi"
$ cat lab2/hook.log
HOOKFIRED-INSTANCE
```

That is an instance-root `.codex/hooks.json`, reached by the `.niwa` marker walk from
three directories deep inside a nested git repo, firing with no prompt. Note the hook did
not fire under the TUI run at default sandbox settings — its command wrote to a path
outside the writable roots; that is a sandbox effect, not a trust effect.

One incidental diagnostic worth recording: `hooks.json` schema is `{description?, hooks}`.
A `"version": 1` field is rejected with `unknown field 'version', expected 'description'
or 'hooks'`, surfaced as a visible TUI startup warning (`HooksFile` in
`codex-rs/config/src/hook_config.rs:12-17`).

---

### 6. The verdict

**A hybrid, and the hybrid is good news.** The project layer carries the payload; the user
config carries a small, static bootstrap that the project layer cannot supply for itself.

Deliverable from `<instance>/.codex/` with **no environment variable and no shell
integration**: instruction context, skills, general config, MCP servers, and hooks. That is
essentially the entire workspace surface.

Not deliverable from the project layer: marketplaces and plugins (user-layer-only by
construction), plus the eleven denylisted keys.

The irreducible bootstrap, all of which lives in one file — `$CODEX_HOME/config.toml`,
which with no env var set is the developer's real `~/.codex/config.toml`:

1. `project_root_markers` — without it the walk stops at the nearest `.git` (or at `cwd`),
   and the instance root is never reached.
2. `[projects."<git repo root>"] trust_level = "trusted"` — one per cloned repo, or the
   TUI prompts on startup.
3. `[hooks.state."<hooks.json>:<event>:<g>:<h>"] trusted_hash` — one per hook handler, if
   niwa ships hooks.
4. `[marketplaces.*]` / `[plugins.*]` — only if niwa delivers skills via plugins rather
   than as loose `SKILL.md` directories.

## Implications

**The fragile part of the design changes shape completely.** The old plan needed
`CODEX_HOME` exported into the developer's environment on every terminal, script, and
Makefile — something niwa's shell integration provably cannot do. The new requirement is
that niwa write a handful of absolute-path-keyed entries into `~/.codex/config.toml` at
instance create time and remove them at reap time. That is a file niwa edits on a
schedule it controls, not an environment variable it must win a race for. Every terminal,
script, and Makefile then works identically, because the trust store and the marker list
are global and the payload is discovered from the filesystem.

**Item 4 can be designed away, and should be.** A bare `SKILL.md` under
`<instance>/.codex/skills/<name>/` loads with no plugin, no marketplace, and — verified —
no trust entry at all. Round-1 already established that a bare `SKILL.md` works in
`$CODEX_HOME/skills/`; it works in the project layer too. Delivering the workspace's
skills as loose directories drops the entire marketplace/plugin dependency, which is the
one thing the project layer flatly cannot do.

**Item 1 has a real cost that needs a decision.** Setting `project_root_markers = [".niwa"]`
globally means `.git` is no longer a project root marker *anywhere* on that machine. For a
repo outside any niwa instance, `project_root` collapses to `cwd`, so a repo-root
`.codex/config.toml`, `.codex/skills/`, or `AGENTS.md` stops being picked up when the
developer is in a subdirectory. Case I proved you cannot hedge with `[".niwa", ".git"]` —
nearest ancestor wins, so `.git` inside a clone always shadows `.niwa` above it.

There is an alternative that avoids touching markers entirely, and the design should weigh
it: **write `.codex/` into each cloned repo root instead of the instance root.** The
default `.git` marker then finds it with no global change at all. The costs are N copies
of the payload per instance and a `.codex/` directory sitting untracked in each clone's
working tree. Whether a symlink from `<repo>/.codex` to `<instance>/.codex` collapses that
duplication is untested — Codex resolves symlinks in some paths, and the `hooks.state` key
is an absolute path, so this needs verifying before it is designed in.

**One security property deserves explicit attention.** Trusting the instance root
transitively trusts every `.codex` directory between it and `cwd`, including inside
third-party clones, and the nearer one *wins on precedence* (case J). A cloned repo that
ships its own `.codex/config.toml` will be honored as trusted config the moment niwa
trusts the instance above it. The denylist limits the blast radius — a project config
cannot redirect `openai_base_url`, swap `model_providers`, or set `notify` — but it can
still register `mcp_servers` and, given a `hooks.json` whose hash a user has approved,
hooks. niwa trusting an instance root is a stronger grant than it looks.

**Practical note on trust entries.** Because the TUI gate consults only `cwd` and the git
repo root, niwa must enumerate every cloned repo and write a trust entry per repo root.
niwa already knows this list — it does the cloning — so this is bookkeeping, not a new
capability. Trusting the instance root alone is not sufficient and fails in exactly the
case that matters (case 2).

## Surprises

The default marker being `.git` means the project layer fails *one directory below the
instance root* even with no git repository anywhere involved (case D). I expected an
unbounded walk to be the risk; the real risk is that the walk barely moves.

Trust is checked twice with two different key rules, and they disagree. A configuration
that loads project config correctly can still prompt in the TUI (case 2). Nothing in the
naming hints at this — `decision_for_dir` falls back through `project_root`,
`get_active_project` does not.

`trust_level = "untrusted"` suppresses the startup prompt rather than reinforcing it,
because the TUI gate tests `is_none()`. An explicit distrust is quieter than saying nothing.

Marketplaces and plugins are blocked without appearing on the denylist, via a separate
`effective_user_config()` filter. Reading only `PROJECT_LOCAL_CONFIG_DENYLIST` would give
exactly the wrong answer, which is why the experiment mattered.

Skills loading from *disabled* layers is deliberate — `all_layers_high_to_low()` is used
where `effective_config()` would have been the obvious choice. It makes
`.codex/skills/` the single most reliable delivery channel Codex offers.

## Open Questions

Whether a symlinked `<repo>/.codex -> <instance>/.codex` works. This is the cleanest way
to get the per-repo layout without duplicating payload or changing `project_root_markers`,
and it is the highest-value untested item. It needs a check of what path the `hooks.state`
key is computed from (the symlink or its target) and whether skill roots deduplicate
across the two. A morning's experiment with the existing lab.

The exact set of `.git` semantics `find_project_root` accepts. It calls `get_metadata` on
`<ancestor>/.git` and only requires the stat to succeed — so a `.git` *file* (a linked
worktree or submodule) should count as a marker, unlike `load_project_layers`, which
explicitly requires `.codex` to be `is_directory`. There is also machinery for linked
worktrees (`root_checkout_hooks_folder_for_dir`, `loader/mod.rs:933-945`) that remaps hook
folders from the checkout root to the repo root. A sibling spike is looking at worktrees;
this interaction is untested here and matters if niwa uses worktrees.

Whether system-level config (`/etc/codex/config.toml`) or managed config could carry the
bootstrap instead of the user's `~/.codex/config.toml`. `project_root_markers` and
`projects` are both read from `merged_so_far`, which includes the `System` layer, so this
should work and would keep niwa out of the developer's personal config file — but it needs
root to install and I did not test it.

Whether `codex exec` in a *non-interactive* context ever prompts for hooks rather than
silently skipping. All my `exec` runs had either correct state or no state; I never saw a
prompt, consistent with round-1, but I did not test a TTY-attached `exec`.

## Summary

Codex's project-level config layer is real and carries almost everything niwa needs — instruction context, skills, config, MCP servers, and hooks all load from a `.codex/` directory found by walking up from cwd — but the walk stops at the nearest `project_root_markers` entry, which defaults to `.git`, so an instance-root `.codex/` is invisible from inside a cloned repo until `project_root_markers` is repointed at a niwa marker. That bootstrap, plus a `trust_level = "trusted"` entry keyed on every cloned repo's git root (without which the interactive TUI blocks on a trust prompt, even when the config itself loads fine), can only come from the user/system config and never from the project layer itself, so niwa still needs to write into one config file it controls — but it no longer needs `CODEX_HOME` exported into anyone's environment, which removes the design's weakest link. The biggest open question is whether symlinking `<repo>/.codex` to the instance's copy works, since that would deliver the payload under the default `.git` marker and avoid changing `project_root_markers` globally at all.
