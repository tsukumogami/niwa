<!-- decision:start id="overlay-slug-derivation-r10" status="assumed" -->
### Decision: Where does the overlay-slug-derivation override (R10) live in `internal/source`, and how does `Source.OverlayDerivedSource()` change?

**Context**

PRD R10 requires that the auto-discovered overlay slug derive from the
source repo name only, regardless of whether the source's subpath was
user-specified, populated by discovery, or empty. Today,
`Source.OverlayDerivedSource()` in `internal/source/source.go` (lines
127-141) splits on `s.Subpath != ""`: empty subpath returns
`<repo>-overlay`; non-empty subpath returns
`<last-segment-of-subpath>-overlay` via the `lastPathSegment` helper.
This case-split is precisely what R10 forbids — once discovery starts
populating `Source.Subpath = ".niwa"` from a previously-bare slug, the
same `--from acme/vision` invocation that produced overlay
`acme/vision-overlay` would silently start producing `acme/.niwa-overlay`.

`internal/source` is a leaf package: it imports only `fmt`, `net/url`,
and `strings`, and is imported by every other niwa package that needs
typed source identity. The change must preserve that property — no
callbacks into `internal/config` or `internal/workspace`.

A repo-wide search confirms that `OverlayDerivedSource` has no
production callers today (the only references are the function itself,
its tests, and the design documents that describe its intended use by
the future materialization pipeline in `internal/workspace`).
`lastPathSegment` is private to the file and called only from
`OverlayDerivedSource`.

**Assumptions**

- A1: R10's "derives from the source repo name only" means literally
  `s.Repo + "-overlay"` — no further transformation, no compound rule.
  This reading is consistent with the design doc's challenge-3 narrative.
- A2: No code outside the niwa module can import `internal/source` (Go's
  internal-package rule), so the method signature is owned entirely by
  this codebase and breaking the subpath case is safe.
- A3: The future caller of `OverlayDerivedSource` lives in
  `internal/workspace` (overlay materialization). The decision is
  forward-looking; there is no existing caller to coordinate with
  beyond the unit tests.

**Chosen: Modify `OverlayDerivedSource()` in place**

Edit `internal/source/source.go` directly:

1. Delete the `if s.Subpath != "" { base := lastPathSegment(s.Subpath); ... }`
   branch. The function body becomes:

   ```go
   func (s Source) OverlayDerivedSource() Source {
       return Source{
           Host:  s.Host,
           Owner: s.Owner,
           Repo:  s.Repo + "-overlay",
           Ref:   s.Ref,
       }
   }
   ```

2. Rewrite the doc comment to cite this PRD's R10 and to drop the
   "subpath cases use the subpath's last segment" sentence. Suggested
   wording:

   > OverlayDerivedSource returns the auto-discovered workspace overlay
   > slug for this source. Per PRD R10 (config-source-discovery), the
   > overlay repo name is the source repo name plus "-overlay",
   > regardless of whether Subpath was user-specified, populated by
   > discovery, or empty. The overlay's own subpath is empty; its ref
   > inherits from the source.

3. Delete the `lastPathSegment` helper (lines 160-169). It has no other
   callers.

4. Reshape the test cases in `internal/source/source_test.go`
   (`TestSource_OverlayDerivedSource`, lines 217-256) so every case
   that varies the subpath asserts the **same** `<source-repo>-overlay`
   repo name — demonstrating R10's invariance across input shapes.
   Keep five cases (whole-repo, single-segment subpath, multi-segment
   subpath, ref inheritance, host inheritance), renaming them so it's
   clear they test stability under varying inputs, not different naming
   rules.

The method signature `(Source) OverlayDerivedSource() Source` stays
unchanged. The leaf-package property is preserved (no new imports). The
only behavioural change is for inputs with non-empty `Subpath`, which
have no production callers today.

**Rationale**

- **R10's wording is unconditional.** "Regardless of whether the
  source's subpath was resolved explicitly, by discovery, or empty"
  leaves no place for the old behaviour. Keeping R35's subpath case
  callable under any name (Alternative 2's V2 method, Alternative 3's
  style enum) documents an alternative the PRD has eliminated.
- **No production callers means no migration friction.** The cost
  normally associated with in-place breaking changes — find-callers,
  coordinate cutover, manage deprecation — is zero. The cost of
  *deferring* via a V2 method is positive: a follow-up cleanup PR to
  delete the old method, plus a window where the codebase has two
  derivations and readers must check which one is current.
- **Single source of truth matches the leaf-package philosophy.** The
  `source` package's job is to encode the canonical slug-and-derivation
  identity. Encoding two derivations contradicts that.
- **Doc comment must change regardless.** The current comment cites
  "PRD R35" and explicitly describes the subpath rule as intended
  behaviour. After R10, the comment is wrong even if the code is left
  alone — so the doc-rewrite work happens in every alternative.
  Alternative 1 collapses code change and doc change into one
  consistent edit; Alternatives 2 and 3 require two comments (old
  method's deprecation note + new method's R10 cite) plus a migration
  paragraph.

**Alternatives Considered**

- **Add `OverlayDerivedSourceV2()` and deprecate the old.** Add a new
  method with R10 semantics, mark the old method `// Deprecated:`,
  point callers at V2. Rejected because: (1) there are no callers to
  migrate, so the deprecation cycle has no purpose; (2) it leaves R35
  subpath behaviour callable in a package whose job is to encode the
  current canonical identity; (3) Go's "V2 method" pattern is reserved
  for binary-compat scenarios that don't apply to an internal package;
  (4) it creates a follow-up cleanup PR that does nothing but delete
  the old method.

- **Parameterize the existing method (style enum or options struct).**
  Add a parameter or `Source` field selecting "R35 style" vs "R10
  style." Rejected because: (1) the only use case for R35 style is
  historical, which is not a use case; (2) adds an enum / option type
  to a leaf package that had none, increasing surface area for no
  callers; (3) tests would have to cover both styles to be honest
  about the parameter, doubling the test surface; (4) either breaks
  the method shape (parameter added) or collapses into Alternative 2
  via a wrapper. None of these costs buy anything since no caller
  needs the old behaviour.

**Consequences**

What changes:
- `internal/source/source.go` loses ~10 lines (subpath branch + helper)
  and gains ~3 lines (the simpler return) plus a rewritten comment.
- `internal/source/source_test.go` has its five `OverlayDerivedSource`
  cases reshaped so subpath variants all expect `<repo>-overlay`.
- Future callers in `internal/workspace` (per the design doc's
  materialization pipeline) get R10 semantics by default without
  needing to know about the rule.

What becomes easier:
- The overlay derivation rule is now a one-liner — readable, audit-able,
  unlikely to drift from the PRD.
- The "discovery populates subpath" data flow no longer needs to worry
  about overlay-slug side effects, because the overlay slug is
  insensitive to subpath.
- The design doc's challenge-3 narrative ("the design needs to
  short-circuit this path") is satisfied directly: there is no path to
  short-circuit because subpath no longer participates.

What becomes harder:
- Nothing in the current codebase. The historical R35 basename rule is
  no longer callable — but no current caller wants it.
- If a future PRD ever wants per-subpath overlays again (e.g., for a
  multi-tenant repo serving multiple overlays from one source), it
  will need a *new* mechanism rather than re-enabling R35. This is
  acceptable: R10 was a deliberate simplification and a future
  multi-tenant feature should design its own slug rule explicitly,
  not inherit one accidentally.
<!-- decision:end -->
