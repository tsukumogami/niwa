# Architecture Review: DESIGN-init-bootstrap-empty-source.md

Date: 2026-05-18
Reviewer role: architecture
Verdict: Approve with two advisory follow-ups (no Blocking items)

## 1. Architecture clarity

The design is concrete enough to implement. Each component has a named
file, exported type, and call shape. The Data Flow section walks the
happy path step-by-step in 11 numbered steps mapped to PRD R-numbers.
The Component overview enumerates every file touched. Call sequence
is well-specified: `runInit` → `classifyMaterializeError` → (nil,nil)
signal → `RunBootstrap` → `ScaffoldFromSource` → `Applier.Create` →
`mcp.CreateSession` → commit via `GitInvoker` → `writeLandingPath` →
success block.

The one place where the design names a tension and resolves it
inline is the BranchName-vs-BranchPrefix mismatch (Two-phase sid
handshake section). The exposition uses `BranchName string`; the
implementation pins `BranchPrefix string`. The design states this
explicitly so an implementer reading "Decision C" doesn't get
mislead. This is correct documentation hygiene.

## 2. PRD coverage walk (R1–R26, N1–N5)

| Req | Design home | Status |
|-----|-------------|--------|
| R1 | RunBootstrap orchestration (bootstrap.go) | Grounded |
| R2 | runInit name derivation (Phase 2) | Grounded |
| R3 | ScaffoldFromSource + Appendix A byte-equality | Grounded |
| R4 | Scaffold writes `[[sources]] repos = [...]`; Applier.Create's runPipeline enforces | Grounded |
| R5 | SessionLifecycleState.BranchName + EffectiveBranchName | Grounded |
| R6 | RunBootstrap calls existing Applier.Create + factored CreateSession | Grounded |
| R7 | Three-layer defer (workspaceCreated, instanceCreated, sessionCreated, CreateSession-internal) | Grounded |
| R8 | runInit preflight via existing CheckInitConflicts | Grounded |
| R9 | runInit src.IsGitHub() check + RunBootstrap re-check | Grounded |
| R10 | classifyMaterializeError arm 3 (*github.StatusError 401/403) | Grounded |
| R11 | classifyMaterializeError arm 4 (404) | Grounded |
| R12 | classifyMaterializeError arm 1 (AmbiguousMarkersError) | Grounded |
| R13 | runInit TTY dispatch (Phase 2) | Grounded |
| R14 | ScaffoldFromSource | Grounded |
| R15 | ScaffoldOptions.IncludeGitkeep | Grounded |
| R16 | ScaffoldOptions.Private (bool, structural) | Grounded |
| R17 | ScaffoldFromSource soft-fail + R17 note emission | Grounded |
| R18 | RunBootstrap commit step via GitInvoker, no --author/env | Grounded |
| R19 | runInit success-block emission (Appendix B) | Grounded |
| R20 | writeLandingPath invocation | Grounded |
| R21 | runInit host check ordering | Grounded |
| R22 | GitInvoker interface (Decision B) | Grounded |
| R23 | Exit-code mapping via InitConflictError.ExitCode field (Phase 2) | Grounded |
| R24 | RunBootstrap has no git push call; R24 AC asserts | Grounded |
| R25 | runInit mutual-exclusion check (Phase 2) | Grounded |
| R26 | C1 scaffold emits [channels.mesh]; Applier.Create runs InstallChannelInfrastructure | Grounded |
| N1 | Out of scope (deferred per PRD) | Acknowledged |
| N2 | classifyMaterializeError precedence order | Grounded |
| N3 | Sentinels deferred (snapshotwriter.go uncomment confirmed) | Grounded |
| N4 | Branch-name format is durable; design doesn't change it | Grounded |
| N5 | Token never persisted; recursive-grep AC | Grounded |

No PRD requirement is unhomed.

## 3. Layering and dependency direction

