# Lead: What single-user assumptions does niwa already encode, and what would break in a multi-user shared-machine scenario?

## Findings

### 1. The trust model is explicit and codified: same-UID cooperative trust

niwa's design documentation states the assumption directly. The single
canonical statement lives in
`docs/designs/current/DESIGN-cross-session-communication.md:1154`:

> "Niwa relies on standard Unix filesystem permissions (0600 files, 0700
> directories under `.niwa/`, independent of umask) to prevent cross-UID
> access. Processes running under the same UID as the user are trusted to
> cooperate; the mesh is not hardened against a malicious same-UID attacker.
> [...] This is the PRD's stated 'role integrity is the only trust boundary'
> ceiling."

This is the boundary. Every primitive below either enforces it
(filesystem permissions excluding other UIDs) or assumes it (no auth
beyond what file mode + advisory flock + PID/start-time matching gives
you). It is explicitly NOT defended against same-UID attackers — that is
already a documented Known Limitation, not a future hardening target.

A complementary statement at
`docs/designs/current/DESIGN-mcp-call-telemetry.md:494` makes the
positioning unambiguous: "Multi-tenant concerns are out of scope
(single-user local CLI)."

### 2. Daemon process model: per-instance, NOT per-user

The daemon binding is purely path-based, not identity-based. A CLI
invocation talks to "the daemon for this instance directory" full stop.
The mechanism is `internal/cli/mesh_watch.go:2363-2376`:

```go
func acquireDaemonPIDLock(path string) (*os.File, error) {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
    ...
    if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
```

The daemon takes an exclusive `flock` on `<instance>/.niwa/daemon.pid.lock`
and writes its PID + start_time to `<instance>/.niwa/daemon.pid`
(`internal/cli/mesh_watch.go:2381-2394`). `EnsureDaemonRunning` in
`internal/workspace/daemon.go:35-102` reads the PID file lock-free and
calls `mcp.IsPIDAlive(pid, startTime)` against `/proc` to decide whether
to spawn.

There is **no socket, no port, no network listener** for the daemon.
`grep` confirms no `net.Listen`, no Unix socket, no HTTP server in
`internal/`. The MCP server itself is stdio-only — see
`internal/mcp/server.go:1` ("Package mcp implements a stdio MCP server")
and `internal/cli/mcp_serve.go:33` (`srv.Run(os.Stdin, os.Stdout)`).
Claude Code spawns `niwa mcp-serve` as a subprocess and pipes JSON over
stdin/stdout — there is no daemon for clients to connect to.

What the **daemon does** is watch inbox files via fsnotify and
spawn worker processes (`claude -p`). It is not the addressable surface
that an `attach` command would talk to.

**Crucial implication for two-user scenarios:** if user A and user B both
run a `niwa` command against the same instance directory:

- **PID lock semantics work cross-user** — `flock(LOCK_EX)` is enforced
  by the kernel regardless of UID, so two daemons cannot coexist for the
  same instance. Whoever spawned the daemon first wins; subsequent
  attempts get `errDaemonAlreadyRunning` and exit cleanly.
- **The losing user's CLI sees a daemon owned by the winning user.**
  `IsPIDAlive` (`internal/mcp/liveness.go:14`) calls
  `proc.Signal(syscall.Signal(0))` to a PID owned by another user. On
  Linux this returns `EPERM` (permission denied), not `ESRCH`. The code
  treats `err != nil` as "not alive," so user B will conclude the daemon
  is dead and try to spawn its own, fail to acquire the flock with
  `EWOULDBLOCK`, and exit with errDaemonAlreadyRunning. The instance is
  effectively single-user-owned by whoever spawned the daemon first; user
  B's commands silently produce no daemon-mediated effects.

### 3. Worktree filesystem ownership: inherited from `os.MkdirAll`, never `chown`d

