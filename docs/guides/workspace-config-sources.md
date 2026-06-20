# Workspace Config Sources

A walkthrough for configuring where niwa fetches workspace
configuration from, the discovery rules that resolve a slug to a
location inside the source repo, and the snapshot model that
materializes the config on disk.

> **Implementation status (April 2026)**: the foundation packages
> (`internal/source`, `internal/testfault`,
> `internal/workspace/snapshot.go`,
> `internal/workspace/provenance.go`,
> `internal/github/{fetch,tar}.go`) and the v3 state schema +
> registry mirror fields are in place. The apply-pipeline rewrite
> that wires these together replaces today's `git pull --ff-only`
> code paths in `configsync.go`/`overlaysync.go`/`init.go`; that work
> is the remaining scope of PR #73 and lands in follow-up commits.
> User-facing behavior described below reflects the eventual contract
> per [DESIGN-workspace-config-sources.md](../designs/DESIGN-workspace-config-sources.md).

## What you get

- **Subpath sourcing.** A workspace config can live in any
  subdirectory of any git repo, not just at the root of a dedicated
  `dot-niwa` repo. `niwa init --from org/brain-repo:.niwa` resolves
  to the `.niwa/` subdirectory inside `org/brain-repo`. The whole-
  repo case is the degenerate `subpath = "/"` form, so existing
  `org/dot-niwa` registries continue to work unchanged.
- **Snapshot materialization.** `<workspace>/.niwa/` is a pure file
  tree containing only the resolved subpath's content plus a single
  provenance marker (`.niwa-snapshot.toml`). No `.git/`. Refresh
  replaces the directory atomically; manual edits inside `.niwa/`
  do not persist.
- **Convention-based discovery.** `niwa init --from
  org/brain-repo` (no subpath) probes the source repo's root for a
  fixed marker vocabulary and resolves the subpath automatically.
- **Issue [#72](https://github.com/tsukumogami/niwa/issues/72)
  becomes structurally impossible.** No working tree means no
  fast-forward, no merge state, no divergence to reconcile.

## Quick start

### 1. Whole-repo source (existing standalone-`dot-niwa` workflow)

```bash
niwa init my-workspace --from org/dot-niwa
niwa create
niwa apply
```

niwa fetches the whole repo, materializes it at
`my-workspace/.niwa/`, and registers the workspace.

### 2. Brain-repo subpath source

```bash
niwa init my-workspace --from org/brain-repo:.niwa
niwa create
niwa apply
```

niwa fetches just the `.niwa/` subpath of `org/brain-repo` (via the
GitHub REST tarball API for github.com sources, or via
`git clone --depth=1` for other hosts). The rest of the brain repo
never touches disk.

### 3. Brain-repo with discovery (no subpath in slug)

```bash
niwa init my-workspace --from org/brain-repo
```

If the brain repo has `.niwa/workspace.toml` at root, discovery
resolves `subpath = ".niwa"` automatically. The discovered subpath
is recorded in the registry so subsequent applies skip the discovery
probe.

## Slug grammar

```
[host/]owner/repo[:subpath][@ref]
```

| Form | Example |
|------|---------|
| Whole repo, default branch | `tsukumogami/niwa` |
| Whole repo, pinned ref | `tsukumogami/niwa@v1.2.0` |
| Subpath, default branch | `tsukumogami/niwa:.niwa` |
| Subpath, pinned ref | `tsukumogami/niwa:.niwa@v1.2.0` |
| Non-GitHub host | `gitlab.com/group/repo:dot-niwa` |

Default host is `github.com`. The host segment is detected by a `.`
in the first segment of the slug (GitHub orgs cannot contain `.`).

The slug parser is strict (PRD R3):

| Rejected | Reason |
|----------|--------|
| `org/repo:` | Empty subpath after `:`. |
| `org/repo@v1:.niwa` | `@ref` must come after `:subpath`. |
| `org/repo: .niwa` | Embedded whitespace. |
| `org/repo:a:b` | Multiple `:` separators. |
| `org/repo@v1@v2` | Multiple `@` separators. |

## Discovery rules

When the slug omits an explicit subpath, niwa probes the source
repo root for marker files in this fixed precedence order:

| Rank | Marker | Resolved subpath |
|------|--------|------------------|
| 1 | `.niwa/workspace.toml` | `.niwa` |
| 2 | `workspace.toml` at root | `` (whole repo) |
| 3 | `niwa.toml` at root | `` (whole repo) |

When more than one marker is present at the source repo root,
discovery hard-errors with a diagnostic naming the conflicting
files. Resolve by removing one of the markers in the source repo;
there's no consumer-side override (an explicit slug-subpath bypasses
discovery entirely, but cannot resolve to "the repo root" because
empty subpaths are rejected).

