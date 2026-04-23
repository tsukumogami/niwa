<!-- decision:start id="snapshot-provenance-marker" status="assumed" -->
### Decision: Snapshot provenance marker — format, filename, and location

**Context**

PRD R10 commits to "every regular file present at the resolved subpath
in the source commit, with directory structure preserved" plus "one
provenance marker file" inside `<workspace>/.niwa/`. R11 fixes the
nine fields the marker carries (`source_url`, `host`, `owner`, `repo`,
`subpath`, `ref`, `resolved_commit`, `fetched_at`, `fetch_mechanism`).
The PRD deliberately leaves format, filename, and on-disk location to
the design phase. Five code paths read the marker: `niwa reset`'s
`isClonedConfig` (R30), the plaintext-secrets public-repo guardrail
(R31), drift detection (R16), `niwa status` detail view (R36), and
the snapshot-corruption integrity heuristic (Known Limitation:
"presence and parseability is the integrity signal"). The format
choice ripples through every reader — a poor pick locks niwa into
a brittle parser or a fragile location for the lifetime of every
existing snapshot on disk.

The decision is coupled across three sub-questions: format (TOML vs
JSON vs key=value vs YAML), filename (`.niwa-snapshot.toml`,
`.niwa.lock`, `niwa-snapshot.toml`, etc.), and location (inside
`<workspace>/.niwa/` vs sibling outside vs elsewhere). YAML is
ruled out by niwa's "stay inside Go stdlib where reasonable"
invariant — `gopkg.in/yaml.v3` would be a new dependency, and neither
TOML (already vendored) nor JSON (stdlib) needs one.

**Assumptions**

- The marker is written exactly once per snapshot materialization
  (write-last-then-rename atomic-swap sequence per R12). Mutation in
  place is not a supported operation.
- A future R11 schema addition stays flat-scalar (no nested tables or
  arrays). If a deeply nested field becomes necessary, the marker's
  `cat`-readability claim weakens — but the format itself does not
  need to change.
