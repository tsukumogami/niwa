# Security review: DESIGN-inert-defaultmode-key (phase 6)

Scope: the Security Considerations section of
`docs/designs/DESIGN-inert-defaultmode-key.md`, read against its Solution
Architecture and the PRD, with every load-bearing claim checked against the
code in this worktree. File:line references are to the tree as it stands
before the change.

## What the change does to the trust boundary

Today `runDispatch` decides whether to forward `--permission-mode
bypassPermissions` by reading `permissions.defaultMode` back out of
`<instance>/.claude/settings.json` (`internal/cli/dispatch.go:550-557`,
projection at `internal/cli/dispatch_plugins.go:198-204`). After the change it
reads a `claude_permissions` literal from `<instance>/.niwa/instance.json`,
written by `Create`/`Apply` from `MergeInstanceOverrides(effectiveCfg)`. The
argv builder stops reading the package global `dispatchPermissionMode`
(`dispatch.go:983`) and takes the mode as a parameter, and both watch launch
sites (`internal/cli/watch.go:576`, `watch.go:836`) pass `""`.

## Question 1: attack vectors not considered

### Writes to `.niwa/instance.json` between provisioning and the dispatch read

The design's argument is three-part: the dispatch process reads right after
its own process created the instance, the directory name is unpredictable,
and `Create` writes the state file whole.

Verified:
- `realProvisionInstance` calls `applier.Create` in-process
  (`internal/cli/instance_from_hook.go:501`), so "same process" holds.
- `Create` builds a fresh `InstanceState` literal and `SaveState` overwrites
  the file with `os.WriteFile` (`internal/workspace/apply.go:569-588`,
  `internal/workspace/state.go:280-297`). A value planted before `SaveState`
  is gone.
- Between `SaveState` and the planned `LoadState` in `runDispatch` there's
  only logging, the pending-marker write (`dispatch.go:506`), and model
  resolution. The window is short.
- The name suffix comes from `crypto/rand` (`dispatch.go:879-889`).

What the section misses is the set of writers that run *inside* the pipeline
and are handed the instance path. Repo setup scripts run at step 6.75
(`apply.go:1976-2004`), before `SaveState`, and receive the instance root
explicitly through `cloneSetupEnv(instanceRoot)` (`apply.go:1983-1984`). The
plugin prewarm (`apply.go:555-557`) also runs before `SaveState`. A setup
script that forks a detached child can wait for `instance.json` to be
rewritten and then overwrite it with `"claude_permissions": "bypass"` before
dispatch reads it. For that actor the "unpredictable name" argument doesn't
apply, and "written whole" doesn't help because the write lands after it.

This is not a regression. Under the current code the same script needs no
race at all: the instance-root `settings.json` is written at step 4.5
(`apply.go:1587-1597`), before setup scripts run, so a script can rewrite it
synchronously and turn the dispatch into a bypass launch. Repo setup code
also already runs as the developer, unsandboxed, so it has other routes to
the same end. The design narrows this path from "synchronous write" to "win a
millisecond race after `SaveState`". But the section should name the actor
and state why it's accepted, instead of implying that only outsiders who'd
have to guess the name can write the file.

A concurrent `niwa apply` run from the workspace root, which loops over every
instance including a just-created dispatch instance, can also rewrite the
file. `SaveState` isn't atomic (truncate-then-write, `state.go:292`), so the
worst case is a torn read, which the design turns into "warn, no bypass",
or a value from the same legitimate configuration. That fails closed.
Nothing to escalate.

### Can a workspace, overlay, or personal-overlay author get bypass they couldn't before?

No. The resolver's input is `MergeInstanceOverrides(effectiveCfg)`, which is
exactly what `RootSettingsMaterializer` builds the instance-root document from
(`internal/workspace/workspace_context.go:340`, `:432`). That materializer runs
unconditionally in the pipeline with `Config: effectiveCfg`
(`apply.go:1587-1597`). It has no agent gate and no early return, so there's no
configuration where the old path wrote no document (and forwarded no flag)
but the new resolver would record `bypass`. `MergeInstanceOverrides` ignores
`[repos.*]` (`internal/workspace/override.go:179-287`), matching the PRD's
per-repo rule. The workspace overlay is merged into `cfg` before this
(`apply.go:1191-1195`). The personal overlay is merged at workspace level by
`ResolveAndMergeEffectiveConfig` (`internal/workspace/effective_config.go:101-102`),
and only when `!skipGlobal` (`apply.go:938`), the same as today. Validation of
the instance-root value always runs before state is saved, because an error
from the root materializer aborts the pipeline and `Create` removes the
directory (`apply.go:524-527`).

So "who can obtain bypass doesn't change" is accurate. The section would be
more useful if it said who that set actually includes. The widest principals
are anyone who can push to the convention-discovered workspace overlay repo
(`<org>/<repo>-overlay`, auto-cloned silently when it's reachable,
`apply.go:1081-1107`) and anyone who can push to the developer's personal
overlay repo (synced at `apply.go:938-979`, and `[global.claude.settings]`
applies to every workspace). That's unchanged, but it's the real answer to
"who decides whether unattended workers run without prompts".

