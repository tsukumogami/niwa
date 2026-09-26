---
schema: spike/v1
status: Complete
question: |
  How does codex-cli actually discover context files, project configuration, and
  skills, and which of those surfaces can niwa write into from a workspace
  instance without touching the developer's own Codex setup? Established so that
  any future dual-agent work starts from measured behavior instead of re-deriving
  it, since the mechanics are not documented upstream and two independent
  first-pass readings of them were wrong.
timebox: "Established across two implementation attempts and the background-dispatch feature; recorded here as durable findings"
---

# SPIKE: codex-cli discovery mechanics

## Status

Complete

## Why this document exists

niwa prepares workspace instances for coding agents. Preparing one for Codex
turns out not to be a filename problem: it is shaped entirely by how the binary
locates context and configuration, and that behavior is unintuitive enough that
two separate attempts to reason about it from the outside reached wrong
conclusions before measurement corrected them.

The first attempt concluded that instance- and group-level context would be
found by an upward walk, and built context materialization on it; sessions
running inside a cloned repository saw none of it. A later black-box probe
concluded the opposite — that only the working directory's own file is ever
read — because the probe directory contained no project-root marker, so no
project root was found at all. Both readings are wrong in different directions.

The findings below were established by live measurement against
`codex-cli 0.147.0` and by reading the matching upstream source at tag
`rust-v0.147.0`. They are recorded as findings, not as a design: the
implementation attempt that produced most of them was withdrawn for unrelated
structural reasons, and nothing here depends on that design being adopted.

## Findings

### 1. Discovery is a bounded downward walk

Codex locates a project root by walking up from the working directory to the
nearest ancestor containing a `project_root_markers` entry (default: `.git`),
then reads context and project configuration from that root **down** to the
working directory. It never walks above the root. When no marker exists
anywhere in the ancestry, the working directory itself becomes the root.

Every cloned repository has a `.git` at its root, so under default settings the
walk starts and stops inside the repository. Content placed above the
repository — at an instance root or a group directory — is never visited from a
session working inside that repository.

The working directory's own context file is always in the chain, whichever way
the root was found. Measured both ways at zero model cost with
`codex debug prompt-input` and an isolated `CODEX_HOME`: with a marker-bearing
ancestor carrying its own `AGENTS.md` and a child working directory carrying a
different one, both render into the prompt, and the walk runs ancestor to
working directory with the working directory always last; with no marker
anywhere in the ancestry, an `AGENTS.md` at the working directory renders on
its own. The general statement is the one that matters — under a narrower
reading, whether a session sees its own directory's file would depend on how
the tree happens to be laid out, and it does not.

That same mechanism is what makes two experiments misleading. A probe
directory with no marker anywhere above it yields a walk of one directory —
the fallback root *is* the working directory — which looks like "only the
working directory is read". And a session started at a directory that *is* the
project root reads that directory's file, which looks like an upward walk
working. Neither observation generalizes; both are the bounded walk plus the
always-last working directory seen from awkward angles.

**Amended — the same walk governs skills, measured with real plugin trees as
both controls.** A fifth pass repeated this finding's shape against the skills
surface at a marker-less working directory: a real plugin tree symlinked at
`<session dir>/.codex/skills/<plugin>` resolved all 20 of its skills,
namespaced `<plugin>:<skill>`, with no trust entry and no project-root marker
anywhere in the ancestry — and an identical real tree placed one directory
*above* the session resolved none of its skills, with Codex's own bundled
skills the only other entries. The negative control is what makes the positive
one evidence about discovery rather than placement: the tree above was really
there and really loadable, and the session did not load it. Anything asserting
that a delivered tree "reaches a session" needs both halves — a tree present
at the working directory that appears, and a tree one directory up that does
not — or it cannot tell "loaded from where the session stands" from "the walk
went somewhere else".

### 2. One file per directory, strict first-match

Each directory in the walk contributes at most one context file, chosen by
hardcoded precedence: `AGENTS.override.md`, then `AGENTS.md`, then any
configured fallback filenames, in that order. The configured fallback list
cannot reorder this.

An empty or whitespace-only file still counts as a match. It claims the
directory's slot and suppresses every remaining candidate, so writing a file
that happens to compose to nothing is worse than writing nothing at all.

