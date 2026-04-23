<!-- decision:start id="github-tarball-fetch-client" status="assumed" -->
### Decision: GitHub tarball fetch client implementation

**Context**

PRD R14 commits niwa to fetching GitHub-hosted workspace config via the
REST tarball endpoint, stream-extracting with Go's `archive/tar`, and
filtering to the requested subpath so files outside that subpath never
land on disk. R16 layers a 40-byte `commits/{ref}` SHA endpoint plus
ETag-conditional GETs against the tarball endpoint for drift detection.
R17 reads `GH_TOKEN` for auth (anonymous when unset). R18 requires
following a 301 rename redirect once and emitting a one-time `note:` via
the existing `DisclosedNotices` mechanism.

niwa already has `internal/github/client.go` exposing an `APIClient`
struct with `HTTPClient *http.Client`, `Token string`, and `BaseURL
string`, plus a `Client` interface (currently just `ListRepos`). All
three callers (`internal/cli/{apply,create,reset}.go`) construct it
identically: `gh := github.NewAPIClient(resolveGitHubToken())`. The
`workspace.Applier` injects `github.Client`, which is how tests substitute
fakes. The `BaseURL` field is the established test-fixture seam.

This decision picks the package boundary, the API shape, where the tar
extractor lives, and how auth and redirects are plumbed.

**Assumptions**

- The `tarballFakeServer` (Decision 4) accepts a substitutable base URL
  and serves both the `api.github.com`-style endpoints and the codeload
  302 target on the same host:port, so a single `BaseURL` swap covers
  every URL the client builds.
- Symlink entries in tarballs are skipped in v1. PRD R10 specifies
  "regular files"; reproducing symlinks safely (without escaping the
  subpath via link targets) is out of scope and can be revisited if the
  PRD broadens.
- The fetcher abstraction from Decision 3 wraps this client without
  re-implementing HTTP plumbing — `GitHubFetcher.Materialize` calls
  `APIClient.FetchTarball` and `APIClient.HeadCommit`, then delegates
  extraction to the package-local `extractSubpath` helper.
- `GH_TOKEN` is stable for the lifetime of the process (env vars don't
  mutate mid-run), so reading it once at constructor time satisfies
  R17's "once per fetch" requirement.

**Chosen: Extend `internal/github/client.go` with methods on `APIClient`**

Add three concerns to the existing package, all hung off the existing
`APIClient` struct:

1. **Methods on `APIClient`.** Two new methods plus a small redirect
   helper:
   ```go
   func (c *APIClient) HeadCommit(ctx context.Context, src Source, etag string) (oid, newETag string, status int, err error)
   func (c *APIClient) FetchTarball(ctx context.Context, src Source, etag string) (body io.ReadCloser, newETag string, status int, redir *RenameRedirect, err error)
   ```
   `Source` is the typed five-tuple from the slug parser (decided
   elsewhere). `RenameRedirect{From, To string}` is non-nil iff a 301
   between `/repos/{old}` and `/repos/{new}` was followed during the
   request.

   The `Client` interface widens to include both methods. Existing
   `workspace.Applier` callers continue to inject `github.Client`
   without constructor changes.

2. **Tar extraction as a package-local free function.** A new file
   `internal/github/tar.go` with
   ```go
   func extractSubpath(r io.Reader, subpath, dst string) error
   ```
   that wraps `gzip.NewReader` then `tar.NewReader`, strips the
   GitHub-emitted top-level `<owner>-<repo>-<sha>/` prefix, and applies
   a literal-prefix subpath filter (`strings.HasPrefix(rel,
   subpath+"/") || rel == subpath`). Trailing `/` is appended before
   matching to prevent `.niwa-extras/` from matching `.niwa`. Regular
   files (`tar.TypeReg`) are written via per-file `os.MkdirAll` +
   `os.OpenFile`. Directory and symlink entries are skipped. The
   extractor consumes the `io.Reader` returned by `FetchTarball`
   directly — no temp file, no in-memory buffering of the whole
   tarball.

3. **Auth at construction.** `APIClient.Token` is set once by
   `NewAPIClient(token)` (existing pattern). When non-empty, requests
   carry `Authorization: Bearer <token>`; when empty, the header is
   omitted entirely (anonymous public-repo access works).

