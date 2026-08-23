# Lead: Once niwa delivers plugin skills at the instance root for Codex, what exactly remains undelivered there — and what must change in the two places that currently describe the gap in prose?

## Findings

### 1. Row-by-row inventory: what a root-started Codex session receives, today and after row 18 flips

Source of truth: `internal/agentplan/declaration.go:76-372` (24 rows, Codex column). I group by whether the row is (a) agent-neutral and lands regardless of where a session starts, (b) delivered only inside a cloned repo or worktree, or (c) not a Codex concept at all.

| Row | Capability | Codex state today | Reaches a root-started Codex session? | After row 18 flips |
|---|---|---|---|---|
| 1 | WorkspaceOrientation | Implemented | Reaches repo sessions via the composed repo doc (`declaration.go:79-84`); at root its content is folded into row 2's document instead | unchanged |
| 2 | RootSessionOrientation | Implemented | **Yes** — root `AGENTS.md` (`declaration.go:87-107`) | unchanged |
| 3 | RepoOrientationDoc | Implemented, Requires DirectoryTrust | No — repo-only document; nothing to deliver at root | unchanged |
| 4 | WorktreeOrientationDoc | Implemented, Requires DirectoryTrust | No — worktree-only | unchanged |
| 5 | PluginSkills | Implemented, no trust required | No today — `SkillsInputs.Dir` is called with `repoDir` only (`internal/workspace/pluginskills.go:483`, `internal/workspace/worktree_content.go:579`); the mechanism itself (`internal/agentplan/skills.go`) is dir-agnostic and requires no trust entry | **Yes** — row 18 is exactly "call this same mechanism with the instance-root dir" |
| 6 | MarketplaceRegistration | Unavailable (cannot-receive) | No — no Codex concept anywhere | unchanged |
| 7 | SubagentTypes | Unavailable (no-such-concept) | No — no Codex concept | unchanged |
| 8 | MCPServers | Implemented, Requires DirectoryTrust | **No** — `payloadLayouts[agent.AgentCodex].scope = PayloadInRepo` (`internal/agentplan/payload.go:352-370`); config.toml is written per cloned repo only | **still No** |
| 9 | SessionEnvironment | Implemented, Requires DirectoryTrust | **No** — same `payloadLayouts` gate, same file | **still No** |
| 10 | DotenvFiles | Implemented | Yes, agent-neutral, if a workspace declares a dotenv path at the root (`declaration.go:188-195`) | unchanged |
| 11 | FileDistribution | Implemented | Yes, agent-neutral (`declaration.go:197-202`) | unchanged |
| 12 | ApprovalPosture | Implemented, Requires DirectoryTrust | **No** — same `payloadLayouts` gate (`declaration.go:204-218`) | **still No** |
| 13 | Hooks | Unavailable (cannot-receive) | No — no route for Codex anywhere | unchanged |
| 14 | WorkSummaryHooks | Unavailable (cannot-receive) | No | unchanged |
| 15 | PRBodyHook | Unavailable (cannot-receive) | No | unchanged |
| 16 | WorktreeHookDelegation | Unavailable (no-such-concept) | No | unchanged |
| 17 | EphemeralSessions | Unavailable (cannot-receive) | No — about the trigger, not about where a session starts | unchanged |
| 18 | RootProjectSkills | Unavailable (not-built) → **Implemented** | **This is the row that flips.** | flips to Implemented |
| 19 | NiwaPlugin | Unavailable (not-built) | No | unchanged, explicitly out of scope |
| 20 | RemoteControl | Unavailable (no-such-concept) | No | unchanged |
| 21 | DispatchKeepAlive | Unavailable (no-such-concept) | No | unchanged |
| 22 | DispatchLaunch | Implemented | Yes (launching itself works at root; this is the row whose own comment names the gap — see below) | unchanged |
| 23 | DirectoryTrust | Implemented | Only ephemerally: `niwa dispatch` grants trust on the launch command line for that one invocation and writes nothing to the developer's Codex config (guide, `docs/guides/codex-agent.md:332-336`); no standing trust entry exists for the instance root | unchanged |
| 24 | GitExcludeBookkeeping | Implemented | Not applicable — the instance root is not itself a git repository (`workspace-context.md`: "This is NOT a single git repository"), so there is nothing to git-exclude there | unchanged |