niwa **never calls `os.Chown`** in production code. `grep -rn "os\.Chown"
internal/` returns zero hits. Files and directories are created with
`os.MkdirAll(..., 0o700)` and `os.WriteFile(..., 0o600)`, which means
ownership equals the UID of whoever ran the command.

For session worktrees, `internal/mcp/handlers_session.go:80-108`
(`scaffoldWorktreeNiwa`) creates the `.niwa/` subdirectory inside a
worktree with mode 0700 and individual files (daemon.pid, daemon.log) at
mode 0600. The git worktree itself is created via `exec.Command("git",
"-C", repoPath, "worktree", "add", ...)`
(`internal/mcp/handlers_session.go:188`), which respects the calling
process's umask.

**Two-user implication:** a worktree created by user A's daemon
(`niwa_create_session` runs in the daemon's MCP server, owned by the
daemon's UID) is owned by user A. User B cannot read the per-task
`state.json`, the inbox, or `daemon.log` because they are mode 0600 owned
by user A. User B's `claude -p`-spawned MCP server fails ENOENT/EACCES on
every read. The mesh is silently broken for user B.

### 4. State file access: relies entirely on filesystem permissions

There is no application-layer access check that distinguishes one local
user from another. The on-disk discipline is uniform mode 0600 for files
and 0700 for directories under `.niwa/`, applied via explicit `os.Chmod`
after open/mkdir to override umask
(`internal/workspace/channels.go:929,946,971`,
`docs/designs/current/DESIGN-cross-session-communication.md:1186-1191`).

Test coverage at `internal/workspace/channels_test.go:421-438` walks
`.niwa/` after Apply and asserts every directory is 0700 and every file
is 0600. The provider-auth file goes further:
`internal/workspace/providerauth.go:54` **refuses to read** the file if
permissions are not exactly 0600 — but this check only protects against
sloppy user setup, not against another local user with read access (mode
0600 owned by the right user already excludes others).

`O_NOFOLLOW` is applied on every `state.json`, `envelope.json`, `.lock`,
and `transitions.log` open (`internal/mcp/taskstore.go:112,356,407,427`,
`internal/mcp/audit.go:96`, `internal/mcp/audit_reader.go:29`). The
documented reason is "to defeat same-UID symlink tampering"
(`DESIGN-cross-session-communication.md:1190`). This is hardening WITHIN
the same-UID trust ceiling — symlink tampering is the threat that
survives even with 0600 mode, because the file owner can replace their
own files. It is not designed for cross-UID protection (which 0600 mode
already provides).

### 5. Locks are filesystem-scoped, not user-scoped

Every lock in the codebase is a `syscall.Flock` against a file:

- `<task>/.lock` for per-task state mutations
  (`internal/mcp/taskstore.go:135-152`)
- `<instance>/.niwa/daemon.pid.lock` for daemon singleton enforcement
  (`internal/cli/mesh_watch.go:2363-2376`)

flock is advisory and process-scoped, not user-scoped. The kernel
enforces it across UIDs (so `daemon.pid.lock` correctly prevents two
daemons even from different users) but the niwa code never inspects
lockholder identity. Any process with read+write access to the lock file
can acquire the lock.

Mode 0600 on the lock file effectively makes the lock single-user. In
practice this means user B cannot acquire the daemon.pid.lock if it
exists with mode 0600 owned by user A — `os.OpenFile(path,
O_CREATE|O_RDWR, 0o600)` fails with EACCES. So the layered effect is
correct (other users cannot fight for the lock) but the protection is
purely filesystem-permission-derived; the lock itself has no notion of
"this is my user's lock."

### 6. No UID checks anywhere

`grep -rn "Getuid|Geteuid|user\.Current"` against `internal/` returns
**one** non-test hit: `internal/workspace/env_example_test.go:285`, which
is a test using `os.Getuid() == 0` to skip a chmod test when running as
root. There is no production code that branches on UID, calls
`user.Current()`, or asserts file ownership.

### 7. Workers spawned by the daemon inherit the daemon's UID

The daemon spawns `claude -p` workers via `exec.Command` with no UID
manipulation (`internal/cli/mesh_watch.go:987,997`,
`internal/workspace/daemon.go:70-77`). Worker MCP servers run with the
daemon's UID. The PPID + start-time auth check
(`internal/mcp/auth.go:172-221`) verifies that the MCP server's parent
PID matches the recorded `state.json.worker.{pid, start_time}` — this
defeats role spoofing by a different process under the same UID, but
explicitly NOT by a different UID's processes (which would already be
locked out by file mode 0600).

