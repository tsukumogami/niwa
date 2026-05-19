---
complexity: testable
complexity_rationale: |
  Touches `internal/cli/init.go` with new flag wiring, a TTY-gated prompt branch,
  an R25 mutual-exclusion check, R2 name derivation, a host-check stub, and
  classifier dispatch into <<ISSUE:1>>'s helper. Adds the `ExitCode` plumbing
  on `*workspace.InitConflictError` and the `os.Exit(...)` mapping at main.
  Bootstrap itself dispatches into a stub that returns "not implemented yet,"
  so the real orchestrator wiring is deferred to <<ISSUE:4>>. Each behavior is
  table-testable: flag combinations, TTY/non-TTY paths, R13 prompt input
  variants, host-check non-GitHub source, classifier wiring. Adds `@critical`
  Gherkin coverage for the user-visible error strings (R25, R13 TTY/non-TTY,
  401/403/404). No new orchestration logic, no git invocations, no scaffold
  derivation -- those are downstream issues.
---

## Goal

Wire the `--bootstrap` / `--no-bootstrap` flag surface, the R13 TTY prompt and non-TTY fail-fast paths, the R9 host-check, and classifier dispatch into <<ISSUE:1>>'s helper so case-specific error messages flow for 401/403/404/ambiguous-markers while bootstrap itself dispatches to a stub.

## Context

Today's `runInit` materialize boundary wraps every error generically. Phase 2 turns on the flag surface and the R13 prompt UX while dispatching adjacent failures through the classifier seam built in <<ISSUE:1>>. Bootstrap itself remains stubbed -- the real `RunBootstrap` lands in <<ISSUE:4>> -- but every adjacent failure mode (auth, 404, ambiguous, no-marker) starts emitting case-specific text and exit codes per the PRD R23 table. R2 derives a workspace name from the slug only when `--bootstrap` is set, preserving today's no-flag behavior.

Design: `docs/designs/DESIGN-init-bootstrap-empty-source.md` (Phase 2, around line 1062).

## Acceptance Criteria