Also worth naming even though it has no row of its own: `project_doc_max_bytes` (the doc-budget key `carriesDocBudget: true`, `internal/agentplan/payload.go:169-186` area / the comment at `declaration.go:111-115`) rides the same `config.toml` that rows 8/9/12 ride. It is not written at the root either, which means the root orientation document (row 2, already implemented) has no override for the 32768-byte default and is silently cut if it runs long — a real, currently-documented gap (`docs/guides/codex-agent.md:337-343`) that row 18 does **not** close, because row 18 only wires up the skills-tree delivery, not `payloadLayouts`.

### 2. What remains missing at the root after row 18 — one cause, not several

After row 18 lands, a root-started Codex session has: orientation (row 2) and skills (row 18, newly). It still lacks MCP servers (row 8), session environment (row 9), approval/sandbox posture (row 12), and the doc-budget override for its own orientation document. **All four share exactly one cause**: `payloadLayouts[agent.AgentCodex]` in `internal/agentplan/payload.go:352-370` hardcodes `scope: PayloadInRepo`, and no code path ever asks for that payload to be written at an instance root. The comment directly above the map (`payload.go:339-347`) names this as a deliberate, deferred decision: "Widening this scope is therefore a decision about what niwa writes outside an instance, not a table edit," because every key in that document needs a `[projects."<path>"]` trust entry in the developer's own Codex config, and niwa writes one per cloned repository only — never for the instance root (`payload.go:341-343`; matches the guide's own account at `docs/guides/codex-agent.md:317-323`).

This is a genuinely different mechanism from row 5/18's skills delivery. Skills (`internal/agentplan/skills.go`) take an arbitrary `Dir` and carry no trust requirement (`skills.go:14-19`); the config payload (`payload.go`) is gated by `PayloadScope` and, for Codex, requires a trust entry per row 8/9/12's `Requires: []Capability{DirectoryTrust}`. Row 18 touches only the first mechanism. So: one underlying policy reason (the trust-widening decision), one specific code gate (`payloadLayouts[...].scope`), and it is untouched by delivering root skills.

### 3. `internal/cli/dispatch.go` warning — exact text, gate, and tests

Exact current text (`internal/cli/dispatch.go:402-405`):

```go
if d, err := agentplan.Lookup(agentplan.RootProjectSkills, dispatchedAgent); err == nil && d.State != agentplan.StateImplemented {
    fmt.Fprintf(cmd.ErrOrStderr(),
        "niwa dispatch: the worker starts at the instance root. It reads the workspace orientation written there, but none of the workspace's skills, MCP servers or posture -- for a %s session those reach it only from inside a repository. Its prompt still has to carry the task.\n",
        dispatchedAgent)
}
```

Gate: `agentplan.Lookup(agentplan.RootProjectSkills, dispatchedAgent).State != agentplan.StateImplemented` — i.e. it fires today for Codex (row 18 unavailable) and is silent for Claude (row 18 implemented, per `declaration.go:78-84`... actually row 18 Claude is `declaration.go:282`, implemented). Once row 18 flips to Implemented for Codex, this condition becomes false and **the warning stops printing for Codex entirely**, even though rows 8/9/12 (MCP servers, environment, posture) are still repo-only. Note the current wording also never mentions "environment" — only "skills, MCP servers or posture" — a pre-existing omission independent of round 2.

Tests that pin this warning, both in `internal/cli/`:

