---
schema: design/v1
status: Current
upstream: docs/prds/PRD-agent-capability-contract.md
problem: |
  The PRD requires a capability contract the workspace-preparation path is
  actually bound to: every (capability, agent) pair implemented or declared
  unavailable with a reason, no agent constants at materializer call sites,
  a package-legible generic/specific boundary, and a first PR that provably
  changes no behavior. The prior attempt (tsukumogami/niwa#248) shipped its
  unifying abstraction as dead code because no test could fail on the
  structure. This design owns the package layout, the contract's shape, and
  the mechanics of each enforcement test.
decision: |
  A new leaf package, internal/agentplan, holds the closed capability set,
  the per-agent declaration table (two states, three reason kinds, Requires
  edges, a delivery Route), and per-agent plan producers that read inputs
  and declare outputs as data. internal/workspace gains one agent-blind
  executor over a closed four-op set. Three structural test families -- an
  AST layout scan, a pure declaration/plan-shape suite, and route-wise
  wiring and binding checks -- plus a ManagedFiles-based characterization
  test committed before the refactor enforce every claim. MCP and session
  environment ride new agent-neutral config surfaces that generate both
  agents' native formats, validated and fully resolved before writing. The
  content table reverts to the agent-neutral [content] spelling openly;
  claude.enabled is restructured, not renamed.
rationale: |
  The codebase already separates "decide what to say" from "write it"
  almost everywhere; the plan model names that existing shape once instead
  of a fifth ad-hoc instance. It is the only considered option under which
  the contract's core properties are assertable in pure table tests with no
  tmpdir, which is what makes the prior attempt's failure -- a seam threaded
  everywhere and load-bearing nowhere -- structurally unrepeatable: bytes
  reach disk only through a plan, and plans only come from a function that
  takes the agent. Measured codex-cli 0.147.0 behavior dictates
  validate-before-write, collision detection, and full value resolution
  rather than leaving them to taste.
---

# DESIGN: agent capability contract

## Status

Current

This design owns the mechanism for the agent capability contract: the
package layout, the plan types and closed operation set, the declaration
model and its tests, the no-behavior-change proof, the MCP and environment
generation surfaces, the configuration rename and gate restructure, and the
secret-hygiene obligations. The upstream PRD owns the requirements
(R1-R24, N1-N3) and the 24-row capability matrix; this design cites them
and does not re-open them. Codex discovery mechanics are consumed from the
standing spike (docs/spikes/SPIKE-codex-discovery-mechanics.md, landing via
tsukumogami/niwa#254) and from the measured codex-cli 0.147.0 findings the
PRD records and R24 routes back to that spike; nothing here re-derives
them.

## Context and Problem Statement

When `niwa apply` prepares an instance, `runPipeline`
(`internal/workspace/apply.go`) drives five materialization levels --
workspace root, instance root, group directories, cloned repositories, live
worktrees -- through writers that produce context files, settings
documents, hook scripts, env files, and distributed files. The
`agent.Agent` type (`internal/agent/agent.go`) was meant to make that path
agent-aware. On main it governs almost nothing:

- Three call sites route a filename through `ag.RootContextFileName()`
  (`content.go:44`, `content.go:73`, `root_materializer.go:375`). That is
  the whole of the working agent-awareness.
- Two functions accept an agent parameter, use it only as a run/skip gate,
  and then hardcode `"CLAUDE.local.md"` inside the gated body
  (`InstallRepoContentTo`, `content.go:156/186/208`;
  `installWorktreeContextLayer`, `worktree_content.go:743`).
  `agent.LocalContextFileName()`, the accessor that was supposed to make
  those writes agent-aware, has zero callers anywhere in the module.
- Everything else takes no agent parameter and writes Claude-shaped output
  unconditionally: `InstallWorkspaceContext`, the overlay and global
  content installers, root and instance settings, and the hooks, env, and
  files materializers.

The first Codex attempt (tsukumogami/niwa#248, branch retained at
`docs/dual-agent-workspace`) shipped exactly the failure its own design
warned against: an `Applier.Agent` field threaded through the pipeline and
read by nothing, while every materializer call site iterated a hardcoded
agent list. Nothing failed, because the intended structure was never
something a test could fail on.

So the design problem is not "how do we write Codex files" -- the prior
branch's composition mechanics are sound and the discovery rules are
measured. The problem is the shape of a contract that (a) a second agent
implements rather than sits beside, (b) a test can fail on when it is
faked, and (c) can land first against existing Claude behavior with a
mechanical proof that nothing observable changed.

## Decision Drivers

- **The contract must be falsifiable.** Every structural claim needs a
  test that goes red on regression (R4, R5, R6). The prior attempt's
  lesson: a structure no test can fail on will not survive its first
  implementation.
- **Assertability without a filesystem.** The cheaper a structural
  property is to check, the more of them the design can afford. Properties
  checkable in a pure table test are strongly preferred over properties
  that need a provisioned instance and a tree walk.
- **The first PR must be invisible.** No behavior change, no config
  surface change, provable mechanically rather than by diff audit (R9,
  R10, N2).
- **Honesty over coverage.** Every (capability, agent) pair is implemented
  or declared unavailable with a machine-readable reason; the guide's gap
  list is generated from those declarations, and no third state exists for
  a real gap to hide inside (R2, R3, R22).
- **House style, not invention.** The repo's precedents govern:
  `internal/vault`'s fail-closed registry posture, leaf packages carved
  out with a stated caller and cycle (`internal/gitexclude`,
  `internal/envformat`, `internal/keyreport`), and the recorded config
  rename mechanism (docs/designs/current/DESIGN-claude-key-consolidation.md,
  commit 81aae0b). There is no in-repo precedent for per-agent files
  (`foo_claude.go` / `foo_codex.go`); per-agent variation is always a
  method with branches or a map keyed by `agent.Agent`.
- **No dead seams.** A type, op, field, or precondition is introduced in
  the PR that first uses it. Shipping structure ahead of its consumer is
  the prior attempt's failure in miniature.
- **Standard toolchain only.** Structural and characterization tests use
  `go/ast`, `go/parser`, `go/token`; no new module dependency, and CI
  stays `gofmt -l .`, `go vet ./...`, `go test -race ./...` (N1, N3).
- **Measured Codex behavior is authoritative.** The merge, failure,
  transport, and trust semantics of codex-cli 0.147.0 (spelled out in
  Decision 5) are measured facts this design builds on, never assumptions
  to revisit. Two attempts to reason about them from outside got them
  wrong in opposite directions.

## Considered Options

### Decision 1 -- structural shape: plan-producing leaf package plus an agent-blind executor (R4, R5, R6, R11)

The decisive observation is that the codebase has already separated
"decide what to say" from "write it" almost everywhere, deliberately.
`buildSettingsDoc` (`materialize.go:654-940`) builds the whole settings
document and performs no write; `renderContentFile` (`content.go:255-268`)
is documented as render-only, existing "so the write-path and
render-to-string paths cannot drift"; the context generators, hook
resolution helpers, and git-exclude block renderer are all pure. The 21
write sites in scope are uniformly `os.MkdirAll` plus `os.WriteFile` with
one of three permission values, and the bookkeeping afterwards consumes
only a flat path list plus a provenance side map (`apply.go:1701-1718`,
`state.go:184-190`).

**Option A (chosen): a leaf package producing declarative plans, one
generic executor in `internal/workspace`.**

A new leaf package, `internal/agentplan` -- sibling to `internal/agent`,
importing `internal/agent` and `internal/config` and nothing above them --
holds three things: the closed capability enumeration with its per-agent
declaration table (Decision 2), the plan vocabulary below, and per-agent
plan producers, which are functions from narrow config inputs to a `Plan`.
`internal/workspace` gains one executor, `applyPlan`, that implements a
closed operation set and contains no agent name and no agent context
filename.

```go
// Op is the closed set of primitive operations an agent's plan may
// declare. Adding a member is a design change, not an implementation
// detail.
type Op uint8

const (
    OpWriteFile      Op = iota // write Content at Path, with Mode
    OpAppendLine               // append Content to Path unless already present
    OpReplaceSection           // replace the region delimited by Marker
    OpDeliverTree              // symlink Source at Path; copy on failure (PR 2)
)

// Precondition is the closed set of conditions gating an entry.
type Precondition uint8

const (
    Always          Precondition = iota
    IfSourceExists               // stat Source first; absent means no-op
    IfNotForeign                 // consult the ownership verdict at write time (PR 2)
)

// Entry is one declared write.
type Entry struct {
    Capability Capability    // which declared capability this write delivers
    Op         Op
    Path       string        // absolute target
    Content    []byte        // OpWriteFile / OpAppendLine / OpReplaceSection
    Source     string        // OpDeliverTree source; IfSourceExists probe path
    Mode       os.FileMode   // 0o600, 0o644, or 0o755
    Marker     string        // OpReplaceSection delimiter
    Pre        Precondition
    Managed    bool          // participates in ManagedFiles + cleanup
    ExcludeAs  string        // extra git-exclude pattern implied, "" for none
    Sources    []SourceEntry // provenance, for SourceFingerprint
}

// Plan is one agent's whole declared output for one level.
type Plan struct {
    Entries  []Entry
    Warnings []string // conflicts, refusals, hoist omissions
    // Exempt []string -- added in PR 2 with the Codex conflict rule:
    // paths cleanup must not delete although this apply didn't produce them.
}
```

Four ops suffice because the evidence says so, not because the model hopes
so: 21 of the write sites in scope are plain writes, and the three that
aren't are each a single named helper with a documented closed rule -- the
`@import` accumulation in `.claude/rules/workspace-imports.md`
(`workspace_context.go:137-155`, becomes `OpAppendLine`), the worktree
context layer's delimited-section replace (`worktree_content.go:754-767`,
becomes `OpReplaceSection`), and the Codex payload's
symlink-or-bounded-copy delivery on the retained branch (`OpDeliverTree`,
the one op whose implementation is genuinely imperative and lives in the
executor). If a fifth op is ever needed, that is a design conversation --
the correct price for a new way niwa touches a user's repository.

**Nothing lands before its consumer.** PR 1 introduces the first three ops
and the first two preconditions. `OpDeliverTree`, `IfNotForeign`, and
`Plan.Exempt` land in PR 2 alongside the Codex payload delivery and the
conflict verdict that need them. Introducing them unused in PR 1 would
reproduce, in miniature, the exact defect this work exists to remove: a
seam with no live consumer.

The bookkeeping vocabulary is three fields (`Managed`, `ExcludeAs`,
`Sources`) because niwa's managed-file machinery is a post-hoc pass over a
flat path list, not something writers participate in. Pipeline integration
is correspondingly small: Step 7 reads `Managed` and `Sources` off entries
instead of a bare slice plus a side map, and the per-repo git-exclude call
collects `ExcludeAs` patterns.

The boundary is stated as **reads inputs, declares outputs** -- not
"pure." The producers do read: niwa-owned content sources, overlay stat
probes, and (in PR 2) a guarded `O_NOFOLLOW` read of a repository's own
committed context file. What they never do is write or launch anything,
and that line is mechanically checkable (Decision 3, family 1). "Pure"
would be a nicer word and a false claim.

Two mechanics keep the boundary honest where the prior attempt's leaked:

- **Probes are declared by the leaf, executed by the workspace, and fed
  back as data.** Anything that must read foreign content in the target
  tree or run a subprocess -- the Codex ownership and conflict detection
  (Lstat, `git ls-files`, bounded marker probe), the harness version probe
  -- stays out of the leaf. The leaf declares what to probe; the workspace
  runs the probe in a pre-pass, exactly as `WorktreeDelegation` is computed
  once and threaded today, and passes the verdict into the producer as
  input. The leaf stays exec-free and write-free by construction, so the
  AST scan stays exact rather than becoming a list of exceptions.
- **`SourceEntry` and `ComputeSourceFingerprint` move down** from
  `internal/workspace/state.go:198-249` into the leaf so plan entries can
  carry provenance without an import cycle, with a type alias left behind
  (`type SourceEntry = agentplan.SourceEntry`) so the state schema, the
  JSON tags, and the existing references don't change. They are four
  strings and a sha256-plus-sort, documented as pure metadata that never
  carries secret material. `EffectiveConfig` (`override.go:17-37`) does
  not move: producers take narrow input structs `internal/workspace`
  fills, matching the house idiom of call-site-sized inputs.

**Capabilities that aren't file deliveries aren't forced into a file
shape.** Each capability carries a delivery `Route` (Decision 2):
plan-borne rows flow through `applyPlan`; procedure-borne rows (directory
trust, plugin installation, git-exclude bookkeeping) bind named procedures
registered against the declaration table; launch-borne rows (dispatch,
keep-alive) gate the launch path on a declaration lookup. Side effects
outside the instance stay where they are architecturally and are bound to
the table rather than rewritten as plan entries -- the contract governs
them without pretending they are writes into the workspace.

**Option B (rejected): an interface implemented inside
`internal/workspace`, with per-agent files.**

Define `AgentMaterializer` in `internal/workspace`, implement it in
`materialize_claude.go` and later `materialize_codex.go`, and have the
pipeline iterate implementations. Its advantages deserve stating: it is
the conventional Go answer, it needs no new package, no type moves, and no
executor, each agent's logic sits in one readable file, and PR 1's diff
would be smaller. It loses on the properties this feature exists for. Its
only observable is the filesystem, so every contract assertion costs a
tmpdir and a full apply, and the plan-shape and wiring tests don't exist
in this shape at all: an interface method that writes directly can
hardcode a filename three lines above its `return nil` and nothing but a
filesystem diff notices. That is not hypothetical -- it is the mechanism
by which tsukumogami/niwa#248 failed while compiling cleanly. And the
`foo_claude.go` / `foo_codex.go` convention has no precedent anywhere in
this repo.

**Option C (rejected): more accessors on `internal/agent`.** Scale up the
`RootContextFileName()` pattern. Accessors don't compose into a checkable
completeness property -- `LocalContextFileName()` is the standing proof
that an accessor can exist, be documented, and be dead. Two-of-twenty-four
coverage on main is the result of this shape, not a partial adoption of
it.

**Option D (rejected): a `vault`-style registry of per-agent
materializers.** vault's Factory/Registry shape serves an open, pluggable
set where implementations self-register from separate packages; the agent
set is closed and enumerated. Its fail-closed posture is adopted -- in the
binding test and the table lookups -- without the open-set machinery, and
a plain interface registry shares Option B's filesystem-only
observability problem.

**Chosen: Option A.** It is the generalization of a shape niwa has already
built four times ad hoc -- `WorktreeDelegation` (a decision computed once,
threaded as data to writers), the prior branch's `CodexRepoVerdict` (a
detection pass consulted by writers and cleanup), `InstalledHooks` (one
materializer's output consumed as another's input), and the rendered
context layers. The plan is the fifth instance, named once. And it is the
only option under which the agent parameter is load-bearing by
construction: there is no path from config to bytes that bypasses
`Plan(agent, inputs)`.

