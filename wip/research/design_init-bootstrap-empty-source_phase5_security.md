# Security Review (Phase 5): init-bootstrap-empty-source design

Scope: `docs/designs/DESIGN-init-bootstrap-empty-source.md` (Proposed, 1342 lines).
PRD: `docs/prds/PRD-init-bootstrap-empty-source.md` (Accepted).
Reviewer cross-reads against `internal/cli/init.go`, `internal/github/{fetch,client}.go`,
`internal/workspace/{clone,snapshotwriter}.go`, `internal/mcp/{session_lifecycle,handlers_session}.go`,
`internal/cli/prompt.go`, `internal/source/source.go`.

## Dimension Analysis

### 1. External artifact handling
**Applies**: Yes

Bootstrap takes the following external inputs:

- `--from <slug>` parsed via `parseInitSource` → `source.Parse` → `*source.Source`.
  Slug grammar is strict; `internal/source/parse.go` validates host shape via
  `containsDot`, rejects whitespace, multiple separators, empty owner/repo,
  unexpected path segments.
- `--bootstrap`, `--no-bootstrap` (bool flags; mutual exclusion at runInit).
- Positional `<name>` validated by `workspace.ValidateInitName`.
- `GH_TOKEN` / `GITHUB_TOKEN` / `gh auth token` → only used as HTTP Authorization
  bearer and subprocess env for the existing materialize git fetch (not the
  bootstrap commit step, per R18).
- GitHub API responses: tarball body (extracted), `/repos/{owner}/{repo}` JSON.
  Tarball is extracted by `github.Extract*` (existing). The new `GetRepo` reads
  only the `Private` bool — Decision §R16 closes the TOML-injection vector by
  refusing to read the remote-controlled `Visibility` string.

Two artifact-handling validation strengths:

- The R16 invariant is structurally enforced: `ScaffoldOptions.Private` is typed
  `bool`. A future refactor that pivoted to the string field would be a visible
  type change.
- The `BranchPrefix` (Decision C/§Two-phase handshake) is constructed at the
  caller, not parsed from any remote source.

One concern: tarball body bytes flow into `git add` inside the worktree without
an additional integrity check beyond what `EnsureConfigSnapshot` already
performs. Bootstrap's add-and-commit step in step 9 of the data flow operates
on files newly written by the bootstrap orchestrator into `<workspaceRoot>/.niwa/`
(scaffold output). The Applier.Create step is responsible for `[[sources]]`
clones into `<instanceRoot>/<group>/<repo>/`, but the bootstrap commit only adds
`.niwa/` content the orchestrator itself generated. So the commit's blob content
is locally-produced, not remote-controlled — good.

### 2. Permission scope
**Applies**: Yes

New filesystem writes introduced by bootstrap:

- `<workspaceRoot>/.niwa/workspace.toml` (0644 implied by Scaffold).
- `<workspaceRoot>/.niwa/claude/.gitkeep` (zero-byte).
- `<workspaceRoot>/<instanceName>/...` via Applier.Create (existing surface; not new).
- `<instanceRoot>/.niwa/sessions/<sid>.json` with the new `branch_name` field —
  WriteSessionLifecycleState writes mode 0600 (already correct).
- `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` via `git worktree add` (existing).
- Landing-path file via `writeLandingPath` (existing; the file niwa session create
  writes today).

No new privilege paths. No setuid, no chroot, no sudo invocation. No new network
ports. No new daemon spawn (the existing per-worktree daemon spawn is reused).

The orchestrator runs entirely in-process. No new IPC surface. The factored
`mcp.CreateSession` is a package-level Go function call from
`internal/workspace/bootstrap.go`; not a network endpoint.

### 3. Supply chain / dependency trust
**Applies**: Yes — and this is the most material finding.

Bootstrap delegates to `Applier.Create`, which runs `runPipeline`, which uses
`Cloner.Clone` (`internal/workspace/clone.go:63`) to invoke
`exec.CommandContext(ctx, "git", "clone", ...)`. The cloned repo is the
user-named bootstrap repo (R4 allow-list scoped to a single repo). After the
clone lands at `<instanceRoot>/<group>/<repo>/.git/`, the subsequent
`git worktree add` (handlers_session.go:230) operates against that repo,
and the subsequent `git add .niwa/` + `git commit` operate inside the worktree.

