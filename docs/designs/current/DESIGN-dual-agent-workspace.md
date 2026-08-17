---
schema: design/v1
status: Proposed
problem: |
  niwa prepares a workspace instance for exactly one coding agent, chosen at
  creation time through an exclusive selector: preparing for Codex replaces the
  Claude context at the niwa-owned levels and skips the repository and worktree
  levels entirely, so the Codex side of the switch delivers less than the
  Claude side. Delivering full parity is blocked by three measured properties
  of codex-cli 0.147.0: context discovery walks only from the nearest
  project-root marker (default `.git`) down to the working directory, so
  instance- and group-level content never reaches a session inside a cloned
  repository; a repository directory is a read-only sandbox until the
  developer's own Codex config carries a trust entry for it; and each
  directory contributes at most one context file by strict first-match, so a
  repository's committed `AGENTS.md` silently displaces anything niwa writes
  under a lower-precedence name.
decision: |
  Materialize for both agents unconditionally, leaving the Claude tree
  byte-for-byte unchanged. The Codex side is a payload directory at the
  instance root (`.codex/` holding a project config and the workspace's
  plugins symlinked whole into its skills directory), reached from each cloned
  repository and worktree through a `.codex` symlink that Codex's default
  `.git` marker discovers; a composed `AGENTS.override.md` per repository
  carrying the instance, group, and repository layers and inlining any
  committed `AGENTS.md`; a context budget declared in the payload's own
  config sized to the composed chain; one path-scoped `[projects.*]` trust
  entry per cloned repository in the developer's Codex config, and nothing
  else there — no hooks, no API key, no global keys, no marker changes. The
  existing agent discriminator survives as a launch-time selector; the
  materialization callers that treated it as exclusive run once per agent
  instead.
rationale: |
  The per-repository symlink under the default marker is the only discovery
  route that works from every directory with no environment preparation
  (niwa's shell integration cannot deliver an environment variable to a
  manually opened shell or a script) and no damage outside niwa instances
  (repointing `project_root_markers` replaces the default machine-wide, and
  a per-instance `CODEX_HOME` needs the variable the shell cannot deliver).
  `AGENTS.override.md` is the only filename that wins first-match discovery
  in every repository; the fallback-filename route works for most
  repositories and silently delivers nothing in any repository shipping its
  own `AGENTS.md`. Plugins ship whole and verbatim because skill namespacing
  derives from the nearest plugin manifest on disk — which Codex follows
  through symlinks — and `${CLAUDE_PLUGIN_ROOT}` is never expanded textually
  on any route, so rewriting buys nothing and corrupts prose. Trust entries
  are mandatory because their absence drops the session to a read-only
  sandbox; hooks and the API key are excluded because each converts a quiet
  failure into a blocking startup modal or a silently metered bill.
upstream: docs/prds/PRD-dual-agent-workspace.md
user_visible_surface: true
---

# DESIGN: dual-agent workspace

## Status

Proposed

Upstream PRD: docs/prds/PRD-dual-agent-workspace.md (Accepted). This design
partially reverses docs/designs/current/DESIGN-interactive-codex-session.md,
the prior increment: it keeps that design's `Agent` type and launch-time
resolution, but replaces its exclusive-materialization model (one prepared
agent per instance), delivers the repository and worktree levels that design
deferred — by a different mechanism than the one it anticipated — and
withdraws its claim that `OPENAI_API_KEY` binding needs no new code. Each
reversal is called out where it lands.

## Context and Problem Statement

The prior increment made OpenAI Codex a selectable alternative to Claude
Code, built as an exclusive switch. A workspace declares `default_agent`, a
single resolved `Agent` value threads through every materialization entry
point, and each write site emits for that one agent: `AGENTS.md` instead of
`CLAUDE.md` at the niwa-owned levels, and nothing at all at the repository
and worktree levels (`WritesRepoLevelContext()` returns false for Codex, and
both repo-level installers return early on it). The upstream PRD states why
that exclusivity now costs too much and what parity requires; this design
cites its requirements (R1–R14) rather than restating them.

Delivering the Codex side is not a filename problem. It is shaped by how
codex-cli 0.147.0 actually discovers configuration and context, established
by live measurement against that binary and by reading the matching upstream
source at tag `rust-v0.147.0`:

1. **Discovery is a bounded downward walk.** Codex locates a project root by
   walking up from the working directory to the nearest ancestor containing a
   `project_root_markers` entry (default: `.git`), then reads context and
   project config from the root down to the working directory. It never walks
   above the root. Every niwa-cloned repository has a `.git` at its root, so
   under defaults the walk starts and stops there: instance- and group-level
   content above the repository is never visited.

2. **One file per directory, strict first-match.** Each directory in the
   walk contributes at most one context file, chosen by hardcoded precedence:
   `AGENTS.override.md`, then `AGENTS.md`, then any configured fallback
   filenames, in that order. The fallback list cannot reorder this. An empty
   or whitespace-only file counts as a match — it claims the directory's slot
   and suppresses every remaining candidate.