### Can a derived flag reach a `niwa watch` launch?

Not after the change, and the design makes it structural. Today safety rests
on nothing in a watch process setting the Cobra-bound global that
`buildDispatchPassthrough` reads (`dispatch.go:974-992`). Watch provisions
through the same `realProvisionInstance` (`watch.go:790`), so every review
instance's state will now also record `claude_permissions: "bypass"` in a
bypass workspace. The design guards the watch launch sites with an explicit
`""` and a test on a real `bypass` materialization. The remaining exposure
is a future reader. A resume, continuation, or `niwa list` feature that reads
`ClaudePermissions` from an existing instance would hand bypass to review
sessions. The design covers that with a code comment only.

The mode change under watch was also checked. The watch guards are
PreToolUse hooks that exit 2 (`internal/watch/containment.go:78-175`), so
they don't depend on the permission mode, and the section's claim that "the
sandboxed boundary doesn't depend on the mode" holds. Sandbox-off reviews,
the trusted path (`watch.go:57`), will run in the developer's own mode rather
than the accidental `default`. That's documented as a negative consequence
and consistent with that path being uncontained by definition.

### Can a vault-backed value leak through an error or state?

State: no, as long as the resolver returns only the canonical literals, which
the design specifies. An unresolved vault value (tolerant mode) becomes an
empty, marked `MaybeSecret` (`internal/config/maybesecret.go:110-135`). That
reads as `""`, which the resolver maps to "nothing" and the materializer
rejects, so it fails closed either way.

Errors: the current code does leak today. `buildSettingsDoc` returns
`fmt.Errorf("unknown permissions value %q", permStr)` with the revealed
plaintext (`internal/workspace/materialize.go:682-687`), and the pipeline's
redactor only scrubs `secret.Errorf`/`Wrap` paths (`apply.go:818-834`). The
design fixes this, with two gaps:
- The design says the vault-safe error "names the reference". After
  resolution the `vault://` URI is gone. The resolver replaces `Plain` with
  `Secret`/`Token` (`maybesecret.go:97-104`). The identifier that is
  available is `m.Secret.Origin()` (provider name and key), which
  `sourceForMaybeSecret` already uses (`materialize.go:1521-1536`). The
  discriminator should be `m.IsSecret()`.
- The two sibling keys in the same function echo revealed plaintext the same
  way: `remoteControlAtStartup` (`materialize.go:716-723`) and
  `keepAliveOnDispatch` (`materialize.go:732-739`). The design's rationale,
  that the redactor doesn't scrub plain `fmt.Errorf`, applies to them too.

## Question 2: are the mitigations enough?

Mostly yes.
- Tampering with and deleting the settings file: closed by construction. The
  tamper test in AC7 exercises it.
- Watch: the explicit parameter plus a test on a real `bypass`
  materialization is the right shape.
- Failure handling: a read or parse failure warns and withholds the flag,
  which fails closed. `LoadState` also refuses a newer schema
  (`state.go:271-275`), which falls into the same branch. One gap: the design
  says a *missing* state file degrades silently "only under test fakes". In
  production `Create` always saves before returning success
  (`apply.go:586-588`), so a missing file after a successful provision means
  something deleted it. That should warn, not stay silent.
- Vault: correct in intent. The error form needs the fix above.
- Future readers: a comment isn't a control. A cheap mechanical pin (see the
  findings) would make "trusted only right after provisioning" enforceable.

## Question 3: are any "unchanged" or "not applicable" claims wrong?

- "Who can obtain bypass doesn't change": correct (see above).
- "Dispatch reads the field immediately after its own process created the
  instance" and "Dispatch always provisions a fresh instance": correct
  (`dispatch.go:476`, `instance_from_hook.go:501`).
- "The instance directory has an unpredictable name": true for outsiders
  before the directory exists. Not true for in-pipeline code, which is handed
  the path, or for anyone who can list the workspace root once it exists.
  Incomplete rather than wrong.
- "Creating the instance writes the state file whole, so a value planted in
  advance is overwritten": true, but it says nothing about writes after
  `SaveState` from children of in-pipeline code.
- "An unknown value already fails before its document is written": correct
  (`materialize.go:682-687` returns before `applyPlan`).
- "For a vault-backed value, [the error] names the reference": not feasible
  as written. The reference isn't retained after resolution. Use the origin.
- "The sandboxed boundary doesn't depend on the mode": correct. The hooks exit
  2 and the OS sandbox is mode-independent.
- "Codex is untouched": correct, provided the
  `flags.PermissionMode == "--permission-mode"` gate is kept (Codex's slot is
  spelled `--sandbox`, per `dispatch.go:543-547`).
- "In an `ask` workspace, the instance-root document used to carry a mode
  Claude Code rejects, which made it discard the whole file, including the
  containment settings watch merges into it": correct, and understated. See
  the next question.

