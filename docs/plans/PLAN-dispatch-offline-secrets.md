---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
upstream: docs/designs/DESIGN-dispatch-offline-secrets.md
milestone: "Provisioning keeps working when the vault login lapses"
issue_count: 7
---

# PLAN: Provisioning keeps working when the vault login lapses

## Status

Active

## Scope Summary

Bound and classify every Infisical call, keep a store of last-resolved values, serve it through a
decorator on the three provisioning bundles when the login lapses or the vault can't be reached,
and report every fallback on each provisioning surface. The work lands as one pull request.

## Decomposition Strategy

**Horizontal.** The design's components meet at stable, typed seams: the classification carried
on errors, the store's `Load`/`Update`, the decorator's `Wrap`, and the notice collector. The
design already sequences them so each unit builds and tests on its own. Golden fixtures of today's
behaviour come first, because the PRD's acceptance criteria compare against them and they have to
be recorded before anything changes. The repository declares no delivery preference, so the
default (consolidated) applies and no split branch fires: one pull request, with the units as its
commit order.

## Issue Outlines

### Issue 1: test(vault): record golden fixtures of today's vault failure handling

**Complexity**: testable

**Goal**: Record, from the code as it is before any behaviour change, the error text and key-report output for every stub scenario the PRD's acceptance criteria compare against, as committed golden fixtures with a passing characterization test.

**Acceptance Criteria**:

Fixtures and characterization test

- [ ] A characterization test in `internal/vault/resolve` (for example `golden_test.go`) drives the real Infisical provider through `resolve.ResolveWorkspace` and `resolve.ResolveGlobalOverride`, with a counting stub `commander` injected via the `_commander` provider config, and for each scenario below writes or compares a fixture file under `internal/vault/resolve/testdata/golden/`, one file per scenario, holding the resulting error text (or `<nil>`) and the `keyreport.RenderText` output.
- [ ] Fixtures are regenerated only with an explicit `-update` flag; a plain `go test ./internal/vault/resolve/...` compares and fails on any difference. Nondeterministic fragments (temp paths, times) are normalised before writing, and the test fails if a fixture contains any of the scenario's secret marker values.
- [ ] Scenarios recorded, each on a non-required key with strict mode off unless stated:
  - [ ] export exits 1 with each of the three logged-out wordings in turn ("No valid login session found ... Please run [infisical login] manually", "we couldn't find your logged in details", "Your login session has expired"): today's hard `infisical: export exited 1: ...` error;
  - [ ] export failing "Response Code: 401", and separately "Response Code: 403": today's tolerated mark;
  - [ ] export failing "Response Code: 404", and separately "Response Code: 500": today's error;
  - [ ] export stderr containing both "No valid login session found" and "Response Code: 404": today's error;
  - [ ] export failing with a connection-refused message and no `Response Code:` line: today's error;
  - [ ] the commander reporting a start failure (binary missing): today's "client not installed" handling;
  - [ ] an export that succeeds but lacks a requested key: today's missing-key mark;
  - [ ] a required key, and separately strict mode on, with the provider unreachable (the 403 stub): today's required-key and strict-mode failure text;
  - [ ] the same 403 and logged-out scenarios for a key in the personal global override layer, so later layer coverage has a baseline.
- [ ] The universal-auth failure is recorded: a test against an `httptest` server returning a non-200 response to `authenticateHTTP` pins today's `infisical auth: universal-auth login returned HTTP <n>: ...` text as a fixture under `internal/vault/infisical/testdata/golden/`.
- [ ] A functional fixture pins today's user-visible outcome for the first classification criterion's stub: the functional `infisical` stub (`writeFakeInfisical` in `test/functional/steps_test.go`) gains only an opt-in failure knob (an environment variable selecting a fixed stderr text and exit 1 for `export`), and a scenario running `niwa apply` under the "No valid login session found" stub asserts it exits non-zero with stderr containing the recorded `export exited 1` text. This is the companion test the PRD's first acceptance criterion names. <<ISSUE:3>> converts this scenario's assertion to the new expectation when it changes the logged-out outcome, and <<ISSUE:7>> owns the fallback half of that criterion end to end.
- [ ] The functional stub also appends one line per invocation (subcommand plus argv, never stdin or values) to a log file named by an opt-in environment variable, so tests can count export and `login status` calls.

PRD acceptance criteria owned by this unit

- [ ] R26: with the counting stub, a successful provisioning run's export invocation count is recorded as a baseline fixture from today's code, and a test asserts a successful run makes exactly that many export invocations and no `login status` invocation. The test is green now and must stay green through every later unit. (R26)
- [ ] The Acceptance Criteria preamble's condition is met: the golden fixtures land in a commit before any commit that changes vault behaviour.

General

- [ ] No production (non-test) Go code changes in this unit.
- [ ] Tests never run the operator's real `infisical` binary or touch the real `HOME` or `XDG_STATE_HOME`. (supports R27)
- [ ] `go test ./...` and `go vet ./...` pass.

**Dependencies**: None
### Issue 2: feat(infisical): bound vault subprocesses and the universal-auth login

**Complexity**: critical

**Goal**: Every `infisical export` and probe runs with a deadline, stdin on the null device, a new session and a process-group kill on timeout, and niwa's universal-auth login request has a 30 s bound, with a clamped test-only override.

**Acceptance Criteria**:

Bounds and override (`internal/vault/infisical/bounds.go`, new):

