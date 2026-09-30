---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-dispatch-offline-secrets.md
problem: |
  Vault resolution runs `infisical export` with no time bound and treats every failure it
  doesn't recognise as fatal. The logged-out wording of the current CLI isn't recognised, so
  a lapsed login aborts every provisioning path, and niwa keeps no copy of previously resolved
  values to fall back on.
decision: |
  The Infisical backend bounds every export and probe, runs them without a terminal, and
  classifies each failure as unauthenticated, unreachable or answered, carried on the error. A
  decorator wrapped only around the three provisioning bundles records successful values in a
  hashed-JSON store under the XDG state directory and serves them only for unauthenticated or
  unreachable failures, and a notice collector beside the key report tells the operator.
rationale: |
  Classification stays in the backend and policy above it, resolveOne is untouched, and the
  decorator returns the identical error on every path it doesn't serve, so today's handling of
  answered failures holds by construction. Unwrapped providers keep credential sync, status and
  onboard away from the store, and the separate notice type keeps strict mode from miscounting.
---

# DESIGN: Provisioning keeps working when the vault login lapses

## Status

Planned

## Context and Problem Statement

Every provisioning command resolves `vault://` references in one place. `realProvisionInstance`
(shared by `niwa dispatch`, the session-start hook and `niwa watch`) and the `create`, `apply`,
`init` and `reset` commands all reach `Applier.runPipeline` in `internal/workspace/apply.go`.
That pipeline resolves three configuration layers against their own provider bundles: the
workspace overlay, the team workspace configuration, and the personal global override. For
each `vault://` value, `resolveOne` in `internal/vault/resolve/resolve.go` calls
`Provider.Resolve`. The Infisical backend loads a whole folder per (project, environment,
path) with one `infisical export --format json` subprocess, keeps it in memory for the run,
and serves keys from it.

Failures are sorted by sentinel errors. `ErrKeyNotFound` and `ErrProviderUnreachable` (with
its narrower `ErrClientNotInstalled`) become *marks*: empty values that the post-merge key
report collects. Strict mode makes any mark fatal; without it only a missing required key is
(a required key behind an unreachable provider is reported, not fatal). Anything else is
a hard error. The Infisical backend maps a non-zero export exit to `ErrProviderUnreachable`
only when scrubbed stderr contains one of a fixed set of markers, which include "401", "403"
and "forbidden" but none of the current CLI's logged-out messages. A lapsed login is therefore
a hard error on every path, while a 403 of any kind is a tolerated mark.

The same lapse hits the credential-sync lookup (`credentialpool.lookupVault`), which reads
machine-identity credentials through the personal global provider and softens only
`ErrProviderUnreachable`. On the host that motivated this work, that provider uses the CLI
user session and a machine identity covers the workspace's own provider. So credential sync
and the personal keys (a GitHub token among them) fail on a lapse, and the workspace keys
don't.

Nothing on this path has a deadline. The root command runs with `context.Background()`, the
export subprocess inherits it, and niwa's own universal-auth HTTP call uses
`http.DefaultClient`, which has no timeout. When the CLI is offline it takes 30 to 61 seconds
per export to give up on a dropped route, and the session-start hook is killed after 180
seconds.

The accepted PRD (`PRD-dispatch-offline-secrets`) sets what the design has to meet:

- **Classification (R0 to R6).** A failed export is *unauthenticated*, *unreachable* or
  *answered*. For a CLI-session principal (the stored login, or an inherited
  `INFISICAL_TOKEN`), the decision comes from an `infisical login status --json` probe
  run after the failure. The probe's session fields are read, never its exit code. A server
    response other than 401 or 403 is always *answered*. The deciding session is the one the
  probe lists for `INFISICAL_TOKEN` when that is set, else the first session listed, which is
  the principal the CLI itself uses; domains are never compared. A 401 or 403 is *answered* only when the
  probe vouches for the session (status `authenticated`, verification `verified`); otherwise it
  is *unauthenticated*. For a token niwa minted itself from a machine identity, there is no
  probe: any server response is *answered*, and anything else is *unreachable*. The CLI's three
  logged-out wordings are a secondary signal when the probe is unusable. A missing binary keeps
  today's "client not installed" handling. The classification travels next to the existing
  "provider unreachable" condition, so credential sync softens as before while the fallback
  keys on the classification alone. *Answered* failures keep exactly today's handling.
- **Bounds (R7 to R9).** Export 30 s, probe 15 s, universal-auth login 30 s. A timed-out call
  is terminated with any child holding its output, within 5 s. Once a run has classified a
  domain *unreachable*, later exports and credential-sync lookups against that domain skip the
  subprocess. Once it has classified a CLI-session principal *unauthenticated*, later
  CLI-session calls on that domain do the same. The vault part of a run is then bounded near
  45 s, or 75 s with a timed-out login.
- **The store (R10 to R15).** Each successful resolution records key, value, resolution time
  and version token under a provider identity: kind, API domain, project ID, environment and
  effective folder path. Updates merge per key. An export that lacks a requested key, or a
  403/404 *answered* failure for the folder, evicts those keys. The store lives at
  `$XDG_STATE_HOME/niwa/secret-cache/` (or `$HOME/.local/state/...`), with the directory at 0700
  and files at 0600. Writes are atomic, serialised per identity by a lock whose wait is bounded
  at 2 s, and skipped with a warning when the directory is inside a git work tree (checked by
  walking up for `.git`, no subprocess). Unreadable files count as empty, and write failures
  only warn. Credential-sync lookups and `niwa status --check-vault` never touch the store.
- **Fallback (R16 to R19).** An *unauthenticated* or *unreachable* key with a stored value is
  served as if fresh, with its stored version token, in every layer, and is not written back.
  One with no stored value keeps today's mark and prints a "nothing to fall back on" line.
  Stored values satisfy strict mode and have no age limit.
- **Reporting (R20 to R23).** One warning per provider identity served from the store, with the
  identity, the reason ("logged out or expired", "timed out", "unreachable"), "may be stale",
  the age of the oldest value, and `infisical login`. Messages go to stderr, and for the hook
  into its `additionalContext` payload whenever it writes one. No secret value or raw probe
  output ever reaches output. Every export and probe runs with stdin on the null device and no
  controlling terminal.
