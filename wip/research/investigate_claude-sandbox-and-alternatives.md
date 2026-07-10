# Claude Code sandbox requirements vs. unprivileged no-egress alternatives

Investigation for the niwa hostile-PR-review feature that needs a Claude Code agent with
no network egress. Covers what Claude Code's OS sandbox actually requires, whether it can
run in an unprivileged niwa container, and which lower-requirement containment mechanisms
work unprivileged.

Date: 2026-07-09. Claude Code version on this host: `2.1.205` (native ELF binary).

## TL;DR

- Claude Code's Linux sandbox needs **two binaries (`bubblewrap` + `socat`)** and a
  **capability-bearing unprivileged user namespace** so it can `unshare` a network
  namespace and bring up loopback. On this host `socat` is missing and
  `apparmor_restrict_unprivileged_userns=1` with no `bwrap` AppArmor profile yields a
  *capability-less* userns, which is exactly why `bwrap --unshare-net` fails with
  `RTM_NEWADDR: Operation not permitted` — reproduced below, matching the target.
- **No sandbox config knob rescues this.** `enableWeakerNestedSandbox` addresses the
  `/proc` mount, not the failing loopback/netns step, and the binary pushes `--unshare-net`
  unconditionally whenever any network restriction is active. `enableWeakerNetworkIsolation`
  is **macOS-only** (confirmed from the binary's own schema string).
- The **deny-all (empty `allowedDomains`) case does NOT avoid the failing loopback** — the
  mechanism is "remove the netns, force all traffic through host proxies," so `--unshare-net`
  is always used. There is no lighter deny-all path inside Claude's sandbox.
- Making Claude's own sandbox work requires **root at provision time** (install `socat`;
  plus either `sysctl kernel.apparmor_restrict_unprivileged_userns=0` or an AppArmor profile
  for `/usr/bin/bwrap`). niwa has that privilege when it *builds* the container, not inside
  an already-unprivileged session.
- The real requirement is narrower than "total air-gap": the review agent must still reach
  the model API but must not reach arbitrary hosts. The right place to enforce that is the
  **container/network layer at provision time** (default-deny egress firewall or a niwa-run
  allowlist proxy that is the only route), which is unprivileged *inside* the session and
  non-bypassable — the same pattern Claude's own dev-container ships. Put the boundary there
  and run Claude without relying on its bwrap netns sandbox.
- Set `sandbox.failIfUnavailable: true` (and `allowUnsandboxedCommands: false`) so the
  sandbox **hard-fails instead of silently disabling** — this directly closes the fail-open gap.

---

## 1. Claude Code sandbox mechanism + exact requirements

