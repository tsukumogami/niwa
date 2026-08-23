# Lead: Environment — can an agent construct a userns-capable environment here?

**Spike, measurement-first. Date: 2026-08-21.**

| | |
|---|---|
| Host | `dgazineu-threadripper` (bare metal, not a container) |
| OS | Ubuntu 24.04.4 LTS |
| Kernel | `6.8.0-137-generic` |
| codex | `codex-cli 0.147.0` |
| bubblewrap | `0.11.2` |
| docker | server `29.7.2` (reachable) |

## Headline

**Yes — and no container is needed.** A userns-capable, `codex sandbox`-capable
environment already exists on this machine. The blocking symptom was a **PATH
ordering accident**, not a missing capability. The one-line fix is putting
`/home/dgazineu/.tsuku/tools/current` ahead of
`/home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin` on `PATH`.

Verified end to end: `codex sandbox` runs, and all three sandbox modes
(`read-only`, `workspace-write`, `danger-full-access`) enforce correctly.

Answers to the four DELIVER questions, in one place:

1. **Why userns is denied:** AppArmor, via Ubuntu 24.04's
   `kernel.apparmor_restrict_unprivileged_userns=1`. Not seccomp (mode 0, zero
   filters), not capabilities (full bounding set), not a container.
2. **Can an agent construct a userns-capable environment from here? YES**, with
   zero privilege and zero setup — the privileged half was already done by
   `niwa setup-sandbox`, which installed an AppArmor profile granting `userns` to
   `/home/dgazineu/.tsuku/tools/current/bwrap`. Only `PATH` was pointing past it.
   Recipe in section 3. A measured Docker fallback is in section 4 for hosts
   without that profile.
3. **CI:** `ubuntu-latest` does **not** allow it by default, but a workflow can
   unlock it itself with passwordless `sudo`. niwa already ships a workflow that
   does. Section 6, with citations.
4. **Cost:** negligible for the host path (~80-100 ms per sandbox invocation, no
   image, no root, no checkout copy). The Docker fallback costs near-`--privileged`
   isolation, which is a poor trade for sandbox testing. Section 3 and 4.

---

## 1. Where am I?

**Not a container.** This is the bare host.

| Probe | Value |
|---|---|
| `/proc/1/cgroup` | `0::/init.scope` (host systemd, not a container cgroup) |
| `/proc/1/comm` | `systemd` |
| `/.dockerenv` | absent |
| `systemd-detect-virt` | `none` |
| hostname | `dgazineu-threadripper` |
| uid | `1000(dgazineu)`, groups include `sudo`, `docker`, `lxd`, `libvirt` |

**Capabilities and seccomp are NOT the cause:**

```
CapEff: 0000000000000000      <- normal unprivileged user, expected
CapBnd: 000001ffffffffff      <- FULL bounding set, nothing dropped
NoNewPrivs:      0            <- not set
Seccomp:         0            <- NO seccomp filter (mode 0 = disabled)
Seccomp_filters: 0
```

**Sysctls:**

```
kernel.unprivileged_userns_clone         = 1        <- userns clone ALLOWED
user.max_user_namespaces                 = 127539   <- plenty available
kernel.apparmor_restrict_unprivileged_userns = 1    <- *** THE CAUSE ***
kernel.apparmor_restrict_unprivileged_unconfined = 0
```

### The precise reason userns is denied

It is **AppArmor**, specifically Ubuntu 24.04's
`kernel.apparmor_restrict_unprivileged_userns=1`. Not seccomp, not a dropped
capability, not a container boundary, not `user.max_user_namespaces`.

The mechanism is subtle and worth stating exactly, because it explains why the
two failure messages in the brief differ. Kernel audit log, captured live:

```
apparmor="AUDIT"  operation="userns_create" class="namespace"
  info="Userns create - transitioning profile" profile="unconfined"
  comm="unshare" requested="userns_create" target="unprivileged_userns"

apparmor="DENIED" operation="open" class="file"
  info="Failed name lookup - disconnected path" error=-13
  profile="unprivileged_userns" name="proc/3502868/uid_map"
  comm="bwrap" requested_mask="wr" denied_mask="wr"
```

