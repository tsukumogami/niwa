# Lead: How should a config source be uniquely identified across the workspace registry, state, telemetry, and the personal overlay layer?

## Findings

### Current schema (registry, state, status output, telemetry)

**Workspace registry (`~/.config/niwa/config.toml`)** — defined in
`internal/config/registry.go:14-36`:

```go
type GlobalConfig struct {
    Global       GlobalSettings           `toml:"global"`        // clone_protocol
    GlobalConfig GlobalConfigSource       `toml:"global_config"` // personal-overlay repo slug
    Registry     map[string]RegistryEntry `toml:"registry"`
}
type RegistryEntry struct {
    Source    string `toml:"source"`               // absolute path to workspace.toml
    Root      string `toml:"root"`                 // absolute path to workspace root
    SourceURL string `toml:"source_url,omitempty"` // org/repo slug or full URL
}
type GlobalConfigSource struct {
    Repo string `toml:"repo,omitempty"` // org/repo or URL
}
```

Concrete shape on disk:

```toml
[global]
clone_protocol = "ssh"

[global_config]
repo = "tsukumogami/dot-tsuku"

[registry.tsukumogami]
source     = "/home/dangazineu/dev/niwaw/tsuku/.niwa/workspace.toml"
root       = "/home/dangazineu/dev/niwaw/tsuku"
source_url = "tsukumogami/fake-dot-niwa"
```

The registry name is the workspace name from `[workspace].name` in
`workspace.toml` (or the explicit `<name>` arg to `niwa init`). It is the
key under `[registry.<name>]` and is also what `vault_scope` matches against
in the personal overlay's `[workspaces.<name>]` table.

**Per-instance state (`<instance>/.niwa/instance.json`)** — defined in
`internal/workspace/state.go:56-73`:

```go
type InstanceState struct {
    SchemaVersion    int                  `json:"schema_version"` // currently 2
    ConfigName       *string              `json:"config_name"`
    InstanceName     string               `json:"instance_name"`
    InstanceNumber   int                  `json:"instance_number"`
    Root             string               `json:"root"`
    Detached         bool                 `json:"detached,omitempty"`
    SkipGlobal       bool                 `json:"skip_global,omitempty"`
    OverlayURL       string               `json:"overlay_url,omitempty"`
    NoOverlay        bool                 `json:"no_overlay,omitempty"`
    OverlayCommit    string               `json:"overlay_commit,omitempty"`
    Created          time.Time            `json:"created"`
    LastApplied      time.Time            `json:"last_applied"`
    ManagedFiles     []ManagedFile        `json:"managed_files"`
    Repos            map[string]RepoState `json:"repos"`
    Shadows          []Shadow             `json:"shadows,omitempty"`
    DisclosedNotices []string             `json:"disclosed_notices,omitempty"`
}
```

Notable: state records `OverlayURL` + `OverlayCommit` (the personal-overlay
clone, set in `init.go:309-310`) but records nothing about the *team config*
source — there's no field naming the slug or commit oid of the
`<workspace>/.niwa` clone. The `RegistryEntry.SourceURL` and the on-disk
`.niwa/.git` are the only ground truth.

**Status display** (`internal/cli/status.go:178-211`): the detail view prints
`Instance:`, `Config:` (= `state.ConfigName`), `Root:`, `Created:`,
`Applied:`, then a `Repos:` block. **It does not print the source URL or
slug at all today.** The summary view (`status.go:137-176`) prints only
name + repo count + drift count + applied-time per instance.

**Telemetry**: there is no telemetry pipeline in `niwa` today. A grep for
`telemetry|Telemetry|TELEMETRY` across `internal/` returns only an
*example* in `docs/guides/vault-integration.md` (a hypothetical
`TELEMETRY_ENDPOINT` env var) and design-doc text. `niwa` ships no
metrics pipeline; the lead's "telemetry" dimension is forward-looking only.

**Clone resolution** (`internal/workspace/clone.go:90-112`): `ResolveCloneURL`
accepts three input shapes:

- `org/repo` shorthand (turned into `git@github.com:org/repo.git` or
  `https://github.com/org/repo.git`)
- a full URL (`https://`, `git@`, or `://`-bearing) — passed through
- an absolute filesystem path with at least two slashes — passed through
  (this is the `file:///...` and bare-path test-fixture path)

It does not understand any subpath, ref, or query suffix. The grammar is
strictly `org/repo`.

