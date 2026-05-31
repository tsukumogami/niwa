# DESIGN: niwa apply emits a workspace-aware role table

## Status

Planned

## Context and Problem Statement

`niwa apply` already computes a workspace's role set. `enumerateRoles`
(`internal/workspace/channels.go:448`) walks the workspace config and group
directories and returns `[]roleEntry{name string, repoPath string}`,
name-sorted, with the coordinator first (its `repoPath` is empty). Apply hands
that set to `InstallChannelInfrastructure` (`channels.go:241`), which creates a
message inbox for each role at `.niwa/roles/<role>/inbox/`. Once the inboxes
exist, the in-memory role set is discarded.

Nothing on disk publishes that set. A tool that wants to address a role by name
from outside niwa — a shirabe-hosted bridge skill turning an `@role` mention
into a dispatch, koto resolving a peer-message destination at the multi-repo
substrate, or an operator confirming a freshly cloned repo became addressable —
has no authoritative file to read. Each one re-derives the topology or keeps a
side table that drifts the moment a repo is added or removed.

The technical problem is narrow: serialize the set niwa already enumerates into
a durable, versioned, machine-readable file at a fixed location, register it so
apply tracks it like every other materialized file, and regenerate it
idempotently. The file becomes a public contract three sibling tools read
against, so its location and shape must be stable, and it must contain no
secrets and no absolute host paths. The work slots into one place in the apply
pipeline — immediately after `InstallChannelInfrastructure`, where the role set
and the accumulating `writtenFiles` list are both in scope.

## Decision Drivers

- **Reuse the existing enumeration unchanged (PRD R3, R11).** The table
  serializes `enumerateRoles` output. No new role model, naming, or validation.
- **Fixed, discoverable location (PRD R2).** Consumers hard-reference the path;
  no globbing or discovery heuristics.
- **Public stability surface (PRD R12).** Location and schema are a contract;
  changes must be additive and version-gated, never silent reshaping.
- **Idempotent regeneration (PRD R8).** An apply that does not change the role
  set produces a byte-identical file; a change is reflected.
- **Managed-file participation (PRD R9).** The file joins apply's managed-file
  tracking and drift detection like other apply-materialized files.
- **No secrets, no absolute host paths (PRD R15, R16).** Destinations are
  expressed relative to the instance root.
- **Negligible cost, no new dependencies (PRD R14).** Serialize in-process data
  with machinery niwa already uses; no network calls or extra repo scans.
- **Convention fit.** Match how niwa already emits on-disk data (JSON via
  `encoding/json`) so the codebase stays uniform.

## Considered Options

### Decision 1: Serialization format

The table is a public surface (R12) read by three external consumers and any
Claude Code session, and must be plain-text, machine- and human-readable (R13)
while adding no new dependency (R14).

Key assumptions: a JSON parser is universally available to consumers (Go,
shell, Claude Code sessions); niwa should not adopt a new serialization library
for one emitted file.

#### Chosen: JSON via `encoding/json` (`MarshalIndent`, 2-space)

Every data file niwa emits to disk today is JSON marshaled with
`json.MarshalIndent(..., "", "  ")` — `.niwa/instance.json` (`state.go:304`),
`.niwa/sessions/sessions.json`, and the `.mcp.json` configs. Reusing JSON adds
zero dependencies, produces deterministic output for a fixed struct plus a
sorted slice (the basis for R8 idempotency), and is parseable by every consumer
without adding a library. It is both machine- and human-readable (R13).

#### Alternatives Considered

- **TOML.** niwa's *config input* is TOML (`niwa.toml`,
  `.niwa-snapshot.toml`), so it is familiar. Rejected: TOML appears in niwa only
  as an authoring/input format, never as emitted machine data; consumers would
  need a TOML parser that JSON does not require, and array-of-tables is weaker
  than JSON for a list of uniform records. Adopting it would fork niwa's emit
  convention.
- **YAML.** Most human-friendly. Rejected: Go's standard library has no YAML
  encoder, so this adds a third-party dependency for one file (against the
  spirit of R14), and YAML's implicit typing adds parsing ambiguity to a
  contract surface that wants an exact, predictable shape.
