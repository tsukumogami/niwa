# Lead: Can project_doc_fallback_filenames deliver per-repo Codex context without writing AGENTS.md into repos?

Binary under test: `/home/dgazineu/.tsuku/tools/current/codex` -> `codex-0.147.0/bin/codex`,
`codex --version` prints `codex-cli 0.147.0`.

Upstream source read at tag `rust-v0.147.0` via
`https://raw.githubusercontent.com/openai/codex/rust-v0.147.0/<path>` (fetch helper
`/home/dgazineu/.claude/jobs/7838923c/tmp/fetch.sh`; downloaded copies under
`/home/dgazineu/.claude/jobs/7838923c/tmp/src147/`).

All experiments ran with an isolated `CODEX_HOME` under
`/home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/`. The host's real `~/.codex` was
never read, written, or referenced. No repo in the live workspace was modified;
the only writes into the workspace are this file. Synthetic trees live under
`/home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/`.

Probe harness: `/home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/probe.sh`, which runs
`codex debug prompt-input` with a given cwd and `CODEX_HOME` and extracts the
`# AGENTS.md instructions ... <INSTRUCTIONS>...</INSTRUCTIONS>` block from the rendered
JSON prompt.

---

## Findings

### 0. The headline correction: there IS an upward walk

**Read directly from the implementation.** The round-1 finding that "Codex ingests
context from exactly the cwd's project doc plus the global file; no upward walk was
observed" is **wrong as a general rule**. It is a correct observation of one special
case.

`codex-rs/core/src/agents_md.rs` opens with the actual contract (lines 1-16):

```rust
//! AGENTS.md discovery and user instruction assembly.
//!
//! Project-level documentation is primarily stored in files named `AGENTS.md`.
//! Additional fallback filenames can be configured via `project_doc_fallback_filenames`.
//! We include the concatenation of all files found along the path from the
//! project root to the current working directory as follows:
//!
//! 1.  Determine the project root by walking upwards from the current working
//!     directory until a configured `project_root_markers` entry is found.
//!     When `project_root_markers` is unset, the default marker list is used
//!     (`.git`). If no marker is found, only the current working directory is
//!     considered. An empty marker list disables parent traversal.
//! 2.  Collect every `AGENTS.md` found from the project root down to the
//!     current working directory (inclusive) and concatenate their contents in
//!     that order.
//! 3.  We do **not** walk past the project root.
```

The implementing function is `agents_md_paths` (`agents_md.rs:154-228`). It calls
`find_nearest_ancestor_with_markers` (`agents_md.rs:176-183`, implemented in
`codex-rs/file-system/src/find_up.rs:27-47`) to locate the project root, then builds
`search_dirs` by walking from cwd up to that root and reversing (`agents_md.rs:184-201`),
so the resulting order is root-first, cwd-last.

The default marker list is `.git`, from
`codex-rs/config/src/project_root_markers.rs:5`:

```rust
const DEFAULT_PROJECT_ROOT_MARKERS: &[&str] = &[".git"];
```

Round-1 almost certainly tested inside a plain, non-git scratch directory. With no
`.git` anywhere above cwd, `find_nearest_ancestor_with_markers` returns `None` and
`search_dirs` collapses to `vec![dir]` (`agents_md.rs:199-201`) — cwd only. That
reproduces the round-1 observation exactly, and it is the *degenerate* branch, not the
rule.

**Verified by experiment.** Two runs against the same shape, one non-git and one git:

Test 8, plain non-git nested tree (`plain/CLAUDE.local.md`, `plain/a/AGENTS.md`,
`plain/a/b/CLAUDE.local.md`), cwd `plain/a/b`, default markers:

```
<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

PLAIN b CLAUDE.local.md

</INSTRUCTIONS>
```

Only cwd. Neither ancestor loaded — matching round-1.

Test 7, inside a git repo. `repoA/.git` exists; files at `repoA/CLAUDE.local.md` and
`repoA/src/CLAUDE.local.md`; cwd is `repoA/src/deep` (which has no doc of its own):

```
<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

REPO-A niwa context (CLAUDE.local.md)


REPO-A src CLAUDE.local.md

</INSTRUCTIONS>
```

Two ancestors, root-first order, cwd contributing nothing. The walk is real.