When the rank-3 `niwa.toml` form resolves to repo root, niwa
**requires** `[workspace] content_dir` to be set explicitly per PRD
R8. This prevents niwa from accidentally reading brain-repo files
(`docs/`, `src/`, etc.) as content. Use `content_dir = "."` to
opt in to "the whole brain repo is content."

## Snapshot model

The materialized `<workspace>/.niwa/` directory is a pure file tree
containing exactly:

1. Every regular file from the resolved subpath in the source
   commit, with directory structure preserved.
2. One provenance marker file: `.niwa-snapshot.toml`.

No `.git/` directory exists. `git status` inside the snapshot
returns "not a git repository." Manual edits to files inside
`.niwa/` survive only until the next `niwa apply`, which replaces
the directory atomically from the upstream source.

### Provenance marker

`.niwa-snapshot.toml` records the source identity for downstream
consumers (`niwa status`, drift detection, `niwa reset`, the
plaintext-secrets guardrail, the snapshot-corruption integrity
heuristic):

```toml
source_url      = "tsukumogami/niwa:.niwa@main"
host            = "github.com"
owner           = "tsukumogami"
repo            = "niwa"
subpath         = ".niwa"
ref             = "main"
resolved_commit = "9f8e7d6c5b4a3210..."
fetched_at      = 2026-04-23T10:15:00Z
fetch_mechanism = "github-tarball"
```

The marker is human-readable: `cat .niwa/.niwa-snapshot.toml` shows
all fields without specialized tooling. Don't edit it; the next
apply overwrites it.

### Atomic refresh

`niwa apply` materializes the new snapshot at a sibling staging
path (`<workspace>/.niwa.next/`), then promotes it via a two-rename
swap:

1. Idempotent preflight cleanup of any stale `.niwa.next/` or
   `.niwa.prev/` from interrupted prior runs.
2. Rename `.niwa/` → `.niwa.prev/` (only if `.niwa/` exists).
3. Rename `.niwa.next/` → `.niwa/`.
4. fsync the parent directory.
5. RemoveAll `.niwa.prev/`.

There is a sub-microsecond window between steps 2 and 3 where
`.niwa/` does not exist; the PRD's R12 contract accepts this. niwa
itself never reads `.niwa/` mid-swap (the snapshot-consuming code
runs after the swap completes).

If extraction fails partway, the staging directory is orphaned at
`.niwa.next/`; the previous `.niwa/` is intact. The next apply's
preflight cleanup removes the orphan.

## Drift detection

For GitHub sources, niwa uses a 40-byte
`GET /repos/{owner}/{repo}/commits/{ref}` request with
`Accept: application/vnd.github.sha` to check whether the source
commit oid has changed since the last apply. When the cached
`resolved_commit` matches, niwa skips the tarball download and
extraction entirely — apply incurs only one round-trip plus state
update.

When the SHA endpoint reports a different oid, niwa issues the
tarball request with `If-None-Match: <stored-etag>`. A 304 response
is treated as no-change without re-extracting; a 200 response
streams a fresh tarball through `archive/tar` for selective
extraction.

Default-branch resolution happens on every apply (no init-time
pin). Ref-less slugs (the common case) follow whichever commit the
remote default branch currently points at; `niwa status` shows
`(default branch)` to make the moving-target nature explicit. To
pin, use an explicit `@<sha>`, `@<tag>`, or `@<branch>` in the
slug.

When the network is unreachable, `niwa apply` continues with the
cached snapshot and emits a `warning:` notice naming the source URL,
cached commit oid, and `fetched_at`. CI operators wanting fail-on-
stale behavior should follow the deferred `--strict-refresh` flag
(documented as future work in PRD-workspace-config-sources Out of
Scope).

