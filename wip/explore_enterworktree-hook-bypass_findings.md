# Explore findings: enterworktree-hook-bypass (niwa#221)

## Round 1 — direct reproduction on the live harness

Method: headless `claude -p` runs that call `EnterWorktree` and report the
verbatim result, against purpose-built fixtures; `git worktree list`, hook
stdin logs, and `niwa worktree list` inspected after each run. Claude Code
2.1.215 was installed locally from npm so the spike's exact version could be
retested.

### F1. The spike's root cause is falsified

The spike states that `EnterWorktree` decides native-vs-hook by an upward
git-repo discovery walk, and that inside a repo it "never consults the hook."
That does not reproduce. In a git repo carrying a `WorktreeCreate` hook in
`.claude/settings.local.json`, the hook fired and no native worktree was
created — on **2.1.215** (the version the spike tested) and on **2.1.220**
(current). Hook stdin carried the documented payload and the hook's stdout
path was honored.

There is therefore no harness regression, and the premise of #221 — "make
harness-support detection non-monotonic" — addresses a defect that does not
exist.

### F2. What actually produces the reported symptom

A session with **no `WorktreeCreate` hook in scope** produces exactly the
reported artifact: a worktree at `<repo>/.claude/worktrees/<name>` on branch
`worktree-<name>`, marked `locked`, invisible to niwa. That is the only
observed path to the symptom.

Hook *failure* does not produce it. A hook that exits non-zero makes
`EnterWorktree` fail loudly with the hook's stderr, on both 2.1.215 and
2.1.220 — there is no silent fallback to the native path.

### F3. The live defect: the hook is pinned to a version-specific binary path

`apply` writes the hook command from `os.Executable()`
(`internal/workspace/apply.go:1448`), which under a versioned install layout
resolves to a version-pinned path. Every repo in every instance on this
machine carries:

```
/home/dgazineu/.tsuku/tools/niwa-0.19.2/bin/niwa worktree from-hook
```

niwa 0.19.2 predates #207 (`fix(worktree): inherit promoted claude.env keys
from the clone`, first released in v0.20.0). So `from-hook` dies with:

```
niwa: error: installing content into worktree <sid>: materializer settings for
repo niwa: claude.env: promoted key "GH_TOKEN" not found in resolved env vars
```

Agent-initiated worktree creation is currently broken in every instance for
this reason. Repointing the same hook at niwa built from HEAD makes the whole
flow succeed end to end: a niwa worktree under `.niwa/worktrees/`, a session
record, visible to `niwa worktree list`.

Two distinct problems sit here. The promoted-key bug is already fixed
upstream. The **pinning** is not: upgrading niwa strands every previously
applied workspace on the old binary until each is re-applied, and nothing
detects or reports that.

### F4. `from-hook` leaves orphans when it fails mid-way

`from-hook` runs `git worktree add` and writes the session record *before*
content install. When content install fails, `EnterWorktree` reports failure
but the git worktree and an `active` session record both survive. Reproduced
twice; both needed manual `worktree destroy` + `git worktree prune`.

### F5. The spike's two validation questions, answered

- **Q1 — yes.** `permissions.deny: ["EnterWorktree","ExitWorktree"]` removes
  the tool from the session entirely, even under
  `defaultMode: "bypassPermissions"`. The deny+steer fallback works as designed.
- **Q2 — yes.** A `WorktreeCreate` hook in a **non-git** directory's
  `.claude/settings.json` fires when `EnterWorktree` runs with cwd there, and
  its stdout path is honored. Hook stdin `cwd` is that non-git directory —
  which means `from-hook`'s cwd-to-repo resolver has no repo to resolve, so a
  root install is not usable as-is.
- **Q3** is moot: no version in the range changed the behavior under test.

### F6. Latent plumbing gap

`ApplyToWorktree` builds `repoMaterializeInputs` without setting
`WorktreeDelegation` (`internal/workspace/worktree_content.go:522`), and
`WorktreeApplyOptions` has no field to carry one. A niwa worktree's own
`.claude/settings.local.json` therefore gets neither the hook nor the deny
entries. Not currently user-visible — delegation still fired from inside a
niwa worktree in testing, apparently resolved via the owning clone's settings
— but the worktree and clone configurations do drift.

### F7. A PATH-resolved hook command would not go stale

A probe hook run by `EnterWorktree` reports `niwa` present on its PATH,
resolving through the install manager's stable `current` shim rather than a
versioned directory. So writing the hook as a PATH-resolved `niwa worktree
from-hook` picks up whatever niwa the user currently has, with no re-apply
needed after an upgrade.

This is consistent with the shipped design's own threat model, which already
extends trust to PATH for the `claude` and `git` binaries it shells out to
(Security Considerations, "Version probe / trusted PATH"), and with the
existing inline `command -v <tool> >/dev/null 2>&1` guarded hook commands niwa
already emits. It is not a shim script, so Decision 6's "no shim script is
shipped" still holds.

The residual risk is an environment where niwa is installed off the hook
subprocess's PATH, which argues for preferring PATH and falling back to the
absolute path rather than replacing one with the other.

## Open question for the user

The correctness fix #221 asks for is aimed at a defect that does not exist,
while two reproducible defects (F3, F4) are not tracked anywhere. The issue,
and the merged spike it rests on, both need re-framing before any scoping.
