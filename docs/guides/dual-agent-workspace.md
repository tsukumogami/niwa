# Dual-agent workspaces

Every instance niwa prepares serves both Claude Code and OpenAI Codex. There
is no agent choice at creation time and nothing to configure: `niwa create`
and `niwa apply` materialize the Claude tree exactly as they always have and,
alongside it, everything a Codex session needs. You run `claude` as before,
and you run `codex` in the same instance — from the instance root, from a
directory nested deep inside a cloned repo, or from a niwa-managed worktree —
with no environment variable, no wrapper command, and no per-repo setup step.
This is about deferring the choice rather than running the two side by side:
you pick an agent when you start a session, and you can pick the other one next
time without re-provisioning anything.

The Claude side is untouched by construction: dual-agent preparation adds a
second reader, it does not rework the first. What a Claude session sees is
byte-for-byte what it saw before.

## What a Codex session gets

A Codex session started anywhere inside a prepared instance sees the layered
workspace context for where it's standing — instance, group, repository, and
in a worktree the worktree's own framing — plus the workspace's skills under
the same names a Claude session resolves. The session can write files
immediately: no trust prompt, no read-only sandbox, no setup command first.

This works without preparation of the session's environment because of how
niwa places the content. Codex discovers project configuration by walking up
from the working directory to the nearest `.git` and reading downward from
there. It never looks above a repository's root, so niwa puts everything a
session needs at or below that boundary. Two mechanisms carry it:

- A `.codex` symlink at each repository root points at a payload directory
  niwa maintains at the instance root. Codex follows the link and finds the
  project config and skills there, from the repo root and from any
  subdirectory.
- A composed context file in each repository carries the full chain of
  workspace layers inside it, so no layer depends on Codex reading a file
  above the repository.

Neither requires anything from you or your shell. A terminal opened by hand,
a script, a Makefile target, and a non-interactive SSH command all work the
same way.

## What niwa writes and where

After `niwa create` or `niwa apply`, an instance looks like this on the
Codex side (the Claude files are unchanged and omitted here):

```
<instance>/
  .codex/                          # the payload niwa owns
    config.toml                    # declares the context byte budget
    skills/<plugin> -> <install root>   # one symlink per configured plugin
  AGENTS.md                        # instance layer
  <group>/
    AGENTS.md                      # instance + group layers
    <repo>/
      .codex -> ../../.codex       # symlink to the payload (or a real copy)
      AGENTS.override.md           # instance + group + repo layers, composed;
                                   # inlines the repo's committed AGENTS.md
      .git/info/exclude            # managed block gains ".codex" and
                                   # "AGENTS.override.md"
```

Some details behind that picture:

- **Every composed file carries the full chain.** Codex reads context only
  from the repository root down, so the instance and group layers travel
  inside every repository's `AGENTS.override.md` rather than being read from
  the directories above. The same rule shapes the niwa-owned levels: a
  group's `AGENTS.md` carries instance plus group, not the group alone.
- **Every composed file opens with a generation marker.** The first line
  says the file is niwa-generated and rewritten on every apply. Edits you
  make to a composed file are lost at the next `niwa apply`; put durable
  content in the workspace config sources instead.
- **`AGENTS.override.md` is the filename because it wins.** Codex picks at
  most one context file per directory, by fixed precedence, and
  `AGENTS.override.md` outranks a repository's own `AGENTS.md`. Writing any
  lower-precedence name would work in most repositories and silently deliver
  nothing in any repository that commits an `AGENTS.md` — a failure with no
  error and no signal, which niwa refuses to ship.
- **A committed `AGENTS.md` is inlined, never replaced.** Because the
  override displaces the repository's own file from discovery, niwa reads the
  committed `AGENTS.md` at apply time and inlines its content into the
  composed override. The committed file itself is never modified. One
  consequence: edits to a repo's `AGENTS.md` reach Codex sessions after the
  next `niwa apply`, not immediately. And one safety rule: niwa reads that
  file only as a regular file — an `AGENTS.md` committed as a symlink is
  refused, reported, and not inlined, while the workspace layers still
  arrive.
- **No content means no file.** An empty context file would still claim the
  directory's single context slot and suppress the repository's own
  `AGENTS.md`. So when the workspace has nothing to say for a location, niwa
  writes no override at all and the repository's own content reaches the
  session undiminished.
- **The payload's `config.toml` declares the context byte budget.** Codex
  spends one `project_doc_max_bytes` counter (default 32768 bytes) across the
  whole context chain and truncates silently when it runs out — the innermost
  layer, the one for the directory you're standing in, is what gets cut.
  niwa sizes the declared budget to the largest composed chain in the
  instance with generous headroom, so every layer arrives whole.
- **Skills are symlinked whole, plugin by plugin.** Each configured plugin's
  installed root is linked into `.codex/skills/`, verbatim: no content
  rewritten, no file added or omitted, and each skill resolves under the
  same `<plugin>:<skill>` name Claude uses. A plugin whose install root is
  missing at apply time (a skipped or failed install) gets no link; the
  apply reports the plugin and the path it expected, and the next apply
  creates the link once the root exists.
- **Repositories stay git-clean.** The managed block in each repo's
  `.git/info/exclude` gains the two names niwa writes, so neither the link
  nor the override ever shows in `git status`. On platforms where directory
  symlinks are unavailable, the `.codex` link is a real copy of the payload
  instead; the exclude patterns cover both forms.

`niwa apply` refreshes all of this: it recomposes every `AGENTS.md` and
`AGENTS.override.md` from the current config sources and each repo's current
committed `AGENTS.md`, rewrites the payload config, reconciles the skills
links against the configured plugin set, and repairs a deleted or retargeted
`.codex` link. Regeneration, not append — nothing accumulates across applies.

