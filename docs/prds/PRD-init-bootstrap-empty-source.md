---
status: Draft
problem: |
  A user adopting a freshly-created GitHub repository as a niwa-managed
  workspace today must run three separate commands (`niwa init`, `niwa
  create`, `niwa apply`) AND hand-author `.niwa/workspace.toml` before
  niwa can help them. The friction discourages first-time adoption and
  means a new niwa user's first interaction is "edit a TOML file you've
  never seen before." There is no path that produces a usable niwa
  workspace from a fresh remote in one step.
goals: |
  Collapse the workspace bootstrap experience into a single command
  that lands the user inside a real niwa worktree with a scaffolded
  workspace config committed on a feature branch ready to push.
  Bootstrap serves as the intro experience for new niwa adopters and
  removes the chicken-and-egg between needing a workspace config to
  use niwa and needing to use niwa to know what the config looks like.
---

# PRD: init bootstrap from empty source

## Status

Draft

## Glossary

Terms used in this PRD with their precise meaning in niwa:

| Term | Definition |
|------|------------|
| **Workspace** | A niwa-managed directory at `<cwd>/<name>/` containing `.niwa/workspace.toml` and (after `niwa create`) one or more instances. |
| **Workspace root** | The absolute path of the workspace directory, i.e. `<cwd>/<name>/`. |
| **Instance** | A specific materialized workspace state at `<workspaceRoot>/<instanceName>/` containing cloned repos and `.niwa/instance.json`. |
| **Instance root** | The absolute path `<workspaceRoot>/<instanceName>/`. |
| **Worktree** (also "session worktree") | A `git worktree`-managed directory at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`. |
| **Session** | A niwa-managed `(branch, worktree, daemon, lifecycle state)` quadruple identified by an 8-hex-char `<sid>` (session ID). |
| **`sid`** | An 8-hex-char session identifier produced by `niwa session create`. |
| **Group** | A visibility classification (`public` or `private`) under which cloned repos are placed at `<instanceRoot>/<group>/<repo>/`. |
| **Bootstrap repo** | The specific repo named in `--from <owner>/<repo>` whose existence triggers bootstrap. |
| **Source org** | The `<owner>` component of the `--from` slug; also the value of the scaffolded `[[sources]] org`. |
| **Scaffold** | The act of writing a freshly-authored `.niwa/workspace.toml` (and `.niwa/claude/.gitkeep`) on disk. |
| **Bootstrap branch** | The branch `niwa-bootstrap/<sid>` created inside the session worktree to hold the scaffolded commit. |
| **Channels (mesh)** | The `[channels.mesh]` feature that enables session worktrees and role inboxes; required for the bootstrap path. |
| **Landing-path file** | The file niwa writes via `writeLandingPath()` whose contents tell the shell wrapper which directory to `cd` to. |

## Problem Statement

A user who creates a fresh GitHub repository and wants to manage it as
a niwa workspace today must:

1. Clone the empty remote outside any workspace.
2. Hand-author `.niwa/workspace.toml`, knowing the schema cold.
3. Push the configured remote.
4. Run `niwa init <name> --from <slug>` to materialize the workspace.
5. Run `niwa create` to provision an instance.
6. Run `niwa apply` (often) to install channel infrastructure.
7. Run `niwa session create <repo> <purpose>` to land in a worktree.

The friction concentrates in the first interaction, when the user
knows niwa least. Step 2 demands schema knowledge that doesn't exist
yet for first-time adopters; steps 4–7 are independent commands the
user must sequence correctly. The chicken-and-egg is real: niwa's
value is in the workspace lifecycle it manages, but the lifecycle
won't start until the workspace.toml exists, and the workspace.toml is
the artifact the lifecycle is supposed to manage.

The affected audience is every first-time niwa user adopting a new
GitHub repository: solo developers starting a project, teams claiming
a new repo for a workspace-managed initiative, anyone whose first
contact with niwa is "I have a fresh repo and I want niwa to help me."

The trigger for acting now: niwa's onboarding surface is a recurring
adoption blocker, and the existing materialize path returns a generic
"no niwa config found" error that gives the user no path forward
beyond reading the manual.

## Goals

1. **One command, full setup.** `niwa init <name> --from <slug>
   --bootstrap` produces a usable niwa workspace, an instance, a
   committed scaffold, and a worktree the user can `cd` into — no
   intermediate manual steps.
2. **Self-revealing.** Per R19, the success output names every niwa
   artifact the command produces (workspace root, instance root,
   branch, worktree path, registry entry) so the user learns niwa's
   layout by reading the command's output.
3. **Recoverable.** Per R7, partial-failure leaves the user in a state
   where the existing standalone commands (`niwa create`, `niwa
   session create`, `niwa destroy`) can finish or reset the work.
4. **Adjacent-failure clarity.** Every failure mode the materialize
   path reaches (auth 401/403, 404 missing/private/zero-commit,
   ambiguous markers, no-marker without `--bootstrap`) gets a
   case-specific Detail+Suggestion message per R10–R13 instead of
   today's opaque wrap.
5. **No surprises.** Per R4, the bootstrap pipeline run clones exactly
   the repo the user named — never the user's whole org by accident.
6. **No automatic push.** Per R24, bootstrap commits locally; the user
   controls when and whether the branch reaches the remote.

## User Stories

- **As a solo developer starting a new project**, I want to run one
  command against my just-created GitHub repository and end up inside
  a working niwa workspace, so that I can start coding without
  learning niwa's config schema first.
- **As a developer onboarding to niwa**, I want the bootstrap command
  to name every artifact it produces (workspace, instance, branch,
  worktree, registry entry), so that I learn niwa's layout by doing
  instead of by reading documentation.
- **As a team lead adopting a new repository in an established
  org**, I want bootstrap to clone exactly the repo I named — not
  every repo in my org — so that the first-run setup is fast and the
  bootstrap commit is reviewable.
- **As a user hitting an auth or 404 error during bootstrap**, I want
  the error message to tell me exactly what's wrong and how to fix
  it, so that I don't have to file an issue or read the niwa source
  to recover.
- **As a user who ran `--bootstrap` against the wrong slug**, I want
  the partial state on disk to be named in the failure message and
  tearable-down via the `niwa destroy <name>` command named in that
  message, so that I can start over without surgery.

## Requirements

### Functional

**R1.** `niwa init <name> --from <github-slug> --bootstrap` shall
execute the lifecycle init → create → session-create as a single
chained command. On success the user shall have a niwa workspace at
`<cwd>/<name>/`, an instance under that workspace, a cloned bootstrap
repo under the instance's group folder, a session worktree at
`<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`, and a committed
bootstrap branch inside the worktree containing the scaffolded
`.niwa/workspace.toml`.

**R2.** `niwa init --from <github-slug> --bootstrap` (no positional
`<name>`) shall derive the workspace name from the slug's repo
basename. `--from owner/foo --bootstrap` produces a workspace at
`<cwd>/foo/`. This behavior is scoped to `--bootstrap` only; today's
behavior of `niwa init --from <slug>` (no positional, no
`--bootstrap`) materializing in cwd is unchanged.

**R3.** The bootstrap scaffold's `.niwa/workspace.toml` shall match
the body in [Appendix A](#appendix-a-scaffold-template) byte-for-byte
after derived-value substitution. R3 does not define scaffold content
on its own; Appendix A is the single source of truth.

**R4.** The first-pipeline-run clone scope shall be restricted via the
scaffold's `[[sources]]` `repos = ["<bootstrap-repo>"]` allow-list
(see Appendix A). The pipeline shall clone exactly the bootstrap repo
on first run regardless of source-org size.

**R5.** The bootstrap branch shall be named `niwa-bootstrap/<sid>`
where `<sid>` is the 8-hex-char session identifier produced by
`niwa session create`. The branch name shall be stored in the session
lifecycle state JSON as a new field `branch_name`. When `branch_name`
is empty or absent (back-compat), session callers fall back to
constructing `session/<sid>`. Because each `niwa session create`
invocation generates a fresh `<sid>`, retries always produce distinct
branch names; no collision-detection logic is required.

**R6.** Each chained step's success criteria shall match the
corresponding standalone command's success criteria. Concretely:
the chain shall pass the same arguments and environment to the
internal create call that `niwa create` would receive standalone, and
the same to the internal session-create call. No new success
preconditions specific to bootstrap shall be introduced.

**R7.** The chained command shall apply stepwise rollback on failure:

- **init step fails**: remove `<cwd>/<name>/`, no registry entry
  (same as today's `niwa init` failure cleanup).
- **create step fails after init succeeded**: tear down the instance
  directory (`niwa destroy --instance` semantics). Keep
  `<cwd>/<name>/.niwa/workspace.toml`, the workspace directory, and
  the registry entry so the user can retry with `niwa create`.
- **session-create step fails after create succeeded**: keep the
  instance intact. Session-create's own internal rollback covers
  worktree, branch, and state-file artifacts. The error message
  points at `niwa session create <repo> bootstrap` for retry.

Daemon shutdown during create-fail rollback follows the same contract
as `niwa destroy --instance`: 5 s graceful shutdown via SIGTERM, then
SIGKILL. Daemon-shutdown timeouts do not block the rollback.

**R8.** When `niwa init <name> --from <slug> --bootstrap` is invoked
against an existing target, niwa shall refuse with bootstrap-specific
Detail+Suggestion text. The three sub-cases:

1. **Workspace already exists** (`<cwd>/<name>/.niwa/workspace.toml`
   present): Detail "workspace `<name>` already exists at
   `<absPath>`."; Suggestion "Run `niwa destroy <name>` to start over,
   or `niwa session create <repo> bootstrap` to land in a fresh
   worktree on the existing workspace."
2. **Registry name in use elsewhere** (registry entry for `<name>`
   points to a different root): Detail "workspace name `<name>` is
   already registered (root: `<otherRoot>`)."; Suggestion "Run
   `niwa destroy <name>` to remove the registry entry, or pick a
   different `<name>`."
3. **Non-niwa file/dir at target** (`<cwd>/<name>/` is a file,
   symlink, or directory without `.niwa/`): Detail "`<absPath>`
   already exists (`<file|symlink|directory>`)."; Suggestion "Pick a
   different `<name>` or remove the path and retry."

**R9.** When the source URL's host is not GitHub, niwa shall refuse
before any git invocation. The error string shall be exactly:
`bootstrap supports only GitHub sources in v1; got host=<host>`.
No partial state shall be written to disk.

**R10.** When the source remote returns HTTP 401 or 403, niwa shall
produce a Detail+Suggestion containing this exact substring on
stderr: `verify GH_TOKEN scopes; fine-grained PATs need Contents:
read, classic PATs need repo scope`.

**R11.** When the source remote returns HTTP 404, niwa shall produce
a Detail+Suggestion containing ALL three of these exact substrings on
stderr (one per cause):

- `verify the slug is correct (org/repo) and the repo exists`
- `if the repo is private, set GH_TOKEN with read access`
- `if the repo is brand new and has no commits yet, push at least one commit (an empty README is enough) and retry with --bootstrap`

**R12.** When the source remote contains both `.niwa/workspace.toml`
and a root-level `workspace.toml`, niwa shall surface the existing
`*config.AmbiguousMarkersError` message verbatim. The Detail
substring must include the literal text returned by today's
`AmbiguousMarkersError.Error()` (specifying both file paths and the
`--from <slug>:<subpath>` escape hatch).

**R13.** When the source remote is reachable but contains no niwa
config (the `*config.NoMarkerError` case), behavior depends on the
flag combination AND stdin's `IsStdinTTY()` value. R25 (mutual
exclusion) runs upstream of R13 and rejects `--bootstrap` +
`--no-bootstrap` before R13 fires.

| TTY | --bootstrap | --no-bootstrap | Behavior |
|-----|------------|----------------|----------|
| Yes | set | unset | Proceed without prompting. |
| Yes | unset | set | Fail-fast with NoMarker text + decline reason. |
| Yes | unset | unset | Prompt: `Remote has no .niwa/workspace.toml. Scaffold a minimal config and stage it on a niwa-bootstrap branch? [Y/n] `. Proceed only on Y. Exit 0 on N. |
| No | set | unset | Proceed without prompting. |
| No | unset | set | Fail-fast with NoMarker text + decline reason. |
| No | unset | unset | Fail-fast: `remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold`. |

**R14.** R14 is intentionally consolidated into [Appendix A:
Scaffold Template](#appendix-a-scaffold-template). The appendix is the
single source of truth for scaffold contents. R14 itself only states:
the scaffolded `.niwa/workspace.toml` shall contain exactly the keys
and sections defined in Appendix A, in the order defined there.
Mapping from `Repo.Private` (the GitHub API metadata bool) to the
visibility group:

- `Private: true` → block `[groups.private]` with `visibility = "private"`
- `Private: false` → block `[groups.public]` with `visibility = "public"`

**R15.** Bootstrap shall write an empty `.niwa/claude/.gitkeep` file
alongside the scaffolded `.niwa/workspace.toml` so the content
directory pushes cleanly when the user later uncomments
`[claude.content.workspace]`.

**R16.** The `[groups.<vis>]` block shall derive `<vis>` exclusively
from the `Private` bool field returned by `GET /repos/{owner}/{repo}`.
The `Visibility` string field from the same API response shall not be
read by the scaffold-writer code path. This is a security invariant
against TOML-metacharacter injection via a malicious GitHub API host.

**R17.** When the visibility lookup fails (network error, 401, 403,
404, 5xx), niwa shall:

- Default the scaffold's group block to `[groups.public]` (with
  `visibility = "public"`).
- Emit this exact stderr `note:` line: `note: could not determine
  remote visibility (<cause>); defaulting to [groups.public]. Edit
  .niwa/workspace.toml to change.` where `<cause>` is one of
  `network error`, `authentication`, `not found`, or `server error`.
- Continue with bootstrap; visibility-lookup failure shall not abort
  the chain.

**R18.** The bootstrap branch's commit shall use the user's normal
git identity from `user.name` / `user.email`. The commit invocation
shall:

- Not pass `--author` as an argv element.
- Not set `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, `GIT_AUTHOR_DATE`,
  `GIT_COMMITTER_NAME`, `GIT_COMMITTER_EMAIL`, or `GIT_COMMITTER_DATE`
  in the subprocess environment.