The practical consequence for any writer: `AGENTS.override.md` is the only
filename that wins in every repository. Writing `AGENTS.md` delivers nothing in
any repository that ships its own.

### 3. The context budget is shared and drains outermost-first

`project_doc_max_bytes` defaults to 32768 bytes. It is a single counter spent
across the whole chain in root-to-working-directory order, and truncation is a
raw byte cut: no marker in the text, nothing on stderr.

Because it drains outermost-first, under-declaring the budget costs the
innermost layer — the context closest to the work — with no signal that
anything was lost.

### 4. Writing files is gated on trust, and trust cannot be self-granted

A directory without a `[projects."<path>"]` entry in the developer's own Codex
configuration is a read-only sandbox, and the interactive TUI blocks on a trust
prompt. Trust cannot be granted from inside a project-level config layer: a
project config vouching for itself is ignored by construction.

Finding 14 extends this picture for headless runs: the trust registry also
decides sandbox posture there, asking for elevation at an untrusted directory
writes trust back into the developer's own file, and one CLI override grants
trust per invocation without writing anything.

### 5. The project config layer is real, but trust gates most of it

A `.codex/` directory found during the walk is a genuine configuration layer.
It carries instruction context, skills, general configuration including the
byte budget, and MCP server declarations.

Trust interacts with it unevenly, and this was measured rather than inferred:

- **Skills load from an untrusted layer.** A symlinked plugin tree at
  `.codex/skills/<plugin>` resolves with the same `<plugin>:<skill>` namespace
  Claude Code produces, with no rewriting of skill content.
- **Configuration keys require trust.** A 60KB context file was silently cut at
  the 32768 default in an untrusted directory despite the layer declaring a
  larger budget; adding a trust entry for the directory made the declared value
  take effect. MCP servers behave the same way: `[mcp_servers.*]` declared in a
  project layer appears in `codex mcp list` inside a trusted repository and is
  absent outside one.

So the byte budget and the trust entry are load-bearing for each other. A
budget declared for a directory that carries no trust entry does not apply.

A later pass measured trust as the only variable and found the line is cleaner
than "uneven" suggested: MCP servers, `shell_environment_policy`,
`approval_policy` and `sandbox_mode` all appear only with a trust entry for the
path, and revert to their defaults (`on-request`, restricted) when it is
removed. Untrusted, a project-declared server and a project-declared environment
variable are both simply absent, and `codex doctor` counts only the developer's
own servers. **Skills are the lone exception
— they load untrusted.**

What the project layer cannot carry at all: trust itself, hook trust state, and
marketplace/plugin registration, plus a denylist of general keys (provider URLs,
`notify`, profiles, and similar).

Two amendments from a later measurement pass on the same build.
**`project_root_markers` is accepted at this layer** rather than rejected, and
a third pass measured the effect half this amendment originally left open.
Declaring the key inside the instance root's own `.codex/config.toml` **is**
inert, as suspected — the layer only loads after the root is found, so a
marker list cannot pull in the root that would have to carry it. But a
`-c 'project_root_markers=[...]'` override on the command line **works**: from
a repository subdirectory, `[".niwa"]` pulls a marker-bearing ancestor into
scope, and the prompt carries the ancestor's and the repository's documents
concatenated ancestor-first under a single heading naming the working
directory. Two traps in using it: keeping `".git"` in the list defeats the
point, because the nearest matching ancestor wins and that is the repository
itself; and at a directory that is already the no-marker fallback root the
override is a no-op. The levers that work are the CLI flag and the user layer.

And **the denylist count did
not reproduce**: probing roughly fifty keys and reading `codex doctor --json`
startup warnings found eight, not the eleven originally recorded. That is not
proof eleven is wrong — the enumeration covered a chosen subset — but treat the
exact figure as unsettled and the mechanism (feed a key through the startup
warning and read the count) as the way to settle it.

**A denylisted or malformed key is not inert.** One bad key fails the entire
config load, not just that key: `forced_login_method = "apikey"` failed with
`unknown variant`, and `experimental_thread_store_endpoint` is rejected
outright. "Codex ignores what it does not understand" is unsafe as an assumption
for anything generating this file.

