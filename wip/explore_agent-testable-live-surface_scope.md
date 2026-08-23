# Explore scope: making the live test surface agent-testable

## Visibility

Public

## Scope

Tactical.

## The problem, stated from a real failure

While shipping the Codex instance-root orientation work (niwa#268), three parts
of niwa's test surface could not be executed by the agent doing the work, and
one of them hid that fact:

1. **`@codex-live` "a live Codex session writes a file on its first attempt"**
   is gated on `unshare --user --map-root-user` succeeding. In the agent's
   container it fails with `write failed /proc/self/uid_map: Operation not
   permitted`, so the step returns `godog.ErrPending`. At the Go subtest level
   that renders as `--- PASS: ... (0.00s)`. A run reporting "175 PASS, 0 SKIP"
   had executed 174 scenarios. CI never runs it either: the workflow does not
   install `codex`.

2. **`@claude-integration` "claude sees workspace context from workspace root
   but not from sub-repo"** is gated on `ANTHROPIC_API_KEY`, unset in the
   agent's environment. The CI job log for niwa#268 shows the "Claude
   integration tests" step **skipped** as well, so that scenario has not run
   anywhere for that change. It is the scenario most directly covering the code
   that change touched.

3. **`make test-live`** needs a real Claude subscription and deliberately does
   not sandbox `HOME`.

The same user-namespace limitation also blocked a measurement the work wanted:
`shell_environment_policy` at an instance root, probed through `codex sandbox`,
which cannot start (`bwrap: loopback: Failed RTM_NEWADDR`).

## What this exploration is for

The author runs agents on this machine with bypass permissions, docker, and the
ability to construct environments. The goal is to remove the human from the loop
for this class of verification -- by opening the gates where that is possible,
by replacing model-dependent assertions with real-binary ones where that is
stronger, and by making a gate that stays closed say so loudly.

Outcomes may be repo changes (test infrastructure, CI, tiering) or durable
documentation for future agents. Both are in scope; which is which is the
crystallize step's job.

## Constraint worth stating early

A gated scenario that silently reports pass is worse than one that fails. Any
recommendation that trades a real assertion for a cheaper one has to say what
coverage it gives up, and any gate that remains has to be visible in the output
an agent actually reads.

## Round 1 leads

| # | Lead | Question |
|---|------|----------|
| L1 | Sandbox gate anatomy | What in codex-cli 0.147.0 actually requires unprivileged user namespaces? Which `--sandbox` modes need it and which do not? Is the functional suite's gate broader than the thing it protects? |
| L2 | Credential-free Codex assertions | What can be asserted against the real `codex` binary with no auth and no sandbox? How does that compare with the suite's own Go reimplementation of the discovery walk, and can the two be held to each other? |
| L3 | Credential-free Claude assertions | Does Claude Code expose the resolved context/rules without a model call? Can the `@claude-integration` scenario's behavioral assertion be made deterministic, or split into a credential-free half and a model half? |
| L4 | Environment construction | What can an agent on this machine actually build -- docker socket reachability, userns-capable containers, rootless podman, devcontainer? What do GitHub-hosted runners permit? What would a documented "all gates open" environment look like? |
| L5 | Pending is invisible | How does `godog.ErrPending` surface at each layer, which other scenarios repo-wide are gated, and what is the smallest change that makes "did not run" distinguishable from "passed" in the output an agent greps? |
| L6 | Credentials for agents | How does the suite handle credentials today (`codexLiveEnv`, HOME sandboxing)? What precedent exists in the repo and workspace for supplying test credentials, and what would a safe contract look like? |