So: the `unshare(CLONE_NEWUSER)` call **succeeds**. The kernel then transitions
the process out of `unconfined` and into the stock `unprivileged_userns`
profile (`/etc/apparmor.d/unprivileged_userns`), whose first two rules are:

```
audit deny capability,
audit deny change_profile,
```

The namespace exists but is stripped of every capability, and the process can no
longer write its own `/proc/self/uid_map` (the "disconnected path" denial).
That produces the two distinct errors:

- `unshare --user --map-root-user true` — the process writes its **own**
  `uid_map` from inside the new (now-confined) namespace → `Operation not
  permitted`.
- `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted` — codex's
  parent process writes the **child's** `/proc/<pid>/uid_map` from outside
  (still unconfined, so that succeeds), then the confined child tries to bring
  up `lo`, which needs `CAP_NET_ADMIN` — denied by `audit deny capability`.

Both are the same root cause seen from two angles.

**Fixability from inside:** the sysctl flip
(`sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`) is root-only, and
so is installing an AppArmor profile. Neither is doable as uid 1000. **But the
privileged step was already taken on this machine** — see below.

---

## 2. The profile that already exists (the actual answer)

`/etc/apparmor.d/niwa-bwrap` is installed and loaded:

```
# Managed by 'niwa setup-sandbox'. Grants bwrap the userns capability on a
# hardened kernel (apparmor_restrict_unprivileged_userns=1) so the Claude Code
# OS sandbox can create its network namespace. Least-privilege: scoped to this
# one binary, rather than relaxing the kernel restriction for every binary.
abi <abi/4.0>,
include <tunables/global>

profile niwa-bwrap /home/dgazineu/.tsuku/tools/current/bwrap flags=(unconfined) {
  userns,
  include if exists <local/niwa-bwrap>
}
```

`niwa setup-sandbox` is the documented, root-only, run-once opt-in step. It has
already been run here (profile dated Jul 11).

The profile is **path-scoped to exactly one binary**:
`/home/dgazineu/.tsuku/tools/current/bwrap` — which is an 859-byte `/bin/sh`
wrapper that sets `LD_LIBRARY_PATH` and `exec`s the real
`/home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin/bwrap`.

### Measured: profiled path works, raw path does not

```
$ /home/dgazineu/.tsuku/tools/current/bwrap --unshare-user --uid 0 --gid 0 --ro-bind / / id
uid=0(root) gid=0(root) groups=0(root),65534(nogroup)          <- WORKS

$ /home/dgazineu/.tsuku/tools/current/bwrap --unshare-all --ro-bind / / env true
exit=0                                                          <- netns WORKS

$ /home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin/bwrap --unshare-user --ro-bind / / id
bwrap: setting up uid map: Permission denied                    <- FAILS
```

### The bug: PATH resolves to the unprofiled binary

Codex prefers `bwrap` from `PATH` and only falls back to its bundled copy
(string extracted from the binary: *"Codex could not find bubblewrap on PATH …
Codex will use the bundled bubblewrap in the meantime"*). In the agent's
default environment:

```
$ command -v bwrap
/home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin/bwrap    <- the UNPROFILED one
```

Because `/home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin` sits **before**
`/home/dgazineu/.tsuku/tools/current` on `PATH`, codex picks the binary the
AppArmor profile does not cover. That is the entire failure.

### Where the bad PATH ordering comes from — likely a niwa bug

The ordering is not from the user's shell rc files (`grep bubblewrap` across
`.bashrc`, `.profile`, `.bash_profile` → no hits). It is injected into the
agent's environment. Positions of the tsuku entries in the agent's ambient
`PATH`:

```
 1: /home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin   <- UNPROFILED bwrap, position 1
 2: /home/dgazineu/.tsuku/tools/socat-1.8.1.3/bin
 3: /home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin   (dup)
 4: /home/dgazineu/.tsuku/tools/socat-1.8.1.3/bin       (dup)
 5: /home/dgazineu/.tsuku/bin
 6: /home/dgazineu/.tsuku/tools/current                 <- PROFILED wrapper, position 6
```

