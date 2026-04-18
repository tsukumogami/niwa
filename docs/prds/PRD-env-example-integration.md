---
status: Draft
problem: |
  Managed app repos commonly ship `.env.example` files declaring the env
  vars they need, with sensible defaults for non-sensitive fields and
  placeholder stubs for secrets. niwa currently requires these values to
  be duplicated into `[repos.<name>.env.vars]` in the workspace config
  repo, creating drift risk: when the app repo adds a new var or changes
  a default, the workspace config does not notice, and teammates can end
  up with silently broken `.local.env` files. This particularly affects
  Node-ecosystem projects where `.env.example` is the dominant
  convention for declaring environment-variable surface.
goals: |
  Let the app repo's `.env.example` be the source of truth for env var
  defaults. niwa discovers these files automatically on every apply,
  merges them into the materialized `.local.env` as the lowest-priority
  defaults layer, and preserves the existing precedence rules so
  deliberate workspace overrides always win. Surface drift (new keys,
  overridden keys, redundant workspace entries) through an audit
  command so maintainers can consolidate over time. Keep managed app
  repos read-only; all niwa output still lands in `.local.env`.
---

# PRD: .env.example Integration

## Status

Draft

## Problem Statement

Application repositories in the Node/JavaScript ecosystem almost
universally ship a `.env.example` file at the repo root. It serves as
both documentation (declaring which env vars the app expects) and a
source of baseline defaults (port numbers, local database URLs,
feature-flag values, safe-to-commit test keys). Developers joining a
project typically copy `.env.example` to `.env.local` and fill in the
sensitive values.

niwa currently ignores `.env.example` entirely. Workspace maintainers
must duplicate every variable they want materialized into
`[repos.<name>.env.vars]` in their dot-niwa workspace config. This has
three concrete consequences:

- **Silent drift.** When the app team adds a new env var to
  `.env.example`, nothing in niwa notices. New teammates clone the
  workspace, run `niwa create`, and get a `.local.env` missing the new
  var. Apps may behave incorrectly without crashing, costing debug
  time before the miss is traced to the drift.
- **Duplicated maintenance.** Every default in `.env.example` has to
  be copied into `workspace.toml`, then updated in two places on every
  change. The single source-of-truth principle is violated; reviewers
  of either repo can't tell whether the values agree.
- **Onboarding friction.** A workspace maintainer setting up a new
  codespar-style project in niwa has to read every `.env.example`
  file and re-type its contents into workspace.toml before the
  workspace functions for teammates.

The trigger scenario is the codespar workspace (codespar/codespar,
codespar/codespar-web), both Node apps with non-trivial `.env.example`
surface. The same pattern applies to any Node/JS project the team
onboards to niwa.

## Goals

- niwa reads each managed repo's `.env.example` on every apply and
  merges it into the repo's materialized `.local.env`.
- `.env.example` acts as the lowest-priority defaults layer;
  workspace-declared overrides always win on collision.
- Drift is visible: users can audit which keys flow from
  `.env.example` versus workspace config, and new keys from
  `.env.example` are surfaced on every apply.
- Values that look like real secrets in `.env.example` are rejected
  at apply time with an actionable error; intentional stubs and
  known-safe test values are permitted.
- The feature is opt-out, not opt-in — existing workspaces get the
  capability by default; workspace maintainers can disable it
  workspace-wide or per-repo for trust-boundary cases.
- niwa never writes to managed app repos. All output remains in
  `.local.env` (mode `0600`) as today.

## User Stories

1. **As a developer onboarding to a codespar-style app,** I want
   `niwa create` to pick up every env var the app declares in its
   own `.env.example`, so that I don't have to wait for the dot-niwa
   maintainer to mirror the app's defaults before I can run the
   app locally.

2. **As a workspace maintainer,** I want my explicit
   `[repos.<n>.env.vars]` entries to always override the app's
   `.env.example` defaults, so that a change in the app repo never
   silently erases a deliberate workspace choice.

3. **As an app repo owner,** I want adding a new entry to
   `.env.example` to flow through to every workspace on the next
   `niwa apply`, with a visible banner listing the new keys, so
   that teammates don't need to notice the diff by reading the
   raw `.env.example` file themselves.

4. **As a security-conscious reviewer,** I want niwa to refuse to
   materialize values from `.env.example` that look like real
   secrets (high entropy, known secret-vendor prefixes), so that a
   misconfigured app repo doesn't silently leak an accidentally
   committed API key into my team's `.local.env` files.

