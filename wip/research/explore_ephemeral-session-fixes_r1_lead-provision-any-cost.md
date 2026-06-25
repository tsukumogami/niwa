# Lead: Real cost and correctness of #171 "Option B" — provision an instance for ANY root session in ephemeral mode, relying only on the master switch + re-entrancy no-op

## Findings

### 1. Cost of a spurious provision (create) — a FULL clone of every repo

The provision path is `realProvisionInstance` (internal/cli/instance_from_hook.go:364) → `applier.Create` (internal/workspace/apply.go:268). Cost trace:

- `Create` makes the instance dir, refreshes the team-config snapshot, then calls `runPipeline` (apply.go:328).
- `runPipeline` discovers all workspace repos and clones each one into the instance. The clone fan-out is at apply.go:1189-1236: it builds one `cloneJob` per classified repo and runs them through `cloneWorker` (apply.go:2040) with `cloneWorkers = 8` parallelism (apply.go:206).
- Each `cloneWorker` calls `Cloner.CloneWithBranch(... CloneOptions{}, ...)` (apply.go:2043). `CloneOptions{}` has **no `Depth`**, so `CloneWith` (clone.go:43) runs a plain `git clone url targetDir` with **no `--depth`** (clone.go:54-61). **This is a full (non-shallow) git clone of every repo's entire history.**

So a single spurious provision = a full clone of *every repo in the workspace* (8 at a time), plus the rest of the apply pipeline (config snapshot sync, vault resolution, CLAUDE.md/content materialization, settings, state write). For this workspace that is ~10 public repos; for a large org-discovered workspace it is however many repos `ListRepos` returns up to `DefaultMaxRepos`. The DESIGN and PRD both already accept this as the per-session cost and call it out explicitly (DESIGN Consequences: "Instance build cost (a full clone per session) is unchanged and accepted; fan-out of N agents is N clones"; PRD Known Limitations: "Instance build cost is real (a full clone per session)").

This matters because the spurious-provision cost is **not** cheap. It is the same heavy operation as a real worker provision — there is no lightweight/COW/shallow path. Every wrong provision is a full N-repo clone on disk and over the network.

### 2. Cost of the reap (destroy)

`realDestroyInstance` (instance_from_hook.go:414) → `workspace.DestroyInstance` (destroy.go:162): validates the dir, prunes Claude plugin records, then `os.RemoveAll(dir)` (destroy.go:175). Destroy is cheap relative to create (a recursive delete + a registry rewrite), but it only ever runs *after* the create already paid the full clone cost. The waste is front-loaded in the clone, not the reap.

### 3. Which sessions WRONGLY get an instance under Option B

Option B drops guard step (2) (`isBackgroundWorker` / `template == "bg"`) and keeps only step (1) master switch + step (3) re-entrancy no-op (`sessionStartGuardPasses`, instance_from_hook.go:248-273). In an ephemeral-mode workspace, provisioning then fires for **every** SessionStart whose `cwd` classifies to the workspace root and does not already resolve inside a real instance:

- **The coordinator session** (the one that runs `claude agents`): launches AT the workspace root. Re-entrancy does NOT spare it (see §4). It gets a full throwaway clone + a `cd`-into-instance injection. This is exactly the waste the PRD's R6 and Decision 3 reject.
- **Any interactive developer session opened at the workspace root** (inspecting/editing the workspace itself): same — full clone + cd injection. This is precisely the PRD user story "As a developer opening an ordinary session at the root, I want it left alone" and the AC "Launching `claude agents` at the root ... does not create an ephemeral instance."

Both wrongly-provisioned sessions get the SessionStart additionalContext telling the agent to `cd` into the throwaway instance (buildSessionStartInjection, instance_from_hook.go:314), so the behavior is not silent — the coordinator/dev session is actively redirected into a clone.

### 4. Re-entrancy no-op does NOT spare the coordinator

`sessionStartGuardPasses` step (3) (instance_from_hook.go:266-270): it calls `workspace.DiscoverInstance(cwd)` and no-ops only if the discovered dir passes `ValidateInstanceDir`. `DiscoverInstance` (state.go:343) walks **up** from cwd looking for any `.niwa/instance.json`. The workspace root itself carries a `.niwa/instance.json` (the root state file holding the ephemeral-mode flag), so `DiscoverInstance` succeeds at the root — but it returns the root dir, and `ValidateInstanceDir` **rejects** a workspace root (the root also carries `.niwa/workspace.toml`; the comment at instance_from_hook.go:260-265 spells this out). So the re-entrancy guard is designed to fire only when cwd is *inside a genuine child instance*. The coordinator launches AT the root, not inside an instance, so re-entrancy returns "not inside an instance" → the guard passes → **the coordinator always gets a spurious instance under Option B.** Re-entrancy only protects nested sub-dispatch (a worker that itself dispatches), never the root coordinator.

### 5. Orphan lifetime — the reaper does NOT promptly reclaim the coordinator's spurious instance

This is the sharp correctness cost of Option B, beyond mere waste. The reaper only destroys an ephemeral instance whose backing session is **dead** by `sessionLive` (reap.go:133, job_state.go:86). `sessionLive` returns true (spare it) while the job-state `state` is non-terminal and `updatedAt` is within `jobLivenessTTL = 30 * time.Minute` (job_state.go:18, 102-107).

