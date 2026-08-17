# Lead: How large is the agent-neutral config rename?

## A. Blast Radius (with counts)

Four Go types carry the "Claude" name, not three -- the brief's list missed
`OverlayClaudeConfig`, which is a distinct, narrower shape used only by
`WorkspaceOverlay`:

| Type | Definition | Non-test refs | Test refs |
|---|---|---|---|
| `ClaudeConfig` | `internal/config/config.go:28` | 26 | 100 |
| `ClaudeOverride` | `internal/config/config.go:69` | 12 | 25 |
| `ClaudeEnvConfig` | `internal/config/config.go:232` | 11 | 37 |
| `OverlayClaudeConfig` | `internal/config/overlay.go:86` | 4 | 10 |
| **Total (type-name greps)** | | **53** | **172** |

These counts are `grep -rn "<TypeName>"` over `internal/`, non-test and
`*_test.go` files split, excluding neither definitions nor doc comments.

**Six embedding sites**, all `toml:"claude"` or `toml:"claude,omitempty"`:

- `WorkspaceConfig.Claude ClaudeConfig` -- `config.go:245`
- `InstanceConfig.Claude *ClaudeOverride` -- `config.go:260`
- `RepoOverride.Claude *ClaudeOverride` -- `config.go:418`
- `GlobalOverride.Claude *ClaudeOverride` -- `config.go:664` (reached from `GlobalConfigOverride` only indirectly, through its `Global GlobalOverride` and `Workspaces map[string]GlobalOverride` fields -- `GlobalConfigOverride` itself, `config.go:681`, has no `Claude` field of its own)
- `WorkspaceOverlay.Claude OverlayClaudeConfig` -- `overlay.go:22` (note: typed `OverlayClaudeConfig`, not `ClaudeOverride`)
- `workspace.EffectiveConfig.Claude config.ClaudeConfig` -- `internal/workspace/override.go:18`

**Broader field-access grep** (`\.Claude\b`, catches every read/write of any
of these six fields regardless of which type name appears on the line):
251 non-test occurrences, 157 test occurrences, across 17 non-test files.

**Raw "Claude" substring count per production file** (includes doc comments,
not just code -- an order-of-magnitude blast-radius signal, not a diff-line
count):

| File | Count |
|---|---|
| `internal/workspace/override.go` | 203 |
| `internal/workspace/workspace_context.go` | 33 |
| `internal/config/config.go` | 29 |
| `internal/workspace/materialize.go` | 25 |
| `internal/workspace/root_materializer.go` | 22 |
| `internal/watch/trust.go` | 22 |
| `internal/workspace/apply.go` | 20 |
| `internal/vault/resolve/resolve.go` | 13 |
| `internal/workspace/required.go` | 13 |
| `internal/workspace/worktree_content.go` | 11 |
| `internal/workspace/shadows.go` | 11 |
| `internal/config/overlay.go` | 10 |
| `internal/config/validate_vault_refs.go` | 10 |
| `internal/vault/resolve/deepcopy.go` | 10 |
| `internal/workspace/scaffold.go` | 3 |
| `internal/workspace/content.go` | 5 |
| `internal/cli/status_audit.go` | 5 |
| `internal/guardrail/githubpublic.go` | 5 |
| `internal/workspace/env_example_prepass.go` | 1 |
| **Total** | **~451** |

`override.go` alone is ~45% of the total -- it holds every merge/copy
function named in the brief: `MergeOverrides` (:46), `MergeInstanceOverrides`
(:177), `MergeGlobalOverride` (:494), `MergeWorkspaceOverlay` (:708),
`deepCopyRepoOverride` (:1013), `copyClaudeConfigFull` (:1041),
`ClaudeEnabled` (:1061), `copyClaudeEnv` (:1212) -- eight functions, all
touching one or more of the Claude-named fields.

Bottom line on size: renaming the *type names* is a ~53-site non-test /
~172-site test mechanical rename (safe, `gofmt`+compiler-checked). Renaming
the *TOML surface* (what users write in `workspace.toml`) is the part that
needs an alias, and that touches all six embedding sites plus every read
site downstream of them -- realistically the full ~451-occurrence footprint,
because a table-level alias requires a parallel "old" and "new" field at
every position where `[claude]`/`[repos.*.claude]`/`[global.claude]`/
`[workspace-overlay.claude]` can appear, not just one.

