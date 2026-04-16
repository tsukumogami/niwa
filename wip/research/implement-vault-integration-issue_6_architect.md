# Architecture Review — Issue 6 (materializer hardening)

Target commit: `ddfbf36f261523ebf4a23f8d068bccadf709a8ca`
Branch: `docs/vault-integration`
Reviewer focus: structural fit, invariant correctness.

## Summary

The change is structurally sound. A single `secretFileMode` constant
(0o600) replaces the scattered `0o644` literals in all three
materializer write sites. `injectLocalInfix` is placed in the one path
(`FilesMaterializer`) where the destination basename was
user-controlled; env and settings already had hardcoded `.local`
filenames, so no parallel pattern is introduced. `.gitignore`
handling lives in a small, dedicated helper with clear idempotence
semantics. R26 (no CLAUDE.md interpolation) is structurally
unreachable through this diff — `installContentFile` only interpolates
a fixed map of workspace/group/repo names, and the parser rejects
`vault://` in `[claude.content.*]` sources at parse time
(`internal/config/vault_test.go:370-413`), so no materializer can ever
see a vault URI in a content source.

There is one advisory concern: `EnsureInstanceGitignore` is called
only from `Applier.Create`, not from `Applier.Apply`. Instances
created before this PR will receive the hardened filename and mode on
re-apply but not the `.gitignore` guard. This is a contained asymmetry
between two code paths that otherwise produce the same managed files,
and it does not compound — but it is worth noting because the
hardening story is "defense in depth: filename + mode + gitignore,"
and Apply drops the third leg.

## Findings

### Blocking

None.

### Advisory

**A1. `EnsureInstanceGitignore` is not called from `Applier.Apply`.**
`internal/workspace/apply.go:85` wires the helper into the Create
path. The Apply path (`apply.go:127-183`) does not call it. The
helper is explicitly idempotent (tested at
`gitignore_test.go:87-117`), so calling it from both entry points is
safe and cheap. As shipped, a user who created an instance before
this change and runs `niwa apply` after upgrading will get 0o600 and
`.local` infixes on materialized files, but their instance-root
`.gitignore` stays unaugmented. This is a contained gap, not a
compounding one — fixing it later is a one-line change to `Apply`
and requires no touch-ups elsewhere. I would add the call to Apply
for symmetry, but not block on it.

### Out of scope / noted

- `InstallWorkspaceRootSettings` (`workspace_context.go:68-185`)
  still writes with 0o644. Confirmed safe per the coder's note: this
  writes to the instance root `.claude/settings.json`, which sits
  under the instance directory (a non-git path by design) and is
  never staged to a tracked repo. The hook scripts it copies are
  chmod'd 0o755 (code, not secrets). Leaving this untouched is
  correct — the three materializers scope of this issue is the
  per-repo write path, and this function is a different concern.

- `HooksMaterializer` (`materialize.go:139`) still writes with
  0o644 then chmod 0o755. Hook scripts are executable code, not
  secret material, and the mode transition is necessary for them to
  run. Correct exclusion from `secretFileMode`.

- `content.go:234` writes CLAUDE.md (and friends) with 0o644. These
  are produced via `installContentFile`, which interpolates only a
  fixed map of workspace/group/repo names
  (`content.go:37-40,66-70,102-106`). No secret can reach this path
  given the parser guard at `internal/config/vault_test.go:370-413`.
  R26 holds structurally.

- `.gitignore` helper writes with 0o644 (`gitignore.go:43,63`). Correct —
  the gitignore file itself is not a secret; it is a public marker.
  Matches the mode used by `state.go:101` for similar
  non-secret-bearing metadata.

## Invariant checks

**R24 (0o600 on materialized files)** — confirmed in all three
materializers:
- `SettingsMaterializer` → `materialize.go:423` uses `secretFileMode`
- `EnvMaterializer` → `materialize.go:538` uses `secretFileMode`
- `FilesMaterializer` single file → `materialize.go:680` uses `secretFileMode`
- `FilesMaterializer` dir walk → `materialize.go:745` uses `secretFileMode`

No residual 0o644 in the three materializer write paths. Covered by
`TestCreateNonVaultConfigStillWrites0o600`
(`apply_test.go:1180-1254`).

**R25 (.local infix + instance .gitignore)** — confirmed:
- `EnvMaterializer` target is hardcoded `.local.env`
  (`materialize.go:537`).
- `SettingsMaterializer` target is hardcoded `settings.local.json`
  (`materialize.go:422`).
- `FilesMaterializer` applies `injectLocalInfix` on every
  destination basename, both in single-file explicit dest
  (`materialize.go:668`) and in the dir walk (`materialize.go:720`).
  The directory-destination branch uses `localRename` on the source
  basename (`materialize.go:660`), which is structurally equivalent
  to injecting `.local` before the extension.
- Instance `.gitignore` covers `*.local*` after `Create` runs
  (`apply.go:85`).

**R26 (no CLAUDE.md interpolation of secrets)** — confirmed
structurally:
- The only interpolation call is `expandVars` in
  `installContentFile` (`content.go:227`).
- The `vars` map passed in every call site is a small fixed set of
  workspace/group/repo names (`content.go:37-40, 66-70,
  102-106`) with no `MaybeSecret` values.
- The parser rejects `vault://` in `[claude.content.workspace]`,
  `[claude.content.groups.*]`, and `[claude.content.repos.*]`
  (`vault_test.go:372-412`), so a vault URI cannot reach
  `installContentFile` via `source`.

**`.gitignore` idempotence** — correct:
- No pre-existing file → writes `"*.local*\n"` (`gitignore.go:42-46`).
- Pre-existing file without pattern → appends with leading newline
  when prior content lacks one (`gitignore.go:55-62`).
- Pre-existing file with exact-line match (whitespace-trimmed) → no-op
  (`gitignore.go:49-51, 74-83`).
- The substring test is deliberately strict (`== instanceGitignorePattern`),
  so `*.local.env` or `**/*.local*` won't be treated as equivalent.
  This is the right call: the invariant is "this exact pattern is
  present," and false positives here would silently defeat the
  guard. Covered by `TestEnsureInstanceGitignoreIdempotent` and
  `TestEnsureInstanceGitignoreAlreadyHasPattern`
  (`gitignore_test.go:87-141`).

**Over-application of `.local` to all `[files]` entries** — accepted
as a security-in-depth invariant. The architectural cost is small:
`injectLocalInfix` is a no-op when the basename already contains
`.local`, so users with pre-existing `.local` conventions see no
behavioral change. Users who wrote `dest = ".config/foo.json"` and
expected `foo.json` will instead see `foo.local.json`. This is a
documented behavior change (the comment at `materialize.go:661-668`
explains the invariant) and it gives the system a uniform
"everything niwa writes under a repo is gitignore-covered"
guarantee — a cleaner invariant than "niwa writes vault-sourced
files with .local; users must opt into the pattern for non-vault
files." One pattern, one invariant; acceptable.

## Recommendation

Approve. The Apply-vs-Create asymmetry on `.gitignore` is worth
calling out but is contained — the primary invariants (0o600, `.local`
infix) hold on both paths, and the gitignore leg can be added to
Apply later without cascading changes.