This matters a great deal to niwa: **niwa's repos are git clones**, so every one of them
carries a `.git` at its root. Under default configuration the walk therefore starts at
the repo root and stops there — it never reaches the group directory or the instance
root. That is why the composed-global-file plan looked forced.

### 1. Exact semantics of `project_doc_fallback_filenames`

**Read directly from the implementation.** `candidate_filenames`
(`agents_md.rs:230-244`):

```rust
fn candidate_filenames(config: &Config) -> Vec<&str> {
    let mut names: Vec<&str> = Vec::with_capacity(2 + config.project_doc_fallback_filenames.len());
    names.push(LOCAL_AGENTS_MD_FILENAME);
    names.push(DEFAULT_AGENTS_MD_FILENAME);
    for candidate in &config.project_doc_fallback_filenames {
        let candidate = candidate.as_str();
        if candidate.is_empty() {
            continue;
        }
        if !names.contains(&candidate) {
            names.push(candidate);
        }
    }
    names
}
```

with `LOCAL_AGENTS_MD_FILENAME = "AGENTS.override.md"` and
`DEFAULT_AGENTS_MD_FILENAME = "AGENTS.md"` (`agents_md.rs:37-39`).

The per-directory probe (`agents_md.rs:205-220`):

```rust
.map(|directory| async move {
    for name in candidate_filenames {
        let candidate = directory.join(name)...;
        match fs.get_metadata(&candidate, None).await {
            Ok(metadata) if metadata.is_file => return Ok(Some(candidate)),
            Ok(_) => {}
            Err(err) if err.kind() == io::ErrorKind::NotFound => {}
            Err(err) => return Err(err),
        }
    }
    Ok(None)
})
```

Answers:

**Is it first-match or concatenate-all?** Both, at different levels, and the distinction
is the crux of this spike:

- **Within a single directory: strictly first-match.** The loop `return`s on the first
  filename whose metadata reports `is_file`. At most one file per directory ever
  contributes.
- **Across directories: concatenate-all.** Each directory in `search_dirs` independently
  yields at most one file, and all of them are concatenated root-to-cwd.

**What is the ordering — is literal `AGENTS.md` always preferred, or does list order
win?** The precedence is hardcoded and the fallback list cannot reorder it:

1. `AGENTS.override.md` (highest)
2. `AGENTS.md`
3. entries of `project_doc_fallback_filenames`, in list order, deduplicated against the
   two above.

The dedup (`!names.contains(&candidate)`) means naming `AGENTS.md` in the fallback list
is a silent no-op — it is already present at position 2 and will not be moved.

Confirmed upstream by `agents_md_preferred_over_fallbacks`
(`codex-rs/core/src/agents_md_tests.rs:1509-1535`), which asserts `"primary"` wins over
`"secondary"` and that discovery returns exactly one path whose basename is
`AGENTS.md`.

**Verified by experiment.** Test 12 set
`project_doc_fallback_filenames = ["CLAUDE.local.md", "AGENTS.md"]` — deliberately
putting `CLAUDE.local.md` *ahead* of `AGENTS.md` in the list — against repoB, which has
both files. cwd `.../inst/public/repoB`:

```
<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

INSTANCE ROOT AGENTS.md


GROUP public AGENTS.md


REPO-B committed AGENTS.md

</INSTRUCTIONS>
```

`REPO-B niwa context (CLAUDE.local.md)` does not appear. List order does not win.

**Verified by experiment (override precedence).** Test 6 added
`repoB/AGENTS.override.md`:

```
<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

REPO-B OVERRIDE FILE

</INSTRUCTIONS>
```

The committed `AGENTS.md` was displaced. `AGENTS.override.md` is the only filename that
outranks a repo's own `AGENTS.md`, and the round-1 spike did not surface it.

**Does an empty file count as a match that stops the search?** **Yes, and this is a
correctness trap.** The stopping decision is made on `get_metadata(...).is_file` alone
(`agents_md.rs:212`); content is only examined later, in `read_agents_md`
(`agents_md.rs:132`), where `if !text.trim().is_empty()` decides whether to push an
entry. By then the other candidate filenames in that directory were never probed. An
empty or whitespace-only `AGENTS.md` therefore consumes the slot and suppresses the
fallback, yielding *no* project doc for that directory rather than falling through.

**Verified by experiment.** Test 5: repoC has a zero-byte `AGENTS.md` and a populated
`CLAUDE.local.md`, with `project_doc_fallback_filenames = ["CLAUDE.local.md"]`:

```
##### TEST 5: empty AGENTS.md + populated CLAUDE.local.md #####
(no AGENTS.md instructions block in prompt)

##### TEST 5b: same, but whitespace-only AGENTS.md #####
(no AGENTS.md instructions block in prompt)
```

Both the zero-byte and the whitespace-only (`"   \n\n"`) case suppress the fallback.
The global file survives — a direct dump of the prompt for that run shows:

```
'# AGENTS.md instructions\n\n<INSTRUCTIONS>\nglobal codex home instructions\n</INSTRUCTIONS>'
```

Note the header degrades from `# AGENTS.md instructions for <dir>` to bare
`# AGENTS.md instructions` when no project doc contributed. Only the repo-level context
is lost, silently.

### 2. The exact discovery rule for project docs generally

**Read from source, verified by experiment.** For each turn environment:

1. If `project_doc_max_bytes == 0`, return nothing (`agents_md.rs:94-98`).
2. Merge every config layer *except* `ConfigLayerSource::Project` and read
   `project_root_markers` from the merged value (`agents_md.rs:161-175`). Project-level
   config deliberately cannot influence root detection — upstream test
   `project_layers_do_not_override_project_root_markers`
   (`agents_md_tests.rs:1361`).
3. Walk up from cwd to the nearest ancestor containing any marker
   (`find_up.rs:27-47`). The probe uses `get_metadata` and accepts `Ok(_)` regardless of
   type, so a marker may be a file or a directory — which is why a `.git` *file* (a
   worktree or submodule gitlink) works as a root marker just as a `.git` directory does.
   Markers are probed for a given ancestor before moving to the next ancestor, so
   **nearest ancestor wins irrespective of marker list order**.
4. Build the directory list root -> cwd inclusive; if no root was found, the list is
   just cwd.
5. Per directory, first-match over `[AGENTS.override.md, AGENTS.md, ...fallbacks]`.
6. Concatenate in root -> cwd order, under a shared byte budget.

Confirmations by experiment, at several depths, git and non-git:

- Test 8 (non-git, depth 2): cwd only. **No walk.**
- Test 7 (git repo, cwd two levels below repo root): repo root + intermediate dir, in
  that order. **Walk, bounded at the repo root.**
- Tests 1/2 (git repo, cwd = repo root): only that repo's file; neither the group
  `AGENTS.md` nor the instance-root `AGENTS.md` loaded, because `.git` at the repo root
  is the boundary.
- Test 9' (`project_root_markers = [".niwa-instance"]`, cwd = git repo `outside/sub`
  with no such marker anywhere above): `(no AGENTS.md instructions block in prompt)` —
  no root found, cwd has no doc, and `outside/AGENTS.md` is *not* reached.
- Empty marker list disables traversal entirely (upstream test
  `empty_project_root_markers_only_probe_cwd_candidates`, `agents_md_tests.rs:952`).

**`project_root_markers` is the lever.** It is configurable, it is read from
`$CODEX_HOME/config.toml`, and it moves the boundary. Test 3', with
`project_root_markers = [".niwa-instance"]` and a `.niwa-instance` file at the instance
root, cwd = `.../inst/public/repoA`:

```
##### TEST 3': marker=.niwa-instance at inst root, cwd=repoA #####
# AGENTS.md instructions for /home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/inst/public/repoA

<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

INSTANCE ROOT AGENTS.md


GROUP public AGENTS.md


REPO-A niwa context (CLAUDE.local.md)

</INSTRUCTIONS>
```

That is the Claude Code layering — workspace root, group, repo — reproduced in Codex,
with the repo layer supplied by `CLAUDE.local.md` and nothing written into the repo's
tree. Test 13 confirms the upper layers still arrive for a repo with no context file of
its own (repoD, only a `.git`): instance root + group loaded, repo layer absent.

**The marker list replaces rather than extends the default, and the two goals conflict.**
`project_root_markers_from_config` returns the configured list verbatim
(`project_root_markers.rs:16-43`) and `agents_md.rs:168-175` uses
`default_project_root_markers()` only when the key is absent. Consequences, both
verified:

- Test 9' above: with `[".niwa-instance"]` alone, an ordinary git project elsewhere on
  the machine loses repo-root discovery entirely.