### Decision 2 -- capability state model and the settled matrix (R1, R2, R3, R21)

**Chosen: two states, a required reason kind on unavailability,
`Requires []Capability` edges on implementation, and a delivery `Route`.**

```go
type Capability uint8 // closed enumeration; Capabilities() returns all 24

type State uint8

const (
    StateImplemented State = iota + 1
    StateUnavailable
)

type ReasonKind uint8

const (
    ReasonAgentCannotReceive ReasonKind = iota + 1 // the agent's own mechanics put it out of reach
    ReasonNoSuchConcept                            // the concept doesn't exist for this agent
    ReasonNotBuilt                                 // a route exists; niwa hasn't built it
)

type Route uint8

const (
    RoutePlan      Route = iota + 1 // delivered as plan entries via the executor
    RouteProcedure                  // delivered by a named registered procedure
    RouteLaunch                     // consulted by the session-launch path
)

type Declaration struct {
    Capability Capability
    Agent      agent.Agent
    State      State
    Kind       ReasonKind   // required iff StateUnavailable, zero otherwise
    Reason     string       // required iff StateUnavailable, empty otherwise
    Requires   []Capability // legal only when StateImplemented
}
```

The table is a package-level slice, one `Declaration` per (capability,
agent) pair, with the two deliberate exclusions recorded beside it so the
closure doesn't look arbitrary (R1): vault-backed secret resolution (an
upstream source feeding rows 9 and 10, not something a session receives)
and the `claude.enabled` gate (a gate over deliveries, not a delivery --
Decision 6). Two supporting exports the tests need and today's code lacks:
`Capabilities()`, and `agent.All()` (today `agent.known` is unexported and
every "for each agent" test hand-lists the two constants).

