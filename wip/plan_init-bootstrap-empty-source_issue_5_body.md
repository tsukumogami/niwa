---
complexity: testable
complexity_rationale: |
  Pure test and documentation work. No production code lands here -- every
  previous issue (<<ISSUE:1>>..<<ISSUE:4>>) wired structural enforcement
  points; this issue exercises every PRD AC against the assembled feature
  via `@critical` Gherkin scenarios under `test/functional/features/`
  (using the existing `localGitServer` and `tarballFakeServer` fixtures
  per the PRD's Test-fixture conventions) and a focused set of unit tests
  for test-seam invariants the Gherkin layer cannot observe (argv guard,
  exec-layer host-check ordering, classifier ordering, no-author/no-GIT_*,
  cleanup-defer at create-fail and init-fail). Adds the user-facing guide
  for `--bootstrap` under `docs/guides/` covering the end-to-end flow,
  the visibility-lookup fallback, the branch-name format, and the R19
  success block, and verifies that the scaffold's footer link to
  `docs/guides/workspace-config-sources.md` is current. Leaf issue with
  no downstream dependents -- closes the feature.
---

## Goal

Land the full PRD Acceptance Criteria matrix as `@critical` Gherkin scenarios plus the test-seam unit tests the Gherkin layer cannot observe, and ship the user-facing `--bootstrap` documentation under `docs/guides/`.

## Context

Issues <<ISSUE:1>>..<<ISSUE:4>> wire every structural enforcement point the PRD requires; Phase 5 exercises each of them end-to-end against the assembled feature. Gherkin coverage uses the `localGitServer` and `tarballFakeServer` fixtures per the PRD's Test-fixture conventions; test-seam ACs (argv guard, exec-layer host-check ordering, classifier ordering table, no-author/no-GIT_*, cleanup-defer at create-fail and init-fail) land as focused Go unit tests because the Gherkin layer cannot observe `*exec.Cmd` argv or env. The documentation deliverable closes the feature: a new page (or extension of an existing one) under `docs/guides/` describes the end-to-end flow, the visibility-lookup fallback, the `niwa-bootstrap/<sid>` branch-name format, and the R19 success block, and verifies that the scaffold's footer link to `docs/guides/workspace-config-sources.md` resolves.

Design: `docs/designs/DESIGN-init-bootstrap-empty-source.md` (Phase 5, around line 1173).

## Acceptance Criteria

### `@critical` Gherkin scenarios (under `test/functional/features/`)

- [ ] **Happy path with positional name** (fixtures: `tarballFakeServer` returning 200 + empty-but-README tree, plus `/repos/owner/my-project` returning `{"private": false}`; `localGitServer` for clone). `niwa init my-project --from owner/my-project --bootstrap` asserts on disk:
  - `<cwd>/my-project/.niwa/workspace.toml` matches Appendix A byte-for-byte after `<placeholder>` substitution
  - `<cwd>/my-project/.niwa/claude/.gitkeep` is zero bytes
  - `<cwd>/my-project/<instanceName>/.niwa/instance.json` parses as instance-state schema v4
  - `<cwd>/my-project/<instanceName>/.niwa/roles/my-project/` exists
  - `<cwd>/my-project/<instanceName>/<group>/my-project/.git` exists (cloned source)
  - `<cwd>/my-project/<instanceName>/.niwa/worktrees/my-project-<sid>/` exists with `<sid>` matching `[0-9a-f]{8}`
  - branch `niwa-bootstrap/<sid>` exists with exactly one commit, subject `Initial niwa workspace config`, author/committer match the user's `git config user.name`/`user.email` (not the literal string `niwa`)
  - registry entry name `my-project` resolves to `<cwd>/my-project/` (absolute path equality)
  - landing-path file contents equal the worktree absolute path
- [ ] **Happy path no positional name** (same fixtures): `niwa init --from owner/foo --bootstrap` produces all the above artifacts at `<cwd>/foo/` (R2 name derivation).
- [ ] **401 auth error** (`tarballFakeServer` returns 401 for the tarball): exit code 1; stderr contains the exact R10 substring `verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope`; no on-disk state remains.
- [ ] **403 auth error** (`tarballFakeServer` returns 403): exit code 1; stderr contains the exact R10 substring; no on-disk state remains.
- [ ] **404 typo case** (`tarballFakeServer` returns 404; `GH_TOKEN` set): exit code 1; stderr contains all three R11 substrings (`verify the slug is correct (org/repo) and the repo exists`; `if the repo is private, set GH_TOKEN with read access`; `if the repo is brand new and has no commits yet, push at least one commit (an empty README is enough) and retry with --bootstrap`).
- [ ] **404 zero-commit case** (`tarballFakeServer` 404 simulating GitHub's response for a no-HEAD repo): exit code 1; stderr contains all three R11 substrings, including the explicit zero-commit substring `if the repo is brand new and has no commits yet, push at least one commit (an empty README is enough) and retry with --bootstrap`.
- [ ] **404 private-no-token case** (`tarballFakeServer` 404; `GH_TOKEN` unset): exit code 1; stderr contains all three R11 substrings.
- [ ] **Ambiguous markers** (`tarballFakeServer` returns 200 with both `.niwa/workspace.toml` and root `workspace.toml`): exit code 1; stderr contains the literal string returned by `(*config.AmbiguousMarkersError).Error()` verbatim (assertion compares against the same call performed in-test, so the message stays in lockstep with `internal/config`).
- [ ] **Non-GitHub source** (no fixture; flag-parse stage): `niwa init bar --from gitlab.com/owner/repo --bootstrap` exits with code 3; stderr contains the exact R9 string `bootstrap supports only GitHub sources in v1; got host=gitlab.com`; the injectable exec invoker (R22) records zero git invocations.
- [ ] **TTY prompt Yes** (functional test with pty helper; `tarballFakeServer` 200 empty-tree): user types `y\n` -> exit 0 plus happy-path artifacts.
- [ ] **TTY prompt No** (same fixtures): user types `n\n` -> exit 0; no scaffolding; `<cwd>/bar/` does not exist on disk.
- [ ] **Non-TTY refusal** (`/dev/null` piped to stdin, NoMarker fixture): exit code 4; stderr contains the exact R13 non-TTY-no-flag fail-fast string `remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold`.
- [ ] **`--no-bootstrap` suppression** (TTY; NoMarker fixture): `niwa init bar --from owner/foo --no-bootstrap` -> exit 4; stderr contains the NoMarker text plus the explicit-decline reason.
- [ ] **Mutual exclusion**: `niwa init bar --from owner/foo --bootstrap --no-bootstrap` -> exit 2; stderr contains the exact R25 string `--bootstrap and --no-bootstrap are mutually exclusive`.
- [ ] **R8 sub-case 1 (workspace exists)**: run bootstrap to success, then re-run with the same `<name>` -> exit 1; stderr Detail contains the substring `workspace `<name>` already exists at`; Suggestion contains both `niwa destroy <name>` and `niwa session create <repo> bootstrap`.
- [ ] **R8 sub-case 2 (registry name in use)**: register `<name>` to a different root manually, then run bootstrap -> exit 1; stderr Detail contains `workspace name `<name>` is already registered`; Suggestion contains `niwa destroy <name>`.
- [ ] **R8 sub-case 3a (non-niwa file at target)**: `touch <cwd>/bar`, then run `niwa init bar --from owner/foo --bootstrap` -> exit 1; stderr Detail contains `<absPath> already exists (file)`; Suggestion contains `Pick a different`.
- [ ] **R8 sub-case 3b (non-niwa directory at target)**: `mkdir <cwd>/bar` (no `.niwa/` inside), then run bootstrap -> exit 1; stderr Detail contains `<absPath> already exists (directory)`; Suggestion contains `Pick a different`.
- [ ] **R8 sub-case 3c (symlink at target)**: `ln -s /tmp/somewhere <cwd>/bar`, then run bootstrap -> exit 1; stderr Detail contains `<absPath> already exists (symlink)`; Suggestion contains `Pick a different`.
- [ ] **Rollback at init step**: forced failure during init (e.g. pre-existing target dir) -> exit 1; stderr error line begins with the literal prefix `bootstrap step=init:`; `<cwd>/<name>/` does not exist afterward; no registry entry; no instance.
- [ ] **Rollback at create step**: `tarballFakeServer` 200 for the config fetch but `localGitServer` returns clone failure -> exit 1; stderr prefix `bootstrap step=create:`; stderr contains `bootstrap: create step failed; instance directory removed. Workspace at <path> preserved; run niwa create to retry.`; `<cwd>/<name>/.niwa/workspace.toml` exists; no `<instanceName>/` directory; registry entry exists.
- [ ] **Rollback at session-create step**: create succeeds; session-create fails (e.g. daemon-spawn timeout via fault injection) -> exit 1; stderr prefix `bootstrap step=session-create:`; stderr contains `bootstrap: session-create step failed; instance preserved at <path>. Run niwa session create <repo> bootstrap to retry.`; instance and workspace remain intact; no worktree exists.
- [ ] **Rollback at commit step**: forced failure inside the worktree's `git add`/`git commit` step (via the injectable exec invoker returning a non-zero status) -> exit 1; stderr prefix `bootstrap step=session-create:`; the bootstrap branch is removed and the session worktree is cleaned per session-create's existing rollback contract.
- [ ] **Scaffold byte-equality**: parse `<cwd>/foo/.niwa/workspace.toml` and assert it matches Appendix A's golden body literally after `<placeholder>` substitution (workspace name, source org, bootstrap repo, vis-key, vis-value).
- [ ] **Two-phase scaffold byte-equality at the committed-tree level**: after a happy-path bootstrap, the tree at `niwa-bootstrap/<sid>` HEAD contains a `.niwa/workspace.toml` file whose contents match `<cwd>/<name>/.niwa/workspace.toml` byte-for-byte. The test uses `git -C <worktreePath> show niwa-bootstrap/<sid>:.niwa/workspace.toml` (or equivalent porcelain reading via the localGitServer's bare-repo backend) and compares against `os.ReadFile(<workspaceRoot>/.niwa/workspace.toml)`. Same byte-equality check for `.niwa/claude/.gitkeep`. This proves the two-phase scaffold writes (one in `runInit` at workspace root, one inside `RunBootstrap` at worktree path) used identical `ScaffoldOptions` and produced identical content end-to-end.
- [ ] **`.gitkeep` present**: `<cwd>/foo/.niwa/claude/.gitkeep` exists and is zero bytes (R15).
- [ ] **`[channels.mesh]` block active**: parsing the scaffold yields `Channels.Mesh != nil` and `Channels.IsEnabled() == true`.
- [ ] **Inline comment on `[channels.mesh]` exact**: the line preceding the `[channels.mesh]` block matches exactly `# Bootstrap enabled mesh channels. Remove this block (and the [channels.mesh] line below) to disable.`
- [ ] **Visibility-from-bool adversarial fixture** (`tarballFakeServer` `/repos/owner/foo` returns `{"private": true, "visibility": "public"}`): scaffold contains `[groups.private]` with `visibility = "private"` and no `[groups.public]` block (R16 -- bool wins over mismatched `Visibility` string).
- [ ] **Visibility-from-bool TOML-injection-shaped string** (`tarballFakeServer` `/repos/owner/foo` returns `{"private": false, "visibility": "\"\n[evil]\nkey = \"x"}`): scaffold contains `[groups.public]` and no `[evil]` block; the injection-shaped string is never read by the scaffold-writer code path (R16).
- [ ] **Visibility-lookup soft-fail (server error)** (`tarballFakeServer` returns 500 on `/repos/`): scaffold contains `[groups.public]`; stderr contains the exact R17 note `note: could not determine remote visibility (server error); defaulting to [groups.public]. Edit .niwa/workspace.toml to change.`; bootstrap exits 0.
- [ ] **Visibility-lookup soft-fail (network error)** (close the fake server before bootstrap reaches the metadata endpoint): scaffold contains `[groups.public]`; stderr contains the exact R17 note with `<cause>` = `network error`; bootstrap exits 0.
- [ ] **Visibility-lookup soft-fail (auth)** (`tarballFakeServer` returns 401 on the `/repos/owner/foo` endpoint only; tarball fetch returns 200): scaffold contains `[groups.public]`; stderr contains the exact R17 note with `<cause>` = `authentication`; bootstrap exits 0.
- [ ] **Visibility-lookup soft-fail (not found)** (`tarballFakeServer` returns 404 on the `/repos/owner/foo` endpoint only): scaffold contains `[groups.public]`; stderr contains the exact R17 note with `<cause>` = `not found`; bootstrap exits 0.
- [ ] **Worktree label in success block**: stderr contains the literal line `Worktree:                     <absolute-path>` where `<absolute-path>` matches the worktree returned by `git worktree list --porcelain`.
- [ ] **Success block format**: the stderr success block matches Appendix B byte-for-byte after `<placeholder>` substitution -- line ordering, label-to-value spacing, indentation under `Next steps:`, and colon-then-space pattern all identical -- preceded by one blank stderr line and followed by one blank stderr line.
- [ ] **Allow-list scoping** (`tarballFakeServer` configured with three repos in the source org `foo`, `bar`, `baz`): after `niwa init foo --from owner/foo --bootstrap`, `<instanceRoot>/<group>/` contains only `foo/`; `bar/` and `baz/` are absent on disk (R4).
- [ ] **Branch-name stored in session state**: after a successful bootstrap, `<instanceRoot>/.niwa/sessions/<sid>.json` contains `"branch_name": "niwa-bootstrap/<sid>"` (R5).
- [ ] **Branch-name back-compat fallback**: a synthesized session-state file pre-dating the `branch_name` schema (field absent) loads cleanly; callers that need the branch fall back to `session/<sid>`; the test asserts no panic and no error from any reader (R5).
- [ ] **R6 parity (`niwa session create` against bootstrapped workspace)**: against a workspace produced by bootstrap, `niwa session create my-project another-purpose` succeeds standalone with no re-initialization of state -- demonstrates that no new preconditions specific to bootstrap leaked into the workspace.
- [ ] **R2 regression (no-flag baseline)**: `niwa init --from owner/foo` (no `--bootstrap`) against an *existing-config* repo materializes in cwd and uses the cloned config's `[workspace] name` -- no regression introduced by R2's `--bootstrap`-only derivation rule.
- [ ] **No-secret-on-disk** (N5): after a happy-path bootstrap with `GH_TOKEN=test-fixture-token-DEADBEEF`, recursively grep `<cwd>/<name>/` for the literal token value; assert zero matches (the token must not appear in the scaffolded workspace.toml, instance state, registry entry, session state, or any other written artifact).

### Unit tests for test-seam invariants

- [ ] **Argv-injection guard (R22)**: passing the slug literal `owner/foo;rm -rf /tmp/x` to bootstrap's slug-parse path either (a) fails `source.Parse`'s grammar check (rejected as malformed) or (b) if it parses, reaches `exec.CommandContext` as a single argv element (asserted via the injectable invoker recording `cmd.Args`) with no shell metacharacter expansion.
- [ ] **Host-check ordering at exec layer**: call `RunBootstrap` directly with non-GitHub `src.Host` and the injectable exec invoker; assert (a) the returned error matches R9's exact string, (b) the recorder contains zero git invocations (defense-in-depth check for the R21 invariant -- catches future callers that bypass `runInit`).
- [ ] **Classifier ordering table-driven test**: exercise error chains satisfying multiple arms simultaneously (e.g. `*config.AmbiguousMarkersError` wrapping a `*github.StatusError{StatusCode: 404}`, or a `*config.NoMarkerError` wrapping a 401); for each row assert the classifier picks the arm dictated by N2's precedence list (`AmbiguousMarkersError` -> `NoMarkerError` -> 401/403 -> 404 -> generic). A wrong implementation that reorders the `errors.As` switch must fail this test.
- [ ] **No-author / no-GIT_AUTHOR_*** at argv layer: happy-path flow with the injectable exec invoker reaches the commit step; the captured `*exec.Cmd` for `git commit` asserts (a) `cmd.Args` contains no element equal to `--author` or starting with `--author=`, (b) `cmd.Env` contains no entry whose key matches `^GIT_(AUTHOR|COMMITTER)_(NAME|EMAIL|DATE)$`.
- [ ] **Cleanup-defer at create-fail**: with the injectable exec invoker, force `git fetch` to fail during create's pipeline; after `RunBootstrap` returns, `<cwd>/<name>/.niwa/workspace.toml` exists and no `<instanceName>/` directory exists (the workspace-dir defer flipped to off-after-init-success per R7).
- [ ] **Cleanup-defer at init-fail (preservation case)**: force the init step to fail (e.g. target dir already exists); after return, `<cwd>/<name>/` does not exist; no instance; no registry write (the workspace-dir defer fires per R7 init-step rollback).

### Documentation

- [ ] New page (or extension of an existing guide) under `docs/guides/` describing `--bootstrap`. The guide covers:
  - the end-to-end flow (`niwa init <name> --from <slug> --bootstrap` -> init -> create -> session-create -> committed scaffold inside a worktree the shell wrapper drops the user into)
  - the visibility-lookup fallback (R17): default to `[groups.public]` on network / auth / not-found / server-error, exact `note:` stderr line per cause, the user edits `.niwa/workspace.toml` afterward to change
  - the branch-name format `niwa-bootstrap/<sid>` (R5) including the back-compat note that pre-schema state files still load and fall back to `session/<sid>`
  - the R19 success block (Appendix B): what each label means, where the user pushes from, why niwa does not auto-push (R24)
- [ ] The scaffold's footer link `https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md` resolves at the file path `docs/guides/workspace-config-sources.md` -- the page exists, is current, and matches what the scaffolded comment cites. (Verifies the link the scaffold places in every bootstrap commit.)
- [ ] Optional README mention if the feature warrants top-level visibility (skip if the existing README structure does not surface init flags individually).

### Reference hygiene

- [ ] Zero `wip/...` path references appear in any new or edited documentation file. (The wip-hygiene rule's own prose under `CLAUDE.md` is the only acceptable mention of `wip/` paths -- new docs must not cite them.)

## Dependencies

Blocked by <<ISSUE:4>>: the orchestrator (`workspace.RunBootstrap`), the factored `mcp.CreateSession`, the `BranchName` field on `SessionLifecycleState`, the R19 success-block emission, and the R20 landing-path call must all be in place before end-to-end Gherkin can exercise the assembled feature.

## Downstream Dependencies

None. Leaf issue -- this closes the feature.
