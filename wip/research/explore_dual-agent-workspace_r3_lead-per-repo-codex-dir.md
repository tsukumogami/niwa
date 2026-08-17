# Lead: Can niwa deliver Codex payload per-repo under the default marker, and does a symlinked .codex work?

All experiments ran against codex-cli 0.147.0 (`/home/dgazineu/.tsuku/tools/current/codex`,
`codex --version` → `codex-cli 0.147.0`) with an isolated `CODEX_HOME` at
`/home/dgazineu/.claude/jobs/7838923c/tmp/r3/home`. The host's real `~/.codex` was never read,
written, or referenced. Sessions ran against a dead fake provider
(`base_url = "http://127.0.0.1:9/v1"`) so no credential was ever used and no network call left the
machine. Source citations are the public `openai/codex` tree at tag `rust-v0.147.0`, fetched to
`/home/dgazineu/.claude/jobs/7838923c/tmp/src147/`.

Scripts are reproducible at `/home/dgazineu/.claude/jobs/7838923c/tmp/r3/`:
`setup.sh` (builds the tree), `probe.sh`, `hooktest.sh`, `gittest.sh`, `matrix.sh`, `final.sh`.

The synthetic instance mimics the real workspace shape:

```
r3/inst/                      instance root
  .codex/                     the single shared payload
    config.toml
    hooks.json
    skills/niwa-probe/SKILL.md
  AGENTS.md                   instance-level context
  public/
    AGENTS.md                 group-level context
    alpha/  .git  CLAUDE.local.md  .codex -> ../../.codex   sub/deep/deeper/
    beta/   .git  CLAUDE.local.md  .codex -> ../../.codex   sub/deep/deeper/
    gamma/  .git  CLAUDE.local.md  AGENTS.md (committed)    .codex (real copy)
    delta/  .git  .agents/skills/agents-probe/SKILL.md      (no .codex at all)
```

## Findings

### 1. The decisive experiment: a symlinked `<repo>/.codex` works — verified

**Yes.** The project config layer, its `config.toml`, its skills, and its hooks all load through a
symlinked `.codex`.

`is_directory` follows symlinks. **Read directly from the implementation**, not inferred —
`codex-rs/exec-server/src/local_file_system.rs:592-614`:

```rust
let symlink_metadata = tokio::fs::symlink_metadata(path.as_path()).await?;
let is_symlink = symlink_metadata.is_symlink();
let metadata = if is_symlink {
    tokio::fs::metadata(path.as_path()).await?   // <-- follows the link
} else {
    symlink_metadata
};
Ok(FileMetadata { is_directory: metadata.is_dir(), ... })
```

That is the exact call the project-layer loader gates on
(`codex-rs/config/src/loader/mod.rs:1250-1256`: `dir.join(".codex")` →
`get_metadata(...).map(|metadata| metadata.is_directory)`), so a symlink to a directory passes.

**Verified by experiment.** From `r3/inst/public/alpha` (payload reachable only through the symlink):

```
$ bash /home/dgazineu/.claude/jobs/7838923c/tmp/r3/final.sh
=== A. repo root, subdir, deep subdir (symlinked payload) ===
  [alpha_root] skill=NIWA_PROBE x1 alpha_ctx=1 beta_ctx=0 ... chain=['.../inst/public/alpha']
  [alpha_sub]  skill=NIWA_PROBE x1 alpha_ctx=1 beta_ctx=0 ... chain=['.../inst/public/alpha/sub']
  [alpha_deep] skill=NIWA_PROBE x1 alpha_ctx=1 beta_ctx=0 ... chain=['.../alpha/sub/deep/deeper']
  [beta_root]  skill=NIWA_PROBE x1 alpha_ctx=0 beta_ctx=1 ... chain=['.../inst/public/beta']
```

The raw `codex debug prompt-input` output from `alpha` carries all three payload channels:

```
### Available skills
- niwa-probe: NIWA_PROBE_SENTINEL_SKILL loaded from the shared instance payload.
  (file: /home/.../r3/inst/.codex/skills/niwa-probe/SKILL.md)

# AGENTS.md instructions for /home/.../r3/inst/public/alpha
<INSTRUCTIONS>
REPO_ALPHA_CLAUDE_LOCAL_SENTINEL
(instance section) INSTANCE_ROOT_AGENTS_SENTINEL_COMPOSED
(group section) GROUP_PUBLIC_AGENTS_SENTINEL_COMPOSED
</INSTRUCTIONS>
```

The `CLAUDE.local.md` pickup is itself the proof that `config.toml` loaded from the symlinked layer:
`project_doc_fallback_filenames = ["CLAUDE.local.md"]` was set **only** in `inst/.codex/config.toml`,
never in the user config. Without that key Codex would not look at `CLAUDE.local.md` at all.

Depth does not matter. From `alpha/sub/deep/deeper`, three levels below the repo root, the skill and
the repo context both still load — the walk climbs to `.git` at the repo root and finds the symlink
there.

#### Do skill roots duplicate across repos?

**No, and the question does not arise.** Only one project layer exists per session, because the walk
stops at the first `.git`. Each probe above shows `skill=NIWA_PROBE x1` — exactly one occurrence,
whether run from `alpha`, from `beta`, or from a deep subdirectory. Codex also dedupes roots by path
anyway (`codex-rs/ext/skills/src/host_roots.rs:295-298`,
`fn dedupe_skill_roots_by_path` → `seen.insert(root.path.clone())`).

#### The `hooks.state` key uses the SYMLINK path, not the resolved target — verified

This is the one place the symlink does **not** collapse duplication. Three runs, each with a
different `[hooks.state]` key in the user config and an identical `hooks.json` in the shared payload
(`hooktest.sh`, computed hash `sha256:b718a6b167b490e7ff2e4b456aa4072f6c35087152667153adfdce5130c009e0`):

```
=== H1: state keyed by RESOLVED path (.../inst/.codex/hooks.json) ===
  markers:
=== H2: state keyed by SYMLINK path (.../inst/public/alpha/.codex/hooks.json) ===
  markers: HOOK_FIRED /home/.../r3/inst/public/alpha|
=== H0: no state block at all (control) ===
  markers:
```

H2 fired from `alpha` and did **not** fire from `beta`, even though both share the same physical
`hooks.json` through their symlinks. So the key is the unresolved per-repo path, and **niwa needs one
`[hooks.state]` entry per repo per handler** — the symlink does not collapse it.

This matches the source: `load_hooks_json` builds `source_path = config_folder.join("hooks.json")`
(`codex-rs/hooks/src/engine/discovery.rs:307`) and the key source is
`source_path.display().to_string()` (`discovery.rs:196`), where `config_folder` is the layer's
`dot_codex_folder` — set from `dot_codex_abs`, the unresolved path
(`codex-rs/config/src/loader/mod.rs:945-953`, `fn project_layer_entry`). Nothing canonicalizes it.

Independent corroboration from the denylist warning, which names the symlink path:

```
$ codex doctor            # cwd = inst/public/alpha
  startup warning  Ignored unsupported project-local config keys in
                   /home/.../r3/inst/public/alpha/.codex/config.toml: model_provider, notify.
```

### 2. Does the symlink survive git? — yes, with one trap

`git status --porcelain` is clean in every repo once `.codex` is excluded (`final.sh` section D):

```
  alpha  status --porcelain: []
  beta   status --porcelain: []
  gamma  status --porcelain: []
```

**The trap: the trailing-slash pattern does not match a symlink.** git 2.43.0, `gittest.sh`:

```
=== alpha: bare '.codex' in .git/info/exclude, symlinked .codex ===
--- status after exclude ---
?? CLAUDE.local.md          (.codex gone — excluded)
=== beta: '.codex/' (trailing slash) against a SYMLINK ===
?? .codex                   (STILL UNTRACKED)
=== gamma: '.codex/' against a REAL directory ===
(.codex excluded fine)

=== git check-ignore -v ===
alpha/.codex -> .git/info/exclude:7:.codex     .codex
beta/.codex  -> NOT IGNORED
gamma/.codex -> .git/info/exclude:7:.codex/    .codex
```

