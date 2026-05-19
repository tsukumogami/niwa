# Lead: How does today's `niwa init --from` fail when the source has no `.niwa/`?

## Findings

### Call graph from `--from` to a parsed `workspace.toml`

The clone path runs entirely inside the `modeClone` branch of
`runInit` at `internal/cli/init.go:239-284`. The relevant steps:

1. **Slug shape validation** — `sourcepkg.Parse(source)` at
   `internal/cli/init.go:244`. Wrapped as `parsing --from slug: %w`. Only
   fires on malformed slugs (e.g., empty owner, non-slug characters). A
   well-formed but empty `dangazineu/commuter` passes here.
2. **Clone URL resolution** — `workspace.ResolveCloneURL(source, …)` at
   `internal/cli/init.go:254` returns the https/ssh URL niwa would clone.
   Wrapped as `resolving clone URL: %w`. Does not touch the network. The
   "Initializing from: <url>" line at `init.go:259` is printed before any
   network call.
3. **`os.Mkdir(workspaceRoot)`** at `init.go:217` created
   `<cwd>/<name>/` *before* the clone runs. A defer at
   `init.go:221-225` removes that directory on any error path before
   `workspaceCreated = false` flips at `init.go:395`.
4. **Materialize** — `workspace.MaterializeFromSource(ctx, src, source,
   niwaDir, config.TeamConfigMarkerSet(), fetcher, reporter)` at
   `internal/cli/init.go:264`. All "remote exists but has no `.niwa/`"
   logic lives below this call. Wrapped as `materializing config repo:
   %w`.
5. **Post-flight parse** — `config.Load(<workspace>/.niwa/workspace.toml)`
   at `init.go:288`. Wrapped as `post-flight verification failed: %w`.
   This only runs after a *successful* materialize; an empty-source
   failure short-circuits at step 4 and never reaches it.

### Inside `MaterializeFromSource`

`MaterializeFromSource` (`internal/workspace/snapshotwriter.go:310`) is a
thin wrapper that defaults `sourceURL` to `src.String()` and delegates to
`materializeAndSwap` (`snapshotwriter.go:345`).

`materializeAndSwap` creates a *sibling* staging directory at
`<workspaceRoot>/.niwa.next` (`snapshotwriter.go:347`), then dispatches
on host:

- **GitHub source** (most common, includes the user's
  `dangazineu/commuter` case): calls `materializeFromGitHub`
  (`snapshotwriter.go:482`). That function issues
  `FetchClient.FetchTarball` against
  `GET /repos/{owner}/{repo}/tarball/HEAD`, buffers the gzipped tar in
  memory, and runs the probe pipeline.
- **Non-GitHub source** (e.g., a `file://` test fixture or a non-github
  host): calls `materializeFromFallback` (`snapshotwriter.go:562`), which
  shells out to `git clone --depth 1` into an `os.MkdirTemp` directory
  and then probes the on-disk tree.

Either way, the staging dir and (for the fallback path) the temp clone
dir are torn down on every error path inside `materializeAndSwap`
(`snapshotwriter.go:367, 372, 383, 416, 436, 441`) and via
`defer os.RemoveAll(tmp)` in `ProbeAndFetchSubpath`
(`fallback.go:286`). The temp clone for the non-GitHub fallback path is
in `os.TempDir()`, not under the workspace root, so it has no visibility
to the user.

### The exact failure point for "remote exists, has no `.niwa/`"

For both transports the failure is constructed in the same place:
`config.RankDecider` at `internal/config/discover.go:201`:

```go
return "", nil, &NoMarkerError{Markers: markers}
```

The user-facing message string is built in `NoMarkerError.Error()` at
`internal/config/discover.go:141-149`:

```
no niwa config found: probed .niwa/workspace.toml and workspace.toml at
source root. If the config lives elsewhere in the repo, pin an explicit
subpath via `--from <owner>/<repo>:<subpath>`.
```

The error reaches the decider only if the probe pass actually scanned
the source — i.e. the remote returned a tarball or `git clone`
succeeded but neither `.niwa/workspace.toml` nor root-level
`workspace.toml` was found inside it. Crucially, the probe is gated on a
type-`TypeReg` check (`internal/github/tar.go:388-398`,
`internal/workspace/fallback.go:343-348`) — an *empty* `.niwa/`
directory at the source root also produces `NoMarkerError`, not a
silent rank-1 promotion. There's a dedicated regression test for that
case: `TestMaterializeFromSource_GitHub_EmptyNiwaDirIsNotRank1`
(`snapshotwriter_probe_test.go:137-154`).

### Final user-facing message today

