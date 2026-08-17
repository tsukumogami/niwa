---
schema: design/v1
status: Current
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
  else there — no hooks, no API key, no global keys, no marker changes.
  Committed repository content at either name niwa writes is a detected
  conflict, reported loudly and never overwritten or trusted on niwa's
  signature; the composer reads repository files only as regular files,
  never through symlinks; and the trust write is atomic, lock-serialized,
  and refuses an unparseable pre-existing file. The existing agent
  discriminator survives as a launch-time selector; the materialization
  callers that treated it as exclusive run once per agent instead.
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

Current

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

- **D1 — No environment preparation, ever (R5).** The mechanism must work from a
  manually opened terminal, a shell spawned deep inside a repository, a
  script, a Makefile, and non-interactive SSH. niwa's shell integration
  intercepts five `niwa` subcommands and never reacts to a plain `cd`, so
  any design that needs an exported environment variable fails in exactly
  the sessions R5 names.
- **D2 — Nothing niwa does may degrade Codex outside its instances (R13).**
  Scoped, additive entries confined to instance paths are acceptable;
  anything that changes discovery or behavior for unrelated repositories on
  the machine is not.
- **D3 — The Claude side is an invariant, not a target (R2).** Every existing
  output must be byte-for-byte unchanged; the change adds a second reader.
- **D4 — Hostility to silent failure.** The measured failure modes — first-match
  displacement, budget truncation, missing trust under `exec` — are all
  silent. A mechanism that works in the majority case and delivers nothing
  in a minority case, with no error, is worse than one that never promised
  delivery. This driver decides more of the design than any other.
- **D5 — Repositories stay clean and unclobbered (R11, R12).** Nothing untracked
  in `git status`, nothing a repository ships overwritten.
- **D6 — Verbatim delivery (R8).** Skills must arrive with the same content
  Claude sees. niwa should not become a content transformer: every rewrite
  is a fidelity risk and a maintenance obligation.
