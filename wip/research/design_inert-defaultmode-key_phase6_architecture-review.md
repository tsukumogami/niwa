# Architecture review: DESIGN-inert-defaultmode-key

Scope: Solution Architecture and Implementation Approach, checked against
`docs/prds/PRD-inert-defaultmode-key.md` and the code under `internal/`. File
and line references are to the worktree at review time.

## 1. Is the architecture clear enough to implement?

Mostly yes. A developer could start from Solution Architecture alone: every
new symbol has a signature, a home file, and a stated behavior, and the data
flow names the real seams. The claims that matter hold up against the code:

- `buildSettingsDoc` reads `cfg.Settings["permissions"]` through
  `maybeSecretString` and the `permissionsMapping` table
  (materialize.go:310-315, 680-702), and builds the worktree-delegation
  `deny` list into the same map (materialize.go:704-716). Swapping the table
  for `claudeDefaultMode` is a local edit, as described.
- The instance pipeline resolves the effective config with
  `ResolveAndMergeEffectiveConfig` at apply.go:1415, after the workspace
  overlay merge at apply.go:1191. It hands that same `effectiveCfg` to
  `RootSettingsMaterializer` at apply.go:1587-1592, and that materializer
  calls `MergeInstanceOverrides(cfg)` (workspace_context.go:340) before
  building the document (workspace_context.go:431). The root settings step
  isn't gated by agent. So `MergeInstanceOverrides(effectiveCfg)` really is the
  input the instance-root document resolves from, and a resolver placed right
  after line 1415 sees the same value, with vault values already revealed.
- The dispatch derivation at dispatch.go:529-557 reads `inst.Permissions`
  from `readInstanceSettings`. The `readInstanceSettings` result stays in use
  for remote control (dispatch.go:589) and keep-alive, as the design says.
- `buildDispatchPassthrough` reads the package global
  `dispatchPermissionMode` implicitly (dispatch.go:974-990). Watch gets an
  empty value only because nothing in its process sets that global
  (watch.go:576, 836), exactly as Decision 4 describes.

Three places are vaguer than they should be:

- **`derivePermissionMode` has no writer.** The design says a `LoadState`
  failure "prints a warning", and the Consequences section promises it names
  the state file. But the signature `derivePermissionMode(instancePath,
  explicit string, flags agentplan.LaunchFlags) (string, bool)` has nowhere
  to print except `os.Stderr`. Every other dispatch notice goes through
  `cmd.ErrOrStderr()`, which is what the dispatch tests capture. Either add
  an `io.Writer` parameter or return the error for `runDispatch` to print.
- **"RootSettingsMaterializer and the pipeline both derive the instance-root
  posture from this helper"** doesn't describe a real code path.
  `RootSettingsMaterializer` passes the whole `effective.Claude.Settings` map
  to `buildSettingsDoc`, which reads `permissions` itself
  (workspace_context.go:431-441). Nothing in that materializer would call
  `instancePermissionsPosture` unless its structure changes. The actual
  guarantee is that both read `MergeInstanceOverrides(effectiveCfg)` on the
  same `effectiveCfg`. The design should say so, say that
  `RootSettingsMaterializer` is otherwise unchanged, and pin the no-divergence
  claim with a test instead of asserting it.
- **"sets `pipelineResult.claudePermissions` ... after
  `ResolveAndMergeEffectiveConfig`"**: `pipelineResult` is only built at the
  pipeline's single return (apply.go:2094-2108). The value has to be held in
  a local from line ~1416 to the return. That's trivial, but the wording
  suggests a struct exists earlier than it does.

## 2. Missing components or interfaces

**Call sites of `buildDispatchPassthrough`.** There are three production
callers: dispatch.go:564, watch.go:576 (the `--resume` continuation), and
watch.go:836 (the fresh review). The design accounts for all three. There's
also a fourth caller the design doesn't mention, a test at
`internal/cli/dispatch_wiring_remotecontrol_test.go:127`
(`buildDispatchPassthrough(claudeLaunchSpec().Flags, "", "")`). Phase 2 won't
compile until it gets the fourth argument. Decision 4's "three call-site
edits" is really four.

**`dispatchPermissionMode` writers after the change.** The Cobra binding
(dispatch.go:28) and the test harness (dispatch_test.go:95-155, 570;
dispatch_permissionmode_test.go:55) are what's left. That fits "Cobra keeps
binding it and nothing else writes it", apart from test setup, which is fine.

