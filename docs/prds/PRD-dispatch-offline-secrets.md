---
schema: prd/v1
status: Done
problem: |
  niwa resolves vault secrets at provisioning time by running the Infisical CLI under the
  operator's login. When that login is missing or expired, the CLI's message matches none of
  niwa's auth-failure markers, so resolution turns into a hard error and `niwa dispatch`,
  `niwa create`, `niwa apply`, `niwa watch` and the session-start hook all abort. Hosts left
  unattended for days have nobody to log back in, so dispatching stops entirely.
goals: |
  Provisioning keeps working through a lapsed login, an expired token, a timed-out export or an
  unreachable vault service (for providers that don't need niwa's own machine-identity login),
  on the last values that resolved for that provider, and says so
  on every run with the provider, the age of the values and the re-login command. Apart from
  new time bounds on vault calls, everything outside that fallback behaves as it does today.
absorbed:
  - docs/briefs/BRIEF-dispatch-offline-secrets.md
---

# PRD: Provisioning keeps working when the vault login lapses

## Status

Done

Absorbed [BRIEF-dispatch-offline-secrets](docs/briefs/BRIEF-dispatch-offline-secrets.md); carried in Absorbed Brief.

## Absorbed Brief

The feature exists because provisioning depends on a vault login being alive at the moment it
runs, and on an unattended host that login lapses with nobody there to renew it. The brief
framed the outcome from the owner's side: sessions left running keep starting workers after
the login lapses, on the last values that resolved, and every such run says plainly which
provider was unavailable, how old the values are and how to log back in. Configuration
changes that a stale value would hide still reach the owner, and logging back in ends the
fallback with nothing to clear by hand. This document's Problem Statement, Goals and User
Stories carry that framing in full, and its Out of Scope carries the brief's exclusions:
keyring storage, changes to how instances are written, logging in on the owner's behalf,
other vault backends and offline provisioning.

## Problem Statement

niwa turns `vault://` references into values while it provisions an instance. For the
Infisical backend it runs `infisical export` as a subprocess and lets the Infisical CLI
authenticate with whatever it has: a per-provider token niwa minted from a machine identity,
an `INFISICAL_TOKEN` in the environment, or the operator's interactive `infisical login`
session. The last of these lapses. Sessions expire, and hosts get logged out.

When the session is missing or expired, `infisical export` exits 1 with messages such as "No
valid login session found ... Please run [infisical login] manually", "we couldn't find your
logged in details", or "Your login session has expired". niwa's auth-failure markers ("not
logged in", "login expired", "session expired" and similar) match none of them. The export
failure therefore falls through as an unexplained provider error, and the resolver turns that
into a fatal error. Every provisioning path shares that resolver: `niwa dispatch`, `niwa
create`, `niwa apply`, `niwa watch`, and the session-start hook that provisions an instance
for a new session. The owner sees a generic "export exited 1" failure. They don't see "you are
logged out". The same lapse also hits the credential-sync lookup, which reads
machine-identity credentials through the personal global configuration's vault provider and
aborts on the same unrecognized error.

A lapsed login is a problem at a terminal, but it's a stoppage on an unattended host. An owner
running long-lived coordinator sessions expects them to keep dispatching workers for days or
weeks. The login doesn't last that long, and there's no one to run `infisical login` and no
terminal a background process could prompt on. The values the provider would return are
usually the same ones that resolved yesterday, and they already sit in plain text in every
instance on the host. Provisioning still stops until the owner returns.

On the host that motivated this work, the workspace's own secrets come through a
machine-identity token, which a CLI logout doesn't affect. The personal global configuration's
provider uses the CLI user session. It supplies cross-cutting personal secrets such as a GitHub
token and also serves as the credential-sync source. So a lapse breaks provisioning through the
credential-sync lookup and the personal keys, not through the workspace keys. The requirements
below cover every vault-backed layer, whatever each one's authentication.

## Goals

- An owner can leave a host unattended and trust that coordinator sessions keep starting
  workers after the vault login lapses.
- Every run that falls back tells the owner plainly which provider was unavailable, that the
  values may be stale, how old they are, and how to log back in.
- Failures that a stale value would hide (a reachable, authenticated provider that denies
  access or lacks a key) keep today's handling and are never masked by stored values.
- A host with nothing stored says so plainly, names the re-login command, and otherwise
  handles the missing keys exactly as today for a provider that can't be reached: a reported
  mark, fatal only in strict mode.
- Once the owner logs back in, fresh values replace the stored ones and the warnings stop,
  with nothing to clear by hand.

## User Stories

1. **Unattended dispatch after the login expired.** As a coordinator session running alone
   on the owner's host, when the Infisical login has expired and I run `niwa dispatch`, I
   want the worker's instance created, applied and launched with the last values that
   resolved, plus a warning naming the provider, the age of the values and `infisical
   login`, so that I keep working without waiting for input or hanging on a login prompt.
2. **Hand provisioning while logged out.** As a workspace owner at a terminal, when I run
   `niwa create` or `niwa apply` without noticing my session lapsed, I want the command to
   complete on the stored values and print the same warning, so that I learn about the lapse
   from the command I ran rather than from a generic provider error.
3. **First run with no stored values.** As a workspace owner on a freshly set-up host, when I
   dispatch before ever logging in, I want a message that says the provider could not be used,
   that no previously resolved value exists to fall back on, and how to log in, so that I know
   exactly what to do. The missing keys keep today's handling for a provider that can't be
   reached: listed in the key report, and fatal only when the workspace runs in strict mode.
4. **Host-level personal secrets.** As a workspace owner whose personal global configuration
   sources a GitHub token and other personal keys from the vault, when my login lapses and I
   dispatch, I want those keys served from the stored copy like the workspace's own, so that
   the worker's tools still authenticate.
5. **A secret removed or denied upstream.** As a workspace owner whose login is valid, when a
   teammate deletes a secret the workspace references or revokes my access to it, I want
   provisioning to behave exactly as it does today for that key, never quietly serving the
   last value it saw, so that the configuration change reaches me.
6. **Logging back in.** As a workspace owner returning to the host, after I run `infisical
   login` and dispatch once, I want fresh values resolved and stored, with no warning, so
   that the fallback ends by itself.
7. **Vault service down while logged in.** As a coordinator session whose login is valid, and
   whose providers use the CLI session rather than a machine identity, when
   the Infisical service can't be reached or a call to it times out, I want provisioning to go
   ahead on the stored values with a warning whose reason is "unreachable" or "timed out", and
   to spend at most one export timeout and one probe timeout on that service per run, so that
   an outage behaves like a lapse and never blows the session-start hook's time limit.

## Requirements

