# Lead: What changed today?

Round 2. Investigated 2026-09-09 on the host that produced the symptom.

## Findings

### The headline: the symptom is not new today

**VERIFIED.** A machine-wide scan of every Claude Code transcript on this host
for the hold's own system event (`type: "system"`, content beginning
`Held peer message — from uds:...`) returns exactly **seven events, ever**, in
two clusters:

| # | Timestamp (UTC) | Receiver's Claude version | Receiver workspace |
|---|---|---|---|
| 1 | 2026-09-02T23:34:03Z | 2.1.258 | second workspace on this host |
| 2 | 2026-09-02T23:38:22Z | 2.1.250 | second workspace |
| 3 | 2026-09-03T00:16:39Z | 2.1.250 | second workspace |
| 4 | 2026-09-03T00:43:02Z | 2.1.259 | **tsukumogami**, instance `tsuku+niwa276`, worktree `scope-dispatch-permission-mode` |
| 5 | 2026-09-03T01:01:16Z | 2.1.259 | **tsukumogami**, instance `tsuku+niwa_dispatch_limitation-fe78bd10` |
| 6 | 2026-09-10T00:40:58Z | 2.1.267 | second workspace |
| 7 | 2026-09-10T00:45:32Z | 2.1.267 | second workspace |

Every receiver is `sessionKind: "bg"` — a background/dispatched session, never
an interactive one. The hold's own wording confirms round 1's mechanism
verbatim: *"The sending session's permission mode class doesn't match this
session's. Review it below, or set `crossSessionInbound` to `accept`."*

So the true onset is **2026-09-02, 23:34 UTC (19:34 EDT)** — a week ago, not
today. Today's two events are a recurrence, not a debut.

### What happened at 23:34 UTC on 2026-09-02: Claude Code 2.1.258 landed

**VERIFIED.** Reconstructing the Claude Code version actually running on this
host, by reading the `version` field out of each session transcript:

- 2.1.246 → 2.1.251 across 2026-08-26 to 2026-08-29
- **2.1.258 first appears at 2026-09-02T23:33:25Z**
- 2.1.259 from 2026-09-03T00:19Z
- 2.1.259 stayed the running build for a week; `~/.claude/.last-update-result.json`
  records `version_from: "2.1.259"`, `version_to: "2.1.267"`,
  `timestamp: 2026-09-09T23:17:51Z`. (2.1.260 and 2.1.263 binaries were
  downloaded into `~/.local/share/claude/versions/` on 09-03 and 09-07 but were
  evidently never promoted to the running build.)

The first hold on this machine fired **38 seconds after the first 2.1.258
session started.** That is not a coincidence, and niwa's own design document
already names the mechanism:
`docs/designs/current/DESIGN-dispatch-permission-mode.md:46` —
*"Claude Code 2.1.258 broke the channel that…"*, and `:92` — the modes a worker
can resolve to *"since Claude Code 2.1.258 no longer includes the project
[settings file]"*.

The causal chain for the Sep 2/3 cluster:

1. Before 2.1.258, a niwa worker got `bypassPermissions` from the instance's
   materialized `.claude/settings.json`, which niwa writes from the workspace's
   `permissions = "bypass"` declaration. Its coordinator peer resolved against
   the same file. **Both sides landed in the bypassing class — a match, so
   messages delivered silently.**
2. 2.1.258 made `permissions.defaultMode` in a project settings file inert. One
   side of each pair fell out of the bypassing class while the other did not.
   **Mismatch → hold.** Events 1–5.
3. niwa PR #277 (`d25ad4d`, merged 2026-09-02 22:41 EDT) restored the posture
   through the only surviving channel, the `--permission-mode` CLI flag, by
   deriving it from the instance settings at dispatch time
   (`internal/cli/dispatch.go:551`, consumed at `:983`). It shipped in niwa
   v0.24.0 (tagged 2026-09-03T02:58Z), and this host installed v0.24.0 at
   **2026-09-02 23:01 EDT = 2026-09-03T03:01Z** (tsuku state record and the
   `~/.tsuku/tools/niwa-0.24.0/` directory mtime agree).

All five events in the first cluster occurred **before** 03:01Z on 09-03, i.e.
before the fix reached this machine. Then: **zero holds for a week**, through
niwa v0.24.0, across cross-session activity on 09-05 and 09-07. That interval
is the evidence that #277 genuinely fixed the fresh-dispatch path.

### Why it came back today: the fix does not survive re-entry

**VERIFIED (code) / INFERRED (that this is what fired today).**

The derivation added by #277 lives in `runDispatch` only. `dispatchPermissionMode`
is set at `internal/cli/dispatch.go:551` and consumed once, at `:983`, when the
launch argv is built. Nothing else in the tree assigns it.

