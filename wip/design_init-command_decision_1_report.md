### Decision 1: How should `niwa init --from <org/repo>` fetch and install a remote config repo?

**Context**: The `niwa init --from <org/repo>` command needs to fetch a remote config
repo and place it at `.niwa/` in the workspace root. This directory becomes the
workspace's config home, containing `workspace.toml` and content files. The design
requires that `.niwa/` be a git checkout so that `niwa update` can later pull changes.
The PRD requires commit/tag pinning for security and a `--review` flag for inspecting
config before registration.

**Assumptions**:
- Git is available on the user's system. This is a safe assumption for a developer
  tool -- git is a near-universal dependency in development environments.
- The existing `Cloner` in `internal/workspace/clone.go` already wraps git clone
  operations and can be extended for this use case.
- The `niwa update` command will need to run `git pull` or `git fetch` + `git checkout`
  in `.niwa/`, which requires `.niwa/` to be a proper git repo (not an extracted archive).
- Config repos are small (a few TOML files and templates), so clone depth and size
  are not meaningful concerns.
- The `CloneProtocol` setting in `GlobalConfig` already provides HTTPS vs SSH selection.

**Chosen**: Git clone (shallow)

Use `git clone --depth 1` to clone the config repo directly as `.niwa/`. When a
specific tag or commit is provided, clone at that ref. The existing `Cloner` struct
gets a new method (e.g., `CloneShallow`) that adds `--depth 1` and optional
`--branch <tag>` support. For commit pinning, the flow is: shallow clone default
branch, then `git checkout <commit>`.

Implementation sketch:
1. Resolve the clone URL from `<org/repo>` using `CloneProtocol()` (HTTPS or SSH).
2. If a tag/version is specified, pass it as `--branch` to the shallow clone.
3. If a commit SHA is specified, clone then checkout the specific commit.
4. If `--review` is set, clone to a temp directory, display the workspace.toml
   contents, prompt for confirmation, then move to `.niwa/` (or abort and clean up).
5. Register the source in the global registry at `~/.config/niwa/config.toml`.

**Rationale**:
- `.niwa/` must be a git repo to support `niwa update`. This rules out the tarball
  download approach (alternative 2) since it produces a plain directory with no git
  history or remote tracking.
- Git is the only external dependency niwa already assumes (the `Cloner` uses it).
  Adding `gh` CLI as a dependency (alternative 3) would violate the self-contained
  philosophy and add a runtime requirement users may not have.
- Shallow clone keeps the initial fetch fast and small. Config repos are tiny, so
  even a full clone would be fine, but `--depth 1` is a free optimization.
- The existing `Cloner` infrastructure handles URL construction, branch selection,
  and directory creation. Extending it is straightforward and keeps clone logic
  centralized.
- Git's native ref handling makes tag/commit pinning simple -- no need to build
  custom version resolution.

**Alternatives Considered**:

1. **Full git clone**: Works but fetches unnecessary history. For config repos this
   barely matters (they're small), but shallow clone is strictly better with no
   downside. `niwa update` can deepen the clone if needed (`git fetch --unshallow`).

2. **GitHub API tarball download**: Download via
   `https://api.github.com/repos/{org}/{repo}/tarball/{ref}` and extract into `.niwa/`.
   This avoids requiring git, but produces a plain directory. `niwa update` would need
   to re-download and diff/replace files, losing git's merge capabilities. Also couples
   niwa to GitHub's API (no GitLab, Bitbucket, or self-hosted support). The existing
   `github.APIClient` only handles org repo listing, so this would need new API surface
   for a suboptimal result.

3. **`gh repo clone`**: Delegates to GitHub's CLI tool. Adds an external dependency
   that users may not have installed. Provides no advantage over raw git clone since
   niwa already constructs clone URLs from the protocol setting. Would make niwa
   less self-contained.

**Consequences**:
- Git remains the sole external runtime dependency (already assumed).
- The `Cloner` struct gains a shallow clone method, keeping clone logic in one place.
- `.niwa/` is a full git repo, so `niwa update` can use standard git operations
  (fetch, checkout, merge).
- Tag and commit pinning work through git's native ref system.
- The approach is host-agnostic: any git remote works (GitHub, GitLab, Bitbucket,
  self-hosted), not just GitHub.
- `--review` implementation is straightforward: clone to temp, inspect, then move
  or discard.