- **D7 — Sessions must act immediately and start clean (R9, R10).** A
  read-only sandbox or a blocking startup modal each fails the feature's
  core promise.

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

  One reading rule is load-bearing for safety: **the composer reads a
  repository's committed `AGENTS.md` only as a regular file, never through
  a symlink** — and the same holds for any other repository file the
  composer ever reads. Git reproduces committed symlinks verbatim, so
  without this rule a hostile repository committing its `AGENTS.md` as a
  symlink to a sensitive absolute path — the developer's own agent
  credentials being the obvious target — would get the target's content
  read by niwa and written into the instruction context of every Codex
  session in that repository. The enforcement is the open itself, not a
  check before it: niwa opens the file with `O_NOFOLLOW` and fails on a
  symlink, so the refusal and the read are one syscall with no window in
  between — a separate type check before the open would leave a gap in
  which the path can be swapped, which is exactly the kind of
  quietly-incomplete defense this design refuses elsewhere. The refusal
  is scoped to the inline, no wider: when the committed file fails the
  open or is anything other than a regular file, niwa reads nothing and
  inlines nothing, but still writes the override carrying the workspace
  layers, which need no repository read at all, and reports the refusal
  loudly (D4). The narrow scope is load-bearing twice over. It preserves
  the workspace half of R6 (the session keeps the instance, group, and
  any worktree layers; only the repository's own content waits for a
  regular file, and the report says so). And it reinforces the defense
  where it applies: `AGENTS.override.md` wins first-match, so the
  written override displaces the symlinked `AGENTS.md` from the
  discovery slot — without it, Codex's own native read would follow the
  symlink and pull the target into the session's context, delivering
  through Codex the disclosure niwa just refused to perform. The
  displacement must not be read as more than it is: **the `O_NOFOLLOW`
  refusal is the part of the defense that holds unconditionally; the
  displacement is a per-directory, content-conditional reinforcement.**
  Per-directory, because the override wins only the repository root's
  context slot — a context symlink committed in a subdirectory occupies
  its own directory's slot, which nothing niwa writes contests, and
  Codex reads it natively there. Content-conditional, because when no
  layer has content the never-empty rule writes no override at all, so
  nothing displaces even the root file. And `O_NOFOLLOW` itself guards
  this read, not a deeper one: it refuses a symlink at the final path
  component only, so any future work that reads through a longer
  repository-controlled path needs full no-symlink path resolution (an
  `openat2`-style no-symlinks mode), not `O_NOFOLLOW` alone. Refusal
  was chosen over the alternative, resolving the link and requiring the
  target to stay inside the working tree, because refusal has no
  resolution edge cases — no chained links, no post-resolution
  traversal — and the benign case it costs (a repository symlinking its
  `AGENTS.md` to another in-tree file) is rare, loudly reported, and
  fixed by committing a regular file. The R13 claim that niwa never
  reads the developer's credentials is true only because of this rule;
  it is stated here so the claim cannot drift away from its
  enforcement.

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

  The one input this turns on is the plugin root itself, and resolving it
  is part of the decision, because niwa's plugin model today is name-based
  end to end: it registers marketplaces and enabled plugins in Claude's
  settings and shells out to the Claude CLI for installation, resolving an
  on-disk directory only for repository-sourced marketplaces — and even
  there only the marketplace root, never the per-plugin subdirectory. The
  payload writer therefore resolves the root per marketplace kind. For a
  repository-sourced marketplace, niwa parses the marketplace manifest it
  already opens for the name and joins the plugin's declared source
  directory onto the marketplace root it already computes. For a
  github-sourced marketplace, the tree lives in Claude Code's user-global
  plugin cache, populated by a pre-warm that is explicitly best-effort:
  skippable by flag and config, dependent on the `claude` binary being
  present, and able to fail or time out — and where a Claude session
  self-heals by installing at startup, a Codex session has no equivalent.
  So a plugin root that is missing at apply time is handled under driver
  D4, not hoped away: niwa skips that plugin's symlink and reports it
  loudly, naming the plugin and the path it expected, and the next apply
  materializes the link once the root exists. Leaving a silent dangling
  symlink is not acceptable here, because when the cause is a skipped or
  failed install nothing later repairs it. This also puts a second
  external layout on the design's dependency list — Claude Code's plugin
  cache — recorded in Consequences beside the codex-cli pins.

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
  key. The key's path form is part of the contract: niwa **canonicalizes
  the repository root** — full symlink resolution of every path component
  — before writing the key, rather than using whatever string the cloner
  happened to hold. Codex resolves the working directory and the git root
  when it looks trust up, so an entry keyed by an unresolved path through
  a symlinked parent (a linked home directory, an automounted volume, a
  symlinked workspace root — all common) would be silently miskeyed, and
  the failure shape is the worst one available: a read-only sandbox with
  no error. The PRD's miskeyed-entry criterion ("keyed by a path that
  resolves to that tree's actual root — a present-but-miskeyed entry
  fails") is the regression check for exactly this. Trust is mandatory,
  not cosmetic: without the entry a session runs
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

  Because this is the one file niwa edits that it does not own, the write
  discipline is part of the decision, not an implementation detail. Three
  rules. *Atomic replacement:* the edited document is written to a
  temporary file in the same directory, synced, and renamed over the
  original, so an interrupted apply leaves the previous file intact,
  never a truncated one. The staging file must sit beside the config —
  an atomic rename requires the same filesystem — and is the one
  transient artifact the "nothing else" claim carves out; it exists only
  for the instant of the write. *Never rewrite what did not parse:* if
  the pre-existing file fails to parse, niwa refuses the trust step,
  leaves the file byte-untouched, and reports the failure as an error
  that makes the apply exit non-zero after the rest of materialization
  completes — an error rather than a warning, because the resulting
  instance would otherwise look prepared while every repository in it
  silently runs read-only at session time. A pipeline that "repairs"
  what it could not read is how additivity guarantees get broken in
  practice. *Serialized concurrent applies:* multiple instances share
  one developer config, and two applies running at once must not drop
  each other's entries or interleave the file into invalid TOML, so
  niwa's writers serialize through an advisory lock held across the
  whole read-modify-write, with the file re-read under the lock before
  editing. The lock lives in niwa's own state directory, keyed by the
  config path, so serialization adds no artifact to the developer's
  config directory; it serializes niwa's writers only, and writes by
  Codex itself are outside niwa's control — the same exposure Codex's
  own concurrent sessions already carry.

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

