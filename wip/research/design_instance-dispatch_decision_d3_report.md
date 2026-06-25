# DESIGN decision D3 — How `niwa dispatch` captures the launched session's identity

**Decision.** The new `niwa dispatch` command launches `claude --bg "<prompt>"`
as a detached background session and must end up holding that session's full
canonical UUID, because `WriteSessionMapping` rejects anything that is not a
lowercase-hex 8-4-4-4-12 UUID via `ValidSessionID`
(`internal/workspace/session_map.go:20,25-27,71-76`). `claude --bg` prints only
human text `backgrounded · <short-id>` (8 hex chars) — there is no `--json`
mode — and each session writes `~/.claude/jobs/<short-id>/state.json` carrying
the full `sessionId`, `cwd`, `state`, `updatedAt`, `backend`. The command runs
claude with `cmd.Dir` = the freshly-created instance dir, which is a unique path.

This document evaluates three capture strategies and recommends one.

---

## Grounding facts (verified)

1. **`WriteSessionMapping` needs a canonical UUID.** `sessionMappingPath`
   rejects via `ValidSessionID` (regex
   `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`) before
   building any path. The 8-char short-id printed by `claude --bg` is therefore
   never directly usable as a mapping key — the full UUID must be recovered
   either way. (`session_map.go:20,25-27,83-109`;
   `wip/research/prd_instance-dispatch_phase2_code-facts.md` Q2.)

2. **niwa already reads `state.json` and the read seam is `jobsDir`.** The
   `jobState` struct (`internal/cli/job_state.go:30-35`) decodes
   `sessionId/template/state/updatedAt`. `readJobState(jobsDir, sessionID)`
   (`job_state.go:116-144`) first tries `<jobsDir>/<full-id>/state.json`, then
   **scans for a dir whose name is a prefix of the session id**. Crucially the
   `cwd` field is NOT in the current struct — it would need adding for any
   cwd-correlation path.

3. **`jobsDir` is already an injectable parameter, not read inline.**
   `defaultJobsDir()` (= `~/.claude/jobs`, `job_state.go:60-66`) is called
   *only* at CLI command boundaries (`reap.go:57,185`,
   `instance_from_hook.go:120`); every internal consumer takes `jobsDir` as an
   argument (`reap.go:90,153`; `instance_from_hook.go:139,248,281`). The hook
   path documents this explicitly: "jobsDir is injected (rather than read from
   the environment) so the guard's test can point at a fixture dir"
   (`instance_from_hook.go:137-139`). **The offline test seam already exists and
   is the established pattern — tests fabricate `state.json` files under a temp
   dir and pass its path.** Any D3 option inherits this seam for free as long as
   it routes its jobs-dir read through a `jobsDir` parameter.

4. **The launch supervisor today streams, it does not capture.**
   `sessionattach.Supervise` (`internal/cli/sessionattach/supervise.go:35-77`)
   wires `cmd.Stdin/Stdout/Stderr` to the terminal (defaulting to
   `os.Stdin/Stdout/Stderr`) and blocks on `cmd.Wait()`. It never uses
   `cmd.Output()` / a `bytes.Buffer`. Capturing stdout to read the
   `backgrounded · <short-id>` line therefore requires a **new capture mode** in
   the supervisor (set `cmd.Stdout` to a pipe/buffer instead of inheriting). A
   cwd-scan path needs no such change. (PRD Q6.)

5. **The instance dir is a unique, exact correlation key.** `applier.Create`
   returns `instanceRoot`, a unique path (`apply.go:404`). No two live dispatches
   share it. The short-id is unique-with-high-probability (8 hex = 2^32) but is a
   weaker key than an exact path equality.

6. **PRD requirements that bear on D3:**
   - **R20 — bounded wait.** `state.json` is written asynchronously by Claude
     after launch; the capture must poll with a timeout, not block forever.
   - **R21 — disambiguation.** With concurrent dispatches, the capture must bind
     to the *right* session, not just *a* session.
   - **R22 — capture failure → rollback.** If identity capture fails within the
     bound, dispatch must tear down the half-created instance (no orphan dir, no
     mapping) rather than leave a dangling instance.

---

## Option A — Scrape stdout, then read `state.json` by short-id

Capture `claude --bg` stdout, parse `backgrounded · <short-id>`, then read
`<jobsDir>/<short-id>/state.json` (bounded poll for the write race) and lift
`sessionId`.

**Pros**
- Directly targets one `state.json` file — the `readJobState` fast/prefix path
  (`job_state.go:116-144`) already resolves a dir keyed by a session-id prefix,
  so the lookup-by-short-id shape is *almost* what exists. (Caveat below.)
- Short-id is an explicit handle returned by the launch, so the poll knows
  exactly which dir to stat — no directory enumeration, no "which new entry is
  mine" reasoning.
- Disambiguation (R21) is implicit: the short-id is per-launch, so concurrent
  dispatches read different dirs.

