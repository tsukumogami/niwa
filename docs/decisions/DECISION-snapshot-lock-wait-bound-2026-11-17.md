---
status: Accepted
decision: |
  A niwa command waits at most 30 seconds for the config snapshot lock
  (`<configDir>.lock`) on each acquisition, polling the same way
  `acquireFileLock` does. When the wait runs out it fails with an error that
  names the lock file and says another niwa command is refreshing that config;
  it never goes on without the lock. The bound is a package constant, like
  `fileLockTimeout`, with no environment override, and no test depends on it.
rationale: |
  The lock's tightest outer limit is the 180 second SessionStart hook that runs
  `niwa instance from-hook`, and one create takes the lock up to four times.
  Thirty seconds keeps that worst case at two minutes, inside the hook, and it
  matches the only other waiting lock in niwa. Two minutes can't fit four waits
  in the hook, and a longer bound still wouldn't cover a holder whose own fetch
  has no timeout. Contention among healthy commands costs under a second per
  holder, so the bound only decides how fast a waiter gives up on a slow or
  stuck one.
---

# DECISION: the snapshot lock waits 30 seconds

## Status

Accepted

## Context

Two niwa commands provisioning the same workspace at the same moment race on
the config snapshot swap. Each one stages the new snapshot in `<configDir>.next`,
renames the live directory to `<configDir>.prev` and renames the staging
directory into place. Run concurrently, one command can lose another's session
mappings, and a reader can find `.niwa` missing between the two renames and
restore `.niwa.prev` over a swap still in progress.

The fix serializes the swap under an exclusive flock on a sibling file,
`<configDir>.lock`, held around the whole refresh or materialize step. That step
includes fetching the config source, so the lock is held across network I/O.
A waiter that can't get the lock has to give up at some point, and this record
settles when.

Facts that bear on it:

- `acquireFileLock` (`internal/workspace/file_lock_unix.go`) waits 30 seconds,
  polling every 20 ms. No lock wait in niwa is longer.
- The fetch has no application-level timeout. The GitHub client uses
  `http.DefaultClient`, so only the transport's 30 second dial and 10 second TLS
  limits apply, and git runs with no timeout. A no-drift refresh takes well under
  a second and a changed config takes a few seconds. With packets dropped, the
  drift check's four attempts keep a healthy holder busy for about two to three
  minutes before it falls back to the cached snapshot. A stalled response body,
  a hung git or a credential prompt can hold the lock with no end.
- `niwa instance from-hook` runs the whole create as a SessionStart hook with a
  180 second timeout. Several background sessions starting together fire it
  against the same workspace at once, which is the main contention case.
- One create takes the lock up to four times: the workspace `.niwa` twice, the
  overlay directory and the global config directory. The bound applies to each
  acquisition.

## Decision

Wait at most 30 seconds for each acquisition, then fail with an error that
names the lock file and says another niwa command is refreshing that config.
Never proceed without the lock. The constant lives beside the lock code, named
in the `fileLockTimeout` style, with no environment override. Tests observe a
waiter through the `snapshot-lock-wait` testfault mark and never through the
bound.

## Alternatives considered

- **2 minutes per acquisition.** It covers most of the dropped-network case, but
  one wait uses two-thirds of the hook's budget and four can take eight minutes,
  so the hook is killed with no instance and no session mapping. It still
  doesn't cover a holder that never returns.
- **No bound.** flock releases on process death, so a crashed holder never
  strands anyone. A hung holder, though, would strand every waiter, including
  every hook, with no message at all.
- **30 seconds, then skip the refresh and keep the current snapshot.** This
  would keep today's warn-and-use-cache behaviour for writers when a snapshot
  already exists. It adds a second outcome to every writer, and readers and
  session-mapping writes can't take it, so it isn't part of this change. It's a
  candidate follow-up.

## Consequences

Contention between healthy commands costs a waiter about the sum of the
holders' refresh times, a few seconds even with many sessions starting together.
A waiter stuck behind a holder that's slow on the network now fails after 30
seconds with a clear message, where before it raced and usually kept going on
its cached snapshot. That trade is deliberate: the race corrupted state, and a
named failure doesn't.

The bound doesn't limit how long a holder keeps the lock. That depends on the
holder's own fetch, which has no request timeout. Giving the snapshot fetch a
request timeout would put a ceiling on the hold itself, and it's the natural
follow-up to this change.