**Overlay derivation** (`internal/config/overlay.go:202-215`): the convention
overlay URL is computed by appending `-overlay` to the repo name:
`tsukumogami/vision` -> `tsukumogami/vision-overlay`. `parseOrgRepo`
handles HTTPS, SSH, and shorthand but does not strip subpath or ref.

### Identity dimensions to capture

A subpath-aware source needs five dimensions; current code captures only
two (host implicit at `github.com`; owner+repo embedded in the slug):

| Dimension | Required? | Default | Source today |
|-----------|-----------|---------|--------------|
| **host** | Optional | `github.com` | implicit in shorthand; explicit in full URLs |
| **owner** (org/user) | Required | none | shorthand part 1 |
| **repo** | Required | none | shorthand part 2 |
| **subpath** | Optional | `/` (whole repo, current behavior) | not captured |
| **ref** | Optional | origin's default branch | not captured (HEAD-tracking pull) |

A sixth, **commit oid**, is the *resolved* form — the snapshot identity at
materialization time. It belongs in state, not in the slug.

### Slug grammar candidates

Below, examples build up from the bare minimum (whole-repo, default branch)
to the full five-tuple.

**Candidate A: colon-subpath, at-ref (`org/repo:subpath@ref`)**

```
tsukumogami/vision
tsukumogami/vision:docs/niwa
tsukumogami/vision:docs/niwa@v1.2.0
tsukumogami/vision@main
github.com/tsukumogami/vision:docs/niwa@main
gitlab.example.com/group/sub/repo:dot-niwa@v2
```

- Parse: split on first `@` for ref; split remainder on first `:` for subpath;
  remainder is `[host/]owner/repo` (host detected by presence of a `.` in
  the first segment).
- Pros: short, familiar (Renovate uses `org/repo:preset`, Docker uses
  `image:tag`, git uses `path@ref` in many places).
- Cons: `:` is reserved in SSH URLs (`git@github.com:org/repo.git`), so
  shorthand and full-URL parsing diverge. `@` in URLs collides with HTTP
  basic-auth (`https://user@host/...`) — but auth is never in a slug, so
  acceptable.

**Candidate B: double-slash subpath, at-ref (Terraform-style: `org/repo//subpath@ref`)**

```
tsukumogami/vision
tsukumogami/vision//docs/niwa
tsukumogami/vision//docs/niwa@v1.2.0
tsukumogami/vision@main
```

- Parse: split on first `@` for ref; if remainder contains `//`, split
  on it for `[host/]owner/repo` and subpath.
- Pros: Terraform module sources use exactly this; it cleanly survives
  inside a real URL (`git::https://github.com/org/repo.git//subdir?ref=v1`)
  because `//` cannot appear in a normal URL path. Unambiguous when subpath
  contains `/` itself.
- Cons: visually heavy; users may double-strike `/` accidentally and not
  notice the meaning shift.

**Candidate C: hash-subpath (`org/repo#subpath@ref`)**

```
tsukumogami/vision
tsukumogami/vision#docs/niwa
tsukumogami/vision#docs/niwa@v1.2.0
```

- Pros: `#` is the URL fragment separator and is naturally the
  "sub-location within a resource" marker. npm uses `org/repo#branch`.
- Cons: `#` is the TOML comment character. Inside a quoted string it's
  fine, but `source_url = tsukumogami/vision#docs/niwa` (unquoted) would
  be eaten as a comment. Forces double-quoting in TOML, which is a small
  trap.

**Candidate D: query-string (Nix flake style: `org/repo?dir=subpath&ref=v1`)**

```
tsukumogami/vision
tsukumogami/vision?dir=docs/niwa
tsukumogami/vision?dir=docs/niwa&ref=v1.2.0
```

- Pros: explicit field names; trivially extensible (add `?host=...`, etc).
- Cons: long, URL-shaped in a place users currently type a slug; `?` and
  `&` need shell quoting. Loses the "feels like a slug" property.

**Candidate E: structured sub-table (no slug)**

```toml
[registry.tsukumogami.source]
host    = "github.com"
owner   = "tsukumogami"
repo    = "vision"
subpath = "docs/niwa"
ref     = "v1.2.0"
```

- Pros: zero ambiguity, machine-friendly, trivially extensible.
- Cons: unusable as a CLI argument (`niwa init tsukumogami --from ???`).
  Forces a parallel slug grammar anyway for the CLI surface, then a
  serialization layer that converts.