### 8. NIWA_SESSION_ROLE is the trust seam

`docs/designs/current/DESIGN-cross-session-communication.md:1125,1174`:

> "An agent that overrides `NIWA_SESSION_ROLE` can act on tasks
> belonging to the spoofed role."

The session role is environment-variable-derived. The PPID + start-time
hardening on Linux defeats *worker* spoofing (because the daemon
recorded the legitimate worker's PID/start_time), but coordinator
identity is **purely env-var trust**. A second process under the same
UID can claim to be the coordinator and dispatch envelopes attributed to
that role. This is the documented v1 trust ceiling. In a two-user
scenario, this manifests as: nothing is enforced application-level
either way; the only thing keeping user B from impersonating user A's
coordinator is filesystem permissions on the inbox directories.

### 9. Network filesystem usage is documented as unsupported

`docs/designs/current/DESIGN-cross-session-communication.md:1133`:

> "**Instance root must be on a local POSIX filesystem.** The
> atomic-rename + flock + parent-directory-fsync pattern assumes local
> filesystem semantics (ext4/xfs/btrfs/apfs/tmpfs all qualify; ext4's
> data=ordered default is the reference model). NFS, SMB, sshfs, and
> other network filesystems have varying atomic-rename and advisory-flock
> semantics and are unsupported in v1."

The two-user-on-network-share scenario is therefore already explicitly
out of scope at the platform level — niwa does not claim correctness on
NFS/sshfs at all. The PRD does not need to invent a new restriction; it
can cite this existing one.

### 10. Claude Code transcripts are inherently per-user

Discovery probes `~/.claude/projects/<base64url-cwd>/*.jsonl` and
`~/.claude/sessions/<ppid>.json` (`internal/mcp/session_discovery.go:23,
24, 66, 109`). `homeDir` is sourced from `os.UserHomeDir()` /
`os.Getenv("HOME")` (`internal/cli/session_register.go:50`,
`internal/cli/mesh_watch.go:1727`). The transcript that an `attach`
command would resume via `claude --resume` lives under the **HOME of the
user that spawned the worker**, not in the workspace instance directory.

This is the most consequential single-user assumption for the attach
feature specifically. If user A's daemon spawned the worker, user A's
`~/.claude/projects/` holds the JSONL transcript. User B running `niwa
session attach <id>` against the same instance directory has no path to
that transcript — `~/.claude/` resolves to user B's home. The transcript
is structurally invisible to user B.

### 11. The attach command itself: what would protect or not protect against user B

If `niwa session attach` is implemented along the lines the issue
sketches — write a "this session is locked for human attach" marker into
`<instance>/.niwa/sessions/<id>.json`, refuse mesh dispatch while the
marker is set, spawn a `claude --resume` for the user — then the lock is
**filesystem-scoped**:

- **What protects against a competing user A vs user B attach:** mode
  0600 on the session state file (already enforced at
  `internal/mcp/session_lifecycle.go:62`, written via tmp+rename with
  0o600). User B cannot read or modify a session lifecycle file owned by
  user A's daemon. They cannot acquire the lock because they cannot
  write to the file at all.

- **What does NOT exist as a check today:** any application-layer notion
  of "the user who acquired the lock." If user A and user B are both
  members of the daemon-owning UID (i.e. they're the same OS user across
  two terminals), there is no way for niwa to distinguish them. Both can
  release the lock, both can read the transcript, both can attach.

- **The transcript barrier is the de-facto enforcement:** even if user B
  could write to user A's session state file (e.g. via misconfigured
  group-write permissions), user B's `claude --resume` would not find the
  transcript because it lives under user A's HOME. The attach would
  spawn a Claude with no history — useless rather than dangerous.

