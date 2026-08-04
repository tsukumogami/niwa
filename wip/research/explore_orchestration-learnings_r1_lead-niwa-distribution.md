# niwa file distribution: how a canonical `_common.md` could ship

Read-only investigation of the niwa repo at
`public/niwa/.claude/worktrees/orchestration-learnings`. Every mechanical claim
below carries a `file:line` citation. Where I could not establish something, I
say so.

---

## 1. Root skill installation

**It is `go:embed`.** `internal/workspace/root_materializer.go:25-26` declares:

```go
//go:embed rootskills
var rootSkillsFS embed.FS
```

The embedded tree currently has exactly one member:
`internal/workspace/rootskills/dispatch/SKILL.md` (confirmed by
`find internal/workspace/rootskills -type f`).

**The function that writes it** is `writeRootSkills(workspaceRoot string)` at
`internal/workspace/root_materializer.go:189-223`. It walks the embedded FS,
picks up every `rootskills/<name>/SKILL.md`, derives the skill name from the
parent directory (`root_materializer.go:201`), and writes to
`<workspaceRoot>/.claude/skills/<name>/SKILL.md` via `os.WriteFile(..., 0o644)`
at `root_materializer.go:213`. The target path is assembled from
`rootClaudeDir = ".claude"` (`root_materializer.go:43`) and
`rootSkillsTargetDir = "skills"` (`root_materializer.go:34`).

**When it runs.** `writeRootSkills` is called from exactly one place:
`MaterializeWorkspaceRoot` at `root_materializer.go:147`. That function has
exactly two production call sites:

- `internal/cli/apply.go:238` — guarded by
  `if scope.Mode == workspace.ApplyAll && scope.WorkspaceRoot != ""`
  (`apply.go:237`). So it runs on `niwa apply` **only at workspace-root scope**.
  An apply scoped to a single instance, or run from inside an instance, does not
  reach it. It *does* run under `--no-cascade`, which caps the operation at the
  root (`apply.go:230-233`, `apply.go:246-253`).
- `internal/cli/init.go:779` — guarded by
  `if !initNoEphemeralSessions && rootConfigInstalls(mode)` (`init.go:771`).
  `rootConfigInstalls` returns true only for `modeNamed` and `modeClone`
  (`init.go:1004-1006`); the bare no-args scaffold mode is deliberately excluded
  (`init.go:997-1003`). Failure here is non-fatal — it prints a warning
  (`init.go:784`).

**`niwa create` does NOT install root skills.** Grep for
`MaterializeWorkspaceRoot|writeRootSkills` across the repo returns only
`root_materializer.go`, `root_materializer_test.go`, `cli/apply.go`,
`cli/init.go`, and one comment in
`test/functional/steps_init_workspace_dir_test.go:130`. Neither `niwa create`
nor `niwa dispatch` re-materializes the workspace root.

**Overwrite semantics: unconditional, every time.** There is no existence check.
`root_materializer.go:186-188` states it plainly:

> "The writes are plain overwrites — the content is static, so re-running is
> idempotent."

`internal/cli/apply.go:233-237` says the same from the caller's side:

> "MaterializeWorkspaceRoot is content-idempotent — it produces the same bytes
> when the config is already current — but it does not skip the write: it
> rewrites the root-managed files via unconditional os.WriteFile on every apply."

**Consequence for a user who edits the installed copy:** the edit is silently
destroyed on the next root-scope `niwa apply`. There is no drift detection, no
warning, and no managed-file record for workspace-root files — see §3.

**Tests.** Unit: `TestMaterializeWorkspaceRoot_DispatchSkill` at
`internal/workspace/root_materializer_test.go:193-232` — asserts the file exists
at `.claude/skills/dispatch/SKILL.md`, starts with `---` frontmatter, contains
`name: dispatch` and `# /dispatch`, and that the path appears in the returned
`written` slice. Functional: `test/functional/features/root-skill-install.feature`
— one `@critical` scenario (`root-skill-install.feature:10-24`) driving
`niwa init` against the offline `localGitServer` fake and asserting the same
three content markers. Neither test covers overwrite-vs-preserve behaviour.

