# Spike: what in codex-cli 0.147.0 needs unprivileged user namespaces, and is niwa's gate broader than what it protects?

Measured 2026-08-21 on Ubuntu 24.04 (noble), kernel 6.8.0-137-generic, not a container
(`/proc/1/cgroup` = `0::/init.scope`, no `/.dockerenv`).

## Bottom line

The bwrap-based sandbox is the only thing that needs userns, and codex 0.147 uses it for
**every** sandboxed mode. niwa's `unshare --user --map-root-user` gate is **not broader** than
the thing it protects -- it probes the exact syscall that fails. If anything it is a hair
*narrow* (under-protective). The one mode that writes here without userns is
`danger-full-access`, which is not the posture the gated scenario tests.

There is a strictly better probe, and it is `codex sandbox -- /bin/true`.

## Environment baseline (the root cause)

```
$ cat /proc/sys/kernel/unprivileged_userns_clone        -> 1
$ cat /proc/sys/user/max_user_namespaces                -> 127539
$ cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns -> 1     <-- this one
```

Ubuntu 24.04's AppArmor userns restriction. The effect is subtle and matters for probe design:

```
$ unshare --user true                    -> exit 0     (userns CAN be created)
$ unshare --user --map-root-user true    -> exit 1     write failed /proc/self/uid_map: Operation not permitted
```

So userns *creation* succeeds; the namespace is created without capabilities, so writing
`uid_map` is denied. A probe that only checks "can I make a userns" would pass here and be
wrong. niwa's probe includes `--map-root-user`, so it checks the right half.

## What codex 0.147.0 actually ships

`codex doctor --json`, check `sandbox.helpers`:

```
"codex-linux-sandbox helper": ".../home/tmp/arg0/codex-arg0kyRTrS/codex-linux-sandbox"
"execve wrapper helper":      ".../home/tmp/arg0/codex-arg0kyRTrS/codex-execve-wrapper"
"filesystem sandbox": "restricted"
"network sandbox":    "restricted"
"approval policy":    "OnRequest"
```

Both helpers are extracted to a per-run temp dir and deleted on exit.

The binary bundles its own bubblewrap:

```
/home/dgazineu/.tsuku/tools/codex-0.147.0/codex-resources/bwrap   (529776 bytes)
$ .../codex-resources/bwrap --version  ->  "bubblewrap built for Codex"
```

Strings in the binary show both backends exist but only one is current:

- `sandboxing/src/landlock.rs`, `LandlockRestrictUsageNotIncluded`, seccompiler error strings
  -- the Landlock+seccomp implementation is still compiled in.
- Its error string is `"error applying **legacy** Linux sandbox restrictions: "`. Alongside it:
  `"error applying Linux sandbox restrictions: "` (non-legacy) plus ~40 bubblewrap-specific
  strings (`failed to exec bundled bubblewrap`, `failed to exec system bubblewrap`,
  `bundled bubblewrap digest mismatch`, `CODEX_BWRAP_SHA256`, synthetic-mount registry, etc.).
- No feature flag selects a backend. `codex features list` (89 flags) has nothing sandbox-related
  except the removed `experimental_windows_sandbox` / `elevated_windows_sandbox`.
- No `CODEX_*` env var selects a backend either (full list extracted; only
  `CODEX_SANDBOX`, `CODEX_SANDBOX_NETWORK_DISABLED` -- both are markers codex *sets for the child*,
  not switches -- and `CODEX_BWRAP_SHA256`, a digest pin).

**So: Landlock is dead code on this path in 0.147. There is no non-userns confinement route.**

Codex says so itself. Every sandboxed run prints:

```
warning: Codex's Linux sandbox uses bubblewrap and needs access to create user namespaces.
```

## bwrap control (no codex involved)

```
$ bwrap --ro-bind / / --dev /dev /bin/true
bwrap: setting up uid map: Permission denied                      exit 1
$ bwrap --ro-bind / / --dev /dev --unshare-net /bin/true
bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted      exit 1
$ bwrap --unshare-user --ro-bind / / --dev /dev /bin/true
bwrap: setting up uid map: Permission denied                      exit 1
```

