// Package agentplan holds the contract between niwa and the coding agents it
// prepares a workspace for: the closed set of capabilities a preparation can
// deliver, one declaration per (capability, agent) pair saying whether that
// agent gets it, and the plan vocabulary a producer uses to declare the writes
// that deliver it.
//
// internal/workspace is the caller. Its pipeline fills narrow input structs
// from the effective configuration, obtains a Plan for each agent, and executes
// the entries; the managed-file bookkeeping reads Managed and Sources off those
// entries and the per-repo git-exclude pass reads ExcludeAs, while the launch
// and procedure paths consult the declaration table instead of testing an agent
// constant. Nothing in this package writes to disk or launches a process: it
// reads inputs and declares outputs, and internal/workspace does the doing.
//
// The package is a leaf. It imports internal/agent for the agent discriminator
// and the standard library; internal/config, which carries the configuration
// types producer inputs are built from, is the ceiling. It imports nothing
// above that and, in particular, nothing from internal/workspace. That
// direction is what the move of SourceEntry and ComputeSourceFingerprint out of
// internal/workspace/state.go buys: plan entries carry the provenance the
// managed-file record needs, and had those types stayed up in
// internal/workspace the two packages would have had to import each other.
// internal/workspace keeps a type alias, so the state schema and its JSON tags
// are unchanged.
//
// Two things the preparation path does are deliberately not capabilities.
// Vault-backed secret resolution is an upstream source feeding the environment
// and dotenv rows, not something a session receives, so a per-agent declaration
// of it would describe where a value came from rather than whether an agent got
// it. The per-agent enabled gates are gates over deliveries rather than
// deliveries: [claude] enabled zeroes Claude's own plan-borne entries for a
// scope and [codex] enabled zeroes Codex's, which makes each one an input to
// its own agent's producer (Producer.Gated), not a row in the table. Recording
// both here is what keeps the closure from looking arbitrary.
package agentplan

import "fmt"

// Capability names one thing a workspace preparation can deliver to an agent
// session. The set is closed: the constants below are the 24 rows of the
// capability matrix in docs/prds/PRD-agent-capability-contract.md, in matrix
// order, and adding a member is a product decision rather than an
// implementation detail.
//
// The zero value is not a capability. Every lookup that takes one rejects it,
// so a forgotten initialization cannot read as row 1.
type Capability uint8

const (
	// WorkspaceOrientation is workspace- and group-level orientation reaching
	// a session opened inside a repository (matrix row 1).
	WorkspaceOrientation Capability = iota + 1

	// RootSessionOrientation is a session started at the workspace or
	// instance root being oriented (row 2).
	RootSessionOrientation

	// RepoOrientationDoc is the repository-level orientation document (row 3).
	RepoOrientationDoc

	// WorktreeOrientationDoc is the worktree-level orientation document
	// (row 4).
	WorktreeOrientationDoc

	// PluginSkills is workspace-declared plugin skills being usable in the
	// session (row 5).
	PluginSkills

	// MarketplaceRegistration is registering a marketplace or plugin with the
	// agent's own plugin system (row 6).
	MarketplaceRegistration

	// SubagentTypes is named subagent types the session can dispatch to
	// (row 7).
	SubagentTypes

	// MCPServers is the MCP servers available to the session (row 8).
	MCPServers

	// SessionEnvironment is environment variables present in the session
	// (row 9).
	SessionEnvironment

	// DotenvFiles is dotenv files written to declared paths (row 10).
	DotenvFiles

	// FileDistribution is arbitrary source-to-destination file distribution
	// (row 11).
	FileDistribution

	// ApprovalPosture is the session's approval and sandbox posture (row 12).
	ApprovalPosture

	// Hooks is lifecycle commands the agent runs on its own events (row 13).
	Hooks

	// WorkSummaryHooks is work-summary capture, delivered as hooks (row 14).
	WorkSummaryHooks

	// PRBodyHook is pull-request body capture, delivered as a hook (row 15).
	PRBodyHook

	// WorktreeHookDelegation is worktree-hook delegation and its deny
	// fallback (row 16).
	WorktreeHookDelegation

	// EphemeralSessions is ephemeral-session provisioning (row 17).
	EphemeralSessions

	// RootProjectSkills is skills installed at the instance root, serving
	// root-started sessions (row 18).
	RootProjectSkills

	// NiwaPlugin is niwa's own plugin, carrying the migrate-config skill
	// (row 19).
	NiwaPlugin

	// RemoteControl is remote-control-at-startup, claude.ai's bridge into a
	// local session (row 20).
	RemoteControl

	// DispatchKeepAlive is keeping a dispatched background session warm
	// (row 21).
	DispatchKeepAlive

	// DispatchLaunch is launching a background worker via niwa dispatch
	// (row 22).
	DispatchLaunch

	// DirectoryTrust is the per-directory trust bootstrap that lets an agent
	// read a project-layer configuration niwa wrote (row 23).
	DirectoryTrust

	// GitExcludeBookkeeping is git-exclude coverage for the files niwa writes
	// into a repository (row 24).
	GitExcludeBookkeeping
)