Terms used below. A **provisioning run** is one provisioning of one instance: one `niwa
dispatch`, `niwa create`, `niwa apply`, `niwa init` or `niwa reset` invocation, one
session-start hook invocation, or the provisioning of one instance within a `niwa watch`
reconcile cycle. A provider's **API URL** is resolved with the precedence niwa already uses for
its own Infisical API calls: the provider's configured `api_url`, else the
`NIWA_INFISICAL_API_URL` environment variable, else the Infisical cloud default. The **API
domain** is the scheme and host of a URL, lower-cased, with no path or trailing slash. It is
used only in the store identity and in messages; classification never compares domains. A provider's **kind** is its configured backend (`infisical`). Its
**effective folder path** is the folder the export reads: the reference's own folder when it
names one, else the provider's configured folder, else `/`, normalised to a leading `/` with no
trailing `/`. A **provider identity** is the tuple of kind, API domain, project ID, environment,
and effective folder path. A **version token** is the value the backend already
returns with each resolved secret today and records in instance state. A **CLI-session
principal** is the stored `infisical login` session or an `INFISICAL_TOKEN` inherited from the
environment; a **minted principal** is a token niwa obtained itself from a machine identity.
A **server response** is an export whose standard output or standard error contains a
`Response Code:` line.

### Functional

**Classifying a failed resolution**

- **R0.** When the `infisical` binary cannot be started (not installed or not executable),
  niwa keeps today's "client not installed" handling, with today's message, and never serves
  from the store: the remedy is to install the client, not to log in. R1 to R4 apply only to an
  export that started.
- **R1.** When an `infisical export` fails, niwa classifies the failure as exactly one of
  *unauthenticated*, *unreachable*, or *answered*, using R2 to R4. For a CLI-session principal
  the order of R2's rules decides, with R4's wording check applied just before rule 6: the
  first rule that matches wins, and a failure that no
  earlier rule classifies as *unauthenticated* or *answered* is *unreachable* (R2 rule 6). For a
  minted principal R3 decides the same way: a server response is *answered*, and anything else
  is *unreachable*. The export's exit code is not used, because it is 1 for every failure.
- **R2.** For a CLI-session principal, after the export fails, or reaches its time bound, niwa
  runs `infisical login status --json` (the probe) with the same environment as the export. It
  reads the fields of the reported sessions, not the probe's exit code (the probe exits 0 when it
  cannot reach the service). The **deciding session** is chosen this way: when `INFISICAL_TOKEN`
  is set in niwa's environment, the first session whose `tokenSource` names that variable (and
  no session decides if none does); when it is not set, the first session listed. The probe
  runs with the export's environment and reports the principal the CLI itself will use, so the
  deciding session is the one the failed export ran as, whatever domain niwa derives for the
  store identity. The probe is **conclusive** when a deciding session exists
  and has `status` `authenticated` with `verification.state` `verified`. The probe **answered**
  when its standard output decodes as a JSON object with a `sessions` list (possibly empty);
  a probe that reached its bound, didn't start, printed more than the output cap, or printed
  anything else (not JSON, `null`, `{}`) gave no answer. Then, in order, the first matching rule
  decides:
  1. The export was a server response with any status other than 401 or 403 (404, 5xx, and so
     on): *answered*. The service answered, and a missing folder or a server error is never
     served from the store, whatever the probe says.
  2. The deciding session's `status` is `expired` or `rejected`, or probe standard error contains
     "token is malformed": *unauthenticated*.
  3. The export was a 401 or 403 server response and the probe is conclusive: *answered*.
  4. The export was a 401 or 403 server response and the probe is not conclusive:
     *unauthenticated*. An expired or revoked session that reaches the server comes back this
     way, so a probe that can't describe it still falls back.
  5. The probe answered with no deciding session (including an empty `sessions` list):
     *unauthenticated*.
  6. Otherwise (the export failed without a server response or reached its bound, and the probe
     is conclusive, or the deciding session has any other `status` or `verification.state`, or
     the probe gave no answer): *unreachable*.
- **R3.** For a minted principal niwa does not run the probe. A server response is *answered*,
  whatever the status code. Any other export failure, and an export that reaches its bound, is
  *unreachable*.
- **R4.** For a CLI-session principal only, when the probe gave no answer (R2) and the export
  was not a server response, an export whose standard error contains
  one of the pinned CLI's logged-out wordings ("No valid login session found", "couldn't find
  your logged in details", "Your login session has expired") is *unauthenticated* rather than
  *unreachable*. The wordings never override a probe that answered.
- **R5.** An *unauthenticated* or *unreachable* failure still surfaces to the rest of niwa as the
  existing "provider unreachable" condition, and additionally carries its classification. The
  credential-sync lookup keys on the existing condition, so it takes its existing soft path
  (falling back to CLI-session credentials with its existing warning) instead of aborting. The
  fallback in R16 keys only on the classification, never on the existing condition alone.
- **R6.** An *answered* failure keeps exactly today's handling and is never served from the
  store. A key the provider reports as missing stays a mark that is fatal only when the key is
  declared required or strict mode is on. A 403 classified *answered* (for a CLI-session
  principal, only when the probe is conclusive, R2 rule 3) stays the tolerated mark it is today. Any other
  answered failure stays the hard error it is today.
- **R7.** Each `infisical export` is bounded at 30 seconds and each probe at 15 seconds. A call
  that reaches its bound is terminated together with any child process holding its output
  streams, and the call returns within 5 seconds of the bound.
- **R8.** niwa's universal-auth login request to Infisical is bounded at 30 seconds. On reaching
  the bound it fails the way it fails today for any other error. The fallback in this PRD does
  not cover machine-identity login failures.
- **R9.** Within one provisioning run, once a failure against an API domain is classified
  *unreachable*, every later export and credential-sync lookup against that domain in the same
  run skips the subprocess and is classified *unreachable* with the same reason (R20). This
  applies to exports for CLI-session and minted principals alike. niwa's own universal-auth login
  (R8) is not skipped by it and does not trip it. Likewise,
  once a CLI-session principal is classified *unauthenticated* for a domain, later calls for
  CLI-session principals against that domain in the same run are classified *unauthenticated*
  without running a subprocess. The vault calls of one run therefore spend at most one export
  bound and one probe bound per unreachable domain: 45 seconds, or 75 seconds including an R8
  login that times out on the same route, plus at most 2 seconds of store-lock wait per identity
  written (R13) and the 5-second termination grace per bounded call (R7), against the
  session-start hook's 180-second limit.

**The store of last-resolved values**

