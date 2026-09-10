# Decision 3: How tests divide across layers

resolution: inline (Decision-bypass-with-inline-resolution, parent `/scope`)
complexity: standard
status: complete
chosen: C -- divide by observable: argv and the document matrix functionally, byte-level and single-function checks as Go tests
confidence: medium

## Question

How are the S1-S9 document matrix and the declaration-driven dispatch tests
split between unit and functional layers, so that each requirement has a test
that can fail?

## Constraints

- Derivation tests take their posture from a real declaration (R17).
- The matrix fixture has a repo and a worktree created through `niwa worktree
  create`, and it asserts that all four documents exist before checking values
  (R18, AC10).
- The dispatch argv is observable only end to end (AC1-AC6, AC8). The existing
  `@critical` scenarios record it through a fake `claude`.
- The functional harness already supports a personal overlay (`a personal
  overlay exists with body`) and `niwa worktree create`.

## Options

**A. Everything functional.** Every acceptance criterion is a Gherkin scenario
against the compiled binary.
- Against: the differential (AC12), tamper (AC7), watch (AC16), and re-entry
  (AC15) checks are statements about single functions and byte comparisons.
  Driving them through the binary adds setup without adding fidelity, and
  AC7's tamper window (between materialization and derivation) can't be
  reached from outside, because dispatch provisions its own instance.

**B. Everything as Go tests.** Drive the real Applier in temporary
directories.
- Against: AC10 names `niwa worktree create` as the worktree's entry point.
  That path goes through `applyContentToWorktree`, which deliberately skips
  the personal overlay, so an in-package `ApplyToWorktree` call wouldn't
  exercise the path S9's worktree cell describes. And the dispatch argv has no
  honest Go-level observable apart from the fake-provisioner seam R17 forbids
  for posture input.

**C. Divide by observable (chosen).**
- Functional: AC1-AC6 and AC8 as dispatch scenarios, including a
  remote-control case and personal-overlay cases, and AC10/AC11 as a Scenario
  Outline over S1-S9 using `niwa init`, `niwa worktree create`, and a new
  step that asserts a settings file's `permissions.defaultMode` (or its
  absence) after asserting that all four files exist and parse.
- Go: AC7 (derivation against a real materialized instance, then tampered),
  AC9 and AC15 (argv and re-entry strings), AC12 (the `buildSettingsDoc`
  differential), AC13 (re-apply over seeded pre-change documents), AC14
  (invalid values at each level), and AC16 (watch review settings applied on
  top of a real materialization).
- For: each check runs at the layer where its observable lives. Every posture
  input is a real declaration. AC10's entry point is the real one.
- Against: two layers to keep consistent, and a Scenario Outline of nine
  examples adds functional runtime.

## Rationale

C is the only option under which every acceptance criterion observes what it
claims to, at the layer where that observable exists. The runtime cost of nine
matrix examples is bounded, and it's the price of exercising the real worktree
entry point.

## Assumptions

- The matrix can run under the non-`@critical` functional suite, with S1 alone
  tagged `@critical`, so the critical lane stays fast.

## Rejected

A (can't reach AC7's tamper window; adds setup to function-level checks),
B (skips the real worktree entry point and has no honest argv observable).