3. **The context budget is shared and drains outermost-first.**
   `project_doc_max_bytes` defaults to 32768 bytes, is one counter spent
   across the whole chain in root-to-cwd order, and truncation is a raw byte
   cut with no marker in the text and nothing on stderr.

4. **Writing files is gated on trust.** A directory without a
   `[projects."<path>"]` entry in the developer's Codex config is a
   read-only sandbox, and the interactive TUI blocks on a trust prompt.
   Trust cannot be granted from inside the project layer — a project config
   vouching for itself is ignored by construction.

5. **The project config layer is real and carries almost everything.** A
   `.codex/` directory discovered in the walk delivers instruction context,
   skills, general config (including the byte budget), and MCP servers.
   Skills load even from an untrusted layer. What it cannot carry: trust,
   marker configuration, hook trust state, marketplaces/plugins, and eleven
   denylisted keys (provider URLs, `notify`, profiles, and the like).

The design problem is therefore: place a Codex-readable payload where this
walk finds it from any directory (R4, R5), win the first-match rule in every
repository including ones shipping their own `AGENTS.md` (R6), keep the
budget from silently starving the innermost layer (R7), deliver the
workspace's skills unmodified (R8), pre-write exactly enough trust that
sessions can act and start clean (R9, R10) — all while keeping repositories
git-clean (R11, R12), the developer's Codex setup theirs (R13), and the
Claude side byte-identical (R2).

## Decision Drivers

- **No environment preparation, ever (R5).** The mechanism must work from a
  manually opened terminal, a shell spawned deep inside a repository, a
  script, a Makefile, and non-interactive SSH. niwa's shell integration
  intercepts five `niwa` subcommands and never reacts to a plain `cd`, so
  any design that needs an exported environment variable fails in exactly
  the sessions R5 names.
- **Nothing niwa does may degrade Codex outside its instances (R13).**
  Scoped, additive entries confined to instance paths are acceptable;
  anything that changes discovery or behavior for unrelated repositories on
  the machine is not.
- **The Claude side is an invariant, not a target (R2).** Every existing
  output must be byte-for-byte unchanged; the change adds a second reader.
- **Hostility to silent failure.** The measured failure modes — first-match
  displacement, budget truncation, missing trust under `exec` — are all
  silent. A mechanism that works in the majority case and delivers nothing
  in a minority case, with no error, is worse than one that never promised
  delivery. This driver decides more of the design than any other.
- **Repositories stay clean and unclobbered (R11, R12).** Nothing untracked
  in `git status`, nothing a repository ships overwritten.
- **Verbatim delivery (R8).** Skills must arrive with the same content
  Claude sees. niwa should not become a content transformer: every rewrite
  is a fidelity risk and a maintenance obligation.
- **Sessions must act immediately and start clean (R9, R10).** A read-only
  sandbox or a blocking startup modal each fails the feature's core promise.

## Considered Options

### Decision 1 — Where the Codex payload lives and how sessions discover it

The payload is the `.codex/` directory carrying project config and skills.
Codex must find it from any working directory inside the instance.

- **Option 1A (chosen): payload at the instance root, one `.codex` symlink
  per cloned repository, discovered by the default `.git` marker.** niwa
  writes the payload once at `<instance>/.codex/` and plants a symlink
  `<repo>/.codex -> <instance>/.codex` in each cloned repository (and each
  worktree). Codex's walk stops at the repository's `.git` and finds the
  symlink right there; the project-layer loader follows symlinks (verified
  in source and by experiment: config, skills, and context all load through
  the link, from the repository root and from directories several levels
  down). No global configuration changes, no environment variable. Real
  copies of the payload are a viable fallback where symlinks are awkward —
  the payload is kilobytes of text, so N copies cost almost nothing — and
  should be the default on platforms where directory symlinks need elevated
  privileges. The symlink is preferred where it works because it leaves one
  source of truth: `niwa apply` regenerates one directory and the change is
  live in every repository at once.

- **Option 1B: a per-instance `CODEX_HOME` with an exported environment
  variable.** The original direction, and the leading candidate until the
  delivery mechanism was examined. A dedicated Codex home per instance
  isolates everything cleanly and can carry marketplaces and plugins, which
  the project layer cannot. It fails on delivery: `CODEX_HOME` must be set
  in the environment of every Codex process, and niwa has no reliable way
  to put it there. The shell integration wraps five specific `niwa`
  subcommands; it does not react to `cd`, so a manually entered shell, a
  terminal opened deep inside a repository, a script, a Makefile target,
  and a non-interactive SSH command all launch Codex without the variable —
  precisely the "any directory, no setup" sessions R5 requires. A wrapper
  binary or mandatory shell hook would be new machinery with its own
  failure modes, and a session launched without the variable would silently
  use the developer's real home, the least acceptable failure shape.
  Rejected on R5; the isolation it bought is delivered instead by the
  project layer plus scoped trust entries.

