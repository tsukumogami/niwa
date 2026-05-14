<!-- decision:start id="skill-probe-entry-point" status="assumed" -->
### Decision: niwa CLI entry point for the shirabe migration skill probe

**Context**

The shirabe migration skill (`/shirabe:niwa-migrate-config <workspace-name>`,
PRD-config-source-discovery R16-R20) walks a user through migrating a
workspace from the deprecated rank-2 whole-repo config shape to the
rank-1 `.niwa/` shape. The skill is a Claude Code skill — Markdown plus
tool calls — and cannot link Go directly. R18 requires the skill to
probe the registered source AND the auto-discovered overlay for marker
files, and explicitly forbids re-implementing the probe in a non-Go
runtime. R7's single-fetch contract applies: the skill's probes must
reuse niwa's existing GitHub-tarball / shallow-clone path, not make a
separate Contents API call. R20 requires the operation to be read-only:
no apply, no git push, no snapshot writes.

The skill needs a niwa CLI surface that accepts a source slug
(supporting both the registered source for path (a) and the
user-provided destination slug for path (b) of the migration), runs the
probe, and returns a machine-readable result so the skill can render
findings to the user and decide between in-place vs slug-swap paths.

Five shapes were considered (hidden subcommand, user-facing subcommand,
workspace-name-only entry point, status-command overload, separate
binary). Three were rejected on feasibility grounds:
workspace-name-only fails to serve path (b)'s unregistered-destination
probe; status-command overload mixes registered-workspace-state
reporting with source-slug probing under one verb; a separate binary
adds packaging cost with no benefit. The real contest is between a
hidden internal command and a user-facing diagnostic command.

**Assumptions**

- The skill invokes niwa via the Bash tool and reads stdout as JSON.
  If wrong (e.g., MCP transport instead), the entry-point *shape* is
  unaffected but the stderr/stdout split may need revision.
- The probe payload must be machine-readable (JSON on stdout). R18's
  "report which marker(s) it found" requires structured output for
  the skill to act on.
- niwa's design ethos values `--help`-driven discoverability over
  hidden internal contracts. If the user later signals that
  discoverability is not a priority for this particular surface, the
  decision could revisit toward the hidden shape.
