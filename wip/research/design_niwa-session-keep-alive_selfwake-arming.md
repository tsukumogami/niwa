# Research: self-wake heartbeat arming (design decision D1/D2)

## Primitives
- `/loop` + `CronCreate/CronList/CronDelete`: session-scoped scheduled prompts, 7-day TTL,
  ±30min jitter on recurring, max 50 tasks/session. Backed by cron.
- `ScheduleWakeup`: ephemeral self-paced loop tool (1m-1h reschedule); not externally invokable.
- `selfWake`/`session_cron`: observed in state.json but NOT in public docs (internal fields).

## Arming at launch (external launcher)
- **No documented CLI flag / settings key / hook** to schedule a cron at `claude --bg`
  startup. The robust path is **prompt injection**: the dispatched prompt instructs the
  agent to create a `/loop` (which invokes CronCreate). Persists 7 days across resume.
- Alternatives (less certain): a SessionStart hook that spawns a `CronCreate` (undocumented
  whether it runs before the first prompt); a pre-seeded `.claude/loop.md`.

## Does a self-wake keep the RC bridge warm? (KEY UNCERTAINTY)
- Documented: RC times out after ~10min of NETWORK unreachability (not our case).
- The observed 6-12h idle drop is NOT explained in docs — could be cloud-side gateway
  timeout, credential/bridge-session expiry, or activity-based idle.
- A local wake keeps the session PROCESS cycling and runs a model turn; whether that
  RESETS a server-side bridge timeout is UNCONFIRMED. If the drop is server-managed and
  not activity-based, a heartbeat would not prevent it.
- Weak signal from the spike: only truly-idle sessions (frozen updatedAt) were seen
  dropping; active sessions were not observed dropping -> leans activity-based -> heartbeat
  plausibly works. NOT confirmed. => design must gate on a validation test.

## Token cost (tensions with R11)
- A prompt-loop heartbeat costs ~100-200 tokens/iteration (a model turn round-trip).
- At 5-10min: ~14-29k tokens/day. At hourly: ~24 wakes/day ~ few thousand tokens/day.
- No documented zero-token heartbeat. Minimize via LONG interval + minimal prompt.
- Since the drop is 6-12h (not 10min), interval can be hourly or even 2-4h and still beat
  it -> keeps token cost immaterial vs a working session. Use a conservative fraction of
  the ~6h floor (e.g. hourly).

## Stop-on-gone
- Cron is session-scoped; when the session's job entry is removed (TUI close / `claude rm`),
  the loop stops (session deleted). `claude stop` also halts it. => stops automatically when
  the session is gone. Good.

## Design implications
- Arming via prompt injection alters the session's first turn -> mild tension with R11
  ("don't alter working state") and reliability (agent must actually set up the loop).
  A hook-armed cron would be cleaner if it works; needs validation.
- The heartbeat's EFFICACY (does it prevent the drop) is the top design risk -> requires a
  live throwaway-session validation: arm heartbeat, confirm reachable past 12h.
- Interval: hourly (well under the 6-12h floor), minimal-prompt wake, to satisfy R11.
