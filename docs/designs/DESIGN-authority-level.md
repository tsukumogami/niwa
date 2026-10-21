---
schema: design/v1
status: Planned
problem: |
  `niwa dispatch` has no notion of how much a dispatched session may decide.
  The PRD asks for an ordered authority level set in the workspace config,
  capped by an optional machine setting, narrowed per dispatch, refused when
  a dispatch asks for more, recorded on the dispatch and in telemetry, and
  answerable per decision point through a new `niwa authority decide`
  command, with a seam where an organization's per-point declaration can be
  read later.
decision: |
  Add a leaf package, `internal/authority`, that owns the level type, its
  parsing, narrowest-wins resolution with a source, and the per-point answer
  computed through a `Declarations` interface whose only implementation today
  is empty. `internal/config` gains `[workspace] authority_level` and
  `[global] authority_level`, both validated through the package's parser.
  `runDispatch` resolves the level right after the machine and workspace
  configs load at step (2b), before provisioning, so a refusal leaves
  nothing behind; it prints the source line, adds `niwa.authority.level` to
  the lineage attributes, and writes `authority_level` and
  `authority_source` into the session mapping, which `niwa list --json`
  copies. A new `niwa authority` command group adds `show` and `decide`;
  `decide` reads the caller's mapping by `CLAUDE_CODE_SESSION_ID` or
  `--session`, then an authority record dispatch writes into the instance
  before launch, then the workspace default.
rationale: |
  Every write lands on a seam a sibling feature already uses: the fail-closed
  machine-config stage, the session mapping and its `niwa list` copy, and the
  lineage attribute composer. Keeping resolution in one pure package lets the
  dispatch path, the two new commands and a later declaration reader share
  one definition of "narrower", and lets the table-driven tests cover the
  ordering without cobra or a filesystem. Reading the caller's level from the
  dispatch record, rather than from an environment variable niwa injects,
  keeps one authoritative copy and covers every agent.
upstream: docs/prds/PRD-authority-level.md
decision_provenance: inline-resolved
---

# DESIGN: authority-level

## Status

Planned

## Context and Problem Statement

The PRD (`docs/prds/PRD-authority-level.md`) fixes the behavior: three
ordered levels (`autonomous`, `supervised`, `strict`), a workspace key, a
machine ceiling, a per-dispatch `--authority` flag, narrowest-wins
resolution with a named source, refusal of a wider flag before any instance
exists, read-back through a stderr line, the session mapping, `niwa list
--json`, a telemetry attribute and `niwa authority show`, and a per-point
answer from `niwa authority decide <point>` with exit codes 0 and 3.

What's left is technical. Dispatch already has a stage that loads the
machine config and refuses on a bad value before anything is created
(`internal/cli/dispatch.go`, step (2a)), a workspace-config read at (2b),
provisioning at (6), a lineage composer that builds
`OTEL_RESOURCE_ATTRIBUTES` (`internal/cli/dispatch_lineage.go`), and a
session mapping written at step (11) (`internal/workspace/session_map.go`),
one JSON file per session id under `<workspace>/.niwa/sessions/`. `niwa list
--json` copies selected mapping fields onto each instance record
(`internal/cli/list.go`). The design has to decide where the level's rules
live, how `decide` finds the caller's level, what the declaration seam looks
like, and where dispatch does each new thing.

## Decision Drivers

- A refusal must happen before provisioning, so it leaves no instance, no
  mapping and no launch (PRD R6, R7, R20).
- One definition of "narrower" for dispatch, `show`, `decide` and the
  future declaration reader; no copy that can drift.
- Workspaces that set nothing must launch an identical worker apart from the
  documented additions (R19).
- The declaration seam must let a later reader plug in without changing any
  caller (R17).
- Follow the sibling `dispatch_permission_mode` feature's patterns, so the
  two machine keys read and fail the same way.
- `decide` is local and fast (R21) and works for every agent (R22).

## Considered Options

### Decision 1: Where the resolution rules live

Resolved inline.

**Chosen: a new leaf package `internal/authority`.** It holds the `Level`
type and its order, `Parse`, the `Source` values, `Resolve` (narrowest wins,
ties to the more specific source, wider flag refused), and `Decide` (the
per-point answer). It does no I/O and imports nothing from niwa.

*Alternatives considered:*

- **Helpers inside `internal/cli`.** Rejected: `show`, `decide` and dispatch
  live there, but `internal/config` must validate the same values when it
  loads a workspace, and `config` can't import `cli`. A second parser in
  `config` is the drift the drivers rule out.