- **Records and tests (R24 to R27).** The work supersedes `PRD-vault-integration` R29 and D-7,
  with notes in that PRD, its design and its guide. The guide gains a priming instruction. A
  healthy run adds no subprocess or network call. Tests never touch the operator's store or
  real CLI.

## Decision Drivers

- **Nothing outside the fallback changes.** Every *answered* failure, every missing-key mark
  and every strict-mode or required-key outcome must match today's behaviour byte for byte, and
  golden fixtures prove it. The design should change as little of the existing resolution
  path as it can.
- **Every layer, one mechanism.** The workspace, overlay and personal global layers resolve
  through separate bundles. The fallback has to cover all three the same way and see which
  key each call requested.
- **Classification belongs to the backend; policy belongs above it.** Only the Infisical
  package knows the CLI's wording, the probe and the principal type. Whether to serve a stored
  value is policy that shouldn't live in a backend.
- **Unattended safety.** Nothing may prompt, hang, or exceed the hook's 180-second limit. Every
  wait has a bound, including the lock and the HTTP login.
- **Secret hygiene.** Plain text on disk is accepted, but only in the store and in instances.
  The store must never land in a git work tree or in the git-synced config directory. Probe
  output must never be echoed, since it carries a token.
- **Testability without the real CLI.** The existing seams (the `_commander` injection, the
  fake vault backend, the functional shell stub) should carry every test the PRD asks for,
  including timeouts that don't cost the suite 30 seconds each.
- **Portability.** niwa ships for Linux and macOS, and file locking and process-group handling
  must work on both.

## Considered Options

### Decision 1: Where the fallback and the store writes live

Every provisioning run touches the vault in four places inside `Applier.runPipeline`, in this
order. First credential sync opens its own provider from the personal configuration and calls
`Resolve` through `lookupVault`. Then three resolve passes run, one per layer: the workspace
overlay, the team workspace, and the personal global override. Each has its own bundle, and all
of them reach the backend through the single `provider.Resolve` call in `resolveOne`. Three
callers must never touch the store: credential sync, `niwa status --check-vault`, and onboard's
`CheckProviderAuth`. The fallback has to key on the classification alone, see each requested key
and its provider identity, cover the three layers the same way, and leave every *answered*
failure exactly as today.

Key assumptions:

- `runPipeline` is the only production path that resolves vault references for provisioning. A
  new provisioning path would have to build its bundles through the same wrapping helper to get
  the fallback.
- niwa derives the API domain for the identity itself (the provider's `api_url`, then
  `NIWA_INFISICAL_API_URL`, then the cloud default), because `infisical export` is not passed
  `--domain`. The identity only has to be stable from run to run on one host, which a derived
  value is.
- An *answered* 403 or 404 applies to the whole folder, so the whole identity's entry is evicted.
  That removes every key the run requested (what the PRD's eviction rule, R11, asks for) and
  also the identity's other stored keys. The broader reading closes a gap: a 404 is today's hard
  error and stops the resolve walk at the first key, so per-key eviction would leave the folder's
  later keys stored and servable on the next lapse.

#### Chosen: an opt-in provider decorator, wrapped at build time on the three provisioning bundles

Classification stays in the backend. The Infisical package classifies each export failure (the
probe run after the failure, the minted-versus-session principal, the logged-out wordings as a
secondary signal) and returns its error inside a `*vault.ClassifiedError{Err, Class}` carrier.
`Error()` returns `Err.Error()` unchanged, and `Unwrap() []error` returns `Err` and the
`*vault.FailureClass{Class, Reason, HTTPStatus}`. So `errors.Is` still finds every sentinel `Err`
carried, `errors.As` finds the class, and the text is byte for byte today's. `errors.Join` or an
extra `%w` would change that text, which is why the carrier is a named type. The choice between
`ErrProviderUnreachable` and a plain error for an *answered* failure is also today's. A
principal is **minted** when the provider's config holds a token niwa obtained from a machine
identity; otherwise it is a CLI-session principal. `Factory.Open` starts reading `api_url` from
the provider config, but only to compute the API domain for the store identity; nothing is
passed to the CLI. *Unauthenticated* and
*unreachable* failures always wrap `ErrProviderUnreachable`, which is what R5 needs: credential
sync already softens that sentinel, so it keeps its soft path with no code change. `ErrClientNotInstalled`
never carries a class, so a missing binary keeps today's handling and is never served (R0).

The R9 short-circuit lives in a run state that `runPipeline` attaches to the provisioning
context. It has three operations. `MarkUnreachable(domain, reason)` and
`MarkUnauthenticated(domain)` record a verdict, and `Check(domain, minted bool)` returns the
recorded verdict, if any: a domain marked unreachable applies to every principal, and one marked
unauthenticated applies only to CLI-session principals. `vault.RunStateFrom(ctx)` returns nil when
no state is attached, as on the status and onboard paths, and a nil state answers "nothing
recorded", so those paths behave exactly as today. When a recorded verdict applies, the provider
skips the subprocess and returns `infisical: export for <domain> skipped: an earlier call in this
run <was logged out or expired | timed out | was unreachable>`, wrapping `ErrProviderUnreachable`
in the same carrier with the recorded class and reason. *Answered* failures are never recorded or
cached, so the number of export subprocesses for them stays what it is today.

Policy sits in a decorator in a new `internal/vault/storefallback` package. The wrapper implements
`vault.Provider`, makes exactly one inner `Resolve` call per reference, and applies one pure policy
function to the result:

- success: record key, value, resolution time and version token for the identity, then return the
  inner result;
- `ErrKeyNotFound`: record an eviction of that key (R11);
- *answered* with HTTP 403 or 404: record an eviction of the whole identity (R11, as interpreted
  above);
- *unauthenticated* or *unreachable* with a stored value: return the stored value, wrapped as a
  `secret.Value`, with the stored `VersionToken`. Note the identity as served, with the reason and
  resolution time, and don't write it back (R16);
- *unauthenticated* or *unreachable* with nothing stored: note a miss (R17) and return the original
  error;
- anything else: return the original error value untouched (R6).

`resolveOne` doesn't change. A served value takes its existing success branch: redactor
registration, the version token in instance state, and counting as supplied for strict mode and the
required-key check (R18). Every other path produces exactly today's mark or error, because the
decorator hands back the identical error value, which a table test can assert by pointer identity.
The wrapper is applied only where the three provisioning bundles are built, through one helper in
`apply.go`. Credential sync, status and onboard open their own providers, which are never wrapped,
so the exclusion comes from which instances exist rather than from anyone remembering a flag.

#### Alternatives Considered

**Resolver hook in `resolveOne`**: call the same policy function right after `provider.Resolve`,
with the store session in `ResolveOptions` or on the context. This was a close second. Rejected
because it edits the resolution path the drivers ask to leave alone, and its context-carried form
fails open: any future resolver caller running on the provisioning context would silently read and
write the store. Its explicit form needs the same three opt-in sites as the decorator plus a
`resolve.go` change.

**Inside the Infisical provider**: read a store handle at `Factory.Open` and serve from inside
`Resolve`. Rejected because it puts serving policy in the backend, against the driver that keeps
classification in the backend and policy above it. It also can't be exercised through the fake
backend, so every workspace-level test would need scripted CLI output.

**Post-merge fill over classified marks**: record the classification on each `MaybeSecret`, then
fill marked slots from the store after the layers merge. Rejected as the largest change: a new field
every deep copy must carry, four hook points, and eviction handled at error sites. It also fails
silently if the fill ever walks fewer slots than the resolver does. Its one gain, reporting only
values that survive the merge, doesn't justify that, since the PRD words its messages per reference.

### Decision 2: The store's format, naming, locking and work-tree check

The PRD fixes the store's behaviour. There is one file per provider identity under
`$XDG_STATE_HOME/niwa/secret-cache/`, carrying a format version, with the directory at 0700 and
files at 0600 (R12). Updates merge and evict per key through a temporary file and a rename, and
they're serialised per identity across processes by a lock whose wait is capped at 2 seconds.
Readers take no lock (R13). An unreadable or unknown file counts as empty, and write failures only
warn (R15). A `.git` walk with no subprocess guards against writing into a work tree (R14). niwa
already ships every mechanism this needs: a `syscall.Flock` lock polled to a deadline on a dedicated
lock file (the Codex trust lock), at least six temp-and-rename writers, and a `.git` walk in the
functional suite. One constraint is easy to miss: `secret.Value` marshals to `"***"` in JSON and
refuses gob, so the store needs its own record type, filled through `reveal.UnsafeReveal`.

Key assumptions:

- The state directory is on a local filesystem where `flock` works. Where it doesn't (some NFS
  setups), each run skips the store and warns once, which is R15's behaviour for a failed write.
