<!-- decision:start id="empty-repo-detection" status="assumed" -->
### Decision: v1 handling of zero-commit remotes (404 from GitHub tarball API)

**Context**

The `--bootstrap` flag (Decision 1) lets `niwa init --from <slug>` scaffold a
minimal `.niwa/workspace.toml` and stage it in a worktree when the remote is
reachable but missing a niwa config. That feature plugs in at
`*config.NoMarkerError` from `RankDecider` — i.e. the remote returned a
tarball, the probe ran, and no markers were found.

A subset of the user's "I just created the repo on GitHub" scenario never
reaches that path. A GitHub repo with zero commits (no `HEAD` ref) returns
HTTP 404 from `/repos/{owner}/{repo}/tarball/HEAD`, which surfaces in
`internal/github/fetch.go:149` as the generic
`github: FetchTarball returned 404`. At that layer 404 is indistinguishable
from "wrong slug," "private repo without credentials," and "empty repo."
The exploration's `lead-other-failures.md` flagged this as case D and the
exploration findings carried it forward as gap G3.

This decision asks whether v1 should disambiguate the zero-commit subset of
404 (so bootstrap can also scaffold against a truly empty remote), or
whether v1 should ship with the `NoMarkerError` path only and let
zero-commit users push a first commit before retrying.

**Assumptions**

- GitHub web-UI creation defaults to "Initialize with README" toggled on,
  so the majority of user-created repos reach the `NoMarkerError` path
  rather than the 404 path. Users who deliberately toggle the README off
  (or use `gh repo create` without `--add-readme`) hit 404.
- The typed-error refactor in `internal/github/fetch.go` recommended by the
  exploration (`*github.StatusError` replacing string-only status errors)
  is in scope for v1 because adjacent failure-mode classification
  (Decision 3) needs it anyway.
- Non-GitHub remotes (`file://`, GitLab, Gitea) surface 404-equivalent
  failures as raw `git clone` stderr from `fallback.go:151`. Any
  disambiguation strategy adopted for GitHub does not generalize to those
  transports without additional per-host plumbing.
- Rate limits for `GET /repos/{owner}/{repo}` are not a concern for the
  init flow itself (init is one-shot), but unauthenticated calls against
  private repos still 404, which limits how much disambiguation the extra
  call actually buys.
- v2 can revisit zero-commit handling once real users report hitting it.
  The user's stated scope is "the empty repo I just created" — they
  haven't pinned which auto-init posture they use.

**Chosen: C — Hint-only middle ground**

On 404 from `FetchTarball`, niwa stays in fail-loud mode but emits a
case-specific message that names the zero-commit scenario explicitly:

```
<sourceURL> not found.
  Verify the slug is correct (org/repo) and the repo exists. If the repo
  is private, set GH_TOKEN with read access. If the repo is brand new and
  has no commits yet, push at least one commit (an empty README is enough)
  and retry with --bootstrap.
```

The message is delivered through the typed-error classifier added in
Decision 3: `*github.StatusError{StatusCode: 404}` matched via `errors.As`
at the `runInit` seam, then wrapped in the `InitConflictError` shape
already used by `preflight.go:36-50` and `init.go:174`. No extra API call,
no new bootstrap subpath, no special handling for the zero-commit subset
in v1.

Implementation footprint:

1. Replace the string-only 404 error in `internal/github/fetch.go:149`
   with `&StatusError{StatusCode: 404}` (same refactor Decision 3 already
   requires).
2. Add a classifier branch in `internal/cli/init.go` around the existing
   `materializing config repo: %w` wrap that matches 404 and prints the
   case-D message above. The auth (401/403) and `NoMarkerError` branches
   land alongside it per Decision 3's ordering.
3. No changes to the bootstrap flow itself. Bootstrap fires only on
   `*config.NoMarkerError`, which means the remote returned a tarball and
   the probe ran. Zero-commit repos never trigger bootstrap in v1; the
   user retries after their first commit.

**Rationale**

Option C delivers the user-visible win (a 404 stops being a generic dead
end and points at the most likely fix) at near-zero cost. The typed-error
refactor is already on the critical path for Decision 3, so the only net
new code is the message string and a `case 404` arm in the classifier.

Option B's extra `repos/get` call would buy the ability to scaffold
against a truly empty remote without requiring the user to push a first
commit. That sounds attractive but the cost-benefit is poor for v1:

- **The remediation is trivial.** Pushing an empty README is a 30-second
  one-liner the user already has muscle memory for. Compared to the
  implementation cost of a new bootstrap subpath that has no clone tree
  to work from, the user-side workaround is cheap.
- **The disambiguation isn't airtight.** `repos/get` still returns 404
  for private repos when the caller lacks credentials. Option B
  reintroduces the C/D collapse that the explicit `--bootstrap` flag
  (Decision 1) was designed to sidestep — except now it does it via a
  silent API probe rather than a flag, which is worse for auditability.
- **Worktree handoff doesn't fit a zero-commit remote.** Decisions 1 and
  4 wire the bootstrap path through `workspace.StageInWorktree`, which
  needs a checked-out tree to add a worktree against. A zero-commit
  remote has no tree; option B would need a separate "scaffold without
  clone, then `git init` locally, push" subpath. That's a different code
  path from the `NoMarkerError` bootstrap, doubling the surface area.
- **Non-GitHub remotes don't benefit.** B is GitHub-specific. Users on
  `file://` fixtures, GitLab, or Gitea get raw `git clone` stderr today
  and nothing about B changes that. C's improved hint generalizes
  because the classifier can recognize git-clone-style "remote not found"
  stderr later in v1.1+ without touching the bootstrap flow.
- **Rate limit and auth posture.** B requires `repos/get` to behave the
  same as `FetchTarball` for auth (token presence, scope), which adds a
  second place where token misconfigurations need consistent messaging.
  A wins by not adding that surface.

Option A (no special hint, just the existing 404 message) was rejected
because it leaves the user without any pointer at the most-common
remediation. The `dangazineu/commuter` case the user described is
specifically the "I created the repo just now, then ran niwa" flow — the
zero-commit failure mode is exactly when the user is fastest moving and
benefits most from a targeted hint. The cost delta between A and C is
one string and one arm in a classifier that's already being added.

**Alternatives Considered**

- **A — No special-case detection, generic 404 error.** Rejected
  because it ships the worst user experience for the scenario the user
  flagged ("I just created the repo"). The implementation savings vs. C
  are negligible (one message string) once the typed-error refactor
  required by Decision 3 lands.

- **B — Extra `repos/get` API call to confirm empty repo, then scaffold
  against an empty tree.** Rejected for v1 because (1) the disambiguation
  is still incomplete against private repos, (2) the implementation
  requires a new no-clone bootstrap subpath that doesn't share code with
  the `NoMarkerError` worktree-handoff flow, (3) the user-side workaround
  ("push an empty README") is trivial, and (4) the benefit doesn't
  generalize to non-GitHub transports. Worth revisiting in v2 if real
  users report hitting zero-commit-repo friction.

**Consequences**

What changes:

- `internal/github/fetch.go` ships a new exported `*StatusError` type
  (carries `StatusCode` and a message). The existing 401/403 message
  ("verify GH_TOKEN scopes...") becomes a `StatusError` with the same
  text. This is the same refactor Decision 3 needs.
- `internal/cli/init.go` gains a typed classifier (an `errors.As` switch)
  around the existing `materializing config repo: %w` wrap. The 404 arm
  prints the case-D message above; the 401/403 arm prints the case-C
  message; the `NoMarkerError` arm flows into the Decision 1 bootstrap
  branch. The generic wrap stays as the fall-through.

What becomes easier:

- Adjacent failure-mode handling (Decision 3) lands on the same machinery.
  Adding new sentinel cases — case A (malformed `workspace.toml`), case C
  (auth), case D (not found) — is uniform.
- The user has a clear "push a first commit, retry" path for the
  zero-commit case without needing niwa to grow a no-clone scaffold mode.

What becomes harder:

- v1 leaves a small UX cliff for users who really do create zero-commit
  repos and don't read the error message carefully. If we see this in
  practice we revisit with option B (or a variant that lets bootstrap
  also `git init` an empty tree locally) in v2.
- The classifier needs to keep the auth-before-404 ordering documented
  in `lead-other-failures.md`, because a public-API call against a
  private repo from an unauthenticated client returns 404 but the
  auth-failure framing is more helpful when a token is present and
  lacks scope. This is a documented test-coverage requirement, not a
  behavioural risk.

Non-goals (deferred):

- Disambiguating zero-commit-empty from missing-repo via `repos/get`.
- Scaffolding against a truly empty remote with no clone tree.
- Non-GitHub transport disambiguation beyond the existing raw stderr.
<!-- decision:end -->