**Why no third state.** The tempting middle state -- "implemented, but
only when the directory is trusted" -- dissolves once directory trust is
named as a capability niwa itself delivers. It is: the prior branch's
trust writer (`codex_trust.go` on `docs/dual-agent-workspace`) performs a
TOML-surgical, additive, lock-serialized edit of the developer's own Codex
config, retracting only keys niwa wrote, and four retained acceptance
scenarios pin its behavior. So trust-dependent rows are plainly
`StateImplemented` with `Requires: [DirectoryTrust]`, and the closure test
makes that honest by construction -- declaring Codex MCP implemented while
Codex trust is unavailable fails CI, the PRD's canonical failing case. A
"conditional" state would have absorbed exactly that drift, and it would
force the gap-list generator to judge rather than filter, which is the
prior attempt's documentation failure in new clothes. A free-text
precondition field fails the same way: it moves the honesty burden into
prose nothing can check. `Requires` is the same idea with a closed domain
-- a precondition is expressible only if it is itself a capability niwa
implements or declares unavailable. Anything niwa can't own (an OS
feature, a developer declining a prompt) isn't expressible, which forces
the honest answer: `StateUnavailable` with a reason. The type system
refuses "works, sort of, if."

The accepted cost: a few rows read oddly for one agent out of context
(`DirectoryTrust` is unavailable/no-such-concept for Claude). The guide
renders no-such-concept rows as short "does not apply" notes and the other
two kinds as the gap list proper -- a rendering rule over an enum, not a
judgment call.

**The settled matrix.** The PRD's 24 rows, with the delivery route each
capability binds through:

| # | Capability | Route | Claude | Codex | Codex reason / notes |
|---|---|---|---|---|---|
| 1 | Workspace/group orientation reaches a repo session | Plan | I | I | Composed into each repo's own context file (the only placement Codex reads) |
| 2 | Root-started session is oriented | Plan | I | U(cannot-receive) | Codex reads context only from the nearest project-root marker downward; an instance root has none |
| 3 | Repo-level orientation doc | Plan | I | I | Requires: DirectoryTrust (byte-budget override) |
| 4 | Worktree-level orientation doc | Plan | I | I | Requires: DirectoryTrust; a linked worktree's `.git` pointer file satisfies the project-root marker (measured, R13) |
| 5 | Workspace-declared plugin skills | Plan | I | I | Loads even untrusted; must not depend on Claude Code's presence (R14) |
| 6 | Marketplace/plugin registration | Procedure | I | U(cannot-receive) | Registration lives in the developer's own configuration; skills are delivered directly instead |
| 7 | Named subagent types | Plan | I | U(no-such-concept) | Codex caches a plugin's agents directory and never surfaces it |
| 8 | MCP servers available to the session | Plan | I | I | Via Decision 5; Requires: DirectoryTrust |
| 9 | Environment variables in the session | Plan | I | I | Via Decision 5; Requires: DirectoryTrust (measured, not inferred) |
| 10 | Dotenv files at declared paths | Plan | I | I | Agent-agnostic |
| 11 | Arbitrary file distribution | Plan | I | I | Agent-agnostic |
| 12 | Approval/sandbox posture | Plan | I | I | Via R21 and the discussion below; Requires: DirectoryTrust |
| 13 | Hooks (lifecycle commands) | Plan | I | U(cannot-receive) | No demonstrated route installs a niwa-owned hook without a blocking review prompt |
| 14 | Work-summary hooks | Plan | I (Requires: Hooks) | U(cannot-receive) | Delivered as hooks; follows row 13 |
| 15 | PR-body hook | Plan | I (Requires: Hooks) | U(cannot-receive) | Delivered as a hook; follows row 13 |
| 16 | Worktree-hook delegation / deny fallback | Plan | I | U(no-such-concept) | Claude Code harness surface; Codex has neither the events nor the tools |
| 17 | Ephemeral-session provisioning | Plan | I (Requires: Hooks) | U(cannot-receive) | Rides a session-start hook and the harness job-state file |
| 18 | Root-installed project skills | Plan | I | U(cannot-receive) | Serve root-started sessions, where Codex reads no configuration |
| 19 | niwa's own plugin (migrate-config) | Procedure | I | U(not-built) | Codex accepts the identical manifest; wiring unbuilt, out of scope (PRD) |
| 20 | Remote-control-at-startup | Plan | I | U(no-such-concept) | Names claude.ai's remote-control bridge |
| 21 | Dispatch keep-alive | Launch | I (Requires: Dispatch) | U(no-such-concept) | No background-session bridge to keep warm |
| 22 | Launching a background worker | Launch | I | U(not-built) | Launch path refuses non-Claude; the model table already carries Codex entries; out of scope (PRD) |
| 23 | Per-directory trust bootstrap | Procedure | U(no-such-concept) | I | Claude Code keeps no per-directory trust record |
| 24 | Git-exclude bookkeeping | Procedure | I | I | Agent-agnostic; covers Codex-side names exactly as Claude-side ones |

Totals once PR 2 lands: Codex 11 implemented, 13 unavailable (7
cannot-receive, 4 no-such-concept, 2 not-built); Claude 23 implemented, 1
unavailable. In PR 1 the Codex column states main's truth (R11): the seven
cannot-receive and four no-such-concept rows already carry their inherent
reasons, and every row PR 2 will implement is declared not-built until PR
2 flips it.

**Row 12 is delivered, and the third safety property is why.** The
measurement resolved settability affirmatively: `approval_policy` and
`sandbox_mode` both take effect from a trusted project layer and both
revert when the trust entry is removed. They are not on the project-layer
denylist -- measured at eight denylisted keys (provider endpoints,
`notify`, `profile`, and two realtime endpoints), which self-enumerates
via the startup-warning row of `codex doctor --json`.

There is a real objection to delivering it: niwa writes the trust entries
itself (row 23), so a workspace-declared posture takes effect on a
developer's machine without a separate consent step from that developer.
The objection doesn't survive contact with what niwa already ships for the
first agent. `permissionsMapping`
(`internal/workspace/materialize.go:295-298`) translates a workspace-level
`permissions` declaration straight onto Claude Code's `bypassPermissions`,
which `buildSettingsDoc` emits as `permissions.defaultMode`
(`materialize.go:669`). A workspace author can already relax a developer's
Claude Code approval posture through workspace config today. Declaring the
second agent's equivalent unavailable on security grounds, while the first
agent's ships, would build in exactly the asymmetry this work exists to
remove -- and it would put a measured, working route on the generated gap
list, which is a lie the gap list is designed not to tell.

So the capability is implemented, with R21's three safety properties:

1. **Opt-in and absent by default.** With no posture declared in workspace
   config, niwa writes neither `approval_policy` nor `sandbox_mode`, and
   Codex's own defaults apply unchanged. niwa is never the reason a session
   runs with weaker guardrails than the developer chose for themselves. The
   absent-declaration case is asserted directly, not inferred.