## B. Alias Mechanism Fitness

The `[content]` -> `[claude.content]` precedent (`config.go:513-528`, cited
in round 1) works like this, precisely:

```go
legacyHasContent := !isContentConfigZero(cfg.Content)
canonicalHasContent := !isContentConfigZero(cfg.Claude.Content)
switch {
case legacyHasContent && canonicalHasContent:
    return nil, fmt.Errorf("config uses both [content] and [claude.content]; ...")
case legacyHasContent:
    cfg.Claude.Content = cfg.Content
    cfg.Content = ContentConfig{}
    warnings = append(warnings, "[content] is deprecated; use [claude.content] instead (removed at v1.0)")
}
```

`isContentConfigZero` (`config.go:552+`) is a **hand-written, field-by-field
zero check** -- not a Go `==` comparison, because `ContentConfig` contains
maps (`Groups map[string]ContentEntry`, `Repos map[string]RepoContentEntry`),
which panic on `==`. This detail matters directly for generalizing the
mechanism: `ClaudeConfig` also contains `HooksConfig` (`map[string][]HookEntry`,
`config.go:367`), `SettingsConfig` (`map[string]MaybeSecret`, `config.go:381`),
and `MarketplaceConfigs` (a slice) -- so a whole-table `[claude]` alias would
need its own bespoke `isClaudeConfigZero` covering every field, not a one-line
comparison. That function is buildable (it's the same pattern, just longer),
but it's real code, and it has to be re-derived (or shared) for `ClaudeOverride`
too if repo/instance/global override positions get the same treatment,
because `ClaudeOverride` is a structurally different, narrower type from
`ClaudeConfig` (no `Content`, no `Marketplaces`) by deliberate design
(`config.go:63-68`, to keep override-position `[claude.content]` surfacing
as an unknown-field warning rather than silently parsing and being dropped).

**Does the existing mechanism generalize from one key to a whole table?**
Structurally yes -- same pattern (sibling field, zero-check, hard-error-on-both,
warn-on-old) -- but it was never exercised at table granularity, and table
granularity multiplies the work in two ways the single-key case didn't have:
(1) the zero-check has to cover N fields instead of 4, and (2) it has to be
implemented separately at *every* embedding site (`WorkspaceConfig`,
`InstanceConfig`, `RepoOverride`, `GlobalOverride`, `WorkspaceOverlay`) because
each is a distinct Go type with its own `Parse`/`Unmarshal` call site, not one
shared decode path. The `[content]` precedent only ever had to solve this once,
at `WorkspaceConfig` level, because `Content` was workspace-scoped only.

**BurntSushi/toml capabilities that could help, and why the codebase already
rejected the fancier one:** `toml.MetaData.Undecoded()` is already in use
(`config.go:545`, and again at `rejectWorkerSpawnCommandKey`,
`validateReservedEnvKeys`) purely to warn about genuinely unknown fields
system-wide -- it is not, and per `DESIGN-claude-key-consolidation.md:223-243`
(cited by round 1) was **deliberately not used**, for alias detection. The
design doc's own reasoning: a metadata walk to detect "was `[content]`
present in the source" is more moving parts for no behavioral gain over a
struct-level sibling field, since Go's zero value already distinguishes
"present but empty" from "absent" closely enough for this use case (an empty
`[content]` table and no `[content]` table both mean "nothing to migrate").
`toml.Unmarshaler` (custom `UnmarshalTOML`) is already used elsewhere in this
file for `MarketplaceConfigs` (`config.go:113`) to support two authoring
syntaxes for one field -- that's a proven pattern for "accept two shapes for
one key," but it operates within a single field's decode, not across two
different top-level table names, so it doesn't shortcut the table-rename
problem either. No BurntSushi feature turns a table-level rename into a
single mechanical step; the sibling-field-plus-zero-check pattern is still
the right tool, it just has to be applied at up to five embedding sites
instead of one.

## C. Warning Surface

`config.Parse` returns `*ParseResult{Config, Warnings []string}`
(`config.go:511-549`). Warnings are plain strings, not a typed/leveled
channel. Four call sites consume `result.Warnings`:

- `internal/cli/apply.go:188` -- `for _, w := range result.Warnings { fmt.Fprintf(os.Stderr, "warning: %s\n", w) }`
- `internal/cli/create.go:179` -- same pattern
- `internal/cli/reset.go:119` -- same pattern
- `internal/cli/instance_from_hook.go:458` -- explicitly **skips** this loop, with a comment noting the asymmetry (hook-invoked flow has no warning surface at all)

None of these route through `internal/workspace/reporter.go`'s `Reporter`
type, which has its own `Warn`/`DeferWarn`/`FlushDeferred` machinery used
elsewhere in the codebase for spinner-aware, deferred terminal output. Config
deprecation warnings bypass that entirely and print straight to `os.Stderr`
with a bare `"warning: %s\n"` prefix, in whatever order `Parse()` appended
them. Separately, `installer.go`/`content.go`'s `InstallRepoContent` result
carries its own `Warnings []Warning` (a different type, with a `.String()`
method, collected into `allWarnings` in `apply.go:1525`) -- that is a
second, unrelated warning list for content-installation-time issues (missing
source files etc.), not config-parse-time deprecation notices. A table-level
`[claude]` rename's deprecation warning would ride the same plain-stderr
path the `[content]` rename already uses -- no new plumbing needed, but also
no improvement on visibility (it's easy to miss in a busy `niwa apply` run,
and `instance_from_hook.go`'s path drops it silently).

## D. Per-Field Verdicts

`ClaudeConfig` (workspace-level) and `ClaudeOverride` (override-level) share
`Enabled`, `Plugins`, `Hooks`, `Settings`, `Env`; `ClaudeConfig` additionally
has `Marketplaces`, `WorkSummaryHooks`, `PrBodyHook`, `Content`.

| Field | Verdict | Reason |
|---|---|---|
| `Enabled` (`claude.enabled`) | **Needs a decision, lean rename in PR 2** | Correctly named today -- gates only genuinely Claude-Code artifacts (CLAUDE.local.md, hooks, settings materializers, per `ClaudeEnabled()` doc comment, `override.go:1061`). Becomes the mandate's exact cautionary example the moment a second agent has repo-level gated delivery. Neutral name candidate: fold into a per-agent gate structure rather than a single renamed key (e.g. delivery becomes conditional on which artifacts a given agent's plan produces, not on one boolean) -- a straight rename to `content.enabled` or similar just moves the same problem under a different label unless the gate itself is restructured. |
| `Plugins` | **Keep Claude-named** | Writes `enabledPlugins` into Claude Code's `settings.json`; this is Claude Code's own plugin-enablement key, not a niwa concept. |
| `Marketplaces` | **Keep Claude-named** | Same reasoning -- Claude Code plugin marketplace registration format. The Codex skills dependency round 1 found (`codexMarketplaceRoots` pointing at `~/.claude/plugins/marketplaces/...`) is a fetch-path bug to fix by resolving into a niwa-owned directory, not evidence the config key itself is misnamed -- marketplace *registration* is still a Claude Code plugin-system concept per round 1's spike findings ("cannot come from the project layer" for other agents anyway). |
| `Hooks` | **Keep Claude-named for now, revisit if a Codex hook analog ships** | `HooksConfig` matches Claude Code's PreToolUse/PostToolUse matcher shape. Round 1's spike found "no demonstrated route" for Codex hooks that avoids the trust-hash/modal problem, so there is no second implementation to be agent-neutral *for* yet. Renaming ahead of a real second consumer just relabels a still-single-agent concept. |
| `Settings` | **Keep Claude-named** | `SettingsConfig` is an arbitrary key/value pass-through into Claude Code's `settings.json`, including `RemoteControlAtStartupKey`/`KeepAliveOnDispatchKey`, which are documented as literally Claude Code's own settings keys (`config.go:390-400`). This is a settings-file-format binding, not a niwa concept to neutralize. |
| `Env` (`ClaudeEnvConfig`: promote/vars/secrets) | **Keep Claude-named** | Round 1 already found the precise reason: it writes into Claude Code's `settings.local.json` `env` block, which "has no record form (it is a Claude Code file, not a niwa one)" (`materialize.go:1044` comment). The already-agent-neutral `[env]` table (`EnvConfig`) is the sibling that's correctly separate and correctly un-gated by `ClaudeEnabled`. Codex's analogous need (`shell_environment_policy.set`, per round 1's gap list) is a different config shape entirely, not a rename target of this field. |
| `WorkSummaryHooks` | **Keep Claude-named, mechanism-bound** | Off-switch for three specific Claude Code hook injections (PostToolUse capture, UserPromptSubmit, SessionStart compact). The *desire* ("capture a session work summary") is agent-neutral, but the field controls a Claude-Code-hook-shaped delivery mechanism that has no Codex equivalent today -- same reasoning as `Hooks`. |
| `PrBodyHook` | **Keep Claude-named, mechanism-bound** | Off-switch for one specific Claude Code PreToolUse hook (`shirabe pr-body-hook` gate). Same reasoning as `Hooks`/`WorkSummaryHooks`. |
| `Content` (`[claude.content]`) | **Rename to agent-neutral -- strongest candidate** | This is the one field whose *consumers* are already agent-neutral: `agent.Agent`'s `RootContextFileName()`/`LocalContextFileName()` (round 1, `lead-capability-inventory`/`lead-prep-path-map`) already branch per-agent on what file this content becomes (CLAUDE.md vs. an eventual AGENTS.md-equivalent). Keeping the *source* of that content namespaced under `[claude]` while the *destination* is already agent-parametric is the exact inversion the mandate describes. This is also the field the codebase's own design doc already flagged as unfinished business: `WorkspaceMeta.ContentDir` (a sibling, top-level `content_dir` key, not part of `ClaudeConfig`) was identified in `DESIGN-claude-key-consolidation.md` Decision 3 as "the same implicit coupling as `[content]`" and its rename was explicitly deferred, never completed (round 1 finding). Any rename effort should treat `[claude.content]` and `content_dir` as one unit of work, not `[claude.content]` alone. |