- [ ] Constants `exportTimeout` (30 s), `probeTimeout` (15 s) and `loginTimeout` (30 s) exist and are read only through one `callBound` helper.
- [ ] `callBound` honours `NIWA_TEST_VAULT_TIMEOUT` only when it parses as a Go duration of at least 50 ms and shorter than the default. A table test covers unset, empty, unparseable, `10ms`, exactly `50ms`, a valid shorter value, a value equal to the default and a longer value (`5m`); only the valid shorter values take effect, so a stray setting can shorten a bound but never lengthen one.
- [ ] When the override is in effect, niwa prints one warning to stderr per run (guarded so repeated calls don't repeat it), and the warning never includes secret material.

Process hygiene (`defaultCommander.Run` in `subprocess.go`):

- [ ] `cmd.Stdin` is set explicitly to the null device (`os.DevNull`); `cmd.Env` stays nil and stdout/stderr stay captured in buffers.
- [ ] `cmd.SysProcAttr` is `&syscall.SysProcAttr{Setsid: true}`, in an untagged file (no build tags), matching the existing use in `dispatch_launcher.go`; the package builds on Linux and macOS.
- [ ] `cmd.Cancel` sends SIGKILL to the whole process group (negative PID), and `cmd.WaitDelay` is 3 s.
- [ ] After `Run` returns, one more group SIGKILL is sent, ignoring "no such process", only when the context's deadline fired or `Run` returned `exec.ErrWaitDelay`. A test pins that a normal (non-timeout) run sends no group signal.
- [ ] The existing start-failure path (binary missing) still returns exit code -1 with a non-nil error, unchanged.

Call-site deadlines and timeout detection:

- [ ] `runInfisicalExport` derives `context.WithTimeout(ctx, callBound(exportTimeout))` from the caller's context and passes it to `Run`.
- [ ] Immediately after `Run` returns, `runInfisicalExport` checks whether its own deadline fired (`ctx.Err() == context.DeadlineExceeded` on the derived context) or `err` is `exec.ErrWaitDelay`, before the existing "client not installed" and exit-code branches. A timed-out export returns an error that wraps `vault.ErrProviderUnreachable`, names the export and the timeout, and is never reported as "client not installed" or as a generic `export exited -1` hard error (fixes the latent bug).
- [ ] A caller's own cancellation (parent context cancelled, not our deadline) is not reported as a timeout.
- [ ] The timeout check is a single, clearly named step (for example a helper returning whether the call timed out) that <<ISSUE:3>> can hand to the classifier as *unreachable*, reason "timed out" (`ClassUnreachable` / `ReasonTimedOut`), without restructuring the call site.
- [ ] Non-timeout paths (`exit 0`, auth-marker exit, other non-zero exit, malformed JSON, binary missing) produce byte-for-byte the error text recorded in <<ISSUE:1>>'s golden fixtures; the characterization test from <<ISSUE:1>> still passes unmodified.
- [ ] `authenticateHTTP` derives its request context from `context.WithTimeout(ctx, callBound(loginTimeout))`. On reaching the bound it returns `infisical: universal-auth login to <api-url> timed out after 30s` (the actual bound in the text), wrapped with `secret.Errorf`, so it follows today's login-error path. The shared `HTTPClient` gets no `Timeout`, so interactive onboarding calls stay unbounded.
- [ ] Other `defaultCommander` users (`DetectSessionStatus` in `session.go`, `folders.go`) get the hygiene changes but no new bound in this unit; their existing tests pass unchanged.

PRD criteria this unit owns:

- [ ] **R8:** A universal-auth login request to a server that accepts the connection and never responds fails within 35 seconds with an error naming the universal-auth login and the timeout, and provisioning stops as it does today for any other login error. Covered by a unit test against an `httptest` server that never responds, run with the real 30 s bound (marked slow), plus a fast variant using the override, plus a test that the error reaches the same `Authenticate` caller path as an HTTP 500 login failure.

Unit-level evidence for R7, R23 and R27 (their end-to-end PRD criteria depend on classification and fallback and are owned by later units; this unit delivers the mechanism and its direct tests):

- [ ] R7 mechanism: with stub scripts driven through the real `defaultCommander` under a shortened override, an export stub that never exits, and one that forks a child which keeps stdout open, each return within the bound plus 5 s, and the forked child's PID no longer exists after the call. One test runs the real 30 s export bound against the hanging stub and asserts return within 35 s (marked slow and skipped under `-short`).
- [ ] R23 mechanism: a test that allocates a pseudo-terminal, runs a stub through `defaultCommander.Run`, and has the stub record its stdin device (the null device) and fail to open `/dev/tty`.
- [ ] R27 (bounds half): no test in this unit runs the operator's real `infisical` binary; every subprocess test uses a stub on a test-controlled `PATH` or an absolute stub path.
- [ ] Existing fakes of `commander` compile unchanged; a fake that simulates a hang blocks on `<-ctx.Done()`.
- [ ] `go test ./...` and `go vet ./...` pass.

**Dependencies**: <<ISSUE:1>>
### Issue 3: feat(vault): classify Infisical failures and carry the class on errors

**Complexity**: critical

**Goal**: Make the Infisical backend classify every failed export as unauthenticated, unreachable or answered, return that class on a `ClassifiedError` whose text is byte-for-byte today's, short-circuit repeat calls through a run-scoped state, and expose each provider's store identity.

**Acceptance Criteria**:

#### Types and plumbing in `internal/vault`

- [ ] `errors.go` adds `FailureKind` with the constants `ClassUnauthenticated`, `ClassUnreachable` and `ClassAnswered`, `Reason` with the constants `ReasonLoggedOut`, `ReasonTimedOut` and `ReasonUnreachable` (the prefixes keep the unreachable class and the unreachable reason from colliding, since Go constants share one package scope), `FailureClass{Class, Reason, HTTPStatus}` with an `Error()` method, and `ClassifiedError{Err error; Class FailureClass}` whose `Error()` returns `Err.Error()` and whose `Unwrap() []error` returns `Err` and `&Class`. A unit test asserts `errors.Is(ce, ErrProviderUnreachable)` when `Err` wraps it, `errors.As(ce, &*FailureClass)` succeeds, and `ce.Error() == Err.Error()`.
- [ ] `provider.go` adds `Identity{Kind, APIDomain, ProjectID, Environment, FolderPath}` and `StoreIdentifier{ StoreIdentity(ref Ref) (Identity, bool) }`, plus one exported normalisation function. This unit is the sole owner of `vault.Identity` and that function: the Infisical provider's `StoreIdentity` calls it here, and <<ISSUE:4>>'s store hashes identities through the same function rather than a copy. Normalisation: the API domain becomes lower-cased scheme and host with no path, userinfo or trailing slash, and the folder path gets a leading `/` and loses any trailing `/`. Table tests pin `/a/b`, `a/b`, `/a/b/` to `/a/b`, and `https://App.Infisical.com/api` and `https://app.infisical.com` to `https://app.infisical.com`.
- [ ] `registry.go` adds `(*Bundle).Wrap(wrap func(Provider) Provider) *Bundle`, which returns a new bundle whose every provider (named and default) is `wrap(p)`, leaves the receiver unchanged, and keeps `Names`, `HasNamedProviders` and `CloseAll` behaving as on the original (closing the underlying providers exactly once).
- [ ] `runstate.go` adds `WithRunState(ctx)`, a nil-safe `RunStateFrom(ctx)`, `MarkUnreachable(domain, reason)`, `MarkUnauthenticated(domain)` and `Check(domain string, minted bool)`. A domain marked unreachable applies to every principal; one marked unauthenticated applies only when `minted` is false. A nil state answers "nothing recorded". The state is safe for concurrent use. Per PRD R20, the first reason recorded for a domain in a run wins: a later mark never replaces an earlier one's reason, so a `ReasonTimedOut` recorded after a `ReasonLoggedOut` does not override it, and `Check` for a CLI-session principal on a domain marked unauthenticated first and unreachable later answers `ClassUnauthenticated` / `ReasonLoggedOut`. A unit test pins that order.

#### Classification in `internal/vault/infisical`

- [ ] `session.go`'s `login status --json` decoder gains `tokenSource` and `verification.state` fields and still has no `token` field; `DetectSessionStatus` keeps its current results (its existing tests pass unchanged).
- [ ] `classify.go` runs the probe with the export's environment, bounded by <<ISSUE:2>>'s `probeTimeout` through `callBound`, reads session fields and never the probe's exit code, applies the six ordered rules, the minted-principal rules and the wording fallback above, and returns a `*vault.ClassifiedError`. Unauthenticated and unreachable results wrap `ErrProviderUnreachable`; every `ClassUnauthenticated` result carries `ReasonLoggedOut`, including one whose export or probe reached its bound; a `ClassUnreachable` result carries `ReasonTimedOut` when the export or the probe reached its bound and `ReasonUnreachable` otherwise. `ReasonTimedOut` never applies to an unauthenticated result (PRD R20). `HTTPStatus` is parsed only from a line beginning `Response Code: `; the same digits elsewhere in stderr are ignored.
- [ ] `runInfisicalExport` attaches the classification on every started-export failure. `ErrClientNotInstalled` never carries a class. For answered failures the inner error is exactly today's (sentinel choice and text): "401"/"403"/"forbidden" in `looksLikeAuthFailure` still choose `ErrProviderUnreachable` versus a plain error, but no longer decide the class. `TestLooksLikeAuthFailure` is updated deliberately, with a comment saying why.
- [ ] This unit converts <<ISSUE:1>>'s logged-out behaviour tests, since it is the first unit that changes that outcome: the characterization test's logged-out golden-case assertions (the three wordings, in the workspace and personal global override layers) and the functional `niwa apply` scenario under the "No valid login session found" stub, which expect today's hard `export exited 1` failure, are changed to assert the new expectation (a `ClassifiedError` with `ClassUnauthenticated` / `ReasonLoggedOut` whose `Error()` text equals the recorded fixture's error text, a tolerated mark for the non-required key, and `niwa apply` exiting 0). The fixture files are not regenerated or edited: they stay the record of the pre-change behaviour and remain referenced by a companion assertion that reads each logged-out fixture and checks it still holds today's hard `export exited 1` error, so the change in behaviour stays pinned. `go test ./...` and the functional suite pass after the conversion.
- [ ] A table test covers every rule and both principal types, and for each answered case compares `err.Error()` byte for byte against the <<ISSUE:1>> golden fixture for the same stub output.
- [ ] `Factory.Open` reads the provider config's `api_url` (then `NIWA_INFISICAL_API_URL`, then the cloud default) only to compute the identity's API domain; nothing new is passed to the CLI. The Infisical provider implements `StoreIdentifier`: kind `infisical`, that API domain, project ID, environment and effective folder path (the ref's folder, else the provider's configured folder, else `/`), normalised.
- [ ] Before starting an export subprocess the provider consults `vault.RunStateFrom(ctx).Check(domain, minted)`. On a recorded verdict it runs nothing and returns `infisical: export for <domain> skipped: an earlier call in this run <was logged out or expired | timed out | was unreachable>` in a `ClassifiedError` wrapping `ErrProviderUnreachable` with the recorded class and reason. Unreachable results call `MarkUnreachable`; unauthenticated results for CLI-session principals call `MarkUnauthenticated`. Answered failures are never recorded. niwa's universal-auth login is neither skipped by nor recorded in the run state.
- [ ] No message, error string or log line produced by this unit contains raw probe output or any value from a probe `token` field; a test feeds a probe JSON with a marker token and asserts the marker is absent from every returned error.

