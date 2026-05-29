# `niwa init --bootstrap`

`niwa init <name> --from <github-slug> --bootstrap` turns an empty (or
unconfigured) GitHub repository into a working niwa workspace in one
command. The flag fires when the source repo has no
`.niwa/workspace.toml` yet; niwa scaffolds a minimal config locally,
runs the create + session-create pipeline, and leaves you on a feature
branch ready to push.

Use it the first time you adopt niwa for a repository that doesn't
yet ship a niwa config. After the bootstrap branch is merged, the
ordinary `niwa init --from owner/repo` clone path takes over.

## At a glance

```bash
# Adopt an existing repo that has no niwa config yet.
niwa init my-project --from owner/my-project --bootstrap
```

When this succeeds you get:

- `<cwd>/my-project/.niwa/workspace.toml` — the scaffolded config
  (see [scaffold template](#scaffold-template) below).
- `<cwd>/my-project/.niwa/claude/.gitkeep` — placeholder so
  `.niwa/claude/` is committed.
- `<cwd>/my-project/<instance>/...` — the instance directory the
  create step produced.
- A worktree at
  `<cwd>/my-project/<instance>/.niwa/worktrees/my-project-<sid>/`
  checked out to branch `niwa-bootstrap/<sid>`.
- A single commit on the bootstrap branch authored by your normal
  git identity (NOT `niwa`), subject `Initial niwa workspace config`.
- A registry entry for `my-project` pointing at the new workspace.

The shell wrapper drops you into the worktree directory automatically
(via the same landing-path mechanism `niwa session create` uses). Push
the bootstrap branch when you're ready:

```bash
git push -u origin niwa-bootstrap/<sid>
```

## When the flag fires

`--bootstrap` is consulted only when the materialize probe reports
`*config.NoMarkerError` — i.e., the source repository was reachable
and HEAD has commits, but neither `.niwa/workspace.toml` nor the
legacy root-level `workspace.toml` exists.

The interactive table for that situation:

| TTY | `--bootstrap` | `--no-bootstrap` | Behavior |
|-----|---------------|------------------|----------|
| Yes | set | unset | Proceed without prompting. |
| Yes | unset | set | Fail-fast with NoMarker text + decline reason. Exit 4. |
| Yes | unset | unset | Prompt: `Remote has no .niwa/workspace.toml. Scaffold a minimal config and stage it on a niwa-bootstrap branch? [Y/n]`. Proceed on `y`/`Y`/bare Enter. Decline on `n`/`N` (exit 0). Re-prompt on anything else. |
| No  | set | unset | Proceed without prompting. |
| No  | unset | set | Fail-fast with NoMarker text + decline reason. Exit 4. |
| No  | unset | unset | Fail-fast: `remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold`. Exit 4. |

`--bootstrap` and `--no-bootstrap` are mutually exclusive; passing
both produces `--bootstrap and --no-bootstrap are mutually exclusive`
and exits 2.

## Bootstrap-only constraints

A few invariants are tighter under `--bootstrap` than under the
regular clone path:

- **GitHub-only.** The source host must be `github.com`. Non-GitHub
  hosts (GitLab, Gitea, file://, SSH to self-hosted git) refuse with
  `bootstrap supports only GitHub sources in v1; got host=<host>`
  and exit 3. This check runs BEFORE any git invocation.
- **No `--rebind`.** Bootstrap refuses registry-collision rather
  than silently rebinding. If `<name>` is already registered, the
  suggestion text points at `niwa destroy <name>`.
- **No `git push`.** Bootstrap commits locally; pushing is left to
  you so you can inspect the scaffold before sharing it.
- **No automatic `niwa apply`.** Bootstrap stops after
  session-create. You can run `niwa apply` later for drift checking.

## Branch-name format

The bootstrap branch is named `niwa-bootstrap/<sid>` where `<sid>` is
the 8-hex-character session id niwa just minted. The format is a
durable user-facing contract:

- It is stored in the session-state JSON under `branch_name`. Pre-schema
  state files without a `branch_name` field fall back to the legacy
  `session/<sid>` value via `EffectiveBranchName()`.
- Future tooling (e.g., an opt-in "open a PR for me" flag) can rely on
  the `niwa-bootstrap/` prefix to identify scaffolding branches without
  re-parsing commit metadata.

The 8-hex suffix is opaque to humans; it guarantees uniqueness across
re-invocations and parallel bootstraps. Workspace-name-based formats
were considered and rejected because they collide when a user re-runs
bootstrap against the same name.

## Visibility lookup (R17 soft-fail)

Bootstrap calls `GET /repos/{owner}/{repo}` to determine the
visibility group for the scaffold's `[groups.<vis>]` block:

- `Private: true` → `[groups.private]` with `visibility = "private"`.
- `Private: false` → `[groups.public]` with `visibility = "public"`.

The lookup reads ONLY the `Private` bool field from the API response.
The `Visibility` string field is intentionally ignored — this is a
security invariant against TOML-metacharacter injection from a
malicious GitHub API host.

When the lookup fails (network error, 401, 403, 404, 5xx) bootstrap
soft-fails: it defaults the scaffold to `[groups.public]` and emits a
single stderr note:

```
note: could not determine remote visibility (<cause>); defaulting to [groups.public]. Edit .niwa/workspace.toml to change.
```

`<cause>` is one of `network error`, `authentication`, `not found`,
or `server error`. The bootstrap chain continues. Edit
`.niwa/workspace.toml` after the fact if you need to switch to
`[groups.private]`.

## Success block (R19)

On full success bootstrap writes the following block to stderr,
preceded and followed by one blank line:

```
Workspace bootstrapped at:    <absolute-workspace-root>
Instance:                     <absolute-instance-root>
Worktree:                     <absolute-worktree-path>
Branch:                       niwa-bootstrap/<sid>

Next steps:
  1. Inspect the scaffold:        git show HEAD
  2. Push the bootstrap branch:   git push -u origin niwa-bootstrap/<sid>
  3. Merge to the default branch, then run `niwa apply` for drift checking.
```

Line ordering, label-to-value spacing, and the indentation under
`Next steps:` are part of the format contract. Tooling that parses
this block (e.g., editor integrations) can rely on byte-equality
after substituting the four path placeholders.

The landing-path file (the worktree absolute path) is also written
to `NIWA_RESPONSE_FILE` when the shell wrapper is sourced, so a
sourced `niwa init --bootstrap` leaves your shell inside the
worktree the same way `niwa session create` does.

## Failure surfaces

Adjacent failure modes route through a typed-error classifier so the
message is case-specific:

| Cause | Detail substring | Exit |
|-------|-----------------|------|
| Non-GitHub source | `bootstrap supports only GitHub sources in v1; got host=<host>` | 3 |
| HTTP 401 or 403 (auth) | `verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope` | 1 |
| HTTP 404 (typo / private / zero-commit) | `verify the slug is correct (org/repo)`, `if the repo is private, set GH_TOKEN`, `if the repo is brand new and has no commits yet` (all three substrings present) | 1 |
| Both `.niwa/workspace.toml` and root `workspace.toml` | `*config.AmbiguousMarkersError` text verbatim | 1 |
| Mutual exclusion (`--bootstrap` + `--no-bootstrap`) | `--bootstrap and --no-bootstrap are mutually exclusive` | 2 |
| NoMarker + non-TTY + no flag | `remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold` | 4 |
| NoMarker + `--no-bootstrap` | NoMarker text + decline reason | 4 |

## Stepwise rollback

Bootstrap chains three steps internally: init → create → session-create.
Each step's failure path leaves a different set of artifacts on disk
so you can retry from a real niwa command rather than starting over:

- **init step fails** (e.g., target dir already exists): stderr prefix
  `bootstrap step=init:`. The `<cwd>/<name>/` directory is removed if
  niwa created it; nothing else is touched.
- **create step fails** (e.g., clone failure during the create
  pipeline): stderr prefix `bootstrap step=create:`. The scaffolded
  `<cwd>/<name>/.niwa/workspace.toml` is preserved; the instance
  directory is removed. Retry with `niwa create` from the workspace
  root.
- **session-create step fails** (e.g., daemon-spawn timeout): stderr
  prefix `bootstrap step=session-create:`. The workspace AND the
  instance are preserved; the worktree and bootstrap branch are
  cleaned up. Retry with `niwa session create <repo> bootstrap`.

The rollback note printed alongside the prefix names the next
command verbatim so you don't have to look it up.

## Scaffold template

The scaffolded `.niwa/workspace.toml` has this exact body (substitute
the angle-bracketed tokens — niwa fills them in for you):

```toml
[workspace]
name = "<workspace-name>"
content_dir = "claude"

[[sources]]
org = "<source-org>"
repos = ["<bootstrap-repo>"]

[groups.<vis-key>]
visibility = "<vis-value>"
# Bind the bootstrap repo to this group by name: explicit-repos sources carry
# no live visibility, so name membership is what places the repo in a group.
repos = ["<bootstrap-repo>"]

# Bootstrap enabled mesh channels. Remove this block (and the [channels.mesh] line below) to disable.
[channels.mesh]

# CLAUDE.md content hierarchy: drop a workspace.md in .niwa/claude/ to populate.
# [claude.content.workspace]
# source = "workspace.md"

# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md
# for the full schema (claude.*, env.*, vault.*, files, instance).
```

Token mapping:

| Token | Source |
|-------|--------|
| `<workspace-name>` | Positional `<name>` arg, or slug repo basename (e.g. `--from owner/foo` → `foo`). |
| `<source-org>` | Owner from the `--from` slug. |
| `<bootstrap-repo>` | Repo from the `--from` slug. |
| `<vis-key>` / `<vis-value>` | `private` or `public` from the visibility lookup. |

The `[[sources]]` allow-list scopes the first apply to the bootstrap
repo only. Other repos in the same org are not cloned — edit the
scaffold after the fact to broaden the scope.

The `[channels.mesh]` block makes the workspace mesh-ready out of
the box. Remove the comment line and the block to disable.

The `.niwa/claude/.gitkeep` placeholder file (zero bytes) is written
alongside the scaffold so the content directory pushes cleanly when
you later uncomment `[claude.content.workspace]`.

## Related

- `docs/guides/workspace-config-sources.md` — full schema for the
  config file the scaffold produces.
- `docs/guides/sessions.md` — what the session-create step produces
  and how to navigate session worktrees.
- `docs/guides/functional-testing.md` — how the end-to-end Gherkin
  scenarios for this feature drive the GitHub fake and the PTY step.