---

## 2. The dispatch-briefs preservation guarantee

**What is preserved:** the whole directory `<configDir>/dispatch-briefs/`,
where `configDir` is `<workspaceRoot>/.niwa`. The name is a constant at
`internal/workspace/snapshotwriter.go:26`:

```go
const dispatchBriefsDirName = "dispatch-briefs"
```

Its doc comment (`snapshotwriter.go:20-25`) states the design intent verbatim:

> "dispatchBriefsDirName is the directory under the config dir where the
> niwa-owned /dispatch skill writes task briefs
> (<workspaceRoot>/.niwa/dispatch-briefs/<slug>.md) immediately before
> invoking `niwa dispatch`. It is niwa-written local state, not upstream
> source content, so the snapshot swap must carry it across the swap the
> same way it carries instance.json. See preserveDispatchBriefs."

**Across which operation:** the config-snapshot *swap*, not apply generally.
`preserveDispatchBriefs(configDir, staging)` is called from exactly one site,
`materializeAndSwap` at `snapshotwriter.go:454`, immediately after
`preserveInstanceState` (`snapshotwriter.go:443`) and immediately before
`SwapSnapshotAtomic` (`snapshotwriter.go:459`). The call-site comment
(`snapshotwriter.go:448-453`):

> "Preserve dispatch-briefs/ across the swap for the same reason
> instance.json is preserved: it is niwa-written local state living under
> the config dir, not upstream source content, so the whole-directory
> rotation would otherwise destroy it. This is the reported single-repo
> defect: the /dispatch skill writes a brief here, then `niwa dispatch`
> runs this refresh on the same config dir before the worker can read it."

`materializeAndSwap` is reached from `refreshSnapshot` (`snapshotwriter.go:165`,
`snapshotwriter.go:197`), `lazyConvertWorkingTree` (`snapshotwriter.go:292`), and
the public `MaterializeFromSource` (`snapshotwriter.go:322`). Those in turn are
driven by `EnsureConfigSnapshotWithStatus` (`snapshotwriter.go:87-111`), whose
production callers are `internal/workspace/apply.go:390` (Create),
`internal/workspace/apply.go:526` (Apply), `internal/workspace/apply.go:806`
(global overlay), `internal/workspace/overlaysync.go:40`, and
`internal/workspace/configreload.go:66` (the pre-materialize reconcile
`ReconcileAndReloadConfig`, invoked from `internal/cli/apply.go:177`).

**The implementation** is `preserveDispatchBriefs` at
`snapshotwriter.go:504-524`. It stats `<configDir>/dispatch-briefs`, no-ops on
`IsNotExist` (`:508-510`), no-ops when the path is not a directory
(`:513-518` — "A non-directory at this path is not a brief store"), and
otherwise `copySubtree(src, dst)` into staging (`:520`). The doc comment
(`snapshotwriter.go:489-503`) records the intent:

> "It is the directory-tree counterpart to preserveInstanceState:
> dispatch-briefs/ is niwa-written local state under the config dir, not
> upstream source content, so it must ride across the swap that otherwise
> replaces the whole config dir with fetched content. … New niwa-local paths
> extend the carry-over set explicitly (see also preserveInstanceState);
> Issue #74 tracks the longer-term manifest-driven fetch that makes the
> source-vs-local-state distinction structural."

**Load-bearing detail for §4:** `copySubtree`
(`internal/workspace/fallback.go:368-426`) copies local files *over* staging,
and `copyRegularFile` (`fallback.go:432-464`) opens the destination with
`os.O_WRONLY|os.O_CREATE|os.O_TRUNC` (`fallback.go:439`). So if the upstream
config source itself shipped `.niwa/dispatch-briefs/_common.md`, the extracted
upstream copy would land in staging and then be **overwritten by the local
copy**. Local wins; upstream only seeds when the local file is absent.
`copySubtree` also skips non-regular entries (`fallback.go:420-423`) and
validates every path segment (`fallback.go:392-396`).

