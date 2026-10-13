package config

import (
	"fmt"
	"reflect"
	"testing"
)

// TestMCPConfigClone checks a clone is equal to its source and shares none of
// its maps or slices, and that nil stays nil.
func TestMCPConfigClone(t *testing.T) {
	if got := (MCPConfig{}).Clone(); got.Servers != nil {
		t.Errorf("clone of an empty config = %+v, want nil servers", got)
	}
	src := MCPConfig{Servers: map[string]MCPServerConfig{
		"files": {Command: "srv", Args: []string{"a"}, Agents: []string{"claude"}, Env: map[string]MaybeSecret{"K": {Plain: "v"}}},
		"docs":  {URL: "https://docs.example.com", Headers: map[string]MaybeSecret{"Authorization": {Plain: "Bearer x"}}},
	}}
	got := src.Clone()
	if !reflect.DeepEqual(got, src) {
		t.Fatalf("clone = %+v, want %+v", got, src)
	}
	if got.Servers["docs"].Env != nil || got.Servers["files"].Headers != nil {
		t.Error("a nil map became non-nil in the clone")
	}

	got.Servers["new"] = MCPServerConfig{}
	files := got.Servers["files"]
	files.Args[0] = "changed"
	files.Agents[0] = "changed"
	files.Env["K"] = MaybeSecret{Plain: "changed"}
	got.Servers["docs"].Headers["Authorization"] = MaybeSecret{Plain: "changed"}

	if _, ok := src.Servers["new"]; ok {
		t.Error("adding a server to the clone reached the source")
	}
	s := src.Servers["files"]
	if s.Args[0] != "a" || s.Agents[0] != "claude" || s.Env["K"].Plain != "v" {
		t.Errorf("source server changed through the clone: %+v", s)
	}
	if src.Servers["docs"].Headers["Authorization"].Plain != "Bearer x" {
		t.Error("source headers changed through the clone")
	}
}

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
