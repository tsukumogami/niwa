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
  the per-agent declaration table (two states, reason kinds, Requires
  edges), and per-agent plan producers that read inputs and declare outputs
  as data. internal/workspace gains one agent-blind executor over a closed
  four-op set (write-file, append-line, replace-section, deliver-tree).
  Three structural tests (AST layout scan, exhaustiveness/plan-shape table
  test, wiring test) plus a ManagedFiles-based characterization test
  committed before the refactor enforce every claim. MCP delivery is
  generated from a structured agent-neutral declaration, validated before
  writing. The content config rename and the claude.enabled restructure
  both land in PR 2.
rationale: |
  The codebase already separates "decide what to say" from "write it"
  almost everywhere; the plan model names that existing shape once instead
  of a fifth ad-hoc instance. It is the only considered option under which
  the contract's core properties are assertable in pure table tests with no
  tmpdir, which is what makes the prior attempt's failure -- a seam threaded
  everywhere and load-bearing nowhere -- structurally unrepeatable: bytes
  reach disk only through a plan, and plans only come from a function that
  takes the agent.
---

# DESIGN: agent capability contract

## Status

Current

This design owns the mechanism for the agent capability contract: the
package layout, the plan types and closed operation set, the declaration
model and its tests, the no-behavior-change proof, the MCP generation
surface, the configuration rename and gate restructure, and the
secret-hygiene obligations. The upstream PRD owns the requirements
(R1-R24, N1-N3) and the 24-row capability matrix; this design cites them
and does not re-open them. Codex discovery mechanics are consumed from the
standing spike (docs/spikes/SPIKE-codex-discovery-mechanics.md, landing via
tsukumogami/niwa#254) and from the measured codex-cli 0.147.0 findings the
PRD records and R24 routes back to that spike; nothing here re-derives
them.

## Context and Problem Statement

niwa's workspace-preparation path delivers roughly twenty-four
capabilities -- context files, settings, hooks, plugins, skills,
environment, trust, git-exclude bookkeeping -- and nearly all of it is
Claude-shaped by construction. The `agent.Agent` type governs two of those
capabilities; everything else takes no agent parameter and runs
Claude-shaped unconditionally. The defect is live on main:
`agent.LocalContextFileName()` has zero callers anywhere in the module,
because the two functions that accept an agent parameter use it only as a
run/skip gate and then hardcode `"CLAUDE.local.md"` inside the gated body
(`internal/workspace/content.go:156`, `worktree_content.go:743`).

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
- **The first PR must be invisible.** No behavior change, no config
  surface change, provable mechanically rather than by diff audit (R9,
  R10, N2).
- **Honesty over coverage.** Every (capability, agent) pair is implemented
  or declared unavailable with a machine-readable reason; the user guide's
  gap list is generated from those declarations (R2, R22).
- **House style, not invention.** The repo's precedents govern:
  `internal/vault`'s fail-closed registry posture, leaf packages carved
  out with a stated caller and cycle (`internal/gitexclude`,
  `internal/envformat`, `internal/keyreport`), the
  `[content]` -> `[claude.content]` rename mechanism
  (docs/designs/current/DESIGN-claude-key-consolidation.md, commit
  81aae0b). There is no in-repo precedent for per-agent files
  (`foo_claude.go` / `foo_codex.go`); per-agent variation is always a
  method with branches or a map keyed by `agent.Agent`.
- **Standard toolchain only.** Structural and characterization tests use
  `go/ast`, `go/parser`, `go/token`; no new module dependency (N1).
- **Measured Codex behavior is authoritative.** Whole-config load failure
  on one malformed entry, recursive field-level layer merge, no SSE
  transport, no `${VAR}` interpolation, trust-gated project layer -- all
  measured against codex-cli 0.147.0 and recorded in the PRD; the design
  treats them as hard constraints, never as assumptions to revisit.

## Considered Options

### Decision 1 -- structural shape: plan-producing leaf package plus an agent-blind executor (R4, R5, R6, R11)

**Option A (chosen): a leaf package producing declarative plans, one
generic executor in `internal/workspace`.**

A new leaf package, `internal/agentplan` -- sibling to `internal/agent`,
importing `internal/agent` and `internal/config` and nothing above them --
holds three things: the closed capability enumeration with its per-agent
declaration table (Decision 2), the plan types below, and per-agent plan
producers, which are functions from narrow config inputs to a `Plan`.
`internal/workspace` gains one executor, `applyPlan`, that implements a
closed operation set and contains no agent name and no agent context
filename.

The closed operation set, sized empirically from the write sites in scope
on main:

```go
// Op is the closed set of primitive operations an agent's plan may
// declare. Adding a member is a design change, not an implementation
// detail.
type Op uint8

const (
    OpWriteFile      Op = iota // write Content at Path, with Mode
    OpAppendLine               // append Content to Path unless already present
    OpReplaceSection           // replace the region delimited by Marker
    OpDeliverTree              // symlink Source at Path; copy on failure
)

// Precondition is the closed set of conditions gating an entry.
type Precondition uint8

const (
    Always          Precondition = iota
    IfSourceExists               // stat Source first; absent means no-op
    IfNotForeign                 // consult the ownership verdict at write time
)

// Entry is one declared write.
type Entry struct {
    Op        Op
    Path      string        // absolute target
    Content   []byte        // OpWriteFile / OpAppendLine / OpReplaceSection
    Source    string        // OpDeliverTree source; IfSourceExists probe path
    Mode      os.FileMode   // 0o600, 0o644, or 0o755
    Marker    string        // OpReplaceSection delimiter
    Pre       Precondition
    Managed   bool          // participates in ManagedFiles + cleanup
    ExcludeAs string        // extra git-exclude pattern implied, "" for none
    Sources   []SourceEntry // provenance, for SourceFingerprint
}

// Plan is one agent's whole declared output for one level.
type Plan struct {
    Entries  []Entry
    Exempt   []string // paths cleanup must not delete though not produced
    Warnings []string // conflicts, refusals, hoist omissions
}
```

Four ops suffice because the evidence says so, not because the model hopes
so: the write sites in the preparation path are `os.MkdirAll` plus
`os.WriteFile` with one of three modes, and each site that isn't is
already a single named helper with a documented closed rule -- the
`@import` accumulation in `.claude/rules/workspace-imports.md`
(append-unless-present, `workspace_context.go`), the worktree context
layer's delimited-section replace (`worktree_content.go`), and the Codex
payload's symlink-or-bounded-copy delivery on the prior branch
(`OpDeliverTree`, the one op whose implementation is genuinely imperative
and lives in the executor). If a fifth op is ever needed, that is a design
conversation -- the correct price for adding a new way niwa touches a
user's repository.

The bookkeeping vocabulary is three fields (`Managed`, `ExcludeAs`,
`Sources`) because niwa's managed-file machinery is a post-hoc pass over a
flat path list, not something writers participate in: `runPipeline` Step 7
walks written paths, hashes each, and builds
`ManagedFile{Path, ContentHash, SourceFingerprint, Sources, Generated}`
(`internal/workspace/apply.go:1695-1718`, `state.go:184-190`), cleanup
deletes prior-state paths absent from the produced set, and git-exclude is
one idempotent call per repo. `Exempt` is the one new concept, and it
exists for a documented reason on the prior branch: the Codex conflict
verdict needs "do not delete a path I refused to write." It is introduced
in PR 2 with its first consumer, not in PR 1 as a dead field -- an unused
seam is the exact smell this work exists to remove.

The boundary is stated as **reads inputs, declares outputs** -- not
"pure." The plan producers do read: niwa-owned content sources, overlay
stat probes, and (in PR 2) a guarded `O_NOFOLLOW` read of a repository's
own committed context file. What they never do is write or launch
anything, and that line is mechanically checkable: an AST test asserts the
leaf package never calls `os.WriteFile`, `os.MkdirAll`, `os.Symlink`,
`os.Remove`, `os.Chmod`, or `exec.Command` (Decision 3, test 1). "Pure"
would be a nicer word and a false claim; "reads inputs, declares outputs"
is the honest boundary and it is just as enforceable.

One supporting move: `SourceEntry` and `ComputeSourceFingerprint`
(`internal/workspace/state.go:198-249`) migrate into the leaf so plan
entries can carry provenance without an import cycle. They are pure
metadata -- four strings and a sha256-plus-sort, documented as never
carrying secret material -- and their JSON tags and the state schema do
not change. `EffectiveConfig` (`internal/workspace/override.go:17-37`)
does not move: the plan producers take narrow input structs that
`internal/workspace` fills, matching the house idiom of call-site-sized
inputs, rather than dragging a 31-reference workspace type down a layer.

**Option B (rejected): an interface implemented inside
`internal/workspace`, with per-agent files.**

Define `AgentMaterializer` in `internal/workspace`, implement it in
`materialize_claude.go` and (later) `materialize_codex.go`, and have the
pipeline iterate implementations. Its real advantages deserve stating: it
is the conventional Go answer; it needs no new package, no type moves, and
no executor; each agent's logic sits in one file a reviewer can read
top-to-bottom; and the diff for PR 1 would be smaller.

It loses on the properties this feature exists for. Its only observable is
the filesystem, so every contract assertion costs a tmpdir and a full
apply -- there is no cheap way to ask "what would Codex get?" without
provisioning an instance and walking the tree. The layout test (Decision
3, test 1) survives, but the plan-shape and wiring tests do not exist in
this shape at all: an interface method that writes directly can hardcode a
filename three lines above its `return nil` and nothing but a filesystem
diff notices. That is not hypothetical -- it is the mechanism by which
tsukumogami/niwa#248 failed while compiling cleanly. And the
`foo_claude.go` / `foo_codex.go` convention has no precedent anywhere in
this repo; `internal/workspace` has never been split, and the repo's
established shape for a closed agent set is branches or maps over
`agent.Agent`, not per-implementation files.

**Chosen: Option A.** It is the generalization of a shape niwa has already
built four separate times ad hoc -- `WorktreeDelegation` (a decision
computed once, threaded as data to writers), the prior branch's
`CodexRepoVerdict` (a detection pass consulted by writers and cleanup),
`InstalledHooks` (one materializer's output consumed as another's input),
and the rendered context layers. The plan is the fifth instance, named
once. And it is the only option under which the agent parameter is
load-bearing by construction: there is no path from config to bytes that
bypasses `Plan(agent, inputs)`.

### Decision 2 -- capability state model: two states, required reasons, requirement edges (R1, R2, R3)

**Chosen: two states, a required reason kind on unavailability, and
`Requires []Capability` edges on implementation.**

```go
type State int

const (
    StateImplemented State = iota + 1
    StateUnavailable
)

type ReasonKind int

const (
    ReasonAgentCannotReceive ReasonKind = iota + 1 // the agent's own mechanics put it out of reach
    ReasonNoSuchConcept                            // the thing does not exist for this agent
    ReasonNotBuilt                                 // a route exists that niwa has not built
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

The capability set is the PRD's 24 rows, exported and enumerable
(`agentplan.All()`), with the two deliberate exclusions recorded beside it
so the closure doesn't look arbitrary: vault-backed secret resolution (an
upstream source feeding environment delivery, not something a session
receives) and the `claude.enabled` gate (a gate over deliveries, not a
delivery -- see Decision 6). `internal/agent` gains an exported
`agent.All()`; today `known` is unexported and every "for each agent" test
hand-lists the two constants.

**Why a third "conditional" state was rejected.** The tempting middle
state -- "implemented, but only when the directory is trusted" -- rests on
a misreading. Codex's project layer is inert without a trust entry in the
developer's own configuration, but niwa is not a project layer: it is a
tool the developer runs on their own machine, and the prior branch already
built the trust write (`codex_trust.go` on `docs/dual-agent-workspace`:
TOML-surgical, additive, lock-serialized, canonical paths, retracting only
keys niwa itself wrote). Trust is therefore a capability niwa delivers --
row 23 of the PRD's matrix -- not a precondition it waits on. Once named,
everything "conditional" becomes plainly implemented with an edge:

```
MCPServers(codex).Requires      = [DirectoryTrust]
SessionEnv(codex).Requires      = [DirectoryTrust]
RepoContext(codex).Requires     = [DirectoryTrust]
ApprovalPosture(codex).Requires = [DirectoryTrust]
DirectoryTrust(codex).State     = StateImplemented
```

and the closure test -- every capability named in `Requires` is itself
implemented for the same agent, and the graph is acyclic -- enforces the
honesty by construction. Declaring Codex MCP implemented while trust is
unavailable is a test failure, the PRD's canonical failing case. A soft
"conditional" state would have absorbed exactly that error instead of
catching it, and it would have forced the guide's gap-list generator to
make a judgment about which conditional rows count as gaps -- the precise
failure mode of the prior attempt's scattered documentation. Two states
keep the generator a filter (R22).

Preconditions niwa cannot own -- the developer declining a prompt, an OS
feature -- are not expressible as requirements, because a requirement must
name a capability. That forces the honest declaration: `StateUnavailable`
with a reason. The type system refuses "works, sort of, if."

The accepted cost: a few rows read oddly for one agent out of context.
`DirectoryTrust` for Claude is `ReasonNoSuchConcept` (Claude Code keeps no
per-directory trust record). The guide renders `ReasonNoSuchConcept` rows
as short "does not apply" notes and the other two kinds as the gap list
proper -- a rendering rule over an enum, not a judgment call.

### Decision 3 -- the three structural tests (R5, R6, N1)

All three use only `go/parser`, `go/ast`, and `go/token`; they run under
the existing `go test -race ./...` with no new dependency.

**Test 1: the layout scan (R5, R6).** An AST walk over non-test files
asserting two things. In `internal/workspace`: no file names
`agent.AgentClaude` or `agent.AgentCodex` in code, and no string literal
in {`"CLAUDE.md"`, `"CLAUDE.local.md"`, `"AGENTS.md"`,
`"AGENTS.override.md"`} appears. In `internal/agentplan`: no call to
`os.WriteFile`, `os.MkdirAll`, `os.Symlink`, `os.Remove`, `os.Chmod`, or
`exec.Command` -- the mechanical form of "reads inputs, declares outputs"
(R6).

The agent-constant half passes on today's tree (the only occurrences are
in comments). The filename-literal half is **red on main at eight sites**:
`content.go:156`, `content.go:186`, `content.go:208`,
`worktree_content.go:743`, `workspace_context.go:196`,
`workspace_context.go:229`, `workspace_context.go:411`, and the dead
`rootClaudeFile` constant at `root_materializer.go:51` (referenced only
from its own test file). This is a feature, not a defect: a structural
test that is red before the conversion and green after is itself a
deliverable of PR 1, and it proves the test can fail -- the exact property
the prior attempt's structure lacked. R5 requires precisely this
meaningful-on-day-one behavior.

**Test 2: the exhaustiveness and plan-shape table test (R2, R3, R4).** A
pure table test over `agent.All() x agentplan.All()`, no tmpdir, no
filesystem, no built binary:

- exactly one `Declaration` per pair; a missing or duplicate pair fails,
  and deleting any single declaration makes it fail;
- well-formedness: `StateUnavailable` implies non-empty `Reason` and an
  in-range `Kind`; `StateImplemented` implies both zero; `Requires` empty
  unless implemented;
- closure: every required capability implemented for the same agent,
  graph acyclic;
- plan shape: for the Codex producer, no entry's `Path` ends in
  `CLAUDE.md` or `CLAUDE.local.md`; for the Claude producer, none ends in
  `AGENTS.override.md`; every entry's `Op` and `Pre` are members of the
  closed sets; and each entry maps to a capability the table declares
  implemented for that agent -- while every implemented declaration is
  claimed by at least one producer path or registered delivery, closing
  both drift directions of R4.

This is the test that catches tsukumogami/niwa#248's exact regression.
That PR iterated a hardcoded agent slice at every call site while the
threaded agent value was read by nothing; under the plan model,
`Plan(AgentCodex, ...)` returning any `CLAUDE.*` entry is a one-line
assertion failure. Today the equivalent check requires provisioning an
instance and walking the tree. An unlisted pair is a hard failure -- the
`vault.Registry.Build` fail-closed posture, not the `agent.known`
honor-system list.

**Test 3: the wiring test (R4, R5).** The same AST machinery asserts that
the executor's only entry point takes a `*Plan`, and that
`internal/workspace` constructs no `agentplan.Plan` value except through
`agentplan.Plan(ag, ...)` -- no composite literals of the plan types
outside the leaf and the executor's own package. This is the structural
answer to "threaded and read by nothing": if bytes can only reach disk via
a plan, and plans only come from a function that takes the agent, then the
agent parameter is load-bearing by construction rather than by review
discipline.

### Decision 4 -- the no-behavior-change proof (R10, N2, N3)

**Chosen: a ManagedFiles-based characterization test, committed on main
before the first refactor commit.**

niwa already builds a near-complete produced-file manifest with content
hashes at apply time: every pipeline write lands in `writtenFiles`, and
Step 7 (`internal/workspace/apply.go:1695-1718`) turns each path into a
`ManagedFile{Path, ContentHash, ...}` persisted in
`InstanceState.ManagedFiles`. The characterization test builds one or two
representative fixture workspaces (reusing the existing
`TestCreateIntegration`-style fixture idiom), runs `Create`, and asserts
the sorted `(Path, ContentHash)` pairs from `state.ManagedFiles` against a
checked-in expectation -- the apply path's own record of everything it
wrote, not a hand-picked subset. Comparing path-plus-hash pairs is
equivalent to comparing full contents without re-reading files, and it
exercises the exact code path production uses to decide what is managed.

Why not the alternatives: the existing unit suite asserts on curated
per-test path lists and has no completeness check -- the broadest
integration test asserts on six named paths and never compares
`ManagedFiles` as a set -- so a refactor that added a file, dropped an
unasserted file, or changed unasserted content would pass it unchanged.
The functional Gherkin suite is a real black-box signal, worth one manual
run before merging PR 1, but as an automated before/after gate it is a
slower, coarser version of the same tree diff. Commit-level discipline (a
visibly mechanical diff) is kept as the review posture, paired with the
characterization test rather than substituted for it.

Ordering is the point: the test lands in its own commit that predates the
first refactor commit, so it pins current behavior rather than being
written to match new code. PR 1 must leave it passing unchanged, and the
acceptance criteria additionally require that no existing test is modified
or deleted at PR 1's head.

Two nondeterminism sources are normalized, and only these two exist -- the
package already sorts every map before writing output, embeds no
hostnames, and uses timestamps only in metadata:

1. **The `{workspace}` template variable.** Content installers bind it to
   the absolute instance root, so fixture content using it differs per
   `t.TempDir()` run. The test normalizes by replacing the known fixture
   root with a placeholder before comparing (or the fixture avoids the
   variable; normalizing is preferred so the variable stays covered).
2. **`os.Executable()` in worktree-delegation hook commands.** The
   resolved niwa binary path is embedded into generated hook commands via
   `WorktreeDelegation.NiwaPath` (`apply.go:1547`), and under `go test` it
   points at a per-run temp build. The `Applier` threads it as data, so
   the test injects a fixed path -- a seam that exists, not a rewrite.

`ManagedFile.Generated` timestamps are stripped before comparison; they
change every run by design. The manifest's known blind spots -- setup
scripts' side effects and the Claude Code global-registry heals -- sit
outside PR 1's delivery-restructuring scope (R11), so they do not weaken
the proof for the PR it gates; the layout and wiring tests, which are not
scoped to the manifest, still cover those files' source.

### Decision 5 -- the MCP surface (R15)

**Chosen: a structured, agent-neutral MCP server declaration in workspace
configuration, from which niwa generates each agent's native format --
Claude's `.mcp.json` and Codex's `[mcp_servers.*]` project-layer table.
The existing verbatim `.mcp.json` distribution keeps working unchanged as
a compatibility path that reaches Claude sessions only, and apply says so:
a workspace distributing `.mcp.json` with no structured declaration gets
an apply-time report that its MCP servers reach Claude sessions only.**

The rejected alternative -- parse the `.mcp.json` niwa already distributes
and translate it -- fails on measured evidence, not taste. Translation is
lossy in both directions: Codex has exactly two transports (`stdio`,
`streamable_http`) and no SSE, and it does not reject a declared
`type = "sse"` server -- it silently serves the URL over streamable HTTP,
a different wire protocol, which is a live failure rather than a missing
server. Codex performs no `${VAR}` interpolation anywhere, so a
`.mcp.json` relying on expansion cannot be copied through. And Codex
carries fields no `.mcp.json` can express (`env_vars`, `cwd`, per-server
timeouts, `bearer_token_env_var`, `env_http_headers`, OAuth fields). A
Claude-format file as the source of Codex delivery would also put one
agent's format in front of another agent's delivery, against R7's spirit.

Four measured codex-cli 0.147.0 behaviors are hard constraints on the
generator (all recorded in the PRD and routed to the standing spike per
R24):

1. **One malformed entry fails the whole config load.** A single bad
   `[mcp_servers.*]` entry makes Codex's entire configuration for the
   directory unloadable, valid sibling servers included -- and even
   denylisted keys are type-checked before being ignored, so a malformed
   ignored key bricks the load too. "Codex will ignore what it doesn't
   understand" is false as a safety assumption. Therefore niwa validates
   everything it writes into a Codex layer -- not only MCP entries --
   before writing, and a generated file must never be the thing that
   bricks a session. On validation failure, apply reports the error and
   writes no partial file.
2. **Name collision produces a hybrid, not an override.** Codex merges
   configuration layers recursively field by field; a project-layer server
   colliding with a developer's server of the same name yields a
   definition neither party wrote -- niwa's `command` with the developer's
   `args` and `cwd`. niwa therefore detects collisions before writing: it
   reads the developer's own Codex configuration read-only for
   `[mcp_servers.*]` names and reports any collision rather than writing
   the entry. Direct read is chosen over shelling out to
   `codex mcp list --json` because niwa already owns careful access to
   that file for the trust write, the read must degrade gracefully when
   the file is unreadable or malformed (R17 requires that posture
   already), and the subcommand itself fails wholesale on a malformed
   config -- exactly the case where niwa still needs an answer.
3. **`type = "sse"` is silently accepted and served as streamable HTTP.**
   So SSE in the neutral declaration is unmappable for Codex and is
   reported, never silently dropped or silently altered.
4. **No `${VAR}` interpolation, anywhere.** Every value niwa writes is
   fully resolved first; nothing relies on expansion at load time. A
   declaration using interpolation for a Codex-bound value is reported as
   unmappable.

The generator writes the Codex entries into the same trust-gated project
layer as environment delivery, and the capability carries
`Requires: DirectoryTrust` (Decision 2) on measured grounds: the project
layer is not parsed at all in an untrusted directory.

### Decision 6 -- configuration rename and gate restructure (R7, R8)

Both land in PR 2, never PR 1 -- a compatibility alias is
behavior-preserving but not diff-free (new warnings, regenerated example
configuration), and PR 1's job is to be invisible. PR 2 is where a second
agent first gives a Claude-named key something to mis-gate.

**The content rename.** The `[claude.content]` table and the top-level
`content_dir` key gain agent-neutral aliases, `[context]` and
`context_dir`, following the repository's recorded rename precedent
exactly (docs/designs/current/DESIGN-claude-key-consolidation.md, commit
81aae0b, mechanism at `internal/config/config.go:513-528`): both keys
accepted, a deprecation warning on the old name, a hard error when both
are set, removal at the v1.0 line. The mechanism generalizes cleanly here
because content is workspace-scoped -- one embedding site, one
`isContentConfigZero`-style check, structurally identical to the shipped
`[content]` -> `[claude.content]` move. Resurrecting the plain `[content]`
name was rejected: users who dutifully migrated to `[claude.content]`
under the earlier deprecation would be whipsawed back, and a key that was
deprecated in one release and canonical two releases later is a
documentation hazard; a fresh name keeps all three generations
distinguishable and the both-set error unambiguous.

Said plainly: **this partially reverses
DESIGN-claude-key-consolidation.md.** That design consolidated content
configuration under the Claude namespace on the explicit grounds that
content was entirely Claude-coupled -- a premise that was true then and
that dual-agent capability is precisely what falsifies. The reversal is
scoped: only `Content`/`content_dir` move. The other Claude-named fields
(`plugins`, `marketplaces`, `hooks`, `settings`, `env`,
`work_summary_hooks`, `pr_body_hook`) stay Claude-named, because each
binds to a Claude Code file format or mechanism with no Codex analog
today; renaming them would be speculative relabeling of
genuinely-Claude-shaped surface. Codex's environment need, for instance,
is a different config shape (`shell_environment_policy`) delivered from
the agent-neutral declarations, not a rename target.

**The `claude.enabled` gate is restructured, not renamed.** On main the
key is correctly named -- it gates only Claude-owned deliveries. The prior
attempt's defect was that `claude = false` on a repository silently
disabled all three Codex delivery steps: cross-agent mis-gating.
Relabeling the key to something agent-neutral would reproduce the same
mis-gating under a new spelling -- one boolean would still govern two
agents' deliveries. The restructure instead moves the gate to plan
production: `claude.enabled` filters the Claude plan and only the Claude
plan, PR 2 introduces a parallel `codex.enabled` with identical semantics
over the Codex plan, and no gate reaches across agents by construction --
the executor never sees a gate at all, only entries that survived their
own agent's filter. Disabling one agent's delivery for a repository leaves
every other agent's delivery intact (R7), and the acceptance criterion
that a Claude-disabled repository still receives full Codex delivery, and
the reverse, asserts it.

### Decision 7 -- secret safety lands with the first secret (R18)

Two obligations, both landing in the same PR 2 increment that first writes
secret material into a Codex-side file -- not after:

1. **The Codex payload configuration is written at `secretFileMode`
   (0o600, `internal/workspace/materialize.go:28`), never 0o644.** The
   payload carries `shell_environment_policy.set` values -- fully resolved
   per Decision 5, which means resolved secrets sit in that file. The
   prior branch wrote it at 0o644 when it carried only the byte budget;
   the mode flips in the commit that changes what the file can contain.
   Under the plan model this is one `Mode` field on the entry, and a test
   asserts the mode on any entry whose capability delivers resolved
   environment values.
2. **Git-exclude coverage for every niwa-written Codex-side name, at the
   instance root as well as in repositories.** The existing exclude
   machinery covers repository-level names; Codex-side files whose names
   don't end in `.local` (the payload `.codex/` directory,
   `AGENTS.override.md`) need explicit patterns, and the instance root
   needs coverage it doesn't have today for names outside the existing
   conventions. The plan model carries this as `ExcludeAs` on the entry,
   so the exclusion is declared beside the write that makes it necessary
   and the same test that checks the mode checks the pattern.

A third fact is recorded here because a reviewer of this surface needs it,
while being clearly Codex's behavior rather than niwa's doing: codex-cli
0.147.0's `ignore_default_excludes` defaults to `true`, so Codex's own
default `*KEY*`/`*TOKEN*` environment excludes are **not applied** unless
explicitly opted into -- secret-named variables in the parent environment
reach sandboxed commands by default. Measured through the user-invoked
`codex sandbox` entry point; the in-session shell tool is believed but not
proven to resolve the same policy, and R24 carries the in-session
confirmation to the spike when taken. niwa does not change this default --
writing hardening keys into the developer's environment posture uninvited
is out of scope -- but the user guide's safety section states it, and no
security claim in niwa's docs may assume the excludes are active.

## Decision Outcome

A leaf `internal/agentplan` package owns what may be said -- the closed
capability set, the two-state declaration table with reason kinds and
`Requires` edges, and per-agent plan producers that read inputs and
declare outputs over a closed four-op vocabulary. `internal/workspace`
owns doing -- one agent-blind executor plus the existing managed-file,
cleanup, and git-exclude bookkeeping, consuming three fields off plan
entries. Three stdlib-only structural tests hold the boundary: the AST
layout scan (red today at eight named sites, green after PR 1's
conversion), the exhaustiveness/plan-shape table test over
`agent.All() x agentplan.All()`, and the wiring test that makes the agent
parameter the only route from config to bytes. A ManagedFiles-based
characterization test, committed on main before the refactor and
normalized for its two nondeterminism sources, proves PR 1 changed
nothing observable. PR 2 delivers Codex through the contract: MCP and
environment generated from agent-neutral declarations validated before
writing, trust as a delivered capability the closure test enforces, the
`[context]`/`context_dir` alias, the per-agent gate restructure, and
secret file mode plus git-exclude landing with the first secret.

## Solution Architecture

Package layout and dependency direction (arrows point at imports):

```
internal/cli ──────────────► internal/workspace ──► internal/agentplan ──► internal/agent
                                    │                        │                   
                                    ▼                        ▼                   
                             internal/gitexclude      internal/config            
```

`internal/agentplan` sits at the leaf level beside `internal/agent`,
`internal/keyreport`, and `internal/envformat` -- the layer
`internal/workspace` may import without a cycle. Its package doc follows
the house bar: first sentence says what it does, the rest names the
specific callers and the cycle the boundary avoids.

The package holds:

- **The capability enumeration and declaration table.** `Capability`
  constants for the PRD's 24 rows, `All()` returning the closed set, the
  `Declaration` table per agent, and the two recorded exclusions. The
  fail-closed lookup mirrors `vault.Registry.Build`: an unknown
  (capability, agent) pair is an error, never a silent default.
- **The plan types.** `Op`, `Precondition`, `Entry`, `Plan`, plus the
  migrated `SourceEntry` and `ComputeSourceFingerprint`.
- **The plan producers.** `Plan(ag agent.Agent, in Inputs) (*Plan, error)`
  per preparation level (root, instance, group, repo, worktree), where
  `Inputs` is a narrow struct `internal/workspace` fills from
  `EffectiveConfig`. Producers read niwa-owned sources and probe overlay
  paths; they never write.
- **The gap-list generator.** A filter over the declaration table --
  `StateUnavailable` for the agent, grouped by `ReasonKind`, rendering
  each `Reason` -- whose output a test compares against the committed
  guide section (R22). `ReasonNoSuchConcept` rows render as "does not
  apply" notes; the other kinds are the gap list proper.

`internal/workspace` gains `applyPlan(*Plan) (written []string,
excludes []string, err error)`: MkdirAll-and-write for `OpWriteFile`,
append-unless-present for `OpAppendLine`, delimited-section replace for
`OpReplaceSection`, and the symlink-or-bounded-copy discipline for
`OpDeliverTree` (its ~150 lines exist on the prior branch and move here as
the implementation of that one op). Preconditions are executed generically:
`IfSourceExists` stats the entry's `Source`; `IfNotForeign` re-checks the
ownership verdict at write time, preserving the prior branch's deliberate
belt-and-braces against the detect-to-write race, with the check in
generic code. `runPipeline` Step 7 reads `Managed` and `Sources` off
entries instead of a bare path list plus a side map; `cleanRemovedFiles`
gains an `Exempt` consultation in PR 2.

What deliberately does not move: the pipeline's step ordering, the
`Materializer` interface, the config-driven materializers with no
agent-specific logic (dotenv files, arbitrary file distribution, hook
installation -- declared and bound under the contract from PR 1 per R11,
restructured only if and when agent-specific behavior ever lands in them,
which R4 and R5 prevent happening outside the contract), `CheckDrift`, the
state schema, `override.go`'s merge functions, the plugin-installer
function-field seam, and the global-registry heals. Side effects outside
the instance -- the plugin installer, registry reconciliation, the Codex
trust write into the developer's own configuration -- stay outside the
plan: they are not files delivered into the workspace, and the contract
does not pretend otherwise. The trust write is nonetheless a declared
capability (row 23); its delivery function is registered and bound like
any other, it just isn't a plan entry.

Delivery flow after PR 2, per apply:

1. `internal/workspace` assembles `Inputs` per level from
   `EffectiveConfig`, applies each agent's own gate
   (`claude.enabled` / `codex.enabled`) to its own plan production only.
2. For each agent in `agent.All()`, `agentplan.Plan(ag, in)` produces the
   declared entries, warnings, and exemptions. Both agents' plans are
   produced on every apply -- no agent choice at creation or apply time
   (R19).
3. The executor applies entries; written paths feed the managed-file
   record exactly as today; `ExcludeAs` patterns feed the per-repo
   git-exclude call; warnings feed the reporter. Where a declaration is
   implemented for Codex, a delivery failure fails the apply loudly (R20).
4. Registered non-plan deliveries (trust) run behind the same binding
   check: implemented declarations resolve to a registered delivery,
   unavailable ones to none (R4).

## Implementation Approach

**PR 1 -- the contract, no behavior change (R9, R10, R11).**

1. Commit the characterization test on main first, in its own commit:
   fixtures, the two normalizations, the checked-in
   `(Path, ContentHash)` expectation, passing against current behavior.
2. Add `internal/agentplan`: capability set, declaration table with
   Claude's column complete and Codex's column stating main's truth
   (nothing delivered), plan types, producers for the surfaces being
   converted. Export `agent.All()`.
3. Land the three structural tests. The layout scan's filename half is
   red at the eight named sites at this point; the exhaustiveness,
   well-formedness, closure, and binding tests are green against the new
   table.
4. Convert the agent-shaped surfaces to plan production: the eight
   context-writer sites and the settings-document builder
   (`buildSettingsDoc` already returns a document and writes nothing; its
   three call sites each carry their own marshal-mkdir-write copy, and
   conversion deletes two of the three). Delete the dead
   `rootClaudeFile` constant and retire `LocalContextFileName()`'s
   zero-caller state by making the producers consult the agent's
   filenames. The layout scan goes green.
5. Verify: characterization test passes unchanged, full suite passes with
   no existing test modified or deleted, generated example configuration
   byte-identical, no new warnings, `gofmt -l .`, `go vet ./...`,
   `go test -race ./...`. One manual functional-suite run as a sanity
   check.

The honest risk in step 4: the context writers carry accumulated boundary
rules (overlay append, subdir content, `@import` migration removals) that
a mechanical conversion can drop, and two of them are the half-broken
functions where the hardcoded filenames live today. This is exactly where
the characterization test is pointed: any dropped rule changes a produced
path or hash and fails the pinned manifest.

**PR 2 -- Codex through the contract (R12-R21).**

1. Flip the Codex column to its target declarations (11 implemented, 13
   unavailable per the PRD's matrix); the exhaustiveness and closure
   tests enforce the shape as it changes.
2. Port the prior branch's sound composition mechanics as plan producers:
   context layers composed within the spike's measured discovery rules
   (R12), worktree context standing on the measured `.git`-pointer-file
   result (R13), the conflict verdict feeding `Exempt` and `Warnings`,
   `OpDeliverTree` for the payload. Skills delivery gains the niwa-owned
   fetch for github-sourced marketplaces so nothing depends on Claude
   Code's presence (R14).
3. Build the agent-neutral MCP and environment declarations and the
   validated generator (Decision 5), the trust delivery (R17), and
   approval/sandbox posture delivery -- opt-in, absent by default,
   reported when written, approvals and sandbox as separate decisions
   (R21).
4. Land the `[context]`/`context_dir` alias and the per-agent gate
   restructure (Decision 6), the secret mode and git-exclude obligations
   (Decision 7), the generated gap list with its drift test (R22), and
   the inherited 15 functional scenarios with scenario 2 restructured to
   assert dispatch's declared unavailability in the open (R23).
5. Post the measured findings to tsukumogami/niwa#254, or commit them
   into the spike file if it has merged (R24) -- including the two spike
   corrections the PRD names. No new spike document.

## Security Considerations

- **Resolved secrets on disk.** The Codex payload configuration carries
  fully resolved environment values, so it is written at `secretFileMode`
  (0o600) and git-excluded in the same increment that first makes that
  true (Decision 7). Plan entries make both properties assertable in a
  unit test rather than a review habit.
- **Codex inherits secret-named variables by default.**
  `ignore_default_excludes` defaults to `true` in codex-cli 0.147.0, so
  the `*KEY*`/`*TOKEN*` excludes are inactive unless the developer opts
  in -- Codex's own behavior, measured through `codex sandbox` and not yet
  through a live session's shell tool. Recorded in the user guide's
  safety section; never assumed active in any niwa claim.
- **A generated file must never brick a session.** One malformed entry --
  including a malformed key Codex would ignore -- fails Codex's whole
  config load for the directory. Everything niwa writes into a Codex
  layer is validated before writing; failure reports and writes nothing
  partial.
- **No hybrid server definitions.** The recursive field-level layer merge
  means a name collision yields a definition neither party wrote. niwa
  reads the developer's configuration read-only, reports collisions, and
  never writes a colliding entry. The trust write remains additive and
  surgical, retracting only keys niwa itself wrote, and an unreadable or
  malformed developer configuration fails neither create nor apply.
- **Posture is never weakened uninvited.** With no declared posture, niwa
  writes neither `approval_policy` nor `sandbox_mode` and Codex's own
  defaults stand. The measured fact that `sandbox_mode =
  "danger-full-access"` disables filesystem and network sandboxing
  together is why approvals and sandbox stay separate declarations: no
  approval relaxation ever changes the sandbox setting as a side effect.
- **The leaf cannot exfiltrate by construction.** The layout scan's
  no-write/no-exec assertion over `internal/agentplan` means the layer
  that decides what to say has no filesystem-write or process-launch
  surface to misuse; every write funnels through one executor whose
  operations are closed and reviewed once.

## Consequences

Positive:

- The prior attempt's failure mode is structurally unrepeatable: bytes
  reach disk only through a plan, plans only come from a function taking
  the agent, and a faked implementation fails a pure table test instead
  of compiling quietly.
- "What does a Codex session get?" has one answer, in code, that the
  guide is generated from and a drift test guards -- code and doc cannot
  disagree silently.
- The first PR is reviewable mechanically: a reviewer checks that the
  characterization and structural tests exist and pass, rather than
  auditing the diff for behavior change.
- A third agent has a defined job: fill a column of the declaration
  table, supply producers, and let the existing tests confront it with
  the whole capability set.

Negative, accepted:

- An intermediate representation now stands between config and disk. The
  growth risk is real; the mitigation is the closed `Op` enum, where
  adding a member is a named design decision rather than a field slipped
  into a struct.
- `SourceEntry` moves down a package, a mechanical but real churn, and
  the eight context writers are rewritten as producers -- the riskiest
  part of PR 1, pinned by the characterization test.
- A few declaration rows read oddly out of context (directory trust is
  "no such concept" for Claude); the guide's rendering rule absorbs
  this, at the cost of one more rendering distinction.
- Two config names (`[claude.content]`, `content_dir`) enter a
  deprecation cycle for users who already migrated once under the
  earlier consolidation -- the cost of reversing a decision whose premise
  dual-agent capability falsified, paid in PR 2 where the second agent
  makes the old names actively misleading rather than merely inert.
- The `codex.enabled` gate adds a second per-agent switch where one
  boolean used to (incorrectly) suffice.

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
  consolidation decision it partially reverses (Decision 6).
- tsukumogami/niwa#248 (branch retained at `docs/dual-agent-workspace`) --
  the closed prior attempt: sound Codex composition mechanics, the 15
  functional scenarios that set the acceptance bar, and the structural
  failure the tests in Decision 3 exist to make unrepeatable.
- `internal/vault` (`provider.go`, `registry.go`) -- the house precedent
  for a mandatory interface with fail-closed registry lookup, mirrored by
  the declaration table's posture.
- `internal/workspace/apply.go:1695-1718`, `state.go:184-190` -- the
  managed-file record the characterization test pins.
