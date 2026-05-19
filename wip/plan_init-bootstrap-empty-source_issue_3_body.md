---
complexity: testable
complexity_rationale: |
  This issue introduces three new package-level surfaces -- `(*github.APIClient).GetRepo`,
  `workspace.ScaffoldOptions` + `workspace.ScaffoldFromSource`, and the partial
  `workspace.GitInvoker` / `stdGitInvoker` / `BootstrapParams` seam in
  `internal/workspace/bootstrap.go` -- plus an internal visibility-from-bool helper
  shared between `GetRepo` and `ListRepos`. Every change is exercisable by unit tests:
  - Byte-for-byte scaffold equality against PRD Appendix A is asserted for both
    `<vis-key>` values and for `IncludeGitkeep: true`/`false`.
  - The R17 soft-fail note text is asserted verbatim for each of the four `<cause>`
    substrings (`network error`, `authentication`, `not found`, `server error`)
    using fake `*github.StatusError` values produced by <<ISSUE:1>>.
  - The R16 invariant is tested with an adversarial fixture where `Repo.Private`
    and `Repo.Visibility` disagree and the `Visibility` string contains TOML
    metacharacters; the scaffold output proves the string is not consulted.
  - The N5 no-secret-on-disk invariant is tested by passing a known fixture token
    and asserting it never appears in the scaffold output bytes.
  - GetRepo + ListRepos consistency is exercised by a table-driven test that
    feeds the same `Private` bool through both call sites and asserts identical
    `Visibility` strings.
  No user-visible behavior changes in this issue (the scaffold function is wired
  but not yet called; flags from <<ISSUE:2>> still hit the stub), so there is
  nothing to verify end-to-end -- the unit-test surface is sufficient.
---

## Goal

Add `(*github.APIClient).GetRepo`, `workspace.ScaffoldFromSource` with `ScaffoldOptions`, and the `GitInvoker` / `BootstrapParams` test seam -- all unit-tested in isolation, with no `RunBootstrap` body yet and no change to user-visible behavior.

## Context

Phase 3 builds the three composable pieces that the bootstrap orchestrator will chain in <<ISSUE:4>>: a single-repo metadata lookup, a scaffold writer that emits PRD Appendix A byte-for-byte, and the test-injectable git seam. Splitting them out lets each be unit-tested against its PRD contract before any orchestration logic depends on them. The R16 invariant -- visibility derived only from the `Repo.Private` bool, never from the remote-controlled `Visibility` string -- is enforced structurally here by typing `ScaffoldOptions.Private` as `bool` and never reading the string field in the scaffold code path.

Design: `docs/designs/DESIGN-init-bootstrap-empty-source.md`

## Acceptance Criteria

- [ ] New method `(*github.APIClient).GetRepo(ctx context.Context, owner, repo string) (*Repo, error)` in `internal/github/client.go` returns the existing `*Repo` struct on HTTP 200 and a `*github.StatusError` (from <<ISSUE:1>>) on any non-2xx response. URL pattern follows the established `ListRepos` style (`GET /repos/{owner}/{repo}`).
- [ ] The `Repo.Private` (bool) -> `Repo.Visibility` (string) normalization that `ListRepos` currently performs inline (the `if repos[i].Visibility == ""` block) is extracted into a package-internal helper. `GetRepo` and `ListRepos` both call it so visibility strings are consistent across the two entry points.
- [ ] **Load-bearing R16 invariant**: `ScaffoldFromSource` derives `[groups.<vis>]` from `Repo.Private` (bool), NOT from `Repo.Visibility` (string). `ScaffoldOptions.Private` is typed `bool`. The docstring on `ScaffoldFromSource` explicitly states that a future refactor must not silently switch to a string-derived visibility -- doing so would require changing the struct field type, which is a visible change reviewers will catch.
- [ ] New `workspace.ScaffoldOptions` struct in `internal/workspace/scaffold.go` with fields: `Name string`, `Org string`, `Repo string`, `Private bool`, `IncludeGitkeep bool`.
- [ ] New `workspace.ScaffoldFromSource(dir string, opts ScaffoldOptions) error` in `internal/workspace/scaffold.go`. Sibling of the existing `Scaffold(dir, name)` function; the existing function and all its callers are untouched.
- [ ] Scaffold body matches PRD Appendix A byte-for-byte after `<placeholder>` substitution. Visibility key mapping is:
  - `Private: true` -> `[groups.private]` with `visibility = "private"`
  - `Private: false` -> `[groups.public]` with `visibility = "public"`