- A home directory tracked through a bare dotfiles repository has no `.git` entry to find. The PRD
  accepts that limit.

#### Chosen: one versioned JSON file per hashed identity, guarded by a lock file

The canonical identity is the five normalised fields encoded as a JSON array, so a separator inside
one field can't make two tuples collide. Its SHA-256 in lower-case hex names three files:
`<hex>.json` (data, 0600), `<hex>.lock` (empty, 0600, never deleted), and transient
`.<hex>.json.tmp-*` files. Hashed names stay within name-length limits, carry no unsafe characters,
and stay distinct on macOS's case-insensitive APFS, where readable names for `Prod` and `prod` would
share a file.

The data file is one JSON object:

```json
{
  "format_version": 1,
  "identity": {"kind": "infisical", "api_domain": "https://app.infisical.com",
               "project_id": "...", "environment": "dev", "folder_path": "/"},
  "keys": {
    "GITHUB_TOKEN": {"value": "<base64>", "resolved_at": "2026-09-29T10:00:00Z",
                     "version_token": "...", "provenance": "..."}
  }
}
```

`value` is a byte slice, so it round-trips exactly. `resolved_at` is RFC 3339 in UTC and supplies
the warning's age. `version_token` and `provenance` are the two halves of `vault.VersionToken`. `Update` reports why
it didn't write through three sentinel errors, `store.ErrLockTimeout`, `store.ErrInWorkTree` and
`store.ErrUnwritable`, so the session can pick the matching notice. The package holds no state, and
nothing caches the work-tree verdict: `Update` walks for `.git` before every write it makes. A
file whose echoed identity doesn't match its name, whose `format_version` isn't 1, or that can't be
read or parsed is treated as empty and overwritten by the next successful write.

The directory comes from `XDG_STATE_HOME` when it is set and absolute, else `$HOME/.local/state`,
then `niwa/secret-cache`. It is created 0700. The store is trusted only when the current user owns it
exclusively. Before any read or write, `Lstat` must show a real directory (not a symlink) owned by
the effective user, not writable by group or others. A looser mode is tightened with `Fchmod` on the
opened directory, and only after ownership is confirmed. After the directory checks pass, every
file operation goes through an `os.Root` opened on the verified directory, so a directory swapped
after the check can't redirect a read or a write. Data and lock files are opened with
`O_NOFOLLOW`, and `Fstat` must show a regular file owned by the effective user with no group or
other bits. A data file larger than 1 MiB is treated as unreadable. Any failed check makes the
store empty and unwritable for the run, with the "store unwritable" warning.

An update, all inside the lock:

1. open `<hex>.lock` and try `Flock(LOCK_EX|LOCK_NB)` every 20 ms until 2 s pass (on timeout: skip
   the update and warn once);
2. read and parse the data file (missing or bad counts as empty);
3. apply this run's evictions, then its puts, leaving every other key alone, and stop if nothing
   changed;
4. remove leftover temp files from an earlier crash;
5. write a temp file in the same directory, `Sync`, close, `Chmod(0o600)`;
6. `Rename` it over the data file.

Test hooks pause after step 2 and abort between steps 5 and 6. A fallback read opens the data file
with no lock, and since writers only rename complete files into place, it always sees one whole
version.

Before each write, inside `Update` and so once per identity a run writes, niwa walks from the
store directory to the root, running `os.Lstat(dir/.git)` at each level. It walks twice: once
over the cleaned path as configured (which need not exist yet), and once over the
symlink-resolved path of its deepest existing ancestor. Any `.git` entry, file or directory,
makes `Update` return `ErrInWorkTree`; the session then stops flushing, so the run writes
nothing more and prints one warning.