2. **What niwa writes is reported at apply time.** A posture change is
   never silent.
3. **Approvals and sandbox stay separate decisions, and niwa never derives
   one from the other.** This property exists because of a real asymmetry
   between the two agents: Codex's most complete approval suppression,
   `sandbox_mode = "danger-full-access"`, collapses approvals and *both*
   sandboxes -- filesystem and network -- into a single setting. Claude
   Code's `bypassPermissions` does not do that; it relaxes approvals
   without a sandbox dimension to take with it. A generator that inferred
   "the workspace wants fewer prompts" into a Codex sandbox setting would
   therefore disable sandboxing as an unstated side effect of a
   declaration that never mentioned it. niwa maps approval posture to
   `approval_policy` and sandbox posture to `sandbox_mode`, each only from
   its own declared source, and a test asserts that no approval-only
   declaration produces a `sandbox_mode` key.

**Rejected alternatives to the state model.** A third "conditional" state
(the soft word a real gap hides inside). An orthogonal free-text
precondition field on implemented rows (two states nominally, prose
nothing can check in practice). A level dimension
(root/instance/repo/worktree) on every capability: rows were split where
the per-agent answer genuinely differs by level (rows 2, 3, 4) and nowhere
else, so a level dimension would multiply the matrix by four for one
distinction already captured.

### Decision 3 -- the three enforcement-test families (R4, R5, R6, R22, N1)

All are standard library only and run under the existing
`go test -race ./...`.

**Family 1 -- the layout scan** (`go/ast` over non-test files), two
halves:

- *Workspace half:* no non-test file in `internal/workspace` names
  `agent.AgentClaude` or `agent.AgentCodex` in code, and no string literal
  in {`CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`, `AGENTS.override.md`}
  appears. Status on today's tree: the constants half passes (the only
  occurrences are comments, which the AST walk never sees); the filename
  half **fails at eight sites** -- `content.go:156`, `:186`, `:208`,
  `worktree_content.go:743`, `workspace_context.go:196`, `:229`, `:411`,
  and the dead `rootClaudeFile` constant at `root_materializer.go:51`,
  referenced only from its own test file. Red before the conversion and
  green after is a feature, not a defect: it proves the scan detects the
  real thing, and R5 names precisely this meaningful-on-day-one behavior.
- *Leaf half (the R6 no-writes assertion):* no non-test file in
  `internal/agentplan` calls `os.WriteFile`, `os.MkdirAll`, `os.Create`,
  `os.Symlink`, `os.Link`, `os.Rename`, `os.Remove`, `os.RemoveAll`,
  `os.Chmod`, or `os.Truncate`, or any `exec.` selector -- and none
  references the write-mode open flags (`os.O_WRONLY`, `os.O_RDWR`,
  `os.O_CREATE`, `os.O_APPEND`, `os.O_TRUNC`). Forbidding the write calls
  alone would leave `os.OpenFile` as an unguarded hole; forbidding the
  flags closes it while keeping read-only access legal -- `os.ReadFile`,
  `os.Stat`, and the `O_NOFOLLOW` read-only open the PR-2 composer needs.
  The boundary is "reads inputs, declares outputs," not "pure."

**Family 2 -- the declaration and plan-shape suite.** Pure table tests, no
tmpdir, no filesystem, no built binary:

- *Exhaustive:* for every pair in `Capabilities() x agent.All()` there is
  exactly one `Declaration`; a missing pair, a duplicate, or an unlisted
  capability is a hard failure -- the fail-closed posture of
  `vault.Registry.Build` (`internal/vault/registry.go:88-114`), not
  `agent.known`'s comment-only honor system. Deleting any single
  declaration makes it fail.
- *Well-formed:* `StateUnavailable` implies a non-empty `Reason` and an
  in-range `Kind`; `StateImplemented` implies both zero; `Requires` is
  empty unless implemented.
- *Closed:* every capability in `Requires` is implemented for the same
  agent, and the graph is acyclic.
- *Plan shape:* `Plan(agent, inputs)` over canonical fixture inputs
  asserts that for Codex no entry's path ends in `CLAUDE.md` or
  `CLAUDE.local.md`, for Claude none ends in `AGENTS.md` or
  `AGENTS.override.md`, every entry's `Op` and `Pre` are members of the
  closed sets, every mode is one of the three known values, and every
  entry's `Capability` is declared implemented for that agent. This is the
  test that catches tsukumogami/niwa#248's exact regression as a one-line
  failure: under the plan model, `Plan(AgentCodex, ...)` returning any
  `CLAUDE.*` entry is an assertion, where today the equivalent check needs
  a provisioned tmpdir and a tree walk.

**Family 3 -- wiring and binding.**

- *Wiring:* the executor's only entry point takes a `*Plan`, and
  `internal/workspace` constructs no `agentplan.Entry` and no
  `agentplan.Plan` literal outside the executor -- plans reach it only
  from producer calls. Same AST machinery as family 1. This is the
  structural answer to "threaded and read by nothing": if bytes reach disk
  only via a plan, and plans only come from functions taking the agent,
  the agent is load-bearing by construction rather than by review
  discipline.
- *Binding, both drift directions (R4), per route:*
  - `RoutePlan`: over a canonical maximal fixture exercising every
    plan-borne capability, each implemented (capability, agent) yields at
    least one entry tagged with that capability -- an implemented
    declaration with nothing behind it fails; and, input-independently, no
    producer may emit an entry tagged with a capability not declared
    implemented for its agent -- a delivery without a declaration fails.
  - `RouteProcedure`: `internal/workspace` keeps a registry
    `map[Capability]map[agent.Agent]procedure`, and the test asserts its
    key set equals exactly the implemented procedure-routed declarations
    (trust binds `EnsureCodexTrust` under (DirectoryTrust, Codex) in PR 2;
    plugin install binds the existing function-field seam).
  - `RouteLaunch`: the dispatch path's agent gate becomes a declaration
    lookup, so the inherited scenario 2 asserts the declaration rather
    than a bare refusal (R23) and the gap list and the refusal can't drift
    apart.

**The gap-list drift test (R22)** rides family 2's data: an exported
renderer filters `StateUnavailable` for the agent, groups by `Kind`, and
renders each `Reason`; a test regenerates the guide's marked section and
fails when the committed section differs. Regeneration is a test flag
(`-update`), keeping the toolchain standard. The renderer and its
machinery land in PR 1; the committed guide section lands in PR 2 with the
guide.

**Rejected alternatives.** Filesystem-based structural checks as the
primary mechanism: an apply per assertion, no way to check declarations at
all, and it reproduces the prior attempt's blind spot -- the structure
isn't observable, only its output is. A vet-style analyzer or third-party
lint: a module dependency or a CI surface against N1, for work `go/ast`
does in an ordinary test. grep-based scans: string matching can't tell
code from comments, so the comment-only agent-constant mentions become
false positives.

### Decision 4 -- the no-behavior-change proof (R10, N2, N3)

**Chosen: a ManagedFiles-based characterization test, committed on main
before the first refactor commit.**

niwa already builds the oracle: every pipeline write lands in
`writtenFiles`, and Step 7 (`apply.go:1701-1718`) hashes each into
`ManagedFile{Path, ContentHash, SourceFingerprint, Sources, Generated}`
persisted in `InstanceState.ManagedFiles`. Comparing sorted (path, hash)
pairs is equivalent to comparing full contents without re-reading files,
and it exercises the exact code path production uses to decide what is
managed.

- **Fixture:** one maximal fixture workspace built with the package's
  existing `t.TempDir()` idiom (the `TestCreateIntegration` pattern),
  exercising every materializer PR 1 touches or binds -- workspace, group,
  repo, and subdir content; overlay and global content; workspace context;
  root and instance settings; hooks; env; files; and a live worktree so
  the worktree layers and the delegation surface are pinned.
