# Review Plan Category B (Design Fidelity): dispatch-sendmessage-approval

- Round: 1
- Mode: fast-path (single agent)
- Input type: design (full check)
- Upstream design: docs/designs/DESIGN-dispatch-sendmessage-approval.md (present, read in full)
- Upstream PRD: docs/prds/PRD-dispatch-sendmessage-approval.md (present, read in full)
- Issues reviewed: 1-8 (all body files present)

## Analysis

### 1. Interface and method name consistency

I checked every public name the issues carry against every section of the design
where it appears: Decisions 1-6, Decision Outcome, Solution Architecture (Components,
Key Interfaces, Data Flow), Implementation Approach, and Security Considerations. The
design spells each name the same way everywhere:

- `renderLaunchSettings(map[string]any) (string, bool)` in `dispatch_settings.go`
  (Decision 1, Components, Phase 2; Issue 3).
- `resolveDispatchInboundAcceptance(flag *bool, global config.GlobalSettings) inboundResolution`
  with fields `on`, `source`, `overrodeMachineOn` (Summary; Issues 2, 4).
- `inboundApplied`, `rcInjected`, `hostGlobal` (Summary, Data Flow; Issues 3, 4, 5, 6).
- `showInboundExplanation(w io.Writer, dir string, dirErr error, isTTY func() bool)`
  (Components, Key Interfaces; Issue 6).
- `inboundGuideURL`, one URL (Key Interfaces; Issues 4, 6, 7, 8).
- `DispatchInboundAcceptance` / `"dispatch-inbound-acceptance"` / `RouteLaunch`, row 25
  (Decision 2, Components; Issue 4).
- `config.CrossSessionInboundKey`, `GlobalSettings.AcceptSessionMessagesOnDispatch`, and
  TOML key `accept_session_messages_on_dispatch` (Summary, Components, Phase 1; Issues 2, 4).
- `SessionMapping.AcceptsSessionMessages` (omitempty) and
  `InstanceRecord.AcceptsSessionMessages` (always present), both with JSON
  `accepts_session_messages` (Decision 4, Key Interfaces; Issue 5).
- `sessionReachDenyMatcher = "SendMessage|SendFile|RemoteTrigger|ListAgents"` and
  `sessionReachDenyHook()` (Decision 5, Components, Security; Issues 1, 8).
- Marker name `accept-session-messages-notice` (Decision 3, Key Interfaces, PRD R11;
  Issues 6, 7, 8).

Every fixed string (flag help, audit line with both source parentheticals, override
line, warning format, and the explanation with both closing sentences) is quoted
identically in Key Interfaces and in Issues 4, 6, 7 and 8. The strings also satisfy
the PRD's substring requirements (R7, R8, R10, R13). I found no second spelling
anywhere in the design.

### 2. Behavioral contradictions across issues

- Remote control and keep-alive: Issue 3 keeps `rcInjected` tied to remote control's
  own inject decision. Issue 4 adds only the inbound key and restates the same rule.
  They're consistent, and they match Decision 1 and D3.
- Stderr placement: Issue 4 puts the audit and override line between step 12 and
  step 13. Issue 6 prints the explanation right after it when no attach follows, or
  after `dispatchAttach` returns. This matches the Summary, Data Flow steps 6 and 8,
  and Decision 3. I checked it against `dispatch.go`: step 14 branches on
  `LaunchForeground`, then `!spec.ResumeDuringTurn`, then `!dispatchDetach`, which are
  the three no-attach and attach conditions Issue 6 names.
- `step 11` literal: Issue 4 delivers `inboundApplied` in scope, and Issue 5 writes it
  into the mapping. No issue recomputes it, so the audit line, the record and the
  explanation agree.
- Warning conditions: Issue 4 warns only when the flag is true and the behavior isn't
  deliverable. It prints the override line only when the behavior was deliverable.
  This matches Decision 2, the Summary, and PRD R8 and R13. Issue 7's Codex scenarios
  assert the same thing.
- Prompt separator: Issue 3 applies it to every Claude launch, including watch's.
  Watch passes `claudeLaunchSpec()` to `dispatchLaunch` (`watch.go` ~581 and ~844), so
  that claim holds. D11's "watch unaffected" covers only the new resolution, which
  Issue 4 confines to `runDispatch`. Issue 7's watch-site unit test asserts it.
- Review deny hook: Issue 1 applies it in all three reachable `(sandbox, ask)`
  combinations. In `ApplyReviewSettings`, `ask` only means something when `sandbox`
  is true, so `(false, true)` isn't a real mode. Issue 1 checks by matcher and
  command, as Decision 5 and Security say.

No two issues implement mutually exclusive behavior for the same component.

### 3. Configuration and schema consistency

- The TOML key, the Go field and the `*bool` type match across Summary, Components,
  Key Interfaces, and Issues 2, 4, 7 and 8.
- The JSON field `accepts_session_messages`, omitempty on the mapping and always
  present on the instance record, matches across Decision 4, Key Interfaces, PRD R9,
  and Issues 5 and 7.
- The launch document's keys are `crossSessionInbound: "accept"` and
  `remoteControlAtStartup: true`, marshaled with sorted keys, consistently throughout.

### Factual checks against the code

- R15 and the `hostGlobal` hoist: `runDispatch` loads `config.toml` best-effort at
  step 2a. Remote control is gated on `gcErr == nil`. `realProvisionInstance`
  tolerates a load error (`if globalCfg, gErr := ...; gErr == nil`). So an unreadable
  or invalid `config.toml` doesn't fail the dispatch, and the claim that it "counts
  as absent, flag still applies" is achievable.
- The code matches the design's claims on these points:
  - `noEgressSandboxStanza`'s `network` map holds only `allowedDomains`.
  - `IsStderrTTY` is a stubbable var, and `triBoolValue` exists.
  - `RouteLaunch` exists.
  - Today there are 24 capability rows and the Codex column is 15 implemented and
    9 unavailable.
  - The "Amendment to rows 18 and 19" heading exists.
  - The containment test helpers Issue 1 cites exist.

### Non-critical observations (not Category B findings)

- Issue 6's Downstream Dependencies section says Issue 7 "asserts ... that four
  parallel dispatches all succeed". Issue 7 has no parallel-dispatch scenario. The PRD
  has an automated acceptance criterion for four parallel terminal dispatches. The
  design covers concurrency only as Phase 5 unit tests, and Issue 6 covers it at the
  helper level with 8 goroutines. This is a coverage or scope question for
  Categories A and C, not a contradiction between two behaviors.
- In its Phase 6 prose, the design calls the watch-site unit test "the Phase 3 unit
  test", while Phase 3's deliverables don't list it and Phase 6's say "if not already
  in Phase 3". The plan settles this by putting the test in Issue 7. That's a
  placement ambiguity with no behavioral effect.

## Result

```yaml
critical_findings: []
```

Confidence: high. The design and PRD were both available and read in full, all
eight issue bodies were present, and I checked the load-bearing factual claims
against `internal/`.
