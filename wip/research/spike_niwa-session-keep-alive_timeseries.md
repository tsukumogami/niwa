# RC bridge time-series (always-on Linux desktop; watching for idle disconnect)

```

===== SNAPSHOT 2026-07-13T20:07:54Z =====
id       name             rc  state    bridge                         firstTerm                upd          mt       
32544a2d commuter_wip     rc  blocked  cse_01F545JxskhdtLRyAe3DDsTw   None                     18:14:02.333 18:14:02Z
4be9ad6d session-state-re no  blocked  -                              2026-07-13T17:41:47.214Z 17:44:20.879 17:44:20Z
71e6c42b niwa_keep_alive  rc  working  cse_018pJczirAQ4dsCdNJM7hZwM   None                     20:07:02.040 20:07:02Z
9a06b95e feature_4_real_c rc  done     cse_01Tja6DLTxPUH236N611S7cC   2026-07-13T19:42:44.102Z 19:42:44.102 19:42:44Z
d12be1a5 niwa_teleport    rc  working  cse_01C5u1kUSuT6wEbZMSUsBRqp   None                     20:05:22.117 20:05:22Z
fc513e7d tpconfirm        rc  done     cse_016yqEZ6wSKDT5WRVRxvKxDp   2026-07-13T20:04:29.781Z 20:04:29.781 20:04:29Z
daemon pid alive: 89127
daemon.log tail:
[2026-07-13T19:20:55.333Z] [bg] bg settled bed24441 (killed)
[2026-07-13T20:04:17.386Z] [bg] bg claimed-spare fc513e7d (shell)
[2026-07-13T20:04:17.388Z] [bg] bg spare spawned host pid=370355
```

<!-- watcher started; watching RC bridges: 32544a2d,71e6c42b,9a06b95e,d12be1a5,fc513e7d -->
```

===== 2026-07-13T20:39:39Z =====
32544a2d commuter_wip     rc  blocked  cse_01F545JxskhdtLRyAe3DDsTw   ft=2026-07-13T20:14:21.067Z upd=20:35:00.440
4be9ad6d session-state-re no  blocked  -                              ft=2026-07-13T17:41:47.214Z upd=17:44:20.879
71e6c42b niwa_keep_alive  rc  working  cse_018pJczirAQ4dsCdNJM7hZwM   ft=None upd=20:23:37.896
9a06b95e feature_4_real_c rc  done     cse_01Tja6DLTxPUH236N611S7cC   ft=2026-07-13T19:42:44.102Z upd=19:42:44.102
d12be1a5 niwa_teleport    rc  working  cse_01C5u1kUSuT6wEbZMSUsBRqp   ft=None upd=20:38:36.598
EVENTS: fc513e7d ENTRY-REMOVED (bridge was cse_016yqEZ6wSKDT5WRVRxvKxDp)
daemon.log tail: [2026-07-13T19:20:55.333Z] [bg] bg settled bed24441 (killed) || [2026-07-13T20:04:17.386Z] [bg] bg claimed-spare fc513e7d (shell) || [2026-07-13T20:04:17.388Z] [bg] bg spare spawned host pid=370355 || [2026-07-13T20:10:50.451Z] [bg] bg settled fc513e7d (killed)
```

<!-- watcher v3 restart; watching RC bridges: 32544a2d,71e6c42b,9a06b95e,d12be1a5 -->

<!--
INTERPRETATION of iteration-1 event (2026-07-13T20:39Z):
- fc513e7d (tpconfirm) ENTRY-REMOVED. daemon.log: "bg settled fc513e7d (killed)" at
  20:10:50, ~6 min after it went 'done' (20:04). This is normal post-completion
  CLEANUP of a short finished session, NOT the multi-hour idle-disconnect we're after.
