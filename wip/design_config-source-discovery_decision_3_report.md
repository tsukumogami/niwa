<!-- decision:start id="rank-2-deprecation-notice-wiring" status="assumed" -->
### Decision: How the rank-2 deprecation notice (R14) wires into `DisclosedNotices`

**Context**

PRD R14 requires niwa to emit a one-time `note:`-prefixed deprecation
notice when discovery (Decision 1's probe) resolves a workspace's team
config OR overlay via rank 2 (root `workspace.toml` /
`workspace-overlay.toml`). The notice must contain the literal
substrings `deprecated`, the workspace name (apply context) or source
slug (init context), and `/shirabe:niwa-migrate-config`. It must fire
at most once per workspace per artifact per command-type via the
existing `DisclosedNotices` mechanism — the same machinery upstream
PRDs R18, R28, and R32 already use.

The `DisclosedNotices` mechanism is mature: three plain
package-level string constants
(`provider-shadow`, `channels-from-flag`,
`config-converted-to-snapshot`), an `opts.disclosedNotices` slice read
from the workspace-root state file at pipeline entry, a
`newDisclosures` slice populated during `runPipeline`, and a
best-effort `saveWorkspaceRootDisclosures` write at the end of
`Create`/`Apply`. Persistence failures cannot block apply (the save is
`_ = SaveState(...)`); notice emission already operates within the
"atomic snapshot integrity" envelope the constraint requires.

The structural question is where the rank-2 detection turns into a
notice. The probe runs inside the snapshot writer
(`materializeAndSwap` → `materializeFromGitHub` for GitHub sources,
or the shallow-clone fallback for non-GitHub sources). The notice
content needs the workspace name (apply) or source slug (init) AND an
artifact label (team config vs overlay). Those signals are NOT
available inside the snapshot writer — the writer is invoked
separately for the team config (in `Apply` line 338 and in the init
CLI) and for the overlay (in `runPipeline` line 593), and neither
invocation tells the writer which one it is. The pipeline / CLI
layers know both signals; the snapshot writer does not.

**Assumptions**

- Decision 1's probe signature exposes the rank to its caller (i.e.,
  returns `(subpath, rank, err)` or equivalent). The design summary
  for this work indicates the probe produces a ranked resolution, so
  this is the expected outcome. If Decision 1 lands a signature that
  hides the rank inside the snapshot writer, only Alternative B
  (snapshot-writer-emits) remains viable for this decision.
- The R14 wording "once per workspace per artifact per command-type"
  is an upper bound: the notice fires AT MOST once across both init
  and apply for a given workspace+artifact. The simpler reading
  matches how `DisclosedNotices` actually persists (one workspace-
  scoped state file shared across commands) and satisfies AC-N3
  trivially.
- Notice IDs use colons as separators (`rank2-deprecation:team-config`,
  `rank2-deprecation:overlay`). Colon is unambiguous against the three
  existing hyphen-only IDs and grep-friendly; substituting hyphens
  would be a trivial string change with no behaviour difference.

**Chosen: Alternative C — probe returns descriptor; a tiny
`disclosure.go` helper centralises notice rendering**

The probe (per Decision 1) returns the rank to its callers. The two
`MaterializeFromSource` / `EnsureConfigSnapshotWithStatus` entry
points each gain one new return value:

```go
func EnsureConfigSnapshotWithStatus(
    ctx context.Context, configDir string, fetcher FetchClient, reporter *Reporter,
) (converted bool, rank int, err error)

func MaterializeFromSource(
    ctx context.Context, src source.Source, sourceURL, configDir string,
    fetcher FetchClient, reporter *Reporter,
) (rank int, err error)
```

A new helper file `internal/workspace/disclosure.go` houses the
notice-rendering helper:

```go
const (
    NoticeRank2DeprecationTeamConfig = "rank2-deprecation:team-config"
    NoticeRank2DeprecationOverlay    = "rank2-deprecation:overlay"
)

// Rank2DeprecationNotice returns the (id, message) pair for a rank-2
// deprecation notice. artifact is "team-config" or "overlay";
// identifier is the workspace name (apply context) or the source slug
// (init context).
func Rank2DeprecationNotice(artifact, identifier string) (id, message string) {
    id = "rank2-deprecation:" + artifact
    message = fmt.Sprintf(
        "note: %s %q is using the deprecated rank-2 layout; "+
        "run /shirabe:niwa-migrate-config %s to migrate.",
        artifact, identifier, identifier,
    )
    return id, message
}
```

Three call sites consume the helper, matching the existing
notice-emission pattern in `apply.go` verbatim:

1. **Team config in `Apply`** (apply context):
   ```go
   _, rank, err := EnsureConfigSnapshotWithStatus(ctx, configDir, fetcher, a.Reporter)
   // ...
   if rank == 2 {
       id, msg := Rank2DeprecationNotice("team-config", cfg.Workspace.Name)
       if !sliceContains(wsDisclosedNotices, id) {
           a.Reporter.Log("%s", msg)
           result.disclosedNotices = append(result.disclosedNotices, id)
       }
   }
   ```

2. **Overlay in `runPipeline`** (apply context):
   ```go
   _, overlayRank, syncErr := EnsureConfigSnapshotWithStatus(ctx, a.GlobalConfigDir, fetcher, a.Reporter)
   // ...
   if overlayRank == 2 {
       id, msg := Rank2DeprecationNotice("overlay", cfg.Workspace.Name)
       if !sliceContains(opts.disclosedNotices, id) {
           a.Reporter.Log("%s", msg)
           newDisclosures = append(newDisclosures, id)
       }
   }
   ```