- **Golden file:** a `testdata/` fixture of sorted
  `relative-path <TAB> normalized-sha256` lines. Hashes only: on mismatch
  the test re-reads the offending file from the tempdir and prints it, so
  diagnosis doesn't require storing content in the repository.
- **Completeness:** the path set comes from `state.ManagedFiles`, not a
  hand-picked list, and the test asserts exact set equality of paths as
  well as per-path hash equality.
- **Normalization one -- the `{workspace}` template variable.** Content
  installers bind it to the absolute instance root, which differs every
  run under `t.TempDir()`. The fixture deliberately uses `{workspace}` so
  the substitution path stays pinned, and the test replaces the known
  instance root with a placeholder in each file's bytes before hashing.
  Normalization, not avoidance.
- **Normalization two -- the executable path in hook commands.**
  `WorktreeDelegation.NiwaPath` is resolved via `os.Executable()`
  (`apply.go:1547`) and embedded in generated hook commands; under
  `go test` it points at a per-run test binary. The `Applier` threads it
  as data, so the test injects a fixed path through that existing seam
  rather than scrubbing output strings.
- **Sequencing:** the test lands in its own commit, passing against main,
  *before* the first refactor commit, so it pins current behavior rather
  than being written to match new code, and it must pass unchanged at PR
  1's head. `Generated` timestamps are excluded from comparison; the audit
  found no other nondeterminism (no hostname, no randomness, and the
  package sorts map keys before writing output everywhere it matters).

Why not the alternatives. The existing unit suite asserts on curated
per-test path lists with no completeness check anywhere -- its broadest
integration test names six paths -- so a refactor that added a file,
dropped an unasserted one, or changed unasserted content passes it
unchanged. The functional Gherkin suite is a real black-box signal, kept
as a one-time manual run before merging PR 1, but as an automated gate
it's a slower, coarser version of the same tree diff. Storing full file
contents in the golden fixture buys the same guarantee for a bigger
fixture; printing the live file on mismatch recovers the diagnostic
benefit. Commit-level discipline stays the review posture, paired with the
test rather than substituted for it.

The manifest's known blind spots -- setup scripts' side effects and the
Claude Code global-registry heals -- sit outside PR 1's
delivery-restructuring scope (R11), so they don't weaken the proof for the
PR it gates; the layout and wiring tests still cover those files' source.

### Decision 5 -- the MCP and session-environment surfaces (R14, R15, R16)

