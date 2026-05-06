# Phase 2 Research: User Researcher Perspective

PRD: `niwa init <name>` creates `<cwd>/<name>/` and uses the positional name
as the effective workspace name everywhere.

## Lead A: Naming and path edge cases

### Findings

The codebase has two separate, non-overlapping notions of "name validation"
that bear on what `<name>` may legally contain:

1. **Workspace config name** — `[workspace] name` in `workspace.toml`. Validated
   by regex `^[a-zA-Z0-9._-]+$`. See
   `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/config/config.go:15`
   (`validName = regexp.MustCompile(...)`) and the validation call sites at
   lines 351, 362, 368. This regex governs every named identifier in
   `workspace.toml` (workspace name, group names, repo override names, vault
   provider names — see comment at `internal/config/vault.go:88`).
   Notably: no slashes, no spaces, no Unicode, no leading-dot exclusion (so
   `.foo` parses), no length cap.

2. **Registry display name** — names listed by completion and `niwa status`.
   Validated by `validRegistryName` at
   `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/config/registry.go:96-106`
   and the parallel `workspace.ValidName` at
   `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/workspace/state.go:404-414`.
   This is a much looser filter: it only rejects ASCII controls and Unicode
   `Cf/Zl/Zp` (format / line separator / paragraph separator) characters.
   The rationale (per the docstring) is to keep cobra's `__complete` line
   protocol from being corrupted and to block bidi-spoof attacks. It does
   **not** restrict slashes, spaces, or general Unicode.

The CLI today does **not** apply either of these to the positional `<name>`
argument before persisting it. Walking through `runInit` at
`/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/cli/init.go:112-241`:

- The argument is captured as-is (line 97: `name := args[0]`).
- `modeNamed` calls `workspace.Scaffold(cwd, name)` which stamps `<name>`
  into the toml template (`internal/workspace/scaffold.go:107`). The
  post-flight `config.Load` at line 181 will *then* run `validate()` and
  reject if the name fails the strict regex. So `niwa init "foo bar"` and
  `niwa init .foo` already fail today in `modeNamed` — but with a confusing
  error pointing at `workspace.toml`, not at the user's input.
- `modeClone` skips the scaffold (the toml comes from upstream) and writes
  the user-provided name straight into the registry (line 221:
  `globalCfg.SetRegistryEntry(registryName, entry)`). So
  `niwa init "foo bar" --from o/r` would **silently succeed** today, with
  the registry holding a name that the regex would reject. Today the
  inconsistency is hidden because no command stamps the registry name into
  the toml; under the new PRD, the override flows in the other direction
  (registry/instance-state name → readers), so weird names will be more
  visible.
- The `<name>` is also used as a path component (line 118 etc., currently
  just `cwd`; under the new behavior it becomes `filepath.Join(cwd, name)`).
  No path-component validation happens anywhere upstream.

For comparison, the `--from` slug (a separate input) is parsed strictly by
`source.Parse` at
`/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/source/parse.go`,
which rejects whitespace, empty segments, multiple separators, and so on.
There is precedent for upfront strict input validation, but it lives on the
source slug, not on the workspace name.

#### Sub-case decision matrix

##### A1. Filesystem-special characters (spaces, leading dots, Unicode, control chars)

- **Spaces** (`niwa init "foo bar"`): the existing `[workspace] name` regex
  rejects them, but only post-flight in `modeNamed`, and not at all in
  `modeClone`. Filesystem-wise spaces are fine on macOS/Linux/Windows. The
  user impact under the new behavior: directory `<cwd>/foo bar/` works but
  shells require quoting forever after.
- **Leading dot** (`niwa init .foo`): `[workspace] name` regex accepts it.
  Filesystem-wise creates a hidden directory — `ls` won't show it,
  `niwa go .foo` works but the user may forget the workspace exists.
- **Unicode** (`niwa init projeto-são-paulo`): regex rejects (only ASCII
  alnum + `._-`). Filesystem accepts. Some shells/terminals corrupt
  non-ASCII names depending on locale.
- **Control characters / bidi overrides** (`niwa init $'foo\x1b[31m'`):
  rejected by `workspace.ValidName` at the registry layer. Filesystem-wise
  these are dangerous (terminal escape injection in `niwa status` output).

**Options considered for A1**:
- (a) Reuse the existing `^[a-zA-Z0-9._-]+$` regex for the positional
  `<name>` and reject anything else upfront with a clear error. Pro: aligns
  with existing toml validation; closes the `modeClone` gap; simple,
  predictable. Con: rejects perfectly fine paths like `projeto-são-paulo`
  that some users would expect to work.
