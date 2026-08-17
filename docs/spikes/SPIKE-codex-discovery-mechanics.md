---
schema: spike/v1
status: Complete
question: |
  How does codex-cli actually discover context files, project configuration, and
  skills, and which of those surfaces can niwa write into from a workspace
  instance without touching the developer's own Codex setup? Established so that
  any future dual-agent work starts from measured behavior instead of re-deriving
  it, since the mechanics are not documented upstream and two independent
  first-pass readings of them were wrong.
timebox: "Established across two implementation attempts; recorded here as durable findings"
---

# SPIKE: codex-cli discovery mechanics

## Status

Complete

## Why this document exists

niwa prepares workspace instances for coding agents. Preparing one for Codex
turns out not to be a filename problem: it is shaped entirely by how the binary
locates context and configuration, and that behavior is unintuitive enough that
two separate attempts to reason about it from the outside reached wrong
conclusions before measurement corrected them.

The first attempt concluded that instance- and group-level context would be
found by an upward walk, and built context materialization on it; sessions
running inside a cloned repository saw none of it. A later black-box probe
concluded the opposite — that only the working directory's own file is ever
read — because the probe directory contained no project-root marker, so no
project root was found at all. Both readings are wrong in different directions.

The findings below were established by live measurement against
`codex-cli 0.147.0` and by reading the matching upstream source at tag
`rust-v0.147.0`. They are recorded as findings, not as a design: the
implementation attempt that produced most of them was withdrawn for unrelated
structural reasons, and nothing here depends on that design being adopted.

## Findings

### 1. Discovery is a bounded downward walk

Codex locates a project root by walking up from the working directory to the
nearest ancestor containing a `project_root_markers` entry (default: `.git`),
then reads context and project configuration from that root **down** to the
working directory. It never walks above the root.

Every cloned repository has a `.git` at its root, so under default settings the
walk starts and stops inside the repository. Content placed above the
repository — at an instance root or a group directory — is never visited from a
session working inside that repository.

Two corollaries worth stating explicitly, because each one produces a
misleading experiment. A directory with no marker anywhere above it yields a
walk of one directory, which looks like "only the working directory is read". A
session started at a directory that *is* the project root reads that
directory's file, which looks like the upward walk working. Neither
observation generalizes.

### 2. One file per directory, strict first-match

Each directory in the walk contributes at most one context file, chosen by
hardcoded precedence: `AGENTS.override.md`, then `AGENTS.md`, then any
configured fallback filenames, in that order. The configured fallback list
cannot reorder this.

An empty or whitespace-only file still counts as a match. It claims the
directory's slot and suppresses every remaining candidate, so writing a file
that happens to compose to nothing is worse than writing nothing at all.

The practical consequence for any writer: `AGENTS.override.md` is the only
filename that wins in every repository. Writing `AGENTS.md` delivers nothing in
any repository that ships its own.

### 3. The context budget is shared and drains outermost-first

`project_doc_max_bytes` defaults to 32768 bytes. It is a single counter spent
across the whole chain in root-to-working-directory order, and truncation is a
raw byte cut: no marker in the text, nothing on stderr.

Because it drains outermost-first, under-declaring the budget costs the
innermost layer — the context closest to the work — with no signal that
anything was lost.

### 4. Writing files is gated on trust, and trust cannot be self-granted

A directory without a `[projects."<path>"]` entry in the developer's own Codex
configuration is a read-only sandbox, and the interactive TUI blocks on a trust
prompt. Trust cannot be granted from inside a project-level config layer: a
project config vouching for itself is ignored by construction.

### 5. The project config layer is real, but trust gates most of it

A `.codex/` directory found during the walk is a genuine configuration layer.
It carries instruction context, skills, general configuration including the
byte budget, and MCP server declarations.

Trust interacts with it unevenly, and this was measured rather than inferred:

- **Skills load from an untrusted layer.** A symlinked plugin tree at
  `.codex/skills/<plugin>` resolves with the same `<plugin>:<skill>` namespace
  Claude Code produces, with no rewriting of skill content.
- **Configuration keys require trust.** A 60KB context file was silently cut at
  the 32768 default in an untrusted directory despite the layer declaring a
  larger budget; adding a trust entry for the directory made the declared value
  take effect. MCP servers behave the same way: `[mcp_servers.*]` declared in a
  project layer appears in `codex mcp list` inside a trusted repository and is
  absent outside one.

So the byte budget and the trust entry are load-bearing for each other. A
budget declared for a directory that carries no trust entry does not apply.

What the project layer cannot carry at all: trust itself, project-root marker
configuration, hook trust state, marketplaces and plugin registration, and
eleven denylisted keys (provider URLs, `notify`, profiles, and similar).

### 6. `shell_environment_policy.set` is available in the project layer

Environment variables can be delivered to a session through
`shell_environment_policy.set` in the project config, which parses cleanly at
this layer. This is the route for anything a session needs in its environment.

### 7. Hooks are plugin-delivered and gated behind a blocking prompt

A loose `hooks.json` placed in a Codex home registers nothing. The working
route is a plugin carrying its own `hooks.json`, with per-plugin trust state
recorded as a `trusted_hash` under `[hooks.state]`.

An interactive session blocks on a review prompt for any hook it cannot verify
against that recorded hash. Any automated hook installation therefore either
solves the trust hash or accepts a modal in front of every session start. No
route that avoids both has been demonstrated.

### 8. Claude Code plugin manifests install into Codex unmodified

`codex plugin marketplace add` accepts a local path or `owner/repo` and reads a
`.claude-plugin/marketplace.json` verbatim. Plugins authored for Claude Code
install and their skills load with no changes to the plugin.

Two limits found alongside this. Plugin `agents/` directories are copied into
the plugin cache but never surface, so Claude-style named subagent types do not
exist under Codex. And for a marketplace sourced from GitHub rather than a
local path, the installed tree lives under Claude Code's own user-global plugin
directory — so anything resolving skills from that location inherits a
dependency on Claude Code being installed and having fetched the marketplace
first.

### 9. There is no per-directory config discovery outside the walk

A `.codex/config.toml` in a directory with no project-root marker above it is
not read. This is a direct consequence of finding 1 and is worth stating
separately because it is the specific way a naive experiment fails.

## What this rules in and out for a writer

Reachable from a workspace instance, without touching the developer's own Codex
configuration: instruction context (via `AGENTS.override.md` at each repository
root), skills (via a project-layer `skills/` directory), the context budget, MCP
servers, and environment variables — the last three only where a trust entry
exists.

Not reachable from the project layer: trust, marker configuration, marketplace
and plugin registration, and hook installation. Trust in particular has to be
written into the developer's own configuration or answered by the developer at
a prompt.

## Method

Measurements were taken against `codex-cli 0.147.0` on Linux, in an isolated
`CODEX_HOME` so the developer's real configuration was never modified, using
`codex debug prompt-input` to render the model-visible prompt and
`codex mcp list` to check server registration. Behavior was cross-checked
against upstream source at tag `rust-v0.147.0`.

These findings are version-specific. Re-verify them against the targeted binary
before relying on them; the discovery rules in findings 1 through 3 are the ones
most likely to change and the most expensive to get wrong.
