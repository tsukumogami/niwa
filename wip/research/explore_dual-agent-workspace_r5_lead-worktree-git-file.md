# Lead: Does a real git worktree's .git file satisfy Codex's project-root marker check?

**Yes.** A working tree produced by real `git worktree add` behaves exactly like a clone for
config-layer, skill, and context discovery. The design needs nothing worktree-specific.

All experiments ran against codex-cli 0.147.0 (`/home/dgazineu/.tsuku/tools/current/codex`,
`codex --version` -> `codex-cli 0.147.0`) with an isolated `CODEX_HOME` at
`/home/dgazineu/.claude/jobs/7838923c/tmp/r5/home`. The host's real `~/.codex` was never read,
written, or referenced; no `codex login`/`logout` ran; the provider pointed at a dead local URL
(`http://127.0.0.1:9/v1`) so no credential was used and no packet left the machine. No repository,
branch, or worktree in the live workspace was touched — the repository and its worktree were built
from scratch in scratch space.

Scripts are reproducible at `/home/dgazineu/.claude/jobs/7838923c/tmp/r5/`: `setup.sh` (builds the
repo and the worktree), `probe.sh` (the three trust runs), `control.sh` (the negative and trust
controls). Source citations are the public `openai/codex` tree at tag `rust-v0.147.0`, fetched to
`/home/dgazineu/.claude/jobs/7838923c/tmp/src147/`.

## Findings

### 1. The lab is a genuine linked worktree, not a synthetic gitlink

`setup.sh` runs `git init`, commits, then `git worktree add -b wtbranch`. Verified output:

```
=== .git in main checkout ===
drwxrwxr-x 9 dgazineu dgazineu 4096 .../inst/public/mainrepo/.git
=== .git in worktree ===
-rw-rw-r-- 1 dgazineu dgazineu   95 .../inst/public/wtrepo/.git
--- contents of worktree .git ---
gitdir: /home/dgazineu/.claude/jobs/7838923c/tmp/r5/inst/public/mainrepo/.git/worktrees/wtrepo
=== .codex in worktree ===
lrwxrwxrwx 1 dgazineu dgazineu 12 .../inst/public/wtrepo/.codex -> ../../.codex
=== git worktree list ===
.../inst/public/mainrepo  f2a0a69 [main]
.../inst/public/wtrepo    f2a0a69 [wtbranch]
=== git rev-parse from worktree ===
--show-toplevel     .../inst/public/wtrepo
--git-dir           .../inst/public/mainrepo/.git/worktrees/wtrepo
--git-common-dir    .../inst/public/mainrepo/.git
```

So the `.git` is a 95-byte **regular file**, the pointer targets a `worktrees/<name>` subdirectory
inside the parent repository's git directory, and git resolves a separate common directory — every
property that distinguishes a real worktree from the synthetic gitlink used in earlier spikes.

The payload uses the design's actual shape: one shared `.codex` at the instance root, reached from
the worktree through a **relative symlink** `wtrepo/.codex -> ../../.codex`. The worktree is a
sibling of the main checkout under `public/`, not nested inside it, so the upward walk from the
worktree can never reach the main checkout's `.git` directory. The layout:

```
r5/inst/                     instance root
  .codex/                    the single shared payload
    config.toml              model = "gpt-5-PROJECT-LEVEL"; project_doc_fallback_filenames = ["CLAUDE.local.md"]
    skills/niwa-probe/SKILL.md
  AGENTS.md                  INSTANCE_ROOT_AGENTS_SENTINEL
  public/
    AGENTS.md                GROUP_PUBLIC_AGENTS_SENTINEL
    mainrepo/   .git (dir)   .codex -> ../../.codex  CLAUDE.local.md  sub/deep/CLAUDE.local.md
    wtrepo/     .git (FILE)  .codex -> ../../.codex  CLAUDE.local.md  sub/deep/CLAUDE.local.md
    nogitrepo/  (no .git)    .codex -> ../../.codex  CLAUDE.local.md  sub/deep/CLAUDE.local.md
```

A scan confirmed no `.git` exists in any ancestor above the instance, so nothing above could rescue
a failed marker match.

### 2. The decisive experiment: all three channels discovered from the worktree