niwa's re-entry surface is a separate, deliberately-centralized file:
`internal/cli/dispatch_reentry.go`. Its header comment states the rule it
enforces — every command that steps back into a session must re-carry the
posture the launch path granted, *"the grant dies with the process it was
passed to. Every way back in starts a new process, so every way back in has to
carry the grant again -- and a way back in that was added later and forgot to
would fail silently, which is exactly how the defect this file fixes reached a
release."* That file carries `WorkdirGrantArgs` (the Codex trust override). It
contains **no reference to `PermissionMode` at all** — verified by grepping the
whole non-test tree for `PermissionMode`, which hits only `dispatch.go`,
`agentplan/dispatch.go`, and a dead helper (below).

So: a Claude worker launched fresh by `niwa dispatch` is in the bypassing
class. The same session resumed or re-entered through `reentryArgs` is not —
the flag is gone and, since 2.1.258, the settings file cannot supply it
either. A fresh worker talking to a resumed one is a class mismatch, which is
exactly what the hold reports.

Both of today's events fit that shape. Both receivers are long-lived
coordinator-role background sessions, and one of them is demonstrably a resumed
session of an instance whose earlier session ran **2.1.250 on 09-02** and which
was running **2.1.267 today** — it was restarted/resumed across the boundary.
One sender identifies itself as the legacy predecessor handing over to its
successor. This is peer-to-peer traffic between two sessions of different
vintage, not the fresh coordinator→worker dispatch path #277 repaired.

I did not directly observe the argv of the two sessions involved (their
processes did not expose a `--permission-mode` flag in `ps`, but background
sessions on this host are claimed from pre-warmed `claude bg-spare` processes
whose argv carries no session flags at all, so `ps` cannot settle it). That is
why this half is marked INFERRED.

### Candidates: eliminated vs. not confirmed