- (b) Looser sanity check (no whitespace, no control chars, no path
  separators) and let the filesystem handle the rest. Pro: more permissive,
  closer to "name is just a path component." Con: introduces a third name
  validation regime alongside the toml regex and the registry sanitizer;
  asymmetry between what `niwa init <name>` accepts and what
  `[workspace] name` accepts.
- (c) No new validation — let the post-flight toml validate fail if the
  name is bad. Pro: zero code change. Con: error message points at the
  wrong thing; inconsistent between scaffold and clone modes; the user
  has to undo a potentially-created `<cwd>/<name>/` directory after the
  failure.

**Recommended position**: (a) — apply the existing
`^[a-zA-Z0-9._-]+$` regex to the positional `<name>` upfront, before any
filesystem writes. Rationale: the override semantics in this PRD make the
positional `<name>` the workspace name everywhere, so it must satisfy the
existing `[workspace] name` rules. Adding upfront validation produces a
clear error pointing at the user's input, not at a synthesized
workspace.toml. This also incidentally rejects A2/A3/A4/A5 as a side
effect.

##### A2. Path separators in the name (`niwa init foo/bar`)

- **Filesystem possibilities**: `filepath.Join(cwd, "foo/bar")` produces
  `<cwd>/foo/bar`. With `os.MkdirAll` it would silently create
  intermediate `foo/`. The `[workspace] name` regex would reject `foo/bar`
  on the post-flight check.
- **User intent ambiguity**: a user typing `niwa init foo/bar --from o/r`
  may mean "put the workspace inside `foo/bar/`" or may have made a typo
  (slug-shaped name). The latter is more common — slashes look like a
  source slug.

**Options**:
- (a) Reject any name containing `/` or `\\` upfront. Pro: rules out the
  ambiguity; user retypes; aligns with regex (a) above. Con: forecloses a
  potential nested-init use case.
- (b) Treat as nested path, `MkdirAll` intermediates. Pro: power-user
  flexibility. Con: inconsistent with `[workspace] name` regex; no other
  place in the codebase does this; expands scope.

**Recommended position**: (a) — reject. Already covered by recommendation
A1. The PRD should explicitly state that `<name>` is a single path
component, not a path. Users wanting nested init can `mkdir -p foo && cd
foo && niwa init bar --from o/r`.

##### A3. Absolute paths (`niwa init /tmp/foo`)

