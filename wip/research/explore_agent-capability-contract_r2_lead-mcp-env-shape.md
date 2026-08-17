# Lead: MCP and environment delivery shape

## A. How MCP Reaches Claude Today

**`.mcp.json` is a purely generic file, never parsed by niwa.** There is no
MCP-specific Go code anywhere in the tree (`grep -rn "mcp_servers\|mcp\.json\|MCP" internal/` on
main turns up only comments — `internal/workspace/scaffold.go:80-84` is a
commented `[root.files]`/`[instance.files]` example, `internal/worktree/worktree.go:102`
is a negative comment ("does NOT create mcp.json"), and unrelated egress-control
comments in `internal/watch/`). The feature is entirely the generic
file-distribution mechanism documented in `docs/guides/file-distribution.md`
and delivered by `docs/designs/current/DESIGN-mcp-root-instance-distribution.md`
(status: Current, already merged onto `main` — this worktree already has it:
`internal/workspace/root_materializer.go`, `internal/workspace/workspace_context.go:242-370`).

- **Not special-cased.** `.mcp.json` is just the destination filename a user
  writes in `[root.files]` / `[instance.files]` (verbatim, no rename) or
  `[files]` (repo level, `.local`-infixed to `.mcp.local.json` — which Claude
  Code never reads, so repo-level MCP delivery via `[files]` doesn't actually
  work today; only the two non-repo tables deliver a loadable `.mcp.json`).
- **Never parsed, only copied.** `FilesMaterializer.materializeFile`
  (`internal/workspace/materialize.go:1873-1907`) and its verbatim counterparts
  `materializeVerbatimFile`/`materializeVerbatimDir`
  (`internal/workspace/materialize.go:1952-2015`) both funnel through
  `writeManagedFile` (`materialize.go:1846-1869`): read source bytes, sha256
  them for the source-fingerprint, write bytes verbatim to the destination.
  There is no JSON/TOML decode step anywhere in this path. The file is a
  byte-opaque blob to niwa.
- **Source/destination path expressions.** Source is `filepath.Join(ctx.ConfigDir, src)`
  (relative to `.niwa/`), containment-checked with `checkContainment`. Destination
  for the two non-repo tables is `filepath.Join(ctx.RepoDir, dest)` where
  `ctx.RepoDir` is set to `instanceRoot` (`workspace_context.go:242` calls
  `InstallWorkspaceRootSettings`, which builds `mctx.RepoDir = instanceRoot`) or
  `workspaceRoot` (`root_materializer.go`, analogous construction) — see
  `materialize.go:1953-1971`.
- **No structured MCP-declaration surface.** `WorkspaceConfig`
  (`internal/config/config.go`) has no `[mcp]` or `[mcp_servers]` table of its
  own. A user who wants Claude to see an MCP server hands niwa a finished,
  already-correctly-shaped `.mcp.json` and points a file-table entry at it.
  niwa's only leverage over the content is "copy these bytes to this path" —
  it has zero visibility into what servers, commands, or transports the file
  declares.

**Consequence for the Codex question:** generating a Codex `[mcp_servers.*]`
table from what a workspace already declares requires either (1) parsing the
user's `.mcp.json` — a new capability niwa does not have today — or (2) a new
structured config surface niwa generates *both* formats from. There is no
third option where niwa "just knows" the servers; today it doesn't even look.

## B. The Codex Project-Layer Shape

Source: `docs/spikes/SPIKE-codex-discovery-mechanics.md` (status: Complete,
measured against `codex-cli 0.147.0`), findings 5 and 6, plus the prior
attempt's `internal/workspace/codex_payload.go` on `origin/docs/dual-agent-workspace`
(read via `git show`, not checked out).

- **`[mcp_servers.*]` — verified presence, unverified schema.** Finding 5 states
  measured fact only at the level of *visibility*: "`[mcp_servers.*]` declared
  in a project layer appears in `codex mcp list` inside a trusted repository and
  is absent outside one." The spike does **not** document field-level shape —
  no `command`/`args`/`env`/transport/`url` breakdown appears anywhere in the
  spike text. The only place any field name shows up in the whole
  `docs/dual-agent-workspace` tree is a test fixture in
  `internal/workspace/codex_trust_retract_test.go:279,290` (`[mcp_servers.fs]\ncommand = "fs-server"\n`)
  — but that file is testing trust-hash retraction *around* an existing table,
  not asserting or exercising the table's schema. Treat the field shape
  (`command`, `args`, `env`, transport selection) as **unverified by this
  codebase's own research** — it would need to come from Codex's own upstream
  config schema (codex-rs), which neither the spike nor the prior attempt read
  for this purpose. Say this plainly to whoever designs the mapping: don't
  invent a schema from the one incidental fixture line.