**Two further amendments from the fifth pass, both on the skills bullet.** A
*copied* plugin tree at `.codex/skills/<plugin>` resolves exactly as a
symlinked one — the same 20 namespaced skills from the same real tree — and a
sentinel file a deliverer plants inside the copied tree neither breaks
discovery nor surfaces as a skill. So symlink-versus-copy is free as far as
Codex is concerned and can be decided on the deliverer's own reconcile terms.
And the `<plugin>:<skill>` namespace is produced by a
`.claude-plugin/plugin.json` at the tree root, nothing else: a tree shipping
only its own marketplace-format `manifest.json` resolves its skill *bare*,
under the skill's own frontmatter name, while the identical tree plus the
plugin manifest resolves it namespaced. A delivered tree that is meant to
namespace must ship that file.

### 6. `shell_environment_policy` is the environment route, with three traps

Environment variables reach a session through `shell_environment_policy` in the
project config. Resolution order is `inherit` (default `all`), then `exclude`,
then `set`, then `include_only`. `set` is additive, overrides on collision, and
takes strings only.

Three properties that bite anything generating this table:

- **`include_only` is a final allowlist and silently drops values `set` placed.**
  Declaring both, in that combination, delivers nothing.
- **No `${VAR}` interpolation happens anywhere** — not here and not in
  `mcp_servers`. Values must be fully resolved before they are written.
- **`ignore_default_excludes` defaults to `true`.** The binary carries `*KEY*`
  and `*TOKEN*` default excludes that are *not* applied unless explicitly opted
  into. Against one parent environment: 12 matching variables inherited with the
  table absent, 12 with the flag `true`, 0 with it `false` —
  `OPENAI_API_KEY` and `GH_TOKEN` among them.

Where the policy actually applies is a three-part answer, and the parts must
not be blurred into one:

1. **The policy is real and takes effect.** An `inherit = "none"` applied
   through the `codex sandbox` surface strips the environment to nothing, and
   the measurements above were all taken through that surface.

2. **In a live `codex exec` session at `sandbox: danger-full-access`, the
   model's shell tool sees the launching process environment regardless of the
   policy** — including a CLI-layer
   `-c shell_environment_policy.inherit=none`, the highest-precedence layer,
   which involves no trust at all. Two alternative explanations were ruled out
   by measurement rather than by argument: trust cannot be the variable,
   because the policy rode the CLI layer; and the probe really read the shell
   rather than the model's context, because the session rollouts carry an
   actual `tools.exec_command` call with the probe value in its output. Be
   clear about what this does not license: the policy is not broken. What was
   measured is that one posture's shell tool does not take the policy-aware
   route.

3. **At every sandboxed posture — `read-only`, `workspace-write`, the defaults
   an ordinary session runs at — this is untested**, and must not be assumed
   in either direction. The untested half is the important half:
   `danger-full-access` is the posture almost nobody runs, and the sandboxed
   defaults are what an ordinary session uses, so a reader generalizing from
   the measured case generalizes in the dangerous direction. The gap stayed
   open for a host reason, not a Codex reason: bubblewrap could not create a
   user namespace on the measuring host (`bwrap: setting up uid map:
   Permission denied`, with bare `unshare --user --map-root-user` failing
   identically), which is an AppArmor restriction on unprivileged user
   namespaces. Closing this needs a host without that restriction, then the
   same probe repeated at `read-only` and `workspace-write`.

### 7. Hooks are plugin-delivered and gated behind a blocking prompt

A loose `hooks.json` placed in a Codex home registers nothing. The working
route is a plugin carrying its own `hooks.json`, with per-plugin trust state
recorded as a `trusted_hash` under `[hooks.state]`.

An interactive session blocks on a review prompt for any hook it cannot verify
against that recorded hash. Any automated hook installation therefore either
solves the trust hash or accepts a modal in front of every session start. No
route that avoids both has been demonstrated.

### 8. Claude Code plugin manifests install into Codex unmodified

`codex plugin marketplace add` accepts a local path or `owner/repo` and reads a
`.claude-plugin/marketplace.json` verbatim. Plugins authored for Claude Code
install and their skills load with no changes to the plugin.

One limit found alongside this: plugin `agents/` directories are copied into the
plugin cache but never surface, so Claude-style named subagent types do not
exist under Codex.