Identical for the bundled `codex-resources/bwrap`. bwrap is **not** setuid here
(`-rwxr-xr-x`), neither the tsuku copy nor `/usr/bin/bwrap`.

The two different error strings are the same root cause at different points in bwrap's
sequence. strace settles the ordering:

```
# with --unshare-net
clone(flags=CLONE_NEWNS|CLONE_NEWUSER|CLONE_NEWNET|SIGCHLD) = ok
  ... dies in loopback setup; uid_map is never opened

# without --unshare-net
clone(flags=CLONE_NEWNS|CLONE_NEWUSER|SIGCHLD) = ok
openat(4, "uid_map", O_RDWR|O_LARGEFILE|O_CLOEXEC) = -1 EACCES (Permission denied)
```

Loopback configuration happens *before* the uid_map write. Both need capabilities in the new
userns, which AppArmor withholds. They co-vary: mapping root into the userns would grant
CAP_NET_ADMIN and the loopback step would succeed too.

## The matrix

All runs used an isolated `CODEX_HOME` with `~/.codex/auth.json` symlinked in, `env -u
OPENAI_API_KEY`, a freshly `git init`ed scratch repo as cwd, `--ephemeral`, `--json`,
`< /dev/null`. Auth was healthy throughout (`codex doctor`: auth ok, websocket HTTP 101,
reachability ok) -- no result below is an auth artifact. Every run returned real model output,
so no result below is a model-call failure either.

| `--sandbox` mode | Session starts? | Sandbox built? | Command ran? | File created? | Exact failure |
|---|---|---|---|---|---|
| `read-only` | yes | **no** | no | n/a | `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted` |
| `workspace-write` (default net = restricted) | yes | **no** | no | **NO** | `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted` |
| `workspace-write` + `-c sandbox_workspace_write.network_access=true` | yes | **no** | no | **NO** | `bwrap: setting up uid map: Permission denied` |
| `danger-full-access` | yes | n/a (no sandbox) | **yes** | **YES** | none |
| default `codex exec`, untrusted project | yes | falls back to `read-only` | no | **NO** | `patch rejected: writing is blocked by read-only sandbox; rejected by user approval settings` |
| default `codex exec`, **trusted** project (what niwa delivers) | yes | **no** | no | **NO** | `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted` |
| `codex sandbox -- /bin/true` (subcommand) | n/a | **no** | no | n/a | `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted` |

### Q3: does `codex sandbox` differ from the sandbox `codex exec` builds?

**No -- they are the same implementation.** The premise that they differ was an artifact of the
earlier probe. `codex exec --sandbox read-only "<some prompt>"` "worked" only because the model
answered *without calling its shell tool*; the sandbox is constructed lazily, on first command.

Proof -- forcing a shell call under `read-only`:

```
prompt: "You MUST call your shell tool. Run exactly this command: ls -a . ... report the exact error text."
-> agent_message: "bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted"
```

Same string `codex sandbox --` produces. Note also the failing runs emit **no**
`command_execution` item in `--json` output at all -- the failure precedes item creation, so
"exit code 0 and plausible-looking output" is not evidence the sandbox worked.

The `--sandbox-state-disable-network` flag on `codex sandbox` is not independently usable; it
requires `--sandbox-state-json`.

### Q5: `workspace-write` specifically

Fails, in both network postures, for the userns reason. With network unrestricted the failure
is verbatim `bwrap: setting up uid map: Permission denied` -- **the exact operation niwa's probe
performs.**

### Loud finding

`danger-full-access` both starts **and writes**:

```
{"type":"item.completed","item":{"type":"command_execution",
 "command":"/bin/bash -lc 'printf OK > PROOF.txt'","exit_code":0,"status":"completed"}}
PROOF.txt present: YES (contents: OK)
```

That is the only writing mode here, and it writes precisely because it constructs no sandbox.
It is not a way to run the gated scenario -- the scenario's subject is that niwa's *delivered
default posture* lets a session write on its first attempt. Running it under
`danger-full-access` would assert nothing about niwa's preparation.

## Verdict on niwa's gate

`theCodexSandboxCanRunHere`
(`public/niwa/.claude/worktrees/root-orientation/test/functional/codex_agent_steps_test.go:1743`)
gates one scenario, `a live Codex session writes a file on its first attempt`
(`test/functional/features/codex-agent.feature:936`).