- **Put the rules in `internal/config`.** Rejected: config would then own
  policy (what a decider is, how a declaration narrows), and the declaration
  reader, which isn't a config parser, would have to import config to get
  them.

### Decision 2: How `decide` finds the calling session's level

Resolved inline.

**Chosen: the caller's session mapping, then an instance record, then the
default.** `decide` classifies its working directory to find the workspace
root and, when the cwd is inside an instance, that instance. It takes the
session id from `--session` or `CLAUDE_CODE_SESSION_ID` and reads
`<root>/.niwa/sessions/<id>.json` with the existing `ReadSessionMapping`. A
mapping with `authority_level` supplies the level and source. Failing that,
it reads `<instance>/.niwa/authority.json`, which dispatch writes after
provisioning and before launch. The mapping alone isn't enough: it's
written at step (11), after the worker is already running, so a worker that
asks early, or an agent that sets no session id, would otherwise fall back
to the wider workspace level. Only when neither record exists does `decide`
fall back to the workspace and machine resolution, marked `(no dispatch
record)`. A record holding an invalid level is an error, never a fallback.

*Alternatives considered:*

- **Inject `NIWA_AUTHORITY_LEVEL` into the worker's environment.**
  Rejected: it would be a second copy of the record, it reaches only agents
  that take niwa's `--settings` env (not Codex), and a session can unset or
  change its own environment as easily as it can claim another id, so it's
  no stronger.
- **The instance record alone, no mapping fields.** Rejected: `niwa list
  --json` already reads the mapping, the mapping is what later
  never-wider-than-parent work will read by session id, and a session whose
  cwd has left its instance still needs an answer.

### Decision 3: The shape of the declaration seam

Resolved inline.

**Chosen: a `Declarations` interface** with one method,
`Lookup(point string) (Declared, bool)`, where `Declared` holds a decider and
an author-exclusion value. `Decide` takes it as an argument. The package
ships `NoDeclarations`, which never finds anything, and every caller passes
it today. When a declared point is found, `Decide` narrows each component to
the stricter of the declared value and the level's own value, and reports
`source=declaration` only if nothing was narrowed.

*Alternatives considered:*

- **A `[authority.points]` table in the workspace config now.** Rejected:
  per-point strictness belongs to the organization's declaration, and
  declaring it here would create the second home the PRD rules out.
- **No seam; change callers when the declaration exists.** Rejected: R17
  asks that callers not change, and `decide`'s output format is the contract
  later tools read.

### Decision 4: Where dispatch does each new thing

Resolved inline.

**Chosen: resolve at (2b), record at (11), attach telemetry in the lineage
composer.** The workspace config is already loaded at (2b); resolution runs
right after it, using the machine ceiling read at (2a) and the `--authority`
value, and any error returns before (3). A workspace config that failed to
load at (2b) today only prints a notice and leaves provisioning to report
it; dispatch now returns the load error there instead, whatever caused it,
because an unreadable config must not let the level fall back to
`autonomous`. Provisioning would fail on the same config anyway, so only
the point of failure moves. Right after provisioning, beside the pending
marker at (8), dispatch writes the instance's authority record. The resolved level goes
into `lineageInputs`, so `composeDispatchLineage` emits
`niwa.authority.level` beside the other attributes under the same
validation, and into the `SessionMapping` written at (11). The stderr line
prints right after resolution succeeds.

*Alternatives considered:*

- **Resolve after provisioning, next to the permission mode at (9a).**
  Rejected: a refusal there would need the rollback path to undo an
  instance, and R20 wants nothing created.
- **Attach the telemetry attribute separately from the lineage composer.**
  Rejected: the composer already merges the user's attributes and validates
  every value; a second writer of the same variable is how the two copies
  (environment and `--settings`) diverge.

## Decision Outcome

The four choices fit as one shape: a pure package owns every rule, config
and dispatch call into it at the points where their inputs already exist,
and the dispatch record that already outlives the worker's process becomes
the one place a session's level is read from. Nothing new is stored
anywhere except two fields on an existing record and one telemetry
attribute. The declaration seam is an argument, so plugging in a reader
later changes the one line that picks the implementation, not `decide`'s
output or any caller that branches on its exit code.

## Solution Architecture

### `internal/authority`