**Does the pipeline have the effective config at that point?** Yes, as shown
above: apply.go:1415, overlay already merged at apply.go:1191, and
`[instance]` overrides applied by `MergeInstanceOverrides` (override.go:176-215,
which copies `Instance.Claude.Settings` over the workspace settings).

**Do both Create and Apply persist it?** Both build `InstanceState` fresh
rather than copying a loaded one: Create at apply.go:567-584 and Apply at
apply.go:764-781. Each copies `result.shadows` and `result.trustKeys`
explicitly, so adding `ClaudePermissions: result.claudePermissions` at both
sites is the complete persistence change. Those are the only two instance
`SaveState` calls (apply.go:586, 783). The only other `SaveState` calls are
the workspace-root disclosure save (apply.go:2737) and init's root state
(init.go:772). Dispatch provisions through `realProvisionInstance`
(instance_from_hook.go:428), which uses `workspace.NewApplier(...).Create`,
and watch provisions through the same function (watch.go:790). So the field
is on disk before `runDispatch` reaches step 9a, as the design claims.

**Is the workspace-root document left out of instance state?** Yes.
`writeRootSettings` (root_materializer.go:237-265) doesn't run inside
`runPipeline`. The workspace-root state is written only by `buildInitState`
(init.go:765-775) and by `saveWorkspaceRootDisclosures`, which loads the
existing root state and merges only `DisclosedNotices`
(apply.go:2718-2738). Neither touches a posture field. The one edge case is
the single-instance layout, where the comment at apply.go:2722-2728 says the
instance state file is the workspace-root state file. There the instance
posture is recorded, which is correct, but a sentence in the design would
keep someone from "fixing" it later.

**The fake provisioner and the new warning.** `installDispatchFakes` creates
`<instance>/.niwa/` but writes no `instance.json` (dispatch_test.go:113-121).
Under the design every dispatch unit test that uses that harness hits a
`LoadState` failure, and the new stderr warning fires. I found no dispatch
test asserting an empty stderr (they all use `strings.Contains`), so nothing
breaks. It's still a warning on the normal test path, and it would also fire
in production for anything that provisions without Create. Recommend staying
silent on `os.IsNotExist` (or only warning on parse or schema errors), or
having the fake write a minimal state file.

**What the declaration-driven dispatch tests plug into.** Phase 2 says the
seven fixture tests become "declaration-driven", and the design's own
argument against the provisionResult option is that a fake would invent the
posture. A rewritten test whose fake provisioner hand-writes `instance.json`
would invent it just the same, one file over. The design should name the
seam: a fake `provisionInstanceFunc` that runs a real
`workspace.NewApplier(stub).Create(...)` against a `workspace.toml` in a temp
dir. There's precedent in `internal/cli/allow_missing_secrets_test.go:235-244`,
which uses a stub repo lister and a real `Create`. The same approach is what
AC7's tamper test needs.

**Dangling reference after the deletion.** `readInstanceSettings`' doc
comment points at `internal/workspace/permissions.go`
(dispatch_plugins.go:222-227), and the `instanceSettings` type comment still
lists "permissions declarations" (dispatch_plugins.go:183-185). The design
only mentions the field comment. Both comments need editing in Phase 2.

## 3. Phase sequencing

The central ordering claim is right. The reader's move (Phase 2) lands ahead
of the producer's change (Phase 3), so `@critical` dispatch scenarios stay
green:

- After Phase 1, output is unchanged and nothing reads the field. The two
  `@critical` dispatch scenarios (dispatch.feature:52-89) still pass through
  the old settings read.
- After Phase 2, the derivation reads `ClaudePermissions` (set by Phase 1).
  S1 still gets `--permission-mode bypassPermissions`, and the explicit-flag
  scenario still gets `acceptEdits` with no `bypassPermissions`. The document
  still says `bypassPermissions`, but nothing reads it.
- After Phase 3, the document stops saying `bypassPermissions`, and the
  derivation doesn't care. The only other `@critical` scenario that reads the
  value is workspace-config-sources.feature:138-165, and Phase 3 updates it
  in the same commit.

The problems:

- **Phase 1 depends on a Phase 3 symbol.** Phase 1 adds
  `instancePermissionsPosture`, which "validates through
  `claudeDefaultMode`". Phase 3 is where `claudeDefaultMode` gets introduced.
  As written, Phase 1 won't compile. Fix it one of two ways. Either introduce
  `claudeDefaultMode` in Phase 1 without wiring it into `buildSettingsDoc`
  (Phase 3 then swaps the call), or skip validation in the resolver
  altogether. The second is simpler: an invalid instance-level value already
  fails the pipeline in `RootSettingsMaterializer` (apply.go:1593-1595),
  before `SaveState`, so the recorded value can never be invalid.
  Validating early in the pipeline also moves the failure ahead of cloning,
  which contradicts Phase 1's "no output changes" in a small way.
