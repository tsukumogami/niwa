# Lead: What proves a refactor changed no behavior?

## A. What the Existing Suite Covers

`internal/workspace` has 41 test functions in `apply_test.go` alone, plus 118
more across `materialize_test.go`, `content_test.go`, `root_materializer_test.go`,
and `worktree_content_test.go`. Every one I sampled follows the same shape:
build a fixture config and repo tree in `t.TempDir()`, run `Create`/`Apply`/a
single materializer, then assert on a handful of named paths.

`TestCreateIntegration` (`apply_test.go:76`) is the broadest integration test
in the package — it exercises workspace, group, repo, and subdir content
installation plus auto-discovery in one run — and it still only calls
`assertFileContains`/`assertFileNotContains` on six specific paths
(`apply_test.go:189-205`: root `CLAUDE.md`, `public/CLAUDE.md`,
`public/app/CLAUDE.local.md`, the docs-subdir file, and the auto-discovered
`private/secrets/CLAUDE.local.md`). It loads the resulting state
(`apply_test.go:208`) but never asserts on `state.ManagedFiles` as a set — no
length check, no comparison against an expected list of paths.

`TestApplyCleanupRemovedFiles` (`apply_test.go:822`) is the test closest to a
completeness check: it creates an instance with workspace content, removes
that content from config, re-applies, and checks that exactly one named file
(`CLAUDE.md`) was removed (`apply_test.go:884-918`). It does not check that
*only* that file was removed, or enumerate what remains.

I grepped all five files for `filepath.Walk`/`WalkDir` — the only test in the
package that walks a directory tree is `worktree_secret_ref_test.go:84`, and
it walks a single worktree's env-output directory to check permission bits on
whatever `.local.env` files it finds, not to enumerate a produced-file set
against an expectation. No test in `apply_test.go`, `materialize_test.go`,
`content_test.go`, `root_materializer_test.go`, or `worktree_content_test.go`
enumerates the full set of files written by an apply and compares it against
an expected manifest.

**Conclusion: the suite asserts on a curated subset of paths per test, chosen
by whatever behavior that test targets. It has no test whose job is "list
everything produced, compare to expected." A refactor that starts writing an
extra file, stops writing a file no existing test happens to check, or
silently changes the *content* of a file no test inspects would pass the
entire suite unchanged.**

## B. The Produced-File Manifest

There already is a produced-file manifest, and it is closer to complete than
round 1's note suggested — but it is a manifest of the *instance-owned* tree
only, not of every side effect apply makes.

`runPipeline` in `apply.go` accumulates every write into a single
`writtenFiles []string` (`apply.go:772`) via eight append sites, one per
pipeline stage:

- `apply.go:1444-1448` — `InstallWorkspaceContent` (root CLAUDE.md/AGENTS.md)
- `apply.go:1465-1469` — `InstallWorkspaceContext`
- `apply.go:1475-1480` — `InstallOverlayClaudeContent`
- `apply.go:1484-1488` — `InstallWorkspaceRootSettings`
- `apply.go:1498-1502` — `InstallGroupContent`, once per group
- `apply.go:1507-1511` — `InstallGlobalClaudeContent`
- `apply.go:1521-1528` — `InstallRepoContent`, once per repo
- `apply.go:1602-1623` — `runRepoMaterializers` (hooks, settings, env), once per repo

Step 6.6 (`apply.go:1636-1660`, `refreshWorktreeEnvs` at `apply.go:1915`)
appends already-hashed `ManagedFile` values directly (`apply.go:1725`), not
raw paths, for worktree env refresh — both freshly written files and
forward-carried entries for skipped-but-live worktrees.

Step 7 (`apply.go:1695-1718`) then turns every path in `writtenFiles` into a
`ManagedFile{Path, ContentHash, Sources, SourceFingerprint, Generated}`
(`state.go:184-190`) by hashing it with `HashFile` (`state.go:641`, SHA-256,
`"sha256:"`-prefixed) and pairs it with any recorded source provenance. This
becomes `InstanceState.ManagedFiles`, persisted to `.niwa/instance.json` by
`SaveState` (`state.go:322`).