5. **As a workspace maintainer auditing drift,** I want a command
   that reports per-repo which `.env.example` keys are new, which
   are redundant with workspace.toml, and which are overridden, so
   that I can consolidate duplicated entries and catch drift
   before it reaches production.

6. **As a workspace maintainer onboarding a third-party repo I
   don't fully trust,** I want to opt that repo out of
   `.env.example` discovery while keeping the feature on for my
   own repos, so that an untrusted contributor's `.env.example`
   can't automatically flow into my team's env files.

## Requirements

### Discovery and materialization

**R1. Automatic discovery.** On every `niwa apply` (and `niwa
create`), niwa MUST discover a file named `.env.example` at the root
of each cloned managed repo, regardless of visibility (public or
private). Files at other paths (e.g., `src/.env.example`,
`.env.sample`) are not discovered in v1.

**R1a. Absence is not a problem.** When a managed repo has no
`.env.example` file at its root, niwa MUST treat the absence as
the normal case: emit an info/debug-level log line (not a warning,
not an error) and continue apply. Silence at normal log levels;
visibility only when the user opts into verbose or debug output.

**R2. Per-repo materialization.** Values parsed from a repo's
`.env.example` MUST flow into that repo's `.local.env` file, not
into other repos' files or into a workspace-level file.
`.env.example` values do NOT cascade across repo boundaries.

**R3. Non-destructive.** niwa MUST NOT write to managed app repos
for any reason. Discovery is read-only. All output remains in
`.local.env` files materialized inside each repo's working tree,
covered by the existing `.gitignore *.local*` invariant.

### Merge precedence

**R4. Lowest-priority defaults layer.** `.env.example` values MUST
sit at the lowest priority in the merge stack. Precedence, from
lowest to highest: `.env.example` → workspace `[env.vars]` /
`[env.secrets]` → `[repos.<n>.env.vars]` / `[repos.<n>.env.secrets]`
→ vault-resolved values → personal-overlay bindings. Higher-priority
entries override lower entries per key.

**R5. Workspace wins on collision.** When a key appears in both
`.env.example` and any workspace-declared layer, the workspace layer
MUST win. No warning on override unless the audit command is
invoked.

### Parser syntax (v1)

**R6. Node-style syntax.** The parser MUST correctly handle:
- `KEY=VALUE` basic lines.
- Single-quoted values (`KEY='value'`): literal, no escape
  interpretation.
- Double-quoted values (`KEY="value"`): interpret `\n`, `\t`, `\"`,
  `\\` escape sequences.
- Full-line comments beginning with `#`.
- `export KEY=VALUE` prefix: treated identically to `KEY=VALUE`.
- Empty values (`KEY=`): produce an empty-string value.
- CRLF line endings: tolerate; normalize to LF.

**R7. Explicit non-support.** The v1 parser does NOT support
variable expansion (`${FOO}`, `${FOO:-default}`), multi-line quoted
values (backslash continuation or heredocs), inline comments after
values, or YAML-style syntax. Files using these features parse as
best-effort; niwa MUST emit a warning naming the line and the
unsupported construct so users know their `.env.example` exceeds
v1's contract.

### Secret detection

**R8. Stub vs. probable-secret classification.** For each
`.env.example` value, niwa MUST classify it as one of: "safe stub"
(empty value, allowlisted prefix, or allowlisted placeholder
pattern), "probable secret" (high Shannon entropy or matches a
known secret-vendor prefix), or "neutral default" (neither). Safe
stubs and neutral defaults flow through materialization. Probable
secrets fail apply with an actionable error.

**R9. Entropy threshold.** Probable-secret detection MUST use
Shannon entropy over the value's characters. Values above **3.5
bits/char** (a deliberately conservative threshold chosen to avoid
flagging readable English defaults like `postgres://localhost/dev`
while catching randomized-looking tokens) are flagged. The
threshold MUST be adjustable via a workspace-level config knob.

**R10. Safe-prefix allowlist.** Values matching any of these
patterns MUST be classified as safe stubs regardless of entropy:
- Empty values (`KEY=`).
- `changeme`, `change-me`, `CHANGE_ME` (case-insensitive).
- `<your-*>`, `<...>`, `example.com`, `example.org`,
  `example@example.com`.
- `your-api-key`, `your-token`, `your-secret` (case-insensitive).
- Prefix `test_`, `TEST_`, `EXAMPLE_`, `EXAMPLE-`.
- Publishable-key prefixes: `pk_test_`, `pk_live_` (Stripe/Clerk —
  publishable by design).
