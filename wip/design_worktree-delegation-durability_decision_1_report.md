# Decision 1 — how the worktree hook resolves the niwa binary

## The question

The per-repo `WorktreeCreate`/`WorktreeRemove` hook command is written at apply
time from `os.Executable()` (`internal/workspace/apply.go:1447` →
`WorktreeDelegation.NiwaPath` → `worktreeFromHookCommand`,
`internal/workspace/materialize.go:317`). Under a versioned install layout that
absolute path is pinned to the release that ran `niwa apply`. After the user
upgrades niwa, every installed hook still invokes the old binary, and keeps
doing so until each workspace is re-applied. The observed failure is a hook
running a release that predates a bug fix and dying with
`claude.env: promoted key "GH_TOKEN" not found in resolved env vars` — agent
worktree creation broken in every instance on the machine.

So: how should the hook command name the binary so an installed hook keeps
working across upgrades?

Two facts constrain every option.

**`os.Executable()` cannot recover a stable symlink on Linux.** Go's Linux
implementation is `readlink("/proc/self/exe")`, which is fully symlink-resolved
by the kernel — the versioned path is all that survives. Darwin's is the
kernel-recorded exec path and is *not* resolved. So the same code yields
different answers per platform, and on Linux there is no way to get back to a
stable non-versioned path from inside the process.

**`from-hook` is location-independent.** `runSessionFromHook`
(`internal/cli/session_from_hook_cmd.go:74`) derives everything from the hook
payload's `cwd` (`workspace.ResolveRepoFromCwd`) — nothing is resolved relative
to the binary's own path. Any niwa binary, from any location, does the same
thing given the same stdin. Nothing about the current pinning is load-bearing.

## Options

### A. PATH-resolved bare command

Write `niwa worktree from-hook`. Drop `NiwaPath` from the emitted string.

*Pros.* Correct after every upgrade, with no re-apply and no new machinery. The
emitted command becomes machine-independent — byte-identical across versions,
hosts, and install layouts — which is the strongest possible form of R11
idempotency. It removes a field, a code path, and the `os.Executable()` error
branch rather than adding any. It matches how the design already treats `claude`
and `git`.

*Cons.* Hard-depends on the hook subprocess's PATH. It also inverts the drift
direction: the hook runs whatever `niwa` PATH names, which need not be the
binary that materialized the workspace.

*Concrete failure.* Claude Code launched from the macOS Dock (the desktop app,
not a terminal) inherits launchd's PATH — `/usr/bin:/bin:/usr/sbin:/sbin` — not
the interactive shell's. `install.sh` puts niwa in `~/.niwa/bin` and appends
that to PATH from a shell profile, which launchd never reads. Every
`EnterWorktree` in that session then fails loudly with `niwa: command not
found`, and because a non-zero `WorktreeCreate` hook has no silent fallback, the
agent cannot create a worktree at all. Today those users work; under A they
don't. This is a regression, not a wash.

### B. Keep the absolute path, add staleness detection

Record the applying niwa's version alongside the hook, and have `apply`, a
session hook, or `from-hook` itself compare and nudge a re-apply.

*Pros.* Nothing about binary resolution changes, so no new PATH exposure. It
also surfaces adjacent staleness the other options don't (a probe result
recorded under an older Claude, say).

*Cons.* It detects rather than fixes: the user still has a broken hook until
they act on the nudge, and the nudge lands in whatever output they weren't
reading. It adds state, a comparison, and a disclosure path to maintain.

*Concrete failure.* Detection has to run somewhere the *new* binary executes,
and in ephemeral-session mode it doesn't. The workspace-root `SessionStart` hook
is itself an absolute-path `niwa instance from-hook`
(`internal/workspace/root_materializer.go:70`, written by `writeRootSettings` at
:257) and it provisions instances *in-process*: `realProvisionInstance`
(`internal/cli/instance_from_hook.go:351`) builds a `workspace.NewApplier` and
applies. So the stale binary runs the apply, and that apply calls its own
`os.Executable()` and stamps its own stale path into every repo of the newly
created instance. Staleness is self-perpetuating along that path, and a
detector living inside the stale binary is the wrong version to know it's the
wrong version. B cannot close the loop without also fixing resolution.

### C. Guarded hybrid — prefer PATH, fall back to the recorded path

Emit `command -v niwa >/dev/null 2>&1 && exec niwa worktree from-hook; exec
'<abs>' worktree from-hook`.

*Pros.* Correct after upgrade in the normal case (PATH wins) and correct when
PATH is empty of niwa (the recorded path wins) — it strictly dominates both the
status quo and A on the set of situations that work. `command -v` +
`||`-guarded inline hook commands are already an established shape in this
codebase (`workSummaryHookCommand`, `materialize.go:436`; `prBodyHookCommand`,
:523), and `shell_init.go:180` already guards on `command -v niwa`. Same shell
semantics, same idempotency class as today.

*Cons.* Which binary actually ran is no longer readable off the settings file —
it depends on the invoking process's PATH. The command string still embeds a
machine-specific path, so it isn't byte-stable across hosts the way A is (still
R11-idempotent: deterministic given the inputs, no duplication on re-apply).
Marginally more shell to get right.

*Concrete failure.* A niwa contributor builds a branch, runs `./niwa apply`,
then asks an agent for a worktree — and the hook runs the *release* niwa from
PATH, not the branch build. Their `from-hook` change is silently not under test,
and a green manual check means nothing. The mitigation is the ordinary Go
workflow (install the branch build ahead on PATH), but the trap is real and it
bites exactly the people changing this code. Worth a sentence in the design's
Consequences.