- Use the exact subject `Initial niwa workspace config` (no body).

**R19.** On full success, niwa shall write the success block to
stderr matching the exact format in [Appendix B: Success Block
Format](#appendix-b-success-block-format). The success block shall be
preceded by one blank stderr line and followed by one blank stderr
line. Lines shall appear in the order specified in Appendix B.

**R20.** The shell-wrapper landing-path mechanism (existing helper
`workspace/landing.go::writeLandingPath`) shall be invoked with the
absolute path of the session worktree at
`<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`. The file written is
the same file `niwa session create` writes today (path supplied via
the `NIWA_RESPONSE_FILE` environment variable; format is a single
line containing the absolute path without trailing newline).

**R21.** The GitHub-host check (R9) shall run before any git
invocation inside the bootstrap orchestrator. Failed host check shall
not invoke `git init`, `git remote add`, `git fetch`, or any other
git subprocess.

**R22.** All git invocations within the bootstrap orchestrator shall
use `exec.CommandContext("git", args...)` with arguments as separate
elements, matching the established pattern at
`internal/workspace/clone.go`. The orchestrator shall expose an
injectable exec invoker (interface or function field) so tests can
record every git invocation without running git.

**R23.** The chained command's exit codes:

| Code | Meaning |
|------|---------|
| 0 | Full success, OR TTY user typed `N` at the R13 prompt (clean decline). |
| 1 | Step failure (init, create, or session-create). The stderr error message identifies which step via the literal prefix `bootstrap step=<init\|create\|session-create>:`. |
| 2 | Flag-validation error (e.g., `--bootstrap` + `--no-bootstrap`). |
| 3 | Host-validation error (R9). |
| 4 | NoMarker without `--bootstrap` (R13's fail-fast cases). |

**R24.** niwa shall NOT invoke `git push` during the bootstrap chain.
The user is responsible for pushing the bootstrap branch after
inspection.

**R25.** A `--no-bootstrap` flag shall exist as a mutually-exclusive
partner to `--bootstrap`. Passing both shall produce the exact error
string: `--bootstrap and --no-bootstrap are mutually exclusive`. Exit
code 2 (per R23).

**R26.** Bootstrap shall NOT invoke `niwa apply` as part of the chain.
Channel infrastructure required for session-create (the
`.niwa/roles/<repo>/` tree) is installed by `niwa create`'s pipeline
when `[channels.mesh]` is present in workspace.toml (which it is per
Appendix A). The user may invoke `niwa apply` later for drift
checking after making further config edits.

### Stdout vs Stderr

| Surface | Content |
|---------|---------|
| stdout | Per-step progress lines emitted by today's commands (e.g., `Initializing from: <url>` from init's existing pre-clone print). |
| stderr | All success-block content (R19), `note:` lines (R17), warnings, error messages, prompt text (R13 TTY prompt). |
| Landing-path file | Worktree absolute path (R20). |

### Flag Interactions

`--bootstrap` interacts with niwa's existing init flags as follows:

| Companion flag | Behavior |
|---------------|----------|
| `--overlay <slug>` | Compatible. Pass-through to the chained create. Overlay clones run as today. |
| `--no-overlay` | Compatible. Suppresses convention-overlay discovery; pass-through to create. |
| `--skip-global` | Compatible. Pass-through to create. |
| `--no-install-plugins` | Compatible. Pass-through. Bootstrap's scaffold is always rank-1, so the rank-2 plugin-install path never fires regardless. |
| `--rebind` | Refused. Bootstrap's preflight (R8 sub-case 2) refuses registry-collision rather than rebinding silently. Suggestion text points at `niwa destroy`. Exit 2. |
| `--no-bootstrap` | Mutually exclusive with `--bootstrap` per R25. Exit 2. |

### Token Presence Semantics

The visibility-lookup (R16) and the materialize fetch use the same
`resolveGitHubToken()` path as existing niwa commands. Bootstrap does
not introduce new token-presence requirements. Behavior table:

| `GH_TOKEN` set? | Source repo visibility | Tarball fetch outcome | Bootstrap behavior |
|----------------|----------------------|---------------------|--------------------|
| Yes, valid | Public | 200 + probe runs | R13 NoMarker path (proceed with `--bootstrap`). |
| Yes, valid | Private | 200 + probe runs | R13 NoMarker path. |
| Yes, invalid/expired | Either | 401 | R10 message. |
| Yes, lacks scope | Private | 403 | R10 message. |
| No (unset) | Public | 200 + probe runs | R13 NoMarker path. |
| No (unset) | Private | 404 | R11 message (cause: "private repo without read access"). |
| No (unset) | Nonexistent | 404 | R11 message (cause: "verify slug"). |
| Either | Empty (zero commits, public) | 404 | R11 message (cause: "zero-commit"). |

### Non-functional

**N1.** *(Moved to Known Limitations — no fixed latency target in v1.)*

**N2.** Adjacent failure-mode classification (R10, R11, R12) shall be
implemented via a typed `*github.StatusError` value (carrying the
HTTP status code) and an `errors.As`-based classifier seam at the
`runInit` materialize boundary. The classifier precedence order is:

1. `*config.AmbiguousMarkersError`
2. `*config.NoMarkerError`
3. `*github.StatusError` with `StatusCode == 401 || StatusCode == 403`
4. `*github.StatusError` with `StatusCode == 404`
5. Generic fall-through (today's wrap)

String-matching against error text is not acceptable.

**N3.** Workspace-level error sentinels (`ErrSourceConfigMalformed`,
`ErrSourceAuthFailed`, `ErrSourceNotFound`) are deferred to a
follow-up. v1 ships typed GitHub status errors and case-specific
classifier output only.

**N4.** The bootstrap branch name format `niwa-bootstrap/<sid>` is a
durable user-facing contract. Future changes to the format are
treated as breaking.

**N5.** No user secrets shall be written to disk by the bootstrap
path. The visibility lookup uses the existing `resolveGitHubToken()`
path; the token is not persisted to the scaffolded workspace.toml,
the instance state, or any registry entry.

### Notices & Observability

Bootstrap emits the following entries through the
`workspace.Reporter` abstraction (matching existing niwa idioms):

| Trigger | Surface | Format |
|---------|---------|--------|
| Each step start | Reporter status | `bootstrap: step=<init\|create\|session-create>` |
| Step success | Reporter log | `bootstrap: step=<...> done` |
| Visibility-lookup soft-fail (R17) | stderr `note:` | Exact text per R17 |
| TTY prompt (R13) | stderr | Exact text per R13 |
| Mutual-exclusion error (R25) | stderr | Exact text per R25 |
| Host-check refusal (R9) | stderr | Exact text per R9 |
| 401/403 case (R10) | stderr | Detail/Suggestion containing R10 substring |
| 404 case (R11) | stderr | Detail/Suggestion containing all R11 substrings |
| Ambiguous case (R12) | stderr | Today's `AmbiguousMarkersError.Error()` verbatim |
| NoMarker fail-fast (R13) | stderr | Exact text per R13 |
| Success block (R19) | stderr | Per Appendix B |
| Rollback-after-create-step (R7) | stderr | `bootstrap: create step failed; instance directory removed. Workspace at <path> preserved; run niwa create to retry.` |
| Rollback-after-session-step (R7) | stderr | `bootstrap: session-create step failed; instance preserved at <path>. Run niwa session create <repo> bootstrap to retry.` |

No telemetry beyond niwa's existing instrumentation surface is
introduced.

## Acceptance Criteria

Test-fixture conventions used below:

- **`tarballFakeServer`**: niwa's httptest-based GitHub fake (in
  `test/functional/`). Use to stage GitHub API responses (tarball
  endpoint and `/repos/{owner}/{repo}` metadata).
- **`localGitServer`**: niwa's file-system git bare-repo fake. Use
  for the cloned source repo content the pipeline pulls in.
- **Injectable exec invoker**: the orchestrator-level seam introduced
  by R22 that lets unit tests record `*exec.Cmd` invocations without
  executing git.

Each AC names the fixture(s) required.

### Happy paths

- [ ] **Happy path with positional name** (fixture:
  `tarballFakeServer` returning 200 + empty-but-README tree;
  `localGitServer` for clone): `niwa init my-project --from
  owner/my-project --bootstrap` produces, on disk:
  - `<cwd>/my-project/.niwa/workspace.toml` byte-for-byte matching
    Appendix A after derived substitution
  - `<cwd>/my-project/.niwa/claude/.gitkeep` zero-byte file
  - `<cwd>/my-project/<instanceName>/.niwa/instance.json` parses as
    instance-state schema v4
  - `<cwd>/my-project/<instanceName>/.niwa/roles/my-project/`
    directory exists
  - `<cwd>/my-project/<instanceName>/<group>/my-project/.git` exists
  - `<cwd>/my-project/<instanceName>/.niwa/worktrees/my-project-<sid>/`
    exists where `<sid>` matches `[0-9a-f]{8}`
  - Bootstrap branch `niwa-bootstrap/<sid>` exists with exactly one
    commit; commit subject equals `Initial niwa workspace config`;
    commit author/committer match `git config user.name/user.email`
  - Registry entry for `my-project` points to `<cwd>/my-project/`
    (absolute path)
  - Landing-path file contents equal the worktree absolute path

- [ ] **Happy path no positional name** (same fixtures): `niwa init
  --from owner/foo --bootstrap` produces all the above with workspace
  at `<cwd>/foo/`.

### Adjacent failure modes

- [ ] **401 auth error** (`tarballFakeServer` returns 401 for
  tarball): exit code 1; stderr contains the R10 substring; no
  on-disk state remains.

- [ ] **403 auth error** (`tarballFakeServer` returns 403): exit code
  1; stderr contains the R10 substring; no on-disk state remains.

- [ ] **404 (typo case)** (`tarballFakeServer` returns 404; `GH_TOKEN`
  set): exit 1; stderr contains all three R11 substrings.

- [ ] **404 (zero-commit case)** (same fixture: a 404 with `GH_TOKEN`
  unset, simulating GitHub's response for a no-commit repo): exit 1;
  stderr contains all three R11 substrings.

- [ ] **404 (private repo, no token)** (`tarballFakeServer` returns
  404; `GH_TOKEN` unset): exit 1; stderr contains all three R11
  substrings.

- [ ] **Ambiguous markers** (`tarballFakeServer` returns 200 with both
  `.niwa/workspace.toml` and root `workspace.toml`): exit 1; stderr
  contains the exact verbatim string returned by today's
  `(*config.AmbiguousMarkersError).Error()`.

- [ ] **Non-GitHub source** (no fixture; flag-parse stage): `niwa init
  bar --from gitlab.com/owner/repo --bootstrap` exits with code 3;
  stderr contains the exact R9 string; the injectable exec invoker
  records zero git invocations.

### Flag and prompt behavior

- [ ] **TTY prompt Yes** (functional test with pty helper): in TTY,
  `niwa init bar --from owner/foo` with `tarballFakeServer` 200
  empty-tree, user types `y\n` → exit 0; happy-path artifacts.

- [ ] **TTY prompt No**: same fixtures, user types `n\n` → exit 0; no
  scaffolding; no on-disk state under `<cwd>/bar/`.

- [ ] **Non-TTY refusal** (pipe `/dev/null` to stdin): `niwa init bar
  --from owner/foo` with NoMarker fixture → exit 4; stderr contains
  the exact R13 non-TTY-no-flag fail-fast string.

- [ ] **--no-bootstrap suppression** (TTY): `niwa init bar --from
  owner/foo --no-bootstrap` with NoMarker fixture → exit 4; stderr
  contains NoMarker text + decline reason.

- [ ] **Mutual exclusion**: `niwa init bar --from owner/foo
  --bootstrap --no-bootstrap` → exit 2; stderr contains the exact R25
  string.

### Idempotency / conflict (R8)

- [ ] **Conflict sub-case 1 (workspace exists)**: run bootstrap to
  success, then re-run with same `<name>` → exit 1; stderr Detail
  contains `workspace \`<name>\` already exists at`; Suggestion
  contains both `niwa destroy <name>` and `niwa session create
  <repo> bootstrap`.

- [ ] **Conflict sub-case 2 (registry name in use)**: register name
  to a different root manually, then run bootstrap → exit 1; stderr
  Detail contains `workspace name \`<name>\` is already registered`;
  Suggestion contains `niwa destroy <name>`.

- [ ] **Conflict sub-case 3 (non-niwa file at target)**: `touch
  <cwd>/bar`, then run `niwa init bar --from owner/foo --bootstrap`
  → exit 1; stderr Detail contains `<absPath> already exists (file)`;
  Suggestion contains `Pick a different`.

### Rollback (R7)

- [ ] **Rollback at init step**: forced failure during init (e.g.,
  pre-existing target dir) → no `<cwd>/<name>/`, no registry entry,
  no instance.

- [ ] **Rollback at create step**: `tarballFakeServer` 200 for the
  config fetch but `localGitServer` returns clone failure → exit 1;
  prefix `bootstrap step=create:`; stderr contains the rollback note
  per the Notices table; `<cwd>/<name>/.niwa/workspace.toml` exists;
  no `<instanceName>/` directory; registry entry exists.

- [ ] **Rollback at session step**: create succeeds; session-create
  fails (e.g., daemon-spawn timeout via fault injection) → exit 1;
  prefix `bootstrap step=session-create:`; stderr contains the
  session-rollback note; instance and workspace both intact; no
  worktree.

### Scaffold + visibility invariants

- [ ] **Scaffold byte-equality**: parse the scaffolded
  `<cwd>/foo/.niwa/workspace.toml` and assert that it matches
  Appendix A's golden body literally (with `<placeholder>`
  substitutions for the workspace).

- [ ] **`.gitkeep` present**: `<cwd>/foo/.niwa/claude/.gitkeep`
  exists and is zero bytes (R15).

- [ ] **`[channels.mesh]` block active**: parsing the scaffold yields
  `Channels.Mesh != nil` and `Channels.IsEnabled() == true`.

- [ ] **Inline comment on `[channels.mesh]`**: the line preceding the
  `[channels.mesh]` block matches exactly: `# Bootstrap enabled mesh
  channels. Remove this block (and the [channels.mesh] line below)
  to disable.`

- [ ] **Visibility-from-bool with adversarial fixture**
  (`tarballFakeServer` `/repos/owner/foo` returns `{"private": true,
  "visibility": "public"}`): scaffold contains `[groups.private]
  visibility = "private"` and no `[groups.public]` block.

- [ ] **Visibility-from-bool with TOML-injection fixture**
  (`tarballFakeServer` returns `{"private": false, "visibility":
  "\"\n[evil]\nkey = \"x"}`): scaffold contains `[groups.public]`
  and no `[evil]` block.

- [ ] **Visibility-lookup soft-fail (server error)**
  (`tarballFakeServer` returns 500 for `/repos/`): scaffold contains
  `[groups.public]`; stderr contains the exact R17 note with
  `<cause>` = `server error`; bootstrap exits 0.

- [ ] **Visibility-lookup soft-fail (network error)** (close the
  fake server before bootstrap reaches the metadata endpoint):
  scaffold contains `[groups.public]`; stderr contains the exact R17
  note with `<cause>` = `network error`; bootstrap exits 0.

### Test-seam and invariant assertions

- [ ] **Host-check ordering at exec layer**: unit test with the
  injectable exec invoker (R22) — call `RunBootstrap` with non-GitHub
  `src.Host` → asserts (a) returned error matches R9, (b) recorder
  contains zero git invocations.

- [ ] **No-author / no-GIT_AUTHOR_* at argv layer**: unit test with
  the injectable exec invoker — happy-path flow runs to the commit
  step; the captured `*exec.Cmd` for the commit asserts (a)
  `cmd.Args` contains no element equal to `--author` or starting
  with `--author=`, (b) `cmd.Env` contains no entry whose key matches
  `^GIT_(AUTHOR\|COMMITTER)_(NAME\|EMAIL\|DATE)$`.

- [ ] **Cleanup-defer at create-fail**: unit test with the injectable
  exec invoker — force `git fetch` to fail during create's pipeline.
  After return, `<cwd>/<name>/.niwa/workspace.toml` exists; no
  `<instanceName>/`. (Asserts the workspace-dir defer flipped to
  off-after-init-success.)

- [ ] **Cleanup-defer at init-fail (preservation case)**: unit test
  forcing the init step to fail (e.g., target dir already exists).
  After return, `<cwd>/<name>/` does not exist; no instance; no
  registry write.

- [ ] **Classifier ordering**: table-driven test exercising error
  chains satisfying multiple arms simultaneously. For each row,
  assert the classifier picks the arm per N2's precedence list. A
  wrong implementation that reorders the `errors.As` switch must
  fail.

- [ ] **No-push assertion**: end-to-end test asserts that the
  injectable exec invoker's record contains no `git push`
  invocation across the happy path (R24).

- [ ] **Allow-list scoping**: `tarballFakeServer` configured with
  three repos in the source org (`foo`, `bar`, `baz`); bootstrap
  `--from owner/foo` → after success, `<instanceRoot>/<group>/`
  contains only `foo/`; `bar/` and `baz/` are absent (R4).

- [ ] **Branch-name stored in session state**: after a successful
  bootstrap, `<instanceRoot>/.niwa/sessions/<sid>.json` contains a
  `branch_name` field equal to `niwa-bootstrap/<sid>` (R5).

- [ ] **Branch-name back-compat fallback**: a session state file
  pre-dating the schema (no `branch_name` field) is still readable;
  callers that need the branch fall back to `session/<sid>` and the
  test asserts no panic, no error (R5).

- [ ] **`niwa session create` parity (R6)**: against a workspace
  produced by bootstrap, running `niwa session create my-project
  another-purpose` succeeds standalone with no re-initialization of
  state. (Demonstrates R6's "no new preconditions introduced.")

- [ ] **Worktree label in success block**: stderr contains the
  literal line `Worktree: <absolute-path>` where the path matches
  the value returned by `git worktree list --porcelain` after
  bootstrap.

- [ ] **Success block format**: stderr success block matches
  Appendix B's exact format (line ordering and exact-string
  comparison, ignoring `<placeholder>` runtime substitution).

- [ ] **R2 regression check** (no-flag baseline): `niwa init --from
  owner/foo` (no `--bootstrap`) against an *existing-config* repo
  continues to behave as today (materializes in cwd, uses cloned
  config's `[workspace] name`) — no regression introduced by the R2
  bootstrap-only derivation rule.

## Out of Scope

- **Future flag for niwa to open a PR on the user's behalf.**
  Deferred to a follow-up feature. The v1 branch-name format
  (`niwa-bootstrap/<sid>`) is chosen so this future flag can layer
  on without renaming branches retroactively.
- **Zero-commit (truly empty) remote disambiguation.** GitHub returns
  404 from the tarball endpoint for repos with no HEAD ref. v1
  surfaces the R11 message asking the user to push a first commit;
  no `repos/get` disambiguation is added.
- **Non-GitHub remotes.** Bootstrap is GitHub-only in v1 (R9, R21).
  `file://`, GitLab, Gitea, and self-hosted SSH remotes get the
  existing raw `git clone` stderr.
- **Workspace-level error sentinels.** v1 ships the typed
  `*github.StatusError` and per-class classifier messages only (N3).
- **Adopting an already-configured remote.** That's today's clone
  path and stays unchanged. Bootstrap fires only on
  `*config.NoMarkerError` plus explicit user intent.
- **Auto-resume after partial-failure.** R7's stepwise rollback
  gives the user real niwa commands to retry from. A
  bootstrap-internal transactional log is deferred.
- **Multi-group workspaces or per-repo overrides in the scaffold.**
  v1 emits one group block (visibility-derived) and one source block.
  Multi-group, `[repos.<name>]`, and explicit-repo filters are user
  edits after bootstrap.
- **Interactive scaffold customization.** Niwa proposes the scaffold
  defined in Appendix A non-interactively. The user edits the file
  before push.
- **`niwa apply` invocation.** Bootstrap stops after session-create
  per R26. The user invokes `niwa apply` later for drift checking.
- **Automatic push.** Per R24, niwa does not push the bootstrap
  branch.

## Decisions and Trade-offs

Seven decisions were made via `/shirabe:decision` during scoping
(Phase 1) and discovery (Phase 2). Each is summarized below with the
chosen option, the rejected alternatives, and the reasoning.

### Decision 1: Lifecycle framing (T1)

**Chose**: T1 — `--bootstrap` chains init → create → session-create
as one turnkey command. **Rejected**: T2 (init+prompt), T3 (init
only), T4 (init+create only). All reject reasons: failing to land
the user in a worktree leaves the worktree-visibility UX target
unmet; partial chains pay most of T1's cost without delivering its
payoff.

### Decision 2: Source-org pipeline scope (S2)

**Chose**: S2 — scaffold `[[sources]] repos = ["<bootstrap-repo>"]`
allow-list (Appendix A). **Rejected**: S1 (clone everything, fails
above DefaultMaxRepos), S3 (parallel clone path bypassing pipeline),
S4 (clone-count threshold prompt with non-deterministic behavior).

### Decision 3: Workspace-name derivation (N1)

**Chose**: N1 — derive from slug's repo basename. **Rejected**: N2
(require positional name), N3 (cwd basename), N4 (slug + TTY prompt
to override).

### Decision 4: Channels behavior (C1)

**Chose**: C1 — scaffold `[channels.mesh]` as active with the inline
comment from Appendix A. **Rejected**: C2 (ephemeral synthesis), C3
(refuse `--no-channels`), C4 (C1 + redundant stderr note).

### Decision 5: Branch name format (B3)

**Chose**: B3 — `niwa-bootstrap/<sid>` stored in session state with
empty-field fallback to `session/<sid>`. **Rejected**: B1 (keep
opaque `session/<sid>`), B2 (deterministic `niwa-bootstrap` with
collision risk), B4 (`niwa-bootstrap/<repo>` redundant with workspace
name).

### Decision 6: Stepwise rollback (R2)

**Chose**: R2 — stepwise rollback per failed step. **Rejected**: R1
(full rollback discards expensive state), R3 (no rollback leaves
mystery half-state), R4 (transactional log overengineered).

### Decision 7: Multi-run idempotency (I3)

**Chose**: I3 — bootstrap-aware preflight error messages naming
`niwa destroy <name>` and `niwa session create <repo> bootstrap`.
**Rejected**: I1 (inherit misleading remediation text), I2 (silent
auto-resume risks contaminated state), I4 (interactive destroy
prompt risks fat-finger wipe).

## Known Limitations

- **N1 latency target.** Bootstrap performs one shallow clone + one
  pipeline-restricted source clone + one session-create. Total wall
  time depends on the user's network and the bootstrap repo's size.
  v1 does not target a specific latency budget; expect the same
  shape as `git clone --depth 1` + `niwa session create` standalone.
- **Org membership requirement.** R4's allow-list approach requires
  the bootstrap repo to be in the same GitHub org as the
  `[[sources]] org` entry. For repos in forks or under a personal
  namespace, the user may need to edit the scaffold before pushing.
- **GitHub-only in v1.** Non-GitHub git providers must wait for a
  follow-up.
- **No auto-push.** The bootstrap commit lives locally until the
  user pushes. The R19 success block names the push command
  explicitly to mitigate.
- **Session-id branch suffix.** The branch name carries an
  8-hex-char suffix that has no meaning to a human reader. This is
  the cost of B3's collision-safety.

## Downstream Artifacts

To be populated after PRD acceptance:

- Design doc: `docs/designs/DESIGN-init-bootstrap-empty-source.md`
  (will be revised against this PRD)
- Plan doc: `docs/plans/PLAN-init-bootstrap-empty-source.md` (will
  be rebuilt against the revised design)

---

## Appendix A: Scaffold Template

When bootstrap succeeds, the scaffolded `.niwa/workspace.toml`
contains exactly this body, with `<placeholder>` tokens substituted
as described below.

```toml
[workspace]
name = "<workspace-name>"
content_dir = "claude"

[[sources]]
org = "<source-org>"
repos = ["<bootstrap-repo>"]

[groups.<vis-key>]
visibility = "<vis-value>"

# Bootstrap enabled mesh channels. Remove this block (and the [channels.mesh] line below) to disable.
[channels.mesh]

# CLAUDE.md content hierarchy: drop a workspace.md in .niwa/claude/ to populate.
# [claude.content.workspace]
# source = "workspace.md"

# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md
# for the full schema (claude.*, env.*, vault.*, files, instance).
```

Substitution rules:

| Token | Source | Notes |
|-------|--------|-------|
| `<workspace-name>` | Positional `<name>` arg, or slug repo basename per R2. | Must match `^[a-zA-Z0-9._-]+$` (today's `ValidateInitName`). |
| `<source-org>` | `<owner>` from the `--from` slug. | E.g., `--from owner/foo` → `owner`. |
| `<bootstrap-repo>` | `<repo>` from the `--from` slug. | E.g., `--from owner/foo` → `foo`. |
| `<vis-key>` | `private` when `Repo.Private == true`, else `public`. | Visibility lookup soft-fail (R17) → `public`. |
| `<vis-value>` | `"private"` or `"public"` matching `<vis-key>`. | Same source as `<vis-key>`. |

Section ordering, blank lines between sections, and comment lines
are part of the byte-equality contract for the happy-path AC. The
empty `.niwa/claude/.gitkeep` file (R15) is written alongside.

## Appendix B: Success Block Format

On full success, niwa writes this block to stderr, preceded and
followed by one blank stderr line:

```
Workspace bootstrapped at:    <absolute-workspace-root>
Instance:                     <absolute-instance-root>
Worktree:                     <absolute-worktree-path>
Branch:                       niwa-bootstrap/<sid>

Next steps:
  1. Inspect the scaffold:        git show HEAD
  2. Push the bootstrap branch:   git push -u origin niwa-bootstrap/<sid>
  3. Merge to the default branch, then run `niwa apply` to refresh.
```

Lines must appear in the order shown. The `Workspace bootstrapped
at:`, `Instance:`, `Worktree:`, and `Branch:` lines use column-aligned
values (the alignment column is byte position 30, padding with spaces).
The "Next steps" block uses two-space indentation and the numbered
prefix shown.
