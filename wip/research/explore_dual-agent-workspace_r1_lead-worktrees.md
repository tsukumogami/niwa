# Lead: Do worktrees inherit the instance's CODEX_HOME or get their own?

## Findings

### 1. Where worktrees live and what niwa materializes into them

A worktree is created under `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` —
`internal/worktree/worktree.go:194-199`:

```go
// Worktree under <instanceRoot>/.niwa/worktrees/<repo>-<sid>/.
worktreesDir := filepath.Join(params.InstanceRoot, ".niwa", "worktrees")
...
wtPath := filepath.Join(worktreesDir, params.Repo+"-"+sid)
```

That's a sibling of `<instanceRoot>/<group>/<repo>` (the clone), not nested
inside it, but still fully inside the instance directory tree — confirmed by
`findRepoInWorkspace` (`worktree.go:119-147`), which only ever scans
`instanceRoot`'s two levels for the clone, and by the doc's filesystem-layout
section (`docs/guides/worktree.md:120-135`).

Two separate things get written into a worktree, by two different code paths:

- `worktree.CreateSession` (`internal/worktree/worktree.go:165-243`) creates
  the git worktree and scaffolds **only** `.niwa/sessions/` inside it
  (`scaffoldWorktreeNiwa`, `worktree.go:97-117`): "It does NOT create mcp.json
  or workspace-context.md — those are main-instance artifacts that are not
  needed in session worktrees."