`bubblewrap` and `socat` are exactly niwa's two declared Linux runtime
dependencies (per `niwa setup-sandbox --help`), so niwa is prepending the
**versioned** tool directories for its own sandbox use — and in doing so it
shadows the very `tools/current/bwrap` wrapper that its own AppArmor profile is
pinned to. niwa installs the profile for one path and then puts a different,
unprofiled path to the same program ahead of it on `PATH`.

This is worth reporting upstream to niwa: either the profile should cover the
real binary path, or the PATH injection should prefer `tools/current`.

---

## 3. The recipe (no container, no root, no image)

```sh
export PATH=/home/dgazineu/.tsuku/tools/current:$PATH
```

That is it. Measured result:

```
which bwrap: /home/dgazineu/.tsuku/tools/current/bwrap
$ codex sandbox -- /usr/bin/env true
exit=0
```

### Sandbox enforcement verified (all measured, not assumed)

| Mode | write cwd | write `$HOME` | network |
|---|---|---|---|
| default (`read-only`) | BLOCKED (`Read-only file system`) | BLOCKED | BLOCKED |
| `-c sandbox_mode="workspace-write"` | **WROTE** | BLOCKED | BLOCKED |
| `-c sandbox_mode="danger-full-access"` | — | — | **NET_OK** |

Other measurements inside the sandbox:
- `id` → `uid=1000(dgazineu) … groups=1000,65534(nogroup)` (userns mapped)
- Go toolchain is visible: `/opt/go/bin/go`, `go version go1.23.4 linux/amd64`
- `-c sandbox_permissions=["disk-full-read-access"]` works (read `/etc/os-release`)
- **No auth required.** `codex sandbox` is a pure local exec wrapper; it never
  touches `~/.codex/auth.json`. No credentials need to enter any test surface.

Note: `sandbox_permissions=["disk-write-cwd"]` is **not** a valid value — the
working knob is `sandbox_mode="workspace-write"`.

### Real Go-workload friction (measured, and solved)

`workspace-write` makes the **cwd** writable but leaves `$HOME` read-only — and
Go's build cache lives at `$HOME/.cache/go-build`. Running a Go command straight
out of the box fails:

```
$ codex sandbox -c 'sandbox_mode="workspace-write"' -- go vet ./internal/config/...
pattern ./internal/config/...: open /home/dgazineu/.cache/go-build/8d/8df069...-d:
  read-only file system
```

Two working fixes, both verified clean (`go vet` passed, exit 0):

```sh
# A. redirect the cache into the writable workspace
codex sandbox -c 'sandbox_mode="workspace-write"' -- \
  env GOCACHE="$PWD/.sandbox-gocache" go vet ./...

# B. punch a writable root through for the real cache (faster: reuses warm cache)
codex sandbox \
  -c 'sandbox_mode="workspace-write"' \
  -c 'sandbox_workspace_write.writable_roots=["/home/dgazineu/.cache/go-build"]' \
  -- go vet ./...
```

Prefer **B** for a test harness — it keeps the warm build cache, so repeated runs
stay fast. **A** is the more hermetic choice if cache isolation matters, at the
cost of a cold build every time.

Note also: passing `-C/--cd` makes `--permission-profile` a *required* argument.
Simply `cd` into the repo before invoking `codex sandbox` instead.

### Startup overhead (measured)

```
codex sandbox -- /usr/bin/env true   ->  0.08s, 0.10s, 0.08s
bwrap --unshare-all ... env true     ->  0.01s
```

**~80-100 ms per `codex sandbox` invocation.** Cheap enough to call per test
case rather than amortizing across a suite.

### Cost

Effectively zero. No image, no container, no download, no setup time, no root.
The repo checkout and Go toolchain are already visible from inside the sandbox
because it is the same filesystem, bind-mounted read-only (or workspace-write).
The only durable requirement — the AppArmor profile — is already installed.

---

## 4. Docker — reachable, and measured as a portable fallback

- `/var/run/docker.sock` — present, `srw-rw---- root:docker`, user is in `docker`.
- `docker` at `/usr/local/bin/docker`; **`podman` not installed**.
- `docker info` succeeds: **server 29.7.2**, security options
  `[name=apparmor name=seccomp,profile=builtin name=cgroupns]`.