- **Custom line-oriented format.** Trivial to emit. Rejected: every consumer
  would hand-roll a parser, there is no schema-version affordance, and it
  abandons the structured-data requirement (R13). Not viable for a versioned
  public contract.

### Decision 2: File location and filename

R2 requires a known, stable location under the instance's `.niwa/` directory,
fixed across applies. The existing layout has `.niwa/instance.json`,
`.niwa/sessions/sessions.json`, and the per-role tree `.niwa/roles/<role>/inbox/`.

Key assumptions: a fixed well-known path beats any discovery scheme for a
hard-referenced contract; the table is instance-scoped data, peer to
`instance.json`, not per-role data.

#### Chosen: `.niwa/roles.json` at the instance `.niwa/` root

The table is a single instance-level manifest of the whole role set, so it sits
at the `.niwa/` root beside `.niwa/instance.json` rather than inside any one
role's directory. `roles.json` reads as the manifest companion to the existing
`.niwa/roles/` directory: the file answers "what roles exist and where do their
messages go?"; the directory holds each role's inbox. The file and the
directory coexist without collision (distinct names). The path is a compile-time
constant, mirroring `StateDir`/`StateFile` in `state.go`, so consumers reference
`<instance-root>/.niwa/roles.json` with no discovery step.

#### Alternatives Considered

- **`.niwa/roles/roles.json`** (inside the roles directory). Rejected: nests an
  instance-level manifest inside a directory whose entries are per-role subtrees,
  blurring "the set of roles" with "one role's data"; a consumer listing roles
  by walking `.niwa/roles/*` would have to special-case a `roles.json`
  pseudo-entry. Keeping the manifest one level up avoids that.
- **`.niwa/role-table.json`** (matches the feature name). Rejected: "role table"
  is the design's internal name; `roles.json` is shorter, matches the sibling
  `roles/` directory, and follows niwa's `<noun>.json` data-file naming
  (`instance.json`, `sessions.json`). No functional difference; convention fit
  decides it.
- **Embed the role set inside `instance.json`.** Rejected: `instance.json` is
  niwa's private state schema (versioned independently at SchemaVersion 4); the
  table is a *public* contract with its own version cadence (R6, R12). Coupling
  them would force consumers to parse niwa-internal state and tie the public
  contract's evolution to internal-state migrations. A dedicated file keeps the
  public surface separable.

### Decision 3: Record schema, delivery-destination semantics, version and ordering

R4 requires each entry to record the role's delivery destination (its inbox)
and the repo it is bound to (or a coordinator marker). R5 keeps the coordinator
always present, R6 requires a schema version, R7 requires deterministic stable
ordering, and R16 requires destinations relative to the instance root. The
source is `enumerateRoles` -> `[]roleEntry{name, repoPath}`, name-sorted, with
the coordinator carrying an empty `repoPath`.

Key assumptions: the destination a consumer needs is the inbox directory
`.niwa/roles/<name>/inbox` (the same path apply creates); repo binding is for
identification, not routing, and is null for the coordinator.

#### Chosen: top-level `{schema_version, generated, roles[]}` with relative-path records

```json
{
  "schema_version": 1,
  "generated": "2026-05-31T00:00:00Z",
  "roles": [
    {"name": "coordinator", "coordinator": true, "repo": null, "inbox": ".niwa/roles/coordinator/inbox"},
    {"name": "api", "coordinator": false, "repo": "groups/backend/api", "inbox": ".niwa/roles/api/inbox"}
  ]
}
```

- **`schema_version`** (int, top-level) mirrors niwa's integer `SchemaVersion`
  convention (`state.go:50`), starting at 1; a consumer reads it first and falls
  back or refuses on an unrecognized value (R6, R12).
- **`generated`** (RFC3339 UTC string) records provenance, matching the
  `Generated time.Time` already on `ManagedFile`.
- **`roles`** (array) carries one entry per enumerated role in
  `enumerateRoles`' existing name-sorted order, coordinator first (R7) — no
  re-sorting.