- `workspace.ApplyToWorktree` (`internal/workspace/worktree_content.go:500-655`)
  does the content install, reusing the exact same installer functions the
  instance apply pipeline uses:
  1. `InstallRepoContentTo` — the repo's CLAUDE.local.md + subdir content
     (skipped entirely under an agent where `WritesRepoLevelContext()` is
     false, i.e. today's Codex — see below).
  2. `runRepoMaterializers` with `worktreeRepoMaterializers` (Hooks, Settings,
     Files — **not** Env) — writes a fresh `settings.local.json` into the
     worktree's own `.claude/` directory.
  3. `inheritEnvOutputs` — byte-copies the clone's already-resolved env file
     into the worktree (no secret re-resolution).
  4. `installWorktreeRulesImport` — writes
     `.claude/rules/worktree-imports.md` with an `@import` back to the
     instance root's `workspace-context.md` (+ overlay/global if present).
  5. `installWorktreeContextLayer` — appends a worktree-specific section to
     `CLAUDE.local.md` naming the repo, purpose, and branch.
  6. `runWorktreeHooks` — runs scripts from the config repo's
     `worktree-hooks/`.

The load-bearing comment explaining *why* step 4 exists
(`worktree_content.go:24-29`):

```go
// worktreeRulesFile is the per-worktree rules import file. A worktree, when
// launched as its own Claude Code project root, does not inherit the instance
// root's .claude/rules/ (rules load for the launched root only, not walked-up
// parents). So the worktree needs its own import pointing at the instance's
// workspace-context.md (and overlay/global where present).
```

This is the single most important fact for the lead: niwa already hit this
exact problem for Claude — "a nested project root doesn't automatically see
ancestor-level config" — and its answer was to **materialize a fresh pointer
into the worktree**, not to assume inheritance.

Deliberately NOT materialized into a worktree: `mcp.json`,
`workspace-context.md` itself (only imported by reference), and (verified via
`internal/agent/agent.go:80-86`) any repo-level context file at all when the
active agent is Codex — `WritesRepoLevelContext()` returns `false` for
`AgentCodex`, so under today's exclusive `default_agent = "codex"` mode,
`ApplyToWorktree` takes the early-return branch at
`worktree_content.go:501-505` and installs *only* the purpose/branch layer
call (which itself no-ops for Codex per `installWorktreeContextLayer`'s guard,
`worktree_content.go:740-742`). So today, a Codex-mode worktree gets nothing
repo-level at all — confirming this is genuinely open design space, not
something the prior increment already answered for worktrees.

### 2. What a worktree is FOR

The docs and design/PRD/brief documents are unambiguous: a worktree is the
**same workspace on a different branch**, not a separate context. From
`docs/guides/worktree.md:1-6`:

> A worktree is an isolated git checkout of one repo in your workspace, on its
> own branch, with the repo's CLAUDE content installed into it. You create one
> when you want a clean place to do a piece of work without disturbing the main
> checkout.

From the PRD's user story (`docs/prds/PRD-niwa-default-worktree.md:74`):

> As a developer, I want my agent's isolated checkout to have the same secrets
> and context as my real checkout without manual setup.

And `docs/guides/worktree.md:31-33`: "Step 3 onward is the work of
`workspace.ApplyToWorktree`, which reuses the same installers the instance
apply pipeline uses. A worktree and a repo checkout cannot drift, because
there is a single materializer path behind both."

So the isolation is about the **branch/filesystem**, deliberately not about
context or config — niwa goes out of its way (steps 1-6 above) to make a
worktree's environment, secrets, plugin config, and workspace context
identical to the instance's. The one thing that's genuinely per-worktree is a
short *addendum* naming the specific purpose/branch (step 5), spliced onto
otherwise-identical content, not a distinct configuration.

### 3. What would actually differ if a worktree got its own Codex home

Given #2, most of a Codex home's content would NOT differ between the
instance and a worktree: `config.toml` (marketplaces, plugins, MCP servers)
is sourced from the same workspace config regardless of which directory
niwa is materializing into (exactly as `runRepoMaterializers` reuses
`MergeOverrides(cfg, repoName)` for both clone and worktree paths, so no
drift is possible by construction — worktree_content.go:88, :601). `skills/`
and `auth.json`/plugin-cache symlinks would be identical too (per
lead-codex-home-layout's finding that those are shared with the host home
regardless of instance).

The one thing that **does** differ is exactly what differs for Claude today:
the purpose/branch layer. Claude's worktree gets this via
`installWorktreeContextLayer` appending a heading + purpose/branch body
directly into the worktree's own `CLAUDE.local.md`
(`worktree_content.go:721-776`), which Claude's cwd-relative discovery picks
up because Claude reads config from the launched project root.

Codex has no equivalent discovery to piggyback on. Per the exploration scope
doc (`wip/explore_dual-agent-workspace_scope.md:32-37`), "Codex does not walk
up the directory tree for AGENTS.md: exactly two sources load, the current
working directory's AGENTS.md and $CODEX_HOME/AGENTS.md." A worktree is a
distinct cwd from the instance root and from the repo clone. So:

- If a worktree **shares** the instance's `CODEX_HOME`, a Codex session
  launched with cwd = worktree sees `$CODEX_HOME/AGENTS.md` — the single
  composed file that's identical for every clone and every worktree in the
  instance, with no way to carry a worktree-specific purpose/branch
  addendum, because that same file is shared by every other worktree and the
  instance root simultaneously. Two concurrent worktrees on different
  branches for different purposes cannot both stamp their own addendum into
  one shared file without clobbering each other — this is qualitatively worse
  than the Claude analog, where the addendum lives in the worktree's own
  `CLAUDE.local.md`, not a shared file.
- If instead niwa wrote a worktree-cwd `AGENTS.md` directly (Codex's other
  read source), that reopens exactly the collision risk the current design
  already closed for Codex — `WritesRepoLevelContext()` exists specifically
  so niwa never writes an agent-context file into a cloned repo, because it
  "would risk clobbering the repository's own committed AGENTS.md"
  (`internal/agent/agent.go:80-85`). A worktree checks out a real branch of
  the same repo and can carry a committed `AGENTS.md` exactly like a clone
  can, so this risk is not smaller in a worktree.

That leaves a per-worktree `CODEX_HOME` (with its own composed AGENTS.md
carrying the purpose/branch addendum) as the only option that reaches parity
with what niwa already gives Claude, without reopening the collision guard.

### 4. The Claude-side precedent

Directly on point, and already answered in the existing code: Claude does
**not** get one shared settings file across the instance and all its
worktrees. Every worktree gets `settings.local.json` freshly materialized
into its own `.claude/` directory by the same `SettingsMaterializer` the
instance clone uses (`worktree_content.go:673-684`, run against
`RepoDir: worktreePath` at `:559`, `:569-589`). It also gets its own
`.claude/rules/worktree-imports.md` (step 4 above) precisely because — per
the code comment quoted in #1 — a launched worktree project root does not
inherit the instance root's `.claude/rules/` by directory walk. The pattern
Claude follows is: **materialize a full, fresh copy into the worktree using
the identical installer/config source as the instance, plus one small
worktree-specific addendum** — never "point the worktree at the instance's
single shared file and hope discovery finds it."

Codex's discovery model (two fixed sources, no walk at all, and unlike
Claude, no notion of "project settings" separate from `$CODEX_HOME` in the
first place) makes the case for a fresh, worktree-scoped materialization even
stronger than it was for Claude, not weaker: Claude's worktree-imports.md
trick (an explicit pointer file at the worktree root) works because Claude
does read *something* from the launched root. Codex's cwd read is limited to
one filename (`AGENTS.md`) with the exact collision risk described in #3, so
the "write a pointer file at the worktree root" trick that solved this for
Claude isn't safely available to Codex.

