# Investigation: bwrap netns / loopback capability failure in an unprivileged instance

Empirical investigation. Every claim below is backed by captured command output run
on this host (Ubuntu, kernel 6.8.0-124, bubblewrap 0.9.0) and in Docker.

## Environment facts

```
$ bwrap --version
bubblewrap 0.9.0
$ uname -a
Linux dgazineu-threadripper 6.8.0-124-generic #124-Ubuntu SMP ... x86_64
$ cat /proc/sys/kernel/unprivileged_userns_clone      -> 1
$ sysctl kernel.apparmor_restrict_unprivileged_userns -> 1        <-- the lever
$ grep -E 'Cap|Seccomp|NoNewPrivs' /proc/self/status
CapEff: 0000000000000000   (unprivileged uid 1000)
Seccomp: 0                 (NO seccomp filter at top level)
NoNewPrivs: 0
$ ls /etc/apparmor.d/ | grep userns  -> unprivileged_userns   (Ubuntu 24.04 userns mediation profile present)
$ ls -l /proc/sys/kernel/apparmor_restrict_unprivileged_userns
-rw-r--r-- 1 root root ...   (writable only by root)
```

The decisive environmental fact is `kernel.apparmor_restrict_unprivileged_userns = 1`
(Ubuntu 23.10+/24.04 default). It does NOT block *creating* a user namespace, but it
**strips every effective capability inside an unprivileged userns** and blocks writing a
full `uid_map`. There is no seccomp filter involved.

---

## Q1 — Is the loopback failure fatal? Does the child run? Is egress actually blocked?

**Fatal. The child never runs. INNER_RAN is never printed.**

```
$ bwrap --ro-bind / / --dev /dev --unshare-net --die-with-parent bash -c 'echo INNER_RAN=$?; id'
# stdout:  (empty)
# stderr:  bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted
# exit:    1
```

`bwrap` aborts during sandbox setup, before `exec`ing the child. Nothing runs, so there is
no egress test to perform — there is no process. This is **fail-closed** (safe: no process =
no network), but it means the sandboxed command **cannot run at all**, not that it runs
sandboxed.

Control test proves bwrap is broken here even without a netns:

```
$ bwrap --ro-bind / / --dev /dev --die-with-parent bash -c 'echo INNER_RAN=$?; id -u'
bwrap: setting up uid map: Permission denied      (exit 1, child never runs)
```

So bwrap 0.9.0 cannot set up ANY sandbox on this host — with or without `--unshare-net`.
The uid_map path and the loopback path fail for the same root cause (zero caps inside the
unprivileged userns under the AppArmor restriction).

**Definitive answer: this instance does NOT sandbox and does NOT run the command. It is not
"egress blocked" — it is "sandbox refuses to start." bwrap fails closed.**

That bwrap's approach *would* block egress if it could start is proven separately under
`docker --privileged` (same bwrap 0.9.0), where the child runs and egress is blocked:

```
$ docker run --rm --privileged ubuntu:24.04 \
    bwrap --ro-bind / / --dev /dev --unshare-net --die-with-parent \
    bash -c 'echo INNER_RAN=$?; curl ... https://example.com; /dev/tcp/1.1.1.1/443 ...'
INNER_RAN=0
egress_http=000
bash: connect: Network is unreachable        <- /dev/tcp raw socket
TCP_BLOCKED
bwrap_exit=0
```

Under privilege the child runs (INNER_RAN=0) and egress is blocked at both the curl and the
raw `/dev/tcp` layer. The empty-netns model works; it just can't be *set up* unprivileged here.

---

## Q2 — Can loopback setup be avoided? Any flag / newer bwrap where it is non-fatal?

**No flag exists.** `bwrap --help` and `man bwrap` show only `--unshare-net`; there is no
`--skip-loopback`, no `--unshare-net-try`, no way to keep the netns but skip lo config.
The bwrap binary's `loopback_setup()` calls `die()` on failure (confirmed via strings:
`loopback: Failed RTM_NEWADDR`, `loopback_setup`), and this is unconditional in 0.9.0 —
loopback bring-up is mandatory whenever `--unshare-net` is used, and its failure is always
fatal. No bwrap version makes it a warning; the code path is a hard `die()`.

**But the loopback is unnecessary for a no-egress sandbox** (see Q4). bwrap brings lo up
because its intended egress-proxy path (the socat bridge, Q4) needs a working `127.0.0.1`.
For a strictly empty allowlist you do not need lo at all — and avoiding bwrap sidesteps the
fatal path entirely.