**Tests.**
- `TestEnsureConfigSnapshot_PreservesDispatchBriefsAcrossRefresh`,
  `internal/workspace/snapshotwriter_test.go:497-547`. Plants a marker, plants
  `dispatch-briefs/investigate-thing.md`, drives a drift refresh against a fake
  tarball that does **not** contain `dispatch-briefs/`, then asserts the upstream
  `workspace.toml` refreshed *and* the brief survived **byte-for-byte**
  (`bytes.Equal`, `:545`). The header comment (`:485-496`) explains the
  config-in-repo single-repo failure mode that motivated it.
- `TestEnsureConfigSnapshot_NoDispatchBriefsToPreserveIsBenign`,
  `snapshotwriter_test.go:553-575`. Asserts the carry-over is a no-op and does
  **not** spuriously create the directory (`:572-574`).
- Functional `@critical`: `test/functional/features/workspace-config-sources.feature:82-99`
  ("niwa apply preserves dispatch briefs across a config-snapshot refresh"),
  with the 12-line rationale comment at `:68-80`. Steps in
  `test/functional/steps_workspace_config_sources_test.go:97-110` (write) and
  `:115-126` (assert survival).

**What the guarantee is NOT.** It protects against the snapshot rotation only.
Nothing stops niwa itself from writing into `.niwa/dispatch-briefs/` on a later
apply — there is no read-only marking, no drift check, no managed-file record.

---

## 3. File distribution tables

Source doc: `docs/guides/file-distribution.md`. Its summary table
(`file-distribution.md:7-11`):

| Table | Lands at | Name |
|-------|----------|------|
| `[files]` | each managed repo | rewritten with a `.local` infix |
| `[instance.files]` | each instance root | verbatim |
| `[root.files]` | the workspace root | verbatim |

Source paths are relative to the config dir (`.niwa/`); destinations are relative
to the target level's root; a trailing `/` on the source means "copy the whole
directory" (`file-distribution.md:13-15`).

### `[files]` — per managed repo

- **Implementation:** `FilesMaterializer.Materialize`,
  `internal/workspace/materialize.go:1535-1578`, reading `ctx.Effective.Files`
  (`:1537`). Per-file work in `materializeFile` (`materialize.go:1613-1648`).
- **Source:** `filepath.Join(ctx.ConfigDir, src)`, containment-checked against
  `ConfigDir` (`materialize.go:1614-1617`).
- **Destination:** inside `ctx.RepoDir` — each cloned repo.
- **Rename:** `.local` infix injected via `injectLocalInfix`
  (`materialize.go:1638`); directory destinations use `localRename`
  (`materialize.go:1633`). A one-line stderr note fires when the rewrite changes
  the author's path (`materialize.go:1518-1531`).
- **Overwrite:** yes, every apply — `writeManagedFile` does an unconditional
  `os.WriteFile` (`materialize.go:1595`).
- **Tracking:** `ctx.recordSources(...)` with a sha256 of the bytes
  (`materialize.go:1599-1604`), so drift detection and `cleanRemovedFiles`
  apply.

### `[instance.files]` — per instance root

- **Implementation:** `materializeVerbatimFiles(mctx, effective.InstanceFiles)`
  at `internal/workspace/workspace_context.go:377`, inside
  `InstallWorkspaceRootSettings` (which, despite the name, targets an *instance*
  root — see `root_materializer.go:117-119`).
- **Destination:** the instance root, verbatim
  (`materializeVerbatimFile`, `materialize.go:1683-1710`; explicit dest used as
  written at `:1704`).
- **Overwrite:** every apply (same `writeManagedFile` core).
- **Tracking/cleanup:** appended to `written`, which joins the instance's
  `ManagedFiles` set — the comment at `workspace_context.go:373-376` says
  "dropping an [instance.files] entry deletes the file on next apply".
  Doc mirror: `file-distribution.md:76-78`.

### `[root.files]` — workspace root

- **Implementation:** `MaterializeWorkspaceRoot`,
  `root_materializer.go:159-173`, calling the same
  `materializeVerbatimFiles` with `RepoDir = workspaceRoot` and
  `ConfigDir = opts.ConfigDir`.