- `internal/cli/dispatch_agentwarning_test.go:52` defines `unorientedWarningFragment = "none of the workspace's skills, MCP servers or posture"` and `TestDispatch_UnorientedWorkerWarningPrintsBeforeThePromptCapture` (`:157-184`) asserts this fragment appears in stderr for a Codex dispatch, and specifically that it appears **before** the prompt-capture call (not just anywhere in final stderr) — it uses `stubCapture`'s callback to snapshot stderr at the moment capture opens.
- `internal/cli/dispatch_contract_test.go:272-315`, `TestDispatchWarnsWhenTheWorkerStartsUnoriented`, loops `agentplan.LaunchableAgents()`, calls `agentplan.Lookup(agentplan.RootProjectSkills, ag)`, and asserts `warned == (decl.State != StateImplemented)` where `warned = strings.Contains(stderr, "starts at the instance root")`. Its own doc comment (`:272-289`) already narrates that this exact test moved once before, from row 2 to row 18, and says explicitly: "Deriving the expectation from the table rather than restating it is what let this test point at the new row without inventing a new rule." Both tests will need to change once row 18 flips, because the postcondition they encode ("warned iff row 18 unavailable") becomes false the moment row 18 flips but the real gap (rows 8/9/12) persists.

### 4. Proposed change to the warning (not implemented)

The honest fix has to stop tying the warning's *presence* to `RootProjectSkills`/row 18, because after the flip that row no longer represents the remaining gap. I checked every row in section 1 for one that could serve as the new gate and found **none**: rows 8, 9, and 12 are declared `StateImplemented` for Codex (they genuinely are delivered — just only inside repositories), so `Lookup` on any of them returns "implemented," not "unavailable," and gating on them would make the warning permanently silent, which is wrong. This is not an oversight — `declaration.go:342-346` (the comment on row 22, DispatchLaunch) says it in so many words: "A session started at an instance root receives none of the repository-delivered capabilities, which is a gap the contract has no axis to state -- declarations say who receives, never from where. The design records it; no row is invented for it." The PRD repeats this at `docs/prds/PRD-agent-capability-contract.md:451-454`: "Rows 5, 8, 9 and 12 stay repository-scoped, which this matrix has no axis to express." Adding a where-from axis is explicitly out of scope for this work per the task brief.

So the smallest correct change is to stop gating the warning on a `Declaration` lookup altogether and gate it on the thing that actually decides the remaining gap: whether this agent's payload layout scope is repo-only, i.e. `payloadLayouts[agent].scope != PayloadAtInstanceRoot` (today true for Codex, false for Claude — matching the warning's current agent split for free, since Claude's MCP payload already sits at `PayloadAtInstanceRoot`, `payload.go:353-358`). Concretely that likely means exposing a small producer-level query (e.g. a `Producer` method returning whether its payload scope excludes the instance root) rather than reusing `agentplan.Lookup`. The wording also needs to split: skills now arrive ("it reads the workspace's orientation and skills"), while MCP servers, environment, and posture still don't — and the message should say "environment" explicitly, since the current text omits it. Both pinned tests (`unorientedWarningFragment` and `TestDispatchWarnsWhenTheWorkerStartsUnoriented`'s row-18-keyed assertion) need to move to whatever the new gate is, consistent with the precedent the contract test's own comment already describes for row 2 → row 18.

### 5. `docs/guides/codex-agent.md`, "Starting a session at the instance root" (lines 283-341), sentence by sentence

Quoting the full block and marking each sentence:

> Most of this guide is about a session you open inside a cloned repository. A session started at the instance root is a different case, and it's the one a dispatched worker is in: `niwa dispatch` launches its worker with the instance root as its working directory.

**Stays true.** Framing only.

> **It gets its orientation.** The instance root carries an `AGENTS.md` composing the workspace-level context and everything a Claude Code session reaches from there by `@import` — the generated repo listing, the private overlay's addendum, the global layer. Claude follows references; Codex has no import mechanism, so a reference would deliver nothing and the layers are folded into the document instead.

**Stays true.** Row 2, unaffected by row 18.

> This works even though an instance root holds no project-root marker. It doesn't need one: a session's own working directory is always the last directory of its discovery walk, whether that walk began at a marker-bearing ancestor or, with no marker anywhere above, at the working directory itself. Measured against codex-cli 0.147.0 both ways.