- **Option 1C: payload at the instance root with `project_root_markers`
  repointed at a niwa marker.** Set `project_root_markers = [".niwa"]` (or
  similar) in the developer's Codex config so the walk climbs past every
  repository's `.git` to the instance root. Measured to work: from
  arbitrary depth inside a nested git repository, the instance payload and
  the full instance/group/repo context chain load. Rejected for its blast
  radius: the setting *replaces* the default rather than extending it, and
  nearest-ancestor-wins means `.git` cannot usefully be kept in the list (a
  repository's own `.git` is always nearer than the instance marker, which
  collapses the walk right back). So every repository on the machine
  outside a niwa instance loses discovery of its own `.codex/` and
  `AGENTS.md` from subdirectories — niwa reaching outside its sandbox to
  degrade unrelated work, violating R13's spirit outright. And the option
  still needs the same per-repository trust entries, because the
  interactive trust gate keys on the working directory and the git
  repository root, not the project root. It buys only a reduction in
  bookkeeping, at an unbounded external cost.

### Decision 2 — The per-repository context filename

The composed per-repository context must win the single per-directory slot
in every repository (R6), including ones that commit their own `AGENTS.md`.

- **Option 2A (chosen): `AGENTS.override.md`, written unconditionally,
  inlining any committed `AGENTS.md`.** `AGENTS.override.md` is hardcoded
  first in Codex's per-directory precedence — the one name that outranks a
  repository's own `AGENTS.md`. niwa writes it into every repository the
  same way regardless of what the repository ships, so behavior is uniform
  rather than dependent on each clone's contents. Because first-match means
  the override *displaces* the committed `AGENTS.md` in discovery, niwa
  reads the committed file at materialization time and inlines its content
  into the composed override — the committed file is never modified (R12)
  and its content still reaches the session (R6). Two boundary rules
  complete the decision: the override is never written empty or
  whitespace-only (an empty file claims the slot and suppresses everything
  behind it), and when niwa has no workspace content for a repository it
  writes no file at all, letting native discovery deliver the repository's
  own `AGENTS.md` undiminished — the degenerate case R6 calls out. A
  further property fell out of measurement: the hardcoded candidate names
  are probed even when the project config layer is disabled for lack of
  trust, so context delivery through this filename has no trust dependency
  at all.

- **Option 2B: `project_doc_fallback_filenames` pointed at the
  `CLAUDE.local.md` niwa already writes.** Add
  `project_doc_fallback_filenames = ["CLAUDE.local.md"]` to the payload
  config and reuse the repo-level file niwa materializes for Claude, at
  zero extra write cost. Measured to work cleanly for every repository that
  ships no `AGENTS.md` of its own — which is most of them. Rejected on the
  silent-failure driver: within a directory the match is strictly
  first-match, so any repository committing an `AGENTS.md` silently
  swallows the fallback — no error, no warning, no signal in the prompt,
  and nothing about the repository tells the developer their workspace
  context stopped loading. A real repository in this workspace ships a
  committed `AGENTS.md` today, and any repository can become one at any
  time with a single commit. The route also inherits a trust dependency the
  override name doesn't have: the fallback list lives in the project
  config layer, which is disabled until trust resolves, so the same
  mechanism that delivers context would go dark in any not-yet-trusted
  tree. A majority-case mechanism with an unsignaled minority failure is
  exactly what the drivers exclude.

### Decision 3 — How the workspace's skills reach a Codex session

- **Option 3A (chosen): whole plugin directories, delivered verbatim,
  symlinked into the payload's skills directory.** For each plugin the
  workspace configures, niwa creates
  `<instance>/.codex/skills/<plugin> -> <plugin root on disk>` — the same
  installed tree the Claude side uses. No content is transformed: no
  frontmatter rewriting, no variable substitution, no file added or
  omitted. This works because of two measured facts. First, skill
  namespacing is not a plugin-cache privilege: it derives from the nearest
  plugin manifest (`plugin.json`) above the skill on disk, and Codex
  deliberately canonicalizes symlinked skill paths and probes the canonical
  ancestors for the manifest — so a symlinked plugin tree yields the same
  `<plugin>:<skill>` names the plugin cache would. Verified against a real
  workspace plugin: all twenty skills loaded, correctly namespaced, from a
  project-layer payload with no Codex home and no content edits. Second,
  the whole-directory unit keeps every plugin-root `references/` and
  `scripts/` file at a real path, which is what the skills' own references
  point at.

- **Option 3B: copy individual skill directories loose.** Copy each
  `skills/<name>/` directory into the payload without its plugin. Rejected
  on two measured failures: a detached skill loses its namespace (it loads
  as bare `decision` instead of `<plugin>:decision`, breaking the
  same-name resolution R8 requires), and it orphans every reference to
  plugin-root `references/` and `scripts/` content that lives above the
  skill directory — those files then exist at no path at all. The unit of
  delivery must be the plugin, not the skill.