A trailing-slash gitignore pattern is directory-only, and git classifies a symlink as a file
regardless of what it points at. This is a live hazard for niwa: `internal/gitexclude/exclude.go:35`
currently writes `var niwaExcludePatterns = []string{"*.local*", ".niwa/"}` — the trailing-slash
form. If `.codex/` were added the same way, every repo with a symlinked payload would show `?? .codex`
forever. **niwa must use the bare `.codex` form.** The bare form matches both a symlink and a real
directory, so it is correct under either delivery.

A dangling symlink (payload moved away) is harmless: `codex debug prompt-input` exits `rc=0` with no
error and simply skips the layer.

### 3. Architecture B with real copies also works, and the duplication is not a problem

Not needed as a fallback for correctness — the symlink works — but measured for completeness.
`gamma` ran the whole time with a **real copied** `.codex` directory rather than a symlink, and
behaved identically (`skill=NIWA_PROBE x1`, context loaded, `git status` clean).

Payload size for the synthetic instance: 681 bytes of content across 3 files, 24K of allocated
blocks (`du -sb`/`du -sh` on `inst/.codex`). A realistic niwa Codex payload is `config.toml` +
`hooks.json` + a skills tree — text, so tens of KB. Copied into 9 repos (the live workspace has 9)
that is well under a megabyte. The one thing that would change the arithmetic is a shipped helper
binary: niwa's Claude payload puts 4.1M in `.claude/bin` (`du -sh .claude/bin` in the live
workspace), and 4.1M × 9 ≈ 37M per instance would be a real cost. Text-only payloads are free.

What niwa must regenerate on every apply, under either variant: the payload itself (one directory
with the symlink, N directories with copies), the per-repo composed context file, the per-repo
`.git/info/exclude` block, and the per-repo user-config entries (trust, plus hook state if hooks are
used). The symlink removes exactly one of those five from the per-repo loop.

### 4. Per-repo context layering

**Confirmed: under Architecture B the instance-root and group context are never reached.** Every
probe reports a chain containing only the repo (or a subdirectory of it) — never
`inst/AGENTS.md` or `inst/public/AGENTS.md`, both of which exist on disk:

```
chain=['/home/.../r3/inst/public/alpha']
INSTANCE_ROOT_AGENTS_SENTINEL 0    GROUP_PUBLIC_AGENTS_SENTINEL 0
```

So niwa must compose instance + group + repo content into a single per-repo file. **Verified this
loads completely.** A 49,767-byte composed `CLAUDE.local.md` (`matrix.sh` T1) came through whole —
all 700 padding lines plus the tail sentinel:

```
CLAUDE.local.md size: 49767
[t1] head_sentinel=1 tail_sentinel=1 pad_lines=700     # project_doc_max_bytes = 65536, PROJECT layer
[t2] head_sentinel=1 tail_sentinel=0 pad_lines=461     # default 32768 — silently truncated
```

`project_doc_max_bytes` can be raised **from the project layer**. This was the question that most
threatened Architecture B, and the answer is favorable: T1 set `project_doc_max_bytes = 65536` only
in `inst/.codex/config.toml`, with the user config holding nothing but `model` and trust entries, and
the full document loaded. It also works from the user config and from a `-c` override:

```
$ codex debug prompt-input                      # 65536 in USER config
pad_lines 700 tail 1
$ codex debug prompt-input -c project_doc_max_bytes=65536
pad_lines 700 tail 1
```

*(An earlier matrix run reported the user-config case failing. That was a bug in my script — the
appended key landed inside the preceding `[projects."..."]` table, making it
`projects.<path>.project_doc_max_bytes`. The clean re-run above is the result that stands.)*

Under Architecture B the budget-starvation hazard largely disappears: the chain begins at the repo
root, so there is no upper layer draining the budget root-first before the repo's own file is read.
Only a subdirectory `AGENTS.md` below the repo root shares the budget, and it comes after.

