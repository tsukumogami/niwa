# Phase 2 Research: Codebase Analyst

## Lead A: v1 host coverage commitment

### Findings

**No host-detection layer exists today.** The codebase is structured around a
small set of host-aware seams; everything else is host-agnostic by accident.

1. `ResolveCloneURL(orgRepo, protocol)` in
   `internal/workspace/clone.go:90-112` is the only place that imputes a host
   from a shorthand. It hard-codes `github.com` for both `ssh` and `https`
   protocols. Anything that already contains `://` or starts with `git@` is
   passed through unchanged, so a user who writes
   `https://gitlab.com/org/repo.git` already gets a working clone today —
   but the shorthand `org/repo` always means "GitHub."

2. `Cloner.CloneWith` (`internal/workspace/clone.go:43-76`) shells out to
   `git clone` and is host-agnostic. It carries `--depth`, optional
   `--branch`, and a post-clone checkout for SHA refs. It works against any
   host `git` itself can reach, including `file://` (used heavily in
   `test/functional/`).

3. `parseOrgRepo` in `internal/config/overlay.go:227-286` parses HTTPS URLs,
   SSH URLs, and `org/repo` shorthand. It accepts any HTTPS host (it just
   strips the host before extracting `org/repo`), so a `https://gitlab.com/foo/bar`
   input parses correctly. SSH parsing accepts any `git@<host>:org/repo`.

4. `DeriveOverlayURL` (`internal/config/overlay.go:202-215`) preserves the
   host on `file://` URLs but produces an `org/repo-overlay` shorthand for
   everything else, which `ResolveCloneURL` then re-inflates as github.com.
   So today's overlay-discovery convention is silently GitHub-only even
   when the source repo lives on GitLab.

5. The github API is only used for `ListRepos` org enumeration
   (`internal/github/client.go`) — *not* for any clone or fetch. The
   tarball endpoint is not used anywhere today.

6. The plaintext-secrets guardrail
   (`internal/guardrail/githubpublic.go:55-62`) is explicitly anchored to
   `github.com` only (case-insensitive). GHE, GitLab, and Bitbucket are
   not flagged. This is documented in the comments at lines 213-216:
   "v1 scope is strictly github.com."

**What this means for v1**: niwa already has a working "anything git can
clone" code path. There is no per-host adapter layer to design — the choice
is whether to add one. The proposed tarball-first mechanism for GitHub is
a *new* code path; the git-clone fallback maps directly to today's
`Cloner.CloneWith` (with the snapshot-shape adjustments to drop `.git/`
after extraction).

### Implications for Requirements

- The PRD must commit to a concrete user-facing statement per host class.
  The existing code base supports a clean two-tier story without forcing
  host adapters at v1.
- Whatever the PRD commits to, `ResolveCloneURL`'s "shorthand always means
  GitHub" assumption must be documented as an explicit v1 contract: bare
  `org/repo` means `github.com/org/repo`, and other hosts require a full
  URL. This is already today's behavior; the PRD just needs to ratify it.
- The `DeriveOverlayURL` convention silently dropping host info is a
  latent bug that the PRD's cross-host story should call out (even if the
  fix lands later) — the convention overlay won't work correctly for
  non-GitHub source repos.
- The SHA-endpoint drift check is GitHub-specific. For the git-clone
  fallback, the equivalent is `git ls-remote <url> <ref>`, which is
  already used elsewhere in the git ecosystem and is host-agnostic. The
  PRD should commit to which drift-check primitive applies in each tier.

### Recommendation

**Ship a two-tier coverage model at v1: GitHub-tarball-fast-path +
git-clone fallback for everything else. Defer per-host adapters
(GitLab, Bitbucket, Gitea) to follow-up.**

Concrete user statement for the PRD:

> niwa v1 sources workspace configuration over two mechanisms:
>
> - **GitHub (`github.com`)** is first-class. niwa fetches the requested
>   subpath via the GitHub REST tarball endpoint with selective `tar`
>   extraction, gated by a 40-byte SHA-endpoint drift check. First-fetch
>   bandwidth is the gzipped repo; on-disk footprint is exactly the
>   subpath. Subsequent applies are a single 40-byte HTTP call when
>   nothing changed.
>
> - **Other git-reachable hosts** (GitLab, Bitbucket, Gitea, GitHub
>   Enterprise, self-hosted, `file://`) use a full-clone-and-copy
>   fallback: niwa clones the repo into a temporary directory with
>   `git clone --depth 1`, copies the requested subpath into the
>   snapshot location, then deletes the temporary directory. The
>   correctness guarantees are identical (no `.git/` in the snapshot,
>   atomic refresh, provenance marker recorded), but the first-fetch
>   bandwidth and time cost the whole repo. Drift detection for the
>   fallback uses `git ls-remote <url> <ref>`.
>
> Future per-host adapters (REST archive endpoints for GitLab,
> Bitbucket, Gitea) are tracked as v1.x follow-up. They are pure
> performance optimizations: any user whose host lacks a niwa adapter
> still gets correct snapshots via the fallback.

Rationale for this middle-ground:

- **Universal correctness.** No host class is "unsupported" — the
  fallback covers everything `git clone` can reach. The user-facing
  story is "all hosts work; GitHub is faster on first fetch and on
  drift checks."
- **Smallest blast radius.** Per-host adapters need their own auth flows
  (GitLab uses different PAT scopes; Bitbucket has its own app-password
  model), their own URL parsers, their own response-code handling, their
  own rate-limit budgets. Each one is a multi-week design + implementation
  + testing investment. v1 is already large; piling these in widens the
  release indefinitely.
- **Real-world demand fit.** The dominant niwa user today targets
  GitHub. The first non-GitHub user can still adopt niwa via the
  fallback the day v1 ships and won't be blocked on adapter availability.
- **Clean extension shape.** The fallback path defines the abstraction
  ("snapshot from URL+ref+subpath returning a directory tree"). A future
  GitLab/Bitbucket/Gitea adapter implements the same interface; no
  retroactive PRD revision needed.
- **Honest cost expectation.** Telling a non-GitHub user "first-fetch is
  the whole repo, subsequent applies are cheap" is honest and matches
  what niwa does today (a `git clone --depth 1` is precisely a whole-repo
  fetch). They lose nothing relative to today; they just don't gain the
  GitHub-tarball wins.

### Open Questions

1. **GitHub Enterprise Server**: should it count as "first-class GitHub"
   or as fallback? The tarball endpoint exists on GHE with the same
   shape, just a different base URL (e.g., `github.mycorp.com/api/v3`).
   The PRD should commit to one of: (a) v1 supports GHE via the tarball
   path with a `GH_HOST=` style override; (b) GHE goes through the
   fallback at v1 with GHE adapter as a follow-up. The plaintext-secrets
   guardrail's existing decision was to *not* anchor on GHE
   (githubpublic.go:213-216) — symmetric treatment here would be
   consistent.