The probe reads `model` from `codex doctor` (project layer won iff it prints `gpt-5-PROJECT-LEVEL`
rather than the user-level `gpt-5-USER-LEVEL`) and greps `codex debug prompt-input` for skill and
context sentinels. From `probe.sh`, run 1 (both checkouts trusted):

```
=== worktree root / nested / deep ===
  [wt_root] skill=1 wt_ctx=1 wt_deep=0 ... chain=['.../inst/public/wtrepo']
  [wt_sub]  skill=1 wt_ctx=1 wt_deep=0 ... chain=['.../inst/public/wtrepo/sub']
  [wt_deep] skill=1 wt_ctx=1 wt_deep=1 ... chain=['.../inst/public/wtrepo/sub/deep']
=== main checkout control ===
  [main_root] skill=1 main_ctx=1 main_deep=0 ... chain=['.../inst/public/mainrepo']
  [main_deep] skill=1 main_ctx=1 main_deep=1 ... chain=['.../inst/public/mainrepo/sub/deep']
=== effective model (which config layer won) ===
  [wt_root]   model  gpt-5-PROJECT-LEVEL · dead
  [wt_deep]   model  gpt-5-PROJECT-LEVEL · dead
  [main_root] model  gpt-5-PROJECT-LEVEL · dead
```

The worktree column is identical to the main-checkout column at every depth. Raw
`codex debug prompt-input` output from `wtrepo/sub/deep`, two levels below the worktree root:

```
- niwa-probe: NIWA_PROBE_SENTINEL_SKILL loaded from the shared instance payload.
  (file: /home/.../r5/inst/.codex/skills/niwa-probe/SKILL.md)

# AGENTS.md instructions for /home/.../r5/inst/public/wtrepo/sub/deep
<INSTRUCTIONS>
WORKTREE_CLAUDE_LOCAL_SENTINEL
WORKTREE_DEEP_CLAUDE_LOCAL_SENTINEL
</INSTRUCTIONS>
```

All three channels are present. The skill resolves to the shared payload at `inst/.codex/`, reached
only through the worktree's relative symlink. The context is concatenated root-down (repo root file
first, then the nested one), which is only possible if the project root was fixed at the worktree
root. And the `CLAUDE.local.md` pickup is itself proof the project `config.toml` loaded, since
`project_doc_fallback_filenames` is set **only** in the shared payload — without it Codex would not
look at `CLAUDE.local.md` at all.

**The nested probe is the discriminating one.** Running from the worktree *root* proves nothing:
if the marker check had failed, `find_project_root` would fall back to `cwd`, and since `cwd` is
itself the directory holding `.codex`, the layer would still load. From a nested directory the two
outcomes separate cleanly. The negative control makes that explicit — `nogitrepo` has the identical
payload shape and no `.git` anywhere (`control.sh`):

```
############ NEGATIVE CONTROL: no .git marker, nested cwd ############
  [nogit_root] skill=1 nog_root_ctx=1 nog_deep_ctx=0 chain=['.../public/nogitrepo']
  [nogit_deep] skill=0 nog_root_ctx=0 nog_deep_ctx=0 chain=[]
  [nogit_root]  model  gpt-5-PROJECT-LEVEL · dead
  [nogit_deep]  model  gpt-5-USER-LEVEL · dead

############ POSITIVE: worktree, nested cwd (repeat) ############
  [wtree_deep] skill=1 wt_root_ctx=1 wt_deep_ctx=1 chain=['.../public/wtrepo/sub/deep']
  [wtree_deep]  model  gpt-5-PROJECT-LEVEL · dead
```

Without a marker, the nested run loses everything: no skill, no context, user-level model. With the
worktree's `.git` **file**, the nested run keeps everything. The probe discriminates, and the
worktree passes.

### 3. Why: the marker check is a bare metadata stat, with no type or git awareness