`OverlayClaudeConfig` mirrors `Hooks`/`Settings`/`Content`/`Marketplaces`/
`Plugins` at the overlay layer with the identical verdicts as their
`ClaudeConfig`/`ClaudeOverride` counterparts -- no independent judgment
needed there.

## E. PR Assignment Recommendation

Don't put a `[claude]` table-wide rename in either PR. Land only the two
fields with a real verdict-to-rename (`Content`/`content_dir`, and
`Enabled` if its gate gets restructured rather than just relabeled), and
put both in **PR 2**, not PR 1.

Reasoning from the measurements above:

- **PR 1 must be behavior-preserving with no user-facing config diff.** A
  compatibility alias is behavior-preserving at parse time (old keys keep
  working), but it is not a no-diff change: it adds new warning text users
  see on every `niwa apply`/`create`/`reset` that still uses the old key,
  changes `DESIGN-workspace-config.md`, the README, and
  `internal/workspace/scaffold.go`'s generated `workspace.toml` (round 1),
  and forces every author of a workspace.toml example to migrate on some
  schedule. That's real user-facing surface for a PR whose whole job is to
  be invisible.
- **The full-table rename is disproportionate to what's actually mis-scoped.**
  Per Part D, six of nine `ClaudeConfig` fields (`Plugins`, `Marketplaces`,
  `Hooks`, `Settings`, `Env`, `WorkSummaryHooks`, `PrBodyHook`) are correctly
  Claude-named today and should stay that way until a second agent actually
  needs an analogous mechanism -- renaming them now is speculative and, per
  round 1's `content_dir` precedent, this codebase's own history shows
  deferred-but-planned renames just don't get picked back up. Doing the
  whole table means writing (and maintaining, until v1.0) alias plumbing for
  six fields that don't need it.