**The committed-`AGENTS.md` collision is worse than it looks, and `CLAUDE.local.md` is the wrong
file to write.** `gamma` ships its own committed `AGENTS.md` alongside a niwa `CLAUDE.local.md`:

```
[gamma_committed] gamma_committed=1 gamma_local=0     # repo's AGENTS.md wins, niwa's file IGNORED
[gamma_override]  gamma_committed=0 gamma_override=1  # AGENTS.override.md wins, both others ignored
```

Because discovery is strict first-match over `[AGENTS.override.md, AGENTS.md, ...fallbacks]` and at
most one file per directory, a repo that commits its own `AGENTS.md` silently swallows niwa's
`CLAUDE.local.md` — no warning, no partial load. `public/shirabe` in the live workspace is exactly
this case. niwa must therefore write **`AGENTS.override.md`**, and because that file also hides the
repo's own `AGENTS.md`, niwa must inline the repo's committed content into the composed file.

### 5. Where each key must be written — the definitive table

Every row is verified by experiment unless the Evidence column says otherwise.

| Key | Project layer `<repo>/.codex/config.toml` | User config `$CODEX_HOME/config.toml` | Evidence |
|---|---|---|---|
| `project_doc_fallback_filenames` | **Works** | Works | `CLAUDE.local.md` picked up with the key set only in the project layer (`final.sh` A) |
| `project_doc_max_bytes` | **Works** | Works | T1: 49,767-byte doc loaded whole from project layer; T2: 32768 default truncates to 461/700 lines; user-config and `-c` re-runs both load 700 |
| `project_root_markers` | **IGNORED** | **Required** | T7: `project_root_markers = ["NIWA_MARKER"]` in the project layer with the marker file present at the instance root — chain still `['.../public/alpha']`, instance and group sentinels both 0. Source: read from `merged_so_far`, built before project layers exist (`loader/mod.rs:303-312`) |
| `mcp_servers` | **Works** | Works | T4: server declared only in the project layer → `codex doctor` reports `mcp 1 server (1 stdio) · 0 disabled` |
| `hooks` (declarations, TOML or `hooks.json`) | **Works** | Works | H2: SessionStart hook fired from `<repo>/.codex/hooks.json` |
| `hooks.state` (`enabled`, `trusted_hash`) | **IGNORED** | **Required**, one entry per repo per handler | `final.sh` C: byte-identical state block moved into the project layer → hook did not fire (`markers: []`); same block in the user config fires (H2). Source: `hook_states_from_stack` skips every layer that is not `User` or `SessionFlags` (`codex-rs/hooks/src/config_rules.rs:23-30`) |
| `sqlite_home` | **Works** | Works | T6: `codex doctor` reports `sqlite home  ~/.../r3/sqlhome_project (dir)` from the project layer |
| `projects.<path>.trust_level` | **IGNORED** | **Required**, one entry per cloned repo git root | T5: trust present only in the project layer → the whole layer was disabled, `project_doc_fallback_filenames` never applied, no context loaded at all (`head_sentinel=0`). Source: trust is read from `merged_so_far` (`loader/mod.rs:325`), a chicken-and-egg the project layer cannot break |
| Denylisted keys — `model_provider`, `model_providers`, `notify`, `profile`, `profiles`, `openai_base_url`, `chatgpt_base_url`, `otel`, `apps_mcp_product_sku`, the realtime base URLs, `features.respect_system_proxy` | **Stripped, with a startup warning** | Required | `codex doctor` startup warning naming `model_provider, notify`; list at `loader/mod.rs:64-76` |

Net user-config surface per architecture, for an instance of N repos with H hook handlers:

| | Architecture A (instance root + repointed marker) | Architecture B (per-repo, default marker) |
|---|---|---|
| `project_root_markers` | 1 entry — **replaces `.git` machine-wide** | none |
| `projects.*` trust | N entries (the TUI gate keys on cwd and git repo root, so per-repo either way) | N entries |
| `hooks.state` | H entries (one payload path) | N × H entries |
| Effect on Codex outside niwa | every non-niwa repo loses `.git` as a project root | none |