**Composability summary:**

| Candidate | Discovery (subpath inferred) | Explicit subpath | Ref pinning | TOML safety | Shell safety | CLI ergonomics |
|-----------|------------------------------|------------------|-------------|-------------|--------------|----------------|
| A `:`/`@` | bare slug works | clean | clean | clean | clean | best |
| B `//`/`@` | bare slug works | clean (Terraform precedent) | clean | clean | clean | second-best |
| C `#`/`@` | bare slug works | clean | clean | needs quoting | needs quoting | OK with quotes |
| D `?dir=` | bare slug works | verbose | verbose | clean if quoted | needs quoting | poor |
| E sub-table | n/a | clean | clean | clean | n/a | none (no slug) |

### Schema changes

**Approach: keep `source_url` as the canonical opaque slug; add structured
mirror fields for fast lookup; record snapshot in state.**

**Registry — before:**

```toml
[registry.tsukumogami]
source     = "/home/dangazineu/dev/niwaw/tsuku/.niwa/workspace.toml"
root       = "/home/dangazineu/dev/niwaw/tsuku"
source_url = "tsukumogami/fake-dot-niwa"
```

**Registry — after (whole-repo, unchanged from user perspective):**

```toml
[registry.tsukumogami]
source     = "/home/dangazineu/dev/niwaw/tsuku/.niwa/workspace.toml"
root       = "/home/dangazineu/dev/niwaw/tsuku"
source_url = "tsukumogami/fake-dot-niwa"
# new fields, optional and back-compat:
# source_host    = "github.com"
# source_owner   = "tsukumogami"
# source_repo    = "fake-dot-niwa"
# source_subpath = "/"
# source_ref     = ""           # empty means "track default branch"
```

**Registry — after (subpath case):**

```toml
[registry.research]
source     = "/home/dangazineu/dev/niwaw/research/.niwa/workspace.toml"
root       = "/home/dangazineu/dev/niwaw/research"
source_url = "tsukumogami/vision:teams/research@main"
# parsed mirror, populated by the writer; readers may ignore them and re-parse:
source_host    = "github.com"
source_owner   = "tsukumogami"
source_repo    = "vision"
source_subpath = "teams/research"
source_ref     = "main"
```

The mirror fields are written for free at registration time and let
`niwa status` / collision detection avoid re-parsing the slug on every
read. Readers older than this change ignore them harmlessly (the toml
decoder skips unknown fields). Newer readers prefer the parsed form
when present and fall back to parsing `source_url` when not.

**State — before (selected fields):**

```json
{
  "schema_version": 2,
  "instance_name": "tsuku-1",
  "overlay_url": "tsukumogami/dot-tsuku",
  "overlay_commit": "ab12cd34..."
}
```

**State — after (add team-source snapshot):**

```json
{
  "schema_version": 3,
  "instance_name": "research-1",
  "config_source": {
    "url": "tsukumogami/vision:teams/research@main",
    "host": "github.com",
    "owner": "tsukumogami",
    "repo": "vision",
    "subpath": "teams/research",
    "ref": "main",
    "resolved_commit": "9f8e7d6c5b4a3210...",
    "fetched_at": "2026-04-22T10:15:00Z"
  },
  "overlay_url": "tsukumogami/vision-overlay",
  "overlay_commit": "ab12cd34..."
}
```

`config_source.resolved_commit` is the snapshot identity — what `niwa apply`
materialized last. Drift detection compares the registry's pin (`ref` field)
to the latest oid on the remote branch and decides whether to re-fetch.
`config_source.url` is redundant with the parsed fields but is kept for
human inspection and for tools that consume `instance.json` without
understanding the schema migration.

Schema bumps to v3. v2 files load with `config_source = nil`; the next
`niwa apply` populates it from the registry slug + a fresh `git ls-remote`
and rewrites the file.

**Migration story:**

1. Existing entries with `source_url = "org/repo"` parse as
   `(host=github.com, owner=org, repo=repo, subpath="/", ref="")`. Behavior
   is identical to today.
2. Entries with `source_url = "org/repo:subpath"` (new shape) parse with
   subpath populated.
