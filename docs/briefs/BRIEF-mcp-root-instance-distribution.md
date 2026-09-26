---
schema: brief/v1
status: Done
problem: |
  niwa can distribute files into the repos it manages, but it cannot place a
  verbatim-named, non-gitignored file at the two non-repo levels of a workspace
  -- the workspace root and each instance root. The one [files] materializer
  runs per-repo and forces a .local infix, and the [instance.files] field that
  looks like the instance-root equivalent is parsed but never written. So a tool
  config that must keep its exact name, like a Claude Code .mcp.json, cannot be
  delivered to a session started at a workspace root or instance root.
outcome: |
  A workspace author declares a file once in workspace config and niwa
  materializes it verbatim -- exact filename, no .local infix -- at the
  workspace root and at every instance root. A developer who starts a session
  at either level finds the file already in place, so a configured Claude Code
  MCP server is available without per-instance hand-copying.
motivating_context: |
  An attempt to ship a Claude Code MCP config (.mcp.json) to a multi-repo niwa
  workspace found the existing mechanisms cannot do it: [files] rewrites the
  name to .mcp.local.json and only targets repos, and [instance.files] produces
  no file at all. The non-repo levels are not git working trees, so the .local
  rationale that justifies the per-repo rewrite does not apply to them.
---

## Status

Done

Authored as the BRIEF in a BRIEF -> PRD -> DESIGN -> PLAN chain for the
mcp-root-instance-distribution feature. The downstream PRD owns the
requirements; the DESIGN owns the config surface and materialization wiring.
This brief stops at framing.

## Problem Statement

niwa materializes configuration into the things it manages at several levels:
the repos it clones (per-repo context, settings, env, hooks, and a general
`[files]` copy mechanism), and the two non-repo container levels above them --
the workspace root and each instance root -- where it writes workspace context
and settings. The `[files]` mechanism is the one general "copy this file to
that place" surface, but it runs only in the per-repo materializer set and
rewrites every destination to carry a `.local` infix (`config.json` becomes
`config.local.json`). That rewrite is deliberate for repos: a managed repo's
working tree gitignores `*.local*` so niwa's output stays invisible.

The two non-repo levels have no equivalent working surface. The workspace root
has no file-distribution table in the schema at all. The instance root has one
that looks like the answer -- an `[instance.files]` field -- but it is dead:
the config is parsed and merged, yet neither code path that materializes the
instance root ever reads it, so it produces no file.

Both levels are also not git repositories: the workspace root and instance root
hold managed repos as subdirectories, each with its own `.git`, while the
container directories themselves are not tracked. The `.local` infix exists to
satisfy a repo's gitignore; at these non-repo levels there is no such gitignore
to satisfy, so the rewrite only corrupts the one thing some files cannot
tolerate losing -- their exact name. Tools that load configuration by a fixed
filename, such as Claude Code reading `.mcp.json` from the directory a session
starts in, need that name preserved. Today niwa cannot deliver such a file to a
workspace root or instance root, so a workspace-wide tool config has to be
hand-placed in every instance and re-placed every time an instance is created.

## User Outcome

A workspace author declares the file once, in workspace config, and niwa puts
it where it said -- at the workspace root and at every instance root -- with the
exact filename the author wrote, no `.local` infix inserted. The author does not
hand-copy the file into each instance and does not re-copy it when a new instance
is provisioned; declaring it once is enough, and niwa keeps it in place and
tracks it like any other managed file.

A developer who opens a session at a workspace root or an instance root finds
the file already there under its real name. For the motivating case, that means
a configured Claude Code MCP server is present and usable in that session
without any per-instance setup. The author controls which file lands and where
through config alone; the verbatim name is what makes the delivered file
actually do its job.

## User Journeys

### A workspace author ships an MCP config to every instance root

An author wants every instance in a workspace to expose the same Claude Code
MCP server. They add one file-distribution entry to workspace config naming a
source file and a verbatim destination of `.mcp.json` at the instance-root
level, then run niwa. Each existing instance root gets a `.mcp.json` with its
exact name, and every instance created afterward gets the same file as part of
being set up. The author never copies the file into an instance by hand.

### A developer starts a session in a freshly provisioned instance

A developer is handed a new instance and starts a Claude Code session at its
root. Because niwa materialized `.mcp.json` verbatim at the instance root when
the instance was provisioned, the session loads the configured MCP server
immediately -- the developer does nothing to enable it and never sees the file
under a mangled `.mcp.local.json` name that the tool would not read.

### A workspace author distributes a root-level file across the workspace

An author wants a single file present at the workspace root that the existing
schema cannot express, because no workspace-root file table exists. They add a
workspace-root file-distribution entry, run niwa, and the file appears at the
workspace root under its exact name -- the same verbatim, non-gitignored
delivery the instance-root level provides, now reachable for the root level too.

## Scope Boundary

### In

- Verbatim file distribution -- exact filename, no `.local` infix -- at the
  workspace root and at every instance root, driven declaratively from
  workspace config.
- Activating the instance-root file surface (the currently-dead
  `[instance.files]`) so it actually materializes files, and adding the
  workspace-root equivalent the schema lacks.
- Wiring materialization into both non-repo paths -- the workspace-root
  materialization path and the instance-root materialization path -- so a
  declared file lands at each level on apply and on instance creation.
- Preserving managed-file state and drift tracking for these files, the same way
  other niwa-managed files are tracked.
- Keeping destinations at the project root of each level, not inside protected
  directories like `.claude/` or `.niwa/`.

### Out

- Per-repo `.mcp.json` distribution and any change to the per-repo `[files]`
  `.local` infix. The repo-level rewrite stays exactly as it is; this feature
  adds non-repo levels, it does not redefine the repo level.
- Solving coverage for sessions started inside a repo subdirectory. Claude Code
  loads `.mcp.json` from the start directory only, with no walk-up to an
  ancestor, so a session begun inside a managed repo is not reached by a
  root-level or instance-root file. This limitation is accepted and named; the
  PRD records it rather than solving it.
- The exact config schema shape (table form, field names) and the
  materialization wiring details -- those are PRD and DESIGN territory, not
  framing.
- Whether niwa must additionally emit a setting so a project MCP server loads
  without a per-session trust prompt. This is flagged for the DESIGN to decide;
  the brief does not commit to it.