- **Option 7A (chosen): the Claude pipeline untouched, a Codex pass beside
  it, and the agent type surviving as a launch-time selector.** Where the
  exclusivity actually lives differs by level, and the cut follows the
  code rather than a slogan:

  - *Workspace-root and instance levels:* the exclusivity is in the
    callers and the accessor is a pure filename selector
    (`RootContextFileName()` feeds a `filepath.Join` and nothing else is
    agent-dependent), so running those writers once per agent genuinely
    yields both files — an existing test already proves the two trees
    coexist by calling the installer twice with different agents. This is
    the only place where re-parameterization is the whole job.
  - *Group level:* the existing installer writes exactly one source, the
    group's own entry. Re-running it under Codex would produce a
    group-only `AGENTS.md`, which violates this design's composition rule
    (a group file must carry instance plus group). The group-level Codex
    file is therefore net-new composition sharing the per-repository
    composer, not the existing writer re-run with a different filename.
  - *Repository and worktree levels:* the exclusivity sits in an
    accessor, `WritesRepoLevelContext()` (`internal/agent/agent.go:85`),
    which gates the two repo-level installers to a no-op under Codex.
    Running those callers "once per agent" writes nothing on the Codex
    pass; the Codex delivery here is a net-new writer beside them, not a
    rework of them.

  Going additive therefore means: the Claude pass runs exactly as today
  (byte-identical by construction); the root- and instance-level writers
  additionally run for Codex; and a net-new Codex materializer produces
  the composed group files, the payload, the per-repository symlinks and
  overrides, and the conflict handling. Both accessors the prior design
  left as Codex seams are **retired**: `LocalContextFileName()`'s Codex
  branch (never called, and wrong — the repository-level Codex filename
  is `AGENTS.override.md`) and `WritesRepoLevelContext()` itself, whose
  gate becomes vacuous once the Claude pass always runs as Claude and the
  Codex pass never calls the repo-level installers. Retiring rather than
  keeping them means the seam cannot silently assert a contract this
  design abandons.

  The `Agent` field on the apply options stops meaning "the agent this
  instance is for" and survives only where launch-time selection is real:
  `niwa dispatch`'s refusal and the model category resolver. The dispatch
  refusal likely needs no logic change — it keys on the resolved launch
  selection, which stays coherent when `default_agent` means "the agent a
  niwa-launched session runs" rather than "the only agent prepared" — but
  its comments narrate the old exclusive framing and must be re-worded,
  as must the now-inert agent assignments on the launch-coupled
  provisioning paths (`internal/cli/instance_from_hook.go:499`,
  `internal/cli/session_lifecycle_cmd.go:337`), which no longer select
  what gets materialized. The `--agent` flag on `niwa create` and
  `niwa apply` likewise loses its materialization meaning: it stays
  accepted so existing invocations keep working (R14), but it no longer
  affects what preparation produces, and its help text is re-worded to
  say so rather than left promising a selection R1 removed.

  The blast radius, checked against the tests rather than remembered:
  `test/functional/features/codex-agent.feature` has three scenarios, of
  which **two** assert materialization exclusivity (the dispatch-refusal
  scenario stands as-is). **Three** assertions change, not two: the
  instance-root `CLAUDE.md does not exist` under a Codex default inverts;
  the instance-root `AGENTS.md does not exist` under a Claude default
  inverts; and `tools/app/CLAUDE.local.md does not exist` (a
  repository-skip assertion, not an other-agent-file assertion) also
  changes, because the Claude pass now writes repository content
  regardless of `default_agent`. One assertion must **not** be touched:
  `tools/app/AGENTS.md does not exist` stays true, because niwa writes
  `AGENTS.override.md` into repositories, never `AGENTS.md` — inverting
  it would delete a valid guarantee. The feature file's description prose
  and its `Design:` pointer narrate the exclusive model and are updated
  alongside. At unit scope, the "other file is absent" assertions in
  `internal/workspace/root_materializer_test.go` and
  `internal/workspace/content_test.go` invert, and the repo-level-skip
  test in `content_test.go` is deleted with the retired
  `WritesRepoLevelContext()` accessor it exercises. That is the complete
  set of test edits, stated here so the PRD's R2 criterion — which
  enumerates exactly these files — is decidable.

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
their answer here, one returning in its expected form and one returning in
a different one:

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
  correct under the copy fallback too. One limit of the mechanism must be
  stated so it is not asked to do a job it cannot: exclude patterns act
  only on *untracked* paths. For a name a repository already tracks, the
  pattern is inert, and a niwa write at that name would show in `git
  status` as a modification — which is why the conflict case below has
  its own rule and is not handled by the pattern.