## Implications

### What the PRD must say

1. **Adopt the existing trust ceiling verbatim.** The PRD does not need
   to invent a new boundary; it can cite the existing
   `DESIGN-cross-session-communication.md:1154` "same-UID cooperative
   trust" model and declare attach lives within it. Specifically: a
   workspace instance directory is presumed to be owned by exactly one
   OS user, and `niwa session attach` inherits the integrity guarantees
   that the surrounding mesh already has — not stronger, not weaker.

2. **State the explicit non-goal.** Multi-user shared-machine attach is
   out of scope. The PRD should say "two OS users sharing a workspace
   instance directory is unsupported" and decline to specify behavior in
   that case. This matches the project's existing posture.

3. **No new safeguard is needed in the attach command itself.** The
   transcript-locality property (Claude transcripts live in
   `~/.claude/projects/` of the spawning user) does the enforcement for
   us: even if a second OS user could somehow read the session state
   file, they cannot resume the conversation. Adding an explicit UID
   check would be net-new code that contradicts the codebase's
   established posture (zero `Getuid` calls in production paths).

4. **Network filesystem prohibition transfers automatically.** Since
   instance roots already cannot live on NFS/SMB/sshfs per
   `DESIGN-cross-session-communication.md:1133`, the multi-machine attach
   case is already closed. The PRD can reference this restriction in
   passing rather than re-deriving it.

5. **One thing the PRD might want to call out as a behavioral note (not
   a safeguard):** if the OS user runs `niwa session attach <id>` from a
   `niwa` invocation that has a different `HOME` than the daemon
   inherited (e.g. `sudo -E` changes vs preserves HOME), `claude
   --resume` may not find the transcript. This is "operate in the same
   shell environment as the daemon" boilerplate, but worth a sentence
   because the failure mode is silent (resume succeeds, transcript empty).

### Concrete failure modes if two OS users try to share a workspace instance

For the PRD to draw the boundary precisely, here are the specific
failure modes — none of which require new mitigations because they are
the natural consequences of the documented trust model:

| Scenario | Failure mode | Where it surfaces |
|----------|--------------|-------------------|
| User B runs `niwa apply` on user A's instance | EACCES on `daemon.pid.lock` open with mode 0600; daemon spawn fails | `internal/cli/mesh_watch.go:2364-2367` |
| User B runs `niwa mesh watch` while A's daemon owns it | Daemon spawns OK (different process), tries flock, gets EWOULDBLOCK, exits cleanly | `internal/cli/mesh_watch.go:2368-2374` |
| User B's CLI tries to read `state.json` | EACCES; `ReadState` returns error; auth code maps to NOT_TASK_PARTY | `internal/mcp/taskstore.go`, `auth.go:103-110` |
| User B's `niwa mcp-serve` spawned by their own Claude Code | MCP server starts (no auth at startup), fails on first task-touching tool call | stdio MCP server has no entry-time UID check |
| User B's worker would try to write to `transitions.log` | EACCES on append-open; flock'd write fails closed | `internal/mcp/taskstore.go:407` |
| User B runs `niwa session attach <id>` (post-implementation) | EACCES on session state file read OR successful state read but no transcript visible under their `~/.claude/` | `internal/mcp/session_lifecycle.go:78-87`, `session_discovery.go:108-109` |

In every case, the failure is fail-closed: a permission error on a file,
not a successful-but-incorrect operation. This is the "filesystem
permissions ARE the protection" property of the existing design.

## Surprises