## Question 4: residual risk to escalate

One item should be escalated rather than only documented. It's pre-existing,
and this design fixes it as a side effect.

Today, in any `ask` workspace, a sandboxed `niwa watch` review that falls back
to the hard-deny posture runs uncontained. The fallback happens whenever
`resolveAskPosture` returns false, for example when trust can't be seeded or
the instance lives under `~/.claude` (`watch.go:921-944`).
`ApplyReviewSettings` preserves the existing `permissions` block and only
overwrites `defaultMode` in the ask posture (`containment.go:201-228`), so
`askPermissions` stays in the merged file. `VerifyReviewSettings`
(`containment.go:290-350`) never checks `defaultMode` in hard-deny, so
verification passes. Claude Code then discards the whole file, including the
no-egress sandbox stanza, `failIfUnavailable`, and the egress-deny and
filesystem-guard hooks, while the review reads untrusted PR code. The design
fixes the root cause in Phase 3 and defers "reject any mode Claude Code
doesn't recognize" to a follow-up. Given that a single bad value silently
drops the whole containment boundary, the verification check belongs in this
change: reject any `defaultMode` outside `default`, `acceptEdits`, `plan`, and
`dontAsk`. It's a few lines and compatible with R13, since all three
materialized postures would still pass. The live exposure should also be
tracked as a security issue now, independent of when Phase 3 lands.

The setup-script race doesn't need escalation. Repo setup code already runs
arbitrary commands as the developer, and the design shrinks the window it
had.

## Verdict

PASS

## Findings

1. **minor**: The Security Considerations section's state-file reasoning
   leaves out in-pipeline writers. Repo setup scripts (`apply.go:1976-2004`,
   given the instance root through `cloneSetupEnv`) and the plugin prewarm
   (`apply.go:555`) run inside `Create` before `SaveState`
   (`apply.go:586`). A detached child can overwrite `instance.json` after
   `SaveState` and before dispatch reads it.
   *Fix:* Name this actor in the section. Say it's accepted because repo
   setup code already runs with the developer's privileges, and that the
   design shrinks its route from a synchronous `settings.json` rewrite to a
   post-save race. Optional hardening: have `Create` return the posture it
   just recorded, and have `realProvisionInstance` carry it out alongside the
   disk read, with dispatch refusing the derived flag when the two disagree.

2. **minor**: A vault-backed invalid value can't produce an error that "names
   the reference". After resolution `MaybeSecret.Plain` no longer holds the
   URI (`maybesecret.go:97-104`). The sibling keys `remoteControlAtStartup`
   and `keepAliveOnDispatch` echo revealed plaintext through plain
   `fmt.Errorf` the same way (`materialize.go:716-739`).
   *Fix:* Branch on `m.IsSecret()`. For a secret, name the config key and
   `m.Secret.Origin()` (provider and key), never the value. Apply the same
   form to the two sibling errors in Phase 3, and add a test per key that a
   vault-backed invalid value's plaintext is absent from the error.

3. **minor (escalate)**: In `ask` workspaces, sandboxed watch reviews that fall
   back to hard-deny currently run with their containment silently discarded.
   `askPermissions` survives `ApplyReviewSettings` (`containment.go:221-228`),
   and `VerifyReviewSettings` never checks `defaultMode` in hard-deny
   (`containment.go:290-350`).
   *Fix:* File a security issue for the live exposure now. Pull the
   follow-up into this change: have `VerifyReviewSettings` reject any present
   `permissions.defaultMode` outside `default`, `acceptEdits`, `plan`, and
   `dontAsk` in every posture, so a future bad value fails the launch instead
   of dropping containment.

4. **minor**: A missing `instance.json` after a successful provision is
   described as happening "only under test fakes" and degrades silently. In
   production it means something deleted the file.
   *Fix:* Warn in both the unreadable and the missing cases. Tests that use
   fake provisioners can assert the warning or ignore stderr.

5. **minor**: "Trusted only for an instance the calling process just
   provisioned" is enforced only by a comment, and every watch-review
   instance will now carry `claude_permissions: "bypass"` in a bypass
   workspace.
   *Fix:* Add a mechanical pin next to AC17. For example, have a test or
   `git grep` assert that `ClaudePermissions` is read only in `runDispatch`'s
   post-provision block. Also add a unit assertion that `Create` never copies
   it from the workspace-root `initState` (`apply.go:477-513`).

6. **minor (doc only)**: "Who can obtain bypass doesn't change" is accurate
   but doesn't say who that set includes.
   *Fix:* Name the widest principals: anyone with push access to the
   convention-discovered `<org>/<repo>-overlay` repo, which is auto-cloned
   silently (`apply.go:1081-1107`), and to the developer's personal-overlay
   repo, whose `[global.claude.settings]` applies to every workspace. Readers
   can then see who actually decides whether dispatched workers run without
   prompts.
