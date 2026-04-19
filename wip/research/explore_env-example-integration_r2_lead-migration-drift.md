# Research: migration + drift handling

## Four-state matrix

| State | `.env.example` in app repo | `[repos.<n>.env.vars]` in workspace.toml | Desired behavior |
|-------|---------------------------|-------------------------------------------|------------------|
| A | no | no | Baseline: no env vars for this repo beyond workspace-level and secrets. No change from today. |
| B | no | yes | Current state: workspace.toml is the only source. Behavior unchanged by the feature. |
| C | yes | no | New: niwa auto-discovers `.env.example`, merges as the defaults layer. All keys flow into `.local.env`. |
| D | yes | yes | Coexistence: `.env.example` is the base layer; `[repos.<n>.env.vars]` overrides per-key. Conflict resolution: workspace.toml wins. On exact value agreement, niwa may emit a redundancy warning on apply (opt-in via flag in v1; promote to default later). |

## Migration tooling

**Command candidate:** `niwa status --audit-env` (extending the
existing audit pattern used for `--audit-secrets`).

Scans every managed repo for `.env.example`, compares keys against
`[repos.<n>.env.vars]` in workspace.toml, emits:

```
Env audit for workspace "codespar":

Repo: codespar
  new-in-example:            (keys in .env.example, not in workspace)
    DATABASE_URL = "postgres://localhost/dev"
    REDIS_URL    = "redis://localhost:6379"
  redundant:                 (exact value in both)
    PORT         = "3000"
    PROJECT_NAME = "default"
  overridden:                (workspace differs from .env.example)
    (none)

Repo: codespar-web
  new-in-example:
    NEXT_PUBLIC_POSTHOG_HOST = ""
  redundant: (none)
  overridden:
    NEXT_PUBLIC_API_URL = workspace:"http://localhost:3000" vs
                          .env.example:"https://codespar-production.up.railway.app"

Recommendation:
  Remove `redundant` entries from workspace.toml to consolidate
  source of truth on `.env.example`. Review `overridden` cases
  to confirm the workspace value is intentional.
```

v1 scope: read-only audit. v2 may add `--apply-diff` to emit a
patch against workspace.toml. Auto-writing workspace.toml in v1
is risky (what if the user has comments, ordering, formatting
they care about?).

## Drift alerting strategy

Three candidates for "app repo adds new key to `.env.example`":

- **Additive, silent.** Add without comment. User reads
  `.local.env` to discover. Ergonomic but fails the "user knows
  what changed" debuggability goal.
- **Additive, loud.** Add to `.local.env`; print banner on apply
  listing new keys from `.env.example`. Most aligned with "silent
  drift is the pain we're fixing".
- **Opt-in per key.** Ignore new keys until workspace.toml
  acknowledges them; warn loudly about the pending new keys. Too
  much friction for the common case (the app team added a
  sensible default — why should the workspace maintainer gatekeep
  it?).

**Chosen: additive, loud.** New keys from `.env.example` flow
through automatically; apply output shows a "new from
.env.example:" section listing the additions. The user sees the
change on the first apply that picks it up; they can edit
workspace.toml to override if the default doesn't suit them.

Rationale: the whole point of the feature is to stop silently
missing app-declared defaults. Gatekeeping each new key would
reintroduce the same drift problem we're solving.

## Backwards compatibility

What breaks: nothing, if the feature ships additive.

Upgrade path for existing workspaces (today's codespar/dot-niwa
with duplicated inline vars):

1. Ship the feature on by default. State D (both sources present)
   with values matching: redundant but not broken. Values
   differing: workspace wins (same as today's inline-wins).
2. Users opt into consolidation over time: run
   `niwa status --audit-env`, review redundancies, delete the
   duplicated `[repos.<n>.env.vars]` entries in one PR per repo.
3. After consolidation, the workspace.toml only declares the
   vars the team wants to override, not the full app surface.

Optional safety valve: a `[config] read_env_example = false`
workspace-level opt-out that restores pre-feature behavior. Ship
with `true` as the default; flag exists for paranoid users /
adopters who need a rollback path.

Issue #61 (static env files in dot-niwa are a degraded subset) is
related but not on this feature's critical path. The
`.env.example` reader and the static env-files reader could
converge later, but v1 keeps them separate to avoid scope creep.

## Per-repo opt-out

Some workspaces may include third-party repos they don't want to
auto-merge. Add a per-repo knob:

```toml
[repos.third-party-repo]
read_env_example = false
```

Default: inherits the workspace-level setting (default true).

## User stories

1. **As a workspace maintainer,** I want to run
   `niwa status --audit-env` to see which `[repos.<n>.env.vars]`
   entries are redundant with the app's `.env.example`, so I can
   consolidate the source of truth without manually diffing files.
2. **As a developer on the app team,** I want my new
   `.env.example` entry to flow into teammates' `.local.env` on
   their next apply with a visible "new from .env.example" note,
   so that everyone picks up the default without the dot-niwa
   maintainer gatekeeping it.
3. **As a workspace maintainer with a third-party repo in my
   workspace,** I want to opt that repo out of `.env.example`
   discovery, so that its env contents don't auto-merge into my
   team's files.
4. **As a CI system,** I want `niwa status --audit-env` to exit
   non-zero when the workspace has overridden keys or when
   `.env.example` has introduced new keys nobody acknowledged,
   so I can flag drift before it reaches production.

## Key finding

The feature's compatibility model is "additive, loud, opt-out
per-repo". Default on; existing workspaces keep working;
consolidation is opportunistic, not forced. `niwa status
--audit-env` gives maintainers the visibility they need to
consolidate over time.