- [ ] `initBootstrap` and `initNoBootstrap` package-level vars + cobra flags declared in `internal/cli/init.go`, matching the existing `initOverlay` / `initNoOverlay` pattern (same scope, same registration site, same help-text style).
- [ ] **R25 mutual exclusion**: passing both `--bootstrap` and `--no-bootstrap` emits the exact stderr string `--bootstrap and --no-bootstrap are mutually exclusive` and exits with code `2`. R25 runs upstream of the NoMarker classifier branch.
- [ ] **R2 name derivation**: when `initBootstrap` is true and `len(args) == 0`, the workspace name derives from `src.Repo` (e.g. `niwa init --from owner/foo --bootstrap` produces workspace `foo` at `<cwd>/foo/`). Today's `niwa init --from <slug>` (no positional, no `--bootstrap`) behavior is unchanged -- a regression test covers the no-flag baseline.
- [ ] **R13 TTY prompt**: on `*config.NoMarkerError` with `IsStdinTTY()` true and neither flag set, niwa writes the exact stderr prompt string `Remote has no .niwa/workspace.toml. Scaffold a minimal config and stage it on a niwa-bootstrap branch? [Y/n] ` and reads via `cli.ReadConfirmation`. Accepts `y`, `Y`, or bare Enter as Yes; `n` or `N` as No; any other input re-prompts. Decline returns exit code `0`.
- [ ] **R13 non-TTY fail-fast**: on `*config.NoMarkerError` with `!IsStdinTTY()` and neither flag set, niwa emits the exact stderr string `remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold` and exits with code `4`.
- [ ] **R13 `--no-bootstrap` path**: on `*config.NoMarkerError` with `--no-bootstrap` (TTY or non-TTY), niwa emits the NoMarker error text plus an explicit-decline reason and exits with code `4`.
- [ ] The bare `"materializing config repo: %w"` wrap at `internal/cli/init.go:265` is replaced with a call into <<ISSUE:1>>'s `classifyMaterializeError`. Classifier output is displayed via the existing `*workspace.InitConflictError` Detail+Suggestion pattern at `init.go:174,183,201` (no new display path).
- [ ] On `*config.NoMarkerError` + `--bootstrap`, the code path dispatches into a stub returning `errors.New("bootstrap step=create: not implemented yet")`. The stub fires AFTER all upstream validation (R25, R2, host check, classifier dispatch) but BEFORE any scaffold write -- Issue 4 owns the workspace-root `ScaffoldFromSource` call and inserts it between the classifier and the real `RunBootstrap` invocation. No scaffold write happens in Issue 2; `workspaceCreated` defer fires on the stub error (init-step rollback per R7).
- [ ] **`workspaceCreated` defer arming unchanged**: this issue does NOT modify the existing `workspaceCreated = true` arming after `os.Mkdir(workspaceRoot)` at `init.go:215-226`. Today's arming behavior is preserved verbatim. The disarm-timing change (flipping `workspaceCreated = false` immediately after the workspace-root `ScaffoldFromSource` call returns nil) is owned by <<ISSUE:4>>, since Issue 4 is the issue that introduces the scaffold call. Issue 2's stub returns before any scaffold write, so the existing defer fires cleanly on stub errors. (Closes the Category D finding.)
- [ ] The classifier's 404 and `*config.NoMarkerError` arms gain the `--bootstrap`-retry hint text (forward-looking text deferred from <<ISSUE:1>>). The 404 Suggestion contains all three R11 substrings; the `*config.NoMarkerError` Suggestion (non-`--bootstrap` paths) cites `--bootstrap` as the next step.
- [ ] **R23 exit-code surfacing**: `*workspace.InitConflictError` carries the `ExitCode int` field introduced in <<ISSUE:1>>, populated per the PRD R23 table (`0`/`1`/`2`/`3`/`4`). `runInit` returns the typed error; the binary main (in `cmd/niwa/main.go`) maps the field to `os.Exit(...)`.
- [ ] **R9 host check (early stub for non-GitHub)**: inside `runInit`, immediately after `parseInitSource(source)` succeeds, the code asserts `src.IsGitHub()`. A non-GitHub source produces exit code `3` and the exact R9 stderr string `bootstrap supports only GitHub sources in v1; got host=<host>`. Implementation **must** use `Source.IsGitHub()` from `internal/source/source.go:148`, **not** `src.Host == "github.com"` -- the canonical slug form leaves `Host` empty and the bare equality check would silently let canonical slugs through.
- [ ] Unit tests cover: flag declaration and registration; R25 mutual exclusion (both flags set together → exit 2 + exact string); R2 derivation (positional empty + `--bootstrap` → name = `src.Repo`; positional set + `--bootstrap` → positional wins; positional empty + no `--bootstrap` → unchanged behavior); R13 TTY-yes / TTY-no / TTY-other-then-Y re-prompt paths; R13 non-TTY fail-fast; `--no-bootstrap` decline path; classifier dispatch wiring (each classifier arm reaches the right Detail+Suggestion at `init.go`).
- [ ] `@critical` Gherkin scenarios under `test/functional/features/` cover: 401 user-visible text; 403 user-visible text; 404 user-visible text (all three R11 substrings); R25 mutual-exclusion exact string + exit code 2; R13 TTY-yes proceed path; R13 TTY-no decline path; R13 non-TTY fail-fast text + exit code 4.

## Dependencies

Blocked by <<ISSUE:1>>: the `*github.StatusError` type, the `classifyMaterializeError` helper, and the `ExitCode` field on `*workspace.InitConflictError` are all introduced upstream and this issue consumes them.

## Downstream Dependencies

<<ISSUE:4>> replaces this issue's stub dispatch on `*config.NoMarkerError` + `--bootstrap` with: (a) the workspace-root `workspace.ScaffoldFromSource(workspaceRoot, opts)` call inserted between the classifier and the orchestrator; (b) the disarm-after-scaffold step (`workspaceCreated = false` immediately after that call returns nil, per R7 create-step preservation); and (c) the real `workspace.RunBootstrap` call (constructing `stdGitInvoker{}` and passing the parsed source + name + scaffold options).
