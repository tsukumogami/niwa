# Security Review: workspace-config-sources

## Dimension Analysis

### External Artifact Handling
**Applies:** Yes — this is the primary security surface of the design.

The fetch path takes a slug, downloads a gzipped tarball from
`api.github.com` (or `codeload.github.com` after a 302 redirect), and
stream-extracts it into `<workspace>/.niwa.next/` via
`internal/github/extractSubpath`. The extracted bytes are arbitrary
content from a remote source — potentially attacker-controlled when the
source slug points at a hostile repo. The git-clone fallback path
(non-GitHub hosts) shares the same trust model: niwa runs `git clone
--depth=1` against an arbitrary URL and copies the requested subpath
into the snapshot. Both paths must defend against the same class of
issues.

The design's Decision 5 summary (line 413) commits to "validates path
containment to prevent escape via crafted entries" and "writes regular
files only (skips symlinks, directories, devices)". That is the
right shape, but the design doc currently captures it as a one-line
bullet — it should be hardened into an explicit list of behaviors so a
reviewer reading the design knows exactly what `extractSubpath` must
enforce.

Specific risks and the design-level mitigations needed:

1. **Path traversal (zip-slip)**: a tar entry named
   `../../../etc/cron.d/evil` would escape the destination directory if
   joined naively. Mitigation: after applying the subpath filter
   (`<wrapper>/<subpath>/...` literal-prefix), niwa must compute the
   absolute destination path (`filepath.Join(dest, rel)`), then
   `filepath.Clean` it, then verify with `strings.HasPrefix(cleaned,
   dest+string(os.PathSeparator))` (or equivalent) that the cleaned
   path is still under `dest`. Entries that fail this check must be
   rejected with a hard error (not silently skipped — silent skip can
   be exploited to make a partial extraction that the user thinks
   succeeded).

2. **Symlink entries**: `archive/tar` exposes `TypeSymlink` and
   `TypeLink` (hard link). Even "skip on extract" is unsafe if a later
   regular-file entry then writes through the symlink. The design says
   "skip symlinks in v1" — this must mean *both* `TypeSymlink` and
   `TypeLink` are dropped at the per-entry switch, *before* any path
   resolution that could follow a previously-extracted symlink. Given
   the design's "regular files only" stance, the safest implementation
   is a positive allowlist: write only `TypeReg` (and arguably
   `TypeDir` for directory creation), reject everything else.

3. **Special device entries**: `TypeChar`, `TypeBlock`, `TypeFifo`,
   `TypeXGlobalHeader` (the `pax_global_header` R10 explicitly
   excludes), and the various GNU extensions. The positive-allowlist
   approach above covers these; the design should state the allowlist
   shape rather than enumerating skip targets.

4. **Decompression bombs**: a small gzipped tarball can expand to
   gigabytes. The design currently has no per-fetch size cap and no
   per-entry size cap. Mitigation options at the design level:
   - Wrap the gzip reader in an `io.LimitReader` with a generous but
     bounded ceiling (e.g., 500 MB decompressed for v1; brain repos
     are large but not that large for a single subpath snapshot, and
     the user can override via env var if needed).
   - Track cumulative bytes written across all entries during
     extraction and fail when the cap is exceeded.
   - Per-entry `header.Size` is attacker-controlled; do not trust it
     for pre-allocation. Use an `io.CopyN` with the cap, not
     `io.Copy`.

   This is the most important addition the design currently lacks. A
   hostile repo with a 5 KB tarball that expands to 50 GB will exhaust
   the workspace volume; a 50 MB tarball expanding to 500 GB will
   exhaust most laptops. The cap is cheap to enforce and the failure
   mode (fail-closed with a diagnostic naming the limit) is
   user-friendly.

5. **Filename encoding**: tar headers can carry non-UTF-8 bytes,
   embedded NUL, control characters, or names that resolve oddly under
   case-insensitive filesystems (macOS HFS+/APFS default). The path
   containment check above defends against `..`-style traversal, but
   also reject names containing NUL bytes or path separators that
   weren't expected at this position (e.g., backslash on POSIX hosts).
   A simple rule: after the literal-prefix subpath filter strips the
   wrapper, the remaining name must match
   `^[a-zA-Z0-9._/-]+$` (or a slightly broader safe-chars set), with
   no `..` segments, no leading `/`, no NUL.

