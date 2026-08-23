# Findings: making the live test surface agent-testable

Round 1. Six leads dispatched; results recorded here as they land.

## The exploration's own conclusion, executed

Both changes the round recommended for the Codex sandbox gate were applied
temporarily — PATH corrected to resolve the AppArmor-covered shim, gate probe
swapped from `unshare` to `codex sandbox -- /bin/true` — and the two
`@codex-live` scenarios were run against latest `main` (`fc50683`).

```
2 scenarios (2 passed)
30 steps (30 passed)
--- PASS: TestFeatures/a_live_Codex_session_writes_a_file_on_its_first_attempt (14.28s)
--- PASS: TestFeatures/a_live_interactive_Codex_session_starts_clean... (30.54s)
```

**14.28s, not 0.00s.** The scenario that had been reporting a vacuous pass in
every run and every CI job since it was written executed for real: a live Codex
session in a niwa-prepared repository, under niwa's delivered trust posture,
created the file. The gate patch was reverted and the tree left clean; nothing
was committed.

This settles the question the exploration was opened to answer. The Codex half
of the "only a human can run this" surface needs no human, no container, no
credential work, and no new infrastructure — two one-line changes and it runs on
this machine today.

## L6 — credentials for agents (complete)

Full: `wip/research/explore_agent-testable-live-surface_r1_lead-credentials.md`

**The correction this forces.** The Codex live gate already solves the credential
problem, and I had assumed it did not. `codexIsAvailable` copies the developer's
real `~/.codex/auth.json` into the per-scenario sandboxed Codex home, with a
comment saying why: the suite's own HOME redaction would otherwise hide a
working login from the binary under test. The copy is content-blind, so a
subscription login works through it exactly as an API key would.

So the two Codex live scenarios differ in exactly one thing, and it is not
credentials. The interactive one ran here (30.5s, real binary, real pty). The
"writes a file" one did not, and its only extra gate is the user-namespace
probe. **That makes L1 the load-bearing lead:** if a narrower probe is correct,
that scenario becomes runnable in this environment today with no credential
work at all.

**Claude is the asymmetric case.** `runClaudeP` never touches `~/.claude`; it
only forwards `ANTHROPIC_API_KEY` from the host env into the sandbox. A
subscription login is invisible to the suite and cannot satisfy the gate. There
is no Claude-side equivalent of the Codex auth-file copy.

**The precedent that matters is niwa's own product feature**, not CI's Infisical
step (which authenticates the vault package's own integration tests against
generic secrets — unrelated). A workspace can bind
`[claude.env.secrets] ANTHROPIC_API_KEY = "vault://..."`, resolved at apply time
into a `0o600` `.local.env`. That is the documented mechanism for handing an
agent an API key, and it is symmetric for `OPENAI_API_KEY`.

**The blocker the lead surfaced is not a credential blocker.** Nothing scrubs
captured stdout or session transcripts before they can reach a PR body, a
commit, or a `wip/` artifact. A human watching a terminal notices a key on
screen; an agent has no such reflex. The recommendation is that credentialed
agent runs depend on output redaction existing in code first — which is a
concrete, separable piece of work, and arguably the thing to build before
anything else here.

Existing guards worth crediting: the per-scenario sandbox bounds writes, CI's
"Verify the suite left the checkout alone" step is a real tripwire, and
`persist-credentials: false` keeps push credentials out of the checkout. The
gap is output, not filesystem.

## L1 — sandbox gate anatomy (complete)

Full: `wip/research/explore_agent-testable-live-surface_r1_lead-sandbox-gate.md`

**The premise this lead was given was wrong, and the correction is the finding.**
This exploration's scope file asserted that `codex exec --sandbox read-only` ran
successfully here while `codex sandbox` did not, and inferred from that
asymmetry that niwa's gate might be broader than what it guards. The asymmetry
is an artifact: codex builds the sandbox **lazily, on first shell command**, and
in those runs the model answered from context without ever calling its shell
tool. Force a shell call and `read-only` fails with the identical bwrap error.
They are one implementation.

**The gate is equal to what it protects, not broader.** Codex 0.147 uses
bubblewrap for every sandboxed mode and bundles its own (`codex-resources/bwrap`).
The Landlock+seccomp path is still compiled in but its error string calls itself
the *legacy* sandbox, and no flag, config key, or `CODEX_*` variable selects it.
Codex warns on every run that its Linux sandbox needs user namespaces. Root
cause here is `kernel.apparmor_restrict_unprivileged_userns=1` (Ubuntu 24.04):
the namespace is created, but without capabilities, so the uid_map write is
denied.

