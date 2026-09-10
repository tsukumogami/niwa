# Architecture review: DESIGN-dispatch-sendmessage-approval

Reviewed against the PRD's Requirements and Acceptance Criteria and against the
code in this worktree. Every claim below was checked in the source; line numbers
refer to the current tree.

## Verdict

The architecture is sound and mostly implementable as written. The builder, the
capability row, the marker beside `config.toml`, and the mapping and list fields
all fit the code's existing seams, and the central factual claims hold. Three
problems need a design change before implementation:

1. **The explanation would be consumed by `claude attach`.** Claude declares
   `ResumeDuringTurn: true` (`internal/agentplan/dispatch.go:383`), so a
   non-detached dispatch from a terminal runs `dispatchAttach` right after the
   post-mapping point (`dispatch.go:852-858`). The design prints the explanation
   and creates the marker at the post-mapping point. Neither the design nor the
   PRD accounts for the attach. So in the most common terminal flow the
   explanation prints a moment before Claude Code's TUI takes over the
   terminal, and the marker records it as seen anyway. That's the exact failure
   Decision 3 and User Story 7 exist to prevent.
2. **Phase 1 as written doesn't pass the test suite on its own.** It also
   declares the Claude row implemented before anything delivers it, which
   contradicts the declaration table's own rule.
3. **The functional coverage R18 requires has no harness for two of its
   cases.** No functional test runs `niwa watch`. The pty helper merges stderr
   into stdout, and the design doesn't mention either.

The rest are smaller gaps where an implementer would have to guess, plus
missing deliverables. They're listed below.

## 1. Where an implementer would have to guess

**Ordering of the post-mapping output relative to steps 13 and 14.** The Summary
says the lines are written "after the launch returns and the session mapping is
written". Step 12 (`dispatch.go:796-797`) is the natural place, but the design
never says whether they go before or after the stdout hints (step 13) or the
attach (step 14). With the attach problem above, the answer changes the
behavior. Recommendation: write the audit line or override line immediately
after step 12, before step 13. It's short, and `niwa list` also carries it.
Then place the explanation-and-marker step by launch outcome:
- If no attach follows (`--detach`, `!spec.ResumeDuringTurn`, or a foreground
  runner), call `showInboundExplanation` right after the audit line.
- If an attach follows, call it after `dispatchAttach` returns, on both its
  success and failure branches. The developer has the terminal back at that
  point, which is the only moment "shown at a terminal" is true.

A developer who never detaches (closes the window) doesn't get the marker,
which is the safe direction. Say this in Decision 3 and the Data Flow.

