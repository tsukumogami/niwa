// Package agent defines the AI coding agent niwa prepares a workspace for.
//
// The Agent discriminator is a session-global choice (one agent for a whole
// workspace preparation), resolved once per session from a workspace-config
// default plus a per-session flag/environment override. It is deliberately a
// leaf package -- it imports nothing else in the module -- so both
// internal/config (which carries the raw default as a string) and the
// higher-level internal/workspace and internal/cli packages can depend on it
// without an import cycle.
//
// The zero value Agent("") behaves as the Claude agent. This is a fail-safe
// contract: a construction site that has not yet been wired to set the agent
// degrades to today's Claude behavior rather than to an empty, broken filename.
package agent

import (
	"fmt"
	"slices"
	"strings"
)

// Agent identifies the coding agent a workspace is prepared for.
type Agent string

const (
	// AgentClaude is Claude Code. It is the default agent and the zero value's
	// meaning.
	AgentClaude Agent = "claude"
	// AgentCodex is OpenAI Codex.
	AgentCodex Agent = "codex"
)

// known lists the accepted agent values. It is kept in sync with the constants
// above and reached from outside the package through All.
var known = []Agent{AgentClaude, AgentCodex}

// All returns every accepted agent, in declaration order. Callers that must
// cover the whole set -- the capability declaration table, and any test that
// asserts something for each agent -- range over this instead of hand-listing
// the constants, so adding a third agent shows up as a failure rather than as
// silence. The result is a fresh slice: the closed set is not narrowable by a
// caller that iterates it.
func All() []Agent { return slices.Clone(known) }

// acceptedValues renders the accepted set for the parse error, comma-joined in
// declaration order. It reads the same list All() serves rather than spelling
// the names out again, so adding a third agent updates the message a developer
// reads instead of leaving the last hardcoded copy of the set quietly wrong.
func acceptedValues() string {
	names := make([]string, 0, len(known))
	for _, a := range known {
		names = append(names, string(a))
	}
	return strings.Join(names, ", ")
}

// ParseAgent validates s against the accepted set and returns the matching
// Agent. An empty string resolves to AgentClaude (the default). Any value
// outside the accepted set returns an error naming that set.
func ParseAgent(s string) (Agent, error) {
	switch Agent(s) {
	case "", AgentClaude:
		return AgentClaude, nil
	case AgentCodex:
		return AgentCodex, nil
	default:
		return "", fmt.Errorf("unknown agent %q; accepted values are: %s", s, acceptedValues())
	}
}

// normalize maps the zero value to AgentClaude so the accessors below can treat
// an unset Agent as Claude (the fail-safe contract) without repeating the check.
func (a Agent) normalize() Agent {
	if a == "" {
		return AgentClaude
	}
	return a
}

// RootContextFileName is the filename niwa writes context to at the niwa-owned,
// non-repository levels (the workspace root and each group directory):
// CLAUDE.md for Claude (and the zero value), AGENTS.md for Codex.
func (a Agent) RootContextFileName() string {
	if a.normalize() == AgentCodex {
		return "AGENTS.md"
	}
	return "CLAUDE.md"
}

// LocalContextFileName is the filename for the repository and worktree levels:
// CLAUDE.local.md for Claude (and the zero value), AGENTS.override.md for Codex.
//
// The Codex value is not a fallback keyed on what a repository happens to ship.
// Codex takes at most one context file per directory by a hardcoded precedence
// -- AGENTS.override.md, then AGENTS.md, then configured fallbacks -- with no
// error and no warning when an earlier name wins. AGENTS.override.md is
// therefore the only name that is read in every repository; writing AGENTS.md
// would deliver nothing in any repository that commits its own. The repository's
// committed file is not displaced in substance: niwa inlines it into the
// document it writes.
func (a Agent) LocalContextFileName() string {
	if a.normalize() == AgentCodex {
		return "AGENTS.override.md"
	}
	return "CLAUDE.local.md"
}

// ResolveAgent computes the session agent from its four sources, once, in
// precedence order: flag > env > workspaceDefault > hostDefault > claude. Each
// argument is a raw string (empty means "not set" for that source); the chosen
// value is validated via ParseAgent, so an invalid value from any source
// returns an error naming the accepted set.
//
// hostDefault is the broadest rung: the developer's own machine-wide default,
// read from their personal niwa config rather than from anything a workspace
// ships. It sits BELOW workspaceDefault deliberately. A workspace that states
// an agent is stating it for everyone who works in it, and the same ordering
// already governs niwa's other host-level dispatch defaults, where a downstream
// setting outranks the personal one. A developer who wants the other agent for
// one shell or one command reaches for env or flag, which both outrank the
// workspace.
func ResolveAgent(flag, env, workspaceDefault, hostDefault string) (Agent, error) {
	switch {
	case flag != "":
		return ParseAgent(flag)
	case env != "":
		return ParseAgent(env)
	case workspaceDefault != "":
		return ParseAgent(workspaceDefault)
	case hostDefault != "":
		return ParseAgent(hostDefault)
	default:
		return AgentClaude, nil
	}
}
