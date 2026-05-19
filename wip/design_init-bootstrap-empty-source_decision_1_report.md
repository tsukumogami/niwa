<!-- decision:start id="bootstrap-end-to-end-ux" status="assumed" -->
### Decision: Bootstrap end-to-end UX model

**Context**

When `niwa init <name> --from <empty-remote> --bootstrap` triggers
the scaffold path, three sub-choices must hang together as one
coherent end-to-end story: where the bootstrap branch's worktree
lives on disk, when the global-config registry entry is written, and
whether niwa pre-commits the scaffold or leaves it
staged/unstaged for the user to author the first commit.

niwa's existing init flow creates `<cwd>/<name>/` via
`os.Mkdir` at `internal/cli/init.go:217`, runs a deferred
`os.RemoveAll(workspaceRoot)` on every error path, and on success
writes both `.niwa/workspace.toml` and `.niwa/instance.json` to
`<workspaceRoot>/.niwa/`, plus a registry entry pointing at the
absolute workspace root. Downstream commands (`niwa apply`,
`niwa list`, `niwa go`) discover the workspace via `config.Discover`
walking up looking for `<dir>/.niwa/workspace.toml`. The existing
worktree-session machinery (`internal/mcp/handlers_session.go`) is
not reusable here because it gates on apply-produced
`<instance>/.niwa/instance.json` and `<instance>/.niwa/roles/<repo>/`.

**Assumptions**

- A bootstrap path will perform `git clone --depth 1 <cloneURL>
  <workspaceRoot>` directly into the workspace root rather than reuse
  the success path's tarball-of-subpath fetch. The bootstrap needs a
  working tree to commit into; the success path doesn't. If
  wrong: bootstrap would need a separate "re-clone for working tree"
  step which is structurally equivalent.
- `niwa apply` from `<workspaceRoot>` should work as soon as
  bootstrap finishes, allowing the user to iterate locally before
  pushing. If wrong: the model adds an apply-refusal gate that
  pessimizes the iteration loop.
- The user's typical first action after bootstrap is to inspect and
  push (or push and merge), not to substantially rewrite the
  scaffold. If wrong: pre-committing wastes a commit the user will
  amend; the user pays one `git commit --amend` keystroke.
- The user wants `niwa list` and `niwa go <name>` to work
  immediately after init, not after a deferred publish step. If
  wrong: the registry entry could be deferred without breaking the
  primary flow.

**Chosen: In-Place / Immediate / Pre-Commit (W1 + R1 + C1)**

The bootstrap path commits to the following end-to-end shape:

1. **Worktree placement — in-place (W1).** The cloned tree IS the
   workspace root. After `git clone --depth 1` into
   `<workspaceRoot>/`, niwa runs
   `git -C <workspaceRoot> checkout -b niwa-bootstrap` to create and
   switch to the bootstrap branch in the main checkout. No separate
   `git worktree add` is invoked. The on-disk layout is identical to
   today's success path: `<workspaceRoot>/.niwa/workspace.toml` at
   the canonical path, with the workspace root being a normal git
   working tree.

2. **Registry timing — immediate (R1).** After the scaffold is
   committed, the existing
   `globalCfg.SetRegistryEntry(name, RegistryEntry{Root, Source,
   SourceURL})` at `init.go:328` fires exactly as it does for the
   clone path. `SourceURL` is set to the `--from` slug or URL.
   `niwa list`, `niwa go commuter`, and registry-aware re-invocations
   of `niwa init commuter` (no `--from`) all work from this point
   forward.

3. **Commit state — pre-commit (C1).** niwa runs:
   ```
   git -C <workspaceRoot> add .niwa/workspace.toml
   git -C <workspaceRoot> commit -m "Initial niwa workspace config"
   ```
   The bootstrap branch has one commit; the working tree is clean.
   The user can inspect via `git show HEAD`, amend via
   `git commit --amend` if they want a different message, and
   `git push -u origin niwa-bootstrap` when ready.

**End-to-end flow**

```
$ niwa init commuter --from dangazineu/commuter --bootstrap
Initializing from: https://github.com/dangazineu/commuter.git
Remote has no .niwa/workspace.toml — scaffolding (--bootstrap).
Scaffolded .niwa/workspace.toml.
Branch:    niwa-bootstrap
Workspace: /home/user/workspaces/commuter

Next steps:
  1. Review the scaffolded config:
       git show HEAD
  2. Push the bootstrap branch:
       git push -u origin niwa-bootstrap
  3. Merge to the default branch, then run `niwa apply`.
```

