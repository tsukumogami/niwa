# Exploration Findings: ephemeral-session-fixes

## Core Question

What does fixing #171 and #172 look like — two independent patches or two symptoms
of one flaw in the cd+inject activation model? Is a unifying "relaunch in the
instance" redesign reachable from the SessionStart hook?

## Round 1

### Key Insights

- **The hook cannot re-root a session — this is the load-bearing finding.**
  (lead-hook-reroot-feasibility) Claude Code resolves `.claude/settings.json`,
  `enabledPlugins`, `extraKnownMarketplaces`, `env`, and `hooks` at LAUNCH from the
  launch cwd, with NO parent-directory fallback for project settings and NO hook
  output field that reloads them. A SessionStart hook fires after settings are
  already resolved. The only lever for launch cwd is the **dispatch call** (CLI
  `--bg` in the right shell cwd, Agent SDK `cwd`, or `--settings`/`--add-dir`/
  `--plugin-dir` flags, v2.1.142+) — which is upstream of niwa's hook entirely.
  So the tempting unifying "relaunch in instance" redesign is NOT reachable from
  the hook surface this feature is built on.

- **The #171 guard signal is wrong in BOTH directions, and the correct one is
  racy.** (lead-sessionstart-signals) Job-state `template` has been observed
  carrying the agent name (false-negative: default-agent bg workers skipped — the
  #171 bug) AND stamped `"bg"` on every session including interactive (false-positive,
  anthropics/claude-code#59848). The semantically-correct marker `sessionKind:"bg"`
  lives in the transcript JSONL, not job state, and is genuinely racy at SessionStart
  because transcript writes are async-flushed after the hook fires (#56631). The only
  race-free job-state-resident candidate is the currently-unread `backend:"daemon"`
  field — but it is unverified whether interactive sessions also carry it.

- **#172's root-scaffold half is a clean, isolated forwarding bug.**
  (lead-root-scaffold-fields) `writeRootSettings` (root_materializer.go:112-130)
  computes the full effective config via `MergeInstanceOverrides` but forwards only
  `Settings`+`SessionHooks` to `buildSettingsDoc`, dropping the already-computed
  `Plugins`, `Marketplaces`, `env`, and user hooks. The instance path passes them;
  the same helper produces plugin-bearing output for instances and plugin-less output
  for the root purely because of which fields are passed. github-sourced marketplaces
  hoist cleanly (two field forwards); `repo:<name>/<path>` directory marketplaces have
  no root-stable path at init (they `os.Stat` a cloned-instance path that doesn't exist
  yet) and must be filtered or deferred.

- **#171 Option B (provision any root session) is costly and unsound.**
  (lead-provision-any-cost) A spurious provision is a FULL (non-shallow) git clone of
  every repo in the workspace. The coordinator session ALWAYS gets one because the
  re-entrancy guard provably never spares a root-launched session
  (`ValidateInstanceDir` rejects the workspace root). Worse, the reaper keys on
  job-state liveness with a 30-min TTL, so the coordinator's mistaken instance is the
  LONGEST-lived orphan — not reclaimed until the coordinator exits. DESIGN Decision 3's
  rejection of "provision every root session" remains sound; #171 shows the signal was
  wrong, NOT that the don't-provision-the-coordinator requirement (PRD R6) was wrong.

- **The two bugs are orthogonal, at different layers, and decouple cleanly.**
  (lead-unifying-vs-patches) #171 = which sessions get an instance (guard). #172 =
  whether having one helps (delivery). Their *value* is coupled asymmetrically:
  #171-alone buys the PRD's primary win (file/branch isolation — no shared working
  tree) but not the managed-instance experience; #172-alone is near-worthless because
  the wrong sessions are still skipped. So **#171 must land first**.

- **Partial hook-time levers exist for #172's env half.** (lead-hook-reroot-feasibility)
  `CLAUDE_ENV_FILE` lets a hook write env vars for subsequent Bash (could deliver the
  instance's `env` like `GH_TOKEN` to tools, though not to Claude's own settings
  resolution), and SessionStart output supports `reloadSkills: true` (re-scan skill
  dirs). Plugins/marketplaces/settings-hooks remain strictly launch-time.

- **Live probe on 2.1.191 (this machine — where #171 was observed) resolves the
  signal gap, and the answer is bad for the easy fix.** The jobs dir contains the
  exact sessions from #171's evidence (`c66f41d1`, `4f015f9e`). Findings:
  - `template`: `"bg"` for `c66f41d1`, `"claude"` for `4f015f9e`/`d3bd64ce` — confirms
    the #171 false-negative; it tracks the launch agent, not fg/bg.
  - `backend`: `"daemon"` for **every** session, including the `template:"claude"`
    workers. So `backend` does NOT discriminate on 2.1.191 — Lead 1's leading
    race-free candidate is dead on the live version.
  - `sessionKind:"bg"`: present in the transcript of all three dispatched workers
    (the correct signal), BUT the **first transcript record carries no `sessionKind`**
    (first record type is `mode`/`agent-setting`). So at SessionStart fire time the
    signal is not yet present — the race is real and reproduced.
  - Net: on 2.1.191 there is NO signal that is both correct and race-free at
    SessionStart. `template` wrong, `backend` non-discriminating, `sessionKind`
    correct-but-late. Softer separators that vary (`intent` = the dispatched task
    prompt, `respawnFlags`, `originCwd`) exist but none is a documented fg/bg flag.

### Tensions

- **Race-free-but-maybe-imprecise vs. correct-but-racy (the #171 signal choice).**
  `backend:"daemon"` is race-free (job state exists at dispatch) but may share #59848's
  false-positive risk. `sessionKind:"bg"` is semantically correct but unreadable
  reliably at SessionStart. The opt-in master switch + reaper already bound a misfire
  to wasted clones — which argues for the race-free signal accepting some false
  positives the reaper cleans up, EXCEPT that the worst false positive (the coordinator)
  is exactly the one the reaper can't reclaim while it's alive. So "let the reaper
  absorb it" is weaker than it looks for the coordinator case specifically.

- **The DESIGN claims an env export that the code does not implement.** Decision 4 says
  the instance path "is also exported via the existing `NIWA_INSTANCE_ROOT` convention";
  `buildSessionStartInjection` emits NO env at all — only additionalContext prose. The
  design overstated what cd+inject delivers.

- **Minimal-correct vs. ideal end state.** The minimal patch pair restores correct
  targeting + file isolation + complete *root* config, but instance-specific
  settings.json, instance-relative marketplaces, instance hooks, and instance env still
  never reach the session. cd+inject is a known architectural ceiling, not a bug that
  can be patched away.

### Gaps

- Which Claude Code version is the workspace actually on? Every signal observation is
  version-pinned to 2.1.142–2.1.177. The right #171 signal depends on the live version.
- Does an interactive foreground session ever carry `backend:"daemon"` post-2.1.139?
  Unverified; needs a live dogfood probe (the original spike's method).
- Is there a later, transcript-safe hook (UserPromptSubmit/PreToolUse) where
  `sessionKind` is reliably present, allowing the provisioning decision to defer without
  breaking the "instance ready before first work" contract?
- Does niwa have ANY lever over the `claude agents` dispatch call (a wrapper, alias, or
  documented launch convention)? This bounds whether full config delivery is ever
  reachable or the feature is permanently capped at file-isolation-only.

### Decisions

- **Eliminate the "unifying hook-relaunch redesign" as a near-term option** — proven not
  hook-reachable. Dispatch-time cwd is the only lever and niwa doesn't own it today.
- **Eliminate #171 Option B (drop the discriminator)** — full clone per session + an
  unreclaimable coordinator orphan; Decision 3's rejection still holds.

### User Focus

(pending convergence response)

## Round 2 (instance-rooted dispatch model)

Triggered by a live user test: `claude --bg "<prompt>"` launched from INSIDE an
instance dir (a) boots rooted in the instance (settings/plugins/hooks/env resolve
natively) AND (b) registers in Agent View (`claude agents`/`attach`/`logs`/`stop`).
So the instance-rooted dispatch model gives full fidelity AND unified management.

### Key Insights

- **`claude --bg` contract (lead C).** Detaches and returns immediately. NO `--json`
  id capture — must scrape `backgrounded · <short-id>` or read the jobs dir. Prompt is
  argv-only (no stdin/file). Honors `--settings`/`--add-dir`/`--plugin-dir`/`--model`/
  `--permission-mode`/`--agent` at launch, resolving settings from the launch cwd.
  SessionStart/SessionEnd hooks fire for `--bg` but resolve from the LAUNCH dir's
  settings, not a parent's.

- **The command is mostly assembly of existing parts (lead A).** Reuses
  `realProvisionInstance`→`applier.Create` (which also materializes `claude.env`/
  `GH_TOKEN` INTO the instance tree, so re-rooting cwd delivers env for free),
  `WriteSessionMapping`, `reapWorkspace`, and a generalized form of the existing
  `niwa session attach` supervisor (`sessionattach/supervise.go:44` already execs
  `claude` with `cmd.Dir` set — swap `--resume <id>` for `--bg "<prompt>"`). The one
  genuinely new building block is capturing a freshly-launched session's id. (Aside:
  the `--channels` string the user saw is NOT in this branch — it's a separate mesh
  launcher — so `session attach`, not a `--channels` wrapper, is the reuse surface.)

- **Teardown works under pre-creation, via the reaper (lead B).** niwa must key the
  durable mapping on the FULL session UUID, recovered from `~/.claude/jobs/<short-id>/
  state.json`'s `sessionId` (the mapping store rejects non-UUID keys) — and that same
  lookup IS the id-capture step lead A flagged. The workspace-root SessionEnd hook
  almost certainly does NOT fire for an instance-rooted session (instance settings omit
  the session hooks; resolution is from the instance), so teardown falls to `niwa reap`,
  which is self-sufficient and runs opportunistically on every `niwa create`. The live
  probe already shows finished bg sessions reach `state:"done"` (terminal), so the
  reaper's liveness check should trip — bounding the leak risk lead B raised.

- **REPLACE beats AUGMENT, by elimination (lead D).** Under REPLACE (retire hook
  auto-provisioning; the command is the only provisioning path), the entire SessionStart
  chain becomes dead code — `runInstanceHookStart`, `sessionStartGuardPasses`,
  `isBackgroundWorker`, `buildSessionStartInjection`, `realProvisionInstance` (all
  confirmed single-caller). **#171 evaporates completely** (the broken `template=="bg"`
  guard has exactly one caller; the reaper reads a different job-state field set,
  `State`/`UpdatedAt`, not `Template`). **#172's cd+inject half dies with it.** The
  reaper, mapping store, and the guard-free SessionEnd teardown all survive. AUGMENT
  cannot be made safe: round 1 proved there is NO race-free correct guard signal at
  SessionStart on 2.1.191 (and the live probe showed `backend:"daemon"` on every
  session, so it can't discriminate either), so any "best-effort net" can still misfire
  on the coordinator and orphan instances. The lack of a good guard signal pushes to
  REPLACE by elimination.

- **#172a is independent of the decision (lead D).** The root scaffold dropping
  Plugins/Marketplaces is a coordinator-fidelity fix that survives regardless of
  augment/replace and converges via the existing unconditional root-`apply` rewrite.

### Tensions

- **User leaned AUGMENT; the evidence pushes REPLACE.** The user's instinct was to keep
  the hook path as a best-effort net. But lead D + round 1 show "best-effort" is unsafe
  here because no guard signal is both correct and race-free, so the net orphans the
  coordinator. REPLACE is cleaner AND removes the unfixable bug rather than half-fixing
  it. This is the one place the recommendation diverges from the user's stated lean.

### Decisions

- **Adopt the instance-rooted dispatch model** as the blessed path (validated end-to-end
  by the user's `claude --bg`-from-instance test).
- **Recover the full UUID + capture the session id via the jobs dir**, not stdout
  scraping alone — one mechanism serves both id-capture and mapping-key needs.
- **Teardown = reaper-primary** (root SessionEnd hook won't fire for instance-rooted
  sessions).

### User Focus

The user validated the model empirically and proposed a niwa command wrapping
`claude --bg` (slug via `claude -p` if not provided, create instance, launch, capture
id, attach). They lean AUGMENT ("doesn't remove the need for 171/172, but makes partial
acceptable") — pending the REPLACE recommendation.

## Accumulated Understanding

The hook surface the feature rests on is provably incapable of re-rooting a session, so
per-instance settings/plugins/hooks/env can only be delivered by controlling the launch
cwd — and the live test settled HOW: `claude --bg` launched from inside an instance dir
boots rooted there (full fidelity) AND registers in Agent View (unified management). The
exploration therefore converged off "patch the two bugs" and onto a redesign:

**The blessed path is an instance-rooted dispatch command.** niwa (1) pre-creates an
ephemeral instance via the existing `applier.Create` (env materializes into the instance
tree for free), (2) launches `claude --bg "<prompt>"` with cwd = the instance, (3) reads
back the full session UUID from `~/.claude/jobs/<short-id>/state.json`, (4) writes the
session->instance mapping keyed on that UUID with `Ephemeral:true`. The worker resolves
the instance's settings/plugins/hooks/env natively at launch; teardown is the existing
`niwa reap` (the root SessionEnd hook does not fire for instance-rooted sessions, and
reap already runs opportunistically on `niwa create`). Most of this is assembly of
existing parts (`applier.Create`, `WriteSessionMapping`, `reapWorkspace`, a generalized
`session attach` supervisor); the one new piece is launch-and-capture-the-id.

**This REPLACES hook auto-provisioning, which dissolves the bugs instead of patching
them.** Retiring the SessionStart chain (`runInstanceHookStart`, the guard,
`isBackgroundWorker`, `buildSessionStartInjection` — all single-caller) makes **#171
evaporate** (the broken `template` guard has one caller and the reaper keys on different
fields) and kills **#172's cd+inject half**. AUGMENT (keep the hook as a best-effort net)
is rejected: round 1 proved there is no race-free correct guard signal at SessionStart on
2.1.191, so any net still misfires on the coordinator and orphans instances. REPLACE is
both cleaner and the only safe option.

**One fix survives the decision: #172a** (root scaffold drops Plugins/Marketplaces). The
workspace-ROOT coordinator session still wants its own plugins/marketplaces, so hoisting
them into `writeRootSettings` is an independent coordinator-fidelity fix, landing
regardless. github-sourced marketplaces hoist cleanly; instance-relative `directory`
sources need filtering or deferral to root-scope `apply`.

**Disposition of the issues under REPLACE:**
- **#171** — closed/mooted (retire the guard; nothing to fix).
- **#172** — cd+inject half mooted by REPLACE; root-scaffold half becomes #172a, a small
  standalone fix.

**Recommended artifact:** a DESIGN doc for the dispatch command (command interface; the
launch-and-capture-id mechanism + UUID recovery; reaper-primary teardown; what the
shipped PR #169 feature retires and the migration for already-`init`-ed workspaces;
whether to keep the SessionEnd hook at all; default/opt-in posture), with #172a tracked
as an independent fix. NOT two-bug-fixes-plus-decision-record (the round-1 shape) — the
decision is now made (build the dispatch surface) and the work is a real design, not a
patch. Open empirical confirmations for the design phase: reaper treats `state:"done"`
as dead (live probe suggests yes); `claude --bg` id-capture robustness; whether long
argv-only prompts need a workaround.