- **Destination:** the workspace root, verbatim.
- **Overwrite:** every apply, and **untracked**. The comment at
  `root_materializer.go:153-158`:

  > "Unlike the instance root, the workspace root has no managed-file state
  > store, so these writes are overwrite-idempotent like the other root-managed
  > files (settings.json, CLAUDE.md, skills): re-written every apply, not
  > removal-cleaned. The returned paths are reported but the callers do not yet
  > track them."

  Doc mirror: `file-distribution.md:79-83` — removing an entry leaves the file
  behind until deleted by hand.
- **Runs only at root scope** (`cli/apply.go:237`) and on qualifying `niwa init`
  modes (`cli/init.go:771`).

### Can any table write into `<workspaceRoot>/.niwa/`?

**Mechanically yes for `[root.files]`; documented as out of bounds; not
enforced in code.**

- The only destination guard is `checkContainment(targetPath, ctx.RepoDir)`
  (`materialize.go:1583-1585`, implementation
  `internal/workspace/content.go:294-322`). For `[root.files]`, `RepoDir` is the
  workspace root, and `.niwa/dispatch-briefs/_common.md` is inside it, so the
  guard passes. Parent dirs are created (`materialize.go:1588-1591`).
- I found **no** validation anywhere rejecting a `.niwa`-prefixed destination.
  `internal/config/validate_vault_refs.go:368-383` iterates `cfg.Files`,
  `ov.Files`, and `cfg.Instance.Files` only to reject `vault://` URIs, not
  paths. `validateGlobalOverridePaths` (`internal/config/config.go:676-692`)
  only rejects `..` and absolute paths, and only for global-overlay `Files`.
- The docs declare it off-limits under "Limitations"
  (`file-distribution.md:92-94`):

  > "**Destinations stay at the project root** of their level. Distributed files
  > are meant for the workspace-root or instance-root project directory, not for
  > niwa-internal directories."

  That is prose, not a check.

Everything else these tables target is `.claude/` and repo/instance/root
directories.

---

## 4. Seeding vs preservation: what already exists

### The tension is smaller than it looks

`preserveDispatchBriefs` guards against the *snapshot swap*, not against niwa's
own writes. `MaterializeWorkspaceRoot` and `materializeAndSwap` are different
code paths with no interaction, and the ordering in `niwa apply` composes
cleanly: `ReconcileAndReloadConfig` (the swap) runs at `cli/apply.go:177`,
*before* `MaterializeWorkspaceRoot` at `cli/apply.go:238`. So a file niwa writes
into `.niwa/dispatch-briefs/` at root-materialize time survives every subsequent
swap for free, via the existing carry-over — including the swap that
`niwa dispatch` triggers through `Create` (`internal/workspace/apply.go:526`,
`:390`), which never re-runs root materialization.

The only real question is **whether niwa clobbers a user's edits to that file**.

### Write-if-absent semantics in the codebase

There is **no** general write-if-absent helper for content files. What exists:

- `EnsureInstanceGitignore`, `internal/workspace/gitignore.go:33-67`. The closest
  precedent for "ensure, don't clobber": creates the file when missing
  (`:41-46`), no-ops when the pattern is already present (`:49-51`), otherwise
  appends preserving existing content (`:53-65`). Its doc comment (`:16-32`)
  spells out the three-state contract.
- `appendToWorkspaceRulesFile`, `internal/workspace/workspace_context.go:135-153`
  — reads the existing file, no-ops if the import line is already present
  (`:141-143`), else appends.
- `os.O_CREATE|os.O_EXCL` exists in three places, none of them content
  distribution: `internal/worktree/atomicid.go:45`,
  `internal/pluginrecord/registry.go:456` and `:518`,
  `internal/cli/dispatch_spill.go:118`.

Everything in the content/materialize layer is unconditional `os.WriteFile`.

### "Seed" / "scaffold" / template concepts

- `Scaffold(dir, name)`, `internal/workspace/scaffold.go:113-137` — writes
  `.niwa/workspace.toml` from `scaffoldTemplate` (`scaffold.go:12-107`) plus an
  empty `.niwa/claude/` dir. Unconditional write (`scaffold.go:127`). Runs on
  bare `niwa init` only.
