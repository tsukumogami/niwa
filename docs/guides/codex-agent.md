# Running Codex in a niwa workspace

niwa prepares a workspace for whichever coding agent you open it with. There's
no agent flag on `niwa create` or `niwa apply` and no per-machine setting to
pick one: every apply prepares both Claude Code and Codex, and you decide which
one to launch by launching it. Nothing you do for one agent takes anything away
from the other.

What that preparation amounts to isn't identical for the two, though, and this
guide says where the difference is. The short version: a Codex session inside a
cloned repository gets its orientation, its skills, its MCP servers, its
environment, and its approval posture. What it doesn't get is mostly Claude
Code harness surface — hooks and the features built on them — plus a few routes
niwa simply hasn't wired up yet.

## Choosing which agent niwa launches

The paragraphs above are about preparation. Launching is a separate question,
and it has its own answer: `default_agent`, on the `[workspace]` table of the
`workspace.toml` that drives your workspace.

```toml
[workspace]
name = "my-workspace"
default_agent = "codex"
```

Which copy of that file you edit depends on your workspace's config model, so
check before you touch it: `cat .niwa/.niwa-snapshot.toml` at the workspace
root. A marker naming a source repo means `.niwa/` is a snapshot — a plain file
tree materialized from that repo, no `.git` — and the durable edit belongs
upstream, in the source repo at the subpath the marker names. No such file, and
`.niwa/` is yours to edit in place. See
[workspace-config-sources.md](workspace-config-sources.md) for the whole model,
including a legacy shape that converts to a snapshot on its next command.

Editing a snapshot in place is the trap. The edit takes effect and then quietly
stops: niwa replaces `.niwa/` wholesale whenever it re-materializes from the
source, which can be the very next command and certainly is the one after the
source repo moves. Nothing warns you, and from the outside it reads like niwa
dropping a setting rather than like a file you changed being replaced. Going
upstream runs one command behind for this particular setting, too — `niwa
dispatch` resolves the agent before it refreshes the snapshot, so a value you
push lands on the dispatch after the one that pulls it. Run `niwa apply` once
after pushing, or use `NIWA_AGENT` for the run in front of you. An overlay is
no help either: `[workspace]` in an overlay is inert, so a `default_agent`
there is dropped without a warning.

Accepted values are `claude` and `codex`, and leaving it out means `claude`. To
override it for a single shell without editing the file, set `NIWA_AGENT`:

```bash
NIWA_AGENT=codex niwa dispatch "..."
```

The environment beats the config, and the config beats the `claude` default.
It's a workspace-level setting, with no per-instance form — every instance of a
workspace launches the same agent unless a shell says otherwise.

What it doesn't do is change what `niwa apply` prepares. Every apply prepares
the tree for every agent niwa supports no matter what `default_agent` says, so
changing it needs no re-apply and takes nothing away from the other agent. It
picks a launch target, and that's all it does. The one command that reads it
today is `niwa dispatch`, which uses it to decide which agent the background
worker runs as.

One name worth disambiguating: `niwa dispatch --agent` is a different setting.
It forwards a subagent type to the worker — a role inside the agent that gets
launched — and is dropped for an agent with no such flag. It never picks the
agent. `niwa create` and `niwa apply` have no `--agent` flag at all and reject
one as an unknown flag.

## What a Codex session gets

Everything up to background dispatch lands inside the instance, in the
repository you open the session in. Background dispatch is the one that starts
somewhere else, which turns out to matter.

**Orientation.** niwa composes the workspace-level and group-level context into
each repository's own `AGENTS.md`, because Codex reads context from the nearest
project-root marker downward and never visits the directories above a
repository. A linked worktree gets its own `AGENTS.override.md` — a worktree
root is a project root as far as Codex is concerned, since its `.git` pointer
file satisfies the marker check. The composed chain has a budget: Codex spends
32768 bytes across the whole chain by default and cuts the overflow off the
innermost layer with nothing in the text and nothing on stderr. niwa measures
the chain as it composes it, and where that chain needs more than the default it
declares a `project_doc_max_bytes` covering it — with headroom, so the bound
doesn't start truncating the first time a context file grows. Where the chain
fits, niwa writes no budget key at all and whatever you set for yourself stands.
Raising the budget is one of several things that only work in a trusted
directory (see below).

**Skills.** Workspace-declared plugin skills are delivered whole into
`.codex/skills/<plugin>`, where Codex resolves them to the same
`<plugin>:<skill>` names Claude Code produces. This doesn't depend on Claude
Code being installed — niwa fetches marketplace content itself rather than
reading it out of Claude Code's plugin directory.