3. The `niwa init --from <slug>` and `niwa config set global <slug>` paths
   accept any of the three shapes (whole-repo, `:subpath`, `@ref`), parse
   them, and write the mirror fields. Old binaries reading a new registry
   see only the slug; they can still clone whole-repos and will fail
   gracefully on subpath slugs at clone time (`ResolveCloneURL` returns
   "invalid org/repo format" today for slugs containing `:`).

### Personal overlay & vault_scope interaction

Today (`internal/workspace/override.go:266-295`,
`internal/config/config.go:128-132`), `vault_scope` is a **string label**
that the personal overlay matches against `[workspaces.<scope>]` blocks.
By default (when `vault_scope` is unset), niwa uses the workspace name
for single-source workspaces, and *requires* an explicit value for
multi-source workspaces (R5). Importantly, `vault_scope` has nothing
intrinsic to do with the source URL — it is a free-form label chosen by
the workspace author.

**With subpath sourcing, nothing here needs to change.** The defaulting
rule is:

- Workspace name `research` (sourced from `tsukumogami/vision:teams/research`)
  defaults to `vault_scope = "research"`. The personal overlay author
  writes `[workspaces.research]`. Same as today.
- The brain repo's name (`vision`) is **not** the default. That would be
  surprising: two workspaces sourced from the same brain at different
  subpaths (`vision:teams/research`, `vision:teams/tsukumogami`) should
  not collide on `vault_scope` by default.

The one new affordance worth offering: when the workspace author writes
`vault_scope = "@source"`, niwa expands it to a stable identifier derived
from the source identity (e.g., `owner-repo-subpath` slugified). This is
useful for "monorepo of teams under one brain" setups where every
subpath workspace should share the same overlay scope. **Recommend
deferring this** to a follow-up — the default of "scope = workspace name"
is sufficient for v1.

### Collision and sharing model

Two workspaces sourcing the same brain repo at different subpaths look like:

```toml
[registry.research]
root           = "/home/d/dev/research-ws"
source         = "/home/d/dev/research-ws/.niwa/workspace.toml"
source_url     = "tsukumogami/vision:teams/research@main"
source_owner   = "tsukumogami"
source_repo    = "vision"
source_subpath = "teams/research"
source_ref     = "main"

[registry.tsukumogami]
root           = "/home/d/dev/tsukumogami-ws"
source         = "/home/d/dev/tsukumogami-ws/.niwa/workspace.toml"
source_url     = "tsukumogami/vision:teams/tsukumogami@main"
source_owner   = "tsukumogami"
source_repo    = "vision"
source_subpath = "teams/tsukumogami"
source_ref     = "main"
```

**Sharing model: shared brain-repo cache, isolated materialized snapshots.**

- A single content-addressed cache under `~/.cache/niwa/sources/<host>/<owner>/<repo>/<commit-oid>/`
  holds the fetched bytes once per (repo, commit). Both workspaces resolve
  `tsukumogami/vision@main` to the same oid and hit the same cache entry.