- **The blind-overwrite guard does not come back, but a conflict rule
  does.** The prior design deferred a guard against clobbering a
  repository's committed `AGENTS.md`; that guard stays unnecessary in its
  original form, because niwa never writes to that filename — the
  committed `AGENTS.md` is handled by inlining (R12). What this design
  adds instead is a rule for the two names niwa *does* write, because "a
  name repositories do not commit" is an assumption about third-party
  behavior, not a guarantee — a committed `.codex/` is a real Codex
  convention any upstream can adopt. Before writing either name, niwa
  checks the target path. A path already occupied by anything niwa did
  not itself materialize — a tracked or untracked file, directory, or
  symlink — is a conflict: niwa writes nothing at that name, modifies and
  deletes nothing (R12, R11), and surfaces the conflict as a loud
  per-repository warning in the apply output. A quiet skip is exactly the
  silent minority-case failure driver D4 excludes.

  "Deletes nothing" is a promise the write rule alone cannot keep,
  because the apply pipeline already deletes by record: its managed-file
  reconciliation removes any recorded path the current apply did not
  produce, unconditionally. A path that becomes a conflict drops out of
  the produced set — a clean repository got its override written and
  recorded, then a committed file arrived at the name (or a `.codex`
  conflict suppressed the override through the coupling below) — and
  record-driven cleanup would then delete whatever now sits there,
  including tracked content. So the rule extends into reconciliation,
  and the mechanism is **an explicit exemption, which requires a named
  change to the cleanup**: the apply hands its per-run conflict
  verdicts to the managed-file cleanup as an input, and the cleanup
  consults that conflicted-path set and skips those paths before
  removing recorded paths the apply did not produce. The cleanup change
  is not optional decoration — today the cleanup tests only membership
  in the produced set, so absence from that set is the deletion
  trigger, and dropping a conflicted path's entry *without* teaching
  the cleanup about conflicts is precisely the deletion this paragraph
  exists to prevent. An implementer must build the consultation, not
  assume reconciliation already knows. With it in place, the conflicted
  path leaves the record — the state stays truthful about what niwa no
  longer owns — the file on disk is untouched, and if the conflict
  later clears, the marker test recognizes the fresh write. The
  rejected alternative was forward-carrying the prior entry into the
  produced set, the idiom the worktree-refresh path uses to shield a
  skipped-but-live worktree's files from this same cleanup (it retains
  the entries so the cleanup sees the path as still produced). That
  needs no cleanup change, but it keeps the record claiming niwa
  ownership of a path niwa just declared foreign, under a content hash
  that no longer describes what sits there — a stale claim every later
  reader of the state file must know to disbelieve. One ownership
  authority governs both the write and the delete decision: the
  conflict verdict, which the writers and the cleanup both consult.

  Detection is per name, but the two names couple in one direction, and
  the coupling is load-bearing. A `.codex` conflict suppresses the
  override write too: the override's byte budget is declared in the
  payload `config.toml` reached only through the link that was just
  refused, so an override written anyway would run the composed chain
  under Codex's 32768-byte default and silently truncate exactly the
  repository layer R7 protects — a silent failure reached through the
  rule written to prevent silent failures. A conflicted-`.codex`
  repository therefore gets nothing from niwa — no link, no override, no
  trust entry — and falls back to native discovery of its own content,
  reported. The reverse does not couple: an `AGENTS.override.md`
  conflict alone suppresses only the override, while the link, the
  exclude patterns, and the trust entry still materialize — skills and
  the payload config still reach sessions there, and the repository's
  own committed override carries the context slot, reported.

  niwa recognizes its own writes two ways, and the distinction matters
  because the whole rule rests on it. The `.codex` symlink is recognized
  by its target (the instance payload). Composed files are recognized by
  content, not by records: every composed file niwa writes begins with a
  generation marker — a line of the document itself, not a sidecar or a
  filesystem attribute, so it is agent-visible, counts against the byte
  budget, and leaves the never-empty rule unambiguous (no content still
  means no file, never a marker-only one) — and a file at the name is
  niwa's exactly when it is untracked and carries the marker. Anything
  tracked is a conflict regardless of content, and an untracked file
  without the marker is foreign. The content test is deliberate: the standalone
  `niwa worktree apply` path persists no managed-file records today, so
  a record-based check would make that path unable to recognize its own
  prior override on re-apply and refuse the refresh R3 requires. (An
  untracked forgery carrying the marker at niwa's own name would be
  overwritten on the next apply — it is niwa's name and nothing
  committed is touched, so R12 is not in play.) For a conflicting
  `.codex` specifically, niwa also **withholds
  that repository's trust entry — and removes one it wrote earlier**:
  with niwa's payload absent, a trust entry would vouch for the
  repository's own committed `.codex/` — third-party content
  impersonating the payload with niwa's signature on it. Withholding
  alone covers only the first apply; a repository that was clean, got
  its entry, and later acquires a `.codex` of its own would keep a stale
  entry vouching for a payload niwa no longer writes — the same
  impersonation path reopening on a repository niwa already trusted. So
  the retraction runs on every apply, and its guarantee is stated
  carefully: niwa removes the entry its record names, **clears the
  record entry with the removal**, and never re-adds an entry while the
  conflict stands. The guarantee is never-reinstated, not
  always-absent. After the removal, the developer may answer Codex's
  own prompt, and Codex writes the same key back — that answer is
  theirs, sits in no niwa record, and later applies leave it alone.
  Clearing the record with the removal is what makes the two rules
  compose: a record left in place would license the next apply to
  delete the developer's answer at that key, the exact harm the record
  test exists to prevent, arriving by another route. Record-file
  disagreement is safe in the direction the mechanism can produce: a
  recorded key already absent from the file is a no-op, and an
  unrecorded key present in the file is left alone. Removal is bounded
  by **record, not shape**: niwa
  records, in instance state, each trust key it writes — the recording
  is available because the instance-apply path is the only path that
  writes them — and removes only keys that record names. Shape cannot
  be the test here: Codex writes an identically-shaped
  `[projects."<path>"]` entry when the developer answers the startup
  trust prompt, which is exactly the prompt this design routes
  conflicted repositories to, so a shape-based removal would delete the
  developer's own answer to a question niwa caused to be asked. The
  file-side generation-marker test has no analogue for a TOML table,
  which is why the two recognition mechanisms differ. Bounded this way,
  the additive-only property over the developer's own keys stands —
  niwa retracts its own signature, never anyone else's. The
  trust decision is left to Codex's own startup prompt, where the
  developer sees exactly what they are being asked to trust. A
  conflicted repository therefore degrades loudly — reported by apply,
  prompting at session start — never silently.

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
  git-exclude block; a repository already occupying either name is a
  detected, loudly reported conflict that is never overwritten (Decision
  7), and the composer reads committed files only as regular files
  (Decision 2).