**Stays true.** Mechanism explanation for row 2; also the exact mechanism row 18 relies on for skills, so it stays relevant (though the header below needs to add skills to what this mechanism now delivers).

> **It gets nothing else, yet.**

**Becomes false.** Skills is no longer "nothing else." Needs to become something like "It gets nothing from a repository's project layer except skills" or be dropped/rewritten as a lead-in to a narrower list.

> Skills, MCP servers, the environment, and the approval posture all ride the project-layer `.codex/` directory, and niwa writes that directory into each repository and not at the instance root.

**Half false, needs qualifying.** "Skills... ride the project-layer `.codex/` directory... and niwa writes that directory into each repository and not at the instance root" becomes false for skills specifically once row 18 lands (niwa now writes a `.codex/skills/` tree at the root too). The MCP-servers/environment/posture clause stays true (per section 2's single-cause finding). Needs to split skills out from the other three.

> So a root-started session has the orientation and none of the rest,

**Becomes false.** It now has orientation and skills, and lacks MCP servers/environment/posture.

> and a worker told in its prompt to work in a particular repository doesn't pick that repository's up either — Codex fixes its discovery at session construction, keyed to the launch directory, and follows neither the working directory as the session moves nor an instruction naming a repo.

**Stays true**, and now applies to more than it used to: previously this sentence was really only about the (already-missing) repo-scoped capabilities; after row 18 it remains true for MCP servers/environment/posture, and is also worth noting as still true for skills (a worker can't pick up a *different* repo's skill set mid-session either, though it now has the workspace-level plugin set from the root).

> The files stay readable on request, which is weaker than the content being in the session's context.

**Stays true**, unaffected — general Codex behavior, not scoped to what's delivered at root.

> Two things are true about closing that gap, and both are measured. The mechanism works: a project layer at a marker-less instance root is read, with skills loading untrusted and the configuration keys taking effect once the directory carries a trust entry. And niwa doesn't write a trust entry for the instance root today — only one per cloned repository — so delivering the configuration half would widen what niwa writes into your own Codex config, which is the one thing this guide promises stays small. That's why the row for instance-root skills sits under "what niwa hasn't built yet" rather than under "what Codex can't receive".

**Becomes stale in a specific way, needs a rewrite rather than a qualifier.** This paragraph currently describes the *planned* future (skills delivery not yet built) as a hypothesis ("the mechanism works... skills loading untrusted"). After row 18, this needs to become a statement of *present fact* for skills (delivered) with the configuration-half analysis kept as the ongoing reason MCP/environment/posture stay repo-only. The final sentence ("That's why the row for instance-root skills sits under 'what niwa hasn't built yet'") becomes false — row 18 has moved to the generated "implemented" side and no longer appears in the gap list at all.

> **An interactive session there will ask you to trust the directory.** niwa writes a trust entry per cloned repository and none for the instance root, so the TUI blocks on its trust prompt the first time you start one there yourself, the way it does in any directory you haven't trusted. The orientation still arrives either way — the context chain is read whatever the trust state; it's the configuration keys that aren't. Answering the prompt writes Codex's own entry, which niwa leaves alone.

**Stays mostly true, needs one addition.** Orientation and now *skills* both arrive regardless of trust state (skills load untrusted per the row 5/18 comments); only "the configuration keys" (MCP/env/posture) wait on trust. The second sentence's "The orientation still arrives either way" should become "The orientation and skills still arrive either way" or equivalent.

> A dispatched worker doesn't hit that prompt, and not because the directory is trusted: `niwa dispatch` grants trust on the launch command line, for that invocation only, and writes nothing to your config. So the worker can work, and the instance root still carries no standing trust entry — which is exactly why the configuration half of the project layer can't be delivered there without a decision about widening what niwa writes.

**Stays true.** Still accurate: the trust grant is ephemeral and per-invocation, and the configuration half (MCP/environment/posture) is still gated on the undelivered decision.

> **The root document has no budget key behind it.** `project_doc_max_bytes` is a project-layer key and there's no project layer at the instance root, so if the composed root document runs past the 32768-byte default it's cut silently like any other over-budget chain. niwa can't raise the bound there, so it reports the size at apply time instead. If you see that warning, shorten the workspace-level context or raise the budget for that directory in your own config.

**Needs one qualifying clause, otherwise stays true.** "there's no project layer at the instance root" becomes imprecise once row 18 lands — there *is* a project layer at the root (a `.codex/` directory now exists there, holding the delivered skills tree), it just doesn't carry `config.toml`/`project_doc_max_bytes`. The paragraph's conclusion (no budget override, still cut silently) stays true; only the premise sentence needs rewording to "there's no `config.toml` at the instance root" or similar, to stay consistent with skills now living under `.codex/` at the root.

### 6. `docs/prds/PRD-agent-capability-contract.md` — rows 18/19 and mechanical checking

The matrix (lines ~420-590) already carries an **amendment** for row 18, written after this PRD's initial matrix was drawn (round 1's correction), so it is *already* internally consistent with the current code, not stale relative to today's `declaration.go`. Quoting the relevant rows and amendment:

- Row 18: `| 18 | Root-installed project skills (e.g. dispatch) | Implemented | Unavailable (not-built) (amended) | Settled here as cannot-receive on row 2's reason; see the amendment below the table |`
- Row 19: `| 19 | niwa's own plugin (migrate-config skill) | Implemented | Unavailable (not-built) | Codex accepts the identical plugin manifest; the wiring is unbuilt and out of this PRD's scope |`
- Amendment text: "Row 18 stays unavailable and becomes not-built: Codex does load a skills tree from a project layer at a root-started session's own working directory, untrusted, so what is missing is niwa writing one there. Rows 5, 8, 9 and 12 stay repository-scoped, which this matrix has no axis to express... Target totals: 'The settled column is 13 implemented and 11 unavailable, asserted by `TestCodexColumnTotals`.'"

Once row 18 flips to Implemented, this table's row-18 cell ("Unavailable (not-built)") and the totals line ("13 implemented and 11 unavailable") both become false and would need a second amendment (14 implemented, 10 unavailable). The "Known Limitations" section (~line 598) also carries a now-already-stale bullet independent of round 2 — "A Codex session started at the workspace or instance root sees nothing niwa wrote" — which was already false as of row 2's correction and remains so; round 2 doesn't change this bullet's staleness, it just adds another reason (skills) it's wrong.

**No test mechanically checks the PRD file itself.** `internal/agentplan/capability_test.go` cites the PRD by name in comments (`:111-129`) as "the authority its own map of final reason kinds follows," and `TestCodexColumnTotals` (`capability_test.go:176`) pins the *code's* totals (13/11) as a Go assertion — but nothing greps or diffs `docs/prds/PRD-agent-capability-contract.md` against `declaration.go`. The only prose that has a structural, CI-enforced tie to the declaration table is the **generated** section of `docs/guides/codex-agent.md`, between `<!-- BEGIN GENERATED: codex gap list -->` and `<!-- END GENERATED -->` (`docs/guides/gaplist_test.go` — actually `internal/agentplan/gaplist_test.go:14-40`, `TestCodexGuideGapSectionMatchesDeclarations`). The hand-authored "Starting a session at the instance root" section sits *above* that marker and is exactly what the round-1 lead flagged: nothing enforces its truth against the code.

## Implications

