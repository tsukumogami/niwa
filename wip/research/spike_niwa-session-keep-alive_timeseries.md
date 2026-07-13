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
