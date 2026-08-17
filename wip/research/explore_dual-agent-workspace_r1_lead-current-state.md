# Lead: What did the prior increment build, and what has to change?

## Findings

### 1. Inventory of `internal/agent`

The package doc, quoted verbatim from `internal/agent/agent.go:1-14`:

> // Package agent defines the AI coding agent niwa prepares a workspace for.
> //
> // The Agent discriminator is a session-global choice (one agent for a whole
> // workspace preparation), resolved once per session from a workspace-config
> // default plus a per-session flag/environment override. It is deliberately a
> // leaf package -- it imports nothing else in the module -- so both
> // internal/config (which carries the raw default as a string) and the
> // higher-level internal/workspace and internal/cli packages can depend on it
> // without an import cycle.
> //
> // The zero value Agent("") behaves as the Claude agent. This is a fail-safe
> // contract: a construction site that has not yet been wired to set the agent
> // degrades to today's Claude behavior rather than to an empty, broken filename.

"one agent for a whole workspace preparation" is the exclusive contract stated in prose, exactly as the map claimed.

Exported symbols, all in `internal/agent/agent.go` (verified — this is the only non-test file in the package):

- `type Agent string` (line 19) — the discriminator.
- `AgentClaude Agent = "claude"` (24), `AgentCodex Agent = "codex"` (26) — the closed set.
- `ParseAgent(s string) (Agent, error)` (36-45) — validates against `{"", "claude", "codex"}`, empty maps to `AgentClaude`.
- `(a Agent) RootContextFileName() string` (59-64) — `AGENTS.md` for Codex, `CLAUDE.md` otherwise. Doc: "the niwa-owned, non-repository levels (the workspace root and each group directory)".
- `(a Agent) LocalContextFileName() string` (73-78) — `AGENTS.md` for Codex, `CLAUDE.local.md` otherwise, but its own doc comment (69-72) says: "The Codex value is provisional seam-completeness: this slice skips all repository/worktree-level writes under Codex (see WritesRepoLevelContext), so this branch is currently unused."
- `(a Agent) WritesRepoLevelContext() bool` (85-87) — `true` for Claude, `false` for Codex. Doc (80-84): "writing an AGENTS.md inside a cloned repository would risk clobbering the repository's own committed AGENTS.md and dirtying the git working tree, so that level is deferred."
- `ResolveAgent(flag, env, workspaceDefault string) (Agent, error)` (94-105) — precedence flag > env > workspaceDefault > claude, each candidate run through `ParseAgent`.

Not exported but load-bearing: `normalize()` (49-54), a private method every accessor calls to fold the zero value to `AgentClaude`.

Call sites for every exported symbol (verified by grep, non-test files only):

| Symbol | Callers |
|---|---|
| `Agent` (type, field) | `internal/workspace/materialize.go` has no direct field but `MaterializeContext`... actually the field lives on `RootMaterializeOptions.Agent` (`root_materializer.go:98`), `WorktreeApplyOptions.Agent` (`worktree_content.go:444`), `Applier.Agent` (`apply.go:49`) |
| `ParseAgent` | Only inside `ResolveAgent` itself (`agent.go:39,41`) — no other non-test caller found |
| `ResolveAgent` | `internal/cli/agent.go:21` (`resolveSessionAgent`) |
| `RootContextFileName` | `internal/workspace/root_materializer.go:375` (`writeRootClaudeMD`), `internal/workspace/content.go:44` (`InstallWorkspaceContent`), `content.go:73` (`InstallGroupContent`) |
| `LocalContextFileName` | none in non-test code — confirmed unused as its own doc states |
| `WritesRepoLevelContext` | `internal/workspace/content.go:130` (`InstallRepoContentTo`), `internal/workspace/worktree_content.go:740` (`installWorktreeContextLayer`) |
| `AgentClaude` / `AgentCodex` (constants) | `internal/cli/dispatch.go:236` (refusal check), `internal/cli/dispatch_model.go` (category/name maps, lines 24,29,42,48,77), `internal/cli/instance_from_hook.go:499` (`applier.Agent = agent.AgentClaude`), `internal/cli/dispatch.go:26,351` (`knownModelHint`/`resolveDispatchModel` called with `agent.AgentClaude` explicitly) |