Source: <https://code.claude.com/docs/en/sandboxing>,
<https://code.claude.com/docs/en/sandbox-environments>,
and the standalone implementation `@anthropic-ai/sandbox-runtime`
(<https://github.com/anthropic-experimental/sandbox-runtime>). Empirically cross-checked
against the installed `2.1.205` binary.

### Binaries and OS primitives

- Linux/WSL2 uses **bubblewrap** for isolation; macOS uses Seatbelt. WSL1/native Windows
  unsupported. (docs: "Linux: uses bubblewrap for isolation … WSL2: uses bubblewrap, same as
  Linux".)
- Linux requires **two packages**: `bubblewrap` (filesystem isolation) and `socat`
  ("the relay used to route network traffic through the sandbox proxy"). Install line the
  docs give: `sudo apt-get install bubblewrap socat`.
- An **optional seccomp filter** (`@anthropic-ai/sandbox-runtime` helper) adds Unix-domain-
  socket blocking; the `/sandbox` Dependencies tab reports `ripgrep`, `bubblewrap`, `socat`,
  and the seccomp filter. `ripgrep` ships bundled with the native binary.

The installed binary confirms all of this: it references `bubblewrap` (10 hits), `socat`
(23 hits), `loopback` (29 hits), and resolves `bwrapPath` / `socatPath` at runtime.

### Kernel / capability requirements

- Needs `unshare(CLONE_NEWUSER)` — an unprivileged **user namespace**. The sandbox-runtime
  README calls out Ubuntu 24.04+ where `kernel.apparmor_restrict_unprivileged_userns` is
  enabled by default and interferes; its fix is `sysctl -w
  kernel.apparmor_restrict_unprivileged_userns=0`. The Claude Code docs give the alternative
  fix: an AppArmor profile for `/usr/bin/bwrap` that grants `userns`.
- Network isolation works by **removing the network namespace entirely**: "The network
  namespace of the sandboxed process is removed entirely, so all network traffic must go
  through the proxies running on the host (listening on Unix sockets that are bind-mounted
  into the sandbox)." Both an **HTTP proxy** and a **SOCKS5 proxy** are provided; `socat`
  bridges the bind-mounted Unix socket. To create that private netns and bring up its
  loopback interface, the process needs `CAP_NET_ADMIN` **within** the new netns — which it
  only holds if the owning user namespace is capability-bearing.
- Does **not** need host `CAP_NET_ADMIN` when the userns is capability-bearing (the caps come
  from owning the fresh namespaces). But under `apparmor_restrict_unprivileged_userns=1`
  without a `bwrap` profile, the userns is created *without* capabilities, so the netns setup
  fails. That is the crux.

Binary confirmation of the netns path (deobfuscated strings from `2.1.205`):

```
if(r){ $.push("--unshare-net");
       ... "Linux HTTP bridge socket does not exist: ... The bridge process may have died."
       ... "Linux SOCKS bridge socket does not exist: ..."
       $.push("--bind", n, n); ...            // bind-mount HTTP + SOCKS bridge sockets
       ...setenv HTTP proxy 3128 / SOCKS 1080, CLAUDE_CODE_HOST_HTTP_PROXY_PORT ...
```

`r` = "network restriction active." `--unshare-net` is pushed **unconditionally** in that
branch — including the empty-allowlist deny-all case.

### Behavior in containers / unprivileged environments (documented)

- Troubleshooting: "**Bubblewrap fails to start inside a container**: in an unprivileged
  container, bubblewrap cannot mount a fresh `/proc` filesystem. Set
  `enableWeakerNestedSandbox` to true so the inner sandbox bind-mounts the container's
  existing `/proc` instead." — i.e. this knob targets the **`/proc` mount**, not the netns.
- Security-limitations broadens the claim: `enableWeakerNestedSandbox` "enables it to work
  inside Docker environments without privileged namespaces, or on Linux hosts where
  unprivileged user namespaces are disabled by sysctl. This option considerably weakens
  security and should only be used when additional isolation is otherwise enforced."
- Known limitation (sandbox-runtime README): the Linux proxy is directed via env vars
  `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` and "may be ignored by programs that don't respect
  these variables." Inside the sandbox this doesn't matter because the netns is removed and
  the proxy socket is the only reachable path; **outside a netns it would matter a lot** (see
  §4b).

### Fail-open and the hard-fail switch

- Default is **fail-open**: "if the sandbox cannot start because dependencies are missing or
  the platform is unsupported, Claude Code shows a warning and runs commands without
  sandboxing."
- Make it a **hard failure**: `sandbox.failIfUnavailable: true` ("a missing dependency such
  as bubblewrap … blocks Claude Code from starting rather than showing a warning and falling
  back"). Pair with `allowUnsandboxedCommands: false` (strict mode; the
  `dangerouslyDisableSandbox` escape hatch is ignored). Both keys exist in the `2.1.205`
  schema (`failIfUnavailable` 13 hits, `allowUnsandboxedCommands` 8 hits).

### Config knobs (from docs + binary schema)

The `2.1.205` settings validator whitelists exactly these `sandbox.*` keys: `enabled`,
`failIfUnavailable`, `allowUnsandboxedCommands`, `network`, `filesystem`, `ignoreViolations`,
`excludedCommands`, `autoAllowBashIfSandboxed`, `enableWeakerNestedSandbox`,
`enableWeakerNetworkIsolation`, `allowAppleEvents`, `ripgrep`.

- `network.allowedDomains` — allowlist; **empty = deny-all** (docs: "no domains are pre-allowed").
- `network.deniedDomains` — block even under a broad wildcard allow.
- `network.allowManagedDomainsOnly` (managed) — non-allowed domains blocked instead of
  prompting; only managed `allowedDomains` honored. This is the "no prompt, hard deny" lockdown.
- `network.tlsTerminate` (experimental, v2.1.199+) — proxy terminates TLS (needed for credential
  masking). Default proxy does **not** inspect TLS, so domain fronting can bypass a broad allow.
- `network.httpProxyPort` / `network.socksProxyPort` — point at a custom proxy.
- `enableWeakerNetworkIsolation` — **macOS only.** The binary's own schema string:
  *"macOS only: Allow access to com.apple.trustd.agent … Needed for Go-based CLI tools (gh,
  gcloud, terraform) to verify TLS … Reduces security."* **Irrelevant on Linux.**
- `enableWeakerNestedSandbox` — `/proc` bind-mount for unprivileged containers (see above).
- `credentials.*` (files deny / envVars deny|mask), `filesystem.*` (allow/deny read/write) —
  not central to egress.

**No documented mode runs the Linux network sandbox without a capability-bearing userns +
netns.** There is no "CAP_NET_ADMIN-free" network-isolation backend.

---

## 2. How Claude uses socat, and whether deny-all avoids the failing step

Yes — `socat` is the userspace relay, not the policy engine. The **allowlist is enforced by
the host-side HTTP/SOCKS proxy**; `socat` just bridges a Unix socket (bind-mounted into the
sandbox) out to that proxy. Isolation = **netns removal + proxy**: inside the sandbox there is
no network namespace with a real route, so the only egress is the bind-mounted proxy socket,
and the proxy applies the allow/deny decision by hostname.

Because isolation depends on the netns being gone, **the deny-all case still performs the
`--unshare-net`** (confirmed in the binary — `--unshare-net` is pushed whenever network
restriction is active, before any allowlist is consulted). So the loopback bring-up that's
failing (`RTM_NEWADDR`) is on the critical path even for an empty allowlist. There is no
lighter deny-all code path to fall back to.

### Empirical reproduction on this host

```
$ bwrap --ro-bind / / --unshare-net --proc /proc echo OK
bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted     # matches the target error

$ unshare --user --map-root-user echo OK
unshare: write failed /proc/self/uid_map: Operation not permitted # capability-less userns

$ sysctl kernel.apparmor_restrict_unprivileged_userns  -> 1
$ ls /etc/apparmor.d | grep bwrap                       -> (no bwrap profile)
$ command -v socat                                       -> MISSING
$ command -v slirp4netns; command -v pasta               -> MISSING; MISSING
$ sudo -n true                                           -> "a password is required" (no passwordless sudo)
```

Root cause chain: `apparmor_restrict_unprivileged_userns=1` + no `/usr/bin/bwrap` AppArmor
profile → the unprivileged user namespace is created **without capabilities** → the process
lacks `CAP_NET_ADMIN` in the fresh netns → bringing up `lo` (`RTM_NEWADDR`) is denied. Even if
`socat` were installed, the sandbox would still fail here. (I could not directly test the fix
because there is no passwordless sudo to flip the sysctl / install the AppArmor profile.)

---

## 3. Version / config: hard-fail instead of fail-open

`claude --version` = `2.1.205`. Relevant switches:

- `sandbox.failIfUnavailable: true` — refuse to start if the sandbox can't initialize
  (missing `socat`, unsupported platform, netns failure). This is the direct answer to
  "make a fail-open never happen." Managed-settings example in the docs combines it with
  `allowUnsandboxedCommands: false`.
- `allowUnsandboxedCommands: false` — disables the `dangerouslyDisableSandbox` retry escape
  hatch ("Strict sandbox mode").
- `network.allowManagedDomainsOnly: true` (managed settings) — hard-deny non-allowlisted
  domains without prompting.
- There is **no flag that swaps the Linux backend** away from bwrap+netns. The only backend
  toggles are the two `enableWeaker*` knobs (one macOS-only, one `/proc`-only).

Caveat: `failIfUnavailable` makes Claude *refuse to run* rather than run un-isolated. That is
the correct posture for a hostile-PR gate, but it means the feature is *blocked* on the target
until the sandbox can actually initialize — it does not by itself make the sandbox work there.

---

## 4. Lower-requirement containment alternatives (unprivileged)

Goal restated: enforce no-arbitrary-egress for the dispatched review session inside an
unprivileged niwa container. Assessed against "needs root?" and "stops a determined
subprocess (alternate binaries, raw sockets)?"

### (a) `unshare --net` + slirp4netns/pasta deny-all
- **Needs a capability-bearing userns — the exact same blocker as bwrap.** On this host
  `unshare --user` already fails (`uid_map: Operation not permitted`), so `unshare --net`
  can't be created either. `slirp4netns` and `pasta` are also not installed.
- If the userns *were* capability-bearing, this is the **strongest** option: a real empty
  netns is kernel-enforced no-egress that alternate binaries and raw sockets cannot escape;
  slirp4netns/pasta then re-add only the egress you choose. But it shares Claude's blocker
  exactly, so it is **not a lower-requirement path** — same root cause.

### (b) HTTP(S) proxy env-var, deny-all default, no other route
- **Fully unprivileged** — binding a localhost proxy needs no privilege (verified: bound an
  ephemeral port as uid 1000).
- **Does NOT contain a determined subprocess.** Empirically, a direct outbound TCP
  `connect()` to `1.1.1.1:443` **succeeded**, ignoring `HTTP_PROXY` entirely. Raw sockets are
  denied (no `CAP_NET_RAW`), but that's irrelevant — ordinary `connect()` already bypasses an
  env-var proxy. This matches the sandbox-runtime README's own caveat that the proxy env vars
  "may be ignored by programs that don't respect these variables." Claude's sandbox only gets
  away with env-var steering because it *also* removes the netns; without the netns the proxy
  is advisory.
- **Verdict: best-effort only, not a security boundary for hostile code.** Unsuitable as the
  sole control for a hostile PR.

### (c) seccomp-bpf blocking `socket()`/`connect()` via a wrapper
- **Can be unprivileged.** A `prctl(PR_SET_NO_NEW_PRIVS)` + `SECCOMP_MODE_FILTER` install
  needs no root/`CAP_SYS_ADMIN`, is inherited across `fork`/`exec`, and can't be dropped by
  the child. A filter that denies `socket(AF_INET/AF_INET6, …)` and `connect()` to inet
  addresses enforces no-egress on the whole review subtree, covering alternate binaries and
  raw sockets (the `socket()` call itself is refused).
- **Real boundary, viable unprivileged.** This is the most promising "reduce requirements"
  candidate. Caveats: you must (i) allow `AF_UNIX` for legitimate local IPC, (ii) if the agent
  itself still needs the model API, carve out exactly the API path — e.g. allow `connect()`
  only to a bound proxy Unix socket / a pinned address — which is more engineering than a
  netns; (iii) argument filtering on `connect()` sockaddr is limited in classic seccomp
  (can't deref the pointer), so the clean design is "deny `socket(AF_INET*)` outright for the
  untrusted Bash subtree, and run the trusted API-talking harness outside that filter."
  `@anthropic-ai/sandbox-runtime` already ships a seccomp helper (`apply-seccomp` creating a
  nested user+PID+mount ns) for Unix-socket blocking — the same technique, so it's proven.
- **Verdict: viable unprivileged; the strongest in-session option when the container layer
  can't help.** Requires implementation work in niwa.

### (d) network cgroup / eBPF
- cgroup v2 has **no `net_cls`/`net_prio`** (those were v1). Per-cgroup egress control is done
  with eBPF `cgroup/skb` or `cgroup/connect` programs, which need `CAP_BPF`/`CAP_NET_ADMIN` —
  **root/privileged.** The delegated user cgroup here exposes only `cpu memory pids`.
- **Verdict: not viable unprivileged.**

---

## 5. Does the feature need full OS-level no-egress? (narrower boundary)

Re-examined: **no, "empty allowlist / total air-gap" is stronger than the actual security
goal.** The review agent must still reach the **model API** to function; what it must NOT do
is let hostile PR code reach **arbitrary** hosts (exfiltrate secrets, phone home). So the real
requirement is **allowlist egress = { model API } only**, not zero egress. That is exactly
`network.allowedDomains: ["api.anthropic.com"]` (or the provider endpoint), not an empty list.

Two consequences:

1. The threat surface that needs containment is the **untrusted code's Bash subprocesses**,
   not the trusted harness that talks to the API. That is precisely the sandboxed-Bash tool's
   scope — but its enforcement still depends on the netns, so it doesn't dodge the blocker.
2. The clean architecture is to move the boundary **outward to the container/network layer**,
   which niwa provisions with privilege, and which is non-bypassable and unprivileged from
   inside the session. This is the documented, blessed pattern: Claude Code's dev container
   ships a "default-deny iptables firewall" and the docs say that firewall is what makes
   `--dangerously-skip-permissions` safe for unattended work; Claude Code on the web uses "a
   network proxy [that] enforces a default allowlist" plus a token-holding proxy outside the
   sandbox. Sources: <https://code.claude.com/docs/en/sandbox-environments>,
   <https://code.claude.com/docs/en/devcontainer>.

---

## Ranked recommendations for niwa

Framed as "improve install" vs "reduce feature requirements."

1. **[Best — reduce requirements + move the boundary] Enforce egress policy at the container
   layer at provision time.** When niwa creates the review container it has enough privilege
   to install a default-deny egress firewall (iptables/nft in the container's own netns) or
   to make a niwa-run allowlist proxy the *only* route (black-hole the default route; allow
   only the model API). Inside the session everything is unprivileged and the boundary is
   non-bypassable by alternate binaries or raw sockets. Then run Claude **without** depending
   on its bwrap netns sandbox. This is the dev-container model and needs no unprivileged-userns
   fix at all. Set `allowedDomains` to just the API for defense-in-depth.

2. **[Improve install] Make Claude's own bwrap sandbox work.** Requires root **at provision**:
   (i) install `socat` (currently missing — a hard blocker even before the userns issue);
   (ii) grant a capability-bearing userns via **either** an AppArmor profile for
   `/usr/bin/bwrap` (per the docs' snippet) **or** `sysctl
   kernel.apparmor_restrict_unprivileged_userns=0` (host-wide, weakens the host). Then set
   `sandbox.failIfUnavailable: true` + `allowUnsandboxedCommands: false` so it never fails
   open. Downside: needs host policy changes niwa may not own in every deployment, and
   `enableWeakerNestedSandbox` most likely does **not** cover the `RTM_NEWADDR` loopback step
   (it targets `/proc`; `--unshare-net` is still pushed) — so test it empirically first as the
   cheapest probe, but don't count on it.

3. **[Reduce requirements — unprivileged in-session hard boundary] seccomp `no_new_privs`
   filter** denying `socket(AF_INET/AF_INET6)`/`connect()` around the untrusted review
   subtree, with the trusted API-talking harness kept outside the filter. Real, unprivileged,
   inheritance-safe boundary; requires implementation effort and careful carve-out of the API
   path. Good fallback where niwa can't touch the container network layer.

4. **[Weakest — do not use alone for hostile code] env-var HTTP proxy.** Advisory only;
   empirically bypassed by a direct `connect()`. Acceptable only as convenience/telemetry on
   top of a real boundary, never as the security control.

### Cross-cutting

- Regardless of mechanism, set `sandbox.failIfUnavailable: true` so a missing dependency or a
  netns failure **refuses to run** instead of silently disabling protection.
- The security goal is **allowlist egress (model API only)**, not a literal air-gap — spec the
  feature that way; it's both achievable and matches Claude Code's own supported patterns.

## Sources

- Claude Code — Configure the sandboxed Bash tool: <https://code.claude.com/docs/en/sandboxing>
- Claude Code — Choose a sandbox environment: <https://code.claude.com/docs/en/sandbox-environments>
- Claude Code — Settings (sandbox settings): <https://code.claude.com/docs/en/settings#sandbox-settings>
- Claude Code — Dev container: <https://code.claude.com/docs/en/devcontainer>
- `@anthropic-ai/sandbox-runtime` README: <https://github.com/anthropic-experimental/sandbox-runtime>
- Empirical probes and binary-string inspection of Claude Code `2.1.205` on the investigation host (this document, §2–4).