**Git hook execution:** `git clone` itself does NOT execute hooks from the
remote — by default git only honors hooks under `<repo>/.git/hooks/`, which
the user controls locally. However, git's templated hooks dir
(`init.templatedir` or `core.hooksPath` set globally on the user's machine)
would be applied to the new clone's `.git/hooks/` directory. Bootstrap inherits
the user's shell environment unchanged — there is no defensive `GIT_CONFIG_NOSYSTEM`,
no `--config core.hooksPath=/dev/null`, no `--no-hardlinks`. This matches niwa's
existing `Cloner.Clone` behavior. The bootstrap design doesn't broaden the
hook surface beyond what `niwa create` already trusts.

**Notable**: the subsequent `git commit -m "Initial niwa workspace config"`
inside the worktree (step 9 of the bootstrap data flow) WILL trigger the
local `pre-commit`, `commit-msg`, `post-commit` hooks if they exist in the
freshly-cloned repo. For an empty-with-README remote (the canonical bootstrap
target), there are no `.git/hooks/*.sample` files renamed to active hooks, so
this is moot in v1. But for an adversarial empty-repo target that ships
`.githooks/` checked-in plus a `core.hooksPath = .githooks` pre-bootstrap
config in `~/.gitconfig`, hooks WOULD run. Out of scope: the user already
owns their `~/.gitconfig`.

**Recommendation**: add a note in Security Considerations §Inherited that the
bootstrap commit step runs whatever local-git-config hook chain the user
already has configured; this is consistent with niwa's existing trust model
but worth naming.

### 4. Data exposure
**Applies**: Yes

- `GH_TOKEN`: resolved via `resolveGitHubToken` (token.go) — env first, then
  `gh auth token`. The token is passed as `req.Header.Set("Authorization",
  "Bearer "+c.Token)` in `applyAuth`; and as the existing git-fetch subprocess
  env (matching niwa's existing pattern). The scaffold writer NEVER receives
  the token — confirmed via PRD N5 + the design's invariant. The recursive-grep
  AC in Phase 5 validates this with a known fixture token.

  **Concern**: `*github.StatusError` carries `Body string` (truncated, diagnostic-only)
  per the design's errors.go sketch. If GitHub returns a 401/403 with a body
  containing the rate-limit headers or an `X-GitHub-Request-Id`, that's fine.
  But if a self-hosted `NIWA_GITHUB_API_URL` server returned a 4xx body that
  echoed back the `Authorization` header (unlikely but possible for a misbehaving
  proxy), the body could land in the user-visible Detail. The design's
  classifier formats Detail from `r.StatusCode + URL`, not body, so the
  exposure is bounded — but the StatusError struct's `Body` field should be
  flagged as "diagnostic-only, never propagated to stderr Detail."

- Scaffold file contents: from `ScaffoldOptions.{Name,SourceOrg,BootstrapRepo,Private}`.
  All four are local-derived (`Name` from positional arg or src.Repo,
  SourceOrg/BootstrapRepo from the parsed slug, Private from `GetRepo.Private`).
  No remote-controlled string lands in the scaffold body, per R16.

- Success message (R19): names workspace root, instance root, branch, worktree
  path, registry entry. All paths are local; the branch name is
  `niwa-bootstrap/<sid>` where `<sid>` is the locally-generated 8-hex
  identifier. No secrets in the success block.

- The R17 stderr note discloses the failure cause ("network error" /
  "authentication" / "not found" / "server error"). Pre-classified; doesn't
  echo response body. Good.

### 5. Confused deputy / TTY interactions
**Applies**: Yes

- R13 prompt: `ReadConfirmation(prompt, expected, in, out)` (prompt.go:37)
  reads a single line and compares against `expected`. Bootstrap uses
  `IsStdinTTY()` (prompt.go:21) — `term.IsTerminal(int(os.Stdin.Fd()))` — to
  gate the prompt. Non-TTY no-flag exits with R13 fail-fast; non-TTY with
  `--bootstrap` proceeds without prompting. This closes the piped-stdin attack
  vector where an automated agent could be tricked into accepting a Y by
  piping `echo y |`. Design upholds this: line 798-800 sets
  `hasBootstrap = initBootstrap || (IsStdinTTY() && !initNoBootstrap && promptUserYesDefault())`.

- The prompt itself is TTY-only — but the prompt's text is fully ASCII, no
  ANSI escapes that could be smuggled via the slug. The slug values are bound
  to `Detail` text formatting (e.g., R10/R11 substrings include the slug), but
  those go to stderr Fprintf with no shell interpretation. Safe.

- `cmd.OutOrStdout()` vs `cmd.ErrOrStderr()` separation is consistent: progress
  on stdout, prompt and errors on stderr (per PRD §Stdout vs Stderr).

### 6. Command injection in git invocations
**Applies**: Yes

- `GitInvoker.CommandContext(ctx, args ...string)` returns `*exec.Cmd` with
  separate argv. No shell. The interface signature precludes string-format
  interpolation. The adversarial-slug AC (`owner/foo;rm -rf /tmp/x`) validates
  the parser rejects this shape at `source.Parse` time before any git call.

- Existing pattern in clone.go:63 already uses `exec.CommandContext` with
  separate argv. The handlers_session.go calls (lines 230, 237, 273) currently
  use `exec.Command` (no Context) — the design's R22 + factored CreateSession
  brings these onto `exec.CommandContext` via GitInvoker. Net improvement.

- `repoPath` (handlers_session.go:216 via `findRepoInWorkspace`) is resolved
  from a known instance layout, not user-supplied at the MCP level. For the
  bootstrap caller, `repoPath` is `<instanceRoot>/<group>/<repo>/` — paths
  computed from validated identifiers. Safe.

- `branchName = "niwa-bootstrap/" + sid` where `sid` matches `[0-9a-f]{8}`.
  No shell metacharacter can appear.

- Worktree path: `<instanceRoot>/.niwa/worktrees/<repo>-<sid>`. `<repo>` is
  the user-supplied repo basename from the slug; `parse.go` already rejects
  shell metacharacters via the strict slug grammar. Safe.

### 7. Race conditions
**Applies**: Yes — and this is the second material finding.

Three TOCTOU windows in the bootstrap orchestrator:

(a) **Workspace dir creation defer race.** `runInit` calls `os.Mkdir` (NOT
MkdirAll) on `workspaceRoot` and arms `workspaceCreated` defer
(init.go:215-226). On RunBootstrap success the defer is disarmed. If
RunBootstrap blocks (e.g., daemon spawn timeout) and the process receives
SIGTERM, the defer wouldn't fire (defers don't run on signal kill). A SIGKILL
mid-RunBootstrap leaves the workspace dir on disk and the instance dir on
disk. Existing behavior — bootstrap doesn't add a new race.

(b) **Two-phase sid handshake (author-flagged Item 3).** Detail under
Author-Flagged Items below.

(c) **`os.Mkdir` symlink-TOCTOU.** init.go:209 comment already notes: "os.Mkdir,
NOT MkdirAll — closes the symlink-TOCTOU window between the Lstat pre-gate and
creation." Bootstrap inherits this protection. Defense already in place.

No new sentinel-file race introduced. The sid placeholder (`ReserveID` /
O_EXCL) is atomic at the kernel-syscall level for the placeholder reservation.

### 8. Per-step rollback contract
**Applies**: Yes

The design's three-layer rollback:

1. `runInit` workspaceCreated defer (init step) — pre-existing, well-tested.
2. RunBootstrap instanceCreated defer (create step) — new. Calls
   `workspace.DestroyInstance(workspaceRoot, instanceName)` (per design
   line 904). Daemon shutdown follows the existing R7 5s SIGTERM → SIGKILL
   ladder; shutdown timeout doesn't block instance removal.
3. CreateSession's own rollback (session step) — pre-existing at
   handlers_session.go:270-278; covers worktree, state JSON, branch.

**Concern**: failure modes that could leave partial state:

- If RunBootstrap step 4 (CreateSession) succeeds but step 5 (commit) fails,
  who cleans up the worktree + state JSON? The design implies that
  CreateSession's rollback handles it, but CreateSession returns success
  before step 5 runs. The commit is bootstrap-owned, not CreateSession-owned.
  Step 5 failure currently has no defer to roll back the session-step
  artifacts that succeeded. The design should specify: either (a) the
  commit step is moved INSIDE CreateSession so its rollback covers it, or
  (b) RunBootstrap adds a sessionCreated defer that mirrors the
  CreateSession rollback.

- Secrets/partial state on commit-step failure: the worktree contains
  `.niwa/workspace.toml` + `.gitkeep` from the scaffold copy step
  (step 5 of the design's data flow places the scaffold at workspaceRoot,
  not the worktree; the bootstrap commit step adds those files inside the
  worktree via `git add .niwa/`). So a commit-step failure leaves the
  worktree with un-committed scaffold files. No secrets in those files
  (R16 + N5).

This is a design clarity gap. Recommended documentation, not a hard
security finding.

## Author-Flagged Items

### Item 1: `GitInvoker` interface exported scope
**Risk**: Low
**Analysis**: The design exports `workspace.GitInvoker` so that
(a) tests in the workspace package + adjacent packages construct a
recorder, and (b) `mcp.CreateSessionParams.GitInvoker` accepts the type
via cross-package reference. The interface has exactly one method
(`CommandContext(ctx, args...)`) returning `*exec.Cmd` — no surface for
state mutation, no constructor that accepts dangerous parameters. A
malicious caller passing a `GitInvoker` that runs `rm -rf` instead of git
would need to be inside the niwa binary (post-build, in-process); at that
point the attacker already has full execution and a fake GitInvoker is
the least of the user's worries.

The "future caller could construct a malicious GitInvoker" framing in the
design (line 1163) overstates the risk. The interface IS the seam; that's
its point. Production has exactly one constructor site (`runInit`).

**Mitigation**: The design's existing mitigation (single production
construction site + optional static-analysis assertion) is adequate. One
small improvement: consider making `stdGitInvoker` unexported (it already is
in the design sketch — lowercase `stdGitInvoker`). The `GitInvoker`
interface itself MUST stay exported because `mcp.CreateSessionParams`
references it across packages. Cannot unexport without restructuring.

