package agentplan

import (
	"fmt"

	"github.com/tsukumogami/niwa/internal/agent"
)

// State is what niwa does about one capability for one agent. There are two
// states and deliberately no third: the tempting middle one -- "implemented,
// but only when some precondition holds" -- is expressed instead as
// Requires edges over capabilities niwa itself delivers, so a real gap has
// nowhere soft to hide.
type State uint8

const (
	// StateImplemented means niwa delivers this capability to this agent.
	StateImplemented State = iota + 1

	// StateUnavailable means it does not, and the declaration says why.
	StateUnavailable
)

// ReasonKind classifies why a capability is unavailable. It is an enum rather
// than prose so the generated gap list can filter and group instead of judging:
// no-such-concept rows render as short "does not apply" notes, the other two
// kinds as the gap list proper.
type ReasonKind uint8

const (
	// ReasonAgentCannotReceive means the agent's own mechanics put the
	// capability out of reach -- niwa could build something, and the agent
	// would not read it.
	ReasonAgentCannotReceive ReasonKind = iota + 1

	// ReasonNoSuchConcept means the capability does not exist for this agent:
	// there is nothing to deliver, not a delivery that failed.
	ReasonNoSuchConcept

	// ReasonNotBuilt means a route exists and niwa has not built it. This is
	// the only kind that is niwa's own debt.
	ReasonNotBuilt
)

// Declaration states, for one capability and one agent, whether niwa delivers
// it -- and when it does not, why not, in a form a generator can filter rather
// than interpret.
//
// Kind and Reason are set exactly when State is StateUnavailable and are zero
// otherwise; Requires is legal only when State is StateImplemented, and every
// capability it names must itself be implemented for the same agent. Those
// invariants are asserted by the structural suite rather than enforced by the
// constructor, because the table is data and a broken row should fail a test
// with a name in it rather than panic at init.
type Declaration struct {
	Capability Capability
	Agent      agent.Agent
	State      State
	Kind       ReasonKind
	Reason     string
	Requires   []Capability
}

