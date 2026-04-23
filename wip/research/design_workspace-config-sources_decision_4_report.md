<!-- decision:start id="test-infrastructure-architecture" status="assumed" -->
### Decision: Test Infrastructure Architecture (`tarballFakeServer`, fault-injection seam, state-file factory)

**Context**

The PRD's Test Strategy section commits to building a `tarballFakeServer`,
a tarball-extraction fault-injection seam, and a state-file factory step
as in-scope deliverables. These are pre-conditions for verifying every
GitHub-path acceptance criterion (AC-G1..AC-G7), atomic-refresh ACs
(AC-M3, AC-M4), state-schema migration ACs (AC-X3, AC-X4), and the
working-tree conversion ACs (AC-Y1..AC-Y5). The three sub-questions are
coupled: the fault-injection seam intercepts work the `tarballFakeServer`
performs end-to-end, and the state-file factory ships in the same
`test/functional/` package and shares the same Gherkin-step style.

niwa already ships `localGitServer` (`test/functional/localrepo_test.go`),
a goroutine-free helper that materializes bare repos under a per-scenario
sandbox root and exposes them via `file://` URLs. Steps live in
`steps_test.go` and are registered in `suite_test.go`'s
`initializeScenario`. Per-scenario state is held on a `testState` struct
threaded through `context.Context`. The new fakes must mirror this
pattern so contributors recognize the shape immediately.

The workspace forbids system dependencies (no docker, no system `tar`),
forbids third-party HTTP libraries beyond niwa's existing deps, and
expects production-code purity: test-only surface in `internal/` should
be minimized and the design should pick exactly one mechanism for
fault injection rather than mixing several.

**Assumptions**

- The `httptest.Server` from Go's standard library satisfies all
  required `tarballFakeServer` capabilities (configurable status
  codes, ETag/`If-None-Match` handling, redirect responses, request
  logging, path-pattern dispatch). This is verified by inspection of
  `net/http/httptest` and `net/http`'s `ServeMux`.
- `httptest.Server` with `Listener.Addr()` exposes a base URL the
  production code can be pointed at via the GitHub API base-URL field
  already present on `internal/github.APIClient` (`BaseURL` field is
  public and overridable). Any new GitHub tarball client (Decision 5)
  will follow the same pattern.
- Tests can supply the base URL to niwa via the standard godog env-
  override mechanism (`s.envOverrides`) without requiring production
  code to read a new env var beyond what Decision 5 already specifies
  for normal API base-URL configuration. If Decision 5 makes the base
  URL configurable via `NIWA_GITHUB_API_URL` (or similar), no
  test-only env var is needed for the happy-path fake.
- The fault-injection seam need only fire in code paths reached by
  the test process; it does not need to inject faults into a
  subprocess. Since functional tests invoke a compiled binary,
  faults are triggered through the binary's environment, not through
  in-process Go hooks.
- Build tags are not desirable here because the functional test
  harness invokes the same binary that ships in releases; a separate
  `niwa_test_faults` binary would diverge from the artifact under
  test.

**Chosen: httptest-based `tarballFakeServer` + env-var fault-injection seam + Gherkin step that writes hand-crafted state files**