- **`shell_environment_policy.set` — verified to parse, neighbors unverified.**
  Finding 6, in full: "Environment variables can be delivered to a session
  through `shell_environment_policy.set` in the project config, which parses
  cleanly at this layer. This is the route for anything a session needs in its
  environment." That is the entire content of finding 6. The spike is
  **silent** on: whether `set` is additive or replacing, what
  `shell_environment_policy`'s other keys are (`inherit`, `exclude`,
  `include_only` are Codex-upstream concepts but are not named or tested in
  this spike), and what the default policy is absent any declaration. Do not
  assume additive semantics — the spike doesn't establish it either way.
- **What else the project layer carries (finding 5).** The layer "carries
  instruction context, skills, general configuration including the byte
  budget, and MCP server declarations." Confirmed keys, gated by trust: the
  context/byte budget (`project_doc_max_bytes` — the one key the prior attempt
  actually wrote, see below), `mcp_servers.*`, and `shell_environment_policy.set`.
  Confirmed **not** carryable at this layer at all (trusted or not): "trust
  itself, project-root marker configuration, hook trust state, marketplaces and
  plugin registration, and eleven denylisted keys (provider URLs, `notify`,
  profiles, and similar)" — the spike names three examples of the eleven, not
  the full eleven; treat the denylist as only partially enumerated by this
  research.
- **The one key the prior attempt actually declared.** `codex_payload.go`
  (`origin/docs/dual-agent-workspace`) writes `.codex/config.toml` at the
  instance root via `renderCodexPayloadConfig`
  (`internal/workspace/codex_payload.go:~205-217` on that branch), whose entire
  body is a comment block plus one line: `project_doc_max_bytes = %d`. No
  `mcp_servers`, no `shell_environment_policy`, no other key. This is the exact
  "declared exactly one key" the review referenced.