- **R10.** Every time a vault reference resolves successfully, niwa records it in the store
  entry for that provider identity: the key, the value, the time it was resolved, and the
  version token the provider returned with it (the empty string when the provider returned
  none). A write updates the entries of the keys it resolved and leaves every other key stored
  for that identity untouched. The store never holds a key that was not requested and resolved.
  Credential-sync lookups (which read machine-identity credentials from the vault) and `niwa
  status --check-vault` neither read nor write the store.
- **R11.** Eviction. When an export succeeds but does not contain a requested key, niwa removes
  that key from the store entry for that provider identity. When an export fails *answered*
  with a 403 or 404 server response, which applies to the whole folder, niwa removes every key
  the run requested from that identity. A 403 classified *unauthenticated* (R2 rule 4) evicts
  nothing. A later lapse can then never serve a key that was
  deleted or denied upstream.
- **R12.** The store lives under `$XDG_STATE_HOME/niwa/secret-cache/`, or under
  `$HOME/.local/state/niwa/secret-cache/` when `XDG_STATE_HOME` is unset, empty, or not an
  absolute path. Each provider identity is one file whose name is derived from the identity
  alone, and each file carries a format version. The directory is created with mode 0700 and
  each file with mode 0600. The location never depends on the working directory, an instance
  path, or the configuration directory.
- **R13.** Every store update reads the current file, applies R10 or R11, and replaces the file
  by writing a temporary file in the same directory and renaming it over the target. The
  read-modify-rename sequence for one identity is serialised across concurrent niwa processes by
  an exclusive lock on a per-identity lock file, so two concurrent runs that resolve different
  keys of the same identity both keep their keys. Waiting for the lock is bounded at 2 seconds;
  on reaching the bound niwa skips that update and prints one warning, and provisioning goes on.
  Reads for fallback take no lock and never wait. An interrupted update leaves the previous file or the new
  one, never a partial file.
- **R14.** Before the first store write in a provisioning run, niwa walks from the store
  directory up to the filesystem root looking for a `.git` entry (file or directory), without
  running any subprocess. If it finds one, niwa makes no store write in that run and prints one
  warning naming the store directory.
- **R15.** A store file niwa cannot read, cannot parse, or whose format version it does not
  recognise is treated as empty for fallback and is rewritten on the next successful write. A
  store that cannot be written (permission denied, disk full) produces one warning per run and
  never aborts provisioning.

**Falling back**

- **R16.** When a vault reference fails as *unauthenticated* or *unreachable* and the store
  holds that key for that provider identity, niwa uses the stored value exactly as if it had just
  resolved, with its stored version token. This applies in every configuration layer that
  resolves through a vault provider: the workspace configuration, the workspace overlay, and the
  personal global configuration. A value served from the store is not written back to it, so
  its resolution time and age are unchanged. R16 and R17 apply per key, so one identity can have some keys
  served from the store and others missing in the same run.
- **R17.** When a vault reference fails as *unauthenticated* or *unreachable* and the store does
  not hold that key, niwa keeps today's handling for an unreachable provider: the key is marked
  unresolved and listed in the key report, and the run fails only if strict mode is on. (A
  key declared required behind an unreachable provider is a reported mark today, not a
  failure, and stays one.) The run also prints, once per provider identity with at least one such key, a line naming the
  provider kind, API domain and project ID and stating that the provider could not be used, that no previously resolved value existed to fall back on, and
  the re-login command `infisical login`.
- **R18.** A value served from the store counts as supplied. Strict mode, the required-key check
  and the key report treat it exactly like a freshly resolved value.
- **R19.** A stored value has no maximum age.

**Telling the owner**

- **R20.** Every provisioning run that serves at least one value from the store prints exactly
  one warning per provider identity it served from. The warning contains: the provider kind, API
  domain, project ID, environment and effective folder path; a reason; the words "may be stale"; the age of the oldest value served
  for that identity; and the re-login command `infisical login`. The reason is "logged out or
  expired" when the classification is *unauthenticated*, "timed out" when it is *unreachable* and
  the export or the probe reached its bound, and "unreachable" otherwise. A call short-circuited
  by R9 reports the reason of the call that tripped it. When one identity has several reasons in
  a run, the first one is shown. The age reads "less than a minute" below 60 seconds, "N
  minute(s)" from 60 seconds to below 60 minutes, "N hour(s)" from 60 minutes to below 48 hours,
  and "N day(s)" from 48 hours, rounded down, with the singular unit when N is 1.
- **R21.** The R14, R15, R17 and R20 messages reach the operator on every provisioning path. For
  `niwa dispatch`, `niwa create`, `niwa apply`, `niwa init`, `niwa reset` and `niwa watch` they
  go to standard error. For the
  session-start hook, whose standard error the operator does not see, they are included in the
  `additionalContext` payload the hook writes to standard output, alongside the key report that
  payload carries today, whenever the hook writes that payload (a successful provisioning, or a
  strict-mode refusal). When the hook fails in any other way it keeps today's behaviour (exit
  non-zero, no payload), and the messages go to its standard error.
- **R22.** No output, log line, error message or warning contains a secret value or the raw
  output of `infisical login status --json` (which includes a `token` field). The only files that
  hold secret values are the store files (R10) and the files niwa already writes into instances
  today.
- **R23.** Nothing in resolution or fallback prompts for input or waits on a terminal. Every
  subprocess niwa starts for an export or a probe has its standard input connected to the null
  device and runs in a new session with no controlling terminal, so it cannot open the terminal
  directly either.

**Existing records and rollout**

- **R24.** This PRD supersedes requirement R29 (INV-NO-DISK-CACHE) and decision D-7 ("no
  niwa-internal secret caching") of `PRD-vault-integration`. The same change adds the line
  "Superseded by PRD-dispatch-offline-secrets." inside the R29 and D-7 entries of that PRD and in
  the corresponding passages of the vault-integration design document and guide, and rewrites
  every code comment that cites INV-NO-DISK-CACHE or vault-integration R29 as current.

- **R25.** The fallback can only serve what a successful run has stored, so the store starts
  empty after upgrading. The vault-integration guide contains the sentence "Run one provisioning
  command while logged in after upgrading, so the store holds values to fall back on." and the
  pull request's release-note section repeats it.

### Non-functional

- **R26.** A successful resolution starts no subprocess and makes no network call beyond the
  ones it makes today. Store reads and writes are local file operations only.
- **R27.** Tests never read or write the operator's real store and never run the operator's real
  `infisical` binary. The functional test harness removes any inherited `XDG_STATE_HOME` from the
  environment it builds and sets its own. The 30- and 15-second bounds may be overridden by a
  test-only setting so the suites don't wait the full bound; the timing criteria use the real
  bounds in at least one test each.

## Acceptance Criteria

Timing criteria allow 5 seconds of margin over the stated bound. Tests fill the store and
backdate entries through a test helper that writes the store format directly with a chosen
resolution time. "Today's error" and "today's
mark" mean the error text or key-report entry the code before this change produces for the same
stub output. They are recorded as golden fixtures by a commit that lands before the behaviour
changes, and the criteria compare against those fixtures.

**Classification and fallback**

- [x] With a stub CLI whose export exits 1 with "No valid login session found" and whose probe
      reports `{"sessions": []}`, a store holding the referenced keys, and `niwa dispatch` run
      end to end through real provisioning: dispatch exits 0, the instance's materialized env
      holds the stored values, and standard error holds the R20 warning with reason "logged out
      or expired". A companion test asserts that the golden fixture for the same stub output is
      today's hard "export exited 1" error, so the criterion pins a change in behaviour. (R2, R16,
      R20) Tests: "niwa dispatch serves stored values while logged out" (vault-fallback.feature); `TestGoldenVaultFailureHandling/logged-out-no-valid-session`.