#### Alternatives Considered

**Lock the data file itself**: same format, no separate lock file. Rejected because each rename
replaces the inode a waiter locked, so a waiter can take the lock on a replaced file and merge
against stale contents.

**Create-exclusive lock file with staleness detection**: portable without `flock`. Rejected because
a holder that dies (the hook is killed at 180 s) leaves the lock behind, and recovering means waiting
past the 2-second bound or breaking the lock on a guess, which is itself a race. `flock` is released
by the kernel when its holder dies.

**`fcntl` record locks or Linux open-file-description locks**: also kernel-released. Rejected
because classic record locks belong to the process (in-process holders don't contend, and closing
any descriptor drops the lock), and open-file-description locks don't exist on macOS.

**One file per key with no lock**: rejected because R12 asks for one versioned file per identity,
evicting a whole identity becomes a series of non-atomic unlinks, and key names would need their
own hashing and case handling.

### Decision 3: How the messages reach the operator

The fallback produces the "may be stale" warning (one per identity served from the store), the
"nothing to fall back on" line (one per identity with an unstored key), and three store warnings
(the store is inside a work tree, the lock wait ran out, the store is unwritable). R21 routes them
to standard error for `dispatch`, `create`, `apply`, `init`, `reset` and `watch`. For the
session-start hook they go into the `additionalContext` payload whenever the hook writes one, and to
the hook's standard error when it fails in any other way. R18 forbids strict mode and the required-key
check from counting them as shortfalls. The warning needs the first reason and the oldest resolution
time per identity, so it can only be written once resolution has finished.

Key assumptions:

- On the hook's success and strict-refusal paths the messages go only into the payload, not also
  to standard error, so R20's "exactly one warning" holds per run.
- Rendering the messages after the key report is acceptable; the PRD checks their presence, not
  their position.

#### Chosen: a dedicated run-scoped notice collector beside the key report

A new stdlib-only leaf package, `internal/fallbacknotice`, holds a nil-safe, mutex-guarded
`Collector`, a sibling of `keyreport.Collector` rather than part of it. It records
`Served(identity, reason, resolvedAt)` (the first reason and the earliest time win),
`NothingToFallBackOn(identity)`, `LockTimeout(identity)`, `StoreInWorkTree(dir)` and
`StoreUnwritable(dir)`. Its `Identity` is its own struct, so it imports nothing from `vault`, and no
method accepts a secret value or CLI output, so R22 holds by construction. It takes a clock, so ages
are testable without sleeping. `RenderText` produces `warning: ` lines for terminals, and
`RenderContext` produces sentences for the agent's context, sanitised the way the key report
sanitises, caps each identity field at 200 characters, and asks the agent to tell the operator to run `infisical login` rather than run it
itself. Both return the empty string when nothing was recorded, so a run with no fallback prints
exactly what it prints today.

The wiring copies the key report's. `Applier` gains a `Notices` field that `runPipeline` hands to the
fallback session. `wireKeyReport` attaches a notice collector and renders it after the key report,
which covers `create`, `apply`, `init` and `reset`. `realProvisionInstance` creates one, and
`provisionResult` carries it on success and on `Create` failure. `dispatch.go` and `watch.go` render
it after the key report. The hook's two payload builders append its context rendering. The hook prints its text rendering
to standard error at each of its six other failure returns: a provisioning error, a failed
session-mapping write, a failed payload build or write on the success path, and a failed payload
build or write on the strict-refusal path. Strict mode reads only `a.Keys`, so
nothing in the notice collector can ever count as a shortfall.

#### Alternatives Considered

**Reporter warnings, with a captured Reporter on the hook**: emit through `Applier.Reporter` where
each event happens. Rejected because it can't produce the oldest age and first reason per identity,
and a captured hook Reporter would pull every other Reporter line into the payload. It also writes to
the process's standard error rather than the command's writer, and the resolver has no Reporter.

**A second list on `keyreport.Collector`**: reuse the collector that's already threaded everywhere,
with notices excluded from `Report()`. The runner-up. Rejected because it contradicts that package's
stated scope and would leave strict-mode safety resting on every future reader knowing that one list
doesn't count.

**Return the notices up the call chain**: rejected because `Create` deletes the instance and returns
a bare error on failure, so the messages would be lost on exactly the path where the "nothing to fall
back on" line has to appear.

### Decision 4: Bounding the vault calls and keeping them non-interactive

Nothing on the resolution path has a deadline, and `defaultCommander.Run` passes the root
`context.Background()` straight to `exec.CommandContext` without `WaitDelay` or `SysProcAttr`. The PRD
bounds exports at 30 s, probes at 15 s and the universal-auth login at 30 s. A timed-out call must
return within 5 s with any child holding its output gone (R7, R8), every subprocess must run with
stdin on the null device and no controlling terminal (R23), and a test-only setting must be able to
shorten the bounds (R27). A throwaway harness measured the standard library's options, with a stub
standing in for the CLI:

- **Default cancel:** with `Setsid` and `WaitDelay`, the default cancel left a forked child alive
  that was holding stdout.
- **Group kill:** killing the whole process group, plus one more group kill after `Wait`, ended
  every case within the bound plus `WaitDelay`, with every child gone.
- **Terminal access:** under a pseudo-terminal, only a `Setsid` child couldn't open `/dev/tty`.
- **Latent bug:** a child killed by its context would come back from `Run` as exit code -1 with no
  error, which today's code reports as a generic hard error.

Key assumptions:

- The real CLI doesn't detach its helpers into a new session. One that did would escape the group
  kill, though `WaitDelay` would still cut its pipes so the call returns on time.

#### Chosen: deadlines at each call site, a process-group-aware commander, and a clamped test override

`runInfisicalExport`, the probe and `authenticateHTTP` each derive `context.WithTimeout` from the
caller's context, using the constants `exportTimeout` (30 s), `probeTimeout` (15 s) and `loginTimeout`
(30 s) read through one `callBound` helper. The `commander` interface doesn't change, so every
existing fake still compiles; a fake that simulates a hang blocks on the context. Right after `Run`
returns, the call site checks whether its own deadline fired (or `Run` returned `exec.ErrWaitDelay`)
before the existing "client not installed" and exit-code branches. A timeout goes to the classifier
as *unreachable*, reason "timed out". A login timeout is wrapped as `infisical: universal-auth login to
<api-url> timed out after 30s`, which keeps it on today's login-error path.

