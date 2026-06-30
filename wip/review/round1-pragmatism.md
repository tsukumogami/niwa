# Pragmatism Review — remote-control by default on dispatched workers (round 1)

Scope reviewed: `internal/cli/dispatch_remotecontrol.go`, `internal/cli/dispatch.go` (step 9a),
`internal/workspace/materialize.go` (buildSettingsDoc remoteControlAtStartup block),
`internal/config/registry.go` (GlobalSettings field), `internal/cli/dispatch_plugins.go`
(instanceSettings field).

## Verdict: nothing blocks. One advisory.

The design is tight. It reuses existing infrastructure (`readInstanceSettings`/`instanceSettings`,
already present for plugin pre-warming) rather than adding new plumbing, and the new config field,
the settings emission, and the dispatch-launch seam are each minimal. All new code is wired and
exercised by tests — no dead code, no unused params.

---

## Findings

### 1. settings-vocabulary extension (buildSettingsDoc + instanceSettings) — NOT over-built
The override story in the feature spec ("inject when on AND not overridden downstream") *requires*
both halves:
- buildSettingsDoc must emit `remoteControlAtStartup` from `[claude.settings]` so a downstream
  `true` actually reaches the worker (it's the ONLY path for downstream-true to take effect, since
  niwa suppresses injection when downstream decided), and so a downstream `false` can win over the
  host default via the worker's own settings.json.
- instanceSettings reading it back is how the resolver distinguishes "downstream decided" from
  "unset" — the suppression signal.

Reading the *materialized* settings.json (post-overlay-merge) is simpler than re-resolving the
workspace config + overlays at dispatch time. Adding one `*bool` field to an existing struct is the
minimal extension. **No finding.**

### 2. ANTHROPIC_API_KEY eligibility check — minor scope creep — NON-BLOCKING (advisory)
Diagnosis: `apiKeyAuthForced` + `apiKeyForcedWarning` add env-prediction and a warning that exceed
the one-line feature ("inject a flag when on and not overridden"). The suppression is not
load-bearing for correctness — injecting `{"remoteControlAtStartup":true}` with an API key set is
harmless (the worker just wouldn't activate Remote); only the warning carries value. The check also
encodes an assumption about Claude Code's auth precedence (ANTHROPIC_API_KEY forces API-key auth,
which precludes claude.ai-login Remote) that could drift if Claude changes that behavior, at which
point the warning becomes stale/misleading.
Fix (optional): defer this until there's evidence users hit the confusing silent no-op; if kept,
it's small and inert (worst case: a stale warning), so it does not block. Genuine diagnostic value
is the reason this is advisory, not flagged for removal.

### 3. pure-resolver-helper split (resolveDispatchRemoteControl / apiKeyAuthForced) — justified
Diagnosis: a pure `(global, inst, env) -> (inject, warning)` function called once from dispatch.go.
This is not ceremony: it isolates 3-branch decision logic from the side-effectful launch path and is
covered by a focused table test (`dispatch_remotecontrol_test.go`) that would otherwise need a full
dispatch harness. `apiKeyAuthForced` is a clearly-named single-purpose predicate, also tested.
Extraction earns its keep. **No finding.**

### 4. Dead code / unused params — none
All three new symbols (`resolveDispatchRemoteControl`, `apiKeyAuthForced`,
`instanceSettings.RemoteControlAtStartup`) and the `GlobalSettings.RemoteControlOnDispatch` field are
referenced from production + tests. Every param is consumed. The dispatch step-9a degradation paths
(missing global config, unreadable instance settings) collapse to "no injection," preserving prior
behavior — appropriate, not gold-plated.

---

## Summary
- F1 settings-vocabulary extension: NOT over-built — no finding
- F2 ANTHROPIC_API_KEY eligibility check: NON-BLOCKING (advisory) — minor scope creep / drift risk
- F3 resolver/helper split: justified — no finding
- F4 dead code / unused params: none

BLOCKING COUNT: 0