- **Trust dependency (finding 5, last paragraph).** "The byte budget and the
  trust entry are load-bearing for each other. A budget declared for a
  directory that carries no trust entry does not apply." The spike measured
  this identically for MCP servers in the same paragraph ("MCP servers behave
  the same way"). So both `project_doc_max_bytes` and `mcp_servers.*` are
  **inert without a trust entry** for that repository root in the developer's
  own `~/.codex/config.toml`. The spike does not explicitly re-run the same
  measurement for `shell_environment_policy.set`, but groups it in the same
  "configuration keys require trust" sentence as the budget — treat it as
  trust-gated by the same mechanism unless a future measurement says otherwise.
  The prior attempt built exactly this trust-bootstrap machinery separately
  (`internal/workspace/codex_trust.go` on that branch): additive,
  path-scoped `[projects."<path>"]` entries written into the developer's own
  Codex config, because "Codex reads trust from the config layers it merges
  before a project layer exists, so the payload niwa plants inside an instance
  cannot vouch for itself."

## C. The Mapping (options + recommendation)

Given A (niwa never parses `.mcp.json`, has no structured MCP surface) and B
(Codex wants a `[mcp_servers.*]` TOML table of unverified schema, trust-gated):

**Option 1 — Parse the distributed `.mcp.json` and translate it.** Requires
building a new capability: a JSON decoder for whatever `.mcp.json` shape Claude
Code accepts (which niwa currently treats as opaque bytes), a translator into
Codex's TOML table (whose own schema is itself unverified per B), and a
decision for every field Claude's format carries that Codex's does not. This
also silently couples niwa to a second `.mcp.json` schema it has never had to
understand or validate before, and to two moving upstream targets to detect
drift in (Claude Code's and Codex's servers config, both of which niwa doesn't
control). Cost: high, and ongoing (two schemas to track, not one).

**Option 2 — Add a structured, agent-neutral MCP declaration to
`workspace.toml`; generate both `.mcp.json` and the Codex table from it,
keeping the existing file-distribution route as a compatibility path for users
who already hand-maintain a finished `.mcp.json`.** This mirrors how niwa
already treats env delivery (see D): one source of truth (`[env]`/`[claude.env]`
tables), materialized into agent-specific destinations. The MCP-neutral table
would need to cover the intersection of fields both agents actually support —
which, per B, niwa cannot state precisely yet for Codex's side because the
spike never pinned it down. Cost: moderate, front-loaded (needs the Codex
schema question resolved first via direct experimentation against `codex-cli`,
mirroring the spike's own method), but pays down over time — one source feeds
N agents instead of N-1 translators.

**Recommendation: Option 2, gated on first closing the B schema gap.** Option 1
makes niwa a permanent translator between two third-party formats it doesn't
own, discovers drift only when a user's session breaks, and has to be
re-derived every time either vendor's schema shifts. Option 2 puts niwa back in
the position it already occupies for every other agent-neutral primitive in
this codebase (env, settings, hooks): one workspace-level declaration,
per-agent materializers. Before building it, the mapping needs a short,
targeted repeat of the spike's own method — `codex mcp list` / `codex mcp get`
against a `[mcp_servers.<name>]` table with `command`, `args`, and `env` set,
plus whatever transport options codex-cli documents — because right now nobody
in this repository's research has verified that shape.

**Honest failure mode (required by the task).** If Option 2 ships, the
contract must be: the neutral declaration only ever offers the fields verified
to exist in *both* targets. A field a user needs that Codex's table cannot
express (a transport Codex doesn't support, for instance) is a **hard
configuration error at apply time naming the unsupported field and the
offending server**, not a silent drop. Silently omitting a server from the
Codex table while it's present in `.mcp.json` reproduces exactly the failure
the review is flagging against the prior attempt (Claude gets tools, Codex gets
none) — except now it would look like the contract accounted for MCP and
quietly didn't, which is worse than the current honest gap. This mirrors the
existing pattern in `promoteUnresolvedSet`/`ErrStrictSecrets`
(`internal/workspace/materialize.go:1001-1023`) — niwa already has a
strict/tolerant framework for "declared but could not supply"; an MCP
cross-agent gap should report through the same shape rather than inventing a
new one.

## D. Environment Delivery and Secret Safety

**Two existing pipelines, confirmed.**

1. **Generic `[env]`** — `EnvMaterializer`/`ResolveEnvVars`
   (`internal/workspace/materialize.go:1268-1737`, not fully re-read this pass
   but confirmed structurally) merges dotenv files, inline vars, and discovered
   files into a `.local.env` file per repo, written via
   `os.WriteFile(abs, data, secretFileMode)` at `materialize.go:1593` —
   `secretFileMode = 0o600` (`materialize.go:28`). Agent-agnostic; not injected
   into any agent's settings file directly.
2. **`[claude.env]`** — `resolveClaudeEnvVars`
   (`internal/workspace/materialize.go:956-1056`) resolves `promote` (pull named
   keys out of the `[env]` pipeline above) plus inline `vars`, and
   `SettingsMaterializer` writes the result into the `env` key of
   `.claude/settings.local.json` per repo (`materialize.go:1237`,
   `secretFileMode`) **and**, on this worktree's already-merged
   `DESIGN-mcp-root-instance-distribution` work, into the instance-root
   `.claude/settings.json` via `InstallWorkspaceRootSettings`
   (`internal/workspace/workspace_context.go:327-360`, also written with
   `secretFileMode` at line 360).

**How secrets get resolved and redacted (`internal/config/maybesecret.go`,
`internal/secret/`).** `MaybeSecret` (`maybesecret.go:116-136`) holds either
`Plain` (literal TOML string) or a resolved `Secret secret.Value` after the
vault resolver runs. `.String()`/`.MarshalText()` (`maybesecret.go:166-193`)
**always** redact a resolved secret to `"***"` — this is the safe default used
by logging, error messages, and any JSON/text serialization that doesn't
deliberately reach past it. The one sanctioned way to get real plaintext bytes
out is `secret/reveal.UnsafeReveal` (`internal/secret/reveal/reveal.go`), a
package that exists specifically so a linter/reviewer can allow-list its
callers; today those callers are exactly the workspace materializers (via
`maybeSecretString`, `materialize.go:39-44`) and the vault provider
implementations — nothing else in the tree calls it. So plaintext secret
material reaches disk *only* at the specific write sites that already call
`maybeSecretString`/`reveal.UnsafeReveal`, and every one of those sites this
research found writes with `secretFileMode` (0600).

**Which pipeline should feed `shell_environment_policy.set`:** `[claude.env]`,
not the generic `[env]` — for the same reason the instance-root Claude settings
already draw from `[claude.env]` rather than raw `[env]`: `[env]` is meant for
files a repo's own tooling reads (arbitrary dotenv shape, no agent framing),
while `[claude.env]` is explicitly the "what does this agent's session see in
its process environment" surface, already supports `promote` (pull from the
`[env]` pipeline) plus agent-scoped inline vars, and already resolves through
the identical vault/secret pipeline. A `[codex.env]` mirroring `[claude.env]`'s
shape (or a shared agent-neutral env table materialized into both agents'
formats, echoing the C recommendation) is the natural next step — this
research didn't find one on any branch, so it doesn't exist yet.

**Is writing resolved secrets into the Codex payload config acceptable, or a
leak? — Specific, not hand-waved.** The Codex payload directory lives at
`<instanceRoot>/.codex/` (`CodexPayloadDirName`,
`internal/workspace/codex_payload.go:17` on `origin/docs/dual-agent-workspace`).
Two concrete problems, both currently real on that branch and both would get
worse the moment `shell_environment_policy.set` starts carrying resolved
secret values:

1. **Wrong file mode today.** `InstallCodexPayload` writes `config.toml` with
   `os.WriteFile(configPath, ..., 0o644)`
   (`codex_payload.go:~170` on that branch) — **not** `secretFileMode`. Every
   other write site in this codebase that can carry secret material
   (`settings.local.json`, instance-root `settings.json`, `.local.env`, the
   generic `[files]`/verbatim-files `writeManagedFile` core) uses `0o600`. The
   payload config was written 0644 because at the time it carried exactly one
   non-secret integer (`project_doc_max_bytes`). Adding
   `shell_environment_policy.set` with real secret values into this same file
   without first fixing its mode to `secretFileMode` would make previously
   safe world/group-readable output start carrying plaintext credentials
   readable by any other local user/process — an actual regression, not a
   theoretical one.
2. **No git-exclusion coverage.** The instance root is explicitly **not** a git
   repository — `EnsureInstanceGitignore`
   (`internal/workspace/gitignore.go:16-59`) writes only a `*.local*` pattern
   into the instance root's own `.gitignore`, and its doc comment explains why:
   "users frequently place it inside a larger tracked working tree; the
   `.gitignore` at the instance root lets those outer repositories inherit the
   `*.local*` exclusion." `.codex/config.toml` carries **no** `.local` infix
   (it's a dedicated writer, not the file-distribution `.local`-rename path),
   so that pattern doesn't cover it. Separately, `gitexclude.EnsureRepoExclude`
   — the mechanism that adds coverage for a custom-named secret output file —
   is only ever called with `repoDir` (`internal/workspace/apply.go:1834`, on
   both this worktree and the dual-agent branch); it is never called for the
   instance root. So if a developer nests their niwa workspace inside a
   tracked outer directory (a pattern the codebase's own gitignore comment
   says is common), `.codex/config.toml` is neither `.local`-pattern-matched
   nor explicitly excluded — a `git add .` in that outer tree could stage a
   file carrying resolved secret plaintext.

**Verdict:** writing resolved secret values into the Codex payload config is
**not safe as the prior attempt left it** — not because the instance-root
location is inherently wrong (the existing `[claude.env]` → instance-root
`settings.json` path already puts resolved secrets in the same directory
safely, at 0600, per D above), but because `codex_payload.go`'s writer
specifically skipped the mode discipline every other secret-bearing writer in
this codebase follows, and because instance-root files generally lack the
`gitexclude` coverage repo-level files get. Both are fixable with small,
mechanical changes (switch to `secretFileMode`; either extend
`gitexclude.EnsureRepoExclude`-equivalent coverage to the instance root, or
give `.codex/config.toml` a name/pattern the existing `*.local*` rule already
catches) — but they are changes the second PR must make explicitly, not
inherit for free, and they should land in the same change that first adds
`shell_environment_policy.set` to the payload, not after.

## Open Questions

- The exact Codex `[mcp_servers.<name>]` field schema (command/args/env/URL,
  transport options) is unverified by any research in this repository — needs
  a targeted follow-up measurement against `codex-cli` before Option 2 (or any
  option) can be implemented, not just designed.
- Whether `shell_environment_policy.set` is additive over an inherited process
  environment or replaces it entirely is unestablished by the spike — needed
  before writing any `[codex.env]`-equivalent materializer, since additive vs.
  replacing changes what "safe default" even means.
- The spike names only 3 of the "eleven denylisted keys" the project layer
  cannot carry; the full list isn't recorded anywhere found in this research
  and would need re-derivation from Codex's own source before any config
  surface tries to write more keys into that layer.
- No `[codex.env]` (or agent-neutral env table) exists on any branch examined;
  this is new surface, not a rename of something half-built.

## Summary

`.mcp.json` reaches Claude purely through niwa's generic verbatim file-distribution tables (`[root.files]`/`[instance.files]`, already merged on main) — niwa copies bytes, never parses them, and has no structured MCP surface today, so producing a Codex `[mcp_servers.*]` table needs either a new `.mcp.json` parser or a new agent-neutral MCP declaration that generates both formats; the latter is recommended, but Codex's own `mcp_servers` field schema and `shell_environment_policy.set` semantics were never pinned down by the spike beyond "they parse and load when trusted," so that has to be measured before implementation, not assumed. Environment delivery should route through a `[codex.env]`-shaped mirror of the existing `[claude.env]` pipeline (which already resolves secrets through the same vault/redaction machinery and writes at 0600), not the agent-agnostic `[env]` pipeline. Writing resolved secrets into the prior attempt's Codex payload config (`<instanceRoot>/.codex/config.toml`) is unsafe as that code stands — it's written at `0o644` instead of the `secretFileMode` (0600) every other secret-bearing writer in the codebase uses, and the instance root has no `gitexclude` coverage for non-`.local`-named files — both are small, mechanical fixes that must land in the same change that first puts secret material into that file.