R15 and R16 require agent-neutral declarations generating each agent's
native format, constrained hard by measured codex-cli 0.147.0 behavior:
recursive field-by-field layer merging (a name collision yields a hybrid
server neither party wrote -- niwa's `command` running with the
developer's `args` and `cwd`), whole-config failure on one malformed entry
(valid siblings become unreachable too, and even keys Codex ignores at the
project layer are type-checked first), no SSE transport (a declared
`type = "sse"` is silently served as streamable HTTP, a live failure
rather than a missing server), no `${VAR}` interpolation anywhere, and
trust-gating of both `mcp_servers` and `shell_environment_policy` (an
untrusted project layer isn't parsed at all).

**MCP.** A new workspace-level table:

```toml
[mcp.servers.<name>]
transport = "stdio" | "http" | "sse"   # optional; inferred from command/url
command   = "..."                       # stdio; mutually exclusive with url
args      = ["..."]
env       = { KEY = "value-or-vault-ref" }
url       = "https://..."               # http/sse
headers   = { X-Header = "value-or-vault-ref" }
agents    = ["claude"]                  # optional; restricts generation
```

From it niwa generates Claude's `.mcp.json` and Codex's
`[mcp_servers.<name>]` entries in the instance payload config that
`OpDeliverTree` links into each repository. The four required properties,
mechanized:

1. **Nothing unmappable is ever silent.** The declaration admits `sse`
   because Claude supports it; Codex generation treats an SSE server as a
   hard, named error, never a silent re-protocol, and the error states the
   remedy: scope the server with `agents = ["claude"]`. That per-server
   escape hatch is the deliberate, visible form of "this server is
   Claude-only." A `${` surviving resolution is likewise a hard error
   naming the server and field, for both agents: niwa writes only resolved
   values (property 2), so interpolation syntax that survives resolution
   is an authoring bug, not something to hand to an agent that would
   (Claude) or wouldn't (Codex) expand it.
2. **Full resolution before writing.** `env` and `headers` values are
   `MaybeSecret` and resolve through the existing vault pipeline; what
   lands on disk is literal. Nothing niwa writes relies on load-time
   expansion.
3. **Validation before writing, atomically.** Generated Codex entries are
   checked against the measured schema (exactly one of `command`/`url`; no
   HTTP-only fields on stdio entries and the reverse -- cross-transport
   field misuse is itself a measured whole-config failure), then the
   complete generated TOML document is re-decoded before any bytes reach
   disk, and the payload config is written whole-or-not-at-all
   (temp-then-rename). A generated file must never be the thing that
   bricks a session; on any validation failure niwa reports and writes no
   partial file. "Codex will ignore what it doesn't understand" is false
   as a safety assumption, so everything niwa writes into a Codex layer is
   validated, not only the MCP entries.
4. **Collisions are detected, never left to the merge.** Before writing,
   niwa reads the `[mcp_servers]` names from the developer's own Codex
   configuration -- the same file and the same TOML machinery the trust
   writer already handles -- and a name collision is a hard apply error
   naming the server and both definition sources (loud per R20; the fix is
   renaming one side). An absent developer config means no collision is
   possible. An unreadable or malformed one degrades to a reported skip of
   collision detection: that's consistent with R17's posture that a broken
   developer config fails neither create nor apply, and it's safe on the
   merits, because a config Codex itself cannot load runs no session that
   could see a hybrid. Direct read beats shelling out to
   `codex mcp list --json`, which fails wholesale on a malformed config --
   exactly the case where niwa still needs an answer.

The existing `.mcp.json` file-distribution route keeps working unchanged
as a compatibility path. It is byte-opaque to niwa today (never parsed;
the verbatim writers just copy) and it stays that way; when a workspace
distributes an `.mcp.json` with no structured declaration, apply reports
that its MCP servers reach Claude sessions only.

**Session environment.** A new agent-neutral table, `[session.env]`,
mirroring the shape of the existing `[claude.env]` (inline `vars`,
`promote` from the `[env]` pipeline, vault-resolved secrets). From it niwa
generates Claude's settings `env` block and Codex's
`shell_environment_policy.set` in the payload config, values fully
resolved -- measured: `set` values are literal strings, additive over the
inherited base, overriding on collision. `[claude.env]` stays as the
Claude-specific surface: it writes into a Claude-owned file format and
gates nothing for another agent (Decision 6), and for Claude the two merge
with `[claude.env]` winning per key. Codex reads only the neutral table,
so no Claude-named key gates Codex delivery (R7). Dotenv-file distribution
(`[env]`, row 10) is untouched and stays agent-agnostic. Both delivery
rows carry the measured `Requires: DirectoryTrust` edge.

**Skills without a Claude Code dependency (R14)** is settled here because
it's the same principle -- no other vendor's state in the delivery path.
Github-sourced marketplace content is fetched into a niwa-owned,
git-excluded instance directory
(`<instanceRoot>/.niwa/marketplaces/<name>/`) using the existing
`github.FetchTarball` plus `ExtractSubpath` machinery, and the Codex skill
links resolve there always -- not only when Claude Code is absent -- so
delivery is deterministic across machines. Repo-sourced marketplaces are
already clean.

**Rejected alternatives.** Parsing the `.mcp.json` niwa already
distributes and translating it: lossy in both directions on measured
evidence (SSE and `${VAR}` can't cross; Codex carries fields `.mcp.json`
can't express -- `env_vars`, `cwd`, timeouts, tool allow/deny,
bearer-token and OAuth fields), and it puts one agent's format in front of
another agent's delivery, against R7's spirit. Namespace-prefixing
generated server names instead of detecting collisions: a `niwa-` prefix
changes every name users address, doesn't make collisions impossible, and
hides the conflict rather than surfacing it. Skipping the compatibility
route's report: a silent Claude-only file is the prior attempt's exact
asymmetry, and the report costs one line at apply time.

### Decision 6 -- configuration rename and gate restructure (R7, R8)

Both land in PR 2, never PR 1: a compatibility alias is
behavior-preserving but not diff-free (new warnings, regenerated example
configuration), and PR 1's job is to be invisible. PR 2 is where a second
agent first gives a Claude-named key something to mis-gate.

**`[claude.content]` reverts to `[content]`.** The recorded precedent
(docs/designs/current/DESIGN-claude-key-consolidation.md, commit 81aae0b)
moved `[content]` under `[claude.content]` on the explicit grounds that
content was entirely Claude-coupled -- every consumer wrote hardcoded
`CLAUDE.md`/`CLAUDE.local.md` destinations. Dual-agent capability is
precisely what falsifies that premise: under this design the same declared
content feeds each agent's plan producer, which routes it to that agent's
filenames. **Said plainly: this design partially reverses
DESIGN-claude-key-consolidation.md**, and says so rather than renaming
quietly. `[content]` returns as the canonical agent-neutral table and
`[claude.content]` becomes the deprecated alias, using the same
sibling-field, hand-written zero-check, hard-error-on-both,
warn-on-the-old mechanism the consolidation itself shipped
(`internal/config/config.go:513-528`, `isContentConfigZero` at `:552`)
with its direction flipped.

The alternative considered and rejected was a third name, `[context]`,
whose case is that users who dutifully migrated to `[claude.content]`
would now be whipsawed back, and that a key deprecated in one release and
canonical two releases later is a documentation hazard. The whipsaw is
real but it's mispriced. Count the migrations per cohort. Workspaces that
already moved to `[claude.content]` pay one alias hop either way -- to
`[content]` or to `[context]`, identical cost. Workspaces still on
`[content]`, which is accepted today with a deprecation warning, pay
*nothing* under the reversal (their warning simply disappears) and pay a
second migration under a third name, having already been told once to move
to `[claude.content]`. The reversal is strictly cheaper for one cohort and
equal for the other. It also keeps two live spellings instead of three,
which keeps the both-set error a single unambiguous check rather than
three pairwise ones, and it keeps the table's name consistent with the
`content_dir` key that isn't moving. The documentation hazard is answered
by writing it down: the deprecation message and the migration note name
the reversal and its cause rather than presenting the new canonical name
as though it had always been so.

Migration story, concretely. A workspace on `[content]`: nothing to do;
the deprecation warning stops appearing when PR 2 ships. A workspace on
`[claude.content]`: keeps parsing, gets a deprecation warning pointing at
`[content]`, and moves one table name at its convenience before the v1.0
removal line. A workspace that sets both: a hard error naming both keys,
as today, with the canonical/deprecated roles swapped.

**`content_dir` stays put.** The top-level `workspace.content_dir` key
(`WorkspaceMeta.ContentDir`, `internal/config/config.go:280`) is already
agent-neutrally named -- it names a directory of content, and no agent
name in it gates anything. R8 renames what has something to mis-gate;
this doesn't. The consolidation design's deferred plan to move it under
the Claude namespace is cancelled here rather than left pending.

**`claude.enabled` is restructured, not renamed.** Its documented meaning
today is correct: it gates genuinely Claude-owned deliveries
(`ClaudeEnabled()`, `override.go:1061`). The prior attempt's defect was
wiring it across the boundary -- `claude = false` on a repository silently
disabled all three Codex delivery steps. Relabeling the key
agent-neutrally would reproduce that mis-gating under a new spelling: one
boolean, two agents. Instead the gate becomes an input to the Claude plan
producer only, zeroing Claude's plan-borne deliveries for that scope and
never read by the Codex producer or any generic path. PR 2 adds the
symmetric `[codex] enabled` (workspace and override positions, defaulting
true), read only by the Codex producer, which is what makes the PRD's
acceptance criterion meaningful in both directions. The layout scan plus
the wiring test keep either gate from reaching across: gates live in
producer inputs, and no `internal/workspace` code names an agent to
consult them.

**Everything else keeps its Claude name.** `plugins`, `marketplaces`,
`hooks`, `settings`, `[claude.env]`, `work_summary_hooks`, `pr_body_hook`
all bind to Claude Code's own file formats and mechanisms; no second agent
reads them, so there is nothing for the name to mis-gate. A whole-table
`[claude]` rename was measured and rejected: four Go types, six embedding
sites, roughly 450 occurrences across nineteen production files,
multiplying the alias machinery per embedding site, for six fields whose
Claude names are correct. This design renames exactly what dual-agent
capability makes wrong and nothing more. PR 1 ships zero configuration
renames (R8), which the acceptance criterion on a byte-identical generated
example config enforces.

### Decision 7 -- secret hygiene and the Codex environment defaults (R18)

Two obligations land in the same PR 2 increment that first writes secret
material into a Codex-side file -- not after -- plus one fact the guide
must carry.

- **File mode.** The prior branch wrote the Codex payload config at 0o644,
  safe then (one non-secret integer) and a regression the moment
  `shell_environment_policy.set` carries resolved secrets. Under this
  design the payload config is written at `secretFileMode` (0o600,
  `materialize.go:28`) from the same increment that adds session-env
  generation. The executor makes this a data change -- `Mode: 0o600` on
  the entry -- and the plan-shape test asserts the mode on any entry whose
  capability delivers resolved environment values, so a regression to
  0o644 fails in a pure test.
- **Git-exclude coverage.** The instance root's `.gitignore` covers only
  `*.local*` today; `.codex/config.toml` carries no `.local` infix, and
  `gitexclude.EnsureRepoExclude` is never called for the instance root, so
  a workspace nested inside an outer tracked tree could stage a
  secret-bearing file. In the same increment, `EnsureInstanceGitignore`
  gains an explicit `.codex/` entry at the instance root, and the per-repo
  exclude block's union patterns gain the Codex-side names (`.codex/`,
  `AGENTS.override.md`) exactly as the Claude-side names are covered (row
  24). The plan model carries this as `ExcludeAs` on the entry, so the
  exclusion is declared beside the write that makes it necessary.
- **Codex's default excludes: niwa states the default, doesn't override
  it.** Measured on codex-cli 0.147.0, `ignore_default_excludes` defaults
  to `true`, so Codex's own `*KEY*`/`*TOKEN*` exclude patterns are **not**
  applied -- a Codex session's commands inherit those variables from the
  parent environment unless the developer opts in. niwa does not write
  `ignore_default_excludes`. Setting it `false` would alter the
  developer's session environment beyond what they declared, dropping
  their own inherited key- and token-named variables, and it protects
  nothing niwa delivers (niwa's values ride `set` and survive excludes
  regardless, since the measured pipeline is inherit -> exclude -> set ->
  include_only). The user guide's safety section -- distinct from the
  generated gap list per R22 -- states the measured default plainly, notes
  that it's Codex's behavior rather than niwa's doing, gives the one-line
  opt-in, and carries the adjacent measured trap: a developer's own
  `include_only` allowlist silently drops variables niwa delivers through
  `set`, with no error from Codex. The measurement caveat -- taken through
  Codex's user-invoked sandbox entry point, with the in-session shell tool
  believed but not proven identical -- flows to the standing spike per R24
  before any security-sensitive claim rests on the defaults.

Deferring the mode and excludes to a later hardening pass is rejected
flatly by R18: the window between "secret material lands" and "hygiene
lands" is exactly the exposure.

## Decision Outcome

A leaf `internal/agentplan` owns what may be said -- the closed capability
set, the two-state declaration table with reason kinds, `Requires` edges
and delivery routes, and per-agent plan producers that read inputs and
declare outputs over a closed four-op vocabulary. `internal/workspace`
owns doing -- one agent-blind executor, the probe pre-passes whose
verdicts feed producers as data, and the existing managed-file, cleanup,
and git-exclude bookkeeping now reading three fields off plan entries.
Three stdlib-only test families hold the boundary (layout scan, pure
declaration and plan-shape suite, wiring plus route-wise binding), and a
ManagedFiles-based characterization test committed before the refactor
proves PR 1 changed nothing observable. PR 2 delivers Codex through the
contract: MCP and session environment generated from `[mcp.servers.*]` and
`[session.env]`, validated and fully resolved before an atomic write;
trust as a delivered capability the closure test enforces; approval and
sandbox posture opt-in, reported, and never inferred one from the other;
the `[content]` reinstatement and the per-agent gate restructure; and
secret file mode plus git-exclude landing with the first secret.

## Solution Architecture

Package layout and dependency direction (`internal/cli` imports
`internal/workspace`, which imports `internal/agentplan`, which imports
`internal/agent` and `internal/config`; nothing in the leaf imports
upward):

```
internal/agent          (unchanged leaf; gains exported All())
internal/agentplan      (new leaf; imports internal/agent, internal/config, stdlib)
    capability.go       Capability enum, Capabilities(), Route
    declaration.go      Declaration, the 24x2 table, Lookup, validation helpers
    plan.go             Op, Precondition, Entry, Plan
    provenance.go       SourceEntry, ComputeSourceFingerprint (moved from workspace)
    producer_claude.go  Claude plan producers (PR 1)
    producer_codex.go   Codex plan producers (PR 2)
    probespec.go        probe declarations the workspace pre-pass executes (PR 2)
    gaplist.go          RenderGapList(agent) for the guide section
internal/workspace      (executor + pipeline integration; loses agent literals)
    planexec.go         applyPlan: ops, preconditions, containment, bookkeeping
    (state.go keeps `type SourceEntry = agentplan.SourceEntry`)
```

Like `internal/keyreport`, `internal/agentplan` is a leaf relative to
workspace that may still import `internal/config`. Its package doc follows
the house bar: first sentence says what it does, the rest names the
specific callers and the cycle the boundary avoids.

Data flow, per apply:

```
EffectiveConfig ──narrow inputs──> agentplan producers ──Plan──> applyPlan ──> disk
        │                              ▲                            │
        │        probe verdicts        │                            ├─ writtenFiles/ManagedFiles (Step 7)
        └──> workspace pre-passes ─────┘                            ├─ ExcludeAs -> gitexclude union
             (ownership, harness)                                   └─ Warnings -> Reporter
```

1. `internal/workspace` assembles `Inputs` per level from
   `EffectiveConfig` and applies each agent's own gate
   (`claude.enabled` / `codex.enabled`) to that agent's plan production
   only.
2. For each agent in `agent.All()`, `agentplan.Plan(ag, in)` produces the
   declared entries, warnings, and exemptions. Both agents' plans are
   produced on every apply -- no agent choice at creation or apply time
   (R19).
3. `applyPlan` applies entries: MkdirAll-and-write, append-unless-present,
   delimited-section replace, and symlink-or-bounded-copy (the last op's
   ~150 lines exist on the prior branch and move here as its meaning).
   Preconditions execute generically -- `IfSourceExists` stats the entry's
   `Source`, `IfNotForeign` re-checks the ownership verdict at write time,
   preserving the prior branch's belt-and-braces against the
   detect-to-write race in generic code -- and the existing containment
   and symlink-escape checks apply uniformly to every entry.
4. Written paths feed the managed-file record exactly as today;
   `ExcludeAs` patterns feed the per-repo git-exclude call; warnings feed
   the reporter. Where a declaration is implemented for Codex, a delivery
   failure fails the apply loudly (R20).
5. Procedure-routed capabilities (trust, plugin install, git-exclude
   bookkeeping) run as today, behind the workspace-side binding registry
   keyed by (capability, agent); launch-routed capabilities gate on
   `agentplan.Lookup`. Implemented declarations resolve to a registered
   delivery, unavailable ones to none (R4).

What deliberately does not move: the pipeline's step ordering, the
`Materializer` interface, the config-driven materializers with no
agent-specific logic (dotenv files, arbitrary file distribution, hook
installation -- declared and bound under the contract from PR 1 per R11,
restructured only if agent-specific behavior ever lands in them, which R4
and R5 prevent happening outside the contract), `CheckDrift`, the state
schema, `override.go`'s merge functions, the plugin-installer
function-field seam, and the global-registry heals.

Codex delivery composition in PR 2 follows the measured discovery rules
(R12). The one piece worth naming here is how a conflict binds: when a
repository commits its own file at one of niwa's names, the ownership
verdict gates the entry via `IfNotForeign`, the refusal lands in
`Plan.Warnings`, and the path lands in `Plan.Exempt` so cleanup leaves it
alone. The payload directory at the instance root carries the generated
Codex config -- budget, MCP servers, session env, posture when declared --
and the skill trees, delivered into each repository by `OpDeliverTree`.

## Implementation Approach

**PR 1 -- the contract, no behavior change (R9, R10, R11).** Increments
ordered so every commit keeps the characterization test and the full suite
green.

1. Characterization test, its own commit, passing against main
   (Decision 4).
2. `internal/agentplan`: capability enumeration with routes, declaration
   table (Claude's column complete; Codex's column stating main's truth),
   plan vocabulary with the first three ops, `agent.All()`. Tests: the
   exhaustive, well-formed, and closure suite; the gap-list renderer and
   its drift machinery.
3. Executor `applyPlan` in `internal/workspace`; pipeline Step 7 reads
   entries; provenance types move down with the alias. Tests: executor
   unit tests, characterization unchanged.
4. Convert the agent-shaped surfaces to plan production: the eight
   context-writer sites and the settings-document builder
   (`buildSettingsDoc` already returns a document and writes nothing; its
   three call sites each carry a marshal-mkdir-write copy, and conversion
   deletes two of the three). Delete the dead `rootClaudeFile` constant
   and retire `LocalContextFileName()`'s zero-caller state by having
   producers consult the agent's filenames. Tests: the layout scan's
   filename half flips red to green; plan-shape suite; wiring test;
   characterization unchanged.
5. Binding registry for the procedure and launch routes over existing
   seams. Tests: the two-direction binding checks.

Exit criteria: every contract and structure acceptance criterion green; no
configuration surface change (example config byte-identical, no new
warnings); full suite passes with no existing test modified or deleted;
`gofmt -l .`, `go vet ./...`, `go test -race ./...`; one manual functional
run as a sanity check.

The honest risk is step 4: the context writers carry accumulated boundary
rules (overlay append, subdir content, `@import` migration removals) that
a mechanical conversion can drop, and two of them are the half-broken
functions where the hardcoded filenames live today. That is exactly where
the characterization test is pointed -- any dropped rule changes a
produced path or hash and fails the pinned manifest.

**PR 2 -- Codex as the second implementation (R12-R24).**

1. `OpDeliverTree`, `IfNotForeign`, `Plan.Exempt`, and the
   ownership/harness probes as workspace pre-passes with leaf-declared
   specs -- each arriving with its first consumer.
2. Codex plan producers: context composition within the measured
   discovery rules (lifting the retained branch's mechanics), the payload
   tree, and the worktree layer standing on the measured `.git`-pointer
   result (R12, R13).
3. Trust writer bound under (DirectoryTrust, Codex); rows 3, 4, 8, 9, and
   12 gain their measured `Requires` edges (R17).
4. `[mcp.servers.*]`: generation of both formats, validation, atomic
   write, collision detection, and the compatibility-route report
   (Decision 5, R15).
5. `[session.env]`: both formats, full resolution -- and in the same
   increment, payload mode 0o600, the instance-root `.codex/` gitignore
   entry, and repo exclude patterns for the Codex-side names (Decision 7,
   R16, R18).
6. Approval and sandbox posture from its own agent-neutral declaration:
   absent by default, reported when written, approvals and sandbox mapped
   from separate sources with a test that an approval-only declaration
   emits no `sandbox_mode` (R21).
7. Skills fetch into the niwa-owned marketplace directory (R14).
8. Renames and gates: `[content]` reinstated as canonical with
   `[claude.content]` as the deprecated alias, `claude.enabled` as a
   Claude-producer input only, `[codex] enabled` added symmetrically
   (Decision 6). Deprecation warning and both-set error tested.
9. Declaration flips (eleven Codex rows to implemented), the generated
   guide gap list plus the separate safety section, and the drift test now
   asserting the committed section (R22).
10. Functional scenarios: all 15 inherited pass, scenario 2 asserting the
    dispatch declaration, scenario 10 in its original measured-valid form,
    and every restructuring named in the PR description (R23).
11. Measurements posted to tsukumogami/niwa#254 as structured comments, or
    committed to the spike file if it has merged by then -- including the
    two spike corrections the PRD names. No new spike document (R24).

## Security Considerations

- **Resolved secrets on disk.** The Codex payload config carries fully
  resolved environment values, so it is written at `secretFileMode`
  (0o600) and git-excluded in the same increment that first makes that
  true (Decision 7). Plan entries make both properties assertable in a
  unit test rather than a review habit. Provenance metadata carries
  fingerprints, never secret values, preserving the existing redaction
  contract.
- **Writes to the developer's own configuration.** The trust writer stays
  additive, canonical, lock-serialized, retracting only keys niwa itself
  wrote, and tolerant of an unreadable config. MCP collision detection
  reads that file and writes nothing to it; where the file can't be read,
  detection is skipped with a report rather than guessed at, which is safe
  because a config Codex can't load runs no session that could see a
  hybrid. Elsewhere, the recursive field-level merge means a name
  collision yields a definition neither party wrote, so detection plus a
  hard error is what keeps niwa from corrupting a developer's setup by
  name coincidence.
- **A generated file must never brick a session.** One malformed entry --
  including a malformed key Codex would otherwise ignore -- fails Codex's
  whole config load for the directory. Everything niwa writes into a Codex
  layer is validated and re-decoded before an atomic write; failure
  reports and leaves nothing partial on disk.
- **Posture is never weakened uninvited, and never by inference.** With no
  declared posture, niwa writes neither `approval_policy` nor
  `sandbox_mode`, and Codex's defaults stand; what niwa does write is
  reported at apply time. Because `sandbox_mode = "danger-full-access"`
  collapses approvals and both sandboxes into one setting -- an asymmetry
  Claude Code's `bypassPermissions` does not have -- niwa never derives a
  sandbox change from an approval declaration. The capability ships rather
  than being withheld because the equivalent Claude-side escalation
  already exists in shipped code (`materialize.go:295-298`, consumed at
  `:669`), and a contract that declares one agent's route unavailable
  while the other's ships is the asymmetry this work exists to remove.
- **Environment inheritance.** Codex's `ignore_default_excludes` defaults
  to true, so sessions inherit `*KEY*`/`*TOKEN*` variables -- Codex's
  behavior, not niwa's write. niwa doesn't alter the developer's posture;
  the guide's safety section states the default, the opt-in, and the
  measured `include_only` trap. The in-session confirmation of the
  sandbox-entry-point measurement is tracked to the standing spike (R24)
  before any security claim rests on it.
- **Path safety.** The executor applies the existing containment and
  symlink-escape checks uniformly to every entry, centralizing what each
  writer previously hand-rolled, and the PR-2 composer's `O_NOFOLLOW` read
  of committed files is preserved with the `IfNotForeign` re-check keeping
  the detection-to-write window as narrow as today's.
- **The leaf cannot exfiltrate by construction.** The layout scan's
  no-write/no-exec assertion -- write calls and write-mode open flags
  alike -- means the layer that decides what agents get has no
  filesystem-write or process-launch surface to misuse. Every write
  funnels through one executor whose operations are closed and reviewed
  once.
- **Supply chain.** No new module dependencies (N1); the github
  marketplace fetch reuses the existing tarball machinery and lands in a
  git-excluded, instance-scoped directory.

## Consequences

Positive:

- The prior attempt's failure mode is structurally unrepeatable: bytes
  reach disk only through a plan, plans only come from a function taking
  the agent, and a faked implementation fails a pure table test instead of
  compiling quietly.
- "What does a Codex session get?" has one answer, in code, that the guide
  is generated from and a drift test guards -- code and doc cannot
  disagree silently.
- The first PR is reviewable mechanically: a reviewer checks that the
  characterization and structural tests exist and pass, rather than
  auditing the diff for behavior change.
- A third agent has a defined job: fill a column, supply producers, and
  let the exhaustiveness test confront it with the whole capability set.
- Two duplicated settings-write blocks are deleted, and the executor
  centralizes containment checks and bookkeeping writers previously
  hand-rolled.

Negative, accepted:

- An intermediate representation now stands between config and disk, and
  one more indirection between a config value and the write it causes when
  debugging. The growth risk is real; the mitigation is the closed `Op`
  enum, where adding a member is a named design decision rather than a
  field slipped into a struct -- the correct price for a new way niwa
  touches a user's repository.
- `SourceEntry` moves down a package (softened by the type alias, so the
  state schema and existing references don't change), and the eight
  context writers are rewritten as producers -- the riskiest part of PR 1,
  pinned by the characterization test.
- The declaration table is 48 rows of hand-maintained data, and a few rows
  read oddly out of context (trust is "no such concept" for Claude). The
  table's honesty is machine-enforced, so maintenance errors are loud, and
  the guide's rendering rule absorbs the odd rows as "does not apply"
  notes.
- Workspaces that migrated to `[claude.content]` get a second, reversed
  deprecation cycle -- the cost of reversing a decision whose premise
  dual-agent capability falsified, paid in PR 2 where the old name becomes
  actively misleading rather than merely inert. Workspaces still on
  `[content]` pay nothing and simply stop seeing a warning.
- The `codex.enabled` gate adds a second per-agent switch where one
  boolean used to (incorrectly) suffice.
- The characterization test pins behavior aggressively: legitimate
  intentional output changes in later work must update the golden file
  knowingly, which the `-update` flag makes a visible, reviewable act.

## References

- docs/prds/PRD-agent-capability-contract.md -- the upstream requirements
  (R1-R24, N1-N3), the 24-row capability matrix with target declarations,
  and the recorded decisions this design implements.
- docs/briefs/BRIEF-agent-capability-contract.md -- the framing: contract
  first, provably behavior-preserving, Codex as second implementation.
- docs/spikes/SPIKE-codex-discovery-mechanics.md (landing via
  tsukumogami/niwa#254) -- measured Codex discovery behavior, consumed not
  re-derived; destination for this work's new measurements per R24.
- docs/designs/current/DESIGN-claude-key-consolidation.md and commit
  81aae0b -- the config rename precedent this design follows, and the
  consolidation decision it partially reverses (Decision 6); the alias
  mechanism itself at `internal/config/config.go:513-528`.
- tsukumogami/niwa#248 (branch retained at `docs/dual-agent-workspace`) --
  the closed prior attempt: sound Codex composition mechanics, the 15
  functional scenarios that set the acceptance bar, and the structural
  failure the tests in Decision 3 exist to make unrepeatable.
- `internal/vault` (`provider.go`, `registry.go:88-114`) -- the house
  precedent for a mandatory interface with fail-closed registry lookup,
  mirrored by the declaration table's posture.
- `internal/workspace/apply.go:1701-1718`, `state.go:184-190` -- the
  managed-file record the characterization test pins.
- `internal/workspace/materialize.go:295-298` and `:669` -- the shipped
  Claude-side mapping from a workspace `permissions` declaration to
  `permissions.defaultMode`, the parity precedent behind row 12.
