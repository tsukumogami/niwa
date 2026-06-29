# Lead: Is there evidence of real demand for remote-control-by-default on dispatch?

## Findings

### Q1. Is demand real? — **Absent**

I searched the full issue list (all states, 100 most recent via `gh issue list --repo tsukumogami/niwa --state all`), keyword-searched issues for "remote control", "Claude Code Remote", "remote-control", "mobile", "Agent View", "claude.ai", and "steer", and grepped the entire repo (`--include=*.go --include=*.md --include=*.toml --include=*.json`). No issue, comment, or maintainer statement requests enabling Claude Code Remote / remote-control on `niwa dispatch` (or on any niwa-launched session). No distinct reporters, no explicit requests, no maintainer acknowledgment. The only durable artifact framing this topic is the exploration's own scope file (`wip/explore_remote-control-by-default_scope.md`), which states the idea comes from "user decisions from scoping" — i.e. the live conversation, not a pre-existing demand signal.

### Q2. What do people do today instead? — **Absent**

No workaround is documented. There is no issue or doc describing manually enabling remote-control after a dispatch, nor a comment lamenting the absence of one. The dispatch launch path (`internal/cli/dispatch_launcher.go`) inherits the parent environment and passes through flags (`buildClaudeBgArgs` builds `claude --bg <passthrough> <prompt>`); nothing in it, or in the global override layer, touches remote-control. The absence of any "do this after each dispatch" note means I cannot even confirm that an unmet workaround exists — there is no evidence either way.

### Q3. Who specifically asked? — **Absent**

No issue numbers, comment authors, or PR references exist. The two PRs that surfaced under a loose full-text search for "remote control" (#155 "configurable .env.example failure policy", #106 "rework niwa destroy") match incidentally on the words "remote"/"control" and are unrelated. Issue #166 ("Explore making niwa worktrees the default... EnterWorktree integration") matched "steer" incidentally and concerns worktrees, not remote-control. The only named source is the user, via the scope file.

### Q4. What behavior change counts as success? — **Absent**

No acceptance criteria, stated outcome, or measurable goal exists in any issue or linked doc. The scope file states design intent (host-level toggle, dispatch-scoped, downstream-overridable) but contains no success metric and is itself an input to this exploration, not a maintainer-authored spec.

### Q5. Is it already built? — **Absent (positively: not built)**

Grep across Go, Markdown, TOML, and JSON found zero implementation. The two `remote-control` string hits in `docs/designs/current/DESIGN-init-bootstrap-empty-source.md` (lines 643, 1269) use "remote-controlled string" to mean attacker-controlled input — a security framing, unrelated to Claude Code Remote. The global override layer (`GlobalConfig` / `GlobalConfigOverride`, `[global.claude.*]`) has no remote-control key. The dispatch launcher has no remote flag, env var, or settings write for it. No partial work, no scaffolding, no feature flag.

### Q6. Is it already planned? — **Absent**

There is no `docs/roadmaps/` directory in the repo. No open issue, design doc, brief, PRD, or spike references remote-control or Claude Code Remote as planned work. The "remote" mentions in the dispatch corpus point the other way: `docs/briefs/BRIEF-instance-dispatch.md:154` lists "Cross-machine or remote dispatch" as an explicit non-goal, and `docs/spikes/SPIKE-ephemeral-session-instances.md:32` uses "remote" to mean "build-from-remote." Neither is Claude Code Remote steering. The feature is neither sequenced nor backlogged.

## Calibration

**Demand not validated** (not "validated as absent").

Five of six questions returned Absent, and Q5 returned a positive "not built." But "not built" is not "rejected." I found no closed PR with maintainer rejection reasoning, no design doc that de-scoped remote-control, and no maintainer comment declining the request. The feature simply does not appear in any durable artifact except this exploration's own scope file, which sources the idea from the live user conversation rather than from prior demand. That is the signature of an un-surfaced / user-originated idea, not of a request the project evaluated and turned down.

The one adjacent data point — cross-machine/remote dispatch named as a non-goal in BRIEF-instance-dispatch.md — is about a different concept (where the worker runs), not about steering a worker from claude.ai/mobile. It does not constitute rejection of remote-control-by-default.

So: the repo carries no evidence for demand and no evidence against it. The gap is real and should be flagged. Validation here depends on the user's own intent (which the scope file records) plus whatever the harness/product context supplies — the niwa repo's durable artifacts cannot corroborate or refute it.

## Open Questions

- Does the demand live outside this repo (e.g. the user's workflow pain, a tsukumogami workspace-level need, or a Claude Code product direction) rather than in niwa's issues? The scope file implies a concrete user ask; that ask is not written down as a niwa issue.
- Is there a private-repo artifact (vision/tools) that states this need? Out of scope for this public-visibility pass and not searched.
- Would the user accept "no in-repo demand signal" as sufficient, given the idea is theirs and the exploration is the first durable capture of it?

## Summary

The niwa repo contains zero durable evidence of demand for enabling Claude Code Remote by default on `niwa dispatch`: no issue, PR, comment, design doc, roadmap, or code references the feature, and the only "remote-control" strings in the tree mean attacker-controlled input, not remote steering. The verdict is **demand not validated** — five of six questions are Absent and the feature is positively not built, but there is no rejection evidence either, so this is an un-surfaced user-originated idea, not a request the project evaluated and declined. The biggest open question is whether the demand exists outside this repo (the user's own workflow or a product-level direction), since the exploration's scope file attributes the idea to the live conversation rather than to any pre-existing artifact niwa could confirm.