`defaultCommander.Run` makes these changes:

- sets `Stdin` explicitly to the null device;
- sets `SysProcAttr{Setsid: true}`, so the child leads a new session with no controlling terminal;
- sets `Cancel` to SIGKILL the whole process group;
- sets `WaitDelay` to 3 s;
- after `Run` returns, sends one more group SIGKILL, ignoring "no such process", but only when the
  deadline fired or `Run` returned `exec.ErrWaitDelay`. A normal run never signals a process group
  ID that might have been recycled.

`callBound` reads `NIWA_TEST_VAULT_TIMEOUT` and uses it only when it parses as a duration of at
least 50 ms that is shorter than the default. A stray setting can therefore shorten a bound but never
lengthen one. niwa prints a warning once per run whenever the override is in effect. No
platform-specific file is needed: `Setsid` and negative-PID kills behave the same on Linux and macOS,
and `dispatch_launcher.go` already sets `Setsid` in an untagged file.

#### Alternatives Considered

**Standard-library `WaitDelay` with the default cancel**: rejected because the harness showed the
forked child surviving the call, which fails R7's check that its process ID is gone.

**A hand-written supervisor** (start, a timer that kills the group, niwa-owned pipe copiers):
rejected because it re-implements `Cmd.Cancel` and `Cmd.WaitDelay` with more code and more room for
races.

**External wrappers (`timeout`, `setsid`)**: rejected because neither ships on stock macOS, which
would add a system dependency.

**Bounds inside the commander, or a `Timeout` on the shared HTTP client**: rejected because the first
changes an interface every fake implements, and the second would also bound the interactive
onboarding calls this PRD doesn't touch.

## Decision Outcome

**Chosen:** the provider decorator (Decision 1), the hashed JSON store with a lock file (Decision 2), the notice collector (Decision 3) and call-site bounds (Decision 4).

### Summary

The Infisical backend learns to tell three kinds of failure apart. Every export and probe now runs
with a deadline, in a new session with no terminal, and is killed as a process group when its time
is up. When an export fails, the backend classifies it. For the operator's own login or an inherited
`INFISICAL_TOKEN`, it runs `infisical login status --json` and picks the deciding session: the one
whose `tokenSource` names the environment variable when that is set, else the first session
listed, which is the principal the CLI itself uses; domains are never compared. It then applies the PRD's ordered rules:

1. a server response other than 401 or 403 is *answered*;
2. an `expired` or `rejected` session, or a malformed token, is *unauthenticated*;
3. a 401 or 403 is *answered* when the probe vouches for the session;
4. a 401 or 403 is *unauthenticated* when the probe doesn't vouch for it;
5. parseable output with no deciding session is *unauthenticated*;
6. everything else is *unreachable*.

For a token niwa minted itself, it skips the probe: any server response is *answered*, and anything
else is *unreachable*. The three logged-out wordings decide only when the probe timed out or printed
garbage and the export wasn't a server response. The HTTP status is taken only from a line that
starts with the CLI's own `Response Code: <n>` format, never from other text the server echoed.
The probe's JSON is decoded into a struct that has no `token` field. The result rides on the error as a `FailureClass`.
*Unauthenticated* and *unreachable* failures wrap `ErrProviderUnreachable`, so credential sync softens
them as it always has. A run-scoped state carried on the provisioning context remembers an
*unreachable* domain, or an *unauthenticated* CLI-session principal, and replays that verdict without
running another subprocess. That caps a dead route at one export bound plus one probe bound per run.

Above the backend, each of the three provisioning bundles (overlay, team, personal) is built with its
providers wrapped in a store-fallback decorator. The decorator records every successful value, evicts
keys the provider says are gone, serves stored values only for *unauthenticated* or *unreachable*
failures, and passes every other result through untouched. Its records are buffered in a run session
that `runPipeline` creates next to the redactor. A deferred flush writes them, one locked update per
identity, to the store: hashed JSON files under `$XDG_STATE_HOME/niwa/secret-cache/`. The flush runs
even when a later step fails, so values that resolved still get stored. Each write
checks for a `.git` entry above the store, and the first one found stops the run's writes.

What the session served, missed or couldn't write goes into a notice collector owned by the command,
next to the key report. Each surface renders it after the key report: to standard error for the
terminal commands, and into the hook's `additionalContext` payload for the session-start hook. The
warning a served identity produces reads:

```
warning: using stored values that may be stale for infisical project <project-id> (env <env>, path <path>, <api-domain>): the provider is logged out or expired; the oldest value is <age> old. Run `infisical login` to refresh them.
```

The reason clause is `is logged out or expired`, `timed out`, or `is unreachable`. The line for an
identity with nothing stored reads:

```
warning: infisical project <project-id> (env <env>, path <path>, <api-domain>) could not be used and no previously resolved value exists to fall back on. Run `infisical login`.
```

The stable text a release check can grep for is `may be stale` together with ``Run `infisical login` to
refresh them``.

### Rationale

Classification travels on the error, so the
decorator needs no knowledge of Infisical and credential sync needs no change. The decorator hands
back the identical error value on every non-served path, so byte-for-byte preservation of *answered*
handling holds by construction rather than by care. The session buffers and flushes per identity, so
each identity costs at most one bounded lock wait, which keeps the store inside R9's budget. The
notice collector is its own type, so strict mode can't miscount a served value, and the collector's
empty rendering means a healthy run prints exactly what it prints today.

The trade-offs accepted:

- **Three opt-in wrap sites instead of one**, guarded by per-layer tests and a check that `apply.go`
  builds no provisioning bundle outside the helper.
- **Whole-identity eviction on a folder-level 403 or 404.**
- **A test-only environment variable read in production code**, clamped to shorten only.
- **Kernel `flock` as the concurrency primitive**, which some network filesystems don't honour. The
  store then warns and stays read-only.

## Solution Architecture

### Overview

