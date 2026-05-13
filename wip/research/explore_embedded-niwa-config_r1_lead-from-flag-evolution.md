# Lead: What changes does the `--from` flag need to support embedded config?

## Findings

### Current `--from` Accepted Forms

**Slug grammar** (`[host/]owner/repo[:subpath][@ref]`):
- `org/repo` — whole repo (implicit subpath = ""), default branch
- `org/repo@v1.0` — whole repo, pinned ref
- `org/repo:.niwa` — subpath (`.niwa/`), default branch
- `org/repo:.niwa@v1.0` — subpath, pinned ref
- `gitlab.com/group/repo` — non-GitHub host
- `gitlab.com/group/repo:path/to/config@branch` — non-GitHub host + subpath + ref

**URL forms** (via `ParseSourceURL` in `internal/workspace/overlaysync.go`, lines 56–132):
- HTTPS: `https://github.com/org/repo[.git]`, `https://gitlab.com/org/repo.git`
- SSH: `git@github.com:org/repo[.git]`, `git@gitlab.com:org/repo.git`
- file:// (local fakes only): `file:///path/to/repo.git`
- Bare paths (local fakes): `/path/to/repo`

**Slug parser** (`internal/source/parse.go`):
- Strict validation per PRD R3: rejects empty subpath, ref-before-subpath ordering, whitespace, multiple colons/ats, empty owner/repo
- Empty subpath after `:` is explicitly rejected (line 65: `if subpath == ""`)
- Colon and at-sign detection is strict; malformed ordering triggers early rejection (lines 37–41)
- All forms route through `source.Parse()` (line 90 in `overlaysync.go`)

**How it works today**:
- `niwa init --from org/repo` clones the entire repo into `.niwa/` (whole-repo case)
- `niwa init --from org/repo:.niwa` clones and extracts only the `.niwa/` subdir into `.niwa/` (subpath case)
- Discovery (empty subpath) is NOT yet implemented at init time; documentation describes the feature as "future work"
- The probed markers per the docs are `.niwa/workspace.toml`, `workspace.toml`, `niwa.toml` at repo root (docs/guides/workspace-config-sources.md, lines 108–126), but init does not yet probe

**Code citations**:
- Flag definition: `internal/cli/init.go` line 40
- Init source parsing: `internal/cli/init.go` lines 31–36, 234–237
- Slug parser: `internal/source/parse.go` lines 25–106
- URL forms: `internal/workspace/overlaysync.go` lines 56–132
- Materialization: `internal/workspace/snapshotwriter.go` lines 263–283

### Option Space: Candidate Evolutions

#### A. No Changes Required (Status Quo + Discovery Completion)

**Description**: Keep `--from org/repo` as-is. Complete discovery in init by probing for `.niwa/workspace.toml` at the cloned repo root before materializing, auto-resolving subpath when found.

**CLI examples**:
```bash
niwa init myws --from org/dot-niwa          # Clones whole repo (no .niwa/ found)
niwa init myws --from org/brain-repo        # Clones; finds .niwa/workspace.toml, auto-subpaths to .niwa
niwa init myws --from org/brain-repo:.niwa  # Explicit subpath; no discovery
```

**Backwards compatibility**: Perfect. Existing workflows unchanged.

**Pros**:
- Zero flag syntax changes; discovery is a pure implementation detail
- Current parsing already supports the subpath forms for users who want explicit control
- Consolidation optional: teams can migrate on their own schedule (docs recommend `org/brain-repo:.niwa`)
- Help text easy: "probes for `.niwa/workspace.toml` and uses it if found"

**Cons**:
- Discovery probing means an extra clone-then-introspect round-trip at init time (two fetches for the happy path)
- If discovery probe fails (network, invalid repo, markers missing), init must decide: hard error or silent skip? Current convention-discovery sketches suggest hard error when markers conflict, silent skip when none found (docs lines 118–120) — but for init, this is ambiguous UX
- Ambiguity: does `--from org/brain-repo` mean "use discovery" or "I expect you to find `.niwa` if present"? If users update to `--from org/brain-repo:.niwa` for explicit control, the implicit-vs-explicit stance is unclear

#### B. Auto-detection with NO New Syntax

