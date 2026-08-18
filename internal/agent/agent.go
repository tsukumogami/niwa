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

// ParseAgent validates s against the accepted set and returns the matching
// Agent. An empty string resolves to AgentClaude (the default). Any value
// outside {"claude", "codex"} returns an error naming the accepted set.
func ParseAgent(s string) (Agent, error) {
	switch Agent(s) {
	case "", AgentClaude:
		return AgentClaude, nil
	case AgentCodex:
		return AgentCodex, nil
	default:
		return "", fmt.Errorf("unknown agent %q; accepted values are: claude, codex", s)
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

// ResolveAgent computes the session agent from its three sources, once, in
// precedence order: flag > env > workspaceDefault > claude. Each argument is a
// raw string (empty means "not set" for that source); the chosen value is
// validated via ParseAgent, so an invalid value from any source returns an
// error naming the accepted set.
func ResolveAgent(flag, env, workspaceDefault string) (Agent, error) {
	switch {
	case flag != "":
		return ParseAgent(flag)
	case env != "":
		return ParseAgent(env)
	case workspaceDefault != "":
		return ParseAgent(workspaceDefault)
	default:
		return AgentClaude, nil
	}
}