### Item 2: `mcp.CreateSession` callability from `internal/workspace`
**Risk**: Low (with a note)
**Analysis**: The factoring exposes `mcp.CreateSession` as a
package-level function. Today's `handleCreateSession` is method-bound to
`*Server` and gated by the MCP server's tool-call dispatch. The new
caller (`internal/workspace/bootstrap.go`) bypasses MCP's outer transport
layer.

Three observations:

(a) The MCP server's transport layer is JSON-RPC over stdio (in-process or
spawned). It does not perform authentication or rate-limiting today — it
trusts the local daemon's stdio peer. So "bypassing MCP authentication"
is moot; there is none to bypass.

(b) The design's claim of "workspace→mcp is a new import direction"
(line 1247) is wrong. `internal/workspace/daemon.go` already imports
`internal/mcp`. The cross-package edge is established.

(c) The Mitigation suggestion ("workspace-package interface satisfied by
an mcp adapter") would invert the direction back to mcp→workspace,
which is cleaner in terms of layer boundaries but adds a level of
indirection for no actual security gain (since there's no authentication
gate to preserve).

**Mitigation**: Document considerations — no change. The
unit-test-asserts-both-sites mitigation already specified is sufficient.

### Item 3: Two-phase sid handshake atomicity
**Risk**: Medium
**Analysis**: The handshake order in the design's §Two-phase sid handshake
section:

1. Reserve sid via `newSessionLifecycleID` → O_EXCL placeholder
   `<sid>.json` at `<sessionsDir>/<sid>.json`.
2. Compute `branchName = "niwa-bootstrap/" + sid`.
3. `git worktree add worktreePath -b branchName`.
4. `scaffoldWorktreeNiwa(worktreePath, repo)`.
5. `WriteSessionLifecycleState` (atomic rename over placeholder).
6. Start daemon. On daemon spawn timeout, rollback: worktree remove,
   state-JSON delete, branch -D.

Crash points:

- **Crash between (1) and (3)**: a placeholder file sits at
  `<sessionsDir>/<sid>.json` with no worktree or branch. The existing
  scan-and-clean ListSessionLifecycleStates skips JSON-unparseable files
  (logs and continues at session_lifecycle.go:118). The placeholder file
  is JSON-empty or zero-byte from O_EXCL creation, which IS JSON-unparseable.
  So future `niwa session list` calls will log+skip. **However**, the
  placeholder file is never garbage-collected. Over many crashes the
  sessions directory accumulates orphan placeholders. This is a
  hygiene problem, not a security one — disk-space DoS through repeated
  crashes is theoretical. **Recommendation**: add a janitor that sweeps
  zero-byte session placeholders older than 1 hour, OR have RunBootstrap's
  defer explicitly delete the placeholder on early-exit paths.

- **Crash between (3) and (5)**: worktree on disk, branch on disk, NO
  state JSON (the placeholder is still zero-byte until WriteSessionLifecycleState's
  rename). On next niwa command, the worktree is "orphan" — listed by
  `git worktree list` but not by niwa. This IS a partial state with
  cleanup ambiguity. The existing CreateSession rollback at lines 270-278
  only fires inside the function; a process crash mid-execution doesn't
  hit it. **Recommendation**: the design should specify how subsequent
  `niwa session list` or `niwa destroy` discovers and reconciles orphan
  worktrees. The existing PRD R7 contract assumes the orchestrator runs
  to completion or returns an error; it doesn't cover SIGKILL mid-execution.

- **Crash between (5) and (6)**: state JSON committed, worktree + branch on
  disk, no daemon. Existing niwa code handles this case: `niwa session
  list` projects the daemon health as "not running"; user can run
  `niwa session resume` or `niwa session destroy`.

**Mitigation**: Document considerations + small implementation change.
Add to Security Considerations: (i) crash-mid-handshake leaves recoverable
orphan worktrees that `niwa session destroy` cleans up; (ii) the
zero-byte placeholder file is a known sweep target — add a janitor or
periodic sweep. The design's Mitigation §Sid handshake atomicity
mentions a focused unit test exercising crash points; this should
explicitly include a verification that orphan placeholders don't accumulate.

### Item 4: `BranchName` field exposure via MCP responses
**Risk**: Low (no new disclosure)
**Analysis**: `sessionListEntry` (handlers_session.go:36) embeds
`SessionLifecycleState` and adds a computed `Daemon` field. Marshaling
`sessionListEntry` to JSON includes every JSON-tagged field of the
embedded state, including the new `BranchName` (tag
`branch_name,omitempty`).

A `niwa session list` consumer that today sees `session_id`,
`worktree_path`, `repo`, `purpose` will also see `branch_name:
"niwa-bootstrap/<sid>"` on bootstrap-created sessions. The branch name
encodes `<sid>` (already disclosed) and the literal prefix
`niwa-bootstrap/` (a niwa internal namespace, not a secret). No new
PII, no token, no path beyond what's already disclosed.

A subtle disclosure: the branch name's prefix reveals the SESSION was
created by bootstrap rather than by `niwa session create`. For a public
project where the branch is later pushed, the branch name appears in
`git log` and on the remote. The PRD §N4 already pins this as a durable
user-facing contract.

**Mitigation**: No change. Document considerations: the field is added
to a response shape that already discloses session_id and worktree_path;
no new threat vector.

### Item 5: Defense-in-depth host check inside `RunBootstrap`
**Risk**: Medium (semantic gap)
**Analysis**: The design states (lines 822, 1129) that RunBootstrap
re-checks `src.Host` against the literal byte string `"github.com"` with
no normalization. The PRD R9 string is exact: `bootstrap supports only
GitHub sources in v1; got host=<host>`.

**Critical finding**: `source.Parse` (internal/source/parse.go:99-105)
leaves `Source.Host` EMPTY for the most common slug shape `owner/repo`
(no host segment in the slug). Only `host.tld/owner/repo` shapes set
`Source.Host` to a non-empty value. The existing helper
`Source.IsGitHub()` returns true when `Host == "" || Host == "github.com"`.

If `RunBootstrap` performs a literal `src.Host == "github.com"`
comparison, then the canonical input `niwa init my-project --from
owner/my-project --bootstrap` (host omitted, owner/repo only) would
FAIL the check — because `src.Host == ""`, not `"github.com"`. This
contradicts the PRD's happy-path AC (line 458: "Happy path with
positional name ... `niwa init my-project --from owner/my-project
--bootstrap`").

The design's literal-byte-comparison spec is therefore semantically wrong.
The correct check is either:

(a) `src.IsGitHub()` (treats empty as github.com — matches every other
niwa caller's behavior); OR

(b) Reject `src.Host != "" && src.Host != "github.com"` (only fail when
an explicit non-GitHub host is provided).

Both formulations bypass the "literal-byte against `github.com`" wording
the design author asked us to confirm.

**Mitigation**: **Design change needed**. Update the design's R9/R21
spec to: "the check accepts `Source.Host == ""` or `Source.Host ==
"github.com"`, matching `Source.IsGitHub()`. No other normalization
(no case-folding, no IDN, no alias resolution) is performed." Update
the error string substitution rule: when `Host == ""`, the R9 error
message's `<host>` placeholder uses the literal token `github.com` (or
the implementer's choice; specify in design).

The runInit-layer check at line 794 has the same semantic issue. Both
sites must align.

## Recommended Outcome

**Option 1 — Design changes needed**:

1. **Critical: fix the host-check spec** (Item 5). The literal-byte
   comparison against `"github.com"` rejects the most common slug shape
   `owner/repo` where `Source.Host == ""`. Change both `runInit` and
   `RunBootstrap` host checks to accept `Host == "" || Host == "github.com"`
   (i.e., `Source.IsGitHub()`). Specify the `<host>` substitution rule
   for the empty-host case in the R9 error string.

2. **Specify commit-step rollback ownership** (Dimension 8). If
   CreateSession returns nil but the subsequent `git add` / `git commit`
   in RunBootstrap fails, document who removes the worktree + branch +
   session JSON. Either move the commit inside CreateSession (cleaner)
   or add a sessionCreated defer in RunBootstrap that mirrors
   CreateSession's existing rollback.

**Option 2 — Document considerations** (add to design's §Security
Considerations):

3. **Orphan placeholder hygiene** (Item 3): note that crash-mid-handshake
   between sid reservation and worktree creation leaves a zero-byte
   placeholder file; specify either a sweep or an explicit defer-driven
   cleanup. The focused unit test in Mitigation §Sid handshake
   atomicity should assert no orphan accumulates.

4. **Orphan worktree reconciliation** (Item 3, crash between worktree-add
   and state-JSON write): document that a SIGKILL'd run leaves a worktree
   `git worktree list` knows about but niwa doesn't; specify the
   recovery path (existing `niwa session destroy` semantics).

5. **`StatusError.Body` propagation contract** (Dimension 4): explicitly
   state that the `Body` field is diagnostic-only and never appears in
   the user-visible Detail. Add an AC asserting the classifier never
   reads `Body`.

6. **Local-git-hook trust inheritance** (Dimension 3): name explicitly that
   `git commit` inside the bootstrap worktree honors the user's local
   `core.hooksPath` / `~/.gitconfig` — this is consistent with niwa's
   existing trust model but should be documented as an inherited
   assumption rather than a new surface.

7. **Workspace→mcp import direction wording fix** (Item 2): the design's
   §Negative bullet (line 1247) claims this is a new edge; it isn't —
   `internal/workspace/daemon.go` already imports `internal/mcp`.

## Summary

The design is mostly secure-by-construction: existing pattern reuse
(`exec.CommandContext`, structured `Source` parser, O_EXCL session ID
reservation, atomic rename writes, structurally-typed `Private bool`
visibility) closes most attack vectors. The `GitInvoker` interface and
the factored `CreateSession` entry point are appropriate test seams,
not security surfaces. One critical correctness issue: the design's
literal-byte host-check spec contradicts the PRD happy-path AC, since
`Source.Host` is empty for the common `owner/repo` slug shape — this
must be fixed before implementation. Two medium-risk gaps (commit-step
rollback ownership; orphan placeholder hygiene) warrant either small
design-document additions or focused unit tests; none rise to a hard
blocker beyond the host-check fix.