- **Option 3C: rewrite `${CLAUDE_PLUGIN_ROOT}` to absolute paths.**
  Substitute the variable in skill bodies so path references resolve
  without Claude's expansion. Rejected because it buys nothing and does
  damage. The Codex binary contains the string exactly once — as an
  environment variable name handed only to plugin hook processes; it is
  never expanded textually in skill bodies, frontmatter, or config, on any
  delivery route, including the plugin cache. Meanwhile a blind
  substitution corrupts the prose sites that *describe* the variable
  (turning "an unexpanded `${CLAUDE_PLUGIN_ROOT}` exits 127" into
  nonsense), and misses the `${CLAUDE_PLUGIN_ROOT:-...}` fallback form the
  plugins' own shell scripts already use to self-resolve from their real
  location — which is exactly why the verbatim tree works unmodified.
  Rewriting would make niwa a content transformer for zero functional
  gain.

### Decision 4 — What niwa writes into the developer's Codex configuration

Some bootstrap cannot come from the payload: trust is read from the config
layers merged before the project layer exists, so the project layer cannot
vouch for itself.

- **Option 4A (chosen): one path-scoped trust entry per cloned repository,
  and nothing else.** `niwa create` and `niwa apply` upsert
  `[projects."<repo root>"] trust_level = "trusted"` in the developer's
  Codex config for each cloned repository, idempotently, touching no other
  key. Trust is mandatory, not cosmetic: without the entry a session runs
  in a read-only sandbox where the agent cannot write files (failing R9),
  and the interactive TUI blocks on a trust prompt (failing R10). With it,
  the TUI was measured to start on a live composer with the project layer,
  skills, and context loaded, from the repository root and from nested
  directories, with no question asked. One measured quirk worth recording:
  the sandbox gate keys on the entry's *presence*, not its value — both
  `trusted` and `untrusted` yield a writable session; only absence
  degrades. niwa writes `trusted` because it is the honest statement of
  intent, not because the value is load-bearing. And one planning gain:
  trust resolution follows a worktree's `.git` file pointer to the main
  repository root, so the repository's entry covers every worktree of that
  repository; per-worktree entries are equally honored but redundant.
  Everything else stays out of the file: no `project_root_markers` (see
  1C), no hook state (Decision 5), no credentials (Decision 6), no global
  behavior keys of any kind. The write is additive and confined to keys
  whose paths resolve inside niwa instances, which is the shape R13
  explicitly permits.

- **Option 4B: write nothing and let the developer trust each repository
  interactively.** The zero-touch alternative: the first `codex` run in
  each repository shows the trust prompt, the developer answers once, and
  Codex writes the entry itself. Rejected against R9 and R10 directly — a
  per-repository setup step by the developer is what those requirements
  exclude — and it degrades worst in the non-interactive case: `codex
  exec` in an untrusted directory never prompts, it silently runs
  read-only. The developer-answers-once path also scales with repository
  count times machine count, which is the bookkeeping niwa exists to do.

- **Option 4C: carry the bootstrap in system-level Codex config.** Trust
  and markers are read from the merged system/user layers, so
  `/etc/codex/config.toml` could hold the entries and keep niwa out of the
  developer's personal file. Rejected: it needs root to install, niwa runs
  unprivileged, and per-instance entries churn far too often for a
  system-managed file. Noted for completeness because it is the only other
  layer that can carry trust at all.

### Decision 5 — Hooks are out of scope, by decision

The PRD excludes Codex hook injection; this design records why that is a
design decision with understood mechanics rather than an unexamined gap.

The mechanism is fully understood and was verified end to end: niwa can
compute a hook's `trusted_hash` — `sha256:` plus the hex SHA-256 of a
key-sorted, compact JSON rendering of the TOML-normalized hook identity, a
canonicalization Go's `json.Marshal` produces natively — and pre-write the
`[hooks.state]` entry so a real session fires the hook with no prompt. The
hash algorithm was independently reimplemented and reproduced byte-for-byte
against hashes the shipped binary wrote itself.

It is excluded anyway, for one decisive behavioral asymmetry: an
interactive session *blocks on a review modal* ("Hooks need review") for
any hook whose recorded hash is missing or stale, where a background run
merely skips the hook silently. Any niwa operation that rewrote the hook
file without recomputing every hash — a payload refresh, a plugin bump —
would leave every developer in the workspace facing a modal on their next
`codex` start. Since nothing niwa ships today needs a Codex-side hook,
carrying the machinery would deliver nothing and put a blocking prompt
directly in the path of the clean start this feature exists to produce
(R10). The offline acceptance check that niwa has written no hook
definitions and no hook-state entries pins the exclusion.

For the increment that first needs a hook, three mechanics are already
established and should not be rediscovered:

- Hooks must be delivered as a loose `hooks.json` in the payload, never
  inside a plugin: plugin-delivered hooks are feature-stage `removed` in
  codex-cli 0.147.0 and inert — they do not fire regardless of trust.