The coordinator is a **live, long-running interactive session** — its job state stays non-terminal and freshly updated for the entire time the developer works. So under Option B the coordinator's spurious instance is **NOT reaped while the coordinator runs**; it lingers for the whole coordinator lifetime and is reclaimed only after the coordinator exits (job entry gone / terminal) AND a `reap` sweep runs (on demand, or opportunistically at the next `niwa create`, reap.go:181). For a developer who keeps a root session open all day, that is an all-day full-clone orphan per root session. Spurious *worker* misfires (if any) self-bound quickly because they die; the *coordinator* misfire does not — it is the worst-lived orphan precisely because the session is healthy.

### 6. Is Decision 3's original rejection still sound?

DESIGN Decision 3 (lines 159-189) rejected option (b) — "provision for every root session including the coordinator -- rejected, spurious coordinator instances are the exact waste the PRD calls out" — and chose the three-part guard whose load-bearing precision came from `template == "bg"`. PRD R6 ("a guard prevents an ordinary or coordinator session from being turned into an ephemeral instance") and R12 (opt-out) plus the AC "Launching `claude agents` at the root ... does not create an ephemeral instance" make "coordinator left alone" a **hard requirement**, not a nicety.

The #171 finding is that the precise guard is *broken in the other direction*: `template` is the launch agent/profile, not a fg/bg flag, so genuine bg workers launched with the default agent are silently **skipped** (false negatives). Option B "fixes" the false-negative by removing the signal — but it does so by reintroducing exactly the false-positive (coordinator provisioning) that Decision 3 and PRD R6 explicitly forbid. So the rejection is **still sound against the requirements**: Option B trades a "misses some workers" bug for a "violates R6 / the coordinator AC" bug, and the coordinator false-positive is the more expensive failure (§5: it is the longest-lived orphan). #171 does not change the calculus in Option B's favor — it shows the *signal* (`template`) was wrong, not that the *requirement* (don't provision the coordinator) was wrong. That points to Option A (find a correct fg/bg signal) over Option B.

## Implications

- Option B is not "cheap waste, reaper cleans up." The waste is a full N-repo clone per spurious provision (§1), and the coordinator orphan is **not** promptly reaped because the coordinator session stays live (§5). The reaper's liveness rule, which correctly spares live workers, also spares the live coordinator's mistaken instance.
- Option B directly violates PRD R6 and the coordinator AC, which are stated as hard requirements with an acceptance check. Shipping it would knowingly fail an existing acceptance criterion.
- The cost asymmetry favors fixing the *signal* (Option A): a correct fg/bg discriminator removes both the false-negative (#171's bug) and the false-positive (coordinator), without weakening R6.

## Surprises

- The clone is **full, not shallow** — `CloneOptions{}` carries no `Depth`, so every instance is a complete-history clone of every repo (apply.go:2043, clone.go:54-61). The "spurious provision" cost is therefore the maximum, not a trimmed clone.
- The re-entrancy guard *looks* like it might spare the coordinator (the root has a `.niwa/instance.json`), but `ValidateInstanceDir` deliberately rejects the root, so re-entrancy provably never spares a root-launched coordinator (instance_from_hook.go:260-270). The guard authors anticipated and excluded exactly this case.
- The reaper makes Option B *worse* for the coordinator than naive intuition: because liveness keys on job-state freshness, a healthy coordinator's mistaken instance is the **least** reapable orphan, lingering for the whole coordinator session.

## Open Questions

- Does an interactive/coordinator session reliably have a `~/.claude/jobs/<id>/state.json` at all? Decision 3 asserts interactive sessions carry `template: "claude"` (implying the file exists), but #171 reframes `template` as the launch profile. If a correct fg/bg field exists *in that same file*, Option A is straightforward; if the file is absent for some interactive sessions, both detection approaches need a fallback. (Needs the actual job-state schema, not assumed.)
- Middle paths worth evaluating (from the code): (a) keep the master switch + re-entrancy but add a *different* exclude-the-coordinator signal — e.g. the coordinator is the session that *issues* `claude agents`, so a marker niwa sets just before dispatch, but Decision 3 option (c) already rejected env markers because dispatched workers inherit the coordinator's env (env set before `claude agents` reaches both); (b) provision **lazily** — not at SessionStart but on first real work (e.g. first UserPromptSubmit or first write), so a coordinator that only dispatches and never does root file work never triggers a clone; the SessionStart stdin (DESIGN Decision 5) lacks a topic but a later `UserPromptSubmit` hook does fire, which is the same hook family. This is not in scope of the current code but is the most promising "provision-any-but-not-the-coordinator" lever because the coordinator's *behavior* (dispatch-only) differs from a worker's (does work), even when their SessionStart metadata is identical.
- Could the reaper be made to reclaim a *live-but-idle root-launched* instance under Option B? No safe way found: the reaper cannot distinguish "live coordinator that was mistakenly provisioned" from "live legitimate worker" without the very fg/bg signal Option B discards — confirming the cost lands at provision time, not reap time.

## Summary

A spurious provision under Option B costs a FULL (non-shallow) git clone of every repo in the workspace (apply.go:2043 + clone.go has no Depth), and under Option B the coordinator session always gets one because the re-entrancy guard provably never spares a root-launched session (ValidateInstanceDir rejects the root). The decisive correctness cost is that the reaper keys on job-state liveness, so a healthy long-running coordinator's mistaken instance is NOT reclaimed until the coordinator exits — making it the longest-lived orphan and a direct violation of PRD R6 and the coordinator acceptance criterion, so Decision 3's rejection of "provision every root session" remains sound. The biggest open question is whether the same job-state file carries a *correct* fg/bg field (favoring Option A), since #171 shows `template` was the wrong signal but not that the don't-provision-the-coordinator requirement was wrong — with lazy/on-first-work provisioning as the most promising middle path.
