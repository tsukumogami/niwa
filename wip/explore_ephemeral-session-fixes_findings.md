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

## Accumulated Understanding

Fixing these is NOT one redesign. The hook surface the whole feature rests on is
provably incapable of re-rooting a session, so the only way to deliver per-instance
settings/plugins/hooks/env is to control the dispatch call's cwd — which lives upstream
of niwa, in the human's `claude agents` fan-out. That splits the work three ways:

1. **#171 — guard signal (must land first; gates everything).** Not a one-line swap.
   `template` is unstable in both directions; the fix needs a *correct, race-free* signal
   verified against the live Claude Code version. Leading candidate: read `backend ==
   "daemon"` from job state (after confirming interactive sessions don't carry it),
   possibly combined with the existing master-switch + re-entrancy gates, with the reaper
   as backstop for residual false positives. Dropping the check (Option B) is rejected.

2. **#172a — root-scaffold forwarding bug (clean, independently shippable).** Hoist
   `Plugins`/`Marketplaces` (and likely `env`/user hooks) into `writeRootSettings`.
   github-sourced marketplaces hoist cleanly; instance-relative `directory` sources must
   be filtered or deferred to root-scope `apply` (where a cloned instance and RepoIndex
   exist). This makes the *root* config the session loads complete, but does not deliver
   *instance* config.

3. **#172b — the activation-model ceiling (a decision, not a bug).** cd+inject can never
   deliver instance-specific settings.json/plugins/hooks. The real question — accept
   cd+inject + root-hoist as the ceiling (file-isolation only) vs. invest in a
   dispatch-time surface where niwa pre-creates instances and dispatches agents pointed
   at them (full delivery, inverts the "provision on SessionStart" premise) — is a single,
   contested, partly-irreversible architectural choice.

Recommended shape: two scoped bug-fix issues (#171 signal, #172a root-hoist) + one
decision record (cd+inject-ceiling vs. dispatch-time-relaunch) that can feed a future
design if the answer is "build the dispatch surface." Not a spike — feasibility is
already resolved. Not a full design doc up front — it would delay the primary
file-isolation fix.

One open contingency could still reshape this: if niwa already has (or could cheaply
add) a wrapper over the `claude agents` dispatch call, the dispatch-time fix moves from
"future design" toward "near-term," and #172b stops being a deferred decision.
