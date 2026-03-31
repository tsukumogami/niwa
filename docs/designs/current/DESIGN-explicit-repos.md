---
status: Current
problem: |
  niwa discovers repos from GitHub org sources, but workspaces sometimes need
  repos from outside those orgs -- a config repo from a personal account, a
  shared tools repo from another org, or a fork. These external repos can't be
  added today. Critically, they also can't be referenced via repo: prefix in
  marketplace sources, which blocks cross-org plugin distribution.
decision: |
  Repos in [repos] with a url and group field are treated as explicit additions.
  They're injected into the classified repos list after discovery, bypassing
  the GitHub API and group classification. They participate in all pipeline
  steps including the repo: reference index.
rationale: |
  Reusing the existing [repos] section avoids new config syntax. The url field
  already exists on RepoOverride, and adding group is a one-field extension.
  Injecting after discovery keeps the pipeline linear -- explicit repos join
  the same classified list and flow through clone, content, materializers,
  setup scripts, and plugins identically to discovered repos.
---

# DESIGN: Explicit Repos

## Status

Proposed

## Context and Problem Statement

niwa's repo discovery is org-based: `[[sources]] org = "my-org"` queries the
GitHub API and returns all repos in that org. This works for single-org
workspaces but falls short when a workspace needs repos from multiple sources:

- A config repo from a personal account (the dot-niwa pattern)
- A shared tools repo from another org (e.g., using tsukumogami's tools repo
  in a codespar workspace for its marketplace manifest)
- A fork of an upstream repo

Today, `[repos.X]` overrides only apply to repos that were already discovered.
A repo not in any source can't be added -- and if it can't be added, it can't
be referenced via `repo:tools/.claude-plugin/marketplace.json` for marketplace
registration. This blocks cross-org plugin distribution entirely.

## Decision Drivers

- Explicit repos must be first-class: same pipeline treatment as discovered repos
- Must participate in the `repo:` reference index for marketplace sources
- Minimal config syntax -- reuse existing `[repos]` section, don't invent new blocks
- Must specify which group directory to clone into (can't auto-classify without
  GitHub API metadata)

## Considered Options

### Decision 1: How explicit repos integrate into the pipeline

How to get repos that aren't in any `[[sources]]` org into the classified
repos list and through the full pipeline.

#### Chosen: Inject into classified list after discovery

Repos in `[repos]` that have both `url` and `group` fields are treated as
explicit additions. After Step 1 (discover) and Step 2 (classify), a new
Step 2.1 scans `[repos]` for entries with `url` + `group` that aren't already
in the classified list, and injects them.

```toml
[repos.tools]
url = "git@github.com:tsukumogami/tools.git"
group = "private"
scope = "tactical"
```

This creates a `ClassifiedRepo` with the given group and a synthetic
`github.Repo` (name from the key, visibility inferred from the group,
clone URL from the `url` field). It joins the same classified list and flows
through all subsequent steps identically.

**TOML requirements:**
- `url` is required (how to clone it)
- `group` is required (which directory -- must match a defined group name)
- Other `RepoOverride` fields (`branch`, `scope`, `claude`, `env`, `files`,
  `setup_dir`, `plugins`) work as normal

**Validation:**
- `group` must reference a group defined in `[groups]`
- `url` must be a valid git clone URL
- The repo name (from the `[repos.X]` key) must not collide with a
  discovered repo

**Warning suppression:** Repos with `url` set are not flagged by
`WarnUnknownRepos` since they're intentionally outside source discovery.

#### Alternatives Considered

**New `[[explicit_repos]]` section:** A separate top-level array-of-tables
for repos not from sources. Each entry has name, url, group, and override
fields.
Rejected because it duplicates the `[repos]` structure. The `[repos]` section
already has all the right fields (`url`, `branch`, `scope`, `claude`, etc.).
Adding `group` to it is simpler than creating a parallel config block with
the same shape.