- Values matching `localhost`, `127.0.0.1`, or `0.0.0.0` URL
  patterns.

The allowlist MUST be extensible via a workspace-level config
knob. Documenting that teams can add their own patterns is a
goal; the exact config key name is an open question.

**R11. Known-secret-prefix detection.** Values matching known
secret-vendor prefixes MUST be flagged as probable secrets
regardless of entropy or allowlist. Initial list:
- `sk_live_`, `sk_test_` (Stripe secret keys — even test secret
  keys are server-side secrets)
- `AKIA`, `ASIA` (AWS access keys)
- `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_` (GitHub)
- `glpat-` (GitLab personal access tokens)
- `xoxb-`, `xoxp-`, `xapp-` (Slack)
- `sq0atp-`, `sq0csp-` (Square)

**R12. Escape hatch.** A `--allow-plaintext-secrets` flag on `niwa
apply` (mirroring the existing flag for inline values) MUST
downgrade probable-secret failures to warnings, allowing the apply
to proceed. The flag is one-shot; there is no persistent override.

**R13. Guardrail coverage.** When a managed repo's git remote is a
public GitHub URL and its `.env.example` contains a probable
secret, niwa MUST fail apply with the same guardrail that already
blocks plaintext in `[env.secrets]` for public remotes. The error
MUST name the offending file and line.

### Drift policy

**R14. Additive-loud default.** When `.env.example` introduces a
new key not present in any workspace-declared layer, niwa MUST
add it to `.local.env` and emit a stderr line during apply:
`new from .env.example: <repo>/<key>`. Multiple new keys
produce one line per key. This behavior is the default; there is
no flag to opt into "additive-silent" in v1.

**R15. Silent on overrides.** When `.env.example` supplies a key
that workspace config overrides, niwa MUST NOT emit a line on
apply — the override is deliberate, not noise. Users query the
audit command (R16) to see overrides.

### Observability

**R16. Audit command.** `niwa status --audit-env` MUST report,
per repo:
- **new-in-example**: keys present in `.env.example` with no
  workspace override.
- **redundant**: keys present in both with matching values
  (consolidation candidates).
- **overridden**: keys present in both with different values
  (deliberate overrides; shows both values).
- **probable-secret**: keys whose `.env.example` value was flagged
  as a probable secret (refused to materialize).

Exit code is 0 when no issues exist and no `.env.example` files
were found. Exit code is 0 when only `new-in-example` or
`redundant` entries exist (informational). Exit code is 1 when any
`probable-secret` entries exist. Exit code is 1 when the guardrail
would fail apply for any repo.

