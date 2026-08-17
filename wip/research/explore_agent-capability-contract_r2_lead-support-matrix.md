# Lead: The capability support matrix

**Evidence shorthand used throughout.**
`Spike F<n>` = numbered finding in `docs/spikes/SPIKE-codex-discovery-mechanics.md`
(measured against `codex-cli 0.147.0`, unmerged tsukumogami/niwa#254), as extracted in
`wip/research/explore_agent-capability-contract_r1_lead-spike-constraints.md`.
`Audit §A/§B` = sections of `wip/research/explore_agent-capability-contract_r1_lead-prior-attempt-audit.md`.
`Scenario <n>` = the numbered acceptance scenario in `test/functional/features/codex-agent.feature`
on the closed `docs/dual-agent-workspace` branch (Audit §C).
`Inventory` = `wip/research/explore_agent-capability-contract_r1_lead-capability-inventory.md`.
`Skills` = `wip/research/explore_agent-capability-contract_r1_lead-skill-resolution.md`.

**What the matrix's states describe.** On `main` today, *nothing* is implemented for
Codex — the type exists and is inert, and all Codex mechanics live on a closed branch.
A matrix of "state on main" would be all-Unavailable for Codex and would carry no
information. So the Codex column states **what the Codex implementation must declare
when the contract lands**, given measured constraints. Each row's evidence column says
which of the three things settles it: a measured Codex behavior (Spike), working code on
the closed branch plus its acceptance scenario (Audit/Scenario), or nothing yet
(UNRESOLVED).

---

## A. State Model (recommendation + justification)

### Recommendation: two states, one required reason kind, and a `Requires` edge. No third state.

```go
type State int

const (
	StateImplemented State = iota + 1
	StateUnavailable
)

type ReasonKind int

const (
	ReasonAgentCannotReceive ReasonKind = iota + 1 // the agent's own mechanics put it out of reach
	ReasonNoSuchConcept                            // the thing being delivered does not exist for this agent
	ReasonNotBuilt                                 // a route exists and niwa has not built it
)

type Declaration struct {
	Capability Capability
	Agent      agent.Agent
	State      State
	Kind       ReasonKind   // required iff State == StateUnavailable, zero otherwise
	Reason     string       // required iff State == StateUnavailable, empty otherwise
	Requires   []Capability // legal only when State == StateImplemented
}
```

### Why the third state dissolves

The round-1 tension was stated as: MCP servers, environment variables and the context
byte budget are *implemented by niwa and inert until the developer's own Codex config
carries a trust entry for the directory, which a workspace cannot self-grant*
(Spike F4, F5). That reading is correct about Codex and wrong about niwa, and the
correction is the whole answer to part A.

The spike scoped itself deliberately to what a **project config layer** can carry
"without touching the developer's own Codex configuration". niwa is not a project config
layer. niwa is a tool the developer runs on their own machine, and the prior attempt
already built exactly the thing the spike says a project layer cannot do: `codex_trust.go`
(688 lines) performs a TOML-surgical, additive, lock-serialized, atomic edit of
`~/.codex/config.toml` to add `[projects."<canonical path>"]`, retracting only keys niwa
itself previously wrote (Audit §B). Two acceptance scenarios pin its behavior offline
(Scenario 8: canonical paths, one entry per repo, developer's own `model`/`[tui]` content
untouched, no accumulation across three applies; Scenario 9: an unreadable credential
file breaks neither create nor apply) and two more pin it against a live binary
(Scenario 14: `codex exec` writes a file on its *first* attempt with no setup step;
Scenario 15: an interactive session under a pty reaches ready state with no trust prompt,
from the repo root and from a nested directory).

So trust is not an external precondition. It is a capability niwa delivers, with an
implementation, a test, and a failure mode. Once you name it — `CapabilityDirectoryTrust`
— everything that was "conditional" becomes plainly implemented with a dependency edge:

```
CapabilityRepoContext(codex).Requires  = [CapabilityDirectoryTrust]  // budget override
CapabilityMCPServers(codex).Requires   = [CapabilityDirectoryTrust]
CapabilitySessionEnv(codex).Requires   = [CapabilityDirectoryTrust]
CapabilityDirectoryTrust(codex).State  = StateImplemented
```

and the closure test — *every capability named in `Requires` is itself Implemented for
the same agent* — makes that honest by construction. If a future PR declares MCP servers
implemented while trust is declared unavailable, the test fails. A soft "conditional"
state could never have caught that; it would have absorbed it.

### Why not a third state, and why not an orthogonal precondition field

**Three states fails criterion 3.** "Conditional" is the exact shape of a soft word a
real gap can hide inside. Worse, it fails criterion 2 in a specific way: the guide's gap
list generator would have to make a *judgment* about whether conditional rows belong on
the list. That is precisely the failure mode the brief names in the prior attempt, which
spread its gaps across a design's negative-space section and a scope note. Two states
makes the generator a filter, not a decision.

**An orthogonal free-text precondition field fails criterion 1's spirit.** It keeps the
pair resolving to one state, but it moves the honesty burden into prose nothing can
check. `Requires []Capability` is the same idea with a closed domain: a precondition is
only expressible if it is itself a capability niwa either implements or declares
unavailable. Anything niwa genuinely cannot own — the developer refusing a prompt, an OS
feature — is not expressible as a requirement, which forces the honest answer:
`StateUnavailable` with `ReasonAgentCannotReceive`. That is a feature. The type system
should refuse to let you write "works, sort of, if."

### The one cost, stated plainly

Because there is no `NotApplicable` escape hatch, a handful of rows come out
`StateUnavailable` for **Claude** in a way that reads slightly odd out of context —
`CapabilityDirectoryTrust` for Claude is `ReasonNoSuchConcept` ("Claude Code has no
per-directory trust record; approval posture comes from settings"). I recommend
accepting this. The guide renders `ReasonNoSuchConcept` rows as a short "does not apply
to this agent" note and `ReasonAgentCannotReceive` / `ReasonNotBuilt` rows as the gap
list proper — a rendering rule over a machine-readable enum, not a judgment call. Adding
a fourth reason kind to make those rows disappear entirely would reintroduce the escape
hatch under a new name.

### The three tests the model has to carry

1. **Exhaustive.** For every `(Capability, agent.Agent)` in `Capabilities() × agent.All()`
   there is exactly one `Declaration`. Requires two things that do not exist today: an
   exported, closed, enumerable capability set, and an exported `agent.All()` — `known`
   is unexported and every current "for each agent" test hand-lists the two constants
   (round-1 structural-test lead). Both are cheap.
2. **Well-formed.** `StateUnavailable` implies non-empty `Reason` and a `Kind` in range;
   `StateImplemented` implies both are zero. `Requires` is empty unless implemented.
3. **Closed.** Every capability in `Requires` is Implemented for the same agent, and the
   graph is acyclic.

Plus the binding that makes the declaration load-bearing rather than decorative — the
lesson of the prior attempt, whose `Applier.Agent` compiled clean and was read by nothing
(Audit §A): **every Implemented declaration resolves to a registered delivery function,
and every Unavailable declaration resolves to none.** A stub that quietly delivers
anyway, or a declaration with no implementation behind it, fails. This is the structural
answer to "no agent constants at materializer call sites" — the pipeline iterates
declarations, so there is no place to put a constant.

### Guide generation

The gap list in the user guide is generated from the same table: filter
`State == StateUnavailable` for the agent, order by `Kind`, render `Reason`. A test
compares the generated text against the committed guide section and fails on drift. The
repo has no golden-file precedent, so this establishes one; it is a few dozen lines of
stdlib and no new dependency (CI runs only `gofmt -l .`, `go vet ./...`,
`go test -race ./...`).

---

## B. The Matrix

`I` = Implemented. `U(kind)` = Unavailable with that reason kind. `?` = UNRESOLVED.

| # | Capability | Identifier | Claude | Codex | Reason (where not plainly implemented) | Evidence for the Codex call |
|---|---|---|---|---|---|---|
| 1 | Workspace/group orientation text reaches a repo session | `CapabilityWorkspaceContext` | I | I | — (delivered differently: composed into each repo's own override rather than placed at the root) | Spike F1 (bounded walk), Audit §B `codex_context.go` `renderInstanceContextLayer`/`renderGroupContextLayer`; Scenario 4 |
| 2 | A session started at the workspace or instance root is oriented | `CapabilityRootContext` | I | U(cannot-receive) | Codex reads context only from the nearest ancestor holding a `.git` marker downward, and an instance root has none — a Codex session started outside any repo sees nothing niwa wrote | Spike F1, F9 |
| 3 | Repo-level orientation doc inside a clone | `CapabilityRepoContext`<br>`Requires: DirectoryTrust` | I | I | — | Spike F1/F2/F3 (`AGENTS.override.md` is the only name that wins in every repo; never write an empty file); Audit §B `codex_context.go`; Scenarios 4, 5, 6, 13 |
| 4 | Worktree-level orientation doc | `CapabilityWorktreeContext`<br>`Requires: DirectoryTrust` | I | I | — | Audit §B `codex_worktree.go`; Scenario 10. **See Unresolved #1** — whether a git worktree's `.git` *file* satisfies `project_root_markers` is unverified against a real binary and could flip this row |
| 5 | Workspace-declared plugin skills usable in the session | `CapabilitySkills` | I | I | — | Spike F5 (skills load from an **untrusted** project layer, `<plugin>:<skill>` namespace preserved), F8 (Claude plugin manifests install unmodified); Audit §B `codex_payload.go` `reconcileCodexSkillLinks`; Scenario 7. **Carries an implementation obligation** — see Unresolved #2 |
| 6 | Marketplace/plugin registration with the agent's own plugin system | `CapabilityPluginRegistration` | I | U(cannot-receive) | A project config layer cannot register a marketplace or a plugin; registration lives in the developer's own Codex configuration. niwa delivers the skill trees directly instead, so what is lost is everything else a plugin carries — commands, agents, hooks | Spike F5 (explicit exclusion list), F6, F8 |
| 7 | Named subagent types | `CapabilitySubagents` | I | U(no-such-concept) | Codex copies a plugin's `agents/` directory into its cache and never surfaces it; there are no named subagent types to address | Spike F8 (measured) |
| 8 | MCP servers available to the session | `CapabilityMCPServers`<br>*(Codex: would require `DirectoryTrust`)* | I | U(not-built) | niwa writes only `project_doc_max_bytes` into the Codex payload config; `[mcp_servers.*]` at the project layer is a working route that niwa has not built | Audit §A (`renderCodexPayloadConfig` writes one key; no `mcp_servers` and no `.mcp.json` reference anywhere in `codex_*.go`); Spike F5 (route works, trust-gated) |
| 9 | Environment variables present in the session's environment | `CapabilitySessionEnv`<br>*(Codex: would require `DirectoryTrust`)* | I | U(not-built) | `shell_environment_policy.set` parses at the project layer and is the route for anything a session needs in its environment; niwa writes no such key | Spike F6; Audit §A. **Caveat:** F6 reports the key parses but never ran the trusted-vs-untrusted comparison F5 ran for budget and MCP — see Unresolved #3 |
| 10 | Dotenv files written to declared paths | `CapabilityEnvFiles` | I | I | — (agent-agnostic; `[env]` is not under `[claude]`) | Inventory (`EnvMaterializer`) |
| 11 | Arbitrary source→destination file distribution | `CapabilityFileDistribution` | I | I | — (agent-agnostic) | Inventory (`FilesMaterializer`) |
| 12 | Approval / sandbox posture (bypass vs. ask) | `CapabilityApprovalPosture` | I | **?** | UNRESOLVED — Codex has `approval_policy`/`sandbox_mode`, but whether they are settable from a project config layer is unknown. The spike names eleven denylisted keys and enumerates three (provider URLs, `notify`, profiles) | Spike F5/F6 partial. **See Unresolved #4** |
| 13 | Hooks (arbitrary lifecycle commands) | `CapabilityHooks` | I | U(cannot-receive) | A loose `hooks.json` registers nothing; the only working route is a plugin carrying its own `hooks.json`, and an interactive session blocks on a review prompt for any hook it cannot match against a recorded `trusted_hash`. No route avoiding both the hash and the prompt has been demonstrated | Spike F7 (measured, with the "not demonstrated" caveat stated in the spike itself). **See Unresolved #5** |
| 14 | Work-summary hooks | `CapabilityWorkSummaryHooks`<br>`Requires: Hooks` | I | U(cannot-receive) | Delivered as lifecycle hooks; see hooks | Derived via the closure rule from row 13 |
| 15 | PR-body hook | `CapabilityPRBodyHook`<br>`Requires: Hooks` | I | U(cannot-receive) | Delivered as a lifecycle hook; see hooks | Derived via the closure rule from row 13 |
| 16 | Worktree-hook delegation (or the deny fallback) | `CapabilityWorktreeHookDelegation` | I | U(no-such-concept) | The whole mechanism is Claude Code harness surface — `WorktreeCreate`/`WorktreeRemove` hook events and the `EnterWorktree`/`ExitWorktree` tools it denies when they are absent. Codex has neither, so there is nothing to delegate to and nothing to deny | Inventory (`harness_compat.go:36` execs a binary literally named `claude`); Spike F7 for the hook half |
| 17 | Ephemeral-session provisioning | `CapabilityEphemeralSessions`<br>`Requires: Hooks` | I | U(cannot-receive) | Provisioning rides a `SessionStart` hook and reads the harness's own job-state file to tell a dispatched worker from an interactive session; Codex has neither the hook route (row 13) nor the job-state file | Spike F7; Inventory (`root_materializer.go` reads `~/.claude/jobs/<id>/state.json`) |
| 18 | niwa's root-installed project skills (e.g. `dispatch`) | `CapabilityRootSkills` | I | U(cannot-receive) | These skills exist to serve a session started at the workspace root, and Codex reads no configuration layer there — a `.codex/` directory with no project-root marker above it is never visited | Spike F9 (measured as the specific way a naive experiment fails), F1 |
| 19 | niwa's own plugin (its `migrate-config` skill) | `CapabilityNiwaPlugin` | I | U(not-built) | niwa writes its embedded plugin only into Claude Code's user plugin directory. `codex plugin marketplace add` accepts the same `.claude-plugin/marketplace.json` manifest unmodified, so the route exists and is unbuilt | Spike F8 (measured: manifests install unmodified); Inventory (`internal/plugin/installer.go` hardcodes `~/.claude/plugins/marketplaces/niwa/`) |
| 20 | Remote-control-at-startup | `CapabilityRemoteControl` | I | U(no-such-concept) | `remoteControlAtStartup` names claude.ai's remote-control bridge; Codex has no equivalent session bridge to enable | Inventory (`config.RemoteControlAtStartupKey`); no Codex analog in the spike |
| 21 | Dispatch keep-alive | `CapabilityDispatchKeepAlive`<br>`Requires: Dispatch` | I | U(no-such-concept) | Keep-alive holds a dispatched worker's session bridge warm; Codex has no background-session bridge, and dispatch does not launch Codex workers | Inventory; Audit §A (`dispatch.go:234-240` refuses non-Claude) |
| 22 | Launching a background worker (`niwa dispatch`), incl. model resolution | `CapabilityDispatch` | I | U(not-built) | `niwa dispatch` exits non-zero when the resolved agent is not Claude. The model-category table is already keyed by agent and carries Codex entries; no launch path uses them | Audit §A (`dispatch.go:234-240`; `resolveDispatchModel` called with a hardcoded constant at `dispatch.go:353`); Scenario 2 pins the refusal |
| 23 | Per-directory trust bootstrap | `CapabilityDirectoryTrust` | U(no-such-concept) | I | Claude side: Claude Code keeps no per-directory trust record for niwa to write; approval posture is settings-driven (row 12) | Spike F4 (trust cannot be self-granted from a project layer — which is why niwa writes the developer's own config instead); Audit §B `codex_trust.go`; Scenarios 8, 9, 14, 15 |
| 24 | Git-exclude bookkeeping for niwa-written files | `CapabilityGitExclude` | I | I | — (agent-agnostic; excludes `.codex/` and `AGENTS.override.md` exactly as it excludes `.claude/`) | Inventory (`internal/gitexclude`); Scenario 11 asserts exclude patterns are stable across three applies |

**Totals for Codex:** 9 Implemented, 14 Unavailable (5 cannot-receive, 5 no-such-concept,
4 not-built), 1 unresolved. **For Claude:** 23 Implemented, 1 no-such-concept.

---

## C. What the Guide Must Say (Codex gap list)

This is the generated output: every row above where the Codex state is Unavailable,
in user-facing language, one line each with its reason. Rendered in three groups because
the reason kind is a machine-readable field, and the three groups genuinely read
differently to a developer deciding whether to run a Codex workspace.

### Codex sessions do not get these, because of how Codex works

- **A workspace-root session gets no context.** Codex only reads context files from the
  nearest directory above you that contains a `.git` marker, and downward from there. The
  instance root has no such marker, so a Codex session started outside a repository sees
  nothing niwa wrote. Start Codex inside a cloned repo.
- **Marketplace and plugin registration.** A workspace cannot register a marketplace or
  enable a plugin in your Codex installation — that lives in your own Codex config. niwa
  delivers the *skills* from your declared plugins directly instead, so skills work; the
  rest of what a plugin carries (its commands, agents, and hooks) does not come along.
- **Hooks.** Codex registers hooks only from a plugin, and it blocks the session on a
  review prompt for any hook it can't match against a trust hash it already recorded.
  There is no known way to install a hook automatically without either solving that hash
  or putting a modal in front of every session start, so niwa installs none.
- **Work summaries and generated PR bodies.** Both are delivered as hooks — see above.
- **Ephemeral per-session instances.** Provisioning runs from a session-start hook and
  reads the harness's own job-state file to tell a dispatched worker from an interactive
  session. Codex offers neither.
- **niwa's own root skills, including `/dispatch`.** They exist to serve a session
  started at the workspace root, and that is the one place Codex reads no configuration
  at all.

### These concepts do not exist for Codex

- **Named subagents.** Codex copies a plugin's `agents/` directory into its cache and
  never surfaces it. There are no named subagent types to address, from a plugin or
  otherwise.
- **Worktree hook delegation.** niwa hands worktree creation and removal to the harness
  through Claude Code's worktree hook events, and denies its worktree tools when the
  harness is too old to accept them. Codex has neither the events nor the tools.
- **Remote control at startup.** This switch enables claude.ai's remote-control bridge.
  Codex has no equivalent.
- **Dispatch keep-alive.** Nothing to keep alive: Codex has no background-session bridge,
  and dispatch does not launch Codex workers anyway.

### niwa has not built these yet, and could

- **MCP servers.** niwa writes only the context byte budget into the Codex config it
  delivers. Declaring MCP servers at that layer works — it needs writing.
- **Environment variables.** Same config, same story: Codex accepts environment variables
  through `shell_environment_policy.set` at this layer, and niwa does not write the key
  yet. Use the dotenv file distribution in the meantime.
- **`niwa dispatch`.** It refuses to launch a worker when the workspace's agent is not
  Claude, and tells you to set `NIWA_AGENT=claude` to override. The per-agent model table
  already knows Codex's model names; the launch path does not.
- **niwa's own `migrate-config` skill.** niwa installs its plugin into Claude Code only.
  Codex accepts the identical plugin manifest, so this is a wiring gap, not a limitation.

### Unresolved at the time of writing

- **Approval and sandbox posture.** Whether a workspace can set Codex's approval policy
  and sandbox mode, or whether those keys are on Codex's project-layer denylist, has not
  been measured. Until it is, assume your own Codex defaults apply and niwa does not
  change them.

### Things that work, with a condition worth knowing

Not gaps, but the guide should say them next to the list above, because a developer will
otherwise read the trust machinery as something they have to do:

- niwa writes a trust entry for each repository into your own `~/.codex/config.toml`,
  additively, leaving everything else in that file alone. The context byte budget, and
  (once built) MCP servers and environment variables, only take effect because that entry
  is there. Skills load whether or not it is.
- Workspace and group context still reaches Codex sessions — it is composed into each
  repository's own context file rather than left at the root, because that is the only
  place Codex will look.
- If a repository already commits a file at one of the names niwa writes, niwa reports it
  and leaves the committed file alone rather than overwriting it. That repository gets
  less than a clean one does.

---

## Re-cuts and Why

The inventory's granularity was right for "what does the code do" and wrong in six places
for "what can we honestly say per agent." Each re-cut below either splits a row whose
Codex answer was not single-valued, or merges rows that fragment one user-visible thing.

**1. Split root/group context into two rows (rows 1 and 2).** The inventory has one
"context files (root/group orientation doc)" row. Its Codex answer is genuinely two
answers: the *content* reaches a Codex session (composed into each repo's override), but
the *placement* does not (nothing above a repo's `.git` is ever read). Left as one row it
would have to be either "implemented" — hiding the fact that a root-started Codex session
is blind — or "unavailable" — falsely telling users their workspace context is lost. Split,
both answers are true and the real gap lands on the guide's list in the form a developer
needs: *start Codex inside a repo*.

**2. Promote MCP servers from "not a capability" to a first-class row (row 8).** The
inventory correctly observes MCP is not something niwa implements — it is an emergent use
of the generic `[files]` mechanism, and Claude Code reads the `.mcp.json` that lands. But
"the file was copied" and "the session has MCP servers" are the same statement only for
Claude. For Codex, copying a `.mcp.json` accomplishes nothing; the servers have to be
declared as `[mcp_servers.*]` in the Codex config layer. If MCP stays folded into file
distribution, the matrix reports it Implemented for both agents and the guide silently
lies. As a row of its own it reports the truth: Claude yes, Codex not built.

**3. Split "environment" into delivery-to-files and delivery-to-session (rows 9 and 10).**
The inventory has `[claude.env]` (settings injection) and `[env]` (dotenv files) as two
rows already, but classifies the second as agent-agnostic-implemented, which is right
about the mechanism and misleading about the outcome: a `.env` file on disk is not an
environment variable in the session. Keeping both, with the session-level row separate,
lets the Codex gap list say the useful thing — *the route exists, it's unbuilt, use the
dotenv files meanwhile*.

**4. Fold the context byte budget into the context rows rather than giving it a row.**
Round 1 listed `project_doc_max_bytes` among the conditional-on-trust set. It is not a
capability — nobody wants a byte budget; they want their context not silently truncated
(Spike F3: single counter, drains outermost-first, raw byte cut, no marker, nothing on
stderr). It is a mechanism internal to Codex context delivery. Folding it in moves the
trust dependency where it belongs — onto `CapabilityRepoContext`/`CapabilityWorktreeContext`
via `Requires` — and keeps the guide from listing a knob as a feature.

**5. Merge dispatch model resolution into `CapabilityDispatch` (row 22).** The inventory
lists model-name resolution as its own capability and notes, correctly, that it is the
one place in the tree an agent-keyed table is done right. But nobody consumes a model
name without launching a worker, and `niwa dispatch` refuses non-Claude workspaces
outright. Two rows would report "model resolution: implemented for Codex" beside
"dispatch: unavailable," which is true of the code and useless to a user. One row says
the honest thing: dispatch doesn't launch Codex, and the model table is already waiting.

**6. Keep work-summary and PR-body hooks as their own rows (14, 15) rather than folding
them into hooks.** These are visible outcomes — a summary appears, a PR body gets filled —
and a developer scanning for "do I lose my PR bodies" should find that line. Folded into
`CapabilityHooks` the answer would be technically present and practically unfindable. The
`Requires: Hooks` edge keeps them honest: nobody can declare them implemented for an
agent whose hooks are declared unavailable, because the closure test fails.

**7. Add `CapabilityDirectoryTrust` (row 23), which the inventory does not have at all.**
It is the keystone of part A. The inventory maps `main`, where trust does not exist; the
prior branch built it; the round-1 tension exists precisely because nobody had put it in
the capability set. Naming it converts three "conditional" rows into three implemented
rows with a checkable dependency.

**8. Excluded from the set: vault-backed secret resolution, and `claude.enabled`.** Vault
resolution delivers nothing to a session — it is an upstream source feeding rows 9 and 10,
and putting it in the set would make the closed set describe niwa's internals rather than
what a session gets. `claude.enabled` is a gate, not a capability; the round-1 finding
that it is correctly named today and misnamed the moment a second agent lands is a
separate question, and the matrix does not need to resolve it. Both should be named in
the design as deliberate exclusions so the closed set does not look arbitrary.

---

## Unresolved Rows

**1. Does a git worktree root count as a Codex project root?** (affects row 4)
The prior branch composes an `AGENTS.override.md` at the worktree root and Scenario 10
asserts it is selected — but that scenario is `@critical`/offline and runs against
`codexContextChain`, niwa's own reimplementation of Codex's discovery algorithm, not
against a real binary. A git worktree has a `.git` **file**, not a directory. Whether
codex-cli's `project_root_markers` check matches a regular file is not in the spike, and
the two `@codex-live` scenarios cover only the repo root and a directory nested inside it.
*What would settle it:* one live check — `codex` started inside a `niwa worktree` with a
niwa-composed override present, asserting it is in the loaded context. If a `.git` file
does not match, the worktree root is not a project root, the walk continues to whatever
ancestor is, and this row becomes `U(cannot-receive)` with a concrete reason.

**2. Where does niwa get a github-sourced marketplace's content?** (gates row 5)
The prior branch resolves the Codex skills symlink for a github-sourced marketplace to
`~/.claude/plugins/marketplaces/<name>/` — a directory only `claude plugin marketplace add`
populates. On a machine without Claude Code it never exists, and the branch's own
`MissingPluginRoot` warning names a remedy ("re-run `niwa apply`") that cannot succeed
there (Skills §B, §D). Repo-sourced marketplaces are already clean. Under the recommended
model this is not a state question but an implementation obligation: either PR 2 fetches
github-sourced marketplace content into a niwa-owned directory — niwa already owns
`github.FetchTarball` + `ExtractSubpath`, used for ordinary cloning in
`snapshotwriter.go` (Skills §C) — or `CapabilitySkills` for Codex cannot honestly be
declared Implemented. *What would settle it:* a scope decision on PR 2, not a measurement.
Recommend building the fetch; the alternative is a row whose truth depends on whether a
different vendor's CLI has run.

**3. Is `shell_environment_policy.set` trust-gated?** (affects row 9's `Requires`)
Spike F6 says the key "parses cleanly at this layer" and the spike's own summary groups
env vars with the trust-gated set — but F6 never ran the trusted-vs-untrusted comparison
F5 explicitly ran for budget and MCP servers, and F5 is the finding that proved trust
gates the layer *unevenly* (skills load untrusted). So the grouping is inference, not
measurement. *What would settle it:* set `shell_environment_policy.set` in a project layer
in an untrusted directory and read the variable from inside the session; repeat with a
trust entry. The row's state does not change either way — only whether it carries the
`Requires: DirectoryTrust` edge.

**4. Approval and sandbox posture.** (row 12, the only `?` in the matrix)
Codex has `approval_policy` and `sandbox_mode`. The spike says eleven config keys are
denylisted at the project layer and names three (provider URLs, `notify`, profiles),
explicitly warning that the list is incomplete and should not be filled in by guessing.
Whether the approval/sandbox keys are among the remaining eight is exactly the kind of
thing two prior attempts got wrong from the outside. *What would settle it:* read the
denylist out of codex source at tag `rust-v0.147.0` (a named constant, per the spike's own
method), or set `approval_policy` in a trusted project layer and observe whether a
session honors it. This is the single highest-value follow-up measurement — approval
posture is the one remaining row where the answer could be "implemented," and it is the
capability a developer notices immediately.

**5. Hooks: cannot-receive or not-built?** (row 13's reason kind, not its state)
The spike says no route avoiding both the trust hash and the blocking modal has been
*demonstrated*, which is not the same as proven impossible, and it flags two specific
unknowns: whether a non-interactive `codex exec` also blocks on the hook review prompt,
and whether `[hooks.state].trusted_hash` can be pre-seeded from outside the developer's
own config. If either turns out permissive, the reason kind flips from cannot-receive to
not-built, and rows 14, 15 and 17 flip with it. The state stays Unavailable in every case,
so the guide's list is correct today either way — only its explanation would change.
Worth noting that niwa already writes the developer's own Codex config for trust
(row 23), so a pre-seeded hook hash is not obviously out of reach on the same grounds.

---

## Open Questions

- **Does the capability set need a level dimension?** Several rows are implemented at some
  levels and not others (context at root vs. repo vs. worktree; settings at root vs.
  instance vs. repo). I resolved this by splitting rows where the *agent* answer differs
  by level (rows 2/3/4) and not otherwise. If a future agent's answer differs by level for
  a capability where Claude's and Codex's do not, that row splits then. Adding a level
  dimension up front multiplies the matrix by four for one honest distinction.

- **Where does the declaration table live?** Part A's model needs the closed capability
  set and `agent.All()` in a leaf package, but the delivery functions the binding test
  checks live inside `internal/workspace`. Round 1 flagged this as the load-bearing design
  decision (declarative plan returned to the workspace, vs. an interface implemented
  inside it). The matrix does not settle it, but it does constrain it: the binding test
  needs to see both the declaration and its implementation, so whichever shape is chosen,
  one package must be able to observe both.

- **Should the guide's gap list be per-agent or comparative?** I wrote section C as a
  Codex-only list because that is what the brief asks for and what a developer choosing an
  agent needs. But the matrix generates a Claude list too (one row), and a comparative
  table is a third rendering of the same data. All three come from the same filter; the
  guide should pick one and not hand-maintain a second.

- **Does `niwa dispatch`'s refusal belong in the gap list or in the capability set, or
  both?** It is currently row 22, and its refusal is already pinned by Scenario 2. But it
  is also the one place on `main` where a resolved agent value actually drives a decision
  (Audit §A) — so it is simultaneously a gap and the existing precedent for the contract
  working. Worth deciding whether the contract absorbs that refusal or leaves it standing
  beside it.

---

## Summary

Two states, not three: the "conditional on trust" case dissolves once directory trust is
named as a capability niwa itself implements — it already does, in `codex_trust.go`, with
four acceptance scenarios — so MCP, environment and the context budget become plainly
implemented with a `Requires: CapabilityDirectoryTrust` edge that a closure test enforces,
and a soft middle state that would have let a real gap hide is never needed. The filled
matrix covers 24 capabilities after six re-cuts (splitting root context from workspace
context, promoting MCP to a first-class row, separating session env from dotenv files,
folding the byte budget and dispatch model resolution into their parents, and adding
directory trust); Codex comes out 9 implemented, 14 unavailable across three distinct
reason kinds, and 1 unresolved. The Codex gap list a developer needs is fourteen lines in
three groups — six things Codex's own mechanics put out of reach, four concepts that do
not exist for it, four things niwa simply has not built — plus one honestly unresolved
row (approval and sandbox posture), which is also the highest-value follow-up measurement
because it is the last row that could still turn out to be implementable.
