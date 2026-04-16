# Architect Review — Issue 7 (SourceFingerprint + state schema v2)

Commit: `b2c56490c2148e34eeddeb731210bdbf0b360fcf` on `docs/vault-integration`.

Scope reviewed:
- `internal/workspace/state.go` + `state_test.go`
- `internal/workspace/materialize.go`
- `internal/workspace/apply.go`
- `internal/workspace/status.go` + `status_test.go`
- `internal/workspace/fingerprint_test.go`
- `internal/workspace/sources_test.go`
- `internal/cli/status.go` + `status_test.go`

## Verdict

Approve. The change fits the existing architecture cleanly, threads provenance
through the materializer contract that already existed (`MaterializeContext`),
keeps the state file as the single serialized contract, and preserves the
offline-by-default status contract. I have zero blocking findings and three
advisory notes.

blocking_count: 0
non_blocking_count: 3

## Critical check walk-through

### 1. R22 compliance (VersionToken never derived from plaintext bytes)

Confirmed. Paths that produce a `SourceEntry.VersionToken`:

- Vault sources — `sourceForMaybeSecret` (materialize.go:650) copies
  `ms.Token.Token` from `vault.VersionToken.Token`. The fake backend derives
  its token from value bytes (test-only, acceptable). The Infisical backend
  (`internal/vault/infisical/subprocess.go:298-329`) explicitly hashes key
  names and byte-lengths, never bytes. The comment at 276-286 calls this
  invariant out.
- Plaintext materializer sources — hash the file/bytes that were just read
  from a non-secret config file on disk. The "secret" interpretation only
  applies if a user puts secrets into a plaintext file they manage (env.files
  or inline `[env.vars]`); in that case the hash is derived from bytes the
  user has already chosen to leave in a plaintext file under their own
  control. A SHA-256 one-way hash of content the user stores unencrypted is
  not a meaningful leak — the plaintext is the original, not the derived
  hash.
- Inline `MaybeSecret` that falls through to the plaintext branch
  (materialize.go:671-677) — hashes the revealed string. The revealed bytes
  only reach that branch when `ms.IsSecret()` is false **or** the vault
  backend didn't produce a Token. For non-secret inline values this is just
  config-file content hashing; for the rare "secret with no Token" case the
  fingerprint is one-way (same consideration as plaintext files).

The per-SourceEntry doc comment on lines 78-83 of state.go documents this
contract explicitly, referencing R15 and DESIGN Decision 4. Good.

### 2. Schema migration (v1 → v2)

Confirmed working. `state.go:60-76` keeps the `ContentHash` field JSON-tagged
as `"hash"` so v1 payloads unmarshal directly. New fields
(`SourceFingerprint`, `Sources`) carry `omitempty`, so pre-Issue-7 binaries
reading a v2 file where no sources were recorded will also not see the new
keys. `TestLoadStateV1MigrationShim` (fingerprint_test.go:127) loads a
hand-written v1 JSON, asserts the new fields are zero, then rewrites and
asserts the schema version bumps. The comment block on LoadState (state.go:
173-181) documents the one-way downgrade break clearly.

Note: `LoadState` itself does not mutate `SchemaVersion`; the test manually
sets `loaded.SchemaVersion = SchemaVersion` before re-saving, and in
production the `Apply` / `Create` paths build a fresh state struct using
`SchemaVersion = SchemaVersion` (apply.go:107, 177). So the version bump
happens implicitly on the next write, not during Load — the test and
production paths agree.

### 3. Rollup correctness — sort before hash

Confirmed. `ComputeSourceFingerprint` (state.go:116-145) builds a local
`pairs` slice, sorts it by `(id, token)`, and only then writes to the hash.
The sort happens before `h.Write(...)`. `TestComputeSourceFingerprintDeterministic`
feeds two differently-ordered slices and asserts equal fingerprints. Kind
and Provenance are intentionally excluded from the rollup (documented +
tested in `TestComputeSourceFingerprintIgnoresProvenanceAndKind`).

### 4. Candidate-path walk in recomputeChangedPlaintextSources

Handles deletion gracefully. `hashFirstExisting` (status.go:191-211) iterates
the candidate roots (`.niwa/` under workspace root, then workspace root
itself, plus absolute-path short-circuit) and returns `("", false)` if no
candidate resolves. The caller (status.go:161-171) records a `ChangedSource`
with `Description: "file missing or unreadable"`, surfacing it as "stale"
with a descriptive note. The test suite doesn't exercise this specific path,
but the code shape is correct.