**So: paths AND content hashes, not just paths.** This is a real oracle — for
the instance-tree files it covers, comparing `ManagedFiles` sets (path +
hash, ignoring `Generated` timestamps) before and after a refactor is
equivalent to comparing full file contents, without needing to read the
files again.

**Completeness gaps** — two kinds of apply-time writes never enter
`writtenFiles` and therefore never enter `ManagedFiles`:

1. Step 6.75 `RunSetupScripts` (`apply.go:1670-1693`) executes arbitrary
   repo-provided scripts; their filesystem side effects are not tracked at
   all (they may write anywhere, and only pass/fail status is recorded).
2. Step 8/9 `healDanglingPluginRecords` (`apply.go:1767-1787`) and
   `reconcileMarketplaceRegistry` (`apply.go:1798-1809`) mutate Claude Code's
   *global* registry files outside the instance root — no path from either
   is added to `writtenFiles`.

Also dead: `InstallRepoContentTo` (`content.go:130`) and
`installWorktreeContextLayer` (`worktree_content.go:740`), flagged in round 1
as having zero callers, are irrelevant to this manifest since nothing calls
them — they don't corrupt the record, they're just unreachable.

For the agent-capability-contract refactor specifically — which round 1
scoped to the context-file/materializer pipeline, not to global-registry
reconciliation or setup scripts — the manifest's coverage lines up well with
the actual blast radius of PR 1.

## C. Candidate Mechanisms and Verdicts

**1. Golden/characterization snapshot added before the refactor.**
Verdict: **sufficient, with normalization work required first.**

The repo has the infrastructure to make this cheap: `t.TempDir()`-based
fixture builders are the existing idiom in every test in this package
(`apply_test.go:77` and the same pattern repeated 40+ times), and
`mockGitHubClient` plus a hand-built `.niwa/workspace.toml` + content-dir
fixture (as in `TestCreateIntegration`) is enough to drive a full `Create`.
There is no dedicated golden-file helper (`assertFileContains` is the closest
existing primitive), but building one is a small addition, not new
infrastructure — walk the instance root, collect (relative path, content
hash) pairs (or full content for small files), sort, serialize, compare
against a checked-in fixture. `ManagedFiles` from part B gives this almost
for free: run apply, read `state.ManagedFiles`, strip `Generated` and
absolute-path-derived fields, sort by `Path`, and diff the (path, hash) pairs
against a checked-in expectation — no directory walk needed, and it exercises
the exact code path production uses to decide what's "managed."

The `localGitServer` helper mentioned in workspace conventions
(`test/functional/localrepo_test.go:13`) belongs to the functional suite, not
`internal/workspace`'s unit tests — it manages bare git repos for the
black-box CLI tests, not fixture workspaces for unit-level golden tests. It's
not directly reusable here but confirms the "build fixtures under a temp
dir" pattern is already load-bearing elsewhere.

Nondeterminism (detailed in D) has to be normalized before this works:
absolute instance-root paths get baked into content via the `{workspace}`
template variable, and the running test binary's `os.Executable()` path gets
baked into worktree-delegation hook commands. Both are fixable by string-
replacing the known fixture root/binary path with a placeholder before
hashing/comparing — normalization, not avoidance.

**2. Functional Gherkin suite, before/after tree diff.**
Verdict: **necessary-but-not-sufficient, and expensive to automate as a
gate.**

