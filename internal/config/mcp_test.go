package config

import (
	"fmt"
	"testing"
)

// TestMCPDeclarationParses pins the authored shape of the agent-neutral
// declaration: every key it documents decodes into a field, and none of them
// surfaces as an unknown one -- which is what a user would see if a key were
// named differently here than in the guide.
func TestMCPDeclarationParses(t *testing.T) {
	input := `
[workspace]
name = "test"

[[sources]]
org = "myorg"

[mcp.servers.files]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem"]
env = { ROOT = "/srv" }

[mcp.servers.search]
transport = "sse"
url = "https://mcp.example.test/v1"
headers = { Authorization = "Bearer token" }
agents = ["claude"]
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("parsing the declaration reported %v; every documented key must decode", result.Warnings)
	}

	names := result.Config.MCP.MCPServerNames()
	if len(names) != 2 || names[0] != "files" || names[1] != "search" {
		t.Fatalf("server names = %v, want [files search] in a stable order", names)
	}

	files := result.Config.MCP.Servers["files"]
	if files.Command != "npx" {
		t.Errorf("files.command = %q", files.Command)
	}
	if len(files.Args) != 2 || files.Args[0] != "-y" {
		t.Errorf("files.args = %v", files.Args)
	}
	if got := files.Env["ROOT"].Plain; got != "/srv" {
		t.Errorf("files.env.ROOT = %q, want /srv", got)
	}

	search := result.Config.MCP.Servers["search"]
	if search.Transport != "sse" || search.URL != "https://mcp.example.test/v1" {
		t.Errorf("search = %+v", search)
	}
	if got := search.Headers["Authorization"].Plain; got != "Bearer token" {
		t.Errorf("search.headers.Authorization = %q", got)
	}
	if len(search.Agents) != 1 || search.Agents[0] != "claude" {
		t.Errorf("search.agents = %v, want [claude]", search.Agents)
	}
}

// TestMCPVaultRefsAreCheckedAgainstDeclaredProviders puts the declaration's two
// secret-bearing slots under the same same-file rule every other value slot is
// under: a reference to a provider this file does not declare fails at parse
// rather than at resolve.
func TestMCPVaultRefsAreCheckedAgainstDeclaredProviders(t *testing.T) {
	const template = `
[workspace]
name = "test"

[[sources]]
org = "myorg"

[mcp.servers.search]
url = "https://mcp.example.test/v1"
headers = { Authorization = "%s" }
`
	if _, err := Parse([]byte(fmt.Sprintf(template, "vault://nowhere/key"))); err == nil {
		t.Error("a reference to an undeclared provider parsed without an error")
	}
	if _, err := Parse([]byte(fmt.Sprintf(template, "Bearer literal"))); err != nil {
		t.Errorf("a literal header value failed to parse: %v", err)
	}
}
