# Decision 1: Repo and Group Schema Structure

## Question

How should repos and groups be structured in the TOML schema?

## Options Evaluated

### Option A: Flat [[repos]] with group attribute

All repos listed as a top-level array of tables. Group is a string field on each repo.

```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[[repos]]
name = "tsuku"
group = "public"

[[repos]]
name = "koto"
group = "public"

[[repos]]
name = "vision"
group = "private"
url = "git@github.com:tsukumogami/vision.git"
```

**Pros:**
- Simple flat structure, easy to scan all repos at a glance
- Adding a repo is one block, no nesting to navigate
- Straightforward Go unmarshalling: `[]RepoConfig` with a `Group string` field
- Group metadata (if needed) would go in a separate `[groups.public]` section

**Cons:**
- Group assignment is a stringly-typed reference -- typos produce orphaned repos with no compile-time safety
- No visual clustering by group; repos from different groups interleave freely
- Group metadata (CLAUDE.md path, visibility, directory) must be defined separately, creating two places to look
- Scaling to 20+ repos: long flat list with no visual structure

### Option B: Nested [groups.\<name\>.repos]

Repos declared inside their group using TOML's nested table syntax.

```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[groups.public]
visibility = "public"

[groups.public.repos.tsuku]

[groups.public.repos.koto]

[groups.public.repos.niwa]

[groups.public.repos.shirabe]

[groups.private]
visibility = "private"

[groups.private.repos.vision]
url = "git@github.com:tsukumogami/vision.git"

[groups.private.repos.tools]
url = "git@github.com:tsukumogami/tools.git"
```

**Pros:**
- Group membership is structural, not a string reference -- impossible to assign a repo to a nonexistent group
- Visual clustering: repos are physically nested under their group
- Group metadata lives right next to its repos
- Go unmarshalling: `map[string]GroupConfig` where `GroupConfig` has `Repos map[string]RepoConfig`