**Cons**
- **Depends on an unstable human-output format.** `backgrounded · <short-id>`
  is undocumented presentation text (note the U+00B7 middle-dot separator, not
  ASCII). A Claude Code release can reword/recolor/relocate it (ANSI codes,
  i18n, a trailing hint line) and the scraper silently breaks. There is no
  `--json` contract to lean on.
- **Forces a supervisor capture mode.** Per fact 4, stdout must be redirected to
  a buffer/pipe — net-new code in `Supervise`, plus the ANSI-stripping and line
  parsing.
- The existing `readJobState` keys on the *session id* (it scans for a dir whose
  name is a **prefix of the session id**, `job_state.go:133-136`). Looking up by
  the printed short-id is a *different* lookup (short-id → full id) and would
  need its own helper; you cannot reuse `readJobState` as-is because you do not
  yet have the session id it expects as input.

**Risk**
- Medium-high. The single point of failure is a presentation string outside
  niwa's control. A format drift produces a capture failure on every dispatch —
  caught by R22 rollback (so no corruption), but the command is **broken until
  patched**. This couples niwa's core dispatch path to Claude Code's cosmetic
  output.

---

## Option B — No scrape; poll the jobs dir for a `state.json` whose `cwd` == instance dir

After launch, enumerate `~/.claude/jobs/*/state.json` and select the entry whose
`cwd` equals the unique instance dir we launched in; read its `sessionId`.
Bounded poll for the write race.

**Pros**
- **No stdout parsing at all.** Sidesteps the fragile human-output format
  entirely (fact 4). The launch supervisor can keep streaming/discarding stdout;
  no capture mode needed.
- **Exact correlation key.** `cwd == instanceDir` is path equality on a unique
  value (fact 5). This is strictly stronger than short-id matching for R21:
  there is no probabilistic collision argument to make — the instance dir is
  unique by construction, so at most one job matches.
- Reuses the established `jobsDir` injection seam (fact 3) directly: the poller
  takes `jobsDir` and offline tests fabricate `state.json` fixtures with a
  chosen `cwd` — the exact pattern `instance_from_hook.go` tests already use.
- Robust to Claude Code output changes — only depends on the *file* contract
  (`state.json` having `sessionId` + `cwd`), which niwa already depends on for
  liveness (`job_state.go`) and the SessionStart guard.

**Cons**
- **Requires adding `Cwd` to the `jobState` struct** (`job_state.go:30-35` has
  no `cwd` field today) and a new dir-enumeration helper. Modest, but net-new.
- Depends on Claude writing `state.json.cwd` exactly equal to the launch
  `cmd.Dir`. If Claude normalizes the path (symlink resolution, trailing slash,
  `filepath.Clean` differences), naive string equality could miss — the match
  should compare cleaned/abs/eval-symlink'd paths to be safe.
- Full-dir enumeration is O(number of job dirs). Trivial at realistic scale, but
  it scans every entry each poll tick rather than stat-ing one known path.
- The write-race window (R20) still applies: the matching entry may not exist yet
  at the first poll tick. Same bounded-poll structure as A, just scanning
  instead of stat-ing.

**Risk**
- Low. The only real failure mode is path-normalization mismatch on `cwd`, which
  is deterministic and fixable once (clean + EvalSymlinks both sides). No
  dependency on undocumented presentation text. A capture failure (no match
  within bound) is cleanly handled by R22 rollback.

---

## Option C — Hybrid: scrape short-id as primary, verify via `state.json.cwd`, fall back to cwd-scan

Scrape `backgrounded · <short-id>` and read `<jobsDir>/<short-id>/state.json`;
**verify** `state.json.cwd == instanceDir` before trusting the `sessionId`. If
the scrape fails, is ambiguous, or the cwd check disagrees, fall back to the
Option-B full cwd-scan.

**Pros**
- Fast path when the format holds (direct stat by short-id), exact-correlation
  safety net when it doesn't.
- The cwd verification closes A's R21 hole: even if two launches somehow printed
  the same short-id (negligible, but) the cwd check rejects a wrong match.
- Survives format drift: a broken scrape degrades to Option B rather than
  failing the dispatch.

**Cons**
- **Carries the union of both implementations' complexity:** the supervisor
  capture mode + ANSI/line parsing (from A) *and* the `Cwd` field + dir-scan
  fallback (from B). It is strictly more code and more branches than either
  pure option.
- Two code paths means two things to test and keep in sync; the fallback path is
  exercised rarely (only on format drift), so it risks bit-rotting undetected
  unless tests force it.
- The "primary" path's only advantage over B is stat-one-file vs scan-the-dir —
  a micro-optimization that does not matter at this scale (fact: a handful of job
  dirs). You pay A's fragility-surface and capture-mode cost to buy a
  performance win that is irrelevant.

**Risk**
- Low for correctness (the cwd check + fallback make it safe), but **highest
  implementation/maintenance cost** of the three. The rarely-run fallback is a
  latent-bug reservoir.