## Failure modes

| Trigger | Behavior |
|---------|----------|
| Subpath not found in source repo | Apply exits non-zero with a diagnostic naming the subpath, the resolved commit oid, and the source slug. The on-disk snapshot is byte-identical to its pre-apply state. |
| Discovery ambiguous (multiple markers at root) | Apply exits non-zero naming the conflicting files. Remove one of the markers in the source repo. |
| Discovery yields nothing | Apply exits non-zero listing the three accepted markers and pointing at the explicit `:subpath` escape hatch. |
| Network unreachable during refresh | Apply continues with cached snapshot and emits a `warning:` notice. Exit code is 0. |
| Snapshot corruption (missing marker) | Auto-heal on next apply: refetch and atomically replace. Hard error only if the refetch also fails. |
| URL change detected on apply (legacy working tree) | Apply refuses without `--force`, naming both URLs and an inspection command (`cd .niwa && git status`). |
| URL change detected (snapshot already in place) | Apply re-validates that the new source's `[workspace].name` matches the registered name, then atomically replaces the snapshot. |
| Same-URL upgrade (legacy working tree, URL unchanged) | Apply lazy-converts to a snapshot in place with a one-time `note:` notice. No `--force` required. |

## Migration from standalone `dot-niwa`

Brain-repo maintainers who want to consolidate their workspace
config into the brain repo:

