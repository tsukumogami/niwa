# Phase 6 Security Re-Review: init-bootstrap-empty-source design

Scope: verify the three Phase 5 findings landed in
`docs/designs/DESIGN-init-bootstrap-empty-source.md` (Proposed, 1400
lines); re-check Security Considerations and the PRD's load-bearing
security invariants (R16 / R18 / R22 / R24 / N5).

## Verification of Phase 5 findings

### Finding 1: Host-check semantic bug — FIXED

Phase 5 flagged that `src.Host == "github.com"` would reject the
canonical `owner/repo` slug form because `source.Parse` leaves
`Host == ""` for slugs without an explicit host. The fix needed to land
across all host-check call sites.

Verified call sites that now reference `IsGitHub()` / `src.IsGitHub()`:

- Line 413: Decision Outcome (runInit's check) — "asserts `src.IsGitHub()` (R9, R21)"
- Lines 415–417: explicitly documents that `IsGitHub()` returns true for
  `Host == ""` OR `Host == "github.com"`, and explicitly warns that "A
  literal-byte `Host == "github.com"` check would reject the happy path."
- Line 658: docstring for `RunBootstrap` — "Host check: src.IsGitHub() must
  return true (handles both the canonical empty-Host slug form and explicit
  `github.com`)"
- Line 800: data flow diagram — `R9/R21 host check: !src.IsGitHub() → exit 3`
- Line 828: defense-in-depth re-check inside `RunBootstrap` — "host re-check
  (defense-in-depth; identical to runInit's check)"
- Lines 1177–1188 (Security Considerations §Invariants): names both layers
  (`runInit` + `RunBootstrap`), cites `internal/source/source.go:148`
  directly, and includes a preventive note against future
  "tighten the check" refactors.

Cross-check against `internal/source/source.go:148`: `IsGitHub()` body is
`return s.Host == "" || s.Host == DefaultHost`. The design's documented
semantics match the helper exactly.

**No residual literal-byte `Host == "github.com"` references remain in the
design body.** The R9 error string in the PRD still requires the substring
`got host=<host>`; the design does not respec the `<host>` substitution
rule when `Host == ""`, which is an implementation-level concern (the
implementer can substitute `github.com` or `""`; the existing PRD wording
admits either reading). Acceptable.

**Verdict: FIXED.**

### Finding 2: Commit-step rollback gap — FIXED

Phase 5 flagged that if `CreateSession` returns nil but the subsequent
`git add` / `git commit` in `RunBootstrap` fails, no defer covers the
worktree + branch + session JSON that `CreateSession` produced.

Verified at lines 925–935 (Cleanup defers § entry 4): a new layer 4 has
been added explicitly:

> **`RunBootstrap` (post-session-create cleanup):** a `sessionCreated`
> defer armed immediately after `CreateSession` returns success and
> disarmed only when the bootstrap commit succeeds. On any error
> between `CreateSession` returning and the commit succeeding (e.g.,
> scaffold write fails inside the worktree, `git add` fails,
> `git commit` fails), the defer calls a helper equivalent to
> `niwa session destroy --force <sid>` so the worktree, branch, and
> session state JSON are removed before `RunBootstrap` returns.

This matches the Phase 5 mitigation exactly. The defer is armed
post-`CreateSession`-success (so it can't double-fire with
`CreateSession`'s internal rollback) and disarmed post-commit (so the
happy path does not destroy the session it just created). The R7
session-step contract end-state — instance preserved, error message
points at `niwa session create <repo> bootstrap` for retry — is
preserved.

**Verdict: FIXED.**

### Finding 3: SIGKILL atomicity — DOCUMENTED

Phase 5 flagged that SIGKILL between sid placeholder reservation,
worktree-add, and state-JSON write leaves orphan state. Phase 5
suggested at minimum documenting the windows.

Verified at lines 940–968 (new §SIGKILL atomicity (operator concern)):
the design now enumerates the three sequential operations, names the
two crash windows ((1)↔(2) leaves zero-byte placeholder; (2)↔(3) leaves
orphan worktree + branch), and provides operator-facing mitigations:

- placeholder sids do not collide with subsequent invocations
- `niwa session destroy --force <sid>` removes both kinds of orphans
- a future `niwa session reap` is acknowledged as out-of-scope

The section explicitly notes "not a v1 acceptance criterion; documented
as a known operator concern" — appropriate scope-setting that matches
Phase 5's "documented" recommendation.

**Verdict: DOCUMENTED.**

## Re-evaluation of Security Considerations

The §Invariants inherited from PRD section (lines 1175–1216) accurately
reflects the fixed state:

- **R9/R21 host check** (line 1177): names both `runInit` and
  `RunBootstrap` layers, cites `IsGitHub()` from `source.go:148`,
  includes preventive comment about literal-byte tightening.
- **R16 visibility-from-bool** (line 1189): "Enforced structurally by
  `ScaffoldOptions.Private` being typed `bool`. The string `Visibility`
  field on `*Repo` is not accessed by `ScaffoldFromSource`'s code path.
  ... A future refactor introducing a string-derived visibility must
  modify the struct field type, which is a visible change." This is
  structural enforcement (typed field), not prose contract.
- **R18 no-author** (line 1196): commit step constructs `*exec.Cmd` via
  `GitInvoker.CommandContext(ctx, "commit", "-m", subject)` with no env
  modification; R22 recorder asserts `cmd.Args` contains no `--author`
  and `cmd.Env` contains no `GIT_*` overrides.
- **R22 exec.CommandContext** (line 1202): "Enforced by the `GitInvoker`
  interface contract: the only method returns `*exec.Cmd`. No shell, no
  string interpolation."
- **R24 no-push** (line 1207): zero `git push` calls; AC asserts the
  recorder records zero `git push` invocations.
- **N5 no-secrets-on-disk** (line 1211): scaffold TOML, instance state,
  registry entry, session state never receive the token.

The §New surface section (lines 1218–1260) catalogs four new surfaces
(GitInvoker, factored CreateSession, BranchName field exposure, two-phase
sid handshake) with mitigations for each.

**Note on Phase 5 wording gap not corrected**: Phase 5 flagged at item
2(b) that the design's §Negative bullet claims `workspace→mcp` is a new
import direction, but `internal/workspace/daemon.go` already imports
`internal/mcp`. The design's §Negative (line 1302) still says "the new
import adds a workspace→mcp edge." This is a factual-accuracy gap in
design prose, not a security issue. Out of scope for Phase 6.

## New security issues introduced by the fixes

### Concern A: `sessionCreated` defer ordering vs `CreateSession`'s internal rollback

The new layer 4 defer (line 925) is armed *after* `CreateSession`
returns success. The internal rollback at `handlers_session.go:270-278`
is described as covering "failures inside `CreateSession` itself"
(line 920). These two cleanup paths do not overlap by construction —
the new defer's arming point is the very point where the internal
rollback can no longer fire (the function has returned). **No race
introduced.**

### Concern B: `sessionCreated` defer + Go defer LIFO ordering

In `RunBootstrap`, the LIFO order of deferred cleanups matters. The
relevant deferred cleanups, listed by arming order, are:

1. `instanceCreated` defer — armed before `Applier.Create`, disarmed
   after create-step success.
2. `sessionCreated` defer — armed after `CreateSession` success,
   disarmed after commit success.

Since `instanceCreated` is disarmed before `sessionCreated` is armed
(the create step must succeed before the session step runs), there is
no possible state where both fire simultaneously on the same return
path. If the commit step fails, only `sessionCreated` fires (since
`instanceCreated` is already disarmed). The instance + workspace
survive — matching the R7 session-step contract. **No issue.**

### Concern C: `sessionCreated` defer interaction with SIGKILL

The new defer does not run on SIGKILL (Go defers don't fire on signal
kill). A SIGKILL between `CreateSession` returning and the bootstrap
commit completing therefore still leaves the orphan worktree + branch
+ state JSON the §SIGKILL atomicity section already discusses. The fix
for finding 2 (sessionCreated defer) and finding 3 (SIGKILL
documentation) are independent — the defer handles the controlled
error-return path; the operator-concern section handles the
uncontrolled signal-kill path. **Both fixes are needed and neither
duplicates the other.**

### Concern D: `niwa session destroy --force <sid>` reuse

The new defer (line 931) describes calling "a helper equivalent to
`niwa session destroy --force <sid>`." This is described as "a helper
equivalent to" rather than "calls the public CLI command" — appropriate
since `RunBootstrap` runs in-process and shelling out to itself would
be a code smell. The design does not name the concrete helper function,
which is a minor gap. Implementation-level concern; recommend the Phase
4 implementation thread a `workspace.DestroySession` (or equivalent
internal entry point) parallel to the existing `workspace.DestroyInstance`
reused at line 910. **Documentation gap, not a security bug.**

## PRD invariant cross-check

### R22: `exec.CommandContext` with separate args — `GitInvoker` interface contract

The design's `GitInvoker` interface (line 642):

```go
type GitInvoker interface {
    CommandContext(ctx context.Context, args ...string) *exec.Cmd
}
```

The method returns `*exec.Cmd` constructed via `exec.CommandContext`
with `args` as a variadic string slice — separate argv elements by
construction. A future implementer cannot add a string-template shortcut
without breaking the interface contract: the method signature does not
accept a single `cmdline string` and there is no overload mechanism in
Go. A future PR adding a `RunString(cmdline string)` method to
`GitInvoker` would be a visible interface change requiring all callers
to be updated.

**However**, a future implementer could write a `stdGitInvoker` variant
whose `CommandContext` method splits a string-formatted `args[0]` and
runs it via `sh -c`. Nothing in the type system prevents this. The
design's mitigation is the production-side single construction site at
`runInit` (line 1223–1228) plus the R22 AC asserting `cmd.Args`
contains no shell metacharacters. Combined, these close the practical
attack surface. **R22 contract preserved with structural + AC
enforcement.**

Recommendation (non-blocking): the §Security Considerations could
state explicitly that the `GitInvoker` contract requires the method
body to call `exec.CommandContext(ctx, "git", args...)` and forbids
shell interpretation. Today this is implicit from the doc comment at
line 645 ("Returns `exec.CommandContext(ctx, "git", args...)`. No
state.") — making it explicit-as-contract would strengthen the
invariant.

### N5: no secrets on disk

Audit of paths where `GH_TOKEN` could leak:

- **Scaffold body**: Scaffold derives from
  `ScaffoldOptions.{Name, SourceOrg, BootstrapRepo, Private}` (line 602).
  None of these fields can carry a token — `Name`/`SourceOrg`/
  `BootstrapRepo` are slug components, `Private` is a bool. ✓
- **Registry entry**: Registry writes only the workspace name +
  absolute path. ✓
- **Instance state**: Written by `Applier.Create`'s existing path —
  the design does not modify the schema. ✓
- **Session state**: New `BranchName` field carries
  `niwa-bootstrap/<sid>` only. No token path. ✓
- **Error messages**: The classifier at `init_classifier.go` produces
  Detail/Suggestion strings from R10/R11/R12/R13 fixed text plus the
  slug; no error-body propagation. ✓
- **`StatusError.Body`**: line 581 — `Body string  // truncated body,
  diagnostic-only`. The classifier reads `StatusCode`, not `Body`,
  per Decision D (line 411–416). However, the design does not include
  an explicit AC asserting `Body` never appears in user-visible output.
  Phase 5 raised this; Phase 6 confirms the gap was not closed in the
  design. The current state is "implicit-by-classifier" — the
  classifier matches on `StatusCode` and assembles its Detail from
  PRD-fixed text plus the slug. Acceptable in practice but the AC
  would harden it.
- **Git environment**: R18 explicitly forbids `GIT_AUTHOR_*` /
  `GIT_COMMITTER_*` overrides; the design's commit step does not add
  any token-bearing env to the subprocess. The existing `git fetch`
  path (per `internal/workspace/clone.go`) is the only token-bearing
  subprocess and it's not on the bootstrap commit code path. ✓
- **Git config**: Bootstrap does not write any `.gitconfig` entries.
  No `git config --local` calls in `RunBootstrap`'s described flow. ✓

**N5 verdict**: No secret-leak paths identified beyond the
`StatusError.Body` non-blocking gap.

### R16: visibility-from-bool structural enforcement

Phase 5 + Phase 6 confirm: `ScaffoldOptions.Private bool` (line 606)
is the structural type. The doc comment names the invariant:
`Private bool    // R16 invariant: bool only, never derived from a
// remote-controlled string`. The Security Considerations entry
(line 1189) explicitly states "A future refactor introducing a
string-derived visibility must modify the struct field type, which is a
visible change."

This is structurally enforced. A future PR that wanted to derive
visibility from `Repo.Visibility` (the API string field) would need
to either change `ScaffoldOptions.Private` to `string` (visible type
change) or read `Repo.Visibility` inside `ScaffoldFromSource` (which
would be visible in the function body and out of contract with the
struct's doc comment). Either change is a visible delta to a reviewer.

**R16 verdict**: structurally enforced via typed bool field.

## Open items not addressed by the fixes

These were Phase 5 "Option 2 — document considerations" items that the
fixes did not close. None are blockers; restate for the record:

1. **`StatusError.Body` propagation AC** — design does not include an
   AC asserting the classifier never reads `Body`. Acceptable; the
   classifier's implementation per Decision D consumes only
   `StatusCode`, but an explicit AC would prevent future drift.
2. **Local-git-hook trust inheritance** — the bootstrap commit step
   runs the user's local `core.hooksPath` / `~/.gitconfig` chain. Not
   a new surface (matches niwa's existing trust model) but unnamed in
   the design. Out of scope for v1.
3. **Workspace→mcp import direction wording** — design's §Negative
   bullet at line 1302 still claims this is a new edge; Phase 5 noted
   `internal/workspace/daemon.go` already imports `internal/mcp`.
   Factual-accuracy gap in design prose; non-security.

## Recommended Outcome

**APPROVE.**

The three Phase 5 critical findings have all landed:

1. Host-check semantic bug — FIXED via `src.IsGitHub()` at all sites,
   plus preventive documentation against future tightening.
2. Commit-step rollback gap — FIXED via the new layer-4 `sessionCreated`
   defer in `RunBootstrap` (lines 925–935).
3. SIGKILL atomicity — DOCUMENTED in the new §SIGKILL atomicity
   (operator concern) section (lines 940–968).

No new security issues were introduced by the fixes. The
`sessionCreated` defer's arming/disarming windows are non-overlapping
with `CreateSession`'s internal rollback (Concern A) and with the
`instanceCreated` defer (Concern B). The defer does not duplicate the
SIGKILL documentation (Concern C). The minor documentation gap on
the destroy-session helper name (Concern D) is implementation-level.

PRD invariants R16, R18, R22, R24, and N5 are all structurally
enforced or have AC coverage. The non-blocking items from Phase 5
(StatusError.Body AC, local-hook trust note, workspace→mcp wording
fix) remain open but are documentation-quality concerns, not security
blockers.

The design is ready to leave Proposed status from a security
perspective.