- [ ] The previous criterion passes for each of `niwa create`, `niwa apply`, `niwa init`, `niwa
      reset`, and one `niwa watch` reconcile cycle. (R16, R21) Deferred: create, apply and reset run end to end ("niwa create serves stored values while logged out", "niwa apply serves stored values while logged out", "niwa reset serves stored values while logged out" in vault-fallback.feature); watch is covered through real provisioning with the fake backend (`TestWatchServesStoredValuesThroughRealProvisioning`), but init is only checked by a source scan (`TestProvisioningCommandsRenderNoticesThroughWireKeyReport`), and neither init nor watch runs end to end against the stub.
- [x] With the same stub, a probe reporting a session `status` of `expired`, and separately one of
      `rejected`, each falls back with reason "logged out or expired". (R2) Tests: `TestClassifyExportFailure/expired_session`, `TestClassifyExportFailure/rejected_session`, `TestServedForEveryServingClassAndReason`.
- [x] A probe exiting 0 with `verification.state` `unknown` falls back with reason
      "unreachable". A probe whose stderr contains "token is malformed" falls back with reason
      "logged out or expired". A probe whose only session is `authenticated` and `verified` on a
      `domain` other than the provider's API domain, with an export failing "Response Code: 403",
      produces today's tolerated mark and serves nothing. A probe listing two
      sessions, first `expired` then one with `status` `authenticated` and `verification.state`
      `verified`, is classified *unauthenticated*; with the order reversed, the same export (no
      server response) is classified *unreachable*. A deciding session with `status` `pending`
      falls back with reason "unreachable". (R2) Tests: `TestClassifyExportFailure/verification_unknown`, `TestClassifyExportFailure/malformed_token`, `TestClassifyExportFailure/403_verified_on_another_domain`, `TestClassifyExportFailure/expired_first_then_verified`, `TestClassifyExportFailure/verified_first_then_expired,_same_export`, `TestClassifyExportFailure/pending_session`, `TestServedForEveryServingClassAndReason`.
- [x] With `INFISICAL_TOKEN` set and the probe listing a stored-login session (`authenticated`,
      `verified`) first and the environment-token session (`rejected`) second, the run falls back
      with reason "logged out or expired". (R2) Tests: `TestClassifyExportFailure/env_token_session_rejected,_stored_login_listed_first`.
- [x] An export that is a server response ("Response Code: 404") with a probe that reaches its
      bound, and separately with a probe listing no sessions, produces today's error and serves
      nothing from the store. An export failing "Response Code: 401", and separately "Response
      Code: 403", with a probe that reaches its bound falls back with the warning reason "logged
      out or expired". An export failing "Response Code: 403" with a probe reporting the deciding
      session as `expired` falls back with the same reason. An export failing "Response Code: 404"
      with a probe reporting the deciding session as `expired` produces today's error and serves
      nothing. A 403 classified as a lapsed login leaves the store entry unchanged. (R2, R11) Tests: `TestClassifyExportFailure/404_with_a_probe_that_would_time_out`, `TestClassifyExportFailure/404_with_no_sessions`, `TestClassifyExportFailure/401_with_a_timed-out_probe`, `TestClassifyExportFailure/403_with_a_timed-out_probe`, `TestClassifyExportFailure/403_with_an_expired_session`, `TestClassifyExportFailure/404_with_an_expired_session`, `TestEviction/unauthenticated_403_evicts_nothing`, `TestNonServedPathsReturnTheInnerErrorValue/answered_404`.
- [x] With `INFISICAL_TOKEN` set in the environment and the probe reporting that token's session
      as `rejected`, the run falls back with reason "logged out or expired". (R2) Tests: `TestClassifyExportFailure/env_token_session_rejected_with_a_401`, `TestClassifyExportFailure/env_token_session_rejected,_stored_login_listed_first`.
- [x] A verified probe with an export failure that has no `Response Code:` line (a connection
      refused error) falls back with reason "unreachable". (R2) Tests: `TestClassifyExportFailure/connection_refused,_verified`.
- [x] A probe that prints non-JSON output, with an export whose stderr contains each of the three
      logged-out wordings in turn, falls back with reason "logged out or expired". A probe stub that
      never exits, with an export stderr containing "Your login session has expired", falls back
      with reason "logged out or expired". A probe reporting a matching `authenticated`, `verified`
      session with an export stderr containing "No valid login session found" and no `Response
      Code:` line falls back with reason "unreachable", not "logged out or expired". A probe that prints non-JSON output with an
      export stderr lacking any logged-out wording falls back with reason "unreachable", and a probe
      that reaches its bound with the same export falls back with reason "timed out". (R2, R4, R20) Tests: `TestClassifyExportFailure/no_valid_session_wording,_probe_not_JSON`, `TestClassifyExportFailure/could_not_find_login_wording,_probe_not_JSON`, `TestClassifyExportFailure/session_expired_wording,_probe_not_JSON`, `TestClassifyExportFailure/session_expired_wording,_probe_timed_out`, `TestClassifyExportFailure/wording_with_a_verified_session`, `TestClassifyExportFailure/no_wording,_probe_not_JSON`, `TestClassifyExportFailure/no_wording,_probe_timed_out`.
- [x] A probe reporting `verified` with an export stderr containing both "No valid login session
      found" and "Response Code: 404" produces today's error and does not use the stored value.
      (R2, R4, R6) Tests: `TestClassifyExportFailure/wording_and_404,_verified`, `TestClassifyAnsweredKeepsSentinel`, `TestPolicy`.
