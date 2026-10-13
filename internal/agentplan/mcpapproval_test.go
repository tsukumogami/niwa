package agentplan

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// The approval list is only useful if it names exactly what the generated
// .mcp.json carries: a name the document lacks approves nothing, and a name
// the list misses brings back the prompt the list exists to remove. So these
// tests compare MCPApprovals against the document PayloadPlan renders for the
// same inputs rather than against a hand-written expectation alone.

// approvalServers is a declaration that exercises the per-agent filter: one
// server scoped away from Claude, and names that arrive out of order.
func approvalServers() []MCPServer {
	return []MCPServer{
		{Name: "zeta", Command: "zeta-mcp"},
		{Name: "example", URL: "https://mcp.example.test/v1"},
		{Name: "codex-only", Command: "codex-mcp", Agents: []string{"codex"}},
		{Name: "alpha", Command: "alpha-mcp", Agents: []string{"claude"}},
	}
}

// generatedClaudeNames decodes the server names out of the .mcp.json the
// Claude producer renders for servers at the instance root.
func generatedClaudeNames(t *testing.T, servers []MCPServer) []string {
	t.Helper()
	plan, err := For(agent.AgentClaude).PayloadPlan(PayloadInputs{
		Scope:   PayloadAtInstanceRoot,
		Dir:     "/instance",
		Servers: servers,
	})
	if err != nil {
		t.Fatalf("PayloadPlan(claude): %v", err)
	}
	if len(plan.Entries) == 0 {
		return nil
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(plan.Entries[0].Content, &doc); err != nil {
		t.Fatalf("decoding the generated .mcp.json: %v", err)
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestMCPApprovalsNameExactlyTheGeneratedServers(t *testing.T) {
	servers := approvalServers()
	got, err := For(agent.AgentClaude).MCPApprovals(PayloadAtInstanceRoot, servers)
	if err != nil {
		t.Fatalf("MCPApprovals: %v", err)
	}
	want := []string{"alpha", "example", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MCPApprovals = %v, want %v", got, want)
	}
	if generated := generatedClaudeNames(t, servers); !reflect.DeepEqual(got, generated) {
		t.Errorf("MCPApprovals = %v, but the generated .mcp.json carries %v", got, generated)
	}
	if !slices.IsSorted(got) {
		t.Errorf("MCPApprovals = %v, want sorted", got)
	}
}

func TestMCPApprovalsAreEmptyWhereNothingIsGenerated(t *testing.T) {
	cases := []struct {
		name     string
		producer Producer
		scope    PayloadScope
		servers  []MCPServer
	}{
		{"no servers declared", For(agent.AgentClaude), PayloadAtInstanceRoot, nil},
		{"every server scoped to another agent", For(agent.AgentClaude), PayloadAtInstanceRoot, []MCPServer{{Name: "x", Command: "x", Agents: []string{"codex"}}}},
		{"a scope Claude takes no document at", For(agent.AgentClaude), PayloadInRepo, approvalServers()},
		{"Claude gated off", For(agent.AgentClaude).Gated(false), PayloadAtInstanceRoot, approvalServers()},
		{"Codex at the instance root", For(agent.AgentCodex), PayloadAtInstanceRoot, approvalServers()},
		{"Codex in a repository", For(agent.AgentCodex), PayloadInRepo, approvalServers()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.producer.MCPApprovals(tc.scope, tc.servers)
			if err != nil {
				t.Fatalf("MCPApprovals: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("MCPApprovals = %v, want none", got)
			}
		})
	}
}

// TestOneLayoutApprovesInSettings guards the assumption the workspace side
// makes when it collects approvals from every agent into one settings
// document: that document belongs to a single agent, so a second layout
// claiming it would put one agent's server names into another's approvals.
func TestOneLayoutApprovesInSettings(t *testing.T) {
	var approving []agent.Agent
	for ag, layout := range payloadLayouts {
		if layout.approvedInSettings {
			approving = append(approving, ag)
		}
	}
	if len(approving) != 1 || approving[0] != agent.AgentClaude {
		t.Errorf("layouts approving in the settings document = %v, want only %s", approving, agent.AgentClaude)
	}
}