// declarations is the capability matrix as data: exactly one row per
// (capability, agent) pair, in matrix order, Claude before Codex.
//
// The Codex column states what niwa delivers today and nothing more. Directory
// trust is the first row it delivers: the entry that makes every later
// trust-gated row possible arrives with the writer that produces it, in the
// same change. Thirteen Codex rows are at their final unavailable state -- the
// eleven whose reason is inherent to the agent plus the two whose route exists
// and is out of this work's scope -- and the rows still reading "not yet" are
// the ones whose delivery has not landed. Each flips in the change that
// delivers it, never before: writing the future state down early would make
// the table a plan rather than a record, which is the failure this contract
// exists to prevent.
var declarations = []Declaration{
	// Row 1: workspace and group orientation reaching a repo session.
	{Capability: WorkspaceOrientation, Agent: agent.AgentClaude, State: StateImplemented},
	// Codex reads context from the nearest project-root marker downward, so the
	// documents above a repository are never visited from a session inside it.
	// The workspace and group layers are composed into the repository's own
	// document instead -- the same document row 3 delivers, which is why this
	// row carries no separate write of its own.
	{Capability: WorkspaceOrientation, Agent: agent.AgentCodex, State: StateImplemented},

	// Row 2: a session started at the workspace or instance root.
	{Capability: RootSessionOrientation, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: RootSessionOrientation, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonAgentCannotReceive,
		Reason: "Codex reads context only from the nearest project-root marker downward, and an instance root has none.",
	},

	// Row 3: the repository-level orientation document.
	{Capability: RepoOrientationDoc, Agent: agent.AgentClaude, State: StateImplemented},
	// The trust edge is about the byte budget, not the document: the composed
	// chain is read whatever the trust state, but the project-layer key that
	// raises the budget above the 32768-byte default is honored only in a
	// trusted directory, and an over-budget chain is cut without a word.
	{
		Capability: RepoOrientationDoc, Agent: agent.AgentCodex,
		State:    StateImplemented,
		Requires: []Capability{DirectoryTrust},
	},

	// Row 4: the worktree-level orientation document.
	{Capability: WorktreeOrientationDoc, Agent: agent.AgentClaude, State: StateImplemented},
	// A linked worktree's `.git` is a regular pointer file, and Codex's
	// project-root marker check is a bare metadata stat that a file satisfies,
	// so the walk stops at a worktree root exactly as it stops at a clone's
	// (measured against codex-cli 0.147.0, with a negative control). The same
	// budget reasoning as row 3 puts the trust edge here too.
	{
		Capability: WorktreeOrientationDoc, Agent: agent.AgentCodex,
		State:    StateImplemented,
		Requires: []Capability{DirectoryTrust},
	},

	// Row 5: workspace-declared plugin skills.
	{Capability: PluginSkills, Agent: agent.AgentClaude, State: StateImplemented},
	// The plugin tree is delivered whole into the project layer, where Codex
	// resolves it to the same `<plugin>:<skill>` names Claude Code produces.
	// There is deliberately no trust edge on this row: skills were measured to
	// load from an untrusted layer, unlike every configuration key beside them,
	// so requiring trust here would declare a dependency niwa does not have.
	{Capability: PluginSkills, Agent: agent.AgentCodex, State: StateImplemented},

	// Row 6: marketplace and plugin registration.
	{Capability: MarketplaceRegistration, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: MarketplaceRegistration, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonAgentCannotReceive,
		Reason: "Registration lives in the developer's own Codex configuration rather than anywhere a workspace can declare it; skills are delivered directly instead.",
	},

	// Row 7: named subagent types.
	{Capability: SubagentTypes, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: SubagentTypes, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNoSuchConcept,
		Reason: "Codex caches a plugin's agents directory and never surfaces named subagent types.",
	},

	// Row 8: MCP servers.
	{Capability: MCPServers, Agent: agent.AgentClaude, State: StateImplemented},
	// The workspace declares its servers once, agent-neutrally, and each
	// agent's own format is generated from that declaration. The trust edge is
	// the whole of what makes the Codex half work: an untrusted project layer
	// is not parsed at all, so the generated entries would sit on disk unread.
	{
		Capability: MCPServers, Agent: agent.AgentCodex,
		State:    StateImplemented,
		Requires: []Capability{DirectoryTrust},
	},

	// Row 9: environment variables in the session.
	{Capability: SessionEnvironment, Agent: agent.AgentClaude, State: StateImplemented},
	// The workspace declares its variables once, agent-neutrally, and each
	// agent's own destination is generated from that declaration: a settings
	// env block for one, a shell environment policy for the other. The trust
	// edge is the same one the MCP row carries and for the same measured
	// reason -- an untrusted project layer is not parsed at all, so the
	// generated policy would sit on disk unread.
	{
		Capability: SessionEnvironment, Agent: agent.AgentCodex,
		State:    StateImplemented,
		Requires: []Capability{DirectoryTrust},
	},

	// Row 10: dotenv files at declared paths.
	{Capability: DotenvFiles, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: DotenvFiles, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNotBuilt,
		Reason: "Dotenv delivery is agent-agnostic, but niwa has not yet bound it to Codex under this contract.",
	},

	// Row 11: arbitrary file distribution.
	{Capability: FileDistribution, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: FileDistribution, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNotBuilt,
		Reason: "File distribution is agent-agnostic, but niwa has not yet bound it to Codex under this contract.",
	},

	// Row 12: approval and sandbox posture.
	{Capability: ApprovalPosture, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: ApprovalPosture, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNotBuilt,
		Reason: "niwa writes neither a Codex approval policy nor a sandbox mode; the route is measured and unbuilt.",
	},

	// Row 13: hooks.
	{Capability: Hooks, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: Hooks, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonAgentCannotReceive,
		Reason: "No demonstrated route installs a niwa-owned hook for Codex without a blocking review prompt.",
	},

	// Row 14: work-summary hooks.
	{Capability: WorkSummaryHooks, Agent: agent.AgentClaude, State: StateImplemented, Requires: []Capability{Hooks}},
	{
		Capability: WorkSummaryHooks, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonAgentCannotReceive,
		Reason: "Work summaries are delivered as hooks, which Codex cannot receive.",
	},

	// Row 15: the PR-body hook.
	{Capability: PRBodyHook, Agent: agent.AgentClaude, State: StateImplemented, Requires: []Capability{Hooks}},
	{
		Capability: PRBodyHook, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonAgentCannotReceive,
		Reason: "The pull-request body capture is delivered as a hook, which Codex cannot receive.",
	},

	// Row 16: worktree-hook delegation and the deny fallback.
	{Capability: WorktreeHookDelegation, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: WorktreeHookDelegation, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNoSuchConcept,
		Reason: "Delegation is Claude Code harness surface; Codex has neither the events nor the tools it is built from.",
	},

	// Row 17: ephemeral-session provisioning.
	{Capability: EphemeralSessions, Agent: agent.AgentClaude, State: StateImplemented, Requires: []Capability{Hooks}},
	{
		Capability: EphemeralSessions, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonAgentCannotReceive,
		Reason: "Provisioning rides a session-start hook and the harness job-state file; Codex has neither.",
	},

	// Row 18: root-installed project skills.
	{Capability: RootProjectSkills, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: RootProjectSkills, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonAgentCannotReceive,
		Reason: "Root-installed skills serve root-started sessions, where Codex reads no configuration at all.",
	},

	// Row 19: niwa's own plugin.
	{Capability: NiwaPlugin, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: NiwaPlugin, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNotBuilt,
		Reason: "Codex accepts the identical plugin manifest; the wiring is unbuilt.",
	},

	// Row 20: remote-control-at-startup.
	{Capability: RemoteControl, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: RemoteControl, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNoSuchConcept,
		Reason: "The capability names claude.ai's remote-control bridge, which has no Codex equivalent.",
	},

	// Row 21: dispatch keep-alive.
	{Capability: DispatchKeepAlive, Agent: agent.AgentClaude, State: StateImplemented, Requires: []Capability{DispatchLaunch}},
	{
		Capability: DispatchKeepAlive, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNoSuchConcept,
		Reason: "There is no Codex background-session bridge to keep warm.",
	},

	// Row 22: launching a background worker.
	{Capability: DispatchLaunch, Agent: agent.AgentClaude, State: StateImplemented},
	{
		Capability: DispatchLaunch, Agent: agent.AgentCodex,
		State:  StateUnavailable,
		Kind:   ReasonNotBuilt,
		Reason: "The launch path refuses a non-Claude agent, although the per-agent model table already carries Codex entries.",
	},

	// Row 23: per-directory trust bootstrap.
	{
		Capability: DirectoryTrust, Agent: agent.AgentClaude,
		State:  StateUnavailable,
		Kind:   ReasonNoSuchConcept,
		Reason: "Claude Code keeps no per-directory trust record; its posture is settings-driven.",
	},
	// Codex reads trust from the layers it merges before any project layer
	// exists, so the one entry that makes a prepared instance usable is
	// written into the developer's own configuration rather than into the
	// instance. That is why this row is delivered by a procedure and not by a
	// plan the executor writes.
	{Capability: DirectoryTrust, Agent: agent.AgentCodex, State: StateImplemented},

	// Row 24: git-exclude bookkeeping.
	{Capability: GitExcludeBookkeeping, Agent: agent.AgentClaude, State: StateImplemented},
	// This row flips with the first Codex-side name niwa writes into a working
	// tree, not after it. The coverage is not cosmetic: an uncovered
	// niwa-written file makes the tree read dirty, and a dirty tree is what
	// stops the worktree teardown from reclaiming a session's worktree. Writing
	// the name in one change and covering it in the next would ship that
	// breakage as an intermediate state.
	{Capability: GitExcludeBookkeeping, Agent: agent.AgentCodex, State: StateImplemented},
}

// Lookup returns the declaration for one (capability, agent) pair.
//
// It is fail-closed, in the posture of vault.Registry.Build: a capability
// outside the closed set, an agent outside the accepted set, and a pair the
// table happens to omit are each an error naming what was asked for. There is
// no default declaration, because the one thing a capability contract must
// never do is answer for a pair nobody has thought about.
//
// The empty agent is not an unknown one: internal/agent documents Agent("") as
// Claude, so it resolves through agent.ParseAgent and is answered as Claude.
func Lookup(c Capability, ag agent.Agent) (Declaration, error) {
	if _, ok := c.row(); !ok {
		return Declaration{}, fmt.Errorf("agentplan: unknown capability %s", c)
	}
	resolved, err := agent.ParseAgent(string(ag))
	if err != nil {
		return Declaration{}, fmt.Errorf("agentplan: %w", err)
	}
	for _, d := range declarations {
		if d.Capability == c && d.Agent == resolved {
			return d, nil
		}
	}
	return Declaration{}, fmt.Errorf("agentplan: no declaration for capability %s and agent %q", c, resolved)
}