- `ScaffoldFromSource` + `scaffoldFromSourceTemplate`,
  `scaffold.go:148-170` onward — the `--bootstrap` template, documented in
  `docs/guides/init-bootstrap.md:192-235`. It also writes an empty
  `.niwa/claude/.gitkeep` when `IncludeGitkeep` is set
  (`scaffold.go:186-190` doc, `init-bootstrap.md:233-235`).
- Both are **one-shot init-time** paths writing into `.niwa/`. Neither re-runs on
  apply, and neither has a "don't clobber" guard — they simply never execute
  against an already-configured workspace. `niwa init --bootstrap` explicitly
  does no automatic apply (`init-bootstrap.md:80-81`).

So: `.niwa/` *is* written by niwa at init time, with existing precedent. What is
missing is a per-apply, non-clobbering writer.

### Concrete mechanisms available

| # | Mechanism | Existing code reused | New code |
|---|-----------|----------------------|----------|
| A | `//go:embed` + write-if-absent into `.niwa/dispatch-briefs/_common.md`, called from `MaterializeWorkspaceRoot` | `writeRootSkills` shape (`root_materializer.go:189`), `preserveDispatchBriefs` (no change), `EnsureInstanceGitignore` as the not-clobbering precedent | new embed dir + ~15-line writer + call site |
| B | `//go:embed` + **sentinel-section merge** into `_common.md` | `installWorktreeContextLayer` (`worktree_content.go:696-736`) and `stripWorktreeContextSection` (`:869-875`) — an existing, tested strip-and-reappend pattern | new embed + ~30-line writer modelled line-for-line on the worktree layer |
| C | Two-file layering: niwa overwrites `_common.md` every apply, user writes `_common.local.md`, the reader loads both | `.local.md` convention (`agent.go:77`), overlay append (`content.go:161-179`) | ~12-line writer + a reader-side convention (skill/brief text), no merge logic |
| D | `[root.files]` with `"common.md" = ".niwa/dispatch-briefs/_common.md"` | nothing new in Go | zero Go, but the file is **workspace-authored, not niwa-shipped**, overwrites every apply, and violates the documented limitation at `file-distribution.md:92-94` |

D does not satisfy "ships with niwa" — it requires every workspace's config repo
to carry the file. It is worth naming only as the do-nothing baseline.

A freezes every existing workspace at whatever version of the agreement it first
saw: niwa could never ship an update, in contrast to `SKILL.md`, which refreshes
on every apply.

B gives both properties — niwa's canonical block updates every apply, user prose
outside the sentinel survives — and it is the only option that copies an
already-tested in-repo pattern rather than inventing semantics.

C is the cleanest if "override" must mean *replace* rather than *extend*, since a
sentinel block can only be extended. It costs the least Go code but moves the
composition burden onto whoever reads the file.

### Gaps that apply to all of A/B/C

- `MaterializeWorkspaceRoot` runs only at **root scope**
  (`cli/apply.go:237`) and on `modeNamed`/`modeClone` init
  (`init.go:771`, `init.go:1004`). A workspace created before the change whose
  owner only ever runs `niwa apply <instance>` from inside an instance would
  never be seeded. `niwa create` and `niwa dispatch` do not re-materialize the
  root.
- Workspace-root writes are **untracked** (`root_materializer.go:153-158`), so
  there is no drift warning if the user edits and niwa overwrites — the loss is
  silent.
- `docs/guides/file-distribution.md:92-94` would need an explicit carve-out
  saying niwa itself owns one path under `.niwa/`, distinct from what the file
  tables may target.

---

## 5. Precedent for layered / overrideable content

Yes — several, all in the CLAUDE.md content layer.

1. **Sentinel-delimited section inside a user-owned file.**
   `worktreeContextHeading = "## Worktree Context (niwa worktree)"`,
   `internal/workspace/worktree_content.go:33`, described at `:31-33` as
   "a stable sentinel so the section can be replaced idempotently on re-apply
   rather than duplicated." `installWorktreeContextLayer`
   (`worktree_content.go:696-736`) reads the existing `CLAUDE.local.md`, calls
   `stripWorktreeContextSection` (`:869-875` — truncate at the heading),
   re-appends the freshly rendered section, and writes back. This is the closest
   thing in the repo to "niwa owns a block, the user owns the rest."

