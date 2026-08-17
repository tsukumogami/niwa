# Handoff to /scope: dual-agent-workspace

Produced by `/explore`. Twelve leads across four rounds, all measured live
against codex-cli 0.147.0, with several leads reading the matching upstream Rust
source (tag `rust-v0.147.0`) rather than reasoning from a stripped binary.

Full research is under `wip/research/explore_dual-agent-workspace_*`; accumulated
findings and the routing decision are in
`wip/explore_dual-agent-workspace_findings.md` and
`wip/explore_dual-agent-workspace_crystallize.md`.

## The problem

niwa prepares a workspace instance for exactly one coding agent. A prior
increment added an agent discriminator and a `[workspace] default_agent` key, but
built it as an **exclusive selector**: under `default_agent = "codex"` niwa writes
`AGENTS.md` at the niwa-owned levels *instead of* `CLAUDE.md`, skips every
repository and worktree write, and refuses background dispatch. A workspace is
prepared for Claude or for Codex, never both.

The goal is that after `niwa create` or `niwa apply`, a developer runs `claude`
exactly as today **and** runs `codex` in the same instance and gets the
workspace's context and skills in every session, from any working directory
inside the instance — with no agent choice at creation time.

## What the exploration established

**Materialization becomes additive.** The `CLAUDE.md` tree stays exactly as it
is; a Codex payload is written alongside it. The two agents do not collide —
different filenames, different discovery, different config files.

**No per-instance `CODEX_HOME` is needed.** This is the central correction to the
starting premise. Codex has a real project-level config layer: a `.codex/`
directory discovered by walking up from the working directory, carrying
instruction context, skills, config, and MCP servers. It cannot carry
marketplaces or plugins — but it does not need to, because skill namespacing
comes from the nearest `plugin.json` on disk and Codex deliberately follows
symlinks when resolving it.

Dropping the Codex home dissolves three problems at once: getting an environment
variable into every terminal and script (which niwa's shell integration provably
cannot do — it intercepts five `niwa` subcommands and never reacts to a plain
`cd`), deciding which home entries to share versus isolate, and the hazard of
symlinking the developer's credentials. niwa never touches the developer's auth.

**Codex does walk up the tree for context.** The "no upward walk" measurement in
the original brief was an artifact of testing outside a git repository. Discovery
walks from the nearest project-root marker down to the working directory, taking
at most one file per directory by strict first-match over `AGENTS.override.md`,
then `AGENTS.md`, then anything in `project_doc_fallback_filenames`.

## The chosen shape

Per cloned repo, under Codex's default `.git` project-root marker:

```
<instance>/.codex/                      # the payload niwa owns
<instance>/.codex/skills/<plugin>       # symlink to the whole plugin directory
<repo>/.codex -> <instance>/.codex      # symlink, one per cloned repo
<repo>/AGENTS.override.md               # composed instance + group + repo context
```

Plus, in the developer's Codex config, one
`[projects."<repo>"] trust_level = "trusted"` entry per cloned repo.

**Why per-repo rather than an instance-root payload.** The alternative repoints
`project_root_markers` at a niwa marker so the walk climbs past repo roots. It
works, but the setting *replaces* `.git` rather than extending it, and the nearest
ancestor always wins — so `.git` cannot be kept alongside it. Every repository on
the machine outside any niwa instance would lose discovery of its own `.codex/`
and `AGENTS.md`. niwa would be reaching outside its sandbox to degrade unrelated
work, and it would still need the same per-repo trust entries, so it buys only a
reduction in hook-state line count.

**Why `AGENTS.override.md`.** It outranks a repository's own committed
`AGENTS.md` and is the only slot that always wins. Relying on a
`CLAUDE.local.md` fallback instead would work for most repos and silently deliver
nothing for any repo shipping its own `AGENTS.md` — `public/shirabe` is such a
repo today, and any repo can become one at any time with no error and no signal.
Writing it unconditionally makes behavior uniform rather than dependent on what
each clone happens to ship. It never overwrites the committed file; it inlines it.

**Why whole plugin directories, verbatim.** niwa should perform no content
transformation — no frontmatter rewriting, no variable substitution.
`${CLAUDE_PLUGIN_ROOT}` is never expanded textually by Codex on any route (the
binary carries the string once, as an environment variable handed only to plugin
hook processes), so substitution buys nothing; it also corrupts self-referential
documentation that *describes* the variable, and misses the
`${CLAUDE_PLUGIN_ROOT:-...}` fallback form the plugins' own scripts already use.
Skills must ship as whole plugin directories because every reference points at
plugin-root `references/` and `scripts/` living above the skill — a detached skill
copy loses its namespace and orphans its references.

Verified against a real plugin: all 20 skills loaded, correctly namespaced, from
a project-layer payload with no Codex home and no content edits.

## Decisions with rationale that must reach a durable document

Everything under `wip/` is deleted before a PR merges. These decisions and their
reasons need a permanent home:

1. Per-repo payload under the default marker, not an instance-root payload with a
   repointed global marker.
2. `AGENTS.override.md` as the per-repo context filename, written unconditionally.
3. Whole plugin directories verbatim; no content transformation.
4. No `OPENAI_API_KEY` export — an exported key is **inert** when the subscription
   login works (a live session uses the login and never contacts the metered
   host), and a **silent metered fallback** when the login is broken, behind a
   green health check. Without the key that failure is loud. Pinning the auth mode
   does not rescue it: `forced_login_method = "chatgpt"` does not fail closed, and
   its sibling value `api` triggers an implicit logout that deletes the auth file.
   niwa must never emit that key.