- **Niwa-owned levels**: `AGENTS.md` at the instance root and group
  directories, now written alongside `CLAUDE.md` rather than instead of it,
  each composing the layers above it (architecture below).
- **The trust bootstrap** (Decision 4): one
  `[projects."<repo root>"] trust_level = "trusted"` entry per cloned
  repository in the developer's Codex config, keyed by the canonicalized
  repository root, upserted idempotently under the stated write
  discipline, and nothing else written there.
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

Worktrees created by `niwa worktree` get the same two per-tree writes: the
`.codex` link with its target computed for the worktree's location, and a
composed override carrying the repository layer plus the worktree's own
framing — repository, purpose, branch — appended to it, matching the merge
a Claude worktree session gets (the worktree lifecycle installs the
repository content into the worktree first and then appends the framing,
rather than replacing anything).

In the developer's Codex config, per cloned repository (the path
canonicalized — symlinks fully resolved — per Decision 4):

```toml
[projects."<canonical absolute repo root>"]
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
one exists); a worktree's carries instance, group, the repository layer,
and the worktree framing appended last — the full set R4 names, not the
framing alone. A worktree is a checkout, so its copy of a committed
`AGENTS.md` is inlined exactly as a clone's is, under the same
regular-file-only rule. This is duplication by design: the walk stops at
the repository root, so the outer layers must travel inside the
per-repository file. The same rule implies the never-empty constraint
from Decision 2: a location with no content at any layer gets no file.

The layers are composed outermost-first, matching the direction Codex
itself concatenates when a chain has several files (root first, working
directory last), so a session that also picks up a committed context file
in a subdirectory reads one consistent general-to-specific document. The
choice has a cost worth stating: budget truncation eats the tail, which
under this ordering is the innermost layer — the one R7 protects — so the
declared budget is the sole defense rather than one of two. The reversed
ordering would degrade more gracefully under truncation, but at the price
of every composed file reading specific-before-general and disagreeing
with the order of Codex's own native chain; the design keeps the
consistent ordering and sizes the budget accordingly.

### Conflicts with committed content

The two names niwa writes into working trees are asserted free before
writing; detection is per name, with one coupling (the full rule and its
rationale are in Decision 7). A conflicting `.codex` degrades the whole
repository: no link, no override (the override's budget declaration lives
in the payload the refused link would have reached, so writing it anyway
would silently truncate under the default budget), and no niwa-written
trust entry — withheld, and removed if an earlier apply wrote one — so
niwa's signature never vouches for a payload it did not write. A
conflicting `AGENTS.override.md` alone suppresses only the override: the
link, exclude patterns, and trust entry still materialize, and the
repository's committed override carries the context slot. Both cases
modify and delete nothing and are reported per repository — and
"deletes nothing" extends into the pipeline's record-driven cleanup,
via a change the cleanup itself needs: it consults the apply's per-run
conflicted-path set and skips those paths before deleting; the entries
leave the record and the paths stay untouched (Decision 7). Ownership is
recognized by the link's target for `.codex` and by the generation
marker in untracked composed files — a content test, chosen so the
standalone worktree-apply path, which keeps no managed-file records, can
still recognize its own prior override. A committed `AGENTS.md` that is
not a regular file is a narrower case: the composer refuses only the
inline and still writes the override with the workspace layers, which
also displaces the symlink from the discovery slot (Decision 2). The
git-exclude patterns play no part in any of this — they suppress only
untracked paths and are inert for names a repository tracks.

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
the only defense. The sizing inputs are read at apply time, and committed
context files in repository subdirectories can grow between applies with
no signal; the headroom covers that window, and the next apply re-sizes.
Because the budget lives in the project config layer, it takes effect
only where trust resolves; the trust entries of Decision 4 are what make
the declaration reliable inside repositories.

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
`niwa worktree apply` does the same for a worktree. A dangling link is
harmless to Codex in the interim — it skips the layer without error — and
apply repairs any link whose target niwa owns, including a skills link
whose plugin root vanished after it was created. The gap apply cannot
close on its own is a plugin root that is still missing (a skipped or
failed install): there is no link then, because Decision 3 skips the
write, and its per-plugin report is the signal that something is absent
rather than repaired.

### What stays Claude-only

The `.claude/` settings tree, hooks, plugin/marketplace registration, and
the env pipeline are untouched and gain no Codex analogue in this design:
Codex reads none of those surfaces, and the payload writer is net-new
rather than a generalization of them (Decision 7B). The embedded
workspace-root project skills niwa writes at the workspace root also get
no Codex analogue — they drive niwa's Claude-only launch paths — so R8's
same-set guarantee is scoped to the plugin-delivered skills inside
instances, which is where the workspace's skills live. Sessions launched
at the instance root or a group directory get that level's composed
`AGENTS.md` and the skills (which load without trust), but no trust entry
— the interactive clean-start guarantee is scoped to repositories (R10),
and the consequence is recorded honestly below.

## Implementation Approach

Four batches, each independently landable, sequenced so the invariant
(Claude unchanged) is protected first and every later batch extends a
seam the previous one proved.

1. **Additive writes where re-parameterization suffices.** Run the
   workspace-root and instance-level writers once per agent — the two
   levels where the exclusivity really is in the callers and the existing
   writers produce both files unchanged (Decision 7A). Invert the
   exclusivity assertions exactly as Decision 7A enumerates them
   (including the one assertion that must not flip), retire the two dead
   accessors and their tests, re-narrate the dispatch refusal's comments
   and the inert launch-path agent assignments, adjust the `--agent`
   help text, and pin the R2 byte-identity check. This lands the
   architectural pivot with the smallest new write surface and proves
   R14 before any repository is touched.

2. **The composition engine and per-tree delivery.** The net-new Codex
   materializer: the layer composer and its three consumers (composed
   group `AGENTS.md`, per-repository `AGENTS.override.md` with
   committed-`AGENTS.md` inlining and the never-empty rule, and the
   worktree variant carrying the repository layer plus framing), the
   payload directory with budget-bearing `config.toml` and plugin
   symlinks including the per-marketplace-kind root resolution and
   missing-root reporting, the per-repository `.codex` link with the
   copy fallback, the conflict detection and loud reporting for occupied
   names, the regular-file-only read rule in the composer, and the
   git-exclude extension with bare patterns. Group composition lives
   here rather than in batch 1 because it is new composition sharing
   this batch's composer, not a re-run of the existing group writer.
   This batch carries the feature's highest-risk details (the bare
   pattern, the empty-file trap, the budget sizing, the refusal rules),
   so its tests are the silent-failure and hostile-content checks.

3. **The trust writer.** Idempotent, additive editing of the developer's
   Codex config: upsert per-repository entries, preserve everything else
   byte-wise, tolerate an unreadable credential file, and prove the
   no-accumulation and no-global-keys criteria — plus the write
   discipline Decision 4 states: atomic temp-file-and-rename
   replacement, refuse-and-report on an unparseable pre-existing file,
   and lock-serialized concurrent applies. Kept separate from batch 2
   because it is the only write outside the instance and deserves its
   own review focus; it consumes batch 2's per-repository conflict
   verdicts to withhold — or remove, when a conflict arrives after an
   entry was written — trust for conflicted repositories, so that
   behavior completes when both have landed.

4. **Worktree parity and docs.** Extend the worktree lifecycle with the
   per-tree writes, verify the marker and trust-inheritance behavior
   end-to-end, and add the guide entry this design's user-visible surface
   warrants (a `docs/guides/*` page covering what dual-agent preparation
   produces and what it writes where).

Batch 1 gates 2 — not because batch 2 reuses batch 1's writers (it is
net-new code beside them), but because batch 1 establishes the per-agent
pass in the apply pipeline that batch 2's writers ride, and lands the
test-suite inversions batch 2's assertions build on. 2 gates 4 (worktrees
reuse the per-repository composer and writers); 3 can land in parallel
after 1, with its conflict-driven withholding wired up once 2 is in.
Live-gated checks
(interactive start, first-write, post-session cleanliness) ride whichever
batch delivers their mechanism, following the repo's existing
skip-when-absent pattern for agent binaries.

## Security Considerations

- **niwa writes into a file it does not own.** The developer's Codex
  config is the one surface outside the instance this design touches. The
  write is bounded three ways: *scope* — only `[projects."<path>"]` keys
  whose paths resolve inside niwa-managed instances, never a global key,
  never a marker, never anything on the denylist or off it that changes
  behavior outside those paths; *additivity* — keys niwa did not write
  are never removed, reordered, or altered, and the edit must preserve
  the file's other content byte-wise (the PRD's criterion makes this
  testable); the only removals niwa ever performs are of its own
  entries, retracting a trust entry when its repository becomes
  conflicted — identified by the record niwa keeps of what it wrote,
  never by shape, since Codex writes identically-shaped entries when
  the developer answers its trust prompt (Decision 7);
  *credentials* — the credential and login files are never read or
  written, and preparation succeeds even when they are unreadable (R13);
  *discipline* — atomic temp-file-and-rename replacement so interruption
  never truncates the file, refusal (never a rewrite) when the
  pre-existing file does not parse, and lock-serialized read-modify-write
  so concurrent applies from multiple instances cannot drop each other's
  entries or corrupt the document (Decision 4). A stale entry after an
  instance is deleted is not fully inert: it trusts whatever later lands
  at that path. The paths are deep inside directories niwa manages, so
  the practical exposure is narrow, but removal on instance destruction
  belongs in the lifecycle as planned work rather than being waved off
  — with one ordering constraint now that removal is record-bounded:
  the trust-key record lives in instance state, the sole authority for
  what may be removed, so destruction must read the record before the
  instance directory goes.

- **Trusting a repository is a real grant, and its blast radius should be
  understood.** A trust entry does not merely silence a prompt: it
  enables project-local config, exec policies, and (were any declared)
  hooks to load from content inside that repository — including a
  `.codex/` directory committed anywhere in the repository's tree, since
  trust resolves transitively down the walk chain and the nearer layer
  wins on precedence. Third-party repository content thereby gains a
  voice in session configuration, and the worst case deserves a concrete
  name: sandbox and approval settings are not on Codex's project-config
  denylist and general config was measured to load from a trusted
  project layer, so a trusted repository's own committed config can
  loosen the sandbox or approval posture of sessions running inside it.
  The bounds are the denylist (a project layer cannot redirect provider
  URLs, swap model providers, or set notification commands), the fact
  that hook execution still requires user-config hash state niwa never
  writes, and the conflict rule above — which exists precisely so that
  niwa never grants trust where its own payload is not the thing being
  trusted. This grant is inherent to making sessions able to act (R9):
  the developer who clones a repository into a niwa workspace is
  expressing the same intent the trust prompt would have asked about,
  and the entry is scoped to exactly those paths. The design accepts
  this residual risk and states it rather than hiding it behind the word
  "trust".

- **Committed content at niwa's names is a conflict, never a
  substrate.** Two attack shapes were considered and closed by rules the
  design states where the writes happen. *Impersonation:* a repository
  arriving with its own `.codex/` at the name niwa symlinks could stand
  in for the payload while niwa's trust entry vouches for it; the
  conflict rule (Decision 7) skips the write, withholds the trust entry
  — removing one written before the conflict existed — and reports, so
  the repository's layer is never trusted on niwa's signature, not even
  by a stale entry from an apply that predates the conflict.
  *Disclosure through a committed symlink:* a repository committing its
  `AGENTS.md` as a symlink to a sensitive path could get the target read
  by niwa's composer and inlined into agent-visible context; the
  `O_NOFOLLOW` read rule (Decision 2) refuses the inline, and the
  override written without it displaces the symlink from the discovery
  slot, so Codex's own native read does not ingest the target either.
  The refusal is the unconditional defense; the displacement is a
  per-directory, content-conditional reinforcement — it covers the
  repository root's slot only when workspace content exists to compose,
  while a context symlink in a subdirectory keeps its own slot and
  stays Codex-native (Decision 2 states the bounds). The R13
  never-reads-credentials claim rests on the refusal. Neither
  requires a hostile actor — a careless upstream adopting Codex's own
  project-layer convention triggers the first — which is why both are
  handled by detection and loud refusal rather than by assuming good
  behavior.

- **The payload is reachable from inside repositories.** The `.codex`
  symlink makes the shared payload addressable from every repository
  working tree, including repositories containing third-party content an
  agent session may be processing. The payload contains no secrets by
  construction (project config and skill content only — Decision 6 keeps
  credentials out of every Codex-readable location), and it is
  regenerated from configured sources on every apply, so a tampered
  payload does not survive re-preparation. One refinement to that claim:
  regeneration heals the payload and its symlinks, not the symlink
  *targets* — the plugin install roots, which are shared across every
  instance and both agents, so tampering there persists until plugin
  content itself is reconciled. That healing belongs to plugin
  installation, and the exposure is shared with, not added to, the
  Claude side, which reads the same roots. A session induced to write
  through the symlink could modify skills mid-session; that exposure too
  is shared with the Claude side's materialized skill tree and is
  bounded by the same fact that nothing in the payload is executed by
  niwa itself.

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
- **A repository occupying niwa's names loses Codex delivery in
  proportion to what it occupies.** A committed `.codex` costs that
  repository everything: no link, no override, no trust entry — sessions
  there fall back to the repository's own content and Codex's own
  prompts. A committed `AGENTS.override.md` alone costs only the
  composed context: skills, payload config, and trust still arrive,
  while the repository's own override holds the context slot. A
  non-regular `AGENTS.md` costs only the inline: the override still
  delivers the workspace layers. Mitigation: each degradation is loud by
  design (a per-repository apply warning, and the trust prompt where it
  applies), confined to the conflicted repository, and the resolution is
  the developer's call — rename or remove the conflicting content, or
  accept the reduced delivery.
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
- **The design leans on two external layouts, not one.** The hardcoded
  first-match precedence of `AGENTS.override.md`, the bare-stat marker
  check that admits a worktree's `.git` file, and symlink-following in
  the project-layer loader are all pinned to codex-cli 0.147.0 by
  measurement; a future Codex release could reorder or tighten any of
  them silently. And the skills symlinks for github-sourced marketplaces
  point into Claude Code's user-global plugin cache, making a second
  external tool's directory layout load-bearing for R8 (Decision 3).
  Mitigation: every Codex reliance is on documented, upstream-tested
  behavior rather than accident; the acceptance criteria double as a
  regression harness; a cheap materialization-time smoke test (render
  the prompt in one repository, assert niwa's sentinel) can catch a
  drift the moment it ships; and a missing or relocated plugin root is
  reported loudly at apply rather than discovered as absent skills.
- **The payload is visible to anything that walks a repository.** The
  `.codex` symlink means a search, an indexer, or an agent's own grep
  that follows symlinks now sees the whole plugin payload from inside
  every repository, not just the security exposure already noted but a
  day-to-day functional one (noisy search results, surprising matches).
  Mitigation: the git-exclude entry keeps it out of git-aware tools,
  most search tools skip symlinked directories or honor ignore files by
  default, and the payload is plain text with nothing sensitive in it.
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