2. **Overlay append.** `InstallRepoContentTo`,
   `internal/workspace/content.go:154-195`. When a repo's content entry carries
   an `OverlaySource`, the overlay file's bytes are appended to the
   already-written `CLAUDE.local.md` separated by a blank line
   (`content.go:170-179`); when there is no base source but an overlay is set,
   the overlay becomes the whole file (`content.go:180-195`). Config side:
   `internal/config/overlay.go:69` — "the overlay appends a file to the base
   repo's CLAUDE.local.md."

3. **Import-chain layering with a fixed order.** `internal/workspace/apply.go:1374-1400`
   establishes `@workspace-context.md` → `@CLAUDE.overlay.md` →
   `@CLAUDE.global.md`, with the comment at `:1379-1382` explaining that the
   workspace context must be installed first "so that the three-way ordering …
   is established on first apply." The import file itself,
   `.claude/rules/workspace-imports.md` (`workspace_context.go:124`), is written
   fresh by `writeWorkspaceRulesFile` (`:127-133`) but *extended* by
   `appendToWorkspaceRulesFile` (`:135-153`), which is idempotent on the import
   line. Constants: `overlayClaudeFile`/`overlayClaudeImport`
   (`workspace_context.go:118-119`), `globalClaudeFile`/`globalClaudeImport`
   (`:121-122`).

4. **`.local` naming as the "yours, not the repo's" convention.**
   `agent.RepoContextFileName()` returns `CLAUDE.local.md`
   (`internal/agent/agent.go:77`); `[files]` injects `.local` so output matches
   the `*.local*` gitignore (`materialize.go:1636-1642`, `gitignore.go:16-32`).
   The non-repo levels drop the infix precisely because there is no gitignore to
   satisfy (`file-distribution.md:17-32`).

5. **Overlay repo auto-discovery.** Documented in the workspace's own
   `CLAUDE.overlay.md` (private overlay is discovered by the `-overlay` naming
   convention and its URL cached in `instance.json`). Code lives in
   `internal/workspace/overlaysync.go` — it calls
   `EnsureConfigSnapshotWithStatus` with `config.OverlayMarkerSet()`
   (`overlaysync.go:40`). This is repo-level layering, not file-level, and it is
   soft-fail by design (see the R37 note at `snapshotwriter.go:60-62`).

**No precedent exists for a niwa-shipped file that a local file can *replace*
wholesale.** The patterns are all extend-or-append, or two files read in a fixed
order by the agent harness.

---

## 6. Testing conventions

`CLAUDE.md:21-32` sets the rule:

> "Unit tests live alongside source files (`*_test.go`). Functional (end-to-end)
> tests live in `test/functional/` and run the compiled binary via
> `make test-functional` or `make test-functional-critical`.
>
> When you ship a user-facing CLI command or fix a regression in the
> init → create → apply workflow, add a `@critical` Gherkin scenario in
> `test/functional/features/`."

A shipped `_common.md` lands squarely inside that trigger: it changes what
`niwa init` and `niwa apply` produce. **A `@critical` scenario would be
expected.**

**Unit tests to extend:**
- `internal/workspace/root_materializer_test.go` — model on
  `TestMaterializeWorkspaceRoot_DispatchSkill` (`:193-232`) for
  "the file lands with the right content and is in the `written` slice",
  and `TestMaterializeWorkspaceRoot_RootFilesVerbatim` (`:390-422`) for the
  file-table half. A new "user edit survives re-materialize" test has no
  existing model — it would be the first of its kind at this altitude.
- `internal/workspace/snapshotwriter_test.go` — extend
  `TestEnsureConfigSnapshot_PreservesDispatchBriefsAcrossRefresh` (`:497-547`)
  or add a sibling asserting `_common.md` specifically rides the swap.

**Feature files to model on:**
- `test/functional/features/root-skill-install.feature` — the direct analogue:
  one `@critical` scenario, `niwa init` from a `localGitServer` config repo,
  then file-exists + content-contains assertions
  (`root-skill-install.feature:10-24`).