I replicated that scenario's exact command by hand -- default `codex exec` (no `--sandbox`),
prompt `Create a file named codex-wrote-this.txt ... containing the single word ready.`, in a
project marked `trust_level = "trusted"` (which is what niwa writes; the scenario's workspace
declares no `[session]` posture, so no `sandbox_mode` is delivered and trust alone decides).
Codex resolved `approval: never`, `sandbox: workspace-write [workdir, /tmp, $TMPDIR]`,
attempted `apply patch` twice, and failed both times:

```
Failed to write file .../codex-wrote-this.txt
"I couldn't create the file because the workspace sandbox failed with a system-level
 permission error (bwrap: loopback: Operation not permitted). No file was written."
FILE: NO
```

**The gate is EQUAL to the thing it protects, not broader.** Codex's bwrap needs a userns with a
uid map; the probe performs exactly that operation and fails with the same kernel refusal. The
doc comment's reasoning ("the probe is the mapping itself rather than a check for any particular
sandbox binary, so it stays true as Codex's implementation moves") is factually correct for
0.147, and the scenario genuinely cannot pass on this machine.

Two qualifications, both minor and both in the *narrow* (under-protective) direction:

1. bwrap needs more than uid_map: `CLONE_NEWNS` plus a long list of mount operations, and
   CAP_NET_ADMIN for loopback when the network is restricted. A hardened container could permit
   the uid mapping and still block the mounts. The probe would pass; the scenario would fail
   with a real (misleading) test failure.
2. The probe depends on `unshare(1)` from util-linux being installed (the code does handle its
   absence with `ErrPending`) and asserts nothing about codex's own bundled bwrap -- e.g. a
   `CODEX_BWRAP_SHA256` digest mismatch or a failed helper extraction would slip through.

### The better probe

Replace the `unshare` invocation with codex's own:

```go
exec.CommandContext(probeCtx, "codex", "sandbox", "--", "/bin/true").Run()
```

Measured properties:

- **42 ms**, versus 4 ms for `unshare`. Both are noise against a live session.
- **Needs no auth.** Verified with a completely empty `CODEX_HOME`: still reaches bwrap and
  fails with the bwrap error, not an auth error.
- **Needs no network and makes no model call.**
- **Tests the actual implementation**, so it covers the mount-namespace and helper-extraction
  gaps above, and it survives a backend change in either direction -- if codex ever restores the
  Landlock path for some modes, this probe goes green exactly when the scenario becomes runnable,
  whereas the `unshare` probe would keep the scenario pending forever on Ubuntu 24.04.
- Trade-off: it presumes the `sandbox` subcommand keeps its name and its "run this command
  confined" contract. `codex exec` cannot substitute (needs auth + a model call).

Do **not** use `codex doctor` for this. Its `sandbox.helpers` check reports
`[ok] sandbox configuration is readable` on this machine, where the sandbox is completely
unusable. It reads config, it does not exercise the sandbox. Overall doctor verdict here was
`17 ok | 1 idle | 1 notes | 0 warn | 0 fail`.

## Commands run (reproduction)

Scripts under `/home/dgazineu/.claude/jobs/e40f3334/tmp/nsprobe/`:
`sbx.sh` (bwrap + `codex sandbox` controls), `exec2.sh <mode> <tag> <prompt>` (per-mode exec
matrix), `net.sh` (workspace-write with network_access), `default.sh` / `trusted.sh` (the
literal gated scenario, untrusted and trusted), `strace.sh` (bwrap failure ordering),
`keys.sh` / `env.sh` (binary string extraction), `probe.sh` (auth-free probe timing),
`doctor.sh` / `helpers.sh` (doctor human + JSON).

Common harness for every codex run:

```bash
env -u OPENAI_API_KEY CODEX_HOME=$B/home \
  codex exec --sandbox <mode> -C $D --json --ephemeral "$PROMPT" < /dev/null
# $B/home/auth.json is a SYMLINK to ~/.codex/auth.json (never copied, never printed)
# $D is a fresh `git init`ed directory with one seed commit
```