```go
type Level int // Autonomous < Supervised < Strict (wider to narrower)

func Parse(raw string) (*Level, error) // trimmed; (nil, nil) when blank
func (l Level) String() string

type Source string // "flag", "machine", "workspace", "default", "declaration"

type Inputs struct {
    Workspace *Level // nil when the key is absent or blank
    Machine   *Level
    Flag      *Level
}

type Resolved struct {
    Level  Level
    Source Source
}

// Ceiling is the narrower of Workspace (or Autonomous/default) and Machine;
// it equals Resolve with no Flag, and exists so the refusal can name it.
func Ceiling(in Inputs) Resolved
// Resolve refuses a Flag wider than the ceiling with *WiderError, else
// returns the narrowest of the three; ties go flag, machine, workspace, default.
func Resolve(in Inputs) (Resolved, error)

type Decider int  // Session < Coordinator < Person
type Answer struct {
    Point           string
    Level           Level
    Decider         Decider
    AuthorExclusion bool
    Source          Source
    NoRecord        bool // printed as the " (no dispatch record)" suffix
}
type Declared struct { Decider Decider; AuthorExclusion bool }
type Declarations interface { Lookup(point string) (Declared, bool) }
var NoDeclarations Declarations

func ValidPoint(name string) bool // ^[a-z][a-z0-9-]{0,63}$
func Decide(point string, r Resolved, decls Declarations) Answer
```

`WiderError` carries the requested level and the ceiling, so dispatch can
print the message R6 asks for. `Decide` applies the PRD's R2 table (level to
decider and author exclusion) and R17's narrowing; the five printed keys are
R14's `point`, `level`, `decider`, `author_exclusion` and `source`.

### Config

- `config.WorkspaceMeta` gains `AuthorityLevel string
  \`toml:"authority_level,omitempty"\``. `validate()` calls
  `authority.Parse` and fails the load naming the key, the value and the
  accepted names, returning a typed `*InvalidAuthorityLevelError` callers can
  match with `errors.As`. Because overlays never set `[workspace]` keys, only the
  base config can declare it.
- `config.GlobalSettings` gains `AuthorityLevel string
  \`toml:"authority_level,omitempty"\`` with an accessor,
  `(*GlobalConfig).AuthorityLevel() (*authority.Level, error)`, mirroring
  `DispatchPermissionMode()`.
- `niwa config set authority-level <level>` and `niwa config unset
  authority-level` follow `config_permission_mode.go`: validate through
  `authority.Parse`, write or clear the raw key.

### Dispatch (`internal/cli/dispatch.go`)

1. A new `--authority <level>` string flag.
2. At (2a), beside `DispatchPermissionMode()`, read
   `gc.AuthorityLevel()`; an error refuses the dispatch.
3. At (2b), after the workspace config load: if the load failed, return
   that error. Otherwise parse the flag, build
   `authority.Inputs`, call `authority.Resolve`, and return any error. Print
   `niwa dispatch: authority level <level> from <source>`.
4. Add the resolved level to `lineageInputs`; `composeDispatchLineage` adds
   `niwa.authority.level`. Agents without lineage attributes get nothing.
5. At (8), beside the pending marker, write
   `<instance>/.niwa/authority.json` holding `authority_level` and
   `authority_source` (mode 0600). The rollback armed at (7) removes it with
   the instance.
6. At (11), set `AuthorityLevel` and `AuthoritySource` on the
   `SessionMapping` (new `omitempty` fields `authority_level` and
   `authority_source`). This step runs for every agent.

### Read-back

- `InstanceRecord` gains `AuthorityLevel` and `AuthoritySource` (`omitempty`),
  and `annotateFromSessionMappings` copies them from the newest mapping with
  the same shape check the other copied fields use.
- `niwa authority show` resolves `Ceiling` from the workspace and machine
  configs and prints the three lines R13 names.
- `niwa authority decide <point> [--session <id>]` validates the point
  (exit 2), classifies the cwd (exit 1 outside a workspace), loads both
  configs (exit 1 on an invalid value), reads the mapping when an id is
  known, then the instance record when the cwd is in an instance, calls
  `authority.Decide` with `NoDeclarations`, prints the five `key=value`
  lines, and exits 0 for `session` or 3 otherwise. Exit 3 reuses the
  `ExitCodeError` pattern `Execute()` in `root.go` already maps, with an
  empty message so nothing extra prints; the type stays in `internal/cli`.

### Data flow