- Per-role fields: **`name`** verbatim from the enumeration (R11);
  **`coordinator`** (bool) true only for the coordinator (R5), so a consumer
  special-cases it without string-matching; **`repo`**, the instance-root-
  relative path of the bound repo or `null` for the coordinator (R4b, R16);
  **`inbox`**, the instance-root-relative inbox path, which *is* the delivery
  destination (R4a).

A consumer resolves a role name by finding its entry and joining `inbox` onto
the instance root it already knows. Because that inbox is the same directory
apply created in the same run, the table and the inboxes cannot disagree.

#### Alternatives Considered

- **Map keyed by role name** (`"roles": {"coordinator": {...}}`). Rejected: JSON
  object key order is not guaranteed across all consumer parsers, undermining
  R7's deterministic-order contract, and it makes the sorted/coordinator-first
  ordering implicit. An array preserves the explicit sort.
- **Destination as the repo path rather than the inbox.** Rejected: the PRD's
  "delivery destination" is the message inbox (R4a); the repo path answers
  identification (R4b). Recording both, distinctly, avoids forcing consumers to
  re-derive the inbox from the repo path.
- **Drop the `coordinator` boolean, infer from `name == "coordinator"` or
  `repo == null`.** Rejected: forces consumers to hard-code the reserved name or
  overload `repo`'s null meaning; an explicit flag is a cleaner contract for one
  bool.
- **Richer per-entry metadata (role kind, created-by, per-entry version).**
  Rejected as scope creep: the PRD fixes contract obligations, not extra
  metadata; R12's additive, version-gated discipline lets later features add
  fields without a v1 commitment. Start minimal.

## Decision Outcome

niwa apply emits `.niwa/roles.json`, a JSON document carrying a top-level
`schema_version`, a `generated` timestamp, and a name-sorted `roles` array whose
entries each record the role name, a coordinator flag, the instance-root-
relative repo binding (or null), and the instance-root-relative inbox path that
serves as the role's delivery destination. The three decisions reinforce one
another: JSON is the format every consumer already parses and niwa already
emits; a dedicated root-level `roles.json` keeps the public contract separable
from niwa's private `instance.json` while reading as the manifest companion to
the `.niwa/roles/` tree; and an explicit array of relative-path records gives a
deterministic, host-path-free contract that maps one-to-one onto the inboxes
apply lays down in the same run. The emitter compares the freshly enumerated
role set against the existing file's contents and rewrites only on a real
change, so unchanged applies leave the file byte-identical (R8) while a changed
topology refreshes both the records and the `generated` timestamp.

## Solution Architecture

### Overview

A new emit step in the apply pipeline serializes the enumerated role set to
`.niwa/roles.json` and registers the file with apply's managed-file tracking.
The step reuses the role set already computed for inbox installation and writes
through the same JSON machinery niwa uses for `instance.json`, so it adds no new
dependency and negligible cost.

### Components

- **Role-table builder** (new, in `internal/workspace`, e.g. `roletable.go`).
  A pure function `buildRoleTable(roles []roleEntry, instanceRoot string)
  roleTable` maps the enumeration into the serializable shape, computing each
  role's instance-root-relative `repo` and `inbox` paths. Pure and
  table-testable; no I/O.
- **Serializable types** (new):
  ```go
  type roleTable struct {
      SchemaVersion int             `json:"schema_version"`
      Generated     time.Time       `json:"generated"`
      Roles         []roleTableEntry `json:"roles"`
  }
  type roleTableEntry struct {
      Name        string  `json:"name"`
      Coordinator bool    `json:"coordinator"`
      Repo        *string `json:"repo"`   // nil -> JSON null (coordinator)
      Inbox       string  `json:"inbox"`
  }
  const roleTableSchemaVersion = 1
  ```
- **Emit step** (new, e.g. `EmitRoleTable(roles []roleEntry, instanceRoot
  string, writtenFiles *[]string) error`). Builds the table, applies the
  idempotency check, writes the file when needed, and appends the path to
  `writtenFiles` so Step 7 hashes it into `ManagedFiles`.