- [x] A verified probe with an export failing "Response Code: 403" produces today's tolerated mark
      and does not use the stored value. A verified probe with an export failing "Response Code:
      500" produces today's error and does not use the stored value. (R6) Tests: `TestClassifyExportFailure/403_verified`, `TestClassifyExportFailure/500_verified`, `TestClassifyAnsweredKeepsSentinel`, `TestPolicy`, `an answered <failure> evicts the folder's stored keys` (vault-fallback.feature).
- [x] With no `infisical` binary on `PATH` and a populated store, the run produces today's "client
      not installed" handling and serves nothing from the store. (R0) Tests: `TestClassifyClientNotInstalled`, `TestGoldenVaultFailureHandling/client-not-installed`, `TestDefaultCommander_StartFailure`, `TestPolicy`, `TestNonServedPathsReturnTheInnerErrorValue/client_not_installed`.
- [x] With a minted principal, an export failing "Response Code: 401" produces today's handling
      without running the probe, and an export failing with a connection-refused network error
      falls back with reason "unreachable" without running the probe. (R3) Tests: `TestClassifyExportFailure/minted_401`, `TestClassifyExportFailure/minted_connection_refused`.
- [ ] With the credential-sync source provider's export failing as in the first criterion,
      provisioning does not abort in the credential-sync lookup and prints the existing
      credential-sync warning. (R5) Deferred: `TestCredentialSync_LapsedLoginTakesSoftPath` pins the soft path (no abort, the unreachable observation recorded), but the printed credential-sync warning is checked only for a hanging export, not for the logged-out stub.
- [x] A vault-sourced key in the personal global configuration, and separately one in the
      workspace overlay, is served from the store under the logged-out stub and appears in the
      instance's materialized env. (R16) Tests: `TestStoreFallbackServesEveryLayer/overlay`, `TestStoreFallbackServesEveryLayer/personal` (fake backend through the real apply pipeline).
- [x] One identity with two stored keys and two further, unstored, non-required keys: the run
      serves the two, omits the other two, and prints one R20 warning and one R17 line for that
      identity. (R16, R17, R20) Tests: `TestStoreFallbackPerKeyWithinOneIdentity`, `TestRenderOneWarningPerIdentityWithTheOldestAge`.
- [ ] With an empty store, a required key, strict mode on and the logged-out stub: `niwa
      dispatch` exits non-zero and standard error holds the R17 line. With strict mode off, whether
      or not the key is declared required: provisioning succeeds without the key, lists it in the
      key report and prints the R17 line. (R17) Deferred: the pieces are pinned separately (`TestDispatchNothingToFallBackOn/required=true`, `TestDispatchNothingToFallBackOn/required=false`, "the hook's payload says when nothing is stored to fall back on" in vault-fallback.feature, "a logged-out export leaves a tolerated mark and niwa apply succeeds" in vault-failure-baseline.feature), but no dispatch test checks the key report and the R17 line together with strict mode off and the key declared required.
- [x] A strict-mode workspace whose every declared key is served from the store provisions
      successfully. (R18) Tests: `TestStoreFallbackServedKeysAreSupplied`, `TestStrictRunWithServedKeysIsNotRefused`.
- [x] A value stored 30 days before the run is served, and the warning's age reads "30 days". Ages
      of 30 seconds, 59 minutes, 47 hours and 48 hours read "less than a minute", "59 minutes",
      "47 hours" and "2 days". (R19, R20) Tests: `TestStoreFallbackServesAThirtyDayOldValue`, `TestFormatAge`.
- [x] After a fallback run, a run with a stub that exports successfully prints no R20 warning and
      the store holds the newly exported values with new resolution times. After a fallback run, the
      instance state records the version token that was stored with each served value, and the store
      file's resolution times are unchanged. (R10, R16) Tests: `TestStoreFallbackRecovery`.
- [x] Two providers on the same API domain with different project IDs, one stored and one not: under
      the logged-out stub the unstored provider's keys are not served from the other's entry, and
      two providers served from the store print two warnings. (R12, R20) Tests: `TestStoreFallbackIdentitySeparation`.

**Timeouts**

- [ ] An export stub that never exits, and one that spawns a child which keeps its output open, are
      each terminated within 35 seconds, the spawned child's process ID no longer exists when the
      run ends, and, with the probe reporting an `authenticated`, `verified` session, the run falls
      back with reason "timed out". A probe stub that never exits, and one that spawns a child that
      keeps its output open, are each terminated within 20 seconds and classified *unreachable*.
      With a minted principal, an export that never exits is terminated within 35 seconds and falls
      back with reason "timed out". (R3, R7) Deferred: every case runs under the shortened test bound ("a hanging export for a CLI session times out and falls back", "a hanging export whose child holds stdout open is killed with its child", "a hanging export for a minted principal times out without a probe", "a hanging probe after a quick export failure times out and falls back", "a hanging probe whose child holds stdout open is killed with its child" in vault-fallback.feature; `TestRunInfisicalExport_HangingStubTimesOut`, `TestRunInfisicalExport_ForkedChildHoldingStdout`), and the real 30-second export bound is pinned by `TestRunInfisicalExport_RealBound`, but no test runs the probe against its real 15-second bound.
- [x] With a hanging export stub, a credential-sync source and two further provider folders on the
      same domain, all stored: the stub records exactly one export invocation and at most one probe
      invocation for that domain in the run, every key is served from the store, and the warnings
      give the reason "timed out". With a minted-principal provider on the same domain, its export
      is skipped too, while niwa's universal-auth login request for it is still made. (R9, R20) Tests: "one hanging domain is tried once per run" (vault-fallback.feature), `TestRunStateSkipsAfterTimeout`.
- [x] With the logged-out stub and three provider folders on one domain, all stored: the stub
      records one export invocation and one probe invocation for that domain in the run. (R9) Tests: "one logged-out domain is tried once per run" (vault-fallback.feature), `TestRunStateSkipsAfterLoggedOut`.
- [x] A universal-auth login request to a server that accepts the connection and never responds
      fails within 35 seconds with an error naming the universal-auth login and the timeout, and
      provisioning stops as it does today for any other login error. (R8) Tests: `TestAuthenticate_LoginTimesOut`, `TestAuthenticate_LoginRealBound`, `TestAuthenticate_TimeoutFollowsLoginErrorPath`.

**The store**

- [x] After a successful resolution of two keys from a folder holding three, the store file for
      that identity exists under `$XDG_STATE_HOME/niwa/secret-cache/` with mode 0600 in a directory
      with mode 0700, and holds exactly the two keys, each with a resolution time and the version
      token the export returned. (R10, R12) Tests: `TestStoreFallbackStoresOnlyRequestedKeys`, `TestModesUnderPermissiveUmask`.
- [x] Two identities differing only in API domain, two differing only in environment, and two
      differing only in folder path, each produce two separate store files. (R12) Tests: `TestIdentitiesThatDifferGetSeparateFiles`.