**"Records AcceptsSessionMessages on the mapping" after the mapping is
written.** The Summary ("After the launch returns and the session mapping is
written ... `runDispatch` records `AcceptsSessionMessages` on the mapping") and
Data Flow step 5 ("After the launch and the mapping write succeed, step 11
records `AcceptsSessionMessages`") both read as a second write after the
first. The field has to be set in the `workspace.SessionMapping` literal at
`dispatch.go:761-772`, beside `KeepAlive`, before `WriteSessionMapping`. The
stderr lines are what follow the successful write. Reword both places.

**What "the behavior took effect" is, as a variable.** The design needs one
boolean computed at step 9c: resolved on, deliverable, and the key actually
added to the builder. That boolean must flow into the mapping literal and gate
the audit line. It's analogous to `keepAliveArmed`. Name it (for example
`inboundApplied`) so the three consumers can't drift.

**How the override case is detected.** The resolver signature
`resolveDispatchInboundAcceptance(flag *bool, global config.GlobalSettings)
(on bool, source string)` can't express "the flag turned off a machine setting
of `true`". The caller would re-derive it from `flag` and `global`. Either
return it (for example a small struct `{on bool; source string;
overrodeMachineOn bool}`) or state the call-site condition explicitly:
`flag != nil && !*flag && global.AcceptSessionMessagesOnDispatch != nil &&
*global.AcceptSessionMessagesOnDispatch && deliverable`.

**`rcInjected` after the builder.** Keep-alive's gate
`remoteControlEnabled(rcInjected, inst)` (`dispatch.go:653`) must stay tied to
remote control's own inject decision, not to "the builder rendered a
document". If an implementer sets `rcInjected` from `render()`'s boolean, an
inbound-only document would arm keep-alive on a worker without remote
control, which breaks the R6 acceptance criterion. One sentence in Decision 1
fixes this.

**Where the zero `GlobalSettings` comes from at step 9c.** The design says
"an unreadable or malformed `config.toml` already yields a zero
`GlobalSettings` in `runDispatch`". That's true only from line 620, where
`hostGlobal` is built for step 9d, after step 9c. At step 9c the code gates on
`gcErr == nil` and reads `gc.Global` directly (`dispatch.go:588-593`). The
implementer has to hoist the `hostGlobal` block above step 9c. Remote control
must still be gated on `gcErr == nil`, which it effectively is, because a zero
`RemoteControlOnDispatch` means no inject. Say "hoist `hostGlobal` above step
9c and pass it to both resolvers".

**The config directory can't be resolved.** `config.GlobalConfigPath()`
returns `(string, error)`, and it fails when `HOME` is unresolvable and
`XDG_CONFIG_HOME` is unset. The design writes
`filepath.Dir(config.GlobalConfigPath())` as if it returned one value, and
says nothing about the error. Recommendation: print the explanation with the
non-terminal closing sentence and skip the marker. Also define "marker
exists" as `os.Stat(path) == nil`. Any stat error, including `EACCES` on an
unsearchable directory, should count as absent, so the unwritable-directory
acceptance criterion holds.

**Constant placement.** "Placed after `DispatchLaunch`" is ambiguous. In the
catalog slice it's harmless. In the `iota` const block it renumbers
`DirectoryTrust` (23 to 24) and `GitExcludeBookkeeping` (24 to 25), which
invalidates the "row 23/24" comments in `declaration.go` and `capability.go`
and the row numbers in `docs/prds/PRD-agent-capability-contract.md`. Numeric
values aren't persisted anywhere (checked), so there's no compatibility risk,
only cross-reference churn. Recommendation: append it as row 25 at the end of
both the const block and the catalog, or explicitly accept the renumbering and
list the comment and PRD updates.

**Where the `crossSessionInbound` spelling lives.** Remote control's key is
the named constant `config.RemoteControlAtStartupKey` (`config.go:449`), and
`dispatch_remotecontrol.go` builds from it so the flag, materializer, and
read-back share one spelling. The design leaves the new key as an inline
literal. Recommendation: add `config.CrossSessionInboundKey` beside it.

**Marker directory mode.** The design uses `os.MkdirAll(dir, 0o700)`, while
`writeGlobalConfigFile` creates the same directory with `0o755`
(`registry.go:344`). Whichever command creates `~/.config/niwa/` first decides
its mode. Pick one and say why; matching `0o755` is simplest.

## 2. Missing components and interfaces

**Tests that enumerate the capability table.** Phase 1 says "update the
capability tests that enumerate the table" without naming them. These fail on
the new row:
- `internal/agentplan/capability_test.go:12`: `TestAllIsTheClosedSet` wants 24.
- `internal/agentplan/capability_test.go` `codexFinalGaps` map and
  `TestCodexColumnTotals` (`wantImplemented, wantUnavailable = 15, 9`): the
  new Codex row makes it 15/10, and the map needs
  `DispatchInboundAcceptance: ReasonNoSuchConcept`.
- `internal/agentplan/gaplist.go` `gapSubjects` (the design mentions this) and
  `TestEveryCapabilityHasAGuideSubject`.
- Prose that states the counts: `capability.go:40` ("the 24 rows"),
  `declaration.go:70-72` ("Fifteen Codex rows are implemented and nine are
  unavailable"), and `capability.go:229` ("24 entries").

`contract_test.go` needs no edit; it derives its expectations from `All()`.

**Generated gap list.** `gaplist_test.go` diffs the rendered section against
`docs/guides/codex-agent.md` between its BEGIN/END GENERATED markers. The new
Codex row (no-such-concept) adds a line under "What doesn't apply to Codex",
so the guide must be regenerated with `go test ./internal/agentplan -run <drift
test> -update`. It's not in any phase's deliverables.

**Capability-contract PRD matrix.** `capability.go` names
`docs/prds/PRD-agent-capability-contract.md` as the authority for the closed
set, and the repo's practice is an amendment under the matrix when the set
changes (see the "Amendment to rows 2 and 18" and "Amendment to rows 18 and
19" notes at line 439 onward). Adding a 25th capability needs the same.

**`GlobalSettings` keys.** No test enumerates the fields. The existing keys
each have their own decode and round-trip test
(`registry_keepalive_test.go`, `registry_remotecontrol_test.go`), so Phase 1
should add a `registry_inbound_test.go` of the same shape. Also add
`accept_session_messages_on_dispatch` to the setter-less list in the
`SaveGlobalConfigTo` comment (`registry.go:303-304`), which the design does
mention. `docs/guides/codex-agent.md:103-104` lists "the other host-level
dispatch defaults"; adding the new key there is optional.

**Dispatch test harness.** The dispatch unit tests reset every flag global in
the shared helper (`dispatch_test.go:~100-160`, which saves and restores
`dispatchKeepAlive`). The new `dispatchAcceptSessionMessages *bool` must be
added there, or the tri-state value leaks between tests. The remote-control
wiring helper `hasRemoteControlSettings`
(`dispatch_wiring_remotecontrol_test.go:67-73`) compares the argv element to
`remoteControlSettingsJSON` for exact equality. The existing tests pass
unchanged, but only if that variable survives the builder. The design should
say to keep `remoteControlSettingsJSON`, and add a builder test pinning
`render()` of remote control alone to it. New tests with both keys need a
helper that parses the document rather than comparing strings.

**`niwa list` surfaces.** Beyond the `--json` flag help
(`list.go:17-18`, `{name, path, ephemeral[, keep_alive]}`), the command's
`Long` text (`list.go:36-40`) describes the record shape and must mention the
always-present field. `InstanceRecord` lives in
`internal/workspace/state.go:365`, so name the file rather than "instance
records". `session-keep-alive.md` documents the list JSON shape (lines 48-62)
and needs no change. `list_keepalive_test.go`'s wire-shape test still passes
with the new field.

**Explanation seam.** `showInboundExplanation(w io.Writer, dir string, isTTY
func() bool)` is fine for unit tests. Say that production passes
`IsStderrTTY` and `cmd.ErrOrStderr()`, and that `dir` comes from the
`GlobalConfigPath()` error handling above.

**Functional step definitions and harness.**
- *TTY scenarios.* The only pty helper runs the binary under
  `script -q -c ... /dev/null` (`steps_init_bootstrap_test.go:~157-215`,
  registered at `suite_test.go:502`). Under `script`, stdin, stdout, and
  stderr are all the pty, and the transcript arrives merged on `script`'s
  stdout. So the terminal scenarios can't assert "stderr contains" on a
  separate stream. They also depend on util-linux `script`, whose flags differ
  on macOS. And without `--detach` they reach the fake `claude attach`, which
  interacts with the finding above. Phase 6 should either add a step that runs
  dispatch under a pty with `--detach` and asserts on the merged transcript,
  or state that terminal assertions read the combined output.
- *Watch exclusion.* No functional feature or step file runs `niwa watch`
  (searched `test/functional`). R18 asks for an `@critical` scenario, and
  building a `watch --once` harness (a GitHub PR fake, sandbox off, a fake
  claude) isn't a small add-on. The design lists "the watch exclusion" as if
  it were a routine scenario. Recommendation: cover it structurally with a
  unit test that stubs `dispatchLaunch` and runs both watch launch sites
  (`watch.go:581` and `:844`) with the machine key `true`, asserting no
  `--settings` or `crossSessionInbound` in the passthrough. Then either budget
  the functional harness explicitly or amend the PRD's R18 to accept unit
  coverage for this one case. Either way the design must say which.
- *R3 source fixtures.* The acceptance criteria require fixtures showing that
  `workspace.toml` keys, `[claude.settings] crossSessionInbound`, and
  repository `.claude/settings*.json` files can't turn the behavior on. The
  design never discusses R3 beyond precedence. It holds today because
  `buildSettingsDoc` emits only the keys it knows (`permissions`,
  `remoteControlAtStartup`, `keepAliveOnDispatch`, hooks and so on;
  `materialize.go:668-760`). An unknown `[claude.settings]` key is silently
  dropped, and an unknown top-level `workspace.toml` field only warns
  (`config.go:624`). The design should state this reliance and list the
  fixture scenario among Phase 6's deliverables.
- *Keep-alive precedent.* `keepalive_steps_test.go` reads the fake claude's
  recorded argv from `$HOME/dispatch-launch-argv`, and the suite already sets
  `HOME` and `XDG_CONFIG_HOME` to a sandbox (`steps_test.go:95-105`). New steps
  can reuse that path. The design should name the new steps: the argv document
  carries or lacks `crossSessionInbound`, the marker exists or doesn't under
  `$XDG_CONFIG_HOME/niwa/`, and `config.toml` is byte-unchanged.

**Shell completion.** Cobra offers every registered flag, and the keep-alive
flag uses the same `triBoolValue` plus `NoOptDefVal` pattern with no
completion code. `completion.feature` has no dispatch-flag scenarios. Phase 6
"check the flag's help and completion" is enough, but point it at a concrete
check such as `niwa __complete dispatch --acc`.

**Manual delivery check.** R4 and R5 rest on the PRD's manual procedure, and
R17 wants the guide to state the Claude Code version it last passed on. No
phase owns running it. Add it to Phase 6, with its result recorded as the
guide's version line.

**Guide URL constant.** The audit line and the explanation both embed the
guide URL. Make it one constant in `dispatch_inbound.go`, since the scaffold
precedent (`scaffold.go:183,192`) duplicates its URL literal twice.

## 3. Phase sequencing

**Phase 1 fails on its own.** A commit that adds only the constant, catalog
row, declarations, and subject fails `TestAllIsTheClosedSet`,
`TestCodexColumnTotals`, and the gap-list drift test unless it also updates
those tests and regenerates `docs/guides/codex-agent.md`. List both in Phase
1's deliverables, plus the capability-contract PRD amendment.

**Phase 1 declares a capability before it's delivered.** `declaration.go:73-75`
says: "Every implemented row flipped in the change that delivered it, never
before: writing a future state down early would make the table a plan rather
than a record." Phase 1 declares Claude `StateImplemented` two commits before
Phase 3 delivers it. Because `Lookup` is fail-closed and
`TestDeclarationTableCoversEveryPairExactlyOnce` requires both agents' rows,
the row can't be omitted either. Fix it one of two ways:
- move the capability row into Phase 3, the commit that delivers it; or
- declare Claude `StateUnavailable, ReasonNotBuilt` in Phase 1 and flip it in
  Phase 3.

The first is simpler and keeps Phase 1 to the config key.

**`dispatch_layout_test.go` in Phase 3.** Phase 3 creates
`dispatch_inbound.go`, but only Phase 2's deliverables list the layout test.
Each file should join `dispatchPathFiles` in the commit that creates it.

**Phase 3 and Phase 4 overlap.** Phase 3 writes the audit line "at the
post-mapping point", and Phase 4 sets the mapping field at the same point from
the same boolean. That works if Phase 3 introduces the boolean, per the naming
recommendation above, and Phase 4 only threads it into the literal. Say so.

**Phase 5's placement depends on the attach decision.** If the explanation
moves after `dispatchAttach`, Phase 5 touches step 14 of `dispatch.go`, which
the design attributes to no phase. Add it to Phase 5's deliverables.

**"None has to merge before another."** This is true only in the one-PR sense
of D11. The phases carry real dependencies (Phase 1 before 3, Phase 2 before
3, Phase 3 before 4 and 5, everything before 6). Drop the clause or say
"all land in one PR, in this order".

## 4. Simpler alternatives

**The builder as a type.** `launchSettings` with `set` and `render` is a map
and a `json.Marshal` call. A plain `map[string]any` built at step 9c, plus one
helper `renderLaunchSettings(map[string]any) (string, bool)`, gives the same
byte-identical output, the same independent contributors, and one less type.
The "next contributor" argument works equally well for a map. This isn't a
blocker; the design should either take the simpler form or say why a type
earns its place. The separate `dispatch_settings.go` could also fold into
`dispatch_remotecontrol.go` or `dispatch_inbound.go`, though a neutral file
name is defensible.

**Watch exclusion coverage.** As above, a unit test at the two watch launch
sites is simpler and more direct than a functional harness. The PRD's
functional requirement is what forces the harder path.

No simpler alternative was overlooked for Decisions 2, 3, or 4. The capability
row, the terminal-gated `O_EXCL` marker, and the always-present field are each
the minimal shape for their requirement.

## 5. Factual claims checked against the code

Correct:
- Step 9c is at `dispatch.go:585-601`, keep-alive gating at `638-658`, and the
  mapping literal at `761-772` (write at `773`).
- `annotateFromSessionMappings` is at `list.go:117` and sets `KeepAlive` only
  when `sessionLive`.
- The watch launch sites are `watch.go:581` and `:844`. Both build their own
  passthrough from `buildDispatchPassthrough(claudeLaunchSpec().Flags, ...)`
  and call `dispatchLaunch` directly, never `runDispatch`, and watch writes no
  `SessionMapping`. R14 holds by construction, and watch instances read
  `false`.
- `IsStderrTTY` is a stubbable package variable (`prompt.go:40`) already used
  through `dispatchInteractive`.
- `GlobalConfigDir()` returns `.../niwa/global` (`registry.go:383-395`), so
  `filepath.Dir(GlobalConfigPath())` is the right directory, modulo the
  two-value return noted above.
- `SaveGlobalConfigTo` re-encodes the struct and drops comments and unknown
  keys (`registry.go:299-318`).
- A `--settings` document built by `encoding/json` is byte-identical for
  remote control alone. Checked by running it: `json.Marshal(map[string]any{
  "remoteControlAtStartup": true})` gives `{"remoteControlAtStartup":true}`,
  equal to the current `fmt.Sprintf("{%q:true}", ...)`, and the merged form
  is `{"crossSessionInbound":"accept","remoteControlAtStartup":true}` as the
  design states. Map keys are sorted, and none of the constant keys or values
  contain the HTML characters `encoding/json` escapes.
- Codex's launch spec has no `Settings` flag. Claude's is `"--settings"`
  (`agentplan/dispatch.go:357`).
- The AST scan (`dispatch_layout_test.go`) forbids exact string literals
  (`"claude"`, `".claude"`, and others) and the agent constants. The planned
  messages contain "Claude Code" and `~/.claude/settings.json` inside longer
  literals, which the scan doesn't flag (it matches whole literals only), so
  the new files pass.
- A `config.toml` with `accept_session_messages_on_dispatch = "yes"` fails
  `toml.Unmarshal` into `*bool`, and a mode-000 file fails `os.ReadFile`. Both
  set `gcErr` and yield "machine setting absent" once `hostGlobal` is hoisted.
  R15 holds.

Imprecise or wrong:
- **"`runDispatch` already yields a zero `GlobalSettings`."** It does, but only
  from line 620, after step 9c. See the `hostGlobal` item under section 1.
- **`filepath.Dir(config.GlobalConfigPath())`.** The function returns two
  values.
- **The Summary and Data Flow steps 5 and 11 imply the field is recorded after
  the mapping write.** It must be set before the write. See section 1.
- **"The same shape remote control and keep-alive use" for deliverability.**
  Remote control checks declaration plus settings flag. Keep-alive checks the
  declaration only (`dispatch.go:638-639`). The inbound check matches remote
  control's shape, not both.
- **Remote control's route.** Decision 2 files the new row under `RouteLaunch`,
  which is right, but `RemoteControl` itself is `RoutePlan` in the catalog
  (`capability.go:192`). This is only worth knowing if someone reads "the same
  shape" as "the same route".
- **Placement "after `DispatchLaunch`"** has row-numbering side effects the
  design doesn't mention. See section 1.

## 6. PRD coverage

Covered: R1, R2, R4 and R5 (by construction plus the manual check), R6 (with
the `rcInjected` clarification), R7, R8, R9, R12, R13, R15, R16, and R17 (the
guide's contents are delegated, fine for a design).

Covered in intent, but undermined or under-specified:
- **R10 and R11, "created only when the explanation was written to an
  interactive terminal".** They're satisfied literally but not in spirit
  during the default non-detached Claude flow, because the attach takes the
  terminal right away. User Story 7 and the Decision 3 rationale ("losing the
  explanation unseen ... isn't recoverable") are defeated. This needs the
  ordering fix from section 1.
- **R3, "no other source can turn it on".** It holds because of
  `buildSettingsDoc`'s key allowlist and Claude Code's project-scope
  restriction, but the design never states the first reliance, and no phase
  owns the fixture scenario from the acceptance criteria.
- **R14.** It holds structurally, but the required `@critical` functional
  scenario has no harness. See section 2.
- **R18.** It's listed, but the design doesn't address the TTY-scenario
  mechanics (merged streams under `script`) or the missing watch harness.
- **R11, "niwa creates its configuration directory if it doesn't exist".**
  Covered, but the `0o700` versus `0o755` mode choice is unexplained.

No requirement is contradicted outright.

## Recommended edits, by section

- **Decision 1:** add "`rcInjected` stays set from remote control's own inject
  decision, never from whether the builder rendered a document; keep
  `remoteControlSettingsJSON` as the pinned remote-control-alone rendering."
  Consider the map-plus-helper form in place of the type.
- **Decision 2:** say to append the constant and catalog row as row 25 (or
  accept the renumbering and list the comment and PRD updates). Add
  `config.CrossSessionInboundKey`. Correct "the same shape remote control and
  keep-alive use" to "the shape remote control uses".
- **Decision 3:** account for `dispatchAttach`. Print the explanation, and
  create the marker, after the attach returns when an attach follows, and
  right after the audit line otherwise. Say what happens when
  `GlobalConfigPath()` errors. Define "exists" as `os.Stat == nil`. Pick and
  justify the directory mode.
- **Decision Outcome, Summary:** hoist `hostGlobal` above step 9c. Set
  `AcceptsSessionMessages` in the mapping literal before
  `WriteSessionMapping`; the stderr lines follow the successful write, between
  steps 12 and 13. Name the single "applied" boolean. State the override
  condition or widen the resolver's return.
- **Solution Architecture, Components:** add `docs/guides/codex-agent.md`
  (regenerated gap list), `docs/prds/PRD-agent-capability-contract.md` (matrix
  amendment), `internal/config/config.go` (key constant),
  `internal/workspace/state.go` by name, the `list.go` `Long` text,
  `dispatch_test.go`'s flag reset, and the watch-site unit test.
- **Solution Architecture, Key Interfaces:** add the guide-URL constant, and
  write `showInboundExplanation`'s production arguments.
- **Solution Architecture, Data Flow:** fix step 5's ordering and add step 14's
  attach interaction.
- **Implementation Approach:**
  - Phase 1: config key only, or add the capability-test count updates, the
    gap-list regeneration, and the PRD amendment. Move the capability row into
    Phase 3, or declare it not-built until then.
  - Phase 3: add the layout-test entry and the capability row.
  - Phase 5: add the `dispatch.go` step 14 change.
  - Phase 6: name the step definitions; say how the terminal scenarios assert
    on merged pty output and use `--detach`; decide between a watch functional
    harness and unit coverage with a PRD amendment; add the R3 fixture
    scenario and the manual delivery check.
  - Replace "none has to merge before another" with the actual order.