4. **Redirect handling via `CheckRedirect` + chain inspection.**
   `APIClient` installs a `CheckRedirect` on its `http.Client` (via a
   small per-request `http.Client` clone, or once at construction) that:
   - Records each hop's old and new URL onto a request-scoped value.
   - Caps the chain at 3 redirects.
   - Returns `nil` (continue following) for both 301 and 302 — the
     codeload 302 must be transparent, the rename 301 must be observed.

   After the response returns, `FetchTarball` walks the recorded chain.
   If any hop is a 301 between two `/repos/{owner}/{repo}/...` URLs
   whose `(owner, repo)` segments differ, that's the rename — populate
   `RenameRedirect{From: "<oldorg>/<oldrepo>", To:
   "<neworg>/<newrepo>"}`. The caller (the snapshot pipeline that owns
   `DisclosedNotices`) emits the one-time notice.

   Tests substitute `BaseURL` to point at the `tarballFakeServer`. The
   fake serves `/repos/{owner}/{repo}/tarball/{ref}` with a 302 to its
   own `/codeload/...` path on the same host:port, so a single BaseURL
   swap covers everything. No `http.RoundTripper` substitution is
   needed.

**Rationale**

This option mirrors every established pattern in the repo with the
fewest moving parts:

- The existing `APIClient` already holds the right state (HTTPClient,
  Token, BaseURL). The token-once-at-construction satisfies R17 with
  zero new plumbing.
- The `BaseURL`-substitution test seam is already used by the existing
  `ListRepos` call sites; reusing it for the tarball path keeps the
  fixture wiring identical to what contributors already understand.
- The `Client`-interface injection seam is already wired through
  `workspace.NewApplier(gh github.Client)`. Widening the interface adds
  ~20 lines per fake but doesn't introduce a parallel injection point.
- Tar extraction is small enough (~100 lines) that creating a separate
  `internal/tar` or `internal/archive` package would be gold-plating
  for a single caller.
- Splitting `HeadCommit` and `FetchTarball` into two methods (rather
  than one combined "fetch with drift check") matches the underlying
  API: the SHA endpoint is independent and may return 304 without ever
  touching the tarball endpoint (AC-G2 verifies this exact behaviour).
- Returning the redirect chain via `*RenameRedirect` rather than via a
  separate `FollowRedirect` call keeps redirect handling internal to
  the fetch — the caller doesn't have to orchestrate two HTTP requests
  when one is sufficient.

The "blends metadata lookup with content fetch" objection is cosmetic.
Both code paths share auth, base URL, and HTTP client; nothing forces
separation. If the package later grows past ~600 lines or develops
genuinely divergent behaviour (e.g., different timeouts), splitting is
a mechanical refactor with no on-disk consequences.

**Alternatives Considered**

- **New package `internal/github/fetcher/`.** Cleaner conceptual split
  of "metadata lookup" (`APIClient.ListRepos`) from "content fetch"
  (`Fetcher.FetchTarball`), but duplicates the auth-resolution and
  BaseURL plumbing across two packages, requires the `Applier` to
  accept a second injection (or callers to wire both together), and
  makes "two packages both labelled github" more confusing than one
  package with three methods. The cosmetic separation costs more than
  it saves.

- **Embed inside the cross-host fetcher abstraction (no `github`
  subpackage).** Push the HTTP plumbing directly into
  `GitHubFetcher`'s methods (the implementation chosen by Decision 3),
  leaving `internal/github/client.go` untouched. Conceptually clean but
  duplicates the request-construction, header-setting, and BaseURL
  patterns from `APIClient` without sharing them. Tests covering both
  org listing and tarball fetch (functional tests do both) would have
  to override two BaseURLs. Couples this decision tightly to Decision
  3's shape — a future refactor of the Fetcher interface drags HTTP
  plumbing along with it.

**Consequences**

- The `github.Client` interface gains two methods. The single in-tree
  fake (in `workspace` tests) and any future fakes must implement
  `HeadCommit` and `FetchTarball`. For `ListRepos`-only call sites
  (`reset.go`), the new methods are never called and can return
  `nil, "", 0, nil, nil`.
- `internal/github/` grows by roughly 250-300 lines (HTTP methods +
  redirect helper + tar extractor) plus tests. Still a single-purpose
  package, still navigable.
- The `tarballFakeServer` fixture (Decision 4) gets a clear API
  contract: serve `/repos/{owner}/{repo}/{commits|tarball}/{ref}` and
  the codeload follow-up on the same host:port. Tests substitute
  `client.BaseURL` and exercise the full redirect + ETag + auth path
  end-to-end.
- Symlink handling is deferred. Source repos that contain symlinks
  inside the targeted subpath will produce snapshots without those
  links. If a future user reports this, broadening the extractor is a
  localised change in `internal/github/tar.go`.
- Future divergence between metadata and content HTTP behaviour
  (different timeouts, retries, pooling) can be addressed by splitting
  the package later. Today's choice doesn't lock that in.
- The `RenameRedirect` return value is the single place rename
  detection happens. The caller (the snapshot pipeline) owns the
  one-time-notice logic, keeping HTTP concerns out of the
  notice-emission code path.
<!-- decision:end -->
