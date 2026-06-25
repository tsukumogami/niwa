# PRD scope: instance-dispatch

Upstream: docs/briefs/BRIEF-instance-dispatch.md (Accepted). Visibility Public.
Auto-mode under /scope. Research grounded in wip/research/prd_instance-dispatch_phase2_code-facts.md
and the prior explore findings.

## Load-bearing code facts (from phase-2 grounding)

- NO concurrency lock on instance naming; the numbered scan is TOCTOU. The hook
  dodges it by naming from session-id-prefix. Dispatch has no session id pre-launch
  -> must pass a freshly-generated unique `--name` token (customName branch).
- Reaper treats job-state `state:"done"` as dead; reclaims Ephemeral:true mapping +
  dead session, joined by instance_path. TTL backstop = 30 min.
- Mapping schema: session_id (must be valid UUID), instance_name, instance_path,
  transcript_path, created, ephemeral, label. NO launch-origin marker today.
- CRITICAL: the reaper only reclaims instances that HAVE a mapping. An instance
  created but not yet mapped is unreclaimable -> rollback-on-failure is mandatory.
- applier.Create returns instance path, materializes claude.env into the tree,
  self-cleans on clean errors, does NOT call reapOpportunistically (the CLI does).
- ClassifyCwd: CwdAtWorkspaceRoot / CwdInsideInstance / CwdInsideWorktree / CwdOutside;
  "inside a repo" resolves to instance/worktree; always exposes WorkspaceRoot.
- session-attach Supervise execs claude with cmd.Dir set, inherits env, streams stdio,
  blocks. Dispatch needs: parameterized args (--bg "<prompt>"), stdout capture, non-blocking.

## Requirement themes

command surface; launch-location resolution; instance creation (unique-name concurrency);
session launch (claude --bg, PATH preflight); id capture (scrape + jobs-dir UUID resolve,
timeout, ambiguity); mapping write (origin marker); teardown (reaper-primary, all cleanup
paths); partial-failure atomicity (rollback so no unreclaimable orphan); concurrency;
hook-path coexistence (re-entrancy no-op); non-functional.