3. **`internal/cli/init.go`** (init context):
   ```go
   rank, err := workspace.MaterializeFromSource(ctx, src, source, niwaDir, fetcher, reporter)
   // ...
   if rank == 2 {
       id, msg := workspace.Rank2DeprecationNotice("team-config", source)
       reporter.Log("%s", msg)
       // Record in the init-time DisclosedNotices state so the first
       // niwa apply does not re-emit.
       initState.DisclosedNotices = append(initState.DisclosedNotices, id)
   }
   ```

Notice emission happens only after the snapshot has been promoted (in
the apply path, after `runPipeline` accumulates the disclosure into
`newDisclosures` which is persisted in `Create`/`Apply` via
`saveWorkspaceRootDisclosures`; in the init path, after
`MaterializeFromSource` returns success). A snapshot-promotion failure
yields no notice; a notice-persistence failure leaves the snapshot
intact and re-arms the notice for the next run — both desirable
properties.

**Rationale**

The decision rests on four observations from research:

1. **Atomic snapshot integrity is a stated constraint.** Alternative B
   (snapshot-writer-emits) violates it: the redirect-style emission at
   `snapshotwriter.go:337` happens BEFORE `SwapSnapshotAtomic` at line
   383, so a marker-write failure or extraction failure between emit
   and swap leaves a deprecation notice for a snapshot that never
   landed. Alternative C emits only after the pipeline / materialize
   call returns success, so the notice describes the on-disk state
   accurately.

2. **The probe should stay pure.** Decision 1's probe answers "which
   marker ranks resolved?". Adding a reporter dependency couples its
   unit tests to a fake-reporter setup that adds no signal to the
   resolution logic. Keeping it pure also lets the shirabe migration
   skill (R18) reuse the probe with its own reporting surface.

3. **Centralising message text matches the AC contract.** AC-N1
   through AC-N6 assert literal substring presence. Putting the
   message in one helper makes those substrings testable in one
   place; duplicating the message at three call sites (Alternative A)
   creates three places to keep the literals in sync.

4. **Minimal divergence from the existing pattern.** Alternatives A
   and C both extend the `DisclosedNotices` pattern verbatim — same
   `sliceContains` + `reporter.Log` + `append(newDisclosures, id)`
   sequence used by the three existing notices. C just lifts the
   message string into a helper.

**Alternatives Considered**

- **Alternative A — probe returns descriptor; pipeline-level branching
  duplicates message at 3 sites.** Same wiring as C without the helper.
  Rejected because the message string would be duplicated at three
  call sites (team-config apply, overlay apply, init CLI), creating
  three places to keep the AC-required literal substrings in sync
  and giving up the standalone helper unit-test surface. The marginal
  cost of the helper (one small file, one function) is more than
  recovered by the test surface and the single source of truth for
  message text.

- **Alternative B — probe emits notices directly via reporter; snapshot
  writer learns artifact + context.** Mirrors the R18 rename-redirect
  precedent. Rejected for two reasons: (1) violates atomic snapshot
  integrity by emitting before `SwapSnapshotAtomic` can complete the
  promotion, (2) pushes workspace-name and artifact-identity context
  into the snapshot writer which today has neither and gains no other
  benefit from learning them. The R18 precedent does not extend
  because the redirect message is context-free (no workspace name,
  no artifact label); R14's message is not.

**Consequences**

What changes:

- One new file: `internal/workspace/disclosure.go`, with two
  package-level notice ID constants and one `Rank2DeprecationNotice`
  helper function.
- Two existing public functions
  (`EnsureConfigSnapshotWithStatus`, `MaterializeFromSource`) each
  gain one new return value carrying the resolved rank. Callers in
  `apply.go` (lines 338, 593), `init.go` (line 248), and
  `config_set.go` (line 70) update to capture it.
- Three call sites add the standard `if rank == 2 { ... }` block
  matching the existing notice-emission pattern.
- The init CLI gains a notice-recording step that writes the notice
  ID into `initState.DisclosedNotices` before
  `runPipeline` reads it on the first apply, ensuring the apply
  context does not re-emit. (This works because `Create` already
  reads `initState.DisclosedNotices` at line 259.)
- `EnsureConfigSnapshot` (the no-status wrapper at line 60) either
  gains the rank return as well or ignores it via `_, _, err := ...`
  in its one-line body.

What becomes easier:

- Unit-testing AC-N1 through AC-N6's literal substring requirements
  becomes a single direct call to `Rank2DeprecationNotice` with
  golden-string assertions, in addition to the functional tests on
  full stderr.
- Adjusting the message text (substring tweaks, wording changes) is a
  one-place edit.
- The shirabe migration skill can call the same probe code from R18's
  read-mostly probe step without inheriting a notice-emission
  responsibility.

What becomes harder:

- Adding a third artifact in the future (e.g., a global-config rank-2
  notice) requires extending the helper rather than copying a
  message block, which is a small cost but real.

What stays the same:

- `DisclosedNotices` schema, persistence path, layout-detection
  heuristic in `saveWorkspaceRootDisclosures` — all unchanged.
- The three existing notice IDs and their emit sites — untouched.
- The snapshot writer's reporter-emission for the R18 rename
  redirect — untouched.
- AC-N1 through AC-N6 stderr substring contracts — satisfied by the
  helper's output.
<!-- decision:end -->
</content>
</invoke>