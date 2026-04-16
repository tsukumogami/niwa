# Decision 4: SourceFingerprint Reduction and Storage

**Question:** What is the `SourceFingerprint` reduction shape and
state-file storage strategy for materialized files that blend multiple
sources? Store only the rollup SHA-256? Also store the per-source tuple
list? What per-backend version-token shape makes `niwa status` show
actionable provenance on "stale"?

**Scope:** state-file schema for `ManagedFile` and the shape of the
Provider-returned version token that feeds it. Out of scope: the
Provider interface itself (Decision 3) and the stage where vault URIs
are resolved (Decision 1).

## Options Evaluated

### Option A — Rollup hash only

Store `SourceFingerprint string` (32-byte SHA-256 as `sha256:<hex>`).
Apply recomputes from the sorted tuple list, compares, and discards
the tuple list.

- **State size.** Minimal. ~40 bytes per managed file. A 10-repo
  instance with 4 managed files per repo adds ~1.6 KB vs v0.6.
- **Backwards compat.** Additive single string field — trivial.
- **Diagnostic quality.** Lossy. On `stale`, niwa can report "at least
  one source changed" but not which one. For `.local.env` blending
  plaintext workspace-env + discovered repo-env + inline vars + vault
  refs, the user sees one opaque "stale" and must re-run apply to
  discover the cause.
- **R15 compliance.** Meets the literal requirement (SHA-256 of
  sorted tuples) but the **provenance** paragraph of R15 says the
  version-token MUST carry enough metadata for a user to trace a
  rotation back to a specific change event. Rollup-only loses the
  per-tuple metadata — the user cannot see the commit SHA or version
  ID associated with the *changed* source because niwa never stored
  which source changed.

### Option B — Rollup hash + tuple list in `state.json`

Store both. `ManagedFile.SourceFingerprint` holds the rollup;
`ManagedFile.Sources []SourceEntry` holds the sorted tuple list.

- **State size.** Grows linearly with sources per file. For the
  common `.local.env` case with say 15 vault refs plus three
  plaintext files, each `SourceEntry` is roughly 120-400 bytes
  (source-id, version-token, possibly a provenance pointer). A
  10-repo instance with 4 managed files averaging 8 sources each
  adds ~25-60 KB. Still small in absolute terms but non-trivial
  on repeated reads.
- **Backwards compat.** Additive; tuple list is `omitempty`. Older
  niwa reading a newer state ignores the field.
- **Diagnostic quality.** Highest. `niwa status` computes a diff of
  stored-vs-recomputed tuple lists and names exactly which source
  changed, including its provenance pointer (commit SHA, version ID).
- **R15 compliance.** Full — both the rollup and the per-source
  attribution live in state.
- **R22 compliance.** Tuples store only provider metadata
  (version-token, provider-name, vault-key). No secret bytes, no
  derivation of secret bytes. Safe.

### Option C — Rollup + tuple list capped at N, fall back to rollup-only

Store tuples when `len(sources) <= N` (e.g., N=32), rollup-only
above the cap.

- **State size.** Bounded. The escape hatch limits worst-case bloat.
- **Backwards compat.** Additive; older niwa ignores.
- **Diagnostic quality.** Good for the common case, degrades
  silently for mega-files. In practice `.local.env` blending from
  10-20 sources is the typical worst case — N=32 never trips.
- **Complexity cost.** Two code paths for status-output attribution
  (attributed and unattributed), plus documentation of when
  attribution disappears. The cap is a tunable with no natural
  value.
- **Analysis.** The cap tries to solve a problem that doesn't exist.
  Per-file source counts in the wild will be bounded by config
  structure, not pathological. The complexity isn't earned.

### Option D — Rollup in `state.json` + tuple list in sidecar file

Keep `state.json` small. Write tuple lists to
`.niwa/fingerprints/<file-id>.json` (one sidecar per managed file).
Load lazily only when `niwa status` needs attribution.

- **State size.** `state.json` stays minimal. Sidecar files add
  inode-per-file cost.
- **Backwards compat.** Additive; older niwa ignores the sidecar
  directory.
- **Diagnostic quality.** Identical to Option B when the sidecars
  are present.