6. **Empty / truncated tarballs**: the fault-injection seam
   (`testfault.Maybe("fetch-tarball")`) and `truncate-after:N` from the
   `tarballFakeServer` exercise this. The expected behavior is a clean
   error (`unexpected EOF` from `gzip.Reader` or `tar.Reader`) that
   propagates up. Crucially, AC-M4 already requires that a partial
   extraction not corrupt the existing snapshot — this is structurally
   guaranteed by the staging-then-swap pattern (failed extraction
   leaves `.niwa.next/` orphaned; preflight cleanup of the next run
   removes it). Worth calling out in Security Considerations as a
   defense-in-depth property.

7. **Tarball-wrapper escape**: GitHub's tarball API wraps content in a
   `<owner>-<repo>-<sha>/` directory at the root. The design's
   "literal-prefix subpath filter" assumes the wrapper name is
   well-formed. If an attacker controls the repo name or sha (they
   control the repo name; not the sha), and the filter does
   string-prefix matching without separator anchoring, a crafted
   wrapper-like entry could bypass the filter. Mitigation: identify
   the wrapper from the *first* tar entry (a directory entry by
   GitHub's convention), then reject any subsequent entry whose path
   doesn't start with that exact wrapper plus `/`.

8. **Git-clone fallback risks**: the fallback path writes to
   `$TMPDIR/...` and copies the subpath out. The same path-containment
   discipline must apply to the copy-out step (a hostile repo can
   place symlinks inside the subpath that point outside the
   destination). Decision 5 doesn't cover the fallback's
   `extractSubpath` equivalent; the design should specify that the
   fallback's copy step is symlink-aware (uses `os.Lstat` not
   `os.Stat`, skips symlinks the same way the tar path does) and
   path-contained.

### Permission Scope
**Applies:** Yes, but the surface is small.

- **Filesystem permissions**: the design relies on default
  `os.MkdirAll` modes (0755 with umask applied) and default file
  modes from `os.OpenFile`. For the snapshot directory and its
  contents, this is appropriate — the snapshot is meant to be
  readable by the user's tooling. The provenance marker contains
  no secrets (URL, host, owner, repo, subpath, ref, commit oid,
  fetched-at, mechanism), so 0644 is fine.
- **`instance.json` permissions**: contains workspace state including
  the `config_source` block (URL + tuple + commit). No credentials.
  Default 0644 is appropriate. Worth stating explicitly in the design.
- **Two-rename swap interference**: a hostile non-niwa process with
  write access to `<workspace>/` could pre-create
  `<workspace>/.niwa.next/` to break the preflight cleanup, or race
  the swap by inserting its own rename between niwa's two renames.
  This is the standard local-filesystem TOCTOU surface. Realistic
  trust model: anyone with write access to `<workspace>/` already
  controls the workspace; niwa cannot defend against a hostile
  co-resident user. Design should note this assumption explicitly.
  The preflight cleanup's `RemoveAll` of stale `.niwa.next/` and
  `.niwa.prev/` should follow `os.Lstat` semantics so it cannot be
  tricked into deleting through a planted symlink.
- **Network egress**: standard outbound HTTPS to `api.github.com` and
  `codeload.github.com` (after redirect) on TCP/443. No special
  privilege. The git-clone fallback inherits whatever transport git
  is configured for (`https://`, `git://`, `ssh://`, `file://`). The
  `file://` case from R15 is worth a callout: a `file://` source
  bypasses TLS entirely and trusts the local filesystem; this is the
  user's choice but should be documented as a trust transfer.

### Supply Chain or Dependency Trust
**Applies:** Yes, but the trust model is the same as `git clone`.

- **No new third-party dependencies are introduced.** Decision 5
  explicitly stays in stdlib (`net/http`, `archive/tar`,
  `compress/gzip`); the existing BurntSushi/toml dependency is
  reused for the marker. This is a strong supply-chain story.
- **No signature verification, no commit-oid pinning by default**. The
  user's slug names a repo; niwa fetches whatever is there. This is
  the same trust model `git clone` provides — niwa doesn't add
  authenticity beyond TLS — and is appropriate for v1. Users who
  want pinning can specify `@<sha>` in the slug.