- [x] A value resolved with an empty version token is stored with an empty token and served back
      with an empty token. (R10, R16) Tests: `TestStoreFallbackEmptyToken`, `TestFlushRecordsSuccessesWithTokens`.
- [x] A credential-sync lookup that succeeds writes nothing to the store, and one that fails as
      *unauthenticated* reads nothing from it. (R10) Tests: `TestStoreFallbackCredentialSyncIsNotWrapped`.
- [x] Folder paths `/a/b`, `a/b` and `/a/b/` map to the same store file. API domains
      `https://App.Infisical.com/api` and `https://app.infisical.com` map to the same store file.
      (R12) Tests: `TestIdentitySpellingsShareAFile`.
- [ ] With `XDG_STATE_HOME` unset, set to the empty string, and set to a relative path, the store
      is under
      `$HOME/.local/state/niwa/secret-cache/`. With the working directory, `XDG_CONFIG_HOME` and the
      instance root inside a git work tree and `XDG_STATE_HOME` outside one, no file is written
      inside the work tree. (R12) Deferred: `TestDir` covers the unset, empty and relative `XDG_STATE_HOME` cases and `TestDirIgnoresWorkTreeCwdAndConfig` the working directory and `XDG_CONFIG_HOME`, but no test puts the instance root inside a work tree during a run.
- [ ] Workspace A resolves keys X and Y from a folder, then workspace B resolves only X from the same
      folder: the store still holds Y. Two concurrent processes resolving X and Y respectively for one
      identity, with a test hook that pauses each writer between reading the file and renaming its
      update until both have read, leave both keys stored. With the lock disabled and the same
      hook, one key is lost, which shows the test detects the race. While a test holds the
      identity's lock, a run's store update waits, and it completes once the test releases the
      lock within 2 seconds. A lock held for longer than 2 seconds makes the run skip the update,
      print one warning, and provision successfully. While a test holds the lock, a logged-out run that
      serves from the store completes without waiting. (R10, R13) Deferred: merging, the race and the lock are pinned at store and session level (`TestUpdateMergesPerKey`, `TestConcurrentWritersKeepBothKeys`, `TestConcurrentWritersWithoutTheLockLoseAKey`, `TestUpdateWaitsForTheLock`, `TestUpdateGivesUpOnALockHeldTooLong`, `TestFlushLockTimeoutNotesAndContinues`, `TestLoadTakesNoLock`), not in a provisioning run that prints the warning and succeeds.
- [x] A successful export that lacks requested key X removes X from the store, and a following
      logged-out run does not serve X. A verified probe with an export failing "Response Code: 403",
      and separately "Response Code: 404", removes every key that run requested from that identity,
      and a following logged-out run serves none of them. (R11) Tests: `TestStoreFallbackEviction/missing_key`, `TestStoreFallbackEviction/answered_403`, `TestStoreFallbackEviction/answered_404`, `an answered <failure> evicts the folder's stored keys` (vault-fallback.feature).
- [ ] With a test hook that aborts an update after the temporary file is written and before the
      rename, the store file still holds its previous valid contents, and a following fallback serves
      them. (R13) Deferred: `TestAbortBeforeRenameKeepsThePreviousFile` shows the previous contents survive, but no following fallback run serves them.
- [ ] With `XDG_STATE_HOME` inside a directory that has a `.git` directory, and separately inside
      one that has a `.git` file (a linked worktree), a successful run creates no store file and
      prints one warning naming the directory, and a counting stub on `PATH` for the version-control
      tool records no invocation. (R14) Deferred: `TestWorkTreeGuard` (a `.git` directory and a `.git` file, with a counting stub) and `TestFlushInWorkTreeNotesOnceAndWritesNothing` pin it below the run, and "the hook's payload warns that the store is inside a git work tree" (vault-fallback.feature) covers a `.git` directory end to end; no full run covers the `.git` file case.
- [x] A store file with invalid JSON, and one with an unknown format version, are each treated as
      empty (the run behaves as with an empty store) and are rewritten by the next successful
      resolution. A store file with mode 0000, in a test running as a non-root user, is treated as
      empty. A store directory that cannot be
      written produces one warning and provisioning succeeds. (R15) Tests: `TestUnreadableDataFilesAreEmptyAndReplaced`, `TestDataFileWithNoPermissionsIsEmpty`, `TestUnwritableStoreDirectory`, `TestUnwritableStoreNotesOnceAndDisablesTheStore`, "the hook's payload warns that the store is unwritable" (vault-fallback.feature).

**Output and safety**

- [x] The R20 warning contains the provider kind, the API domain, the project ID, the reason, "may be
      stale", the age and `infisical login`. Ages of 1 minute and 1 hour read "1 minute" and "1 hour";
      ages of exactly 60 seconds and exactly 60 minutes read "1 minute" and "1 hour". With two keys
      stored 3 days and 5 days ago served for one identity, one warning prints and reads "5 days".
      When one identity's first failure in a run was unauthenticated and a later one timed out, the
      reason reads "logged out or expired". (R20) Tests: `TestRenderServedCarriesEveryR20Field`, `TestFormatAge`, `TestRenderOneWarningPerIdentityWithTheOldestAge`, `TestRenderFirstReasonWins`, `TestMappedFirstReasonWinsInTheRendering`.
- [x] Under the session-start hook, the R14, R15, R17 and R20 messages each appear in the
      `additionalContext` payload on standard output when their condition is set up and the hook
      provisions successfully. When a required key has no stored value, the hook provisions
      without it and the R17 line is in the payload; with strict mode on, the hook writes its
      strict-refusal payload, as today, and the R17 line is in that payload. (R21) Tests: "the hook's payload carries the stale-value warning", "the hook's payload says when nothing is stored to fall back on", "the hook's payload warns that the store is inside a git work tree", "the hook's payload warns that the store is unwritable" (vault-fallback.feature); `TestSessionStartNoticesGoIntoThePayload`, `TestSessionStartStrictRefusalCarriesNotices`.
- [x] A test using distinct marker secret values captures standard output, standard error and every
      file written during a fallback run, a successful run, a run that fails in strict mode with
      an empty store, and a run with a 404 answer. The markers appear only in the
      store files and the instance's materialized files, and no captured stream or file contains a
      `token` field taken from probe output. (R22) Tests: "secret values and the probe's token never leak" (vault-fallback.feature).
