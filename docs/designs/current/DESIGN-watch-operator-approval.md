---
schema: design/v1
status: Current
problem: |
  In a sandboxed `niwa watch --once` review session (`watch_sandbox = required`),
  an out-of-instance Write/Edit/MultiEdit/NotebookEdit is a hard deny (PR #198).
  The deny is secure, but it gives the operator no in-the-loop choice: a rare,
  anomalous out-of-instance write is blocked flat rather than surfaced as an
  approve/deny decision in the `claude agents` view. A prior attempt at an
  operator prompt failed because the session runs under bypassPermissions, where
  a hook's `ask` decision is silently treated as allow (fail-open).
decision: |
  Run the sandboxed review session under a non-bypass permission mode
  (`permissions.defaultMode = "default"`) so a PreToolUse hook's `ask` decision is
  honored. niwa seeds workspace trust for the ephemeral instance in `~/.claude.json`
  (`projects[<instance>].hasTrustDialogAccepted`) so in-instance autonomy is not
  broken by the untrusted-workspace path, adds auto-allow hooks for the normal
  review tools (Bash/Read/Glob/Grep and in-instance writes) so the session does not
  hang on a prompt in `--bg`, and reworks the filesystem guard to emit
  `permissionDecision:"ask"` for an out-of-instance write and `"allow"` for an
  in-instance one. The egress deny, the Bash post-guard, and the OS sandbox are
  unchanged. When niwa cannot guarantee the two spike-surfaced prerequisites
  (a trusted workspace, and an instance not under `~/.claude`), it falls back to the
  shipped hard-deny posture, which stays the fail-closed floor.
rationale: |
  A feasibility spike proved the posture on a real `claude --bg` host: under
  `default` mode with trust seeded, the normal review work runs autonomously, the
  out-of-instance write blocks pending approval and fails closed if unanswered, the
  pending state surfaces in `claude agents --json` (`status:"waiting"`,
  `waitingFor:"permission prompt"`), and egress plus Bash stay caged. Trust cannot
  be granted by a project-local setting or a CLI flag -- `~/.claude.json` is the
  only store -- so niwa seeds it, and asserts the instance is outside `~/.claude`
  (a location Claude Code protects independently of mode/trust/hooks). The change is
  additive: the `ask` path is the upgrade for the anomalous case, the hard deny is
  retained verbatim as the floor whenever a prerequisite cannot be met, so the
  security boundary never regresses.
---

# DESIGN: operator-approval for out-of-instance writes in sandboxed review sessions

## Status

Current

Refinement of the shipped `niwa watch --once` containment (see
`docs/designs/current/DESIGN-niwa-watch-once-pr-review.md` and PR #198),
authored from issue [#201](https://github.com/tsukumogami/niwa/issues/201) after
its Step-1 feasibility spike returned PROCEED.

## Context and Problem Statement

`niwa watch --once` dispatches a review agent into an isolated instance to draft a
PR review from untrusted PR content. In `watch_sandbox = required` mode the
session is caged by a combination: the OS no-egress sandbox over Bash, a PreToolUse
hook denying the WebFetch/WebSearch/MCP channels the sandbox does not cover, and a
filesystem guard denying built-in Write/Edit/MultiEdit/NotebookEdit calls that
resolve outside the instance. The filesystem guard exists because those built-in
tools run through the permission system, which a dispatched session's
`bypassPermissions` mode skips; without the hook an injected agent could write
`~/.ssh/authorized_keys`, `~/.bashrc`, or a `~/.gitconfig` `hooksPath` and persist.

Today that guard is a hard deny: `niwa watch guard-fs` exits 0 (in-instance) or 2
(out-of-instance), and the hook wrapper maps any non-zero to `exit 2`. A hard deny
is the correct default -- a review-drafting agent's only legitimate writes (its
draft, clone-local files) are in-instance, so an out-of-instance write is never
legitimate. But the deny removes the operator from the loop. The desired behavior
is an operator approval: the anomalous write surfaces as an approve/deny decision
in the `claude agents` view, and if unanswered it fails closed.

The obvious mechanism -- have the guard emit `permissionDecision:"ask"` -- does not
work under the current posture. A dispatched session runs under
`permissions.defaultMode = "bypassPermissions"` (inherited from the materialized
workspace settings), and under bypass a hook's `ask` is treated as allow. The spike
confirmed this empirically: the same `ask` fired, computed the right instance root,
and the write landed anyway (fail-open). So the change is not just to the guard; it
is to the permission mode the whole session runs under, plus the machinery that a
non-bypass mode requires to keep the normal work autonomous.

## Decision Drivers

- **D1 -- The `ask` must be honored, not silently allowed.** This rules out
  `bypassPermissions` for the review session and forces a non-bypass mode where hook
  `ask`/`allow` decisions are respected.
- **D2 -- Normal review work must stay autonomous in `--bg`.** Under a non-bypass
  mode the session must not hang on a permission prompt for Bash git ops, Read/Glob/
  Grep, or in-instance writes. The spike showed an untrusted workspace also breaks
  this: Claude Code ignores project `permissions.allow` entries and hook `allow`
  decisions in an untrusted workspace, so trust is a hard prerequisite, not a nicety.
- **D3 -- Fail closed, never fail open.** An unanswered `ask` must leave the write
  un-landed and the session recoverable (approve or stop). A malformed or adversarial
  hook payload must deny, not ask.
- **D4 -- The other guards must not regress.** Network egress (WebFetch/WebSearch/
  MCP) and Bash egress stay denied under the new mode.
- **D5 -- The hard deny remains the floor.** The `ask` posture is additive. Whenever
  niwa cannot guarantee its prerequisites in a given deployment, the session must
  fall back to the shipped hard deny rather than fail open.
- **D6 -- Minimise the blast radius.** The change is confined to the review-session
  settings assembly and the guard; it does not touch unrelated watch/sandbox
  behavior, the OS sandbox stanza, or the egress/post guards.

## Considered Options

### Option A -- Keep `bypassPermissions`, make the guard smarter

Leave the session under bypass and try to have the guard surface an approval some
other way (e.g. writing a sentinel the operator polls). Rejected: under bypass there
is no permission prompt to surface, so there is no `claude agents` approval UI to
hook into. Any niwa-built approval channel would be a parallel, unfamiliar UI that
does not fail closed the way the harness's own prompt does. The spike's whole point
was that the harness's `ask` is the right surface, and it is inert under bypass.

### Option B -- Run under `default` mode, rely on the existing hooks only

Switch the review session to `default` mode and keep only the current hooks
(egress deny, fs guard, post guard). Rejected: under `default`, every normal tool
call (Bash, Read, Glob, Grep, in-instance Write) that is not explicitly allowed
prompts for permission. In a detached `--bg` session that is an unrecoverable hang
-- exactly the D2 failure. `default` mode needs auto-allow hooks for the normal
tools before it is viable.

### Option C -- Run under `default` mode with a project-local trust marker

Switch to `default`, add auto-allow hooks, and mark the workspace trusted via a
project-local setting (`.claude/settings.json` or `settings.local.json`). Rejected:
no project-local trust store exists. Claude Code persists workspace trust only in
the user-global `~/.claude.json` under `projects[<abs-path>].hasTrustDialogAccepted`.
Project `permissions.allow` entries and hook `allow` decisions are ignored until that
flag is set. A CLI flag that grants trust without editing `~/.claude.json` does not
exist either (`--dangerously-skip-permissions` is just bypass under another name and
would re-introduce the D1 fail-open). So trust must be seeded in `~/.claude.json`.

### Option D -- Chosen: `default` mode + seeded trust + auto-allow hooks + `ask` guard, with hard-deny fallback

Run the sandboxed review session under `permissions.defaultMode = "default"`; seed
trust for the instance in `~/.claude.json`; add an auto-allow PreToolUse hook for
`Bash|Read|Glob|Grep`; rework the filesystem guard to emit `permissionDecision:"ask"`
for an out-of-instance write and `"allow"` for an in-instance one; keep the egress
deny, post guard, and OS sandbox unchanged. When either prerequisite (trusted
workspace, instance not under `~/.claude`) cannot be guaranteed, fall back to the
shipped hard-deny posture. This is the spike's validated working-settings shape,
made additive so the deny stays the floor.

## Decision Outcome

Adopt Option D. The sandboxed review session is assembled in one of two postures:

- **Operator-approval (ask) posture** -- the upgrade, used when niwa can guarantee
  the prerequisites. `permissions.defaultMode = "default"`; instance trust seeded in
  `~/.claude.json`; auto-allow hook on `Bash|Read|Glob|Grep`; the filesystem guard
  emits `allow` for in-instance and `ask` for out-of-instance writes (fail-closed
  deny on a malformed payload); egress deny and post guard unchanged.
- **Hard-deny posture** -- the shipped floor (PR #198), used verbatim as the
  fallback. The session keeps the inherited `bypassPermissions`, the filesystem
  guard exits 0/2 (in/out), egress deny and post guard unchanged.

Posture selection happens per instance, before launch. niwa attempts to satisfy the
prerequisites; success yields the ask posture, any failure yields the hard-deny
fallback. The posture in force is reported so the operator always knows which
guarantees apply.

## Solution Architecture

### Components

- **Posture resolution (`internal/cli/watch.go`, `stageReview`)** -- after applying
  the review settings' sandbox baseline, niwa attempts to establish the ask
  prerequisites for the provisioned instance. It resolves the real `HOME`, asserts
  the instance path is not under `<HOME>/.claude`, and seeds trust. If all succeed,
  the instance is assembled in the ask posture; otherwise it falls back to hard deny.
- **Trust seeding (`internal/watch`, new `EnsureInstanceTrusted` / `RemoveInstanceTrust`)**
  -- an atomic read-modify-write of `<HOME>/.claude.json` that adds
  `projects[<instanceRealPath>] = {hasTrustDialogAccepted: true,
  hasTrustDialogHooksAccepted: true}`, preserving every other key, written via a
  temp file and rename. `hasTrustDialogHooksAccepted` is set alongside so a
  hooks-trust prompt cannot hang the `--bg` session. Removal is best-effort on
  instance destroy so stale entries for reclaimed instances do not accumulate.
- **Settings assembly (`internal/watch/containment.go`, `ApplyReviewSettings`)** --
  gains an `ask` posture flag. In the ask posture it writes
  `permissions.defaultMode = "default"` (fully owned, overriding the inherited
  bypass), appends the auto-allow hook, and wires the filesystem guard in ask mode.
  In the hard-deny posture it behaves exactly as today (no `defaultMode`, guard in
  deny mode). `VerifyReviewSettings` re-verifies the posture-appropriate shape.
- **Filesystem guard (`internal/watch/guardfs.go`, `GuardFSDecision`)** -- gains an
  `askOutside` mode. In deny mode it is unchanged (exit 0 in-instance, exit 2
  out-of-instance, exit 2 fail-closed). In ask mode it prints a PreToolUse decision
  object to stdout -- `permissionDecision:"allow"` for in-instance, `"ask"` for
  out-of-instance -- and exits 0; fail-closed inputs (unreadable, unparseable, no
  target, no root) still exit 2 (deny).
- **Guard CLI (`internal/cli/watch_guard.go`)** -- the hidden `niwa watch guard-fs`
  gains an `--ask-outside` flag the ask-posture hook bakes in.

### The two hook shapes for the filesystem guard

Hard-deny (unchanged):

```
<niwa> watch guard-fs --root <instance>; ec=$?; if [ "$ec" = "0" ]; then exit 0; else exit 2; fi
```

Ask posture:

```
<niwa> watch guard-fs --root <instance> --ask-outside
```

In ask mode the decision rides the JSON on stdout, so the wrapper passes the exit
code straight through (0 for an emitted allow/ask decision, 2 for a fail-closed
deny). The JSON shapes:

```json
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"in-instance write permitted by niwa watch review guard"}}
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"out-of-instance write; operator approval required"}}
```

### The auto-allow hook

A PreToolUse hook on `Bash|Read|Glob|Grep` that emits an `allow` decision and exits
0. It runs only in the ask posture (in the hard-deny posture bypass already allows
these). It coexists with the Bash post guard: for a normal Bash call the post guard
exits 0 (no decision) and the auto-allow emits `allow`; for a `gh pr review`/`gh pr
comment` the post guard exits 2 (deny), and an explicit deny overrides an allow, so
posting stays blocked.

### Data flow (ask posture)

```
stageReview
  -> provision instance, fetch PR head (unchanged)
  -> resolve HOME; assert instance not under ~/.claude
  -> EnsureInstanceTrusted(instance)                (writes ~/.claude.json)
  -> ApplyReviewSettings(instance, sandbox=true, ask=true)
       writes settings.json: sandbox stanza + defaultMode=default
       + PreToolUse: post-guard, egress-deny, auto-allow, fs-guard(--ask-outside)
  -> dispatchLaunch (default mode, real HOME/daemon, --bg)
  -> [in session] in-instance writes -> guard allow -> land
                  out-of-instance write -> guard ask -> blocks pending approval
                                        -> surfaces in `claude agents` view
                                        -> unanswered => write never lands
  -> on destroy: RemoveInstanceTrust(instance)      (best-effort)
```

## Implementation Approach

1. Add `askOutside` to `GuardFSDecision` and emit the PreToolUse decision JSON on
   stdout for the in/out cases while keeping every fail-closed path a deny; wire the
   `--ask-outside` flag on the guard CLI.
2. Add `EnsureInstanceTrusted` and `RemoveInstanceTrust` in `internal/watch`, with an
   atomic, key-preserving merge of `~/.claude.json` and a resolvable `HOME` seam for
   tests.
3. Extend `ApplyReviewSettings`/`VerifyReviewSettings` with the ask posture: own
   `permissions.defaultMode = "default"`, append the auto-allow hook, and select the
   guard hook shape; keep the hard-deny posture byte-for-byte as shipped.
4. In `stageReview`, resolve the posture: attempt trust seeding and the
   `~/.claude` assertion; on success use the ask posture, else fall back to hard
   deny; report the posture. Remove the trust entry when the instance is destroyed.
5. Extend the live adversarial gate (`internal/watch/adversarial_test.go`) to assert,
   under the ask posture, both that the out-of-instance write fails closed (absent
   file) and that it surfaced a pending approval (`claude agents --json` shows the
   session waiting on a permission prompt). Keep the egress assertions. Update the
   unit tests (`guardfs_test.go`, `containment_test.go`) for the new posture.
6. Update the docstrings that assert "hard deny, not an ask" to describe both
   postures.

## Security Considerations

- **The deny stays the floor (D5).** The ask posture is entered only when both
  prerequisites hold; any failure -- no `HOME`, an instance under `~/.claude`, or a
  failed trust write -- falls back to the shipped hard deny. The boundary never
  fails open relative to today.
- **Fail-closed guard (D3).** Every non-classifiable guard input (unreadable,
  unparseable, missing target path, undeterminable root) remains a hard deny (exit 2)
  in both postures. Only a cleanly-resolved out-of-instance target becomes an `ask`;
  a cleanly-resolved in-instance target becomes an `allow`.
- **Unanswered `ask` is fail-closed.** In a `--bg` session an out-of-instance write
  blocks pending approval; the write does not land and the session is recoverable
  (approve or stop). The gate asserts the absent file, not the agent's self-report.
- **Trust seeding is scoped and reversible.** niwa adds exactly one
  `projects[<instance>]` entry, preserving all other `~/.claude.json` content via a
  temp-file-and-rename write, and removes it on instance destroy. It never grants
  trust for anything but the ephemeral instance path. `~/.claude.json` is a file in
  `HOME`, not under the protected `~/.claude` directory, so writing it does not touch
  the sensitive location.
- **Egress unchanged (D4).** The egress-deny hook and OS sandbox are untouched, so
  WebFetch/WebSearch/MCP and Bash network egress stay denied under `default` mode.
- **Auto-allow does not widen the boundary.** The auto-allowed tools (Bash, Read,
  Glob, Grep) were already allowed under bypass; Bash egress is still caged by the OS
  sandbox and posting is still blocked by the post guard. The net tool posture for
  the normal case is unchanged; only the out-of-instance write changed from deny to
  operator-gated.
- **Residual: `~/.claude.json` write race.** The real Claude daemon also writes
  `~/.claude.json`. niwa's atomic temp-and-rename merge preserves existing keys, and
  the seed happens before the daemon has an entry for the fresh instance, so the
  window is narrow; a lost seed degrades to a broken-autonomy session the operator
  can re-stage, not to a fail-open. Tracked as a residual, not a blocker.

## Consequences

### Positive

- The operator gets an in-the-loop approve/deny for the rare anomalous write,
  surfaced in the familiar `claude agents` view, instead of a flat block.
- The security floor is unchanged: the hard deny is retained verbatim as the
  fallback, and the ask path fails closed.
- The change is confined to the review-session assembly and the guard; the OS
  sandbox, egress deny, and post guard are untouched.

### Negative / costs

- niwa now writes the user-global `~/.claude.json` (trust seeding), a new side effect
  with a narrow concurrent-write window against the Claude daemon.
- The review-session settings gain a second posture, so `ApplyReviewSettings` and the
  guard carry a mode parameter and two code paths.
- The live adversarial gate grows a surfacing assertion that depends on
  `claude agents --json` output shape.

### Mitigations

- The trust write is an atomic, key-preserving merge; the entry is removed on
  destroy; and a lost seed degrades to a re-stageable session, not a fail-open.
- The hard-deny posture is kept byte-for-byte and covered by its existing tests, so
  the second path does not disturb the floor.
- The surfacing assertion tolerates absence defensively and the whole gate stays
  opt-in (`NIWA_WATCH_LIVE_TEST=1`), never a false pass.

## References

- `docs/designs/current/DESIGN-niwa-watch-once-pr-review.md` -- the shipped
  watch-once containment this refines.
- `docs/prds/PRD-niwa-watch-once-pr-review.md` -- the parent requirements.
- Issue [#201](https://github.com/tsukumogami/niwa/issues/201) -- the operator-approval
  request and its Step-1 feasibility spike (PROCEED).
- PR #198 -- the shipped hard-deny this makes the fallback floor.