A. **`tarballFakeServer` shape**: an `httptest.Server`-based helper
   placed alongside `localGitServer` in `test/functional/` (file:
   `test/functional/tarballfake_test.go`). The helper wraps
   `httptest.NewServer` with a programmable handler. Its public
   surface mirrors `localGitServer`'s style:

   ```go
   type tarballFakeServer struct {
       srv      *httptest.Server
       mu       sync.Mutex
       tarballs map[string][]byte    // "owner/repo@ref" -> tar.gz bytes
       commits  map[string]string    // "owner/repo@ref" -> 40-char oid
       etags    map[string]string    // "owner/repo@ref" -> etag
       redirects map[string]string   // "oldorg/oldrepo" -> "neworg/newrepo"
       requests []recordedRequest    // every request, in order
       statusOverride map[string]int // path-prefix -> status code
   }

   func newTarballFakeServer() *tarballFakeServer
   func (s *tarballFakeServer) URL() string
   func (s *tarballFakeServer) Close()
   func (s *tarballFakeServer) Tarball(slug, ref, commitOID string, files map[string][]byte)
   func (s *tarballFakeServer) Commit(slug, ref, commitOID string)
   func (s *tarballFakeServer) ETag(slug, ref, etag string)
   func (s *tarballFakeServer) Redirect(oldSlug, newSlug string)
   func (s *tarballFakeServer) StatusOverride(pathPrefix string, status int)
   func (s *tarballFakeServer) DropNextRequest(pathPrefix string)
   func (s *tarballFakeServer) Requests() []recordedRequest  // for "no request was made" assertions
   ```

   The handler dispatches on URL path:
   - `GET /repos/{owner}/{repo}/commits/{ref}` with
     `Accept: application/vnd.github.sha` returns the configured 40-byte
     commit OID body.
   - `GET /repos/{owner}/{repo}/tarball/{ref}` returns the configured
     gzipped tar bytes; honors `If-None-Match` (returns 304 when the
     header matches the configured ETag); honors redirect map
     (returns 301 with `Location` rewritten); honors status overrides
     (e.g., 401, 404).
   - All requests are appended to the `requests` log under the mutex
     so steps can assert "no tarball request was made" or "request N
     carried Authorization: Bearer test-token".

   Lifecycle is per-scenario: the Before hook in `suite_test.go`
   constructs a `tarballFakeServer`, stores it on `testState.tarballSrv`,
   sets `NIWA_GITHUB_API_URL=<srv.URL()>` (or whatever name Decision 5
   assigns to the API base-URL knob) on `s.envOverrides`, and the After
   hook calls `srv.Close()`.

B. **Fault-injection seam**: a single env-var contract
   `NIWA_TEST_FAULT=<mode>:<arg>` read once during fetch initialization
   in the production tarball client (one new function call:
   `testfault.FromEnv()` returning a small `Faulter` struct). The
   production code surface is one package (`internal/testfault/`),
   one exported function (`FromEnv() *Faulter`), and one method
   (`f.Wrap(io.ReadCloser) io.ReadCloser`). When the env var is
   unset, `FromEnv()` returns nil and `Wrap` is a no-op pass-through.

   Supported modes (parsed by `internal/testfault/`):
   - `truncate-after:N` — wraps the tarball-response body so that
     after N bytes are read, the next read returns `io.ErrUnexpectedEOF`
     and the underlying connection is closed. Verifies AC-M4.
   - `delay:Nms` — injects a `time.Sleep` before the first read.
     Useful for AC-R4-style timeout scenarios.
   - `disk-full` — causes the extraction `os.WriteFile`/`io.Copy`
     wrapper to return `syscall.ENOSPC` after the first file write.
     This requires a parallel one-line hook in the
     extraction code path (`testfault.WrapWriter`).
   - `permission-denied:<path-glob>` — causes the wrapped writer
     to return `os.ErrPermission` for paths matching the glob.
   - `dns-unreachable` — implemented purely as a test-side trick:
     the test points niwa at `127.0.0.1:1` (a closed port on
     loopback) using the same env var the happy-path fake uses
     (`NIWA_GITHUB_API_URL=http://127.0.0.1:1`). No production code
     change is needed. The Gherkin step
     `the github api is unreachable` sets this override.

   Rejected alternatives: build tags (forks the shipped binary),
   public test hooks (test-coupled package-level vars in production
   code), and full dependency injection (changes production
   structure to accommodate testing). Env vars keep the production
   surface minimal (one helper package, exclusively read at known
   seam points) and discoverable from outside the test process.

   Gherkin steps registered in `steps_test.go`:
   ```
   the next tarball fetch is truncated after (\d+) bytes
   the next tarball fetch is delayed by (\d+)ms
   tarball extraction will fail with disk-full
   tarball extraction will fail with permission-denied for "([^"]*)"
   the github api is unreachable
   ```
   Each step writes to `s.envOverrides["NIWA_TEST_FAULT"]` (or
   `NIWA_GITHUB_API_URL` for the unreachable case) before the next
   `niwa apply` invocation.