Files importing `internal/agent` (non-test): `internal/workspace/content.go`, `internal/workspace/root_materializer.go`, `internal/workspace/apply.go`, `internal/workspace/worktree_content.go`, `internal/cli/agent.go`, `internal/cli/dispatch.go`, `internal/cli/dispatch_model.go`, `internal/cli/instance_from_hook.go`.

Which encode the *exclusive* contract vs. which are genuine runtime choices:
- **Exclusive-by-construction:** `RootContextFileName`/`WritesRepoLevelContext` are binary switches — a single `Agent` value picks exactly one filename/behavior per call, and every write site in the materializer tree is called with the *same* resolved `Agent` value for the whole preparation (see §2). There is no code path today that calls these accessors twice with two different agents for one workspace preparation.
- **Genuinely runtime/session-scoped, not inherently exclusive:** `ResolveAgent`'s precedence chain (flag > env > workspace default > claude) is a "pick one agent for *this invocation*" resolver. Nothing about its signature stops it from being called once per launched agent rather than once per workspace; the exclusivity comes from `RootMaterializeOptions.Agent`/`Applier.Agent` being a single scalar field threaded through one materialize pass, not from `ResolveAgent` itself.

### 2. The full materialization path

**Context files, by level, with the exact write condition:**

| Level | File | Condition | Where |
|---|---|---|---|
| Workspace root | `{Agent}.RootContextFileName()` (`CLAUDE.md`/`AGENTS.md`) | Always, on `niwa init`/root materialize | `root_materializer.go:373-380` (`writeRootClaudeMD`) |
| Instance root | `{Agent}.RootContextFileName()` | Only if `cfg.Claude.Content.Workspace.Source != ""` (`content.go:29-31`) | `content.go:28-50` (`InstallWorkspaceContent`), called from `apply.go:1444` |
| Group dir | `{Agent}.RootContextFileName()` | Only if `cfg.Claude.Content.Groups[groupName].Source != ""` (`content.go:57-60`) | `content.go:56-87` (`InstallGroupContent`), called from `apply.go:1498` |
| Repo (clone) | `CLAUDE.local.md` (always this literal, never agent-switched) | Only if `ag.WritesRepoLevelContext()` is true (i.e. Claude); no-op under Codex | `content.go:127-217` (`InstallRepoContentTo`), guard at line 130-132, called from `apply.go:1521` |
| Worktree | `CLAUDE.local.md` (same literal) | Same `WritesRepoLevelContext()` guard | `worktree_content.go:736-742` (`installWorktreeContextLayer`), called from `ApplyToWorktree` step 4 (line 641) |

Quoted branch, repo level (`content.go:130-132`):
```go
if !ag.WritesRepoLevelContext() {
    return result, nil
}
```
Quoted branch, worktree level (`worktree_content.go:740-742`):
```go
if !ag.WritesRepoLevelContext() {
    return nil, nil
}
```
Quoted branch, filename selection (`root_materializer.go:375`):
```go
claudePath := filepath.Join(workspaceRoot, ag.RootContextFileName())
```

**What a `default_agent = "codex"` workspace produces today, file by file** (verified by reading the functional test `test/functional/features/codex-agent.feature:11-39`, which exercises exactly this and asserts the file list):
- `AGENTS.md` at the instance root (content-bearing, from `[claude.content.workspace]`).
- No `CLAUDE.md` at the instance root.
- No `tools/app/CLAUDE.local.md` and no `tools/app/AGENTS.md` inside the cloned repo — both assertions are explicit in the scenario (lines 38-39), confirming the repo level writes *nothing at all* under Codex, not even the Codex filename.
- `.claude/settings.local.json`, `.local.env`, hook scripts, and `[files]` distributions are unaffected by the agent discriminator — those materializers (`HooksMaterializer`, `SettingsMaterializer`, `EnvMaterializer`, `FilesMaterializer`) take no `Agent` parameter at all and run identically regardless of the selected agent (confirmed: `Materialize(ctx *MaterializeContext) ([]string, error)` signatures in `materialize.go` carry no agent field, and `MaterializeContext` itself has no `Agent` field).

