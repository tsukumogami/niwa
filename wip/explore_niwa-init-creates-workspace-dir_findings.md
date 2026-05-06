# Exploration Findings: niwa-init-creates-workspace-dir

## Core Question

When `niwa init <name>` is invoked with an explicit positional `<name>`, niwa
should create `<cwd>/<name>/` and initialize the workspace inside it (instead
of initializing in `cwd`). The explicit name should also become the workspace
name, overriding whatever the `--from` config declares.

## Round 1

### Key Insights

1. **The name-override gap is real and pre-existing** [name-override-strategy].
   Today, `niwa init my-name --from upstream` only persists `my-name` to the
   global registry. `niwa status`, `niwa apply`, and other readers consult
   `.niwa/workspace.toml`'s `[workspace] name`, which still says `upstream`.
   `niwa go my-name` works but everything else shows the upstream name. The
   directory-creation feature is a chance to fix both UX issues in one stroke.

2. **`InstanceState` is the right home for the name override**
   [name-override-strategy]. Existing fields (`SkipGlobal`, `NoOverlay`,
   `OverlayURL`, `OverlayCommit`) follow exactly the pattern needed: init-time
   decisions persisted to `.niwa/state.json` that downstream code consults in
   preference to / alongside the toml. Adding `InstanceNameOverride` (or a
   similar field) fits this convention. Rewriting the cloned toml in-place
   would dirty the workspace against its source-repo HEAD — rejected.

3. **Pre-flight check splits cleanly into two layers**
   [preflight-conflict-semantics]. Caller computes `targetDir = filepath.Join(cwd, name)`,
   checks `os.Stat(targetDir)` for any pre-existing path (file/dir/symlink) → reject
   with new sentinel `ErrTargetDirExists`. Then `CheckInitConflicts(targetDir)` runs
   the existing niwa-state validations (workspace.toml, orphan .niwa/, nested
   instance) on the to-be-created target. Function signature unchanged. The
   `DiscoverInstance` walk-up already handles the non-existent target correctly
   because it walks from the parent.

4. **No ripple effects in other commands** [ripple-effects]. All workspace-location
   resolution is either dynamic (cwd-walk via `DiscoverInstance` — used by status,
   destroy, reset, apply, session, task) or registry-driven (read by `niwa go`,
   `niwa apply <ws>`, `niwa create <ws>`). `niwa go` validates `Root` exists at
   call time. No code anywhere assumes "workspace root == cwd at init time."

5. **`niwa create` shares the same papercut** [ripple-effects]. It also requires
   the workspace root to pre-exist; users must mkdir+cd before running it. Whether
   to extend the same "create-the-folder" convention to `niwa create` is the only
   real "in-scope or not" question remaining for this exploration.

### Tensions

1. **Pre-flight check location** [preflight-conflict-semantics]. The recommended
   approach puts the "target path exists?" check in the caller (`init.go`)
   because no-name modes (where the target is `cwd`) shouldn't trigger it. This
   splits conflict detection across two files. Defensible (separation of
   concerns: `CheckInitConflicts` is niwa-state-aware; the caller handles
   filesystem pre-gates), but the alternative — `CheckInitConflicts(parent, name string)`
   with a conditional — is also viable. Low-stakes design call for the
   implementation phase.

2. **Discounted: "tests already expect this" claim** [test-and-doc-fallout].
   The lead-4 agent reported 20+ functional scenarios that already verify
   instance paths matching the new design. Verified false: those scenarios
   refer to niwa **instances** (subdirs created by `niwa apply` from
   `[workspace]` config), not the workspace directory itself. Disregarded.

### Gaps

1. **Override field name and downstream-reader audit.** "Add `InstanceNameOverride`
   to `InstanceState`" is the recommended shape, but the exact field name and the
   complete list of readers that need to consult it are design-level details, not
   exploration gaps.
2. **Whether `niwa create` should be in scope.** Open question for the user
   (see below). If yes, scope expands modestly; if no, defer to a separate issue.
3. **Doc-fallout grep for `docs/guides/`** wasn't completed by the test/doc agent.
   Mechanical follow-up at implementation time.

### User Focus

User answered Q1 (name-given dictates both folder + workspace name) and Q2
(error if exists) directly, then pushed back on basic questioning and asked
the explorer to apply the decision framework rather than ask the user about
remaining items. Remaining decisions (override implementation, preflight
shape, ripple to `niwa create`) were settled by research, not user input.

## Accumulated Understanding

This is a small, well-bounded UX change with a clean implementation path:

**Behavior change:**
- `niwa init <name>` (with or without `--from`) creates `<cwd>/<name>/` and
  inits inside it. Errors if `<cwd>/<name>` already exists (any path type).
- `niwa init` and `niwa init --from <src>` (no positional name) keep their
  current behavior: init in cwd, name from defaults / from config.
- Positional `<name>`, when given, also becomes the effective workspace name
  for all readers (status, apply, etc.), overriding the cloned toml's
  `[workspace] name`.

**Implementation shape (no surprises):**
- New sentinel `workspace.ErrTargetDirExists` and a caller-side existence
  check before calling `CheckInitConflicts(targetDir)`.
- New `InstanceState` field for the name override; downstream readers
  (`Apply.Create`, status formatting) consult it.
- Registry entry's `Root` is the new dir; `niwa go <name>` works as-is.
- No changes to other commands. Optional extension to `niwa create` for
  parallel UX.

**Test/doc work:** modest. `internal/cli/init_test.go` named-mode assertions
shift from cwd to `cwd/name`. Add a `@critical` Gherkin scenario per project
convention. Update `init.go` help text and `README.md` examples that show
the old `mkdir + cd + init` pattern.

**Unknowns that need design-level (not exploration-level) work:** exact field
name on `InstanceState`, error message text, whether to extend the convention
to `niwa create`. None of these need more research.

## Decisions This Round

See `wip/explore_niwa-init-creates-workspace-dir_decisions.md`.

## Decision: Crystallize