- [ ] Every export and probe subprocess started in the tests above has standard input connected to
      the null device and has no controlling terminal. The test runs niwa under a pseudo-terminal it
      allocates, and the stub records its standard input's device and fails to open `/dev/tty`. (R23) Deferred: `TestDefaultCommander_NoTerminalAccess` (Linux only) runs the subprocess runner under a pseudo-terminal with one stub; niwa itself is not run under one, and the functional stub does not record its standard input or try `/dev/tty`.
- [ ] `PRD-vault-integration`, the vault-integration design document and the guide each contain
      "Superseded by PRD-dispatch-offline-secrets." within the R29 and D-7 passages (between their
      headings and the next heading), and a search of the Go sources for "INV-NO-DISK-CACHE" or
      "vault-integration R29" finds only lines that also name this PRD. (R24) Deferred: no test; the notes and the source search were checked by hand.
- [ ] The vault-integration guide committed with this feature contains the R25 sentence verbatim.
      (R25) Deferred: no test; the sentence was checked by hand.
- [x] With a counting stub, a successful provisioning run makes the same number of export
      invocations as the code before this change, and no probe invocation. (R26) Tests: `TestGoldenSuccessfulRunInvocations`, `TestClassifySuccessRunsNoProbe`, "a successful apply makes one export per folder and no login status call" (vault-failure-baseline.feature).
- [ ] The full unit and functional suites are run with `HOME` and `XDG_STATE_HOME` pointing at two
      sentinel directories that do not exist and with no `infisical` binary on `PATH`; they pass,
      and afterwards neither sentinel directory exists. (R27) Deferred: enforced by the Linux CI job's sentinel steps rather than a test, and not run on macOS.

## Out of Scope

- **Keyring or OS secret-store storage, and encrypting the store.** The owner accepted plain
  text on disk, since the same values already sit in plain text in every instance. A keyring
  is a later enhancement.
- **Any change to how resolved values are written into instances** (which files, what format,
  what permissions).
- **Logging in or refreshing the Infisical session on the owner's behalf.** niwa keeps
  delegating authentication to the provider's CLI.
- **Falling back when machine-identity login fails.** If niwa's own universal-auth login cannot
  mint a token, provisioning fails as it does today (now within a bounded time, R8).
- **Provisioning without any network.** Creating an instance also refreshes the workspace and
  global configuration from their remotes, which needs the network whatever happens to
  secrets.
- **Vault backends other than Infisical.** They get only what the shared resolver provides
  for free. The classification in R1-R4 is Infisical-specific.
- **Changing what strict mode, the required-key check, or a missing key mean.** A stored value
  counts as supplied (R18); everything else is unchanged.
- **Status reporting from the store.** `niwa status --check-vault` keeps querying the provider
  directly and never reads the store.
- **Managing the store by hand.** No command lists, inspects or clears it, entries for
  providers no longer referenced are not pruned, and there is no size limit. The store holds
  only requested keys, so it stays as small as the configurations that use it.
- **Refreshing instances provisioned during a lapse.** Instances created on stored values keep
  them until they are next applied. niwa does not re-materialize them when the login returns.
- **A structured fallback signal.** The warning is text on standard error (or in the hook's
  payload). No machine-readable status is added for callers such as coordinator sessions.

## Known Limitations

- **The expired-user-session wording is inferred, not measured.** The strings "Your login
  session has expired" and a probe status of "expired" were read from the Infisical CLI binary
  and from machine-token experiments. A real expired user session wasn't reproduced. If the
  real wording differs, R2's probe still classifies it, since the probe reports session state
  rather than wording. If the probe also reported it unexpectedly, classification still fails
  open, toward serving stored values: rule 6 sends every failure without a usable answer (no
  server response, a timeout, a probe that gave no answer) to *unreachable*, which is served,
  and rule 4 sends every 401 or 403 the probe can't vouch for to *unauthenticated*, which is
  served and evicts nothing. For a CLI-session principal, the store is bypassed only for a
  server response other than 401 or 403, or for a 401 or 403 backed by a verified session. (For
  a minted principal every server response bypasses it (R3), and so does a missing client
  (R0).)
- **Classification errs toward serving stale values.** The same rules mean that a mistake in
  the reasoning behind them shows up as a stored value served with a warning, not as a failed
  run. A real refusal that coincides with a probe that times out or prints output niwa can't
  parse is served stale for that run (rule 4), as recorded below for permission denials.
- **Stored values can be arbitrarily old.** With no age ceiling (R19), a value rotated upstream
  keeps being served until the owner logs back in. The warning's age makes this visible but
  does not stop it.
- **An outage still stops machine-identity providers.** niwa's own universal-auth login runs
  before any export, and when the service is unreachable it fails as it does today (R8). The
  fallback covers an outage only for providers that use the CLI session or an ambient token.
- **"First listed" as the CLI's principal is measured on one session only.** On this host the
  probe listed exactly one session, the CLI's default profile. That the probe lists the effective
  principal first when several profiles exist is inferred from the CLI's behaviour, not measured.
- **The store is empty until a run succeeds on the new version.** A host upgraded and then left
  unattended before any successful provisioning has nothing to fall back on. R25 puts the
  priming step in the release notes; nothing enforces it.
- **A real expired user session was not reproduced.** An expired machine token was measured: the
  probe reports `expired` locally and the export gets a 403. Planting a fake expired user session
  into an isolated file vault failed because this CLI version's vault item format isn't
  documented. R2's rules 2, 4 and 5 are written so that an expired user session falls back
  whether the probe describes it, the export reaches the server, or neither.
- **A permission denial can be served from the store when the probe is inconclusive.** R2 rule 4
  treats a 401 or 403 as a lapsed login unless a verified probe says otherwise. If the probe
  times out at the same moment a real denial happens, the stale value is served for that run,
  with the warning. A 404 is never served.
- **The hook's failures stay silent to the operator.** When the session-start hook fails for
  any reason other than a strict-mode refusal, it writes no payload today, and this PRD doesn't
  change that.
- **Wording drifts across CLI versions.** R4's text markers track the pinned CLI version. R2's
  probe carries the classification when wording changes.
- **The work-tree check cannot see every setup.** A home directory managed as a bare-repository
  dotfiles checkout has no `.git` directory in its parent chain, so R14 cannot detect it.