1. `git mv` the standalone `dot-niwa` payload into
   `<brain-repo>/.niwa/`. Drop the standalone repo's `README.md`,
   `LICENSE`, `.gitignore` (or fold useful bits into the brain
   repo's existing equivalents). Commit and push.
2. Announce: "the workspace config now lives at
   `org/brain-repo:.niwa` — run `niwa config set global
   org/brain-repo` to switch."
3. Each consumer's switch is independent; the standalone `dot-niwa`
   repo can stay in place for graceful overlap.

Each consumer:

1. Run `niwa config set global org/brain-repo` (or edit
   `~/.config/niwa/config.toml` directly to change the registered
   `source_url`).
2. Run `niwa apply`. On the first run, niwa detects the URL change
   and refuses without `--force`. Inspect `<workspace>/.niwa/` for
   any pending edits worth preserving.
3. Run `niwa apply --force`. niwa atomically replaces the working
   tree with a snapshot from the new source.

The auto-discovered workspace overlay slug also changes during this
migration: `org/dot-niwa-overlay` → `org/.niwa-overlay`. The brain-
repo maintainer must arrange for the overlay repo at the new slug
(rename the existing overlay repo, or publish a new one) before
consumers complete migration; otherwise the overlay clone silently
skips and consumers lose the augmentation without warning. This is
a one-time coordination cost; subsequent applies behave normally.

## CLI reference

| Surface | Purpose |
|---------|---------|
| `niwa init <name> --from <slug>` | Register and clone a new workspace from a source slug. |
| `niwa config set global <slug>` | Set the personal-overlay source. |
| `niwa apply` | Fetch the latest snapshot and re-materialize. Detects URL changes and refuses without `--force`. |
| `niwa apply --force` | Discard the on-disk `.niwa/` and re-materialize from the registered source. Required after a registered URL change. |
| `niwa status` | Display the resolved source slug, the cached `resolved_commit`, and `(default branch)` annotation when no ref is pinned. |
| `niwa reset` | Re-fetch the snapshot and reconstruct instance state. Reads the provenance marker to recover the source URL. |

## Security model

The snapshot pipeline's primary security surface is the GitHub
tarball + tar extraction path. The defense suite is documented in
[DESIGN-workspace-config-sources.md §Security
Considerations](../designs/DESIGN-workspace-config-sources.md#security-considerations);
in summary:

- Positive type allowlist: only regular files and directories are
  written. Symlinks, hard links, devices, FIFOs, and pax extensions
  are skipped at the per-entry switch.
- Wrapper anchoring: the first tar entry establishes the GitHub
  tarball wrapper directory; subsequent entries that don't begin
  with the wrapper prefix are rejected.
- Subpath filter: entries outside the resolved subpath are skipped
  before any byte is written.
- Path containment: the resolved destination path must live under
  the snapshot directory (defends against `..`-style traversal).
- Filename validation: NUL bytes, `..` segments, leading `/`, and
  backslashes are rejected.
- Decompression-bomb cap: 500 MB cumulative across the extraction;
  per-entry `header.Size` is bounded against the remaining budget.
- Failure leaves no partial state at the canonical path: the
  staging directory absorbs all in-flight bytes.

`GH_TOKEN` is read once at `APIClient` construction and attached as
`Authorization: Bearer <token>` on outbound requests. The token
never appears in error messages, log lines, or surfaced API types.

## .env.example failure policy {#env-example-failure-policy}

When `niwa apply` materializes a repo, a pre-pass scans that repo's
`.env.example` for keys that look like real secrets (a vendor-token
prefix, or a value with high entropy) but aren't declared in your
workspace config. Each such detection produces a warning by default
and apply continues. You can promote specific detections to hard
failures that abort apply, and you can do it at the granularity that
fits — a whole category, a single repo, or one named variable.

### The `[env_example_policy]` block

The policy lives in an `[env_example_policy]` table with two category
keys, each accepting `"warn"` or `"fail"`:

```toml
[env_example_policy]
vendor_token = "fail"   # warn | fail
entropy = "warn"        # warn | fail
```

- `vendor_token` covers values that match a known vendor-token prefix.
- `entropy` covers values that cross the entropy threshold.

A key you leave out is unset — it inherits from the next-broader
level rather than defaulting to anything on its own.

At project scope (the workspace top level and per-repo positions) you
can also set per-variable actions in an `[env_example_policy.vars]`
sub-table, keyed by the exact variable name:

```toml
[env_example_policy.vars]
STRIPE_EXAMPLE_KEY = "warn"
ACME_API_TOKEN = "fail"
```

The `vars` sub-table is project-scope only. A personal/global config
carries category keys only; a `vars` entry there has no effect.

### The three levels

The block can be set at three levels, broadest to most specific:

| Level | Where | Scope | Supports `vars` |
|-------|-------|-------|-----------------|
| User | Personal/global niwa config — `[global]` or `[workspaces.<name>]` in `~/.config/niwa/config.toml` (or your global config repo's `niwa.toml`) | All workspaces (or one named workspace) | No |
| Project | `[env_example_policy]` at the workspace config top level | The whole workspace | Yes |
| Per-repo | `[repos.<name>.env_example_policy]` in the workspace config | One repo | Yes |

### Precedence

For each undeclared probable-secret key, niwa resolves one effective
action by walking from most specific to broadest and taking the first
match:

1. Operator per-variable entry — per-repo `vars`, then workspace `vars`.
2. The key's inline annotation (see below).
3. Per-category policy — per-repo, then workspace, then user.
4. Default: `warn`.

So a per-variable entry beats a project setting, which beats a user
setting. Any level you leave unset inherits the broader one, and when
nothing is configured anywhere the action is `warn` — detections warn
and don't block apply unless a `fail` policy applies.

### Inline annotations in `.env.example`

A repo can annotate a single line in its own `.env.example` with a
trailing comment to set that key's action:

```bash
STRIPE_EXAMPLE_KEY=sk_test_xxxxxxxx # niwa: warn
ACME_API_TOKEN=replace-me           # niwa: fail
```

The marker syntax is `# niwa: warn` or `# niwa: fail`. It's extracted
independently of value quoting, so it works on unquoted, single-quoted,
and double-quoted values; a `# niwa:` sequence inside a quoted value is
not treated as a marker. An unknown marker is ignored with a warning
naming the key.

Because an inline annotation sits at step 2 of the cascade, an
operator's per-variable config entry (step 1) overrides it. That's the
operator's recourse: if a repo's inline `# niwa: warn` lowers a key
you've set to `fail` at the category level, add an
`[env_example_policy.vars]` entry for that key to take it back. When an
inline annotation does lower a configured `fail` to `warn`, niwa emits
a distinct warning so the downgrade is visible in apply output.

### Per-run override and disabling the scan

`niwa apply --allow-plaintext-secrets` downgrades every resolved
`fail` to `warn` for that one run, across all repos, emitting a per-key
audit line for each downgrade. Use it to push through a run without
editing config; it doesn't change anything persisted.

The separate `read_env_example = false` toggle (settable at the
workspace and per-repo levels) is unchanged: it turns the whole scan
off for that scope, so no detection — and no policy — runs at all. The
failure policy applies only when the scan is on.

## Secret-output targets {#env-output}

By default niwa expands each repo's resolved secrets into a single
`.local.env` file in dotenv (`KEY=value`) form. The `env_output` setting
makes that destination configurable per repo so each repo gets the file
its stack actually reads.

### The `env_output` setting

`env_output` declares one or more output targets. It accepts three forms:

```toml
# a single target (format inferred from the extension)
env_output = ".env.local"

# a list of targets, each inferred
env_output = [".env.local", "secrets.json"]

# a list of tables when a target needs an explicit format
env_output = [{ path = "secrets", format = "shell" }, { path = ".env" }]
```

A single array MUST NOT mix bare strings and tables; use a list of tables
when any element needs an explicit `format`. Every declared target
receives the repo's full resolved secret set.

### Formats and extension inference

Three formats are supported: `dotenv` (`KEY=value`, the default),
`json` (a flat object), and `shell` (`export KEY='value'`). The format is
inferred from the target's extension unless an explicit `format` is given:

| Extension | Format |
|-----------|--------|
| `.json` | json |
| `.sh` | shell |
| `.env`, `.local.env`, `.env.local`, anything else, or no extension | dotenv |

An explicit `format` always wins over inference.

### The three levels and precedence

| Level | Where it's set | Applies to |
|-------|----------------|------------|
| User | `env_output` in the personal/global override | All workspaces |
| Project | `env_output` at the workspace config top level | The whole workspace |
| Per-repo | `env_output` under `[repos.<name>]` | One repo |

Resolution is most-specific-wins at the list level: a non-empty per-repo
`env_output` replaces the workspace one, which replaces the user one. An
unset level inherits the broader one. When nothing is set anywhere, the
default is a single `.local.env` dotenv target — so a repo that declares
no `env_output` keeps today's behavior byte-for-byte.

### Git invisibility

Whatever name you choose — including git-tracked-by-default names like
`.env` — niwa records the target in the repo's git exclude coverage
before writing it, so a resolved secret never becomes committable and you
never edit the repo's committed `.gitignore`. A custom-named target in a
directory niwa cannot confirm is a git repository is refused (fail-closed)
rather than written unprotected. Target paths are validated: an absolute
path, a path escaping the repo, or one escaping via a symlinked parent is
rejected.

## Source layouts (rank-1, rank-2, rank-3) {#source-layouts}

niwa probes each remote source for one of two recognized layouts.
The first marker found at the source root resolves rank; ambiguity
(both markers present) and absence (neither marker present) are
both errors.

### Single-repo workspace {#single-repo-workspace}

Story 1: you want to drive your day-to-day repository as a niwa
workspace without standing up a separate `dot-niwa` repository.
Drop `.niwa/workspace.toml` (plus any niwa-managed components like
`CLAUDE.md`, `hooks/`, `mcp/`) into the repo and point `niwa init`
at it:

```bash
niwa init --from owner/repo
```

niwa probes the source, finds the rank-1 marker at the source
root, and materializes only the `.niwa/` subtree into the
workspace's snapshot directory. The rest of the repo (your
application code, README, src/, etc.) is never fetched — the
selective extraction means even a multi-gigabyte general-purpose
repo costs the same to clone as a tiny `dot-niwa` repo.

On-disk shape at the source repo:

```
owner/repo/
├── .niwa/
│   ├── workspace.toml
│   ├── CLAUDE.md
│   ├── hooks/
│   │   └── post-apply.sh
│   └── mcp/
│       └── filesystem.json
├── src/                  # not fetched
├── tests/                # not fetched
└── README.md             # not fetched
```

The auto-discovered personal overlay (PRD R10) follows the slug
naming convention `<owner>/<repo>-overlay` regardless of where the
team config lived in the source. For a workspace seeded from
`dangazineu/foo`, niwa probes for `dangazineu/foo-overlay`.

### Brain repo {#brain-repo}

Story 2: you maintain a "brain" repository like `acme/vision` that
holds the org's product strategy alongside a niwa workspace. The
brain repo is also the workspace's source — niwa's `discoverAllRepos`
pass treats `acme/vision` as both the config source AND a repo that
the workspace's `Classify` step pulls into the local checkout for
day-to-day editing. The cross-reference in PR #138 covers the
discovery refactor.

The overlay slug for a brain repo follows the same R10 rule:
`acme/vision` derives `acme/vision-overlay`, NOT `acme/.niwa-overlay`.

### Overlay slug rule {#overlay-slug-rule}

PRD R10 makes the overlay slug derivation unconditional: for any
team config source with `Owner=<owner>` and `Repo=<repo-name>`, the
auto-discovered overlay is `<owner>/<repo-name>-overlay`. The
team config's subpath has no effect on the overlay's repo-name.

Worked examples:

```
dangazineu/dot-niwa            → dangazineu/dot-niwa-overlay
acme/vision                    → acme/vision-overlay
acme/brain (with subpath .niwa) → acme/brain-overlay
github.com/foo/bar             → foo/bar-overlay
```

This is a deliberate change from the previous behavior, which
derived the overlay's repo-name from the team config's subpath
(e.g., `acme/brain:.niwa` → `acme/.niwa-overlay`). Workspaces that
relied on the old derivation see a one-time URL-change gate the
next time they apply; the rename to the new convention is the
remediation.

### Rank-2 deprecation {#rank-2-deprecation}

The rank 2 layout — `workspace.toml` at the source repo root with
no `.niwa/` subdirectory — is deprecated but still accepted for
backwards compatibility. niwa emits a one-time `note:` (PRD R14)
the first time a workspace's team config or overlay resolves to
rank 2:

```
note: source acme/dot-niwa uses the deprecated rank-2 layout
(workspace.toml at repo root). Run /niwa:migrate-config to migrate
the source to the rank-1 layout (.niwa/workspace.toml).
```

The notice is recorded into `InstanceState.DisclosedNotices`, so
subsequent applies on the same workspace stay silent.

The `/niwa:migrate-config` skill (auto-installed; see
[niwa plugin install](#niwa-plugin-install) below) walks the user
through two migration paths (PRD R23):

1. **In-place restructure**: the source-repo maintainer adds
   `.niwa/`, moves `workspace.toml` into it, and pushes. The
   workspace user runs `niwa apply --force <workspace>` to
   re-discover.
2. **Slug swap**: the workspace user points the registry at a
   different repo that already carries the rank-1 layout. niwa
   rewrites `source_url` in `~/.config/niwa/config.toml`; the user
   runs `niwa apply --force <workspace>`.

The hard removal of rank-2 acceptance is deferred to a future
release — until then, rank-2 workspaces continue to work but
disclose the deprecation once each.

### Rank-3 removal {#rank-3-removal}

A previous niwa iteration recognized a third **workspace config**
layout: a `niwa.toml` file at the source repo root acting as a
workspace config (rank 3, with an explicit `[workspace] content_dir`
declaration to mark where workspace content lived inside the repo).
That layout has been removed. niwa never probes for `niwa.toml` as
a workspace-config marker; sources that ship only `niwa.toml` in
this shape resolve as no-marker (PRD R4) and fail with a clear
error.

Existing workspaces seeded from rank-3 sources need their source
repos migrated to either rank 1 (`.niwa/workspace.toml`) or rank 2
(`workspace.toml` at root). The /niwa:migrate-config skill does
NOT handle rank-3 → rank-1 transitions automatically — the rank-3
schema diverged enough from the current `workspace.toml` shape
that manual review is required.

> **Note**: rank-3 removal applies only to the **per-workspace
> source** layout. It does NOT affect the **personal global config
> overlay** — the `niwa.toml`-shaped repository a user registers
> via `niwa config set global` (or `[global_config]` in
> `~/.config/niwa/config.toml`) that applies across all niwa
> workspaces on the machine. The personal global config overlay
> keeps its existing file convention (`niwa.toml` at repo root with
> `[global]` and `[global.vault.*]` sections); niwa clones the
> entire repo verbatim and reads the file. The rank-1/rank-2
> discovery model documented above is for **workspace** sources only.

## niwa plugin install {#niwa-plugin-install}

When niwa detects a rank-2 source (team config OR overlay), it
auto-installs an embedded Claude Code plugin to make the
`/niwa:migrate-config` skill available the next time the user
invokes Claude Code. The plugin lives at
`~/.claude/plugins/marketplaces/niwa/` and is fully self-contained
in the niwa binary via `//go:embed` — no network fetch happens at
install time (PRD R17).

The install is silent in the success path: niwa emits a single
disclosure note alongside the rank-2 deprecation notice (PRD R18),
records the install in `InstanceState.DisclosedNotices`, and
suppresses the note on subsequent applies. Idempotent re-runs that
find an up-to-date plugin on disk return `(UpToDate, nil)` without
mutating the filesystem.

### Opting out

Two opt-out paths (PRD R19) are honored:

- **Persistent**: set `auto_install_plugins = false` in
  `~/.config/niwa/config.toml`'s `[global]` section. The flag
  applies to every `niwa init` and `niwa apply` invocation.
- **Per-invocation**: pass `--no-install-plugins` to
  `niwa init` or `niwa apply`. Overrides the persistent setting
  for the current command only.

Either opt-out causes niwa to emit a skip-notice with the manual
install command:

```
note: niwa Claude Code plugin install skipped. To install manually,
run: niwa plugins install
```

### Failure handling

A user-environment failure (read-only `$HOME`, permission denied,
mid-rename filesystem error) is treated the same as an opt-out
(PRD R20): niwa emits the skip-notice with the manual install
command and continues — `niwa apply` does not exit non-zero
because the plugin install couldn't run. The `<install-path>.next/`
staging directory is cleaned up so the next apply can retry.

## Claude marketplaces {#claude-marketplaces}

The `[claude]` block's `marketplaces` setting lists the Claude Code
plugin marketplaces niwa registers for the workspace. niwa writes them
into Claude's `known_marketplaces` / `extraKnownMarketplaces` so the
plugins in `plugins = [...]` resolve.

### Two authoring forms

The legacy string-list form still works unchanged:

```toml
[claude]
marketplaces = ["tsukumogami/shirabe", "repo:tools/.claude-plugin/marketplace.json"]
plugins = ["shirabe@shirabe"]
```

The table form adds per-marketplace options:

```toml
[claude]
plugins = ["shirabe@shirabe"]

[[claude.marketplaces]]
source = "tsukumogami/shirabe"
# auto_update omitted -> false; track omitted -> latest release (github)

[[claude.marketplaces]]
source = "repo:tools/.claude-plugin/marketplace.json"
auto_update = true
```

A bare string is equivalent to a table with that `source` and the
defaults below.

### `auto_update` (default: `false`)

Whether Claude Code auto-updates the marketplace. **The default is now
`false`** — previously niwa force-enabled auto-update on every
marketplace, which churned cached plugin versions and was a contributing
cause of dangling install records. Set `auto_update = true` per
marketplace to opt back in. This matches Claude Code's own safer default
for third-party marketplaces.

### `track` (default: `release` for github sources)

Which version of a github-sourced marketplace to track:

- `release` (default) — niwa resolves the marketplace's latest stable
  (non-prerelease) release tag.
- `main` — track the default branch (the prior behavior).
- an explicit ref/tag — pin to that ref.

Local (`directory` / `repo:`) sources ignore `track`.

> **Known limitation.** Claude Code's github marketplace *source* object
> currently ignores a ref pin and always clones default-branch HEAD, so
> a marketplace whose `main` carries an in-development version still
> installs that `-dev` build today. niwa resolves the release tag and
> records it best-effort (forward-compatible) and reports when it falls
> back to the branch, but the effective switch to releases is blocked
> upstream. Pinning a daily-stable marketplace via a local `repo:`
> checkout of a release is the current workaround.

### Automatic record healing

On every `niwa create` and `niwa apply`, niwa removes *dangling* Claude
plugin install records — records whose installed plugin directory or
project path no longer exists — and reports how many it removed. This
repairs registries that accumulated stale records over time (a frequent
cause of skills intermittently failing to register) with no separate
command. The heal only removes records whose referenced paths are gone;
it never touches records for live workspaces, backs up the registry
before the first change, and never fails create/apply on a registry
error.