## What niwa writes into your Codex configuration

Codex treats a directory without a trust entry as a read-only sandbox, and an
interactive session there blocks on a trust prompt. Trust cannot be granted
from files inside the repository — a project vouching for itself is ignored
by design — so this one piece has to live in your own Codex config
(`$CODEX_HOME/config.toml`, default `~/.codex/config.toml`).

niwa writes exactly one entry per cloned repository, and nothing else:

```toml
[projects."<absolute repo root>"]
trust_level = "trusted"
```

The key is the repository root with every symlink in the path resolved, so
the entry matches what Codex looks up regardless of how your home directory
or workspace root is mounted. The entry also covers every niwa-managed
worktree of that repository — Codex resolves a worktree back to its main
repository — so no per-worktree entries are written.

Because this is the one file niwa edits that it does not own, the write is
deliberately careful:

- **Additive and scoped.** Only per-project keys whose paths sit inside niwa
  instances are touched. Your own settings are never removed, reordered, or
  altered, and no global key is written. Codex behaves exactly as before in
  every repository outside a niwa instance.
- **Atomic.** The edited file is staged beside the original and renamed over
  it, so an interrupted apply leaves your previous config intact rather than
  a truncated one.
- **Refused when unreadable.** If your existing config fails to parse, niwa
  leaves it byte-untouched, finishes the rest of materialization, and exits
  non-zero naming the file — it never "repairs" a file it could not read.
- **Serialized.** Concurrent applies from multiple instances take a lock
  before editing, so they cannot drop each other's entries.

niwa records each trust key it writes in instance state, and only keys in
that record are ever removed (see conflicts below). Your Codex credentials
and login state are never read or written; preparation succeeds even when
the credential file is unreadable. You authenticate Codex yourself, once,
however you choose.

One scope note: the trust entries cover repositories, where work happens. A
session started at the instance root or a group directory still gets the
composed context and the skills (which load without trust), but sees Codex's
trust prompt once — your answer there is Codex's own entry, and niwa leaves
it alone.

## When a repository already uses niwa's names

niwa writes two names into a working tree: `.codex` and `AGENTS.override.md`.
A repository can legitimately commit content at either — `.codex/` is
Codex's own project-config convention, and any upstream can adopt it. niwa
treats committed content at either name as a conflict: it writes nothing at
that name, modifies and deletes nothing, and prints a per-repository warning
in the apply output. This is a deliberate refusal, not a failure — the
alternative is overwriting content the repository ships, or trusting content
niwa did not write.

The two names degrade differently:

- **A committed `.codex` costs the repository everything.** No link, no
  composed override, and no trust entry. The override depends on the byte
  budget declared inside the payload the refused link would have reached, so
  writing it anyway would silently truncate; and a trust entry would put
  niwa's signature on the repository's own committed `.codex/` content. If
  an earlier apply wrote a trust entry before the conflict appeared, the
  next apply removes it. Sessions in that repository fall back to Codex's
  native discovery of the repository's own content, and Codex's own trust
  prompt decides trust — your answer to that prompt is yours, and niwa never
  touches it.
- **A committed `AGENTS.override.md` alone costs only the composed
  context.** The `.codex` link, the exclude patterns, and the trust entry
  still materialize, so skills and the payload config still reach sessions
  there; the repository's own override holds the context slot.

To resolve a conflict, rename or remove the conflicting content in the
repository (or accept the reduced delivery). The next `niwa apply` detects
the cleared name and writes fresh files, and a withheld trust entry comes
back with them. niwa recognizes its own writes — the link by its target, a
composed file by the generation marker on an untracked file — so its own
output from a previous apply is never mistaken for a conflict.

## Worktrees

Worktrees created by `niwa worktree` are first-class for Codex, the same as
for Claude. Each worktree gets the same two per-tree writes a clone gets: a
`.codex` link with its target computed for the worktree's location, and a
composed override carrying the instance, group, and repository layers plus
the worktree's own framing (repository, purpose, branch) appended last, with
the checkout's committed `AGENTS.md` inlined under the same rules.
`niwa worktree apply` refreshes both, and a workspace-wide `niwa apply`
reaches every worktree in the same pass. No separate trust entry is needed:
the repository's entry covers its worktrees.

## What is deliberately not written

niwa never writes any of the following, and each absence is a decision:

- **No Codex hooks and no hook state.** An interactive Codex session blocks
  on a review prompt for any hook it cannot verify, so injected hooks would
  put a blocking modal directly in the path of the clean start this feature
  exists to deliver — and nothing niwa ships today needs a Codex-side hook.
- **No API key and no auth keys.** With a working Codex login an exported
  `OPENAI_API_KEY` is inert; with a broken login it silently becomes a
  metered fallback behind a green health check, turning a loud failure into
  a quietly billed one. Leaving the key unbound keeps the failure loud. The
  secret-binding table's own issues are tracked as niwa#228. No
  auth-related key of any kind appears in any file niwa generates.
- **No global Codex keys.** Nothing niwa writes changes how Codex discovers
  configuration or behaves outside niwa instances — no root-marker changes,
  no defaults, nothing beyond the path-scoped trust entries above.
- **No credentials.** Codex's credential and login files are never read or
  written.

Two adjacent limits, so they don't read as gaps: `niwa dispatch` remains
Claude-only — the background-dispatch pipeline is built around Claude Code
and refuses a Codex-default workspace as before. And the per-workspace
`default_agent` setting keeps only its launch-time meaning (which agent a
niwa-launched session runs); it no longer affects what preparation produces,
and workspaces that declare it — or predate it — work with no migration.