5. The git-exclude extension **is** needed (for `.codex` and `AGENTS.override.md`),
   contrary to the original brief's expectation — but the collision guard is not,
   because `AGENTS.override.md` never overwrites a committed file.

## Traps that belong in acceptance criteria, not assumptions

Each of these fails silently, which is why they need explicit tests:

- **The git-exclude pattern must be the bare `.codex`, not `.codex/`.** niwa's
  existing idiom for `.niwa/` uses the trailing-slash form; copying it here leaves
  permanent dirt in every repo's `git status`. Highest-risk detail in the feature.
- **`project_doc_max_bytes` defaults to 32768, the budget is shared across the
  whole walk chain, and it drains root-first** — so an oversized upper layer
  starves exactly the per-repo layer. Truncation is a raw byte cut with no marker
  and nothing on stderr. It can be raised from the payload's own config.
- **An empty or whitespace-only context file claims its directory's single slot**
  and suppresses every remaining candidate. If there is nothing to say for a
  directory, write no file.
- **Project trust must be pre-written.** A missing `[projects."<abs path>"]` entry
  drops the session to a read-only sandbox where the agent cannot write files.
  Oddly, the recorded *value* does not matter — both `trusted` and `untrusted`
  yield a writable sandbox; only absence degrades.
- **A wrong or stale hook `trusted_hash` degrades in complete silence** — exit 0,
  no warning, no mention of hooks anywhere. The state key embeds the declaring
  file's absolute path, so the block must be regenerated from the instance's real
  path on every apply, never templated once.

## Hooks

niwa can pre-write a `trusted_hash` that Codex accepts with no prompt; this was
verified end to end by a `SessionStart` hook writing a marker during a real
session driven only by hand-written files. The hash is `sha256:` plus the hex
SHA-256 of a key-sorted, compact JSON rendering of a TOML-normalized hook
identity, and it was independently reimplemented and reproduced against all 13
hashes the shipped binary had written for a real installed plugin. Go's
`json.Marshal` already sorts map keys, which is the canonicalization Codex wants.

Hooks must be delivered as a loose `hooks.json`, never inside a plugin —
plugin-delivered hooks are stage `removed` and inert, which inverts the original
assumption but simplifies the design by decoupling hook injection from the plugin
machinery entirely.

**Whether hooks are in scope for this feature is a scoping decision for the
chain.** Nothing niwa ships today requires a Codex hook, and neither plugin in a
real workspace ships one. The mechanism is proven and can be deferred.

## Worktrees

A worktree should get its own composed context file and payload symlink, not a
shared one. The precedent is settled for Claude: when niwa hit the same problem —
a launched root that does not inherit ancestor config — its answer was to freshly
materialize from the same config source plus a small addendum naming the repo,
purpose, and branch. For Codex the argument is stronger: a single shared context
file cannot carry N concurrent worktrees' purpose/branch context at once.

There is no existing Codex-worktree behavior to preserve, so there is no contract
to break.

## Blast radius in the existing code

- The exclusivity lives in the **callers**, not the accessors. Each write site
  assumes it runs once with one resolved agent; going additive means running once
  per materialized agent, leaving the agent type alive as a pure launch-time
  selector.
- Three functional scenarios assert exclusivity directly; two of their assertions
  invert. Several unit tests assert "and the other file does not exist". One
  existing test already checks that the two context trees may coexist.
- The settings builder is Claude-shaped end to end — its keys are Claude API
  surface, not portable concepts. A Codex config writer is net-new, not a
  generalization of it.
- The dispatch refusal most likely needs no logic change (it is keyed on the
  launch-time selection, which stays coherent), but its comments narrate the old
  framing and would read as stale. Codex background dispatch stays out of scope.
- **`default_agent` has zero live adopters** — it is set nowhere in the reference
  config repo, nowhere in the private overlay, and nowhere in any workspace
  config. Backward compatibility is a non-issue in practice; no existing on-disk
  output changes.

## Adjacent, already tracked — do not absorb

- The secret table having no materializer is tracked as niwa#228. The exploration
  found it is wrong on a second axis nobody had noted: its destination is a
  settings file Codex never reads. Re-homing that table is explicitly deferred.
- The per-repo context gap when an agent crosses into a repo it did not start in
  is tracked as niwa#247. This feature incidentally improves the Codex side of it
  but does not close it.
- The shell wrapper's directory-change dispatch has **no `worktree)` arm** — only
  the deprecated `session create` spelling triggers it. A real pre-existing bug,
  found in passing, unrelated to this feature.

## Explicitly out of scope

Codex background dispatch; ephemeral session provisioning and the reaper's
liveness proxy; renaming or re-homing the existing config tables; any change to
the workflow-skills plugin or the orchestration engine, including named subagent
types, which do not exist under Codex.

## Known unknown

Whether the interactive TUI starts clean in a prepared directory was still being
verified when this handoff was written. Every piece of evidence gathered came
from non-interactive entry points, none of which render the startup trust path.
The mechanism has no alternative — trust entries are the only lever — so this does
not change the design, but it should land as an acceptance criterion and, if it
prompts, as a stated limitation.