| mode | sandbox built | file created | failure |
|---|---|---|---|
| `read-only` | no | n/a | `bwrap: loopback: Failed RTM_NEWADDR` |
| `workspace-write` | no | no | same |
| `workspace-write` + network | no | no | `bwrap: setting up uid map: Permission denied` |
| `danger-full-access` | none built | **yes** | none |
| default exec, untrusted | falls back to read-only | no | `writing is blocked by read-only sandbox` |
| default exec, **trusted** (niwa's delivery) | no | no | loopback error |

The lead replicated the gated scenario by hand — default `codex exec`, its exact
prompt, project marked `trust_level = "trusted"`, which is what niwa writes and
what alone decides posture since the scenario declares none. Codex resolved
`workspace-write`, tried `apply patch` twice, failed both, no file.

**The one mode that works is a non-starter.** `danger-full-access` writes the
file, but only because it constructs no sandbox at all. The scenario's subject is
that niwa's *delivered default posture* lets a session write; running it under
full access asserts nothing about niwa's preparation.

**The gate should still change — to a better probe, not a narrower one.**
`codex sandbox -- /bin/true`: 42 ms, no auth (verified with an empty
`CODEX_HOME`), no network, no model call, and it exercises the real
implementation. That covers a gap the `unshare` probe misses — bwrap also needs
`CLONE_NEWNS` and mount operations, so a hardened container could pass the
current probe and then fail the scenario as a genuine test failure. It also
un-sticks on its own if codex ever restores a non-userns backend, where the
`unshare` probe would keep the scenario pending forever.

**Two traps for anyone writing tests here.** `codex doctor` reports its
`sandbox.helpers` check `[ok]` on a machine where the sandbox is entirely
unusable — do not use it as a probe. And a run whose sandbox failed emits no
`command_execution` item in `--json` and still exits 0, so "exit 0 plus
plausible output" is not evidence that anything ran.

## L2 — credential-free Codex assertions (complete)

Full: `wip/research/explore_agent-testable-live-surface_r1_lead-codex-oracle.md`

**Three real disagreements out of 26 fixtures**, found by running niwa's actual
unexported helpers against the binary — a scratch copy of the checkout plus a
throwaway probe test, not a reimplementation. This is the strongest evidence the
round produced, and it is the argument for the contract test on its own.

- **Symlinked directory in the walk.** A Codex process's cwd is canonical by
  construction and the binary ignores `$PWD` entirely (forced both ways).
  `codexContextChain` uses `filepath.Abs`, which does not resolve symlinks, so
  given a logical path it walks a tree no session can be in — in the fixture it
  flipped the project root and lost the root layer. Latent today only because
  `TestMain` happens to `EvalSymlinks` the sandbox for an unrelated reason.
  Symlinked *files* are handled correctly; symlinked *directories* are not.
- **Unreadable chain file.** The binary drops the **entire** context block,
  including a perfectly readable root layer, exit 0, silent. The model returns a
  two-file chain and then hard-errors on read.
- **Budget truncation.** The binary cut at exactly 32768 raw concatenated bytes,
  mid-file in the innermost layer. `codexChainBytesAt` is exactly right, but
  `codexContextBytes` performs no truncation — so `contains` steps can pass on
  content no default-budget session ever sees. That is a false-positive
  generator sitting under every content assertion in the Codex feature file.

Everything else agreed, including every rule the model exists to encode: the
bounded walk, nearest-marker-wins, gap directories, `AGENTS.override.md`
shadowing, an empty file claiming a slot, `.git`-as-pointer-file worktrees and
their negative control, `IsRegular` rejecting a directory-named candidate, and
12-level nesting.

**A discovery that partially rescues the sandbox-gated scenario.**
`codex debug prompt-input` reports the resolved **sandbox posture line**, and it
flips `read-only` → `workspace-write` on a trust stanza alone. That is a
zero-cost read of the trust registry with no session, no credentials, and no
working sandbox. It cannot assert "a session wrote a file", but it can assert
"Codex resolves a writable posture here **because** niwa's trust entry exists",
which is a large part of what the gated scenario is for. Worth weighing against
L1's verdict that the write itself is unreachable in this environment.

**Observable surface, credential-free and sandbox-free**, at 0.11s per probe
with an empty `CODEX_HOME`: context content and order, cwd, workspace roots, and
the full skill inventory with source locators including project-layer skills.
Trust-gated: MCP servers, effective `project_doc_max_bytes`, and the posture
line above.

**The contract test is cheap.** ~120 lines of Go, ~3s for 26 fixtures, gated on
`LookPath("codex")` alone — no credential copy, no user namespace. It converts
every `the Codex context at "X" selects "Y"` step from a tautology, where the
model is both the criterion and the only thing under test, into a claim about
the binary. It would be the first thing in the suite capable of noticing a Codex
version bump that changes the rules.

**Two findings beyond the ask.** The over-budget scenario's comment says a
session reads the whole chain because the project layer declares a covering
budget — true, but **only with a trust entry**; untrusted, the declared value is
ignored and the chain is cut at 32768 anyway. The suite asserts the budget half
and the trust half in separate scenarios and never their conjunction, which is
the thing that actually makes the claim true. Separately,
`project_doc_fallback_filenames` is a real key that adds candidates, so the
model's hardcoded two-name precedence is valid only under default config — not
niwa's to set, but worth a comment.

**Closes an open question in the spike.** Finding 11 noted as untested whether a
linked worktree can reach the main repository's context. Measured: it cannot.

## L3 — credential-free Claude assertions (complete)

Full: `wip/research/explore_agent-testable-live-surface_r1_lead-claude-oracle.md`

**Yes, and it is better than the assertion it would replace.** Not as a
`claude` feature — the lead enumerated all 13 subcommands of claude 2.1.238 and
found nothing that reports memory resolution. `doctor` is byte-identical from
the instance root and the sub-repo and never mentions CLAUDE.md, rules or
memory. There is no config command, no context dump, no dry-run.
`--debug-file` produced 15.6KB across 20 categories with zero hits for
`workspace-imports|workspace-context|claude.md|memory|rules`.

The oracle is `ANTHROPIC_BASE_URL` pointed at a loopback mock plus a **dummy**
API key. The fully resolved context arrives in the request body as an explicit
manifest — one `Contents of /abs/path (project instructions, checked into the
codebase):` header per file — so import resolution is directly readable. From
the instance root the payload lists `CLAUDE.md`, the rules file, **and**
`workspace-context.md` carrying the sentinel; from `tools/myapp` it lists the
first two plus the sub-repo's own CLAUDE.md and no sentinel. 3/3 deterministic,
1.19s per probe, 12KB with `--tools ""`. No model call, no quota, no real key.
A dummy key is mandatory: with none at all claude prints `Not logged in` and
captures nothing.

**An assertion trap the lead caught, which would have produced a false pass.**
From the sub-repo the rules file *is* loaded and its literal
`@/abs/path/workspace-context.md` line appears verbatim in the payload — only
the expansion is missing. Asserting on the bare string `workspace-context.md`
would pass in both directions. The assertion has to be the sentinel, or the
`Contents of …workspace-context.md` header.

**A second, independent reason the scenario could never have run.** `buildEnv`
replaces HOME with an empty sandbox, so subscription OAuth cannot resolve —
`claude auth status` reports logged out inside it. Even a developer with a
working subscription gets `ErrPending`. This is the same distinction
`test/live/dispatch_live_test.go` already documents when it deliberately does
not sandbox HOME.

**Minimal change.** In `runClaudeP`, stand up an `httptest.NewServer` recording
request bodies; set `ANTHROPIC_BASE_URL` to it and a dummy `ANTHROPIC_API_KEY`;
explicitly unset `ANTHROPIC_AUTH_TOKEN` / `CLAUDE_CODE_USE_BEDROCK` /
`CLAUDE_CODE_USE_VERTEX`, since `buildEnv` passes the inherited env through; add
`--tools ""`; store the lowercased bodies in `s.stdout` so the existing
contains / does-not-contain steps work unchanged. Keep the sandboxed HOME — it
now helps, by forcing the API-key path. Then drop the `ANTHROPIC_API_KEY` gate
and retire the `@claude-integration` tag, because nothing about it is
credentialed any more.

## L4 — environment construction (complete)

Full: `wip/research/explore_agent-testable-live-surface_r1_lead-environment.md`

**The environment already exists. The blocker is a defect in niwa.**

This is not a container — `/proc/1/cgroup` is `0::/init.scope`, PID 1 is
systemd, `systemd-detect-virt` says none. Bare metal Ubuntu 24.04 with
`kernel.apparmor_restrict_unprivileged_userns=1`. Not seccomp (mode 0), not
capabilities (full bounding set).

`niwa setup-sandbox` had **already been run here**. It installed
`/etc/apparmor.d/niwa-bwrap`, granting `userns` to exactly one path:
`~/.tsuku/tools/current/bwrap`. `command -v bwrap` resolves
`~/.tsuku/tools/bubblewrap-0.11.2/bin/bwrap` instead, which the profile does not
cover.

### Correction: the lead's attribution was wrong, twice

The lead reported this as "niwa installs a profile for one path and then shadows
it with another path to the same program." Checked directly, neither half holds,
and the true version is a better bug.

**They are not the same program.** `tools/current/bwrap` is an 859-byte tsuku
**shim** — a `/bin/sh` script that exports `LD_LIBRARY_PATH` and `exec`s the
real 114KB binary in the versioned directory. Different inodes, different bytes.
The profile covers the shim, and it works only because the profile carries
`flags=(unconfined)`, so the exec'd real binary inherits it and gets `userns`.

**niwa does not do the shadowing.** `setup_sandbox.go:126` does
`exec.LookPath("bwrap")` and profiles whatever that resolves — the correct
behavior. The only PATH entry niwa writes is `$HOME/.niwa/bin`
(`shell_init.go:179`). The versioned bin directory ahead of `tools/current`
comes from the environment this session inherited; no config file examined
writes it, and it appears **twice** in the first four entries, which suggests
something sourced more than once. Attribution beyond "not niwa's PATH code" is
unproven and is not claimed.

### What the defect actually is

The unlock is **pinned to whichever `bwrap` was on PATH at setup time, and
silently stops applying when PATH later resolves a different one.** niwa already
owns the probe that would detect this (`probeNetnsOK`) and a good error message
for it at `setup_sandbox.go:190` — but both run only at setup. Nothing re-checks
afterwards, so a host that was correctly unlocked in July is silently locked
again today, and the only symptom is a bwrap error from whatever tool tried to
use the sandbox.

Verified: the covered shim path passes niwa's own probe; the PATH-resolved
binary fails it.

```
$ ~/.tsuku/tools/current/bwrap --ro-bind / / --unshare-net --die-with-parent true
                                                             # clean
$ ~/.tsuku/tools/bubblewrap-0.11.2/bin/bwrap --ro-bind / / --unshare-net ... true
bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted
```

This also generalizes past PATH order: installing bubblewrap 0.11.3 tomorrow
creates a new versioned directory and leaves the profile pointing at a path that
no longer wins, with the same silent result.

### Verified directly, not taken on report

```
$ command -v bwrap
/home/dgazineu/.tsuku/tools/bubblewrap-0.11.2/bin/bwrap     <- not the covered path

$ codex sandbox -- /bin/true                                 # inherited PATH
bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted

$ PATH=/home/dgazineu/.tsuku/tools/current:$PATH codex sandbox -- /bin/true
                                                             # clean, no output

$ PATH=... codex sandbox -- sh -c 'touch /home/dgazineu/x && echo WROTE || echo BLOCKED'
touch: cannot touch '/home/dgazineu/x': Read-only file system
BLOCKED                                                      # and it enforces

$ PATH=... unshare --user --map-root-user true
unshare: write failed /proc/self/uid_map: Operation not permitted
```

The last line is the important one. The AppArmor profile is scoped per-binary by
design, so bare `unshare` stays denied **even on a machine where codex's sandbox
works perfectly**. niwa's gate probes `unshare`. It therefore reports "cannot run
here" on precisely the machines niwa itself has configured for this.

### This reframes L1

L1's verdict — the gate is equal to what it protects — held for the environment
as measured, but that environment was misconfigured by niwa. The gate's real
defect is not breadth: it probes a capability grant that niwa deliberately scopes
to one binary, using a different binary, so it can never go green under niwa's
own hardening recipe. L1's recommended replacement, `codex sandbox -- /bin/true`,
is now verified to go green here where `unshare` stays red. Two independent leads
arrived at the same probe from opposite directions.

### The rest

`workspace-write` leaves `$HOME/.cache/go-build` read-only, so `go vet` and
`go test` fail inside it; fix with `sandbox_workspace_write.writable_roots` or by
redirecting `GOCACHE`. Both verified. Startup is 80-100 ms and `codex sandbox`
never reads `auth.json`.

CI: `ubuntu-latest` does not permit unprivileged userns by default (same Ubuntu
24.04 restriction; runner-images#11489 closed unmerged, #10024 reverted). But a
workflow can unlock it with passwordless sudo, and **niwa already ships one that
does** — `.github/workflows/watch-live-egress.yml` runs
`sudo niwa setup-sandbox --apply-profile --bwrap-path "$(command -v bwrap)"` and
then probes, warning rather than false-passing. Note that invocation passes the
*resolved* bwrap path, which is the fix for the shadowing bug stated in niwa's
own CI and not applied to its own PATH.

Docker fallback was measured for hosts without the profile: minimum for bare
`unshare` is `--security-opt seccomp=unconfined`; full `codex sandbox` needs
seccomp + apparmor unconfined + `--cap-add SYS_ADMIN`, each load-bearing. That is
close enough to `--privileged` to be a weak boundary for testing a sandbox —
prefer the host path. Avoid `container:` jobs; Actions passes no `--security-opt`.

## L5 — pending is invisible (complete)

Full: `wip/research/explore_agent-testable-live-surface_r1_lead-pending-visibility.md`

**A correction to how this exploration's own problem statement was framed.**
Godog's pretty formatter already prints its own summary — the lead reproduced
`2 scenarios (1 passed, 1 pending)` sitting directly beside Go's `--- PASS:`
lines. The information was in the output all along. What discarded it was the
grep used to summarize the run, which read only Go's `--- PASS`/`--- SKIP`
markers and never godog's line. So this is less "the suite hides pending" and
more "the obvious way to summarize a godog run hides it", which is a different
fix and a smaller one.

The underlying mechanism is godog's documented default, not a niwa bug:
`suite_test.go` sets `godog.Options{TestingT: t}`, mapping each scenario onto a
Go subtest; `suite.shouldFail` treats `ErrPending` as a failure only when
`Options.Strict` is set, and niwa never sets it, so `t.Errorf` is never called
and the subtest passes.

**The inventory is small and bounded.** Exactly 3 of 175 scenarios carry a
pending-capable gate: `claude is available` (gating the one
`@claude-integration` scenario) and `codex is available` / `the Codex sandbox
can run here` (gating the two `@codex-live` ones). No other skip mechanism
exists in the suite.

**The sharper problem is CI, and it is durable.** No workflow anywhere installs
a `codex` CLI, and the default job runs the full untagged suite. So both
`@codex-live` scenarios have gone pending on every CI run since they were
written — permanently, with nothing distinguishing that from real coverage.
`@claude-integration` has a dedicated job that would re-run it for real once
`claude` is installed, but that job is itself gated on an `ANTHROPIC_API_KEY`
secret which the job log for niwa#268 shows is not configured. There is no
Codex equivalent of that job at all.

**One option is dead, and knowing that is worth the lead on its own.**
Converting the gates to a real `t.Skip` is not reachable through godog's
`TestingT`/subtest wiring — godog never calls `*testing.T.Skip`, and its own
`godog.T(ctx).Skip()` shim is treated identically to `nil` by `shouldFail`. It
would take forking godog or abandoning subtests for gated scenarios.

**Ranked fixes.**
1. Cheapest, breaks nothing: wrap the formatter to collect names from godog's
   `Pending()` callback, print `PENDING SCENARIOS (N): ...`, and fail only when
   a tag was explicitly requested via `NIWA_TEST_TAGS` and every scenario under
   it went pending. Pure addition.
2. A `test-functional-codex-live` Makefile target and CI job mirroring the
   Claude one, installing `codex` and seeding a login. The only fix that makes
   those two scenarios actually run anywhere.
3. Once (2) exists, `Strict` behind a `NIWA_TEST_STRICT` env var scoped to that
   job. Global `Strict` would break every developer laptop and the existing
   default CI job, where `codex` and `claude` are genuinely absent.

A precedent for honest reporting already exists in the repo:
`test/live/dispatch_live_test.go` is a plain non-godog test using real
`t.Skip()`.