**MCP servers, environment, and posture.** The workspace declares each of these
once, agent-neutrally, and niwa generates the format each agent reads. For
Codex that's a single project-layer `.codex/config.toml` carrying
`[mcp_servers.*]` entries, a `shell_environment_policy.set` table, the
`project_doc_max_bytes` above where one is needed, and — only if the workspace
declared them — `approval_policy` and `sandbox_mode`. Every
value is fully resolved before it's written; Codex expands nothing at load
time. The whole document is validated and re-decoded before any of it is
written, because one malformed entry fails Codex's entire config load and takes
its valid siblings with it. A validation failure reports the error and writes
no file at all, so a generated config can't brick a session.

**Directory trust.** The one thing niwa writes outside the instance. Codex
merges its trust record before any project layer exists, so nothing niwa plants
inside an instance can vouch for itself. Without a trust entry, a session in
that directory runs read-only and the project-layer config is never parsed —
which means no MCP servers, no environment, no posture, and no raised context
budget. The next section of this guide covers exactly what that write does and
doesn't touch.

**Dotenv files and distributed files.** These land on disk whoever opens the
session — the writers read no agent and have no agent-specific destination — so
a declared `.env` and anything in the `[files]`, `[instance.files]`, and
`[root.files]` tables are in front of a Codex session exactly as they are in
front of a Claude Code one. See `docs/guides/file-distribution.md`.

**Git-exclude coverage.** Every name niwa writes into a working tree is covered
by the repository's exclude block, so a prepared tree doesn't read dirty.

**Background dispatch.** `niwa dispatch` launches a Codex worker the same way
it launches a Claude one, with the same command and no agent flag — the agent
is whichever one `NIWA_AGENT` and your workspace's `default_agent` already
resolve to. It provisions a fresh instance, starts `codex exec` detached inside
it, recovers the session id from the session record Codex writes, and prints
`codex resume <id>`. It doesn't run that command for you, even without
`--detach`, and `--detach` makes no difference to a Codex dispatch: Codex won't
open a session while its turn is still running, and the worker has just started
one. So dispatch says the worker is going and leaves you the command, which
works from the moment the turn ends. The worker's own output is kept in the instance, at
`.niwa/dispatch-codex.out` and `.niwa/dispatch-codex.err`, which is where to
look when a run does something you didn't expect.

Three things are worth knowing before you rely on it.

The worker starts in the instance root, and everything the sections above
deliver lands inside the repositories below it — so a dispatched worker gets
none of it. No composed orientation, none of the workspace's skills, no MCP
servers, no posture from the project layer. Codex fixes what it reads when the
session is constructed, keyed to the directory it started in, and it doesn't
pick things up later from a directory you tell it to work in. Your own
user-level Codex skills still load, since those aren't part of what niwa
delivers; so does your environment, and so does the task you gave it, and the
repository files are all there to read. But a dispatched worker is briefed by
its prompt rather than by the workspace, so write the prompt accordingly.

A Codex run's exit status doesn't tell you whether the work happened. A worker
that couldn't write still exits 0, and an API failure of any kind — including
running out of quota — exits 1 alongside every other error. Read the last
message or the run's output rather than the exit code.

And a dispatched Codex instance isn't reclaimed automatically. `niwa reap`
reclaims an instance once its session's record is gone, and Codex never removes
those records, so there's no way to tell a session you finished with from one
you're coming back to. niwa spares it rather than guess, says so when it runs,
and leaves it to you: `niwa destroy <instance>` when you're done with the
session. That holds even for an instance whose dispatch died before it was
recorded, which reap otherwise cleans up on age — if a Codex session was
started there, reap can't tell whether that worker is still writing, so it
leaves the directory alone.

## What a Codex session doesn't get

<!-- BEGIN GENERATED: codex gap list (internal/agentplan/gaplist.go) -->

This list is generated from niwa's capability declarations in
`internal/agentplan/declaration.go`. If it and the code ever disagree, the
code is right and this section is the bug — a test fails until they match
again.

### What Codex can't receive

Codex's own mechanics put these out of reach. niwa could write something and
the session would never read it, so these move only if the agent changes.

- **Orientation for a session you start at the workspace or instance root.**
  Codex reads context only from the nearest project-root marker downward, and
  an instance root has none.
- **Registering a marketplace or plugin with the agent's own plugin system.**
  Registration lives in the developer's own Codex configuration rather than
  anywhere a workspace can declare it; skills are delivered directly instead.
- **Hooks, the lifecycle commands an agent runs on its own events.** No
  demonstrated route installs a niwa-owned hook for Codex without a blocking
  review prompt.
- **A written summary of the work a session did.** Work summaries are
  delivered as hooks, which Codex cannot receive.
- **Filling in a pull request's body from the session.** The pull-request body
  capture is delivered as a hook, which Codex cannot receive.
- **An instance provisioned automatically for a session niwa did not launch.**
  Provisioning rides a session-start hook and the harness job-state file;
  Codex has neither.