- **Complexity cost.** Highest. Two-file write per apply (atomicity
  concerns), sidecar-file stale/orphan cleanup on rename/delete,
  a separate test matrix for "state.json present, sidecar missing"
  recovery, backup/restore across two locations. The problem this
  solves (state.json bloat) isn't material at the sizes we're
  looking at.
- **Analysis.** Premature optimization. State sizes under 100 KB
  don't warrant sidecar complexity.

## Chosen

**Option B — rollup hash + tuple list, both in `state.json`.**

## Rationale

R15's provenance paragraph is explicit: a user seeing `stale` must be
able to answer "what change caused this?" without re-running apply.
Rollup-only (Option A) can't deliver that — it only tells you
*something* changed. The state-size cost of storing tuples is small
in absolute terms (tens of KB for a realistic instance), predictable
(linear in source count), and bounded by config structure rather than
unbounded user data. Capping (Option C) and sidecar splits (Option D)
both add two-path complexity to solve a problem the numbers don't
show exists. Storing tuples inline keeps state atomicity as a single
file-write and keeps the `niwa status` code path simple — load state,
diff tuples, print attribution.

## Rejected

- **Option A (rollup only).** Fails the R15 provenance requirement.
  The PRD demands actionable "stale" diagnostics, and rollup-only
  forces users to re-run apply to learn which source changed.
- **Option C (capped tuple list).** Solves a nonexistent scaling
  problem at the cost of a silent attribution degradation and a
  tunable with no natural value. Simplicity argument wins.
- **Option D (sidecar tuple file).** Premature optimization.
  Introduces atomicity and cleanup complexity that state.json-sized
  tuples never justify. Revisit only if telemetry shows multi-MB
  state files in the field.

## Type API Sketch

```go
// state.go

// SchemaVersion bumps to 2 to signal the additive ManagedFile extension.
// Older niwa treats missing Sources/SourceFingerprint as nil/empty
// (every file reports ok on hash match, same as v0.6 behavior).
const SchemaVersion = 2

// ManagedFile tracks a file written by niwa apply.
type ManagedFile struct {
    Path              string        `json:"path"`
    Hash              string        `json:"hash"`               // SHA-256 of materialized bytes
    Generated         time.Time     `json:"generated"`
    SourceFingerprint string        `json:"source_fingerprint,omitempty"` // "sha256:<hex>" rollup
    Sources           []SourceEntry `json:"sources,omitempty"`            // sorted stable
}

// SourceEntry is one input to a materialized file.
type SourceEntry struct {
    // Kind is "plaintext" or "vault". Determines which fields are populated.
    Kind string `json:"kind"`

    // SourceID is the stable identifier folded into the fingerprint.
    // - Plaintext: the config-relative file path (e.g., "env/shared.env").
    // - Vault:     "<provider-name>:<vault-key>" (e.g., "infisical-prod:DATABASE_URL").
    SourceID string `json:"source_id"`

    // VersionToken is provider-opaque. For plaintext, it is "sha256:<hex>"
    // of the source file bytes at resolution time. For vault, it is the
    // token returned by Provider.Resolve (shape per-backend below).
    VersionToken VersionToken `json:"version_token"`
}

// VersionToken is a structured, JSON-serializable provenance payload.
// It is the persisted form of what Provider.Resolve returns.
// All fields are provider metadata; R22 forbids any secret-value
// derivation (see Per-Backend section).
type VersionToken struct {
    // Kind matches the provider's Kind() ("infisical", "sops", "plaintext").
    // "plaintext" is used only for non-vault SourceEntry.
    Kind string `json:"kind"`

    // Token is the canonical version identifier:
    //   - Infisical: the secret-version UUID
    //   - sops:      the git commit SHA of the encrypted file's last change
    //   - plaintext: "sha256:<hex>" of source bytes
    //   - synthesized: "sha256:<hex>" of the encrypted blob
    Token string `json:"token"`

    // Provenance is an optional human-facing pointer added alongside Token
    // for actionable "stale" output. Backend-specific shape — see below.
    // Never included in the fingerprint hash (see Fingerprint computation).
    Provenance *Provenance `json:"provenance,omitempty"`
}

// Provenance holds the actionable "what change caused this?" pointer.
type Provenance struct {
    // GitCommit is the commit SHA that last touched the source.
    // Populated for sops (encrypted-file path) and any future git-hosted
    // backend. May be populated for plaintext sources that live inside
    // a git worktree (best-effort; niwa-hosted config repo is the common case).
    GitCommit string `json:"git_commit,omitempty"`

    // ProviderVersionID is the backend-native version identifier as a
    // user-resolvable string (e.g., Infisical's version UUID).
    ProviderVersionID string `json:"provider_version_id,omitempty"`

    // AuditURL, if non-empty, is a URL the user can open to see the
    // rotation event in the provider's audit log (e.g., Infisical Cloud
    // audit-log entry URL). Empty when the provider can't construct one
    // without additional project metadata.
    AuditURL string `json:"audit_url,omitempty"`
}
```