- **The two fields that do need it are cheap in isolation and expensive
  entangled.** `Content`/`content_dir` is a single-field, single-embedding-
  site rename -- structurally identical to the proven `[content]` precedent,
  reusing the same `isContentConfigZero`-style function, at the
  `WorkspaceConfig` level only (Content is workspace-scoped, not present at
  override positions). That's a half-day-scale change, not a table-scale one.
  `Enabled` is not a rename at all if it's fixed correctly -- Part D's
  conclusion is that relabeling `claude.enabled` to some other single name
  reproduces the same mis-gating shape under new spelling; the real fix is
  restructuring what the gate governs, which is a PR 2 design decision (what
  does Codex's repo-level delivery gate look like) that can't be resolved
  until PR 2 defines Codex's delivery plan.
- **Both remaining candidates are motivated by PR 2, not PR 1.** `Content`
  only needs to move out from under `[claude]` once something agent-neutral
  actually reads it *as* agent-neutral config -- today `RootContextFileName`/
  `LocalContextFileName` already do the per-agent branching in Go, so the
  TOML source table being named `[claude]` is inert-but-wrong, not yet
  actively misleading. It becomes actively misleading (a real misnamed gate,
  not just a naming inconsistency) exactly when PR 2 wires Codex through the
  same content pipeline. `Enabled` is explicitly PR 2's cautionary example
  per round 1. Doing either rename in PR 1 buys nothing for PR 1's own goal
  (structure without behavior change) and pre-empts a design decision PR 2
  hasn't made yet.

Net: PR 1 ships zero config renames. PR 2 ships the `Content`/`content_dir`
alias (mechanical, low-risk, reuses the proven pattern) and resolves
`claude.enabled` as part of designing Codex's delivery gate (not a pure
rename -- a restructure that may or may not keep the TOML key name
`claude.enabled` as a Claude-specific sub-gate nested under a broader
agent-neutral one). The other six `ClaudeConfig`/`ClaudeOverride` fields
stay Claude-named in both PRs; nothing found in this pass argues for
touching them.