- [ ] **R15 .gitkeep**: when `opts.IncludeGitkeep` is true, `ScaffoldFromSource` writes an empty `.niwa/claude/.gitkeep` file (zero bytes) alongside `.niwa/workspace.toml`. Production callers always pass `true`; unit tests may pass `false` to suppress filesystem side effects when asserting toml-only output.
- [ ] **R17 soft-fail**: callers of `GetRepo` whose lookup fails (network error, 401, 403, 404, 5xx) fall back to `opts.Private = false` and emit the exact PRD R17 stderr `note:` line with the appropriate `<cause>` substring. The four `<cause>` values map as:
  - network / dial / DNS error -> `network error`
  - `*github.StatusError` with `StatusCode == 401 || StatusCode == 403` -> `authentication`
  - `*github.StatusError` with `StatusCode == 404` -> `not found`
  - `*github.StatusError` with `StatusCode >= 500` -> `server error`
- [ ] A shared helper produces the schema doc-link footer (`# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md ...`) that is reused between the existing `Scaffold` and the new `ScaffoldFromSource`. Extraction only -- no behavior change for the existing scaffold's output bytes.
- [ ] New file `internal/workspace/bootstrap.go` (partial) contains:
  - `GitInvoker` interface with single method `CommandContext(ctx context.Context, args ...string) *exec.Cmd`
  - `stdGitInvoker` concrete implementation whose `CommandContext` returns `exec.CommandContext(ctx, "git", args...)`
  - `BootstrapParams` struct with fields: `WorkspaceRoot string`, `WorkspaceName string`, `Src source.Source`, `Fetcher FetchClient`, `GitInvoker GitInvoker`, `Reporter *Reporter`
- [ ] **NO `RunBootstrap` body yet** -- the function body lands in <<ISSUE:4>>. This issue declares only the supporting types and the test seam.
- [ ] Unit tests cover:
  - `ScaffoldFromSource` byte-equality against PRD Appendix A for `Private: true` and `Private: false`
  - `ScaffoldFromSource` byte-equality for both `IncludeGitkeep: true` (file exists, zero bytes) and `IncludeGitkeep: false` (file absent)
  - R16 visibility-from-bool with an adversarial fixture: `Repo.Private` and `Repo.Visibility` disagree, and `Repo.Visibility` contains TOML-injection-shaped metacharacters (e.g. `"private"]\n[malicious]\ninjected = "yes"`). The scaffold output proves only the bool was consulted.
  - R17 note text per cause: server, network, auth, not-found -- each asserted as an exact-substring match against the PRD R17 string with the correct `<cause>` token
  - `GetRepo` + `ListRepos` consistency via the shared visibility helper: feed the same `Private` bool through both call sites and assert identical resulting `Visibility` strings
- [ ] **N5 no-secret-on-disk**: a unit test sets `GH_TOKEN=test-fixture-token-DEADBEEF`, invokes `ScaffoldFromSource`, and asserts the token literal never appears anywhere in the output bytes (workspace.toml content + .gitkeep content).
- [ ] **User-visible state after this issue: NO CHANGE.** The scaffold function is unit-tested but not yet called from `runInit`; the flags introduced in <<ISSUE:2>> still dispatch into the stub. `RunBootstrap`'s body lands in <<ISSUE:4>>.

## Dependencies

- Blocked by <<ISSUE:1>>: `GetRepo` returns `*github.StatusError` directly, and the R17 cause-classification logic depends on the typed error introduced there.

## Downstream Dependencies

- <<ISSUE:4>>'s `RunBootstrap` body calls `ScaffoldFromSource` (after deriving `ScaffoldOptions.Private` from `GetRepo` + R17 soft-fail) and consumes the `GitInvoker` interface and `BootstrapParams` struct declared here. The struct shape and the interface method signature are frozen by this issue.