- The hook-state key embeds the path Codex *discovered*, not a
  canonicalized one. Under the symlink architecture that is
  `<repo>/.codex/hooks.json`, so one shared payload file needs one entry
  per repository symlink pointing at it — the symlink does not collapse
  the bookkeeping the way it does for the payload itself.
- The `session_start` hook fires when the first turn is submitted, not at
  TUI startup; a verifier watching the first frame will wrongly conclude
  hooks are broken.

### Decision 6 — No API key binding

niwa binds no `OPENAI_API_KEY` and writes no auth-related key into any
Codex-readable location. This reverses the prior design's Decision 4, which
presented the key as bindable through the existing secret table with no new
code.

The measured behavior makes the key harmful in both states of the world.
With a working subscription login, an exported key is inert: the session
uses the login, dials the subscription endpoint, and never contacts the
metered API — the key changes nothing except adding a permanent warning to
every `codex doctor` run. With a broken or missing login, the same key
silently becomes a metered fallback: the session dials the metered endpoint
and the health check reports green, converting a loud "no credentials
found" failure into a quietly billed session. Pinning the auth mode does
not rescue it: `forced_login_method = "chatgpt"` was measured not to fail
closed (with no login and a key set, the session still used the metered
endpoint), and the sibling value `api` triggers an implicit logout that
deletes the credential file outright. niwa must never emit that key in any
generated config — a config generator having that value within reach is a
hazard on its own. Leaving the key unbound keeps the broken-login failure
loud, which is the safer default for a tool that prepares workspaces
unattended (R13's credential clause also simply forbids most of the
alternatives). The secret-binding table's own defects are tracked
separately as niwa#228.

### Decision 7 — Making materialization additive

The exclusivity must come out of the pipeline without disturbing the Claude
byte-stream (R2) or the launch-time meaning of the agent setting (R14).

- **Option 7A (chosen): callers run once per materialized agent; the agent
  type survives as a launch-time selector.** The measured shape of the
  existing code makes this the natural cut: the exclusivity lives in the
  *callers*, not the accessors. Each write site — the workspace-root
  materializer, the instance and group content installers, the repo and
  worktree installers — takes one resolved `Agent` and writes for it; no
  accessor needs to change meaning. Going additive means each preparation
  runs the relevant writers once per agent: the Claude pass exactly as
  today (byte-identical by construction), and a Codex pass that writes the
  `AGENTS.md` files at the niwa-owned levels plus the net-new payload,
  symlink, and override deliveries. The `Agent` field on the apply options
  stops meaning "the agent this instance is for" and survives only where
  launch-time selection is real: `niwa dispatch`'s refusal and the model
  category resolver. The dispatch refusal likely needs no logic change —
  it keys on the resolved launch selection, which stays coherent when
  `default_agent` means "the agent a niwa-launched session runs" rather
  than "the only agent prepared" — but its comments narrate the old
  exclusive framing and must be re-worded, or they will mislead the next
  reader. The prior design's provisional `LocalContextFileName()` Codex
  branch (returning `AGENTS.md`, never called) is superseded: the
  repository-level Codex filename is `AGENTS.override.md`, and the
  accessor should be retired or corrected rather than left asserting a
  contract this design abandons.

  The blast radius is known and bounded. Three functional scenarios in
  `test/functional/features/codex-agent.feature` assert exclusivity
  directly; the two assertions of the form "and the other agent's file
  does not exist" invert. The unit tests in
  `internal/workspace/content_test.go` and
  `internal/workspace/root_materializer_test.go` that assert the other
  file's absence are the same inversion at unit scope. The PRD's R2
  criterion pins that these are the *only* tests modified.

- **Option 7B: generalize the settings builder into an agent-neutral
  config writer.** Extend `buildSettingsDoc` to emit either agent's
  configuration from one abstraction. Rejected: the builder is
  Claude-shaped end to end — its keys (`permissions`, `hooks`,
  `enabledPlugins`, `extraKnownMarketplaces`) are Claude Code API surface,
  not portable concepts, and Codex reads none of them. A Codex payload
  writer is net-new code, not a generalization; forcing an abstraction
  over two unrelated schemas would couple every future change in either
  agent's surface to the other.

- **Option 7C: model the materialized agents as a set on the config
  surface.** Replace the scalar agent with a configurable list of agents
  to prepare. Rejected as scope the PRD forbids: R1 makes preparation for
  both agents unconditional, so a knob choosing which agents to
  materialize reintroduces the choice the feature removes, plus a config
  migration R14 rules out.

Two deferred mechanisms from the prior design's repo-level analysis get
their answer here, one returning and one staying out:

- **The git-exclude extension is needed.** niwa now writes two
  non-`.local`-named entries into repository working trees (`.codex` and
  `AGENTS.override.md`), which the existing managed patterns (`*.local*`,
  `.niwa/`) do not cover. Both patterns must be written **bare**, not with
  a trailing slash: a trailing-slash gitignore pattern is directory-only,
  and git classifies a symlink as a file regardless of target, so the
  repo's existing `.niwa/` idiom copied here would leave `?? .codex` in
  every repository's `git status` forever — no error, just permanent dirt,
  which is why this is the highest-risk detail in the feature (R11). The
  bare form matches a symlink and a real directory alike, so it stays
  correct under the copy fallback too.
- **The collision guard stays unnecessary.** The prior design deferred a
  guard against clobbering a repository's committed `AGENTS.md`; it does
  not come back, because niwa never writes to that filename.
  `AGENTS.override.md` is Codex's designated local-override slot — the
  analogue of `CLAUDE.local.md`, a name repositories do not commit — and
  the committed `AGENTS.md` is handled by inlining, never by overwriting
  (R12). The guard was insurance against a write this design does not
  perform.

## Decision Outcome

One preparation, two readers. `niwa create` and `niwa apply` materialize
the Claude tree exactly as today and, unconditionally alongside it:

- **The payload** (Decision 1): `<instance>/.codex/` holding a project
  `config.toml` (whose one required job is declaring the context budget,
  Decision 2/architecture) and `skills/<plugin>` symlinks to each
  configured plugin's installed root, whole and verbatim (Decision 3).
- **Per-repository delivery** (Decisions 1, 2): a `.codex` symlink (or
  copy, where symlinks are unavailable) and a composed
  `AGENTS.override.md` in every cloned repository and worktree, plus bare
  `.codex` and `AGENTS.override.md` patterns in each repository's managed
  git-exclude block (Decision 7).
- **Niwa-owned levels**: `AGENTS.md` at the instance root and group
  directories, now written alongside `CLAUDE.md` rather than instead of it,
  each composing the layers above it (architecture below).
- **The trust bootstrap** (Decision 4): one
  `[projects."<repo root>"] trust_level = "trusted"` entry per cloned
  repository in the developer's Codex config, upserted idempotently, and
  nothing else written there.
- **Nothing for hooks** (Decision 5) and **nothing for credentials**
  (Decision 6).
- **The agent discriminator** survives as a launch-time selector for
  dispatch and model resolution; `default_agent` keeps its value set and
  its launch-time meaning with no migration (Decision 7, R14).

## Solution Architecture

### On-disk shape

```
<instance>/
  .codex/                          # the payload niwa owns
    config.toml                    # project_doc_max_bytes (the budget)
    skills/<plugin> -> <plugin install root>   # one symlink per plugin
  CLAUDE.md                        # unchanged
  AGENTS.md                        # instance layer, composed
  <group>/
    CLAUDE.md                      # unchanged
    AGENTS.md                      # instance + group layers, composed
    <repo>/
      .git/                        # Codex's default project-root marker
      .codex -> ../../.codex       # symlink (or real copy)
      CLAUDE.local.md              # unchanged
      AGENTS.override.md           # instance + group + repo, composed;
                                   # inlines any committed AGENTS.md
      .git/info/exclude            # managed block gains bare
                                   # ".codex" and "AGENTS.override.md"
```

Worktrees created by `niwa worktree` get the same two per-tree writes (the
`.codex` link with its target computed for the worktree's location, and a
composed override carrying the worktree's own framing — repository, purpose,
branch — in place of the repository layer).

In the developer's Codex config, per cloned repository:

```toml
[projects."<absolute repo root>"]
trust_level = "trusted"
```

### The composition rule

Codex's walk delivers only files at or below the nearest marker root — and
at the niwa-owned levels, where no marker exists above, only the working
directory itself. So no file niwa writes for Codex can rely on an outer
layer being read: **every Codex-facing context file composes the full chain
of layers from the instance root down to its own directory.** The instance
root's `AGENTS.md` carries the instance layer; a group's carries instance
plus group; a repository's `AGENTS.override.md` carries instance, group,
and repository (with the repository's committed `AGENTS.md` inlined when
one exists); a worktree's carries instance, group, and the worktree
framing. This is duplication by design: the walk stops at the repository
root, so the outer layers must travel inside the per-repository file. The
same rule implies the never-empty constraint from Decision 2: a location
with no content at any layer gets no file.

### The byte budget

The composed chain competes for a single `project_doc_max_bytes` budget,
default 32768, consumed outermost-first with silent truncation — so an
oversized outer layer starves exactly the innermost layer R7 protects. The
budget is settable from the payload's own `config.toml` (measured: a
project-layer value took effect and a 49,767-byte composed document loaded
whole under a 65,536 limit). niwa therefore declares the budget in
`<instance>/.codex/config.toml`, sized to cover at least the byte size of
the largest composed file plus any committed context files in
subdirectories below a repository root, with generous headroom rather than
a tested-once number — the truncation gives no signal, so the margin is
the only defense. Because the budget lives in the project config layer, it
takes effect only where trust resolves; the trust entries of Decision 4
are what make the declaration reliable inside repositories.