- **Transport integrity**: HTTPS to `api.github.com` provides TLS
  authentication of the GitHub endpoint; content authenticity flows
  from "GitHub returned this commit oid for this ref". A hostile
  GitHub backend is out of scope. The `NIWA_GITHUB_API_URL` env var
  lets a hostile env redirect to a fake server; trust model is "user
  owns their env" (acceptable; document as test-only).
- **301 redirect on rename (R18)**: niwa follows once. The redirect is
  cryptographically vouched for by GitHub's TLS, so an attacker
  cannot induce niwa to fetch from a different repo unless they
  already control the target repo on GitHub. Safe by construction.
- **Git-clone fallback authenticity**: same as `git clone` —
  HTTPS/SSH transport authentication, no in-tree signature check.
  The design defers credential handling entirely to git's existing
  resolver (R17), which is the right call.

### Data Exposure
**Applies:** Yes — narrow surface, easy to get right.

- **`GH_TOKEN`**: read once at `APIClient` construction, attached as
  `Authorization: Bearer <token>` to outbound HTTPS requests. The
  token must never appear in:
  - Error messages (the AC-G6 "401 with PAT scope" error must name
    the status code and a generic remediation, not echo the token).
  - Log lines, the request log captured by `tarballFakeServer`
    (test code has access; production never logs request headers).
  - The provenance marker, `instance.json`, or any on-disk artifact.
  - The `RenameRedirect` struct or any other surfaced API.

  Worth a one-line invariant in Security Considerations: "The
  `GH_TOKEN` value is read into memory once and is never written to
  disk, never logged, and never included in error messages."

- **Provenance marker contents**: source URL, host, owner, repo,
  subpath, ref, commit oid, fetched-at, mechanism. Equivalent to
  `git remote -v` plus a timestamp — not sensitive in itself but
  reveals which repo a workspace is configured against. Public-readable
  is fine for a workspace; users with private-org repos should be
  aware that the marker exposes that affiliation in the workspace
  directory. Document the fact, don't restrict the permission.

- **`NIWA_GITHUB_API_URL`**: a hostile env can redirect fetches to an
  attacker-controlled endpoint. Trust model: the env is owned by the
  user; if the env is hostile, niwa is not the weakest link.
  Document that this var is intended for tests primarily; production
  use is supported only for self-hosted GHE-equivalent endpoints
  the user trusts.

- **`NIWA_TEST_FAULT`**: in production builds the only effect is a
  single env-var lookup per `Maybe` call. Not a security risk; worth
  documenting as test-only so production users don't expect it to be
  load-bearing.

### niwa-specific (guardrail interaction, marker tampering, overlay symmetry)
**Applies:** Yes — important interactions worth explicit treatment.