### 6. Bonus: `.agents/skills` is a zero-config skills channel

`delta` has **no `.codex` at all**, no trust entry, and no config anywhere — only
`.agents/skills/agents-probe/SKILL.md`:

```
=== .agents/skills root: no .codex, no trust, no config ===
AGENTS_DIR_SENTINEL_SKILL: 1 | NIWA_PROBE: 0
```

The skill loaded. Source: `repo_agents_skill_roots` probes `<dir>/.agents/skills` for every directory
between the project root and cwd, independent of the config layer stack
(`codex-rs/ext/skills/src/host_roots.rs:168-201`). This is an even lower-friction delivery path than
`.codex/skills` when skills are all niwa needs to ship.

## Implications

**Verdict: Architecture B, with a symlinked `<repo>/.codex -> <instance>/.codex`.** The question that
was supposed to decide it — does a symlinked `.codex` work — comes back yes, and the question that
was supposed to sink it — can `project_doc_max_bytes` be raised without touching the user config —
also comes back yes. B loses nothing that matters and A's cost is unbounded.

The honest trade-off. Architecture A buys one thing: `hooks.state` collapses from N × H entries to H,
because there is a single payload path. It pays for that by writing `project_root_markers` into the
developer's personal `~/.codex/config.toml`, which *replaces* `.git` rather than extending it and so
degrades Codex in every repository on the machine that is not inside a niwa instance — their own
repo-root `.codex/` and `AGENTS.md` stop being found from subdirectories. That is niwa reaching
outside its own sandbox to break unrelated work, for a saving of a few TOML lines. It also does not
even save the trust entries: the interactive TUI gate keys on cwd and the git repo root, so A still
needs one `projects.*` entry per cloned repo. A is worse on every axis except hook-state line count.

What niwa must write into the developer's personal `~/.codex/config.toml` under B: one
`[projects."<repo>"] trust_level = "trusted"` per cloned repo, plus one `[hooks.state."..."]` block
per repo per hook handler if niwa ships hooks. Nothing else. **No global behavior changes at all** —
no `project_root_markers`, no `CODEX_HOME`, no denylisted keys. Everything else niwa needs
(`project_doc_fallback_filenames`, `project_doc_max_bytes`, `mcp_servers`, `sqlite_home`, and the hook
declarations themselves) lives in the payload it owns. If niwa ships no hooks, the personal-config
footprint is exactly N trust lines, all of them scoped to paths inside the instance and all of them
removable when the instance is reaped.

Three things the design must get right, each verified above and each a silent failure if missed:

The gitignore pattern must be the bare `.codex`, not `.codex/`. niwa's existing
`internal/gitexclude/exclude.go:35` uses the trailing-slash form for `.niwa/`; copying that idiom for
`.codex` would leave `?? .codex` in every repo. This is the single highest-risk detail because it
produces no error, just permanent dirt in `git status`.

The per-repo context file must be `AGENTS.override.md`, not `CLAUDE.local.md`, and it must inline
whatever `AGENTS.md` the repo already commits. A repo with its own `AGENTS.md` silently discards
niwa's `CLAUDE.local.md` — `public/shirabe` is that repo today. Using `AGENTS.override.md`
unconditionally makes the behavior uniform across all repos rather than depending on what each clone
happens to ship, and it is the only slot that always wins.

That per-repo file must carry instance + group + repo content composed into one document, because the
walk stops at the repo root and the upper layers are never visited. Raise
`project_doc_max_bytes` from the payload's own `config.toml` — 32768 is not enough for a composed
document and the overflow is cut mid-byte with no marker and nothing on stderr.