- Test 11, with `[".niwa-instance", ".git"]`, cwd = repoA inside the instance:

```
<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

REPO-A niwa context (CLAUDE.local.md)

</INSTRUCTIONS>
```

  Adding `.git` back restores outside repos (test 11b loads `outside/AGENTS.md`) but
  collapses the in-instance walk to repo-only, because `.git` at the repo root is the
  *nearer* ancestor. Marker list order does not change this — tests 10 and 10b, with
  `[".niwa", ".git"]` and `[".git", ".niwa"]`, produced identical repo-only output.

So the layered walk and default repo-root behavior are mutually exclusive **within one
config**. They are not mutually exclusive across configs: because
`project_root_markers` is read from `$CODEX_HOME/config.toml`, a per-instance
`CODEX_HOME` makes the setting apply only to sessions launched into that instance,
leaving the user's real `~/.codex` (and every project outside) on the stock `.git`
behavior.

### 3. Whether multiple project docs can load at once

**Yes, but only through the directory chain** — established above. Beyond that, I
searched the config surface systematically for any key that adds an instruction file by
absolute path or names extra search locations.

Method: dumped the serde field-name tables from the binary
(`grep -o "[a-z_]*_instructions\|instructions_file\|project_doc[a-z_]*\|project_root_markers"`
over a strings dump of the binary) and cross-read
`codex-rs/config/src/config_toml.rs` (140 `pub` fields). The instruction-related keys
that exist:

| Key | Source | What it does | Usable for extra per-repo docs? |
|---|---|---|---|
| `project_doc_fallback_filenames` | `config_toml.rs:290-291` | Extra *filenames* probed per directory, first-match | Yes — the mechanism this spike is about |
| `project_doc_max_bytes` | `config_toml.rs:286-287` | Shared byte budget | No |
| `project_root_markers` | `project_root_markers.rs:16` | Moves the walk boundary | Yes — the more powerful lever |
| `model_instructions_file` | `config_toml.rs:232-236` | Absolute path, but it **overrides the built-in model instructions**; the doc comment says "Users are STRONGLY DISCOURAGED from using this field, as deviating from the instructions sanctioned by Codex will likely degrade model performance" | No — replaces, does not add |
| `instructions` | `config_toml.rs:213-214` | Inline system-instructions string | No — replaces, not a path, not per-repo |
| `developer_instructions` | `config_toml.rs:216-218` | Inline string injected as a `developer` role message | Only as an additional *global* channel; inline TOML, no path, not per-repo |
| `experimental_realtime_start_instructions` | binary field table | Realtime/voice session bootstrap | Not applicable |

**There is no key that names an additional instruction file by absolute path, and no key
that names extra search locations.** The only paths Codex will read as project
instructions are `<dir>/{AGENTS.override.md, AGENTS.md, ...fallbacks}` for `dir` in the
root->cwd chain, plus `$CODEX_HOME/{AGENTS.override.md, AGENTS.md}`.

Note also that `$CODEX_HOME` itself takes an override file:
`codex-rs/codex-home/src/instructions/mod.rs:26-30` probes
`["AGENTS.override.md", "AGENTS.md"]` in the Codex home and returns the first non-empty
one. That confirms round-1's "global file" finding and adds the override name. That
loader uses plain `tokio::fs::read` with no size check anywhere in the function, which
is the mechanical reason the global file is exempt from `project_doc_max_bytes` —
consistent with round-1.

### 4. `project_doc_max_bytes`: actual default and truncation behavior

**Read directly from the implementation.** `codex-rs/config/src/config_toml.rs:67`:

```rust
pub const DEFAULT_PROJECT_DOC_MAX_BYTES: usize = 32 * 1024;
```

i.e. **32768 bytes**, wired in at `config_toml.rs:73-75` / `286-287`. This is consistent
with round-1's "exceeds 25,012 bytes" but pins it. `project_doc_fallback_filenames`
defaults to an empty vector (`config_toml.rs:78-80`).

Truncation behavior, from `read_agents_md` (`agents_md.rs:105-143`):

- The budget is a **single `remaining` counter shared across the whole chain**, not a
  per-file cap. It is decremented by each accepted doc (line 141).
- A doc larger than what remains is **truncated mid-content** at a byte boundary
  (`data.truncate(remaining as usize)`, line 120) — no ellipsis, no marker, no
  delimiter.