- **Instance-root skills such as `/dispatch`.** Root-installed skills serve
  root-started sessions, where Codex reads no configuration at all.

### What niwa hasn't built yet

A route exists on Codex's side and niwa hasn't wired it up. This is niwa's own
debt, and it's the one group that can shrink without the agent changing.

- **niwa's own plugin, which carries the migrate-config skill.** Codex accepts
  the identical plugin manifest; the wiring is unbuilt.

### What doesn't apply to Codex

Nothing is missing here. Each one names something that exists only in the
other agent's harness, so there's no delivery to make and none of them failed
to arrive.

- **Named subagent types the session can dispatch work to** doesn't apply to
  Codex. Codex caches a plugin's agents directory and never surfaces named
  subagent types.
- **Worktree-hook delegation and the deny fallback behind it** doesn't apply
  to Codex. Delegation is Claude Code harness surface; Codex has neither the
  events nor the tools it is built from.
- **Remote control of a session at startup** doesn't apply to Codex. The
  capability names claude.ai's remote-control bridge, which has no Codex
  equivalent.
- **Keeping a dispatched background session warm** doesn't apply to Codex.
  There is no Codex background-session bridge to keep warm.

<!-- END GENERATED: codex gap list -->

## Safety: what niwa writes outside the instance, and what it won't

This section answers a different question from the one above. The gap list is
about capabilities that don't reach a Codex session. This is about the blast
radius of the one file niwa edits that it doesn't own, and about two measured
Codex behaviors that can surprise you. None of these are gaps, and none of them
are on the list above.

### The single entry in your own Codex configuration

niwa writes exactly one thing into `~/.codex/config.toml` (or `$CODEX_HOME`, if
you've set it): one `[projects."<path>"]` block per cloned repository, carrying
`trust_level`. That's the whole of it.

What niwa will not write there:

- **No hooks.** Codex can't receive a niwa-owned hook without a blocking review
  prompt, and niwa doesn't install one anyway.
- **No API keys or credentials.** Nothing in the trust writer opens `auth.json`
  or any other login file. An unreadable one can't fail an apply, because it's
  never read.
- **No global keys.** Only whole `[projects."<path>"]` blocks are ever appended.
  Nothing at the top level of your config is added, removed, reordered, or
  rewritten.
- **No edits to anything niwa didn't write.** Your existing bytes are copied
  verbatim and the new blocks go after them. When niwa retracts a trust entry,
  it goes by its own record of what it wrote, never by the entry's shape —
  Codex writes an identically-shaped block when you answer its own trust
  prompt, and a shape test would delete your answer.

Two more properties worth knowing. The edit is atomic: the new document is
written beside the config, fsynced, and renamed over the original, so an
interrupted apply leaves the previous file whole. And a config niwa can't read
or can't parse is left byte-untouched and reported as a warning — replacing it
would discard content that isn't niwa's to discard, and failing over it would
make one broken file break every create and apply.

### Codex's default environment excludes are off, and niwa leaves them that way

Measured against codex-cli 0.147.0: `ignore_default_excludes` defaults to
`true`, which means Codex's own `*KEY*` and `*TOKEN*` exclude patterns are **not
applied**. A Codex session's commands inherit key- and token-named variables
from the parent environment unless you opt in.

That's Codex's default, not something niwa does. niwa deliberately doesn't write
that key. Setting it to `false` would drop variables you never asked niwa to
touch, and it would protect nothing niwa delivers: the measured pipeline is
inherit, then exclude, then `set`, then `include_only`, so values niwa writes to
`set` survive the exclude stage either way.

If you want the excludes on, that's your call to make in your own config:

```toml
[shell_environment_policy]
ignore_default_excludes = false
```

Measurement caveat: this was measured through `codex sandbox`, Codex's
user-invoked sandbox entry point. It has not been confirmed from inside a live
session's shell tool. The two are believed identical, but if you're about to
rest a security decision on the default, measure it in the shape you care about
first.

### An `include_only` allowlist silently drops what niwa delivers

If your own Codex configuration sets `shell_environment_policy.include_only`,
it runs last — after the `set` table niwa writes. Any workspace-declared
variable whose name isn't in your allowlist is dropped, and Codex reports
nothing. The session just doesn't have it.

There's no way for niwa to detect this from its side, so if you use
`include_only` and a workspace-declared variable seems to be missing, that's
the first place to look.

## Regenerating the list

The "doesn't get" section comes from the declaration table in
`internal/agentplan/declaration.go`, which records for every capability and
every agent whether niwa delivers it and, when it doesn't, why. Flip a row
there and the section is out of date. Don't edit it by hand — regenerate it:

```
go test ./internal/agentplan -run TestCodexGuideGapSectionMatchesDeclarations -update
```

The same test without `-update` fails when the committed section and the table
disagree, which is what keeps this document from drifting into describing a
version of niwa nobody is running.
