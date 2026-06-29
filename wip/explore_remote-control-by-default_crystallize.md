# Crystallize Decision: remote-control-by-default

## Chosen Type

Spike Report (routes to /spike) — user decision: prove it works first, then
decide what to build next.

The crystallize scoring favored a Design Doc (decisions already made + open
how-questions). The user overrode toward a Spike because the whole approach hinges
on one unproven empirical claim — that enabling `remoteControlAtStartup` on a
`claude --bg` worker actually makes it live-steerable from Agent View / mobile. No
design is worth committing until that go/no-go is settled. After the spike proves
(or disproves) it, the design decisions recorded below become the input to a
follow-up Design Doc.

### Original scoring (retained for the follow-up design)

Design Doc (routes to /design), with an empirical validation step folded in for
two claude-side unknowns.

## Rationale

The exploration settled WHAT to build (enable Claude Code Remote by default on
`niwa dispatch` sessions via a host-level, downstream-overridable toggle) but left
genuine HOW decisions, and it already made several architectural choices that a
future contributor must understand. Both facts point to a Design Doc: a permanent
record of the decisions plus a place to resolve the remaining ones.

Decisions already made that need to be on record (lost when wip/ is cleaned
otherwise):
- The toggle lives in host config layer 1 (`config.GlobalSettings`,
  `~/.config/niwa/config.toml [global]`), NOT the overlay's `[global.claude.settings]`
  (layer 2) — because layer 2 materializes into every instance and cannot be
  dispatch-scoped.
- The enable lever is the Claude Code settings key `remoteControlAtStartup: true`,
  not the interactive-only `--remote-control` flag and not `--bg` alone.
- Injection happens at the dispatch-exclusive argv seam via
  `--settings '{"remoteControlAtStartup":true}'`, not a post-provision settings.json
  hand-edit (which would collide with niwa's managed-file fingerprint).

Decisions still open (the design doc's job):
- Override-resolution strategy: have niwa read the dispatched instance's effective
  `remoteControlAtStartup` and inject only when unset (niwa resolves), vs. rely on
  claude's `--settings`-vs-settings.json precedence.
- How to handle the daemon-level `autoAddRemoteControlDaemonWorker` requirement IF a
  live test shows the per-session key alone doesn't make a `--bg` worker steerable.
- Config field naming and the precondition/error-surfacing behavior when a worker is
  ineligible (API-key-only, missing scopes, org policy off).

## Signal Evidence

### Signals Present (Design Doc)
- What to build is clear, how to build is not: WHAT settled in scoping; HOW has
  open decisions (override resolution, daemon handling, field design).
- Technical decisions need to be made between approaches: three injection seams
  evaluated; override-resolution approach still a choice.
- Architecture/integration questions remain: daemon-worker requirement and settings
  precedence are integration unknowns.
- Exploration surfaced multiple viable implementation paths: seams (a) flag-style
  `--settings`, (b) env var, (c) settings.json write — all weighed.
- Architectural decisions were made during exploration that should be on record:
  layer-1-not-2, settings-key-not-flag, argv-seam-not-write.
- Core question is "how should we build this?": yes.

### Anti-Signals Checked
- "What to build is still unclear": not present — scoping settled it.
- "No meaningful technical risk or trade-offs": not present — real trade-offs and
  two correctness-gating unknowns exist.
- "Problem is operational, not architectural": not present.

## Alternatives Considered

- **Spike Report**: Real fit for the two empirical unknowns (does the per-session
  key alone make a `--bg` worker steerable in Agent View; claude's settings-source
  precedence for daemon workers). Ranked lower because it covers only a slice of the
  work — the bulk is "how to build," not "can we." Best treated as a validation step
  *inside* the design, or a fast pre-design spike if the user wants to de-risk the
  daemon question before committing to a design. No anti-signals; close second.
- **No Artifact**: Demoted — architectural decisions were made during exploration
  (layer choice, seam choice, mechanism), which is an explicit anti-signal; they
  can't be allowed to vanish with wip/.
- **Plan**: Demoted — no upstream PRD/design exists yet and open architectural
  decisions remain (override resolution, daemon handling).
- **Decision Record**: Demoted — this is several interrelated decisions, not a
  single one; an ADR would fragment them where a design doc unifies them.
- **PRD**: Demoted — requirements were given as scoping input, not discovered.
- **Rejection Record**: Not applicable — demand is un-surfaced but NOT rejected
  (no positive rejection evidence; "found nothing" != "found it was declined").

## Deferred Types

- **Prototype**: Not selected. The empirical unknowns could be answered by a
  throwaway live test, but that's better captured as the design's validation step
  than as a standalone prototype.
