# Explore Findings: dual-agent-workspace

Accumulated across rounds. Round 1 ran eight leads; seven landed and one was
re-run after an unrelated interruption. Round 2 chases two round-1 discoveries
that contradicted the exploration's starting premises.

All Codex behavior below was measured against codex-cli 0.147.0. Several leads
read the matching upstream Rust source (tag `rust-v0.147.0`, whose crate layout
matches the shipped binary's retained panic paths) rather than reasoning from a
stripped binary, then verified by experiment.

## Round 1

### Closed: hook trust (open question 1)

**niwa can pre-write a `trusted_hash` that Codex accepts with no prompt.** This
was verified end to end: a `SessionStart` hook wrote a marker file during a real
`codex exec` session driven only by hand-written files, with no interactive step
and no TTY.

The hash is `sha256:` plus the hex SHA-256 of a key-sorted, compact JSON
rendering of a TOML-normalized hook identity. The normalization matters and is
fully characterized: `timeout` is always materialized (default 600, but clamped
to 1..3 for `SessionEnd`), `command_windows` is forced absent, `async` is always
present, `matcher` is forced absent for `UserPromptSubmit` and `Stop`, and the
command is hashed before environment substitution. The lead reimplemented the
algorithm independently and reproduced **all 13 hashes** the shipped binary
itself had written for a real installed plugin. Reproducing it in Go is a small
amount of code, and Go's `json.Marshal` already sorts map keys, which is exactly
the canonicalization Codex wants.

**Hooks must not be plugin-delivered.** This inverts the assumption the brief
carried. `plugin_hooks` is at feature stage `removed` with effective state
`false` in 0.147.0: a plugin-supplied `hooks.json` never fires, even with a
correct hash and even with trust bypassed entirely. The `[hooks.state]` entries
for plugins sitting in a real host config are fossils from an earlier version.
The delivery that works is a loose `hooks.json` (in the Codex home or inline in
`config.toml`) — which is what the earlier probe had tested and misread: nothing
auto-populates `[hooks.state]` for any delivery path, because that entry is the
input, not the output.

This simplifies the design rather than complicating it: hook injection is
decoupled from the plugin machinery entirely, so it cannot break because a
marketplace add failed or a plugin was disabled.

Two consequences to carry into the design. First, the state key embeds the
**absolute path** of the file declaring the hook, so the config is not
relocatable and must be regenerated from the instance's real path on every
apply, never templated once. Second, and more seriously, a wrong or stale hash
**degrades in complete silence** — exit 0, no warning, no diagnostic, no mention
of hooks anywhere in the session output. Anything built on a Codex `SessionStart`
hook needs its own liveness signal rather than an assumption that the hook ran.

An escape hatch exists (`--dangerously-bypass-hook-trust`) but is per-invocation
only, warns loudly twice, and disables review for every hook in the session. The
computed hash is strictly better and costs nothing.

### Closed: whether niwa should export `OPENAI_API_KEY` (open question 3)

**No — and the reason is the opposite of the one the brief anticipated.**

The `codex doctor` warning about "mixed auth signals" describes only doctor's
own reachability probe. A live session with both a stored subscription login and
the key present in the environment uses the **login**: it dials the
subscription endpoint and never contacts the metered API host. So an exported
key does not divert billing.

The problem is the other state. When the login is missing or broken, the key
becomes a **silent** fallback: the session dials the metered endpoint and doctor
reports a green check. Since the per-instance layout deliberately shares the
auth file with the host home, a broken share is the single most likely way this
design fails — and it is exactly the condition under which an exported key does
damage. Without the key, that failure is loud and the developer fixes it. With
it, the same breakage silently bills metered credits.

Pinning the auth mode does not rescue the option: `forced_login_method =
"chatgpt"` does **not** fail closed (with no login it still used the metered
key), and its sibling value `api` triggers an implicit logout that **deletes the
auth file**. That key is a hazard to have anywhere near a config generator, and
niwa must never emit it.

Related defect found in passing: the scaffold and the vault guide both document
a secret binding that no code implements. The `[claude.env.secrets]` table has
no materializer — this is already tracked as niwa#228 — and it is wrong on a
second axis nobody had noted: its destination is a settings file Codex never
reads. Fixing the prose is in scope as a small correction; building the
materializer is not.

### Closed: the Codex home share/isolate matrix (open question 4)

**Symlinks are sufficient, and the feared hazard does not materialize.** Codex
resolves symlinks before writing — for the auth file, which it writes in place,
and for `config.toml`, which genuinely does use temp-and-rename. Two real token
refreshes were driven against a synthetic credential with a redirected refresh
endpoint: the target inode was unchanged and the symlink intact both times. Hard
links, by contrast, must not be used: a rename-written file would silently
diverge.

The share list is short: the auth file, `installation_id`, `plugins/cache`, the
content-addressed `cache/` (18 MB, and the largest entry in a real home), and
optionally `models_cache.json`. Isolating `cache/` would cost a developer with
ten instances 180 MB and ten redundant downloads for no benefit.

Two entries need explicit statements rather than defaults. `rules/` **must be
isolated**: it looks like a cache but holds persisted command-approval decisions
containing relative paths that mean different things in different instances,
making it the most security-relevant entry in the home. And `skills/` is
**co-owned** — Codex materializes its own `skills/.system/`, and a bundled skill
hard-codes an absolute path inside it — so niwa must write there additively and
must never clear the directory.

The scope of what niwa owns turns out to be much smaller than the brief implied:
of roughly 25 entries in a real home, niwa creates four, and Codex lazily
creates the rest. `installation_id` is not merely telemetry — it rides on every
API request and is the identity for remote-control enrolment — which is a firmer
reason to share it than tidiness. A `sqlite_home` key exists and cleanly
relocates all six databases in one line, verified under concurrent access; it is
not needed for correctness but is a lever if instance size becomes a problem
(one log database reached 15 MB in three hours).

The residual gap: no real interactive session was ever run, so every
session-created artifact was reasoned about from copies and schemas rather than
observed. Closing that properly needs a second, disposable login, because
refreshing a copied token would invalidate the host's live one server-side.

### Closed: worktrees (open question 5)

**A worktree should get its own Codex home.**

Worktrees live inside the instance tree, at `<instanceRoot>/.niwa/worktrees/`.
The precedent is unambiguous and already settled for Claude: when niwa hit the
same problem — a launched root that does not inherit ancestor config — its
answer was to freshly materialize a full copy from the same config source, plus
a small worktree-specific addendum naming the repo, purpose, and branch. It
never points a worktree at one shared file.

For Codex the argument is stronger, not weaker. A single shared composed
instruction file **cannot** carry N concurrent worktrees' purpose/branch context
at once; two worktrees would clobber each other's addendum. Writing the addendum
into the worktree's own directory instead would reopen precisely the collision
risk the design already closed, since a worktree checks out a real branch and
can carry a committed `AGENTS.md` exactly like a clone can.

There is no existing Codex-worktree behavior to preserve: under today's
exclusive mode a worktree gets nothing repo-level at all, so this is new ground
with no contract to break. The cost is bounded — a materialization step
alongside the existing worktree content install, a re-sync path, and delivery at
the two commands where niwa already launches or hands off an agent for a
worktree.

Found in passing, and independent of this feature: the shell wrapper's
directory-change dispatch has **no `worktree)` arm**. Only the deprecated
`session create` spelling triggers it, despite `worktree` being the canonical
verb and the guide documenting the behavior under that heading. This looks like
a leftover from a pre-rename design and is a real pre-existing bug.

### Reframed: how `CODEX_HOME` reaches the developer (open question 2)

The brief assumed niwa's shell wrapper fires on entering an instance directory,
making it the natural home for an export. **It does not.** It is a shell
function that intercepts exactly five `niwa` subcommands and cd's the shell on
their behalf. There is no `chpwd`, no `PROMPT_COMMAND` polling, no direnv-style
mechanism anywhere in it. A developer who manually cd's into an instance — the
normal way anyone navigates after the first create — gets no reaction at all.

So the coverage gap is structural, not an edge case. It misses a manually-cd'd
shell, a terminal opened deep inside a cloned repo, any non-interactive
invocation (scripts, Makefiles, CI, non-interactive SSH), an unsupported shell
(only bash and zsh are detected), and any developer who never installed the
integration. In every one of those cases `codex` silently runs against the
host's default home — no error, no warning, just the wrong context.

There is no CLI escape hatch: the environment variable is the only lever. `-c`
overrides values inside an already-loaded config and `-p/--profile` layers onto
the home but cannot relocate it. That leaves three mechanisms — extending the
shell wrapper, a launcher subcommand, or a PATH-shadowing shim — none of which
is clean, which is what motivated the round-2 project-config lead below.

### Confirmed: the deferred mechanisms can be dropped (brief's request)

The reasoning holds. `internal/gitexclude` manages exactly `*.local*` and
`.niwa/`, which suffices only because every niwa-authored repo-level file
carries a `.local` infix by construction. No code path writes `AGENTS.md` into a
repository working tree: the single gate returns early for Codex at both
repo-level write sites, and the accessor that would return the Codex filename
there has **zero non-test callers**. A functional scenario asserts the absence
directly.

So both deferred mechanisms — a git-exclude extension and a collision guard —
are genuinely unnecessary **as long as repository-level Codex writes stay out of
scope**, which the Codex-home direction guarantees. The design should say this
explicitly rather than dropping them silently.

It is worth stating sharply that the collision risk was never academic:
`public/shirabe/AGENTS.md` is a real, committed file living beside that repo's
own `CLAUDE.md`. It would be destroyed on day one by any increment that started
writing `AGENTS.md` into clones.

### Inventoried: what shipped, and the blast radius

The prior increment is a leaf `internal/agent` package with a closed `Agent`
type, filename accessors, and a precedence resolver, threaded through **one
scalar `Agent` field per preparation**. The exclusivity is not in the accessors
— those are fine and can be called for either agent independently — it is in the
callers, each of which assumes it runs once with one resolved agent. Going
additive means those sites run once per materialized agent, leaving `Agent`
alive as a pure launch-time selector.

The settings builder is Claude-Code-shaped end to end; its JSON keys are Claude
API surface, not portable concepts. A Codex `config.toml` writer is net-new
work, not a generalization of it.

The env and secrets pipeline has no Codex output path at all today.

Three functional scenarios assert exclusivity directly, and two of their
assertions invert under additive materialization. Several unit tests assert "and
the other file does not exist". One existing test already checks that the two
context trees may coexist, which is partial groundwork.

The dispatch refusal most likely needs **no logic change** — it is keyed on the
launch-time selection, which stays coherent — but its doc comments narrate the
old framing and would read as stale. Background dispatch for Codex stays out of
scope, so the refusal itself remains.

**Backward compatibility is a non-issue in practice.** `default_agent` has zero
live adopters: it is set nowhere in the reference config repo, nowhere in the
private overlay, and nowhere in the workspace config. Nobody's on-disk output
changes.

### Composition: what the single instruction file should carry

Nothing in niwa composes context into one file today; the prior increment only
renamed per-directory files and explicitly declined to inline the `@import`-only
companion layers. So this is new work.

The measured shape: the workspace, instance, and group layers plus the companion
files total roughly 4,400 tokens — cheap enough that no selectivity argument
applies, and they should be inlined verbatim with headers naming their source.
Full per-repo bodies are the opposite case: about 61% of a naive flatten (~80KB,
~20K tokens), and irrelevant on every turn except the one repo the developer is
actually in. Those should be a short index instead.

One property makes eager global composition safe: the global instruction file is
**exempt** from the size cap that applies to a per-directory project doc. That
is a strong signal from Codex's own design about where a large composed file
belongs.

Because composition happens at materialization time, the file goes stale when a
cloned repo's own context changes. niwa has no live-refresh mechanism, so the
composed file should carry a one-line note saying what produced it and when.

### Materialization is pure file-writing, and trust is mandatory

A hand-written `config.toml` alone does nothing for plugins: Codex never
populates its own plugin cache, and it skips an uncached plugin in total
silence. But the cache it wants is just a recursive copy of the plugin tree
under `plugins/cache/<marketplace>/<plugin>/<version>/` with a one-key
`plugin.json`. So materialization stays pure file-writing — no CLI invocation,
no interactive step — and it reads nothing from the network at session start,
verified even with a GitHub-sourced marketplace whose host does not resolve.
One rule to encode: the version directory must be a real directory; everything
below it may be a symlink, and a symlinked version directory is silently
skipped.

The `[marketplaces.*]` block turns out not to be load-bearing at all. Deleting
it left every skill in the prompt; it buys `codex plugin list` and `upgrade`
and nothing else. niwa's existing marketplace model maps onto Codex's two
source types mechanically, names and all.

**Project trust is as session-blocking as hook trust and must be pre-written**:
one `[projects."<abs path>"] trust_level` entry per directory a session may
start in. Absence drops the session to a read-only sandbox where the agent
cannot write files. Curiously, the recorded *value* does not matter — both
`"trusted"` and `"untrusted"` yield a writable sandbox, and only a missing
entry degrades. An explicit distrust is quieter than saying nothing.

One capability does not survive the port: a plugin-supplied MCP server invoked
via `${CLAUDE_PLUGIN_ROOT}/...` never starts, and fails silently while
`codex mcp list` reports it as enabled. Neither plugin this workspace actually
uses ships an MCP server or a `hooks.json`, so this is a latent trap rather
than a present one.

## Round 2 — two premises overturned

### Codex does have a project-level config layer

The brief states flatly that a `.codex/config.toml` in a working directory is
"ignored entirely". That is wrong. There is a real project layer, discovered by
walking up from cwd, and it carries **instruction context, skills, config, MCP
servers, and hooks**. It cannot carry marketplaces or plugins — those are
filtered out separately, not via the documented denylist, so reading the
denylist alone would give exactly the wrong answer.

Two details shape everything downstream. First, the walk stops at the nearest
directory holding a `project_root_markers` entry, and that defaults to `.git` —
so an instance-root payload is invisible from inside a clone unless the marker
is repointed, and repointing **replaces** `.git` rather than extending it.
Second, **trust is checked twice by two rules that disagree**: config-layer
loading falls back through the project root, while the interactive gate consults
only cwd and the git repo root. A configuration that loads config correctly can
still block on a trust prompt.

The most useful discovery here: **skills load even from disabled, untrusted
layers**, deliberately. That makes a loose skill directory the lowest-friction
delivery channel Codex offers.

### Codex does walk up for context, and the walk is configurable

The round-1 "no upward walk" finding was an artifact of testing outside a git
repository. The walk is the documented default; cwd-only is the degenerate
branch when no marker is found. Discovery takes at most one file per directory
by strict first-match over `AGENTS.override.md`, then `AGENTS.md`, then anything
listed in `project_doc_fallback_filenames`, concatenated root-to-cwd.

`AGENTS.override.md` outranks a repository's own committed `AGENTS.md`, which is
the only lever that resolves the collision case — and it resolves it without
writing a file the repo would notice.

Three traps, each silent. The byte budget (`project_doc_max_bytes`, default
32768) is **shared across the whole chain and drains root-first**, so an
oversized upper layer starves exactly the per-repo layer this design exists to
deliver; truncation is a raw byte cut with no marker and nothing on stderr. An
empty or whitespace-only file claims its directory's single slot and suppresses
every remaining candidate. And putting `AGENTS.md` into the fallback list is a
silent no-op, because a dedup check drops it.

## Round 3 — the architecture decision

Two candidate architectures fell out of round 2, and one question decided
between them.

**Architecture A** puts the payload at the instance root and repoints
`project_root_markers` at a niwa marker so the walk climbs past repo roots. It
works, but the cost is global: `.git` stops being a project-root marker
**machine-wide**, so every repository outside any niwa instance loses discovery
of its own `.codex/` and `AGENTS.md` from subdirectories. niwa would be reaching
outside its sandbox to degrade unrelated work. A badly chosen marker name fails
even worse — `.niwa` silently hijacked an experiment by matching the user's real
config directory and treating the entire home directory as the project root.

**Architecture B** writes the payload per cloned repo, found by the default
`.git` marker with no global change at all.

**The verdict is B, with `<repo>/.codex` symlinked to a single
`<instance>/.codex`.** The symlink works: config, skills, instruction context,
and hooks all load, from the repo root and from directories several levels
below. And `project_doc_max_bytes` — the finding most likely to sink B — can be
raised from the payload's own config rather than the user's.

A's only remaining advantage was collapsing hook-state entries, and it does not
even save the trust entries, since the interactive gate keys on the git repo
root either way. B needs no `CODEX_HOME`, no `project_root_markers`, and no
denylisted keys. What niwa writes into the developer's personal config reduces
to one trust line per cloned repo, plus one hook-state entry per repo per
handler if niwa ships hooks — all scoped to paths inside the instance, all
removable when the instance is reaped.

Three details the design must get right, each a silent failure if missed:

- The git-exclude pattern must be the bare `.codex`, **not** `.codex/`. niwa's
  existing idiom for `.niwa/` uses the trailing-slash form, and copying it here
  would leave permanent dirt in every repo's `git status`.
- The per-repo context file must be `AGENTS.override.md`, written
  unconditionally, inlining whatever `AGENTS.md` the repo already commits. Using
  the `CLAUDE.local.md` fallback instead would work for most repos and silently
  deliver nothing for any repo that ships its own `AGENTS.md`.
- That file must carry instance + group + repo content composed together,
  because the walk stops at the repo root and the upper layers are never
  visited.

This partially reopens something round 1 had closed. niwa now does write a file
into each repository's working tree. `CLAUDE.local.md` would have needed no
exclude work (it already matches the managed `*.local*` pattern), but
`AGENTS.override.md` and `.codex` both do — so the git-exclude extension the
brief expected to drop is needed after all, for different files and for a
different reason than the collision guard, which remains unnecessary because
`AGENTS.override.md` never overwrites a committed file.

## Round 4 (in flight)

One tension remains between round 1 and round 3. The config-materialization lead
argued the plugin-cache route dominates loose skills, because loose skills lose
namespacing and every `${CLAUDE_PLUGIN_ROOT}` reference — but the plugin cache
lives in a Codex home, which is exactly what the chosen architecture avoids
needing.

Measuring the real content reframes it: neither plugin this workspace uses ships
a `hooks.json` or an `.mcp.json`, so two of the three named losses are
hypothetical. What remains is namespacing and `${CLAUDE_PLUGIN_ROOT}` (370
occurrences across 90 files in one plugin, 62 across 33 in the other). And the
same lead separately observed that the variable was **not** expanded even via
the cache route, leaving "where is it honored" as an open question — so if the
answer is "nowhere", both routes lose it equally and the argument collapses to
namespacing alone. The final lead is settling that.