**R17. Source traceability.** `niwa status --verbose` (or a new
`niwa status --env-sources` subcommand — exact flag is an open
question) MUST list, for each materialized env var in each repo:
the key, the resolved value (redacted to `***` for secrets), and
the source (`.env.example`, `[env.vars]`, `[repos.<n>.env.vars]`,
`[env.secrets]`, vault://..., or personal overlay).

### Opt-outs

**R18. Workspace-level opt-out.** A top-level `[config]
read_env_example = false` flag in workspace.toml MUST disable
`.env.example` discovery for every managed repo in the workspace.
Default when unset: `true`.

**R19. Per-repo opt-out.** A per-repo
`[repos.<n>] read_env_example = false` flag MUST disable
discovery for that specific repo, overriding the workspace-level
setting for that repo only. Useful for third-party or untrusted
repos.

### Backwards compatibility

**R20. Existing workspaces unaffected.** Workspaces that currently
duplicate vars in `[repos.<n>.env.vars]` MUST continue to work
without modification. The feature ships on by default;
collisions resolve via R5 (workspace wins), so no materialized
value changes unless the `.env.example` introduces a key the
workspace has not acknowledged.

**R21. No workspace.toml rewrite.** v1 does NOT provide a
`--apply-diff` or similar auto-rewrite of workspace.toml. Users
consolidate manually based on `niwa status --audit-env` output.

### Non-functional

**R22. Performance.** `.env.example` discovery and parsing MUST
NOT add measurable latency to `niwa apply`. Budget: 5ms per
managed repo for discovery + parse, measured on a local disk.
Real-world files are small (typically under 2 KB); this budget
has headroom.

**R23. Parser robustness.** A malformed or unreadable
`.env.example` MUST NOT crash niwa or fail apply. Two failure
modes, both resolve to warnings (not errors):

- **Per-line parse errors** (e.g., `= missing-key`, unmatched
  quotes): emit a warning naming the file, line number, and
  problem. The parser continues past the bad line; other lines
  in the same file still contribute to materialization.
- **Whole-file failures** (e.g., permission denied, binary file,
  empty read): emit a warning naming the file and the failure
  reason. The repo is treated as having no `.env.example` for
  the rest of the apply. Other repos' `.env.example` files are
  processed normally.

In both cases, apply continues and exits 0 unless a separate
failure (probable-secret, guardrail, non-env cause) blocks it.

## Acceptance Criteria

### Discovery and materialization

- [ ] A managed repo with `.env.example` at its root produces a
  `.local.env` containing every non-secret-flagged entry from
  that file, in addition to workspace-declared values.
- [ ] A managed repo without `.env.example` produces the same
  `.local.env` as before the feature shipped (no regression).
  Apply completes with exit code 0; no warning or error is
  emitted at the default log level.
- [ ] A managed repo whose `.env.example` is unreadable
  (permission denied, binary content, etc.) produces a
  warning naming the file and reason; apply still exits 0 and
  materializes the repo's other env sources normally.
- [ ] A managed repo whose `.env.example` has one malformed
  line among otherwise-valid lines emits a warning for the bad
  line and materializes the other lines' values.
- [ ] `.env.example` at a path other than the repo root (e.g.,
  `src/.env.example`) is ignored.
- [ ] niwa never writes to a managed app repo's working tree
  outside `.local.env` files (verified by per-repo git-status
  check after apply).

### Merge precedence

- [ ] When `.env.example` has `KEY=foo` and `[repos.<n>.env.vars]`
  has `KEY=bar`, the materialized value is `bar`.
- [ ] When `.env.example` has `KEY=foo` and workspace `[env.vars]`
  has `KEY=bar`, the materialized value is `bar`.
- [ ] When `.env.example` has `KEY=foo` and nothing else
  declares KEY, the materialized value is `foo`.
- [ ] When `.env.example` has `KEY=foo` and `[env.secrets]` has
  `KEY=vault://…`, the materialized value is the vault-resolved
  value.

### Parser

- [ ] Single-quoted, double-quoted, and bare values parse to
  their expected interpretations.
- [ ] Escape sequences (`\n`, `\t`, `\"`, `\\`) inside
  double-quoted values are interpreted.
- [ ] Escape sequences inside single-quoted values are preserved
  literally.
- [ ] Lines beginning with `#` are treated as comments.
- [ ] `export KEY=VALUE` parses identically to `KEY=VALUE`.
- [ ] Empty values (`KEY=`) produce empty-string entries.
- [ ] CRLF line endings parse correctly.
- [ ] `${FOO}` expansion is NOT performed (value is literal).
- [ ] A malformed line (e.g., `= value`) emits a warning but
  does not crash apply; parsing continues past the bad line.

### Secret detection

- [ ] A value with entropy above 3.5 bits/char not matching any
  allowlist pattern is flagged as probable-secret and blocks
  apply.
- [ ] `pk_test_Y2FyaW5nLXNocmV3LTc5LmNsZXJrLmFjY291bnRzLmRldiQ`
  (the codespar example value) is allowed through (safe-prefix).
- [ ] An empty value (`KEY=`) passes regardless of other rules.
- [ ] `KEY=changeme` and `KEY=<your-api-key>` pass (allowlist).
- [ ] A value with `sk_live_` prefix is flagged regardless of
  entropy or allowlist.
- [ ] `--allow-plaintext-secrets` downgrades probable-secret
  failures to warnings for a single apply.
- [ ] A managed repo with a public GitHub remote containing a
  probable-secret value fails apply with the public-repo
  guardrail error; a private-remote repo does not.

### Drift policy

- [ ] A new key in `.env.example` produces `new from
  .env.example: <repo>/<key>` on apply stderr.
- [ ] A key overridden by workspace config does NOT produce a
  stderr line on apply.

### Observability

- [ ] `niwa status --audit-env` lists new-in-example, redundant,
  and overridden keys per repo with exit code 0 when no
  probable-secret entries exist.
- [ ] `niwa status --audit-env` exits 1 when any
  probable-secret entries exist.
- [ ] `niwa status --verbose` (or the chosen equivalent)
  reports the source of each materialized env var per repo.

### Opt-outs

- [ ] Setting `[config] read_env_example = false` in
  workspace.toml disables discovery for all repos in the
  workspace.
- [ ] Setting `[repos.<n>] read_env_example = false` disables
  discovery for that repo only; other repos continue to
  discover normally.

### Backwards compatibility

- [ ] A workspace that today materializes correctly produces
  identical `.local.env` content after the feature ships,
  provided no `.env.example` introduces a key outside the
  workspace's existing declarations.

## Out of Scope

- **Framework-specific `.env.*` variants.** Next.js `.env.local`
  layering, Laravel `.env`, Django settings modules, Elixir
  `runtime.exs`, Rails credentials, etc. These are framework
  concerns, not the `.env.example` convention.
- **Writing to managed app repos.** niwa stays declarative and
  read-only on app repos. No mechanism to generate or update
  `.env.example` from niwa is in scope.
- **Non-root `.env.example` paths.** Only `<repo-root>/.env.example`
  is discovered in v1. Monorepo sub-package `.env.example` files
  are deferred.
- **Non-Node-ecosystem parsers.** Python, Ruby, Rust, Go, Elixir
  `.env` variants that ship with the same name but different
  syntax are deferred. The Node-style parser covers the
  observable user base.
- **Variable expansion and multi-line values.** Deferred until a
  user with a real `.env.example` requiring them is found.
- **`--apply-diff` auto-rewrite of workspace.toml.** Users
  consolidate manually based on audit output.
- **Issue #61 (static env-files parity).** Related but separate
  scope. This feature may eventually subsume the
  `[env].files` path, but v1 ships both side-by-side.
- **Issue #62 (vault URIs in recommended/optional sub-tables).**
  Unrelated.

## Open Questions

- **Exact workspace-level config key names** for the entropy
  threshold override and the allowlist extension. Candidates:
  `[config.env_example] entropy_threshold = 3.5`,
  `[config.env_example] allowlist = [...]`. Pin these during
  design or first implementation PR.
- **`niwa status` flag name** for source traceability: `--verbose`
  (reuse existing) vs. new `--env-sources` vs. new
  `--show-sources`. Preference: extend existing `--verbose` to
  include env sources rather than add a new flag, but this
  depends on whether the existing `--verbose` output is already
  cluttered.
- **JSON output for audit command.** `niwa status --audit-env
  --format json` would be useful for CI scripts. Ship in v1 or
  defer? Preference: defer to a follow-up so v1 ships smaller;
  CI consumers can parse the text output until JSON lands.

## Known Limitations

- **Secret-detection false positives.** The entropy threshold is
  conservative (3.5 bits/char vs. truffleHog's 3.0) to minimize
  flagging readable English defaults. This means some
  genuinely-random test values (e.g., a randomly-generated UUID
  used as a webhook token) will slip through unflagged unless
  they match a known-prefix rule. Users who care about these
  cases can add their own prefix patterns to the allowlist
  (forbidden direction) or lower the threshold via the config
  knob.
- **Safe-prefix allowlist needs curation.** The initial list is
  based on observed patterns in the codespar workspace and
  well-known vendor conventions. Teams with their own test-token
  conventions will need to extend the list for their workspace.
  The feature ships with an opinionated initial set; extending
  it is a documented workflow.
- **No cross-repo drift reporting.** `niwa status --audit-env`
  reports per-repo. A workspace-wide summary across repos is not
  in v1.
- **Windows line endings tolerated, not optimized.** The parser
  handles CRLF but niwa today targets macOS + Linux; Windows is
  via WSL per the vault-integration guide.

## Decisions and Trade-offs

### Decision: workspace intent always wins over `.env.example` defaults

**Decided:** When a key appears in both `.env.example` and any
workspace-declared layer, the workspace value wins.

**Alternatives considered:**
- `.env.example` wins (app team controls defaults). Rejected
  because it would silently erase deliberate workspace choices
  when the app team changes a default — exactly the drift
  direction we want to avoid.
- Warn-and-pick-one (configurable). Rejected because it adds a
  knob without a clear "right" default.

**Reasoning:** Exploration research converged across every lead
on this direction. Workspace overrides are deliberate; app defaults
are baselines; the precedence should match that intent.

### Decision: additive-loud drift policy

**Decided:** New keys in `.env.example` flow into `.local.env`
automatically; apply output emits `new from .env.example:
<repo>/<key>` for each.

**Alternatives considered:**
- Additive-silent (add without comment). Rejected — reintroduces
  the "silent drift" problem the feature is fixing.
- Opt-in per key (fail until workspace acknowledges). Rejected —
  adds friction for the common case where the app team added a
  sensible default and teammates should just get it.

**Reasoning:** The problem statement names silent drift as the
core pain. Any strategy that keeps drift silent contradicts the
goal. Gatekeeping each key reintroduces the maintenance burden
we're trying to eliminate.

### Decision: feature defaults to on; opt-out instead of opt-in

**Decided:** `read_env_example = true` is the default; users
disable per-workspace or per-repo.

**Alternatives considered:**
- Opt-in workspace flag. Rejected — forces every existing
  workspace to touch its workspace.toml to benefit, dampening
  adoption.
- Per-repo opt-in in workspace.toml. Rejected — defeats the
  auto-discovery goal.

**Reasoning:** The feature is additive and workspace values win
on collision, so enabling by default is safe for existing
workspaces. Trust-boundary cases (third-party repos) are the
minority and get a per-repo opt-out.

### Decision: Node-style parser only for v1

**Decided:** The v1 parser handles Node-ecosystem `.env.example`
syntax (quoted values, comments, `export` prefix). Python/Ruby/
Rust/Elixir/Go variants are deferred.

**Alternatives considered:**
- Per-ecosystem parser dispatched by a `type = "python"` field.
  Rejected — no observable user base demands it; adds complexity
  for hypothetical scenarios.
- dotenvy-compatible parser supporting all known syntax. Rejected
  — variable expansion and multi-line values are real parsing
  hazards with corner cases; scope it when a user hits the limit.

**Reasoning:** The trigger users (codespar) are Node. Every
research lead confirmed Node dominates the convention. Starting
Node-narrow is safer than over-generalizing.

### Decision: no vendored dotenv library

**Decided:** niwa extends its existing `parseEnvFile` function
with ~50 LOC rather than depending on `godotenv` or `gotenv`.

**Alternatives considered:**
- Depend on `github.com/joho/godotenv`. Rejected — the existing
  parser already covers the simple case, and secret-detection
  logic has to be custom anyway.
- Write a fresh parser in a new package. Rejected — splits the
  surface across two nearly-identical parsers.

**Reasoning:** The dependency boundary is cheap to extend, and
niwa's R20 no-Go-deps-beyond-stdlib preference applies.

### Decision: entropy threshold 3.5 bits/char

**Decided:** The default probable-secret threshold is 3.5
bits/char, higher than truffleHog's 3.0.

**Alternatives considered:**
- 3.0 (match truffleHog). Rejected because readable English
  default values like `postgres://codespar:codespar_dev@localhost/
  codespar` score around 3.2 and would flag noisily.
- 4.0 (conservative). Rejected because genuine randomized test
  tokens can score 3.8-4.0 and would slip through.

**Reasoning:** 3.5 is a deliberate trade-off calibrated to the
codespar example where readable defaults must pass and
randomized-looking values must fail. Users can adjust the
threshold via config for different preferences.

### Decision: absence is silent; parse failures are warnings; neither blocks apply

**Decided:** A missing `.env.example` is the normal case — niwa
emits only an info/debug log line. A malformed or unreadable
`.env.example` emits a warning and skips the file; apply still
succeeds.

**Alternatives considered:**
- Absence emits a warning. Rejected — the absence is common (non-
  Node repos, repos with env managed elsewhere); warning on the
  normal case is noise.
- Parse failures emit errors that block apply. Rejected — a
  corrupt file in one repo shouldn't prevent niwa from
  materializing the other repos' env vars, and workspace intent
  (inline `[env.vars]`) can still supply the values the bad file
  would have covered.

**Reasoning:** The feature is additive defaults. An additive
feature should never block apply on its own. Visibility into
problems (warnings) without blocking behavior matches the
precedent set by other optional layers like Claude content
files.

### Decision: `niwa status --audit-env` not `niwa env audit`

**Decided:** Extend the existing `niwa status` command with an
`--audit-env` flag, mirroring the existing `--audit-secrets`
pattern.

**Alternatives considered:**
- New `niwa env` subcommand family. Rejected — adds a new
  top-level verb for a single operation; violates "one obvious
  way" by fragmenting audit surfaces.
- `niwa env audit` as a standalone verb. Rejected for the same
  reason.

**Reasoning:** Consistency with `--audit-secrets` is worth more
than a marginally-more-discoverable new verb.