Exit 0. Shell wrapper drops the user in `/home/user/workspaces/commuter/`
on branch `niwa-bootstrap` with a clean working tree.

The user can run `niwa apply` from the workspace root before
pushing — apply reads `<workspaceRoot>/.niwa/workspace.toml` on disk
without caring about git branch state. This lets the user verify the
config works locally before publishing.

**Rationale**

1. **No machinery rework.** Every existing code path downstream of
   `init.go:264` works as-is: post-flight `config.Load`, registry
   write, `SaveState` writing `<workspaceRoot>/.niwa/instance.json`,
   `writeLandingPath` writing the workspace root. The bootstrap path
   is structurally a sibling of the clone path — the only difference
   is what produced `.niwa/workspace.toml` (scaffold vs. fetched
   tarball).

2. **`niwa apply` works at the workspace root immediately.** No
   "must push first" gate to maintain or document. The user's
   iteration loop (edit → apply → fix → apply → push) matches
   today's clone-path UX. Cross-examination D2 settled on apply-
   availability as table-stakes for a usable workspace.

3. **Registry behaves identically to the clone path.** `RegistryEntry`
   shape unchanged; no new fields, no new gates, no migration. A
   later `niwa init commuter` (no flag) from the same machine replays
   SourceURL as a clone (which works after the user pushes), matching
   the convention at `init.go:124-128`.

4. **The bootstrap branch is purely local until the user pushes.**
   No remote-state mutation by niwa. The user controls when (and
   whether) the bootstrap becomes visible. Matches the user-stated
   constraint: "no automatic push from niwa."

5. **Convention with niwa's own audit-trail style.** Pre-committing
   with a fixed message parallels niwa's "completed-action-with-audit-trail"
   pattern (`--rebind` WARNING, name-override `note:`, vault bootstrap
   `note:`). The git history records exactly what niwa did.

6. **Recovery is git-native.** If the user abandons the workspace,
   `rm -rf <workspaceRoot>/` plus a registry prune
   (manual or future `niwa registry prune`) reverts the local state.
   No orphan worktrees in `.niwa/worktrees/` to track and clean.

**Alternatives Considered**

- **Sub-Worktree / Marked-Pending / Pre-Commit (W2 + R3 + C1):**
  Symmetric with `niwa session create`'s worktree placement at
  `<instance>/.niwa/worktrees/<repo>-<id>/`. Main checkout stays on
  the remote's default branch; bootstrap activity is in a sibling
  worktree; `InstanceState.BootstrapPending` gates apply until the
  bootstrap is pushed. **Rejected because:**
  (a) Post-flight verification at `init.go:288` and apply discovery
  via `config.Discover` both expect `<workspaceRoot>/.niwa/workspace.toml`
  to exist on disk in the main checkout; with W2 it does not until
  merge+pull, forcing reworks in both code paths.
  (b) Introduces a new `InstanceState` field and a new apply-gate
  with auto-clear logic — three new code paths and a schema migration
  for a feature W1 handles for free.
  (c) Two locations on disk (`<workspaceRoot>/` and
  `<workspaceRoot>/.niwa/worktrees/bootstrap-<id>/`) for "the
  workspace" create a "where do I run apply from?" footgun. The
  exploration's worktree-integration lead explicitly cautioned that
  "the session abstraction's value is in mesh delivery; init has no
  mesh."

- **In-Place / Immediate / Stage-Only (W1 + R1 + C2):** Same as the
  chosen alternative but niwa runs `git add` and leaves the commit
  to the user. **Rejected (narrowly) because:**
  (a) `git status` showing a staged file immediately after
  `niwa init ... --bootstrap` is unusual; users may interpret it as
  "init didn't finish" before recognizing the pattern.
  (b) The user pays one extra `git commit -m "..."` step before
  push; pre-commit (C1) makes `git push` work directly after init.
  (c) C1's explicit commit message ("Initial niwa workspace config")
  signals niwa authorship clearly enough that a user who wants to
  author the message themselves can `git commit --amend` —
  symmetric cost to C2's "user authors the commit."
  This was the closest runner-up; the difference is a UX-tone call,
  not a structural one.