**Description**: Same as A, but emphasize it in docs/help as the preferred default. Do not expose a separate syntax; discovery is always tried unless a `:subpath` is given.

**CLI examples**:
```bash
niwa init myws --from org/brain-repo  # Always probes for .niwa/workspace.toml
```

**Backwards compatibility**: Perfect.

**Pros**:
- Simplest UX: one `--from` form works for both embedded and whole-repo cases
- No parser changes needed; discovery is a runtime behavior in the fetcher/materializer
- Users don't need to learn subpath syntax for the common embedded case

**Cons**:
- Hides the cost: users don't realize they're paying for an extra fetch/probe
- Hard error vs. silent skip ambiguity remains
- Discoverability: `niwa init --help` doesn't obviously signal "I'll look for `.niwa/` if it exists"

#### C. Explicit Subdir Syntax: `--from org/repo:.niwa`

**Description**: Colon already works as a subpath separator in the slug. Document the pattern; no new flag changes needed.

**CLI examples**:
```bash
niwa init myws --from org/dot-niwa         # Whole repo
niwa init myws --from org/brain-repo:.niwa # Subdir (explicit)
niwa init myws --from org/brain-repo:.config # Alternate subdir
```

**Backwards compatibility**: Perfect. Fully additive.

**Pros**:
- Grammar already in the codebase; no new syntax complexity
- Explicit: user states intent; no ambiguity or discovery overhead
- Works for any subdir name (`.niwa`, `.config`, `config/niwa`, etc.)
- Already documented as the migration path (workspace-config-sources.md, line 24)

**Cons**:
- Discovery is manual/opt-in; users must know to use it (requires documentation discipline)
- Longer CLI invocation than `org/repo` alone
- If users don't know subpath syntax exists, they won't use it; consolidated `.niwa/` convention not automatic

#### D. Separate Flag: `--from org/repo --config-path .niwa`

**Description**: Introduce a new `--config-path` flag (or `--subpath`, `--from-subdir`) that specifies where inside the repo the config lives.

**CLI examples**:
```bash
niwa init myws --from org/dot-niwa                     # No subpath
niwa init myws --from org/brain-repo --config-path .niwa  # Explicit subpath
```

**Backwards compatibility**: Perfect. Fully additive.

**Pros**:
- Flag name makes intent explicit (e.g., `--config-path .niwa` is very clear)
- Separates concerns: `--from` is "where is the repo," `--config-path` is "where is the config inside it"
- Discoverability: `niwa init --help` can prominently document this flag

**Cons**:
- Introduces a new flag; CLI surface grows
- More verbose than the existing colon syntax already in the codebase
- Duplication: slug grammar already supports `:subpath`, so a flag is redundant
- Parser complexity: must handle both `--from org/repo:.niwa` AND `--from org/repo --config-path .niwa` (conflict resolution needed)

#### E. Auto-detection + Consolidation Mandate: `.niwa/` Only

**Description**: Hard-code discovery to look for `.niwa/workspace.toml` only (not `workspace.toml` or `niwa.toml` at root). If found, use it. If not, fail with a clear message telling users to migrate or use `--from org/repo` (whole-repo) explicitly.

**CLI examples**:
```bash
niwa init myws --from org/brain-repo  # Probes for .niwa/; hard error if not found
niwa init myws --from org/brain-repo:.niwa  # Same result (explicit, always works)
```

**Backwards compatibility**: Good for fresh setups, breaks existing `org/repo` workflows that expect whole-repo.

**Pros**:
- Consolidation wins: all new workspaces adopt `.niwa/` convention automatically
- Simpler discovery: only one probe, no conflict resolution
- Clearer error messages when `.niwa/` is not found
- Future-proofs the codebase: `.niwa/` becomes the universal convention

**Cons**:
- Breaking change: users who maintain whole-repo sources must update to `--from org/repo@default-branch` (or use a marker file to flip the detection logic)
- UX regression: `--from org/brain-repo` fails when `.niwa/workspace.toml` is missing, even if the user meant "whole repo"
- Migration burden: existing `org/dot-niwa` repos must be converted or abandoned

#### F. Local Path Support: `--from ./relative/path` or `--from /abs/path`

**Description**: Extend `--from` to accept filesystem paths (already supported for development via `file://` URLs). Add shorthand for local paths (no `file://` prefix) so users can do `niwa init myws --from ./config-repo`.

