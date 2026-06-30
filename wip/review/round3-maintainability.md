# Round 3 Maintainability Review — remote-control by default on dispatched workers

HEAD e76800f. Scope: `git diff main -- internal/ docs/`. Fresh skeptical pass on
naming, comment accuracy vs current code, test readability, implicit contracts.

## Prior-round comments re-verified (all still accurate)

1. **dispatch.go:226-229 step 9a env-coupling note** — ACCURATE. The comment
   claims `realDispatchLaunch launches with cmd.Env = os.Environ()` and that the
   resolver must inspect the same env. Confirmed at `dispatch_launcher.go:40`
   (`cmd.Env = os.Environ()`) and the resolver call at `dispatch.go:230` passes
   `os.Environ()`. Both sources match; the "keep these identical" warning names
   the exact invariant a future editor could break, and it is the one that
   actually holds.

2. **materialize.go:418-432 RC comment** — ACCURATE. States the key is emitted
   only when a user explicitly sets `[claude.settings].remoteControlAtStartup`,
   that nothing turns it on by default here, and that the host default is applied
   at the dispatch launch seam. Matches the code (`if rc, ok := cfg.Settings[...]`
   guard, no default branch). Pinned by `TestInstallWorkspaceRootSettings_NoRemoteControlByDefault`
   and `TestBuildSettingsDoc_RemoteControlAtStartup`.

3. **dispatch_remotecontrol.go:23-49 resolver doc comment** — ACCURATE. The
   "default-fill, never a forced override" description, the nil-inst = "downstream
   unset" note, and the "downstream off wins via its own settings.json" rationale
   all match the three-branch body. Behavior table fully covered by
   `TestResolveDispatchRemoteControl` (10 reachable equivalence classes) and
   `TestResolveDispatchRemoteControl_NilInstance`.

## Findings

**NON-BLOCKING — grep display artifact, not a real defect.** A content search
initially rendered `config.go:324` and `registry.go:32` as single-slash `/`
comment lines (would be a compile error). Read of the raw files confirms both are
correct `//` doc comments. No action; noted only so a later reviewer running the
same grep is not misled.

**NON-BLOCKING — divergent "downstream unset" representation across test and
prod (dispatch.go:225 vs dispatch_wiring_remotecontrol_test.go:53).** Production
passes `inst == nil` to the resolver when settings.json is absent
(`inst, _ := readInstanceSettings(...)` discards the error and the nil return).
The wiring tests instead pass `&instanceSettings{}` for the unset case. Both
resolve identically (the resolver only inspects `inst.RemoteControlAtStartup`,
which is nil either way), and the nil path is covered separately by
`TestResolveDispatchRemoteControl_NilInstance`, so this is not a gap — just a
representational twin a future editor should know is intentional. The resolver's
own "inst may be nil" doc comment already signals it.

## Confirmed clear (no change needed)

- Naming: `resolveDispatchRemoteControl`, `apiKeyAuthForced`, `inject`/`warning`
  returns, `remoteControlSettingsJSON`, `RemoteControlOnDispatch` vs
  `RemoteControlAtStartup` (host preference vs Claude settings key) all read true
  to behavior. The two-name split is deliberate and explained at the type.
- The single-source-of-truth `config.RemoteControlAtStartupKey` is enforced
  end-to-end by `TestRemoteControlKey_EndToEnd_MaterializeReadBack` and
  `TestInstanceSettings_TagMatchesKey` — this is the prior blocking key-consistency
  finding, now closed and guarded against one-sided rename drift.
- Test names match their assertions (HostOn_Injects, DownstreamOff_Wins,
  HostUnset_NoChange with byte-for-byte argv equality, APIKey_WarnsAndSkips,
  MalformedInstanceSettings_DegradesToInject, InvalidHostConfig_NoInjectNoFail).
- Implicit contracts are documented where they exist (env-source coupling,
  dash-free slug invariant, `claude --settings` outranking project settings.json).

## Verdict

Zero blocking. The prior blocking key-consistency finding remains resolved. The
two non-blocking notes are informational, not change requests.

BLOCKING COUNT: 0