### D. Absolute path to a stable non-versioned location

Resolve through a stable symlink instead of the versioned directory.

*Pros.* Would keep the pinned-path property while surviving upgrades.

*Cons/failure.* Not implementable from `os.Executable()`. On Linux
`/proc/self/exe` is already resolved past the symlink, so the stable path is
gone before niwa can read it; on Darwin it usually survives. Recovering it would
mean `exec.LookPath("niwa")` — which is option A with extra steps and the same
PATH dependency, only resolved at apply time instead of hook time, so it
*re-freezes* the answer and reacquires the staleness bug the moment the tool
manager repoints its shim. D is a non-option as stated.

## Recommendation: C, applied to both hook consumers

Emit the guarded hybrid. PATH resolution is what actually fixes the bug — a
user who upgrades niwa gets a working hook with no re-apply, which is the whole
point — and the recorded absolute path costs one clause to keep as the safety
net for GUI-launched harnesses and stripped-PATH environments, where A regresses
working setups into loud failures.

The security argument is a wash rather than a cost. The design's Security
Considerations already accept a trusted PATH for executing `claude` and `git`;
executing `niwa` from PATH is the same class of trust, and an attacker who can
prepend a PATH entry already controls the `git` niwa shells out to constantly
and can rewrite `settings.local.json` (mode 0600, user-owned) directly. The
Security Considerations bullet should be widened from "version probe" to cover
the hook command, not treated as a new boundary.

Apply the same shape to the workspace-root `SessionStart` hook
(`instanceFromHookCommand`). It has the identical defect and, per the failure
scenario in option B, it is the one that *propagates* staleness: fixing only the
per-repo hook leaves a stale root hook minting fresh instances full of stale
per-repo hooks. Both consumers are three lines apart in the same package and
should share one helper.

Not recommended as a companion: B's version-stamping. Once resolution is fixed,
the recorded path matters only in the fallback branch, and paying for state plus
a disclosure channel to report on a fallback is not worth it.

## Implementation notes

**Command shape.** Prefer `;` over `||` between the branches. With `A && exec B
|| exec C`, a failed `exec B` in a non-interactive shell terminates the shell
before `||` is evaluated, so the `||` reads as a fallback it isn't; `;` makes
the control flow honest and doesn't depend on that subtlety.

```
command -v niwa >/dev/null 2>&1 && exec niwa worktree from-hook; exec '/abs/niwa' worktree from-hook
```

**Quote the absolute path.** Hook commands go through a shell, so today's
unquoted `filepath.ToSlash(niwaPath) + " worktree from-hook"` already breaks on
an install path containing a space (`/Users/x/My Tools/niwa`) — a latent bug
that composing a longer shell line makes more visible. Single-quote the path and
escape embedded single quotes the standard way (`'` → `'\''`).

**Files.**

- `internal/workspace/materialize.go` — replace `worktreeFromHookCommand`
  (:317) with a shared `guardedNiwaHookCommand(niwaPath, suffix string) string`;
  keep `worktreeFromHookCommandSuffix` (:289) as its argument. Update the
  `WorktreeDelegation.NiwaPath` doc comment (:308) — the path is now the
  fallback, not the primary.
- `internal/workspace/root_materializer.go` — `instanceFromHookCommand` (:70)
  calls the same helper with `instanceFromHookCommandSuffix`. Same package, no
  new file needed.
- `internal/workspace/apply.go:1447-1459` — the `os.Executable()` error branch
  currently downgrades to the deny fallback. With a PATH-first command that is
  now over-strict: emit the PATH-only form (`command -v niwa >/dev/null 2>&1 ||
  exit 1; exec niwa worktree from-hook`) and keep the deferred warning. Keeping
  the existing deny behavior is defensible too (the branch is near-unreachable —
  it needs the binary deleted mid-run); either way, state which, because the
  current comment's premise ("we cannot write a valid hook command") stops
  holding.

**Tests that change.**

- `internal/workspace/materialize_worktree_test.go:87` asserts the exact string
  `"/usr/local/bin/niwa worktree from-hook"`, and :99 asserts
  `filepath.IsAbs(TrimSuffix(cmd, " worktree from-hook"))`. Both need the new
  shape. The `HasSuffix(cmd, "worktree from-hook")` checks at :96 still pass —
  the guarded command ends with the suffix.
- `internal/workspace/root_materializer_test.go:99` (`HasSuffix`) and
  `internal/cli/init_test.go:791` / `materialize_sessionhooks_test.go:99`
  (`Contains "instance from-hook"`) all survive unchanged.
- `test/functional/features/worktree-delegation.feature:94` and `:115` assert
  contains/does-not-contain `"worktree from-hook"` — both survive.
- Worth adding: a unit test that the emitted command contains both branches and
  that a niwa path containing a space or a quote round-trips through the shell
  correctly.

**Design doc edits.** Decision 6's "No shim script is shipped (the hook command
invokes the niwa binary directly)" stays true — a guarded inline command is not
a shim file — but the sentence should say the command prefers PATH with a
recorded-path fallback. Decision 1's "with no separate shim file and no PATH
dependency" is the clause that becomes wrong and must be rewritten. Add the
upgrade-durability rationale to Consequences (including the contributor trap
above), and widen the Security Considerations "Version probe / trusted PATH"
bullet to cover hook-command resolution.
