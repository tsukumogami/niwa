# Testability Review

## Verdict: FAIL

All three gating edits landed and are individually sufficient, but R19's REST-double resource-state-seeding list claims fixtures (folder structure, the wizard-end read) that the ACs and the PRD's own CLI/REST division of labor assign to the CLI stub — a fixture-ownership contradiction a test author would trip on.

## Untestable Criteria

None. Every AC is testable with a clearly-owned fixture as written.

## Missing Test Coverage

None. The revoke path (AC-33/AC-34/AC-35b), the unrecoverable-prior-id branch (AC-35b), the seeded-body wizard-end failure (AC-18b), and the team-phase landing-check failure (AC-35) are all covered with induced fixtures.

## Introduced Inconsistency (blocking)

**R19's REST-double modeled-resource list over-scopes the REST double to include CLI-owned fixtures.** Two conflicts:

1. **Folder structure.** R19 (line 428) lists "the folder structure" among the REST double's seedable resources. But folder creation and verification are CLI-delegated everywhere else — R6/D3 ("the one team-phase operation the installed CLI exposes natively"), AC-8 (folder-create fires on the CLI stub), and AC-35 explicitly (line 662: "the folder structure via the CLI stub"). The REST double has no folder surface to seed. R19 and AC-35 name different doubles as owner of the same fixture.

2. **Wizard-end read failure.** R19 (line 429) says the REST double's resource-state seeding lets a test "drive ... a wizard-end read failure." AC-18b (lines 582-585) settles that the wizard-end read resolves through the credential-sync provider's `infisical export` path, so the stored-body fixture owner is the **CLI stub**. R19 attributing the wizard-end read failure to the REST double contradicts the AC-18b edit made this same round.

Note: conflict (1) traces to the prior review's own clearing instruction, which asked R19's REST double to seed "folders as present/absent/malformed" — but folders are CLI-native, so honoring that literally put folder seeding on the wrong double.

**Narrow fix:** scope R19's REST-double resource-state seeding to its REST-reachable resources (identity, `client_id`, minted-secret bodies, environment read grant) and move folder-structure seeding and the wizard-end-read-failure driver to the CLI stub, matching AC-8, AC-35, and AC-18b.

## Summary

The three required edits (R19 resource-state seeding + revoke endpoint + revocation-failure fault; AC-18b fixture owner; AC-35b for R20's unrecoverable-prior-id branch) are all present and sufficient. The blocker is that R19's REST-double resource list assigns folder-structure seeding and the wizard-end read failure to the REST double, while AC-8/AC-35 (folders) and AC-18b (stored body) assign both to the CLI stub — a one-line-fix ownership contradiction, not a coverage gap.

## Resolution (orchestrator note)

The single blocking item — R19's REST-double seeding list claiming the two
CLI-owned fixtures — was fixed exactly as the narrow fix prescribed: seeding
rescoped to REST-reachable resources (identity, client_id, minted secret
bodies, environment read grant); folder structure and the stored credential
body moved to the CLI stub's fixture list. With this applied the review's own
assessment (untestable criteria: none; missing coverage: none) clears the
verdict. Testability: PASS after fix.