### Fingerprint computation

The rollup is SHA-256 of a canonical representation that includes
**only** `SourceID` and `VersionToken.Token` — **not** `Provenance`.
Provenance fields like `AuditURL` or `GitCommit` are user-facing and
may legitimately shift without meaning the secret changed (e.g.,
Infisical Cloud domain change). Excluding them keeps the fingerprint
stable across cosmetic metadata changes:

```
tuple_canonical = SourceID + "\x00" + VersionToken.Kind + "\x00" + VersionToken.Token + "\x1e"
rollup = sha256(sort(tuple_canonical_list).join(""))
```

Sorting is lexical on the concatenated `SourceID`. Null and record
separators (`\x00`, `\x1e`) block length-extension ambiguities where
a source-id could masquerade as a token.

## Per-Backend Version Token Shape

| Backend | `Kind` | `Token` | `Provenance` fields populated |
|---------|--------|---------|-------------------------------|
| **Infisical** | `"infisical"` | Secret-version UUID from Infisical API response | `ProviderVersionID` = same UUID (redundant but explicit); `AuditURL` = `https://app.infisical.com/audit/<project-id>?filter=version:<uuid>` when project ID is known; `GitCommit` empty |
| **sops + age** | `"sops"` | Git commit SHA that last touched the encrypted file (`git log -1 --pretty=%H -- <path>`) | `GitCommit` = same SHA; `ProviderVersionID` empty; `AuditURL` empty |
| **sops fallback** (file not in git, or git unavailable) | `"sops"` | `"sha256:<hex>"` of encrypted-blob bytes | All Provenance fields empty; `niwa status --audit-secrets` prints the reason ("file not tracked by git; fingerprint uses blob hash") |
| **Synthesized** (future backends without native versions) | `<provider-kind>` | `"sha256:<hex>"` of the encrypted/resolved-metadata envelope | Best-effort `GitCommit` if the envelope is in a git worktree; otherwise empty |
| **Plaintext** (not a vault backend, but uses the same struct) | `"plaintext"` | `"sha256:<hex>"` of source-file bytes | `GitCommit` = last-touching commit if the file is in the niwa config repo worktree; otherwise empty |

### R22 compliance note

Every `Token` value is either (a) a provider-assigned ID, (b) a
commit SHA from git log, or (c) a SHA-256 of encrypted/ciphertext
bytes. None is a hash or derivative of the *decrypted* secret value.
The sops synthesized case uses the encrypted blob precisely to keep
R22 inviolate while still changing when the ciphertext rotates.

### Git-SHA derivation location

Git-SHA lookup happens inside the provider's `Resolve`, not in
`materialize.go`. Rationale:

- The provider knows which file is the secret-bearing one (the
  `.sops.enc.env` path, the `.sops.yaml` for synthesized
  fallback). Surfacing that to materialize leaks backend-specific
  state.
- Stateless providers can cache `git log` results per-file within
  a single apply run via the `Provider.Close()` lifecycle.

Mechanics: `exec.CommandContext(ctx, "git", "-C", repoRoot, "log",
"-1", "--pretty=%H", "--", relPath)`. On non-zero exit or empty
output, `GitCommit` stays empty and `Token` falls back to
content-hash (see sops fallback row above).

### Infisical audit-log pointer shape

Pair, not a single value. `ProviderVersionID` is the machine-readable
version UUID (the canonical fingerprint input); `AuditURL` is the
human-clickable pointer. niwa prints the UUID on the `stale` line
and, when present, the URL on a continuation line. This shape lets
the same state record serve both scripting (parse the UUID) and
humans (click the URL). When self-hosted Infisical is configured
with a non-default domain, the provider constructs the URL from
the configured base URL.

