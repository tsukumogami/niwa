# Review Plan Category A (Scope Gate): dispatch-sendmessage-approval

Round 1, fast-path, input_type `design`, execution_mode `single-pr`. Full check.

## Inputs read

- Analysis, milestones, decomposition, dependencies, manifest, decisions.
- All eight issue body files listed in the manifest. All are present.
- Upstream design `docs/designs/DESIGN-dispatch-sendmessage-approval.md`
  (frontmatter `user_visible_surface: true`).
- PRD `docs/prds/PRD-dispatch-sendmessage-approval.md`: R17, R18, and the
  watch-related acceptance criteria.

## 1. Issue count range

The design has seven implementation phases, and its Components section names
about 20 file-level components. These group into roughly eight cohesive units:
the config key, the settings rendering plus prompt separator, the capability
row, the flag, resolver and stderr lines, the durable record plus `niwa list`,
the explanation plus marker, the review deny hook, and tests plus the guide.
The plan has 8 issues, one per phase, with Phase 6 split into a test issue (7)
and a docs issue (8).

Measured against 7 phases, the range is 4 to 35. Measured against about 20
file components, it's 10 to 100. By the second measure 8 issues is under 10,
but that measure is the wrong one here: several files form one unit
(capability.go, declaration.go, gaplist.go, and their tests; session_map.go,
state.go, and list.go). Every component still has an owning issue (see
section 3). No count finding.

## 2. Complexity coverage

The architecturally significant, high-risk pieces carry `critical` complexity:

- Issue 1: review-containment deny hook. It's a security boundary for
  reviewers reading untrusted changes.
- Issue 3: the single `--settings` slot refactor plus the argv change on
  every Claude launch.
- Issue 4: the behavior switch, precedence, eligibility through the
  capability row, and the audit lines.

Issues 2, 5, 6, and 7 are `testable`, and issue 8 is `simple`. Two
classifications were challenged:

- Issue 6 (marker plus timing around attach) was checked for under-rating.
  The design says the marker gates no authority, and the issue's ACs already
  pin the race, symlink, and error-tolerance behavior, so `testable` is
  defensible.
- Issue 8 (manual delivery check that can force a matcher change) was checked
  the same way. The matcher change would land in containment code that issue
  1 owns as `critical`. Issue 8's own deliverables are the guide and the
  recorded measurements, so `simple` isn't a structural gap.

No finding.

## 3. Missing components

Each design component was mapped to an owning issue:

| Design component | Issue |
|---|---|
| `registry.go` field, setter-less list comment, `registry_inbound_test.go` | 2 |
| `config.go` `CrossSessionInboundKey` | 2 |
| `capability.go`, `declaration.go`, `gaplist.go`, and count updates | 4 |
| `docs/guides/codex-agent.md` regenerated gap list | 4 |
| `PRD-agent-capability-contract.md` row-25 amendment | 4 |
| `dispatch_settings.go` `renderLaunchSettings` | 3 |
| `dispatch_inbound.go` flag, resolver, `inboundResolution`, `inboundGuideURL`, message strings | 4 |
| `dispatch_inbound.go` `showInboundExplanation`, marker | 6 |
| `dispatch.go` flag registration, `hostGlobal` hoist, step 9c, stderr line | 3, 4 |
| `dispatch.go` step 11 literal | 5 |
| `dispatch.go` explanation call sites (after audit line, after `dispatchAttach`) | 6 |
| `dispatch_layout_test.go` additions | 3, 4, 6 |
| `dispatch_test.go` flag-reset helper | 4 |
| Watch-site unit test (both launch sites) | 7 |
| `session_map.go`, `state.go`, `list.go` (annotation, marker, `--json` help, `Long`) | 5 |
| `containment.go` deny matcher and hook, Apply/Verify, stanza comment | 1 |
| `agentplan/dispatch.go` `PromptSeparator` plus argv test updates, fake claude | 3 |
| Functional `@critical` scenarios and step definitions | 7 |
| Guide, CLAUDE.md index, help and completion check | 8 |
| Manual delivery check plus the five extra measurements | 8 |

Items checked adversarially:

- The design says "the PRD's R18 is amended to match" for the watch-exclusion
  unit test. The PRD in the tree already carries the amended R18 (lines
  303-309), so no issue owns an outstanding PRD edit.
- The design places the watch-site unit test in "Phase 3 (or Phase 6)". The
  plan puts it in issue 7, and issue 4's ACs don't claim it, so it has
  exactly one owner.
- The PRD-level criteria (R15 unreadable config, R16 stdout, R12 personal
  settings, the R3 source fixtures) are covered by unit ACs in issues 2, 4,
  and 6, and by the scenarios in issue 7.

No component is absent from the issue set.

## 4. Docs-coverage backstop

The design frontmatter sets `user_visible_surface: true`, so the check applies.
Issue 8 is `**Type**: docs` (decomposition `docs_coverage_issue: 8`). Its
deliverables are `docs/guides/session-message-acceptance.md` and the
contributor-guide index entry. Issue 4 also regenerates the codex-agent guide's
gap list. Docs coverage is present. No finding.

## Findings

```yaml
critical_findings: []
```

Confidence: high. Every input artifact and issue body was present, the design
has an explicit component list and phase order, each component maps to an
issue, and the one outstanding edit the design names (the PRD's R18) is
already in the tree.
