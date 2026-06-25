# Lead: Is fixing #171 and #172 one unifying redesign or two independent patches — dependency order, minimal-correct vs ideal end state?

## Findings

### The two bugs sit at different layers and do not share a root cause

- **#171 is a guard-signal bug.** `sessionStartGuardPasses` (internal/cli/instance_from_hook.go:248) gates provisioning on `isBackgroundWorker`, which reads `~/.claude/jobs/<id>/state.json` and requires `template == "bg"` (instance_from_hook.go:281-296, const at :76). The scope doc establishes `template` is the launch agent/profile, not a fg/bg flag — so a default-agent background worker carries `template: "claude"` and is silently skipped. This is purely about **which sessions get an instance**. The fix lives entirely inside `isBackgroundWorker` / the guard: swap the signal (read `sessionKind` from the transcript) or drop the discriminator and rely on master-switch + re-entrancy.

- **#172 is a delivery-model bug.** Even when an instance IS provisioned, the activation model is "inject `additionalContext` text + tell the agent to `cd`" (`buildSessionStartInjection`, instance_from_hook.go:314-340). The parallel feasibility agent (wip/research/..._r1_lead-hook-reroot-feasibility.md) settled the load-bearing question **decisively: a SessionStart hook cannot re-root settings resolution.** Claude resolves `.claude/settings.json`, `enabledPlugins`, `extraKnownMarketplaces`, `env`, and `hooks` at LAUNCH from the launch cwd, with **no parent-directory fallback for project settings** and **no hook output field that reloads them**. A mid-session `cd` moves only Bash's cwd. So the instance's settings.json/plugins/hooks/env never reach the session — the injected text (path + CLAUDE.md body + cd instruction) is the ceiling of what cd+inject can deliver.

- **#172 has a second, independent half: the root scaffold drops Plugins/Marketplaces.** `writeRootSettings` (internal/workspace/root_materializer.go:112-130) calls `buildSettingsDoc` with only `Settings` (permission posture) + `SessionHooks`; it never passes `Plugins` or `Marketplaces`. By contrast the instance/repo path DOES emit them: `buildSettingsDoc` writes `enabledPlugins` from `cfg.Plugins` (apply.go:528-533) and `extraKnownMarketplaces` from `cfg.Marketplaces` (apply.go:537-553). So even the root config the root-launched session DOES load is missing plugins/marketplaces. This half is a genuine forwarding bug, fixable in isolation.

- **A design/code contradiction reinforces #172.** Decision 4 (DESIGN:191-201) claims the instance path "is also exported via the existing `NIWA_INSTANCE_ROOT` convention." The actual `buildSessionStartInjection` exports **no env at all** — there is no `NIWA_INSTANCE_ROOT`, no `CLAUDE_ENV_FILE` write, only additionalContext prose. The design overstated what cd+inject delivers; the code is honest about the ceiling.

### Dependency between the two bugs

They are **orthogonal, not ordered** — but their *value* is coupled asymmetrically:

- **Fixing #171 alone** (right sessions provisioned) without #172 leaves the feature broken-in-place: every dispatched worker now gets an instance, `cd`s into it, and operates with a working tree that is isolated for **file/branch collisions** (the PRD's primary R1 goal — "no shared working tree") but **without the instance's skills/plugins/hooks/env/settings**. So #171-only buys the core isolation win but not the full managed-instance experience.

- **Fixing #172 alone** (better delivery) without #171 is nearly worthless: the wrong sessions still get skipped, so there is no instance to deliver anything into for the common default-agent case.

So if only one ships, **#171 must be first** — it is the gate that makes any instance exist. #172 is a quality improvement on top. They do not have to ship together for the file-isolation value, but the feature only fully matches its PRD contract (R3, R7 — instance context/skills/permissions reach the session) when both are addressed.

### Does "relaunch in instance" dissolve both at once? — No, because it is not hook-reachable