---

## Comparison against PRD requirements

| | A (scrape short-id) | B (cwd-scan) | C (hybrid) |
|---|---|---|---|
| **R20 bounded wait** | Poll one path until timeout | Poll/scan until timeout | Poll path, then scan, within one budget |
| **R21 disambiguation** | Implicit via per-launch short-id (probabilistic) | **Exact** via unique-path equality | Exact (cwd verify backs the short-id) |
| **R22 capture-fail rollback** | Triggers on format drift *and* timeout | Triggers only on genuine timeout | Triggers only after both paths exhausted |
| **Supervisor change** | **Yes** — add stdout capture mode | **No** — streaming is fine | **Yes** — needs capture mode |
| **Format coupling** | **High** — depends on `backgrounded · …` text | None — file contract only | Low — fallback absorbs drift |
| **New struct field** | None | `Cwd` on `jobState` | `Cwd` on `jobState` |
| **Offline test seam** | `jobsDir` seam reusable, *but* must also fake stdout | `jobsDir` seam reused **directly** | Both seams; must fake stdout *and* fixtures |
| **Net code volume** | Medium | Small–medium | **Largest** |

---

## Recommendation — Option B (cwd-scan, no stdout scrape)

**Rationale.**

1. **It removes the only fragile dependency.** The `backgrounded · <short-id>`
   string is undocumented presentation text with a non-ASCII separator and no
   `--json` fallback; binding niwa's core dispatch path to it (Options A and C)
   makes a cosmetic Claude Code change able to break every dispatch. Option B
   depends only on the `state.json` *file* contract that niwa **already** relies
   on for reaper liveness and the SessionStart guard (`job_state.go`,
   `instance_from_hook.go`). One contract surface, already load-bearing, instead
   of two.

2. **It gives the strongest disambiguation (R21), not the weakest.** The
   instance dir is unique by construction (fact 5), so `cwd == instanceDir` is an
   exact match — there is no collision-probability argument to make. The short-id
   key in A/C is merely unlikely-to-collide. Counter-intuitively, the option that
   *throws away* the launch's printed handle has the more rigorous correlation
   key, because the launch already chose a unique `cmd.Dir`.

3. **It needs no supervisor capture mode.** Per fact 4, both A and C force a new
   capture path in `Supervise` (redirect stdout to a buffer, strip ANSI, parse).
   B lets the supervisor keep streaming/discarding stdout — strictly less surface
   in the launch path, which is the riskiest code to perturb.

4. **It is the easiest to test offline.** The `jobsDir` injection seam already
   exists and is the documented test pattern (fact 3) — `instance_from_hook.go`
   tests already fabricate `state.json` fixtures under a temp dir. Option B reads
   *only* through that seam, so an offline test writes a `state.json` with a
   chosen `cwd` + `sessionId` and asserts capture. A and C additionally require
   faking process stdout, a second and clumsier seam.

5. **Option C's only edge over B (stat-one-file vs scan-the-dir) is an
   irrelevant micro-optimization** at the realistic scale of a handful of job
   dirs, bought at the price of A's full fragility surface plus a rarely-run
   fallback branch. YAGNI: do not build the hybrid until a measured need appears.

**Implementation notes for the design doc.**
- Add a `Cwd string \`json:"cwd"\`` field to `jobState` (`job_state.go:30-35`).
- Add a `findSessionByCwd(jobsDir, instanceDir string, deadline) (sessionID string, ok bool)`
  helper that enumerates `<jobsDir>/*/state.json`, decodes each, and returns the
  `sessionId` of the entry whose cleaned/EvalSymlinks'd `cwd` equals the
  cleaned/EvalSymlinks'd `instanceDir`. Route the jobs-dir read through a
  `jobsDir` parameter (mirroring `readJobState`); call `defaultJobsDir()` only at
  the command boundary.
- Compare paths after `filepath.Clean` + `filepath.EvalSymlinks` on both sides to
  survive normalization differences (the one real failure mode, see B/Cons).
- Wrap the scan in a bounded poll (R20): retry on no-match until a timeout, then
  treat as capture failure → R22 rollback (destroy the instance, write no
  mapping).
- Reaper interaction: only after the UUID is captured does `WriteSessionMapping`
  run with a valid key (`session_map.go:83-109`); set the mapping (and the
  on-disk record) `Ephemeral` per the dispatch policy so a `done` session is
  later reapable (PRD Q3).

**Residual risk to track.** Path-normalization mismatch on `cwd` is the single
correctness hazard; the EvalSymlinks-both-sides comparison neutralizes it, and a
functional fixture (fabricated `state.json` with a symlinked cwd) should lock it
in. If a future Claude Code version ever stops writing `cwd` to `state.json`,
Option B's premise collapses — but that same field is what the SessionStart guard
and reaper already lean on, so the blast radius is shared, not unique to dispatch.