- **Path constant** (new). `RoleTableFile = "roles.json"` beside the existing
  `StateDir`/`StateFile` constants, plus a helper
  `RoleTablePath(instanceRoot) = filepath.Join(instanceRoot, StateDir,
  RoleTableFile)`.

### Key Interfaces

- **Public contract:** `<instance-root>/.niwa/roles.json` with the schema in
  Decision 3. `schema_version` is the compatibility gate; `inbox` is the
  resolution target; `repo`/`coordinator` identify the binding.
- **Internal call site:** `runPipeline` in `internal/workspace/apply.go`, right
  after `InstallChannelInfrastructure` returns (around `apply.go:1276`, before
  Step 5). The enumerated roles are obtained the same way
  `InstallChannelInfrastructure` obtains them (the role enumeration is shared or
  re-invoked there); `EmitRoleTable` receives them plus `instanceRoot` and the
  `&writtenFiles` accumulator already threaded through the pipeline.
- **Managed-file registration:** appending the path to `writtenFiles` is the
  whole integration — Step 7 (`apply.go:1435-1452`) already hashes every
  `writtenFiles` entry into a `ManagedFile` and persists it in
  `InstanceState.ManagedFiles`, so the table participates in drift detection
  (`CheckDrift`, `apply.go:411`) with no table-specific code.

### Data Flow

1. Apply runs the pipeline; Step 4.75 calls `InstallChannelInfrastructure`,
   creating `.niwa/roles/<role>/inbox/` for each enumerated role.
2. The new emit step receives the same `[]roleEntry` and `instanceRoot`.
3. `buildRoleTable` produces a `roleTable` with relative `repo`/`inbox` paths,
   coordinator-first name-sorted order preserved from the enumeration.
4. The idempotency check reads any existing `.niwa/roles.json`, compares its
   `roles` array to the freshly built one; if equal, the step returns without
   writing (the path is still appended to `writtenFiles` so tracking stays
   consistent). If different (or the file is absent), it sets `generated` and
   marshals with `json.MarshalIndent`, writing `0o644`.
5. Step 7 hashes `.niwa/roles.json` into a `ManagedFile` and `SaveState` writes
   it into `.niwa/instance.json`.

### Idempotency detail

The comparison is on the `roles` array only, not on `generated`. A naive
`time.Now()` on every apply would rewrite the file each run and break R8.
Comparing the role records (which are what actually reflect topology) and
preserving the prior `generated` on a no-op keeps the timestamp meaningful while
guaranteeing byte-identical output when nothing changed. When the role set does
change, both the records and `generated` refresh.

## Implementation Approach

### Phase 1: Types and pure builder

Add `internal/workspace/roletable.go` with `roleTable`, `roleTableEntry`, the
`roleTableSchemaVersion` constant, the `RoleTableFile` path constant and
`RoleTablePath` helper, and the pure `buildRoleTable(roles, instanceRoot)`. Add
table-driven unit tests covering coordinator-null `repo`, a topology role, an
explicit-override role, ordering, and relative-path computation.
Deliverables: `roletable.go`, `roletable_test.go`.

### Phase 2: Emit step with idempotency

Add `EmitRoleTable(roles, instanceRoot, &writtenFiles)`: build, read-compare
existing file, write-or-skip, append path to `writtenFiles`. Unit-test the
write path, the no-op-on-unchanged path (byte-identical), the rewrite-on-change
path, and the absent-file path.
Deliverables: emit function in `roletable.go`, tests.

### Phase 3: Pipeline wiring

Call `EmitRoleTable` in `runPipeline` immediately after
`InstallChannelInfrastructure`, passing the enumerated roles, `instanceRoot`,
and `&writtenFiles`. Confirm Step 7 picks up the path into `ManagedFiles`.
Deliverables: edit to `apply.go`; assertion in an apply-level test that
`.niwa/roles.json` is present in `InstanceState.ManagedFiles`.

### Phase 4: Functional coverage

Add a `@critical` Gherkin scenario in `test/functional/features/` driving real
`niwa apply` against `localGitServer` repos: assert `.niwa/roles.json` exists,
lists exactly the enumerated roles (coordinator + one per cloned repo), each
`inbox` matches the created inbox directory, a second apply is byte-identical,
adding a repo adds exactly one entry, and removing one removes exactly that
entry.
Deliverables: feature file plus any step bindings.