So the sibling-container escape hatch **is** available. I measured it anyway, as
a portable fallback for machines that lack the `niwa setup-sandbox` profile.

### Measured: plain `unshare` across Docker privilege levels (image `debian:12`)

Command: `unshare --user --map-root-user id`

| `docker run` flags | Result |
|---|---|
| *(default)* | `unshare failed: Operation not permitted` |
| `--security-opt apparmor=unconfined` | `unshare failed: Operation not permitted` |
| **`--security-opt seccomp=unconfined`** | **`uid=0(root) gid=0(root)` — WORKS** |
| `--cap-add SYS_ADMIN` | WORKS |
| `--privileged` | WORKS |

**Minimum for bare userns: `--security-opt seccomp=unconfined`.**

Note the inversion, which is the interesting part: inside a container, AppArmor
is **not** the blocker — Docker's default **seccomp** profile is (it blocks
`unshare(CLONE_NEWUSER)`). Relaxing AppArmor alone changes nothing. And the host's
`apparmor_restrict_unprivileged_userns` does not reach into the container,
because containers run under the `docker-default` profile rather than
`unconfined`, and the restriction only targets `unconfined`.

### Measured: full `codex sandbox` in Docker needs more

`codex` is a **static-pie** ELF (`statically linked`), as is its bundled
`codex-resources/bwrap` — so both bind-mount cleanly into any image, no
compilation, no package install. Important layout gotcha: codex looks for the
bundled bwrap at `codex-resources/bwrap` **next to the executable**, but tsuku
installs it one level up (`codex-0.147.0/codex-resources/bwrap`, sibling of
`bin/`). Mounting only the codex binary panics with *"bubblewrap is unavailable"*.

Escalating from a working userns, `codex sandbox` still failed until all three
relaxations were present:

| flags | result |
|---|---|
| `seccomp=unconfined` | `bwrap: Failed to make / slave: Permission denied` |
| `seccomp=unconfined` + `--cap-add SYS_ADMIN` | `Failed to make / slave` |
| `--cap-add SYS_ADMIN` | `Failed to make / slave` |
| `seccomp=unconfined` + `apparmor=unconfined` | `bwrap: loopback: Failed RTM_NEWADDR` |
| `apparmor=unconfined` + `--cap-add SYS_ADMIN` | `bwrap: pivot_root: Operation not permitted` |
| **all three** | **rc=0 — WORKS** |
| `--privileged` | rc=0 — WORKS |

**Minimum for `codex sandbox` in Docker:**

```sh
docker run --rm \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --cap-add SYS_ADMIN \
  -v /home/dgazineu/.tsuku/tools/codex-0.147.0/bin/codex:/usr/local/bin/codex:ro \
  -v /home/dgazineu/.tsuku/tools/codex-0.147.0/codex-resources/bwrap:/usr/local/bin/bwrap:ro \
  -e HOME=/root -e CODEX_HOME=/root/.codex \
  debian:12 \
  sh -c 'mkdir -p /root/.codex /work && cd /work && codex sandbox -- /usr/bin/env true'
```

Each of the three flags is load-bearing — dropping any one reproduces a distinct
failure (rows above). Note that the
`seccomp=unconfined + apparmor=unconfined` row reproduces the *exact* host error
from the brief (`RTM_NEWADDR`), which independently confirms the diagnosis:
making the container `unconfined` re-exposes it to the host's AppArmor userns
restriction.

**No credentials were placed in any container.** `codex sandbox` needs no auth;
an empty `$CODEX_HOME` is sufficient (it only has to *exist* — codex errors out
if `CODEX_HOME` points at a missing path).

**Cleanup: all containers used `--rm` and are gone; no images were built.**
Verified: `docker ps -a --filter name=spike_` returns nothing, no `spike` images.

### Why the host path is still better

This container recipe costs a `--cap-add SYS_ADMIN` plus two `unconfined`
relaxations — which is close to `--privileged` and is a genuinely weak boundary.
Using it to *test a sandbox* is somewhat self-defeating. Prefer the host path
(section 3) whenever the AppArmor profile is installed; keep this recipe as the
portable fallback for machines where it is not.