C. **State-file factory step**: a Gherkin step that hand-writes
   v1, v2, or v3 `InstanceState` JSON files into the per-scenario
   workspace, parameterized by schema version and arbitrary field
   overrides. Implementation in `steps_test.go`:

   ```
   an instance "([^"]*)" of workspace "([^"]*)" has a v(\d+) state file with body:
   ```

   The step accepts a docstring containing the JSON body to write
   verbatim under `<workspaceRoot>/<instance>/.niwa/instance.json`
   (or wherever Decision 2 places the state file). Tests supply the
   exact JSON to exercise corner cases (missing `config_source`,
   `schema_version: 99`, mixed v2 and v3 fields). For the common
   "valid v2 state file" case, a second step provides defaults:

   ```
   an instance "([^"]*)" of workspace "([^"]*)" has a default v(\d+) state file
   ```

   This step calls a small `defaultStateFile(version int) []byte`
   factory function in `test/functional/statefactory_test.go` that
   returns a canonical state file at the requested version. The
   factory function exists in Go (not as fixture files on disk)
   because: (1) state-file shape is a moving target during the
   migration work itself; (2) keeping the canonical shape in code
   means a schema bump invalidates one function rather than N
   fixture files; (3) Gherkin scenarios that need byte-exact
   variants use the docstring form instead.

   Rejected alternatives: pre-baked fixture directories
   (`test/functional/fixtures/state/v2/...`) coupled to the schema
   shape; would proliferate as schema evolves. Pure Go factory
   without a Gherkin step (Gherkin tests can't author state without
   bouncing through Go code, which violates the
   "step-defined preconditions" convention).

**Rationale**

Three properties of niwa's existing test infrastructure dictated the
choice:

1. **Reuse of `localGitServer`'s pattern.** The chosen
   `tarballFakeServer` is a goroutine-managing helper struct with
   lifecycle methods, exactly matching `localGitServer`'s style.
   `httptest.Server` is the standard library's idiomatic way to
   build such a helper; rejecting it in favor of a hand-rolled
   `net.Listen` + `http.Serve` goroutine adds ceremony with no
   capability gain.

2. **Production-code purity.** The env-var fault-injection seam
   limits test-only surface in `internal/` to a single new package
   (`internal/testfault/`) exposing one function. No build tags
   means the test binary is byte-identical to the release binary
   (modulo go-test wrapper). No DI rework means production code
   structure stays oriented around production needs.

3. **Step-vocabulary discoverability.** Steps follow the existing
   verb-object pattern (`a config repo "X" exists with body:`,
   `the next tarball fetch is truncated after N bytes`). Each fault
   mode is one self-explanatory step. The state-file factory
   handles the common case via a default and the byte-exact case
   via a docstring, mirroring how `aWorkspaceExistsWithBody` works
   today.

The choice also satisfies the constraint that test infrastructure
not need its own meta-tests: `httptest.Server` is stdlib-tested,
the env-var parser is small enough that one Gherkin scenario per
fault mode demonstrates correctness, and the state-file factory
is a thin JSON marshaller.

**Alternatives Considered**

- **A2 — Standalone goroutine HTTP server**: rejected because it
  duplicates what `httptest.Server` already provides
  (auto-port-selection, graceful shutdown, TLS toggle, `Listener.Addr()`).
  No required capability is missing from `httptest.Server`.
- **A3 — Extension of `localGitServer`**: rejected because it
  conflates two protocols (`file://` for git, HTTP for tarball).
  Tests that use only one of the two pay setup cost for both, and
  the helper struct's surface bloats.
- **A4 — Test container running a real GitHub-API-shaped server**:
  rejected per the workspace's no-system-deps invariant. Docker is
  not available on the standard contributor toolchain that niwa
  targets.
- **B1 (rejected within the chosen approach) — alternative env-var
  shapes**: scoped per-call vars like `NIWA_TEST_FAULT_TARBALL=...`
  vs. one-shot vars consumed and cleared. Chose a single
  `NIWA_TEST_FAULT` var read once at fetch entry because the
  scenario sandbox is per-scenario; tests never run two faults
  concurrently in the same process.
- **B2 — Build tag (`//go:build niwa_test_faults`)**: rejected
  because the functional test harness invokes the same binary
  shipped to users. A test-tag-only binary would diverge from the
  artifact under test, defeating the purpose of end-to-end testing.
