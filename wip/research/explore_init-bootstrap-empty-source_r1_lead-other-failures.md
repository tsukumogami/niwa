# Lead: What other failure modes should init handle, and how?

## Findings

### Per-case map

The materialize path for `niwa init --from <slug>` is:

1. `internal/cli/init.go:264` calls `workspace.MaterializeFromSource(ctx, src, source, niwaDir, config.TeamConfigMarkerSet(), fetcher, reporter)`.
2. That dispatches to `materializeAndSwap` at `internal/workspace/snapshotwriter.go:345`.
3. GitHub sources go through `materializeFromGitHub` at `snapshotwriter.go:482`, which calls `fetcher.FetchTarball` (`internal/github/fetch.go:92`) then `github.ProbeAndExtractSubpath` (`internal/github/tar.go:248`).
4. Non-GitHub sources go through `materializeFromFallback` at `snapshotwriter.go:562`, which shallow-clones via `shallowCloneToTemp` (`internal/workspace/fallback.go:136`) then runs `ProbeAndFetchSubpath`.
5. The post-flight at `init.go:287-291` parses the freshly written `.niwa/workspace.toml` via `config.Load`.

Mapping each failure mode to where it surfaces:

**A. `.niwa/` exists, `workspace.toml` is malformed (parse fails).**
- The probe at `ProbeMarkers` (`tar.go:347`) treats the marker as a *file presence* check only — content is not parsed during probe. The malformed file passes the probe and `ProbeAndExtractSubpath` (`tar.go:248`) extracts it. The error surfaces at the post-flight `config.Load` in `init.go:288`, which wraps it as `"post-flight verification failed: %w"`.
- Current behaviour: a generic Go-error string like `post-flight verification failed: parsing config: <toml error>`; the orphan `workspaceRoot` directory is rolled back via the deferred `os.RemoveAll` (`init.go:221-225`), so retry is possible but the user gets no remediation hint.

**B. `.niwa/` directory exists in the remote tree but `workspace.toml` is missing entirely (other files only).**
- The probe is *file-presence-aware* (`tar.go:388-395` requires `TypeReg` and an exact path match on `<Rank1Dir>/<Rank1File>`). An empty `.niwa/` directory or a `.niwa/` with sibling files but no `workspace.toml` produces `found.HasRank1() == false`.
- `config.RankDecider` (`discover.go:178`) falls through to `NoMarkerError` at `discover.go:201`. `NoMarkerError.Error()` (`discover.go:141`) already includes a remediation hint: *"If the config lives elsewhere in the repo, pin an explicit subpath via `--from <owner>/<repo>:<subpath>`."*
- Test fixture: `TestMaterializeFromSource_GitHub_EmptyNiwaDirIsNotRank1` (`snapshotwriter_probe_test.go:137`).

**C. Remote is private and the user lacks credentials (auth error).**
- For GitHub: `FetchTarball` (`internal/github/fetch.go:142-146`) maps HTTP 401/403 to a specific error wrapping the status with the message *"verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope"*. `HeadCommit` (`fetch.go:67-70`) has parallel handling.
- Bubbles up through `materializeFromGitHub` (`snapshotwriter.go:489` wraps as `"EnsureConfigSnapshot: fetch %s: %w"`), then `materializeAndSwap` returns the error, then `init.go:266` wraps as `"materializing config repo: %w"`.
- For non-GitHub: `shallowCloneToTemp` (`fallback.go:148-152`) returns `git clone` stderr verbatim. SSH key failures, HTTP 401, missing credentials all surface as a fairly raw stderr blob.

**D. Remote returns 404 (does not exist or wrong URL).**
- For GitHub: `FetchTarball` (`fetch.go:147-149`) returns a generic *"FetchTarball returned 404"* error. No special-casing distinguishes a missing repo from any other non-2xx that isn't 401/403/304.
- Note: A repo that *exists but is empty* (no commits / no default branch) typically yields HTTP 404 from the tarball endpoint — GitHub's tarball API does not differentiate empty-repo from missing-repo. This is exactly the user's `dangazineu/commuter` case in disguise: an "empty remote" today already manifests as a 404 unless the user pushes a commit first.
- For non-GitHub: `git clone` failure of a missing repo is, again, raw `git` stderr.