1. **No socket, no port, no IPC.** The daemon does not expose any
   addressable interface. Everything goes through the filesystem (inbox
   files watched via fsnotify, state files, lock files). This was
   stronger evidence than expected for the "single-user local CLI"
   posture — there's literally no network surface that a multi-user
   design would have to negotiate.

2. **No `os.Chown` anywhere.** Zero hits in production code. niwa never
   reaches for `chown(2)` to manage ownership; it relies entirely on the
   process UID at the time of file creation. This is a clean design but
   means cross-user scenarios are completely outside the design space —
   not even on a "we tried it once" level.

3. **The `--mcp-config` design quietly enforces single-user too.** The
   daemon spawns workers with `--mcp-config=<workerMCPPath>`
   (`internal/cli/mesh_watch.go:987,997`) pointing at a path under the
   daemon's instance root. A worker spawned by user A's daemon can only
   read/write files at user A's UID. There is no path to spawning a
   worker that runs as user B against user A's instance.

4. **The `same-UID attacker` framing is everywhere.** The codebase
   consistently uses "same-UID" as the threat model unit, not "the OS
   user." This means the design has been thought about with adversarial
   processes under the same UID in mind (and accepted that as the trust
   ceiling), but cross-UID adversaries are not part of the threat model
   at all because filesystem permissions handle them by construction.

5. **The MCP server has no startup auth check.** `internal/cli/mcp_serve.go`
   starts a server based on `NIWA_INSTANCE_ROOT` and `NIWA_SESSION_ROLE`
   env vars with no validation that the caller has any standing to access
   that instance. The implicit check is "if you can read the instance's
   state files, you're authorized" — i.e., file permissions ARE the auth.
   This is the most thorough proof point for the "same-UID = trusted"
   posture: there's no defensive code at the MCP layer at all.

## Open Questions

1. **Should `niwa session attach` print a one-line note when the
   workspace instance is owned by a different UID than the invoking
   user?** Today, the failure modes above are all fail-closed but the
   error messages are filesystem-level (EACCES) without context. A
   one-line "this workspace instance appears to be owned by a different
   user; niwa is single-user per workspace" hint at the very top of the
   attach command would not change behavior but would convert a confusing
   permission denied into a clear "this isn't supported." This is purely
   a UX call, not a safety call. The PRD should decide explicitly.

2. **Does the PRD want to make the network-filesystem statement
   user-facing?** The existing prohibition lives in the design doc, not
   in user-facing docs. If the attach command lands without a mention,
   users on shared workstations who set up `.niwa` on an NFS mount may
   find the failure surprising. This is an existing gap, not new — but
   the attach PRD is a natural place to either fix it or explicitly punt.

3. **Out of scope but worth flagging once:** if niwa ever supports
   workspace instances on shared filesystems (say, a shared `/team/repos/`
   layout), every assumption above flips. The trust model would need
   explicit reconsideration, not an extension. This isn't an attach-PRD
   concern — it's a "if anyone is thinking about this, here's the list of
   stones to overturn" note. The biggest surprises would be (a) per-task
   flock semantics on the underlying FS, (b) ownership of spawned
   workers, (c) Claude transcript locality. Worth a sentence in the PRD
   under "Future considerations" so this research isn't lost.

## Summary

niwa already encodes a precise single-user-per-instance trust model with
the boundary stated verbatim in `DESIGN-cross-session-communication.md`
("same-UID cooperative trust") and enforced structurally by mode 0600 on
every file under `.niwa/`, no `chown` calls anywhere, no network
sockets, and Claude transcripts living under the spawning user's HOME
that the attach command will rely on for `claude --resume`. The PRD
should adopt this existing ceiling verbatim — declare attach single-user,
cite the existing design doc, and note that no new safeguard is needed
because the transcript-locality property and filesystem permissions
already make cross-user attach fail-closed. The biggest open question is
purely UX: whether to add a one-line "this instance is owned by a
different user" hint at the top of the attach command to convert a
confusing EACCES into a clear "not supported" message, or to leave it as
a documented platform expectation.
