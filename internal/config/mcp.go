package config

import "sort"

// The agent-neutral MCP declaration.
//
// [mcp.servers.<name>] describes one MCP server once, in terms no agent owns,
// and niwa generates each agent's native format from it. It is deliberately not
// spelled in any agent's own schema: the file-distribution route that carries a
// hand-written .mcp.json still works and is byte-opaque to niwa, but a file in
// one agent's format cannot be translated into another's without losing what
// only one of them can express -- which is why the declaration is its own shape
// rather than a parse of somebody's config file.
//
// Values in env and headers are MaybeSecret, so a vault:// reference resolves
// through the same pipeline every other secret slot uses. What lands in a
// generated file is always the resolved literal: nothing niwa writes relies on
// an agent expanding anything at load time.

// MCPConfig is the workspace-level [mcp] table.
//
// It is workspace-scoped, like [claude.marketplaces]: an MCP server is a
// property of the workspace rather than of one repository inside it, and no
// per-repo override position reads it.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `toml:"servers,omitempty"`
}

// MCPServerConfig is one declared server.
//
// Transport is optional and inferred from which of command and url is present;
// declaring it is how an author asks for the one transport inference cannot
// reach (sse, which is url-shaped like http). Exactly one of command and url
// belongs to a well-formed entry, and the fields of the other transport are
// rejected rather than ignored -- a Codex config load fails whole on a
// cross-transport field, so accepting one here would trade a legible error for
// an unusable session.
type MCPServerConfig struct {
	// Transport is "stdio", "http", or "sse". Empty means inferred.
	Transport string `toml:"transport,omitempty"`

	// Command is the stdio server's executable.
	Command string `toml:"command,omitempty"`

	// Args are the stdio server's arguments.
	Args []string `toml:"args,omitempty"`

	// Env is the stdio server's environment, resolved before writing.
	Env map[string]MaybeSecret `toml:"env,omitempty"`

	// URL is the http or sse server's endpoint.
	URL string `toml:"url,omitempty"`

	// Headers are the http or sse server's request headers, resolved before
	// writing.
	Headers map[string]MaybeSecret `toml:"headers,omitempty"`

	// Agents restricts which agents this server is generated for, by the same
	// names the workspace's default_agent takes. Empty means every agent.
	//
	// It is the deliberate, visible form of "this server is for one agent":
	// a construct one agent cannot express is a named error rather than a
	// silent omission, and this is the escape hatch that error points at.
	Agents []string `toml:"agents,omitempty"`
}

// MCPServerNames returns the declared server names in a stable order, so
// everything generated from the declaration -- files, errors, reports -- is the
// same on every apply.
func (m MCPConfig) MCPServerNames() []string {
	if len(m.Servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.Servers))
	for name := range m.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