The functional suite runs against the built binary and is genuinely
black-box, so it would catch a real behavior regression. But per the
CLAUDE.md workspace constraint, I did not execute it (running it concurrently
with another run in the same checkout is disallowed, and I have no isolated
checkout for a second run here). Structurally: it's a slower, coarser
version of option 1 — same idea (diff the produced tree) without the
`ManagedFiles` shortcut, and it tests through the CLI/config layer rather
than the `Applier` API directly, so it also exercises argument parsing and
output formatting that aren't part of the preparation-path refactor's
concern. Worth running once manually as a sanity check before merging PR 1,
not worth building into an automated before/after diff gate — option 1 gives
the same guarantee more cheaply at the unit level.

**3. Relying on the existing unit suite alone.**
Verdict: **not worth it — this is exactly the "weak proof" the brief flagged.**
Part A shows precisely why: the suite asserts on the paths each test author
thought to check, not on completeness. It would catch a materializer that
stopped firing for a repo it used to fire for, if some test happens to check
that repo's file. It would not catch a new file appearing, an existing
untested file's content silently changing, or a file that no test's
assertion list includes being dropped.

**4. Commit-level discipline (pure mechanical refactor, reviewable as such).**
Verdict: **necessary-but-not-sufficient, and cheap.** A diff that is visibly
"parameter added, call sites updated, no branch logic changed" is easy for a
human reviewer to verify by inspection and costs nothing extra to produce if
PR 1 is actually mechanical. It's a real signal but not a mechanical proof —
it depends on the reviewer's attention and doesn't cover interactions the
diff's shape doesn't make obvious (e.g., a changed default value threaded
through many call sites can look mechanical and still change output). Best
paired with 1, not substituted for it.

**Recommendation for PR 1:** pair 1 and 4. Add a `ManagedFiles`-based
characterization test *before* starting the refactor (so it's proven to pass
on `main` first), normalize the two known nondeterminism sources, commit it
as its own preparatory commit, then do the refactor as a visibly mechanical
diff reviewable under 4. Run the functional suite once by hand as a final
sanity check (option 2) but don't build tooling around it.

## D. Nondeterminism Audit

Sources checked and their status:

- **`time.Now()`**: `apply.go:405` (`Create`) and `apply.go:563` (`Apply`)
  produce the `now` used only for `ManagedFile.Generated` and
  `InstanceState.Created`/`LastApplied` (state.go:126-127, 189) — timestamp
  *metadata*, not embedded in the content of any produced file. Also
  `session_map.go:102` and `snapshotwriter.go:189,414` for session/provenance
  timestamps, both outside the apply-content path. **Not a risk for
  content-hash comparison** if the golden test strips `Generated`/timestamp
  fields before comparing `ManagedFiles` (which it must do anyway, since
  those fields change on every run by design).

- **`os.Hostname`**: no occurrence in `internal/workspace/*.go` (non-test).
  **Not a risk.**

- **`math/rand`/`crypto/rand`**: no occurrence in `internal/workspace/*.go`
  (non-test). **Not a risk.**