## Security Considerations

`.niwa/roles.json` is a non-secret, local-only data file. It carries no
credentials, tokens, or vault material — only role names, a coordinator flag,
and workspace-relative repo/inbox paths, all of which niwa already computes and
partly exposes (for example in the `## Channels` section). The emit step makes
no network access, spawns no process, executes no external input, and adds no
dependency beyond `encoding/json`, which niwa already uses for `instance.json`
and `sessions.json`. niwa never unmarshals its own output, so there is no
deserialization attack surface.

Two invariants must hold in the implementation and be covered by tests:

1. **File mode 0600.** Write the file through the channels installer's
   `writeIdempotent` helper (or `os.WriteFile` followed by an explicit
   `Chmod(0o600)`), matching every sibling managed file. Never rely on a bare
   write that inherits the process umask. This keeps the file consistent with
   `instance.json`/`sessions.json` and avoids needlessly exposing workspace
   topology to other local users on shared hosts.

2. **Relative paths only — enforced, not assumed.** `enumerateRoles` holds
   absolute on-disk repo paths in `roleEntry.repoPath` (topology entries are
   `filepath.Join(groupDir, repoName)`; explicit entries resolve against the
   instance root or are kept verbatim when already absolute). Before
   serialization, convert each to a path relative to the instance root
   (`filepath.Rel`), emit `null` for the coordinator (empty `repoPath`), and
   reject — or null out — any role whose resolved path falls outside the
   instance root. The file is intended to be committed and read by other tools,
   so a leaked absolute path would expose the user's home directory and
   workspace layout in git history and to every downstream consumer. Add a test
   asserting no emitted path is absolute, contains the instance-root prefix, or
   contains a `..` segment.

Role names and path components are constrained by niwa's existing
`^[a-z0-9][a-z0-9-]{0,31}$` validation, which apply hard-fails on violation.
That regex excludes JSON control bytes, quotes, path separators, `..`, and
shell metacharacters, so emitted values cannot smuggle injection payloads into a
consumer. Combined with `encoding/json` escaping on the producer side, the emit
path is injection-safe; consumers (shirabe, koto) should still treat the values
as data and never pass them to a shell or `eval`.

Publishing the role set inherently reveals which repos a workspace coordinates
and how roles map to them. Given the file's stated purpose (cross-tool role
resolution) this disclosure is intended, and the repo names are already visible
in the committed workspace tree and the `## Channels` section; no sensitive data
beyond what is already on disk in the same instance is exposed.

## Consequences

### Positive

- One authoritative, versioned file replaces every consumer's drifting side
  table; the bridge skill, koto, the `niwa role` CLI, and any session resolve
  role names against the same source of truth niwa used to build the mesh.
- The integration is minimal and low-risk: a pure builder plus a single
  pipeline call, reusing the existing JSON, path, and managed-file machinery.
  No new dependency, no new network or scan cost (R14).
- Drift detection and idempotency come almost for free by appending to
  `writtenFiles`; the file behaves like every other apply-materialized artifact.

### Negative

- The table is an apply-time snapshot; filesystem changes between applies are
  not reflected until the next `niwa apply` (inherent to niwa's
  materialize-on-apply model, called out in the PRD's Known Limitations).
- A new public contract is now committed to: `schema_version` must be honored,
  and future shape changes are constrained to additive, version-gated evolution.
- The idempotency comparison adds a read of the existing file on every apply (a
  small, bounded cost on a local file).

### Mitigations

- The snapshot limitation is documented in the schema's intent and matches how
  every other managed file behaves, so consumers already expect apply-time
  semantics.
- `schema_version` is the explicit lever for evolving the contract safely; new
  fields are additive and old consumers ignore unknown keys, while a major bump
  signals an incompatible change a consumer can detect and refuse.
- The extra read is one local `os.ReadFile` of a small file, dwarfed by the
  surrounding apply work; no network or repo scan is involved.