- The two authored-prose sites do not fail the same way. `dispatch.go`'s warning is a binary present/absent signal wired to a single `Lookup` call — once row 18 flips it goes silent for Codex with no code change, silently under-warning about a real remaining gap (MCP/environment/posture). `codex-agent.md`'s section is static prose that simply says something false until a human edits it; nothing fails CI to catch it, unlike the generated section below it.
- Both fixes are additive corrections, not full rewrites: the warning needs a new, non-declaration-based gate (a `PayloadScope` check) plus reworded text splitting "skills" from "MCP servers, environment, posture"; the guide needs targeted sentence edits (marked above) rather than a rewritten section, since most of the prose (the mechanism explanation, the trust-prompt paragraph, the budget paragraph) remains accurate.
- The PRD is a third site with the same class of problem (stale prose, no mechanical check) but the task brief scoped this lead to the two places named — worth flagging to whoever owns the PRD, not fixing here.
- The "several distinct causes" question resolves cleanly to one: `payloadLayouts[agent.AgentCodex].scope == PayloadInRepo` in `internal/agentplan/payload.go:352-370` is the single code location gating everything that remains missing (MCP servers, environment, posture, and the root document's byte budget). Row 18 does not touch it.

## Surprises

- The skills-delivery mechanism (`internal/agentplan/skills.go`) was already root-agnostic in its API shape (`SkillsInputs.Dir` takes any directory, no trust requirement) — the "not built" part of row 18 is purely a missing call site at the instance-root provisioning step, not a missing capability in the producer layer. This means row 18's implementation is likely a small, localized change (call `InstallRepoSkills`-equivalent with the instance root at `niwa create`/provisioning time), which makes the prose-staleness problem land fast relative to how long it may have looked "far off" in the guide's current wording.
- The current warning text already omits "environment" from its list ("skills, MCP servers or posture") even though row 9 (SessionEnvironment) is exactly as repo-scoped as rows 8 and 12. This is a pre-existing gap in the warning's accuracy, unrelated to round 2, worth folding into the same rewrite rather than leaving for a third pass.
- `docs/guides/codex-agent.md`'s premise "there's no project layer at the instance root" (the budget paragraph) is itself about to become imprecise in a way distinct from the "gets nothing else" claim — once skills land, a `.codex/` directory *does* exist at the root, it just lacks `config.toml`. This is easy to miss if someone only patches the "gets nothing else" sentence and skips the budget paragraph three paragraphs later.

## Open Questions

- What is the actual mechanism by which the warning's new gate should be exposed — a new unexported `Producer` method reading `payloadLayouts[...].scope`, or something coarser (e.g., hardcoding "Codex" in `dispatch.go` since it's the only agent with `PayloadInRepo`)? The former stays mechanically tied to code the way `TestDispatchWarnsWhenTheWorkerStartsUnoriented`'s docstring says matters; the latter is simpler but reintroduces a name-based check the existing test design deliberately avoided.
- Should the PRD's stale matrix cells/totals be amended in the same change that flips row 18, given nothing enforces it mechanically and it will otherwise silently drift the way the "Known Limitations" bullet already has? Out of this lead's stated scope, but adjacent.

## Summary
After root skills land (row 18 → Implemented), a root-started Codex session gains orientation (already had it) and skills (new), but MCP servers, session environment, and approval/sandbox posture stay missing — all three gated by one hardcoded line, `payloadLayouts[agent.AgentCodex].scope = PayloadInRepo` in `internal/agentplan/payload.go:352-370`, which row 18 does not touch; no capability row exists or may be invented to express this, so the warning in `internal/cli/dispatch.go:402-405` (gated on `agentplan.Lookup(RootProjectSkills, ...)`) must be re-gated on that payload-scope fact directly rather than on any declaration, and its two pinning tests (`dispatch_agentwarning_test.go:52`, `dispatch_contract_test.go:272-315`) must move with it. The hand-authored "Starting a session at the instance root" section in `docs/guides/codex-agent.md:283-341` has several sentences that go outright false ("It gets nothing else, yet," "a root-started session has the orientation and none of the rest") and one that needs a narrower premise ("there's no project layer at the instance root" → "no `config.toml`"), while the trust-prompt and mechanism paragraphs remain accurate as written; the PRD's matrix at `docs/prds/PRD-agent-capability-contract.md` is already internally consistent for today's code but will need its own new amendment, and nothing mechanically checks it against `declaration.go`.