The tempting unifying redesign (#172 Option B: don't cd, **relaunch the worker rooted INSIDE the instance** so settings.json/plugins/hooks/env all resolve natively) WOULD, if feasible, collapse both bugs: a worker that starts inside the instance trivially satisfies #171's re-entrancy no-op (it IS inside an instance) and #172's delivery (settings resolve from the instance cwd). **But the feasibility agent's answer is no.** A SessionStart hook fires *after* launch, after settings are resolved; it cannot change the session's cwd persistently nor force a settings re-resolution. The only place cwd-at-launch can be set is the **dispatch call** (CLI `--bg` in the right shell cwd, or Agent SDK `cwd` param / `--settings` / `--add-dir` flags) — which is upstream of niwa's hook entirely. niwa does not control the dispatch; the developer's `claude agents` fan-out does. So Option B is **not reachable from the hook surface this feature is built on.** The unifying redesign would require either a Claude Code feature that does not exist (mid-session re-root) or moving niwa's intervention point from the hook to the dispatch call (a different, larger architecture — pre-creating instances and dispatching agents pointed at them, inverting the "provision on SessionStart" premise the entire PRD/DESIGN/spike rests on).

### Candidate end-states

1. **Minimal patch pair (recommended near-term).**
   - #171: replace the `template == "bg"` read with the reliable `sessionKind: "bg"` transcript signal, OR drop the discriminator and lean on master-switch + re-entrancy (Decision 3 option (b), originally rejected for "spurious coordinator instances" but the reaper now bounds that cost).
   - #172: hoist `Plugins`/`Marketplaces` into `writeRootSettings` (#172 Option A) so the root config the session loads is at least complete.
   - **What stays broken:** instance-specific settings.json, instance-relative marketplaces (`directory`/`path` sources have no root-stable form), instance hooks, and env still never reach the session, because cd+inject is still the activation model and the session is still rooted at the workspace root. This pair restores correct *targeting* and *file-tree isolation* and improves root config completeness, but does NOT deliver per-instance managed config. It is honest minimal-correct, not ideal.

2. **Unifying redesign (ideal end-state, but blocked).** Move provisioning from SessionStart-hook to dispatch-time so the worker launches rooted in the instance. Dissolves both bugs at the root. **New machinery:** instances pre-created (or created synchronously before dispatch), a dispatch wrapper that sets cwd/`--settings`/`--add-dir`, and a rethink of the SessionStart guard (it would shift from "should I provision?" to "am I already in my instance? then no-op"). This is a different feature, not a patch — and depends on a dispatch-control surface niwa does not currently own. **Contingent on the relaunch-feasibility answer, which came back negative for the hook path.**

3. **Hybrid (pragmatic middle).** Ship the minimal patch pair now to make the feature correct-if-limited, and separately open a design question on whether niwa should own a dispatch wrapper (the only path to full config delivery). The hybrid acknowledges cd+inject is a known ceiling, not a bug to be patched away.

## Implications

- **Artifact-type recommendation:** Produce **(a) two bug-fix issues for /work-on, PLUS (c) a short decision record** — not a full design doc, not a spike.
  - #171 is a clean, well-scoped bug fix (swap one signal in one function + the guard test matrix). One issue. No design needed.
  - #172's **root-scaffold half** (hoist Plugins/Marketplaces) is also a clean bug fix. One issue.
  - #172's **activation-model half** is NOT a bug — the feasibility agent proved cd+inject's limits are architectural, not a defect. The real question "do we accept cd+inject as the ceiling (Option A, file-isolation only) or invest in dispatch-time relaunch (Option B, full delivery, new machinery)?" is a single, contested, partly-irreversible choice. That is exactly a **decision record** ("cd+inject-as-ceiling vs relaunch-at-dispatch"), feeding a future design if the answer is "relaunch."
  - **Why not a spike:** the feasibility unknown is already resolved — the parallel agent confirmed hook-relaunch is impossible and dispatch-time-cwd is the only lever. There is nothing left to prototype; the open question is a *decision* (accept the ceiling vs build the dispatch surface), not a *feasibility probe*.
  - **Why not a full design doc now:** the two issues are small and independently shippable; gating them behind a design doc for the activation model would delay the file-isolation fix (the PRD's primary value) for a decision that can be recorded separately.

- **Contingency (now resolved):** Had the feasibility agent found hook-driven relaunch feasible, the recommendation would flip toward a design doc, because #171 and #172 would genuinely be one redesign and the guard logic would need rework. Since relaunch is NOT hook-reachable, the bugs decouple and the minimal-patch-pair + decision-record routing holds.

## Surprises

- **The DESIGN already confessed both soft spots and shipped anyway.** Decision 3 calls the `template` read "a stability risk if Claude Code changes the format" and Decision 4 admits `cd` "does not re-root." The issues are not new discoveries — they are the documented risks materializing. Consequences section (DESIGN:397-402) even bounds the guard-misfire blast radius. The feature was knowingly shipped on two acknowledged-fragile decisions.
- **Decision 4's `NIWA_INSTANCE_ROOT` export claim is not implemented.** The design says the path is exported via env; `buildSessionStartInjection` emits no env. A reader trusting the design would over-estimate what reaches the session.
- **The guard's re-entrancy clause (instance_from_hook.go:266-270) is already the seed of Option B.** It no-ops when cwd is already inside an instance — exactly the state a relaunched-in-instance worker would be in. The current guard would "just work" under a dispatch-time-relaunch model, which is why Option B is structurally tempting even though it is not hook-reachable.

## Open Questions

- Is `sessionKind: "bg"` in the transcript stable and readable at SessionStart fire time without a race (transcript may not yet exist / be flushed)? This is the #171-fix's own feasibility detail — handed to the signals-availability lead.
- For #172 Option A, do instance-relative marketplace sources (`directory`/`path`) have ANY root-stable representation, or are they intrinsically undeliverable from the root? If intrinsically undeliverable, that strengthens the case that cd+inject can never be complete and the decision record should lean toward "build the dispatch surface eventually."
- Does niwa have any lever over the `claude agents` dispatch call (a wrapper, an alias, a documented launch convention)? This bounds whether Option B is ever reachable at all, or whether the feature is permanently capped at file-isolation-only.

## Summary
#171 (wrong guard signal) and #172 (cd+inject can't deliver instance config) are orthogonal bugs at different layers, not one root cause; #171 must land first because it is the gate that makes any instance exist, while #172 splits into a clean root-scaffold forwarding fix (hoist Plugins/Marketplaces) and an architectural ceiling that the parallel feasibility agent proved is NOT hook-fixable — hook-driven relaunch is impossible, dispatch-time cwd is the only lever and niwa doesn't own it. Recommended artifact: two bug-fix issues for /work-on (#171 signal swap; #172 root-scaffold hoist) plus one decision record for the genuinely contested "accept cd+inject as ceiling vs build a dispatch-time relaunch surface" choice — not a spike (feasibility already resolved) and not a full design doc (it would delay the primary file-isolation fix). The biggest open question is whether niwa can ever own the `claude agents` dispatch call, since that determines if the feature is permanently capped at file-tree isolation or can someday deliver full per-instance config.