// Route names how a capability's delivery reaches a session. It is a property
// of the capability rather than of one agent's declaration of it: the same
// capability arrives by the same mechanism for whoever implements it, which is
// what lets a binding check ask one route-appropriate question of every
// implemented declaration.
type Route uint8

const (
	// RoutePlan is delivered as plan entries the executor writes.
	RoutePlan Route = iota + 1

	// RouteProcedure is delivered by a named procedure registered in
	// internal/workspace against the (capability, agent) pair -- a side effect
	// outside the instance, which a plan entry cannot express honestly.
	RouteProcedure

	// RouteLaunch is consulted by the session-launch path rather than written
	// at all: the declaration is the gate.
	RouteLaunch
)

// capabilityRow is one row of the catalog: a capability's value, the stable
// name it is reported under, and the route its delivery takes.
type capabilityRow struct {
	capability Capability
	name       string
	route      Route
}

// catalog is the single source of truth for the closed set. All, String, and
// Route all derive from it, so a capability constant never listed here has no
// name, no route, and cannot be declared -- there is no second list for it to
// fall out of sync with.
var catalog = []capabilityRow{
	{WorkspaceOrientation, "workspace-orientation", RoutePlan},
	{RootSessionOrientation, "root-session-orientation", RoutePlan},
	{RepoOrientationDoc, "repo-orientation-doc", RoutePlan},
	{WorktreeOrientationDoc, "worktree-orientation-doc", RoutePlan},
	{PluginSkills, "plugin-skills", RoutePlan},
	{MarketplaceRegistration, "marketplace-registration", RouteProcedure},
	{SubagentTypes, "subagent-types", RoutePlan},
	{MCPServers, "mcp-servers", RoutePlan},
	{SessionEnvironment, "session-environment", RoutePlan},
	{DotenvFiles, "dotenv-files", RoutePlan},
	{FileDistribution, "file-distribution", RoutePlan},
	{ApprovalPosture, "approval-posture", RoutePlan},
	{Hooks, "hooks", RoutePlan},
	{WorkSummaryHooks, "work-summary-hooks", RoutePlan},
	{PRBodyHook, "pr-body-hook", RoutePlan},
	{WorktreeHookDelegation, "worktree-hook-delegation", RoutePlan},
	{EphemeralSessions, "ephemeral-sessions", RoutePlan},
	{RootProjectSkills, "root-project-skills", RoutePlan},
	{NiwaPlugin, "niwa-plugin", RouteProcedure},
	{RemoteControl, "remote-control", RoutePlan},
	{DispatchKeepAlive, "dispatch-keep-alive", RouteLaunch},
	{DispatchLaunch, "dispatch-launch", RouteLaunch},
	{DirectoryTrust, "directory-trust", RouteProcedure},
	{GitExcludeBookkeeping, "git-exclude-bookkeeping", RouteProcedure},
}

// All returns every capability in matrix order. The result is a fresh slice, so
// a caller iterating the closed set cannot narrow it for everyone else.
func All() []Capability {
	out := make([]Capability, len(catalog))
	for i, row := range catalog {
		out[i] = row.capability
	}
	return out
}

// String returns the capability's stable name, or a numeric form for a value
// outside the closed set so an error about an unknown capability can still
// print what it was handed.
func (c Capability) String() string {
	if row, ok := c.row(); ok {
		return row.name
	}
	return fmt.Sprintf("capability(%d)", uint8(c))
}

// Route returns the delivery route for this capability, or the zero Route for a
// value outside the closed set. The zero value is not a valid route, so an
// unknown capability cannot pass for a plan-borne one.
func (c Capability) Route() Route {
	if row, ok := c.row(); ok {
		return row.route
	}
	return 0
}

// row finds the catalog entry for c. The scan is linear over 24 entries, which
// costs less than the map plus init needed to avoid it.
func (c Capability) row() (capabilityRow, bool) {
	for _, row := range catalog {
		if row.capability == c {
			return row, true
		}
	}
	return capabilityRow{}, false
}