Recorded for completeness: `/etc/subuid` and `/etc/subgid` both contain
`dgazineu:100000:65536`, and `newuidmap` is present — so a rootless-podman path
would also be plausible if podman were installed.

## 5. Rootless podman

Not installed. Not pursued — the host-local path works.

---

## 6. CI: GitHub-hosted runners — **already solved in this workspace**

The strongest evidence is not on the web, it is in the repo. **niwa already ships
a CI workflow that does exactly this**, and it is the authoritative answer:

`public/niwa/.github/workflows/watch-live-egress.yml`

Its own comments state the problem and the fix verbatim:

> an Ubuntu runner where `niwa setup-sandbox` can unlock the AppArmor userns
> restriction (**ubuntu-24.04 restricts it by default**; the step below installs
> the least-privilege bwrap profile via sudo, **which is passwordless on GitHub
> runners**).

**So: no, `ubuntu-latest` does NOT permit unprivileged userns out of the box**
— it is Ubuntu 24.04 and carries the same
`kernel.apparmor_restrict_unprivileged_userns=1` as this host. **But yes, a
workflow can fix it itself**, because GitHub-hosted runners give passwordless
`sudo`. The working steps, lifted from that workflow:

```yaml
- name: Install sandbox backend (bubblewrap + socat)
  run: sudo apt-get update && sudo apt-get install -y bubblewrap socat

- name: Unlock the OS sandbox on this runner (AppArmor userns profile)
  run: |
    go build -o /tmp/niwa ./cmd/niwa
    BWRAP="$(command -v bwrap)"
    sudo /tmp/niwa setup-sandbox --apply-profile --bwrap-path "$BWRAP" || true
    if bwrap --ro-bind / / --unshare-net --die-with-parent true; then
      echo "netns OK: the OS sandbox can be enforced on this runner"
    else
      echo "::warning::bwrap still cannot create a netns"
    fi
```

Two things worth lifting from this for the codex work:

1. **`niwa setup-sandbox` has hidden root-only flags** `--apply-profile` and
   `--bwrap-path <path>`, not shown in `--help`. Confirmed present in the
   installed `niwa 0.23.0` (running it non-root yields
   `setup-sandbox: --apply-profile must run as root`). `--bwrap-path` means the
   profile can be pointed at **any** bwrap binary — including
   `/home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin/bwrap`, which would fix
   the local PATH problem permanently instead of via a PATH prefix. It needs
   `sudo`, so the PATH fix in section 3 remains the sudo-free option.
2. **The probe-then-warn pattern is the right harness shape.** That workflow
   verifies with `bwrap --ro-bind / / --unshare-net --die-with-parent true` and
   *skips rather than false-passes* when the sandbox is unavailable. Any
   codex-sandbox test suite should do the same.

### Web research confirms this, with citations