2. **Adapter abstraction shape at v1**: even though only GitHub ships an
   adapter at v1, the abstraction needs to be defined now so the
   fallback isn't a special case. Should the PRD commit to the
   abstraction shape (e.g., "a host adapter is a URL parser + a fetcher
   + a drift checker") or leave that to design-doc time?

3. **`file://` is already exercised by functional tests** (the
   `localGitServer` helper). Is `file://` a fallback case (uses git
   clone) or a third tier (zero-network, instant)? Current code treats
   it identically to other git-reachable URLs. PRD should note that
   `file://` is supported via the fallback for parity with today.

---

## Lead B: niwa.toml content_dir requirement

### Findings

`content_dir` is currently optional with a default of `.`
(`internal/workspace/content.go:259-265`):

```go
func contentDirRoot(cfg *config.WorkspaceConfig, configDir string) string {
    contentDir := cfg.Workspace.ContentDir
    if contentDir == "" {
        contentDir = "."
    }
    return filepath.Join(configDir, contentDir)
}
```

So when `content_dir` is omitted, the *config dir itself* becomes the
content root. In a standalone `dot-niwa` repo (today's model) the config
dir is the cloned repo root — i.e., the entire repo is the content root.
That's harmless because the repo is purpose-built for niwa.

In the brain-repo case, with `niwa.toml` at repo root and no
`content_dir`, the *brain repo's whole working tree* becomes the content
root. The blast radius is real:

- `InstallWorkspaceContent` (`content.go:27-49`) reads
  `cfg.Claude.Content.Workspace.Source` relative to `contentDirRoot()`.
  A config that says `source = "CLAUDE.md"` would read `<repo>/CLAUDE.md`
  — quite likely the brain repo's own top-level CLAUDE.md, which was
  written for human readers, not for niwa templating.
- `InstallGroupContent` and `InstallRepoContent` follow the same pattern.
- `autoDiscoverRepoSource` (`content.go:240-254`) probes
  `{content_dir}/repos/{repoName}.md`. With repo-root as content_dir,
  this probes `<repo>/repos/<repoName>.md`. If a brain repo happens to
  have a directory called `repos/` (some monorepos do), niwa silently
  starts auto-discovering files there.
- `installContentFile` (`content.go:270-294`) reads the file under
  `contentRoot` and writes it to the target via `expandVars`. Template
  variables in arbitrary brain-repo files would be rewritten on the way
  out — a non-obvious surprise.

The path-safety guard is `checkContainment` (`content.go:299-327`), which
ensures the *resolved source path stays within* `contentRoot`. With
`contentRoot = repoRoot`, every file in the repo passes that check.
`checkContainment` does its job (no `..` escape, no symlink escape) but
its definition of "allowed directory" trusts whatever `content_dir`
produces. It is not a brain-repo-isolation primitive.

`validateContentSource` (`config/config.go:378-391`) blocks `..` and
absolute paths in source values, which prevents
`source = "../../etc/passwd"` style attacks but does not address the
brain-repo blast radius — the threat model is "niwa silently picks up
unrelated files," not "niwa is tricked into reading arbitrary disk."

`validateWithinDir` (`workspace/discover.go:129-150`) enforces hooks/env
discovery boundaries against `configDir`, not `contentDir`. So
hooks-discovery is already implicitly bounded by the config dir, which
in the brain-repo + repo-root case is still the whole repo.

### Implications for Requirements

The PRD has three options for the `niwa.toml` rank-3 case (when
discovery resolves to repo root):

| Option | Mechanic | User cost | Safety |
|---|---|---|---|
| A. **Required** | `[workspace] content_dir` becomes a *required* field when discovery resolved to repo root via `niwa.toml`. | Brain-repo authors must declare a content directory explicitly. One-line addition to `niwa.toml`. | Tightest: brain-repo authors are forced to think about which subdir is content. |
| B. **Optional with default ban-root** | `content_dir` stays optional, but when discovery resolves via `niwa.toml`, an unset `content_dir` defaults to the *absence* of content — `cfg.Claude.Content.Workspace.Source` and friends become "no content installed" rather than "content root is repo root." | Zero migration cost, but silent change in behavior for any brain-repo author who omits the field. | Medium: niwa never reads outside an explicit content path, but the user discovers this only when wondering "where did my CLAUDE.md go?" |
| C. **Optional with default `./`** (status quo) | Inherit today's behavior: unset means "repo root is content root." | Zero friction. | Worst: matches the brain-repo blast radius exactly. |

The validators today don't bound the damage. `checkContainment` trusts
the configured content root; the only path-traversal protection
(`validateContentSource`) operates against the content root, not against
"arbitrary brain repo files."

### Recommendation

**Option A: `content_dir` is *required* when discovery resolves to repo
root via `niwa.toml`. It remains optional in all other cases.**

Rationale:

1. **Forces an explicit decision at the right moment.** The brain-repo
   author is the one who knows which directory is "for niwa" vs "for
   humans." Making them name it converts a silent foot-gun into a
   conscious choice.
2. **No surprises for existing users.** The `.niwa/workspace.toml`
   (rank 2) and root `workspace.toml` (rank 3) cases keep `content_dir`
   optional — the dot-niwa repo is purpose-built, and its root *is* the
   content root by definition. So today's standalone `dot-niwa` users
   experience zero behavior change.
3. **Easy validator location.** A new validator runs after discovery
   resolution: if the discovery rank is "rank 3 niwa.toml" AND
   `cfg.Workspace.ContentDir == ""`, fail with a targeted error. The
   validator has all the information it needs (discovery records the
   rank; the parsed config carries `ContentDir`). Implementation cost is
   small.
4. **Aligns with the lead-discovery-conventions recommendation**, which
   leaned toward required.
5. **Preserves the "scaffold default" experience.** The scaffolded
   `workspace.toml` already writes `content_dir = "claude"` explicitly
   (`scaffold.go:15`), so users who started from scaffold never see the
   blast radius even today. The new requirement just extends the same
   safety to brain-repo authors who skip the scaffold path.

The rejection of Option B is mostly about predictability: silent
behavior changes (Option B) are worse than loud errors (Option A) when
the user has clearly signaled "this is a brain repo, not a dedicated
niwa repo."

The validator error wording (proposal):

> niwa.toml at repository root requires `[workspace] content_dir` to be
> set explicitly. When discovery resolves to a brain repository via
> niwa.toml, niwa needs to know which subdirectory is the workspace
> content tree (so unrelated brain-repo files are not read as content).
> Set `content_dir = "<your-content-subdir>"` (e.g., `"claude"`,
> `".niwa-content"`) and re-run.

### Open Questions

1. **Should `content_dir = "."` be a valid declaration**, opting
   explicitly into "repo root is the content root"? Allowing it
   preserves the escape hatch for brain-repo authors who really do mean
   "the whole brain repo is content"; rejecting it forces
   subdirectory-scoping universally. Recommend: allow `"."` as a valid
   explicit value (it's an opt-in with informed consent), but the
   default omitted case still errors.
2. **Should the requirement apply to `[workspace] setup_dir` too?**
   `setup_dir` (`config.go:127`) has the same shape and the same
   blast-radius concern. The PRD should commit one way or the other for
   consistency.
3. **Is the validator coupling discovery-rank to validation a clean
   layering?** The config parser today doesn't know how the config was
   sourced. The redesign needs to propagate "discovery rank" (or at
   least "this came from a niwa.toml at repo root") into validation
   context. PRD-level commitment; design-doc detail.

---

## Lead C: Edge-case behavior contracts

### Findings

#### Empty subpath in slug (`org/repo:`)
- `ResolveCloneURL` (`clone.go:90`) doesn't parse subpaths today, so this is greenfield. The PRD's slug grammar regex from
  `lead-discovery-conventions.md`
  (`^[^/:]+/[^:/]+(:[^@]+)?(@.+)?$`) treats `:` followed by nothing as an
  empty capture group.
- Analogous behavior: `parseOrgRepo` (`overlay.go:227-286`) rejects empty
  components in `org/repo` strictly. The pattern is "empty-component =
  invalid input." Following that pattern: empty subpath after `:` should
  error with the same precision.

#### Subpath that exists but has no workspace.toml
- Today's only equivalent: `CheckInitConflicts`'s `ErrNiwaDirectoryExists`
  case (`preflight.go:67-74`) — `.niwa/` exists but `workspace.toml`
  doesn't. The error names the missing file and tells the user to remove
  the directory.
- For subpath sourcing the situation is different — the user *explicitly*
  named the path, so falling through to discovery (which the rank-2/3/4
  mechanism does for unnamed sources) would mask their intent.
- The lead-discovery-conventions precedent (table line 97) is "explicit
  subpath bypasses discovery; if missing, hard error." That's the right
  pattern.

#### Subpath resolves to a file (not a directory)
- L4 leaned toward "treat it as the dir containing this file"
  (lead-discovery-conventions table line 98). This matches the rank-4
  `niwa.toml` semantics: pointing at the file resolves to its parent
  directory becoming the config dir. PRD should confirm.

#### Default branch renamed remotely between two applies
- Today's `SyncConfigDir` runs `git pull --ff-only origin` — no branch
  argument. So the default branch rename (e.g., `master` → `main`) on
  origin is silently re-resolved on each apply. There's no stable
  "what branch were we tracking" record.
- The redesign records `source_ref` in the registry (per round-1
  decisions). The PRD's "default-branch ref resolution timing" open
  question (research lead 3) is the relevant decision: pin at init time
  or re-resolve every apply. Either choice has a different "default
  branch renamed" behavior.

#### Repo renamed mid-flight (GitHub returns 301)
- The GitHub REST API follows repo renames via 301 by default; `git`
  clone over HTTPS follows GitHub's redirect transparently and usually
  emits a warning to stderr. The git fallback path inherits whatever
  `git clone`'s default redirect-following does.
- Today there's no explicit policy in the codebase. The plaintext-secrets
  guardrail (`githubpublic.go:74`) reads `git remote -v` to enumerate
  remotes; under a rename, the remote URL still points at the old name
  until the user updates it manually. Same shape applies after the
  redesign for the registry's `SourceURL`.

#### Very large brain repos (slow tarball)
- Today the only timeout is whatever `git clone` defaults to (none for
  the actual transfer). `Cloner.CloneWith` accepts `context.Context` so
  caller-side cancellation works, but no caller imposes a deadline.
- The Reporter (`reporter.go:62-115`) already has spinner-based progress
  for slow operations. It runs during git output; for tarball downloads
  niwa would need to plumb the spinner through the HTTP client.
- No timeout policy exists anywhere in `internal/`; the
  `internal/github/client.go` HTTP client uses `http.DefaultClient`
  (`client.go:36`), which has no per-request timeout.

#### Network unreachable during refresh
- Today: `SyncConfigDir` returns the git error. `runApply` aborts with
  "applying to <workspace>: pulling config from origin: exit status 128."
  Hard error — apply does not continue with the cached config.
- The asymmetric precedent is `SyncRepo` (`sync.go:103-150`) for managed
  source repos: `FetchRepo` failure becomes
  `SyncResult{Action: "fetch-failed", Reason: err.Error()}` and apply
  continues. This is the "informational, not blocking" pattern the PRD
  could extend to snapshots.
- The redesign's snapshot model has an extra capability today's pull
  doesn't: a previously-fetched snapshot is on disk and is still valid
  (just not freshest). That's the use case for "continue with cache."

#### Snapshot corrupted on disk
- No analog today — there's no "validate the disk-state matches a
  manifest" code path. `isValidGitDir` (`overlaysync.go:55-60`) is the
  closest pattern: checks `.git` exists AND `git rev-parse HEAD`
  succeeds. The "corrupt clone is not a valid prior clone" comment at
  line 53 captures the spirit.
- For snapshots, the equivalent is "provenance sidecar exists AND parses
  AND its `resolved_commit` matches a verifiable hash of the snapshot
  contents." The verification cost is real — hashing the whole
  snapshot is O(snapshot-size).
- A cheaper heuristic: presence and parseability of the provenance
  sidecar is enough; treat absence/parse-failure as corrupted. This
  matches the `isValidGitDir` philosophy of "if the marker isn't right,
  redo."

### Recommended PRD commitments

| Scenario | Proposed niwa behavior | Rationale |
|---|---|---|
| `org/repo:` (empty subpath after `:`) | **Hard error** at slug-parse time. "subpath is empty after `:`; remove the colon to use the repo root, or pass a non-empty subpath like `org/repo:.niwa`." | Matches `parseOrgRepo`'s strict empty-component rejection. Trying to silently treat `:` as `/` invites bug reports because the user clearly meant to type something. |
| Subpath exists but contains no `workspace.toml` | **Hard error**, do *not* fall through to discovery. "no workspace.toml found at <subpath>/ in <slug>; verify the subpath or pass a different one." | Explicit subpath = explicit intent. Falling through to discovery would mask typos and surprise users. |
| Subpath resolves to a file | **Treat as "config dir is the parent directory of this file"** (rank-4 single-file semantics). | Matches lead-discovery-conventions. Lets users point at `org/repo:path/to/niwa.toml` directly and get the same result as `org/repo:path/to`. Convenient for shell tab-completion that lands on files. |
| Default branch renamed remotely | **Two answers**, depending on PRD's open decision on default-branch resolution timing. If pinned at init: hard error on next apply with "the recorded source ref `master` no longer exists; the source repository's default branch may have been renamed. Run `niwa registry repin <name>` to track the new default." If re-resolved every apply: silent re-resolve, with a one-time `niwa apply` notice "source default branch is now `main` (was `master`)." | The PRD's stance on resolution timing decides this. Either way, the user gets *some* signal that the rename happened — silent silent change is the wrong answer in both scenarios. |
| Repo renamed (301 from GitHub) | **Follow the redirect once on the immediate fetch, but record a one-time `DisclosedNotices`-backed warning** ("source repo has been renamed from `org/old` to `org/new`; update your registry with `niwa registry retarget`"). Persistently re-following on every apply masks the rename in the registry. | Mirrors how the Reporter handles other one-time notices. The user keeps working, but is told about the drift so they can fix the canonical reference. |
| Very large brain repos / slow fetch | **Spinner-based progress via the existing Reporter (no timeout at v1)**. Reuse `Reporter.Status("syncing config…")` for the tarball download; emit byte-count updates if the HTTP client surfaces them. Document a known limitation: "snapshots over ~100 MB may take noticeable time on first fetch; subsequent applies use the SHA-endpoint drift check and are near-instant." | Today the codebase has no timeout policy and the reporter already does spinner UX. Adding timeouts at v1 widens scope. The doc-the-limit approach matches today's behavior. The `--ignore-cache` open question from L2 ("Should drift-check happen on apply, status, both, or be configurable?") could be coupled here at design-doc time. |
| Network unreachable during refresh | **Apply continues with the cached snapshot, with a loud `Reporter.Warn` notice** ("could not refresh config snapshot from <url> (network unreachable); using cached snapshot from <fetched_at>"). | Matches `SyncRepo`'s "fetch-failed → continue informationally" pattern (`sync.go:108-110`). The snapshot is on disk and still valid; refusing to apply punishes users for ephemeral network loss. The notice mirrors how niwa today reports `SyncResult{Action: "fetch-failed"}`. |
| Snapshot corrupted on disk (missing/unparseable provenance sidecar, partial extract, wrong perms) | **Auto-heal: refetch.** Only emit a warning if the refetch *also* fails (then hard-error with a recovery hint pointing at `niwa registry refetch <name>`). | Snapshots are by definition disposable — there's no user state to preserve. Auto-healing is the right pose. The "treat as missing, refetch, fail loudly only if the refetch fails" pattern matches `isValidGitDir`'s philosophy of "if the marker isn't right, the prior clone never happened." |

### Open Questions

1. **Should the network-unreachable case be configurable?** A
   `--strict-refresh` flag (or registry-level setting) for users who want
   "either I get the latest config or no apply runs" (e.g., CI
   environments where stale config is unsafe). Out of scope for v1, but
   the PRD should signal whether the design admits this.
2. **Snapshot integrity check granularity**: presence-of-sidecar, or
   sidecar-+-content-hash? The cheap version (presence) catches most
   real corruption (interrupted extract). The expensive version
   (content hash) catches user fiddling. PRD should commit to which.
3. **Rate limit and 429 handling**: the GitHub adapter will hit rate
   limits eventually. Should v1's behavior be "retry with backoff,"
   "fall through to git clone fallback after N retries," or "hard error
   with a "wait and retry" hint"? The codebase has no retry primitives
   today.
4. **Repo-rename detection at the SHA-endpoint level**: when the API
   returns a 301 from `/repos/<owner>/<old>/commits/<ref>` to
   `/repos/<owner>/<new>/commits/<ref>`, can niwa detect that without
   downloading the tarball? Probably yes (the redirect URL is
   inspectable). Worth confirming at design-doc time.
5. **The `--allow-dirty` flag's fate** — already an open PRD question
   from the scope file. Lead C touches it indirectly because the
   "snapshot corrupted" case is the closest replacement use case for
   "I edited the snapshot locally and want apply to skip the refresh."
   The PRD should connect these.

---

## Lead D: Failure-mode narratives

### Recommended narratives

The trigger / message / remediation table below uses niwa's existing
direct, no-preamble error style. Multi-line errors follow the
`SyncConfigDir` two-line pattern (problem on first line, hint on
second line indented).

#### 1. Subpath not found in source repo

- **Trigger**: User runs `niwa init <name> --from org/brain-repo:wrong-subpath`,
  or `niwa apply` after the brain repo's maintainer renamed the subpath
  upstream.
- **Intended message**:

  > no workspace config found at `wrong-subpath/` in `org/brain-repo`.
  > Verify the subpath or run `niwa registry show <name>` to see the
  > recorded source URL.

- **Remediation**: User checks the brain repo's actual layout (e.g., on
  GitHub web), then either retypes the slug with the correct subpath or
  runs `niwa registry retarget <name> --to org/brain-repo:correct-subpath`.

#### 2. Discovery ambiguous (multiple markers at root)

- **Trigger**: User runs `niwa init <name> --from org/brain-repo` (no
  explicit subpath) and the brain repo has both `.niwa/workspace.toml`
  AND a root `workspace.toml`.
- **Intended message**:

  > ambiguous niwa config in `org/brain-repo`: both
  > `.niwa/workspace.toml` and `workspace.toml` exist at the repository
  > root.
  >   Remove one, or pass an explicit subpath:
  >     niwa init <name> --from org/brain-repo:.niwa
  >     niwa init <name> --from org/brain-repo:

- **Remediation**: Brain-repo maintainer removes the unintended marker;
  or the user disambiguates with an explicit subpath. The
  empty-after-colon form is suggested as the way to mean "repo root"
  even though Lead C recommends rejecting empty-after-colon. Resolving
  this contradiction is an open question (use a different syntax for
  "explicitly repo root" — perhaps `org/repo:.` or `org/repo:/`).

#### 3. Discovery yields nothing (no marker at root, no explicit subpath)

- **Trigger**: User runs `niwa init <name> --from org/some-repo` against
  a repo that has none of `.niwa/workspace.toml`, root `workspace.toml`,
  or root `niwa.toml`.
- **Intended message**:

  > no niwa config found in `org/some-repo`. Expected one of these at
  > the repository root:
  >   - `.niwa/workspace.toml`
  >   - `workspace.toml`
  >   - `niwa.toml`
  >   To use a different location, pass an explicit subpath:
  >     niwa init <name> --from org/some-repo:path/to/config

- **Remediation**: User confirms they meant the right repo; if so, they
  add a marker file to the brain repo or pass `:subpath` explicitly.

#### 4. Host adapter not implemented and fallback fails (non-network reason)

- **Trigger**: User runs `niwa init <name> --from
  https://exotic-host.example.com/org/repo.git`. The GitHub adapter
  doesn't apply; the git-clone fallback runs `git clone` and fails for a
  non-network reason (e.g., authentication required and not configured,
  HTTPS cert error, server returns "service not available").
- **Intended message**:

  > could not fetch workspace config from
  > `https://exotic-host.example.com/org/repo.git`.
  >   git clone failed: <wrapped git stderr>
  >   niwa uses git clone as a fallback for hosts other than github.com.
  >   Verify the URL and your git authentication for that host.

- **Remediation**: User configures git credentials (PAT, SSH key,
  certificate) for the host outside of niwa, then retries.

#### 5. Snapshot refresh failed (network) — non-fatal

- **Trigger**: Mid-apply, the tarball fetch (or `git ls-remote` drift
  check) returns a transport error (DNS lookup failed, connection
  refused, TLS handshake timed out).
- **Intended message** (warn, not error; apply continues):

  > warning: could not refresh config snapshot for `<workspace-name>`
  > from `<source-url>`: <wrapped network error>.
  > Using cached snapshot from <fetched_at>. Re-run when the network is
  > reachable to pick up upstream changes.

- **Remediation**: User keeps working; on next apply with network
  available, the snapshot updates.

#### 6. Snapshot corrupted on disk — fatal (after auto-heal failed)

- **Trigger**: Snapshot's provenance sidecar is missing/unparseable AND
  refetch ALSO fails (e.g., disk full, repo deleted upstream).
- **Intended message**:

  > workspace config snapshot at `<.niwa>` is corrupted (no provenance
  > marker found) and refetch failed: <wrapped fetch error>.
  >   Manually clear the snapshot and re-init:
  >     rm -rf <.niwa-path>
  >     niwa init <name> --from <source-url>

- **Remediation**: User clears the snapshot dir manually and re-inits.
  This is the path-of-least-resistance pattern from the current
  recovery-scenarios docs (lead-current-architecture finding 6).

#### 7. URL change detected on apply (registry says X, snapshot's provenance says Y)

- **Trigger**: User edited `~/.config/niwa/config.toml` to change the
  `source_url` of an existing registry entry without running a refetch
  (or some external migration tool changed it).
- **Intended message**:

  > registry source URL for `<workspace-name>` does not match the
  > snapshot on disk:
  >   registry says:    `<new-url>`
  >   snapshot says:    `<old-url>` (fetched <fetched_at>)
  >   Run `niwa registry retarget <name> --to <new-url>` to refetch
  >   from the new source, or restore the previous URL in
  >   `~/.config/niwa/config.toml`.

- **Remediation**: User runs the suggested `retarget` command (if the
  change was intentional) or restores the registry entry (if not).

### Open Questions

1. **The repo-root sentinel in the slug**: narrative #2 suggests
   `org/repo:` to mean "repo root" but Lead C recommends rejecting
   empty-after-colon. The PRD must pick a single sentinel. Candidates:
   `org/repo:.` (mirrors filesystem dot), `org/repo:/` (mirrors
   filesystem root), or "no colon at all" (the bare-shorthand form
   already means "discover at root, then repo-root if discovery fails").
   Cleanest is probably "bare slug runs discovery; explicit subpath is
   always non-empty." Then narrative #2's hint becomes "remove one of
   the markers; ambiguity at root means there's no unambiguous way to
   ask for it."

2. **Provenance sidecar location**: narratives #6 and #7 reference "the
   provenance sidecar." Per the scope file, this is a downstream
   design-doc question. The PRD doesn't need to nail it down, but
   should commit to *what* the sidecar records (URL, host, owner, repo,
   subpath, ref, resolved commit, fetched-at, mechanism) so the URL-
   change detection is well-defined.

3. **The `niwa registry retarget` command**: several narratives
   (#1, #6, #7) reference it as remediation, but it doesn't exist
   today. Is this a v1 deliverable, or is the v1 remediation always
   "manually edit `~/.config/niwa/config.toml` and run `niwa apply`"?
   The PRD should commit explicitly. Without `retarget`, the
   remediation hints in those narratives need rewording.

4. **`niwa registry refetch <name>`** (narrative #6's auto-heal-then-
   manual-recovery hint): same question. If not in v1, the hint becomes
   "manually rm -rf the snapshot directory and re-run apply."

5. **The exact prefix on warning lines**: today's code uses both
   "warning: " (lowercase, used by `Reporter.Warn` and several inline
   `fmt.Fprintf(stderr, "warning: …")` patterns) and "note: " (used by
   the vault bootstrap pointer at `init.go:228`). PRD should ratify
   "warning: " for non-fatal-but-attention-worthy and "note: " for
   purely informational.

---

## Summary

The codebase is structured to support a "GitHub-first-class +
git-clone-fallback for everything else" v1 host model with minimal
disruption — there is no per-host adapter layer to retrofit, only a
small set of GitHub-anchored seams (`ResolveCloneURL` shorthand
expansion, the plaintext-secrets guardrail, `DeriveOverlayURL`'s host
amnesia) — so v1 should commit to that two-tier model and defer per-host
adapters as performance-only follow-ups. The biggest constraint
discovered is that today's `content_dir` defaults silently to repo root
when omitted (`content.go:259-265`), and existing path-safety validators
don't bound the brain-repo blast radius — making `content_dir` *required*
for the rank-3 `niwa.toml` discovery case is the smallest-surface fix.
The biggest open question is the slug grammar's "repo root" sentinel:
narrative #2's ambiguity-resolution hint contradicts Lead C's empty-after-
colon rejection, and resolving it cleanly affects every error message
the PRD writes about subpath syntax.
