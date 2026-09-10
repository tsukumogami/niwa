# /design Decisions: inert-defaultmode-key

Auto mode, invoked by `/scope` under a `parent_orchestration:` sentinel. The
parent binding for Phase 2 is Decision-bypass-with-inline-resolution: each
decision is resolved inside this design's own evaluation and recorded in
Considered Options, with `decision_provenance: inline-resolved` in frontmatter.

| id | artifact | tier | status | question |
|----|----------|------|--------|----------|
| G1.1 | wip/design_inert-defaultmode-key_coordination.json | 2 | confirmed | How many decision questions, and which are coupled? |

## G1.1 -- Three decision questions

**Evidence.** The PRD settles the WHAT completely, including the expected
value for every document under every override combination, so the open
choices are HOW choices. Three are independent. The first is where the
derivation's input comes from, which is critical: it is the one choice with a
security edge, since a wrong input source lets a file grant bypass. The second
is how the materializer's mapping and validation change. The third is how the
tests divide across layers. The doc corrections and the dead-reader deletion
are required by the PRD and involve no choice. The first and third are mildly
coupled, because the derivation's unit test takes the shape of its input, but
the options for one don't constrain the options for the other.

**Decision.** Three questions (1 critical, 2 standard), within the 1-5 band,
proceeding normally.

| G3.1 | wip/design_inert-defaultmode-key_coordination.json | 2 | confirmed | Do any decision's assumptions conflict with a peer's choice? |

## G3.1 -- Cross-validation passed with no conflicts

**Evidence.** Decision 1 assumes the instance-root posture is available as a
resolved value during materialization, and that `InstanceState` takes an
additive `omitempty` field without migration. Both hold:
`RootSettingsMaterializer.Materialize` resolves it through
`MergeInstanceOverrides(cfg)`, and Create/Apply already persist
`pipelineResult` fields (`shadows`, `trustKeys`) into `InstanceState`.
Decision 2 assumes one builder covers all four documents, and
`writeRootSettings` does use `buildSettingsDoc`. Decision 3's tamper test
reads the posture through `LoadState` on a real materialization, which is
Decision 1's read path. No choice contradicts a peer's assumption.

**Decision.** `cross_validation: passed`, no restarts. One constraint carried
into the architecture: the value persisted for Decision 1 and the value
`buildSettingsDoc` writes at the instance root come from one shared resolver
over `MergeInstanceOverrides(effectiveCfg)`, so the two can't diverge.

| G4.1 | docs/designs/DESIGN-inert-defaultmode-key.md | 2 | confirmed | Implicit: keep the derived mode in a package global, or pass it to the builder? |

## G4.1 -- Implicit decision surfaced in Phase 4: the builder takes the permission mode as a parameter

**Evidence.** Writing the Solution Architecture exposed a choice the prose
would otherwise have asserted. `runDispatch` writes the derived value into
the package-level `dispatchPermissionMode`, and `buildDispatchPassthrough`
reads it implicitly. `niwa watch` calls the same builder and gets an empty
value only because nothing in a watch process sets the variable. The PRD
requires that watch never carry a derived flag.

**Decision.** Pass the mode as an explicit parameter; both watch call sites
pass `""`. Recorded as Decision 4 in Considered Options. Under `--auto` this
is recorded rather than asked; it costs three call-site edits and turns
watch's safety into a property a test can pin.

| G6.1 | docs/designs/DESIGN-inert-defaultmode-key.md | 2 | confirmed | Strawman check on rejected alternatives |

## G6.1 -- Strawman check passed

Every rejected alternative in Considered Options describes a viable approach
and fails on a named, checkable weakness, not on "less good":

- D1 provisioning-result threading fails R17 at the fake-provisioner seam.
- D1 re-resolve at dispatch duplicates precedence and vault resolution.
- D1 niwa-owned settings key fails both of R3's tamper cases.
- D2 empty-string sentinel keeps the retired "Claude mode" claim in the table.
- D2 load-time validation can't see `vault://` plaintext.
- D3 all-functional can't reach the tamper window.
- D3 all-Go skips the real `niwa worktree create` config path.
- D4 package global leaves watch's safety unguardable by a test.

Each is taken from the corresponding decision report and was a real
contender. None needs strengthening.
