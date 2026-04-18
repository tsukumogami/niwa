# Research: user workflows + observable contract

## Onboarding scenario today

A new engineer joins codespar, clones the workspace via
`niwa init codespar --from codespar/dot-niwa` + `niwa create`. The
materializer reads `[env.vars]` from workspace.toml, per-repo
`[repos.<n>.env.vars]` blocks, and vault-resolved `[env.secrets]`,
merging into `.local.env` per repo.

Happy path: the app and workspace config are in sync; `npm start`
works.

**Failure mode — silent miss.** The app repo adds
`ENABLE_FEATURE_X=false` to its own `.env.example` in a PR last
week. Nobody updated codespar/dot-niwa. A new hire clones, runs
`niwa create`, gets a `.local.env` missing `ENABLE_FEATURE_X`
entirely. The app reads `process.env.ENABLE_FEATURE_X` as
`undefined`, behaves incorrectly (feature may default off, on, or
crash depending on code). The new hire debugs 30 minutes before
spotting the missing var. Nothing in niwa surfaced the miss —
`niwa status` has no concept of "values the app expected but the
workspace didn't supply".

The pain is not a loud error; it's a silent divergence between two
things that should be one.

## The imagined future

With `.env.example` integration:

1. `niwa create` clones the app repos as today.
2. The materializer discovers `.env.example` at each cloned repo's
   root, parses it, and merges the values as the lowest-priority
   defaults tier.
3. Workspace `[env.vars]` / `[repos.<n>.env.vars]` overlay on top
   and win per-key. Vault-resolved `[env.secrets]` top the stack.

Signals the user sees:

- `niwa create` emits a summary line: `loaded 12 env vars from
  codespar/.env.example (3 overridden by workspace.toml)`.
- `niwa status` gains a per-repo env count: `.env.example: 12,
  workspace: 3, vault: 2` (or a `--verbose` breakdown).
- `.local.env` contains `ENABLE_FEATURE_X=false` without any dot-niwa
  change needed.

Same scenario after the app merges `ENABLE_FEATURE_X=false`:

- Next apply picks it up automatically on next clone-or-pull.
- Apply output lists new keys: `new from .env.example:
  [ENABLE_FEATURE_X]`. User knows something changed; can investigate
  if they don't like the new default.

## Debuggability

Three questions users will ask, and what niwa must surface:

**"Why is this var missing from my .local.env?"**
A `niwa status --audit-env KEY` (or similar) that lists where niwa
looked: `[env.vars]`, `[repos.<n>.env.vars]`, `.env.example` files,
`[env.secrets]` declarations. Missing from all → print the search
list, recommend adding to one of them.

**"Why does this var have the wrong value?"**
Same command shows the resolution chain: "KEY resolved to 'xyz' from
`[repos.codespar.env.vars]` (workspace.toml line 45). Also present
in codespar/.env.example with value 'abc' (overridden)."

**"Which file supplied this value?"**
A new column on `niwa status --verbose` (or dedicated
`niwa status --env-sources`) mapping each materialized key to its
source file and line number.

## User stories

1. **As a new engineer onboarding to a codespar repo,** I want
   `niwa create` to pick up all env vars the app declares in its
   own `.env.example`, so that I don't have to coordinate with the
   workspace maintainer every time the app team adds a new var.

2. **As a workspace maintainer,** I want my workspace.toml
   overrides to always win over the app's `.env.example` defaults,
   so that a change in the app repo never silently overrides a
   deliberate workspace choice.

3. **As a developer debugging "why isn't my var picked up",** I
   want niwa to tell me which files it searched and where each
   resolved value came from, so I can trace the drift without
   grepping the whole workspace.

4. **As a security-conscious user,** I want niwa to refuse to
   materialize values from `.env.example` that look like real
   secrets (high entropy, known secret prefixes), so that a
   misconfigured app repo doesn't leak a real API key into
   `.local.env` files.

5. **As a workspace owner onboarding a third-party repo,** I want
   to opt out of `.env.example` discovery for a specific repo I
   don't fully trust, so that its `.env.example` contents don't
   automatically flow into my team's env files.

## Key finding

The feature's observable contract is **merge semantics +
transparency**. Users need to see where values come from and have
confidence that workspace intent always wins over app defaults. The
PRD's acceptance criteria should center on:

- Correct precedence across the merge stack.
- Visible status/verbose output showing sources.
- Loud (not silent) behavior when `.env.example` introduces new
  keys or when stub values look secret-shaped.
- Per-repo opt-out for trust-boundary cases.