**Concern raised by design itself (Consequences > Negative bullet 2):**
`internal/workspace/bootstrap.go` will import `internal/mcp`. This is
a new edge in the dependency graph. The design acknowledges it
explicitly and offers a mitigation in the Mitigations section
("introduce a workspace-package interface that handlers_session.go
satisfies via an adapter").

Trace: today's `internal/mcp` imports types/helpers from
`internal/workspace` (it uses workspace types in handler bodies); the
proposed change adds `internal/workspace` → `internal/mcp`. That
would be a cycle if the existing mcp→workspace import is not
abstracted first.

I checked: the design's Mitigations section names this and defers
the decision to Phase 4 ("Phase 4 should evaluate the trade and
pick the cleaner shape"). This is **Advisory, not Blocking** — the
design is honest about the cycle risk and provides a fallback. But
the implementation must NOT land Phase 4 without resolving the
direction: either the mcp→workspace edge has to disappear (unlikely
for v1) or a workspace-side interface that mcp implements must be
introduced. Recommend Phase 4 commit boundary include the chosen
shape.

**Other layering observations:** clean.

- `internal/cli` → `internal/workspace` → `internal/github` is the
  established direction.
- `internal/cli/init_classifier.go` lives in cli, consuming workspace
  types (InitConflictError) and github types (StatusError) — correct
  direction.
- `internal/github` is a leaf in this design (no imports of higher
  layers).

## 4. Interface contracts

**BootstrapParams (6 fields).** Wide but justified. The design's
Negative bullet acknowledges this. Alternative (positional args) is
worse. No abstraction is premature: each field has a documented
consumer.

**GitInvoker.** Single-method interface with one production impl. This
is a test seam, not a polymorphism story — and the design is clear
about that. The CommandContext signature returns `*exec.Cmd` which
is the right shape for the recorder.

**CreateSession / CreateSessionParams.** Factoring from
`handleCreateSession`. The Two-phase sid handshake note (with
BranchPrefix instead of BranchName) is a concrete shape — implementer
won't have to guess.

**ScaffoldOptions.** Five fields, all named-parameter style, all
typed correctly. The `Private bool` field is the structural
enforcement point for R16 — moving this to a string field would
require a visible change.

***github.StatusError.** Carries StatusCode + URL + Body. The body is
explicitly diagnostic-only ("truncated"). Replaces four wrap sites in
fetch.go and the fifth in snapshotwriter.go:503. All five sites are
named.

No premature abstraction. No interface that has a single caller
masquerading as polymorphism — except GitInvoker, which is
explicitly a test seam.

## 5. Implementation phases

Five phases, each ending CI-green:

- **Phase 1 (error classification):** No user-visible change; pure
  refactor + new type. Classifier helper is unit-tested but not yet
  called. Green CI.
- **Phase 2 (flag surface):** Stub returns "bootstrap step=create:
  not implemented yet" for the NoMarker+bootstrap path. Adjacent
  failure modes (401/403/404/Ambiguous) now produce case-specific
  messages. Green CI.
- **Phase 3 (scaffold + GitInvoker):** ScaffoldFromSource unit-tested
  but not yet called by orchestrator. GitInvoker interface lands.
  Green CI.
- **Phase 4 (orchestrator):** Composition complete. RunBootstrap
  replaces stub. End-to-end happy path works. Layering decision (see
  §3) lands here. Green CI.
- **Phase 5 (AC + docs):** Full Gherkin matrix lands. Green CI.

Sequencing is correct: errors-and-flags-before-orchestrator is the
right order because Phase 4 needs Phase 1's classifier and Phase 3's
scaffold. The stub in Phase 2 keeps each commit boundary green.

## 6. Defer/cleanup contract walkthrough

Four cleanup layers (per Cleanup defers section):

1. `runInit` workspaceCreated defer
2. `RunBootstrap` instanceCreated defer
3. `CreateSession` internal rollback
4. `RunBootstrap` sessionCreated defer (post-CreateSession, pre-commit)

Walking partial-failure paths:

**Path A: init step fails before RunBootstrap returns.**
runInit's workspaceCreated defer fires. Workspace removed. Registry
not yet written. instanceCreated not armed (RunBootstrap not entered).
sessionCreated not armed. CreateSession-internal not entered. → exactly
1 defer fires. Correct.

**Path B: Scaffold write succeeds, Applier.Create fails.**
RunBootstrap armed instanceCreated immediately before Applier.Create.
The defer fires on the Applier.Create error return. Instance dir
removed. workspaceCreated stays armed in runInit until RunBootstrap
returns success — but RunBootstrap returns non-nil here, so
runInit's workspaceCreated also fires, removing the workspace dir.

Wait. Per R7: "create step fails after init succeeded: Keep
`<cwd>/<name>/.niwa/workspace.toml`, the workspace directory, and
the registry entry so the user can retry with `niwa create`."

So workspaceCreated must NOT fire on create-step failure. Per the
design (step 11 of Decision Outcome and Cleanup defers section 1):
"Disarmed on the RunBootstrap success path (set to false after the
call returns nil)." That's the wrong disarm trigger if R7 wants the
workspace preserved on create-step failure.

Reading more carefully: runInit's existing workspaceCreated defer at
init.go:215-226 — this design relies on whatever disarm logic
already exists in runInit. The design says "Disarmed on the
RunBootstrap success path" but R7 requires the workspace to be
preserved on create-step failure (RunBootstrap returns non-nil).

**This is a contract drift between the design's prose and R7.**

The fix is: workspaceCreated must be disarmed when RunBootstrap is
ABOUT TO BE CALLED (post-scaffold-write, pre-Applier.Create), not
"after RunBootstrap returns nil." Or equivalently, disarmed
immediately after ScaffoldFromSource succeeds — at that point the
init step has produced its durable artifacts (workspace.toml,
.gitkeep) and R7's "init succeeded" predicate is true.

Recommend: clarify the workspaceCreated disarm trigger in the
Cleanup defers section. The current wording ("set to false after the
call returns nil") preserves workspace only on full success — but
R7 wants it preserved on any failure after scaffold-write.

**Severity: Advisory.** The design's intent is correct (R7 is
walked through correctly elsewhere), but the defer-disarm trigger
language in §"Cleanup defers — who owns what" #1 says "Disarmed on
the RunBootstrap success path" which contradicts the rollback walk
in step 7 of Decision Outcome and the R7 wording in Notices.
Implementer following the literal Cleanup-defers wording will
delete the workspace dir on create-step failure, breaking R7.

**Path C: Applier.Create succeeds, CreateSession internal failure.**
instanceCreated stays armed (it was armed before Applier.Create);
Applier.Create returned nil; design says "disarm instanceCreated defer
(create-step success)" (step 6 of Data Flow). So instanceCreated is
disarmed. sessionCreated not yet armed (only armed after CreateSession
returns success). CreateSession's internal rollback fires. → exactly
1 defer fires (CreateSession internal). Correct.

**Path D: CreateSession succeeds, git commit fails.**
sessionCreated armed; fires. instanceCreated already disarmed.
workspaceCreated state: if we follow the design's stated disarm
trigger ("after RunBootstrap returns nil"), workspaceCreated is
still armed when RunBootstrap returns non-nil → it fires, removing
workspace. That contradicts R7 (session-step failure should preserve
instance and workspace). Same fix needed as Path B.

**Path E: Full success.** workspaceCreated disarmed; instanceCreated
disarmed; sessionCreated disarmed. No defer fires. Correct.

## 7. Critical chain validation (channels infrastructure)

PRD requires session-create to land a worktree under
`<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`. This requires
`<instanceRoot>/.niwa/roles/<repo>/` to exist (channel infra). The
design relies on Applier.Create's pipeline installing this when
`[channels.mesh]` is present in workspace.toml.

End-to-end trace:

1. **Scaffold `workspace.toml` has `[channels.mesh]`.** Appendix A
   of PRD has the line `[channels.mesh]`. Design step 5 confirms
   `ScaffoldFromSource` writes the Appendix-A body byte-for-byte.
   Phase 3 deliverables include "ScaffoldFromSource implements PRD
   Appendix A byte-for-byte." Acceptance criterion "[channels.mesh]
   block active" asserts post-scaffold parse yields
   `Channels.Mesh != nil && Channels.IsEnabled() == true`. **Link
   1: Documented.**

2. **RunBootstrap calls Applier.Create.** Design step 6 of Decision
   Outcome: "RunBootstrap invokes the create step by calling
   Applier.Create(ctx, cfg, configDir, workspaceRoot, instanceName)
   exactly as `niwa create` does (R6 parity)." Phase 4 deliverable:
   "RunBootstrap full body... Calls Applier.Create and
   mcp.CreateSession." **Link 2: Documented.**

3. **runPipeline sees channels enabled and calls
   InstallChannelInfrastructure.** Design step 6 continues: "writes
   instance state, and — because `[channels.mesh]` is active by
   default per R3/C1 — runs InstallChannelInfrastructure so
   `<instanceRoot>/.niwa/roles/<repo>/` exists." The Component
   overview cites `internal/workspace/channels.go` as
   `InstallChannelInfrastructure` invoked by Applier.Create's
   pipeline. Data Flow's Applier.Create sub-tree says: "writes
   `<instanceRoot>/.niwa/instance.json`" and "InstallChannelInfrastructure
   (R26 confirms no apply call; channels run via create's pipeline
   because the scaffold declares [channels.mesh])." **Link 3:
   Documented.**

4. **After create returns, `<instanceRoot>/.niwa/roles/<repo>/`
   exists.** Acceptance criterion "Happy path with positional name"
   asserts `<cwd>/my-project/<instanceName>/.niwa/roles/my-project/`
   directory exists. **Link 4: Documented.**

5. **CreateSession checks for that directory and passes the gate.**
   This is the link the design depends on but does not explicitly
   trace. The design says CreateSession is the factored body of
   today's `handleCreateSession`; that body's preflight check for
   `.niwa/roles/<repo>/` is inherited from the existing code. Design
   does not explicitly call out: "CreateSession's preflight will pass
   because Applier.Create has already created roles/<repo>/ via
   InstallChannelInfrastructure on the [channels.mesh] scaffold." It
   relies on R6 ("each chained step's success criteria match the
   corresponding standalone command's") to carry this implication.

**Verdict on Link 5: Implicit, not explicitly traced.** The chain
is sound — but the design does not name the CreateSession preflight
that consumes the roles/<repo>/ artifact. A reader who isn't already
familiar with handleCreateSession's body has to reason it through.

**Severity: Advisory.** Recommend a one-sentence addition to either
the Data Flow section or the Critical Validation chain (which
doesn't currently exist in the design) explicitly tracing
scaffold→pipeline→InstallChannelInfrastructure→roles/<repo>/→
CreateSession preflight pass. The current trace stops at "channels
run via create's pipeline" without closing the loop on the
CreateSession side.

## 8. Simpler alternatives considered

The design's "Considered Options" already walks A1/A2/A3, B1/B2/B3,
C1/C2/C3, D1/D2/D3 for placement, exec injection, branch-name
storage, and classifier seam. Each option is justified and the
rejected alternatives are named. I checked for missing alternatives:

- **Could `RunBootstrap` live in `internal/cli` (option A3) with a
  thin workspace-side adapter?** This would eliminate the
  workspace→mcp import. Rejected per the design's rationale that
  cli's role is flag/UX adaptation. Reasonable.
- **Could `BootstrapParams` be replaced by a builder?** Premature
  for v1. The struct is named-parameter style; future additions are
  additive. Reasonable.
- **Could the two-phase sid handshake be replaced by a one-phase
  approach (generate sid in caller, pass it in)?** This would
  require CreateSession to accept a pre-generated sid, which
  changes the existing session-machinery contract. The design's
  BranchPrefix approach is the lighter touch. Reasonable.

No structurally simpler alternative was overlooked.

## 9. Verdict

**Approve.** The design satisfies every PRD requirement at a
specific component/type/function. The four decisions (orchestrator
placement, exec injection, branch-name storage, classifier seam)
each have clear chosen options with named alternatives. The five
implementation phases each end CI-green.

Two advisory follow-ups, neither blocking acceptance:

1. **Clarify the workspaceCreated defer disarm trigger.** Section
   "Cleanup defers — who owns what" #1 says "Disarmed on the
   RunBootstrap success path." That wording contradicts R7's
   create-step / session-step preservation rules (workspace must
   survive any failure after the scaffold write). Recommended
   wording: "Disarmed immediately after ScaffoldFromSource returns
   nil — at that point R7's 'init succeeded' predicate is true, and
   subsequent create-step or session-step failure preserves the
   workspace dir."

2. **Explicitly trace the channels→roles/<repo>→CreateSession gate.**
   The design implicitly relies on R6 parity to argue that
   CreateSession's preflight passes; a one-sentence trace closing
   the loop ("CreateSession's existing preflight check for
   `<instanceRoot>/.niwa/roles/<repo>/` is satisfied because
   Applier.Create's runPipeline invokes
   InstallChannelInfrastructure for the [channels.mesh] scaffold")
   would make this explicit.

The MCP→workspace import direction is acknowledged in the design's
Negative consequences and Mitigations sections; deferring the
choice to Phase 4 is acceptable as long as the resolution lands
inside that phase.

No Blocking findings.