- **B3 — Public test hook (package-level `testFaultHook` var)**:
  rejected because functional tests invoke a separate process; the
  test process can't write to the binary process's package-level
  vars. Even for unit tests this introduces test-only surface in
  production packages with no compensating benefit over the env-var
  approach.
- **B4 — Dependency injection (Fetcher interface)**: rejected
  because it changes production structure for test concerns and
  doesn't help functional tests at all (the binary boundary is
  what tests cross). The interface refactor may still be valuable
  for unit testing the new tarball client (Decision 5), but that
  is a separate concern from the fault-injection seam.
- **C1 — Gherkin step `given_a_v2_state_file_exists` with hard-coded
  body**: chosen as the named "default" variant. The "with body:"
  docstring variant covers the byte-exact corner cases the named
  step can't.
- **C2 — Pure Go factory called from steps**: chosen as the
  implementation backing the default-variant step. The factory is
  not directly callable from Gherkin (steps must wrap it).
- **C3 — Pre-baked fixture directories**: rejected because state
  schema versions evolve during this very work; in-code factories
  refactor more cleanly than scattered fixture trees.

**Consequences**

What changes:

- New helper file `test/functional/tarballfake_test.go` (parallels
  `localrepo_test.go`).
- New helper file `test/functional/statefactory_test.go` (small,
  one default-shape function per supported schema version).
- New Gherkin steps registered in `suite_test.go`'s
  `initializeScenario`: one `tarball fake server is set up` step
  (Before-hook driven, parallel to `a local git server is set up`),
  one repo-state-config step group, one fault-injection step group,
  and one state-file step pair.
- New `internal/testfault/` package: ~50-line single-purpose package
  exporting `FromEnv() *Faulter`, `(*Faulter).WrapReader`,
  `(*Faulter).WrapWriter`. The new tarball client (Decision 5) and
  the extraction code call these wrappers at fetch and write seams.
- `testState` gains `tarballSrv *tarballFakeServer` and a default
  `envOverrides["NIWA_GITHUB_API_URL"]` set in the Before hook.

What becomes easier:

- AC-G1..AC-G7 verifiable by configuring the fake's response map
  before invoking `niwa apply`.
- AC-M3, AC-M4 (atomic refresh under truncated tarball) verifiable
  via the `truncate-after:N` fault.
- AC-X3, AC-X4 (state migration / forward-version rejection)
  verifiable by writing the exact state file body in the scenario
  Background.
- AC-R4 (offline default-branch resolution failure) verifiable by
  pointing niwa at an unreachable URL via the same env-var override
  mechanism the happy-path fake uses.
- Future contributors recognize the helper pattern from
  `localGitServer`; no new conventions to learn.

What becomes harder:

- The `internal/testfault/` package is test-only surface in the
  production module path. Reviewers unfamiliar with the design
  may flag it as "test code in production"; the package's doc
  comment must explain that this is the intentional minimal seam
  the design committed to (alternative seams would be worse).
- Adding a new fault mode requires editing `internal/testfault/`
  (small change) and registering a new Gherkin step (small
  change). The locality is good, but it's two places.
- The fault-injection seam can theoretically be triggered by a
  user setting `NIWA_TEST_FAULT` in their shell; this is acceptable
  given (a) it's clearly named and documented, (b) niwa is a
  workspace tool, not a security-critical service, (c) the
  failure modes it injects (truncate, delay, disk-full) are all
  conditions niwa already has to handle gracefully on real
  failures. No PII or auth bypass is exposed.

Operational notes:

- The Before hook should wire `NIWA_GITHUB_API_URL` and the
  tarball-fake's `URL()` only when a scenario opts in (e.g., via
  the `a tarball fake server is set up` step). Setting it
  unconditionally would mask scenarios that incorrectly assume
  the real GitHub API.
- Request log size is bounded by the per-scenario sandbox lifetime;
  no rotation needed.
- `httptest.Server` uses a random ephemeral port; tests must
  reference the URL via `srv.URL()`, not a hard-coded port.
<!-- decision:end -->