- The probe logic itself (Decision 1's outcome) can be exposed as a
  read-only function over a source slug, independent of the
  materializer's snapshot-promotion side effects. The PRD's R7
  guarantee that the probe uses the same fetch as materialization
  does not require sharing the snapshot-write code path — only the
  fetch-and-scan portion.

**Chosen: User-facing `niwa source inspect <slug> [--json]` subcommand**

A new cobra command group `niwa source` is added at the root, with a
single subcommand `inspect` that takes a source slug positional
argument. The slug uses the existing
`[host/]owner/repo[:subpath][@ref]` grammar (so it can express
explicit subpaths and refs if the user wants to probe a non-default
branch). Without `--json`, output is a human-readable report
("Found `.niwa/workspace.toml` at root, rank 1. Overlay
`acme/vision-overlay` reachable with rank 2 config."). With `--json`,
output is a structured payload on stdout:

```json
{
  "schema_version": 1,
  "source_url": "acme/dot-niwa",
  "host": "github.com",
  "team_config": {
    "found_markers": ["workspace.toml"],
    "resolved_rank": 2,
    "resolved_subpath": "",
    "deprecated": true,
    "error": null
  },
  "overlay": {
    "slug": "acme/dot-niwa-overlay",
    "reachable": true,
    "found_markers": ["workspace-overlay.toml"],
    "resolved_rank": 2,
    "resolved_subpath": "",
    "deprecated": true,
    "error": null
  },
  "suggested_migration": {
    "kind": "rank2-team-and-overlay",
    "summary": "Both team config and overlay are on the deprecated rank-2 path."
  }
}
```

Error states (source unreachable, ambiguous markers, no markers,
decompression-bomb cap exceeded, auth required) populate the
respective `error` field with `{type, message}` and leave the other
fields at their best-effort values. Exit code is 0 when the probe
runs to completion (even if it reports "no marker found" — that's a
probe *result*, not a *command failure*); non-zero only for transport
or invocation errors (binary couldn't start, malformed slug, etc.).

The command does not touch the registry, does not materialize any
snapshot, does not write to `<workspace>/.niwa/`, does not invoke
git push, does not run apply. R20's read-only contract is enforced by
the command's narrow scope (probe-only) and verified by the skill's
acceptance tests (AC-S6).

**Rationale**

1. **Slug-accepting shape is required regardless.** Path (b) of the
   migration (slug swap, R19) needs to probe the user-provided
   destination slug, which is not in the registry. That forces a
   slug-accepting entry point and rejects the workspace-name-only
   variant.

2. **Discoverability matches niwa's existing ethos.** Every other
   niwa command is `--help`-listed and discoverable via cobra
   completion. Introducing a hidden `__`-prefixed command for one
   skill caller would be a new convention with no compensating
   benefit; the JSON shape's stability is served just as well by a
   `schema_version` field as by hiding the command.

3. **The diagnostic value is real and free.** Once the probe logic
   exists (mandatory for discovery — Decision 1), exposing it via
   a thin renderer costs only `--help` text and a human-readable
   output mode. Users debugging their own brain repo layout or CI
   pipelines benefit from being able to run `niwa source inspect`
   on its own.

4. **`niwa source` as a namespace is forward-looking.** Future
   source-related diagnostics (`fetch`, `verify`, `list`) have a
   natural home without growing the top-level command list.

5. **The PRD constraint that the skill is the documented invoker
   does not forbid other invokers.** R18 specifies what the skill
   must do; nothing in the PRD requires niwa to hide the surface
   the skill calls. The user-facing shape satisfies R18 identically
   while preserving the option of human invocation.

The trade-off accepted: one additional command in `niwa --help`, and
a `--json` schema that follows niwa's normal compatibility
expectations (additive changes only within a major version).

**Alternatives Considered**

- **Hidden subcommand `niwa __probe-source <slug>`**: returns JSON
  only; marked internal via `Hidden: true` and `__` prefix. Rejected
  because hiding the command breaks niwa's existing
  `--help`-driven-discoverability pattern with no compensating
  benefit — the JSON contract's stability is served by versioning
  the schema, not by hiding the command. The user-facing variant
  costs only a `--help` line and a human-readable renderer while
  preserving the diagnostic value for the only known niwa user.

- **Workspace-name-only entry point `niwa probe <workspace-name>`**:
  takes a workspace name, looks up the slug in the registry, probes.
  Rejected because path (b) of the migration probes a
  *user-provided destination slug* that is not yet in the registry;
  a workspace-name-only command cannot serve that case. A
  slug-accepting variant is required regardless, making this option
  strictly dominated by Alts 1 and 2.

- **Overload `niwa status <name> --probe-source --json`**: add a
  flag to the existing status command. Rejected because `niwa
  status` reports state of *registered, materialized* workspaces,
  while probe operates on *source slugs*, possibly unregistered.
  The semantic mismatch confuses both `--help` text and the user's
  mental model, and forces the status command to grow a third
  output-mode (alongside instance-state and audit variants).

- **Separate Go binary the skill invokes directly**: ship
  `niwa-probe-source` (or similar) as a second release artefact.
  Rejected because it doubles the release-artifact count, creates a
  synchronization problem if probe behaviour drifts between the two
  binaries, and offers no benefit over a subcommand of the existing
  binary — the skill's invocation cost is identical
  (`bash niwa source inspect ...` vs `bash niwa-probe-source ...`).

**Consequences**

What changes:

- `niwa --help` gains a new entry: `source` (with subcommand
  `inspect`). The skill calls `niwa source inspect <slug> --json`.
- niwa's CLI surface grows by one command group and one subcommand.
  Existing tests are unaffected; new tests cover the probe payload
  shape and the read-only contract.
- The JSON schema (`schema_version: 1` initially) becomes part of
  niwa's CLI contract. Additive changes within the major version
  are fine; removals or renames are breaking and require a new
  schema version.

What becomes easier:

- The skill has one stable call to make, with a well-documented
  contract that can be tested independently of the skill's
  Markdown.
- Users debugging discovery issues (e.g., "why does niwa say my
  source has no markers?") can run `niwa source inspect` directly
  without invoking the migration skill.
- Future related diagnostics (`niwa source verify`, `niwa source
  fetch`) have a natural namespace.

What becomes harder:

- The command must maintain a human-readable rendering mode in
  addition to JSON. The renderer is small but it doubles the
  output-formatting test surface compared to a JSON-only command.
- Backwards-compat expectations apply to the `--json` schema. Any
  field rename or removal needs a `schema_version` bump and the
  skill must handle both versions during the transition.
- A small surface-area risk: a future PRD might want to add side
  effects to `niwa source inspect` (e.g., caching the probe
  result). The read-only contract is now part of the command's
  advertised behaviour and must be preserved or explicitly broken
  with user notice.

Risk surface:

- If the probe logic (Decision 1) ends up tightly coupled to the
  snapshot-write path (e.g., scans only mid-promotion), exposing a
  read-only probe means refactoring the probe into a standalone
  function that returns the marker map without writing. Decision 1
  should plan for this; this decision assumes it is feasible (an
  explicit Assumption above).
<!-- decision:end -->