### Worktrees

No worktree-specific mechanism exists, verified rather than assumed: a real
`git worktree add` working tree's `.git` *file* satisfies the marker check
(the check is a bare metadata stat with no type test), and config, skills,
and context all loaded from a worktree through the payload symlink,
identically to a clone, at every depth probed. Trust resolves through the
`.git` file's pointer to the main repository root, so the per-repository
trust entry covers all of that repository's worktrees. What worktrees do
need is the same two per-tree writes every clone gets, freshly composed —
a single shared context file cannot carry N concurrent worktrees' framing
— which the worktree lifecycle already does for Claude and extends here
(R3).

### What `niwa apply` refreshes

Re-materialization is regeneration, not append (R3, and the PRD's
no-accumulation criterion). On every apply niwa recomposes all `AGENTS.md`
and `AGENTS.override.md` files from the current config sources and the
repositories' current committed `AGENTS.md` content (the inlined copy goes
stale between applies otherwise); rewrites the payload `config.toml` and
reconciles the skills symlinks against the configured plugin set; repairs
or re-creates the per-repository `.codex` links; extends managed
git-exclude blocks for newly cloned repositories; and upserts trust
entries for new repositories while leaving existing entries untouched.
`niwa worktree apply` does the same for a worktree. A dangling payload
symlink is harmless in the interim — Codex skips the layer without error —
but apply repairs it.

### What stays Claude-only

The `.claude/` settings tree, hooks, plugin/marketplace registration, and
the env pipeline are untouched and gain no Codex analogue in this design:
Codex reads none of those surfaces, and the payload writer is net-new
rather than a generalization of them (Decision 7B). Sessions launched at
the instance root or a group directory get that level's composed
`AGENTS.md` and the skills (which load without trust), but no trust entry
— the interactive clean-start guarantee is scoped to repositories (R10),
and the consequence is recorded honestly below.

## Implementation Approach

Four batches, each independently landable, sequenced so the invariant
(Claude unchanged) is protected first and every later batch extends a
seam the previous one proved.

1. **Additive niwa-owned levels.** Rework the materialization callers to
   run once per agent at the workspace-root, instance, and group levels,
   writing both `CLAUDE.md` and the composed `AGENTS.md` unconditionally.
   Invert the exclusivity assertions (the functional scenarios and the
   "other file does not exist" unit tests — the only permitted test
   changes per R2's criterion), re-narrate the dispatch refusal's
   comments, and pin the R2 byte-identity check. This lands the
   architectural pivot with the smallest new write surface and proves R14
   (no migration, launch-time meaning intact) before any repository is
   touched.

2. **The payload and per-repository delivery.** The net-new Codex
   materializer: payload directory with budget-bearing `config.toml` and
   plugin symlinks, per-repository `.codex` link with the copy fallback,
   composed `AGENTS.override.md` with committed-`AGENTS.md` inlining and
   the never-empty rule, and the git-exclude extension with bare
   patterns. This batch carries the feature's highest-risk details (the
   bare pattern, the empty-file trap, the budget sizing), so its tests
   are the silent-failure checks the PRD's criteria spell out.

3. **The trust writer.** Idempotent, additive editing of the developer's
   Codex config: upsert per-repository entries, preserve everything else
   byte-wise, tolerate an unreadable credential file, and prove the
   no-accumulation and no-global-keys criteria. Kept separate from batch
   2 because it is the only write outside the instance and deserves its
   own review focus.

4. **Worktree parity and docs.** Extend the worktree lifecycle with the
   per-tree writes, verify the marker and trust-inheritance behavior
   end-to-end, and add the guide entry this design's user-visible surface
   warrants (a `docs/guides/*` page covering what dual-agent preparation
   produces and what it writes where).

Batch 1 gates 2 (the per-agent caller seam is what the Codex materializer
plugs into); 2 gates 4 (worktrees reuse the per-repository writers); 3 is
independent of 2 and can land in parallel after 1. Live-gated checks
(interactive start, first-write, post-session cleanliness) ride whichever
batch delivers their mechanism, following the repo's existing
skip-when-absent pattern for agent binaries.

## Security Considerations