- Once `remaining` hits 0 the loop `break`s (lines 109-111) and **every later doc is
  dropped entirely**.
- The only signal is `tracing::warn!("project doc exceeds remaining budget; truncating")`
  (lines 123-129), which goes to the tracing subscriber, not the user.
- Truncation happens at a raw byte offset before `String::from_utf8_lossy` (line 131),
  so cutting mid-codepoint yields replacement characters rather than an error.

**Verified by experiment.** Test with `project_root_markers = [".niwa-instance"]` and a
chain of three docs: instance root ~30 KB, group ~4 KB, repo ~1 KB, each with unique
START/END sentinels, default byte cap:

```
--- STDERR ABOVE (if any) ---
ROOTSTART: PRESENT
ROOTEND: PRESENT
GROUPSTART: PRESENT
GROUPEND: MISSING
REPOSTART: MISSING
REPOEND: MISSING
project doc region bytes: 32771
tail repr: 'ggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg\n'
```

The group doc was cut mid-body and the repo doc vanished completely. **stderr was
empty**, and a re-run with `RUST_LOG=warn` produced no matching line on stderr either
(`grep -i "trunc\|budget\|exceed"` returned nothing). From the CLI's point of view the
loss is silent. Cutting at exactly the budget is confirmed by the trailing run of `g`
characters ending abruptly with no sentinel.

Raising the cap fixes it. Same tree with `project_doc_max_bytes = 400000`:

```
ROOTSTART: PRESENT
ROOTEND: PRESENT
GROUPSTART: PRESENT
GROUPEND: PRESENT
REPOSTART: PRESENT
REPOEND: PRESENT
project doc region bytes: 35064
```

And `project_doc_max_bytes = 0` disables project docs entirely, as the source says
(`agents_md.rs:96-98`) — verified: `(no AGENTS.md instructions block in prompt)` while
the global instructions still rendered.

**This is a live trap for niwa at default settings.** The round-1 measurement of a naive
flatten was ~80 KB, and even the "61%" composed form is ~49 KB. Both blow through 32768
bytes. Under the layered walk, the instance-root layer is read *first*, so an oversized
workspace doc would eat the budget and silently starve the per-repo layer that this
whole spike exists to deliver — the failure lands exactly where it hurts most.

### 5. The decisive end-to-end test

Synthetic instance tree, mirroring the real workspace shape (all under
`/home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/`):

```
home/AGENTS.md                            "global codex home instructions"   <- $CODEX_HOME
home/config.toml                          project_doc_fallback_filenames = ["CLAUDE.local.md"]
inst/AGENTS.md                            "INSTANCE ROOT AGENTS.md"
inst/public/AGENTS.md                     "GROUP public AGENTS.md"
inst/public/repoA/.git                    (gitlink file)
inst/public/repoA/CLAUDE.md               "REPO-A CLAUDE.md"
inst/public/repoA/CLAUDE.local.md         "REPO-A niwa context (CLAUDE.local.md)"
inst/public/repoB/.git                    (gitlink file)
inst/public/repoB/AGENTS.md               "REPO-B committed AGENTS.md"        <- the shirabe case
inst/public/repoB/CLAUDE.md               "REPO-B CLAUDE.md"
inst/public/repoB/CLAUDE.local.md         "REPO-B niwa context (CLAUDE.local.md)"
```

Command, run once per repo directory:

```
cd <repo> && CODEX_HOME=<L>/home /home/dgazineu/.tsuku/tools/current/codex debug prompt-input
```

**Result, cwd = repoA (`CLAUDE.local.md` only):**

```
# AGENTS.md instructions for /home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/inst/public/repoA

<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

REPO-A niwa context (CLAUDE.local.md)

</INSTRUCTIONS>
```

**Result, cwd = repoB (own `AGENTS.md` + `CLAUDE.md` + `CLAUDE.local.md`):**

```
# AGENTS.md instructions for /home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/inst/public/repoB

<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

REPO-B committed AGENTS.md

</INSTRUCTIONS>
```

Direct answers to the three questions posed:

- **Does the repo with both files get its own `AGENTS.md`, the `CLAUDE.local.md`, or
  both?** Its own `AGENTS.md`, and **only** that. The `CLAUDE.local.md` is silently
  dropped. There is no concatenation within a directory. This is the shirabe case, and
  the fallback mechanism alone **fails** it.