On symlink versus real copies: the symlink is the better default because it gives one source of truth
— `niwa apply` regenerates one directory instead of N, and a payload edit is instantly live in every
repo. But the copies also work, and the payload is text measured in kilobytes, so copies are a
perfectly acceptable fallback where symlinks are awkward (Windows without Developer Mode). The design
should treat this as an implementation detail with a fallback, not a load-bearing decision — with one
exception: `hooks.state` needs N × H entries either way, so choosing the symlink does not simplify
the user-config write.

## Surprises

**Skill file paths are canonicalized but config-layer paths are not.** The same symlinked payload
reports its skill as `/home/.../inst/.codex/skills/niwa-probe/SKILL.md` (resolved target) from both
`alpha` and `beta`, while the config layer and the hook key both use the unresolved
`/home/.../inst/public/alpha/.codex/...`. Two path identities for one directory in one process. It
did not bite here, but any future feature that correlates a skill back to its declaring layer by path
would compare a resolved path against an unresolved one.

**`project_doc_max_bytes` works from the project layer.** This was the finding most likely to sink
Architecture B, and going in I expected a document-size limit to be user-only on the same reasoning
that keeps `project_root_markers` user-only — a repo shouldn't get to expand its own footprint in the
prompt. It isn't in the denylist, and it takes effect.

**A full `codex exec` session runs start to finish with no credential at all.** Pointing
`model_provider` at a dead local URL gets you session bootstrap, hook dispatch, and prompt assembly
before anything tries to authenticate. That made the whole hook investigation possible without going
anywhere near the host's live login, and it is a genuinely useful technique for any future spike.

**The `.agents/skills` root needs nothing.** No `.codex`, no config, no trust entry — just the
directory. It is a lower-friction skills channel than the project config layer, discovered by a
separate code path that never consults the layer stack.

## Open Questions

The interactive TUI trust gate was not exercised. Everything here ran through `codex debug
prompt-input`, `codex doctor`, and `codex exec`, none of which render `get_active_project`'s startup
prompt. The established finding that trust must be written per cloned repo git root is consistent
with T5 (where a project-layer-only trust entry disabled the layer entirely), but whether a fully
correct user-config trust block actually suppresses the *TUI* prompt for a symlinked payload needs a
real terminal session. Exercising it would take a pty harness driving `codex` interactively — the
prior spikes' `tui.py`/`tui2.py`/`tui3.py` scratch files suggest that harness already exists.

MCP servers were confirmed to *load* from the project layer (`codex doctor` counts one stdio server)
but never launched. Whether a project-layer MCP server actually spawns and its tools reach the model
would need a real MCP server process and a live session.

Windows symlink behavior is untested — this ran on Linux only. Creating a directory symlink on
Windows requires Developer Mode or elevation, and `dunce::canonicalize` has UNC-stripping behavior
that only matters there. If niwa supports Windows, the copy fallback needs to be the default on that
platform, and the `.codex` gitignore-versus-symlink interaction should be re-checked with Windows git.

The composed-context budget was tested at 49,767 bytes against a 65,536 limit. Where the practical
ceiling sits for a real instance — instance context plus group context plus a repo's own committed
`AGENTS.md`, times the largest repo in the workspace — was not measured, and the truncation is
silent, so the design should pick a limit with real headroom rather than a tested-once number.

## Summary

A symlinked `<repo>/.codex -> <instance>/.codex` fully works — config, skills, instruction context, and hooks all load under Codex's default `.git` marker from the repo root and from directories several levels below it — and critically `project_doc_max_bytes` can be raised from the payload's own `config.toml`, so Architecture B needs no global config change at all beyond one `projects.*` trust line per cloned repo (plus one `[hooks.state]` entry per repo per handler, which the symlink does not collapse because the key uses the unresolved per-repo path). Architecture B is the clear recommendation: Architecture A's only advantage is collapsing those hook-state entries, and it pays for that by replacing `.git` as the project-root marker machine-wide, degrading Codex in every repository outside any niwa instance while still needing the same per-repo trust entries. The biggest open question is the interactive TUI trust gate, which none of `debug prompt-input`, `doctor`, or `exec` renders and which would need a pty harness to exercise.