- **CLI output is capped, not streamed.** niwa keeps at most 32 MiB of an export's or probe's
  standard output and 1 MiB of its standard error; the rest is discarded. An export that exits
  0 with more output than that is *answered* (like output niwa can't parse) and serves
  nothing. A probe that passes the cap gives no usable answer, so R2's later rules decide. A
  truncated standard error only loses its tail, which feeds messages and wording checks.
- **The store key has no principal.** A provider identity is kind, API domain, project,
  environment and folder path. Two principals that read the same folder share one store file,
  so when one principal's login lapses, niwa can serve values that another principal recorded
  for the same identity, including values the lapsed principal was never allowed to read.
- **A caller that doesn't collect notices falls back to the reporter.** The command surfaces
  each hand the run a notice collector. A caller of the provisioning code that doesn't still
  gets the warnings: the run collects them itself and writes them through its progress
  reporter (standard error by default) when it ends, rather than serving stale values
  silently.

## Decisions and Trade-offs

- **Fall back only for unauthenticated, timed-out and unreachable failures.** Alternatives were
  to fall back on any failure, or to fail hard on everything that isn't a clean resolution. A
  missing key or a denied read from an authenticated principal is a configuration change the
  owner needs to see; serving a stale value would hide it. Failing hard on everything is today's
  behaviour, which is the problem.
- **A 403 from an authenticated principal keeps today's tolerated mark** rather than becoming
  fatal. An earlier position made permission-denied fatal. That would have been stricter than
  today and added a new way for unattended runs to fail. The rule adopted instead: outside the
  fallback, behaviour stays exactly as it is.
- **With nothing stored, missing keys keep today's handling rather than always failing.** The
  framing this PRD absorbed treated a first run with nothing stored as a failure. R17 fails only where today's rules
  already fail and otherwise adds a message. Always failing
  would make optional keys fatal on a logged-out host, a new unattended failure that today's
  behaviour doesn't have.
- **"Today's handling" for an unstored key is strict mode alone, not a required declaration.**
  Earlier drafts of R17, its criteria, story 3 and the goals said a required key behind a
  logged-out or unreachable provider fails the run. The golden fixtures recorded before this
  change show otherwise: such a key is a reported mark, and only strict mode turns it into a
  failure, as it has since the key report was introduced. R17 keeps that behaviour rather than
  adding a new failure, so the text now says so, and the session-start hook's case follows: it
  provisions without the key, or in strict mode writes its strict-refusal payload, and the R17
  line travels in the payload either way.
- **Evict keys an authenticated run finds missing or denied.** Without eviction a key deleted
  upstream would stay in the store, and the next lapse would serve it. That's exactly the
  masking the fallback must never do.
- **Merge per key and lock per identity.** Several workspaces and concurrent dispatches write the
  same identity's entry. Replacing the whole entry would drop keys another workspace needs;
  merging without a lock would lose keys when two runs race. The lock is local and short-held.
- **Include the API domain in the identity.** A self-hosted instance can reuse a project ID
  from another domain; without the domain the two would share entries.
- **Keep the classification beside the existing "provider unreachable" condition.** The
  credential-sync soft path already keys on that condition and keeps working. A 403 also maps
  to it today, so the fallback must key on the new classification or it would serve stored
  values for a denial.
- **Detect a work tree by walking for `.git`, not by running a subprocess.** A subprocess would
  break R26 and add a dependency on the version-control tool being installed. The walk catches
  every ordinary checkout, including linked worktrees, whose `.git` is a file.
- **Classify with the probe's JSON, not its exit code, and not the export's text alone.** Every
  export failure exits 1, and a 403 is ambiguous between a broken session and a real denial.
  The probe exits 0 when offline, so only its fields separate "logged out" from "offline".
  Text markers stay as a secondary signal.
- **The deciding session is the CLI's own principal, not a domain match.** An earlier draft
  picked the probe session whose domain matched the domain niwa derives for the provider. The
  export, though, runs as whatever principal the CLI is configured for, and the probe reports
  exactly that principal. On a host whose CLI is logged in to a domain niwa doesn't derive (a
  self-hosted instance with no `api_url` declared), domain matching found no session, rule 5
  classified every 403 as a lapse, and a revoked grant was served on every run. Taking the
  first listed session removes the comparison, and with it that failure.
- **Only a 401 or 403 server response can become a fallback, and only when the probe doesn't
  vouch for the session.** When the service answered with anything else, the safe reading is
  that it meant what it said: a 404 or a 5xx is never served from the store, whatever the probe
  reports. A 401 or 403 is ambiguous between a lapsed session and a real denial, so a conclusive
  probe keeps it *answered* and anything less falls back. That covers an expired session whose
  request reaches the server even when the probe can't describe it, at the cost recorded under
  Known Limitations.
- **Store under `$XDG_STATE_HOME`, not the config directory or a cache directory.** The XDG
  state directory is meant for data that persists but isn't configuration, and cache cleaners
  don't touch it. The niwa config directory holds a git-synced clone of the global
  configuration, where a misplaced path could commit secrets.
- **Key the store by provider identity and keep only requested keys.** Two workspaces reading
  the same Infisical folder would get the same values from the provider anyway, so sharing
  can't cross-contaminate. One mechanism then covers every configuration layer. Storing the
  whole folder would put more plaintext on disk than the instances hold.
- **Stored values satisfy strict mode.** Otherwise a strict workspace would still stop
  unattended. The warning still prints.
- **No age ceiling.** A ceiling shorter than an unattended absence defeats the purpose, and any
  fixed number is arbitrary. The warning shows the age instead.
- **Timeouts of 30 s for export and 15 s for the probe.** Healthy round trips measured under
  0.9 s for the export path and under 0.25 s for the probe. The CLI's own behaviour on a
  dropped route is 30 to 61 s per export, and the session-start hook is killed at 180 s. 30 s
  gives more than 30x headroom while keeping one export plus one probe well inside the hook's
  budget. 15 s sits above the probe's own 10 s network cap, so niwa normally reads the probe's
  explicit "network error" answer rather than timing out.
- **The worst case is summed across the whole run, not per call.** One provisioning run makes
  its vault calls in sequence: the credential-sync lookup, then the workspace overlay, the team
  workspace and the personal global layers, one export per provider folder. Without R9 a
  dropped route could cost 30 s per folder plus a probe each time, which would pass 180 s with
  four folders. With R9 the first timeout marks the service unreachable. The remaining calls go
  straight to the stored copy, so the vault part of a run is bounded at 45 s. A machine-identity
  login on the same dropped route adds at most 30 s before it fails as today (R8), 75 s in total.
  Dispatch provisions one instance per run, so there is no per-instance multiplier. Machine
  identities re-authenticate from their stored client credentials on every run, and the minted
  token is held only in memory, so its lifetime can't lapse during an absence.
- **Credential-sync lookups are reclassified, not cached.** Caching machine-identity credentials
  on disk would contradict `PRD-machine-identity-vault-sync` R6. Reclassification lets the
  existing soft path take over.
- **niwa's own GitHub calls are unchanged.** niwa never puts resolved secrets into its own
  process environment (`PRD-vault-integration` R28), so a vault-sourced GitHub token only
  reaches instances. Serving it from the store covers story 4 without changing token lookup.