The feature adds four things to the existing resolution path and changes one. It adds a
classification that travels on errors, a store fallback that wraps providers only where
provisioning builds its bundles, a store on disk, and a notice collector next to the key report.
It changes how the Infisical subprocess runs: every call is bounded, runs without a terminal, and
dies as a process group when its time is up. None of the four new pieces is reachable from
credential sync, `niwa status --check-vault` or onboard, and none is consulted when resolution
succeeds, beyond one buffered record per resolved key.

### Components

```
internal/vault
  errors.go          + FailureClass{Class, Reason, HTTPStatus}; Class = ClassUnauthenticated|ClassUnreachable|ClassAnswered
                       Reason = ReasonLoggedOut|ReasonTimedOut|ReasonUnreachable
  provider.go        + StoreIdentifier interface: StoreIdentity(ref Ref) (Identity, bool)
                       + Identity{Kind, APIDomain, ProjectID, Environment, FolderPath}
    registry.go        + (*Bundle).Wrap(wrap func(Provider) Provider) *Bundle: a new bundle over wrapped
                       providers
    runstate.go        + run-scoped R9 state on ctx: WithRunState, RunStateFrom (nil-safe),
                       MarkUnreachable, MarkUnauthenticated, Check
                     + ClassifiedError carrier (Error = inner text; Unwrap = inner, class)
internal/vault/resolve
  resolve.go         unchanged (the helper in apply.go calls BuildBundle, then Bundle.Wrap)

internal/vault/infisical
  subprocess.go      defaultCommander.Run: null stdin, Setsid, group-kill Cancel, WaitDelay 3s,
                       post-Wait group kill; runInfisicalExport: bounded ctx, timeout check first,
                       classifier call on failure
    session.go         extend the existing `login status --json` decoder (DetectSessionStatus's
                       struct) with tokenSource, verification.state; still no token field
  classify.go        + bounded probe call using that decoder, ordered rules, wording markers,
                       ClassifiedError construction
  bounds.go          + exportTimeout/probeTimeout/loginTimeout, callBound (NIWA_TEST_VAULT_TIMEOUT)
    infisical.go       + StoreIdentity(ref); api_url read at Open for the identity only;
                       consults run state before starting a subprocess
  auth.go            authenticateHTTP: request ctx bounded by loginTimeout, timeout error text
internal/vault/store             (new)
  store.go           Dir(), Load(identity), Update(identity, puts, evicts), record type (only
                       UnsafeReveal site), identity hashing, format_version 1
  lock_unix.go       flock on <hex>.lock polled every 20 ms to a 2 s deadline
  worktree.go        lexical + symlink-resolved .git walk, run by Update before each write
internal/vault/storefallback     (new)
  session.go         run Session: buffered puts/evicts per identity, served/miss notes, Flush()
  decorator.go       Provider wrapper + pure policy function
internal/fallbacknotice          (new, stdlib only)
  collector.go       Served / NothingToFallBackOn / LockTimeout / StoreInWorkTree / StoreUnwritable
  render.go          RenderText, RenderContext
internal/workspace
  apply.go           runPipeline: RunState + Session beside the redactor, deferred Flush,
                       provisioningBundle helper at the overlay/team/personal build sites;
                       Applier.Notices field
internal/cli
  keyreport.go       wireKeyReport also attaches and renders a notice collector
  instance_from_hook.go  provisionResult.Notices; payload builders append notices;
                       stderr on other failures
  dispatch.go, watch.go  render notices after the key report
```

### Key Interfaces

```go
// internal/vault
type FailureClass struct {
    Class      FailureKind // ClassUnauthenticated, ClassUnreachable, ClassAnswered
    Reason     Reason      // ReasonLoggedOut, ReasonTimedOut, ReasonUnreachable (meaningful for the first two)
    HTTPStatus int         // parsed from "Response Code: <n>", 0 when none
}
func (f *FailureClass) Error() string // never displayed

type ClassifiedError struct{ Err error; Class FailureClass }
func (e *ClassifiedError) Error() string   { return e.Err.Error() } // byte-for-byte today's text
func (e *ClassifiedError) Unwrap() []error { return []error{e.Err, &e.Class} }

type Identity struct{ Kind, APIDomain, ProjectID, Environment, FolderPath string }
type StoreIdentifier interface{ StoreIdentity(ref Ref) (Identity, bool) }

// internal/vault/store
func Dir() (string, error)
func Load(dir string, id vault.Identity) (map[string]Entry, error)          // no lock
func Update(dir string, id vault.Identity, puts map[string]Entry, evictKeys []string, evictAll bool) error
type Entry struct {
    Value        []byte    // plaintext; json base64
    ResolvedAt   time.Time
    VersionToken string
    Provenance   string
}

// internal/vault/storefallback
func NewSession(dir string, notices *fallbacknotice.Collector, now func() time.Time) *Session
func (s *Session) Wrap(p vault.Provider) vault.Provider // returns p unchanged unless p implements vault.StoreIdentifier (type assertion; no ref needed)
                                                        // used as bundle.Wrap(session.Wrap)
func (s *Session) Flush()                               // per identity: one Update; notes failures
```

Normalisation lives in one function in `internal/vault`:

- the API domain is lower-cased scheme and host;
- the folder path gets a leading `/` and loses any trailing `/`.

The Infisical provider's `StoreIdentity` and the store's hashing both call it; classification
never compares domains. A classification test pins that a 403 with an authenticated, verified
session on a different domain is *answered*.

### Data Flow

A healthy run: `runPipeline` creates the run state, the session and the notice collector. Each
provisioning bundle is built with `session.Wrap`. `resolveOne` calls the wrapper, which calls the
Infisical provider. The export succeeds, the wrapper buffers a put, and the value flows on exactly
as today. At the end of the pipeline the deferred `Flush` runs one locked `Update` per identity,
each of which walks for a work tree before it writes. The collector stays empty, so nothing new
is printed.

A lapsed login:

1. The export exits 1 with a logged-out message.
2. The provider sees no timeout and no server response, and runs the probe. The probe prints
   `{"sessions": []}`.
3. The classifier returns *unauthenticated*, reason ReasonLoggedOut, wrapping `ErrProviderUnreachable`,
   and the run state records the principal for that domain.
4. The wrapper finds the class, loads the identity's entries, and returns the stored value with its
   token. It notes `Served(identity, ReasonLoggedOut, resolvedAt)`.