- **Filesystem possibilities**: `filepath.Join(cwd, "/tmp/foo")` resolves
  to `/tmp/foo` (Go's `filepath.Join` does this). So absent validation,
  `niwa init /tmp/foo` would create a workspace at `/tmp/foo/` regardless
  of where the user is — surprising.
- **`[workspace] name` regex** rejects (slash).

**Options**:
- (a) Reject (already covered by A1/A2 — slash forbidden).
- (b) Support as a deliberate "absolute path" mode. Con: introduces a new
  CLI shape mid-PRD; the PRD's stated value proposition is "no mkdir+cd";
  letting a name be an absolute path is a different feature.

**Recommended position**: (a) — reject. PRD scope is explicitly
"`<cwd>/<name>/`". An absolute-path init is a separate feature, deferred
or rejected entirely.

##### A4. `..` traversal (`niwa init ../foo`)

- **Filesystem possibilities**: `filepath.Join(cwd, "../foo")` = parent's
  `foo/`. Regex rejects (slash + `..`).
- **Even without slashes**: `niwa init ..` alone means "the parent
  directory" via `filepath.Join`. That's a hidden semantic that the PRD
  shouldn't expose.

**Options**:
- (a) Reject any name containing `..` or starting with `.`. Pro: removes
  surprise. Con: regex `[a-zA-Z0-9._-]+` actually allows `.foo` and `..`.
- (b) Reject only `..` and `.` exactly (the path-traversal cases) but
  allow `.foo` (hidden directory).
- (c) Reject anything containing `..` as a substring.

**Recommended position**: (b) — reject the names `..` and `.` exactly,
but otherwise let the existing regex govern. The string `.foo` is a
legitimate (if unusual) workspace name. Forbidding the dot-traversal
literals removes the only filesystem-traversal escape hatch the regex
allows.

##### A5. Empty / just-slash inputs (`niwa init ""`, `niwa init /`)

- **`niwa init ""`**: cobra's `MaximumNArgs(1)` allows zero args; an empty
  string is one arg. `args[0] == ""` would currently fall through to
  `name := ""`, hitting `modeScaffold`-via-condition-checks but likely
  scaffolding with the default name. The post-flight regex would reject
  an empty name — but only if it gets stamped, which the empty case
  short-circuits.
- **`niwa init /`**: regex rejects (slash). Without validation,
  `filepath.Join(cwd, "/")` = `cwd`, which collapses the new behavior to
  the old one — confusing.

**Options**:
- (a) Reject both with an upfront error.
- (b) Treat empty arg as "no arg" (modeScaffold).

**Recommended position**: (a) — reject both with the same regex
violation. Empty positional is almost certainly a quoting accident
(`niwa init "$NAME"` with `NAME` unset); a clear "name required, must
match regex" error is far better than silently scaffolding.

##### A6. Existing registry collision

This is the trickiest sub-case because it touches the existing
"registry-as-source-of-truth" model. Today:

- `niwa init <name>` with `<name>` already in the registry resolves to
  `modeClone` using the registered source URL (`init.go:104-107`). User
  intent: "set up the workspace I previously registered, in this new
  directory."
- The registry entry has a `Root` field pointing to where the workspace
  was last initialized (`init.go:213-215`).
- Users do invoke this pattern intentionally:
  `cd ~/other-dir && niwa init my-team` — see README lines 152–159.

Under the new PRD, `niwa init my-team` (no `--from`) when `my-team` is
registered would:
1. Compute `targetDir = <cwd>/my-team`.
2. Pre-flight: does `<cwd>/my-team` exist?
3. If not, create it, clone there, **update registry's `Root` to the new
   path** (overwriting the old one).

This is consistent with the PRD's "name flows everywhere" goal but
introduces a second-order question: should niwa warn the user that they
are about to silently rebind the registered workspace's `Root` from the
previous location to the new one?

**Options**:
- (a) Silently update `Root` (current behavior already does this — see
  `init.go:221`). Pro: no new code. Con: a user who `cd`s to a fresh
  directory and runs `niwa init my-team` expecting "use the registered
  config" now also moves the workspace's registry-blessed location. The
  old `~/work/my-team/.niwa/` is orphaned.
- (b) Detect "registry entry exists with `Root != newTarget`" and emit a
  warning ("the registered workspace was previously at `<old>`; updating
  to `<new>`"). Pro: user-visible signal that something non-trivial is
  happening. Con: extra path; possibly noisy if users intentionally
  re-init multi-dir.
- (c) Reject with an error and require the user to pass an explicit flag
  (e.g., `--rebind`) to overwrite. Pro: safest. Con: breaks the
  README-documented `cd ~/other-dir && niwa init my-team` flow.

**Recommended position**: (b) — warn, don't error. The README-documented
re-init-elsewhere pattern stays a single-command flow. The warning
informs the user that the registry now points to a different `Root`, so
they know the previous location is no longer registry-blessed (it
remains usable via cwd-walk discovery, but `niwa go my-team` will go to
the new location). This matches niwa's existing pattern of
warning-on-stderr for non-fatal-but-surprising state changes.

**Notes on the pre-flight check** (PRD scope item):

- The PRD says "error if `<cwd>/<name>` already exists, for any
  pre-existing path type." Per the explore round 1 lead at
  `wip/research/explore_niwa-init-creates-workspace-dir_r1_lead-preflight-conflict-semantics.md`,
  the implementation uses `os.Stat` on the target. This handles A6 case
  where someone has an unrelated `<cwd>/my-team/` directory cleanly: the
  pre-flight rejects, regardless of registry state. A6 only kicks in
  when the target does **not** exist.

### Implications for Requirements

The PRD should add an **Input Validation** subsection that pins the
following:

1. **Allowed characters in `<name>`**: the existing
   `^[a-zA-Z0-9._-]+$` regex (matches `[workspace] name` / group / repo
   override / vault provider validation). Phrase it as "the same
   characters allowed elsewhere in `workspace.toml`," not as a freshly
   minted rule, so future contributors don't see it as a one-off.
2. **Reserved literals**: reject `.` and `..` exactly (the only
   regex-passing names that double as path-traversal sentinels).
3. **Empty positional**: explicitly enumerated as an error case with a
   message suggesting either `niwa init` (no arg) or `niwa init <name>`.
4. **Validation timing**: must run **before** any filesystem write
   (including before the target-exists pre-flight). A bad name should
   not leave any directory or partial state behind.
5. **Error message**: should quote the offending input back at the user
   and reference the allowed set explicitly. Consistent with existing
   slug-parse errors (`source slug %q contains whitespace`, etc.).
6. **Registry-collision warning** (sub-case A6): when the positional
   `<name>` is already registered to a different `Root`, init should
   succeed but emit a stderr warning ("registered workspace `<name>` was
   previously rooted at `<old-path>`; updating registry to point at
   `<new-path>`"). This is in scope for this PRD because the new
   behavior makes the rebind more visible and more likely to surprise
   users running `cd ~/other-dir && niwa init my-team`.

The PRD's "Out of Scope" list currently says "Naming validation rules
(allowed characters, length limits). These are whatever the existing
registry already enforces; this PRD doesn't add new rules." That
statement is now slightly misleading — the existing registry sanitizer
(`workspace.ValidName`) is **looser** than the toml regex, and today
the CLI applies neither upfront. Recommend rewording: "Name validation
reuses the existing `[workspace] name` regex (`^[a-zA-Z0-9._-]+$`),
applied upfront before filesystem writes. This PRD adds the upfront
application but does not introduce a new rule set."

### Open Questions

1. **Registry collision warning vs. error**: the recommendation is
   "warn, succeed, rebind." User confirmation may prefer "error,
   require explicit flag." Worth surfacing.
2. **Should the regex itself be tightened in this PRD?** `^[a-zA-Z0-9._-]+$`
   allows `..` (rejected by the recommended carve-out) and `.foo`
   (recommended to keep). It also has no length cap — a 1000-character
   name passes today. Tightening is out of scope per the PRD's stated
   intent; flagging in case the user wants to scope-creep.
3. **Case sensitivity on case-insensitive filesystems**: macOS default
   HFS+/APFS is case-insensitive. `niwa init Foo` and
   `niwa init foo` would both create the same directory. The registry,
   however, is case-sensitive (it's a Go map keyed on the literal
   string). So a user can register both, but only one survives on
   disk. Today this is also true; the PRD doesn't introduce the
   conflict, but it makes it more visible. Probably out of scope.

## Lead B: Affected user types and scenarios

### Findings

The README quickstart at lines 36–41 currently shows:

```
mkdir my-workspace && cd my-workspace
niwa init my-project
```

This is the canonical first-time-user introduction to niwa, and it is
exactly the pattern the PRD wants to remove. Note the asymmetry: the
mkdir uses `my-workspace` and the init uses `my-project` — the user has
to read carefully to notice the directory and the workspace name are
distinct. Under the new behavior the README would collapse to:

```
niwa init my-project
cd my-project
```

(or just `niwa init my-project` with the user navigating later via
`niwa go my-project`).

The README at lines 152–159 also documents the registry-replay pattern:

```
cd ~/other-dir
niwa init my-team    # uses the registered source from the first --from
niwa apply
```

Under the new behavior this becomes:

```
cd ~/other-dir
niwa init my-team    # creates ~/other-dir/my-team/ and inits inside it
```

The `cd ~/other-dir` is still required (it's where the new workspace
goes), but the implicit "and inits in cwd" mental model becomes "and
inits in `<cwd>/<name>`." This is a small but real cognitive shift for
existing users. (See sub-case A6 above for the registry-rebind issue.)

#### CI/automation usage

`grep "niwa init" .github/workflows/*.yml` returns no results in the
niwa repo — niwa's own CI does not invoke `niwa init`. Functional
tests do call it (`test/functional/steps_test.go:847`,
`test/functional/mesh_steps_test.go:52,95,205`), but those are
internal test harness usage, not user-facing CI patterns.

External CI usage almost certainly exists (users running niwa in
their pipelines), but there's no evidence in this repo to enumerate
patterns. The most plausible CI shape is a non-interactive
`niwa init <name> --from <org/repo> && niwa apply` invocation; under
the new behavior, the directory creation happens inside the CI
runner's working directory and `niwa apply` would either need a
`cd <name>` or path-aware invocation. This is a real
breaking-change consideration for any CI script following the old
pattern.

#### Power user with multiple concurrent workspaces

The user who initiated this PRD fits this profile (per the explore
findings: "uses `niwa init --from <src>` (no positional name)").
For them, the no-positional-name path is unchanged. The new
*affordance* is that they can now choose to type the name and get
the directory for free, where previously typing the name was just a
registry hint with no UX payoff (and one that wasn't even consistent
across `niwa go` vs `niwa status`).

#### Existing user with internalized old flow

The README and prior docs have shown `mkdir + cd + niwa init` for
some time. An existing user who has internalized this will, on
upgrading, type the old pattern and hit the new "directory exists"
error. Per Research Lead #3 in the PRD scope, the error message is
intended as the migration aid. Importantly, this user's
`mkdir my-workspace && cd my-workspace` step creates a directory
named `my-workspace`; their `niwa init my-project` then tries to
create `<cwd>/my-project/` — which doesn't conflict with
`my-workspace`, so the old pattern silently produces a nested
result. That's worse than an error. Worth flagging.

### Implications for Requirements

Draft user stories for the PRD's "User Stories" section:

1. **As a first-time niwa user**, I want `niwa init <name>` to
   create the workspace directory for me, so I can follow the
   quickstart with a single command and don't have to remember to
   `mkdir + cd` first.

2. **As a first-time user setting up a shared workspace from my
   team's config**, I want `niwa init <name> --from <org/repo>` to
   land me in a predictably-named directory containing the cloned
   config, so I can immediately `cd <name> && niwa apply` without
   guessing where the files went.

3. **As a power user with multiple concurrent niwa workspaces**, I
   want a single command to spin up a new workspace under my
   current directory, so context-switching between workspaces is
   one `niwa init <name> --from <src>` away rather than three
   shell steps.

4. **As an existing niwa user who learned the old `mkdir + cd +
   niwa init` flow**, I want the new behavior to fail loudly and
   explain what changed when I run my old muscle-memory pattern,
   so I can update my habits without losing data or producing
   surprising nested workspaces.

5. **As a user re-initializing a registered workspace in a new
   directory** (`cd ~/other-dir && niwa init my-team`), I want the
   command to clearly tell me when it's rebinding the registry's
   `Root` from a previous location, so I don't accidentally orphan
   my old workspace clone.

6. **As a CI/automation operator scripting niwa for a build
   pipeline**, I want the new directory-creation behavior to be
   explicit in the `--help` text and `README`, so I can update my
   `niwa init` invocations without trial and error in the runner.

7. **As a user who passes `niwa init <name> --from <src>` and
   inspects the result with `niwa status`**, I want the workspace
   name shown to be the name I typed (`<name>`), not whatever the
   cloned config's `[workspace] name` says, so the system reflects
   my intent consistently.

Story 7 is the "name override" half of the PRD. It belongs in the
user stories list alongside the directory-creation stories so the
PRD's two-part value prop (directory + name precedence) is explicit
in the user-narrative section, not buried in the requirements.

The PRD's Research Lead #3 ("discoverability and migration") maps
to Story 4. The error message wording is already flagged as a
design concern; the user story formalizes the discovery
expectation.

### Open Questions

1. **CI breakage risk**: niwa's own repo doesn't use `niwa init`
   in CI, but external users certainly do. Is there appetite to
   add a release-note migration bullet, or is the pre-1.0 status
   the only mitigation?
2. **Story 5 (registry rebind)**: depends on the resolution of
   sub-case A6 above. If the chosen behavior is "error, not warn,"
   the story rewords to "I want the command to refuse to rebind
   without explicit confirmation."
3. **Story 4 specificity**: should the PRD enumerate the exact
   error-message text, or leave it to implementation? The scope
   doc's Research Lead #1 already calls out error voice; suggest
   the PRD specify intent ("must mention the new flow," "must
   suggest the user remove the empty pre-created directory") but
   not literal wording.

## Summary

The PRD should pin upfront name validation to the existing
`^[a-zA-Z0-9._-]+$` regex (with explicit `.` / `..` carve-outs and
empty-string rejection), applied before any filesystem write, so
all of the path-edge-case sub-cases (slash, absolute, traversal,
empty) collapse to one consistent rule rather than scattered
behavior. The registry-collision sub-case (A6) is the only edge
case that warrants a new user-visible signal — a stderr warning
when the positional `<name>` is already registered to a different
`Root` and is about to be rebound. Seven user stories are drafted
covering first-time users, returning users with old muscle memory,
the power-user / PRD-initiator profile, the CI/automation operator,
and the registry-replay user; the PRD's `mkdir + cd` quickstart in
README.md is the most concrete documentation update the change
forces, and the existing-user migration story is the highest-risk
discoverability gap.