- **In-Place / Immediate / No Stage (W1 + R1 + C3):** Same as the
  chosen alternative but niwa neither stages nor commits.
  **Rejected because:**
  (a) Untracked file in a fresh init contradicts the
  scaffold-tool convention (`cargo init`, `npm init`, `helm create`)
  of leaving at least a staged/committed state.
  (b) `git clean -fd` would silently wipe the scaffold, creating a
  recovery cliff unique to this option.
  (c) Provides no benefit over C2 (stage-only) — both leave the
  user authoring the commit message, but C3 also forfeits the
  protection of staging.

- **W1 + R2 (deferred registry):** Considered and excluded during
  alternatives clustering. R2 creates a discoverability black hole
  between init and push — `niwa list`, `niwa go` don't see the
  workspace; re-running `niwa init commuter --from ...` treats it as
  fresh. Adds no offsetting benefit.

- **W1 + R3 (marked-pending in-place):** Excluded during clustering.
  R3's apply-refusal gate contradicts the rationale point that
  "apply must work at workspace root immediately." R3 only makes
  sense with a worktree split (W2), and W2 itself is rejected.

- **W3 (`~/.cache/niwa/bootstrap/<sid>/`):** Excluded during
  clustering. Lands the user in a cache directory on success;
  caches are semantically ephemeral; the workspace they just
  created should not feel ephemeral.

- **W4 (sibling directory `<cwd>/<name>-bootstrap/`):** Excluded
  during clustering. Two top-level directories pollute the user's
  cwd ancestry for what is logically one workspace, plus inherits
  W2's post-flight and apply-discovery problems.

**Consequences**

What changes:
- The bootstrap path performs a full `git clone --depth 1` into
  `<workspaceRoot>/` rather than the success path's tarball-of-subpath
  fetch. This is one extra network round trip relative to the
  tarball, but the source is empty by definition so the clone is
  fast.
- A new helper is needed at `internal/workspace/scaffold_bootstrap.go`
  (proposed name: `workspace.ScaffoldOnBranch` or
  `workspace.BootstrapScaffold`, ~30-50 LOC). The exploration's
  `StageInWorktree` name was based on assuming W2-style placement
  and should be replaced with a name that reflects "branch + write
  + stage + commit."
- The `--bootstrap` flag in `internal/cli/init.go` flips control
  at `init.go:265` to the bootstrap helper when
  `errors.As(err, &config.NoMarkerError{})` matches AND
  `--bootstrap` was passed (or TTY prompt accepted).

What becomes easier:
- Single-flow mental model: bootstrap is "scaffold + commit in
  place"; no new on-disk locations to learn.
- Local iteration: the user can run `niwa apply` to test the config
  before pushing, edit, apply again, push when satisfied.
- Registry-driven workflows (`niwa list`, `niwa go`,
  cross-machine re-init) all work from the moment init returns.
- No new `InstanceState` field, no schema migration, no
  apply-side gate logic.

What becomes harder:
- The user's main checkout sits on `niwa-bootstrap` rather than the
  remote's default branch until they push and merge. The success
  message must clearly call out the branch name and the push
  command so this is not surprising. Mitigated by following niwa's
  existing audible audit-trail style (similar to `--rebind` WARNING
  block).
- A user who runs `niwa init ... --bootstrap` and then immediately
  tries to `git push` (without `-u`) gets a generic git "no
  upstream branch configured" error. The success message must show
  the full `git push -u origin niwa-bootstrap` command.
- If the user abandons the bootstrap workspace, the registry entry
  persists until pruned. This is no different from today's normal
  init abandonment scenario, but bootstrap may make abandonment more
  common (users experimenting). Future `niwa registry prune`
  detecting orphan entries would help.

**Implementation notes (for the design doc to expand)**

- The bootstrap branch name defaults to `niwa-bootstrap` (fixed; no
  configuration flag in v1). A future `--bootstrap-branch <name>`
  flag can be added if demand surfaces.
- The commit message defaults to `Initial niwa workspace config`
  (fixed; no configuration flag). The user can `git commit --amend`
  to author over it.
- The cleanup defer at `init.go:221-225` must be disarmed
  (`workspaceCreated = false`) when the bootstrap path succeeds —
  matching today's success-path pattern at `init.go:395`.
- Post-flight, registry write, `SaveState`, vault bootstrap pointer,
  overlay discovery, and `writeLandingPath` all run unchanged.
- The plugin auto-install path (`init.go:281-283`) does NOT fire on
  bootstrap because the scaffold is unambiguously rank-1.
<!-- decision:end -->