#### PRD acceptance criteria owned by this unit

These are made to pass at the classifier level here: each is a table case asserting the returned class, reason and error text against a stubbed `commander`. Cases are written as class / reason: a prose class (*unauthenticated*, *unreachable*, *answered*) stands for the matching `ClassUnauthenticated`, `ClassUnreachable` or `ClassAnswered` constant, and the backticked name is the `Reason` constant. The observable "falls back"/"serves nothing" half rides on <<ISSUE:5>>'s decorator, which serves only for unauthenticated or unreachable classes and passes every other error through untouched, and <<ISSUE:7>>'s functional scenarios re-drive them end to end.

- [ ] (R1 (umbrella of R2-R4), R2) A probe reporting a deciding session `expired`, and separately `rejected`, with the logged-out export, classifies unauthenticated / `ReasonLoggedOut`.
- [ ] (R2) A probe exiting 0 with `verification.state` `unknown` classifies unreachable / `ReasonUnreachable`. Probe stderr containing "token is malformed" classifies unauthenticated / `ReasonLoggedOut`. A probe whose only session is `authenticated`, `verified` on a domain other than the provider's API domain, with an export failing "Response Code: 403", classifies answered and returns today's tolerated-mark error. Two sessions, first `expired` then `authenticated`/`verified`, classify unauthenticated; reversed, with no server response, unreachable. A deciding session `pending` classifies unreachable / `ReasonUnreachable`.
- [ ] (R2) With `INFISICAL_TOKEN` set and the probe listing a stored-login session (`authenticated`, `verified`) first and the environment-token session (`rejected`) second, the class is unauthenticated / `ReasonLoggedOut`.
- [ ] (R2, R11) An export with "Response Code: 404" and a probe that reaches its bound, and separately a probe listing no sessions, classifies answered with today's error text. "Response Code: 401", and separately "Response Code: 403", with a probe that reaches its bound classify unauthenticated / `ReasonLoggedOut` with `HTTPStatus` 401/403. "Response Code: 403" with a deciding session `expired` classifies unauthenticated / `ReasonLoggedOut`. "Response Code: 404" with a deciding session `expired` classifies answered with today's error. (The "403 classified as a lapse leaves the store entry unchanged" clause follows from the class being unauthenticated, which <<ISSUE:5>>'s policy never evicts on.)
- [ ] (R2) With `INFISICAL_TOKEN` set and the probe reporting that token's session as `rejected`, the class is unauthenticated / `ReasonLoggedOut`.
- [ ] (R2) A verified probe with an export failure that has no `Response Code:` line (connection refused) classifies unreachable / `ReasonUnreachable`.
- [ ] (R2, R4, R20) A probe printing non-JSON, with export stderr containing each of "No valid login session found", "couldn't find your logged in details" and "Your login session has expired" in turn, classifies unauthenticated / `ReasonLoggedOut`. A probe that reaches its bound with export stderr "Your login session has expired" classifies unauthenticated / `ReasonLoggedOut`. A matching `authenticated`, `verified` probe with export stderr "No valid login session found" and no `Response Code:` line classifies unreachable / `ReasonUnreachable`. A non-JSON probe with stderr lacking any wording classifies unreachable / `ReasonUnreachable`; a probe reaching its bound with that export classifies unreachable / `ReasonTimedOut`.
- [ ] (R2, R4, R6) A verified probe with export stderr containing both "No valid login session found" and "Response Code: 404" classifies answered with today's error.
- [ ] (R6) A verified probe with "Response Code: 403" classifies answered and returns today's tolerated-mark error (wrapping `ErrProviderUnreachable`, as today). A verified probe with "Response Code: 500" classifies answered and returns today's hard error.
- [ ] (R0) With the `infisical` binary absent, the error is today's "client not installed" error, `errors.Is(err, ErrClientNotInstalled)` holds, `errors.As` finds no `FailureClass`, and no probe runs.
- [ ] (R3) With a minted principal, "Response Code: 401" classifies answered with today's handling and the probe is never invoked; a connection-refused export classifies unreachable / `ReasonUnreachable`, again with no probe invocation.
- [ ] (R5) A `CredentialPool.lookupVault` test in `internal/workspace` whose source provider returns the logged-out classified error takes the existing soft path and emits the existing credential-sync warning, with no change to `credentialpool.go`.
- [ ] (R9) With a stub commander recording invocations, the logged-out stub and three provider folders on one domain resolved under one run state: exactly one export and one probe invocation. With a hanging export (short test bound), a credential-sync lookup and two further folders on the same domain: exactly one export and at most one probe invocation, and every later call returns the skipped error with reason `ReasonTimedOut`; a minted-principal provider on that domain also skips its export, while its universal-auth login request is still made.
- [ ] (R26) A counting stub shows a successful export starts exactly the same number of export subprocesses as before this change and zero probe invocations.

#### Regressions

- [ ] `niwa status --check-vault` (`internal/cli/status_check_vault.go`) and onboard's `CheckProviderAuth` (`internal/workspace/doctor.go`) run with no run state attached and behave as before apart from the bounds and the classified error text for a lapsed login; tests pin both.
- [ ] `go test ./...` and `go vet ./...` pass on Linux and macOS.

#### Downstream deliverables

- [ ] Must deliver: `vault.FailureClass`, `vault.ClassifiedError`, `vault.Identity`, `vault.StoreIdentifier` implemented by the Infisical provider, `(*vault.Bundle).Wrap`, and the run state (`WithRunState`, `RunStateFrom`) that `runPipeline` attaches (required by <<ISSUE:5>>).
- [ ] Must deliver: `vault.Identity` and its single exported normalisation function in `internal/vault`, so the store's file-name hashing and `StoreIdentity` agree byte for byte (required by <<ISSUE:4>>, whose store takes `vault.Identity` and hashes its normalised form, and by <<ISSUE:5>>).

**Dependencies**: <<ISSUE:1>>, <<ISSUE:2>>
### Issue 4: feat(vault): add the store of last-resolved values

**Complexity**: critical

**Goal**: Add a new `internal/vault/store` package that keeps one hashed, versioned JSON file per provider identity under the XDG state directory, trusted only when the current user owns it exclusively, with lock-free reads, per-identity `flock`-serialised atomic updates that merge and evict per key, and a subprocess-free `.git` walk that refuses writes inside a work tree.

**Acceptance Criteria**:

#### API and format

- [ ] `internal/vault/store` exports `Dir() (string, error)`, `Load(dir string, id vault.Identity) (map[string]Entry, error)`, `Update(dir string, id vault.Identity, puts map[string]Entry, evictKeys []string, evictAll bool) error`, and `Entry{Value []byte; ResolvedAt time.Time; VersionToken string; Provenance string}`, matching the design's Key Interfaces. The package holds no package-level mutable state apart from unexported test hooks.
- [ ] `Update` reports why it didn't write through three exported sentinels, `ErrLockTimeout`, `ErrInWorkTree` and `ErrUnwritable`, which callers match with `errors.Is`. Every write failure wraps one of the three. A lock timeout wraps `ErrLockTimeout`, a `.git` finding wraps `ErrInWorkTree`, and any failed trust check, permission error, full disk or rename failure wraps `ErrUnwritable`. No error text contains a secret value.
- [ ] The store normalises every `vault.Identity` through <<ISSUE:3>>'s exported normalisation function before hashing or echoing it, and the package declares no identity type or normalisation code of its own. (The normalisation rules themselves are pinned by <<ISSUE:3>>'s table tests; the R12 cases below prove the store applies them.)
- [ ] The canonical identity is the five normalised fields encoded as a JSON array. Its lower-case hex SHA-256 names `<hex>.json` (data), `<hex>.lock` (lock, empty, never deleted) and transient `.<hex>.json.tmp-*` files.
- [ ] The data file is one JSON object with `format_version: 1`, an `identity` object echoing the normalised fields, and a `keys` map whose entries carry `value` (a byte slice, so base64 in JSON), `resolved_at` (RFC 3339, UTC), `version_token` and `provenance`. A round-trip test shows arbitrary bytes, including non-UTF-8 and empty values, and an empty version token come back exactly.

