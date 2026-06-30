# Round 2 Maintainability Review — remote-control by default on dispatched workers

Scope: verify Round-1 B1 fix (triple-duplicated key + missing end-to-end test), then
re-scan for remaining maintainability blockers. Worktree: `rc-by-default-explore`,
fix commit `a4af4a1`.

## B1 verification — CLOSED

The spelling `"remoteControlAtStartup"` now has a single source of truth:
`config.RemoteControlAtStartupKey` (`internal/config/config.go:329`). Of the four
sites that must agree, three reference the const directly:

- emit: `internal/workspace/materialize.go:425,429,431`
- inject: `internal/cli/dispatch_remotecontrol.go:16` (`remoteControlSettingsJSON`)
- end-to-end + tag tests reference the const

The one site that cannot reference a const — the JSON struct tag at
`internal/cli/dispatch_plugins.go:143` (Go forbids const struct tags) — is pinned by
a test.

Would a one-sided rename now fail a test? Yes, in every direction:

- Rename the const value only: `TestRemoteControlKey_EndToEnd_MaterializeReadBack`
  (`dispatch_remotecontrol_roundtrip_test.go:20`) materializes with the new key and
  reads back through the unchanged struct tag -> `RemoteControlAtStartup` stays nil ->
  fails at line 51. `TestInstanceSettings_TagMatchesKey` (line 63) also fails because
  the marshaled literal tag no longer matches `m[const]`.
- Rename the struct tag literal only: both tests fail again (read-back nil; marshaled
  key no longer found at `m[const]`).

The end-to-end test drives the real path `[claude.settings] -> InstallWorkspaceRootSettings
-> readInstanceSettings`, not a hand-rolled stand-in. I confirmed the referenced
functions exist with matching signatures:
`workspace.InstallWorkspaceRootSettings` (`internal/workspace/workspace_context.go:242`)
and `readInstanceSettings` (`internal/cli/dispatch_plugins.go:167`). B1 is genuinely
closed.

## Round-1 non-blocking notes — status

- **N1 (env coupling)** — Addressed. `resolveDispatchRemoteControl` and
  `apiKeyAuthForced` take the env slice as an explicit parameter instead of reading
  `os.Environ()` internally, and `dispatch.go:226-229` carries a comment requiring the
  eligibility check and the worker launch to inspect the same env source. The contract
  is now visible in the signature and the call site. **NON-BLOCKING** (resolved).

- **N2 (truth-table comment)** — Addressed. `dispatch_remotecontrol_test.go:16-19`
  explains why only reachable equivalence classes are enumerated (host off/unset and
  downstream-decided short-circuit the later dimensions). The next reader will not
  wonder whether a missing cell is an oversight. **NON-BLOCKING** (resolved).

- **N3 (warning duplicated in guide)** — Still present.
  `docs/guides/remote-control-on-dispatch.md:55` reproduces the `apiKeyForcedWarning`
  const (`dispatch_remotecontrol.go:21`) verbatim inside an example output block, with
  no test or comment linking the two. If the const text changes, the guide silently
  drifts. This is a divergent-twin, but it is doc-vs-code, not code-vs-code: it cannot
  cause a wrong mental model that leads to a code bug — at worst the guide's example
  line goes stale. **NON-BLOCKING** (unchanged from Round 1; documentation drift only).

## New scan — naming, contracts, comments, test clarity

No new blockers. Spot checks:

- Naming: `resolveDispatchRemoteControl` returns `(inject bool, warning string)`; the
  DEFAULT-FILL-not-override semantics are documented at `dispatch_remotecontrol.go:23-36`
  and match the implementation. No name-behavior mismatch.
- Implicit contract: the nil-`instanceSettings` = "downstream unset" rule is stated in
  the doc comment (line 36) and pinned by `TestResolveDispatchRemoteControl_NilInstance`.
- Comment accuracy: the materialize comment (`materialize.go:418-424`) correctly
  describes "emit only when explicitly set; host default applied at the dispatch seam,
  not here." Matches the code.
- Error message: `materialize.go:429` names the offending key and the accepted values
  (`"true"`/`"false"`) and points at `[claude.settings]` — accurate, no misdirection.
- Test clarity: wiring tests (`dispatch_wiring_remotecontrol_test.go`) are named for the
  behavior they assert (`DownstreamOff_Wins`, `HostUnset_NoChange`) and the assertions
  match the names. `TestApiKeyAuthForced` covers the prefix-collision and empty-value
  edges. No test-name lies.

## Verdict

B1 is closed and survives a one-sided rename in either direction. N1 and N2 were
resolved; N3 remains a documentation-drift advisory only. Nothing blocks.

BLOCKING COUNT: 0