**Cons:**
- TOML dotted headers get verbose: `[groups.public.repos.tsuku]` is long
- Adding properties to a repo requires a full header line per property, or inline tables
- For repos with zero overrides, you still need the header line (though it's minimal)
- Deeply nested keys are harder to type from memory

### Option C: Separate [[groups]] and [[repos]] with group reference

Groups and repos as independent arrays of tables, with repos referencing groups by name.

```toml
[[groups]]
name = "public"
visibility = "public"

[[groups]]
name = "private"
visibility = "private"

[[repos]]
name = "tsuku"
group = "public"

[[repos]]
name = "vision"
group = "private"
url = "git@github.com:tsukumogami/vision.git"
```

**Pros:**
- Clean separation of group definitions and repo definitions
- Groups and repos each have their own array, easy to iterate in Go
- Familiar relational pattern (foreign key reference)

**Cons:**
- Same stringly-typed reference problem as Option A
- Two separate sections that must stay in sync
- More boilerplate than A (explicit group definitions) without the structural safety of B
- Parsing requires a validation pass to check that every repo's group exists in the groups array
- Worst of both worlds: neither as terse as A nor as safe as B

### Option D: Hybrid -- [groups] defines metadata, [[repos]] references groups

Groups defined as a table of tables for metadata, repos as a flat array referencing them.

```toml
[workspace]
name = "tsuku"
org = "tsukumogami"

[groups.public]
visibility = "public"

[groups.private]
visibility = "private"

[[repos]]
name = "tsuku"
group = "public"

[[repos]]
name = "vision"
group = "private"
url = "git@github.com:tsukumogami/vision.git"
```

**Pros:**
- Group metadata has a clear home in `[groups.*]`
- Repos remain flat and scannable
- Go unmarshalling: groups as `map[string]GroupConfig`, repos as `[]RepoConfig`

**Cons:**
- Still stringly-typed group references (typos create invalid configs)
- Two places to look (same as C but slightly more compact for groups)
- Mixed paradigms: groups use map tables, repos use array tables

## Chosen Approach: Option B -- Nested [groups.\<name\>.repos]

### Rationale

The structural safety of nesting repos inside their groups outweighs the verbosity cost. The key arguments:

1. **Structural correctness by construction.** A repo physically inside `[groups.public.repos.*]` can't reference a nonexistent group. This eliminates an entire class of validation logic and user errors. The other three options all require a post-parse validation pass to detect orphaned group references.

2. **Matches the filesystem layout.** The workspace directory structure is `workspace/public/tsuku/`, `workspace/private/vision/`. The config structure mirrors this: `groups.public.repos.tsuku`, `groups.private.repos.vision`. Config and disk align, which reduces cognitive load.

3. **Group metadata co-located with repos.** Visibility, CLAUDE.md references, and other group-level properties sit directly above the repos they govern. You don't need to cross-reference between sections.

4. **Convention over configuration keeps it terse.** Most repos in the tsukumogami workspace need zero overrides -- they use the org shorthand for URL, default branch, and no special settings. A header-only line like `[groups.public.repos.koto]` is 34 characters. That's acceptable boilerplate for structural safety.

5. **Go parsing is clean.** The struct maps directly:

```go
type WorkspaceConfig struct {
    Workspace WorkspaceMeta              `toml:"workspace"`
    Groups    map[string]GroupConfig     `toml:"groups"`
}

type GroupConfig struct {
    Visibility string                    `toml:"visibility,omitempty"`
    Repos      map[string]RepoConfig    `toml:"repos"`
}

type RepoConfig struct {
    URL    string `toml:"url,omitempty"`
    Branch string `toml:"branch,omitempty"`
    Scope  string `toml:"scope,omitempty"`
}
```

6. **Scales adequately.** At 20+ repos, the nesting actually helps readability by grouping related repos visually. The flat options (A, C, D) would become a wall of `[[repos]]` blocks where group membership is buried in a field.

### Concrete Example: tsukumogami workspace

```toml
[workspace]
name = "tsuku"
org = "tsukumogami"
default_branch = "main"

[groups.public]
visibility = "public"

[groups.public.repos.tsuku]

[groups.public.repos.koto]

[groups.public.repos.niwa]

[groups.public.repos.shirabe]

[groups.public.repos.".github"]
claude = false

[groups.private]
visibility = "private"

[groups.private.repos.vision]
url = "git@github.com:tsukumogami/vision.git"
scope = "strategic"

[groups.private.repos.tools]
url = "git@github.com:tsukumogami/tools.git"
```

**Notes on the example:**

- Repos with no overrides (tsuku, koto, niwa, shirabe) are single-line headers. The URL defaults to `https://github.com/{org}/{name}.git`.
- `.github` requires quoting in the TOML key due to the dot. This is valid TOML but worth noting. The `claude = false` flag marks it as a repo with no Claude Code configuration.
- Private repos use explicit SSH URLs since they can't be cloned over HTTPS without auth tokens. (This could also be handled by a group-level `url_scheme = "ssh"` default in a future iteration.)
- The `scope` override on vision marks it as strategic (default would be tactical per the CLAUDE.md conventions).
- Group directories on disk are `public/` and `private/` -- the group name IS the directory name by convention.

### Edge Cases Considered

- **Repos not in any group**: Not supported by design. Every repo belongs to a group. If someone wants an ungrouped repo, they create a group for it (e.g., `[groups.standalone]`).
- **Multiple orgs**: A repo can override `url` to point anywhere. The workspace-level `org` is just a default for URL construction.
- **Empty groups**: Valid TOML -- `[groups.experimental]` with no repos under it. Harmless, could be used as a placeholder.

## Rejected Options

- **A (Flat [[repos]])**: No structural safety for group membership. Stringly-typed references require validation. No visual grouping.
- **C (Separate arrays)**: Worst option -- all the downsides of string references plus more boilerplate than A. Two parallel arrays that must stay synchronized.
- **D (Hybrid)**: Marginal improvement over A/C but still stringly-typed. Mixed table styles (map vs array) add inconsistency without meaningful benefit.