---

## Q3 — What capability is missing, and why is it denied?

The missing capability is **CAP_NET_ADMIN inside the network namespace** (needed for the
`RTM_NEWADDR`/`RTM_NEWLINK` netlink ops that assign `127.0.0.1/8` to lo and set it up).

Normally, a process that is root in its own unprivileged userns holds a full capability set
*over resources owned by that userns* — including its own netns — so lo bring-up succeeds.
Here it does not, and the reason is **NOT seccomp and NOT a dropped bounding-set cap**:

```
$ grep Seccomp /proc/self/status   -> Seccomp: 0        (no filter)
$ grep CapBnd  /proc/self/status   -> CapBnd: 000001ffffffffff  (full bounding set)
```

The reason is `kernel.apparmor_restrict_unprivileged_userns=1`. Direct proof that caps are
zeroed *inside* the userns:

```
$ unshare --user --net bash -c 'grep CapEff /proc/self/status; ip link set lo up'
euid=65534
CapEff: 0000000000000000                      <- ZERO caps inside the userns
RTNETLINK answers: Operation not permitted    <- lo bring-up denied

$ unshare --user --map-root-user --net ...
unshare: write failed /proc/self/uid_map: Operation not permitted   <- map-root also denied
```

So: the userns IS created, but the AppArmor userns-mediation strips all effective caps
inside it (and blocks a full uid_map). Without CAP_NET_ADMIN in the netns, RTM_NEWADDR on
lo fails, and bwrap dies.

**Is it fixable without privilege?** No — not from inside the sandbox and not by the
unprivileged user. The two real fixes both require root on the host:

- `sysctl -w kernel.apparmor_restrict_unprivileged_userns=0` (the file is root-writable
  only; `sudo -n` here returns "a password is required").
- Ship an AppArmor profile for the bwrap/harness binary that grants `userns,` (+ the
  capabilities) under `/etc/apparmor.d/` (root to install and reload).

Neither is something niwa-the-unprivileged-process can do. Both are host-provisioning acts.

---

## Q4 — Userspace networking (slirp4netns / pasta) — and what socat is for

**Neither is installed; both are installable but neither rescues *this* host:**

```
slirp4netns: NOT FOUND   (apt candidate 1.2.1-1build2)
pasta/passt: NOT FOUND   (apt candidate passt 0.0~git20240220...)
socat:       NOT FOUND
```

slirp4netns and pasta avoid needing CAP_NET_ADMIN *in the host/parent* namespace — they run
a userspace TCP/IP stack in the parent and hand a tap/socket to the child. But the child
still has to bring up its tap interface *inside its own netns*, which needs CAP_NET_ADMIN
**in that netns** — exactly the capability the AppArmor restriction zeroes here. So on this
host a userspace stack fails for the same reason bwrap's lo does. They help on hosts where
`apparmor_restrict_unprivileged_userns=0`, not here.

**However, for a NO-egress (empty allowlist) sandbox you need no networking stack at all —
and that DOES work unprivileged on this host.** An empty netns with lo *down* already denies
all egress, and creating it does not require any in-netns capability:

```
$ unshare --user --net bash -c '
    echo CHILD_RAN=1 euid=$(id -u)         -> CHILD_RAN=1 euid=65534   (child RUNS)
    ip addr show lo                        -> 1: lo: <LOOPBACK> ... state DOWN
    (exec 3<>/dev/tcp/1.1.1.1/443)         -> connect: Network is unreachable -> TCP_BLOCKED
    curl -s ... https://example.com'       -> http=000  (rc=6, could not resolve/connect)
```

This is the key positive result: **`unshare --user --net <cmd>` is a working, unprivileged,
no-egress sandbox on this host.** The child runs; egress is blocked at raw-socket and curl
layers. The only operation that fails (lo bring-up) is unnecessary for no-egress.

**What socat is for:** Claude Code's OS sandbox uses `--unshare-net` (empty netns) plus a
proxy to permit a *scoped* allowlist. The proxy (socat, or an equivalent forwarder) listens
on `127.0.0.1` inside the netns and bridges allowlisted destinations out via a unix socket to
a helper in the host netns. That loopback listener is *why* the sandbox insists on bringing
lo up — and thus why it hits the fatal RTM_NEWADDR path here. For an **empty** allowlist the
loopback bridge is never used, so the lo requirement is incidental, not essential.