Read directly from the implementation. `codex-rs/config/src/loader/mod.rs:1161-1184`:

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
            let marker_path_uri = PathUri::from_abs_path(&marker_path);
            if fs.get_metadata(&marker_path_uri, /*sandbox*/ None).await.is_ok() {
                return Ok(ancestor);   // <-- success of the stat is the ENTIRE test
            }
        }
    }
    Ok(cwd.clone())
}
```

The check is **existence only**. There is no `is_directory` test, no read of the file, no `gitdir:`
parse, no `git` invocation. A regular file named `.git` satisfies it exactly as a directory does —
and so would a FIFO, a socket, or a dangling-target-free symlink. The default marker list is
`&[".git"]` (`codex-rs/config/src/project_root_markers.rs:5`).

The same pattern appears independently in `find_git_checkout_root`
(`codex-rs/config/src/loader/mod.rs:1186-1208`), which also gates purely on
`get_metadata(&dot_git_uri, None).await.is_ok()`. Both the project-root walk and the checkout-root
walk therefore stop at a worktree's `.git` file. Contrast this with the one place Codex *does*
inspect the file — see the next section — which confirms the plain stat in `find_project_root` is a
deliberate difference, not an oversight.

### 4. The adjacent machinery: the worktree remap exists, and it is hooks-only

The remap the reviewer had heard about is real, and I can state its scope precisely. Two pieces:
`root_checkout_hooks_folder_for_dir` (`codex-rs/config/src/loader/mod.rs:932-942`) computes, for a
directory inside a linked worktree, the corresponding folder in the main checkout —
`Some(repo_root.join(relative_dir).join(".codex"))`, returning `None` early when
`checkout_root == repo_root` (an ordinary clone). That path is then consumed by
`merge_root_checkout_project_hooks` (`loader/mod.rs:1364`), whose doc comment at line 1362 states
the scope in words: *"For linked worktrees, preserve ordinary worktree-local project config while
replacing only hook declarations with the matching root-checkout layer."* The body does exactly
that — `config_table.remove("hooks")`, then re-insert `hooks` from the root checkout's
`config.toml`. Every other key in the worktree's project layer stays worktree-local. Nothing
comparable exists for skills or context: grepping the whole fetched tree for `worktree` produces no
hit in `core/src/agents_md.rs`, `core/src/project_doc.rs`, or `ext/skills/src/host_roots.rs`. So
config-layer and context discovery are untouched by worktree status, which is what the experiment
independently showed. Hooks remain out of scope for this feature.

### 5. Bonus, and it matters for the trust story: a worktree inherits the main checkout's trust

Run 2 of `probe.sh` trusted **only** `mainrepo` and left `wtrepo` absent from the trust map. The
worktree still got the full project layer:

```
############ RUN 2: trust keyed ONLY on the MAIN checkout ############
  [wt_root_untrusted] skill=1 wt_ctx=1 ... chain=['.../inst/public/wtrepo']
  [wt_root_untrusted]  model  gpt-5-PROJECT-LEVEL · dead
```

That is a genuine inheritance and not an artifact of trust being irrelevant, because the control
with **nothing** trusted disables the layer for both checkouts:

```
############ TRUST CONTROL: NOTHING trusted at all ############
  [notrust_wt_deep] skill=1 wt_root_ctx=0 wt_deep_ctx=0 chain=[]
  [notrust_wt_deep]  model  gpt-5-USER-LEVEL · dead
  [notrust_main]     skill=1 ... chain=[]
  [notrust_main]     model  gpt-5-USER-LEVEL · dead
