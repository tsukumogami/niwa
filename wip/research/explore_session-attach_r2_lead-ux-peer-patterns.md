# Lead: Peer-tool human-takeover patterns

Round 2 UX investigation for `niwa session attach` (issue #117). Surveys six tools that ship a "connect to a long-running thing already in flight" verb, captures their concrete syntax, and maps their conventions onto the niwa session attach design space.

PR #115 cross-check: confirmed via `gh pr view 115` — the in-flight mesh-reliability design touches `niwa_list_sessions` (adds a computed `daemon` sub-object) but does not redefine the tabular `niwa session list` columns. The `AVAILABILITY` column proposed for attach is orthogonal and unblocked.

---

## Findings

### tmux

The dominant precedent for terminal-multiplexer attach UX. Every developer has muscle memory for it.

- **Attach command:** `tmux attach-session -t <target>` — aliased to `tmux attach`, `tmux a`. Bare `tmux attach` reattaches the most-recently-used session. Targeting is by session name (`-t mywork`) or session ID (`-t $0`).
- **Detach mechanism:** key combo `Ctrl-b d` (default prefix + `d`), intercepted inside the tmux client. Drops you back to the parent shell; the server keeps running. Also `tmux detach-client [-s target-session]` from a separate shell to kick a client off remotely. `tmux kill-session -t <id>` ends the work.
- **List output indicator:** `tmux ls` (alias of `list-sessions`). Format:
  ```
  0: 3 windows (created Mon Apr  7 09:12:01 2025) (attached)
  mywork: 1 windows (created Mon Apr  7 13:44:58 2025)
  ```
  The literal trailing `(attached)` annotation is the indicator. Detached sessions just omit the marker — there is no `(detached)` token.
- **Force / takeover naming:** `tmux attach -d` ("attach and detach others"). The `-d` flag re-attaches you and forcibly disconnects every other client currently attached to that session. There is no `--force`/`--steal`/`--takeover` keyword — it is a single letter that means "detach the other guy first."
- **Clean exit:** detaching with `Ctrl-b d` leaves the session running on the server. Closing the terminal/window: also leaves the session running, because the server is a separate daemon. The process inside the session never sees the disconnect.
- **Crash / network drop:** identical to clean detach from the session's perspective — the server didn't lose anything. The orphan tmux client process gets reaped; the next `tmux attach` just reconnects.
- **Invalid attach errors:** terse, lowercase, no period.
  - `no server running on /tmp/tmux-1000/default`
  - `can't find session: foo`
  - `sessions should be nested with care, unset $TMUX to force` — when you try to attach from inside an existing tmux session.

### GNU screen

Older, more verbose. Distinguishes "detached" and "attached" explicitly.

- **Attach command:** `screen -r [pid.tty.host]` (resume). `screen -x` for multi-attach (multiple users see the same screen, no kicking). `screen -dr` is the canonical "detach the existing client first, then attach me" idiom.
- **Detach mechanism:** key combo `Ctrl-a d` (default prefix). Also `screen -d <session>` from outside detaches the named session's existing client without attaching you.
- **List output indicator:** `screen -ls`:
  ```
  There are screens on:
          12345.pts-0.host        (Detached)
          12678.pts-1.host        (Attached)
  2 Sockets in /run/screen/S-user.
  ```
  Both states surface in title-cased parentheses. `(Multi attached)` appears when `-x` is in use.
- **Force / takeover naming:** the `-d` flag means "detach a running session." Composed: `screen -dr` (detach then resume), `screen -dRR` (detach, reattach, create-if-missing, force-on-conflict), `screen -D -RR` (the Big Hammer: log out the previous user, create the session if missing, take over). No English keyword — just letters. The double-`R` says "be more aggressive about it."
- **Clean exit:** `Ctrl-a d` detaches; the session stays. `exit` from the inner shell ends the inner program, which usually ends the screen window, which (when the last window closes) ends the session.
- **Crash / network drop:** session goes from `(Attached)` to `(Detached)` automatically when the controlling terminal disappears. The PTY survives; output buffers in the scrollback.
- **Invalid attach errors:**
  - `There is no screen to be resumed matching foo.`
  - `There is a screen on: 12345.pts-0.host (Attached) — There is no screen to be resumed.` — when you try `screen -r` on an already-attached session without `-d`. Punctuation is verbose, sentence-cased.

### kubectl exec / attach

Two verbs, deliberately distinct. The split is the most relevant single design lesson for niwa.

- **Attach command:**
  - `kubectl attach <pod> [-c <container>] [-i] [-t]` — connects your terminal's stdin/stdout to the container's *PID 1 main process*. There is one main process per container; multiple `kubectl attach` clients can connect concurrently and all see the same stdout.
  - `kubectl exec <pod> [-c <container>] -it -- <command>` — *spawns a new process* inside the container and gives you a terminal to it. Each `exec` is independent.
- **Detach mechanism:** Ctrl-C in the attached client only kills your local kubectl, *unless* `--stdin --tty` is set, in which case Ctrl-C is forwarded to the remote process and may kill it. There is no built-in detach key sequence the way tmux/screen have one. Closing the terminal is the detach. Documented: "If you don't see a command prompt, try pressing enter." Famously janky.
- **List output indicator:** `kubectl get pods` doesn't show "is anyone attached." Attachment is an in-flight RPC, not a persistent state, so it isn't listable. `kubectl get events` may show `Started`/`Killed`. This is a design choice: kubectl treats attach as ephemeral, not a session.
- **Force / takeover naming:** no concept of "kicking another attacher" — concurrent attachers all share. `kubectl delete pod --force --grace-period=0` is the closest thing to a takeover-of-state, and it's about destroying the pod, not stealing the attach. The `--force` flag specifically means "skip graceful termination," and `--grace-period` controls SIGTERM-to-SIGKILL delay.
- **Clean exit:** depends entirely on whether `-i -t` was set. Attach with `-i` forwards your stdin's EOF to the container, which usually terminates PID 1, which terminates the pod. Without `-i`, exiting kubectl is invisible to the container.
- **Crash / network drop:** the API server's stream RPC times out; the container is unaffected. No state to clean up because there was no attach state.
- **Invalid attach errors:**
  - `Error from server (NotFound): pods "foo" not found`
  - `error: unable to upgrade connection: container not found ("ruby-container")`
  - `error: cannot attach to a container in a completed pod; current phase is Succeeded`
  - `Unable to use a TTY - input is not a terminal or the right kind of file`

### fly ssh console / fly machine ssh

Cloud-CLI ergonomics, takeover-implicit, nothing fancy.

- **Attach command:** `fly ssh console [-a <app>] [--machine <id>] [--select]` — opens a fresh shell on a machine via wireguard tunnel. `fly machine ssh <machine-id>` is the lower-level form. There is no separate "attach to running shell" verb — every invocation is a new exec, kubectl-style.
- **Detach mechanism:** Ctrl-D on the remote shell. There is no protocol-level detach.
- **List output indicator:** `fly machine list` and `fly status` show machine state (`started`/`stopped`/`stopping`) but never "session count" or "attached users." Sessions are not first-class.
- **Force / takeover naming:** none. Multiple developers concurrently `fly ssh console` open independent shells; nobody is kicking anybody.
- **Clean exit:** Ctrl-D / `exit` ends the spawned shell. The machine and its primary process are unaffected.
- **Crash / network drop:** the spawned shell becomes a zombie until the SSH server reaps it (typically <1 minute). No persistent attach state.
- **Invalid attach errors:** `Error: machine ID foo not found in app bar`. Sentence-cased, error-prefixed.

The takeaway from fly: when sessions are not stateful (every connection is a fresh exec), no attach/detach surface is needed at all. Niwa is *not* this case — niwa sessions own a worktree and a `claude --resume` history, which is exactly the state attach needs to mediate.

### ssh ControlMaster

The closest peer to niwa's design (a long-lived background process that *clients* attach to via a Unix socket). Underdocumented but worth studying.

- **Attach command:** transparent. With `ControlMaster auto` and `ControlPath ~/.ssh/cm_%C` configured, every subsequent `ssh host` reuses the existing master. There's no separate attach verb. The user-visible command stays `ssh host`.
- **Detach mechanism:** clients exit normally (`exit` / Ctrl-D); master persists per `ControlPersist`. The master can be killed with `ssh -O exit host` (graceful) or `ssh -O stop host` (refuse-new-connections, drain old).
- **Control commands:** `ssh -O <ctl_cmd> host`. Valid `ctl_cmd`: `check`, `forward`, `cancel`, `exit`, `stop`. This is the most useful prior art for a "control-plane verb on an existing session" surface — terse single-word control commands dispatched to the master via a flag.
- **List output indicator:** none built in. `ssh -O check host` returns `Master running (pid=12345)` or `Control socket connect(/path): No such file or directory` — listing is not surfaced because connection-sharing is opportunistic, not enumerable.
- **Force / takeover naming:** none. Multiple SSH clients to the same master multiplex peacefully — no concept of "kick the other client."
- **Clean exit:** client exits; master decides whether to live based on `ControlPersist`. `ControlPersist 10m` keeps the master 10 minutes after the last client; `ControlPersist yes` keeps it indefinitely.
- **Crash / network drop:** master's TCP connection breaks; master process exits; subsequent clients fall back to "open a fresh connection."
- **Invalid attach errors:** stale-socket footgun. `mux_client_request_session: read from master failed: Broken pipe` — when the socket file exists but the master died. Recovery: delete the socket file by hand, or `ssh -O exit host` (which will also fail if the master is dead).

### docker attach / docker exec

Docker rebrands the same kubectl split with one important flourish.

- **Attach command:**
  - `docker attach <container>` — attach stdin/stdout/stderr to the container's PID 1.
  - `docker exec -it <container> <command>` — spawn a new process.
- **Detach mechanism:** key sequence `Ctrl-P Ctrl-Q` — *the most niwa-relevant detail in this entire doc*. Docker is the one tool that ships a key chord *specifically to detach without killing the foreground process*. Configurable via `--detach-keys` flag or `~/.docker/config.json`'s `detachKeys` setting.

  Crucially, this only works if the container was started with `-t` (TTY allocated). Without `-t`, Ctrl-P Ctrl-Q is just two keystrokes that go to the foreground process.
- **List output indicator:** `docker ps`:
  ```
  CONTAINER ID   IMAGE   COMMAND   CREATED   STATUS          PORTS   NAMES
  abc123def456   nginx   "nginx"   2m ago    Up 2 minutes            web
  ```
  No "attached" indicator. Like kubectl, Docker treats attach as ephemeral.
- **Force / takeover naming:** none. Multiple `docker attach` clients all see the same stdout; stdin is racy (whichever client sends a byte wins). No "kick" verb.
- **Clean exit (without `--detach-keys`):** Ctrl-C / Ctrl-D pass through to PID 1, which usually means *killing the container*. This is the famous Docker footgun. The detach-keys exist specifically to avoid it.
- **Crash / network drop:** container is unaffected; the docker CLI process exits.
- **Invalid attach errors:**
  - `Error response from daemon: No such container: foo`
  - `Error response from daemon: Container abc123 is not running`
  - `unable to attach: tty is not a terminal`

### Other (Terraform Cloud, Vercel)

- **Terraform `force-unlock`:** `terraform force-unlock <LOCK_ID>` — most-cited "stale lock recovery" verb in modern dev tooling. Requires the operator to type the lock ID it printed in the error message (a confirmation of intent). The verb `force-unlock` is two words separated by a hyphen — *not* `--force` on a `unlock` subcommand. This is a strong precedent for "destructive lock takeover deserves its own verb name." Niwa's planned `niwa session detach <id> --force` follows the *opposite* convention (flag, not separate verb), which is defensible but worth flagging.
- **Vercel `vercel promote`:** for promoting a deployment. Not really a takeover pattern — included only to confirm Vercel doesn't have one. Vercel's deployment model is functional-style (each deploy is immutable), so there's no attach surface to take over.
- **`fuser -k <file>`:** Linux primitive for "kick whoever holds this file." Not a peer tool but worth noting because niwa's attach lock is literally a flock — the kernel-level analog already exists.

---

## Synthesis

### Two strongest precedents for niwa to follow

**1. tmux — for verb naming and list-output indicator.**

tmux is where every developer learned the word "attach" in this context. The convention is so universal that picking any other verb (`connect`, `enter`, `take`) would create friction for zero benefit. `niwa session attach <id>` reads exactly the way `tmux attach -t <id>` does, and the round-1 lock-semantics agent already chose to call the lockfile `attach.lock` — verb consistency is already half-baked in.

The trailing-paren list-output convention is also worth borrowing: `(attached)` after the row entry, omitted otherwise, no separate column. But: the Round 1 plan calls out an explicit `AVAILABILITY` column. Recommendation below splits the difference.

**2. Docker — for detach mechanism and the "attach is for a long-lived process" model.**

Docker is the only tool that ships a *key sequence specifically designed to detach without killing the foreground process while preserving the running process for future attach*. That is exactly niwa's situation: the foreground process is `claude --resume`, killing it is bad, the user wants to disconnect cleanly. The Ctrl-P Ctrl-Q chord plus the `--detach-keys` configurability is the right shape.

However: niwa shouldn't try to intercept terminal keystrokes itself. niwa's attach is a process-tree wrapper around `claude --resume`, not a PTY-multiplexer. The key chord lesson translates instead to: **document a clean-detach gesture (exit Claude Code via `/exit` or `Ctrl-D`), and ship `niwa session detach <id> --force` as the out-of-band recovery for terminals that crashed**. The user's exit path inside Claude Code is the equivalent of Docker's detach-keys.

### Where peer tools diverge

- **Verb spelling:** `attach` (tmux, kubectl, docker) vs. `resume`/`-r` (screen) vs. `console`/`ssh` (fly) vs. transparent (ssh ControlMaster). **Recommend `attach`**. Convention is overwhelming.
- **Force semantics:** flag-on-attach (`tmux -d`, `screen -d`/`-dRR`) vs. separate verb (Terraform `force-unlock`, ssh `-O exit`). **Recommend separate verb (`niwa session detach <id> --force`)** — already in the round-1 plan, and Terraform's precedent for "lock recovery is its own verb" is the right safety stance.
- **List indicator format:** `(attached)` annotation (tmux), `(Detached)`/`(Attached)` column-aligned (screen), nothing (kubectl, docker, fly). **Recommend a column** since niwa's `session list` is already columnar — see below.
- **Multi-attach:** allowed read-only (screen `-x`, kubectl, docker), forbidden by default (tmux without `-d`), N/A (ssh CM, fly). **Recommend forbid by default**, consistent with the flock semantics in the round-1 lock-semantics lead.

---

## Implications

Concrete recommendations for the PRD:

### Verb naming → `attach` and `detach`

- `niwa session attach <id>` — primary attach verb. Matches tmux/kubectl/docker.
- `niwa session detach <id>` — out-of-band release. Reads naturally as the inverse of attach. *Not* `release`, *not* `unlock`, *not* `take`/`steal`/`yield`.
- Detaching from inside a live attach happens via the user's normal exit gesture in Claude Code (`/exit` slash command, `Ctrl-D` at the prompt, or process exit). **Niwa does not intercept keystrokes.** The flock is held by the niwa wrapper process; on wrapper exit (any cause), the lock is released by the kernel. This matches Docker's "user controls when to leave" model without the keystroke-interception complexity tmux requires.

### Detach mechanism

Three paths, in order of normalcy:

1. **Clean detach:** user types `/exit` (or whatever Claude Code's quit gesture is) → claude exits → niwa wrapper exits → kernel releases flock. This is the 99% path. No niwa-specific gesture to teach.
2. **Crashed terminal detach:** SSH disconnects, laptop closes, terminal app crashes. Wrapper process gets SIGHUP, exits, kernel releases flock. Self-healing — *unless* the wrapper was disowned/nohup'd, which leaves a stale flock.
3. **Stale-lock detach:** `niwa session detach <id> --force` from a fresh terminal. Reads the lockfile, signals the holder PID, waits for grace period, falls back to deleting the lock and warning that the holder may still be alive. Borrows Terraform's force-unlock confirmation pattern: print the holder's PID/host/user, ask the user to retype the session ID to confirm (or use `--yes` to skip). This is the "force / steal" surface — and naming it `detach --force` (not `--steal` or `--takeover` or `unlock`) keeps the verb pair symmetric.

### Force flag naming → `--force`

**Recommend `--force`**, not `--steal` or `--takeover`. Reasons:

- `--force` is the universal "I know what I'm doing, override the safety" flag in modern CLIs (`rm -f`, `git push --force`, `kubectl delete --force`, `terraform force-unlock` is itself a `force-` verb).
- `--steal` implies adversarial multi-user; niwa's primary use case is single-user-recovering-from-own-crash, where "steal" reads wrong.
- `--takeover` is two syllables longer with no clarity gain.

Pair `--force` with a confirmation prompt by default. Skip the prompt when `--yes` is passed or when stdin isn't a TTY (script-friendly).

### List-output indicator → dedicated `AVAILABILITY` column

The round-1 plan already calls for an `AVAILABILITY` column orthogonal to `STATUS`. Confirm and refine:

```
SESSION ID           STATUS    AVAILABILITY      WORKTREE
sess-abc123          working   free              /home/user/work/feature-x
sess-def456          working   attached(pid=42)  /home/user/work/feature-y
sess-ghi789          done      free              /home/user/work/feature-z
sess-jkl012          working   stale-lock        /home/user/work/feature-w
```

`AVAILABILITY` values:
- `free` — no flock held, ready to attach
- `attached` (or `attached(pid=N)` in `--verbose`) — flock held by a live process
- `stale-lock` — lockfile exists but holder PID is dead; `niwa session detach <id> --force` will recover

This is closer to screen's `(Attached)`/`(Detached)` than to tmux's optional annotation, but column-aligned because niwa's list is column-aligned. Matches the table layout of `kubectl get pods` and `docker ps`. The `STATUS` column stays single-writer (PR #115's concern) because it describes the work, not the human-attach state.

### Stale-lock recovery command → `niwa session detach <id> --force`

Already settled in round-1 plan. Reaffirmed. Alternatives considered and rejected:

- `niwa session unlock` — splits the verb space (`attach`/`detach`/`unlock` instead of `attach`/`detach`). Loses symmetry.
- `niwa session steal` — implies adversarial intent.
- `niwa force-detach` (Terraform-style hyphenated verb) — defensible but breaks the noun-verb-noun pattern (`niwa session <verb>`) niwa has consistently used.

The `--force` flag promotes `detach` from "ask the holder to leave" (signal + wait) to "guarantee the lock is gone when this returns" (signal, wait, then break the lock).

### Error wording

Borrow Docker/kubectl's error-prefix convention (`Error: ...`) and tmux's terseness. Concrete recommended wording:

| Situation | Recommended message |
|-----------|---------------------|
| No such session | `Error: no session with id "sess-xyz" — try \`niwa session list\`` |
| Already attached | `Error: session sess-xyz is attached by pid 4242 (started 2m ago) — use \`niwa session detach sess-xyz --force\` to take over` |
| Session ended | `Error: session sess-xyz has ended (status: done) — its worktree is at /path/to/worktree if you need to inspect` |
| Stale lock | `Warning: lock file present but holder pid 4242 is not running — run \`niwa session detach sess-xyz --force\` to clean up` |
| Trying to attach from inside an attach | `Error: already inside niwa session sess-xyz (NIWA_SESSION_ID is set) — exit Claude Code first` (mirrors tmux's "sessions should be nested with care") |
| Worktree gone | `Error: session sess-xyz's worktree at /path is missing — the session is unrecoverable; remove it with \`niwa session delete sess-xyz\`` |

All errors:
- prefix `Error:` or `Warning:` (matches docker, kubectl)
- lowercase first word after the prefix (matches tmux/git)
- include a remediation hint inside backticks (matches modern Rust/Cargo style)
- name the session ID literally so users can copy-paste

---

## Surprises

- **Docker's Ctrl-P Ctrl-Q is the only key chord across all six tools that means "detach without killing."** tmux/screen have prefix-`d`, but those are *inside an explicit multiplexer*. Docker had to invent a chord because attach is a thinner abstraction. Niwa is closer to Docker's case (no multiplexer) than to tmux's, which validates the recommendation to *not* intercept keystrokes — the alternative is reinventing Docker's detach-keys, which most developers don't even know exists.
- **screen's `-dRR` (the "Big Hammer" — detach existing client, reattach me, create if missing, force on conflict) collapses three semantically distinct operations into two letters.** This is a usability anti-pattern niwa should avoid: each force-step deserves its own observable command.
- **kubectl is the most popular tool with the worst attach UX.** "If you don't see a command prompt, try pressing enter" is genuinely in the docs. Multiple concurrent attachers race on stdin. No detach key. The reason it survives: kubectl-attach is rarely the right tool — `kubectl exec -it` is — so the bad UX is ignored. Niwa's attach is the *primary* interaction, so it cannot afford to be janky.
- **ssh ControlMaster ships a separate `-O <ctl_cmd>` flag for control-plane verbs.** Niwa using subcommands (`niwa session detach`) instead of a control-plane flag (`niwa session -O detach`) is the right call — but ssh's pattern is interesting prior art if niwa ever grows more session control verbs.
- **Terraform's `force-unlock` requires retyping the lock ID for confirmation.** This is a UX detail worth borrowing for `niwa session detach --force` in destructive contexts.

---

## Open Questions

1. **Should `niwa session attach` itself accept `--force` (tmux `-d`-style one-step takeover), or is the explicit two-step (`niwa session detach <id> --force; niwa session attach <id>`) preferable?** Recommendation leans two-step (clearer audit trail, harder to do by accident), but tmux's one-step is iconic. Worth deciding in the PRD.
2. **What happens when the user runs `niwa session attach` and the session is in `working` status with no flock?** Two cases: (a) session was created but never attached — fine, attach normally; (b) session was attached, then the wrapper crashed — also fine, attach normally, the kernel already released the flock. But: should niwa log/notice the second case as "recovered from prior attach"?
3. **Does the `AVAILABILITY` column belong in the default `niwa session list` output, or only behind `--verbose`?** Adding a column has churn cost; PR #115 cross-check confirmed no conflict, so this is purely a column-density question.
4. **Should `niwa session detach --force` also offer a `--signal=TERM/KILL` knob (kubectl `--grace-period` analog), or always SIGTERM-then-SIGKILL on a fixed grace period?** Recommendation: hardcoded grace period for v1, surface a flag only if a real user asks.
5. **Is there an MCP-tool equivalent of attach (so coordinator agents can attach to worker sessions programmatically), and does its name need to mirror the CLI?** This is the `ux-mcp-surface` lead's territory but the naming choice cascades — if the CLI is `attach`, the MCP tool should likely be `niwa_attach_session`.
6. **What gesture do we recommend users teach themselves to clean-detach?** Inside Claude Code: `/exit`? `Ctrl-D`? Closing the terminal? The PRD should pick one canonical recommendation and put it in the help text, even if all three work.

---

## Summary

The dominant precedent is tmux: the verb `attach`, the noun `session`, and the parenthetical "(attached)" annotation are baked into developer muscle memory, and Docker reinforces the verb plus contributes the lesson that "detach without killing" needs a deliberate user-controlled gesture. Recommend `niwa session attach <id>` / `niwa session detach <id> --force` as the verb pair, an explicit `AVAILABILITY` column on `niwa session list` with values `free`/`attached`/`stale-lock`, and Terraform-style confirmation prompts on the destructive `--force` path. The biggest open question is whether `niwa session attach --force` should exist as a one-step takeover (tmux `-d`-style) or whether users must run `detach --force` then `attach` as two distinct steps — a clarity-vs-ergonomics tradeoff the PRD needs to settle.