#### Location (R12)

- [ ] (R12) `Dir()` returns `$XDG_STATE_HOME/niwa/secret-cache` when `XDG_STATE_HOME` is set and absolute. With `XDG_STATE_HOME` unset, set to the empty string, and set to a relative path, it returns `$HOME/.local/state/niwa/secret-cache`. With the working directory and `XDG_CONFIG_HOME` inside a git work tree and `XDG_STATE_HOME` outside one, `Dir()` names the `XDG_STATE_HOME` path and an `Update` writes no file inside the work tree. The result never depends on the working directory, an instance path or the configuration directory. (The end-to-end form, with a real instance root inside the work tree, is exercised by <<ISSUE:7>>'s functional scenarios.)
- [ ] (R12) Two identities differing only in API domain, two differing only in environment, and two differing only in folder path each produce two separate data files.
- [ ] (R12) Folder paths `/a/b`, `a/b` and `/a/b/` map to the same data file. API domains `https://App.Infisical.com/api` and `https://app.infisical.com` map to the same data file.
- [ ] The directory is created with mode 0700 and each data, lock and temp file with mode 0600, whatever the process umask. A test checks the modes under a permissive umask such as 0o000.

#### Trust checks

- [ ] Before any read or write, `Lstat` must show the store directory is a real directory, not a symlink, owned by the effective user and not writable by group or others. A directory that's owned by the user but looser than 0700 is tightened with `Fchmod` on the opened directory, and only after ownership is confirmed. A test shows a 0755 directory ends up 0700.
- [ ] A store directory that's a symlink, or not owned by the effective user (tested where the test can arrange it, and skipped when run as root), makes `Load` return no entries and `Update` return an error wrapping `ErrUnwritable`, with nothing written through the symlink.
- [ ] After the directory checks pass, every file operation goes through an `os.Root` opened on the verified directory. Data and lock files are opened with `O_NOFOLLOW`, and `Fstat` must show a regular file owned by the effective user with no group or other permission bits. A data file or lock file that's a symlink makes the store empty and unwritable for that identity, and the symlink's target is neither read nor modified.
- [ ] A data file larger than 1 MiB is treated as unreadable: `Load` returns no entries, and the next successful `Update` replaces it.
- [ ] A data file whose echoed `identity` doesn't match the identity its name was derived from is treated as empty by `Load` and overwritten by the next `Update`.

#### Unreadable and unknown files (R15)

- [ ] (R15) A data file with invalid JSON, and one with an unknown `format_version`, each make `Load` return an empty map without an error the caller must act on, and the next `Update` for that identity rewrites the file as valid format 1 holding only that update's puts.
- [ ] (R15) A data file with mode 0000, in a test running as a non-root user (skipped as root), is treated as empty by `Load`.
- [ ] (R15) A store directory that can't be written (for example a read-only parent, skipped as root) makes `Update` return an error wrapping `ErrUnwritable` and never panics. Turning that error into one warning and a successful provisioning run is <<ISSUE:5>> and <<ISSUE:6>>'s job.

#### Updates, merging and eviction (R10, R13)

- [ ] `Update` runs inside the lock in this order: try `Flock(LOCK_EX|LOCK_NB)` on `<hex>.lock` every 20 ms until 2 s pass; read and parse the data file, where missing or bad counts as empty; apply `evictAll`, then `evictKeys`, then `puts`, leaving every other key alone; return without writing when nothing changed; remove leftover `.<hex>.json.tmp-*` files from an earlier crash; write a temp file created exclusively at 0600 in the same directory, `Sync`, close, `Chmod(0o600)`; `Rename` it over the data file.
- [ ] (R10, R13) Merge: an `Update` putting X and Y, followed by one putting only X (the "workspace A resolves X and Y, workspace B resolves only X" case), leaves Y stored with its original value and `resolved_at`.
- [ ] `evictKeys` removes only the named keys, and `evictAll` removes every key for the identity before the same call's puts are applied. An `Update` whose only effect would be evicting keys that aren't stored doesn't rewrite the file.
- [ ] (R13) Concurrency: two separate OS processes (the test binary re-executing itself as helpers), one putting X and one putting Y for the same identity, with a test hook that pauses each writer after it reads the file until both have read, leave both X and Y stored. With the lock disabled through a test hook and the same pause, one key is lost, which shows the test detects the race.
- [ ] (R13) While a test holds the identity's `<hex>.lock` with `flock`, an `Update` waits, and it completes and writes once the test releases the lock within 2 s. A lock held for longer than 2 s makes `Update` return an error wrapping `ErrLockTimeout` within 2 s plus one poll interval, leaving the data file unchanged. (The warning and the successful provisioning run built on that error belong to <<ISSUE:5>>.)
- [ ] (R13) While a test holds the lock, `Load` returns the stored entries without waiting, measured well under the 2 s bound. `Load` never opens or locks the lock file.
- [ ] (R13) With a test hook that aborts an update after the temp file is written and before the rename, the data file still holds its previous valid contents, and a following `Load` returns them. The next `Update` removes the leftover temp file.

#### Work-tree guard (R14)

- [ ] (R14) The package exposes the walk (for example `InWorkTree(dir string) bool`) so <<ISSUE:5>>'s session can run it once per run and cache the verdict, and `Update` returns an error wrapping `ErrInWorkTree` without creating the directory or any file when the walk finds `.git`.
- [ ] (R14) The walk runs `os.Lstat(<dir>/.git)` at every level from the store directory to the filesystem root, twice: once over the cleaned path as configured, which need not exist yet, and once over the symlink-resolved path of its deepest existing ancestor. A `.git` directory and, separately, a `.git` file (a linked worktree) above the store each disable writes. So does a store path reached through a symlink whose resolved target sits under a `.git` directory.
- [ ] (R14) The walk starts no subprocess: with a counting `git` stub first on `PATH`, the tests above record no invocation. (The one warning naming the directory is rendered by <<ISSUE:6>>.)

#### Test isolation (R27)

- [ ] Every test in the package sets `XDG_STATE_HOME` and `HOME` to `t.TempDir()` paths with `t.Setenv`, so no test reads or writes the operator's real store.
- [ ] `go test ./internal/vault/...` and `go vet ./...` pass on Linux and macOS. `flock` code lives in `lock_unix.go` with a `unix` build tag, like the Codex trust lock.

**Dependencies**: <<ISSUE:1>>, <<ISSUE:3>>
### Issue 5: feat(vault): serve stored values through a provisioning-bundle decorator

**Complexity**: critical

**Goal**: Add an `internal/vault/storefallback` session and provider decorator that wraps only the overlay, team and personal provisioning bundles in `runPipeline`, records successful resolutions, evicts per the PRD, serves stored values only for *unauthenticated* or *unreachable* failures, notes what it served or missed in a run-scoped notice collector, and flushes once per identity when the pipeline ends.

**Acceptance Criteria**:

**Decorator and policy (`internal/vault/storefallback/decorator.go`, `session.go`)**

- [ ] `storefallback.NewSession(dir string, notices *fallbacknotice.Collector, now func() time.Time) *Session` and `(*Session).Wrap(p vault.Provider) vault.Provider` exist; `Wrap` decides by a type assertion alone, with no reference in hand: it returns `p` unchanged when `p` doesn't implement `vault.StoreIdentifier`, and otherwise returns a wrapper. `StoreIdentity(ref)` is called only inside the wrapper's `Resolve`, per reference; when it returns `false` for a reference, that `Resolve` passes straight through to the inner provider (the inner result or error returned as is, nothing buffered, loaded or noted). A test covers both the non-`StoreIdentifier` provider and a per-reference `false`.
- [ ] The wrapper makes exactly one inner `Resolve` call per reference, forwards `Name`, `Kind` and `Close` to the inner provider, and applies one pure policy function to the result:
  - success: buffer a put of key, value (via `reveal.UnsafeReveal`, the only reveal in the package), resolution time from `now`, and the returned `VersionToken` (token and provenance, empty when the provider returned none), then return the inner result unchanged;
  - `vault.ErrKeyNotFound`: buffer an eviction of that key;
  - *answered* (`ClassAnswered`) with `HTTPStatus` 403 or 404: buffer an eviction of the whole identity;
  - *unauthenticated* or *unreachable* (`ClassUnauthenticated` or `ClassUnreachable`, whatever the `Reason`) with the key in `store.Load` for that identity: return the stored value as a `secret.Value` with its stored `VersionToken`, note `Served(identity, reason, resolvedAt)`, and buffer no put for it;
  - `ClassUnauthenticated` or `ClassUnreachable` with the key not stored: note `NothingToFallBackOn(identity)` and return the original error;
  - anything else (including `ErrClientNotInstalled` and an unclassified `ErrProviderUnreachable`): return the original error.
- [ ] A table test asserts by pointer identity (`err == innerErr`) that every non-served path returns the exact error value the inner provider returned, including a 403 *unauthenticated*-with-nothing-stored case, a 403 *answered* case and a plain unclassified error.
- [ ] A policy table test covers the served path for every class and reason that serves, not only the logged-out one. With a stored key (value bytes, a non-empty version token and a known `resolved_at`), the inner provider returns a `ClassifiedError` with `ClassUnreachable` / `ReasonTimedOut`, and separately `ClassUnreachable` / `ReasonUnreachable`, each once for an identity standing for a CLI-session principal and once for a minted principal (the decorator keys on the class alone, so both must serve). Each case asserts the returned value's bytes equal the stored value, the returned `VersionToken` equals the stored token, and the collector holds one `Served` note for that identity whose reason is the collector's value for exactly the case's reason (`ReasonTimedOut` or `ReasonUnreachable`; the stdlib-only collector has its own reason values, and the session maps each `vault.Reason` one to one) with the stored `resolved_at`. A `ClassUnauthenticated` / `ReasonLoggedOut` row sits in the same table. A decorator that serves only on `ClassUnauthenticated`, or that drops or rewrites the reason, fails this test. (R7, R9, R16, R20)
- [ ] A `ClassUnauthenticated` 403 (the classification Issue <<ISSUE:3>> gives rule 4) buffers no eviction and leaves the store entry unchanged. (R11)
- [ ] `store.Load` is called at most once per identity per run and its result is cached in the session; an unreadable or empty load behaves as "nothing stored".
- [ ] A value served from the store is not buffered as a put, so a later `Flush` leaves its `resolved_at` unchanged. (R16)

**Flush (`(*Session).Flush`)**

- [ ] `Flush` makes at most one `store.Update(dir, identity, puts, evictKeys, evictAll)` call per identity with buffered changes and none for an identity with no changes (a run that only served makes no write).
- [ ] `store.ErrInWorkTree` notes `StoreInWorkTree(dir)` once and skips the rest of the flush; `store.ErrLockTimeout` notes `LockTimeout(identity)` and continues with the next identity; `store.ErrUnwritable` (and a failed `store.Dir`) notes `StoreUnwritable(dir)` once per run. No flush error is returned to or aborts the pipeline. (R13, R14, R15 routing; the store behaviours themselves belong to <<ISSUE:4>>)

**Wiring (`internal/workspace/apply.go`)**

- [ ] `runPipeline` attaches the Issue <<ISSUE:3>> run state with `vault.WithRunState` next to the redactor, creates one `storefallback.Session` over `store.Dir()` and `a.Notices`, and defers `session.Flush()` so it runs on success and on every later error return.
- [ ] A single `provisioningBundle(ctx, registry, vaultCfg, label)` helper calls `resolve.BuildBundle` then `bundle.Wrap(session.Wrap)`, and the three sites that today call `resolve.BuildBundle` for the overlay (`"workspace-overlay.toml"`), team (`"workspace config"`) and personal (`"global overlay"`) bundles call it instead. `CloseAll` still runs on each bundle.
- [ ] A source-scanning test fails if `apply.go` calls `resolve.BuildBundle` anywhere other than inside `provisioningBundle`, and credential sync's provider build (the `syncBundle` path) stays unwrapped.

**PRD acceptance criteria this unit makes pass** (workspace-level tests through the fake backend, which gains `identity`, `fail_class`, `fail_status` and `fail_plain` config knobs so it implements `StoreIdentifier` and returns a `ClassifiedError`, with `fail_class` selecting both the class and the reason so every class/reason pair can be driven; `XDG_STATE_HOME` is a `t.TempDir()`)

- [ ] **R16 (per layer):** a vault-sourced key in the personal global configuration, and separately one in the workspace overlay (and the team workspace configuration), is served from the store when its provider fails *unauthenticated*, and appears in the instance's materialized env.
- [ ] **R7/R9/R16 (unreachable at workspace level):** through the fake backend's `fail_class` knob set to *unreachable* with reason timed out, and separately *unreachable* with reason unreachable, a stored vault-sourced key is served and appears in the instance's materialized env, the instance state records the stored version token, and the collector's `Served` note carries `ReasonTimedOut` or `ReasonUnreachable` respectively.
- [ ] **R16/R17/R20 (per-key within one identity):** one identity with two stored keys and two further unstored, non-required keys: the run serves the two, omits the other two, and the collector holds exactly one `Served` entry and one `NothingToFallBackOn` entry for that identity. (The printed warning and line come from <<ISSUE:6>>'s rendering of those entries.)
- [ ] **R18:** a strict-mode workspace whose every declared key is served from the store provisions successfully, and a required key served from the store passes the required-key check; `a.Keys.Report()` lists the served keys as supplied.
- [ ] **R10/R16 (recovery and tokens):** after a fallback run, a run whose provider resolves successfully leaves the collector empty, and the store holds the newly resolved values with new resolution times. After a fallback run, the instance state records the version token that was stored with each served value, and the store file's resolution times are unchanged.
- [ ] **R12/R20 (identity separation):** two providers on the same API domain with different project IDs, one stored and one not, both failing *unauthenticated*: the unstored provider's keys are not served from the other's entry, and when both are stored the collector holds two `Served` entries.
- [ ] **R10/R16 (empty token):** a value resolved with an empty version token is stored with an empty token and served back with an empty token.
- [ ] **R10 (credential sync):** a credential-sync lookup that succeeds writes nothing to the store, and one that fails *unauthenticated* reads nothing from it (asserted by a store directory that stays absent, and separately a pre-populated store whose value does not appear in the synced credentials).
- [ ] **R10/R12 (only requested keys):** after a successful resolution of two keys from a folder holding three, the store file for that identity exists under `$XDG_STATE_HOME/niwa/secret-cache/` with mode 0600 in a 0700 directory and holds exactly the two keys, each with a resolution time and the version token the provider returned.
- [ ] **R11 (eviction):** a successful resolution that reports requested key X missing (`ErrKeyNotFound`) removes X from the store, and a following *unauthenticated* run does not serve X. An *answered* failure with `HTTPStatus` 403, and separately 404, removes every key that run requested from that identity, and a following *unauthenticated* run serves none of them.

**Not owned here** (so reviewers don't double-count): classification rules and the probe (<<ISSUE:3>>); store format, locking, race, abort, work-tree and corrupt-file criteria (<<ISSUE:4>>); warning text, ages, hook payload and per-surface stderr (<<ISSUE:6>>); end-to-end `niwa dispatch`/`create`/`apply`/`init`/`reset`/`watch` scenarios through the stub CLI, the marker-secret and pseudo-terminal sweeps, R26's counting stub and R27 isolation (<<ISSUE:7>>).

**General**

- [ ] `resolve.go` has no diff.
- [ ] `go test ./...` and `go vet ./...` pass on Linux and macOS.
- [ ] Must deliver: `internal/fallbacknotice.Collector` with the recording methods and accessors above, and a populated `Applier.Notices` flow from `runPipeline` into the session, so <<ISSUE:6>> only adds rendering and surface wiring (required by <<ISSUE:6>>).

**Dependencies**: <<ISSUE:3>>, <<ISSUE:4>>
### Issue 6: feat(cli): report fallback notices on every provisioning surface

**Complexity**: testable

**Goal**: Render the fallback notices (the "may be stale" warning, the "nothing to fall back on" line and the three store warnings) to standard error for `niwa dispatch`, `create`, `apply`, `init`, `reset` and `watch`, and into the session-start hook's `additionalContext` payload (or its standard error on other failures), with the exact text fixed in the design's Decision Outcome.

**Acceptance Criteria**:

#### Collector and rendering (`internal/fallbacknotice`)

- [ ] `internal/fallbacknotice` imports only the standard library; its `Identity` is its own struct (kind, API domain, project ID, environment, folder path) and the package imports nothing from `internal/vault`.
- [ ] `*Collector` is nil-safe (every method on a nil collector is a no-op and both renderings return "") and mutex-guarded (a `-race` test records from several goroutines).
- [ ] `Served(identity, reason, resolvedAt)` keeps the first reason recorded for an identity and the earliest `resolvedAt`; a table test records the reason that <<ISSUE:5>>'s session maps from `vault.ReasonLoggedOut` followed by the one it maps from `vault.ReasonTimedOut`, and asserts the rendered reason is "is logged out or expired" (the first reason wins; a later timed-out reason never overrides it, per PRD R20).
- [ ] The collector takes a clock (`now func() time.Time`), so ages are tested without sleeping.
- [ ] `RenderText` produces, for a served identity, exactly:
      ``warning: using stored values that may be stale for infisical project <project-id> (env <env>, path <path>, <api-domain>): the provider <reason-clause>; the oldest value is <age> old. Run `infisical login` to refresh them.``
      where the reason clause is `is logged out or expired` for the collector reason mapped from `vault.ReasonLoggedOut`, `timed out` for `vault.ReasonTimedOut` and `is unreachable` for `vault.ReasonUnreachable`. The collector keeps its own reason values (it imports nothing from `internal/vault`), and the one-to-one mapping from `vault.Reason` lives in <<ISSUE:5>>'s session. (Golden-string test per reason.)
- [ ] `RenderText` produces, for an identity with an unstored key, exactly:
      ``warning: infisical project <project-id> (env <env>, path <path>, <api-domain>) could not be used and no previously resolved value exists to fall back on. Run `infisical login`.``
- [ ] `RenderText` produces one `warning: ` line each for `LockTimeout(identity)`, `StoreInWorkTree(dir)` and `StoreUnwritable(dir)`; the work-tree and unwritable lines name the store directory, and each fires at most once per run however many times it is recorded. The exact wording of these three lines is defined once in the package and pinned by golden tests.
- [ ] Output is deterministic: identities render in a stable order (served warnings, then nothing-to-fall-back-on lines, then store warnings, each sorted by identity), so two runs with the same records print the same bytes.
- [ ] `RenderContext` has golden-string tests, one per notice kind (served, nothing-to-fall-back-on, store in a work tree, lock timeout, store unwritable, including the empty-directory case), committed as fixtures in `internal/fallbacknotice/testdata/`. The same R20 field checks that run against `RenderText` run against `RenderContext`: the served sentence contains the provider kind, API domain, project ID, environment, effective folder path, the reason clause, "may be stale", the age in R20's format, and an instruction to ask the operator to run `infisical login` rather than run it itself. A rendering that drops any of these fails.
- [ ] Both renderings strip control and line-separator characters from identity fields and directory paths, the way `keyreport` sanitises, and cap each identity field at 200 characters; a test feeds a project ID containing `\n`, `\r`, ` ` and a 300-character field.
- [ ] Both renderings return "" when nothing was recorded.
- [ ] **R20 (output format), owned here:** a test asserts the served warning contains the provider kind, the API domain, the project ID, the reason, "may be stale", the age and `infisical login`; ages of exactly 60 seconds and 1 minute read "1 minute", exactly 60 minutes and 1 hour read "1 hour"; with two keys stored 3 days and 5 days ago served for one identity, exactly one warning prints and it reads "5 days"; when one identity's first failure was unauthenticated and a later one timed out, the reason reads "logged out or expired".
- [ ] **R19/R20 (ages), owned here:** a value stored 30 days before the run is served and the warning's age reads "30 days" (exercised through the fake backend at the `runPipeline` level with a backdated store entry, so it also shows no age ceiling applies); collector tests assert ages of 30 seconds, 59 minutes, 47 hours and 48 hours read "less than a minute", "59 minutes", "47 hours" and "2 days", with the singular unit when N is 1 and rounding down.

#### CLI wiring

- [ ] `wireKeyReport` attaches a notice collector to `applier.Notices` and renders its text after the key report; a test for each of `create`, `apply`, `init` and `reset` (through the fake vault backend's `fail_class` knob from <<ISSUE:5>>, or a pre-filled collector) shows the served warning on the command's stderr writer after the key report.
- [ ] `provisionResult` carries `Notices` on success and on `Create` failure; `dispatch.go` and `watch.go` render them after the key report on both paths. Tests use the package's fake provisioner to return a result with recorded notices and assert stderr.
- [ ] **R17 (empty store, dispatch), owned here:** with an empty store, a required key and a logged-out (unauthenticated) failure, `niwa dispatch` exits non-zero and its standard error holds the nothing-to-fall-back-on line; with the key not required and strict mode off, provisioning succeeds without the key and still prints that line. (Test through `realProvisionInstance` and the fake backend, so the `Create` failure path is the one exercised.)
- [ ] **R21 (hook), owned here:** in `instance_from_hook_test.go`, when the hook provisions successfully with each of the R14 (store in a work tree), R15 (store unwritable), R17 (nothing to fall back on) and R20 (served) conditions recorded, each message appears in the `additionalContext` payload on standard output and none appears on standard error. The test decodes the payload and asserts, for the served notice, the project ID, environment, folder path, API domain, reason, age and `infisical login`, and for the nothing-to-fall-back-on notice the project ID, environment, folder path and API domain. On the strict-refusal path the notices appear in the strict-failure payload. When a required key has no stored value and provisioning fails non-strictly, the hook exits non-zero with no payload, as today, and the R17 line is on its standard error. Each of the hook's four other failure returns prints the text rendering to standard error.
- [ ] A run with no fallback (empty collector) produces byte-for-byte today's stdout, stderr and hook payload on every surface; existing key-report and hook-payload golden tests pass unchanged.
- [ ] Strict mode and the required-key check still read only `Applier.Keys`; a test with notices recorded and every key supplied shows no strict refusal.
- [ ] `go test ./...` and `go vet ./...` pass.
- [ ] Must deliver (required by <<ISSUE:7>>): the served warning and the nothing-to-fall-back-on line rendered with exactly the Decision Outcome text above on stderr for `dispatch`, `create`, `apply`, `init`, `reset` and `watch`, and inside `additionalContext` for the hook, so functional scenarios can grep for `may be stale` together with ``Run `infisical login` to refresh them``, for `no previously resolved value exists to fall back on`, and for the store warnings' pinned wording.

**Dependencies**: <<ISSUE:5>>
### Issue 7: docs(vault): cover the fallback end to end and record the superseded invariant

**Type**: docs

**Complexity**: testable

**Goal**: Prove the fallback end to end on every provisioning surface through a stub `infisical` CLI, isolate the functional harness from the operator's state directory, and bring the vault-integration records and guide in line with the new store.

**Acceptance Criteria**:

**Stub and harness**

- [ ] The functional `infisical` stub gains modes selectable per scenario: logged-out export (each of the three pinned wordings), server-response export (`Response Code: <n>` for 401, 403, 404, 500), a quick non-server export failure (connection-refused stderr with no `Response Code:` line and no logged-out wording), hanging export (never exits), hanging export with a forked child that keeps stdout open and writes its PID to a file the step can read, and a probe mode for `infisical login status --json` that can print a given sessions payload, print non-JSON, never exit, or fork a child that keeps stdout open (again recording the child's PID). The stub records each invocation (subcommand and count) to a file a step can assert on, and never echoes a `token` field anywhere except its own probe stdout.
- [ ] `testState.buildEnv` strips any inherited `XDG_STATE_HOME` and sets it to a directory inside the scenario sandbox, alongside the existing `HOME`/`XDG_CONFIG_HOME`/`TMPDIR` handling. A step exists to pre-fill and backdate the store under that directory through the store test helper (writing the store format directly with a chosen resolution time).
- [ ] Scenarios use `NIWA_TEST_VAULT_TIMEOUT` so hang modes finish quickly; no functional scenario waits the full 30 s or 15 s bound (the real-bound tests live with the subprocess unit).

**End-to-end fallback (PRD criteria owned by this unit)**

- [ ] (R2, R16, R20) With the stub's export exiting 1 with "No valid login session found", the probe reporting `{"sessions": []}`, and a store holding the referenced keys, `niwa dispatch` run end to end through real provisioning exits 0, the instance's materialized env holds the stored values, and stderr holds the stale-value warning with reason "logged out or expired", "may be stale", the age and `infisical login`. A companion assertion checks that the Phase 1 golden fixture for the same stub output is today's hard "export exited 1" error, so the scenario pins a change in behaviour.
- [ ] (R16, R21) The previous criterion passes for each of `niwa create`, `niwa apply`, `niwa init`, `niwa reset`, and one `niwa watch` reconcile cycle, each printing the warning on stderr.
- [ ] (R21) Under the session-start hook (`niwa instance from-hook`), the store-in-work-tree warning (R14), the unreadable/unwritable-store warning (R15), the nothing-to-fall-back-on line (R17) and the stale-value warning (R20) each appear in the `additionalContext` payload on stdout when their condition is set up and the hook provisions successfully. The scenario decodes the payload and asserts the served notice's project ID, environment, folder path, API domain, reason, "may be stale", age and `infisical login`, compared against the `RenderContext` golden fixture from <<ISSUE:6>>. When a required key has no stored value, the hook exits non-zero with no payload, as today, and the nothing-to-fall-back-on line is on its stderr.
**Timeouts and short-circuiting end to end (PRD R7 and R9 criteria owned by this unit)**

All scenarios below run under `NIWA_TEST_VAULT_TIMEOUT` with a short export and probe bound, store the referenced keys first through the pre-fill step, and use a non-required key with strict mode off unless stated. "Within the bound" means the command's wall time for the vault call stays under the shortened bound plus 5 s, the scaled form of the PRD's 35 s and 20 s limits.

- [ ] (R3, R7, R20) Hanging export, CLI-session principal: the stub's export never exits and its probe prints one `authenticated`, `verified` session for the provider's API domain. `niwa apply` exits 0 within the bound, the instance's materialized env holds the stored values, and stderr holds exactly one stale-value warning per identity with reason "timed out", "may be stale" and `infisical login`.
- [ ] (R7) The same scenario with the forked-child export mode: the command returns within the bound, the child PID the stub recorded no longer exists when the run ends, and the run falls back with reason "timed out".
- [ ] (R3, R7, R20) Hanging export, minted principal: a provider whose credentials come from the sandbox's `provider-auth.toml` (universal-auth login answered by a local test HTTP server), with the stub's export never exiting. `niwa apply` exits 0 within the bound, the stored values are materialized, the warning's reason is "timed out", and the stub's invocation log records no `login status` call.
- [ ] (R2, R7, R20) Hanging probe, CLI-session principal: the export fails quickly with the connection-refused mode and the probe never exits; separately, the probe forks a child that keeps stdout open. Each run returns within the probe bound plus 5 s, the forked child's PID no longer exists afterwards, the failure is classified *unreachable* (`ClassUnreachable` / `ReasonTimedOut`), and the run falls back with the stored values and a warning whose reason is "timed out".
- [ ] (R9, R20) One hanging domain: a credential-sync source and two further provider folders, all on the same API domain and all stored, with the stub's export hanging and the probe reporting an `authenticated`, `verified` session. The stub's invocation log records exactly one export and at most one `login status` invocation for that domain in the run, every stored key from both folders is served (present in the materialized env), each served identity's warning gives the reason "timed out", and credential sync takes its existing soft-failure path without reading the store.

- [ ] (R22) A scenario using distinct marker secret values captures stdout, stderr and every file written under the sandbox during four runs: a fallback run, a successful run, a run that fails on a required key with an empty store, and a run with a 404 answer. The markers appear only in the store files and the instance's materialized files, and no captured stream or file contains the `token` field value the stub's probe printed.

**Records and guide**

- [ ] (R24) `docs/prds/PRD-vault-integration.md`, `docs/designs/current/DESIGN-vault-integration.md` and `docs/guides/vault-integration.md` each contain "Superseded by PRD-dispatch-offline-secrets." within the R29 and D-7 passages (between the passage's heading and the next heading; for the design's requirements-table row and the guide's bullets, in the row or bullet itself). The comments in `internal/workspace/apply.go` and `internal/cli/status_check_vault.go` that cite the no-disk-cache rule are rewritten so they no longer present it as current. `git grep -n -e 'INV-NO-DISK-CACHE' -e 'vault-integration R29' -- '*.go'` returns only lines that also name `PRD-dispatch-offline-secrets`.
- [ ] (R25) `docs/guides/vault-integration.md` contains verbatim: "Run one provisioning command while logged in after upgrading, so the store holds values to fall back on." The PR's release-note section repeats it.
- [ ] The guide gains a section on the fallback that states, from the design's Security Considerations: the store location (`$XDG_STATE_HOME/niwa/secret-cache/`, default `~/.local/state/niwa/secret-cache/`) and its 0700/0600 modes; that it holds the last resolved value of every requested key in plain text across workspaces and the personal layer, with no age limit, outliving reaped instances; that only unauthenticated or unreachable failures are served and the warning shows the age; that during a lapsed CLI session an upstream revocation or rotation is not seen until the owner logs back in, and a machine identity whose credentials come from the local `provider-auth.toml` avoids that; the advice to exclude the directory from backups on shared or managed hosts; the store integrity rules (owned by the user, not group/other writable, never written under a `.git` work tree, otherwise disabled with a warning); and the purge step: delete the directory.
- [ ] The guide explains that the hook's notice asks the agent to tell the operator to run `infisical login` rather than run it itself, and that `NIWA_TEST_VAULT_TIMEOUT` is test-only and prints a warning when set.

**Isolation in CI**

- [ ] (R27) CI (`.github/workflows/test.yml`) runs the full unit and functional suites with `HOME` and `XDG_STATE_HOME` pointing at two sentinel directories that do not exist and with no `infisical` binary on `PATH`; the suites pass, and a following step asserts neither sentinel directory exists.

**Live acceptance (post-release, recorded in the PR's test plan)**

- [ ] The PR's test plan includes a live acceptance step for the owner to run after release, which covers the unmeasured expired-user-session path: with the store warmed by one dispatch made while logged in, the owner runs `infisical logout`, dispatches a trivial session, confirms the stale-value warning prints (reason "logged out or expired") and that the global keys are present in the worker's instance (checking key names only, never printing values), then runs `infisical login`. The step is written into the test plan as a checkbox the owner ticks, not run by CI.

- [ ] `go test ./...` and the functional suite pass on Linux and macOS.

**Dependencies**: <<ISSUE:6>>

## Implementation Sequence

**Critical path:** I1 -> I2 -> I3 -> I4 -> I5 -> I6 -> I7, all seven units. Issue 3 owns
`vault.Identity` and its normalisation function, which the store in Issue 4 consumes, so there is
no parallel work between units. Within Issue 4, the parts that don't touch identities (locking,
atomic writes, trust checks, the `.git` walk) can be drafted while Issue 3 is in progress.

**Recommended order:** as numbered. Commit each unit separately so the golden-fixture commit (I1)
precedes every behaviour change, which the PRD's acceptance criteria require.

**Notes carried from plan review (non-blocking):**

- Issue 3's tests use `{"sessions": []}` as the logged-out probe answer, matching Issue 7. The
  functional stub's default `login status` answer (an authenticated session with no verification
  field) classifies as unreachable, not unauthenticated.
- A failed ownership or symlink check on any store file disables the store for the run, as the
  design says, not only for that identity.
- The store flush runs the `.git` walk once, before the first write of the run.
- The source scan asserting that `apply.go` builds no provisioning bundle outside the wrapping
  helper skips comments.
- The "store unwritable" notice renders sensibly when `store.Dir()` itself failed and the
  directory is empty.
- Issue 7 extends Issue 1's functional-stub knobs (fixed failure, invocation log) in place.
- Issue 5 implements how `Served` notes combine (first reason wins, oldest time wins). Issue 6 owns
  only rendering and wiring.
