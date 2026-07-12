# Plan Analysis: DESIGN-niwa-onboard

## Source Document
Path: docs/designs/DESIGN-niwa-onboard.md
Status: Accepted (transitioned to Planned by this run's sentinel-gated handoff)
Input Type: design

## Scope Summary
One `niwa onboard` cobra command delivering the whole vault-onboarding
choreography as an interactive wizard: team setup (folders automated via the
operator's infisical CLI; identity/UA/grant guided with landing checks) and
individual setup (mint/verify/store/revoke via a new management REST surface,
credential correct by construction, zero-or-one login pauses by topology),
ending in distinct wizard-end verifications and typed exit codes.

## Components Identified
- internal/vault/infisical/management.go: ReadIdentity, MintClientSecret,
  RevokeClientSecret, ReadEnvironmentSecrets (R9 read hop); session.go
  (session/org detection); resolveAPIURL + api_url validation.
- internal/onboard/: wizard engine (per-step landing-check loop, stateless
  re-derivation), prompt kit (Confirm/Select/Pause + display-sanitizer),
  detection funnel, team runner, individual runner, R20 record helper
  (~/.config/niwa/), surgical TOML insert + three per-site config drivers.
- internal/cli/onboard.go: command registration, flags (incl. --team/
  --individual, --same-login/--split-login, --json, --accept-api-url),
  onboard.ExitCodeError with codes 2-6, non-TTY fail-fast.
- Test doubles: infisicalFakeServer (httptest; seeding, fault injection,
  request recording; NIWA_INFISICAL_API_URL) + extended writeFakeInfisical CLI
  stub; @critical Gherkin scenarios in test/functional.

## Implementation Phases (from design)
Phases 0-8 as written in the design's Implementation Approach section:
0 early verification of carried assumptions; 1 management client + doubles;
2 prompt kit + detection + api_url entry gate; 3 command surface + exit codes;
4 team setup; 5 individual setup + store + R20; 6 config authoring; 7
wizard-end verification + preconditions; 8 functional @critical scenarios.
(See the design doc for the full per-phase deliverables and test surfaces —
they are the authoritative source for issue outlines.)

## Success Metrics
- Every PRD AC (AC-1..AC-37) has a home in a phase's test surface.
- The @critical individual happy path and TTY-decline scenarios run offline
  against the two doubles (AC-31/AC-32).
- Secret hygiene assertions (AC-27/28/29) and custody boundary (AC-10, AC-23)
  hold.

## External Dependencies
- Operator's infisical CLI on PATH (delegated session) — existing pattern.
- Infisical REST endpoints (management + R9 read hop) — Phase 0 verifies exact
  shapes against current docs; fallbacks defined in the design.
- No new Go dependencies (BurntSushi/toml stays the only TOML lib; no TUI lib).
