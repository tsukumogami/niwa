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

## Round 2 (in flight)

Two round-1 discoveries contradicted premises the brief treated as settled, and
both can change the design:

1. **Codex does have a project-level config layer.** The brief states flatly
   that a `.codex/config.toml` in a working directory is "ignored entirely". The
   hook-trust spike found a project layer in the upstream loader, with its own
   denylist, and fired a hook from a project-local `hooks.json`. If that layer
   loads from an instance root, niwa could write config into the instance the
   way it already writes Claude's settings — and the fragile environment-variable
   delivery problem disappears. The caveats are real (the test directory was not
   a git repo, and a trust precondition was not established), so this needs
   settling before the design hardens.

2. **`project_doc_fallback_filenames` may deliver per-repo context** without
   writing into a repo's git tree, by pointing Codex at the git-ignored
   `CLAUDE.local.md` niwa already writes. Whether it is first-match or
   concatenating is unverified, and that determines its behavior for the one
   repo that ships its own `AGENTS.md`.

A re-run of the config-materialization lead is also in flight, covering the
minimal file set for marketplaces, plugins, skills, MCP servers, and project
trust.