**CLI examples**:
```bash
niwa init myws --from ./local-repo/.niwa  # Relative path + subpath
niwa init myws --from /abs/path/.niwa     # Absolute path
niwa init myws --from file:///abs/path    # Explicit file:// URL
```

**Backwards compatibility**: Good. Paths are already handled; shorthand is additive.

**Pros**:
- Useful for monorepo setups: each team's config in a shared local path
- Development workflow: config authors can test locally before pushing
- No new flag syntax needed; paths just work

**Cons**:
- Complicates error messages: "looks like URL or path" detection adds cognitive load
- Snapshot materialization stores the local path in the provenance marker; applying from a different machine breaks (path doesn't exist)
- Ambiguity: is `org/repo` a slug or a relative path? (Partially mitigated by requiring `:` or `/` in paths, but edge cases remain)

#### G. Separate Init Mode: `niwa init --from-dir ./config` or `niwa init --embedded`

**Description**: Add a distinct flag or mode that explicitly signals "this is an embedded config from a mono/general-purpose repo."

**CLI examples**:
```bash
niwa init myws --from org/dot-niwa         # Whole-repo mode (unchanged)
niwa init myws --from org/brain-repo --embedded  # Embedded mode; probe for .niwa/
niwa init myws --from-dir ./brain-repo     # Whole-arg is a dir path
```

**Backwards compatibility**: Perfect. Fully additive.

**Pros**:
- Explicit mode selector; no ambiguity about intent
- Can attach distinct semantics to each mode (discovery rules, defaults, error handling)
- Help text opportunity: `--embedded` immediately signals the pattern

**Cons**:
- Proliferates flags; `--from` + `--embedded` is two separate concepts
- Unclear grammar: is `--embedded` a boolean or does it take a value?
- Redundant: slug subpath syntax already expresses the same distinction
- UX regression: users typing `--from org/repo` see "nothing happens" and must read help to learn about `--embedded`

---

### Summary of Trade-offs

| Option | Syntax Change | New Flag | Discovery | Consolidation | Parser Impact | Discoverability |
|--------|---------------|----------|-----------|----------------|---------------|-----------------|
| A. Status quo + discovery | None | None | Auto (opt-in via empty subpath) | Opt-in | None | Medium (needs docs) |
| B. Auto-detect always | None | None | Auto (always) | Opt-in | None | Medium (discovery hidden) |
| C. Explicit `:subpath` | None | None | Manual | Opt-in | None | Low (requires user knowledge) |
| D. Separate flag | None | Yes (`--config-path`) | Manual | Opt-in | Higher (conflict resolution) | High (new flag visible) |
| E. Consolidation mandate | None | None | Auto (`.niwa` only) | Forced | None | Medium (breaking change) |
| F. Local path shorthand | Additive | None | N/A | N/A | Higher (path detection) | Medium (adds paths) |
| G. Separate mode | None | Yes (`--embedded`) | Depends | Opt-in | None | High (mode selector) |

---

## Implications

### For Backwards Compatibility

**The cleanest paths forward are A, C, and B** (in order of preference):

1. **Option A** (Status Quo + Discovery Completion) is the recommendation:
   - Zero breaking changes
   - Discovery is implementable as a pure runtime behavior (probe the clone before materialize)
   - Users who want explicit control use `:subpath`; users who want implicit discovery omit it
   - The cost (extra fetch on discovery probe) is paid only by new users; existing registries are unaffected

2. **Option C** (Explicit `:subpath`) is the fallback:
   - If discovery becomes contentious (hard error vs. silent skip), users can opt into `:subpath` explicitly
   - Already documented as the migration path
   - No new parser logic needed

3. **Option B** (Auto-detect Always) is acceptable but less transparent:
   - Same result as A, but discovery is mandatory (no explicit opt-out)
   - Hides the fetch cost; UX impact unclear

### For Embedded Config Support

**The current slug grammar already supports embedded config**:
- `--from org/brain-repo:.niwa` is valid and working
- Discovery (`--from org/brain-repo` probing for `.niwa/workspace.toml`) is documented but not yet implemented
- No flag changes are needed; implementation is in the `github.ExtractSubpath()` fetcher and the snapshot materializer

**To move embedded config from "documented" to "primary pattern"**:
1. Complete discovery in init (PRD R2 / R3)
2. Document `.niwa/` as the recommended convention for new workspaces
3. Provide migration guidance for teams consolidating from `org/dot-niwa` to `org/brain-repo:.niwa` (already in docs)

### For Parser Complexity

**No new grammar is needed**. The existing `:subpath` and `@ref` separators are:
- Unambiguous (colon-before-at ordering is enforced)
- Mutually compatible (multipart subpaths like `teams/research` are valid)
- Already tested (source_test.go has 50+ test cases)
- Composable with all URL forms (GitHub, GitLab, SSH, file://)

**Discovery logic is separate from parsing**:
- Parsing lives in `source.Parse()` and returns a typed `Source` struct
- Discovery lives in the snapshot materializer (probes the cloned source before extraction)
- No parser entanglement; the design is clean

---

## Surprises

1. **Discovery is not yet implemented at init time**, despite being documented. The design docs (DESIGN-workspace-config-sources.md) describe the feature, but the init command does not probe. This is marked as "Implementation status (April 2026): ... remaining scope of PR #73 and lands in follow-up commits." (docs/guides/workspace-config-sources.md line 14–16)

2. **The slug grammar already supports embedded config** via `:subpath`. This is not a new feature request; it's an existing feature that's underdocumented at the UX level. Users who discover the colon syntax can use `--from org/brain-repo:.niwa` today.

3. **Whole-repo materialization is the degenerate case** of subpath extraction. When subpath is empty, `github.ExtractSubpath()` extracts the repo root. This is implemented consistently: the "whole-repo" and "subpath" modes are the same code path, not separate branches.

4. **The provenance marker stores the subpath**. Each snapshot records `source_url`, `subpath`, `ref`, and `resolved_commit` in `.niwa-snapshot.toml`. This means discovery results are persisted: a workspace initialized via `--from org/brain-repo` (which probes and finds `.niwa/`) will re-apply against `.niwa/` on future applies, without re-probing.

5. **Convention overlay derivation is already in place** (source.go lines 127–141, `OverlayDerivedSource()`). When a workspace uses `org/brain-repo:.niwa`, the auto-discovered overlay slug is `org/.niwa-overlay` (not `org/brain-repo-overlay`). This is baked into the Source type and used by init's overlay-discovery path (init.go line 565, `config.DeriveOverlayURL()`).

---

## Open Questions

1. **Should discovery be mandatory or optional?**
   - If mandatory (Option B): `niwa init --from org/brain-repo` always probes, and `--from org/brain-repo@ref` (with explicit ref) disables probing?
   - If optional (Option A): `niwa init --from org/brain-repo` probes; `niwa init --from org/brain-repo:.` (empty subpath after colon) is rejected by the parser and signals "use discovery"?
   - Current parser already rejects empty subpath (parse.go line 65), so Option A's escape hatch doesn't work. This is a grammar design decision.

2. **What happens when discovery probes and finds nothing?**
   - Hard error: "no `.niwa/workspace.toml`, `workspace.toml`, or `niwa.toml` found at repo root"?
   - Silent skip: treat as whole-repo (empty subpath)?
   - Current docs suggest hard error + guidance to use `--from org/repo:.path` explicitly (workspace-config-sources.md line 223).

3. **Should the provenance marker include a "discovery_applied" flag or the "probed_markers" list?**
   - Currently, once materialized, the marker stores the resolved `subpath`. On subsequent applies, drift detection uses the stored values; no re-probing happens.
   - If discovery rules change (e.g., a team adds `.niwa/workspace.toml` to a previously-whole-repo source), existing workspaces won't auto-migrate. Is this acceptable?

4. **Does consolidation to `.niwa/` convention require a breaking change, or can it be gradual?**
   - If opt-in (Option A/C): old `org/dot-niwa` sources continue to work; new workspaces can choose `.niwa/` via `--from org/brain-repo:.niwa` or discovery.
   - If mandatory (Option E): `--from org/brain-repo` fails unless `.niwa/` exists. This breaks existing `org/dot-niwa` users.
   - Recommendation: stay opt-in for at least one release cycle.

5. **Should `--from` accept bare filesystem paths (Option F)?**
   - Today, local-only workspaces are supported via `file://` URLs in `--from`, but the CLI doesn't advertise it.
   - Making `./local-repo` a shorthand would enable monorepo setups but adds ambiguity to slug-vs-path detection.
   - This is a lower-priority feature; post-v1 scope is reasonable.

---

## Recommendations

### Primary Path: Option A (Status Quo + Discovery Completion)

**Implement discovery in init**, completing the gap between design intent and implementation:

1. **Parser**: No changes. Existing `:subpath` and `@ref` syntax is sufficient.

2. **Fetcher/Materializer**: Add discovery probe in the snapshot writer. When `src.Subpath` is empty (i.e., user did not specify `:path`), probe the cloned repo root for markers in order:
   - `.niwa/workspace.toml` → resolve subpath to `.niwa`
   - `workspace.toml` → resolve subpath to `` (whole repo)
   - `niwa.toml` → resolve subpath to `` (whole repo), but validate `[workspace] content_dir` is set
   - Nothing found → hard error with guidance to use `--from org/repo:path` explicitly

3. **Registry/State**: Store the discovered subpath in the provenance marker so future applies re-use the same subpath without re-probing.

4. **Help Text**: Update `niwa init --help` to document discovery:
   ```
   --from <slug>  Source repo for workspace config. Slug forms:
                    org/repo             → whole repo (auto-probes for .niwa/
                                          workspace.toml; hard error if not found)
                    org/repo:.niwa       → explicit subdir (no discovery)
                    org/repo@v1.0        → pinned ref (discovery applied after pin)
                    gitlab.com/g/repo    → non-GitHub host
   
   Discovery checks for .niwa/workspace.toml, then workspace.toml, then
   niwa.toml at the repo root and resolves the subpath automatically.
   Use explicit :subpath to bypass discovery.
   ```

5. **Examples in docs**: Add a table showing the discovery outcomes for different source structures.

**Backwards compatibility**: Perfect. Existing workflows unchanged. New users benefit from auto-discovery.

**Cost**: Implementation of discovery probe (probes before extraction); users pay one extra fetch at init time (justified by the benefit).

### Secondary Path: Option C (Explicit `:subpath` with Docs)

If discovery becomes contentious or introduces complexity, fall back to **explicit subpath syntax**:

1. No code changes needed; `:subpath` is already fully functional.

2. **Marketing**: Update docs to emphasize the pattern as the recommended way to use embedded config:
   ```bash
   # Recommended for new workspaces with embedded config:
   niwa init myws --from org/brain-repo:.niwa
   
   # Whole-repo (legacy dot-niwa pattern):
   niwa init myws --from org/dot-niwa
   ```

3. **Help text**: Same as above, but omit discovery language. Keep it simple: "use `:subpath` to specify where the config lives inside the repo."

**Backwards compatibility**: Perfect. Purely additive.

**Cost**: Minimal (docs + messaging). Users must know to use the colon syntax.

### Not Recommended

- **Option D** (Separate Flag): Redundant with existing `:subpath` syntax; adds CLI surface.
- **Option E** (Consolidation Mandate): Breaking change; forces migration.
- **Option G** (Separate Mode): Over-engineered; the slug grammar is expressive enough.
- **Option F** (Local Paths): Interesting but lower-priority; post-v1 scope.

---

## Summary

**The `--from` flag does NOT need to change to support embedded config.** The existing slug grammar (`[host/]owner/repo[:subpath][@ref]`) already supports arbitrary subdirectories: `--from org/brain-repo:.niwa` is valid and works. The main gap is **discovery** — probing the source repo for marker files (`.niwa/workspace.toml`) to auto-resolve subpath when the user omits it.

**Recommended next step**: Complete discovery in the snapshot materializer (probe before extraction), store the resolved subpath in the provenance marker, and document the pattern in help text and migration guides. This requires zero parser changes and yields the best UX for consolidation workflows (users type `niwa init myws --from org/brain-repo` and get embedded config auto-magically if `.niwa/` exists).

**Alternative if discovery is contentious**: Stick to explicit syntax (`--from org/brain-repo:.niwa`) and rely on documentation to make the pattern discoverable. Lowest friction, but requires user knowledge.

