# Plan Decomposition: niwa-onboard

## Decomposition Strategy

Horizontal (--no-skeleton equivalent), matching the design's own Implementation
Approach: the design already sequences nine phases where each builds one
component fully on stable interfaces settled by the five decisions (management
client, prompt kit, command shell, team runner, individual runner, config
authoring, verification, functional tests). A separate walking-skeleton slice
would duplicate the Phase 3 "command shell wired to a wizard engine skeleton"
step the design already contains. Rationale recorded per decision protocol
(tier 2, confirmed: the design text explicitly says "Sequential, buildable and
testable incrementally, feeding a single-PR plan").

## Issues

| # | Title | Design phase | Complexity |
|---|-------|--------------|------------|
| <<ISSUE:1>> | Verify carried REST/CLI assumptions (research note) | 0 | simple |
| <<ISSUE:2>> | Management REST client, session detection, api_url validation, test doubles | 1 | critical |
| <<ISSUE:3>> | Prompt kit, display sanitizer, api_url entry gate, detection funnel | 2 | testable |
| <<ISSUE:4>> | onboard command surface, exit codes, non-TTY contract | 3 | testable |
| <<ISSUE:5>> | Team setup runner (folder delegation, guided steps, plan-gate degradation, R21 sweep) | 4 | testable |
| <<ISSUE:6>> | Individual setup runner (mint pipeline, store, split-login pause, R20 record + revocation) | 5 | critical |
| <<ISSUE:7>> | Config authoring (surgical TOML insert, three per-site drivers) | 6 | testable |
| <<ISSUE:8>> | Wizard-end verification and preconditions | 7 | testable |
| <<ISSUE:9>> | Functional @critical scenarios | 8 | testable |

Complexity notes: <<ISSUE:2>> and <<ISSUE:6>> are critical (credential custody,
secret hygiene, auth flows). <<ISSUE:1>> is simple (research note, no code).
The rest are testable (new behavior with unit/functional coverage).

## Value Confirmation (step 3.5a)

Single-pr: the unit under the value test is the one PR — a working `niwa
onboard` wizard with tests — which is independently useful (a developer or
admin can onboard a workspace vault end-to-end). The nine issues are build
steps inside that unit, not PR-shaped deliverables, so the per-unit value test
applies to the PR as a whole and passes. Decision recorded: tier 2, confirmed.

## Execution Mode (step 3.6)

single-pr — confirmed. The user directed single-pr explicitly at dispatch; the
default rule agrees (no hard constraint forces multiple PRs: single repo, no
merge gate between steps, and the intermediate issues are not independently
useful to a reader). Decision recorded: tier 2, confirmed.