**Second source with personal account:** Add `[[sources]] org = "my-user"`
to pull all repos from the personal account. Rejected because it pulls
everything -- you'd get all public repos, not just the one you need. No
filtering mechanism exists beyond `max_repos`.

## Decision Outcome

Explicit repos reuse the existing `[repos]` section with two fields: `url`
(already exists) and `group` (new). When both are set and the repo isn't in
any source discovery result, it's injected into the classified repos list
and treated identically to discovered repos.

This means `repo:tools/.claude-plugin/marketplace.json` works regardless of
whether `tools` was discovered from a source or explicitly declared. The
`repoIndex` built in Step 6.9 includes all classified repos -- discovered
and explicit alike.

## Solution Architecture

### Overview

One new field on `RepoOverride` (`Group`), one new pipeline step (2.1), and
a change to `WarnUnknownRepos` to suppress warnings for repos with URLs.

### Components

**`RepoOverride.Group`** -- new `string` field (`toml:"group,omitempty"`).
Required for explicit repos, ignored for discovered repos (they get their
group from classification).

**Step 2.1: Inject explicit repos** -- after classification, scan `[repos]`
entries. For each with `url` and `group` set, check if the name already
appears in the classified list. If not, create a `ClassifiedRepo` and
append it.

**`WarnUnknownRepos`** -- skip repos where `URL != ""` (they're intentionally
outside discovery).

### Key Interfaces

```go
type RepoOverride struct {
    URL      string            `toml:"url,omitempty"`
    Group    string            `toml:"group,omitempty"`
    Branch   string            `toml:"branch,omitempty"`
    // ... existing fields ...
}
```

```go
// InjectExplicitRepos adds repos from [repos] with url+group that aren't
// already in the classified list.
func InjectExplicitRepos(
    classified []ClassifiedRepo,
    repos map[string]config.RepoOverride,
    groups map[string]config.GroupConfig,
) ([]ClassifiedRepo, []string, error)
```

### Data Flow

```
[[sources]] orgs
    |
    v
Step 1: Discover (GitHub API)
    |
    v
Step 2: Classify (match to groups)
    |
    v
Step 2.1: Inject explicit repos (NEW)
    |     scan [repos] for url+group entries
    |     skip if already in classified list
    |     validate group exists
    |     append ClassifiedRepo
    |
    v
Step 2.5: Warn unknown repos (skip repos with url)
    |
    v
Steps 3-7: Clone, content, materializers, setup, plugins
    (explicit repos flow through identically)
```

## Implementation Approach

### Phase 1: Add Group field and injection

- Add `Group string` to `RepoOverride`
- Implement `InjectExplicitRepos` function
- Call it between Step 2 and Step 2.5 in `runPipeline`
- Update `WarnUnknownRepos` to skip repos with `URL` set
- Tests: explicit repo injected, collision with discovered repo, invalid group

### Phase 2: Validation and edge cases

- Validate `group` references a defined group
- Validate `url` is non-empty when `group` is set (and vice versa)
- Test `repo:` references to explicit repos work in marketplace resolution
- Update scaffold template with example

## Security Considerations

Explicit repos are cloned from user-provided URLs. The same trust model applies
as source-discovered repos -- if the user declares a URL in their config, they
trust the content. The `url` field is used directly as the git clone argument,
same as `RepoCloneURL` already does for discovered repos with URL overrides.

Path containment for `repo:` references uses `checkContainment` on the resolved
path, which works identically whether the repo was discovered or explicit.

## Consequences

### Positive

- Cross-org workspaces become possible (clone from multiple orgs/users)
- `repo:` references work for repos outside source discovery
- No new config syntax -- reuses existing `[repos]` structure

### Negative

- Users must know the group name upfront (can't auto-classify without API metadata)
- Two ways to get repos into the workspace (sources vs explicit) -- could confuse

### Mitigations

- Group names are visible in `[groups]` -- users already define them
- Clear docs: sources for org-wide discovery, explicit repos for one-offs