**E. Remote exists but is the rank-2 legacy layout (whole-repo, no `.niwa/` subpath).**
- Detected by `ProbeMarkers` finding `markers.Rank2Path` (root `workspace.toml`) and not finding `<.niwa>/workspace.toml`. `RankDecider` returns `("", &DeprecationNotice{Rank: 2, ...}, nil)` at `discover.go:197`.
- Materialization *succeeds* with `rank=2`. `init.go:274-283` emits a `EmitRank2Notice` deprecation pointer and installs the embedded plugin so `/niwa:migrate-config` is available. This is **not** a failure case — it's a supported-but-deprecated success path.

**F. Remote clone succeeds but the cloned tree is otherwise empty (no files at all).**
- GitHub: a repo with zero commits returns 404 on the tarball endpoint, so this collapses into case **D** in practice. A repo with one commit that contains only the auto-generated wrapper directory (effectively no real files) produces a tarball whose only entry is the wrapper, and probe records `found = MarkerSet{}`, which `RankDecider` turns into `NoMarkerError` (case **B**'s code path).
- Non-GitHub (file:// or other): `shallowCloneToTemp` succeeds with the empty tree; `ProbeAndFetchSubpath` runs `probeAndResolveCloneRoot` which finds nothing, decider returns `NoMarkerError`. Same surface as case **B**.

**G. Remote clone succeeds and contains `workspace.toml` at the root instead of inside `.niwa/`.**
- This is exactly the rank-2 case. See **E**. The probe finds `markers.Rank2Path` (root `workspace.toml`), `RankDecider` returns the rank-2 deprecation branch, and the snapshot writer extracts the whole repo verbatim into `.niwa/`. Init logs a deprecation notice and installs the niwa Claude Code plugin so the migration command is available.

## Recommendations

| Case | Handling | Rationale |
|------|----------|-----------|
| **A** malformed `workspace.toml` | **fail-loud-with-hint** | The user has a partially-set-up workspace they think works; auto-scaffolding would destroy their intent. Surfacing parse errors precisely lets them fix the TOML. |
| **B** `.niwa/` exists, no `workspace.toml` | **fail-loud-with-hint** | The directory's presence is a strong "someone tried to set this up" signal; auto-scaffolding could clobber a partial migration. The existing `NoMarkerError` text already suggests `--from owner/repo:<subpath>`. |
| **C** private remote, no credentials | **fail-loud-with-hint** | Cannot distinguish "private and you need a token" from "empty" without successful authentication. Auto-scaffolding would mask a credential problem and surprise the user when push fails. |
| **D** remote 404 / does not exist | **fail-loud-with-hint** *(but see Surprises)* | Without authentication, GitHub returns 404 indistinguishably for "missing repo," "empty repo," and "private repo you can't see." Treating 404 as "go bootstrap" is dangerous (auth case **C** masquerades as 404). The right move is to ask the user for an explicit signal — see Open Questions. |
| **E** rank-2 legacy layout | **unchanged from today** | Already handled correctly: rank-2 materializes successfully, emits the deprecation notice, installs the migration plugin. No new work. |
| **F** clone succeeds, tree empty | **fail-loud-with-hint** | Collapses to **B** (no markers found, `NoMarkerError`). Hint is identical. |
| **G** `workspace.toml` at repo root | **unchanged from today** | This *is* rank-2. See **E**. |

### Draft messages for fail-loud cases

These follow the `InitConflictError` shape already established in `preflight.go:36-50` and `init.go:174` (printf `"%s\n  %s"` of `Detail` + `Suggestion`). New sentinel errors live in `workspace/preflight.go` alongside the existing ones.

**Case A — malformed `workspace.toml`:**
```
workspace.toml is malformed in <sourceURL>: <underlying parse error>
  Fix the TOML at .niwa/workspace.toml in the source repo, push, and retry. Or pin an alternate subpath via --from <owner>/<repo>:<subpath>.
```
Where to emit: wrap the `config.Load` error at `init.go:290`. Sentinel: `ErrSourceConfigMalformed`.

**Case B — `.niwa/` exists but `workspace.toml` is missing (and case F):**

The existing `NoMarkerError.Error()` is already user-actionable; the init caller should preserve it verbatim rather than wrap it generically. Recommended polish — when the directory `.niwa/` is *present in the source* but the file is missing, append a `.niwa/` directory hint:
```
no niwa config found at <sourceURL>: probed .niwa/workspace.toml and workspace.toml at source root.
  If your source repo is brand new, push a .niwa/workspace.toml first (or use `niwa init <name>` without --from to scaffold locally). If the config lives elsewhere, pin a subpath via `--from <owner>/<repo>:<subpath>`.
```
Where to emit: replace the generic `"materializing config repo: %w"` wrap at `init.go:266` with a typed-error switch that prints `Detail`/`Suggestion` for `NoMarkerError` and other classified failures.

**Case C — auth error:**
```
cannot read <sourceURL>: <401|403 message>
  niwa needs GH_TOKEN with read access to this repo. Run `gh auth login` (or set GH_TOKEN with `repo` scope) and retry. For non-GitHub remotes, ensure your SSH key or HTTPS credentials are configured.
```
Sentinel: `ErrSourceAuthFailed`. Detection: HTTP status 401/403 from `FetchTarball`/`HeadCommit`, or `git clone` exit code with stderr matching `"Authentication failed"` / `"Permission denied"`.

**Case D — 404 / does not exist:**
```
<sourceURL> not found.
  Verify the slug is correct (org/repo) and the repo exists. If the repo is private, set GH_TOKEN with read access. If the repo is brand new and has no commits yet, push at least one commit (an empty README is enough) and retry.
```
Sentinel: `ErrSourceNotFound`. Detection: HTTP 404 from `FetchTarball`.

## Detection Ordering

When `materializeAndSwap` fails inside `runInit`, the init caller should classify the error before printing. Recommended ordering (most-specific first; first match wins):

1. **Is the error a typed `*config.AmbiguousMarkersError`?** → print its existing remediation message (covers the rare case where both `.niwa/workspace.toml` and root `workspace.toml` co-exist).
2. **Is the error a typed `*config.NoMarkerError`?** → print case **B**/**F** message. This is the "remote reachable, but no niwa config" surface — it covers both "tree is empty" and "tree has files but no markers."
3. **Does the wrapped GitHub status indicate 401 or 403?** → case **C** auth message. Match by string-checking the error text (or, better, introduce a typed `*github.AuthError` with the status code so `errors.As` works without substring matching).
4. **Does the wrapped GitHub status indicate 404?** → case **D** not-found message. Same typing recommendation: `*github.NotFoundError`.
5. **Is the error a parse error from the post-flight `config.Load`?** → case **A** malformed message.
6. **Anything else** (transport failure, gzip error, timeout, fallback `git clone` error not matching auth/404) → today's generic `"materializing config repo: %w"` wrap is fine; the underlying error already carries enough detail.

This ordering matters because:
- Auth (3) must come before 404 (4): a public-API call against a private repo from an unauthenticated client returns 404, but the auth-failure framing is more helpful when a token is present and lacks scope.
- `NoMarkerError` (2) must come before post-flight parse (5): if probe fails, we never get to post-flight, so parse-error classification can't conflict.
- Ambiguous (1) is rarest but its message is the most specific (names both file paths), so it goes first.

## Implications

1. **Three new sentinel error types in `internal/workspace/preflight.go`**: `ErrSourceConfigMalformed`, `ErrSourceAuthFailed`, `ErrSourceNotFound`. Each wrapped in `InitConflictError` at the init-caller seam so the existing `Detail`/`Suggestion` printing in `init.go:174,183,201` handles them uniformly.
2. **One new typed error in `internal/github/fetch.go`**: replace the `fmt.Errorf("github: FetchTarball returned %d", ...)` string-only errors with `*github.StatusError{StatusCode, Message}` so init can classify via `errors.As` instead of substring matching. Low-risk refactor; preserves the existing strings via `Error()`.
3. **No change needed to rank-2 handling (E/G)**: already correct. The empty-source bootstrap feature must not regress the existing `EmitRank2Notice` + plugin-install behaviour — adding it to the test matrix is sufficient.
4. **The bootstrap fallback (the user's primary feature) plugs in *only* at case D-when-empty**: see Open Questions for how to disambiguate from C and the missing-repo subset of D.

## Surprises

1. **GitHub's tarball API returns 404 for empty repos.** A brand-new GitHub repo with zero commits (no default branch) is HTTP 404 from `/repos/{owner}/{repo}/tarball/{ref}`. This means the *primary feature's* trigger condition (the user's `dangazineu/commuter` case) is **indistinguishable at the HTTP layer** from "wrong slug" and from "private repo without token." This is the central design tension for the empty-source bootstrap feature — it's not a "we detected empty, scaffold automatically" story; it's a "we got 404, ask the user what they meant" story.

2. **Existing `NoMarkerError.Error()` already includes a remediation hint.** The `--from <owner>/<repo>:<subpath>` escape-hatch text was added per PRD R28 and is already user-facing. Cases A/B/F can largely reuse this framing rather than invent new copy.

3. **Probe is file-presence only, never content-aware.** A `workspace.toml` containing pure garbage passes the probe and reaches post-flight. This isolates "is there a niwa config?" from "is it valid?" cleanly — case **A** is genuinely a different code path from cases **B**/**F**.

4. **Rank-2 acceptance has an off-switch (`rankTwoAcceptedTestHook` at `discover.go:160`).** When rank-2 is eventually hard-removed, cases **E**/**G** will fall through into `NoMarkerError` (case **B**'s path). The bootstrap fallback should be designed against the post-removal world so its behaviour doesn't shift under rank-2's eventual retirement.

## Open Questions

1. **How does init disambiguate case D (empty/missing remote) from case C (private + no creds)?** Both surface as HTTP 404 in many real configurations. Options to discuss with the human:
   - (a) Require an explicit `--bootstrap` (or `--init-config`) flag for the scaffold-on-404 path; default-deny otherwise. Safe for CI, no surprise for users with typos.
   - (b) Inspect `GH_TOKEN` presence: if no token is set, treat 404 as "credentials likely missing, fail with auth hint"; if a token is set, treat 404 as "repo empty or missing, prompt." Less safe — token may still lack scope.
   - (c) Probe `HeadCommit` separately: an empty-repo 404 from `/commits/HEAD` and a missing-repo 404 from `/repos/{owner}/{repo}` may be distinguishable. GitHub API docs do *not* guarantee this distinction stays stable.
   - Recommendation: (a). It's the only path that survives the C/D ambiguity without false positives, and the user already expects an explicit signal per the scope doc ("user explicitly scoped the main feature to the empty-remote case").

2. **Should an empty `.niwa/` directory in the source (case B) trigger the bootstrap fallback?** The directory's existence is some signal the user intended to set up a workspace. But it could equally be debris from a half-deleted setup. The conservative answer is "no — fail loud per the table"; the user's stated intent ("empty remote" only) supports this.

3. **Repo with only a README (or other non-config files) — same as empty?** Functionally yes (probe finds no markers, `NoMarkerError` fires). But a "real" repo with content is a *stronger* signal that the user intended this repo for something else, not for niwa adoption. Suggests the bootstrap path should be gated on the `--bootstrap` flag rather than triggering on any `NoMarkerError`.

4. **Should non-GitHub remotes (file://, git@) get the same bootstrap path?** The git-clone fallback (`fallback.go:148`) gives weaker error classification (raw stderr), so 404-vs-auth disambiguation is harder. The scoping question: is the primary feature GitHub-only for v1, or does it need to work for `file://` fixtures and self-hosted GitLab/Gitea on day one? The user's primary case is GitHub; recommendation is GitHub-first with non-GitHub deferred.

## Summary

Of the seven failure modes, only **D** (404, when paired with `--bootstrap` user intent) is a candidate for auto-scaffold; the rest should fail-loud with case-specific hints reusing the existing `InitConflictError`/`NoMarkerError` patterns, and rank-2 cases **E**/**G** are already handled correctly today. The detection ordering must classify by typed error (ambiguous, no-marker, auth, not-found, parse) before falling back to a generic wrap, which requires three new sentinels in `workspace/preflight.go` and a small refactor in `internal/github/fetch.go` to replace HTTP-status string errors with typed ones. The central unresolved question is how to tell case D from case C — GitHub's tarball API returns 404 indistinguishably for empty, missing, and private-without-token, so the bootstrap fallback should be gated on an explicit user signal (`--bootstrap` flag) rather than triggering automatically on any 404.
