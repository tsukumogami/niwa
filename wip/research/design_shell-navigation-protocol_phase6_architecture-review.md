# Architecture Review: Shell Navigation Protocol

## Subject

Design document for replacing the current stdout-capture shell protocol with a
temp-file-based protocol (`NIWA_RESPONSE_FILE`). Covers three implementation
phases: CLI protocol writer, shell wrapper update, Phase 3 stdout pollution fix.

## Current State

The existing `shellWrapperTemplate` (shell_init.go:37-55) captures the entire
stdout of `command niwa "$@"` with command substitution (`__niwa_dir=$(command
niwa "$@")`). The landing path is the only thing written to stdout in `create`
and `go`; progress messages go to stderr. The protocol works but is fragile: any
accidental stdout write from a subprocess corrupts the captured value. Phase 3
exists to document that risk.

`create.go` and `go.go` already call `validateStdoutPath` and write the path via
`fmt.Fprintln(cmd.OutOrStdout(), ...)`. The protocol is already separated;
only the transport mechanism (command substitution vs. temp file) changes.

## Finding 1: The protocol change is structurally clean -- no bypass, no parallel pattern

The proposed `writeLandingPath` helper replaces the two `fmt.Fprintln` callsites
in `create.go` (line 153) and `go.go` (line 80). It doesn't duplicate the
existing validation path (`validateStdoutPath` stays), and the shell wrapper
remains the sole consumer of the landing path. The concern separation -- CLI
writes path, shell wrapper reads it -- is preserved exactly.

No action dispatch bypass, no new interface introduced in parallel with an
existing one. Advisory-clean.

## Finding 2: Phase 3 scope claim does not match the code -- BLOCKING

The design document states Phase 3 fixes stdout pollution in:
- `apply.go` (6 `fmt.Printf` calls) -- CONFIRMED: lines 246, 252, 254, 258, 264, 330 all use `fmt.Printf` (unrouted to any writer)
- `clone.go` (lines 61, 70) -- NOT PRESENT: `clone.go` has no `fmt.Printf` or `fmt.Println` calls; it only uses `fmt.Errorf` and `fmt.Sprintf`
- `setup.go` (line 105) -- NOT PRESENT: `setup.go` has no printf-style stdout calls
- `sync.go` (lines 68, 88) -- NOT PRESENT: `sync.go` has no printf-style stdout calls
- `configsync.go` (line 42) -- NOT PRESENT: `configsync.go` has no printf-style stdout calls

The actual pollution is confined to `apply.go`. The four other files named in
the document have no stdout calls. If an implementer follows the document
literally, they'll look for code that isn't there and may miss the real problem.

This is a blocking issue in the design doc as written. The Phase 3 callsite list
must be corrected to `apply.go` only (6 calls, confirmed), or the document
becomes a mismatch with the codebase and risks confusing the implementer.

## Finding 3: Phase 3 has a structural coupling problem

`apply.go`'s `fmt.Printf` calls are inside `Applier` methods that have no
`io.Writer` parameter. The `Applier` struct (`apply.go:15`) currently has no
output writer field. Routing those calls to stderr (as Phase 3 intends) requires
either:

a. Adding an `io.Writer` field to `Applier` (e.g., `Progress io.Writer`) -- the
   idiomatic Go approach -- and setting it to `cmd.ErrOrStderr()` in the callers
   (`create.go`, `apply.go` CLI command).
b. Or just replacing `fmt.Printf` with `fmt.Fprintf(os.Stderr, ...)` -- simpler,
   but hardcodes `os.Stderr` in a library package, which breaks testability.

The design doc says "route subprocess stdout to stderr" but doesn't say which
approach to take. Without specifying, an implementer might pick option (b), which
is the wrong structural choice: library packages shouldn't write to `os.Stderr`
directly (the existing `Applier` already uses `fmt.Fprintf(os.Stderr, ...)` for
warnings in a few places, but that's already a latent problem, not one to
propagate). Option (a) -- an `io.Writer` field on `Applier` -- respects the
package boundary. The design doc should specify this.

Advisory severity, but will compound if left unspecified: the implementer will
likely replicate the existing `os.Stderr` pattern.

## Finding 4: Sequencing is correct, but Phase 1 and Phase 3 are independent

Phase 1 (protocol writer) and Phase 3 (stdout pollution fix) don't depend on
each other. Phase 3 is actually the prerequisite for making the current protocol
safe -- not something that must wait until after Phase 1. The document sequences
them correctly for the purpose of landing the new protocol, but a reviewer should
know they can be done in parallel or in reverse order if needed.

## Finding 5: Fallback behavior in the shell wrapper

The pseudocode has correct fallback on `mktemp` failure:

```sh
__niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }
```

This degrades to the current stdout-capture behavior, which is sensible. One
edge case not addressed: if the Go binary writes nothing to the temp file (e.g.,
an error path where the command fails but exits 0 due to a bug), the wrapper
silently skips the `cd`. This is the same behavior as today, so it's not a
regression -- just worth noting that the wrapper's `[ -d "$__niwa_dir" ]` check
is the guard for this.

## Summary

| # | Finding | Severity |
|---|---------|----------|
| 1 | Protocol change is structurally clean | No issue |
| 2 | Phase 3 callsite list names files with no stdout pollution (`clone.go`, `setup.go`, `sync.go`, `configsync.go`) | Blocking -- incorrect spec |
| 3 | Phase 3 doesn't specify how to route output in `Applier` (field vs. `os.Stderr`); current pattern will compound | Advisory |
| 4 | Phase 1 and Phase 3 are independent; ordering is valid but not mandatory | Informational |
| 5 | Fallback and guard behavior are correct | No issue |

## Recommendation

Fix the Phase 3 callsite list before implementation begins: remove the four
incorrect files and confirm the fix scope is `apply.go` only. Add a note to
Phase 3 specifying that the fix should introduce an `io.Writer` progress field on
`Applier`, not `os.Stderr` hardcoding.