- `test/functional/features/workspace-config-sources.feature:82-99` — the
  survive-a-refresh half, with reusable steps
  `Given a dispatch brief "<f>" exists in the workspace root` /
  `And the dispatch brief "<f>" still exists in the workspace root`
  (`steps_workspace_config_sources_test.go:97-126`).
- `test/functional/features/mcp-root-instance-distribution.feature:17-43` —
  the full init → create → apply chain, if the scenario needs to prove the file
  survives an apply cycle rather than just init.
- `test/functional/features/apply-root-not-instance.feature` — likely relevant
  to the root-scope-only gap noted in §4, though I did not read it.

Guide: `docs/guides/functional-testing.md` (referenced at `CLAUDE.md:30-32`;
not read for this investigation).

---

## Implications

**A shipped-plus-overrideable `_common.md` is cheap and structurally supported.**
The preservation guarantee and the seeding path do not actually collide:
`preserveDispatchBriefs` defends the snapshot swap, `MaterializeWorkspaceRoot`
writes at a different moment, and `niwa apply` already orders the swap before the
materialize (`cli/apply.go:177` then `:238`). A file niwa writes into
`.niwa/dispatch-briefs/` inherits the existing carry-over for free — including
across the `niwa dispatch` refresh that motivated the guarantee in the first
place.

**Best fit: option B, the sentinel-section merge**, modelled on
`installWorktreeContextLayer` (`worktree_content.go:696-736`) and
`stripWorktreeContextSection` (`:869-875`). It is the only option that keeps
niwa able to *update* the agreement in existing workspaces while letting a user's
own prose survive, and it copies a pattern that is already in the tree and
already tested rather than inventing semantics. Concrete cost: one new embedded
file (`internal/workspace/dispatchbriefs/_common.md` + a `//go:embed` sibling to
`root_materializer.go:25`), one ~30-line `writeDispatchBriefCommon(workspaceRoot)`
in `root_materializer.go` called alongside `writeRootSkills` at `:147`, a
generalization or copy of `stripWorktreeContextSection`, two unit tests, and one
`@critical` scenario. No change to `snapshotwriter.go` at all.

Pick **C** instead if "override" must mean *replace* rather than *extend* — the
two-file `_common.md` + `_common.local.md` split costs less Go (a plain
overwrite writer, ~12 lines) and matches the established `.local` convention,
but it pushes composition onto the reader and needs the `/dispatch` SKILL.md text
to change in lockstep.

**Three things to decide, none of them blocking:**

1. **Coverage gap.** `MaterializeWorkspaceRoot` runs only at root-scope apply
   (`cli/apply.go:237`) and on named/clone init (`init.go:1004`). Workspaces
   whose owners only run instance-scoped applies never get seeded. Either accept
   it, or add a second call site — `niwa create` (`internal/workspace/apply.go`
   `Create`) is the obvious candidate since `niwa dispatch` goes through it.
2. **Silent-clobber risk.** Workspace-root writes are untracked by design
   (`root_materializer.go:153-158`), so if the merge strategy ever loses user
   content there is no drift warning to catch it. That is the strongest argument
   for the sentinel merge over a plain overwrite.
3. **Doc contradiction.** `docs/guides/file-distribution.md:92-94` says
   distributed files are "not for niwa-internal directories." A niwa-owned file
   under `.niwa/` does not violate that rule (it is not a *distributed* file),
   but the guide should say so explicitly, or a reader will conclude the two
   mechanisms are in conflict.

**Surprising:** `copySubtree` + `copyRegularFile`'s `O_TRUNC`
(`fallback.go:439`) mean local content already wins over upstream inside
`dispatch-briefs/`. A workspace whose *config source repo* ships
`.niwa/dispatch-briefs/_common.md` today already gets seed-once-then-local-wins
semantics with zero code changes — the extraction puts upstream's copy in
staging and `preserveDispatchBriefs` overwrites it with the local one when the
local one exists. That is exactly the semantics option A would hand-code, and it
is already working, just not for a file that ships with the *binary*.