The error reaches the user through three layers of wrapping:

1. `RankDecider` returns `&NoMarkerError{...}` (raw message above).
2. `materializeAndSwap` returns it via the GitHub branch
   (`snapshotwriter.go:373`) **without re-wrapping** — `ghErr` flows
   verbatim. Note the non-GitHub branch at `snapshotwriter.go:384`
   **does** wrap it as `EnsureConfigSnapshot: %w`, which is a small
   asymmetry the user-facing message inherits.
3. `runInit` wraps with `materializing config repo: %w`
   (`init.go:266`).
4. cobra prints `Error: <wrapped>` to stderr.

So the exact stderr line a user with a tarball-reachable empty GitHub
repo sees today is:

```
Error: materializing config repo: no niwa config found: probed
.niwa/workspace.toml and workspace.toml at source root. If the config
lives elsewhere in the repo, pin an explicit subpath via
`--from <owner>/<repo>:<subpath>`.
```

The same probe failure on a `file://` or non-GitHub source would read:

```
Error: materializing config repo: EnsureConfigSnapshot: no niwa config
found: probed .niwa/workspace.toml and workspace.toml at source root.
If the config lives elsewhere in the repo, pin an explicit subpath via
`--from <owner>/<repo>:<subpath>`.
```

The "Initializing from: <cloneURL>" line at `init.go:259` is the only
thing on stdout before the error.

### Discriminating "empty repo" from other failures

`materializeAndSwap` distinguishes the failure modes via different error
types (when callers care to inspect — today no caller does):