## Open Questions

- If `Enabled`'s fix is a gate restructure rather than a rename, what does
  the restructured shape look like -- a per-agent-plan-derived gate (no
  config key at all, computed from what artifacts a given agent's delivery
  plan would write) or a genuinely renamed-and-generalized boolean that both
  agents' delivery mechanisms check? This wasn't resolved here and blocks
  sizing `Enabled`'s PR 2 work precisely.
- Should `isClaudeConfigZero`-style helpers be written once and shared
  between `ClaudeConfig` and `ClaudeOverride`, or does the type-level
  difference between them (Content/Marketplaces present on one, absent on
  the other) mean they need separate, non-shared zero-check functions? Not
  investigated here since no rename needs a whole-`ClaudeConfig` zero-check
  given the E recommendation only moves `Content`, which already has
  `isContentConfigZero`.
- The warning surface gap found in Part C (`instance_from_hook.go` silently
  drops config warnings) is a pre-existing bug independent of any rename --
  worth flagging to whoever owns the rename PR, since a new deprecation
  warning added there would be invisible on that one code path exactly like
  the `[content]` one already is.

## Summary

The `[claude]` table's true footprint is four Go types (the brief missed
`OverlayClaudeConfig`), six embedding sites, and roughly 450 "Claude"
occurrences across 19 production files (`override.go` alone is ~45% of
that), but per-field analysis shows only two of nine `ClaudeConfig` fields
actually warrant an agent-neutral rename today: `Content`/`content_dir`
(whose consumers are already agent-parametric in Go while the TOML source
stays Claude-namespaced) and `Enabled` (which needs a gate restructure, not
just a relabel). The proven `[content]`->`[claude.content]` alias mechanism
(sibling field + hand-written zero-check + hard-error-on-both + warn-on-old,
`config.go:513-528`) generalizes cleanly to a single field but multiplies
per embedding site for a whole-table rename, so a full `[claude]` rename is
disproportionate; both real candidates belong in PR 2, where a second
agent's delivery plan first gives them something to actually mis-gate, not
in PR 1, which needs to stay free of user-facing config diffs.