- Each workspace's `.niwa/` is materialized from the cache by extracting
  only the subpath's bytes. The materialized form is a
  no-`.git` directory (per the snapshot-shape lead's domain), so there is
  no working-tree state to keep in sync per workspace.
- Identity at the cache layer is `(host, owner, repo, commit)`; identity
  at the workspace layer adds subpath.

**Detection.** A registry-wide check on `niwa init` catches accidental
duplication: same `(owner, repo, subpath, ref)` registered to two
workspace names is a hard error ("workspace `research` is already
registered for `tsukumogami/vision:teams/research@main` at /home/d/dev/research-ws").
Same `(owner, repo)` with different subpaths is fine and proceeds silently.

**`niwa status`.** When two registry entries share a brain repo, the
summary view continues listing them as two separate workspaces. The detail
view (proposed below) prints the full slug, so users see the subpath
distinction explicitly.

### `niwa status` and telemetry display

**Detail view (`niwa status` from inside an instance) — proposed addition:**

```
Instance: research-1
Config:   research
Source:   tsukumogami/vision:teams/research @ 9f8e7d6 (main, fetched 2h ago)
Root:     /home/d/dev/research-ws/research-1
Created:  2026-04-15 10:30
Applied:  2026-04-22 09:00
...
```

Format rules:

- Whole-repo, default branch: `tsukumogami/dot-tsuku` (slug only — matches
  today's mental model).
- Subpath: `tsukumogami/vision:teams/research` (slug + subpath).
- Pinned ref: append ` @ <short-oid> (<ref>, fetched <relative-time>)`.
- Default-branch: append ` @ <short-oid> (fetched <relative-time>)` —
  no ref label since "default branch" is unstable.

**Summary view** stays unchanged (no source column) to keep the table narrow.
Users opt in to the source column with `niwa status --verbose` (which
already exists for source-attribution display on managed files).

**Telemetry.** No pipeline exists today. When one is added, the **slug
without subpath and ref is the safe default to report** because:

- The `(owner, repo)` portion is essentially public for github-hosted
  configs (anyone observing the network sees the clone target).
- The `subpath` reveals internal repo layout, which leaks information
  even from public repos (which subteam's config is in use).
- The `ref` value (a tag or branch name) similarly leaks private naming
  conventions.

Recommendation: telemetry events carry `source_host` and a hash of
`(owner, repo, subpath, ref)` rather than the literal values. The hash
preserves collision/cohort analysis ("how many workspaces share a config
source?") without exposing the layout. Operators who want full visibility
can opt in via a workspace-level `[telemetry].include_source = true`.

### Ref handling

Today: niwa pulls origin's default branch. `git pull --ff-only origin` in
`configsync.go:42`. There is no concept of a pinned ref.

With subpath sourcing, ref pinning becomes natural and useful:

- `tsukumogami/vision@v1.2.0` — pin to a tag (immutable; great for sharing
  configs across a release boundary).
- `tsukumogami/vision@main` — track a branch (current behavior, made
  explicit).
- `tsukumogami/vision@9f8e7d6` — pin to a commit (escape hatch; commit-SHA
  detection already exists via `isCommitSHA` in `clone.go:78-84`).

The `@ref` suffix lands on the slug (Candidate A or B); `state.config_source.resolved_commit`
records the materialized oid regardless. On `niwa apply`, the resolver:

1. Reads the slug ref. Empty -> use default branch.
2. `git ls-remote <url> <ref>` -> latest oid.
3. If oid differs from `state.resolved_commit`, fetch the new bytes into
   the cache and re-materialize.
4. If `ref` is a commit-shaped string (40 hex), the ls-remote step is
   skipped — pinned commits never re-resolve.

### URL-vs-slug duality

Today the registry stores both `source` (a filesystem path to
`workspace.toml`) and `source_url` (a slug). They are not redundant:
`source` is the *materialized* artifact's location, `source_url` is the
*upstream* identity. With subpath sourcing they remain distinct: `source`
points at `<root>/.niwa/workspace.toml` (or wherever the materializer
lands the slug), `source_url` is the upstream slug.

Non-GitHub hosts: the slug grammar should accept an optional
`<host>/<owner>/<repo>...` prefix (Candidate A's form). Detection rule:
the first segment is a host iff it contains a `.` (`gitlab.example.com/group/repo`)
or it is a literal `git@host` (rejected as malformed in slug context).
GitHub stays the default when the host segment is absent. The protocol
(`ssh` vs `https`) remains a separate concern (`[global].clone_protocol`),
not part of the slug.

### Backwards compat

Existing registry entries (all with whole-repo `source_url = "org/repo"`)
continue to work because:

1. `org/repo` parses unambiguously as
   `(host=github.com, owner=org, repo=repo, subpath="/", ref="")` under any
   of the candidate grammars.
2. The new mirror fields (`source_host`, etc.) are optional. Old entries
   missing them are parsed at read time.
3. The new `state.config_source` block is optional in v3. v2 files load
   with `config_source = nil` and are populated lazily on next `niwa apply`.

There is no read-time migration script needed; lazy population covers
both registry and state.

## Recommendation

**Adopt slug grammar Candidate A: `[host/]owner/repo[:subpath][@ref]`,
with `:` separating subpath and `@` separating ref.** Persist it as the
opaque `source_url` field (unchanged shape, evolved meaning) plus parsed
mirror fields (`source_host`, `source_owner`, `source_repo`,
`source_subpath`, `source_ref`) for fast lookup. Add a v3 `state.config_source`
block recording the resolved commit oid and fetched-at timestamp so drift
detection and reproducibility work without consulting the cache. Keep
`vault_scope` keyed on workspace name (no semantic change). The shared
brain-repo case uses a content-addressed cache keyed on `(host, owner,
repo, commit)` so multiple subpath workspaces deduplicate fetches without
any explicit user coordination. The grammar's main risk is `:` in SSH
URLs (`git@host:owner/repo.git`), but slugs and URLs occupy disjoint
syntactic niches in the registry so the conflict never materializes
inside `source_url`.

## Implications

- The slug parser becomes a shared utility (probably `internal/source`
  package) used by `init`, `config set global`, registry write, and
  status display.
- `internal/workspace/clone.go:ResolveCloneURL` must learn to ignore the
  subpath/ref portions of a slug when producing the clone URL — or, more
  cleanly, the slug parser produces a typed `Source` struct whose `.CloneURL()`
  method is what `Cloner.CloneWith` consumes. This decouples the materialization
  decision (subpath vs whole repo) from the URL resolution.
- The state schema bump (v2 -> v3) reuses the migration shim pattern
  already established for v1 -> v2 (`state.go:25-29`): old fields stay
  loadable, new fields populate lazily on next save.
- Collision detection requires the registry write path to enumerate
  existing entries and compare the parsed source tuple — a small but
  not-yet-existing query.
- `niwa status` gains a `Source:` line in the detail view, an alteration
  already tested in `status_test.go` so a snapshot update will be required.
- Telemetry, when added, must hash source identity by default; the design
  above sketches the privacy boundary but doesn't implement it.

## Surprises

- **There is no telemetry today.** The lead framed it as a fifth axis
  alongside registry/state/overlay; in fact niwa ships zero metrics
  collection. The recommendation above is forward-looking only.
- **Status hides the source URL today.** The detail view prints the
  workspace name, root, created/applied times, and per-repo status, but
  *never the upstream slug*. Users have no in-CLI way to see what URL
  produced a given workspace without reading the registry. The redesign
  is a clean opportunity to surface it.
- **`vault_scope` is decoupled from source identity.** The lead implied
  `vault_scope` might need to default off the brain repo. In fact it
  defaults off the workspace name, which is the right behavior for
  subpath sourcing — no change needed.
- **Convention overlay derivation works on the bare slug.** `DeriveOverlayURL`
  (`internal/config/overlay.go:202`) takes a URL and appends `-overlay`
  to the repo name. With Candidate A, calling it on
  `tsukumogami/vision:teams/research` would naively produce
  `tsukumogami/vision:teams/research-overlay` — wrong. The slug parser
  must be threaded through here so the overlay convention applies to the
  brain-repo identity, not the literal slug bytes.
- **`source` vs `source_url` are not redundant.** Initial reading of the
  lead suggested they could be collapsed. They serve genuinely different
  roles (materialized path vs upstream identity) and both need to remain.

## Open Questions

1. **Subpath identity vs cache identity.** Should the materialized
   snapshot path under `<workspace>/.niwa/` carry any breadcrumb
   identifying its subpath origin (e.g., a `.niwa/.source.json` sidecar)?
   It would help debugging but introduces another file the user might
   edit.
2. **Default branch resolution timing.** When a slug omits `@ref`, do we
   resolve the default branch at `niwa init` time (and pin it into the
   registry) or at every `niwa apply` (and let it drift)? Today's
   behavior is the latter; ref-less slugs would inherit it, but that
   makes `niwa status` show a moving target.
3. **`vault_scope = "@source"` shorthand.** Is this worth adding in the
   first cut? It addresses a real use case (one brain, many team
   workspaces) but is easy to defer.
4. **Telemetry inclusion.** The privacy-redacted hash design is a
   reasonable default, but it depends on whether the telemetry pipeline
   is built repo-by-repo or workspace-wide. Defer until a telemetry
   PRD/design exists.
5. **Non-GitHub host detection rule.** "First segment contains a `.`" is
   a heuristic; an org named `my.org` on GitHub would be misidentified
   (GitHub orgs cannot contain `.`, so the heuristic is safe today, but
   this is worth pinning down formally).

## Summary

Recommended slug grammar: `[host/]owner/repo[:subpath][@ref]` (Candidate A),
persisted as opaque `source_url` plus parsed mirror fields in the
registry, with `vault_scope` keyed on workspace name unchanged. Schema
impact is a registry mirror-field addition (back-compat) and an
`InstanceState.config_source` block at schema v3 carrying the resolved
commit oid and fetched-at timestamp; both migrate lazily on next write.
The biggest open question is whether ref-less slugs should pin the
resolved default branch into the registry at `niwa init` time or
re-resolve every `niwa apply` — the answer drives whether `niwa status`
shows a stable upstream identity or a moving target.
