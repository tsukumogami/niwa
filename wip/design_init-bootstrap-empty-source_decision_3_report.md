<!-- decision:start id="adjacent-failure-classification-scope" status="assumed" -->
### Decision: Scope of adjacent failure-mode classification in v1

**Context**

Today every error from `MaterializeFromSource` surfaces through the same
wrap at `internal/cli/init.go:266`: `"Error: materializing config repo:
<underlying>"`. Exploration's `lead-other-failures.md` mapped seven
failure modes (A-G) and proposed a typed-error refactor across
`internal/workspace/preflight.go` (three new sentinels:
`ErrSourceConfigMalformed`, `ErrSourceAuthFailed`, `ErrSourceNotFound`)
and `internal/github/fetch.go` (typed `*StatusError` replacing the
string-formatted status errors). Init would then dispatch via
`errors.As` and emit `InitConflictError`-style Detail + Suggestion lines
per class.

This decision determines what slice of that refactor v1 actually ships.
A naive scoping ("everything in exploration") risks over-engineering
adjacent polish for cases whose underlying error is already
self-describing (parse errors from `config.Load` already cite line and
column; the auth path at `fetch.go:142-145` already includes the
`GH_TOKEN` scope hint). A minimal scoping ("nothing beyond the
bootstrap path's `NoMarkerError` discrimination") risks leaving the 404
case — which today reads as a bare `"FetchTarball returned 404"` — with
no remediation pointer, which is exactly the case Decision 2 needs to
handle for the user's "I just created the repo" scenario.

Decision 2 ("v1 handling of zero-commit remotes") has already committed
to two pieces of this refactor as part of v1: (1) the typed
`*github.StatusError` in `internal/github/fetch.go`, and (2) the
`errors.As`-based classifier at the `runInit` seam covering at least
404 and 401/403. Decision 3 inherits those as a floor and decides how
much further to go.

**Assumptions**

- Decision 2's commitment to `*github.StatusError` plus the 404/auth
  classifier is binding. Decision 3 cannot ship a scope smaller than
  what Decision 2 already assumes, or Decision 2's chosen flow breaks.
- Production callers of `internal/github/fetch.go` consume the
  `statusCode int` return value directly, not the error text. The only
  string-matching consumers are test fakes in
  `internal/workspace/snapshotwriter_test.go` (four sites). Refactoring
  to a typed `StatusError` whose `Error()` preserves today's text is
  additive for production and requires updating those four fakes.
- The existing `config.NoMarkerError` and `config.AmbiguousMarkersError`
  types plus their `IsNoMarker` / `IsAmbiguousMarkers` predicates remain
  the dispatch shape for "probe ran, no markers / ambiguous markers."
  v1 does not need to wrap these in workspace-level sentinels — they
  carry their own user-actionable text.
- Non-GitHub remotes (`file://`, GitLab, Gitea) surface auth and
  not-found through raw `git clone` stderr at `fallback.go:151`. No
  Decision-2-class user-visible value comes from classifying those in
  v1; users hitting them are using less-supported paths and the raw
  stderr is at least diagnostic.
- The `internal/config/config.Load` post-flight at `init.go:288-291`
  emits an underlying TOML error that already names line + column. The
  marginal value of wrapping it in a workspace-level
  `ErrSourceConfigMalformed` sentinel is low; the parse error is
  effectively self-classifying for an audience that can edit TOML.
- Functional `@critical` test coverage is required for any
  user-visible init-message change per the niwa CLAUDE.md.

**Chosen: B-narrow — Typed errors for the cases Decision 2 needs, plus
case-specific auth/404 messages, but no workspace-level sentinels for
malformed-config and no non-GitHub transport classification.**

Concretely, v1 ships:

1. **Typed `*github.StatusError`** in `internal/github/fetch.go`. New
   type with `StatusCode int`, `Message string`, optional `URL string`
   fields. The four error-construction sites (lines 69, 72, 145, 149)
   return `&StatusError{...}` instead of `fmt.Errorf`. `Error()`
   preserves today's text so existing production string display is
   unchanged. Update the four test fakes in
   `snapshotwriter_test.go` to construct the typed value.

2. **Classifier at the `runInit` seam** in `internal/cli/init.go`,
   replacing the bare `"materializing config repo: %w"` wrap. Branches
   (ordered most-specific first, per `lead-other-failures.md`):

   - `*config.AmbiguousMarkersError` → already-actionable text from
     the type's `Error()`. Format as `InitConflictError`-style Detail
     + Suggestion using the existing text.
   - `*config.NoMarkerError` → bootstrap-feature branch (Decision 1)
     OR, when `--bootstrap` is not set, the existing
     `NoMarkerError.Error()` text in Detail/Suggestion form.
   - `*github.StatusError` with `StatusCode == 401 || 403` →
     case-C message: `"cannot read <sourceURL>: <status>. niwa needs
     GH_TOKEN with read access to this repo. Run \`gh auth login\` or
     set GH_TOKEN with \`repo\` scope and retry."`
   - `*github.StatusError` with `StatusCode == 404` → case-D message
     from Decision 2: `"<sourceURL> not found. Verify the slug is
     correct (org/repo) and the repo exists. If the repo is private,
     set GH_TOKEN with read access. If the repo is brand new and has
     no commits yet, push at least one commit (an empty README is
     enough) and retry with --bootstrap."`
   - Anything else → today's generic `"materializing config repo:
     %w"` wrap, unchanged.

3. **Reuse of existing display machinery.** Each classified case
   constructs an `InitConflictError{Err: <sentinel-or-typed-source>,
   Detail: ..., Suggestion: ...}` and falls through the same
   `fmt.Errorf("%s\n  %s", conflict.Detail, conflict.Suggestion)`
   formatting that `init.go:174,183,201` already uses. No new display
   layer.

What v1 **does not** ship (deferred to a follow-up issue):

- **`ErrSourceConfigMalformed` sentinel** for the post-flight
  `config.Load` parse error (case A). The underlying TOML error
  already names line + column; the marginal hint ("alternate subpath
  via `--from <owner>/<repo>:<subpath>`") is real but small. Today's
  `"post-flight verification failed: %w"` wrap stays. Filed as a
  follow-up if real users report TOML errors are hard to parse.
- **`ErrSourceAuthFailed` and `ErrSourceNotFound` as workspace-level
  sentinels.** The typed `*github.StatusError` plus the per-class
  message in the classifier is sufficient for v1's surface. Wrapping
  in workspace-level sentinels adds a layer of indirection without
  changing the user-visible output. The workspace sentinels can be
  added later if external callers of `internal/workspace` need to
  classify these errors programmatically.
- **Non-GitHub transport classification.** `fallback.go:151` continues
  to emit raw `git clone` stderr. Users on `file://` fixtures, GitLab,
  or Gitea see today's behaviour. Deferred to v1.1+ when (a) we have
  data on which transports users actually use and (b) Decision 2's
  bootstrap path proves itself for GitHub first.

**Rationale**

The decision space here is narrower than the three nominal options
suggest, because Decision 2 has already committed to the typed-error
refactor and the auth/404 classifier arms. Decision 3's real degrees
of freedom are (a) whether to add the workspace-level sentinels on top
of the typed GitHub errors, (b) whether to classify the post-flight
TOML parse error, and (c) whether to classify non-GitHub transport
failures. On each of those three:

- **(a) Workspace-level sentinels.** Adds indirection without changing
  the user-visible surface. The typed `*github.StatusError` plus the
  per-class message in the init classifier already delivers everything
  the user sees. Workspace sentinels are useful only if non-init
  callers need to classify these errors, and no such callers exist
  today. Defer.

- **(b) Malformed TOML.** The underlying parser already cites line +
  column. The marginal Detail/Suggestion is "fix the TOML and retry,
  or pin a different subpath" — useful but not load-bearing. The
  bootstrap feature itself never hits this path (bootstrap fires on
  `NoMarkerError`, post-flight runs only on probe success). Defer.

- **(c) Non-GitHub transport.** Raw `git clone` stderr is at least
  diagnostic; users on these transports are already opting into a
  less-polished path. No Decision-2-equivalent user pressure exists
  to classify them. Defer.

The chosen scope (B-narrow) is the smallest scope consistent with
Decision 2's commitments, and it covers exactly the cases where the
case-specific hint provides material user value beyond what the
underlying error already says (404 with no remediation; auth where
the inline hint is good but the framing as Detail/Suggestion improves
readability). The deferred items remain easy follow-ups — each can
ship as an isolated PR — because the typed `StatusError` and the
classifier shape are the load-bearing infrastructure, and they land
in v1.

Option A (bootstrap path only, no adjacent classification) was
rejected because it conflicts with Decision 2: Decision 2 needs the
typed `*github.StatusError` and the `errors.As` classifier for the
404 message. Shipping A would force Decision 2 to either rewrite its
chosen flow or sink the typed refactor into Decision 2's own PR,
neither of which is coherent.

Option C (string-match dispatch) was rejected because (1) it's
incompatible with Decision 2's `errors.As` assumption, (2)
string-matching against `internal/github/fetch.go`'s error text is
brittle — any rewording in that package silently breaks the
classifier with no compile-time signal, and (3) mixing string-based
dispatch with the existing typed `IsNoMarker` / `IsAmbiguousMarkers`
predicates is a stylistic inconsistency that would invite confusion
in future maintenance.

Option B-wide (the exploration's full proposal including all three
workspace sentinels, the malformed-config sentinel, and non-GitHub
classification) was rejected as over-scoped for v1. The user
explicitly scoped the main feature to the bootstrap path and asked
that adjacent polish be considered separately. The chosen B-narrow
scope ships the polish that's load-bearing (404 hint, auth framing)
and defers the polish that's gilding (sentinels nothing consumes,
self-describing parse errors, transports users mostly don't use).

**Alternatives Considered**

- **A — Bootstrap path only, no adjacent classification.** Rejected
  because it conflicts with Decision 2's commitment to the typed
  `*github.StatusError` and the `errors.As`-based 404/auth classifier.
  Sinking that refactor into Decision 2's own PR would muddy the
  feature/refactor split.

- **B-wide — Full typed-error refactor as exploration proposed.**
  Rejected as over-scoped for v1. Adds workspace-level sentinels
  (`ErrSourceAuthFailed`, `ErrSourceNotFound`,
  `ErrSourceConfigMalformed`) for which no production caller currently
  exists, plus non-GitHub transport classification the user did not
  scope. The deferred pieces are straightforward follow-ups.

- **C — String-match dispatch at the init seam.** Rejected because
  (1) it's incompatible with Decision 2's `errors.As` shape, (2) it's
  brittle against any future rewording of error text in
  `internal/github/fetch.go`, and (3) it introduces stylistic
  inconsistency with the existing typed predicates
  (`config.IsNoMarker`, `config.IsAmbiguousMarkers`).

**Consequences**

What changes:

- `internal/github/fetch.go` ships a new exported `*StatusError` type.
  `Error()` preserves today's text, so the production string display
  is unchanged for callers that print the wrapped error verbatim.
- `internal/cli/init.go` gains a typed classifier (`errors.As` switch)
  around the existing `"materializing config repo: %w"` wrap, covering
  `*config.AmbiguousMarkersError`, `*config.NoMarkerError`,
  `*github.StatusError` (401/403 and 404), with a fall-through to
  today's generic wrap.
- `internal/workspace/snapshotwriter_test.go` updates four test fakes
  to construct `&StatusError{StatusCode: ...}` instead of
  `errors.New("github: HeadCommit returned ...")`. Same effective
  surface; preserves existing assertions.
- Functional `@critical` Gherkin scenarios land for the 404 and
  401/403 user-visible messages (two new scenarios). The
  `NoMarkerError` and `AmbiguousMarkersError` paths reuse existing
  coverage.

What becomes easier:

- The follow-up "add `ErrSourceConfigMalformed` for post-flight TOML
  errors" PR is small and self-contained: introduce one sentinel,
  classify one error site in `init.go:288-291`, add one functional
  scenario.
- Non-GitHub transport classification can plug into the same
  classifier shape later by adding a `*workspace.CloneError` type
  alongside `*github.StatusError`. No refactor of the classifier seam
  required.
- Future callers of `internal/github/fetch.go` outside of
  `internal/workspace` (if any) can classify HTTP failures via
  `errors.As(err, *github.StatusError{})` without touching the
  workspace package.

What becomes harder:

- v1 leaves a small inconsistency between GitHub-transport failures
  (case-specific messages) and non-GitHub-transport failures (raw
  `git clone` stderr). Documented as a known limitation in the v1
  release notes.
- `*github.StatusError` is a new exported type with a stable shape.
  Adding fields later is non-breaking; removing fields is. The v1
  shape is intentionally minimal (`StatusCode`, `Message`, `URL`) to
  keep the future evolution surface small.
- The classifier ordering (auth before 404, marker errors before
  status errors) is load-bearing per `lead-other-failures.md`.
  Documented as a code comment at the classifier seam and reinforced
  by the ordering of test cases.
<!-- decision:end -->
