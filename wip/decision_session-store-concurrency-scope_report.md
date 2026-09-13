<!-- decision:start id="session-store-concurrency-scope" status="confirmed" -->
### Decision: How far the #297 concurrency guarantee reaches

**Context**

The workspace-root `.niwa/` is both the configuration snapshot, which every
refresh rebuilds elsewhere and swaps in wholesale, and the home of niwa-local
state that the swap has to carry across. Issue #297 names two failures:
refreshes colliding on the fixed staging path, and a session mapping written
during another command's refresh getting lost. Phase 2 research (the PRD's
concurrency research) found the same window affects more than mappings:

- A mapping the reaper deletes during another command's refresh comes back
  when that refresh lands.
- The reaper's backstop sweep reads the mapping store. A read taken during a
  swap looks empty, which makes mapped dispatch instances older than 30
  minutes eligible for reclamation.
- The root `instance.json` is written non-atomically, from a read taken at
  the start of a long command (`saveWorkspaceRootDisclosures`,
  `apply.go:2727-2748`). A concurrent refresh can copy a truncated copy of
  it, and a failed read writes a fresh state that drops
  `ephemeral_session_mode` and the overlay settings.
- Watch's `.niwa/watch-handled` and `.niwa/watch/` are never carried across
  any refresh, concurrent or not.

The PRD has to say which of these the one PR guarantees. The task brief asks
for "never lose a session mapping or fail with a missing staging path" and
rules out redesigning reap or the ephemeral lifecycle.

**Assumptions**

- The DESIGN will introduce one per-config-dir ordering primitive (a lock
  outside the rotated directory) that serializes carry-over plus swap. Once
  it exists, each extra writer brought under it is a small change. What
  makes B and C bigger than A is the separate fix each covered file also
  needs, not the lock itself.
- Watch-state loss happens on every refresh, not only a concurrent one, so
  it is a missing carry-over, not a race.

**Chosen: A. Session mappings plus the reaper's read, with two guards from B**

The guarantee covers every niwa write and delete of a session mapping, and
the reaper's mapping-store read. A mapping written or deleted around a
concurrent refresh ends in the state its writer left it. No refresh fails
because of another. The reaper never decides to destroy an instance on a read
taken mid-swap. Two guards from B are added, per the coordinator's verdict:
`SaveState` writes atomically (temp file, then rename), and root disclosures
are never written after a failed state read when the file exists. Together
they stop a torn read during parallel dispatch from rewriting the root
`instance.json` without `ephemeral_session_mode`. The stale
read-modify-write itself (two commands each disclosing a different notice,
one key lost, the notice shown again), watch state, and agent-written briefs
are recorded as known limitations, with follow-up issues proposed to the
coordinator.

**Rationale**

A is the smallest guarantee that meets the brief. It closes both failures
the issue names, and it closes the two other ways the same mapping store
loses or misreports a mapping: resurrection after reap, and the empty read.
Each of those ends the way #297 does, with a worker unreachable or an
instance reclaimed wrongly. Leaving them out would ship a lock that still
lets the reaper act on a store it can see mid-swap.

B's real defect is the stale read-modify-write in
`saveWorkspaceRootDisclosures`, and a lock around the write alone doesn't
fix it; the read would also have to move under the lock, which reorders
Create and Apply. C's defect isn't concurrency at all. Both are real, and
both belong in their own issues, where they get their own tests.

**Alternatives Considered**

- **B. A plus root `instance.json`:** serialize root `SaveState` with the
  swap and make it atomic. Rejected for this PR. It doesn't fix the stale
  read without restructuring Create and Apply's state handling, and that
  widens the change past what the brief scoped.
- **C. B plus watch state carried across every refresh:** Rejected for this
  PR. The watch-state loss isn't a race; it's a missing entry in the
  carry-over set. It deserves its own issue and test.
- **D. Mappings only, reaper read left out:** Rejected. A mid-swap read
  still looks empty to the backstop sweep, so a mapped instance can be
  reclaimed, which is the data loss #297 is about, reached another way.

**Consequences**

- The PR contains one ordering primitive and three callers under it: the
  refresh's carry-over and swap, mapping write and delete, and the reaper's
  mapping read. Plus the refresh-versus-refresh staging fix.
- The PR also makes `SaveState` atomic and stops root disclosures from being
  written after a failed read, so the root `instance.json` can no longer be
  wiped. What stays exposed until its own issue: a lost notice key when two
  commands disclose different notices at once, and watch state. The PRD states
  both under Known Limitations.
- Two follow-up issues proposed to the coordinator: root `instance.json`
  stale read-modify-write (notice-key loss); watch state not carried across
  refresh.
<!-- decision:end -->