| Failure mode | Error type / shape | Where constructed |
|---|---|---|
| Slug malformed (`org/`, empty, illegal chars) | `parse error from `source.Parse`` | `internal/source/parse.go:25` (slug grammar) |
| GitHub 404 (repo doesn't exist) | `fmt.Errorf("github: FetchTarball returned %d", 404)` | `internal/github/fetch.go:149` |
| GitHub 401/403 (auth denied or scope missing) | `fmt.Errorf("github: FetchTarball returned %d (verify GH_TOKEN scopes; ...)", 401/403)` | `internal/github/fetch.go:144-145` |
| Empty repo (no commits, GitHub returns 404 for HEAD) | Same 404 path as missing repo | `internal/github/fetch.go:149` |
| Transport failure (DNS, network) | `fmt.Errorf("github: FetchTarball request failed: %w", err)` | `internal/github/fetch.go:133` |
| Tarball gzip/tar malformed | `fmt.Errorf("probeAndExtractSubpath: ...", err)` | `internal/github/tar.go:277-292, 362-380` |
| Both `.niwa/workspace.toml` AND root `workspace.toml` present | `*config.AmbiguousMarkersError` | `internal/config/discover.go:181-183` |
| **Neither marker present (incl. empty `.niwa/`, brand-new repo with only README, etc.)** | **`*config.NoMarkerError`** | **`internal/config/discover.go:201`** |
| `git clone` failure on fallback path | `fmt.Errorf("fallback: git clone %s: %w\n%s", url, err, out)` | `internal/workspace/fallback.go:151` |
| Malformed `workspace.toml` after successful clone | TOML decode error from `config.Parse` at post-flight | `internal/config/config.go:272`, wrapped at `init.go:290` |

For the user's scenario (`dangazineu/commuter` exists, was just created,
maybe has a single `README.md` from GitHub's auto-init, no `.niwa/`),
the failure is the `*config.NoMarkerError` row.

The two predicates `config.IsNoMarker(err)` and
`config.IsAmbiguousMarkers(err)`
(`internal/config/discover.go:212-216, 204-210`) already exist for
callers that want to discriminate without touching the concrete type —
no caller in `runInit` uses them today, but the seams are there.

A *completely empty* GitHub repo (zero commits) responds 404 on the
tarball endpoint because there is no `HEAD` ref to materialize. That
case currently surfaces as `materializing config repo: github:
FetchTarball returned 404`, which is **indistinguishable from
"repository does not exist"** at the runInit error-handling layer.
Discriminating those two would require an extra API call (e.g.
`GET /repos/{owner}/{repo}` to confirm the repo exists, then inspect
`default_branch` / `size` to detect emptiness) — niwa does not do this
today.

### Side effects on the failure path

What gets created vs. cleaned up on an empty-`.niwa/` failure today:

| Artifact | Created when | Cleaned up by |
|---|---|---|
| `<cwd>/<name>/` workspace dir | `init.go:217` (before clone) | deferred `os.RemoveAll(workspaceRoot)` at `init.go:221-225`, disarmed only on success at `init.go:395` |
| `<cwd>/<name>/.niwa.next/` staging | `snapshotwriter.go:354` | `safeRemoveAll(staging)` on every error branch (`snapshotwriter.go:367, 372, 383, 416, 436, 441`) |
| `os.TempDir()/niwa-fallback-*/` temp clone (non-GitHub only) | `fallback.go:137` | `defer os.RemoveAll(tmp)` at `fallback.go:286` |
| Registry entry | `init.go:328` | Never created on this failure path — registry write happens AFTER post-flight succeeds |
| Instance state file | `init.go:342` | Never created on this failure path |
| Landing-path file (shell wrapper handoff) | `init.go:390` | Never written on this failure path |

So on an `NoMarkerError`, the user is left with **exactly the state
they started in**: cwd unchanged, no new directory on disk, no registry
entry. There's no partial clone tree for a fallback path to plug into —
by the time `runInit` returns, the staging is gone and the workspace
dir defer has already fired.

A fallback bootstrap path that wants to *use* the clone tree to scaffold
a config would need to intercept the error inside or before the
materialize swap-and-cleanup, because once `materializeFromGitHub`
returns the buffered tarball bytes have been freed and once
`ProbeAndFetchSubpath` returns the temp clone is gone via its deferred
`RemoveAll`.

### Adjacent error paths

Worth tagging for the design phase:

- **`AmbiguousMarkersError`** (`config/discover.go:118-131`): the source
  has BOTH `.niwa/workspace.toml` and root-level `workspace.toml`. The
  PRD calls this out as user-actionable ("remove one or pin a subpath")
  and the message is distinct enough that a scaffolding fallback
  shouldn't try to handle it.
- **GitHub 401/403** at `fetch.go:142-145`: includes a hint about
  `GH_TOKEN` scopes. Authentic auth failure, not a fallback candidate.
- **GitHub 404** at `fetch.go:147-149`: covers both "repo doesn't
  exist" and "empty repo" with the same message — see discussion above.
- **`fetch.go:133`** (transport error): network/DNS, also not a
  fallback candidate.
- **`fallback.go:149-151`** (git clone failed): same shape as a 404 for
  the fallback transport, but stderr includes git's full output so the
  text already discloses the underlying cause (e.g.,
  `Authentication failed`, `Could not find remote branch`).
- **Post-flight `config.Load` failure** (init.go:288-291): only fires
  after the clone *succeeded* and `.niwa/workspace.toml` is present but
  malformed. The wrap is `post-flight verification failed: %w` and the
  underlying error includes a `parsing config: ` prefix from
  `config.Parse`. This is structurally different from the empty-source
  case — the file exists but is broken — and would route to a different
  fallback (e.g., "open in editor and fix") if anything.

## Implications

The plug point for a scaffold-and-stage fallback is **inside `runInit`'s
`modeClone` block, around the `MaterializeFromSource` call at
`init.go:264-266`** — specifically, branching on `errors.As(err,
*config.NoMarkerError)` before the error wrapper runs. At that
checkpoint:

- The workspace dir `<cwd>/<name>/` exists (created at `init.go:217`).
- The `.niwa.next/` staging is gone (cleaned by the error path in
  `materializeAndSwap`).
- The `defer os.RemoveAll(workspaceRoot)` from `init.go:221-225` is
  still armed. A fallback that wants to keep the directory has to
  flip `workspaceCreated = false` (or skip the defer) explicitly.
- The clone URL is in the local `cloneURL` variable from
  `init.go:254` — usable for "open in editor" hints or as the push
  target.
- The source slug is in the local `source` variable — usable as the
  remote-name in the scaffolded config and as a push target.
- The GitHub fetch client (`fetcher`) is wired but has only seen one
  tarball request; the rest of the API surface (creating a branch,
  pushing files via the Contents API or git) is not currently used by
  init.

A staged-on-a-branch fallback needs the *clone tree* to inject
scaffolded files into and push from. That tree is no longer available
at this checkpoint — `materializeFromGitHub` worked off an in-memory
buffer and `ProbeAndFetchSubpath`'s on-disk tree is gone. Two options:

1. **Re-clone** the source into a worktree-session directory and
   scaffold there. Adds one round trip but isolates the new code from
   `materializeAndSwap`'s contracts.
2. **Refactor `materializeAndSwap`** to keep the tree around when
   the probe fails (or expose a probe-then-discard variant that
   surfaces the tree to the caller). Tighter integration, but couples
   the fallback to the snapshot-writer's internals.

The session/worktree mechanism the user wants the fallback to live
inside is a *peer* of `runInit`, not a callee — niwa's worktree
sessions are an apply-time construct. Wiring a worktree session into
init is itself a small new seam.

## Surprises

- **Two error messages for the same probe failure.** The GitHub branch
  at `snapshotwriter.go:373` returns `ghErr` verbatim (already
  prefix-free `NoMarkerError`); the non-GitHub branch at
  `snapshotwriter.go:384` wraps it as `EnsureConfigSnapshot: %w`.
  Users on `file://` test fixtures see an extra layer of wrapping that
  GitHub users don't. Probably a small bug, but a fallback path needs
  to match against the underlying `*NoMarkerError` (via `errors.As`)
  rather than string-matching the prefix.
- **Empty GitHub repos surface as 404, not as `NoMarkerError`.** A
  brand-new GitHub repo with zero commits responds 404 on
  `/repos/.../tarball/HEAD` because there's no ref to resolve, which
  bottoms out at `internal/github/fetch.go:149` as
  `github: FetchTarball returned 404` — same message as a nonexistent
  repo. The user's described scenario assumes the repo has at least
  one commit (the GitHub "auto-init with README" toggle, or a manual
  first push). If they hit the truly-empty case the fallback wouldn't
  trigger today because the failure is upstream of the probe.
- **The workspace dir cleanup is aggressive.** `init.go:217` uses
  `os.Mkdir` (not `MkdirAll`) and the defer removes the entire
  workspace root on any failure path. A fallback that creates an
  on-disk session in `<workspace>/.niwa.session/` (or similar) and
  *expects the user to inspect it* has to disarm the defer before
  returning, or rewrite the cleanup contract to preserve session
  state. There's no precedent in `runInit` for "fail with side
  effects."
- **The probe gracefully handles "empty `.niwa/` exists" by collapsing
  it into `NoMarkerError`** — same error as "no `.niwa/` at all." A
  fallback can't distinguish "completely empty repo with a README" from
  "repo that already has an empty `.niwa/` dir" without re-fetching
  and re-probing. Both should route to the same scaffold-and-stage
  flow anyway, so this isn't load-bearing.
- **Tarball-mode probe is purely in-memory.** Even for the GitHub
  path, niwa never writes the source tree to disk except for the files
  under the resolved subpath. A fallback that wants to scaffold "next
  to" the existing repo content needs either a separate clone or a
  re-fetch.

## Open Questions

- **Do we want the fallback to fire on `AmbiguousMarkersError` too?**
  It's a different kind of "I can't proceed automatically" but the
  user's repo could plausibly recover via a scaffold that picks one
  rank. Probably out of scope for the immediate lead; flagging for
  the design phase.
- **Is the empty-repo 404 worth disambiguating?** Today both "repo
  doesn't exist" and "repo has no commits" return the same 404 with
  identical message text. If the fallback should also handle the
  empty-commits case (which seems like the user's actual scenario for
  a freshly-created repo, modulo the auto-init toggle), niwa needs
  one extra API call to distinguish them.
- **How does a session-based fallback interact with the `os.Mkdir`
  defer cleanup?** Open design question: should a fallback that keeps
  state on disk disarm the cleanup eagerly, or should it move its
  state into a session directory outside the workspace root and let
  the cleanup fire normally? The latter is cleaner, but breaks the
  "user can just `cd` into their new workspace" expectation.
- **GitHub's Contents API write surface isn't wired today.** The
  current `FetchClient` interface is read-only
  (`HeadCommit` + `FetchTarball`). A scaffold-and-push fallback either
  shells out to `git` (matching the non-GitHub fallback) or needs a
  new client method. The latter is more native but adds API surface
  for one feature.

## Summary

Today `niwa init <name> --from <empty-remote>` flows through
`runInit` → `MaterializeFromSource` → `materializeAndSwap`, where the
GitHub tarball (or `git clone` fallback) succeeds but the in-memory
probe at `internal/config/discover.go:201` returns a `*NoMarkerError`
that surfaces verbatim to the user as `Error: materializing config
repo: no niwa config found: probed .niwa/workspace.toml and
workspace.toml at source root. ...`. By the time the error reaches
`runInit`, every disk artifact has been cleaned up — the staging dir,
the fallback temp clone, and (via the deferred
`os.RemoveAll(workspaceRoot)`) the workspace directory itself — so a
fallback that wants to scaffold against the source tree has to either
re-clone or interpose itself before `materializeAndSwap`'s cleanup
fires. The natural plug point is `internal/cli/init.go:265`, branching
on `config.IsNoMarker(err)` before the wrapper runs and disarming the
workspace-dir cleanup defer.