**`.claude/settings.json` / `settings.local.json` construction** (this is the closest analog to a future Codex `config.toml`):
Built by `buildSettingsDoc` (`materialize.go:651-940`), called from `SettingsMaterializer.Materialize` (per-repo, writes `settings.local.json`) and from `writeRootSettings` (workspace-root, writes `settings.json`). The document assembles, key by key:
- `permissions` (defaultMode from `[claude.settings]`, or `deny` from worktree delegation) — lines 666-692.
- `remoteControlAtStartup` / `keepAliveOnDispatch` — boolean passthroughs from `[claude.settings]`, lines 694-724.
- `hooks` — from `InstalledHooks` (populated by `HooksMaterializer`), plus worktree-delegation hooks, work-summary default hooks, pr-body default hook, and ephemeral session-start hooks — lines 726-884.
- `env` — from `resolveClaudeEnvVars` (below) — lines 890-902.
- `includeGitInstructions` — optional passthrough — 904-907.
- `enabledPlugins` — from the effective `[claude.plugins]`/plugin list — 909-916.
- `extraKnownMarketplaces` — from `[claude.marketplaces]`, resolved via `mapMarketplaceSourceWithIndex` — 918-937.

None of this document construction is agent-aware; it is entirely Claude-Code-shaped (Claude Code JSON keys: `permissions`, `hooks`, `enabledPlugins`, `extraKnownMarketplaces`). A Codex analog (`config.toml`) has no equivalent builder today — `buildSettingsDoc` is the one function whose shape the new work will need to mirror or generalize.

### 3. The config surface (`internal/config`)

`WorkspaceMeta.DefaultAgent` — `internal/config/config.go:282-289`, quoted:
```go
// DefaultAgent is the workspace-default coding agent niwa prepares the
// workspace for (e.g. "claude" or "codex"). It is a session-global
// discriminator, not a per-repo mergeable value, so it lives here on the
// workspace metadata rather than in the [claude] override cascade. It is
// stored as a raw string (validated at resolution time by
// internal/agent.ParseAgent) so internal/config does not import
// internal/agent. Empty means the default agent (claude).
DefaultAgent string `toml:"default_agent,omitempty"`
```
It decodes as a raw string with no validation at parse time (confirmed by `internal/config/agent_test.go`, which asserts round-trip decode of `default_agent = "codex"` and that absence decodes to `""`); validation happens later, in `agent.ParseAgent`, invoked via `resolveSessionAgent` (`internal/cli/agent.go:16-22`).

`[claude.env]`, `[claude.env.vars]`, `[claude.env.secrets]`, top-level `[env.*]`: traced through `resolveClaudeEnvVars` (`materialize.go:961-1056`) and `ResolveEnvVars` (`materialize.go:1307-1436`).
- `[env].vars` / `[env].secrets` → resolved by `ResolveEnvVars`, written to the per-repo `.local.env` (or configured `env_output` target) by `EnvMaterializer.Materialize` (line 1516). Delivering function: `EnvMaterializer.Materialize`.
- `[claude.env].promote` (list of keys) → resolved via the `[env]` pipeline above, then overlaid; delivering function: `resolveClaudeEnvVars`, consumed by `SettingsMaterializer.Materialize` into the `env` block of `settings.local.json`.
- `[claude.env.vars]` (inline `map[string]MaybeSecret`) → same path, overlays promoted keys (materialize.go:1039-1050) — inline wins.
- None of these tables reach a Codex output today — there is no Codex env materializer; the whole env pipeline is Claude-shaped (`ctx.Effective.Claude.Env`, `.claude/settings.local.json`, `.local.env`) with no agent branch at all. This is a genuine gap for the additive-materialization work: today Codex gets **no** env/secrets output whatsoever, not even the `.local.env` file (the `EnvMaterializer` itself is unconditional — it runs for every repo with Claude enabled, agent-blind — but it writes no OPENAI_API_KEY-oriented output; the DESIGN doc (Decision 4) argues the secret table is "already agent-neutral" for the *input* side, but the *output* side — where a resolved secret lands for Codex to read — does not exist).
- I found no table that is parsed/validated but never materialized among the agent-adjacent config; `DefaultAgent` itself IS materialized (it drives the filename switch), so it is not a dead field.

### 4. The dispatch path