- `ubuntu-latest` still maps to **Ubuntu 24.04**
  ([GitHub docs](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)),
  and the runner images **do not** relax the restriction. The one PR that tried,
  [actions/runner-images#11489](https://github.com/actions/runner-images/pull/11489)
  ("[Ubuntu] Disable apparmor user namespace restrictions"), was **closed
  unmerged** in Jan 2025 — "Workaround for that issue already provided." An
  earlier blanket [#10024](https://github.com/actions/runner-images/pull/10024)
  was [reverted by #10070](https://github.com/actions/runner-images/pull/10070).
  So the Ubuntu default (`=1`, from `/usr/lib/sysctl.d/10-apparmor.conf`) stands.
- Documented CI breakage from exactly this:
  [runner-images#10443](https://github.com/actions/runner-images/issues/10443)
  (skopeo/buildah), [pa11y#724](https://github.com/pa11y/pa11y/issues/724) and
  [mermaid-cli#825](https://github.com/mermaid-js/mermaid-cli/pull/825) (Chrome
  "No usable sandbox!").
- The failure mode matches this host exactly — Qualys reproduces
  `unshare -U -r -m /bin/sh` → `write failed /proc/self/uid_map: Operation not
  permitted` on stock 24.04
  ([advisory](https://www.qualys.com/2025/three-bypasses-of-Ubuntu-unprivileged-user-namespace-restrictions.txt),
  [Ubuntu spec](https://discourse.ubuntu.com/t/spec-unprivileged-user-namespace-restrictions-via-apparmor-in-ubuntu-23-10/37626)).
- **The blunt workaround is maintainer-endorsed** — it is what GitHub pointed to
  when closing #11489: `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`.

Narrower alternatives, all valid on a runner:

- `aa-exec --profile=chrome -- <cmd>` — borrow a stock profile that already has
  `userns,` (what mermaid-cli does; Ubuntu ships ~90 such profiles).
- Hand-write `/etc/apparmor.d/bwrap` with `abi <abi/4.0>,` + `userns,` and
  `sudo systemctl reload apparmor` — this is structurally identical to what
  `niwa setup-sandbox` installs.
- **`sudo apt install apparmor-profiles && sudo apparmor_parser
  /usr/share/apparmor/extra-profiles/bwrap-userns-restrict`** — 24.04 *ships*
  this profile but leaves it disabled; it becomes default-on from 25.04. Cheapest
  path if you do not want to depend on the niwa binary in CI.

### `container:` jobs — avoid, and note a contradiction

The Actions runner builds job containers with `docker create` passing only
`--name/--label/--workdir/--network/-p/-e/-v/--entrypoint` — **no `--privileged`,
no `--security-opt`, no `--user`**
([DockerCommandManager.cs](https://github.com/actions/runner/blob/main/src/Runner.Worker/Container/DockerCommandManager.cs)).
Extra flags are only reachable via `container.options`.

The web research reasoned *from mechanism* that userns should therefore work
inside a job container (docker-default AppArmor has no `userns` rule and a
pre-4.0 ABI, so the permission is silently granted; and Docker's default seccomp
has permitted `CLONE_NEWUSER` since 20.10), while flagging that it could not find
a direct measurement.

**My section-4 measurement contradicts that reasoning.** On Docker 29.7.2, a
default container fails:

```
docker run --rm debian:12 sh -c 'unshare --user --map-root-user id'
  -> unshare failed: Operation not permitted
```

and `--security-opt apparmor=unconfined` alone does **not** fix it, while
`--security-opt seccomp=unconfined` does — i.e. on this Docker version the
default **seccomp** profile is the blocker, not AppArmor. GHA's Docker version
may differ, so I cannot state the GHA container case as settled; but the
"it should just work in a container" reasoning is not safe to rely on.

**Recommendation: run on the runner host and use `sudo`, not a job container.**
That path is measured, documented, and already in use by niwa. If a container is
unavoidable, note that the real-world precedent takes the blunt route: the
official [flatpak-github-actions](https://github.com/flathub-infra/flatpak-github-actions)
runs `container: { image: ..., options: --privileged }`.

---

## Honest notes / fragility

- The fix is **host-specific and already provisioned**. On a fresh machine the
  privileged `niwa setup-sandbox` step is required first; without it, an
  unprivileged agent has **no** in-process path to userns and would have to fall
  back to the Docker socket.
- The profile pins an **absolute path containing the username**
  (`/home/dgazineu/...`). It is not portable across users or machines.
- It also pins the tsuku `current` shim. If tsuku's `current/bwrap` wrapper is
  removed, renamed, or restructured, the profile silently stops matching and the
  failure returns as the same confusing `RTM_NEWADDR` error.
- The failure mode is **silent and misleading**: codex falls back to its bundled
  bubblewrap without a clear diagnostic, and the resulting error names a netlink
  operation rather than the real cause (AppArmor). Anyone debugging this without
  reading the kernel audit log will chase the wrong thing.
- **Recommendation:** rather than relying on ambient `PATH`, any test harness
  that shells out to `codex sandbox` should either prepend
  `/home/dgazineu/.tsuku/tools/current` explicitly, or probe with
  `bwrap --unshare-user --ro-bind / / /bin/true` at startup and fail loudly with
  the AppArmor explanation if it does not work.