5. Later keys and identities on the same domain for a CLI-session principal find the run state's
   verdict, so no subprocess runs.
6. `Flush` has nothing to put for served keys, so it doesn't write.
7. The surface renders the key report (unchanged: served keys are supplied), then the notices,
   which print the warning.

A dead route: the export reaches its 30 s bound. The call site sees its own deadline and classifies
the failure as *unreachable*, reason ReasonTimedOut, without a probe for a minted principal. For a
CLI-session principal it runs the probe, bounded at 15 s, whose answer can only confirm
*unreachable*, move the failure to *unauthenticated*, or leave it unchanged. The run state marks the
domain unreachable, and every later call on that domain returns at once.

An *answered* failure passes through the wrapper unchanged, apart from buffered evictions for a
missing key or a folder-level 403/404. It then produces exactly today's mark or error.

## Implementation Approach

The PRD asks for golden fixtures of today's behaviour recorded before the behaviour changes, so they
come first.

### Phase 1: Golden fixtures of today's behaviour

Record, from the current code, the error text and key-report output for each stub scenario the
acceptance criteria compare against. That covers the logged-out wordings, 403, 404, 500, "client not
installed", the universal-auth failure, and a required key with an unreachable provider. They go
under `internal/vault/resolve/testdata/golden/` and `internal/workspace/testdata/golden/` and the
functional fixtures, with a test that asserts they match the current code.

Deliverables: fixture files; a characterization test.

### Phase 2: Bounded, non-interactive subprocesses

Implement decision 4 in `internal/vault/infisical`: `bounds.go`, the `defaultCommander.Run` changes,
the timeout check ahead of the existing mappings, and the login timeout in `auth.go`.

Until Phase 3 lands, a timed-out export returns `ErrProviderUnreachable` (with no class), so it is
already a soft mark rather than a hang or a hard error.

Deliverables: `bounds.go`; `subprocess.go` and `auth.go` changes; unit tests using the
hang, child-holding-pipe and pseudo-terminal stubs; one real-bound test marked slow.

### Phase 3: Classification

Add `FailureClass`, `ClassifiedError`, `Identity`, `StoreIdentifier`, `Bundle.Wrap` and the run
state in `internal/vault`, and `classify.go` with the probe (reusing and extending the decoder in
`session.go`), the ordered rules and the wording markers. Attach the classification in
`runInfisicalExport` so `ErrProviderUnreachable` wrapping follows R5 and *answered* failures keep
today's sentinel and text. Add run-state consultation, `StoreIdentity` and the `api_url` read in `Factory.Open`.
Remove "401"/"403"/"forbidden" from `looksLikeAuthFailure`'s role in deciding the class (the
sentinel choice for *answered* failures is unchanged). Update `TestLooksLikeAuthFailure` deliberately.

Deliverables: `errors.go`, `provider.go`, `registry.go`, `runstate.go`, `session.go`, `classify.go`,
`infisical.go` changes; table tests for every rule and principal type, including a byte-for-byte
text test of every *answered* error against the Phase 1 fixtures; regression tests that
`niwa status --check-vault` and onboard's session check behave as before apart from the new bounds
and classification.

### Phase 4: The store

Implement decision 2 as `internal/vault/store`.

Deliverables: the package; tests for format, hashing, permissions, ownership and symlink refusal,
the size cap, merge, eviction, lock bound, the pause and abort hooks, the work-tree walk, and
corrupt, unknown-version and unreadable files.

### Phase 5: Fallback decorator, notices and wiring

Implement `internal/vault/storefallback` and `internal/fallbacknotice`. Wire `runPipeline` (run state, session,
deferred flush, `provisioningBundle` at the three sites). Render the notices from `wireKeyReport`,
`dispatch.go`, `watch.go` and the hook.

Deliverables: the two packages; `apply.go` and CLI changes; the
decorator's pass-through identity test; per-layer fallback tests through the fake backend (which
gains `identity`, `fail_class`, `fail_status` and `fail_plain` knobs); strict-mode and required-key
tests.

### Phase 6: Functional coverage, isolation and records

- Extend the functional `infisical` stub with logged-out, server-response, hang and probe modes.
- Add the end-to-end scenarios for dispatch, create, apply, init, reset, watch and the hook.
- Make the harness strip and set `XDG_STATE_HOME`.
- Add the supersession notes to `PRD-vault-integration`, its design and the guide.
- Rewrite the code comments that cite the no-disk-cache rule.
- Add to the guide the priming sentence, the store's location and contents, the advice to exclude
  it from backups on shared hosts, and the purge step (delete the directory).

Deliverables: stub and scenario changes; doc edits; the R27 sentinel-directory run in CI.

## Security Considerations

**Plaintext secrets at rest.** The store holds secret values in plain text, as the owner accepted,
in one file per provider identity under `$XDG_STATE_HOME/niwa/secret-cache/` (default
`~/.local/state/niwa/secret-cache/`). Files are 0600 in a 0700 directory and never inside a git
work tree or the git-synced configuration directory. Unlike instance files, the store isn't
removed when instances are reaped and has no age limit. It keeps the last value of every key a
provisioning run has resolved, across all workspaces and the personal layer, until an answered
403 or 404, or an export that no longer contains the key, evicts it. The vault-integration guide
names the directory, says what it holds, recommends excluding it from backups on shared or managed
hosts, and gives the purge step: delete the directory. Credential sync, `niwa status --check-vault`
and onboard never read or write the store, because their providers are never wrapped.

**Who can read the store.** Any process running as the owner can read the store, including
dispatched agent sessions and `niwa watch` review sessions, just as they can already read the
instance files that hold the same values. The store gathers them in one place and outlives reaped
instances. Adding the store directory to the read-deny rules niwa writes for sandboxed sessions is
worth doing as a follow-up; this design doesn't change those rules.