`internal/cli/dispatch.go:224-246` (quoted):
```go
// spawns the claude binary), so it refuses when the workspace's resolved
// agent is not Claude -- otherwise the instance would be prepared for another
...
    if resolvedAgent != agent.AgentClaude {
        return fmt.Errorf("niwa: error: niwa dispatch launches a Claude worker; this workspace's agent is %q, which background dispatch does not support yet. Set NIWA_AGENT=claude to dispatch a Claude worker, or wait for Codex background dispatch", resolvedAgent)
    }
```
A second, more explicit refusal-shaped comment lives in `instance_from_hook.go:489-499` (the SessionStart-hook provisioning path, which underlies both `niwa dispatch` and `niwa watch`), quoted:
```go
// This launch-coupled provisioning path (the Claude SessionStart hook,
// `niwa dispatch`, and `niwa watch`) always launches a Claude worker into the
// instance, so the instance is prepared for Claude regardless of the
// workspace's default_agent. Preparing it for a different agent here would
// materialize context the launched Claude worker cannot read. The
// interactive-Codex flow prepares its workspace through `niwa apply`/`create`
// (which honor default_agent), not through this path. `niwa dispatch` refuses
// up front when the resolved agent is not Claude; Codex background dispatch
// and a Codex SessionStart hook are later features that will carry their own
// agent here.
applier.Agent = agent.AgentClaude
```

What has to change vs. stay: the refusal is keyed on "the resolved *session* agent (flag/env/workspace-default) is not Claude" — under exclusive materialization this doubled as "the workspace was NOT prepared with Claude context." Once materialization is additive (both `CLAUDE.md` and a Codex home always exist), that premise breaks: `resolvedAgent != agent.AgentClaude` will still be true for a `default_agent = "codex"` workspace even though Claude context is now also present. The refusal's real job — "dispatch always launches a `claude` binary, so refuse if the operator's *chosen launch agent* for this dispatch is not Claude" — should key on the **launch-time selection** (the `--agent`/`NIWA_AGENT` override a caller passes to `dispatch`), not on the workspace's `default_agent`, since `default_agent` no longer means "the only agent this workspace has." The instruction the error already gives — `Set NIWA_AGENT=claude to dispatch a Claude worker` — is consistent with this: it is already telling the operator to override the *session* choice, which is exactly the right lever once every workspace is dual-capable. So the refusal's existence and message stay; only the semantic weight of what `resolvedAgent != agent.AgentClaude` means needs to be understood as "launch selection ≠ claude," which — because `ResolveAgent`'s precedence already treats `default_agent` as only the fallback rung below flag/env — should still resolve correctly as long as callers keep the ability to override with `--agent claude`/`NIWA_AGENT=claude` even in a codex-default workspace. In other words: the code likely needs **no logic change**, only re-justification, PROVIDED `ResolveAgent`'s override chain keeps working once the workspace stops being single-agent. This is worth a design decision rather than an assumption — flagged in Open Questions.

Also confirmed: `applier.Agent = agent.AgentClaude` at `instance_from_hook.go:499` hardcodes Claude for the SessionStart-hook/dispatch/watch launch path regardless of `default_agent` — this is INDEPENDENT of the exclusive-materialization problem and already behaves the way "launch-time agent choice" should: it always prepares Claude context for a Claude-launched worker. Under additive materialization this line does not need to change at all; it is already correct for its purpose (it is not asking "what is the workspace default," it is asserting "this specific launch is Claude").

### 5. Blast radius of going additive

