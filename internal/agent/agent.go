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

import "fmt"

// Agent identifies the coding agent a workspace is prepared for.
type Agent string

const (
	// AgentClaude is Claude Code. It is the default agent and the zero value's
	// meaning.
	AgentClaude Agent = "claude"
	// AgentCodex is OpenAI Codex.
	AgentCodex Agent = "codex"
)

// known lists the accepted agent values for error messages. It is kept in sync
// with the constants above.
var known = []Agent{AgentClaude, AgentCodex}

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

// LocalContextFileName is the filename for the repository and worktree
// levels: CLAUDE.local.md for Claude (and the zero value). The repository and
// worktree levels are always materialized on the Claude pass regardless of
// the workspace's selected agent (Decision 7A); the Codex-facing equivalent
// at these levels is the composed AGENTS.override.md, written by the
// dedicated Codex materializer rather than through this accessor.
func (a Agent) LocalContextFileName() string {
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