## niwa status Output Shape

Default (non-verbose) output stays R27-compliant: path + status only
for the summary block, optional provenance lines below **without**
any file content or secret fragment.

```
$ niwa status
Workspace: ~/workspace/tsukumogami-1
Instance:  1 (last applied 2026-04-15 09:22 UTC)

Repos (3):
  OK  koto           (4 managed files)
  OK  niwa           (4 managed files)
  !!  shirabe        (4 managed files, 2 issues)

Managed files:
  ok        koto/.local.env
  ok        koto/.claude/settings.local.json
  drifted   niwa/.local.env
              reason:   local edit (content hash differs, sources unchanged)
              suggest:  niwa apply to regenerate, or edit the source config
  stale     shirabe/.local.env
              reason:   upstream source rotated
              changed:  infisical-prod:DATABASE_URL
                          version: 7f3a...e2d1
                          audit:   https://app.infisical.com/audit/abc123?filter=version:7f3a...e2d1
              changed:  env/shared.env
                          last-commit: 4e5c2a1f (2026-04-14)
              suggest:  niwa apply to pick up the rotation
  stale     shirabe/.claude/settings.local.json
              reason:   upstream source rotated
              changed:  sops:secrets/prod.sops.env
                          last-commit: 9ab7f32d (2026-04-14 "rotate DB creds")
              suggest:  niwa apply to pick up the rotation

Summary: 2 ok, 1 drifted, 2 stale
```

Flags:

- `niwa status --quiet` — summary line only, no per-file listing.
- `niwa status --json` — emits `Sources` inline so scripts can parse
  which source changed.
- `--audit-secrets` (from R31 / Decision 6) extends the listing with
  shadow/overlay flags.

Key properties:

- **No secret values or diffs** (R27). Tokens, git SHAs, and URLs are
  provider metadata only.
- **Actionable.** For sops-backed staleness, the user can
  `git show 9ab7f32d` immediately. For Infisical, they can click
  the audit URL or copy the UUID into `infisical` CLI.
- **Distinguishes drifted vs stale** as the PRD requires: drifted
  means "you changed the file"; stale means "the source rotated".

### "ok" line with no provenance

On the common healthy case, the output says `ok  <path>` with no
continuation. Provenance appears only when a file is `stale` or
when `--verbose` / `--audit-secrets` is set.

## Open Items for Phase 3 Cross-Validation

- **Decision 1 (pipeline ordering).** If resolution happens inside
  `LoadWorkspace` before merge (the D-6 direction), the tuple list
  must be threaded from each provider's `Resolve` call all the way
  through the merge stage to `materialize.go`. Cross-validate that
  the resolution stage returns both the `secret.Value` *and* the
  `VersionToken` as a pair, and that merge preserves both on a
  per-source basis (not flattening to last-write-wins).

- **Decision 3 (Provider interface).** This decision assumes
  `Provider.Resolve` returns a structured `VersionToken` (not a
  plain string). Decision 3 must adopt the same struct. If Decision
  3 chooses a plain string, this decision degrades: the Provenance
  breakout would have to be synthesized inside `materialize.go`
  from the raw string, which leaks backend-specific parsing.
  **Recommendation to Decision 3:** adopt `VersionToken` as the
  return type.

- **Decision 2 (secret.Value).** The tuple list never holds secret
  bytes, but Provider.Resolve returns `secret.Value` alongside the
  token. Cross-validate that the call-site contract prevents
  accidental inclusion of `secret.Value` fields in the tuple
  (e.g., a provider sneaking the hash of the decrypted value into
  `Token`). An R22 lint test should assert that `VersionToken.Token`
  derives only from provider metadata or ciphertext.

- **Decision 6 (shadow detection).** `niwa status --audit-secrets`
  needs to render both source-fingerprint provenance *and* shadow
  flags. Cross-validate that the output schemas compose cleanly —
  both surfaces hang off `ManagedFile` and don't fight for the same
  continuation lines.

- **State migration.** SchemaVersion bump from 1 to 2 requires a
  read-side shim: niwa 0.7+ reading a v1 state must treat missing
  `Sources` as "no fingerprint recorded; treat every file as ok
  on hash match" (pre-R15 behavior). The first apply under v0.7
  populates both fields. No write-side migration needed.