**Unit tests that assert exclusivity (will need updating or will start failing):**
- `internal/workspace/content_test.go:159-183` — `TestInstallRepoContentTo...` (name inferred from context) explicitly asserts `assertNotExist(t, filepath.Join(repoDir, "AGENTS.md"))` under Codex — this assertion is the literal statement of "Codex writes nothing at repo level," which additive work does not necessarily change (repo-level AGENTS.md is still out of scope per the lead's framing — Codex context lives in CODEX_HOME) but the *root/instance* level tests will need the biggest rework.
- `internal/workspace/content_test.go:104-115` — table-driven test asserting `RootContextFileName()` returns `AGENTS.md` under Codex and (implicitly) `CLAUDE.md` under Claude as *mutually exclusive* outcomes for a single call — this pattern doesn't necessarily break (the accessor itself can stay), but any test asserting "and the OTHER file does not exist" will need a rewrite once both are written.
- `internal/workspace/content_test.go:187-225` (`TestContentTreesCoexist`) — already tests that a CLAUDE.md tree and an AGENTS.md tree "may coexist" — worth reading in full; this may already be partial groundwork for additive behavior and should be checked before assuming it needs rewriting.
- `internal/workspace/root_materializer_test.go:153-164` — table-driven, asserts `{"codex", agent.AgentCodex, "AGENTS.md", "CLAUDE.md"}` shape (file-that-should-exist, file-that-should-not-exist) — the second column ("file that should not exist") is precisely the exclusivity assertion that must flip.
- `internal/agent/agent_test.go` — asserts `RootContextFileName`/`LocalContextFileName` return exactly one filename per agent value; these accessor-level tests likely stay valid (the accessors are still meaningful; what changes is how many times materialization *calls* them per preparation), but any test asserting `ResolveAgent` selects a single agent for "the preparation" needs to be re-read against the new contract where `ResolveAgent` selects a single agent for "the launch," not "the preparation."
- `internal/cli/dispatch_model_test.go` — tests `resolveDispatchModel`/category maps per agent; these are keyed by agent already and are likely unaffected (they are inherently a launch-time, not materialization-time, concept).
- `internal/cli/agent_test.go` — tests `resolveSessionAgent` precedence; likely unaffected in mechanism, but its framing ("resolves the session-global coding agent") will need re-documentation once "session-global" no longer implies "the only agent materialized."

**Functional/e2e tests:**
- `test/functional/features/codex-agent.feature` — ALL THREE scenarios assert exclusivity directly:
  - Scenario 1 (line 11-39): asserts `CLAUDE.md` does NOT exist when `default_agent = "codex"` (line 37) — this line inverts under additive materialization.
  - Scenario 2 (line 41-59): the dispatch refusal scenario — needs re-verification against whatever the launch-time semantics become (see §4).
  - Scenario 3 (line 61-87): asserts `AGENTS.md` does NOT exist in the default (Claude) workspace (line 86) — this line inverts too, since additive materialization means a Claude-default workspace ALSO gets a Codex home.
- No other `.feature` files reference `default_agent`, `codex`, or `AGENTS.md` (confirmed by grep across `test/functional/features/`).

**Docs that describe the exclusive behavior (checklist for the implementation):**
- `internal/workspace/scaffold.go:17-20,62` — the `niwa init --bootstrap` scaffold template's own comment: "default_agent selects the coding agent niwa prepares the workspace for. ... AGENTS.md instead." — this is user-facing generated content and will need rewording once both are always prepared.
- `docs/designs/current/DESIGN-interactive-codex-session.md` — the full design doc; Decision 2, 3, and the "Negative/limitations" section (quoted below) all describe the exclusive/deferred model as intentional and will need either superseding or an explicit "supersedes §X" note.
- `docs/prds/PRD-interactive-codex-session.md` and `docs/briefs/BRIEF-interactive-codex-session.md` — not read in full but exist as upstream-chain artifacts for the same slice; likely reference the same exclusive framing (not verified line-by-line — flagged as unread in Open Questions).
- `docs/designs/current/DESIGN-workspace-root-claude.md` — appears in the AGENTS.md grep hit list; not read in full (Open Question).
- `docs/guides/vault-integration.md` — appears in the AGENTS.md grep hit list; likely documents `OPENAI_API_KEY` binding per Decision 4; not read in full (Open Question).
- `test/functional/features/codex-agent.feature` itself carries a `Design:` pointer to the DESIGN doc and prose describing the exclusive contract (lines 1-6) that needs rewriting alongside the scenarios.

**Non-test/doc callers that assume exclusivity by construction (not by assertion):** every write site enumerated in §2 assumes it is called exactly once per preparation with one `Agent` value. Making materialization additive means each of these call sites (root/group/instance CLAUDE.md write, plus a new Codex-home write) needs to run TWICE — once per agent — rather than being parameterized by a single resolved `Agent`. This is a structural change to the materializer entry points (`MaterializeWorkspaceRoot`, `InstallWorkspaceContent`, `InstallGroupContent`, and the `Applier.Agent` field itself), not just a test-fixing exercise.

### 6. Confirm or refute the gitexclude/collision-guard reasoning

**`internal/gitexclude`** (`internal/gitexclude/exclude.go:1-35`): package doc says it "records niwa's ignore coverage in a managed repository's `.git/info/exclude` so niwa-authored files stay invisible to the repository's git status." Its managed pattern set is exactly `{"*.local*", ".niwa/"}` (line 35) — it does NOT cover `AGENTS.md` today, and was never asked to: every niwa-authored repo-level file carries a `.local` infix by construction (see `injectLocalInfix`/`localRename` in `materialize.go`), which is precisely the mechanism that makes the base pattern sufficient. `AGENTS.md` has no `.local` infix, so if niwa ever wrote one into a repo, it WOULD need a `gitexclude` extension — the reasoning that this becomes unnecessary is only true because no code path writes `AGENTS.md` into a repo.

**Does any code path still write `AGENTS.md` into a repo working tree?** No. Confirmed by:
- `WritesRepoLevelContext()` returning `false` for Codex is the single gate; both call sites that would write repo-level context (`InstallRepoContentTo` at `content.go:130-132`, `installWorktreeContextLayer` at `worktree_content.go:740-742`) check it and return early. There is no other write site targeting a repo/worktree directory with an agent-derived filename — `LocalContextFileName()` (which WOULD return `AGENTS.md` for Codex) is called from zero non-test locations (confirmed by grep — see §1 table).
- The functional test explicitly asserts this (`codex-agent.feature:39`): `And the file "tools/app/AGENTS.md" does not exist in instance "ws"`.

So the reasoning holds **today**, and holds by construction as long as the design direction keeps repository-level content out of scope (Codex context lives in `CODEX_HOME`, per the lead's framing, not inside cloned repos). The reasoning would break the instant any future work adds a repo-level Codex write — at which point both deferred mechanisms (gitexclude extension, collision guard) become necessary again, exactly as `DESIGN-interactive-codex-session.md`'s Option 3B analysis (quoted below) already anticipated.

**Does any repo in this workspace actually have a committed `AGENTS.md`?** **YES — this is the one place the reasoning needs a sharper statement, not a correction.** `public/shirabe/AGENTS.md` is a real, committed file (`git log -1 -- AGENTS.md` returns commit `8e07f07a...` in the `shirabe` repo, remote `git@github.com:tsukumogami/shirabe.git`). It coexists with `shirabe`'s own `CLAUDE.md` at the repo root — both files are present side by side, serving different agents' conventions (`CLAUDE.md` for the repo's full workflow/skill conventions, `AGENTS.md` for a narrower "eval requirement" rule addressed to "any agent working on this repo"). This is exactly the collision scenario `DESIGN-interactive-codex-session.md` Option 3B calls out as "real in practice — repositories in a typical workspace ship their own AGENTS.md" (quoted in full below), now confirmed with a concrete instance rather than a hypothetical. It does NOT refute the reasoning that the two deferred mechanisms are unnecessary — since niwa still writes no `AGENTS.md` into any repo — but it strengthens the case that if a future increment ever does add repo-level Codex writes, the collision guard is not a theoretical nicety; `shirabe` would break on day one without it.

**Residual case the reasoning misses:** none identified for the *current* scope (workspace/instance/group levels + `CODEX_HOME`, no repo writes). The one edge worth naming: the workspace ROOT and GROUP directories are asserted to be "non-git" (`DESIGN-interactive-codex-session.md` Decision 3: "The workspace-root and group directories are niwa-owned and are not git repositories, so an `AGENTS.md` there cannot collide"). This holds structurally — `niwa init` scaffolds these as plain directories, never `git init`s them — but was not independently re-verified in this pass beyond reading the design doc's own claim; flagged in Open Questions.

### 7. Backward compatibility for `default_agent`

- **This workspace's own config** (`/home/dgazineu/dev/niwaw/tsuku/.niwa/workspace.toml`): grepped for `default_agent`/`agent` — no match. The file does not set `default_agent` at all, so it is not exercising the codex path today.
- **`public/dot-niwa` repo** (the reference config repo this niwa instance ships as an example): grepped for `default_agent`/`AGENTS.md` — no match. It carries no `default_agent` setting either.
- So within the visible instances of this workspace, `default_agent` is not set anywhere in the wild — it is a shipped, tested capability (via `codex-agent.feature`) with (as far as this pass found) zero live adopters yet.

**What "handle it compatibly" has to mean concretely:** since nobody's config currently sets `default_agent = "codex"`, there is no live config whose *meaning* would change if the key's semantics shift from "the only agent" to "the launch-time default when no `--agent`/`NIWA_AGENT` override is given." The compatibility obligation is narrower than "don't break existing configs" — it is "keep the key's existing *values* (`""`, `"claude"`, `"codex"`) meaningful under the new contract," which the current design already supports structurally: `ResolveAgent`'s precedence (flag > env > workspace default > claude) is unchanged by additive materialization — it still answers "which agent does THIS launch prefer," which remains a coherent question once both trees always exist. A workspace that already sets `default_agent = "codex"` (if one exists outside this visible workspace) would, post-change, gain a `CLAUDE.md` tree it did not have before — additive, not breaking, but worth flagging in release notes since it changes on-disk output for anyone who set the key expecting exclusivity (the CURRENT documented/tested contract, per `codex-agent.feature` and the DESIGN doc).

### 8. The repo's conventions for this kind of change

`docs/designs/current/DESIGN-interactive-codex-session.md` (547 lines) is confirmed as the direct predecessor design and the right structural template: it follows an MADR-like shape — `Status` → `Context and Problem Statement` → `Decision Drivers` → `Considered Options` (per-decision, each with chosen/rejected options and stated trade-offs) → `Decision Outcome` → `Solution Architecture` → `Implementation Approach` → `Security Considerations` → `Consequences` (Positive/Negative-limitations/Neutral). Five explicit lettered "Decision N" sections, each with 2-3 lettered options and a chosen one marked `(chosen)`.

Upstream artifacts for the same slice exist at `docs/prds/PRD-interactive-codex-session.md` (376 lines) and `docs/briefs/BRIEF-interactive-codex-session.md` (155 lines) — the full BRIEF → PRD → DESIGN chain this repo's tactical scope uses (per `CLAUDE.md`: "Default Scope: Tactical").

Test layout: unit tests colocated (`*_test.go` beside source), functional/Gherkin tests under `test/functional/features/*.feature` run via `make test-functional`/`make test-functional-critical`, with `@critical` tags marking scenarios that gate CI (all three `codex-agent.feature` scenarios carry `@critical`). `docs/guides/functional-testing.md` documents the pattern and the `localGitServer` fixture helper used throughout the feature file read here.

## Implications

The single biggest structural implication: **every write site currently takes one resolved `agent.Agent` value and writes for it.** Going additive is not a matter of changing `RootContextFileName()`'s return value or `WritesRepoLevelContext()`'s boolean — those accessors are fine as-is and can be called for either agent value independently. The change is that the *callers* (`MaterializeWorkspaceRoot`, `InstallWorkspaceContent`, `InstallGroupContent`, and whatever new `CODEX_HOME` writer gets built) need to run once per agent that should be materialized, not once for "the" resolved agent. `Applier.Agent agent.Agent` (a scalar field, `apply.go:49`) is the load-bearing type that will likely need to become a set/slice, or the apply pipeline needs to call the materialize functions twice (once per always-materialized agent), leaving `agent.Agent` as purely a *launch-time* selector consumed only by `resolveSessionAgent`/`ResolveAgent`/`resolveDispatchModel`/the dispatch refusal — never by the materializer entry points.

The `.claude/settings.json`/`settings.local.json` builder (`buildSettingsDoc`) is Claude-Code-shaped end to end and has no Codex analog; a `CODEX_HOME`/`config.toml` equivalent is net-new work, not a generalization of the existing builder (the JSON keys — `permissions`, `enabledPlugins`, `hooks` — are Claude Code API surface, not portable concepts).

The env/secrets pipeline (`ResolveEnvVars`/`resolveClaudeEnvVars`) is entirely under `ctx.Effective.Claude.*` and has no branch for a Codex-bound secret today; `OPENAI_API_KEY` is documented as bindable via the existing agent-neutral `map[string]MaybeSecret` INPUT table (Decision 4 of the prior design), but there is no OUTPUT delivery path to a Codex-readable location yet — this is a real gap distinct from the context-file gap, and worth scoping explicitly (the lead's exploration may already be covering this via `lead-openai-key`).

The dispatch refusal (`resolvedAgent != agent.AgentClaude`) most likely needs zero code changes if `ResolveAgent`'s precedence keeps working as a pure launch-time selector — but this deserves explicit design confirmation, not an assumption, because its current doc comments (`dispatch.go:224-225`, `instance_from_hook.go:489-499`) narrate the OLD framing ("the workspace's resolved agent," "prepared for another agent") and will read as stale/misleading once materialization is additive, even if the logic itself is correct.

## Surprises

1. **The map was accurate on every substantive point checked.** No part of the lead's summary of the prior increment was wrong.
2. **`LocalContextFileName()` and `ParseAgent` are exported but effectively dead code outside their own package/`ResolveAgent`.** `LocalContextFileName` has zero non-test callers (explicitly documented as "provisional seam-completeness... currently unused" in its own doc comment); `ParseAgent` is only called from within `ResolveAgent`. Worth knowing before assuming every exported symbol needs a call-site audit — two of the seven don't have one yet.
3. **A committed `AGENTS.md` exists in this very workspace** (`public/shirabe/AGENTS.md`), turning the design doc's hypothetical ("repositories in a typical workspace ship their own AGENTS.md") into a concrete, checkable fact. It does not change any conclusion (niwa still writes no repo-level AGENTS.md), but it means the collision-guard deferral was not academic — a real collision is one repo-level Codex write away.
4. **`default_agent` has no live adopters in the visible workspace instances checked** (neither this workspace's `.niwa/workspace.toml` nor `public/dot-niwa`). The backward-compatibility surface for this change may be smaller than the lead's framing implied — worth confirming there isn't a private-overlay config setting it, which this pass could not check (visibility scope: public only).
5. **The Codex `LocalContextFileName` accessor already anticipates a *different* filename decision for a future repo-level Codex write** (`AGENTS.md`, same as root level) — but the design doc's Option 3B explicitly names this as requiring new mechanisms first. If the CODEX_HOME direction permanently removes repo-level Codex writes from scope (per the lead's framing), `LocalContextFileName` may become permanently dead rather than "provisional," which is worth flagging to whoever owns the seam's cleanup.

## Open Questions

1. Does `ResolveAgent`'s override chain (flag > env > workspace default > claude) still let an operator dispatch a Claude worker from a `default_agent = "codex"` workspace once "default_agent" no longer means "the only agent"? This affects whether the dispatch refusal at `dispatch.go:236` needs a doc-comment-only update or an actual logic change — traced as "probably comment-only" in this pass but not confirmed against the design's intended launch-time semantics.
2. `PRD-interactive-codex-session.md` and `BRIEF-interactive-codex-session.md` were not read in this pass (found via grep, not opened) — they likely restate the exclusive framing and should be checked for anything DESIGN doesn't already cover (e.g. PRD "Known Limitations," referenced at `DESIGN-interactive-codex-session.md`'s "tracked removal is deferred (PRD Known Limitations)" line).
3. `docs/designs/current/DESIGN-workspace-root-claude.md` was found via the AGENTS.md grep but not read — unclear whether it documents workspace-root non-git-directory invariants relevant to Decision 3's "cannot collide" claim (§6).
4. `docs/guides/vault-integration.md` was found via the AGENTS.md grep but not read — likely documents the `OPENAI_API_KEY` binding from Decision 4; relevant to whoever scopes the env/secrets-delivery gap noted in §3/Implications.
5. Whether a private-overlay or any repo outside the `public/` visibility scope sets `default_agent` was not checked (out of scope per the visibility instruction) — worth a private-side pass before finalizing the backward-compatibility story.
6. Whether the workspace-root and group directories are unconditionally non-git (Decision 3's premise for "cannot collide") was taken from the design doc's own claim, not independently re-derived from `niwa init`/scaffold code in this pass.

## Summary

The prior increment (PR #208) built exactly what the map described: a leaf `internal/agent` package with a closed `Agent` type, filename accessors, and a `ResolveAgent` precedence resolver, threaded through one `Agent` field per preparation (`Applier.Agent`, `RootMaterializeOptions.Agent`, `WorktreeApplyOptions.Agent`) so a single call decides `CLAUDE.md` vs. `AGENTS.md` at the workspace/instance/group levels and skips repository/worktree-level writes entirely under Codex — verified against the code, the functional test (`codex-agent.feature`), and the design doc's own "Decision 3" rationale, which explicitly named the gitexclude-extension and collision-guard mechanisms as deferred rather than unnecessary. Making materialization additive is structurally a matter of turning every "one resolved agent → one write" call site into "one write per always-materialized agent," while keeping `Agent` alive as a pure launch-time selector for dispatch/model-resolution/session-agent choice; the collision-guard reasoning holds today (no code path writes repo-level `AGENTS.md`) but is not hypothetical — `public/shirabe/AGENTS.md` is a real, committed collision candidate the moment any future work adds that write. The biggest open question is whether the dispatch refusal needs a logic change or only a re-narrated doc comment once "the workspace's agent" stops meaning "the only agent it has."
