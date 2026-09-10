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

| G6.2 | docs/designs/DESIGN-inert-defaultmode-key.md | 2 | confirmed | Architecture review FAIL: apply fixes or rework? |

## G6.2 -- Architecture review findings applied in place

The architecture reviewer returned FAIL on two blocking findings, both small.
It confirmed the phase ordering keeps the `@critical` scenarios green and found
no simpler architecture.

| Source | Finding | Action | Applied |
|--------|---------|--------|---------|
| Architecture (blocking) | Phase 3 can't observe the `workspace-config-sources` `@critical` scenario without editing its `workspace.toml` body, which AC19 forbids | The assertion step checks `.niwa/instance.json` records `claude_permissions: "bypass"` | [x] |
| Architecture (blocking) | Phase 1 won't compile: the resolver validates through a Phase 3 function | The resolver maps to literals and doesn't validate; the root materializer already rejects invalid values before state is saved | [x] |
| Architecture | `derivePermissionMode` promises a warning but takes no writer | Made pure over the recorded value; `runDispatch` loads and warns | [x] |
| Architecture | Test fakes write no `instance.json`, so the warning would fire suite-wide | A missing state file is silent; only a read/parse error warns | [x] |
| Architecture | "Declaration-driven" tests don't name their seam | Real `Applier.Create`, per `allow_missing_secrets_test.go` | [x] |
| Architecture | Fourth `buildDispatchPassthrough` caller (a test) unmentioned | Named in Phase 2 | [x] |
| Architecture | The "shared helper" claim doesn't match the code | Restated as a shared input, with an S1-S9 agreement test | [x] |
| Architecture | Goldens are hashes; "review the diff" shows nothing | Review the live bytes printed on mismatch | [x] |
| Architecture | S9 needs an instance apply; AC12 should run per location | Both added | [x] |
| Architecture | AC16 and AC19's parse test not in any phase | Added to Phase 3 | [x] |
| Architecture | Stale doc comments in `dispatch_plugins.go`; explicit-flag scenario lacks "exactly one" | Phase 2 and Phase 4 | [x] |
| Security (Phase 5) | Vault-safe error; same-process property; watch tests on a real materialization; watch side effects | Folded in earlier this phase | [x] |

**Decision.** Apply in place; the fixes are local and don't reopen a decision,
so the architecture review is not re-run. The structural-format and security
Phase 6 reviewers run next against the revised document.

| G6.3 | docs/designs/DESIGN-inert-defaultmode-key.md | 2 | confirmed | Structural-format review: which of eleven minor findings to apply |

## G6.3 -- Structural-format review PASS; ten of eleven minor findings applied

| Source | Finding | Action | Applied |
|--------|---------|--------|---------|
| Structural-Format | Optional frontmatter order; `user_visible_surface` missing | `upstream` moved after `rationale`; `user_visible_surface: true` added (the design changes dispatch argv, error text, a stderr warning, and two guides). `decision_provenance` kept: the dispatch fallback reference requires it for inline-resolved decisions | [x] |
| Structural-Format | Claude Code version facts uncited | Cited the public 2.1.257 release notes and the PRD's recorded measurement | [x] |
| Structural-Format | Context restates PRD requirements R1-R11 in prose | **Not applied.** `/design` Phase 0 step 0.3 directs the design to carry the PRD's requirements in its own body rather than cite numbers only, because `/scope`'s consolidation judgment reads whether the design holds what the PRD was for. The reviewer's suggestion conflicts with the skill's own instruction; the instruction wins | [ ] |
| Structural-Format | `S1-S9` undefined at first use; step numbers `9a`/`9c`/`9d` | S1-S9 defined in Decision 3; steps described by function | [x] |
| Structural-Format | Same-process statement repeated three times; Summary repeats Architecture | One full statement in Security Considerations; Components points to it; Summary shortened | [x] |
| Structural-Format | Phase 1 says "no output changes" | Reworded: no settings document changes; `instance.json` gains a field the goldens don't hash (verified: no characterization file references `instance.json`) | [x] |
| Structural-Format | Data Flow step 1 omits `[instance.claude.settings]` | Added | [x] |
| Structural-Format | Decision Drivers not numbered | Not applied: optional in the reference | [ ] |

The rewrite leaves the Security Considerations section's substance unchanged,
so the in-flight security reviewer's read is still current.