One small architectural leak: the candidate walk is an offline-mode
heuristic that works around the fact that `ComputeStatus` is not given a
`configDir`. The in-file comment (status.go:137-142) is honest about this
("lets the status code handle both real-world layouts... and test fixtures
... without threading a configDir argument through ComputeStatus"). I'd
rather see `configDir` threaded in cleanly — but this is advisory, not
blocking. See Advisory 1.

### 5. Status OFFLINE contract

Confirmed. `internal/workspace/status.go` imports only `fmt`, `os`,
`path/filepath`, `time`. `internal/cli/status.go` imports `cobra`,
`internal/config`, `internal/workspace` — no `internal/vault` or
`internal/vault/resolve`. The only mentions of "vault" in status.go are
string-literal prefixes in diagnostic output (line 192) and comment blocks
referencing Issue 10's `--check-vault` future-flag. The R23 offline contract
is preserved.

### 6. Materializer ripple — no cross-repo or intra-repo duplicates

The `sourceTuples` map lives on apply.go:210 and is keyed by the absolute
on-disk target path. Target paths are constructed from
`{instanceRoot}/{group}/{repoName}/...`, so two different repos cannot share
a target path. Within a single repo's materialize pass, each of the four
materializers writes to disjoint paths (`.claude/hooks/<event>/...`,
`.claude/settings.local.json`, `.local.env`, user-specified `[files]`
destinations). `recordSources` uses `append` to the per-path slice, which is
the intended behavior within a single call (SettingsMaterializer merges
settings-key sources with env-promote sources into one settings.local.json
entry; that append is intentional, not a dup).

Checked: no path where the same `(SourceID, VersionToken)` pair would be
double-recorded for the same file within a single repo's materialize.

## Advisory notes

**Advisory 1 — configDir thread-through in ComputeStatus.**
`recomputeChangedPlaintextSources` guesses plaintext source locations by
walking `[workspaceRoot/.niwa, workspaceRoot]`. This works today because the
production layout always uses `.niwa/` and tests use the workspace root
directly, but it couples status classification to filesystem conventions
that live elsewhere. When a user passes `--config /elsewhere/niwa.toml` (or
similar) this walk will fail and every plaintext source will show up as
"file missing". Consider threading `configDir` into `ComputeStatus` so the
candidate list is derived from where the config actually lives. Not
blocking because the failure mode is informative ("file missing or
unreadable") and the current call sites don't support non-default configDir
placement. Contained; fix later won't require touching callers.

**Advisory 2 — hook SourceID is relative to a context the user can't see.**
`HooksMaterializer` records `SourceID: scriptPath` (materialize.go:183)
where `scriptPath` is `entry.Scripts[i]` as read from the effective config.
That's the user-written string — which for global-config-sourced hooks may
be an absolute path (handled by the abs check on line 144) but for workspace
hooks is a path relative to `configDir`. For status drift classification
this is exactly right (it matches how `looksLikePath` and the candidate
walk resolve plaintext sources), but for user-facing display ("changed
source: plaintext://<path>") the same SourceID gets surfaced in
`cli/status.go:196`. A hook recorded as `hooks/pre-tool-use.sh` prints
"plaintext://hooks/pre-tool-use.sh" — fine — but a global-config hook
recorded with an absolute path prints the full host-specific path. Minor
UX note; no architectural issue. Consider normalizing display paths in
Issue 10 when `--check-vault` adds similar surface area.

**Advisory 3 — `looksLikePath` heuristic and future SourceID shapes.**
Line 219-226 of status.go classifies a SourceID as a filesystem path when
it doesn't contain a colon. The two current synthesizers of colon-bearing
IDs are `workspace.toml:env.secrets.KEY`-style labels and vault IDs like
`team/API_TOKEN` (no colon, but `Kind == vault` filters those out before the
check). If a future SourceID scheme introduces colon-free synthetic labels
— for example, a future "file bundle" scheme using `@bundle/...` — the
heuristic would false-positive. Keep this in mind when Issue 10 or later
work adds new SourceKind values. Not a current defect.

## Non-issues worth noting (for the record, not the summary)

- `Infisical` backend confirmed to hash keys + lengths, not bytes (R22).
- `secretFileMode = 0o600` applied uniformly across all four materializers
  — a nice side-cleanup; settings and env files were previously 0o644.
- The `sortedKeys` / `sortedKeysSettings` pattern gives deterministic
  Source ordering independent of Go map iteration. Fingerprint would be
  stable anyway because `ComputeSourceFingerprint` sorts, but deterministic
  Sources[] order also makes state.json diffs cleaner across reapplies.
- Schema-version constant centralized at `state.go:28` with a descriptive
  comment linking to the migration shim — future bumps have a single edit
  site.

## Summary

The change threads provenance through the existing materializer contract
rather than inventing a parallel mechanism. State schema v2 migrates
cleanly via omitempty + kept JSON tag. Fingerprint rollup sorts before
hashing and excludes non-determining metadata (Kind, Provenance) from the
hash, as documented. The offline-status contract is preserved — no vault
imports cross into the status path. R22 compliance holds across both the
Infisical backend (keys + lengths) and the plaintext materializer branch
(one-way hashes of content users have already left unencrypted). No
architectural violations.