- **Phase 3 conflicts with AC19.** The `@critical` scenario at
  workspace-config-sources.feature:138-165 force-pushes a config body with
  `permissions = "bypass"`, then asserts the workspace-root settings contain
  `bypassPermissions`. AC19 forbids changing any `workspace.toml` body in a
  functional scenario that existed before this change. After Phase 3 a
  `bypass` declaration writes nothing into the workspace-root document. The
  first body in that scenario has no `[claude.settings]`, so the root
  document is byte-identical before and after the push, and nothing in the
  root settings file can show that the change landed. The obvious rewrite
  (switch the body to `ask`, assert `"default"`) violates AC19. The design
  has to say what the marker is. A workable choice with the body unchanged
  is the instance's `.niwa/instance.json` `claude_permissions: "bypass"`
  after the apply. But that shifts the scenario's subject from the root
  document to the instance pipeline, and its comment block
  (workspace-config-sources.feature:165-173 and the issue #214 header) would
  need to be reworded to match. The other option is to record AC19 as not
  applying to this scenario, with the reason, and send that back to the PRD.
- **Phase 2 build break.** The remote-control wiring test at
  dispatch_wiring_remotecontrol_test.go:127 has to change in the same commit
  (see section 2).
- **The Phase 3 goldens are hashes.** `testdata/characterization/*.txt` store
  SHA-256 digests of file contents (for example
  `instance_managed_files.txt:3`). The fixture declares
  `permissions = "bypass"` (characterization_test.go:351), so the
  instance-root, both repo, and worktree settings hashes all change. "Review
  the diff" will only show new digests, which proves nothing about content.
  Pair the regeneration with a content assertion (no `defaultMode` in those
  files), or dump the documents in the test's failure output.

Each phase can be committed on its own once these fixes are in. Phase 4 adds
scenarios only, and Phase 5 touches docs and a feature comment only.

## 4. Simpler alternatives

The chosen shape is already close to minimal. It adds one state field,
reuses the existing `pipelineResult` carry, and swaps one table for one
function. Decision 1's rejected alternatives are argued correctly: the
provisionResult route puts posture in the fakes, and re-resolving at dispatch
duplicates the precedence and the vault work. Two small simplifications are
worth taking:

- **Drop validation from `instancePermissionsPosture`.** Record the resolved
  string as-is. `buildSettingsDoc` already rejects an invalid value for the
  same input before state is saved, so a second validation path adds nothing
  and creates the Phase 1 dependency. The helper then reduces to one
  expression, which could simply be written inline in the pipeline.
- **Make `derivePermissionMode` a pure function of the loaded state.** For
  example, `derivePermissionMode(state *workspace.InstanceState, explicit
  string, flags) (string, bool)`, with `runDispatch` doing the load and
  printing the warning through `cmd.ErrOrStderr()`. The derivation becomes
  trivially testable, the writer problem goes away, and the tamper test still
  runs against a real materialization by loading the state that Create wrote.

## 5. AC coverage by phase

| AC | Delivered by | Notes |
|---|---|---|
| AC1 | Existing `@critical` S1 + Phase 4 (remote control) | OK |
| AC2 | Existing `@critical` scenario + Phase 4 | Existing scenario lacks "exactly one `--permission-mode`"; Phase 4 must add a count step and apply it here |
| AC3 | Phase 4 (explicit flags) | Needs the same count step |
| AC4, AC5 | Phase 4 | OK |
| AC6 | Phase 4 (personal overlay step exists: critical-path.feature:177) | OK |
| AC7 | Phase 2 | Needs the real-Create seam named (section 2) |
| AC8 | Phase 4 (fake codex step exists: workspace-config-sources.feature:119) | OK |
| AC9 | Phase 2 | OK |
| AC10, AC11 | Phase 4 | S9's worktree cell is checked "after an instance apply"; the outline as described runs only `niwa init` and `niwa worktree create`, so S9 needs an extra `niwa apply` step |
| AC12 | Phase 3 | The design frames it as a `buildSettingsDoc` differential; AC12 requires it per location, over the four real documents |
| AC13, AC14 | Phase 3 | OK |
| AC15 | Phase 2 | Partially existing (list_resume_test.go, dispatch_reentry_test.go) |
| AC16 | **None** | Decision 3 lists "watch's review settings applied on top of a real materialization", but no phase names it; it belongs in Phase 3, once the S1/S2/S3 documents are final |
| AC17 | Phase 2 | Plus the stale doc comments in dispatch_plugins.go |
| AC18 | Phase 5 | OK (seven documents match the PRD list) |
| AC19 | **None**, and Phase 3 conflicts with it | No phase has the four-level parse unit test; see the workspace-config-sources conflict |
| AC20 | Phase 2 | Holds only if the rewritten tests use a real `Create` |
| AC21 | Every phase | OK |

## Verdict

FAIL

The architecture is sound and the reader-before-producer ordering is right.
It fails on one unresolved conflict: Phase 3's instruction for the
`workspace-config-sources` `@critical` scenario can't be followed without
breaking AC19, and the design doesn't say which marker to use. There's also
a compile dependency between Phases 1 and 3, and two ACs have no phase. All
of it is fixable in a few sentences.

## Findings

1. **blocking**: Phase 3's "marker that doesn't depend on
   `bypassPermissions`" for workspace-config-sources.feature:138-165 can't
   use the workspace-root settings file without changing the scenario's
   `workspace.toml` body, which AC19 forbids. With the body unchanged, that
   file is identical before and after the push. **Fix:** name the marker
   (for example, the instance's `.niwa/instance.json`
   `claude_permissions: "bypass"` after the apply) and reword the scenario
   comment. Or record an explicit AC19 exception for this scenario and send
   it back to the PRD.
2. **blocking**: Phase 1's `instancePermissionsPosture` validates through
   `claudeDefaultMode`, which Phase 3 introduces, so Phase 1 won't compile.
   **Fix:** drop validation from the resolver (the root materializer already
   rejects invalid values before `SaveState`), or move `claudeDefaultMode`'s
   introduction into Phase 1.
3. **minor**: AC16 (watch review settings on a real S1/S2/S3
   materialization, both postures) appears in Decision 3 but in no phase.
   **Fix:** add it to Phase 3's deliverables.
4. **minor**: AC19's four-level parse unit test isn't in any phase.
   **Fix:** add it to Phase 1 or Phase 3.
5. **minor**: The `buildDispatchPassthrough` caller at
   dispatch_wiring_remotecontrol_test.go:127 isn't listed, so Phase 2 breaks
   the build until it's updated, and "three call-site edits" is really four.
   **Fix:** list it in Phase 2's deliverables.
6. **minor**: `derivePermissionMode` promises a stderr warning but takes no
   writer. **Fix:** add an `io.Writer`, or have `runDispatch` do the
   `LoadState` and warning, passing the loaded state to a pure derivation
   function.
7. **minor**: The dispatch test fake writes no `instance.json`
   (dispatch_test.go:113-121), so the new warning fires across the dispatch
   unit suite. **Fix:** stay silent when the state file doesn't exist, or
   have the fake write a minimal state file.
8. **minor**: Phase 2's "declaration-driven" tests don't name their seam, and
   a fake that hand-writes `instance.json` is the invented-posture pattern
   Decision 1 argues against. **Fix:** specify a fake
   `provisionInstanceFunc` that runs a real `Applier.Create`, following
   allow_missing_secrets_test.go:235-244.
9. **minor**: The claim that `RootSettingsMaterializer` "derives the posture
   from this helper" doesn't match the code, since `buildSettingsDoc` reads
   the settings map itself. **Fix:** say the materializer is unchanged, that
   the shared input is `MergeInstanceOverrides(effectiveCfg)`, and add a test
   asserting the recorded field matches the written instance-root document
   for S1-S9.
10. **minor**: The characterization goldens are SHA-256 digests, so
    "regenerate and review the diff" can't show content. **Fix:** add a
    content assertion for the settings files in the characterization
    fixture.
11. **minor**: AC10's S9 worktree cell needs an instance apply after
    `niwa worktree create`, and AC12 is per location, not per function.
    **Fix:** add the apply step to the S9 example, and run the AC12
    differential over the four materialized documents.
12. **minor**: Two comments go stale when `permissions.go` is deleted: the
    `readInstanceSettings` doc comment points at it, and the
    `instanceSettings` type comment still lists permissions
    (dispatch_plugins.go:183-185, 222-227). **Fix:** edit both in Phase 2.
13. **minor**: The existing `@critical` explicit-flag scenario doesn't assert
    "exactly one `--permission-mode`", which AC2 requires. **Fix:** Phase 4's
    new count step should be added to that scenario too.