- **Map-iteration order**: the package is already disciplined here. Three
  dedicated sort helpers exist specifically to defeat Go's randomized map
  iteration before writing output: `sortedKeysSettings` (`materialize.go`,
  serving `config.SettingsConfig`), `sortedKeys` (`materialize.go`, serving
  `MaybeSecret` maps, with the explicit comment "Used to make
  sources/provenance lists deterministic regardless of Go's map-iteration
  randomization"), and an inline sort in the env materializer
  (`materialize.go:1540-1548`, comment: "Build an ordered item slice (sorted
  keys)... so output is deterministic and the default dotenv target stays
  byte-identical to niwa's historical .local.env"). `HooksMaterializer`
  (`materialize.go:234`, `worktree_content.go:94,121`) iterates
  `map[string][]HookEntry` keyed by event name to build per-event output
  directories — order there affects only directory-creation sequencing, not
  written byte content, since each event gets its own subdirectory. **Not a
  risk for the files that matter**, and the pattern shows the authors were
  already aware of this failure mode and designed against it.

- **Absolute paths embedded in generated content**: real and the most
  material risk found. `InstallWorkspaceContent`/`InstallGroupContent`/
  `InstallRepoContent` (`content.go:39,68,140`) bind `"{workspace}"` to
  `absInstance := filepath.Abs(instanceRoot)` and substitute it into content
  files verbatim when a template uses `{workspace}` (demonstrated in
  `TestCreateIntegration`, `apply_test.go:117`: `"Root: {workspace}\n"`).
  Since `t.TempDir()` produces a different absolute path every test run, any
  content file using `{workspace}` will differ byte-for-byte across runs.
  **Not normalized today** — a golden test must either avoid `{workspace}` in
  its fixture content, or normalize by string-replacing the known fixture
  root before hashing/comparing.

- **User/home-dir and binary-path interpolation**: `WorktreeDelegation.NiwaPath`
  (`apply.go:1547`, `os.Executable()`) is the resolved path of the running
  niwa binary, embedded into worktree-hook commands via the settings
  materializer (`root_materializer.go:82`, `materialize.go:344` document the
  same field). Under `go test`, `os.Executable()` resolves to the test
  binary's path in a per-run temp build directory, so this also differs
  between invocations — and, notably, would differ between a "before" and
  "after" test run even with byte-identical logic, since Go rebuilds the
  test binary. **Not normalized today.** A characterization test must either
  inject a fixed `NiwaPath` (the `Applier` threads it as data, so this is a
  seam, not a rewrite) or strip/normalize that one field before comparison.

## Recommendation

Add a `ManagedFiles`-based characterization test in `apply_test.go` (or a new
file) before starting the refactor: build one or two representative fixture
workspaces (reuse the `TestCreateIntegration`/`TestCreateMaterializersIntegration`
patterns), run `Create`, then assert the sorted `(Path, ContentHash)` pairs
from `state.ManagedFiles` against a checked-in expected list — after
normalizing the two real nondeterminism sources (avoid `{workspace}` in
fixture content, or normalize it out; inject a fixed `NiwaPath` if the
fixture exercises worktree delegation). Commit this test passing on `main`
first, so it's established as a true characterization of current behavior,
not written to match the refactored code. Then do the refactor as a visibly
mechanical diff and let this test plus the full existing suite gate it. This
is materially stronger than "existing suite passes," is cheap (the manifest
already exists, no new hashing infrastructure needed), and its known blind
spots (setup-script side effects, global Claude Code registry mutations) are
outside PR 1's scoped blast radius per round 1's findings, so they don't
weaken the proof for the PR this actually gates.

## Open Questions

- Should the characterization test hash full content or just record
  `ContentHash` from `ManagedFiles`? Hashing gives no diagnostic on failure
  (a changed hash doesn't say *what* changed); storing small files' full
  content in the fixture would make failures more debuggable at the cost of
  a larger checked-in fixture. Round 2 didn't need to resolve this to answer
  the mechanism question, but whoever writes the test should decide it.
- Does PR 1's scope (per round 1's tension about how much of the pipeline to
  bring under the contract) change which materializers the characterization
  fixture needs to exercise? A fixture that only covers context files
  under-tests the refactor if PR 1 also touches hooks/settings/env
  materializers.

## Summary

The existing suite asserts on a hand-picked subset of paths per test and has
no completeness check — a real gap, confirmed by reading `apply_test.go`,
`materialize_test.go`, `content_test.go`, `root_materializer_test.go`, and
`worktree_content_test.go` directly. But niwa already builds a near-complete
produced-file manifest with content hashes at apply time
(`InstanceState.ManagedFiles`, `apply.go:1695-1718`) that covers everything
PR 1's scoped blast radius touches, so a `ManagedFiles`-based
characterization test — not a new golden-file mechanism — is the cheap,
sufficient mechanical proof, once two identified nondeterminism sources
(the `{workspace}` absolute-path template variable and `os.Executable()` in
worktree-delegation hook commands) are normalized.