- **niwa writes into a file it does not own.** The developer's Codex
  config is the one surface outside the instance this design touches. The
  write is bounded three ways: *scope* — only `[projects."<path>"]` keys
  whose paths resolve inside niwa-managed instances, never a global key,
  never a marker, never anything on the denylist or off it that changes
  behavior outside those paths; *additivity* — existing keys are never
  removed, reordered, or altered, and the edit must preserve the file's
  other content byte-wise (the PRD's criterion makes this testable);
  *credentials* — the credential and login files are never read or
  written, and preparation succeeds even when they are unreadable (R13).
  An entry whose path no longer exists grants nothing — trust keys are
  consulted only when a session runs at that path — so the residual risk
  of stale entries after an instance is deleted is clutter, not
  capability. Removing them belongs to the instance-destruction lifecycle
  and is not load-bearing for safety.

- **Trusting a repository is a real grant, and its blast radius should be
  understood.** A trust entry does not merely silence a prompt: it
  enables project-local config, exec policies, and (were any declared)
  hooks to load from content inside that repository — including a
  `.codex/` directory committed anywhere in the repository's tree, since
  trust resolves transitively down the walk chain and the nearer layer
  wins on precedence. Third-party repository content thereby gains a
  voice in session configuration. The bound is Codex's own project-config
  denylist (a project layer cannot redirect provider URLs, swap model
  providers, or set notification commands) plus the fact that hook
  execution still requires user-config hash state niwa never writes. This
  grant is inherent to making sessions able to act (R9): the developer
  who clones a repository into a niwa workspace is expressing the same
  intent the trust prompt would have asked about, and the entry is scoped
  to exactly those paths. The design accepts this residual risk and
  states it rather than hiding it behind the word "trust".

- **The payload is reachable from inside repositories.** The `.codex`
  symlink makes the shared payload addressable from every repository
  working tree, including repositories containing third-party content an
  agent session may be processing. The payload contains no secrets by
  construction (project config and skill content only — Decision 6 keeps
  credentials out of every Codex-readable location), and it is
  regenerated from configured sources on every apply, so a tampered
  payload does not survive re-preparation. A session induced to write
  through the symlink could modify skills mid-session; that exposure is
  shared with the Claude side's materialized skill tree and is bounded by
  the same fact that nothing in the payload is executed by niwa itself.

- **No new process execution and no credential handling.** niwa gains no
  code that launches an agent or touches an auth file; the design writes
  files and edits one TOML document. The refusal to emit
  `forced_login_method` in any form removes the one config key measured
  to have a destructive side effect (its `api` value deletes the
  credential file).

## Consequences

### Positive

- Every prepared instance serves both agents with no choice at creation
  time, no migration, and no per-instance configuration (R1, R14); the
  Claude side is untouched by construction, not by care (R2).
- A Codex session anywhere inside the instance — repository root, nested
  directory, worktree — gets the full layered context and the workspace's
  skills under their same names, and can write files immediately with a
  clean interactive start (R4–R10). Every mechanism this relies on is
  measured against the shipped binary, several down to the implementing
  source line, not assumed.
- Repositories stay git-clean and unclobbered under both delivery
  variants (symlink and copy), and the developer's Codex setup differs
  only by path-scoped additive entries (R11–R13).

### Negative / limitations

- **Composed content is duplicated per repository.** The instance and
  group layers travel inside every override file, so an instance with N
  repositories carries N copies of that prose, and all of them plus the
  niwa-owned files must be recomposed on apply. Mitigation: the content
  is kilobytes of text, regeneration is already the apply model, and the
  duplication is forced by the walk boundary, not chosen.
- **The inlined committed `AGENTS.md` goes stale between applies.** A
  repository editing its own `AGENTS.md` sees the change reach Codex
  sessions only after the next `niwa apply`, while a Claude session sees
  it immediately. Mitigation: apply refreshes it, the committed file
  itself is never touched, and the window is the same one every other
  materialized content source already has.
- **Sessions at the niwa-owned levels get less interactively.** No
  trust entry is written for the instance root or group directories, so
  an interactive session started there sees the trust prompt once, and
  the payload's budget declaration does not apply there until the
  developer answers it. Mitigation: R10 scopes the clean-start guarantee
  to repositories, where work happens; the developer's one-time answer
  writes Codex's own entry; a later increment can add an instance-root
  entry if this bites in practice.
- **The design leans on upstream discovery invariants.** The hardcoded
  first-match precedence of `AGENTS.override.md`, the bare-stat marker
  check that admits a worktree's `.git` file, and symlink-following in
  the project-layer loader are all pinned to codex-cli 0.147.0 by
  measurement. A future Codex release could reorder or tighten any of
  them silently. Mitigation: every reliance is on documented, upstream-
  tested behavior rather than accident; the acceptance criteria double as
  a regression harness; and a cheap materialization-time smoke test
  (render the prompt in one repository, assert niwa's sentinel) can catch
  a drift the moment it ships.
- **niwa now edits a developer-owned file.** However bounded, this is a
  new category of write for niwa and a new way to surprise a developer
  reading their own config. Mitigation: the entries are few, legible,
  path-scoped, and inert outside the instance; the security section
  states the grant plainly; and the additivity criterion makes any
  overreach a test failure.

### Neutral

- The agent discriminator, its config key, and the dispatch refusal all
  survive with their launch-time meaning; Codex background dispatch
  remains out of scope and keyed on the same selector, so the refusal's
  behavior is unchanged even though its rationale is re-worded.
- The cross-repository context gap (a session crossing into a repository
  it did not start in) is improved incidentally on the Codex side by the
  per-repository files but not closed; it stays tracked as niwa#247.