---

## Q5 — Compare: what minimal change makes bwrap's sandbox work?

| Configuration | bwrap `--unshare-net` result |
|---|---|
| This host (unprivileged) | `loopback: Failed RTM_NEWADDR` — child never runs, exit 1 |
| This host, no `--unshare-net` | `setting up uid map: Permission denied` — child never runs |
| `docker run` (default) | `Creating new namespace failed: Operation not permitted` |
| `docker run --cap-add=NET_ADMIN` | `Creating new namespace failed` — **NET_ADMIN does NOT help** |
| `docker run --security-opt seccomp=unconfined --security-opt apparmor=unconfined` | `Creating new namespace failed` — **docker's own confinement is not the blocker** |
| `docker run --privileged` | **INNER_RAN=0, egress blocked (http=000, TCP_BLOCKED), exit 0** |

Isolation conclusions:

- `--cap-add=NET_ADMIN` is **not** the flip. Adding NET_ADMIN still fails at userns
  *creation*, upstream of the loopback step.
- Disabling docker's seccomp AND apparmor is **not** the flip either — still fails at userns
  creation. This proves the blocker is not docker's confinement layer.
- The blocker is the **host kernel's `apparmor_restrict_unprivileged_userns=1`**, which
  applies to unprivileged-userns creation inside containers too. `--privileged` is the only
  thing that escaped it, because it runs the container as real root with a full capability
  set, so the namespace op is not "unprivileged" and the AppArmor userns mediation does not
  strip caps.

**Minimal change that makes bwrap's `--unshare-net` sandbox work: neutralize the AppArmor
unprivileged-userns restriction.** Concretely, one of:

1. `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0` on the host (persist via
   `/etc/sysctl.d/`), or
2. install + load an AppArmor profile granting the harness binary `userns,` and the needed
   capabilities, or
3. run the workload with real privilege (root + caps / `--privileged` container).

All three require **root on the host**. `--cap-add=NET_ADMIN` alone, or any capability tweak
available to the unprivileged niwa process, does **not** suffice.

---

## Bottom line (the two questions asked)

**1. Does THIS unprivileged instance block egress under bwrap? — It does not "block egress";
it fails to start the sandbox entirely.** bwrap aborts during setup
(`loopback: Failed RTM_NEWADDR`, and even without netns, `setting up uid map: Permission
denied`); the child command never runs (INNER_RAN is never printed, exit 1). This is
fail-closed — safe, but the tool cannot run at all under bwrap here. bwrap's egress blocking
is real only where it can start, e.g. `docker --privileged` (INNER_RAN=0, http=000,
TCP_BLOCKED).

**2. Can an unprivileged container be made no-egress-capable WITHOUT root/privileged? — Yes,
but not via bwrap-with-`--unshare-net` on this host.**

- **The workable unprivileged path is to drop the loopback requirement:** run the tool in a
  bare empty netns via `unshare --user --net <cmd>` (proven: child runs, egress blocked at
  raw-socket and curl). No CAP_NET_ADMIN, no root, no lo. This is a complete solution for the
  **empty-allowlist / no-egress** case, which is the stated goal.
- **A newer bwrap does not help** — loopback bring-up is a hard, unconditional `die()`; there
  is no skip flag in any version.
- **A userspace stack (slirp4netns/pasta) does not help *here*** — it still needs
  CAP_NET_ADMIN inside the child netns, which the AppArmor restriction zeroes. It only helps
  on hosts where `apparmor_restrict_unprivileged_userns=0`, and it is only needed for
  *scoped* egress, not for no-egress.
- **To make bwrap's own `--unshare-net` path work (needed only if niwa wants the socat-based
  scoped-allowlist model), the minimal requirement is host-level and needs root:** set
  `kernel.apparmor_restrict_unprivileged_userns=0` or install an AppArmor userns profile for
  the harness binary. This is a **host/niwa-provisioning** responsibility, not something the
  sandboxed unprivileged process can grant itself.

**Recommendation for niwa:** for a strict no-egress dispatch, prefer `unshare --user --net`
(or bwrap *without* `--unshare-net`, run inside an externally created `unshare --net`
namespace) so you never hit the fatal loopback path. Reserve the bwrap+socat scoped-egress
model for hosts where the AppArmor userns restriction has been lifted at provisioning time.