**ELIMINATED — remote control on dispatch.** Two independent reasons.
(a) Timing: `remote_control_on_dispatch = true` sits in `~/.config/niwa/config.toml`,
whose mtime is **2026-08-26 20:41**, two weeks old; and remote control has been
on by default for dispatched workers since `b364915` (2026-06-30, PR #181).
Nothing about it changed today. (b) Direct evidence: all seven holds, today's
included, name the sender as `uds:/run/user/1000/cc-socks/<pid>.sock` with a
verified local pid — **local Unix domain sockets, not bridge addresses.** These
messages never left the machine, so the cross-machine send gate is not in play
regardless of how peer discovery lists 116 peers.

**ELIMINATED — `isolatePeerMachines`.** Not set anywhere on this host. Absent
from `~/.claude/settings.json`, `~/.claude/settings.local.json`, and the
instance's materialized `.claude/settings.json`. **There is no managed settings
file on this machine at all** — `/etc/claude-code/managed-settings.json`,
`/usr/local/share/claude-code/`, `/opt/claude-code/` and `/etc/claude/` are all
absent, and a `find` across `/etc`, `/usr/local/share` and `/opt` returns no
`managed-settings.json`. Round 1's open question is closed: no managed policy
can be forcing the sender-side gate on. The only matches for the string
anywhere under `~/.claude` are transcripts of this investigation itself.

**ELIMINATED — the workspace newly declaring bypass.** `permissions = "bypass"`
has been in the workspace config since its initial commit, 2026-04-17
(`git log -L25,25:.niwa/workspace.toml` in the public `dot-niwa` repo lands on
`f9c32ac chore: initial commit`). The public workspace config has had no commit
since 2026-08-20.

**ELIMINATED — a Claude Code upgrade crossing the 2.1.224 boundary today.** The
host was on 2.1.246+ by 08-26 and 2.1.251 by 08-29, both well past 2.1.224. The
relevant boundary this host crossed was **2.1.258**, on 09-02.

**NOT CONFIRMED, NOT FULLY RULED OUT — something in 2.1.260…2.1.267.** The
host did jump 2.1.259 → 2.1.267 today at 23:17Z, about 83 minutes before the
first of today's two holds. I found no positive evidence that anything in that
range touches the inbound-hold path, and the hold's wording is byte-identical
to the 2.1.258 one, which argues the rule is unchanged. But a version jump this
close in time to the recurrence deserves the caveat.

**NOT CONFIRMED — a worktree settings-resolution change in niwa v0.25.0.**
v0.25.0 was installed today at 20:05 EDT and does rewrite
`internal/workspace/worktree_content.go` substantially (+331 lines). But
diffing `v0.24.0..v0.25.0` on that file for `settings|permission|defaultMode`
returns **nothing**, and both of today's holds are in a workspace whose
sessions are not worktree-rooted. No evidence for it; not the explanation.

## Implications

**The urgency goes up, and the framing changes.** The Sep 2/3 cluster was a
whole-path outage that #277 fixed. Today's cluster is the residue: the
posture #277 grants is per-process and evaporates on every re-entry, so the
hold returns for any session pair where one side has been resumed. That is not
a transient that will flip back on its own — it is a structural gap in a file
whose own header comment declares that this exact class of omission is what it
exists to prevent. Every long-running coordinator eventually gets resumed, so
the failure recurs on a schedule set by session lifetime, not by configuration.

**No niwa-side setting change can be blamed, and none will clear it.** The
three "something got switched on" hypotheses (remote control, a new bypass
declaration, a managed policy) are all eliminated with dated evidence. The
proposed feature cannot be deferred on the theory that the environment drifted
and will drift back.

**The cheapest correct fix is probably not the permission flag at all.** Two
candidates, and the second is stronger. (1) Carry the derived permission mode
through `reentryArgs` alongside `WorkdirGrantArgs`, which restores class
symmetry on resume; this is the minimal repair and closes today's exact
failure. (2) Have niwa materialize `crossSessionInbound: "accept"` into the
instance settings it already writes. That is the setting the hold message
itself names, it is scope-resolved rather than process-scoped so it survives
re-entry for free, and it makes delivery independent of whether the two peers
ever agree on permission class — which is the fragile coupling underneath both
clusters. Option 2 subsumes option 1's symptom without depending on niwa
winning a race against Claude Code's settings-channel semantics a second time.

**One caveat for whoever writes the design.** `crossSessionInbound: "accept"`
turns off a hold that exists to flag a real asymmetry. In this workspace both
sides are niwa-launched under a workspace that already declares
`permissions = "bypass"`, so the asymmetry is an artifact rather than a
signal. That argument should be written down rather than assumed, because it is
the whole justification for the setting.

## Surprises

**The session that was scoping the fix got held by the bug it was scoping.**
Event 4's receiver cwd is
`tsuku+niwa276/public/niwa/.claude/worktrees/scope-dispatch-permission-mode` —
the `/scope` run that produced DESIGN-dispatch-permission-mode was itself a
victim, 2h18m before the fix it authored was installed.

**niwa already has a function for exactly this, and nothing calls it.**
`internal/workspace/permissions.go` exports `WorkerPermissionMode(instanceRoot)`,
which reads the instance's materialized `.claude/settings.json` and returns
`"bypassPermissions"` or `"acceptEdits"`. Its only reference in the tree is its
own test. It was added by PR #87 (2026-04-28) for the since-removed agent mesh,
and #277 reimplemented the same read inline in `dispatch.go` rather than
calling it. Whoever fixes re-entry should reconcile the two rather than add a
third copy.

**Held messages are logged; delivered ones are not.** The only reason this
timeline is reconstructable is that a hold writes a `system`/`warning` event
into the transcript. There is no corresponding delivery record, so "zero holds
for a week" is provable but "messages were flowing that week" can only be
argued from surrounding activity.

**Background sessions are claimed from a pre-warmed spare pool.** This host has
`claude bg-spare` processes alive since 2026-08-27 still pinned to the 2.1.250
binary, which is why two of the Sep 2/3 holds were received by a 2.1.250 session
hours after 2.1.258 was the installed build. Version skew between concurrently
live sessions is normal here, and it is plausibly part of why class mismatches
arise at all.

## Open Questions

1. Does `niwa`'s re-entry path actually drop `--permission-mode` in practice, or
   does Claude Code's `--resume` restore the launch mode from session state?
   The code says niwa never passes it; whether Claude Code remembers it across a
   resume is untested here and is the single highest-value experiment left. It
   is cheap: dispatch a worker, resume it, and compare its class against a
   freshly dispatched peer.
2. What actually distinguishes today's two receivers from the week of silence —
   resume specifically, or merely age (a session launched before v0.24.0 and
   never restarted)? Both fit the evidence.
3. Is there anything in 2.1.260…2.1.267 touching this path? A changelog grep
   came back empty in round 1, but the 83-minute proximity of the upgrade to the
   recurrence is not nothing.
4. Which side does the operator actually see the prompt on? Every receiver here
   is a background session, so the dialog surfaces in Agent View rather than in
   the coordinator's terminal — worth confirming, because it determines whether
   an unattended dispatch stalls silently or visibly.

## Summary

The prompt is not new today: a machine-wide scan found exactly seven holds ever, five of them on 2026-09-02/03 starting 38 seconds after Claude Code 2.1.258 first ran on this host, and two today — with a clean week in between that exactly matches the period when niwa v0.24.0's #277 fix was installed. The Sep 2/3 cluster was 2.1.258 making `permissions.defaultMode` inert in project settings, which #277 repaired via the `--permission-mode` CLI flag; today's recurrence is that repair failing to survive session re-entry, since `dispatch_reentry.go` carries the Codex workdir grant but never the permission mode, so a resumed peer falls out of the bypassing class while a freshly dispatched one stays in it. Remote control, a new bypass declaration, and a 2.1.224-boundary upgrade are all eliminated with dated evidence, and `isolatePeerMachines` is set nowhere on this machine — there is no managed settings file at all — so no environment drift will clear this on its own.