- Contributors inspecting the marker reach for `cat` first
  (matching R38's literal ordering "`cat`, `jq`, or `toml`"), with
  `jq`/`tomljson` as a secondary path for ad-hoc queries.
- `BurntSushi/toml v1.6.0` continues to be a maintained direct
  dependency. (It already powers `workspace.toml`,
  `~/.config/niwa/config.toml`, `niwa.toml`, and overlay configs;
  removing it would require an unrelated migration of every config
  parser.)

**Chosen: Alt 1 — TOML at `<workspace>/.niwa/.niwa-snapshot.toml`**

The marker is a leading-dot TOML file at
`<workspace>/.niwa/.niwa-snapshot.toml` containing a flat-scalar
record:

```toml
schema_version = 1
source_url = "org/brain:.niwa@main"
host = "github.com"
owner = "org"
repo = "brain"
subpath = ".niwa"
ref = "main"
resolved_commit = "abc123def..."
fetched_at = 2026-04-22T12:00:00Z
fetch_mechanism = "github-tarball"
```

`schema_version` is fixed at `1` for v1; later versions add the
existing `instance.json`-style migration shim if the schema grows.
The marker uses TOML's native datetime literal for `fetched_at`
(always UTC, always `Z`-suffixed). All other fields are strings.

The marker is written into `<workspace>/.niwa.next/` last during
materialization (after every source file has been extracted), then
the whole directory is atomically `rename(2)`'d to `.niwa/`. This
preserves R12 atomic-swap semantics: the marker travels with the
snapshot, and a half-extracted snapshot has no marker (which the
five readers detect as "no source identity").

R10 collision (a brain-repo author writing a file named
`.niwa-snapshot.toml` in the source subpath) is defended at
extraction time: the tarball/clone extractor refuses to write a
source file at the marker path, exiting non-zero with an error
naming the colliding source file and the reserved marker filename.
The leading-dot name is extremely unlikely to be authored by
brain-repo maintainers in practice — the extract-time check is the
safety net for the theoretical case.

A new package `internal/snapshot/marker.go` exposes:
- `const MarkerFilename = ".niwa-snapshot.toml"`
- `const MarkerSchemaVersion = 1`
- `type Marker struct { ... }` with `toml` tags
- `WriteMarker(snapshotDir string, m *Marker) error`
- `ReadMarker(snapshotDir string) (*Marker, error)` — returns
  `os.ErrNotExist` cleanly so consumers can distinguish "no marker"
  from "marker present but unparseable" (the latter triggers the
  R30/R31 fall-through to "treat as user-authored / not GitHub").

**Rationale**

- **R12 atomic-swap unity (correctness)**: marker lives inside the
  swapped directory, so a single `rename(2)` moves marker and
  content together. Alt 3 (sibling location) creates a documented
  non-atomic window the PRD's R12 forbids; this disqualifies it on
  hard-constraint grounds.
- **R38 `cat`-readability (DX)**: TOML's flat-key form reads with
  zero syntax noise, beating JSON's braces and quoted keys for the
  contributor inspecting the marker manually. R38 names `cat`
  first; the format choice should weight that ordering.
- **R11 native datetime support (correctness)**: TOML parses
  `fetched_at` directly into `time.Time` via BurntSushi/toml. JSON
  requires every consumer to `time.Parse(time.RFC3339, ...)` —
  trivial code, but one more drift surface.
- **No new dependency (workspace invariant)**: BurntSushi/toml is
  already vendored. The marker reuses a parser the rest of niwa
  depends on, with no supply-chain expansion.
- **R10 collision mitigation (correctness)**: leading-dot uncommon
  filename + extract-time collision check is sufficient for the
  realistic threat model. The collision case is theoretical;
  Alt 3's structural elimination doesn't pay for the R12 violation
  it creates.
- **Convention alignment**: niwa has two file-format conventions —
  TOML for human-readable configs, JSON for machine-managed runtime
  state. The marker is in the former bucket: it's
  cat-inspected provenance more often than it's runtime-tooled.

**Alternatives Considered**

- **Alt 2 (JSON, `.niwa-snapshot.json`, inside snapshot dir)**:
  defensible on convention grounds — co-locates with `instance.json`
  in the same directory and mirrors its `LoadState` pattern.
  Rejected because TOML's `cat` readability and native datetime
  better serve R38's literal ordering and R11's `fetched_at`
  semantics. The runner-up; if a future field shape (heterogeneous
  arrays, deep nesting) makes TOML painful, swapping to JSON later
  is a one-time migration with the existing `schema_version`
  framework.
- **Alt 3 (TOML, sibling at `<workspace>/.niwa-snapshot.toml`)**:
  eliminates R10 collision structurally. Rejected because the
  required two-step swap sequence creates a window where marker
  and content disagree, violating R12. The collision case it solves
  is mitigated cheaply by Alt 1's leading-dot + extract-time
  check.
- **Alt 4 (plain `key=value`, `.niwa-snapshot`, inside snapshot
  dir)**: maximally `cat`-friendly. Rejected because the bespoke
  parser shifts complexity from a tested library into niwa's own
  code (edge cases for embedded `=`, comments, whitespace, type
  coercion, schema evolution). The "no dependency" win is
  illusory — the dependency moves into untested code.
- **YAML (any filename, any location)**: cluster-rejected at the
  alternatives stage. Requires a new third-party dependency
  (`gopkg.in/yaml.v3`), violating "stay in stdlib where
  reasonable." No benefit over TOML or JSON for a flat-scalar
  9-field record.
- **`.niwa.lock` filename**: cluster-rejected. The `.lock` extension
  in Unix tradition signals "exclusive write lock held by a
  process." The marker is not a lock.
- **`niwa-snapshot.toml` (no leading dot)**: cluster-rejected. The
  file appears in `ls` output and looks like content; weaker R10
  collision defense, weaker "system file" signal to contributors.

**Consequences**

What changes:
- Five consumers (`isClonedConfig`, plaintext-secrets guardrail,
  drift detector, `niwa status` detail view, integrity heuristic)
  call into a shared `internal/snapshot/marker.go` API instead of
  doing ad-hoc filesystem checks.
- The tarball and git-clone extraction paths gain a "reject source
  file at marker path" gate. Cheap (one path comparison per
  extracted entry) and tested via a fixture that authors the
  collision case.
- The materialization sequence formalizes "write marker last, then
  atomic rename." This becomes a documented invariant of the
  snapshot writer.

What becomes easier:
- Reading source identity is one function call, not a subprocess
  shell-out (`git -C <dir> remote -v`) or a directory-existence
  check (`<dir>/.git/`). Faster, more testable, more uniform.
- Schema evolution: `schema_version = 1` plus the existing
  `instance.json` migration-shim pattern means new fields are an
  append-only operation.
- Integrity heuristic: "marker present and parseable" is a one-liner
  the snapshot writer can guarantee atomically. No new code; the
  parseable-ness check IS `ReadMarker(dir)` succeeding.

What becomes harder:
- Marker schema changes require coordinated thinking across five
  consumers. Cheap to manage today (all five share one struct), but
  the design must commit to "marker fields are part of the public
  contract between the snapshot writer and its consumers."
- A future requirement for nested data in the marker (e.g., a list
  of tarball segments verified) would either bend TOML's flat-scalar
  comfort zone or trigger a marker schema migration. The
  "stays flat-scalar" assumption (above) is load-bearing.
- A user manually editing `.niwa-snapshot.toml` to lie about
  provenance is undetectable in v1 (Known Limitation: "tampered
  but-syntactically-valid snapshots are not detected"). This is the
  PRD's accepted trade-off; no mitigation in this decision.
<!-- decision:end -->