- **Plaintext-secrets public-repo guardrail (R31)**: the new path reads
  the provenance marker's `host`/`owner`/`repo` instead of
  `git remote -v`. The risk to verify: when the marker is missing
  (e.g., a user-authored config dir, or the marker was removed by an
  attacker who wants to bypass the guardrail), R30 says treat as
  "user-authored" and skip the cloned-config recovery path. By the
  same logic, the guardrail should *not fire* on a missing marker
  (because there's no remote to check against). This is correct
  behavior — the guardrail's job is "warn if pushing plaintext to a
  public GitHub repo", and absent provenance means there's no remote
  to warn about.

  However, this means an attacker with workspace-write access who can
  delete `<workspace>/.niwa/.niwa-snapshot.toml` can disable the
  guardrail. This is acceptable in the threat model: workspace-write
  access is full ownership; the guardrail is a safety net, not an
  access control. Document the guardrail's failure mode as
  "fail-open on missing marker, by design" so future contributors
  don't tighten it incorrectly.

- **`niwa reset` marker tampering**: R30 reads the marker to decide
  whether to offer re-fetch. If the marker is tampered to remove the
  URL or replaced with a different URL, `niwa reset` will offer
  re-fetch from the tampered URL. Mitigation: `niwa reset` should
  display the URL it's about to re-fetch from (not just say "from
  the original source") so a user notices a swap. The atomic-swap
  primitive then re-materializes from the tampered URL — but the
  user-visible URL display gives the user a chance to abort. Same
  threat model as above: workspace-write access is full ownership.

- **Workspace overlay clone symmetry (R13, R35)**: same fetch path,
  same security surface. R37's "no files outside subpath" must hold
  for overlays too. Since the overlay path is treated as whole-repo
  (R35 explicitly: "overlay clone is treated as a whole-repo
  source"), the subpath filter degenerates to "the entire wrapper
  contents under the wrapper directory" — which still requires the
  wrapper-anchoring discipline from External Artifact Handling above.
  No new surface; just confirm the same `extractSubpath` is used.

- **Lazy migration of registry / state file**: if a hostile user (or
  a corrupted file) puts a malformed v3 file in place of v2, the
  parser must produce a clean error and leave the file
  byte-identical to its pre-load state (R25 already requires this
  for forward-version state files). Same guarantee should apply to
  malformed registry entries: the lazy mirror-population path must
  not silently rewrite a malformed registry. AC-X1 covers the
  happy path (older binary's registry parses + lazy-upgrades);
  Security Considerations should note that parse failures are
  reported, not papered over.

- **Snapshot integrity (PRD Known Limitations)**: the PRD already
  documents that "tampered but-syntactically-valid snapshots are not
  detected" — niwa treats marker-present-and-parseable as
  integrity confirmation. This is the right v1 stance; future
  enhancements (e.g., commit-oid signature verification, snapshot
  content hash recorded in `instance.json`) can land later. Worth
  acknowledging in Security Considerations as an accepted limitation
  rather than a gap.

## Recommended Outcome

**OPTION 2 — Document considerations:**

Draft of the Security Considerations section to land verbatim in the
design doc:

---

## Security Considerations

The fetch + extract pipeline is this design's primary security
surface. The bytes streamed into `<workspace>/.niwa.next/` are
arbitrary content from a remote git source; a hostile source slug or
a man-in-the-middle attacker (defeating TLS) could deliver
attacker-controlled content. The design's defenses concentrate in
`internal/github/extractSubpath` and the snapshot swap primitive.

### Tarball extraction defenses

`extractSubpath` MUST enforce the following invariants on every entry
streamed from the tarball reader, before writing any bytes to disk:

1. **Positive type allowlist.** Only `tar.TypeReg` (regular file) and
   `tar.TypeDir` (directory) are acted on. All other entry types —
   `TypeSymlink`, `TypeLink` (hard link), `TypeChar`, `TypeBlock`,
   `TypeFifo`, `TypeXGlobalHeader`, `TypeXHeader`, GNU extensions —
   are skipped. This rules out symlink-and-write-through attacks
   structurally.

2. **Wrapper anchoring.** GitHub's tarball API wraps content in a
   single root directory (`<owner>-<repo>-<sha>/`). The first entry
   establishes the wrapper name; every subsequent entry's path must
   begin with `<wrapper>/`. Entries that don't are rejected with a
   hard error.

3. **Subpath filter.** After the wrapper prefix is stripped, the
   remaining path must begin with `<subpath>/` (or equal `<subpath>`
   for a single-file subpath per R4). Entries outside the subpath are
   skipped without writing.

4. **Path-containment check.** The destination path is computed via
   `filepath.Join(dest, relativePath)` then `filepath.Clean`-ed, then
   verified to live under `dest` (string-prefix check anchored to
   `dest + os.PathSeparator`). Entries that escape are rejected with a
   hard error, not silently skipped.

5. **Filename validation.** Entry paths must contain no NUL bytes, no
   `..` path segments, no leading `/`, and no embedded path separators
   beyond the expected `/`. Malformed names are rejected with a hard
   error.

6. **Decompression bomb defense.** The gzip reader is wrapped in an
   `io.LimitReader` with a 500 MB decompressed ceiling (overridable
   for legitimate large-subpath cases via a future env var). Per-entry
   writes use `io.CopyN(dest, src, header.Size)` with a per-entry
   ceiling derived from the same budget; cumulative bytes written are
   tracked across the extraction. Exceeding the cap returns a clean
   error.

7. **Failure leaves no partial state.** Any error during extraction
   leaves `.niwa.next/` orphaned at the staging path; the existing
   `.niwa/` is untouched. The next refresh's preflight cleanup
   removes the orphan.

The git-clone fallback path applies the same discipline to its
copy-out step: regular files only, path containment enforced, no
symlink following.

### Permissions and atomic swap

- The snapshot directory and its contents use default modes (0755 for
  directories, 0644 for files, with the user's umask applied). The
  provenance marker is world-readable: it contains no secrets.
- `instance.json` uses default 0644. The `config_source` block contains
  source URL, tuple, commit oid, and fetched-at — equivalent in
  sensitivity to `git remote -v` output.
- The two-rename swap relies on the workspace owner having exclusive
  write access to `<workspace>/`. niwa does not defend against a
  hostile co-resident user with write access to the workspace parent
  directory; that user already controls the workspace.
- The preflight cleanup of stale `.niwa.next/` and `.niwa.prev/` uses
  `os.Lstat`-aware removal so it cannot be tricked into deleting
  through a planted symlink.

### Credential handling

- `GH_TOKEN` is read once at `APIClient` construction and attached as
  `Authorization: Bearer <token>` on outbound requests. The token
  value is never written to disk, never logged, and never included
  in error messages or surfaced API types.
- Authentication for the git-clone fallback is delegated to git's
  existing credential resolver (SSH agent, `~/.netrc`, credential
  helpers). niwa does not inject or override credentials on the
  fallback path.

### Trust model and supply chain

- niwa fetches whatever the user's source slug names. There is no
  signature verification and no commit-oid pinning by default. Users
  who want pinning specify `@<sha>` in the slug. This matches the
  trust model `git clone` provides.
- Transport integrity comes from HTTPS to `api.github.com` and the
  redirect target on `codeload.github.com`. A hostile GitHub backend
  is out of scope.
- The design introduces no new third-party dependencies. Tarball
  extraction uses Go's stdlib (`archive/tar`, `compress/gzip`,
  `net/http`); the provenance marker reuses the existing
  BurntSushi/toml dependency.

### `.git/`-replacement guardrail interactions

- The plaintext-secrets public-repo guardrail (R31) reads the
  provenance marker's `host`/`owner`/`repo` instead of
  `git remote -v`. When the marker is missing, the guardrail does
  not fire — there is no remote to warn about. An attacker with
  workspace-write access who deletes the marker disables the
  guardrail; this is acceptable in the threat model (workspace-write
  is full ownership) and is by design.
- `niwa reset` (R30) reads the marker to recover the source URL for
  re-fetch. To protect against marker-tampering swap-attacks, the
  reset flow displays the URL it is about to re-fetch from before
  acting; users notice an unexpected URL.
- Lazy state and registry migrations (R23, R24, R28) reject malformed
  files cleanly: a malformed v3 state file leaves the on-disk file
  byte-identical to its pre-load state (R25); a malformed registry
  entry surfaces a parse error rather than being silently rewritten.

### Configurable endpoints

- `NIWA_GITHUB_API_URL` overrides the default `https://api.github.com`
  base URL and is intended primarily for tests against
  `tarballFakeServer`. Production use is supported for self-hosted
  endpoints the user trusts.
- `NIWA_TEST_FAULT` is a test-only seam. In production builds it
  causes a single env-var lookup per `testfault.Maybe` call and has
  no other effect.

### Accepted limitations

- **Snapshot integrity is presence-based**, not content-based. niwa
  treats marker-present-and-parseable as integrity confirmation.
  Tampered but syntactically-valid snapshots are not detected.
  Future enhancements (commit-oid attestation, content-hash
  verification recorded in `instance.json`) can land in follow-up
  releases.
- **`file://` sources bypass TLS** by definition. Users who choose
  `file://` for the git-clone fallback path are trusting the local
  filesystem; this is a deliberate choice, not a niwa weakness.

## Summary

The design's primary security surface is the tarball-fetch +
stream-extract path, and the existing one-line summary in Decision 5
("validates path containment to prevent escape via crafted entries")
under-specifies the extraction defenses that ship-quality code needs.
Drafting the Security Considerations section above does not require
any changes to the architecture itself — every defense fits inside
`extractSubpath`, the existing fault-injection seam, and the
already-decided staging-then-swap pattern. The most important
addition the design currently lacks is a **decompression-bomb cap**
(LimitReader + per-entry budget + cumulative budget), which is cheap
to add at the design level and prevents a hostile repo from
exhausting workspace storage. The credential-handling, marker-tamper,
and supply-chain considerations are all already aligned with niwa's
existing patterns and the PRD's stated trust model.