- Data point: when an entry is removed, the bridge goes with it (entry-gone => bridge-gone).
- Still-open question (idle-disconnect of a LIVE session) unanswered by this event.
- commuter_wip (32544a2d): went terminal 20:14, idle since, STILL holds its bridge at
  20:39 -> the real idle-session to watch. v3 watcher relaunched to catch its (or
  another live session's) genuine idle bridge-drop, distinguishing killed/cleanup from
  a true idle drop.
-->
```
===== 2026-07-13T21:11:10Z (it 1) =====
32544a2d commuter_wip     rc  blocked  cse_01F545JxskhdtLRyAe3DDsTw   ft=2026-07-13T20:14:21.067Z upd=20:35:00.440
4be9ad6d session-state-re no  blocked  -                              ft=2026-07-13T17:41:47.214Z upd=17:44:20.879
71e6c42b niwa_keep_alive  rc  working  cse_018pJczirAQ4dsCdNJM7hZwM   ft=None upd=20:41:44.750
9a06b95e feature_4_real_c rc  done     cse_01Tja6DLTxPUH236N611S7cC   ft=2026-07-13T19:42:44.102Z upd=19:42:44.102
d12be1a5 niwa_teleport    rc  working  cse_01C5u1kUSuT6wEbZMSUsBRqp   ft=None upd=20:56:21.636
```
```
===== 2026-07-13T21:41:10Z (it 2) =====
32544a2d commuter_wip     rc  blocked  cse_01F545JxskhdtLRyAe3DDsTw   ft=2026-07-13T20:14:21.067Z upd=20:35:00.440
4be9ad6d session-state-re no  blocked  -                              ft=2026-07-13T17:41:47.214Z upd=17:44:20.879
71e6c42b niwa_keep_alive  rc  working  cse_018pJczirAQ4dsCdNJM7hZwM   ft=None upd=20:41:44.750
9a06b95e feature_4_real_c rc  done     cse_01Tja6DLTxPUH236N611S7cC   ft=2026-07-13T19:42:44.102Z upd=19:42:44.102
d12be1a5 niwa_teleport    rc  working  cse_01C5u1kUSuT6wEbZMSUsBRqp   ft=None upd=20:56:21.636
```
```
===== 2026-07-13T22:11:10Z (it 3) =====
32544a2d commuter_wip     rc  done     cse_01F545JxskhdtLRyAe3DDsTw   ft=2026-07-13T20:14:21.067Z upd=22:01:55.048
4be9ad6d session-state-re no  blocked  -                              ft=2026-07-13T17:41:47.214Z upd=17:44:20.879
71e6c42b niwa_keep_alive  rc  working  cse_018pJczirAQ4dsCdNJM7hZwM   ft=None upd=21:42:25.228
96295a92 niwa_worktree_er rc  blocked  cse_0197vc3PBL1DAx1zgwdXK3gz   ft=None upd=22:06:50.428
9a06b95e feature_4_real_c rc  done     cse_01Tja6DLTxPUH236N611S7cC   ft=2026-07-13T19:42:44.102Z upd=19:42:44.102
d12be1a5 niwa_teleport    rc  done     cse_01C5u1kUSuT6wEbZMSUsBRqp   ft=2026-07-13T21:58:04.743Z upd=21:58:04.743
```
```
===== 2026-07-13T22:41:10Z (it 4) =====
176618cd cloud_multirepo  rc  blocked  cse_01AsM1FyqsYXZJgdJD9UNG41   ft=None upd=22:39:37.734
32544a2d commuter_wip     rc  done     cse_01F545JxskhdtLRyAe3DDsTw   ft=2026-07-13T20:14:21.067Z upd=22:39:54.846
4be9ad6d session-state-re no  blocked  -                              ft=2026-07-13T17:41:47.214Z upd=17:44:20.879
71e6c42b niwa_keep_alive  rc  working  cse_018pJczirAQ4dsCdNJM7hZwM   ft=None upd=21:42:25.228
96295a92 niwa_worktree_er rc  blocked  cse_0197vc3PBL1DAx1zgwdXK3gz   ft=None upd=22:06:50.428
9a06b95e feature_4_real_c rc  done     cse_01Tja6DLTxPUH236N611S7cC   ft=2026-07-13T19:42:44.102Z upd=19:42:44.102
d12be1a5 niwa_teleport    rc  working  cse_01C5u1kUSuT6wEbZMSUsBRqp   ft=2026-07-13T21:58:04.743Z upd=22:41:00.985
```

<!--
OBSERVATION at 22:43Z (4 iterations, ~2.5h of watching):
- NO local bridge-drop event yet on any watched RC session.
- feature_4_real_c (9a06b95e): idle with updatedAt FROZEN at 19:42:44 for ~3h, and
  bridgeSessionId STILL present locally. Cleanest idle specimen.
- commuter_wip (32544a2d): went 'done'; updatedAt keeps MOVING (20:35 -> 22:01 -> 22:39)
  even while idle -> the daemon or a self-wake touches the entry periodically; bridge held.
- niwa_teleport (d12be1a5): oscillates done<->working (still actively used).
KEY EMERGING HYPOTHESIS: the local bridgeSessionId may persist regardless of actual
server-side RC reachability. If so, state.json is NOT a reliable dropped-bridge signal,
which would favor a HEARTBEAT (prevent-the-drop) design over detect-and-reconnect.
CANNOT confirm locally -- needs a phone/claude.ai reachability check of an idle session
while its local bridge still shows present. Watcher continues (up to ~12h).
-->