**Store integrity.** A value read from the store goes into instances as if it had just been
resolved, so the store is only trusted when the current user owns it exclusively. Before any read
or write, niwa requires the store directory to be a real directory (not a symlink), owned by the
effective user and not writable by group or others. Data and lock files are opened without
following symlinks and must be regular files owned by the effective user with no group or other
permission bits. A looser directory is tightened only after its ownership is confirmed. If any
check fails, the store is treated as empty and unwritable for the run and one warning is printed,
which falls back to today's behaviour. Data files larger than a fixed cap are treated as unreadable.
The format-version and identity-echo checks catch corruption, not tampering; ownership is the
boundary. The store identity leaves out who authenticated, so during a lapse a workspace
configuration that names another workspace's folder is served that folder's stored values, and a
workspace that gets a 403 on a shared folder evicts the values other workspaces stored from it.
Both require a configuration the owner wrote, on a host the owner controls. Pointing `XDG_STATE_HOME` at a directory someone else controls therefore disables the
store rather than exposing it.

**Stale values after upstream changes.** The fallback serves a stored value only when a failure is
classified *unauthenticated* or *unreachable*. Any failure the server answered for a minted
machine-identity token, and any 403 the probe vouches for, keeps today's handling and evicts where
the PRD says to. So a key deleted upstream, or a folder grant revoked, takes effect on the next run
that reaches the server with a valid principal. When the CLI user session itself is revoked or
expires, niwa can't tell a revocation from a lapse, and serves the stored values with the "may be
stale" warning and their age on every run until the owner logs back in or deletes the store. That
includes a rotated credential and a key removed as a kill switch. The host already holds those
values in its instances, so this adds no confidentiality exposure, but upstream revocation doesn't
reach an unattended host that uses a CLI session. Owners who need it to are advised to use a
machine identity whose credentials come from the local `provider-auth.toml`, or to delete the
store. A machine identity whose credentials come through credential sync doesn't help: when the
personal CLI session lapses, credential sync soft-fails, no token is injected, the provider falls
back to the lapsed CLI session, and its failures are served from the store like any other. There
is no optional age limit. The owner declined one, since any ceiling shorter than an unattended
absence defeats the purpose.

**Untrusted text in classification and messages.** The classifier reads only the probe's session
fields. The probe's `token` field is never decoded, stored or printed, and neither raw probe
output nor raw export stderr reaches any message. The deciding session is the principal the CLI itself uses, so no domain comparison can
reclassify a denial as a lapse. The HTTP status is taken from the CLI's own
`Response Code` line, so server-echoed text can at most move a failure toward serving a stored
value, never toward serving a value that didn't come from a successful export. Identity fields in
notices come from workspace configuration and are stripped of control and line-separator characters
in both the terminal and the hook renderings. The API domain is reduced to scheme and host, so URL
userinfo never appears in a message or in the store. The notice collector accepts no secret values
or CLI output by construction, and values served from the store are registered with the run's
redactor like fresh ones.

**Agent-facing messages.** For the session-start hook, notices go into the `additionalContext` an
agent reads. The rendering asks the agent to tell the operator to run `infisical login` and not to
run it itself, since an interactive login from an agent's shell would prompt or hang.

**Subprocess containment.** Every export and probe runs with stdin on the null device, in a new
session with no controlling terminal, within a deadline (export 30 s, probe 15 s, universal-auth
login 30 s). On timeout the whole process group is killed. A final group kill after the call
returns is sent only when the deadline fired or the output pipes had to be cut, so a recycled
process group ID is never signalled on the normal path. The `infisical` binary is looked up on
`PATH` as today, and the probe adds no trust beyond what the export already has.
`NIWA_TEST_VAULT_TIMEOUT` can only shorten bounds, above a small floor, and niwa prints a warning
whenever it's in effect, because a very short bound turns fresh resolution into serving stored
values.

**Locking and the work-tree guard.** The per-identity lock is a kernel `flock` on a lock file that's
never deleted, so a killed holder can't wedge later runs, and the wait is capped at 2 seconds.
Temp files are created exclusively at 0600 and renamed into place, so readers never see a partial
file. A crash can leave a temp file behind until a later update clears it. The `.git` walk guards
against accidental commits, not against an attacker. A `.git` entry anywhere above the store only
disables writes.

## Consequences

### Positive

- A lapsed login, an expired token or a dead route no longer stops unattended provisioning, and each
  run that falls back says so with the identity, the reason, the age and the fix.
- A lapsed login now shows up as "logged out or expired" rather than an unexplained "export exited 1".
- Every vault subprocess and the universal-auth login now have bounds, which removes the
  indefinite-hang risk from the session-start hook whether or not the fallback applies. That also
  fixes a latent path where a killed child would have been reported as a generic hard error.
- *Answered* failures keep today's handling by construction: the decorator returns the identical
  error value, and `resolveOne` doesn't change.
- Credential sync, status and onboard are unaffected, because their providers are never wrapped.

### Negative

- The `defaultCommander` changes (null stdin, `Setsid`, group kill) also apply to onboard's
  subprocess calls, and the classification changes what `niwa status --check-vault` reports for a
  lapsed login (a classified "provider unreachable" instead of an unexplained error). It can also
  add a probe call of up to 15 s. Regression tests pin both paths.


- Secret values now persist in plain text in one more place, the store under the state directory,
  for as long as a provider identity is used. The owner accepted this. The store is 0600 in a 0700
  directory, never inside a work tree, and never in the git-synced configuration directory.
- A stored value can be arbitrarily old and can mask an upstream rotation until the next successful
  run. The warning states the age.
- A real 403 denial that coincides with an inconclusive probe serves stale values for that run (R2,
  rule 4), with the warning.
- Three wrap sites have to stay in step. A future provisioning layer that forgets the helper gets no
  fallback.
- A test-only environment variable, `NIWA_TEST_VAULT_TIMEOUT`, is read in production code. It can
  only shorten bounds.
- A folder-level 403 or 404 evicts the identity's whole entry, including keys another workspace
  stored from the same folder. They're restored on that workspace's next successful run.

### Mitigations

- Tests pin the pass-through identity, one fallback test per layer, and a check that no provisioning
  bundle in `apply.go` is built outside the wrapping helper.
- The work-tree walk runs before every write, and the directory mode is tightened if it's found
  looser.
- The release notes and the vault-integration guide tell owners to run one provisioning command while
  logged in after upgrading, so the store is populated before a host is left unattended.
- A live acceptance step after release covers the unmeasured expired-user-session path: with the
  store warmed, log out, dispatch, see the warning and the keys present, then log back in.