A note for anything delivering skills programmatically rather than through
`codex plugin add`. Skills load from a plain `skills/<name>/SKILL.md` tree in
the project layer, including through a symlink, with no plugin registration at
all — see finding 5. Resolving that tree out of *another agent's* installation
directory couples the two products: a first implementation did exactly that for
github-sourced marketplaces and left a machine without Claude Code installed
with no skills and no way to self-heal. Fetch the content into a directory you
own instead.

### 9. Layer precedence is a recursive merge, not an override

Neither the project layer nor the developer's config wins wholesale. They merge
field by field, with the project layer winning only on the keys it actually
declares. A name collision on an MCP server produced a hybrid in one measured
run: the project layer's `command` alongside the developer's `args` and `cwd`.

Anything writing into a shared configuration has to detect collisions rather
than assume it either wins or loses cleanly.

### 10. The `mcp_servers` schema, and what silently goes wrong

`[mcp_servers.<name>]`, keyed by server name. Stdio transport: `command`
(required, and its presence is what selects the transport), `args`, `env`,
`env_vars` (names forwarded from Codex's own environment, distinct from `env`),
`cwd`. Streamable HTTP: `url` (required, selects the transport),
`http_headers`, `env_http_headers`, `bearer_token_env_var`, `oauth_resource`,
`oauth.client_id`. Both: `enabled`, `startup_timeout_sec`, `tool_timeout_sec`,
`enabled_tools`, `disabled_tools`. Established by generating the file with
`codex mcp add` and round-tripping every field through `codex mcp get --json`
rather than by guessing the TOML.

Three failure modes matter to any generator:

- **There is no SSE transport, and `type = "sse"` is not rejected.** It is
  silently served as streamable HTTP, so a server declared SSE is a live
  protocol mismatch rather than an obviously missing server.
- **One malformed entry fails the whole config load**, not just that server.
  There is no per-server failure isolation.
- **Unknown fields vanish silently**, and `--strict-config` is unavailable on
  the `mcp` subcommand, so a mistranslated field name yields a server that
  loads and quietly lacks the behavior.

### 11. A linked worktree's `.git` file satisfies the project-root marker

In a linked git worktree, `.git` is a regular file holding a `gitdir:` pointer
rather than a directory. It still satisfies the marker. Measured with a context
file placed only at the worktree root and a session run two directories below
it, so the file was reachable only if the walk found a root; a negative control
with no marker anywhere above returned nothing, which is what makes the positive
result trustworthy rather than the walk picking up its own directory's file.

Untested: whether the main repository's context is also reachable from inside a
linked worktree. It should not be, but that follows from finding 1 rather than
from anything measured.

### 12. There is no per-directory config discovery outside the walk

A `.codex/config.toml` in a directory the walk does not visit is not read. This
is a direct consequence of finding 1 and is worth stating separately because it
is the specific way a naive experiment fails: a config file placed at a
*parent* of the project root, or at a sibling, is simply invisible.

**Amended.** This finding originally said "a directory with no project-root
marker above it", which reads as excluding the marker-less directory the session
is actually in, and that is false. A later pass measured a `.codex/` layer at a
marker-less working directory in both ancestries — no marker anywhere, and a
marker-bearing ancestor above — and it is read in both, on the same trust line
as anywhere else: the skills tree loaded with no trust entry, and
`[mcp_servers.*]`, `approval_policy` and `sandbox_mode` took effect once the
directory carried one. The rule is the walk, and by finding 1 the working
directory is always the walk's last directory; it is never outside its own
discovery.

### 13. Outside a git repository, `--skip-git-repo-check` is mandatory and trust is not a substitute

`codex exec` at a directory that is not a git repository and has none above it
dies at startup with exit 1 and `Not inside a trusted directory and
--skip-git-repo-check was not specified.` The failure happens before any model
call, so probing it is free. `--skip-git-repo-check` makes it start normally.

The error message implies trust would satisfy the check. It does not: marking
the directory `trust_level = "trusted"` reproduces the identical failure. The
flag is mandatory for any launcher starting sessions outside a repository, not
a workaround for missing trust.

### 14. The trust registry decides sandbox posture, and elevated flags write trust back

Whether a headless session can write is decided by the trust registry rather
than by the command line. The default posture at an untrusted directory is
`approval: never`, `sandbox: read-only`. The identical command at a directory
carrying a `[projects."<path>"] trust_level = "trusted"` stanza lands in
`sandbox: workspace-write [workdir, /tmp, $TMPDIR]`.

Asking for elevation at an untrusted directory has a side effect on the
developer's own configuration. Passing `--sandbox workspace-write` or
`--dangerously-bypass-approvals-and-sandbox` there **appends the trust stanza
to `$CODEX_HOME/config.toml`**, and so does
`-c 'sandbox_mode="workspace-write"'` — the write-back triggers on an
effective elevated posture at an untrusted directory, whatever the source, so
routing the posture through config instead of the flag is not a workaround.
Plain runs write nothing.

One zero-footprint route exists:
`-c 'projects={"<abs path>"={trust_level="trusted"}}'`, overriding the whole
projects table as a TOML inline table, gives the invocation `workspace-write`
and leaves the file unchanged. The dotted-path spelling
`-c 'projects."<path>".trust_level="trusted"'` parses cleanly and silently
does nothing — and that is not a quoting artifact, because it fails
identically on a dot-free path. The general lesson deserves its own sentence,
because it reaches past this key and past this feature: **a clean exit from a
`-c` override is not evidence the override took effect.**

One caveat here is a host property, not Codex behavior. On the measuring host
a `workspace-write` sandbox fails to initialize at all — the same AppArmor
user-namespace restriction described in finding 6 — so a worker granted
`workspace-write` there degrades to explaining in its final message that it
cannot write. The same failure blocks *reads* through the sandbox helper: one
real run answered a question about a file's contents with an invented token
rather than reporting that it could not read the file. Both are what a sandbox
that cannot construct itself looks like on that host, not what
`workspace-write` does on a working one.

### 15. `codex exec` autonomy is `--sandbox`, and `--full-auto` does not exist there

`codex exec` has no `--full-auto` and no `-a`/`--ask-for-approval`; both are
interactive-only. Autonomy on `exec` is `--sandbox`, `--approve-for-me`
(unmeasured), and `--dangerously-bypass-approvals-and-sandbox`. Any document
that reaches for `--full-auto` in a headless invocation is stale.

### 16. Exit status is not a task-success signal

A worker under the default read-only sandbox, instructed to create a file,
fails the write and still exits 0, with the model explaining in its final
message that it could not. The exit codes mean: 0 the session ran, 1 runtime
error, 2 argument parse error. An API failure of any kind — quota exhaustion
included — is exit 1 among all the other runtime errors. Anything supervising
workers has to read the output, not the status.

### 17. Thread ids and rollouts are stable, live, and in local time

`codex exec --json` emits `{"type":"thread.started","thread_id":"<uuid>"}` as
the first line of stdout — measured at +0.665s of a 12.1s run — and no later
event repeats the id. The rollout file for the same run existed with a
complete, parseable `session_meta` first line at +0.716s, measured with a 50ms
poller. So the hypothesis that a rollout is only written when a run finishes
is false: both the id and the rollout are available almost immediately, while
the session is still running.

Two details that matter to anything managing these files: the rollout tree's
`YYYY/MM/DD` directories use host **local** time, not UTC, and every rollout's
first line is roughly 18.5KB because it embeds the whole system prompt.

`codex exec resume <uuid>` appends to the existing rollout and creates no new
file, and its own `thread.started` reports the resumed id — so a session id
and its rollout path are both stable across arbitrarily many resumes. There is
no `--session-id` or `--thread-id`: Codex mints the UUIDv7 itself and a caller
learns it afterward. And one live footgun: `resume` given a **non-UUID** id
treats it as a thread name, and for an unknown name it silently starts a fresh
session and exits 0. Anything passing user input through to `resume` can
quietly fork a conversation instead of failing.

### 18. A session cannot be resumed while its own turn is still running

Resume is stable and cheap once a turn ends (finding 17). It is refused while
one is in flight. Measured with a worker mid-turn — a `codex exec` running a
`sleep 40` — a second process running `codex exec resume <id>` against the same
session exits 1 with:

```
ERROR codex_core::session::session: failed to initialize thread persistence:
  thread-store conflict: thread <id> already has an active writer
Error: thread/resume: thread/resume failed: thread <id> already has an active
  writer (code -32600)
```

It is not specific to the exec surface. The interactive `codex resume` was
measured separately against a different live worker, under a real pty, and
fails the same way — exit 1 after 1.27s, `thread/resume failed during TUI
bootstrap: … already has an active writer`, dropping the user back to the
shell. And it is a conflict on the writer, not a permission or a validation
error, so it clears by itself the moment the turn ends; the resume that failed
here succeeded against the same session afterwards.

The mechanism is a per-thread writer lock: `$CODEX_HOME/thread-writer-locks/`
holds one zero-byte `<thread-id>.lock` per thread with a live writer, created
when a process opens the thread and removed when it exits. That is what makes
the refusal deterministic rather than racy. The lock is advisory and the kernel
releases it when the holder dies, so a file left behind by a killed worker does
not brick the thread — a later resume acquires it anyway and the file's mtime
moves.

**Amended — the last sentence originally read "Stale `.lock` files sitting in
that directory are routine", and that is false.** A clean exit *unlinks* the
lock file: across five clean exits the directory held only `.coordination.lock`
afterwards. So a `.lock` file present with no holder is produced only by an
abnormal death, and it is litter rather than a routine state. The one file that
prompted the original claim was re-measured and found to be *held* — a live
interactive session had held that flock continuously for five days
(`/proc/locks` showed the row against a running `codex` pid), which is also
positive evidence that the lock does not decay on long-lived sessions.

Two further mechanics, measured while building instance reclamation on this
lock. It is a genuine BSD advisory lock rather than a marker file: `strace` of a
session bootstrap shows `flock(fd, LOCK_EX|LOCK_NB)` against the thread's lock
file, with no `LOCK_UN` anywhere, so the lock is held for the process lifetime
and released when the fd closes. Polled from another process every 0.5s it was
unbroken for the whole turn — 157.8s and 67.3s across two runs, no free sample
in between — and released within about 0.3s of the worker exiting. And the file
is a *fresh inode* every time a writer attaches, since it is unlinked at exit
and re-created at the next open, so anything checking it must open the path
again on each probe and must never cache an fd or inode.

One hazard for anything probing this lock: opening with `O_CREAT` manufactures
a lock file for a thread that has already exited, which is indistinguishable
from a crash leftover. Open without it and read a missing file as "no writer" —
which, given that a clean exit unlinks, is an answer rather than a failure to
look. Testing for the file first and opening second would be the same answer
with a race in the middle.

`.coordination.lock` in the same directory is not a per-session signal. It is a
directory-wide blocking `LOCK_EX` held only for the instant of a registry
mutation: across a full 68s live run it never appeared in `/proc/locks` at any
sample, never appeared in the worker's fd table, and its mtime never moved
across six sessions.

The refusal is clean, not merely survivable, and that rules out most of what
you would otherwise have to worry about. Across a rejected resume the worker's
rollout was byte-identical (85056 bytes, 19 lines, same md5 before and after);
the worker completed normally, emitted its final message, and printed nothing
at all about the attempt; no second rollout was forked; and no model spend was
incurred, because the refusal lands during session bootstrap before a turn
starts — the failed resume's stdout was zero bytes. Rollout corruption from
concurrent writers is ruled out at the source: a second writer is never
admitted.

One usability note for anything wrapping this. The message is phrased in
Codex-internal terms — "thread-store conflict", "code -32600" — rather than
"that session is still running", so a caller that surfaces it raw hands its
user an error about a data structure instead of about their session.

That combination is easy to miss, because the natural way to test resume is
against a session that has finished, which is exactly the case that works. It
matters to anything that resumes a session it just started: the turn is in
flight by construction, and the caller gets a store-conflict error out of an
operation that otherwise worked.

`codex exec resume` also takes far fewer flags than `codex exec` — its usage
line is `resume --skip-git-repo-check --config <key=value> <SESSION_ID>
[PROMPT]`, and passing `--sandbox` there is a parse error and exit 2. Posture
on a resume goes through `-c` config keys.

### 19. `codex exec` reads stdin, and a detached child needs its own session and file-backed stdio

`codex exec` reads stdin *in addition to* its positional prompt, and on an
inherited or held-open stdin it blocks before doing anything: measured at 20
seconds of nothing — zero stdout, no rollout file, no API call, one line on
stderr. The failure leaves nothing on disk to diagnose it by, which is exactly
the shape of hang a launcher would misattribute to the network.

Launching detached works cleanly. `cmd.Start()` with
`SysProcAttr{Setsid: true}` and stdio redirected to files returned in about
670 microseconds; the child survived the parent's exit, was reparented,
completed its turn, wrote its rollout, and ignored signals sent to the
launcher's process group.

stderr is not empty on a healthy run — 1.4KB of MCP tracing on the measuring
host — and must never be merged into a `--json` stdout stream.

### 20. The trust override is per process, and a resume is a new process

This finding and the two after it were measured against `codex-cli 0.149.0`, a
newer build than the rest of this document.

The inline-table override from finding 14 —
`-c 'projects={"<dir>"={trust_level="trusted"}}'` — grants posture to the one
invocation carrying it, and a resume is a new invocation. A session launched
with the override records `sandbox_policy: workspace-write` on its first turn.
The same session resumed without the override records `read-only`; resumed
with it, `workspace-write` again.

Two alternative readings were ruled out by measurement rather than argument.
The drop is not subagent contamination: subagent threads write their own
separate rollout files, and the drop reproduces on a plain `codex exec resume`
with no subagent present. And it is not decay within a process: the launched
process stays `workspace-write` for its whole life, with the first `read-only`
turn appearing only at the next process's bootstrap.

### 21. A session's resolved posture is observable at zero model cost, with no credential

Codex writes each turn's `turn_context`, carrying `sandbox_policy` and
`approval_policy`, into the session rollout at turn bootstrap, before the
first model request. Declaring a model provider whose `base_url` points at an
unreachable endpoint therefore lets every turn bootstrap, record its posture,
and then fail on connect. This needs no login, which matters because CI has
none.

The standing `-m bogus-model-xyz` probe recorded in the Method section still
works but does need a credential: with one present it constructs the session
and dies at the API boundary with a 400; under an empty `CODEX_HOME` on
0.149.0 it did not reach session construction within 60 seconds. Two more
mechanics bound what a probe can do: a session whose turn is still running
cannot be resumed (finding 18's writer lock), and against an unreachable
endpoint the binary retries rather than ending the turn, so a probe has to
bound its own runs.

### 22. An explicit sandbox selection outranks the trust-derived default, and the two resume forms differ

On the interactive `codex resume`, the override alone records
`workspace-write`; the override plus an appended `--sandbox read-only` records
`read-only`. A posture the developer names wins, and it wins in the binary's
own resolution rather than by argument ordering. The flag surface differs
between the two resume forms: `codex resume` accepts both `-c` and
`-s/--sandbox`; `codex exec resume` accepts `-c` and has no sandbox flag at
all (`error: unexpected argument '--sandbox' found`), which matches the usage
line recorded in finding 18.

The whole-table override also merges rather than replacing, even though it
names the entire `projects` table: with the developer's own configuration
trusting directory A, an invocation in A carrying an override naming only B
still resolves writable for A. The override adds trust for the directory it
names and does not strip trust granted elsewhere. The measured case is the
disjoint one, which is the only shape niwa produces. And the grant is keyed to
the directory it names: an invocation in an untrusted B resolves read-only,
and the same invocation with an override naming B resolves writable.

## What this rules in and out for a writer

Reachable from a workspace instance, without touching the developer's own Codex
configuration: instruction context (via `AGENTS.override.md` at each repository
root, and via `AGENTS.md` at a niwa-owned directory a session may be started in,
per the amendment to finding 12), skills (via a project-layer `skills/`
directory), the context budget, MCP servers, and environment variables — the
last three only where a trust entry exists.

Not reachable from the project layer: trust, marker configuration, marketplace
and plugin registration, and hook installation. Trust in particular has to be
written into the developer's own configuration, answered by the developer at a
prompt, or granted per invocation with the inline-table override in finding 14
— the one measured route that leaves the developer's file untouched. Marker
configuration likewise works from the command line or the user layer, just not
from the project layer itself (finding 5).

## Method

Measurements were taken against `codex-cli 0.147.0` on Linux, in an isolated
`CODEX_HOME` so the developer's real configuration was never modified, using
`codex debug prompt-input` to render the model-visible prompt and
`codex mcp list` to check server registration. Behavior was cross-checked
against upstream source at tag `rust-v0.147.0`.

These findings are version-specific. Re-verify them against the targeted binary
before relying on them; the discovery rules in findings 1 through 3 are the ones
most likely to change and the most expensive to get wrong.

Findings 6 and 9 through 11, and the trust and denylist amendments in finding 5,
come from a second measurement pass on the same build, using the same isolation
discipline plus `codex mcp add` / `codex mcp get --json` for schema round-trips,
`codex doctor --json` startup warnings for denylist probing, and `codex sandbox`
for environment policy. Where that pass could not reach a question it is labelled
untested rather than inferred; those labels are load-bearing and should survive
future edits.

Findings 13 through 19, the finding 1 statement that the working directory
always contributes, the closure of the `project_root_markers` question in
finding 5, and the narrowing of finding 6's untested label come from a third
pass against the same build on Linux with ChatGPT auth, taken while building
background dispatch for Codex. Most of the posture measurements cost zero
model turns: `codex debug prompt-input` renders the prompt without a session,
and passing `-m bogus-model-xyz` to `codex exec` prints the full startup
header and constructs the session before dying at the API boundary. The
control that validates the bogus-model probe is that
`--sandbox workspace-write` under it still wrote its trust stanza, which shows
the write-back happens at session start rather than at any model turn.

The amendments marked as a fifth pass in findings 1 and 5 were taken against
the same build while designing skills delivery at a workspace instance root,
with the same isolation discipline and zero model turns: a real 20-skill
plugin tree, symlinked and separately copied, at and one directory above a
marker-less session directory, rendered with `codex debug prompt-input` under
an isolated and completely empty `CODEX_HOME` — empty is load-bearing, since
it is also what established that the render needs no credential. The
namespacing measurement varied exactly one thing between runs: the presence of
`.claude-plugin/plugin.json` at the delivered tree's root.

Finding 18's amendment and the lock mechanics beside it come from a fourth pass
against the same build, taken while building instance reclamation on that lock.
The lock's syscall was established with `strace -f -y -e trace=flock,fcntl` over
a bogus-model bootstrap, which reaches the lock and costs nothing; the hold
duration, the release timing, the SIGKILL edge and the unlink-on-clean-exit
behavior were taken from live runs whose turns were a single `sleep`, polled
from a separate process with `flock -n` and cross-checked against `/proc/locks`
by inode. The correction to the "stale locks are routine" claim came from
checking the one file that had been assumed stale rather than from a new run,
which is the whole lesson: the assumption had been made twice from the file's
age alone, and one look at `/proc/locks` refuted it.

Findings 20 through 22 come from a sixth pass, taken against
`codex-cli 0.149.0`, a newer build than the rest of this document, while
making dispatched workers resumable at their launch posture. The isolation
discipline is the same: an isolated `CODEX_HOME`, with the developer's real
`~/.codex` never written. Posture was read from each turn's `turn_context`
line in the session rollout, which finding 21 establishes as written at turn
bootstrap. The runs needed no credential, because a declared model provider
with an unreachable `base_url` lets a turn bootstrap and record its posture
before failing on connect, and each run was bounded from outside, since the
binary retries the connect rather than ending the turn.

Two ways a measurement here can look rigorous and not be. A checksum of
`$CODEX_HOME/config.toml` is not a usable change signal, because every run
rewrites an unrelated marketplace `last_updated` timestamp; the trust-relevant
signal is the count of `[projects.` stanzas. And a clean exit from a `-c`
override is not evidence the override took effect — finding 14's dotted-path
trap is the measured case.

Some sandboxed-posture measurements could not be taken on the measuring host
at all, because bubblewrap cannot create a user namespace there
(`bwrap: setting up uid map: Permission denied`; bare
`unshare --user --map-root-user` fails identically) — an AppArmor restriction
on unprivileged user namespaces, not anything about Codex. Findings that hit
this limit are labelled with the host property and with what closing them
needs: a host without that restriction.