```

Trusting the worktree by its own path also works (run 3). The mechanism is
`resolve_root_git_project_for_trust` (`codex-rs/git-utils/src/info.rs:775-821`), which *is*
git-aware: it finds the nearest `.git`, and if that is not a directory it reads the file, strips the
`gitdir:` prefix, resolves the pointer, **requires the parent component to be literally named
`worktrees`**, and returns `common_dir.parent()` — the main repository's checkout root. The trust
lookup then tries both the project-root key and this repo-root key
(`loader/mod.rs:1024-1031, 892-908`). So niwa may key trust on either the worktree path or the
parent repository path; the parent covers all of its worktrees at once.

## Implications

**The answer is yes, and the design needs no worktree-specific mechanism.** A linked worktree is
indistinguishable from a clone for every channel the design depends on: the project config layer
loads from `<worktree>/.codex/config.toml`, skills load from the shared payload through the
relative symlink, and instruction context composes root-down from the worktree root. This holds
from the worktree root and from nested directories alike, which is the case that would have broken.
The design's existing plan — write the payload at the instance root, symlink `<repo>/.codex ->
<instance>/.codex` into each checkout, rely on the default `.git` marker, touch neither
`CODEX_HOME` nor the global config — applies verbatim to worktrees. No new code, no new
configuration key, no `project_root_markers` override.

The result is also more durable than a lucky coincidence. `find_project_root` cannot become
worktree-hostile without someone deliberately adding a type check that upstream conspicuously did
not write, and the one place upstream *does* parse the gitlink is scoped by an explicit doc comment
to hooks alone. The residual coupling is to upstream keeping the marker test as a bare stat.

One small planning gain falls out: because trust resolves through the main repository root, niwa can
write a single `[projects."<repo>"] trust_level = "trusted"` entry per repository and have every
worktree of it inherit trust, rather than needing an entry per worktree. Whether to rely on that or
to write per-worktree keys is a design call, not a blocker — both work.

Since the answer is yes, the "what a worktree-specific answer would cost" question is moot and I
have not costed it.

## Surprises

Two.

**Skills load even when the project layer is untrusted.** In the no-trust control, `skill=1` while
`model` fell back to user level and the context chain was empty. So an untrusted project still
contributes its `.codex/skills/` to the prompt, even though its `config.toml`, hooks, and exec
policies are gated off. That is a wider surface than "untrusted means the project layer is
disabled" suggests. It is good news for the design's delivery path but it is a security-relevant
asymmetry someone should confirm is intended, and it is orthogonal to worktrees — it reproduces on
the plain clone too.

**Probing from a repository root proves nothing about the marker.** Because `find_project_root`
falls back to `cwd` on failure, and `cwd` at the repo root is exactly the directory holding
`.codex`, a root-only probe returns a passing result whether or not the marker matched. Any future
spike on marker behavior must probe from a nested directory. The negative control here exists
specifically to close that hole; without it this experiment would have been worthless.

Also worth recording as a harness note: `wire_api = "chat"` is rejected outright by 0.147.0 with
"no longer supported" — the dead-provider recipe used by sibling spikes needs
`wire_api = "responses"`.

## Open Questions

I did not exercise these, and none of them gate the answer:

- **Worktrees whose path is outside the instance tree.** My worktree was a sibling under
  `public/`. If niwa parks worktrees somewhere else (say `.niwa/worktrees/`), the relative `.codex`
  symlink target changes and the group-level directory above it differs. The marker logic is
  path-independent so I expect no difference, but the symlink arithmetic is niwa's to get right.
- **The hooks remap end to end in a worktree.** I read its scope from source and confirmed by grep
  that it does not touch config or context, but I did not fire a hook from a worktree. Hooks are
  out of scope, and the r3 finding that `[hooks.state]` is keyed by the unresolved path interacts
  with this remap in a way someone should check when hooks come back into scope.
- **Nested and detached worktrees.** A worktree placed *inside* the main checkout's tree, and
  `git worktree add --detach`, were not tested.
- **Windows.** The trust remap has Windows-specific path normalization; everything here was Linux.
- **Concurrent sessions in two worktrees of the same repository** sharing one payload — not
  exercised, though the r3 finding that only one project layer exists per session suggests no
  interaction.

## Summary

Yes — a real `git worktree add` working tree's `.git` **file** satisfies Codex's project-root marker
check, and the project config layer, the skills from a symlinked shared payload, and the instruction
context were all discovered from the worktree root and from two levels below it, identically to a
plain clone; a no-`.git` negative control loses all three at nested depth, confirming the probe
discriminates. The reason is that `find_project_root` (`codex-rs/config/src/loader/mod.rs:1161-1184`)
tests nothing but the success of a metadata stat on `<ancestor>/.git`, with no directory-type check
and no git awareness, and the one worktree-aware remap upstream carries
(`loader/mod.rs:932-942` feeding `merge_root_checkout_project_hooks` at `loader/mod.rs:1364`) is
scoped by its own doc comment to hook declarations only, leaving config and context alone. The
design therefore needs no worktree-specific mechanism; residual risk is limited to upstream someday
tightening that bare stat into a type check, plus a bonus finding worth folding in — trust resolves
through the main repository root, so one trust entry per repository covers all its worktrees.