```
workspace.toml [workspace].authority_level ─┐
config.toml [global].authority_level ───────┼─> authority.Resolve ─> stderr line
niwa dispatch --authority ──────────────────┘          │
                                                       ├─> lineage: niwa.authority.level
                                                       └─> sessions/<id>.json
                                                                │
       niwa authority decide <point> ── CLAUDE_CODE_SESSION_ID ─┘
                     │      (else <instance>/.niwa/authority.json, written at (8))
                     └─> authority.Decide(point, resolved, NoDeclarations)
```

## Implementation Approach

1. **Resolution package and config keys.** `internal/authority` with table
   tests for `Parse`, `Resolve`, `Ceiling` and `Decide` (including a fake
   `Declarations`), the two config fields with validation, and the `niwa
   config set/unset authority-level` pair, the typed config error, and
   `niwa authority show`, so later steps have a read-back path. No behavior
   change for dispatch.
2. **Dispatch resolution, refusal and record.** The `--authority` flag,
   resolution at (2b), the stderr line, the telemetry attribute, the mapping
   fields and the `niwa list --json` copy, with unit tests using the existing
   dispatch fakes and functional scenarios for refusal and read-back.
3. **The decision-point question and end-to-end proof.** `decide` with its
   exit-code mapping, the functional scenario where a fake agent branches on `decide`,
   the guide `docs/guides/authority-level.md`, and the live run with a real
   Claude Code worker recorded in the pull request.

Steps 2 and 3 both depend on step 1; step 3's end-to-end scenario also needs
step 2's mapping fields.

## Security Considerations

**Trust boundary.** Everything here runs as the operator, and a dispatched
session already holds the operator's rights. The level is advisory to the
session: it can ignore `decide`, edit its own mapping, or pass another
session's id. None of that grants anything the session couldn't already do,
so the feature adds no privilege boundary and claims none. Making the strict
end hold against the session itself is later locking work.

**Widening.** The property this feature enforces is that a single
dispatch's resolution never widens: every input is narrowest-wins, a wider flag is
refused rather than clamped, an invalid workspace value fails the load
instead of falling back to `autonomous`, and an invalid machine value fails
dispatch at (2a) as `dispatch_permission_mode` does, and an unreadable
workspace config refuses the dispatch. A cloned workspace config can't
loosen a machine owner's ceiling, and a machine can't loosen a workspace.
It doesn't hold across dispatches or against the session: a `strict`
session can dispatch a child at the workspace ceiling, or edit the configs
and records it can write. Both are named limits, owned by the
never-wider-than-parent and locking work.

**Unauthenticated answers.** Nothing in the mapping, the instance record or
`decide`'s output is signed. A tool that trusts them about another session
(a coordinator reading a worker's level, an enforcement check) trusts files
the worker could have edited. That's accepted for now, within one
operator's workspace, and the locking work owns closing it. The guide says
so.

**Failing open.** `decide` falls back to the workspace level only when no
record exists, never on a malformed one. The fallback is marked in
`source`, and the guide tells callers to treat any exit other than 0 as "not
yours to settle".

**Input handling.** The point name is checked against
`^[a-z][a-z0-9-]{0,63}$` before use and never reaches a path or a shell.
The session id goes through `ValidSessionID` (a lowercase UUID) inside
`ReadSessionMapping` before it becomes a path, so `--session ../x` can't
read outside the sessions directory. Level values are a closed set of three.

**Data exposure.** The telemetry attribute carries one of three fixed words
and passes the lineage value check; the composer strips inherited `niwa.*`
attributes, so a user's environment can't spoof it. It's best-effort
observability, not an audit record. Nothing secret is printed or recorded;
the mapping fields hold the same three words and a source label.

**Dependencies.** No new modules.

## Consequences

### Positive

- An operator can state, narrow and read back how much dispatched sessions
  decide, and a session gets a one-command answer it can branch on.
- Enforcement work and the organization declaration have one package and
  one output format to build on.
- Workspaces that set nothing see one extra stderr line and nothing else.

### Negative

- After reap only telemetry knows a session's level, and `decide` falls back
  to the workspace's current value.
- A broken `workspace.toml` now stops `niwa dispatch` at (2b) instead of at
  provisioning.
- A nested dispatch isn't capped at its parent's level yet; a `strict`
  session can dispatch an `autonomous` child in an `autonomous` workspace.
- A workspace config with an invalid `authority_level` now stops every
  command that loads it, where unknown keys only warn.

### Mitigations

- The record fields are the input the never-wider-than-parent work needs,
  and the guide states the nested-dispatch gap.
- The load error names the key, the bad value and the accepted names, so the
  fix is one edit.