### 5. Practical mechanics

**If worktrees got a per-worktree `CODEX_HOME`:** there is a ready-made
materialization moment. `worktree.CreateSession` (git-level: worktree + branch
+ state) and `workspace.ApplyToWorktree` (content-level) are already two
distinct steps run back-to-back by every creation path — the CLI's
`niwa worktree create`, and the hook path `runFromHookCreate`
(`internal/cli/session_from_hook_cmd.go:120-166`), which calls
`worktree.CreateSession` then `applyContentToWorktree`. A Codex-home
materialization step is a natural third step alongside `ApplyToWorktree`,
reusing whatever composer/materializer lead-config-materialization and
lead-composed-context build for the instance level, parameterized by
`worktreePath`/`branch`/`purpose` the same way `installWorktreeContextLayer`
already is. `niwa worktree apply <id>` (the existing idempotent re-sync
command, `internal/workspace/worktree_content.go` doc at
`docs/guides/worktree.md:397-415`) is the natural place to re-sync it too —
it already re-syncs env and CLAUDE content on demand and on every
`niwa apply` fan-out (`docs/guides/worktree.md:59-66`).

**Delivery — the part that's actually hard, independent of the
inherit-vs-own answer:** niwa's shell integration is not an ambient,
cwd-triggered mechanism (no direnv-style hook that reacts to a bare `cd`).
`internal/cli/shell_init.go:52-71` wraps only specific `niwa` subcommands
(`create|destroy|go|init`, and `session create`) via a one-shot
`NIWA_RESPONSE_FILE` temp-file protocol that the Go side writes a target
directory into and the shell `cd`s to on exit
(`internal/cli/landing.go:13-45`, `shell_init.go:37-50`). There is no
mechanism today that would make an env var like `CODEX_HOME` automatically
change value just because the shell happens to be sitting in a different
subdirectory. This has two consequences for the lead, in both directions:

- If worktrees **inherit** a single instance-scoped `CODEX_HOME`: whatever
  delivery mechanism lead-codex-home-delivery designs for the instance root
  (exporting `CODEX_HOME` on `niwa create`/`niwa go`, most likely) reaches a
  worktree "for free" *precisely because* a worktree is inside the instance
  tree (#1) — a single env var value doesn't need to change per subdirectory,
  so no extra delivery work is needed. This is the strongest argument in
  favor of inheriting, and it's a real cost saving.
- If worktrees get **their own** `CODEX_HOME`: delivery needs to special-case
  the worktree entry points specifically — `niwa worktree create` and
  `niwa worktree attach` (which execs `claude --resume` directly today,
  `docs/guides/worktree.md:485-490`; a Codex analog would need to export the
  worktree's `CODEX_HOME` before exec). This is extra, but bounded: the same
  two commands are already the two places worktree-specific state is wired
  today (create writes the lifecycle state file; attach is the one place
  niwa itself launches an agent process).

**Surprise, found while checking this:** the shell wrapper's case dispatcher
literally does not have a `worktree)` arm — only `create|destroy|go|init`
and `session) case create)`. `niwa worktree create` (the canonical spelling
per `internal/cli/session.go:25-27`, where `worktree` is `Use` and `session`
is the deprecated `Aliases` entry) therefore does **not** go through
`__niwa_cd_wrap` today; only the deprecated `niwa session create` spelling
does. `docs/guides/worktree.md` documents cd-on-success under the canonical
`niwa worktree create` heading. Git history confirms this: the `session)`
arm was written by the original mesh-session design
(`docs/designs/archive/DESIGN-mesh-session-lifecycle.md:392-393`, "the
`session create` calls `__niwa_cd_wrap`") before `worktree` became the
canonical verb, and the case statement was never updated for the rename.
**Inferred from reading the code**, not run live — I did not source the
wrapper and test `niwa worktree create` in a real shell (the task instructed
me not to create/destroy worktrees in this session beyond what already
exists). This is a pre-existing bug independent of the Codex-home question,
but directly relevant to it: it's more evidence that per-worktree env
delivery needs deliberate engineering, not incidental cd-wrapping magic.

## Implications

Recommendation: **a worktree should get its own Codex home**, materialized
fresh from the same composer/config source as the instance's (so content
never drifts between clone and worktree, mirroring exactly how
`ApplyToWorktree` reuses `InstallRepoContentTo`/`runRepoMaterializers` today),
plus its own composed `AGENTS.md` carrying a purpose/branch addendum
analogous to today's `CLAUDE.local.md` worktree-context section. Auth and
plugin-cache symlinks would point at the same shared host-home targets the
instance's Codex home already uses (per lead-codex-home-layout), so a
worktree's Codex home costs no fresh login.

This is squarely the Claude precedent (#4), not a divergence from it: Claude
already answered "does a launched-root config that doesn't walk up ancestor
directories get inherited or freshly materialized?" with "freshly
materialized, every time, from the same source" — for `settings.local.json`
via the shared materializer, and via the explicit worktree-imports.md pointer
for the one thing Claude *can* read from the worktree root. Codex has an even
narrower discovery surface (fixed `$CODEX_HOME` plus exactly one cwd file,
`AGENTS.md`, with a real collision risk against a repo's own committed
`AGENTS.md` that niwa has already fenced off for clones), so "share one
`CODEX_HOME` across every worktree" is not a smaller version of what Claude
does — it's a materially different, and materially worse, contract: a single
composed file cannot carry N different concurrent worktrees' purpose/branch
context simultaneously, whereas Claude's per-worktree `CLAUDE.local.md`
naturally can.

What this costs, concretely: a new "materialize Codex home into worktree"
step alongside `ApplyToWorktree` in both the CLI create path and the
`from-hook` create path, reusing the instance-level composer; a re-sync path
alongside `niwa worktree apply`/the `niwa apply` worktree fan-out; and
worktree-specific `CODEX_HOME` delivery at the two commands where niwa
already launches or hands off an agent process for a worktree (`create`'s
shell cd-wrap, and `attach`'s direct exec). None of this is a new
architecture — it's the same "materialize per-instance, reuse across the
worktree with a small addendum" pattern the Claude worktree path already
established, extended one level.

What it does NOT cover, and needs lead-codex-home-delivery/
lead-config-materialization to close: exactly how `CODEX_HOME` gets exported
into a shell sitting in a worktree given the shell-init gap found above (both
the missing `worktree)` case-arm bug, and the absence of any ambient
cd-triggered mechanism in niwa generally); and whether `config.toml` itself
needs any worktree-specific content beyond `AGENTS.md` (e.g. does Codex have
a per-repo profile/permissions concept the way Claude's
`settings.local.json` "permissions" key is repo-scoped via
`MergeOverrides(cfg, repo)`? — worth checking, not investigated here).

## Surprises

- The shell wrapper's cd-dispatch case statement has no `worktree)` arm — only
  the deprecated `session create` spelling triggers `__niwa_cd_wrap`, despite
  `worktree` being the canonical command and the docs describing cd-on-success
  under the canonical heading. This is a leftover from the pre-rename mesh
  design and looks like a real, independent bug, not something this
  exploration needs to fix, but it's directly relevant here because it shows
  niwa's env/cd delivery mechanism is narrowly wired to specific literal
  subcommands rather than being general-purpose — exactly the kind of gap a
  per-worktree `CODEX_HOME` delivery mechanism would need to avoid repeating.
- Today, under the existing exclusive `default_agent = "codex"` mode,
  `ApplyToWorktree` installs essentially nothing repo-level into a worktree
  (`WritesRepoLevelContext()` is false for Codex, so both `InstallRepoContentTo`
  and the purpose/branch layer no-op). So there is no existing Codex-worktree
  behavior to preserve for backward compatibility — this is genuinely new
  ground, which simplifies the recommendation (no existing contract to break).

## Open Questions

- Exact shape of a per-worktree `CODEX_HOME`: full independent directory tree,
  or a thinner structure that symlinks/hardlinks everything except
  `AGENTS.md` and (if needed) a worktree-scoped slice of `config.toml` back to
  the instance's home? This affects disk cost when many worktrees exist
  concurrently and is squarely lead-codex-home-layout's + lead-config-
  materialization's territory to resolve, informed by this lead's answer.
- Whether `config.toml` needs any worktree-specific content at all beyond
  `AGENTS.md` (e.g., a repo-scoped permissions/profile analog to Claude's
  `settings.local.json` "permissions" key). Not investigated here.
- The concrete delivery mechanism for a worktree-scoped `CODEX_HOME` env var
  (shell-wrapper export at `worktree create`/`attach`, a `niwa codex` launcher
  reading worktree state, or something else) is owned by
  lead-codex-home-delivery; this lead only establishes that whatever is
  chosen must explicitly cover the two worktree entry points, and that the
  existing shell-wrapper case-arm gap needs fixing regardless.
- Whether the missing `worktree)` shell-wrapper case-arm should be fixed as
  part of this work or filed separately — it predates and is independent of
  the dual-agent effort, but any Codex-home delivery mechanism built on the
  cd-wrap protocol inherits the same gap if left unfixed.

## Summary

A worktree lives inside the instance's own directory tree
(`<instanceRoot>/.niwa/worktrees/...`), and for Claude, niwa's answer to "this
launched root won't automatically see ancestor config" was to freshly
materialize a full copy from the same config source plus a small
purpose/branch addendum — never to point at one shared file — which is exactly
the pattern a per-worktree `CODEX_HOME` should follow, since Codex's even
narrower cwd/AGENTS.md-only discovery makes a single shared home structurally
unable to carry more than one worktree's purpose/branch context at a time. The
main open item is delivery: niwa's shell integration has no ambient
cd-triggered env mechanism (and its cd-wrap case statement doesn't even cover
the canonical `niwa worktree create` spelling today), so whichever
`CODEX_HOME`-delivery approach lead-codex-home-delivery designs must
explicitly wire the two places niwa launches/hands off an agent for a
worktree — `create` and `attach` — rather than relying on directory-based
inheritance to "just work."