- **Does the repo with only `CLAUDE.local.md` get it?** Yes, cleanly.
- **Does the global file still load in both cases?** Yes, in both, and it is emitted
  first, separated by the `\n\n--- project-doc ---\n\n` marker
  (`AGENTS_MD_SEPARATOR`, `agents_md.rs:43`; emitted at `legacy_text`, lines 329-333).

Note what is *absent* from both outputs: `INSTANCE ROOT AGENTS.md` and
`GROUP public AGENTS.md`. With default markers, `.git` at each repo root stops the walk
and the upper layers never load. Under the fallback key alone, niwa gets the repo layer
(sometimes) and the global layer, and nothing in between.

**The variant that actually works.** Same tree, plus a `.niwa-instance` marker file at
the instance root and `project_root_markers = [".niwa-instance"]` in
`$CODEX_HOME/config.toml`. cwd = repoA (test 3', full output in section 2) yields
instance root + group + `CLAUDE.local.md`. cwd = repoB (test 4') yields:

```
# AGENTS.md instructions for /home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/inst/public/repoB

<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

INSTANCE ROOT AGENTS.md


GROUP public AGENTS.md


REPO-B committed AGENTS.md

</INSTRUCTIONS>
```

Upper layers now arrive, but repoB's niwa context is still lost — the collision is
per-directory and no marker configuration fixes it.

**The variant that also fixes the shirabe case.** Verified against a *real* git repo
(`git init`, committed `AGENTS.md`), using `AGENTS.override.md` — the one filename that
outranks a committed `AGENTS.md` — combined with `.git/info/exclude`, which lives inside
`.git/` and is therefore not part of the working tree:

```
$ printf 'SHIRABE niwa per-repo context (AGENTS.override.md)\n' > AGENTS.override.md
$ printf 'AGENTS.override.md\n' >> .git/info/exclude
$ git status --porcelain
$ echo "(end)"
(end)
$ cat AGENTS.md
SHIRABE committed AGENTS.md (must not be clobbered)
```

`git status` is clean and the committed file is untouched. Prompt:

```
# AGENTS.md instructions for /home/dgazineu/.claude/jobs/7838923c/tmp/lead_pdf/real/public/shirabe

<INSTRUCTIONS>
global codex home instructions

--- project-doc ---

INSTANCE ROOT AGENTS.md


GROUP public AGENTS.md


SHIRABE niwa per-repo context (AGENTS.override.md)

</INSTRUCTIONS>
```

The caveat is inherent to first-match: `AGENTS.override.md` **replaces** the repo's own
`AGENTS.md` rather than concatenating with it, so shirabe's committed guidance would be
lost unless niwa composes it in. Since niwa materializes this file anyway, that is a
mechanical fix — read the repo's committed `AGENTS.md` at materialization time and
prepend it to the override file it writes.

Relevant to feasibility: niwa already writes into `.git/info/exclude`
(`internal/worktree/worktree.go:225`, "Record niwa's ignore coverage in the repo's shared
.git/info/exclude"), and `internal/agent/agent.go:67-77` already models a per-agent
context filename ("CLAUDE.local.md for Claude (and the zero value), AGENTS.md for
Codex"). `CLAUDE.local.md` currently rides on repos' own `*.local*` gitignore patterns
(`internal/workspace/content_test.go:386`), which `AGENTS.override.md` would not match —
hence the explicit exclude entry.

### 6. The verdict

See Implications.

---

## Implications

**Can niwa rely on `project_doc_fallback_filenames` to deliver per-repo context? Partly,
and not on its own.** Used alone it is a *majority-case* mechanism with a silent
minority failure. It delivers the repo layer for every repo that has no committed
`AGENTS.md` — which is most of them — and silently delivers nothing for repos that do.
shirabe is a real instance of that failure today, and any repo can become one at any
time by committing an `AGENTS.md`, with no error, no warning, and no signal in the
prompt. Nothing about the repo's state tells niwa's user that their per-repo context
stopped loading. A design that silently loses per-repo context is worse than one that
never promised it, so **the fallback key alone is not a sufficient answer**.

**The far more valuable finding is `project_root_markers`.** The round-1 premise that
"Codex does not walk up" is false for git repos, and the walk boundary is configurable.
Dropping a marker file at the instance root and setting
`project_root_markers = ["<marker>"]` in `$CODEX_HOME/config.toml` reproduces Claude
Code's layered discovery — instance root, then group, then repo, concatenated in that
order — with every file staying exactly where niwa already puts it. This is the finding
that changes the design.

**Consequently, yes: the composed global `AGENTS.md` can stay thin.** It no longer has
to carry the workspace and group layers at all, because those load from the tree via the
walk. Its remaining job is whatever must be true regardless of cwd — a repo index, the
cross-repo orientation, agent-invariant conventions. The ~80 KB flatten and the ~49 KB
composed form both stop being necessary. Given the 32768-byte default cap and its
silent, chain-shared truncation, keeping it thin is not merely nicer — it is the
difference between working and quietly not working.

**Recommended configuration**, all of it verified end-to-end above:

1. Write a marker file at the instance root and set
   `project_root_markers = ["<marker>"]` in the instance's `$CODEX_HOME/config.toml`.
   Pick a name unique to niwa — see Surprises for why `.niwa` specifically is a bad
   choice.
2. Set `project_doc_fallback_filenames = ["CLAUDE.local.md"]` so repos with no committed
   `AGENTS.md` pick up the file niwa already writes, at zero extra materialization cost.
3. For repos that *do* have a committed `AGENTS.md`, write `AGENTS.override.md` into the
   repo directory with the repo's own committed `AGENTS.md` content prepended to niwa's
   context, and register `AGENTS.override.md` in that repo's `.git/info/exclude`. The
   working tree stays clean and the committed file is never touched. niwa already has
   both the exclude-writing code and the per-agent-filename abstraction.
4. Raise `project_doc_max_bytes` explicitly and generously (the tests used 400000). Do
   not run on the 32768 default: the instance-root layer is read first, so an oversized
   workspace doc starves exactly the per-repo layer this design exists to deliver.
5. Never let niwa write a zero-byte or whitespace-only context file. An empty file
   claims the directory's single slot and suppresses every remaining candidate. If there
   is nothing to say for a directory, write no file at all.

**Requires a per-instance `CODEX_HOME`.** `project_root_markers` replaces the default
`.git` rather than extending it, and adding `.git` back collapses the in-instance walk
because the repo root is the nearer ancestor — verified both orderings, both collapse.
So the setting is safe only if it is scoped to the instance. Since it is read from
`$CODEX_HOME/config.toml` and niwa is already planning a per-instance `CODEX_HOME`, that
falls out for free — but it becomes a hard dependency rather than a convenience, and the
design should say so. If a shared `CODEX_HOME` is ever used, this configuration will
break every ordinary git project on the machine.

**Version fragility: moderate, worth stating plainly.** Every mechanism relied on here
is a documented, tested config key with upstream test coverage
(`agents_md_tests.rs:952, 1331, 1361, 1490, 1511`), not an accident of implementation.
The risk is not that the keys vanish; it is that the *precedence table* in
`candidate_filenames` is hardcoded (`agents_md.rs:230-244`) and unconfigurable, so any
upstream reordering silently changes which file wins in the collision case. That risk is
concentrated entirely on step 3 of the recommendation. A cheap guard: have niwa assert
its expected discovery at materialization time by running `codex debug prompt-input` in
one repo and checking that niwa's own sentinel appears — this spike's entire method,
reduced to a smoke test.

## Surprises

1. **`AGENTS.override.md` exists and outranks a committed `AGENTS.md`.** Round-1 never
   surfaced it. It is the only lever that resolves the collision case, and it is
   hardcoded at the top of the precedence list rather than being configuration.

2. **The "no upward walk" finding was an artifact of testing outside a git repo.** The
   walk is the documented, tested default behavior; the cwd-only case is the degenerate
   branch that fires when no marker is found. Reproduced both branches side by side
   (tests 7 and 8). This inverts the premise the composition strategy was built on.

3. **`/home/dgazineu/.niwa` exists on this host and silently hijacked an experiment.**
   Test 9 was run with `project_root_markers = [".niwa"]` against a git repo in a scratch
   directory that had no `.niwa` anywhere nearby — and it still picked up an ancestor's
   `AGENTS.md`. The walk had climbed to `/home/dgazineu`, matched the user's real niwa
   config directory, and treated the entire home directory as the project root.
   Re-running with a unique `.niwa-instance` marker gave the expected empty result. Two
   lessons: the marker name must not collide with anything that can appear high in a
   user's tree (so **not** `.niwa`), and a badly-chosen marker fails by silently
   widening the walk enormously rather than by erroring.

4. **The byte budget is shared across the entire chain and drains root-first.** I
   expected a per-file cap. It is one counter, spent in root-to-cwd order, so the layer
   niwa cares most about (the repo, read last) is the first casualty. Truncation is a
   raw byte cut with no marker in the text and no message on stderr even at
   `RUST_LOG=warn`.

5. **Putting `AGENTS.md` into `project_doc_fallback_filenames` is a silent no-op** — the
   dedup check drops it because it is already in the hardcoded list. A plausible attempt
   to reorder precedence that fails quietly.

6. **Project-layer config is deliberately excluded from root-marker resolution**
   (`agents_md.rs:161-166`, upstream test at `agents_md_tests.rs:1361`). A repo cannot
   redefine the project root for AGENTS.md discovery via its own `.codex/config.toml`.
   This is a small security-adjacent hardening detail and it also means the sibling
   spike's project-config layer cannot be used to solve this problem.

## Open Questions

- **Worktrees.** niwa manages git worktrees (`internal/worktree/worktree.go`), where
  `.git` is a *file* rather than a directory. `find_up.rs` accepts either
  (`Ok(_) => Ok(Some(ancestor))`, no type check), and the synthetic repos here used
  gitlink-style `.git` files, so the mechanism should hold. But I did not test against a
  real `git worktree add` tree, nor against a worktree located outside the instance root
  (where the instance marker would not be an ancestor at all — likely a real gap, since
  the walk is purely path-based and knows nothing about the repo the worktree belongs
  to). Exercising it needs a real worktree under a real instance; a sibling spike owns
  worktrees and should be asked directly.

- **Multi-environment budget accounting.** Upstream tests
  `project_doc_byte_limit_is_applied_independently_per_environment`
  (`agents_md_tests.rs:1178`) and
  `multiple_environments_can_exceed_single_environment_project_doc_limit`
  (`agents_md_tests.rs:1205`) show the cap is per turn-environment, and
  `load_project_instructions` (`agents_md.rs:58-77`) loops over
  `environments.turn_environments()`. Every test here ran in the single-environment
  case. If niwa ever binds more than one environment per session, the output switches to
  the `environment_labeled_text` layout (`agents_md.rs:343-386`) with per-environment
  headers, which I did not exercise. Doing so needs a multi-environment session setup.

- **Whether the truncation warning is reachable at all from the CLI.** I confirmed it
  does not reach stderr at default or `RUST_LOG=warn`, and found no log directory in the
  isolated `CODEX_HOME`. Whether it lands in a file under a configured `log_dir` was not
  determined; it would take setting `log_dir` and re-running the oversize case. Either
  way it is not something a user would notice, so the practical conclusion (silent) is
  unaffected.

- **Behavior across Codex versions.** Everything here is pinned to
  `rust-v0.147.0`, matched to the installed binary and confirmed by experiment against
  that binary. I did not check whether `AGENTS.override.md` or `project_root_markers`
  exist in older or newer releases, so niwa's minimum-supported-Codex claim is
  unestablished. Checking would mean diffing `agents_md.rs` across tags.

## Summary

Codex does walk up — from the nearest ancestor holding a `project_root_markers` entry
(default `.git`) down to cwd, taking at most one file per directory by strict first-match
over `[AGENTS.override.md, AGENTS.md, ...fallbacks]` — so `project_doc_fallback_filenames`
alone delivers `CLAUDE.local.md` only for repos that have not committed their own
`AGENTS.md`, and silently delivers nothing for ones that have, such as shirabe. Setting
`project_root_markers` to a niwa marker at the instance root reproduces Claude Code's
full instance/group/repo layering with no file written into any repo tree, which means
the composed global `AGENTS.md` can stay thin instead of inlining every repo — and the
one remaining gap, repos with a committed `AGENTS.md`, closes by writing
`AGENTS.override.md` (which outranks it) plus a `.git/info/exclude` entry, verified to
leave `git status` clean. The biggest open question is worktrees: the walk is purely
path-based, so a worktree living outside the instance root would fall outside the marker
entirely, and that case was not exercised.
